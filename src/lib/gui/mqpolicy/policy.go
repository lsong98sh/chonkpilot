// Package mqpolicy 是主仓侧的 **MQ 分发出口策略**（清单 49 §2.22.7）。
//
// 分层：framework `runtime/dispatch` 只做机制（单订阅 ">" → 组装 Event → 问 Policy → 逐目标
// 投递 + sink 注册表）；**workdir / instance 路由属于业务**，规则全部落在本包的
// `WorkDirPolicy`，framework 不认识 `data-*` / `instance_id` / `work_dir` / 前端 type。
//
// 迁移两步（不得合并）：
//
//	S1（2026-10-29）= 把 `gui/bridge` 原先「**每窗口各自订阅 ">" + 各自过滤**」的四条判定
//	**逐字搬进** `WorkDirPolicy.Deliver`（顺序与短路语义不变，行为零变化）：
//	  ① 自发布丢弃：发布方自己的那条事件不回投自己的前端（D-11：topic + 载荷指纹，1s 窗口，
//	     「同主题不同载荷」不误吞）；
//	  ② `data-*` 的 persist 同主题应答丢弃（`{req_id, ok, result|error}` 信封）；
//	  ③ 载荷 `instance_id` 非空 → 只投该实例的**窗口** sink（该 sink 未标识则放行；无匹配 = 空）；
//	  ④ 其余（无 `instance_id`）→ 全投。
//
//	S2（2026-10-29）= 在 ①–④ 之上加一级「workdir 级主题只投同 workdir」：
//	  · 主题级别用**显式清单**（topics.go：`workDirTopics` / `serverTopics`，不靠前缀猜）；
//	  · 事件 workdir 取值顺序：载荷 `work_dir` → 否则按 `instance_id` 反查（宿主注入的
//	    framework `instance.Manager` 视图；**不自建第二份登记表**）；
//	  · 只有 workdir 级主题、且**事件 workdir 已知**时才收紧；workdir 未知 → **保守全投**
//	    并记 Debug 日志（宁可少收紧，不可漏投）；
//	  · sink 侧 workdir 未知（空串）同样放行（该 sink 未必属于任何项目目录）。
//
// 收紧**只会缩小**投递集合：instance 级（有 `instance_id`）仍沿用 S1 的精确匹配，workdir
// 判定不会把原本能收到的窗口放大回去。
//
//	S3（2026-10-29）= **插件 sink 接入同一分发器与同一份策略**（pluginhost 桥不再自订阅 ">"）：
//	  · instance 级（有 `instance_id`）：窗口按 `ID`（其 Instance 为空）= 实例 id；插件按
//	    `Sink.Instance`（桥在 `plugin.hello` 后填写）。二者**未声明**（空）→ 放行（与 S1 的
//	    「未标识放行」同口径）；
//	  · workdir 级：窗口与插件**同规则**（`pathx.Same` 比 sink 的 WorkDir；空 = 未知 → 放行）；
//	  · server 级：全投（窗口 + 插件）；
//	  · `data-*` 应答丢弃、自发布吞掉两条对两类 sink 一致。
//	  任何未知情形一律放行 —— 只缩小，不放大。
package mqpolicy

import (
	"encoding/json"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/dispatch"
	"github.com/lsong98sh/chonkpilot-util/pathx"
)

// selfPublishWindow 是「自发布」判定的时间窗口（D-11，与改造前 bridge.isSelfPublished 一致）：
// 1s 内同 (subject, 载荷指纹) 视为本发布方刚发的回环。
const selfPublishWindow = time.Second

// pubStamp 是一次发布登记的指纹 + 时间戳。
type pubStamp struct {
	fp uint64
	at time.Time
}

// ledgerKey 标识「某发布方在某主题上的最近一次发布」（发布方 = 窗口实例 id）。
type ledgerKey struct {
	owner   string
	subject string
}

// WorkDirPolicy 实现 dispatch.Policy：S1 与改造前 `gui/bridge` 的本地过滤逐字等价；S2 在其上
// 追加「workdir 级主题只投同 workdir」。
type WorkDirPolicy struct {
	mu        sync.Mutex
	published map[ledgerKey]pubStamp

	// instanceWorkDir 是 `instance_id → work_dir` 的**只读视图**（S2）：由宿主注入 framework
	// `instance.Manager`（唯一活动实例视图，见 `gui/host`），本包**不自建第二份表**。
	// nil / 未命中 → workdir 未知（保守全投）。
	rMu             sync.RWMutex
	instanceWorkDir func(instanceID string) (string, bool)
}

// New 构造策略。
func New() *WorkDirPolicy {
	return &WorkDirPolicy{published: make(map[ledgerKey]pubStamp)}
}

// SetInstanceWorkDir 注入 `instance_id → work_dir` 反查（S2；宿主装配期调用一次）。
// fn 为 nil = 不反查（workdir 未知）。实现必须并发安全（framework `instance.Manager` 满足）。
func (p *WorkDirPolicy) SetInstanceWorkDir(fn func(instanceID string) (string, bool)) {
	if p == nil {
		return
	}
	p.rMu.Lock()
	p.instanceWorkDir = fn
	p.rMu.Unlock()
}

// workDirOfInstance 按 instance 反查 workdir（未注入 / 未命中 → ok=false）。
func (p *WorkDirPolicy) workDirOfInstance(instanceID string) (string, bool) {
	if instanceID == "" {
		return "", false
	}
	p.rMu.RLock()
	fn := p.instanceWorkDir
	p.rMu.RUnlock()
	if fn == nil {
		return "", false
	}
	wd, ok := fn(instanceID)
	if !ok || wd == "" {
		return "", false
	}
	return wd, true
}

// Mark 记录一次发布（**发布前**调用；owner = 该窗口实例 id）。与改造前
// `Bridge.markPublished` 同一口径：标记值 = subject + 载荷指纹。
func (p *WorkDirPolicy) Mark(owner, subject string, payload []byte) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published[ledgerKey{owner: owner, subject: subject}] = pubStamp{
		fp: pubFingerprint(subject, payload),
		at: time.Now(),
	}
}

// selfPublished 判定「该 sink 正是本条事件的发布方」（逐字沿用 bridge.isSelfPublished：
// 过期即清理、指纹逐字节相等才算自环）。
func (p *WorkDirPolicy) selfPublished(owner, subject string, payload []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := ledgerKey{owner: owner, subject: subject}
	st, ok := p.published[k]
	if !ok {
		return false
	}
	if time.Since(st.at) >= selfPublishWindow {
		delete(p.published, k) // 过期：清理，不再视为自环
		return false
	}
	return st.fp == pubFingerprint(subject, payload)
}

// Deliver 实现 dispatch.Policy（规则见包注释 ①–④ + S2 workdir 收紧）。
//
// 说明：自发布（①）按 **sink 级**判定而非整条事件 —— 与改造前「每窗口各有发布账本、
// 只吞自己发的那条」逐字等价（同一主题两个窗口各发一次时会分别吞掉各自的那条）；
// framework 的 `Event.Src` 是单值，无法表达多发布方，故本策略不看 Src。
func (p *WorkDirPolicy) Deliver(evt dispatch.Event, sinks []dispatch.Sink) []dispatch.Sink {
	// ② persist 同主题应答（他源发出、带 req_id + ok + result|error）不回投任何前端。
	if isDataReply(evt.Topic, evt.Payload) {
		return nil
	}
	instID := eventInstanceID(evt.Payload)
	// S2：workdir 级主题 → 解析事件 workdir（载荷 `work_dir` 优先，否则按 instance 反查）。
	// 非 workdir 级主题 / workdir 未知 → 不收紧（保守全投，只记 Debug）。
	narrow, wd := false, ""
	if isWorkDirTopic(evt.Topic) {
		if v, ok := p.eventWorkDir(evt.Payload, instID); ok {
			narrow, wd = true, v
		} else {
			slog.Debug("mqpolicy: workdir 级主题的事件 workdir 未知 → 保守全投",
				"topic", evt.Topic, "instance_id", instID)
		}
	}
	out := make([]dispatch.Sink, 0, len(sinks))
	for _, s := range sinks {
		// ① 自发布：本 sink 就是发布方 → 吞掉这条回环。
		if p.selfPublished(s.ID, evt.Topic, evt.Payload) {
			continue
		}
		// ③ 载荷带 `instance_id` 且本 sink 已声明实例归属 → 必须精确相等（**未声明** sink
		// 一律放行，与改造前 `acceptEventInstance` 的 `b.instanceID == ""` 分支等价）；
		// ④ 无归属 → 全投。窗口（`ID` = 窗口实例 id）与插件（`Instance` = 会话实例 id）
		// 同规则，仅取值来源不同。
		if instID != "" {
			if own := sinkInstance(s); own != "" && own != instID {
				continue
			}
		}
		// S2/S3：workdir 级主题只投**同 workdir** 的 sink（窗口与插件同规则；事件 workdir 与
		// sink workdir 都已知时才判；sink workdir 空 = 未知 → 放行，不静默丢事件）。
		if narrow && s.WorkDir != "" && !pathx.Same(s.WorkDir, wd) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// sinkInstance 返回 sink 的实例归属（S3）：
//   - 插件 sink（`Kind=SinkPlugin`）→ `Sink.Instance`（桥在 `plugin.hello` 后填写）；
//   - 其余 sink（窗口）→ `Sink.ID`（= 该窗口的实例 id，与 S1 逐字一致）。
//
// 空 = **未声明**（未标识）→ 调用方一律放行（宁可少收紧，不可漏投）。
func sinkInstance(s dispatch.Sink) string {
	if s.Kind == dispatch.SinkPlugin {
		return s.Instance
	}
	return s.ID
}

// eventWorkDir 解析一条事件的 workdir（S2）：载荷 `work_dir` 优先；否则按 `instance_id` 反查
// （framework `instance.Manager` 视图）。都拿不到 → ok=false（= 未知，调用方保守全投）。
func (p *WorkDirPolicy) eventWorkDir(payload []byte, instID string) (string, bool) {
	if wd := eventWorkDirField(payload); wd != "" {
		return wd, true
	}
	return p.workDirOfInstance(instID)
}

// pubFingerprint 计算「主题 + 载荷」的指纹（与改造前 bridge.pubFingerprint 逐字一致，
// 亦与 llm/httpapi 的 eventFingerprint 同口径）。
func pubFingerprint(subject string, payload []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(subject))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload)
	return h.Sum64()
}

// isDataReply 判定「persist 同主题应答」信封（逐字沿用改造前 bridge.isDataReply）：
// `data-*` 主题 + 带 `req_id` 与非空 `ok`，且含应答专属信封键 `result` 或 `error` 的载荷
// （persist 应答恒含 req_id + ok + result|error；变更广播/业务事件不带 req_id）。
func isDataReply(subject string, payload []byte) bool {
	if !strings.HasPrefix(subject, "data-") {
		return false
	}
	var probe struct {
		ReqID  string          `json:"req_id"`
		OK     *bool           `json:"ok"`
		Result json.RawMessage `json:"result"`
		Err    json.RawMessage `json:"error"`
	}
	if json.Unmarshal(payload, &probe) != nil {
		return false
	}
	return probe.ReqID != "" && probe.OK != nil && (probe.Result != nil || probe.Err != nil)
}

// eventInstanceID 从事件载荷提取 `instance_id`（无字段 / 空串 / 非 JSON 对象 → ""，即无归属；
// 逐字沿用改造前 bridge.eventInstanceID）。
func eventInstanceID(payload []byte) string {
	var probe struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return ""
	}
	return probe.InstanceID
}

// eventWorkDirField 从事件载荷提取 `work_dir`（无字段 / 空串 / 非 JSON 对象 → ""）。
func eventWorkDirField(payload []byte) string {
	var probe struct {
		WorkDir string `json:"work_dir"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return ""
	}
	return probe.WorkDir
}

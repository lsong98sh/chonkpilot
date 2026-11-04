// MQ 分发业务策略（PM 的 `Route`，P5 步骤 3 / SANDBOX-WRITE-1 C15）。
//
// 分层：framework `runtime/dispatch` 只做机制（单订阅 ">" → 组装 Event → 问策略 → 逐目标投递）；
// **workdir / instance 路由属于业务**，规则集中在 `Router.Route`。本文件是唯一实现，
// `src/lib/gui/mqpolicy` 只保留薄封装（`WorkDirPolicy` 把 dispatch.Sink 转成本包的 `Sink`）。
//
// 规则（与迁移前逐字等价）：
//
//	① 自发布丢弃：发布方自己的那条事件不回投（topic + 载荷指纹，1s 窗口）；
//	② `data-*` 的 persist 同主题应答丢弃（`{req_id, ok, result|error}` 信封）；
//	③ 载荷 `instance_id` 非空 → 只投该实例的 sink（该 sink 未标识则放行；无匹配 = 空）；
//	④ workdir 级主题 → 只投同 workdir 的 sink（事件 workdir 未知 → 保守全投）；
//	⑤ 其余 → 全投。
//
// 收紧**只会缩小**投递集合：instance 级精确匹配不受 workdir 判定影响；任何未知一律放行。
package workspace

import (
	"encoding/json"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/lsong98sh/chonkpilot-util/pathx"
)

// selfPublishWindow 是「自发布」判定的时间窗口（1s 内同 (subject, 载荷指纹) 视为回环）。
const selfPublishWindow = time.Second

// Sink 是一个路由目标描述（**不含任何句柄 / 对象**，P5 §7.4：PM 可在无 GUI 形态运行）。
type Sink struct {
	// ID 是窗口 sink 的窗口实例 id（插件 sink 时无意义）。
	ID string
	// Instance 是插件 sink 声明的实例归属（窗口 sink 不使用）。
	Instance string
	// WorkDir 是 sink 所属项目目录（空 = 未知 → 不按 workdir 丢弃）。
	WorkDir string
	// Plugin 标记插件 sink（归属取值 = Instance；窗口 sink = ID）。
	Plugin bool
}

// Target 是 `Route` 保留的目标：Index = 入参 sinks 的下标（调用方据此还原自己的 sink 对象）。
type Target struct {
	Index    int
	ID       string
	Instance string
	WorkDir  string
	Plugin   bool
}

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

// Router 是 MQ 分发的业务策略实现（进程内单例；`workspace.Manager` 持有同一份）。
type Router struct {
	mu        sync.Mutex
	published map[ledgerKey]pubStamp

	// instanceWorkDir 是 `instance_id → work_dir` 的**只读视图**：由 PM 的实例视图注入
	// （唯一活动实例视图），本结构**不自建第二份登记表**。nil / 未命中 → workdir 未知。
	rMu             sync.RWMutex
	instanceWorkDir func(instanceID string) (string, bool)
}

// NewRouter 构造路由策略（空发布账本）。
func NewRouter() *Router {
	return &Router{published: make(map[ledgerKey]pubStamp)}
}

// SetInstanceWorkDir 注入 `instance_id → work_dir` 反查（宿主装配期调用一次）。
// fn 为 nil = 不反查（workdir 未知）。实现必须并发安全。
func (r *Router) SetInstanceWorkDir(fn func(instanceID string) (string, bool)) {
	if r == nil {
		return
	}
	r.rMu.Lock()
	r.instanceWorkDir = fn
	r.rMu.Unlock()
}

// workDirOfInstance 按 instance 反查 workdir（未注入 / 未命中 → ok=false）。
func (r *Router) workDirOfInstance(instanceID string) (string, bool) {
	if instanceID == "" {
		return "", false
	}
	r.rMu.RLock()
	fn := r.instanceWorkDir
	r.rMu.RUnlock()
	if fn == nil {
		return "", false
	}
	wd, ok := fn(instanceID)
	if !ok || wd == "" {
		return "", false
	}
	return wd, true
}

// Mark 记录一次发布（**发布前**调用；owner = 该窗口实例 id）。标记值 = subject + 载荷指纹。
func (r *Router) Mark(owner, subject string, payload []byte) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.published[ledgerKey{owner: owner, subject: subject}] = pubStamp{
		fp: pubFingerprint(subject, payload),
		at: time.Now(),
	}
}

// selfPublished 判定「该 sink 正是本条事件的发布方」（过期即清理、指纹逐字节相等才算自环）。
func (r *Router) selfPublished(owner, subject string, payload []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := ledgerKey{owner: owner, subject: subject}
	st, ok := r.published[k]
	if !ok {
		return false
	}
	if time.Since(st.at) >= selfPublishWindow {
		delete(r.published, k) // 过期：清理，不再视为自环
		return false
	}
	return st.fp == pubFingerprint(subject, payload)
}

// Route 计算一条事件应投递的目标（规则见包注释 ①–⑤）。
//
// 自发布（①）按 **sink 级**判定而非整条事件 —— 与迁移前「每窗口各有发布账本、只吞自己发的那条」
// 逐字等价（同一主题两个窗口各发一次时会分别吞掉各自的那条）。
func (r *Router) Route(topic string, payload []byte, sinks []Sink) []Target {
	// ② persist 同主题应答（他源发出、带 req_id + ok + result|error）不回投任何前端。
	if isDataReply(topic, payload) {
		return nil
	}
	instID := eventInstanceID(payload)
	// ④ workdir 级主题 → 解析事件 workdir（载荷 `work_dir` 优先，否则按 instance 反查）。
	narrow, wd := false, ""
	if IsWorkDirTopic(topic) {
		if v, ok := r.eventWorkDir(payload, instID); ok {
			narrow, wd = true, v
		} else {
			slog.Debug("workspace: workdir 级主题的事件 workdir 未知 → 保守全投",
				"topic", topic, "instance_id", instID)
		}
	}
	out := make([]Target, 0, len(sinks))
	for i, s := range sinks {
		// ① 自发布：本 sink 就是发布方 → 吞掉这条回环。
		if r.selfPublished(s.ID, topic, payload) {
			continue
		}
		// ③ 载荷带 `instance_id` 且本 sink 已声明实例归属 → 必须精确相等（未声明一律放行）。
		if instID != "" {
			if own := s.instanceOf(); own != "" && own != instID {
				continue
			}
		}
		// ④ workdir 级主题只投**同 workdir** 的 sink（两边都已知时才判；sink 未知 → 放行）。
		if narrow && s.WorkDir != "" && !pathx.Same(s.WorkDir, wd) {
			continue
		}
		out = append(out, Target{Index: i, ID: s.ID, Instance: s.Instance, WorkDir: s.WorkDir, Plugin: s.Plugin})
	}
	return out
}

// instanceOf 返回 sink 的实例归属：插件 sink → Instance；窗口 sink → ID。
// 空 = **未声明**（未标识）→ 调用方一律放行（宁可少收紧，不可漏投）。
func (s Sink) instanceOf() string {
	if s.Plugin {
		return s.Instance
	}
	return s.ID
}

// eventWorkDir 解析一条事件的 workdir：载荷 `work_dir` 优先；否则按 `instance_id` 反查。
// 都拿不到 → ok=false（= 未知，调用方保守全投）。
func (r *Router) eventWorkDir(payload []byte, instID string) (string, bool) {
	if wd := eventWorkDirField(payload); wd != "" {
		return wd, true
	}
	return r.workDirOfInstance(instID)
}

// ─── 主题级别档案（显式清单，不靠前缀猜）────────────────

// WorkDirTopics 是 workdir 级主题（收紧的唯一判据）。**只读**：调用方不得修改。
//
// 收录依据：
//   - `filesys.changed` / `filesys.watch-error`（61 §2.4）：载荷带 `work_dir`，语义 = 某工作目录内的
//     文件变化；filesys 在无 watch 声明者时不带 `instance_id` 直接广播 → 必须由 workdir 把关。
//   - `data-<domain>-refresh` 的项目级域（61 §3.1）：prj-config / prj-security / prompt / mcp /
//     scenario / memory 的落点都是项目级或「项目优先」的四级合并 → 同 work_dir 的窗口共享同一份
//     数据；列入本表是**兜底**（当前 persist 带 `instance_id` 逐实例下发）。
var WorkDirTopics = map[string]struct{}{
	msgkeys.TopicFilesysChanged:    {},
	msgkeys.TopicFilesysWatchError: {},

	msgkeys.TopicDataPrjConfigRefresh:   {},
	msgkeys.TopicDataPrjSecurityRefresh: {},
	msgkeys.TopicDataPromptRefresh:      {},
	msgkeys.TopicDataMcpRefresh:         {},
	msgkeys.TopicDataScenarioRefresh:    {},
	msgkeys.TopicDataMemoryRefresh:      {},
}

// ServerTopics 是**显式列白**的「服务端级广播」：语义上必须跨窗口（含不同 workdir），不得收紧。
// 先查本表 → 即使将来有人误把它们加进 `WorkDirTopics`，也不会收紧。**只读**。
var ServerTopics = map[string]struct{}{
	msgkeys.TopicDataUserConfigChanged:   {},
	msgkeys.TopicDataSessionTitleChanged: {},
	msgkeys.TopicDataUserConfigRefresh:   {},
	msgkeys.TopicConfigRefresh:           {},
}

// IsWorkDirTopic 判定主题是否 workdir 级（server 级白名单优先）。
func IsWorkDirTopic(topic string) bool {
	if _, ok := ServerTopics[topic]; ok {
		return false
	}
	_, ok := WorkDirTopics[topic]
	return ok
}

// ─── 纯函数 ─────────────────────────────────────────────

// pubFingerprint 计算「主题 + 载荷」的指纹（与 llm/httpapi 的 eventFingerprint 同口径）。
func pubFingerprint(subject string, payload []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(subject))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload)
	return h.Sum64()
}

// isDataReply 判定「persist 同主题应答」信封：`data-*` 主题 + 带 `req_id` 与非空 `ok`，
// 且含应答专属信封键 `result` 或 `error` 的载荷。
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

// eventInstanceID 从事件载荷提取 `instance_id`（无字段 / 空串 / 非 JSON 对象 → ""）。
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

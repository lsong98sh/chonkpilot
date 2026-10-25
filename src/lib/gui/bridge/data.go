// 数据桥（v3，2026-10-25 单一入口）：data-<domain>-<op> 统一由 **persist 数据服务**处理。
//   - dataCall：唯一入口 → dataViaPersist（请求注入 req_id + instance_id；应答按 req_id 匹配；
//     无应答保护 = `b.dataOffline` 快失败（no-server）+ 3s 超时兜底）。
//     **双绑定已删除**：桥内不再 inline 直调 data 门面（原 facade_domains.go / facade_session.go
//     已删），desktop / gui / 插件一律走同一条 MQ 入口 —— 一份翻译、一份测试。
//   - prjConfigSave/Load/List：gui.* 状态（layout/window/ui/opened-files/filetree-*）
//     落 prj config 表键值面（61-消息一览 §3.1；记录形态 {"v":"<json>"}）
//   - readUserConfig：用户配置（usr config 表 user_config 整体对象；无记录回落默认）
//   - sval / asAnyMap / asAnySlice：值归一化（compat/local 等桥内通用）
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/idgen"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// dataReqTimeout data-* 请求超时（persist 应答同步：合并形态微秒级；给分离形态留裕量）。
// X1 收口：persist 恒在线（GUI 恒启 server），未接数据服务（no-server）时 dataCall 走
// `b.dataOffline` 快失败，其余无应答一律走本超时兜底返回失败信封。
const dataReqTimeout = 3 * time.Second

// dataCall 发一条 data-<domain>-<action> 请求（唯一路径 = 总线 → persist 数据服务）。
func (b *Bridge) dataCall(subject string, payload []byte) (result any, errs []error) {
	// --no-server（本地无数据服务）：直接快速失败返回空信封，不经总线等待 3s 超时
	// （首屏 init-data 会串起多个 dataCall → 数秒累积）。调用方（prjConfigList /
	// readUserConfig 等）据 errs 回落缺省空值，行为与「超时兜底」一致，仅耗时归零。
	if b.dataOffline {
		err := fmt.Errorf("%s: data service offline (no server)", subject)
		return map[string]any{"ok": false, "error": err.Error()}, []error{err}
	}
	return b.dataViaPersist(subject, payload)
}

// dataViaPersist 发 data-<domain>-<op> 请求并等结果（对齐 server dataclient.go 语义）：
// 请求注入 req_id + instance_id；应答与请求同主题（persist.reply/fail 直发，无 .reply）。
// 成功 → 返回应答 result 载荷（map，透传）；失败/超时 → {ok:false, error} 信封 + errs。
func (b *Bridge) dataViaPersist(subject string, payload []byte) (result any, errs []error) {
	var req map[string]any
	if err := json.Unmarshal(payload, &req); err != nil || req == nil {
		req = map[string]any{}
	}
	req["req_id"] = idgen.NewUUID()
	if _, ok := req["instance_id"]; !ok {
		req["instance_id"] = b.instanceID
	}
	raw, _ := json.Marshal(req)

	type reply struct {
		result map[string]any
		err    error
	}
	done := make(chan reply, 1)
	sub, err := b.bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			ReqID  string         `json:"req_id"`
			OK     *bool          `json:"ok"`
			Result map[string]any `json:"result"`
			Error  string         `json:"error"`
		}
		if json.Unmarshal(v.Payload, &m) != nil || m.ReqID != req["req_id"] || m.OK == nil {
			return nil // 忽略他人请求/应答与失败回复
		}
		if *m.OK {
			select {
			case done <- reply{result: m.Result}:
			default:
			}
			return nil
		}
		msg := m.Error
		if msg == "" {
			msg = subject + " failed"
		}
		select {
		case done <- reply{err: errors.New(msg)}:
		default:
		}
		return nil
	})
	if err != nil {
		e := fmt.Errorf("%s: subscribe: %w", subject, err)
		return map[string]any{"ok": false, "error": e.Error()}, []error{e}
	}
	defer func() { _ = sub.Unsubscribe() }()

	// 防环：请求与应答同主题（persist.reply/fail 直发），标记自发布跳过 forwardEvent 回显。
	b.markPublished(subject, raw)
	_ = b.bus.Emit(context.Background(), subject, raw)
	select {
	case r := <-done:
		if r.err != nil {
			return map[string]any{"ok": false, "error": r.err.Error()}, []error{r.err}
		}
		return r.result, nil
	case <-time.After(dataReqTimeout):
		e := fmt.Errorf("%s via persist timeout", subject)
		return map[string]any{"ok": false, "error": e.Error()}, []error{e}
	}
}

// ── prj-config 键值面（gui.* 状态落库；20-gui / 61-消息一览 §3.1）──

// prjConfigSave 保存 prj config 键（save 载荷 {data:{key,value}}，value = 值字符串）。
// 失败返回 error（调用方按需静默丢弃，不阻塞 UI）。
func (b *Bridge) prjConfigSave(key, value string) error {
	raw, _ := json.Marshal(map[string]any{"data": map[string]any{"key": key, "value": value}})
	_, errs := b.dataCall(msgkeys.TopicDataPrjConfigSave, raw)
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// prjConfigSaveBatch 批量保存 prj config 键（save 载荷 {data:{entries:{<key>:<value>,…}}}，
// 61 §3.1 批量 entries 面；persist 整批一次落库 + **仅 1 条**刷新广播）。空 entries → 直接返回。
func (b *Bridge) prjConfigSaveBatch(entries map[string]string) error {
	if len(entries) == 0 {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"data": map[string]any{"entries": entries}})
	_, errs := b.dataCall(msgkeys.TopicDataPrjConfigSave, raw)
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// prjConfigLoad 读 prj config 键（load 载荷顶层 {id}；应答 result.data = 值字符串；
// 无记录/失败 → ""）。
func (b *Bridge) prjConfigLoad(key string) string {
	raw, _ := json.Marshal(map[string]any{"id": key})
	res, errs := b.dataCall(msgkeys.TopicDataPrjConfigLoad, raw)
	if len(errs) > 0 {
		return ""
	}
	m, ok := res.(map[string]any)
	if !ok {
		return ""
	}
	if s, ok := m["data"].(string); ok {
		return s
	}
	return sval(m["data"])
}

// prjConfigList 一把取 prj config 表平铺 map（list 载荷 {}；应答 result.list = key→值
// 字符串平铺；失败 → 空 map。前端启动恢复布局/UI/文件树展开键用，读缺省空值）。
func (b *Bridge) prjConfigList() map[string]any {
	res, errs := b.dataCall(msgkeys.TopicDataPrjConfigList, []byte(`{}`))
	if len(errs) > 0 {
		return map[string]any{}
	}
	m, ok := res.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	if list := asAnyMap(m["list"]); list != nil {
		return list
	}
	return map[string]any{}
}

// ── 用户配置（usr config 表 user_config 整体对象；61-消息一览 §3.1）──

// defaultUserConfig 无记录/读取失败时的默认配置（对齐前端消费字段 llms/defaultLLM/
// defaultScenario/theme）。theme 缺省 = light（与 persist userConfigSystemDefaults 一致；
// 原 "system" 无实现，见 64-配置项一览 §3）。
// defaultScenario 类型 = **string**（场景目录名引用；空 = 未配置），与 persist
// userConfigSystemDefaults 的 "" 对齐（2026-09-19 修正原 int 0 的类型不一致）。
func defaultUserConfig() map[string]any {
	return map[string]any{
		"llms":            []any{},
		"defaultLLM":      0,
		"defaultScenario": "",
		"theme":           "light",
	}
}

// readUserConfig 读用户配置（data-user-config-load；persist 无记录回落默认配置）。
// 读取失败（超时等）→ 默认配置 + error（调用方按需提示；值不因落库异常而缺）。
func readUserConfig(b *Bridge) (map[string]any, error) {
	res, errs := b.dataCall(msgkeys.TopicDataUserConfigLoad, []byte(`{}`))
	if len(errs) > 0 {
		return defaultUserConfig(), errs[0]
	}
	m, ok := res.(map[string]any)
	if !ok {
		return defaultUserConfig(), nil
	}
	if cfg, ok := m["data"].(map[string]any); ok {
		return cfg, nil
	}
	return defaultUserConfig(), nil
}

// asAnyMap 归一化「平铺 键→值」取值：MQ 序列化往返给出 map[string]any；直接构造处也可能给
// map[string]string。两者都认 —— 调用方只认"平铺键值表"这一领域事实。
func asAnyMap(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case map[string]string:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = val
		}
		return out
	}
	return nil
}

// asAnySlice 归一化「领域对象数组」取值（[]any 与 []map[string]any 两种形态都认）。
func asAnySlice(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case []map[string]any:
		out := make([]any, 0, len(t))
		for _, m := range t {
			out = append(out, m)
		}
		return out
	}
	return nil
}

// SessionKind 读某会话的 `kind`（`sessions.kind`；群聊 = `group_chat`）。
//
// 用途 = **群聊窗口单独额度**判定（群聊方案 §9 D28/D30 · 阶段 G G3）：宿主在 `open-chat` 建窗前
// 需知道目标会话是不是群聊，而 `gui.window.list` / `open-chat` 的**载荷不变**（零新增字段）→
// 不能由前端传，只能宿主自取。取数经既有**只读**消息 `data-session-get`。
// **取不到**（总线不可用 / 会话不存在 / 超时 / --no-server）→ 空串 →
// 调用方按**普通会话**处理 → 普通对话窗口的既有额度与行为逐字不变。
func (b *Bridge) SessionKind(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	payload, err := json.Marshal(map[string]any{"id": sessionID})
	if err != nil {
		return ""
	}
	res, errs := b.dataCall(msgkeys.TopicDataSessionGet, payload)
	if len(errs) > 0 {
		return ""
	}
	m, ok := res.(map[string]any)
	if !ok {
		return ""
	}
	// 应答形态 = `{data: 会话行|null}`（wire.SessionGetResult）。
	data, ok := m["data"].(map[string]any)
	if !ok {
		return ""
	}
	return sval(data["kind"])
}

// sval 把 Record/载荷值转字符串（string 原样；数字/bool 等 fmt 兜底；nil → ""）。
func sval(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// cfgScalar 把前端配置值转存储字符串：字符串原样；数字/bool 取字面量（避免 fmt 引入
// 科学计数等差异）；其余 JSON 序列化。
func cfgScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// prefixedConfig 从扁平配置 map 取指定前缀的条目并剥前缀（v6 细 key 还原为对象，
// 如 layout.filetreeWidth → {filetreeWidth}）。
func prefixedConfig(cfg map[string]any, prefix string) map[string]any {
	out := map[string]any{}
	for k, v := range cfg {
		if strings.HasPrefix(k, prefix) {
			out[strings.TrimPrefix(k, prefix)] = sval(v)
		}
	}
	return out
}

// legacyBlob 解析 v6 之前的整块 JSON 键（迁移期兼容读取：细 key 缺失时回落；坏值 → 空 map）。
func legacyBlob(cfg map[string]any, key string) map[string]any {
	out := map[string]any{}
	if s := sval(cfg[key]); s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	return out
}

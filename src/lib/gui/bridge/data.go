// 数据桥（v2.2，2026-09-21 阶段 4 第四批 / 41 G-36：data 面各域均走门面）：
//   - dataCall：data-<domain>-<action> 统一入口 —— **config 类**（prj-config / prompt /
//     prj-security / user-config）· **session 域**（data-session-*：session / turn / message
//     三域面）· **tasktree / knowledge / filelist / scenario / memory** 经 data 门面（inline
//     绑定，同进程直调，dataViaFacade → facade_session.go / facade_domains.go）；
//     其余与未接线时经总线 persist 数据服务（dataViaPersist）。两条路径的应答载荷键名与
//     形态逐字一致（61 §3），**前端消息面一字未变**。
//   - dataViaPersist：data-* 请求-响应客户端（请求注入 req_id + instance_id；应答按
//     req_id 匹配；3s 超时兜底 = 唯一无应答保护，no-server 离线快败窗已退役删除）
//   - prjConfigSave/Load/List：gui.* 状态（layout/window/ui/opened-files/filetree-*）
//     落 prj config 表键值面（61-消息一览 §3.1；记录形态 {"v":"<json>"}）
//   - readUserConfig：用户配置（usr config 表 user_config 整体对象；无记录回落默认）
//   - sval / asAnyMap / asAnySlice：值归一化（compat/local 等桥内通用；门面 inline 绑定给出
//     领域形态 map[string]string / []map[string]any，序列化绑定给出 map[string]any / []any）
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// dataReqTimeout data-* 请求超时（persist 应答同步：合并形态微秒级；给分离形态留裕量）。
// X1 收口：persist 恒在线（GUI 恒启 server），无应答一律走本超时兜底返回失败信封。
const dataReqTimeout = 3 * time.Second

// dataCall 发一条 data-<domain>-<action> 请求：**config 类与 session 域走 data 门面**
// （同进程直调，阶段 4 第二/三批 / 41 G-34），其余域与未注入门面时经总线（dataViaPersist，
// 行为同改前）。两条路径的应答载荷键名/形态逐字一致，调用方（前端 promise / gui.* 本地实现）无感。
func (b *Bridge) dataCall(subject string, payload []byte) (result any, errs []error) {
	if res, errs, ok := b.dataViaFacade(subject, string(payload)); ok {
		return res, errs
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
	req["req_id"] = newUUID()
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
	b.markPublished(subject)
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

// ── config 类 data-*：经 data 门面（阶段 4 第二批，41 G-34）──────────────

// configFacadeDomains 是**由 data 门面承载**的 config 类域（61 §3.1 配置五域中的 kv / 用户
// 配置域；scenario = 文件化 capability 域、memory = 文档域，由第四批承载，见 facade_domains.go）。
var configFacadeDomains = []string{"prj-config", "prompt", "prj-security", "user-config"}

// dataViaFacade 处理**已进门面**的 data-<domain>-<op>：**走 data 门面**（inline 绑定，同进程
// 直调），不再"再发一条 MQ 给 persist"（A6 收敛态的下一站：23 §7「一律经门面」）：
//   - config 类（prj-config / prompt / prj-security / user-config）→ 第二批（61 §3.1）
//   - session 域（data-session-*，含 turn / message 两域面）→ 第三批（61 §3.2 / §3.2a），
//     见 facade_session.go
//   - tasktree / knowledge / filelist / scenario / memory → 第四批（61 §3.3 / §3.4 及其余），
//     见 facade_domains.go
//
// **前端消息面一字不变**：主题名与请求 payload 不变（本函数只解析既有字段），应答 payload 的
// 键名与取值形态由 `facade/wire` 生成（MQ 信封层用同一份翻译）→ 两路径逐字一致。
// 写入后的变更广播（data-<domain>-refresh、user-config 另带 config-refresh、session-new、
// task-deleted、scenario/memory 刷新）由门面实现侧发出（同一份实现 → 与 MQ 路径同源），
// 故订阅方（前端 / 插件 / llm server）照旧收到。
//
// handled=false = 非门面域 / 未注入门面绑定（-no-server 薄客户端）→ 交回总线转发。
func (b *Bridge) dataViaFacade(subject, payloadJSON string) (result any, errs []error, handled bool) {
	if b.cfg == nil {
		return nil, nil, false
	}
	if isSessionSubject(subject) {
		return b.sessionViaFacade(strings.TrimPrefix(subject, sessionSubjectPrefix), payloadJSON)
	}
	if res, errs, ok := b.domainViaFacade(subject, payloadJSON); ok {
		return res, errs, true
	}
	domain, op, ok := splitConfigSubject(subject)
	if !ok {
		return nil, nil, false
	}
	var req map[string]any
	if json.Unmarshal([]byte(payloadJSON), &req) != nil || req == nil {
		req = map[string]any{}
	}
	instanceID, _ := req["instance_id"].(string)
	if instanceID == "" {
		instanceID = b.instanceID // 与 dataViaPersist 同口径：入口绑定兜底
	}
	scope := facade.Scope{WorkDir: b.workDir, DataDir: b.dataDir}
	fail := func(err error) (any, []error) {
		return map[string]any{"ok": false, "error": err.Error()}, []error{err}
	}
	if domain == "user-config" {
		return b.userConfigViaFacade(op, req, instanceID, fail)
	}
	return b.kvViaFacade(domain, op, req, instanceID, scope, fail)
}

// kvViaFacade 处理 kv 域（prj-config / prompt / prj-security）的四个动作。
func (b *Bridge) kvViaFacade(domain, op string, req map[string]any, instanceID string,
	scope facade.Scope, fail func(error) (any, []error)) (any, []error, bool) {
	kvReq := facade.ConfigKVGetRequest{Domain: domain, InstanceID: instanceID, Scope: scope}
	switch op {
	case "list":
		resp, err := b.cfg.ConfigKVList(facade.ConfigKVListRequest{
			Domain: domain, InstanceID: instanceID, Scope: scope,
		})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"list": resp.List}, nil, true
	case "load":
		kvReq.Keys = []string{reqID(req)}
		resp, err := b.cfg.ConfigKVGet(kvReq)
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"data": resp.Values[reqID(req)]}, nil, true
	case "save":
		// 报文语义同 persist（61 §3.1）：兼容批量 `data:{entries:{…}}` 与既有单键
		// `data:{key,value}`（entries 优先；翻译见 wire.ConfigSaveEntries —— 前端不变时行为不变）。
		entries, id, ok := wire.ConfigSaveEntries(asAnyMap(req["data"]))
		if !ok {
			res, errs := fail(errors.New("key required"))
			return res, errs, true
		}
		if _, err := b.cfg.ConfigKVSet(facade.ConfigKVSetRequest{
			Domain: domain, InstanceID: instanceID, Entries: entries, Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"ok": true, "id": id}, nil, true
	case "delete":
		if _, err := b.cfg.ConfigKVDelete(facade.ConfigKVDeleteRequest{
			Domain: domain, InstanceID: instanceID, Keys: []string{reqID(req)}, Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"ok": true}, nil, true
	}
	return nil, nil, false // 未知动作 → 交回总线（不静默丢弃）
}

// userConfigViaFacade 处理用户配置域的四个动作（应答键名与 MQ 路径逐字一致）。
func (b *Bridge) userConfigViaFacade(op string, req map[string]any, instanceID string,
	fail func(error) (any, []error)) (any, []error, bool) {
	switch op {
	case "list":
		resp, err := b.cfg.UserConfigView(facade.UserConfigViewRequest{})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"list": resp.List}, nil, true
	case "load":
		resp, err := b.cfg.UserConfigGet(facade.UserConfigGetRequest{
			InstanceID: instanceID, Scope: facade.Scope{WorkDir: b.workDir, DataDir: b.dataDir},
		})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"data": resp.Config}, nil, true
	case "save":
		entries, _ := req["data"].(map[string]any)
		resp, err := b.cfg.UserConfigSet(facade.UserConfigSetRequest{
			Entries: entries, InstanceID: instanceID,
		})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"ok": true, "id": resp.ID}, nil, true
	case "delete":
		key := reqID(req)
		keys := []string{}
		if key != "" {
			keys = append(keys, key)
		}
		if _, err := b.cfg.UserConfigDelete(facade.UserConfigDeleteRequest{
			Keys: keys, InstanceID: instanceID,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		if key != "" && key != "user_config" { // user_config = legacy 整块键（清空整份，应答无 id）
			return map[string]any{"ok": true, "id": key}, nil, true
		}
		return map[string]any{"ok": true}, nil, true
	}
	return nil, nil, false
}

// splitConfigSubject 把 data-<domain>-<op> 解析为（config 类域名, 动作）；非 config 类 → false。
func splitConfigSubject(subject string) (domain, op string, ok bool) {
	rest := strings.TrimPrefix(subject, "data-")
	for _, d := range configFacadeDomains {
		if strings.HasPrefix(rest, d+"-") {
			return d, strings.TrimPrefix(rest, d+"-"), true
		}
	}
	return "", "", false
}

// reqID 取请求 id：顶层 id → data.id → data.key（与 persist reqKey 同口径，统一转字符串）。
func reqID(req map[string]any) string {
	if id := sval(req["id"]); id != "" {
		return id
	}
	data, _ := req["data"].(map[string]any)
	if data == nil {
		return ""
	}
	if id := sval(data["id"]); id != "" {
		return id
	}
	if k, _ := data["key"].(string); k != "" {
		return k
	}
	return ""
}

// ── prj-config 键值面（gui.* 状态落库；20-gui / 61-消息一览 §3.1）──

// prjConfigSave 保存 prj config 键（save 载荷 {data:{key,value}}，value = 值字符串）。
// 数据面 = dataCall（config 类 → 门面 inline；未接线 → 总线 persist，见 data.go 头）。
// 失败返回 error（调用方按需静默丢弃，不阻塞 UI）。
func (b *Bridge) prjConfigSave(key, value string) error {
	raw, _ := json.Marshal(map[string]any{"data": map[string]any{"key": key, "value": value}})
	_, errs := b.dataCall("data-prj-config-save", raw)
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// prjConfigLoad 读 prj config 键（load 载荷顶层 {id}；应答 result.data = 值字符串；
// 无记录/失败 → ""）。
func (b *Bridge) prjConfigLoad(key string) string {
	raw, _ := json.Marshal(map[string]any{"id": key})
	res, errs := b.dataCall("data-prj-config-load", raw)
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
	res, errs := b.dataCall("data-prj-config-list", []byte(`{}`))
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
	res, errs := b.dataCall("data-user-config-load", []byte(`{}`))
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

// asAnyMap 归一化「平铺 键→值」取值：门面 inline 绑定（同进程直调）给出领域形态
// map[string]string（配置值恒为字符串），序列化绑定（MQ / http）给出 map[string]any。
// 两者都认 —— 调用方只认"平铺键值表"这一领域事实。
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

// asAnySlice 归一化「领域对象数组」取值：inline 绑定给出 []map[string]any，序列化绑定给出
// []any。两者都认。
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

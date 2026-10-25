// configuser.go —— usr 层「插件自身配置」能力面（PLUGIN-UI-1 第四段）。
//
// 口径（2026-10-24 第二版：插件配置**保持四级**）：`system` / `usr` / `prj` / `prjusr` 四级并存，
// **写哪一级由插件自己的配置页决定**；能力面按 scope 提供方法，与既有 get/load/delete 对称——
// usr 层 = `plugin/v1/config.user.{get,load,set,delete}`（本文件）、prj 层 =
// `plugin/v1/config.project.*`（capabilities.go）。manifest 的 `config.scope` 只作声明，
// 平台不据此选级；「密钥恒落 usr」只是存储位置惯例。prjusr 无独立数据面域 / 主题 → 不提供
// `config.prjusr.*`（数据层 prjusr 仅为 prj-config 的个人运行态键内部落点）。
//
// 唯一链路：插件 configStore → `plugin/v1/config.user.{load,get,set,delete}`
//
//	→ EmitData（同主题应答）→ data 层 `data-user-config-*`
//
// 两条硬约束：
//  1. **键前缀由桥会话身份推导**（`plugins.<session.PluginID()>.`），调用方**不得声明**；
//     任一键越界 → `config_key_forbidden`，且**整批不写**（先全量校验再发）。
//  2. 读侧只回**调用方自己**的键（最小权限：插件拿不到别的插件 / llms 等 usr 配置）。
package bridge

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	mq "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/bus"
	msgkeys "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/topics"
)

// configUserMethods 是 usr 配置**读**能力（get / load → data-user-config-load）。
//
// 与 configProjectMethods 同理必须走 EmitData（raw Emit 的 v.Result 恒空）。
var configUserMethods = []mqMethod{
	{"plugin/v1/config.user.get", msgkeys.TopicDataUserConfigLoad},
	{"plugin/v1/config.user.load", msgkeys.TopicDataUserConfigLoad},
}

// 插件配置键 / 值的长度上限（与 data 层 internal/config 的校验同口径：
// 超限明确拒绝，不静默截断）。
const (
	maxPluginConfigKeyChars   = 128
	maxPluginConfigValueChars = 32768
)

// pluginConfigPrefix 返回调用方自身的强制键前缀（`plugins.<插件id>.`）。
func pluginConfigPrefix(pluginID string) string {
	return "plugins." + pluginID + "."
}

// registerUserConfigCapabilities 注册 usr 配置读写能力（读收窄 + 写前缀强制）。
func registerUserConfigCapabilities(reg *Registry, bus mq.Bus) {
	if bus == nil {
		return
	}
	for _, m := range configUserMethods {
		m := m
		reg.Register(m.Method, func(ctx context.Context, sess *Session, args json.RawMessage) (any, error) {
			pluginID, err := configCallerID(sess)
			if err != nil {
				return nil, err
			}
			payload, view, err := configArgs(args)
			if err != nil {
				return nil, err
			}
			// 单键读（`{id}` / `{key}`）= 语义与 config.project.get 对齐；无语义键 → 回自身子树。
			key := configKeyArg(view)
			if key != "" {
				if err := validatePluginConfigKey(key, pluginID); err != nil {
					return nil, err
				}
			}
			res, err := EmitData(ctx, bus, m.Topic, payload)
			if err != nil {
				return nil, &Error{Code: CodeInternal, Msg: err.Error()}
			}
			return ownConfigView(res, pluginID, key), nil
		})
	}
	reg.Register("plugin/v1/config.user.set", func(ctx context.Context, sess *Session, args json.RawMessage) (any, error) {
		return handleUserConfigSet(ctx, bus, sess, args)
	})
	reg.Register("plugin/v1/config.user.delete", func(ctx context.Context, sess *Session, args json.RawMessage) (any, error) {
		return handleUserConfigDelete(ctx, bus, sess, args)
	})
}

// handleUserConfigSet 写 usr 层插件配置：`{entries:{<键>:<值>}}`（或单键 `{id,value}`）。
//
// 先整批校验（前缀 / 键名 / 值类型与长度），任一失败即返回错误且**不发**数据面请求
// —— 保证「越界即整批不写」。
func handleUserConfigSet(ctx context.Context, bus mq.Bus, sess *Session, args json.RawMessage) (any, error) {
	pluginID, err := configCallerID(sess)
	if err != nil {
		return nil, err
	}
	_, view, err := configArgs(args)
	if err != nil {
		return nil, err
	}
	entries, err := configEntries(view)
	if err != nil {
		return nil, err
	}
	body := make(map[string]any, len(entries)+1)
	for key, raw := range entries {
		if err := validatePluginConfigKey(key, pluginID); err != nil {
			return nil, err
		}
		val, err := configScalar(key, raw)
		if err != nil {
			return nil, err
		}
		body[key] = val
	}
	if inst, ok := view["instance_id"].(string); ok && strings.TrimSpace(inst) != "" {
		body["instance_id"] = strings.TrimSpace(inst)
	}
	res, err := EmitData(ctx, bus, msgkeys.TopicDataUserConfigSave, body)
	if err != nil {
		return nil, &Error{Code: CodeInternal, Msg: err.Error()}
	}
	return res, nil
}

// handleUserConfigDelete 删一条 usr 层插件配置（`{id}` / `{key}`；前缀同样强制）。
func handleUserConfigDelete(ctx context.Context, bus mq.Bus, sess *Session, args json.RawMessage) (any, error) {
	pluginID, err := configCallerID(sess)
	if err != nil {
		return nil, err
	}
	_, view, err := configArgs(args)
	if err != nil {
		return nil, err
	}
	key := configKeyArg(view)
	if key == "" {
		return nil, errf(CodeArgsInvalid, "config: 缺少 id（要删除的配置键）")
	}
	if err := validatePluginConfigKey(key, pluginID); err != nil {
		return nil, err
	}
	body := map[string]any{"id": key}
	if inst, ok := view["instance_id"].(string); ok && strings.TrimSpace(inst) != "" {
		body["instance_id"] = strings.TrimSpace(inst)
	}
	res, err := EmitData(ctx, bus, msgkeys.TopicDataUserConfigDelete, body)
	if err != nil {
		return nil, &Error{Code: CodeInternal, Msg: err.Error()}
	}
	return res, nil
}

// ── 身份 / 参数 / 校验 ─────────────────────────────────────────────

// configCallerID 取调用方插件 id（未 hello → 明确错误）。
//
// id 里不允许 `.`：否则 `plugins.<id>.` 会让调用方越界写到别的插件键空间。
func configCallerID(sess *Session) (string, error) {
	if sess == nil || sess.PluginID() == "" {
		return "", errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	id := sess.PluginID()
	if strings.Contains(id, ".") {
		return "", errf(CodePluginUnauthorized, "插件 id 不得包含 '.'（配置键前缀按 id 推导）")
	}
	return id, nil
}

// configArgs 解析入参并返回「平铺视图」（顶层字段 + `data` 内字段，与 data 层 parseDataReq 同口径）。
// payload 原样交给 EmitData（`data` 包装由数据面自己解）。
func configArgs(args json.RawMessage) (payload, view map[string]any, err error) {
	payload = map[string]any{}
	if len(args) > 0 {
		if uerr := json.Unmarshal(args, &payload); uerr != nil {
			return nil, nil, &Error{Code: CodeArgsInvalid, Msg: "payload 解析失败: " + uerr.Error()}
		}
	}
	view = make(map[string]any, len(payload))
	for k, v := range payload {
		view[k] = v
	}
	if inner, ok := payload["data"].(map[string]any); ok {
		for k, v := range inner {
			if _, exists := view[k]; !exists {
				view[k] = v
			}
		}
	}
	return payload, view, nil
}

// configKeyArg 取单键参数（顶层 id/key，或 data.id/data.key）。
func configKeyArg(view map[string]any) string {
	if k, _ := view["id"].(string); k != "" {
		return k
	}
	if k, _ := view["key"].(string); k != "" {
		return k
	}
	return ""
}

// configEntries 取写侧「键 → 值」：`entries` 优先，回落单键 `{id|key, value}`。
func configEntries(view map[string]any) (map[string]any, error) {
	if raw, ok := view["entries"]; ok && raw != nil {
		entries, ok := raw.(map[string]any)
		if !ok {
			return nil, errf(CodeArgsInvalid, "config: entries 必须是对象（键 → 值）")
		}
		if len(entries) == 0 {
			return nil, errf(CodeArgsInvalid, "config: entries 不能为空")
		}
		return entries, nil
	}
	key := configKeyArg(view)
	if key == "" {
		return nil, errf(CodeArgsInvalid, "config: 缺少 entries")
	}
	if _, ok := view["value"]; !ok {
		return nil, errf(CodeArgsInvalid, "config: 缺少 value")
	}
	return map[string]any{key: view["value"]}, nil
}

// validatePluginConfigKey 校验键属于调用方且形如 `plugins.<id>.<字段>`。
//
// 前缀 = 桥会话身份推导的 `plugins.<PluginID>.`；越界 → `config_key_forbidden`。
func validatePluginConfigKey(key, pluginID string) error {
	if key == "" {
		return errf(CodeArgsInvalid, "config: 配置键不能为空")
	}
	prefix := pluginConfigPrefix(pluginID)
	if !strings.HasPrefix(key, prefix) {
		return errf(CodeConfigKeyForbidden, "config: 配置键 %q 越界（只允许 %s* 前缀）", key, prefix)
	}
	field := strings.TrimPrefix(key, prefix)
	if field == "" || strings.HasPrefix(field, ".") || strings.HasSuffix(field, ".") || strings.Contains(field, "..") {
		return errf(CodeArgsInvalid, "config: 配置键 %q 的字段名非法", key)
	}
	if len(key) > maxPluginConfigKeyChars {
		return errf(CodeArgsInvalid, "config: 配置键超长（上限 %d 字符，收到 %d）", maxPluginConfigKeyChars, len(key))
	}
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			return errf(CodeArgsInvalid, "config: 配置键 %q 只允许可见 ASCII 字符（不含空白）", key)
		}
	}
	return nil
}

// configScalar 把写侧值归一为字符串（插件配置值恒为字符串；非标量/超长 → 明确错误）。
func configScalar(key string, raw any) (string, error) {
	var s string
	switch t := raw.(type) {
	case string:
		s = t
	case bool:
		s = strconv.FormatBool(t)
	case float64:
		if t != math.Trunc(t) || math.IsInf(t, 0) || math.IsNaN(t) {
			return "", errf(CodeArgsInvalid, "config: %s 的值必须是整数（收到 %v）", key, t)
		}
		s = strconv.FormatInt(int64(t), 10)
	case int:
		s = strconv.Itoa(t)
	case int64:
		s = strconv.FormatInt(t, 10)
	case json.Number:
		s = t.String()
	case nil:
		return "", errf(CodeArgsInvalid, "config: %s 的值不能为 null", key)
	default:
		return "", errf(CodeArgsInvalid, "config: %s 的值必须是字符串/数字/布尔（收到 %T）", key, raw)
	}
	if n := utf8.RuneCountInString(s); n > maxPluginConfigValueChars {
		return "", errf(CodeArgsInvalid, "config: %s 的值超长（上限 %d 字符，收到 %d）", key, maxPluginConfigValueChars, n)
	}
	return s, nil
}

// ownConfigView 把数据面 load 应答收窄到调用方自身键空间。
//
//   - key 非空 → `{data: <该键的值串>}`（未配置 → 空串，与 config.project.get 同口径）；
//   - key 为空 → `{data: {<自身键>: <值串>}}`（其余插件 / llms 等 usr 配置一律不返回）。
func ownConfigView(res any, pluginID, key string) any {
	cfg := map[string]any{}
	if m, ok := res.(map[string]any); ok {
		switch data := m["data"].(type) {
		case map[string]any:
			cfg = data
		case string: // 兼容「data 为单值」的应答形态（如数据面按 id 回单键）
			if key != "" {
				cfg[key] = data
			}
		}
	}
	prefix := pluginConfigPrefix(pluginID)
	if key != "" {
		v, _ := cfg[key].(string)
		return map[string]any{"data": v}
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return map[string]any{"data": out}
}

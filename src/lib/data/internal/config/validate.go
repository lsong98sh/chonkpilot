// 写侧字段校验（INPUTVAL-1 P0-2 / P0-3 / P0-4）。
//
// 口径（docs/input-validation/输入校验专题.md §2 / §3）：
//   - 前端校验 = 体验，**后端校验 = 唯一可信边界**；失败返回明确错误（不截断、不静默丢弃）；
//   - 只加在**写侧**（读侧保持既有兜底，兼容存量脏数据）；
//   - 本文件是字段基线的代码落点，前端同源规则见 `src/frontend/src/utils/fieldRules.js`。
package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 字段基线（v1 · 2026-10-21 已拍板；专题 §3）。
const (
	MaxNameChars  = 64      // 名称 / 服务名 / 场景名 / 目录名 / Agent 名 / 记忆分类
	MaxTitleChars = 128     // 会话标题 / 原语标题 / 文件与目录名 / 模型名（模型名同 128）
	MaxURLChars   = 2048    // baseURL / MCP url
	MaxPathChars  = 512     // 路径 / cwd / 工具链路径 / 信任目录
	MaxKeyChars   = 512     // apiKey
	MaxTextChars  = 262144  // 提示词 / 正文 / 记忆内容（256 KB 字符）
	MaxLLMTemp    = 2.0     // temperature 上限
	MinLLMOutTok  = 256     // maxOutputToken 下限
	MaxLLMOutTok  = 1000000 // maxOutputToken 上限
	MaxLLMCtxTok  = 1000000000
	MaxLLMIter    = 2000
)

// ── 插件自身配置（usr 层 `plugins.<插件id>.<字段>`）──────────────
//
// 口径（49-待实现项·订正 2026-10-24「插件配置不分级」）：插件配置**只有一层**，落 usr 配置库
// 的 config 表，键名 = `plugins.<插件id>.<字段>`；密钥恒落同一层（存储位置，不是调用方选级）。
// 键空间按插件 id 分段（id 内不允许 `.`），故不同插件的键互不重叠。
const (
	pluginConfigKeyPrefix = "plugins."
	// maxPluginConfigKeyChars / maxPluginConfigValueChars 是插件配置键 / 值的长度上限
	// （INPUTVAL-1 口径：超限**明确拒绝**，不静默截断）。
	maxPluginConfigKeyChars   = 128
	maxPluginConfigValueChars = 32768
)

// IsPluginConfigKey 判定键是否属于插件自身配置命名空间（`plugins.<插件id>.<字段>`）。
func IsPluginConfigKey(key string) bool {
	return strings.HasPrefix(key, pluginConfigKeyPrefix)
}

// ValidatePluginConfigKey 校验插件配置键名格式：`plugins.<id>.<字段>`
//   - id：1–64 字符，只允许字母 / 数字 / `-` / `_`（**不含 `.`**，否则越界到别的插件）；
//   - 字段：非空、不以 `.` 起止、不含 `..`；
//   - 整键 ≤ 128 字符、只允许可见 ASCII（不含空白 / 控制字符）。
func ValidatePluginConfigKey(key string) error {
	if !IsPluginConfigKey(key) {
		return fmt.Errorf("%s: 插件配置键必须以 %s 开头", key, pluginConfigKeyPrefix)
	}
	if len(key) > maxPluginConfigKeyChars {
		return fmt.Errorf("%s: 键名超长（上限 %d 字符）", key, maxPluginConfigKeyChars)
	}
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			return fmt.Errorf("%s: 只允许可见 ASCII 字符（不含空白 / 控制字符）", key)
		}
	}
	rest := strings.TrimPrefix(key, pluginConfigKeyPrefix)
	dot := strings.Index(rest, ".")
	if dot <= 0 || dot == len(rest)-1 {
		return fmt.Errorf("%s: 须形如 plugins.<插件id>.<字段>", key)
	}
	id, field := rest[:dot], rest[dot+1:]
	if len(id) > MaxNameChars {
		return fmt.Errorf("%s: 插件 id 超长（上限 %d 字符）", key, MaxNameChars)
	}
	for _, r := range id {
		ok := r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return fmt.Errorf("%s: 插件 id 只允许字母 / 数字 / - / _", key)
		}
	}
	if field == "" || strings.HasPrefix(field, ".") || strings.HasSuffix(field, ".") || strings.Contains(field, "..") {
		return fmt.Errorf("%s: 字段名非法", key)
	}
	return nil
}

// PluginConfigValue 把插件配置值归一为字符串：只接受标量（字符串 / 数字 / 布尔），
// 超长或非标量 → **明确错误**（不静默截断 / 不 JSON 序列化）。
func PluginConfigValue(key string, v any) (string, error) {
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case bool:
		s = strconv.FormatBool(t)
	case int:
		s = strconv.Itoa(t)
	case int64:
		s = strconv.FormatInt(t, 10)
	case float64:
		if t != math.Trunc(t) || math.IsInf(t, 0) || math.IsNaN(t) {
			return "", fmt.Errorf("%s: 值必须是整数（收到 %v）", key, t)
		}
		s = strconv.FormatInt(int64(t), 10)
	case json.Number:
		s = t.String()
	case nil:
		return "", fmt.Errorf("%s: 值不能为 null", key)
	default:
		return "", fmt.Errorf("%s: 值必须是字符串 / 数字 / 布尔（收到 %T）", key, v)
	}
	if n := utf8.RuneCountInString(s); n > maxPluginConfigValueChars {
		return "", fmt.Errorf("%s: 值超长（上限 %d 字符，收到 %d）", key, maxPluginConfigValueChars, n)
	}
	return s, nil
}

// ValidateUserConfigPayload 校验 usr 配置整批载荷（saveUserConfig 写库前调用）：
// 标量已知键（数值/长度）与 `llms` 集合逐条四项。未知键沿用既有语义（白名单外静默忽略）。
func ValidateUserConfigPayload(payload map[string]any) error {
	for k, v := range payload {
		if k == "id" || k == "llms" {
			continue
		}
		// 插件自身配置（`plugins.<插件id>.<字段>`）：键名格式 + 值标量/长度（INPUTVAL-1 口径）。
		// 前缀边界（只能写调用方自己）由插件桥 `plugin/v1/config.user.set` 强制；数据层只做
		// 「确属插件命名空间 + 形态合法」。失败 → 整批拒绝（不产生部分写入）。
		if IsPluginConfigKey(k) {
			if err := ValidatePluginConfigKey(k); err != nil {
				return err
			}
			if _, err := PluginConfigValue(k, v); err != nil {
				return err
			}
			continue
		}
		kind, known := userConfigKeyKinds[k]
		if !known {
			continue
		}
		if err := validateScalarKey(k, kind, v); err != nil {
			return err
		}
	}
	if raw, ok := payload["llms"]; ok {
		items, _ := raw.([]any)
		if err := validateLLMItems(items); err != nil {
			return err
		}
	}
	return nil
}

// validateScalarKey 校验 usr 标量键（只对已拍板范围的键生效）。
func validateScalarKey(key, kind string, v any) error {
	switch key {
	case "responseTimeout", "streamTimeout":
		// 已拍板：timeout 必须 > 0（0 非法）——只在显式提供且非空时校验。
		if isEmptyScalar(v) {
			return nil
		}
		n, ok := asInt(v)
		if !ok || n <= 0 {
			return fmt.Errorf("%s: 必须为正整数（>0）", key)
		}
		if n > 600 {
			return fmt.Errorf("%s: 超出范围 1–600", key)
		}
	case "retryCount", "retryBudgetSec", "autoContinueDelaySec", "turnEventLogRetentionDays", "nextRunMax":
		if isEmptyScalar(v) {
			return nil
		}
		if n, ok := asInt(v); !ok || n < 0 {
			return fmt.Errorf("%s: 必须为非负整数", key)
		}
	case "turnEventLog":
		if s, ok := v.(string); ok && s != "" {
			switch s {
			case "none", "minimal", "outline", "verbose":
			default:
				return fmt.Errorf("turnEventLog: 非法取值 %q（none|minimal|outline|verbose）", s)
			}
		}
	case "continueText":
		if s, ok := v.(string); ok {
			if err := validateText(key, s, MaxTextChars, false); err != nil {
				return err
			}
		}
	case "chromePath", "javaPath", "pythonPath", "nodePath", "goPath", "rustPath", "cCompilerPath":
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return validateAbsPath(key, s)
		}
	}
	_ = kind
	return nil
}

// validateLLMItems 逐条校验 `llms` 集合（整表替换 → 失败带 index 定位，事务失败不破坏旧数据）。
func validateLLMItems(items []any) error {
	seen := make(map[string]int, len(items))
	for i, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("llms[%d]: 条目必须为对象", i)
		}
		name := strings.TrimSpace(strOf(item["name"]))
		if err := validateName(fmt.Sprintf("llms[%d].name", i), name, MaxNameChars); err != nil {
			return err
		}
		if prev, dup := seen[name]; dup {
			return fmt.Errorf("llms[%d].name: 与 llms[%d] 重名（%q）", i, prev, name)
		}
		seen[name] = i

		// model / baseUrl 空串 = **显式继承系统默认**（exe flag `-llm-model` / `-llm-base`，既有语义，
		// `ResolveBridgeTarget` / providerFromConfig 均按空回落）→ 非空才做格式 / 长度校验。
		model := strings.TrimSpace(strOf(item["model"]))
		if model != "" {
			if err := validateText(fmt.Sprintf("llms[%d].model", i), model, MaxTitleChars, true); err != nil {
				return err
			}
		}
		baseURL := strings.TrimSpace(strOf(item["baseUrl"]))
		if baseURL != "" {
			if err := validateURL(fmt.Sprintf("llms[%d].baseUrl", i), baseURL); err != nil {
				return err
			}
		}
		if key := strOf(item["apiKey"]); key != "" {
			if err := validateAPIKey(fmt.Sprintf("llms[%d].apiKey", i), key); err != nil {
				return err
			}
		}
		// 数值范围（前端已受控，后端复验）。
		if v, exists := item["temperature"]; exists && v != nil {
			f, ok := asFloat(v)
			if !ok || f < 0 || f > MaxLLMTemp {
				return fmt.Errorf("llms[%d].temperature: 超出范围 0–2", i)
			}
		}
		// maxOutputToken：0 = 显式「未设置」（既有语义：0 原样下发 max_tokens=0），
		// 非零才受 256–1e6 约束（INPUTVAL-1）。
		if err := validateZeroOrIntRange(item, i, "maxOutputToken", MinLLMOutTok, MaxLLMOutTok); err != nil {
			return err
		}
		if err := validateIntRange(item, i, "maxContextToken", 0, MaxLLMCtxTok); err != nil {
			return err
		}
		if err := validateIntRange(item, i, "maxToolIterations", 0, MaxLLMIter); err != nil {
			return err
		}
	}
	return nil
}

// validateConfigKV 校验 prj-config / prj-security 的已知键写入（ConfigKVSet 写库前调用）。
//
// 未知键沿用既有语义（域白名单外由调用方拒绝；已知域内未知键不额外拦截）。
func ValidateConfigKV(domain string, entries map[string]string) error {
	for key, val := range entries {
		if domain == "prompt" {
			// 提示词 / 记忆内容 / 正文：≤ 256 KB 字符（专题 §3.3 文本类）。
			if err := validateText("prompt:"+key, val, MaxTextChars, false); err != nil {
				return err
			}
			continue
		}
		if domain == "prj-security" {
			if err := validateSecurityEntry(key, val); err != nil {
				return err
			}
			continue
		}
		switch key {
		case "timeout_sec":
			if n, ok := parseIntString(val); !ok || n < 1 || n > 86400 {
				return fmt.Errorf("%s: 必须为 1–86400 的整数", key)
			}
		case "max_concurrency":
			if n, ok := parseIntString(val); !ok || n < 1 || n > 1024 {
				return fmt.Errorf("%s: 必须为 1–1024 的整数", key)
			}
		case "project-id":
			if err := validateProjectID(val); err != nil {
				return err
			}
		case "keep_full_max_turns", "keep_full_max_tokens", "compress_token_threshold":
			if n, ok := parseIntString(val); !ok || n < 0 {
				return fmt.Errorf("%s: 必须为非负整数", key)
			}
		case "pending_task_timeout_sec":
			if n, ok := parseIntString(val); !ok || n < 1 {
				return fmt.Errorf("%s: 必须为正整数", key)
			}
		}
	}
	return nil
}

// validateSecurityEntry 校验一条信任目录（`{dir, writable}` JSON；专题 §3.2）。
func validateSecurityEntry(key, val string) error {
	var obj struct {
		Dir      string `json:"dir"`
		Writable any    `json:"writable"`
	}
	if err := json.Unmarshal([]byte(val), &obj); err != nil {
		return fmt.Errorf("security-%s: 必须是 {dir,writable} JSON（%v）", key, err)
	}
	dir := strings.TrimSpace(obj.Dir)
	if dir == "" {
		return fmt.Errorf("security-%s.dir: 必填", key)
	}
	if err := validateAbsPath(fmt.Sprintf("security-%s.dir", key), dir); err != nil {
		return err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("security-%s.dir: 目录不存在或不可访问（%s）", key, dir)
	}
	if obj.Writable == nil {
		return fmt.Errorf("security-%s.writable: 必填（true|false）", key)
	}
	return nil
}

// validateName 名称类：必填 + 长度 + 不含控制字符。
func validateName(field, val string, max int) error {
	if strings.TrimSpace(val) == "" {
		return fmt.Errorf("%s: 必填", field)
	}
	return validateText(field, val, max, true)
}

// validateText 文本类：长度（Unicode 码点）+ 可选禁控制字符。
func validateText(field, val string, max int, noControl bool) error {
	if utf8.RuneCountInString(val) > max {
		return fmt.Errorf("%s: 超长（上限 %d 字符）", field, max)
	}
	if noControl {
		for _, r := range val {
			if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
				return fmt.Errorf("%s: 含非法控制字符", field)
			}
		}
	}
	return nil
}

// validateURL 地址类：http/https + 长度（不做网络可达性校验）。
func validateURL(field, val string) error {
	if len(val) > MaxURLChars {
		return fmt.Errorf("%s: 超长（上限 %d 字符）", field, MaxURLChars)
	}
	low := strings.ToLower(val)
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		return fmt.Errorf("%s: 必须以 http:// 或 https:// 开头", field)
	}
	return nil
}

// validateAPIKey 密钥类：1–512 字符、无空白 / 换行（不明文回显由调用方保证）。
func validateAPIKey(field, val string) error {
	if utf8.RuneCountInString(val) > MaxKeyChars {
		return fmt.Errorf("%s: 超长（上限 %d 字符）", field, MaxKeyChars)
	}
	if strings.ContainsAny(val, " \t\r\n") {
		return fmt.Errorf("%s: 不得包含空白或换行", field)
	}
	return nil
}

// validateAbsPath 路径类：长度 + 绝对路径 + 不含 ".." 段。
func validateAbsPath(field, val string) error {
	if len(val) > MaxPathChars {
		return fmt.Errorf("%s: 超长（上限 %d 字符）", field, MaxPathChars)
	}
	if !filepath.IsAbs(val) {
		return fmt.Errorf("%s: 必须为绝对路径（%s）", field, val)
	}
	for _, seg := range strings.FieldsFunc(filepath.ToSlash(val), func(r rune) bool { return r == '/' }) {
		if seg == ".." {
			return fmt.Errorf("%s: 不得包含上级目录引用（..）", field)
		}
	}
	return nil
}

// validateProjectID 项目 id：uuid 或 `[A-Za-z0-9_-]` 标识（长度 8–64）。
func validateProjectID(val string) error {
	v := strings.TrimSpace(val)
	if v == "" {
		return nil // 空 = 首次自动生成
	}
	if len(v) < 8 || len(v) > MaxNameChars {
		return fmt.Errorf("project-id: 长度须为 8–%d", MaxNameChars)
	}
	for _, r := range v {
		if r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		return fmt.Errorf("project-id: 仅允许字母 / 数字 / - / _")
	}
	return nil
}

// ── 小工具 ─────────────────────────────────────────────

func validateIntRange(item map[string]any, i int, key string, min, max int) error {
	v, exists := item[key]
	if !exists || v == nil || isEmptyScalar(v) {
		return nil
	}
	n, ok := asInt(v)
	if !ok || n < min || n > max {
		return fmt.Errorf("llms[%d].%s: 超出范围 %d–%d", i, key, min, max)
	}
	return nil
}

// validateZeroOrIntRange：`0` = 显式未设置（放行），非零才受 [min,max] 约束。
func validateZeroOrIntRange(item map[string]any, i int, key string, min, max int) error {
	v, exists := item[key]
	if !exists || v == nil || isEmptyScalar(v) {
		return nil
	}
	n, ok := asInt(v)
	if !ok {
		return fmt.Errorf("llms[%d].%s: 必须为整数", i, key)
	}
	if n == 0 {
		return nil
	}
	if n < min || n > max {
		return fmt.Errorf("llms[%d].%s: 超出范围 %d–%d（0 = 未设置）", i, key, min, max)
	}
	return nil
}

func strOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func isEmptyScalar(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case json.Number:
		n, err := t.Int64()
		return int(n), err == nil
	case string:
		return parseIntString(t)
	}
	return 0, false
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%g", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

func parseIntString(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n := 0
	sign := 1
	i := 0
	if s[0] == '-' {
		sign, i = -1, 1
	}
	if i >= len(s) {
		return 0, false
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return sign * n, true
}

// helpers.go —— 插件复用层（框架）：所有插件都要写的"杂活"在这里收敛一次。
//
// 约定：插件业务代码只关心「工具做什么」，不要再各自实现
// 参数取值 / 信封解包 / llm.step 解析 / 默认参数合并 / ping 应答。
package pluginsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Str 把任意 JSON 值转成字符串（nil → ""；数字/布尔走 fmt）。
func Str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

// Int 把任意 JSON 值转成 int（float64 / int / int64 / json.Number / 数字字符串）。
func Int(v any) int {
	switch x := v.(type) {
	case nil:
		return 0
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return n
		}
	}
	return 0
}

// Bool 把任意 JSON 值转成 bool；无法判定时返回 def。
func Bool(v any, def bool) bool {
	switch x := v.(type) {
	case nil:
		return def
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return def
}

// Unwrap 逐层剥掉 `{ok,result}` 信封，返回最内层 result。
// 解析失败或没有 result 键时原样返回（调用方不需要区分信封层数）。
func Unwrap(raw []byte) any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	for i := 0; i < 3; i++ {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		inner, ok := m["result"]
		if !ok {
			return v
		}
		v = inner
	}
	return v
}

// UnwrapMap 是 Unwrap 的 map 便捷形态（非对象 → 空 map）。
func UnwrapMap(raw []byte) map[string]any {
	if m, ok := Unwrap(raw).(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// MergeMissing 把 defaults 中 args **缺失**的键补上（显式入参优先，永不覆盖）。
func MergeMissing(args, defaults map[string]any) {
	if args == nil || len(defaults) == 0 {
		return
	}
	for k, v := range defaults {
		if _, ok := args[k]; !ok {
			args[k] = v
		}
	}
}

// LLM 调一次宿主 LLM（plugin/v1/llm.step，无会话无工具）并返回纯文本。
// system 可为空；timeout <= 0 时不额外设超时（沿用 ctx）。
func (p *Plugin) LLM(ctx context.Context, prompt, system string, timeout time.Duration) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("llm: prompt 不能为空")
	}
	cctx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	payload := map[string]any{"prompt": prompt}
	if s := strings.TrimSpace(system); s != "" {
		payload["system"] = s
	}
	raw, err := p.Call(cctx, "plugin/v1/llm.step", payload)
	if err != nil {
		return "", err
	}
	var out struct {
		Text   string `json:"text"`
		Result struct {
			Text string `json:"text"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("llm: 应答解析失败: %w", err)
	}
	text := strings.TrimSpace(out.Text)
	if text == "" {
		text = strings.TrimSpace(out.Result.Text)
	}
	if text == "" {
		return "", fmt.Errorf("llm: 返回空文本")
	}
	return text, nil
}

// PingHandler 是标准化的 `ping` RPC（管理页/宿主自检共用）。
func PingHandler(version string) RequestFunc {
	return func(_ context.Context, _ map[string]any) (any, error) {
		return map[string]any{"ok": true, "version": version}, nil
	}
}

// StatusHandler 把「组装状态」的函数包成标准 status RPC（自动带 ok/version）。
func StatusHandler(version string, build func(context.Context) map[string]any) RequestFunc {
	return func(ctx context.Context, _ map[string]any) (any, error) {
		out := map[string]any{"ok": true, "version": version}
		if build != nil {
			for k, v := range build(ctx) {
				out[k] = v
			}
		}
		return out, nil
	}
}

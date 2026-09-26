package dsl

import (
	"encoding/json"
	"strconv"
	"strings"
)

// asMap 解包 *Rec 为 map。
func asMap(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, true
	case *Rec:
		m, ok := t.V.(map[string]any)
		return m, ok
	}
	return nil, false
}

func asList(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case *Rec:
		l, ok := t.V.([]any)
		return l, ok
	}
	return nil, false
}

// parseNumber 数字识别（含负号、小数）。
func parseNumber(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// fmtNumber 数字转文本（整数不带小数点）。
func fmtNumber(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// toJSON 把值序列化为 JSON 文本。
func toJSON(v any) (string, error) {
	b, err := json.Marshal(unwrap(v))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// unwrap 递归解 *Rec。
func unwrap(v any) any {
	if r, ok := v.(*Rec); ok {
		return unwrap(r.V)
	}
	if m, ok := v.(map[string]any); ok {
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[k] = unwrap(val)
		}
		return out
	}
	if l, ok := v.([]any); ok {
		out := make([]any, len(l))
		for i, val := range l {
			out[i] = unwrap(val)
		}
		return out
	}
	return v
}

// valueToText 值 → 文本（SET/动作输出写文件用；标量直接、复合 JSON）。
func valueToText(v any) (string, error) {
	v = unwrap(v)
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		return fmtNumber(t), nil
	default:
		return toJSON(v)
	}
}

// interpText 插值渲染：句柄→描述串；标量→文本；复合→JSON。
func interpText(v any) (string, error) {
	switch t := v.(type) {
	case *VFile, *VDB, *VTable:
		return describe(v), nil
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		return fmtNumber(t), nil
	case *Rec:
		return interpText(t.V)
	default:
		return toJSON(v)
	}
}

// deepGet 按字段路径取值（仅 map 字段；缺失返回 ok=false）。
func deepGet(v any, path []string) (any, bool) {
	cur := v
	for _, p := range path {
		m, ok := asMap(cur)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// parseJSONValue 尝试把文本解析为 JSON 值（用于动作输出 → 表记录等）。
func parseJSONValue(text string) any {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err == nil {
		return v
	}
	return strings.TrimSpace(text)
}

// 工具注册载荷与热重载 diff（P6 2026-10-19，纯逻辑，便于单测）。
package main

import (
	"encoding/json"
	"sort"

	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
)

// registerPayload 是 plugin/v1/tools.register 的载荷（字段对齐 gateway regMsg，
// timeout 用指针区分「未设置」与「显式 0/-1 = 无上限」）。
type registerPayload struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Schema         string   `json:"schema,omitempty"`
	HandlerSubject string   `json:"handler_subject"`
	Owner          string   `json:"owner"`
	Hot            bool     `json:"hot"`
	Category       string   `json:"category,omitempty"`
	Async          string   `json:"async,omitempty"`
	AsyncThreshold int      `json:"async_threshold,omitempty"`
	Timeout        *float64 `json:"timeout,omitempty"`
	RequestID      string   `json:"request_id"`
}

// buildRegisterPayload 把 ExecTool 的契约定义编译为注册载荷：
// name = 契约名（不带 self_ 前缀；暴露名由 gateway self 节点派生为 self_<name>）。
func buildRegisterPayload(et *mcpms.ExecTool) map[string]any {
	if et == nil || et.Tool == nil {
		return map[string]any{}
	}
	p := registerPayload{
		Name:           et.Tool.Name,
		Description:    et.Tool.Description,
		HandlerSubject: handlerSubject,
		Owner:          "self-tools",
		Hot:            true,
		RequestID:      "self-tools:" + et.Tool.Name,
	}
	if et.Tool.InputSchema != nil {
		if raw, err := json.Marshal(et.Tool.InputSchema); err == nil {
			p.Schema = string(raw)
		}
	}
	if et.Doc != nil {
		p.Category = et.Doc.Category
		p.Async = et.Doc.Async
		p.AsyncThreshold = et.Doc.AsyncTh
		if et.Doc.TimeoutSet {
			timeout := float64(et.Doc.Timeout) // 含 0/-1 = 无上限
			p.Timeout = &timeout
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// unregisterPayload 构造 tools.unregister 载荷。带 request_id（命令类方法）→ pluginbridge
// 同步等待注销完成，保证紧随其后的同名 register 不会撞「already registered」。
func unregisterPayload(name string) map[string]any {
	return map[string]any{
		"name":       name,
		"request_id": "self-tools-unregister:" + name,
	}
}

// payloadFingerprint 返回注册载荷的稳定指纹（encoding/json 对 map 键排序）——
// 用于判断描述/schema/meta 是否变化（变化 → 先 unregister 再 register）。
func payloadFingerprint(p map[string]any) string {
	raw, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(raw)
}

// diffTools 对比已注册集（prev）与目标集（next，name → 指纹）：
//   - removed = prev 有而 next 无（契约删除/改名）；
//   - upserted = next 有而 prev 无，或两侧指纹不同（新增/描述/schema/meta 变化）。
//
// 两个切片均按名称排序（注册顺序稳定，便于日志/测试断言）。
func diffTools(prev, next map[string]string) (removed, upserted []string) {
	for name := range prev {
		if _, ok := next[name]; !ok {
			removed = append(removed, name)
		}
	}
	for name, fp := range next {
		if old, ok := prev[name]; !ok || old != fp {
			upserted = append(upserted, name)
		}
	}
	sort.Strings(removed)
	sort.Strings(upserted)
	return removed, upserted
}

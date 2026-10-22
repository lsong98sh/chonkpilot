// 工具消息的 LLM 投影（设计 §3.8）：未完成 / 空结果按状态给明确占位，不得为空串。
package kernel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

func toolContentJSON(resultContent string) string {
	m := map[string]any{
		"call": map[string]any{"tool_call_id": "tc1", "name": "core_file_read", "arguments": map[string]any{}},
	}
	if resultContent != "" {
		m["result"] = map[string]any{"content": resultContent, "status": "completed"}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestToolLLMContentStatus(t *testing.T) {
	cases := []struct {
		name    string
		content string
		status  string
		want    string
	}{
		{"运行中占位", toolContentJSON(""), "running", "（工具执行中，结果稍后返回）"},
		{"中断占位", toolContentJSON(""), "interrupted", "（工具已中断，未获得结果；如需重试请使用「重试」）"},
		{"取消占位", toolContentJSON(""), "cancelled", "（工具已取消）"},
		{"失败无结果", toolContentJSON(""), "failed", "（工具执行失败）"},
		{"完成空结果", toolContentJSON(""), "completed", "（工具执行完成，无文本输出）"},
		{"有结果优先", toolContentJSON("ok"), "completed", "ok"},
		{"旧数据无状态", toolContentJSON(""), "", ""},
	}
	for _, c := range cases {
		if got := ToolLLMContentStatus(c.content, c.status); got != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	// MsgRowToChat 必须把行级状态透传给投影（不能返回空串）。
	rec := data.Record{
		"role": "tool", "tool_call_id": "tc1", "tool_call_status": "running",
		"content": toolContentJSON(""),
	}
	m, ok := MsgRowToChat(rec)
	if !ok || !strings.Contains(m.Content, "工具执行中") {
		t.Fatalf("MsgRowToChat 未按状态投影：%+v", m)
	}
}

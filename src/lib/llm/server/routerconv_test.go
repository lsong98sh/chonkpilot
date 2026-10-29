// 白盒：canonical（router.Message / router.ToolCall）↔ chat 自持消息（chat.Message / chat.ToolCall）
// 转换（迁自原 `chonkpilot-router/conv` 的 LR-1 白盒，落位见 routerconv.go 文件头；
// MOD-1 M2 起消息类型 = chat 自持，快照 wire 互转在 chatadapter.go）。
package server

import (
	"testing"

	"github.com/lsong98sh/chonkpilot-agent/router"
)

// TestRouterConvToolCallsEmptyToNil：空工具调用 → nil（快照侧 omitempty 口径，避免写出 `[]`）。
func TestRouterConvToolCallsEmptyToNil(t *testing.T) {
	if got := ToRouterToolCalls(nil); got != nil {
		t.Fatalf("ToRouterToolCalls(nil)=%+v want nil", got)
	}
	if got := FromRouterToolCalls(nil); got != nil {
		t.Fatalf("FromRouterToolCalls(nil)=%+v want nil", got)
	}
	back := FromRouterToolCalls([]router.ToolCall{{ID: "tc-1", Name: "f", Arguments: "{}"}})
	if len(back) != 1 || back[0].Name != "f" || back[0].Arguments != "{}" {
		t.Fatalf("回写 ToolCall=%+v want {f, {}}", back)
	}
}

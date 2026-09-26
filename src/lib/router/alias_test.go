// 落位取证（第 1 步）：根包类型是 `internal/canon` 的**别名**（`type X = canon.X`，同一类型），
// 而非新定义类型 —— 故 `router.Message` / `router.Event` / `router.Spec` 等对外签名与既有用法
// **逐字不变**（llm 侧 `routerconv.go` 零改）。
package router

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// 编译期取证：`func(router.X) X` 与 `func(canon.X) canon.X` 是同一函数类型 —— 仅当两侧是**同一
// 类型**（别名）才可互赋；若 X 是独立定义类型，则函数类型不同、**编译失败**。
var (
	_ func(canon.Message) Message             = func(v Message) Message { return v }
	_ func(canon.Part) Part                   = func(v Part) Part { return v }
	_ func(canon.ImagePart) ImagePart         = func(v ImagePart) ImagePart { return v }
	_ func(canon.ToolCall) ToolCall           = func(v ToolCall) ToolCall { return v }
	_ func(canon.ToolDef) ToolDef             = func(v ToolDef) ToolDef { return v }
	_ func(canon.Spec) Spec                   = func(v Spec) Spec { return v }
	_ func(canon.Reasoning) Reasoning         = func(v Reasoning) Reasoning { return v }
	_ func(canon.CallOptions) CallOptions     = func(v CallOptions) CallOptions { return v }
	_ func(canon.Request) Request             = func(v Request) Request { return v }
	_ func(canon.Event) Event                 = func(v Event) Event { return v }
	_ func(canon.ToolCallDelta) ToolCallDelta = func(v ToolCallDelta) ToolCallDelta { return v }
	_ func(canon.Usage) Usage                 = func(v Usage) Usage { return v }
	_ func(canon.Caps) Caps                   = func(v Caps) Caps { return v }
	_ func(canon.Error) Error                 = func(v Error) Error { return v }
	_ func(canon.Role) Role                   = func(v Role) Role { return v }
	_ func(canon.EventType) EventType         = func(v EventType) EventType { return v }
	_ func(canon.ErrorKind) ErrorKind         = func(v ErrorKind) ErrorKind { return v }
)

// TestAliasedConstants：常量是**转发**（别名）而非复制 —— 类型（编译期由下方类型化字面量取证）与
// 取值（与 LR-1/LR-5 契约字面量比对）一致。
func TestAliasedConstants(t *testing.T) {
	roles := []Role{RoleSystem, RoleUser, RoleAssistant, RoleTool}
	wantRoles := []string{"system", "user", "assistant", "tool"}
	for i := range roles {
		if string(roles[i]) != wantRoles[i] {
			t.Fatalf("Role[%d]=%q want %q", i, roles[i], wantRoles[i])
		}
	}
	if PartText != "text" || PartImage != "image" {
		t.Fatalf("Part 常量=%q/%q want text/image", PartText, PartImage)
	}
	protos := []string{ProtocolOpenAI, ProtocolResponses, ProtocolAnthropic, ProtocolEcho}
	wantProtos := []string{"openai", "responses", "anthropic", "echo"}
	for i := range protos {
		if protos[i] != wantProtos[i] {
			t.Fatalf("协议常量[%d]=%q want %q", i, protos[i], wantProtos[i])
		}
	}
	evs := []EventType{EvTextDelta, EvReasoningDelta, EvToolCallDelta, EvToolCall, EvUsage, EvDone, EvError}
	wantEvs := []string{"text_delta", "reasoning_delta", "tool_call_delta", "tool_call", "usage", "done", "error"}
	for i := range evs {
		if string(evs[i]) != wantEvs[i] {
			t.Fatalf("事件类型[%d]=%q want %q", i, evs[i], wantEvs[i])
		}
	}
	kinds := []ErrorKind{ErrorNetwork, ErrorTimeout, ErrorRateLimit, ErrorServer, ErrorAuth, ErrorProtocol, ErrorInvalid}
	wantKinds := []string{"network", "timeout", "rate_limit", "server", "auth", "protocol", "invalid"}
	for i := range kinds {
		if string(kinds[i]) != wantKinds[i] {
			t.Fatalf("错误分类[%d]=%q want %q", i, kinds[i], wantKinds[i])
		}
	}
}

// GATEWAY-2 L3 故障域补偿测试（设计 §3.3 第 1 条）：内置 mcp-server 单进程承载全部工具，
// handler 内的 panic 必须转成 isError 结果返回，**不得**冒泡到 server 主循环（否则单个工具
// 故障会拖垮同进程的其他调用）。
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestToolHandlerRecoversPanic 断言 handler panic → isError 结果（不是 error，也不崩溃）。
// 故障注入：lim 为 nil（缺省装配错误）→ limiter.acquire 空指针 panic。
func TestToolHandlerRecoversPanic(t *testing.T) {
	td := &ToolDoc{Name: "boom", Runtime: "builtin", Category: "meta"}
	h := makeToolHandler(td, &Config{}, nil)
	res, err := h(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "boom"},
	})
	if err != nil {
		t.Fatalf("panic 应转为 isError 结果而非协议 error：%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("panic 应返回 isError 结果：%+v", res)
	}
	if len(res.Content) == 0 {
		t.Fatalf("isError 结果应带内容")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(tc.Text, "panic") || !strings.Contains(tc.Text, "boom") {
		t.Fatalf("结果应说明 panic 与工具名：%+v", res.Content[0])
	}
}

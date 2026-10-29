// tools_progress_keys_test.go — `mcp-tools-progress`（GATEWAY-2 L8，2026-10-28）的
// **注册/契约级**对账（依据 50-测试体系 §1/§8：msg/payload 增改必须同步 61 文档 + 补齐测试）。
//
// 锁定三处一致（**不伪造业务链路**）：
//   - ① 生成的 msgkeys 键常量存在且取值正确（编译即证存在；逐值断言取值）；
//   - ② `61-messages.schema.json` 已收录该主题与四个字段（`instance_id` / `tool_call_id` /
//     `progress` / 可选 `total`）；
//   - ③ `61-消息一览.md`（唯一准则）已登记该主题与字段（文档 ↔ 代码同源）。
//
// 链路驱动与载荷断言见：`src/lib/gwadapter/mqadapter`（`TestProgressHandlerEmitsContractTopic`）、
// 前端 `test/contractReconcile.test.js`。
package msgkeys_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

// TestMcpToolsProgressContract 断言 `mcp-tools-progress` 键常量 / schema / 61 文档三处一致。
func TestMcpToolsProgressContract(t *testing.T) {
	// ① 键常量取值正确。
	if msgkeys.TopicMcpToolsProgress != "mcp-tools-progress" {
		t.Fatalf("TopicMcpToolsProgress=%q，want mcp-tools-progress", msgkeys.TopicMcpToolsProgress)
	}
	for _, kv := range [][2]string{
		{msgkeys.McpToolsProgressEventInstanceId, "instance_id"},
		{msgkeys.McpToolsProgressEventToolCallId, "tool_call_id"},
		{msgkeys.McpToolsProgressEventProgress, "progress"},
		{msgkeys.McpToolsProgressEventTotal, "total"},
	} {
		if kv[0] != kv[1] {
			t.Fatalf("键常量取值 %q，want %q", kv[0], kv[1])
		}
	}

	root := findRepoRoot(t)

	// ② schema：主题存在，`progress` 必填数值、`total` 可选。
	raw, err := os.ReadFile(filepath.Join(root, "docs", "spec", "60-reference", "61-messages.schema.json"))
	if err != nil {
		t.Fatalf("读契约失败: %v", err)
	}
	var doc struct {
		Topics []struct {
			Topic string `json:"topic"`
			Event map[string]struct {
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"event"`
		} `json:"topics"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析契约失败: %v", err)
	}
	var found bool
	for _, ts := range doc.Topics {
		if ts.Topic != msgkeys.TopicMcpToolsProgress {
			continue
		}
		found = true
		for _, req := range []string{
			msgkeys.McpToolsProgressEventInstanceId,
			msgkeys.McpToolsProgressEventToolCallId,
			msgkeys.McpToolsProgressEventProgress,
		} {
			f, ok := ts.Event[req]
			if !ok {
				t.Fatalf("%s.event 缺必填字段 %s", ts.Topic, req)
			}
			if !f.Required {
				t.Fatalf("%s.event.%s 应为必填", ts.Topic, req)
			}
		}
		if f := ts.Event[msgkeys.McpToolsProgressEventProgress]; f.Type != "number" {
			t.Fatalf("%s.event.progress 应为 number，got %q", ts.Topic, f.Type)
		}
		tot, ok := ts.Event[msgkeys.McpToolsProgressEventTotal]
		if !ok {
			t.Fatalf("%s.event 缺可选字段 total", ts.Topic)
		}
		if tot.Required {
			t.Fatalf("%s.event.total 应为可选（缺省 = 不显示百分比）", ts.Topic)
		}
	}
	if !found {
		t.Fatalf("61-messages.schema.json 未收录 %s", msgkeys.TopicMcpToolsProgress)
	}

	// ③ 61-消息一览（唯一准则）已登记该主题与字段。
	docRaw, err := os.ReadFile(filepath.Join(root, "docs", "spec", "60-reference", "61-消息一览.md"))
	if err != nil {
		t.Fatalf("读 61 文档失败: %v", err)
	}
	docText := string(docRaw)
	for _, want := range []string{
		msgkeys.TopicMcpToolsProgress,
		msgkeys.McpToolsProgressEventProgress,
	} {
		if !strings.Contains(docText, want) {
			t.Errorf("61-消息一览.md 未登记 %q（消息面增改必须同步 61 文档）", want)
		}
	}
}

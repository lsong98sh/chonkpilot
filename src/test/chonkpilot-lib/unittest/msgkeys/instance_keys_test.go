// instance_keys_test.go — `instance-register.session_id`（SUBCHAT-3 阶段 F-2，2026-10-16）的
// **注册/契约级**对账（依据 50-测试体系 §1/§8：msg/payload 增改必须同步 61 文档 + 补齐测试）。
//
// 锁定三处一致（**不伪造业务链路**）：
//  ① 生成的 msgkeys 键常量存在且取值正确（编译即证存在；逐值断言取值）；
//  ② `61-messages.schema.json` 已收录该可选字段（`instance-register` event）；
//  ③ `61-消息一览.md`（唯一准则）已登记该字段（文档 ↔ 代码同源）。
//
// 链路驱动与窗口级断言见：`chonkpilot-gui/unittest/bridge`（写侧上报 + 定向投递）、
// `chonkpilot-llm/unittest/...` 与 `chonkpilot-llm/server`（`InstanceForSession` /
// `askTargetInstance`）。
package msgkeys_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// TestInstanceRegisterSessionIDContract 断言 `instance-register.session_id` 键常量 / schema / 61 文档三处一致。
func TestInstanceRegisterSessionIDContract(t *testing.T) {
	// ① 键常量取值正确。
	if msgkeys.InstanceRegisterEventSessionId != "session_id" {
		t.Fatalf("InstanceRegisterEventSessionId=%q，want session_id", msgkeys.InstanceRegisterEventSessionId)
	}
	if msgkeys.TopicInstanceRegister != "instance-register" {
		t.Fatalf("TopicInstanceRegister=%q，want instance-register", msgkeys.TopicInstanceRegister)
	}

	root := findRepoRoot(t)

	// ② schema：instance-register 的 event 收录可选 `session_id`。
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
		if ts.Topic != msgkeys.TopicInstanceRegister {
			continue
		}
		f, ok := ts.Event[msgkeys.InstanceRegisterEventSessionId]
		if !ok {
			t.Fatalf("%s.event 缺字段 %s（须先登记契约）", ts.Topic, msgkeys.InstanceRegisterEventSessionId)
		}
		if f.Type != "string" || f.Required {
			t.Fatalf("%s.event.%s 应为可选 string，got type=%q required=%v",
				ts.Topic, msgkeys.InstanceRegisterEventSessionId, f.Type, f.Required)
		}
		found = true
	}
	if !found {
		t.Fatalf("61-messages.schema.json 未收录 %s", msgkeys.TopicInstanceRegister)
	}

	// ③ 61-消息一览（唯一准则）已登记该字段（文档 ↔ 代码同源）。
	docRaw, err := os.ReadFile(filepath.Join(root, "docs", "spec", "60-reference", "61-消息一览.md"))
	if err != nil {
		t.Fatalf("读 61 文档失败: %v", err)
	}
	docText := string(docRaw)
	for _, want := range []string{msgkeys.TopicInstanceRegister, msgkeys.InstanceRegisterEventSessionId} {
		if !strings.Contains(docText, want) {
			t.Errorf("61-消息一览.md 未登记 %q（消息面增改必须同步 61 文档）", want)
		}
	}
}

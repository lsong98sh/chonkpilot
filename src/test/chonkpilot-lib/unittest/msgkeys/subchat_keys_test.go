// subchat_keys_test.go — 单聊 `subchat-*` 4 条业务消息的**注册/契约级**对账（2026-10-16，SUBCHAT-2 B-1）。
//
// 依据（项目测试准则 / 50-测试体系 §1、§8）：msg / payload 的增改删必须**同步 61 文档 + 补齐测试**。
// 本用例锁定三处一致（**不伪造业务链路**）：
//  ① 生成的 msgkeys 键常量**存在且取值正确**（本文件编译即证存在；逐值断言取值）；
//  ② `61-messages.schema.json` 收录这 4 个主题，且方向 / payload / result / event 字段与
//     required 标记 = 61 文档口径（§5.2 / §10 Q1）；
//  ③ `61-消息一览.md`（**唯一准则**）已登记这 4 个主题名。
//
// 覆盖边界（务必知悉）：本批（B-1）**只做消息面 + 跨窗口 instance 投递基础设施** ——
// `subchat_open` / `subchat_finish` 工具契约与 server handler 属 **B-2**、前端 F1–F10 属**阶段 C**，
// 尚无生产者 / 消费者 → 本用例不驱动真实业务链路（驱动面登记见 chonkpilot-llm
// `contract_reconcile_test.go` 的 TestServerNotDrivenRegistry）。
package msgkeys_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// contractField 是 schema 字段声明（与 61-messages.schema.json 对齐）。
type contractField struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

// contractTopic 是 schema 主题声明（仅取本用例所需列）。
type contractTopic struct {
	Topic       string                   `json:"topic"`
	Face        string                   `json:"face"`
	Direction   string                   `json:"direction"`
	ClientTopic string                   `json:"clientTopic"`
	Payload     map[string]contractField `json:"payload"`
	Result      map[string]contractField `json:"result"`
	Event       map[string]contractField `json:"event"`
}

// subchatCase 是单聊消息的预期契约形状（与 61 文档 §5.2 / §10 Q1 逐字段对齐）。
type subchatCase struct {
	topic     string
	gotVal    string // 生成的 msgkeys 主题常量取值
	direction string
	payload   map[string]bool // 字段 → required
	result    map[string]bool
	event     map[string]bool
}

func loadContractTopics(t *testing.T) map[string]contractTopic {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), "docs", "spec", "60-reference", "61-messages.schema.json"))
	if err != nil {
		t.Fatalf("读契约失败: %v", err)
	}
	var doc struct {
		Topics []contractTopic `json:"topics"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析契约失败: %v", err)
	}
	out := make(map[string]contractTopic, len(doc.Topics))
	for _, ts := range doc.Topics {
		out[ts.Topic] = ts
	}
	return out
}

// assertSide 逐字段比对一侧声明（字段集 + required 标记；缺键 / 多键 / required 不符 = 红）。
func assertSide(t *testing.T, topic, which string, want map[string]bool, got map[string]contractField) {
	t.Helper()
	for k, req := range want {
		f, ok := got[k]
		if !ok {
			t.Errorf("%s.%s 缺字段 %q（61 已登记）", topic, which, k)
			continue
		}
		if f.Required != req {
			t.Errorf("%s.%s.%s required=%v，want %v", topic, which, k, f.Required, req)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s.%s 多出字段 %q（契约未登记 = 红）", topic, which, k)
		}
	}
}

// TestSubchatMessageContract 断言 4 条单聊消息的键常量 / schema 契约 / 61 文档三处一致。
func TestSubchatMessageContract(t *testing.T) {
	topics := loadContractTopics(t)

	cases := []subchatCase{
		{
			topic: msgkeys.TopicSubchatOpened, gotVal: "subchat-opened", direction: "server->client",
			event: map[string]bool{
				"instance_id": true, "session_id": true, "parent_session_id": true,
				"title": true, "purpose": false,
			},
		},
		{
			topic: msgkeys.TopicSubchatList, gotVal: "subchat-list", direction: "client->server",
			payload: map[string]bool{"parent_id": false},
			result:  map[string]bool{"list": true},
		},
		{
			topic: msgkeys.TopicSubchatFinish, gotVal: "subchat-finish", direction: "client->server",
			payload: map[string]bool{"session_id": true},
			result:  map[string]bool{"ok": true},
		},
		{
			topic: msgkeys.TopicSubchatState, gotVal: "subchat-state", direction: "server->client",
			event: map[string]bool{
				"session_id": true, "parent_session_id": true, "lifecycle": true, "instance_id": false,
			},
		},
	}

	if len(cases) != 4 {
		t.Fatalf("单聊消息族应为 4 条，got %d", len(cases))
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.topic, func(t *testing.T) {
			// ① 键常量取值正确（常量存在性由编译保证）。
			if tc.topic != tc.gotVal {
				t.Fatalf("msgkeys 主题常量取值=%q，want %q", tc.topic, tc.gotVal)
			}
			// ② schema 契约（方向 / face / 各侧字段与 required）。
			ts, ok := topics[tc.topic]
			if !ok {
				t.Fatalf("61-messages.schema.json 未收录 %s（须先登记契约）", tc.topic)
			}
			if ts.Face != "server" {
				t.Errorf("%s face=%q，want server", tc.topic, ts.Face)
			}
			if ts.Direction != tc.direction {
				t.Errorf("%s direction=%q，want %q", tc.topic, ts.Direction, tc.direction)
			}
			if tc.payload != nil {
				assertSide(t, tc.topic, "payload", tc.payload, ts.Payload)
			}
			if tc.result != nil {
				assertSide(t, tc.topic, "result", tc.result, ts.Result)
			}
			if tc.event != nil {
				assertSide(t, tc.topic, "event", tc.event, ts.Event)
			}
		})
	}
}

// TestSubchatGeneratedFieldKeys 断言生成字段键常量的取值（生成物与契约同源，防手改漂移）。
func TestSubchatGeneratedFieldKeys(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"SubchatOpenedEventSessionId", msgkeys.SubchatOpenedEventSessionId, "session_id"},
		{"SubchatOpenedEventParentSessionId", msgkeys.SubchatOpenedEventParentSessionId, "parent_session_id"},
		{"SubchatOpenedEventTitle", msgkeys.SubchatOpenedEventTitle, "title"},
		{"SubchatOpenedEventPurpose", msgkeys.SubchatOpenedEventPurpose, "purpose"},
		{"SubchatListPayloadParentId", msgkeys.SubchatListPayloadParentId, "parent_id"},
		{"SubchatListResultList", msgkeys.SubchatListResultList, "list"},
		{"SubchatFinishPayloadSessionId", msgkeys.SubchatFinishPayloadSessionId, "session_id"},
		{"SubchatFinishResultOk", msgkeys.SubchatFinishResultOk, "ok"},
		{"SubchatStateEventLifecycle", msgkeys.SubchatStateEventLifecycle, "lifecycle"},
		{"SubchatStateEventInstanceId", msgkeys.SubchatStateEventInstanceId, "instance_id"},
		{"FieldPurpose", msgkeys.FieldPurpose, "purpose"},
		{"FieldLifecycle", msgkeys.FieldLifecycle, "lifecycle"},
		{"FieldParentId", msgkeys.FieldParentId, "parent_id"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("msgkeys.%s=%q，want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestSubchatTopicsRegisteredInDoc 断言 61-消息一览（唯一准则）已登记 4 条消息（文档 ↔ 代码同源）。
func TestSubchatTopicsRegisteredInDoc(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), "docs", "spec", "60-reference", "61-消息一览.md"))
	if err != nil {
		t.Fatalf("读 61 文档失败: %v", err)
	}
	doc := string(raw)
	for _, topic := range []string{
		msgkeys.TopicSubchatOpened, msgkeys.TopicSubchatList,
		msgkeys.TopicSubchatFinish, msgkeys.TopicSubchatState,
	} {
		if !strings.Contains(doc, topic) {
			t.Errorf("61-消息一览.md 未登记主题 %s（消息面增改必须同步 61 文档）", topic)
		}
	}
	// 新增字典语义键亦须在文档可追溯（purpose / lifecycle 为本次新增语义）。
	for _, sem := range []string{msgkeys.FieldPurpose, msgkeys.FieldLifecycle} {
		if !strings.Contains(doc, sem) {
			t.Errorf("61-消息一览.md 未出现新增字段语义 %q", sem)
		}
	}
}

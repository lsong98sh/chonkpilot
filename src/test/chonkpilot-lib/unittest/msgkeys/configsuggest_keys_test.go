// configsuggest_keys_test.go — `config.suggest`（AICFG-1 配置页/工具原语「AI 智能填充」，
// 2026-10-16）的**注册/契约级**三处一致对账（依据 50-测试体系 §1/§8：消息面增改必须同步
// 61 文档 + 补齐测试）。
//
// 锁定三处一致（**不伪造业务链路**）：
//  ① 生成的 msgkeys 键常量**存在且取值正确**（本文件编译即证存在；逐值断言取值）；
//  ② `61-messages.schema.json` 收录 `config.suggest`，且 face / direction / payload / result
//     字段与 required 标记 = 61 文档口径；
//  ③ `61-消息一览.md`（**唯一准则**）已登记该主题（文档 ↔ 代码同源）。
//
// 运行时行为（三 kind 白名单 / 禁填丢弃 / 解析失败 / provider 链路 / 超时）见
// `src/lib/llm/server/configsuggest_test.go`（mq 驱动 + 监听断言）；
// browser 上行白名单放行断言见 `src/lib/llm/httpapi/httpapi_test.go`（TestDottedWhitelistConfigSuggest）。
package msgkeys_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// TestConfigSuggestMessageContract 断言键常量 / schema 契约两处一致（点分直通主题 = 相对主题同名）。
func TestConfigSuggestMessageContract(t *testing.T) {
	if msgkeys.TopicConfigSuggest != "config.suggest" {
		t.Fatalf("TopicConfigSuggest=%q，want config.suggest", msgkeys.TopicConfigSuggest)
	}

	topics := loadContractTopics(t)
	ts, ok := topics[msgkeys.TopicConfigSuggest]
	if !ok {
		t.Fatalf("61-messages.schema.json 未收录 %s（消息面增改必须同步契约）", msgkeys.TopicConfigSuggest)
	}
	if ts.Face != "gui" {
		t.Errorf("%s face=%q，want gui", msgkeys.TopicConfigSuggest, ts.Face)
	}
	if ts.Direction != "client->server" {
		t.Errorf("%s direction=%q，want client->server", msgkeys.TopicConfigSuggest, ts.Direction)
	}
	assertSide(t, msgkeys.TopicConfigSuggest, "payload", map[string]bool{
		"instance_id": true, "kind": true, "text": true, "current": false,
	}, ts.Payload)
	assertSide(t, msgkeys.TopicConfigSuggest, "result", map[string]bool{
		"ok": true, "fields": false, "notes": false, "error": false,
	}, ts.Result)
}

// TestConfigSuggestGeneratedFieldKeys 断言生成字段键常量的取值（生成物与契约同源，防手改漂移）。
func TestConfigSuggestGeneratedFieldKeys(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"ConfigSuggestPayloadInstanceId", msgkeys.ConfigSuggestPayloadInstanceId, "instance_id"},
		{"ConfigSuggestPayloadKind", msgkeys.ConfigSuggestPayloadKind, "kind"},
		{"ConfigSuggestPayloadText", msgkeys.ConfigSuggestPayloadText, "text"},
		{"ConfigSuggestPayloadCurrent", msgkeys.ConfigSuggestPayloadCurrent, "current"},
		{"ConfigSuggestResultOk", msgkeys.ConfigSuggestResultOk, "ok"},
		{"ConfigSuggestResultFields", msgkeys.ConfigSuggestResultFields, "fields"},
		{"ConfigSuggestResultNotes", msgkeys.ConfigSuggestResultNotes, "notes"},
		{"ConfigSuggestResultError", msgkeys.ConfigSuggestResultError, "error"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("msgkeys.%s=%q，want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestConfigSuggestTopicsRegisteredInDoc 断言 61-消息一览（唯一准则）已登记主题（文档 ↔ 代码同源）。
func TestConfigSuggestTopicsRegisteredInDoc(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), "docs", "spec", "60-reference", "61-消息一览.md"))
	if err != nil {
		t.Fatalf("读 61 文档失败: %v", err)
	}
	if doc := string(raw); !strings.Contains(doc, msgkeys.TopicConfigSuggest) {
		t.Errorf("61-消息一览.md 未登记主题 %s（消息面增改必须同步 61 文档）", msgkeys.TopicConfigSuggest)
	}
}

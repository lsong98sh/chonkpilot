// chat 工具端口适配器（MOD-1 M5b-②）契约测试。
package server

import (
	"context"
	"encoding/json"
	"testing"
)

// TestChatToolCatalogTools：ToolDef → chat.ToolSpec（InputSchema 原文；Servers 恒空）。
func TestChatToolCatalogTools(t *testing.T) {
	cat := newChatToolCatalog(nil, func(instanceID string) []ToolDef {
		if instanceID != "ins-test" {
			t.Fatalf("instance 透传错：%q", instanceID)
		}
		return []ToolDef{
			{Name: "core_file_read", Description: "read", Parameters: map[string]any{"type": "object"}, Meta: map[string]any{"async": "never"}},
			{Name: "no_schema"},
		}
	})
	tools, err := cat.Tools(context.Background(), "ins-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "core_file_read" || tools[1].Name != "no_schema" {
		t.Fatalf("工具列表错：%+v", tools)
	}
	var schema map[string]any
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil || schema["type"] != "object" {
		t.Fatalf("InputSchema 错：%s %v", tools[0].InputSchema, err)
	}
	if tools[1].InputSchema != nil {
		t.Fatalf("无参数工具不应产出 schema：%s", tools[1].InputSchema)
	}
	if tools[0].Meta["async"] != "never" {
		t.Fatalf("_meta 未透传：%+v", tools[0].Meta)
	}
	if servers, err := cat.Servers(context.Background(), "ins-test"); err != nil || servers != nil {
		t.Fatalf("Servers 应恒空：%+v %v", servers, err)
	}
}

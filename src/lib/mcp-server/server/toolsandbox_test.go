// tool_sandbox（agentbox 沙箱开关）配置解析 / 装配的**规格期望值**测试。
//
// 期望值来源：docs/spec/10-architecture/14-安全域-agentbox.md §0.6（`{"executors":{...},
// "mcps":{...}}` 解析、旧形状读取时迁移）、
// docs/spec/10-architecture/18-工具异步超时与取消.md §L7（沙箱下沉 gateway，
// 本模块只保留配置字段与解析）、docs/spec/20-modules/25-mcp-server.md §0④。
//
// 断言口径：新旧形状 → **精确 map**；非法 JSON / 非 bool 值 → 拒绝（零配置 + 精确错误类型），
// 装配层据此**保留原值**；同值（含 key 顺序 / 空白差异）→ 不触发变化。
package server

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestParseToolSandboxShapes 覆盖新旧两种形状的精确解析结果。
func TestParseToolSandboxShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want ToolSandboxConfig
	}{
		{
			name: "新形状完整",
			raw:  `{"executors":{"core.exe":true,"desktop.exe":false},"mcps":{"github":true,"fs":false}}`,
			want: ToolSandboxConfig{
				Executors: map[string]bool{"core.exe": true, "desktop.exe": false},
				MCPs:      map[string]bool{"github": true, "fs": false},
			},
		},
		{
			name: "首尾空白不影响",
			raw:  " \t\n{\"executors\":{\"x.exe\":true}}\r\n ",
			want: ToolSandboxConfig{Executors: map[string]bool{"x.exe": true}},
		},
		{
			name: "空串 → 空配置",
			raw:  "",
			want: ToolSandboxConfig{},
		},
		{
			name: "纯空白 → 空配置",
			raw:  " \t\n",
			want: ToolSandboxConfig{},
		},
		{
			name: "仅 executors",
			raw:  `{"executors":{"x.exe":true}}`,
			want: ToolSandboxConfig{Executors: map[string]bool{"x.exe": true}},
		},
		{
			name: "仅 mcps（显式 false 保留）",
			raw:  `{"mcps":{"mcp-a":false}}`,
			want: ToolSandboxConfig{MCPs: map[string]bool{"mcp-a": false}},
		},
		{
			name: "子表 null → nil（未配置）",
			raw:  `{"executors":null,"mcps":null}`,
			want: ToolSandboxConfig{},
		},
		{
			name: "空子表保留为非 nil 空 map",
			raw:  `{"executors":{},"mcps":{}}`,
			want: ToolSandboxConfig{Executors: map[string]bool{}, MCPs: map[string]bool{}},
		},
		{
			name: "未知键忽略",
			raw:  `{"executors":{"x.exe":true},"future":1}`,
			want: ToolSandboxConfig{Executors: map[string]bool{"x.exe": true}},
		},
		{
			name: "新旧形状并存 → 新形状优先",
			raw:  `{"core":true,"executors":{"x.exe":true}}`,
			want: ToolSandboxConfig{Executors: map[string]bool{"x.exe": true}},
		},
		{
			// 2026-10-25（PLUGIN-SPLIT-1）后五迁移表已空：旧类别键读取成功但不产生任何单位键。
			name: "旧形状（类别键）迁移表已空 → 无键",
			raw:  `{"core":true,"desktop":true,"browser":false}`,
			want: ToolSandboxConfig{Executors: map[string]bool{}},
		},
		{
			name: "旧形状未知键同样被忽略",
			raw:  `{"foo":true}`,
			want: ToolSandboxConfig{Executors: map[string]bool{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseToolSandbox(tc.raw)
			if err != nil {
				t.Fatalf("ParseToolSandbox(%q) error = %v", tc.raw, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseToolSandbox(%q) = %#v\nwant = %#v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestParseToolSandboxRejectsInvalid 覆盖非法 JSON / 非 bool 值：拒绝（零配置）+ 精确错误类型。
func TestParseToolSandboxRejectsInvalid(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantErr  string // 错误文本必须包含（换行/位置细节不写死）
		wantType func(error) bool
	}{
		{
			name:    "语法错误（未闭合对象）",
			raw:     `{`,
			wantErr: "tool_sandbox: JSON 解析失败",
			wantType: func(err error) bool {
				var se *json.SyntaxError
				return errors.As(err, &se)
			},
		},
		{
			name:    "顶层是数组而非对象",
			raw:     `[true]`,
			wantErr: "tool_sandbox: JSON 解析失败",
			wantType: func(err error) bool {
				var ute *json.UnmarshalTypeError
				return errors.As(err, &ute)
			},
		},
		{
			name:    "顶层是字符串",
			raw:     `"executors"`,
			wantErr: "tool_sandbox: JSON 解析失败",
			wantType: func(err error) bool {
				var ute *json.UnmarshalTypeError
				return errors.As(err, &ute)
			},
		},
		{
			name:    "executors 值非 bool",
			raw:     `{"executors":{"x.exe":"yes"}}`,
			wantErr: "cannot unmarshal",
			wantType: func(err error) bool {
				var ute *json.UnmarshalTypeError
				return errors.As(err, &ute) && ute.Type.Kind().String() == "bool"
			},
		},
		{
			name:    "mcps 值非 bool",
			raw:     `{"mcps":{"mcp-a":1}}`,
			wantErr: "cannot unmarshal",
			wantType: func(err error) bool {
				var ute *json.UnmarshalTypeError
				return errors.As(err, &ute) && ute.Type.Kind().String() == "bool"
			},
		},
		{
			name:    "旧形状值非 bool",
			raw:     `{"core":"yes"}`,
			wantErr: "cannot unmarshal",
			wantType: func(err error) bool {
				var ute *json.UnmarshalTypeError
				return errors.As(err, &ute) && ute.Type.Kind().String() == "bool"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseToolSandbox(tc.raw)
			if err == nil {
				t.Fatalf("ParseToolSandbox(%q) 应报错，got %#v", tc.raw, got)
			}
			if !reflect.DeepEqual(got, ToolSandboxConfig{}) {
				t.Fatalf("解析失败必须返回零配置，got %#v", got)
			}
			if tc.wantErr != "" {
				if msg := err.Error(); !strings.Contains(msg, tc.wantErr) {
					t.Fatalf("错误文本 = %q, want 含 %q", msg, tc.wantErr)
				}
			}
			if tc.wantType != nil && !tc.wantType(err) {
				t.Fatalf("错误类型不符：%T %v", err, err)
			}
		})
	}
}

// TestApplyToolSandboxKeepsPreviousOnInvalid 模拟装配层 applyToolSandbox 的失败语义
// （src/lib/llm/server/execconfig.go）：解析失败 → 记日志并**保留现值**，不误清空。
func TestApplyToolSandboxKeepsPreviousOnInvalid(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.SetToolSandbox(map[string]bool{"old.exe": true}, map[string]bool{"old-mcp": true}) {
		t.Fatal("首次设置应触发变化")
	}
	before := ToolSandboxConfig{
		Executors: map[string]bool{"old.exe": true},
		MCPs:      map[string]bool{"old-mcp": true},
	}
	if !reflect.DeepEqual(cfg.ToolSandbox, before) {
		t.Fatalf("设置后 ToolSandbox = %#v", cfg.ToolSandbox)
	}

	parsed, err := ParseToolSandbox(`{"executors":{"x.exe":"yes"}}`)
	if err == nil {
		t.Fatal("非 bool 值应解析失败")
	}
	// applyToolSandbox：err != nil → 直接 return（不调用 SetToolSandbox、保留现值）。
	if !reflect.DeepEqual(cfg.ToolSandbox, before) {
		t.Fatalf("解析失败不应改写现值，got %#v, want %#v", cfg.ToolSandbox, before)
	}

	// 空串 = 显式清空（装配层口径：键缺失/空串 → 全部不隔离）。
	parsed, err = ParseToolSandbox("")
	if err != nil {
		t.Fatalf("空串不应报错：%v", err)
	}
	changed := cfg.SetToolSandbox(parsed.Executors, parsed.MCPs)
	if !changed {
		t.Fatal("空串应清空现值（变化 = true）")
	}
	if !reflect.DeepEqual(cfg.ToolSandbox, ToolSandboxConfig{}) {
		t.Fatalf("清空后 ToolSandbox = %#v", cfg.ToolSandbox)
	}
}

// TestSetToolSandboxChangeDetection 覆盖归一化（去空白 / 丢空键 / 显式 false 保留）与
// 「同值不触发变化」。
func TestSetToolSandboxChangeDetection(t *testing.T) {
	cases := []struct {
		name  string
		steps []struct {
			executors map[string]bool
			mcps      map[string]bool
			want      bool
			state     ToolSandboxConfig
		}
	}{
		{
			name: "同值（新 map 实例 / key 顺序 / 空白）不触发变化",
			steps: []struct {
				executors map[string]bool
				mcps      map[string]bool
				want      bool
				state     ToolSandboxConfig
			}{
				{map[string]bool{"a.exe": true}, map[string]bool{"m": true}, true,
					ToolSandboxConfig{Executors: map[string]bool{"a.exe": true}, MCPs: map[string]bool{"m": true}}},
				{map[string]bool{" a.exe ": true}, map[string]bool{"m": true}, false,
					ToolSandboxConfig{Executors: map[string]bool{"a.exe": true}, MCPs: map[string]bool{"m": true}}},
			},
		},
		{
			name: "显式 false 保留且参与比较",
			steps: []struct {
				executors map[string]bool
				mcps      map[string]bool
				want      bool
				state     ToolSandboxConfig
			}{
				{map[string]bool{"b.exe": false}, nil, true,
					ToolSandboxConfig{Executors: map[string]bool{"b.exe": false}}},
				{map[string]bool{"b.exe": false}, nil, false,
					ToolSandboxConfig{Executors: map[string]bool{"b.exe": false}}},
			},
		},
		{
			name: "空键丢弃（等价未配置 → 不触发变化）",
			steps: []struct {
				executors map[string]bool
				mcps      map[string]bool
				want      bool
				state     ToolSandboxConfig
			}{
				{map[string]bool{" ": true, "": false}, nil, false, ToolSandboxConfig{}},
				{map[string]bool{" ": true, "x.exe": true}, nil, true,
					ToolSandboxConfig{Executors: map[string]bool{"x.exe": true}}},
			},
		},
		{
			name: "两张子表互不串",
			steps: []struct {
				executors map[string]bool
				mcps      map[string]bool
				want      bool
				state     ToolSandboxConfig
			}{
				{map[string]bool{"x": true}, nil, true,
					ToolSandboxConfig{Executors: map[string]bool{"x": true}}},
				{nil, map[string]bool{"x": true}, true,
					ToolSandboxConfig{MCPs: map[string]bool{"x": true}}},
			},
		},
		{
			name: "nil 与空 map 等价",
			steps: []struct {
				executors map[string]bool
				mcps      map[string]bool
				want      bool
				state     ToolSandboxConfig
			}{
				{nil, nil, false, ToolSandboxConfig{}},
				{map[string]bool{}, map[string]bool{}, false, ToolSandboxConfig{}},
			},
		},
		{
			name: "清空（nil/nil）触发变化",
			steps: []struct {
				executors map[string]bool
				mcps      map[string]bool
				want      bool
				state     ToolSandboxConfig
			}{
				{map[string]bool{"x": true}, map[string]bool{"m": true}, true,
					ToolSandboxConfig{Executors: map[string]bool{"x": true}, MCPs: map[string]bool{"m": true}}},
				{map[string]bool{}, map[string]bool{}, true, ToolSandboxConfig{}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			for i, step := range tc.steps {
				got := cfg.SetToolSandbox(step.executors, step.mcps)
				if got != step.want {
					t.Fatalf("第 %d 步返回 %v, want %v", i, got, step.want)
				}
				if !reflect.DeepEqual(cfg.ToolSandbox, step.state) {
					t.Fatalf("第 %d 步 ToolSandbox = %#v\nwant = %#v", i, cfg.ToolSandbox, step.state)
				}
			}
		})
	}
}

// TestSameToolSandbox 覆盖等价比较的边界（nil/空 map、子表长度、逐键值）。
func TestSameToolSandbox(t *testing.T) {
	cases := []struct {
		name string
		a, b ToolSandboxConfig
		want bool
	}{
		{"零值相等", ToolSandboxConfig{}, ToolSandboxConfig{}, true},
		{"nil 与空 map 等价", ToolSandboxConfig{}, ToolSandboxConfig{
			Executors: map[string]bool{}, MCPs: map[string]bool{}}, true},
		{"executors 不同", ToolSandboxConfig{Executors: map[string]bool{"a": true}},
			ToolSandboxConfig{Executors: map[string]bool{"b": true}}, false},
		{"mcps 不同", ToolSandboxConfig{MCPs: map[string]bool{"m": true}},
			ToolSandboxConfig{MCPs: map[string]bool{"m": false}}, false},
		{"子表长度不同", ToolSandboxConfig{Executors: map[string]bool{"a": true, "b": true}},
			ToolSandboxConfig{Executors: map[string]bool{"a": true}}, false},
		{"同键同值（顺序无关）", ToolSandboxConfig{
			Executors: map[string]bool{"a": true, "b": false}, MCPs: map[string]bool{"m": true}},
			ToolSandboxConfig{
				MCPs:      map[string]bool{"m": true},
				Executors: map[string]bool{"b": false, "a": true}}, true},
		{"数值不同（true vs false）", ToolSandboxConfig{Executors: map[string]bool{"a": true}},
			ToolSandboxConfig{Executors: map[string]bool{"a": false}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameToolSandbox(tc.a, tc.b); got != tc.want {
				t.Fatalf("sameToolSandbox = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestToolSandboxParseSetRoundTrip：同一语义的两种 JSON 文本 → 解析后 Set 视为同值。
func TestToolSandboxParseSetRoundTrip(t *testing.T) {
	c1, err := ParseToolSandbox(`{"executors":{"a.exe":true,"b.exe":false},"mcps":{"m":true}}`)
	if err != nil {
		t.Fatalf("ParseToolSandbox: %v", err)
	}
	c2, err := ParseToolSandbox(`{"mcps":{"m":true},"executors":{"b.exe":false,"a.exe":true}}`)
	if err != nil {
		t.Fatalf("ParseToolSandbox: %v", err)
	}
	if !sameToolSandbox(c1, c2) {
		t.Fatalf("同语义文本应等价：%#v vs %#v", c1, c2)
	}
	cfg := DefaultConfig()
	if !cfg.SetToolSandbox(c1.Executors, c1.MCPs) {
		t.Fatal("首次设置应变化")
	}
	if cfg.SetToolSandbox(c2.Executors, c2.MCPs) {
		t.Fatal("同值（key 顺序不同）不应触发变化")
	}
}

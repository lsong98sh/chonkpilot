// 工具链占位符 {{toolchain.<key>}} 白盒：
//   - 已知 key 有配置 → 替换为配置路径；
//   - 已知 key 未配置（空）→ 替换为空串；
//   - 未知 key（{{toolchain.unknown}}）→ 原样保留；
//   - 不影响其它占位符（{{arg}} / {{env.X}}）；
//   - prompts/get 渲染（makePromptHandler）先替换 {{arg}} 再替换 {{toolchain.<key>}}。
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestReplaceToolchain ：替换语义（有值 / 未配置空串 / 未知 key 原样 / 其它占位符不动）。
func TestReplaceToolchain(t *testing.T) {
	vars := map[string]string{"java": `C:\jdk\bin\java.exe`}
	cases := []struct {
		name   string
		in     string
		want   []string // 必须包含
		absent []string // 必须不包含
	}{
		{
			name:   "已知 key 有配置",
			in:     "编译器：{{toolchain.java}}",
			want:   []string{`编译器：C:\jdk\bin\java.exe`},
			absent: []string{"{{toolchain.java}}"},
		},
		{
			name:   "已知 key 未配置→空串",
			in:     "go=[{{toolchain.go}}]",
			want:   []string{"go=[]"},
			absent: []string{"{{toolchain.go}}"},
		},
		{
			name:   "未知 key 原样保留",
			in:     "x={{toolchain.unknown}}",
			want:   []string{"x={{toolchain.unknown}}"},
			absent: nil,
		},
		{
			name:   "不影响 {{arg}}/{{env.X}}",
			in:     "{{arg}} / {{env.CHONKPILOT_WORKDIR}} / {{toolchain.java}}",
			want:   []string{"{{arg}}", "{{env.CHONKPILOT_WORKDIR}}", `C:\jdk\bin\java.exe`},
			absent: nil,
		},
		{
			name:   "无占位符原样返回",
			in:     "纯文本",
			want:   []string{"纯文本"},
			absent: nil,
		},
	}
	for _, c := range cases {
		got := ReplaceToolchain(c.in, vars)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Fatalf("%s：%q 应含 %q", c.name, got, w)
			}
		}
		for _, a := range c.absent {
			if strings.Contains(got, a) {
				t.Fatalf("%s：%q 不应含 %q", c.name, got, a)
			}
		}
	}
}

// TestReplaceToolchainNilVars：取值表缺省（未配置任何工具链）→ 已知 key 替换为空串、未知 key 保留。
func TestReplaceToolchainNilVars(t *testing.T) {
	got := ReplaceToolchain("a{{toolchain.python}}b{{toolchain.nope}}c", nil)
	if got != "ab{{toolchain.nope}}c" {
		t.Fatalf("nil vars 替换结果 = %q，want %q", got, "ab{{toolchain.nope}}c")
	}
}

// TestMakePromptHandlerToolchain：prompts/get 渲染 —— {{arg}} 实参语义不变（既有行为），
// 且 {{toolchain.<key>}} 按 Config.Toolchain 替换。
func TestMakePromptHandlerToolchain(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SetToolchain(map[string]string{"python": `C:\Python\python.exe`})
	h := makePromptHandler(&PromptDoc{Name: "p", Body: "参数={{arg}}；解释器={{toolchain.python}}；未知={{toolchain.unknown}}"}, cfg)

	res, err := h(context.Background(), &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{Name: "p", Arguments: map[string]string{"arg": "V"}},
	})
	if err != nil {
		t.Fatalf("makePromptHandler: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("messages = %d，want 1", len(res.Messages))
	}
	tc, ok := res.Messages[0].Content.(*mcp.TextContent)
	if !ok {
		t.Fatalf("content 类型 = %T，want *mcp.TextContent", res.Messages[0].Content)
	}
	want := `参数=V；解释器=C:\Python\python.exe；未知={{toolchain.unknown}}`
	if tc.Text != want {
		t.Fatalf("渲染文本 = %q，want %q", tc.Text, want)
	}
}

// TestToolchainVarsCopyIsolation：toolchainVars 返回副本（外部改动不污染 Config）。
func TestToolchainVarsCopyIsolation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SetToolchain(map[string]string{"node": "n1"})
	got := cfg.toolchainVars()
	got["node"] = "n2"
	if again := cfg.toolchainVars(); again["node"] != "n1" {
		t.Fatalf("取值表应返回副本，got %q", again["node"])
	}
}

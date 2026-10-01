// prompt 占位符 {{toolchain.<key>}} 白盒（chonkpilot-server 侧四个落点）：
//   - 系统提示词三层拼接后的整段（newTurnCtx 的注入口径，25 §3；replaceToolchain 对合成文本生效）；
//   - 记忆库带出指引（memoryGuide）；
//   - 工具契约描述（toolsForLLM）；
//   - 无上下文单轮 LLM（onLLMSimple，如压缩摘要 system 提示词）。
//
// 断言口径：有配置 → 文本含配置路径；未配置（空）→ 替换为空串；未知 key → 原样保留；
// {{env.X}} 等其它占位符不受影响。取值经既有 data-user-config-load（不新增消息面）。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// toolchainTestJavaPath 测试用 java 路径（usr 配置 javaPath）。
const toolchainTestJavaPath = `C:\jdk\bin\java.exe`

// saveTestUserConfig 写 usr 配置（data-user-config-save；persist 应答）。
func saveTestUserConfig(t *testing.T, s *Server, data map[string]any) {
	t.Helper()
	res := dataCall(t, s, "data-user-config-save", map[string]any{
		"instance_id": "ins-test", "data": data,
	})
	if ok, _ := dataResult(t, res)["ok"].(bool); !ok {
		t.Fatalf("save user-config failed: %+v", res)
	}
}

// TestToolchainPlaceholderScenarioPrompt：系统提示词**场景层**（scenario.description）的占位符替换
// （有值 → 路径；未配置 → 空串；未知 key → 原样保留）。25 §3/T3：系统提示词改由 llm server 按
// 三层拼（全局/场景/agent），占位符对**合成后的整段**替换 —— 此处以场景层为代表验证。
func TestToolchainPlaceholderScenarioPrompt(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	saveTestUserConfig(t, s, map[string]any{"javaPath": toolchainTestJavaPath})

	res := dataCall(t, s, "data-scenario-save", map[string]any{
		"instance_id": "ins-test",
		"data": map[string]any{
			"id": "tc-scenario", "level": "project",
			"description": "java={{toolchain.java}} go={{toolchain.go}} unknown={{toolchain.unknown}}",
			"agents":      []any{map[string]any{"name": "main", "isMain": true, "prompt": "主"}},
		},
	})
	if ok, _ := dataResult(t, res)["ok"].(bool); !ok {
		t.Fatalf("save scenario failed: %+v", res)
	}

	desc, _, agents := s.loadScenario("ins-test", "tc-scenario")
	got := s.replaceToolchain("ins-test", scenarioLayer("tc-scenario", desc, agents))
	if !strings.Contains(got, "java="+toolchainTestJavaPath) {
		t.Fatalf("场景层未替换 java 路径: %q", got)
	}
	if strings.Contains(got, "{{toolchain.java}}") {
		t.Fatalf("场景层残留 java 占位符: %q", got)
	}
	if !strings.Contains(got, "go= ") { // goPath 未配置 → 替换为空串
		t.Fatalf("未配置工具链应替换为空串: %q", got)
	}
	if !strings.Contains(got, "{{toolchain.unknown}}") {
		t.Fatalf("未知 key 应原样保留: %q", got)
	}
}

// TestToolchainPlaceholderMemoryGuide：记忆带出指引替换（类别清单拼入指引正文，
// 类别名含占位符即被替换）；固定文本里的 {{env.CHONKPILOT_WORKDIR}} 不受影响。
func TestToolchainPlaceholderMemoryGuide(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	enableTestMemory(t, s) // 记忆指引受门控（默认关闭）→ 取指引前须启用
	saveTestUserConfig(t, s, map[string]any{"javaPath": toolchainTestJavaPath})

	res := dataCall(t, s, "data-memory-save", map[string]any{
		"req_id": "tc-mg", "instance_id": "ins-test",
		"data": map[string]any{"category": "{{toolchain.java}}", "content": "占位符类别"},
	})
	if ok, _ := dataResult(t, res)["ok"].(bool); !ok {
		t.Fatalf("save memory category failed: %+v", res)
	}

	guide := s.memoryGuide("ins-test", testWorkDir)
	if !strings.Contains(guide, toolchainTestJavaPath) {
		t.Fatalf("记忆指引未替换 java 路径: %q", guide)
	}
	if strings.Contains(guide, "{{toolchain.java}}") {
		t.Fatalf("记忆指引残留 java 占位符: %q", guide)
	}
	if !strings.Contains(guide, "{{env.CHONKPILOT_WORKDIR}}") {
		t.Fatalf("记忆指引不应影响 {{env.X}} 占位符: %q", guide)
	}
}

// writeToolContractDesc 在 <root>/tools 写一个 hot=true 且描述含指定文本的 *.tool.md 契约。
func writeToolContractDesc(t *testing.T, root, name, desc string) {
	t.Helper()
	dir := filepath.Join(root, "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := "# " + name + "\n\n[meta]\nhot=true\n\n[description]\n" + desc +
		"\n\n[parameters]\n{\"type\":\"object\",\"properties\":{\"x\":{\"type\":\"string\"}}}\n"
	if err := os.WriteFile(filepath.Join(dir, name+".tool.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write contract: %v", err)
	}
}

// TestToolchainPlaceholderToolDescription：发往 LLM 的工具契约描述替换
// （loadExecConfig → Config.SetToolchain / toolsForLLM 替换 description）。
func TestToolchainPlaceholderToolDescription(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)
	saveTestUserConfig(t, s, map[string]any{"javaPath": toolchainTestJavaPath})

	writeToolContractDesc(t, persist.CapProjectRoot(testWorkDir), "tc_tool",
		"用 {{toolchain.java}} 运行；未知 {{toolchain.unknown}} 保留")

	// 实例注册 → 接入项目级 capability 根 + loadExecConfig 注入 usr 工具链取值。
	s.onInstanceRegister("instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest", "work_dir": testWorkDir,
	}))

	name := "ins-test-project_tc_tool"
	if _, ok := waitTool(t, s, name, true, 3*time.Second); !ok {
		t.Fatalf("工具 %s 未接入工具面", name)
	}
	var desc string
	for _, d := range s.toolsForLLM("ins-test") {
		if d.Name == name {
			desc = d.Description
		}
	}
	if desc == "" {
		t.Fatalf("LLM 工具面缺 %s", name)
	}
	if !strings.Contains(desc, toolchainTestJavaPath) {
		t.Fatalf("工具描述未替换 java 路径: %q", desc)
	}
	if strings.Contains(desc, "{{toolchain.java}}") {
		t.Fatalf("工具描述残留 java 占位符: %q", desc)
	}
	if !strings.Contains(desc, "{{toolchain.unknown}}") {
		t.Fatalf("未知 key 应原样保留: %q", desc)
	}
}

// TestToolchainPlaceholderLLMSimple：llm-simple 的 prompt/system 文本替换
// （覆盖 compress 摘要 system 提示词一类无上下文单轮调用）。
func TestToolchainPlaceholderLLMSimple(t *testing.T) {
	var mu sync.Mutex
	var got []ChatMsg
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = body.Messages
		mu.Unlock()
		llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, ""), sseChunk(map[string]any{}, "stop")})
	}))
	defer srv.Close()

	s := newTestServer(t, srv)
	saveTestUserConfig(t, s, map[string]any{"javaPath": toolchainTestJavaPath})

	v := &mq.Value{Payload: jb(map[string]any{
		"instance_id": "ins-test",
		"system":      "system={{toolchain.java}}",
		"prompt":      "prompt={{toolchain.java}} unknown={{toolchain.unknown}}",
	})}
	if err := s.onLLMSimple(context.Background(), "llm-simple", v); err != nil {
		t.Fatalf("onLLMSimple: %v", err)
	}
	if v.Result == nil {
		t.Fatal("llm-simple 无应答结果")
	}

	mu.Lock()
	msgs := append([]ChatMsg{}, got...)
	mu.Unlock()
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[1].Role != "user" {
		t.Fatalf("请求消息 = %+v，want [system user]", msgs)
	}
	if !strings.Contains(msgs[0].Content, toolchainTestJavaPath) || strings.Contains(msgs[0].Content, "{{toolchain.java}}") {
		t.Fatalf("system 未替换: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[1].Content, toolchainTestJavaPath) || !strings.Contains(msgs[1].Content, "{{toolchain.unknown}}") {
		t.Fatalf("prompt 未按预期替换: %q", msgs[1].Content)
	}
}

// TestToolchainPlaceholderUnconfigured：完全未配置 usr 工具链（persist 系统默认 = 空串）
// → 已知 key 替换为空串、未知 key 原样保留。
func TestToolchainPlaceholderUnconfigured(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	got := s.replaceToolchain("ins-test", "a{{toolchain.rust}}b{{toolchain.nope}}c")
	if got != "ab{{toolchain.nope}}c" {
		t.Fatalf("未配置替换结果 = %q，want %q", got, "ab{{toolchain.nope}}c")
	}
}

// Gateway 纯化 CI 断言（GATEWAY-2 L5，2026-10-21）：gateway 只保留
// Contracts / Policy / Dispatcher / TaskLayer，不再持有任何工具实现或能力源。
//
// 6 条检查：
//  1. 无能力源字段（原 Params 上的 server/config 字段）；
//  2. 无契约扫描器（原 Params 注入项）；
//  3. 包依赖图不含仓内 mcp-server 模块；
//  4. 无进程内 MCP 传输（原内存传输对）；
//  5. 无资产注册表 / 内部 LLM 选工具（原 meta_tools 辅助）；
//  6. 包内不自建 MCP server（无 SDK server 构造调用）。
//
// 注意：本文件不得出现上述关键字字面量（否则自命中）——一律用字符串拼接构造。
package mcpgateway

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// purityKeywords 是「包内 .go 源不得出现」的字面量集合（key = 说明，value = 拼接构造的关键字）。
func purityKeywords() map[string]string {
	return map[string]string{
		"能力源字段": "MCP" + "Server",
		"契约扫描":  "Contract" + "Scanner",
		"内在传输":  "InMemory" + "Transport",
		"资产注册表": "assetsBy" + "Kind",
		"内部选工具": "llmTool" + "Pick",
		"自建服务":  "mcp.New" + "Server(",
	}
}

// packageGoFiles 返回本包**生产源**（非 _test.go）文件内容。
// 测试替身可自建下游 stdio server（模拟被 spawn 的第三方 MCP），不属于 gateway 能力源。
func packageGoFiles(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

// TestPurityNoCapabilitySource 覆盖检查 1/2/4/5/6：包内生产源不得出现
// 能力源字段、契约扫描、进程内传输、资产注册表/内部选工具、自建 MCP server。
func TestPurityNoCapabilitySource(t *testing.T) {
	files := packageGoFiles(t)
	for desc, kw := range purityKeywords() {
		for name, content := range files {
			if strings.Contains(content, kw) {
				t.Errorf("gateway 包文件 %s 出现 %q（%s）；L5 后 gateway 不得持有工具实现或能力源", name, kw, desc)
			}
		}
	}
}

// TestPurityNoCapabilityServerDependency 覆盖检查 3：包依赖图不得包含仓内 mcp-server 模块。
func TestPurityNoCapabilityServerDependency(t *testing.T) {
	banned := "chonkpilot-mcp" + "-server"
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(dep, banned) {
			t.Fatalf("gateway 包依赖了 %s（L5：不得 import mcp-server 模块）", dep)
		}
	}
}

// TestPurityNoTaskTreeDependency 覆盖 GATEWAY-2 B（任务树归 chat 侧）：
// gateway 只持有执行态（ExecTask / ExecSink 窄接口），不得依赖任务树 / 任务层 / 数据面模块
// —— 节点创建、父子关系、收敛均在 chat 侧（chonkpilot-task + llm/server taskManager）。
// ExecSink 在 gateway 包内声明、由装配方注入，故单向依赖成立时本包不应出现这些模块。
func TestPurityNoTaskTreeDependency(t *testing.T) {
	banned := []string{
		"chonkpilot-" + "task",
		"chonkpilot-" + "data",
		"chonkpilot-" + "llm",
	}
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		for _, b := range banned {
			if strings.HasPrefix(dep, "github.com/chonkpilot/"+b) {
				t.Fatalf("gateway 包依赖了 %s（B：任务树归 chat 侧，gateway 仅执行态）", dep)
			}
		}
	}
}

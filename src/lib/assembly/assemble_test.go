// L1 白盒：入口装配器（RB-5 L5）——装配顺序约束 / 依赖注入连线 / 失败态 / 幂等性。
//
// 只断言本包 `Build` / `Scan` / `AppToolNames` 的**可观测**行为（以实际实现为准，不臆造）：
//   - 成功路径 = 能力源 + 执行配置 + 内嵌 gateway 三者齐备，契约根注入执行配置；
//   - 失败态 = gateway 缺依赖（Bus=nil）→ 报错但**能力源/执行配置恒保留**（Stack 非 nil）；
//   - 顺序约束 = 契约注册失败**不阻断** gateway（空能力面继续），执行配置 Root 先于 gateway 就位；
//   - 依赖倒置 = Stack 实现 `mcpgateway.ContractScanner`，`Scan` 扫独立 dir 契约根建独立 server；
//   - 幂等 / 重复装配 = 同参两次 `Build` 得**独立**实例，无共享全局态。
package assembly

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
)

// 编译期：Stack 必须满足 gateway 的 `ContractScanner` 契约（RB-2 依赖倒置注入点）。
var _ mcpgateway.ContractScanner = (*Stack)(nil)

// writeToolContract 在 dir 下写一个最小 `*.tool.md`（原语名 = 文件名去后缀；无 runtime 亦可
// 注册 —— runtime 仅在 tools/call 时经 resolveRuntime 解析，见 mcp-server/server/contract.go）。
func writeToolContract(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name+".tool.md")
	body := "# " + name + "\n\n[description]\nfixture tool " + name + "\n\n[parameters]\ntype: object\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

// writeBadToolContract 写一个**非法** `*.tool.md`（input schema 顶层 type 非 object → buildTool 报错）。
func writeBadToolContract(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "bad.tool.md")
	if err := os.WriteFile(path, []byte("# bad\n\n[parameters]\ntype: array\n"), 0o644); err != nil {
		t.Fatalf("write bad fixture %s: %v", path, err)
	}
}

func newBus(t *testing.T) mq.Bus {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "test."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

// TestBuildWiring 成功路径（依赖注入连线）：能力源 / 执行配置 / 内嵌 gateway 三者齐备；
// 契约根注入执行配置；AppTools 收集 app 根工具名。
func TestBuildWiring(t *testing.T) {
	root := t.TempDir()
	writeToolContract(t, root, "alpha")

	st, err := Build(Options{Bus: newBus(t), Root: root})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if st == nil || st.Server == nil || st.Config == nil || st.Gateway == nil {
		t.Fatalf("装配产物不齐（能力源/执行配置/gateway）：%+v", st)
	}
	if st.Config.Root != root {
		t.Fatalf("执行配置契约根 = %q，want %q", st.Config.Root, root)
	}
	if !st.AppTools["alpha"] {
		t.Fatalf("AppTools 未收集 alpha：%v", st.AppTools)
	}
}

// TestBuildGatewayFailure：失败态 —— 缺依赖（Bus=nil）→ gateway 构建失败并报错，但
// **能力源 / 执行配置恒保留**（Stack 永不为 nil，与改前「gateway 失败仍保留 mcpServer/mcpCfg」一致）。
func TestBuildGatewayFailure(t *testing.T) {
	root := t.TempDir()
	writeToolContract(t, root, "alpha")

	st, err := Build(Options{Root: root}) // Bus 缺省 nil
	if err == nil {
		t.Fatal("Bus 缺省应导致 gateway 构建失败")
	}
	if st == nil || st.Server == nil || st.Config == nil {
		t.Fatalf("gateway 失败仍应保留能力源/执行配置：%+v", st)
	}
	if st.Gateway != nil {
		t.Fatal("gateway 失败不应挂 Gateway")
	}
	if st.AppTools == nil {
		t.Fatal("AppTools 不应为 nil")
	}
}

// TestBuildCapabilityErrorContinues：顺序约束 —— 契约注册**失败**（非法契约）只记日志、
// **不阻断** gateway（空能力面继续）；Stack 完整且 err=nil。
func TestBuildCapabilityErrorContinues(t *testing.T) {
	root := t.TempDir()
	writeBadToolContract(t, root)

	var logged []string
	st, err := Build(Options{
		Bus:  newBus(t),
		Root: root,
		Logf: func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) },
	})
	if err != nil {
		t.Fatalf("契约注册失败不应阻断 gateway：%v", err)
	}
	if st == nil || st.Server == nil || st.Config == nil || st.Gateway == nil {
		t.Fatalf("Stack 不完整：%+v", st)
	}
	if !strings.Contains(strings.Join(logged, "\n"), "契约注册失败") {
		t.Fatalf("应记录契约注册失败日志，got %v", logged)
	}
}

// TestBuildDefaultRoot：Root 缺省 → `<exe 目录>/capability`（exedir 可用时；否则回落相对 `capability`）。
func TestBuildDefaultRoot(t *testing.T) {
	st, err := Build(Options{Bus: newBus(t)})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := "capability"
	if dir, e := exedir.Dir(); e == nil {
		want = filepath.Join(dir, "capability")
	}
	if st.Config == nil || st.Config.Root != want {
		t.Fatalf("缺省契约根 = %q，want %q", st.Config.Root, want)
	}
}

// TestScanInjection：依赖倒置连线 —— `Scan` 扫**另一**契约根建**独立** server（dir 节点入口，
// 与 self 不复用同一实例）；非法契约 → 明确报错（不静默）。
func TestScanInjection(t *testing.T) {
	st, err := Build(Options{Bus: newBus(t), Root: filepath.Join(t.TempDir(), "self")}) // self 根缺失 = 空能力面
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	dirRoot := t.TempDir()
	writeToolContract(t, dirRoot, "beta")
	srv, err := st.Scan(dirRoot)
	if err != nil || srv == nil {
		t.Fatalf("Scan(%s) = (%v, %v)，want 非 nil server", dirRoot, srv, err)
	}
	if srv == st.Server {
		t.Fatal("dir 节点应建独立 server（不与 self 复用同一实例）")
	}

	badRoot := t.TempDir()
	writeBadToolContract(t, badRoot)
	if _, err := st.Scan(badRoot); err == nil {
		t.Fatal("非法契约应报错（input schema 非 object）")
	}
}

// TestAppToolNames：递归收集 `*.tool.md` 原语名（= 文件名去后缀）；非契约文件忽略；
// 缺失根 → 空非 nil 集合；同根两次调用一致（幂等）。
func TestAppToolNames(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "cat", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeToolContract(t, root, "top")
	writeToolContract(t, sub, "nested")
	// 非 `*.tool.md`（prompt/普通 md）不属工具原语。
	if err := os.WriteFile(filepath.Join(root, "note.prompt.md"), []byte("# note\n"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	names := AppToolNames(root)
	if len(names) != 2 || !names["top"] || !names["nested"] {
		t.Fatalf("AppToolNames = %v，want {top,nested}", names)
	}
	if again := AppToolNames(root); len(again) != len(names) {
		t.Fatalf("两次调用不一致（幂等破坏）：%v vs %v", names, again)
	}
	missing := AppToolNames(filepath.Join(root, "no-such-dir"))
	if missing == nil || len(missing) != 0 {
		t.Fatalf("缺失根应得空非 nil 集合，got %v", missing)
	}
}

// TestBuildRepeatable：幂等 / 重复装配 —— 同参两次 `Build` 得**独立** Stack / Server / Gateway，
// 无共享全局态（各入口各自装配互不干扰）。
func TestBuildRepeatable(t *testing.T) {
	bus := newBus(t)
	root := t.TempDir()
	writeToolContract(t, root, "alpha")
	opts := Options{Bus: bus, Root: root}

	a, err := Build(opts)
	if err != nil {
		t.Fatalf("Build#1: %v", err)
	}
	b, err := Build(opts)
	if err != nil {
		t.Fatalf("Build#2: %v", err)
	}
	if a == b || a.Server == b.Server || a.Gateway == b.Gateway {
		t.Fatal("重复装配应产生独立实例（无共享全局态）")
	}
}

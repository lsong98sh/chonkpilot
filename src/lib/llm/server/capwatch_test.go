// T-21 capability 原语保存后热生效测试（21-llm-server / KB-007-S04）：
// 用户/项目级 capability 根经 fsnotify 监听 + 60ms 去抖 → 复用既有 servers/unregister +
// servers/register 重扫 dir 节点 → 工具面（tools/list）在数十毫秒级反映增删，无需重启。
// 边界：不新增消息面主题；系统级 self 节点不在本链路（见 capwatch.go 说明）。
package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// waitTool 轮询 tools/list 直到 name 出现（want=true）/ 消失（want=false）或超时；返回耗时。
func waitTool(t *testing.T, s *Server, name string, want bool, timeout time.Duration) (time.Duration, bool) {
	t.Helper()
	start := time.Now()
	for {
		found := false
		if defs, err := s.gc.ListTools(context.Background()); err == nil {
			for _, d := range defs {
				if d.Name == name {
					found = true
					break
				}
			}
		}
		if found == want {
			return time.Since(start), true
		}
		if time.Since(start) > timeout {
			return time.Since(start), false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCapabilityHotReload T-21：项目级原语保存（新增/删除 *.tool.md）后，工具面在
// 数十毫秒级热生效——目录监听 → 去抖重扫 → servers/register 重新扫描契约。
func TestCapabilityHotReload(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)

	prjRoot := persist.CapProjectRoot(testWorkDir)
	// 先建一个既有工具：保证 instance-register 时项目根存在并被监听。
	writeHotToolContract(t, prjRoot, "prj_before")
	s.onInstanceRegister("instance-register", jb(map[string]any{
		"instance_id": "ins-hot", "client_type": "unittest", "work_dir": testWorkDir,
	}))
	before := "ins-hot-project_prj_before"
	if _, ok := waitTool(t, s, before, true, 2*time.Second); !ok {
		t.Fatalf("初始项目级工具未接入 tools/list")
	}

	// 模拟「保存」：新写一个 hot 契约（等价 data-knowledge-save 落盘）。
	writeHotToolContract(t, prjRoot, "prj_after")
	after := "ins-hot-project_prj_after"
	elapsed, ok := waitTool(t, s, after, true, 3*time.Second)
	if !ok {
		t.Fatalf("保存后新工具未在超时内热生效（watcher 未触发重扫）")
	}
	if elapsed > time.Second {
		t.Fatalf("热生效耗时过长: %v（应数十毫秒级；去抖 %v）", elapsed, capWatchDebounce)
	}
	t.Logf("保存 → 工具面生效耗时 %v（去抖 %v）", elapsed, capWatchDebounce)

	// 删除文件 → 同样热生效（工具退出工具面）。
	if err := os.Remove(filepath.Join(prjRoot, "tools", "prj_after.tool.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := waitTool(t, s, after, false, 3*time.Second); !ok {
		t.Fatalf("删除后工具未在超时内热移除")
	}
	// 既有工具不受影响。
	if _, ok := waitTool(t, s, before, true, time.Second); !ok {
		t.Fatalf("重扫误删既有工具: %s", before)
	}
}

// TestAppCapabilityHotReload T-21②：系统级 app 根（<exeDir>/capability，内嵌 self 节点承载）
// 的原语变更同样热生效——外部/构建期更新该根后，契约重注册 + gateway/reload 使工具面在
// 数十毫秒级反映增删，无需重启；app 只读语义不变（UI 不可改，用例以临时目录模拟该根）。
func TestAppCapabilityHotReload(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	appRoot := t.TempDir()
	writeHotToolContract(t, appRoot, "app_before") // 启动前既有 app 工具（New 时 RegisterContracts）
	s := newTestServerMCPAppRoot(t, llm, appRoot)

	before := "self_app_before" // self 节点暴露名带 entry.ID 前缀（gateway applyPrefix）
	if _, ok := waitTool(t, s, before, true, 2*time.Second); !ok {
		t.Fatalf("app 根既有工具未在启动时进入工具面")
	}

	// 模拟「构建期/外部更新」：向 app 根写入新契约。
	writeHotToolContract(t, appRoot, "app_after")
	after := "self_app_after"
	elapsed, ok := waitTool(t, s, after, true, 5*time.Second)
	if !ok {
		t.Fatalf("app 根新增契约未在超时内热生效（watcher 未触发重扫）")
	}
	if elapsed > time.Second {
		t.Fatalf("app 根热生效耗时过长: %v（应数十毫秒级；去抖 %v）", elapsed, capWatchDebounce)
	}
	t.Logf("app 根写入 → 工具面生效耗时 %v（去抖 %v）", elapsed, capWatchDebounce)

	// 删除契约 → 同样热生效（RemoveTools + gateway/reload）。
	if err := os.Remove(filepath.Join(appRoot, "tools", "app_after.tool.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := waitTool(t, s, after, false, 5*time.Second); !ok {
		t.Fatalf("app 契约删除后工具未在超时内热移除")
	}
	if _, ok := waitTool(t, s, before, true, time.Second); !ok {
		t.Fatalf("app 根重扫误删既有工具: %s", before)
	}
}

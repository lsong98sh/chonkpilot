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

	"github.com/fsnotify/fsnotify"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// TestCapWatchIgnoresDirsOutsideRoots 回归：flash 全新 HOME 下 instance-register 首次 60s 超时。
// 当 usr/prjusr capability 根尚未创建时，watcher 回落到最近已存在祖先监听（如 <usr> =
// <HOME>/.chonkpilot）；该祖先内的无关目录——典型如 WebView2 profile 树 <usr>/webview2/**——
// **不得**被递归纳入监听：否则被 fsnotify 全量接管（运行态大树、数万目录 + 高频变更），
// 持 cw.mu 期间阻塞并发的 watchInstance（→ instance-register 分派 60s 超时）。
func TestCapWatchIgnoresDirsOutsideRoots(t *testing.T) {
	cw, err := newCapWatcher(nil) // 手动喂事件驱动；本用例不触发 fire（s 不参与）
	if err != nil {
		t.Fatalf("newCapWatcher: %v", err)
	}
	defer cw.Close()

	tmp := t.TempDir()
	usrAnc := filepath.Join(tmp, "usr")         // 模拟 <usr>（如 <HOME>/.chonkpilot，已存在）
	root := filepath.Join(usrAnc, "capability") // 目标 capability 根（尚未创建）
	if err := os.MkdirAll(usrAnc, 0o755); err != nil {
		t.Fatalf("mkdir usrAnc: %v", err)
	}

	cw.mu.Lock()
	cw.want[normPath(root)] = true
	cw.syncLocked() // 根不存在 → 回落到祖先 usrAnc 监听
	_, watchingAnc := cw.watched[normPath(usrAnc)]
	cw.mu.Unlock()
	if !watchingAnc {
		t.Fatalf("根不存在时应回落到最近已存在祖先监听: %s", usrAnc)
	}

	// 模拟运行态大树（WebView2 profile）：<usr>/webview2/gui-1/EBWebView/Cache
	decoy := filepath.Join(usrAnc, "webview2", "gui-1", "EBWebView", "Cache")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatalf("mkdir decoy: %v", err)
	}
	// 等价 fsnotify 对 usrAnc 直属子项 webview2 的 Create 投递。
	cw.handle(fsnotify.Event{Name: filepath.Join(usrAnc, "webview2"), Op: fsnotify.Create})

	cw.mu.Lock()
	_, watchedWv := cw.watched[normPath(filepath.Join(usrAnc, "webview2"))]
	_, watchedDeep := cw.watched[normPath(decoy)]
	cw.mu.Unlock()
	if watchedWv || watchedDeep {
		t.Fatalf("目标根之外的目录不得纳入递归监听（webview2=%v deep=%v）", watchedWv, watchedDeep)
	}
}

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

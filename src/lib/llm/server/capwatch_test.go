// T-21 capability 原语保存后热生效测试（21-llm-server / KB-007-S04）：
// 用户/项目级 capability 根经 fswatcher 监听 + 60ms 去抖 → 复用既有 servers/unregister +
// servers/register 重扫 dir 节点 → 工具面（tools/list）在数十毫秒级反映增删，无需重启。
// 边界：不新增消息面主题；系统级 self 节点不在本链路（见 capwatch.go 说明）。
package server

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sgtdi/fswatcher"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// TestCapWatchIgnoresDirsOutsideRoots 回归：flash 全新 HOME 下 instance-register 首次 60s 超时。
// 当 usr/prjusr capability 根尚未创建时，watcher 回落到最近已存在祖先监听（如 <usr> =
// <HOME>/.chonkpilot）；该祖先内的无关目录——典型如 WebView2 profile 树 <usr>/webview2/**——
// **不得**被递归纳入监听：否则被 fswatcher 全量接管（运行态大树、数万目录 + 高频变更），
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
	cw.mu.Unlock()
	cw.sync() // 根不存在 → 回落到祖先 usrAnc 监听（LLM-P4：收敛在 syncMu 内、IO 不持 mu）
	cw.mu.Lock()
	_, watchingAnc := cw.synced[normPath(usrAnc)]
	cw.mu.Unlock()
	if !watchingAnc {
		t.Fatalf("根不存在时应回落到最近已存在祖先监听: %s", usrAnc)
	}

	// 模拟运行态大树（WebView2 profile）：<usr>/webview2/gui-1/EBWebView/Cache
	decoy := filepath.Join(usrAnc, "webview2", "gui-1", "EBWebView", "Cache")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatalf("mkdir decoy: %v", err)
	}
	// 等价 fswatcher 对 usrAnc 直属子项 webview2 的 Create 投递。
	cw.handle(fswatcher.WatchEvent{
		Path:  filepath.Join(usrAnc, "webview2"),
		Types: []fswatcher.EventType{fswatcher.EventCreate},
	})

	cw.mu.Lock()
	_, watchedWv := cw.synced[normPath(filepath.Join(usrAnc, "webview2"))]
	_, watchedDeep := cw.synced[normPath(decoy)]
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

// waitCapWatched 等待 capWatcher 已对该根建立监听（AddPath 收口）。instance-register 的
// 注册链在后台「先 register 再 watchInstance」，工具面已可见不代表监听已建立；用例若在
// AddPath 前保存，事件面天然看不到已存在文件（Windows 尤其明显），会把测试同步缺陷误判为
// 热生效缺陷。保存前等待监听就绪，保持「保存 → 热生效」的既有语义。
func waitCapWatched(t *testing.T, cw *capWatcher, root string, timeout time.Duration) {
	t.Helper()
	if cw == nil {
		t.Fatal("capability watcher 未启动")
	}
	root = normPath(root)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cw.mu.Lock()
		_, watched := cw.synced[root]
		cw.mu.Unlock()
		if watched {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("capability 根未在超时内建立监听: %s", root)
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
	payload := jb(map[string]any{
		"instance_id": "ins-hot", "client_type": "unittest", "work_dir": testWorkDir,
	})
	// 实例 → work_dir 映射只有一份（共通实例视图 s.im）：生产链路由 im 先处理同一
	// instance-register，此处直调 handler 须先补登记，重扫才看得到该实例。
	s.im.HandleRegister(payload)
	s.onInstanceRegister("instance-register", payload)
	before := "ins-hot-project_prj_before"
	if _, ok := waitTool(t, s, before, true, 2*time.Second); !ok {
		t.Fatalf("初始项目级工具未接入 tools/list")
	}
	waitCapWatched(t, s.capWatch, prjRoot, 5*time.Second) // 确保「保存」发生在监听建立之后

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

// startWatchedProjectInstance 建真实内嵌 MCP 栈并注册一个项目级实例：初始工具可见且
// 项目 capability 根已进入监听。返回后即可精确控制「保存」时点（热生效用例共用）。
func startWatchedProjectInstance(t *testing.T, instanceID, beforeTool string) (*Server, string) {
	t.Helper()
	llm := mockLLMServer()
	t.Cleanup(llm.Close)
	s := newTestServerMCP(t, llm)
	prjRoot := persist.CapProjectRoot(testWorkDir)
	writeHotToolContract(t, prjRoot, beforeTool)
	payload := jb(map[string]any{
		"instance_id": instanceID, "client_type": "unittest", "work_dir": testWorkDir,
	})
	s.im.HandleRegister(payload)
	s.onInstanceRegister("instance-register", payload)
	before := instanceID + "-project_" + beforeTool
	if _, ok := waitTool(t, s, before, true, 5*time.Second); !ok {
		t.Fatalf("初始项目级工具未接入 tools/list: %s", before)
	}
	waitCapWatched(t, s.capWatch, prjRoot, 5*time.Second)
	return s, prjRoot
}

// TestCapabilityWatchLostEventFallback 事件丢失兜底：模拟 fswatcher 漏投递（Windows 快速保存
// 写临时文件 + rename 的典型症状），仅靠周期重扫也必须让新工具在一个周期左右进入 tools/list、
// 删除后退出——证明热生效不会因单次丢事件而永久失效。
func TestCapabilityWatchLostEventFallback(t *testing.T) {
	s, prjRoot := startWatchedProjectInstance(t, "ins-lost", "prj_before")
	cw := s.capWatch
	// 先让注册前后的既有事件/去抖收口，避免把在途事件误当兜底路径。
	time.Sleep(capWatchDebounce + 50*time.Millisecond)
	cw.dropEvents.Store(true) // 此后丢弃全部 fsnotify 事件（只留周期重扫）
	runs0, ops0 := cw.rescanRuns.Load(), cw.rescanOps.Load()

	writeHotToolContract(t, prjRoot, "prj_lost")
	after := "ins-lost-project_prj_lost"
	elapsed, ok := waitTool(t, s, after, true, capWatchRescanInterval+5*time.Second)
	if !ok {
		t.Fatalf("事件丢失后新工具未热生效（周期兜底重扫未起作用）")
	}
	if cw.rescanRuns.Load() <= runs0 {
		t.Fatalf("工具生效但周期重扫计数未增长（走的是事件路径？）")
	}
	// rescanOps 在该轮 scan 返回后累加（工具面操作先于计数可观测）→ 轮询收口再断言。
	opsDeadline := time.Now().Add(2 * time.Second)
	for cw.rescanOps.Load() <= ops0 && time.Now().Before(opsDeadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if cw.rescanOps.Load() <= ops0 {
		t.Fatalf("周期重扫未产生新节点操作（无法证明差量重扫接入新契约）")
	}
	t.Logf("漏事件 → 周期兜底生效耗时 %v（间隔 %v）", elapsed, capWatchRescanInterval)

	// 删除同样只靠周期重扫退出工具面。
	if err := os.Remove(filepath.Join(prjRoot, "tools", "prj_lost.tool.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	runs1 := cw.rescanRuns.Load()
	elapsed, ok = waitTool(t, s, after, false, capWatchRescanInterval+5*time.Second)
	if !ok {
		t.Fatalf("事件丢失后删除未在周期内退出 tools/list")
	}
	if cw.rescanRuns.Load() <= runs1 {
		t.Fatalf("删除生效但周期重扫计数未增长")
	}
	t.Logf("漏事件 → 删除退出耗时 %v（间隔 %v）", elapsed, capWatchRescanInterval)
	if _, ok := waitTool(t, s, "ins-lost-project_prj_before", true, time.Second); !ok {
		t.Fatalf("周期重扫误删既有工具")
	}
}

// TestCapabilityWatchPeriodicRescanNoDuplicate 负例：无任何变更时，周期重扫不得产生重复
// 节点 / 重复注册（capRootSignature 差量判据 → 零操作），工具面也不得抖动。
func TestCapabilityWatchPeriodicRescanNoDuplicate(t *testing.T) {
	s, _ := startWatchedProjectInstance(t, "ins-dup", "prj_dup")
	cw := s.capWatch
	tool := "ins-dup-project_prj_dup"
	cw.dropEvents.Store(true) // 隔离事件路径，只观察周期重扫

	beforeNodes := capDirNodesOf(s, "ins-dup")
	if len(beforeNodes) != 1 {
		t.Fatalf("初始 dir 节点数应为 1，got %v", beforeNodes)
	}
	runs0, ops0 := cw.rescanRuns.Load(), cw.rescanOps.Load()

	// 静默跨越至少 2 个重扫周期（期间无任何文件变更）。
	time.Sleep(2*capWatchRescanInterval + 200*time.Millisecond)
	if delta := cw.rescanRuns.Load() - runs0; delta < 2 {
		t.Fatalf("周期重扫未按间隔执行: %d 次（间隔 %v）", delta, capWatchRescanInterval)
	}
	if delta := cw.rescanOps.Load() - ops0; delta != 0 {
		t.Fatalf("未变更时周期重扫产生 %d 次重复注册/注销（工具面抖动）", delta)
	}
	if afterNodes := capDirNodesOf(s, "ins-dup"); !reflect.DeepEqual(beforeNodes, afterNodes) {
		t.Fatalf("周期重扫后 dir 节点变化: before=%v after=%v", beforeNodes, afterNodes)
	}
	defs, err := s.gc.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if c := countTool(defs, tool); c != 1 {
		t.Fatalf("未变更时工具 %s 出现 %d 次（重复注册）", tool, c)
	}
	t.Logf("周期重扫 %d 次，注册操作 0（无重复节点）", cw.rescanRuns.Load()-runs0)
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

	before := "app_before" // P0a 2026-10-29：暴露名 = 原名（gateway applyPrefix 对 self 节点不再加前缀）
	if _, ok := waitTool(t, s, before, true, 2*time.Second); !ok {
		t.Fatalf("app 根既有工具未在启动时进入工具面")
	}

	// 模拟「构建期/外部更新」：向 app 根写入新契约。
	writeHotToolContract(t, appRoot, "app_after")
	after := "app_after"
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

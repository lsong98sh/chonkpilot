// filesys 单测：目录子项过滤 + 工作区监听（PM 驱动）+ 去抖/归一 + 隐藏清单 + 复制嵌套防护。
package filesys

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/chonkpilot/chonkpilot-workspace"
	"github.com/sgtdi/fswatcher"
)

// newTestWM 建「总线 + 工作区管理 + 已登记实例」的 watcher 管理器：
// 监听由工作区打开驱动（而非前端展开）。
func newTestWM(t *testing.T, bus mq.Bus, wd, instanceID string) *watchManager {
	t.Helper()
	pm := workspace.New(bus, workspace.Options{Logf: t.Logf})
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("workspace.Start: %v", err)
	}
	t.Cleanup(pm.Stop)
	raw, _ := json.Marshal(map[string]any{"instance_id": instanceID, "work_dir": wd})
	bus.Emit(context.Background(), "instance-register", raw).Wait()
	m := newWatchManager(bus, pm)
	t.Cleanup(m.Close)
	return m
}

// TestListDirNodesFilter：隐藏/临时条目过滤（I-44，对齐旧 FilterDirEntry 口径）：
// "."/"~$" 前缀、"/~"/"-wal"/"-shm" 后缀跳过；普通文件保留。
func TestListDirNodesFilter(t *testing.T) {
	dir := t.TempDir()
	keep := []string{"keep.txt", "sub"}
	skip := []string{".hidden", "~$tmp.docx", "x-wal", "y~", "z-shm"}
	for _, n := range keep {
		if n == "sub" {
			if err := os.Mkdir(filepath.Join(dir, n), 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range skip {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nodes := listDirNodes(dir, nil)
	got := map[string]bool{}
	for _, n := range nodes {
		got[n["name"].(string)] = true
	}
	for _, n := range keep {
		if !got[n] {
			t.Fatalf("过滤误删合法条目 %q（nodes=%v）", n, got)
		}
	}
	for _, n := range skip {
		if got[n] {
			t.Fatalf("临时/隐藏条目 %q 未被过滤（nodes=%v）", n, got)
		}
	}
}

// TestWatchNewDirBroadcastsParentBatch：新建目录时除自身外，**父目录批次**（带 children）
// 也须广播且父 children 含新目录节点（I-43；否则前端树不插入目录节点）。
func TestWatchNewDirBroadcastsParentBatch(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()

	wd := t.TempDir()
	m := newTestWM(t, bus, wd, "ins-test")

	ch := newChangedCh(t, bus)
	if err := m.open(wd, "ins-test"); err != nil {
		t.Fatal(err)
	}

	newDir := filepath.Join(wd, "probe_rootdir")
	if err := os.Mkdir(newDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 轮询等待：父目录批次（children 含 probe_rootdir + 携带实例 instance_id）
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			children, ok := ev["children"].([]any)
			if !ok {
				continue // 单文件事件（无 children）
			}
			for _, c := range children {
				cm, _ := c.(map[string]any)
				if cm["name"] != "probe_rootdir" {
					continue
				}
				if ev["instance_id"] != "ins-test" {
					t.Fatalf("父批次缺/错 instance_id: %+v", ev)
				}
				return // 命中：父批次已含新目录节点
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("新建目录未广播含新目录节点的父目录批次（I-43）")
}

// TestNextWaitCapsAtMaxWait 去抖等待封顶（D-37）：默认 debounce；窗口内累计耗时逼近 maxWait
// 时等待被压缩到剩余量；超出 maxWait 即 0（立即触发，不再被持续事件无限推后）。
func TestNextWaitCapsAtMaxWait(t *testing.T) {
	if got := nextWait(0); got != debounce {
		t.Fatalf("首事件应等待 debounce：got=%v", got)
	}
	if got := nextWait(maxWait - debounce - time.Millisecond); got != debounce {
		t.Fatalf("剩余 > debounce 时应等待 debounce：got=%v", got)
	}
	if got := nextWait(maxWait - 10*time.Millisecond); got != 10*time.Millisecond {
		t.Fatalf("剩余 < debounce 时应等待剩余量：got=%v", got)
	}
	if got := nextWait(maxWait + time.Millisecond); got != 0 {
		t.Fatalf("超过 maxWait 应立即触发（0）：got=%v", got)
	}
}

// TestOperationNormalize 事件操作归一：fswatcher（Windows）重命名新旧名均为 EventRename，
// 且同窗口内同路径事件类型会合并 → 以「处理时对象是否存在」定语义，保证 indexer / 文件树一致。
func TestOperationNormalize(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone.txt")
	cases := []struct {
		name  string
		path  string
		types []fswatcher.EventType
		want  string
	}{
		{"存在 + Create", file, []fswatcher.EventType{fswatcher.EventCreate}, "create"},
		{"存在 + Rename（改名新路径）", file, []fswatcher.EventType{fswatcher.EventRename}, "create"},
		{"存在 + Mod/Rename 合并", file, []fswatcher.EventType{fswatcher.EventMod, fswatcher.EventRename}, "create"},
		{"存在 + Mod", file, []fswatcher.EventType{fswatcher.EventMod}, "write"},
		{"不存在 + Rename（改名旧路径）", gone, []fswatcher.EventType{fswatcher.EventRename}, "rename"},
		{"不存在 + Remove", gone, []fswatcher.EventType{fswatcher.EventRemove}, "remove"},
		{"不存在 + Create/Remove 合并", gone, []fswatcher.EventType{fswatcher.EventCreate, fswatcher.EventRemove}, "remove"},
	}
	for _, c := range cases {
		if got := operation(c.path, c.types); got != c.want {
			t.Errorf("%s：operation=%q，期望 %q", c.name, got, c.want)
		}
	}
}

// TestWatchContinuousWritesNotStarved 持续写入下批次不被无限推后（D-37）：写入**仍在进行**时
// 就应收到 filesys.changed（旧实现每个事件都重置 debounce，直到写入停顿才触发 → 饥饿）。
func TestWatchContinuousWritesNotStarved(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	m := newTestWM(t, bus, wd, "ins-deb")
	ch := make(chan struct{}, 64)
	if _, err := bus.On("filesys.changed", 0, func(_ context.Context, _ string, _ *mq.Value) error {
		select {
		case ch <- struct{}{}:
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.open(wd, "ins-deb"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(wd, "busy.log")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				_ = os.WriteFile(target, []byte(time.Now().String()), 0o644)
			}
		}
	}()

	// 写入持续进行中：应在 maxWait 量级内收到至少一条事件（远早于写入停止 → 无饥饿）。
	select {
	case <-ch:
	case <-time.After(maxWait + 2*time.Second):
		close(stop)
		<-done
		t.Fatalf("持续写入下 %v 内未收到 filesys.changed（批次被无限推后，D-37 未生效）",
			maxWait+2*time.Second)
	}
	close(stop)
	<-done
}

// TestWorkdirOpenWatchesSubtree：工作区打开即**递归**监听整棵子树（不再由前端展开声明）；
// 未展开的深层目录内新增文件同样广播 —— 索引 / 历史与文件树看到同一份事件面。
func TestWorkdirOpenWatchesSubtree(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	sub := filepath.Join(wd, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newTestWM(t, bus, wd, "ins-rec")
	ch := newChangedCh(t, bus)
	if err := m.open(wd, "ins-rec"); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(sub, "deep.txt")
	if err := os.WriteFile(deep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitChangedPath(t, ch, deep, 5*time.Second) {
		t.Fatal("工作区打开后深层子目录内新增文件未触发 filesys.changed")
	}
}

// newChangedCh 订阅 filesys.changed 并把 payload 投进带缓冲通道。
func newChangedCh(t *testing.T, bus mq.Bus) chan map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 64)
	if _, err := bus.On("filesys.changed", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var ev map[string]any
		if json.Unmarshal(v.Payload, &ev) == nil {
			select {
			case ch <- ev:
			default:
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return ch
}

// waitChangedPath 期望收到 path == want 的单文件 changed 事件（含 operation），超时返回 false。
func waitChangedPath(t *testing.T, ch chan map[string]any, want string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			if _, isBatch := ev["children"]; isBatch {
				continue
			}
			if ev["path"] == filepath.ToSlash(want) {
				return true
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	return false
}

// TestShouldHideDirNameSameSource：目录判定同源 —— 对同一目录名，
// 文件树过滤（filterDirEntry(isDir=true)）与 watcher 事件过滤（shouldHideDirName）返回一致。
func TestShouldHideDirNameSameSource(t *testing.T) {
	hidden := []string{"target", "node_modules", "out"}
	names := []string{".git", ".hidden", "target", "node_modules", "out", "src", "Target", "dist", ""}
	for _, n := range names {
		if got, want := filterDirEntry(n, true, hidden), shouldHideDirName(n, hidden); got != want {
			t.Fatalf("目录判定不同源：name=%q filterDirEntry=%v shouldHideDirName=%v", n, got, want)
		}
	}
	// 点开头恒隐藏（不可配置放开）
	if !shouldHideDirName(".git", nil) || !shouldHideDirName(".chonkpilot", nil) {
		t.Fatal("点开头目录应恒隐藏")
	}
	// 清单命中隐藏；大小写敏感（目录名匹配，Windows 亦按字面）
	if !shouldHideDirName("target", hidden) {
		t.Fatal("清单命中项应隐藏")
	}
	if shouldHideDirName("Target", hidden) {
		t.Fatal("清单匹配应大小写敏感")
	}
	// 清单不影响**文件**判定（文件规则独立）
	if filterDirEntry("target", false, hidden) {
		t.Fatal("普通文件 target（同名）不应被目录清单隐藏")
	}
}

// TestListDirNodesHiddenDirsFilter：listDirNodes 按隐藏清单过滤目录，但不误伤同名文件。
func TestListDirNodesHiddenDirsFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodes := listDirNodes(dir, []string{"target"})
	got := map[string]bool{}
	for _, n := range nodes {
		got[n["name"].(string)] = true
	}
	if got["target"] {
		t.Fatalf("隐藏清单命中目录不应出现: %v", got)
	}
	if !got["src"] || !got["target.txt"] {
		t.Fatalf("非同名的目录/文件不应被误过滤: %v", got)
	}
}

// TestWatchSkipsHiddenDirEvents：隐藏清单内的目录事件不广播（与文件树显示同源，层2）；
// 监听本身由 fswatcher 递归覆盖，不再按清单增删 watch。
func TestWatchSkipsHiddenDirEvents(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	target := filepath.Join(wd, "target")
	if err := os.MkdirAll(filepath.Join(target, "classes"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(wd, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newTestWM(t, bus, wd, "ins-hide")
	ch := newChangedCh(t, bus)
	m.applyHidden(wd, []string{"target"}) // 装载清单
	if err := m.open(wd, "ins-hide"); err != nil {
		t.Fatal(err)
	}

	hiddenFile := filepath.Join(target, "classes", "deep.txt")
	if err := os.WriteFile(hiddenFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if waitChangedPath(t, ch, hiddenFile, 500*time.Millisecond) {
		t.Fatal("隐藏目录内文件不应广播 filesys.changed")
	}

	visible := filepath.Join(src, "a.txt")
	if err := os.WriteFile(visible, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitChangedPath(t, ch, visible, 5*time.Second) {
		t.Fatal("可见目录内文件应广播 filesys.changed（watcher 未工作）")
	}
}

// TestRefilterAppliesHiddenChange：清单变更即时生效（事件过滤）——现已隐藏者不再广播，
// 「由隐藏转可见」者恢复广播；监听根不随清单增删（fswatcher 递归接管）。
func TestRefilterAppliesHiddenChange(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	target := filepath.Join(wd, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wd, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := newTestWM(t, bus, wd, "ins-refilter")
	ch := newChangedCh(t, bus)
	if err := m.open(wd, "ins-refilter"); err != nil {
		t.Fatal(err)
	}

	// 初始可见 → 广播
	f1 := filepath.Join(target, "f1.txt")
	if err := os.WriteFile(f1, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitChangedPath(t, ch, f1, 5*time.Second) {
		t.Fatal("初始应广播 target 内变更")
	}

	// 加入清单 → target 内变更不再广播
	m.applyHidden(wd, []string{"target"})
	f2 := filepath.Join(target, "f2.txt")
	if err := os.WriteFile(f2, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if waitChangedPath(t, ch, f2, 500*time.Millisecond) {
		t.Fatal("清单加入后 target 内变更不应广播")
	}

	// 移出清单 → 恢复广播
	m.applyHidden(wd, []string{"node_modules"})
	f3 := filepath.Join(target, "f3.txt")
	if err := os.WriteFile(f3, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitChangedPath(t, ch, f3, 5*time.Second) {
		t.Fatal("清单移除后 target 内变更应恢复广播")
	}
}

// TestHiddenEventsDroppedBeforeQueue：隐藏目录内事件在**入队前**被丢弃（G-86 ①）——
// `droppedHidden` 递增说明事件未进 60ms 队列（若只在 processBatch 过滤，该计数恒为 0）。
func TestHiddenEventsDroppedBeforeQueue(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	hidden := filepath.Join(wd, "node_modules", "pkg")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newTestWM(t, bus, wd, "ins-pre")
	m.applyHidden(wd, []string{"node_modules"})
	ch := newChangedCh(t, bus)
	if err := m.open(wd, "ins-pre"); err != nil {
		t.Fatal(err)
	}

	// 等 watcher 就绪（可见文件探针）。
	probe := filepath.Join(wd, "probe.txt")
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if waitChangedPath(t, ch, probe, 150*time.Millisecond) {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("watcher 未就绪")
	}

	m.mu.Lock()
	dw := m.watchers[wd]
	m.mu.Unlock()
	if dw == nil {
		t.Fatal("dirWatcher 缺失")
	}

	// 隐藏目录内写文件：事件应被入队前丢弃，且不广播。
	before := dw.droppedHidden.Load()
	deep := filepath.Join(hidden, "x.txt")
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(deep, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	ok := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if dw.droppedHidden.Load() > before {
			ok = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ok {
		t.Fatal("隐藏目录事件未在入队前被丢弃（droppedHidden 未递增）")
	}
	if waitChangedPath(t, ch, deep, 500*time.Millisecond) {
		t.Fatal("隐藏目录内文件不应广播 filesys.changed")
	}
}

// TestParseHiddenDirs：解析（逗号/分号/换行分隔、去空白、去尾分隔符、去重、空 → 缺省）。
func TestParseHiddenDirs(t *testing.T) {
	got := parseHiddenDirs(" target , node_modules;out/\n\n build ")
	want := []string{"target", "node_modules", "out", "build"}
	if !sameStrings(got, want) {
		t.Fatalf("parseHiddenDirs=%v want %v", got, want)
	}
	if got := parseHiddenDirs("target,target"); !sameStrings(got, []string{"target"}) {
		t.Fatalf("应去重: %v", got)
	}
	if got := parseHiddenDirs("   "); !sameStrings(got, defaultHiddenDirs) {
		t.Fatalf("空配置应回落缺省: %v", got)
	}
}

// newBoundFilesys 建「总线 + PM + 已登记实例」的 Filesys（不启动订阅，直接驱 handler）。
func newBoundFilesys(t *testing.T, wd, instanceID string) *Filesys {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	pm := workspace.New(bus, workspace.Options{Logf: t.Logf})
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("workspace.Start: %v", err)
	}
	t.Cleanup(pm.Stop)
	raw, _ := json.Marshal(map[string]any{"instance_id": instanceID, "work_dir": wd})
	bus.Emit(context.Background(), "instance-register", raw).Wait()
	return &Filesys{bus: bus, pm: pm}
}

// TestOnCopyRejectsNestedTarget（R7-C-5）：dest_dir 选源目录内部子目录时拒绝，
// 且不得在源目录内创建目标（否则 copyTree 会把新建目标递归吸入 → 无限嵌套复制）。
func TestOnCopyRejectsNestedTarget(t *testing.T) {
	wd := t.TempDir()
	src := filepath.Join(wd, "a")
	if err := os.MkdirAll(filepath.Join(src, "n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	f := newBoundFilesys(t, wd, "i1")
	payload, _ := json.Marshal(map[string]any{
		"instance_id": "i1", "path": src, "dest_dir": filepath.Join("a", "sub"),
	})
	v := &mq.Value{Payload: payload}
	if err := f.onCopy(context.Background(), "", v); err != nil {
		t.Fatalf("onCopy err: %v", err)
	}
	if len(v.Errors) == 0 {
		t.Fatalf("嵌套复制应被拒绝，errors=%v result=%v", v.Errors, v.Result)
	}
	if _, err := os.Stat(filepath.Join(src, "sub")); !os.IsNotExist(err) {
		t.Fatalf("不得在源目录内创建目标目录（stat err=%v）", err)
	}
}

// TestOnCopyNormalTargetAllowed（R7-C-5 反向）：目标在源目录之外的正常复制仍成功。
func TestOnCopyNormalTargetAllowed(t *testing.T) {
	wd := t.TempDir()
	src := filepath.Join(wd, "a")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	f := newBoundFilesys(t, wd, "i1")
	payload, _ := json.Marshal(map[string]any{
		"instance_id": "i1", "path": src, "dest_dir": "out",
	})
	v := &mq.Value{Payload: payload}
	if err := f.onCopy(context.Background(), "", v); err != nil {
		t.Fatalf("onCopy err: %v", err)
	}
	if len(v.Errors) != 0 {
		t.Fatalf("正常复制不应失败: %v", v.Errors)
	}
	if _, err := os.Stat(filepath.Join(wd, "out", "a", "f.txt")); err != nil {
		t.Fatalf("目标文件应存在：%v", err)
	}
}

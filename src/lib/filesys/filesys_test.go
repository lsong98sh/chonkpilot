// filesys 单测：目录子项过滤（I-44）+ 新建目录父批次广播（I-43）。
package filesys

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

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
	nodes := listDirNodes(dir)
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
	m := newWatchManager(bus)
	defer m.Close()

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
	if err := m.watch(wd, wd, false, "ins-test"); err != nil {
		t.Fatal(err)
	}

	newDir := filepath.Join(wd, "probe_rootdir")
	if err := os.Mkdir(newDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 轮询等待：父目录批次（children 含 probe_rootdir + 携带声明者 instance_id）
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

// TestWatchRecursiveFlag：recursive 标志被消费（T-08）——递归监听时子目录内新增文件触发
// filesys.changed；非递归监听同一路径时不触发（同一 fsnotify watcher 只监听直接子项）。
func TestWatchRecursiveFlag(t *testing.T) {
	// 期望收到 path == want 的单文件 changed 事件（含 operation），超时返回 false。
	waitChanged := func(t *testing.T, ch chan map[string]any, want string, d time.Duration) bool {
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
	newCh := func(t *testing.T, bus mq.Bus) chan map[string]any {
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

	t.Run("recursive=true 子目录内新增触发", func(t *testing.T) {
		bus, err := mq.New(mq.Options{Prefix: "chonk."})
		if err != nil {
			t.Fatal(err)
		}
		defer bus.Close()
		wd := t.TempDir()
		sub := filepath.Join(wd, "sub")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		m := newWatchManager(bus)
		defer m.Close()
		ch := newCh(t, bus)
		if err := m.watch(wd, wd, true, "ins-rec"); err != nil {
			t.Fatal(err)
		}
		deep := filepath.Join(sub, "deep.txt")
		if err := os.WriteFile(deep, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !waitChanged(t, ch, deep, 5*time.Second) {
			t.Fatal("recursive=true 时子目录内新增文件未触发 filesys.changed")
		}
	})

	t.Run("recursive=false 子目录内新增不触发", func(t *testing.T) {
		bus, err := mq.New(mq.Options{Prefix: "chonk."})
		if err != nil {
			t.Fatal(err)
		}
		defer bus.Close()
		wd := t.TempDir()
		sub := filepath.Join(wd, "sub")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		m := newWatchManager(bus)
		defer m.Close()
		ch := newCh(t, bus)
		if err := m.watch(wd, wd, false, "ins-norec"); err != nil {
			t.Fatal(err)
		}
		deep := filepath.Join(sub, "deep.txt")
		if err := os.WriteFile(deep, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if waitChanged(t, ch, deep, 500*time.Millisecond) {
			t.Fatal("recursive=false 时子目录内新增文件不应触发 filesys.changed")
		}
		// 反向确认 watcher 正常工作：直接子项新增仍触发
		top := filepath.Join(wd, "top.txt")
		if err := os.WriteFile(top, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !waitChanged(t, ch, top, 5*time.Second) {
			t.Fatal("直接子项新增未触发 filesys.changed（watcher 未工作）")
		}
	})
}

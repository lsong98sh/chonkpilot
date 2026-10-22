// 待索引队列入队（P2）：filesys 在广播 filesys.changed 的同时把每条变更写入注入的队列。
package filesys

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

const queueTestInstance = "ins-queue"

// fakeIndexQueue 是记录型队列（可注入返回错误）。
type fakeIndexQueue struct {
	mu      sync.Mutex
	changes []IndexChange
	err     error
}

func (q *fakeIndexQueue) Append(c IndexChange) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.changes = append(q.changes, c)
	return q.err
}

func (q *fakeIndexQueue) snapshot() []IndexChange {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]IndexChange, len(q.changes))
	copy(out, q.changes)
	return out
}

// setupQueueWatch 装配真实 mq + 临时 work_dir + 递归 watch，返回 work_dir / Filesys /
// filesys.changed 事件通道。
func setupQueueWatch(t *testing.T, queue IndexQueue) (string, *Filesys, chan map[string]any) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	wd := t.TempDir()
	f := NewWithOptions(bus, Options{IndexQueue: queue})
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Stop)

	ch := make(chan map[string]any, 128)
	if _, err := bus.On(msgkeys.TopicFilesysChanged, 0, func(_ context.Context, _ string, v *mq.Value) error {
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

	// 总线为同步派发：Emit 返回时 instance 绑定与 watch 声明均已生效。
	bus.Emit(context.Background(), msgkeys.TopicInstanceRegister, map[string]any{
		"instance_id": queueTestInstance,
		"work_dir":    wd,
	})
	bus.Emit(context.Background(), msgkeys.TopicFilesysWatch, map[string]any{
		"instance_id": queueTestInstance,
		"path":        wd,
		"recursive":   true,
	})
	return wd, f, ch
}

// waitIndex 轮询等待满足条件的队列记录（超时 fail）。
func waitIndex(t *testing.T, q *fakeIndexQueue, pred func(IndexChange) bool, msg string) IndexChange {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range q.snapshot() {
			if pred(c) {
				return c
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s（已记录 %v）", msg, q.snapshot())
	return IndexChange{}
}

// waitChanged 轮询等待 path 等于 want 的单文件 filesys.changed 事件。
func waitChanged(t *testing.T, ch chan map[string]any, want string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			if _, isBatch := ev["children"]; isBatch {
				continue
			}
			if ev["path"] == want {
				return true
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	return false
}

// createEmptyFile 只触发 Create（不写入，避免同批 Write 覆盖 Create —— 队列按路径去重只留最后事件）。
func createEmptyFile(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestIndexQueueEnqueueCreate：新建文件 → 队列收到 create。
func TestIndexQueueEnqueueCreate(t *testing.T) {
	q := &fakeIndexQueue{}
	wd, _, _ := setupQueueWatch(t, q)
	p := filepath.Join(wd, "new.txt")
	createEmptyFile(t, p)
	c := waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(p) && c.Operation == "create"
	}, "新建文件未以 create 入队")
	if c.WorkDir != wd {
		t.Fatalf("WorkDir=%q，期望 %q", c.WorkDir, wd)
	}
}

// TestIndexQueueEnqueueWrite：写入文件 → 队列收到 write。
func TestIndexQueueEnqueueWrite(t *testing.T) {
	q := &fakeIndexQueue{}
	wd, _, _ := setupQueueWatch(t, q)
	p := filepath.Join(wd, "doc.txt")
	if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 先等首批（create/write 均可）落队，确保随后写入进入新批次。
	waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(p)
	}, "首次创建未入队")
	if err := os.WriteFile(p, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(p) && c.Operation == "write"
	}, "写入文件未以 write 入队")
}

// TestIndexQueueEnqueueNewDirAndChild：新建子目录 → create；随后子目录内建文件也能入队
// （验证 fswatcher 递归监听自动覆盖新子树，无需逐目录 AddPath）。
func TestIndexQueueEnqueueNewDirAndChild(t *testing.T) {
	q := &fakeIndexQueue{}
	wd, f, _ := setupQueueWatch(t, q)
	sub := filepath.Join(wd, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(sub) && c.Operation == "create"
	}, "新建目录未以 create 入队")

	// 递归监听由 fswatcher 接管：子目录不进入声明根集合（仅声明根被记录）。
	f.wm.mu.Lock()
	dw := f.wm.watchers[wd]
	f.wm.mu.Unlock()
	if dw == nil || hasPath(dw, sub) {
		t.Fatalf("子目录不应成为显式声明根（递归由 fswatcher 接管）")
	}

	child := filepath.Join(sub, "child.txt")
	createEmptyFile(t, child)
	waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(child) && c.Operation == "create"
	}, "子目录内新建文件未入队（新目录未被监听）")
}

// TestIndexQueueEnqueueDirRename：子目录 rename → 旧路径 rename（已不存在）/ 新路径 create
// （fswatcher Windows 后端把 RENAMED_OLD_NAME / RENAMED_NEW_NAME 均映射为 EventRename；
// processBatch 按对象是否存在归一为新 create / 旧 rename）。
func TestIndexQueueEnqueueDirRename(t *testing.T) {
	q := &fakeIndexQueue{}
	wd, _, _ := setupQueueWatch(t, q)
	oldp := filepath.Join(wd, "old")
	if err := os.Mkdir(oldp, 0o755); err != nil {
		t.Fatal(err)
	}
	waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(oldp) && c.Operation == "create"
	}, "重命名前的新目录未入队")

	newp := filepath.Join(wd, "new")
	if err := os.Rename(oldp, newp); err != nil {
		t.Fatal(err)
	}
	waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(oldp) && (c.Operation == "remove" || c.Operation == "rename")
	}, "重命名旧路径未以 remove|rename 入队")
	waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(newp) && c.Operation == "create"
	}, "重命名新路径未以 create 入队")
}

// TestIndexQueueEnqueueDirRemove：删除目录 → remove。
func TestIndexQueueEnqueueDirRemove(t *testing.T) {
	q := &fakeIndexQueue{}
	wd, _, _ := setupQueueWatch(t, q)
	dir := filepath.Join(wd, "gone")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(dir) && c.Operation == "create"
	}, "待删目录未入队")
	if err := removeWithRetry(dir); err != nil {
		t.Fatal(err)
	}
	waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(dir) && c.Operation == "remove"
	}, "删除目录未以 remove 入队")
}

// TestIndexQueueErrorDoesNotBreakChanged：队列写失败不影响 filesys.changed 广播。
func TestIndexQueueErrorDoesNotBreakChanged(t *testing.T) {
	q := &fakeIndexQueue{err: errors.New("queue down")}
	wd, _, ch := setupQueueWatch(t, q)
	p := filepath.Join(wd, "err.txt")
	createEmptyFile(t, p)
	if !waitChanged(t, ch, filepath.ToSlash(p), 5*time.Second) {
		t.Fatal("队列写失败时 filesys.changed 未广播")
	}
	waitIndex(t, q, func(c IndexChange) bool {
		return c.Path == filepath.ToSlash(p)
	}, "队列未被调用")
}

// removeWithRetry 删除目录并在 Windows 句柄短暂占用时重试（fswatcher 目录句柄
// 与 os.Remove 存在瞬时共享冲突）。
func removeWithRetry(p string) error {
	deadline := time.Now().Add(3 * time.Second)
	var err error
	for {
		if err = os.RemoveAll(p); err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

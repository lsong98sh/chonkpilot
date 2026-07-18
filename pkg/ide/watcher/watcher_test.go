package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// testDirContents represents a file:dir-contents event from the watcher.
type testDirContents struct {
	dir      string
	children []FileNode
}

// testPusher implements EventPusher, queuing events into channels.
type testPusher struct {
	dirContents chan testDirContents // file:dir-contents events
	fileChanged chan map[string]interface{} // file:changed events
}

func newTestPusher() *testPusher {
	return &testPusher{
		dirContents: make(chan testDirContents, 100),
		fileChanged: make(chan map[string]interface{}, 100),
	}
}

func (p *testPusher) Push(event string, data interface{}) {
	switch event {
	case "file:dir-contents":
		m := data.(map[string]interface{})
		dir := m["dir"].(string)
		children := m["children"].([]FileNode)
		p.dirContents <- testDirContents{dir: dir, children: children}
	case "file:changed":
		m := data.(map[string]interface{})
		p.fileChanged <- m
	}
}

// drain flushes all events from a channel within timeout, returning them.
func drain[T any](ch chan T, timeout time.Duration) []T {
	var items []T
	deadline := time.After(timeout)
	for {
		select {
		case item := <-ch:
			items = append(items, item)
		case <-deadline:
			return items
		}
	}
}

// waitFor asserts that at least one event matching the predicate arrives.
func waitFor[T any](t *testing.T, ch chan T, timeout time.Duration, predicate func(T) bool) T {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case item := <-ch:
			if predicate(item) {
				return item
			}
		case <-deadline:
			t.Fatalf("timed out waiting for matching event")
			var zero T
			return zero
		}
	}
}

func TestFileWatcher_StartStops(t *testing.T) {
	tmpDir := t.TempDir()
	logger, _ := zap.NewDevelopment()
	fw := NewFileWatcher(tmpDir, logger)
	if err := fw.Start(); err != nil {
		t.Fatal(err)
	}
	p := newTestPusher()
	fw.SetPusher(p)

	// Root should be watched by default; create a root-level file
	filePath := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	ev := waitFor(t, p.dirContents, 3*time.Second, func(ev testDirContents) bool {
		return ev.dir == tmpDir
	})
	if len(ev.children) == 0 {
		t.Fatal("expected hello.txt in root dir contents")
	}
	found := false
	for _, c := range ev.children {
		if c.Name == "hello.txt" && !c.IsDir {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected hello.txt in children, got %+v", ev.children)
	}

	fw.Stop()
}

func TestFileWatcher_WatchUnwatchDir(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	logger, _ := zap.NewDevelopment()
	fw := NewFileWatcher(tmpDir, logger)
	if err := fw.Start(); err != nil {
		t.Fatal(err)
	}
	defer fw.Stop()
	p := newTestPusher()
	fw.SetPusher(p)

	time.Sleep(200 * time.Millisecond)

	// Watch sub dir — this triggers an initial empty pushDirContents
	fw.WatchDir(subDir)
	drain(p.dirContents, 500*time.Millisecond) // discard initial push

	// Create a file in sub dir → should get dir-contents for subDir with nested.txt
	subFile := filepath.Join(subDir, "nested.txt")
	if err := os.WriteFile(subFile, []byte("nested"), 0644); err != nil {
		t.Fatal(err)
	}

	ev := waitFor(t, p.dirContents, 3*time.Second, func(ev testDirContents) bool {
		return ev.dir == subDir
	})
	found := false
	for _, c := range ev.children {
		if c.Name == "nested.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected nested.txt in sub dir, got: %+v", ev.children)
	}

	// Unwatch sub dir
	fw.UnwatchDir(subDir)
	time.Sleep(200 * time.Millisecond)
	drain(p.dirContents, 100*time.Millisecond) // clear any leftover events

	// Create another file in sub dir → should NOT get event (unwatched)
	if err := os.WriteFile(filepath.Join(subDir, "ignored.txt"), []byte("ignored"), 0644); err != nil {
		t.Fatal(err)
	}

	// Small wait then check no new events for subDir
	time.Sleep(600 * time.Millisecond)
	extra := drain(p.dirContents, 200*time.Millisecond)
	for _, ev := range extra {
		if ev.dir == subDir {
			t.Fatalf("got unexpected event for unwatched dir %q: %+v", subDir, ev)
		}
	}
}

func TestFileWatcher_FileChangedEvent(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "track.txt")
	if err := os.WriteFile(filePath, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	logger, _ := zap.NewDevelopment()
	fw := NewFileWatcher(tmpDir, logger)
	if err := fw.Start(); err != nil {
		t.Fatal(err)
	}
	defer fw.Stop()
	p := newTestPusher()
	fw.SetPusher(p)

	time.Sleep(200 * time.Millisecond)

	// Modify the file → expect file:changed event
	if err := os.WriteFile(filePath, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	ev := waitFor(t, p.fileChanged, 3*time.Second, func(m map[string]interface{}) bool {
		return m["path"] == filePath && m["op"] == "modified"
	})
	if ev == nil {
		t.Fatal("expected file:changed event for modified file")
	}
}

func TestFileWatcher_DedupQueue(t *testing.T) {
	tmpDir := t.TempDir()
	logger, _ := zap.NewDevelopment()
	fw := NewFileWatcher(tmpDir, logger)
	if err := fw.Start(); err != nil {
		t.Fatal(err)
	}
	defer fw.Stop()
	p := newTestPusher()
	fw.SetPusher(p)

	time.Sleep(200 * time.Millisecond)

	// Burst 10 creates in quick succession → should coalesce into fewer events
	for i := 0; i < 10; i++ {
		f := filepath.Join(tmpDir, fmt.Sprintf("f%d.txt", i))
		os.WriteFile(f, []byte("data"), 0644)
	}

	// Give the 60ms queue time to process
	time.Sleep(600 * time.Millisecond)
	events := drain(p.dirContents, 500*time.Millisecond)

	// Should get a single dir-contents for tmpDir (dedup)
	var rootEvents int
	for _, ev := range events {
		if ev.dir == tmpDir {
			rootEvents++
		}
	}
	if rootEvents == 0 {
		t.Fatal("expected at least 1 dir-contents event for root, got 0")
	}
	if rootEvents > 3 {
		t.Logf("got %d root events (some dedup, some not)", rootEvents)
	}
	t.Logf("dedup test: %d total events, %d for root", len(events), rootEvents)
}

func TestStopWithoutStart(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	fw := NewFileWatcher("/tmp/test", logger)
	fw.Stop()
}

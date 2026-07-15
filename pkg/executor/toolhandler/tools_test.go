package toolhandler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"go.uber.org/zap"
)

func newTestHandler(t *testing.T, workDir string) *Handler {
	t.Helper()
	h := NewHandler(workDir, workDir, "", "test-turn", zap.NewNop())
	// Set event callbacks for run_tasks / ask_user tools
	h.SetOnProgress(func(data map[string]interface{}) {})
	h.SetWriteEvent(func(evtType string, payload map[string]interface{}) {})
	return h
}

func ensureDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	ensureDir(t, dir)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func r(t *types.ToolResult) bool { return t != nil && t.Success }

// ─── 1. File tools ──────────────────────────────────────────

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "hello.txt")
	writeTestFile(t, f, "Hello, World!")
	h := newTestHandler(t, dir)

	t.Run("ok single file", func(t *testing.T) {
		res := h.Dispatch("read_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{"path": f},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success, got error=%q output=%q", res.Error, res.Output)
		}
		if !strings.Contains(res.Output, "Hello, World!") {
			t.Fatalf("expected 'Hello, World!', got %q", res.Output)
		}
	})

	t.Run("ok multiple files", func(t *testing.T) {
		f2 := filepath.Join(dir, "hello2.txt")
		writeTestFile(t, f2, "File 2")
		res := h.Dispatch("read_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{"path": f},
				map[string]interface{}{"path": f2},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success, got error=%q output=%q", res.Error, res.Output)
		}
		if !strings.Contains(res.Output, "hello.txt") || !strings.Contains(res.Output, "hello2.txt") {
			t.Fatalf("expected both files in output: %s", res.Output)
		}
	})

	t.Run("missing files", func(t *testing.T) {
		res := h.Dispatch("read_file", map[string]interface{}{}, 0)
		if r(res) {
			t.Fatal("expected failure for missing files")
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		res := h.Dispatch("read_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{"path": filepath.Join(dir, "nope.txt")},
			},
		}, 0)
		if r(res) {
			t.Fatal("expected failure for nonexistent file")
		}
	})
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir)
	f := filepath.Join(dir, "out.txt")

	t.Run("ok single file", func(t *testing.T) {
		res := h.Dispatch("write_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{"path": f, "content": "test content"},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success: %s", res.Error)
		}
		got := readTestFile(t, f)
		if got != "test content" {
			t.Fatalf("expected 'test content', got %q", got)
		}
	})

	t.Run("ok multiple files", func(t *testing.T) {
		f2 := filepath.Join(dir, "multi.txt")
		res := h.Dispatch("write_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{"path": f, "content": "overwritten"},
				map[string]interface{}{"path": f2, "content": "new file"},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success: %s", res.Error)
		}
		if readTestFile(t, f) != "overwritten" {
			t.Fatal("overwrite content mismatch")
		}
		if readTestFile(t, f2) != "new file" {
			t.Fatal("new file content mismatch")
		}
	})

	t.Run("missing files", func(t *testing.T) {
		res := h.Dispatch("write_file", map[string]interface{}{}, 0)
		if r(res) {
			t.Fatal("expected failure for missing files")
		}
	})

	t.Run("creates subdirs", func(t *testing.T) {
		deep := filepath.Join(dir, "a", "b", "c", "deep.txt")
		res := h.Dispatch("write_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{"path": deep, "content": "deep"},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success for deep path: %s", res.Error)
		}
		if readTestFile(t, deep) != "deep" {
			t.Fatal("deep file content mismatch")
		}
	})
}

// ─── 1b. Directory tools ────────────────────────────────────

func TestMakeDirectory(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir)

	t.Run("ok single", func(t *testing.T) {
		p := filepath.Join(dir, "newdir")
		res := h.Dispatch("make_directory", map[string]interface{}{
			"paths": []interface{}{p},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success: %s", res.Error)
		}
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			t.Fatal("expected directory to exist")
		}
	})

	t.Run("ok multiple", func(t *testing.T) {
		p1 := filepath.Join(dir, "a", "b")
		p2 := filepath.Join(dir, "c")
		res := h.Dispatch("make_directory", map[string]interface{}{
			"paths": []interface{}{p1, p2},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success: %s", res.Error)
		}
		for _, p := range []string{p1, p2} {
			st, err := os.Stat(p)
			if err != nil || !st.IsDir() {
				t.Fatalf("expected directory %s to exist", p)
			}
		}
	})

	t.Run("missing paths", func(t *testing.T) {
		res := h.Dispatch("make_directory", map[string]interface{}{}, 0)
		if r(res) {
			t.Fatal("expected failure for missing paths")
		}
	})
}

func TestRemoveDirectory(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir)

	t.Run("ok remove single", func(t *testing.T) {
		p := filepath.Join(dir, "toremove")
		os.MkdirAll(p, 0755)
		res := h.Dispatch("remove", map[string]interface{}{
			"paths": []interface{}{p},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success: %s", res.Error)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("expected directory to be removed")
		}
	})

	t.Run("ok remove non-empty", func(t *testing.T) {
		p := filepath.Join(dir, "nonempty")
		os.MkdirAll(filepath.Join(p, "sub"), 0755)
		os.WriteFile(filepath.Join(p, "f.txt"), []byte("x"), 0644)
		res := h.Dispatch("remove", map[string]interface{}{
			"paths": []interface{}{p},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success: %s", res.Error)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("expected non-empty directory to be removed recursively")
		}
	})

	t.Run("refuses workdir root", func(t *testing.T) {
		res := h.Dispatch("remove", map[string]interface{}{
			"paths": []interface{}{dir},
		}, 0)
		if r(res) {
			t.Fatal("expected failure for removing workspace root")
		}
	})
}

func TestRename(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir)

	t.Run("ok single rename", func(t *testing.T) {
		src := filepath.Join(dir, "old.txt")
		dst := filepath.Join(dir, "new.txt")
		os.WriteFile(src, []byte("hello"), 0644)
		res := h.Dispatch("rename", map[string]interface{}{
			"pairs": []interface{}{
				map[string]interface{}{"from": src, "to": dst},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success: %s", res.Error)
		}
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Fatal("expected source to no longer exist")
		}
		if _, err := os.Stat(dst); err != nil {
			t.Fatal("expected destination to exist")
		}
	})

	t.Run("ok multiple renames", func(t *testing.T) {
		src1 := filepath.Join(dir, "a.txt")
		src2 := filepath.Join(dir, "b.txt")
		dst1 := filepath.Join(dir, "a_renamed.txt")
		dst2 := filepath.Join(dir, "b_renamed.txt")
		os.WriteFile(src1, []byte("a"), 0644)
		os.WriteFile(src2, []byte("b"), 0644)
		res := h.Dispatch("rename", map[string]interface{}{
			"pairs": []interface{}{
				map[string]interface{}{"from": src1, "to": dst1},
				map[string]interface{}{"from": src2, "to": dst2},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("expected success: %s", res.Error)
		}
		for _, p := range []string{src1, src2} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("expected source %s to no longer exist", p)
			}
		}
		for _, p := range []string{dst1, dst2} {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("expected destination %s to exist", p)
			}
		}
	})
}

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "main.go")
	writeTestFile(t, f, "fmt.Println(\"hello\")\nfmt.Println(\"world\")\n")
	h := newTestHandler(t, dir)

	t.Run("find and replace", func(t *testing.T) {
		res := h.Dispatch("replace", map[string]interface{}{
			"path": f,
			"old":  "fmt.Println",
			"new":  "log.Println",
		}, 0)
		if !r(res) {
			t.Fatalf("expected success: %s", res.Error)
		}
		got := readTestFile(t, f)
		if !strings.Contains(got, "log.Println") {
			t.Fatalf("expected log.Println in output: %s", got)
		}
		if strings.Contains(got, "fmt.Println") {
			t.Fatalf("did not expect fmt.Println in output: %s", got)
		}
	})

	t.Run("prepend (old empty)", func(t *testing.T) {
		f2 := filepath.Join(dir, "prepend.txt")
		writeTestFile(t, f2, "world")
		res := h.Dispatch("replace", map[string]interface{}{
			"path": f2,
			"old":  "",
			"new":  "hello ",
		}, 0)
		if !r(res) {
			t.Fatalf("prepend failed: %s", res.Error)
		}
		if got := readTestFile(t, f2); got != "hello world" {
			t.Fatalf("expected 'hello world', got %q", got)
		}
	})

	t.Run("delete (new empty)", func(t *testing.T) {
		f3 := filepath.Join(dir, "delete.txt")
		writeTestFile(t, f3, "remove this keep")
		res := h.Dispatch("replace", map[string]interface{}{
			"path": f3,
			"old":  "remove this ",
			"new":  "",
		}, 0)
		if !r(res) {
			t.Fatalf("delete failed: %s", res.Error)
		}
		if got := readTestFile(t, f3); got != "keep" {
			t.Fatalf("expected 'keep', got %q", got)
		}
	})
}

func TestDiffPatch(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.txt")
	f2 := filepath.Join(dir, "b.txt")
	writeTestFile(t, f1, "line1\nline2\nline3\n")
	writeTestFile(t, f2, "line1\nline2 modified\nline3\n")
	h := newTestHandler(t, dir)

	t.Run("diff", func(t *testing.T) {
		res := h.Dispatch("diff", map[string]interface{}{"file1": f1, "file2": f2}, 0)
		if !r(res) {
			t.Fatalf("diff failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "modified") {
			t.Fatalf("expected 'modified' in diff output: %s", res.Output)
		}
	})

	t.Run("patch", func(t *testing.T) {
		target := filepath.Join(dir, "target.txt")
		writeTestFile(t, target, "aaa\nbbb\nccc\n")
		diffContent := "--- target.txt\n+++ target.txt\n@@ -1,3 +1,3 @@\n aaa\n-bbb\n+BBB\n ccc\n"
		res := h.Dispatch("patch", map[string]interface{}{"target": target, "diff": diffContent}, 0)
		if !r(res) {
			t.Fatalf("patch failed: %s", res.Error)
		}
		got := readTestFile(t, target)
		if !strings.Contains(got, "BBB") || strings.Contains(got, "bbb") {
			t.Fatalf("patch content unexpected: %s", got)
		}
	})
}

func TestGrep(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "package main\nfunc hello() {}\n")
	writeTestFile(t, filepath.Join(dir, "b.go"), "package main\nfunc world() {}\n")
	h := newTestHandler(t, dir)

	t.Run("ok", func(t *testing.T) {
		res := h.Dispatch("grep", map[string]interface{}{"pattern": "func", "path": dir}, 0)
		if !r(res) {
			t.Fatalf("grep failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "hello") || !strings.Contains(res.Output, "world") {
			t.Fatalf("expected both funcs, got: %s", res.Output)
		}
	})

	t.Run("no match", func(t *testing.T) {
		res := h.Dispatch("grep", map[string]interface{}{"pattern": "zzz_nonexistent", "path": dir}, 0)
		if !r(res) {
			t.Fatalf("grep should succeed even with no matches: %s", res.Error)
		}
	})

	t.Run("invalid regex", func(t *testing.T) {
		res := h.Dispatch("grep", map[string]interface{}{"pattern": "[invalid", "path": dir}, 0)
		if r(res) {
			t.Fatal("expected failure for invalid regex")
		}
	})
}

func TestSearchFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "main.go"), "")
	writeTestFile(t, filepath.Join(dir, "util.js"), "")
	writeTestFile(t, filepath.Join(dir, "styles.css"), "")
	h := newTestHandler(t, dir)

	t.Run("glob match", func(t *testing.T) {
		res := h.Dispatch("search_files", map[string]interface{}{"pattern": "*.go", "path": dir}, 0)
		if !r(res) {
			t.Fatalf("search_files failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "main.go") {
			t.Fatalf("expected main.go in results: %s", res.Output)
		}
		if strings.Contains(res.Output, "util.js") {
			t.Fatalf("did not expect util.js: %s", res.Output)
		}
	})

	t.Run("non-glob name", func(t *testing.T) {
		res := h.Dispatch("search_files", map[string]interface{}{"pattern": "main.go", "path": dir}, 0)
		if !r(res) {
			t.Fatalf("search_files failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "main.go") {
			t.Fatalf("expected main.go: %s", res.Output)
		}
	})
}

func TestListDirectory(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "")
	ensureDir(t, filepath.Join(dir, "subdir"))
	h := newTestHandler(t, dir)

	t.Run("ok single", func(t *testing.T) {
		res := h.Dispatch("list_directory", map[string]interface{}{
			"paths": []interface{}{dir},
		}, 0)
		if !r(res) {
			t.Fatalf("list_directory failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "a.txt") || !strings.Contains(res.Output, "subdir") {
			t.Fatalf("expected files in listing: %s", res.Output)
		}
	})

	t.Run("ok multiple", func(t *testing.T) {
		sub := filepath.Join(dir, "subdir")
		res := h.Dispatch("list_directory", map[string]interface{}{
			"paths": []interface{}{dir, sub},
		}, 0)
		if !r(res) {
			t.Fatalf("list_directory failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "a.txt") || !strings.Contains(res.Output, "subdir") {
			t.Fatalf("expected files in listing: %s", res.Output)
		}
	})

	t.Run("missing paths", func(t *testing.T) {
		res := h.Dispatch("list_directory", map[string]interface{}{}, 0)
		if r(res) {
			t.Fatal("expected failure for missing paths")
		}
	})
}

// ─── 2. Shell/process tools ─────────────────────────────────

func TestExecuteCommand(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir)

	t.Run("echo", func(t *testing.T) {
		res := h.Dispatch("execute_command", map[string]interface{}{"command": "echo hello"}, 0)
		if !r(res) {
			t.Fatalf("execute_command failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "hello") {
			t.Fatalf("expected 'hello' in output: %s", res.Output)
		}
	})

	t.Run("dir", func(t *testing.T) {
		res := h.Dispatch("execute_command", map[string]interface{}{"command": "dir /b " + dir}, 0)
		if r(res) {
			t.Logf("dir output: %s", res.Output)
		}
	})
}

func TestProcessWait(t *testing.T) {
	h := newTestHandler(t, t.TempDir())
	t.Run("ok", func(t *testing.T) {
		start := time.Now()
		res := h.Dispatch("process_wait", map[string]interface{}{"duration": float64(1)}, 0)
		elapsed := time.Since(start)
		if !r(res) {
			t.Fatalf("process_wait failed: %s", res.Error)
		}
		if elapsed < 900*time.Millisecond {
			t.Fatalf("expected ~1s wait, got %v", elapsed)
		}
	})
}

// ─── 3. Fetch tool ──────────────────────────────────────────

func TestFetch(t *testing.T) {
	h := newTestHandler(t, t.TempDir())
	t.Run("missing url", func(t *testing.T) {
		res := h.Dispatch("fetch", map[string]interface{}{}, 0)
		if r(res) {
			t.Fatal("expected failure for missing url")
		}
	})
}

// ─── 4. Note tools ──────────────────────────────────────────

func TestNoteTools(t *testing.T) {
	dir := t.TempDir()
	// init workspace
	ensureDir(t, filepath.Join(dir, ".ide"))
	h := newTestHandler(t, dir)

	t.Run("note_write", func(t *testing.T) {
		res := h.Dispatch("note_write", map[string]interface{}{
			"title":   "test-note",
			"content": "test content",
		}, 0)
		if !r(res) {
			t.Fatalf("note_write failed: %s", res.Error)
		}
	})

	t.Run("note_read", func(t *testing.T) {
		res := h.Dispatch("note_read", map[string]interface{}{"title": "test-note"}, 0)
		if !r(res) {
			t.Fatalf("note_read failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "test content") {
			t.Fatalf("expected 'test content' in note: %s", res.Output)
		}
	})

	t.Run("note_list", func(t *testing.T) {
		res := h.Dispatch("note_list", map[string]interface{}{}, 0)
		if !r(res) {
			t.Fatalf("note_list failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "test-note") {
			t.Fatalf("expected 'test-note' in note list: %s", res.Output)
		}
	})

	t.Run("note_read missing", func(t *testing.T) {
		res := h.Dispatch("note_read", map[string]interface{}{}, 0)
		if r(res) {
			t.Fatal("expected failure for missing title")
		}
	})
}

// ─── 5. Async task tools ────────────────────────────────────

func TestProcessTaskStatus(t *testing.T) {
	h := newTestHandler(t, t.TempDir())

	t.Run("unknown task", func(t *testing.T) {
		res := h.Dispatch("process_task_status", map[string]interface{}{"task_id": "nonexistent"}, 0)
		if r(res) {
			t.Fatal("expected failure for unknown task id")
		}
	})

	t.Run("missing task_id", func(t *testing.T) {
		res := h.Dispatch("process_task_status", map[string]interface{}{}, 0)
		if r(res) {
			t.Fatal("expected failure for missing task_id")
		}
	})
}

func TestProcessTaskStop(t *testing.T) {
	h := newTestHandler(t, t.TempDir())

	t.Run("unknown task", func(t *testing.T) {
		res := h.Dispatch("process_task_stop", map[string]interface{}{"task_id": "nonexistent"}, 0)
		if !r(res) {
			// Might fail or succeed depending on implementation, but should not panic
			t.Logf("process_task_stop nonexistent returned: success=%v error=%q", res.Success, res.Error)
		}
	})
}

// ─── 6. Add tool (custom tool registration) ─────────────────

func TestAddTool(t *testing.T) {
	dir := t.TempDir()
	ensureDir(t, filepath.Join(dir, ".ide"))
	h := newTestHandler(t, dir)

	t.Run("register and use", func(t *testing.T) {
		// Register
		res := h.Dispatch("add_tool", map[string]interface{}{
			"name":        "my_echo",
			"description": "echoes arg",
			"command":     "echo {input}",
		}, 0)
		if !r(res) {
			t.Fatalf("add_tool failed: %s", res.Error)
		}

		// Call the custom tool
		res2 := h.Dispatch("my_echo", map[string]interface{}{"input": "hello world"}, 0)
		if !r(res2) {
			t.Fatalf("my_echo failed: %s", res2.Error)
		}
		if !strings.Contains(res2.Output, "hello world") {
			t.Fatalf("expected 'hello world' in output: %s", res2.Output)
		}
	})
}

// ─── 7. Unknown tool ────────────────────────────────────────

func TestUnknownTool(t *testing.T) {
	h := newTestHandler(t, t.TempDir())
	res := h.Dispatch("nonexistent_tool_xyz", map[string]interface{}{}, 0)
	if r(res) {
		t.Fatal("expected failure for unknown tool")
	}
}

// ─── 8. Async execution flow (end-to-end) ───────────────────

func TestAsyncExecuteCommand(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir)

	t.Run("async then check status", func(t *testing.T) {
		// Launch async command
		res := h.Dispatch("execute_command", map[string]interface{}{
			"command": "ping -n 3 127.0.0.1",
			"async":   true,
		}, 0)
		if !r(res) {
			t.Fatalf("async execute_command failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "task_id") {
			t.Fatalf("expected task_id in output: %s", res.Output)
		}

		// Extract task_id — parse "task_id=cmd-1 status=running"
		taskID := ""
		for _, part := range strings.Fields(res.Output) {
			if strings.HasPrefix(part, "task_id=") {
				taskID = strings.TrimPrefix(part, "task_id=")
				break
			}
		}
		if taskID == "" {
			t.Fatalf("could not extract task_id from: %s", res.Output)
		}
		t.Logf("async task_id: %s", taskID)

		// Poll status with timeout
		deadline := time.Now().Add(30 * time.Second)
		completed := false
		var lastStatus string
		for time.Now().Before(deadline) {
			statusRes := h.Dispatch("process_task_status", map[string]interface{}{"task_id": taskID}, 0)
			lastStatus = statusRes.Output
			if r(statusRes) {
				if !strings.Contains(lastStatus, "running") {
					t.Logf("async task completed: %s", lastStatus)
					completed = true
					break
				}
				t.Logf("async task status: %s", lastStatus)
			}
			time.Sleep(1 * time.Second)
		}
		if !completed {
			t.Fatalf("async task did not complete within 30s, last status: %s", lastStatus)
		}
	})
}

package toolhandler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ===== grep.md / cleanup.md 测试 =====

func TestInvolveGrepOnlyFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "main.go"), "package main\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(dir, "util.js"), "function helper() {}\n")
	writeTestFile(t, filepath.Join(dir, "README.md"), "# Project\n")
	h := newTestHandler(t, dir)

	// 01: only_files with file_pattern
	t.Run("only_files_with_glob", func(t *testing.T) {
		res := h.Dispatch("grep", map[string]interface{}{
			"only_files":   true,
			"file_pattern": "*.go",
			"path":         dir,
		}, 0)
		if !r(res) {
			t.Fatalf("only_files failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "main.go") {
			t.Fatalf("expected main.go in output, got: %s", res.Output)
		}
		if strings.Contains(res.Output, "util.js") {
			t.Fatalf("expected NO .js files, got: %s", res.Output)
		}
	})

	// 02: only_files pattern treated as glob when no file_pattern
	t.Run("only_files_pattern_as_glob", func(t *testing.T) {
		res := h.Dispatch("grep", map[string]interface{}{
			"only_files": true,
			"pattern":    "*.go",
			"path":       dir,
		}, 0)
		if !r(res) {
			t.Fatalf("only_files failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "main.go") {
			t.Fatalf("expected main.go, got: %s", res.Output)
		}
	})

	// 03: max_matches alias (int from Go map literal)
	t.Run("max_matches_int", func(t *testing.T) {
		res := h.Dispatch("grep", map[string]interface{}{
			"pattern":     "func",
			"path":        dir,
			"max_matches": 1,
		}, 0)
		if !r(res) {
			t.Fatalf("max_matches failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "Found 1 matches") {
			t.Fatalf("expected 1 match, got: %s", res.Output)
		}
	})

	// 04: grep without only_files still searches content
	t.Run("normal_grep_still_works", func(t *testing.T) {
		res := h.Dispatch("grep", map[string]interface{}{
			"pattern":     "func",
			"file_pattern": "*.go",
			"path":        dir,
		}, 0)
		if !r(res) {
			t.Fatalf("grep failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "func main") {
			t.Fatalf("expected func main, got: %s", res.Output)
		}
	})
}

// ===== diff.md 测试 =====

func TestInvolveDiffFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a_v1.go"), "package main\nfunc old() {}\n")
	writeTestFile(t, filepath.Join(dir, "a_v2.go"), "package main\nfunc new() {}\n")
	writeTestFile(t, filepath.Join(dir, "b_v1.go"), "package main\nfunc foo() {}\n")
	writeTestFile(t, filepath.Join(dir, "b_v2.go"), "package main\nfunc bar() {}\n")
	h := newTestHandler(t, dir)

	// 01: files multi-pair syntax
	t.Run("files_multi_pair", func(t *testing.T) {
		res := h.Dispatch("diff", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{"path": "a_v1.go", "path2": "a_v2.go"},
				map[string]interface{}{"path": "b_v1.go", "path2": "b_v2.go"},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("diff files failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "vs") {
			t.Fatalf("expected 'vs' as pair separator, got: %s", res.Output)
		}
	})

	// 02: legacy file1/file2 still works
	t.Run("legacy_file1_file2", func(t *testing.T) {
		res := h.Dispatch("diff", map[string]interface{}{
			"file1": "a_v1.go",
			"file2": "a_v2.go",
		}, 0)
		if !r(res) {
			t.Fatalf("legacy diff failed: %s", res.Error)
		}
		// Should show differences (not identical)
		if strings.Contains(res.Output, "no differences") {
			t.Fatalf("expected differences, got: %s", res.Output)
		}
	})

	// 03: identical files shows diff headers but no hunks (no error)
	t.Run("identical_files", func(t *testing.T) {
		res := h.Dispatch("diff", map[string]interface{}{
			"file1": "a_v1.go",
			"file2": "a_v1.go",
		}, 0)
		if !r(res) {
			t.Fatalf("identical diff should succeed: %s", res.Error)
		}
		// Just check it doesn't crash - the output may have headers but no hunks
	})
}

// ===== replace.md / remove.md 测试 =====

func TestInvolveReplaceRemove(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "package main\nfunc hello() {}\n")
	writeTestFile(t, filepath.Join(dir, "b.go"), "package main\nfunc hello() {}\n")
	h := newTestHandler(t, dir)

	// 01: replace file_pattern batch
	t.Run("replace_file_pattern_batch", func(t *testing.T) {
		res := h.Dispatch("replace", map[string]interface{}{
			"file_pattern": "*.go",
			"old":          "hello",
			"new":          "world",
		}, 0)
		if !r(res) {
			t.Fatalf("replace batch failed: %s", res.Error)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "a.go"))
		if !strings.Contains(string(data), "world") {
			t.Fatalf("file content not updated: %s", string(data))
		}
	})

	// 02: replace dry_run
	t.Run("replace_dry_run", func(t *testing.T) {
		writeTestFile(t, filepath.Join(dir, "dry_test.go"), "package main\nfunc x() {}\n")
		res := h.Dispatch("replace", map[string]interface{}{
			"path":    "dry_test.go",
			"old":     "x",
			"new":     "y",
			"dry_run": true,
		}, 0)
		if !r(res) {
			t.Fatalf("replace dry_run failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "DRY RUN") {
			t.Fatalf("expected DRY RUN, got: %s", res.Output)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "dry_test.go"))
		if strings.Contains(string(data), "y") {
			t.Fatalf("dry_run modified file: %s", string(data))
		}
	})

	// 03: remove file_pattern
	t.Run("remove_file_pattern", func(t *testing.T) {
		writeTestFile(t, filepath.Join(dir, "del_me1.tmp"), "tmp")
		writeTestFile(t, filepath.Join(dir, "del_me2.tmp"), "tmp")
		writeTestFile(t, filepath.Join(dir, "keep.go"), "keep")
		res := h.Dispatch("remove", map[string]interface{}{
			"file_pattern": "*.tmp",
		}, 0)
		if !r(res) {
			t.Fatalf("remove file_pattern failed: %s", res.Error)
		}
		if _, err := os.Stat(filepath.Join(dir, "del_me1.tmp")); err == nil {
			t.Fatalf("del_me1.tmp should be removed")
		}
		if _, err := os.Stat(filepath.Join(dir, "keep.go")); err != nil {
			t.Fatalf("keep.go should remain")
		}
	})

	// 04: remove dry_run
	t.Run("remove_dry_run", func(t *testing.T) {
		writeTestFile(t, filepath.Join(dir, "safe_file.txt"), "important")
		res := h.Dispatch("remove", map[string]interface{}{
			"paths":   []interface{}{"safe_file.txt"},
			"dry_run": true,
		}, 0)
		if !r(res) {
			t.Fatalf("remove dry_run failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "DRY RUN") {
			t.Fatalf("expected DRY RUN, got: %s", res.Output)
		}
		if _, err := os.Stat(filepath.Join(dir, "safe_file.txt")); err != nil {
			t.Fatalf("dry_run deleted file")
		}
	})
}

// ===== list_directory.md 测试 =====

func TestInvolveListDirectory(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub1"), 0755)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a")
	writeTestFile(t, filepath.Join(dir, "sub1", "c.txt"), "c")
	h := newTestHandler(t, dir)

	// 01: default recursive=true shows nested files
	t.Run("default_recursive", func(t *testing.T) {
		res := h.Dispatch("list_directory", map[string]interface{}{
			"paths": []interface{}{dir},
		}, 0)
		if !r(res) {
			t.Fatalf("list_directory failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "sub1") || !strings.Contains(res.Output, "c.txt") {
			t.Fatalf("expected sub1 and c.txt in recursive output, got: %s", res.Output)
		}
	})

	// 02: recursive=false shows only top level
	t.Run("single_level", func(t *testing.T) {
		res := h.Dispatch("list_directory", map[string]interface{}{
			"paths":     []interface{}{dir},
			"recursive": false,
		}, 0)
		if !r(res) {
			t.Fatalf("list_directory recursive=false failed: %s", res.Error)
		}
		if strings.Contains(res.Output, "c.txt") {
			t.Fatalf("single level should not show nested c.txt")
		}
	})

	// 03: type=file filter shows only files
	t.Run("type_filter_file", func(t *testing.T) {
		res := h.Dispatch("list_directory", map[string]interface{}{
			"paths": []interface{}{dir},
			"type":  "file",
			"recursive": false,
		}, 0)
		if !r(res) {
			t.Fatalf("list_directory type=file failed: %s", res.Error)
		}
		// Should show a.txt but not sub1/ (which is a dir)
		if !strings.Contains(res.Output, "a.txt") {
			t.Fatalf("expected a.txt in output, got: %s", res.Output)
		}
	})
}

// ===== execute_command.md 测试 =====

func TestInvolveExecuteCommand(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "test.txt"), "hello from cwd\n")
	h := newTestHandler(t, dir)

	// 01: cwd parameter
	t.Run("cwd_parameter", func(t *testing.T) {
		res := h.Dispatch("execute_command", map[string]interface{}{
			"command": "type test.txt",
			"cwd":     dir,
		}, 0)
		if !r(res) {
			t.Fatalf("execute_command cwd failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "hello from cwd") {
			t.Fatalf("expected 'hello from cwd', got: %s", res.Output)
		}
	})

	// 02: async mode returns task_id
	t.Run("async_returns_task_id", func(t *testing.T) {
		res := h.Dispatch("execute_command", map[string]interface{}{
			"command": "echo async_works",
			"async":   true,
		}, 0)
		if !r(res) {
			t.Fatalf("execute_command async failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "task_id=") {
			t.Fatalf("expected task_id=, got: %s", res.Output)
		}
	})
}

// ===== write_file.md 测试 =====

func TestInvolveWriteFile(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir)

	// 01: template substitution
	t.Run("template", func(t *testing.T) {
		res := h.Dispatch("write_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{
					"path":    "greeting.txt",
					"content": "Hello, {{name}}!",
					"template": map[string]interface{}{"name": "World"},
				},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("write_file template failed: %s", res.Error)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "greeting.txt"))
		if string(data) != "Hello, World!" {
			t.Fatalf("template not applied: got %q", string(data))
		}
	})

	// 02: backup creates .bak
	t.Run("backup", func(t *testing.T) {
		writeTestFile(t, filepath.Join(dir, "backup_test.txt"), "original")
		res := h.Dispatch("write_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{
					"path":    "backup_test.txt",
					"content": "modified",
					"backup":  true,
				},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("write_file backup failed: %s", res.Error)
		}
		if _, err := os.Stat(filepath.Join(dir, "backup_test.txt.bak")); err != nil {
			t.Fatalf("backup file not created: %s", err)
		}
	})

	// 03: dry_run
	t.Run("dry_run", func(t *testing.T) {
		writeTestFile(t, filepath.Join(dir, "dry_test.txt"), "original")
		res := h.Dispatch("write_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{
					"path":    "dry_test.txt",
					"content": "modified",
				},
			},
			"dry_run": true,
		}, 0)
		if !r(res) {
			t.Fatalf("write_file dry_run failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "DRY RUN") {
			t.Fatalf("expected DRY RUN, got: %s", res.Output)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "dry_test.txt"))
		if string(data) != "original" {
			t.Fatalf("dry_run modified file: %q", string(data))
		}
	})
}

// ===== patch.md 测试 =====

func TestInvolvePatchMultiFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "package main\nfunc old() {}\n")
	writeTestFile(t, filepath.Join(dir, "b.go"), "package main\nfunc legacy() {}\n")
	h := newTestHandler(t, dir)

	t.Run("patch_multi_file", func(t *testing.T) {
		diff1 := "--- a.go\n+++ a.go\n@@ -1,2 +1,2 @@\n package main\n-func old() {}\n+func new() {}\n"
		diff2 := "--- b.go\n+++ b.go\n@@ -1,2 +1,2 @@\n package main\n-func legacy() {}\n+func modern() {}\n"
		res := h.Dispatch("patch", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{"path": "a.go", "diff": diff1},
				map[string]interface{}{"path": "b.go", "diff": diff2},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("patch multi-file failed: %s", res.Error)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "a.go"))
		if !strings.Contains(string(data), "func new()") {
			t.Fatalf("a.go not patched: %s", string(data))
		}
		data, _ = os.ReadFile(filepath.Join(dir, "b.go"))
		if !strings.Contains(string(data), "func modern()") {
			t.Fatalf("b.go not patched: %s", string(data))
		}
	})

	t.Run("patch_dry_run", func(t *testing.T) {
		writeTestFile(t, filepath.Join(dir, "c.go"), "package main\nfunc old() {}\n")
		diff := "--- c.go\n+++ c.go\n@@ -1,2 +1,2 @@\n package main\n-func old() {}\n+func new() {}\n"
		res := h.Dispatch("patch", map[string]interface{}{
			"target":  "c.go",
			"diff":    diff,
			"dry_run": true,
		}, 0)
		if !r(res) {
			t.Fatalf("patch dry_run failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "DRY RUN") {
			t.Fatalf("expected DRY RUN, got: %s", res.Output)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "c.go"))
		if !strings.Contains(string(data), "func old()") {
			t.Fatalf("dry_run modified: %s", string(data))
		}
	})
}

// ===== rename.md 测试 =====

func TestInvolveRenameRules(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "report_2024.txt"), "2024 data") // target for rename
	h := newTestHandler(t, dir)

	// 01: rename by file_pattern + replace
	t.Run("rename_rule_replace", func(t *testing.T) {
		res := h.Dispatch("rename", map[string]interface{}{
			"file_pattern": "*.txt",
			"replace":      map[string]interface{}{"from": "2024", "to": "2023"},
		}, 0)
		if !r(res) {
			t.Fatalf("rename rule replace failed: output=%q", res.Output)
		}
		if _, err := os.Stat(filepath.Join(dir, "report_2023.txt")); err != nil {
			t.Fatalf("renamed file not found: %s", err)
		}
	})

	// 02: rename dry_run
	t.Run("rename_dry_run", func(t *testing.T) {
		writeTestFile(t, filepath.Join(dir, "test_dry.log"), "log")
		res := h.Dispatch("rename", map[string]interface{}{
			"file_pattern": "*.log",
			"extension":    ".bak",
			"dry_run":      true,
		}, 0)
		if !r(res) {
			t.Fatalf("rename dry_run failed: output=%q", res.Output)
		}
		if !strings.Contains(res.Output, "DRY RUN") {
			t.Fatalf("expected DRY RUN, got: %s", res.Output)
		}
		if _, err := os.Stat(filepath.Join(dir, "test_dry.bak")); err == nil {
			t.Fatalf("dry_run created file")
		}
	})

	// 03: rename pairs mode still works
	t.Run("rename_pairs_mode", func(t *testing.T) {
		writeTestFile(t, filepath.Join(dir, "old_name.txt"), "data")
		res := h.Dispatch("rename", map[string]interface{}{
			"pairs": []interface{}{
				map[string]interface{}{"from": "old_name.txt", "to": "new_name.txt"},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("rename pairs failed: output=%q", res.Output)
		}
		if _, err := os.Stat(filepath.Join(dir, "new_name.txt")); err != nil {
			t.Fatalf("pairs rename failed: %s", err)
		}
	})
}

// ===== read_file.md 测试 =====

func TestInvolveReadFileFeatures(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 50)
	for i := 0; i < 50; i++ {
		lines[i] = fmt.Sprintf("Line %d", i+1)
	}
	writeTestFile(t, filepath.Join(dir, "multi_page.txt"), strings.Join(lines, "\n"))
	h := newTestHandler(t, dir)

	// 01: line_numbers shows " N  content" format
	t.Run("line_numbers", func(t *testing.T) {
		res := h.Dispatch("read_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{
					"path":         "multi_page.txt",
					"start":        1,
					"limit":        3,
					"line_numbers": true,
				},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("read_file line_numbers failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "1  Line 1") && !strings.Contains(res.Output, "1|") && !strings.Contains(res.Output, "1	") {
			t.Fatalf("expected line numbers, got: %s", res.Output[:200])
		}
	})

	// 02: tail reads last N lines
	t.Run("tail", func(t *testing.T) {
		res := h.Dispatch("read_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{
					"path": "multi_page.txt",
					"tail": 3,
				},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("read_file tail failed: %s", res.Error)
		}
		if !strings.Contains(res.Output, "Line 48") || !strings.Contains(res.Output, "Line 50") {
			t.Fatalf("expected last 3 lines, got: %s", res.Output[:200])
		}
	})

	// 03: info returns metadata only
	t.Run("info_only", func(t *testing.T) {
		res := h.Dispatch("read_file", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{
					"path": "multi_page.txt",
					"info": true,
				},
			},
		}, 0)
		if !r(res) {
			t.Fatalf("read_file info failed: %s", res.Error)
		}
		// Should show file metadata like lines, size
		if !strings.Contains(res.Output, "50行") && !strings.Contains(res.Output, "50 lines") && !strings.Contains(res.Output, "lines") {
			t.Fatalf("expected file metadata, got: %s", res.Output[:200])
		}
	})
}

// ===== execute_python_script.md 测试 =====

func TestInvolveExecutePythonScript(t *testing.T) {
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python not in PATH")
	}
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "hello.py"), "print('hello from file')\n")
	h := newTestHandler(t, dir)

	t.Run("execute_from_file", func(t *testing.T) {
		res := h.Dispatch("execute_python_script", map[string]interface{}{
			"file": filepath.Join(dir, "hello.py"),
		}, 0)
		if !r(res) {
			t.Fatalf("execute_python_script file failed: output=%q", res.Output)
		}
		if !strings.Contains(res.Output, "hello from file") {
			t.Fatalf("expected 'hello from file', got: %s", res.Output)
		}
	})

	t.Run("args_parameter", func(t *testing.T) {
		writeTestFile(t, filepath.Join(dir, "args_test.py"),
			"import sys\nprint(','.join(sys.argv[1:]))\n")
		res := h.Dispatch("execute_python_script", map[string]interface{}{
			"file": filepath.Join(dir, "args_test.py"),
			"args": []interface{}{"a", "b", "c"},
		}, 0)
		if !r(res) {
			t.Fatalf("execute_python_script args failed: output=%q", res.Output)
		}
		if !strings.Contains(res.Output, "a,b,c") {
			t.Fatalf("expected 'a,b,c', got: %s", res.Output)
		}
	})

	t.Run("async_mode", func(t *testing.T) {
		res := h.Dispatch("execute_python_script", map[string]interface{}{
			"file":  filepath.Join(dir, "hello.py"),
			"async": true,
		}, 0)
		if !r(res) {
			t.Fatalf("execute_python_script async failed: output=%q", res.Output)
		}
		if !strings.Contains(res.Output, "task_id=") {
			t.Fatalf("expected task_id=, got: %s", res.Output)
		}
	})
}

// safeSlice safely truncates a string to maxLen characters.
func safeSlice(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

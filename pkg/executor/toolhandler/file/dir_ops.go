package file

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
)

// HandleMakeDirectory creates one or more directories.
func HandleMakeDirectory(workDir string, args map[string]interface{}) *types.ToolResult {
	raw, ok := args["paths"]
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   "arguments must be a JSON array",
			Tool:    "make_directory",
		}
	}
	rawPaths, ok := raw.([]interface{})
	if !ok || len(rawPaths) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "expected a non-empty array of path strings",
			Tool:    "make_directory",
		}
	}

	var outputs []string
	var errs []string

	for _, raw := range rawPaths {
		p, ok := raw.(string)
		if !ok || p == "" {
			errs = append(errs, "invalid path (must be non-empty string)")
			continue
		}
		resolved, errMsg := resolveWritePath(p, workDir)
		if errMsg != "" {
			errs = append(errs, fmt.Sprintf("%s: %s", p, errMsg))
			continue
		}
		if err := os.MkdirAll(resolved, 0755); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", p, err))
			continue
		}
		outputs = append(outputs, p)
	}

	if len(errs) > 0 {
		result := formatMakeDirectoryOutput(outputs)
		if result != "" {
			result += "\n\n"
		}
		result += "errors:\n" + strings.Join(errs, "\n")
		return &types.ToolResult{
			Success:   false,
			Output:    result,
			Tool:      "make_directory",
			RawResult: outputs,
		}
	}
	return &types.ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("📁 已创建 %d 个目录", len(outputs)),
		Tool:      "make_directory",
		RawResult: outputs,
	}
}

// formatMakeDirectoryOutput formats the directory creation output.
// Single directory: "✅ created {path}"
// Multiple directories:
//
//	✅ 创建了 N 个目录
//	=== 创建清单 ===
//	  dir1/
//	  dir2/
//	  dir3/
func formatMakeDirectoryOutput(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	if len(dirs) == 1 {
		return fmt.Sprintf("✅ created %s", dirs[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "✅ 创建了 %d 个目录\n", len(dirs))
	b.WriteString("=== 创建清单 ===\n")
	for _, d := range dirs {
		b.WriteString("  ")
		b.WriteString(d)
		b.WriteString("/\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatRemoveOutput formats the remove operation output per remove.md spec.
//
//	Single path (no dry run):  "✅ removed {path}"
//	Multiple paths (no dry run):
//	  "✅ 已删除 N 个文件/目录
//	   === 删除清单 ===
//	     item1
//	     item2"
//	Dry run:
//	  "🔍 [DRY RUN] 将删除 N 项
//	   === 删除清单 ===
//	     item1
//	     item2
//	   （去掉 dry_run 以执行）"
func formatRemoveOutput(outputs, errs []string, dryRun bool) string {
	if len(outputs) == 0 {
		result := ""
		if len(errs) > 0 {
			result = "errors:\n" + strings.Join(errs, "\n")
		}
		return result
	}

	if dryRun {
		var b strings.Builder
		fmt.Fprintf(&b, "🔍 [DRY RUN] 将删除 %d 项", len(outputs))
		b.WriteString("\n=== 删除清单 ===\n")
		for _, o := range outputs {
			b.WriteString("  ")
			b.WriteString(o)
			b.WriteString("\n")
		}
		b.WriteString("（去掉 dry_run 以执行）")
		return strings.TrimRight(b.String(), "\n")
	}

	// Non-dry-run
	if len(outputs) == 1 {
		result := fmt.Sprintf("✅ removed %s", outputs[0])
		if len(errs) > 0 {
			result += "\nerrors:\n" + strings.Join(errs, "\n")
		}
		return result
	}

	var b strings.Builder
	fmt.Fprintf(&b, "✅ 已删除 %d 个文件/目录", len(outputs))
	b.WriteString("\n=== 删除清单 ===\n")
	for _, o := range outputs {
		b.WriteString("  ")
		b.WriteString(o)
		b.WriteString("\n")
	}
	if len(errs) > 0 {
		b.WriteString("errors:\n")
		for _, e := range errs {
			b.WriteString("  ")
			b.WriteString(e)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// snapshotDir snapshots every file under a directory tree before deletion.
func snapshotDir(versioner *fileversions.Versioner, resolved, workDir, turnID string) error {
	if versioner == nil || turnID == "" {
		return nil
	}
	return filepath.Walk(resolved, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip problematic entries
		}
		if info.IsDir() {
			return nil
		}
		snapshotBeforeWrite(versioner, path, workDir, turnID)
		return nil
	})
}

// HandleRemove removes one or more files or directories (recursively, like rm -rf).
// Supports paths (direct), file_pattern (glob), and dry_run (preview).
func HandleRemove(workDir string, versioner *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	dryRun, _ := args["dry_run"].(bool)

	// file_pattern mode: glob-based batch deletion
	if fp, ok := args["file_pattern"].(string); ok && fp != "" {
		files, err := findFilesByGlob(workDir, fp)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("file_pattern error: %s", err.Error()), Tool: "remove", RawResult: map[string]interface{}{"removed": []string{}, "errors": []string{err.Error()}}}
		}
		if len(files) == 0 {
			return &types.ToolResult{Success: true, Output: fmt.Sprintf("⚠️ 未找到匹配 '%s' 的文件。跳过。", fp), Tool: "remove", RawResult: map[string]interface{}{"removed": []string{}, "errors": []string{}}}
		}

		var outputs []string
		var errs []string
		for _, f := range files {
			rel, _ := filepath.Rel(workDir, f)
			if dryRun {
				outputs = append(outputs, rel)
			} else {
				info, statErr := os.Stat(f)
				if statErr != nil {
					errs = append(errs, fmt.Sprintf("%s: %s", rel, statErr))
					continue
				}
				if info.IsDir() {
					snapshotDir(versioner, f, workDir, turnID)
				} else {
					snapshotBeforeWrite(versioner, f, workDir, turnID)
				}
				if err := os.RemoveAll(f); err != nil {
					errs = append(errs, fmt.Sprintf("%s: %s", rel, err))
					continue
				}
				outputs = append(outputs, rel)
			}
		}
		return &types.ToolResult{
			Success: len(errs) == 0,
			Output:  formatRemoveOutput(outputs, errs, dryRun),
			Tool:    "remove",
			RawResult: map[string]interface{}{
				"removed": outputs,
				"errors":  errs,
			},
		}
	}

	// paths mode (original)
	raw, ok := args["paths"]
	if !ok {
		return &types.ToolResult{
			Success:   false,
			Error:     "arguments must be a JSON array",
			Tool:      "remove",
			RawResult: map[string]interface{}{"removed": []string{}, "errors": []string{}},
		}
	}
	rawPaths, ok := raw.([]interface{})
	if !ok || len(rawPaths) == 0 {
		return &types.ToolResult{
			Success:   false,
			Error:     "expected a non-empty array of path strings",
			Tool:      "remove",
			RawResult: map[string]interface{}{"removed": []string{}, "errors": []string{}},
		}
	}

	var outputs []string
	var errs []string

	for _, raw := range rawPaths {
		p, ok := raw.(string)
		if !ok || p == "" {
			errs = append(errs, "invalid path (must be non-empty string)")
			continue
		}
		resolved, errMsg := resolveWritePath(p, workDir)
		if errMsg != "" {
			errs = append(errs, fmt.Sprintf("%s: %s", p, errMsg))
			continue
		}

		// Guard: prevent deleting the workspace root
		if resolved == filepath.Clean(workDir) {
			errs = append(errs, fmt.Sprintf("%s: refusing to remove workspace root", p))
			continue
		}

		// Check if path exists
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", p, statErr))
			continue
		}

		if dryRun {
			outputs = append(outputs, p)
			continue
		}

		if info.IsDir() {
			// Snapshot all files in the tree before removal
			if err := snapshotDir(versioner, resolved, workDir, turnID); err != nil {
				errs = append(errs, fmt.Sprintf("%s: failed to snapshot: %s", p, err))
				continue
			}
		} else {
			// Snapshot single file before removal
			snapshotBeforeWrite(versioner, resolved, workDir, turnID)
		}

		if err := os.RemoveAll(resolved); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", p, err))
			continue
		}
		outputs = append(outputs, p)
	}

	return &types.ToolResult{
		Success: len(errs) == 0,
		Output:  formatRemoveOutput(outputs, errs, dryRun),
		Tool:    "remove",
		RawResult: map[string]interface{}{
			"removed": outputs,
			"errors":  errs,
		},
	}
}

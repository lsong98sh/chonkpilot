package file

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
)

// HandleDiff generates a unified diff between two files.
// Supports both old-style (file1+file2) and new multi-file syntax (files array).
func HandleDiff(workDir string, args map[string]interface{}) *types.ToolResult {
	format, _ := args["format"].(string)
	if format == "" {
		format = "text"
	}
	filePattern, _ := args["file_pattern"].(string)

	// Path array mode: [file1, file2] or [dir1, dir2]
	if rawPath, ok := args["path"].([]interface{}); ok && len(rawPath) == 2 {
		p1, ok1 := rawPath[0].(string)
		p2, ok2 := rawPath[1].(string)
		if ok1 && ok2 && p1 != "" && p2 != "" {
			return handlePathMode(workDir, p1, p2, format, filePattern)
		}
	}

	// New multi-pair syntax: files: [{path, path2}, ...]
	if rawFiles, ok := args["files"].([]interface{}); ok && len(rawFiles) > 0 {
		var sections []string
		var results []diffFileResult
		var hasError bool
		for _, raw := range rawFiles {
			pair, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			path1, _ := pair["path"].(string)
			path2, _ := pair["path2"].(string)
			if path1 == "" || path2 == "" {
				continue
			}
			diffText, diffLines := diffTwoFiles2(workDir, path1, path2)

			if format == "json" {
				results = append(results, makeFileResult(path1, path2, diffText, diffLines))
			} else {
				if diffText == "" {
					sections = append(sections, fmt.Sprintf("=== %s vs %s ===\n⚠️ 文件相同，无差异", path1, path2))
				} else if diffLines > 0 {
					sections = append(sections, fmt.Sprintf("=== %s vs %s ===\n📋 diff 结果 (%d 行差异)\n%s", path1, path2, diffLines, diffText))
				} else {
					sections = append(sections, fmt.Sprintf("=== %s vs %s ===\n%s", path1, path2, diffText))
				}
			}
		}
		// Build raw results for all pairs
		var rawResults []map[string]interface{}
		for _, pair := range results {
			rawResults = append(rawResults, map[string]interface{}{
				"path":       pair.Path,
				"path2":      pair.Path2,
				"status":     pair.Status,
				"diff_lines": pair.DiffLines,
				"diff":       pair.Diff,
			})
		}

		if format == "json" {
			if len(results) == 0 {
				return &types.ToolResult{Success: false, Error: "each file pair needs 'path' and 'path2'", Tool: "diff"}
			}
			jsonBytes, _ := json.Marshal(results)
			return &types.ToolResult{
				Success:   true,
				Output:    string(jsonBytes),
				Tool:      "diff",
				RawResult: map[string]interface{}{"results": rawResults},
			}
		}
		if len(sections) == 0 {
			return &types.ToolResult{Success: false, Error: "each file pair needs 'path' and 'path2'", Tool: "diff"}
		}
		output := strings.Join(sections, "\n\n")
		if hasError {
			return &types.ToolResult{
				Success:   true,
				Output:    output,
				Tool:      "diff",
				RawResult: map[string]interface{}{"results": rawResults},
			}
		}
		return &types.ToolResult{
			Success:   true,
			Output:    output,
			Tool:      "diff",
			RawResult: map[string]interface{}{"results": rawResults},
		}
	}

	// Legacy mode: file1 + file2
	file1, _ := args["file1"].(string)
	file2, _ := args["file2"].(string)
	if file1 == "" || file2 == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "provide path:[p1,p2], files:[{path,path2}], or file1+file2",
			Tool:    "diff",
			RawResult: map[string]interface{}{
				"error": "provide path, files, or file1+file2",
			},
		}
	}

	diffText, diffLines := diffTwoFiles2(workDir, file1, file2)
	status := "different"
	if diffText == "" {
		status = "identical"
	}
	rawSingle := map[string]interface{}{
		"path":       file1,
		"path2":      file2,
		"status":     status,
		"diff_lines": diffLines,
		"diff":       diffText,
	}

	if format == "json" {
		result := makeFileResult(file1, file2, diffText, diffLines)
		jsonBytes, _ := json.Marshal([]diffFileResult{result})
		return &types.ToolResult{
			Success:   true,
			Output:    string(jsonBytes),
			Tool:      "diff",
			RawResult: map[string]interface{}{"results": []map[string]interface{}{rawSingle}},
		}
	}

	if diffText == "" {
		return &types.ToolResult{
			Success:   true,
			Output:    fmt.Sprintf("⚠️ 文件相同，无差异"),
			Tool:      "diff",
			RawResult: map[string]interface{}{"results": []map[string]interface{}{rawSingle}},
		}
	}
	if diffLines > 0 {
		return &types.ToolResult{
			Success:   true,
			Output:    fmt.Sprintf("📋 diff 结果 (%d 行差异)\n%s", diffLines, diffText),
			Tool:      "diff",
			RawResult: map[string]interface{}{"results": []map[string]interface{}{rawSingle}},
		}
	}
	return &types.ToolResult{
		Success:   true,
		Output:    diffText,
		Tool:      "diff",
		RawResult: map[string]interface{}{"results": []map[string]interface{}{rawSingle}},
	}
}

// diffTwoFiles2 returns the diff text and the number of changed lines.
func diffTwoFiles2(workDir, file1, file2 string) (string, int) {
	resolved1, errMsg := resolveReadPath(file1, workDir)
	if errMsg != "" {
		return fmt.Sprintf("file1: %s", errMsg), 0
	}
	resolved2, errMsg := resolveReadPath(file2, workDir)
	if errMsg != "" {
		return fmt.Sprintf("file2: %s", errMsg), 0
	}

	data1, err := os.ReadFile(resolved1)
	if err != nil {
		return fmt.Sprintf("failed to read %s: %s", file1, err.Error()), 0
	}
	data2, err := os.ReadFile(resolved2)
	if err != nil {
		return fmt.Sprintf("failed to read %s: %s", file2, err.Error()), 0
	}

	rel1 := resolved1
	rel2 := resolved2
	if wd := workDir; wd != "" {
		if r, err := filepath.Rel(wd, file1); err == nil {
			rel1 = r
		}
		if r, err := filepath.Rel(wd, file2); err == nil {
			rel2 = r
		}
	}

	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(data1), string(data2), true)
	dmp.DiffCleanupSemantic(diffs)

	diffText := BuildUnifiedDiff(rel1, rel2, diffs, 3)

	// Count changed lines
	diffLines := 0
	for _, d := range diffs {
		if d.Type != diffmatchpatch.DiffEqual {
			diffLines += strings.Count(d.Text, "\n")
		}
	}
	if diffLines > 0 && !strings.HasSuffix(diffText, "\n") {
		// Account for last line without trailing newline
		for _, d := range diffs {
			if d.Type != diffmatchpatch.DiffEqual && d.Text != "" && !strings.HasSuffix(d.Text, "\n") {
				diffLines++
				break
			}
		}
	}

	return diffText, diffLines
}

// patchFileResult holds the result of applying a diff to one file.
type patchFileResult struct {
	Path    string `json:"path"`
	Success bool   `json:"success"`
	DryRun  bool   `json:"dry_run,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
	Error   string `json:"error,omitempty"`
}

// diffFileResult holds the result of comparing two files for JSON output.
type diffFileResult struct {
	Path      string `json:"path"`
	Path2     string `json:"path2,omitempty"`
	Status    string `json:"status"`
	DiffLines int    `json:"diff_lines,omitempty"`
	Diff      string `json:"diff,omitempty"`
}

// ignoredDirs are directories to skip when walking directories for comparison.
var ignoredDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".svn":         true,
	".hg":          true,
	".idea":        true,
	".vscode":      true,
	"__pycache__":  true,
}

// HandlePatch applies a unified diff to a file.
// Supports single-file mode (target+diff, v1 compat) and multi-file mode (files array).
func HandlePatch(workDir string, versioner *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	dryRun, _ := args["dry_run"].(bool)
	format, _ := args["format"].(string)
	if format == "" {
		format = "text"
	}

	// Multi-file mode: files array
	if rawFiles, ok := args["files"].([]interface{}); ok && len(rawFiles) > 0 {
		var results []patchFileResult

		for _, raw := range rawFiles {
			item, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			path, _ := item["path"].(string)
			diffContent, _ := item["diff"].(string)
			if path == "" || diffContent == "" {
				continue
			}

			resolved, errMsg := resolveWritePath(path, workDir)
			if errMsg != "" {
				results = append(results, patchFileResult{Path: path, Success: false, Error: errMsg})
				continue
			}

			// Read file content; treat non-existent files as empty (new file creation)
			data, readErr := os.ReadFile(resolved)
			source := ""
			if readErr == nil {
				source = string(data)
			}

			newContent, applyErr := ApplyUnifiedDiff(source, diffContent)
			if applyErr != nil {
				results = append(results, patchFileResult{Path: path, Success: false, Error: fmt.Sprintf("patch FAILED: %s", applyErr.Error())})
				continue
			}

			if dryRun {
				results = append(results, patchFileResult{Path: path, Success: true, DryRun: true, Bytes: len(newContent)})
				continue
			}

			snapshotBeforeWrite(versioner, resolved, workDir, turnID)
			if err := os.WriteFile(resolved, []byte(newContent), 0644); err != nil {
				results = append(results, patchFileResult{Path: path, Success: false, Error: fmt.Sprintf("write failed: %s", err.Error())})
				continue
			}

			results = append(results, patchFileResult{Path: path, Success: true, Bytes: len(newContent)})
		}

		// Build raw results for AI consumption
		var rawPatchResults []map[string]interface{}
		for _, r := range results {
			item := map[string]interface{}{
				"path": r.Path,
			}
			if r.Success {
				if r.DryRun {
					item["status"] = "dry_run"
					item["bytes"] = r.Bytes
				} else {
					item["status"] = "ok"
					item["bytes"] = r.Bytes
				}
			} else {
				item["status"] = "failed"
				item["error"] = r.Error
			}
			rawPatchResults = append(rawPatchResults, item)
		}

		if format == "json" {
			jsonBytes, _ := json.Marshal(results)
			return &types.ToolResult{
				Success:   true,
				Output:    string(jsonBytes),
				Tool:      "patch",
				RawResult: map[string]interface{}{"results": rawPatchResults},
			}
		}

		// Build text output
		var sections []string
		for _, r := range results {
			if r.DryRun {
				sections = append(sections, fmt.Sprintf("=== %s ===\n🔍 [DRY RUN] patch can be applied (%d bytes)", r.Path, r.Bytes))
			} else if r.Success {
				sections = append(sections, fmt.Sprintf("=== %s ===\n✅ patch applied (%d bytes)", r.Path, r.Bytes))
			} else {
				sections = append(sections, fmt.Sprintf("=== %s ===\n❌ %s", r.Path, r.Error))
			}
		}

		successCount := 0
		failCount := 0
		for _, r := range results {
			if r.Success {
				successCount++
			} else {
				failCount++
			}
		}
		output := strings.Join(sections, "\n\n")
		emoji := "✅"
		if failCount > 0 && successCount == 0 {
			emoji = "❌"
		} else if failCount > 0 {
			emoji = "⚠️"
		}
		summaryOutput := fmt.Sprintf("%s 补丁应用：%d 个成功，%d 个失败\n\n%s", emoji, successCount, failCount, output)
		return &types.ToolResult{
			Success:   true,
			Output:    summaryOutput,
			Tool:      "patch",
			RawResult: map[string]interface{}{"results": rawPatchResults},
		}
	}

	// Single-file mode (v1 compat: target + diff)
	target, _ := args["target"].(string)
	diffContent, _ := args["diff"].(string)

	if target == "" || diffContent == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "provide either files:[{path,diff}] or target+diff",
			Tool:    "patch",
			RawResult: map[string]interface{}{
				"error": "provide files or target+diff",
			},
		}
	}

	resolved, errMsg := resolveWritePath(target, workDir)
	if errMsg != "" {
		return &types.ToolResult{
			Success: false,
			Error:   errMsg,
			Tool:    "patch",
			RawResult: map[string]interface{}{
				"path":  target,
				"error": errMsg,
			},
		}
	}

	// Read file content; treat non-existent as empty
	data, readErr := os.ReadFile(resolved)
	source := ""
	if readErr == nil {
		source = string(data)
	}

	newContent, applyErr := ApplyUnifiedDiff(source, diffContent)
	if applyErr != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("patch apply failed: %s", applyErr.Error()),
			Tool:    "patch",
			RawResult: map[string]interface{}{
				"path":  target,
				"error": applyErr.Error(),
			},
		}
	}

	if dryRun {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("🔍 [DRY RUN] patch can be applied to %s (%d bytes)", target, len(newContent)),
			Tool:    "patch",
			RawResult: map[string]interface{}{
				"path":   target,
				"status": "dry_run",
				"bytes":  len(newContent),
			},
		}
	}

	snapshotBeforeWrite(versioner, resolved, workDir, turnID)
	if err := os.WriteFile(resolved, []byte(newContent), 0644); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write: %s", err.Error()),
			Tool:    "patch",
			RawResult: map[string]interface{}{
				"path":  target,
				"error": err.Error(),
			},
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("✅ patch applied to %s (%d bytes)", target, len(newContent)),
		Tool:    "patch",
		RawResult: map[string]interface{}{
			"path":  target,
			"status": "ok",
			"bytes": len(newContent),
		},
	}
}

// BuildUnifiedDiff converts diffmatchpatch diffs to unified diff format.
func BuildUnifiedDiff(fromFile, toFile string, diffs []diffmatchpatch.Diff, contextLines int) string {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("--- %s\n", fromFile))
	buf.WriteString(fmt.Sprintf("+++ %s\n", toFile))

	type lineChange struct {
		op   byte
		text string
	}

	var changes []lineChange
	for _, d := range diffs {
		lines := strings.Split(d.Text, "\n")
		for i, line := range lines {
			if i == len(lines)-1 && line == "" {
				continue
			}
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				changes = append(changes, lineChange{' ', line})
			case diffmatchpatch.DiffDelete:
				changes = append(changes, lineChange{'-', line})
			case diffmatchpatch.DiffInsert:
				changes = append(changes, lineChange{'+', line})
			}
		}
	}

	i := 0
	for i < len(changes) {
		for i < len(changes) && changes[i].op == ' ' {
			i++
		}
		if i >= len(changes) {
			break
		}

		hunkStart := i - contextLines
		if hunkStart < 0 {
			hunkStart = 0
		}

		var hunk []lineChange
		oldStart := 1
		newStart := 1
		for k := 0; k < hunkStart; k++ {
			if changes[k].op != '+' {
				oldStart++
			}
			if changes[k].op != '-' {
				newStart++
			}
		}

		for k := hunkStart; k < i; k++ {
			hunk = append(hunk, changes[k])
		}

		changeEnd := i
		for changeEnd < len(changes) && (changes[changeEnd].op != ' ' || changeEnd-i < contextLines) {
			if changeEnd-i >= contextLines && changes[changeEnd].op == ' ' {
				trailingContext := 0
				for j := changeEnd; j < len(changes) && changes[j].op == ' ' && trailingContext < contextLines; j++ {
					trailingContext++
				}
				break
			}
			changeEnd++
		}

		for k := i; k < changeEnd && k < len(changes); k++ {
			hunk = append(hunk, changes[k])
		}

		oldCount := 0
		newCount := 0
		for _, c := range hunk {
			if c.op != '+' {
				oldCount++
			}
			if c.op != '-' {
				newCount++
			}
		}

		buf.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount))

		for _, c := range hunk {
			buf.WriteString(string(c.op) + c.text + "\n")
		}

		i = changeEnd
	}

	return buf.String()
}

// ApplyUnifiedDiff parses a unified diff string and applies it to the given source text.
func ApplyUnifiedDiff(source, diffContent string) (string, error) {
	lines := strings.Split(diffContent, "\n")
	sourceLines := strings.Split(source, "\n")

	type hunk struct {
		oldStart, oldCount int
		newStart, newCount int
		lines              []string
	}

	var hunks []hunk
	var currentHunk *hunk

	for _, line := range lines {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		if strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") {
			continue
		}
		if strings.HasPrefix(line, "Binary files ") {
			continue
		}

		if strings.HasPrefix(line, "@@") {
			if currentHunk != nil {
				hunks = append(hunks, *currentHunk)
			}
			currentHunk = &hunk{}
			var oldStart, oldCount, newStart, newCount int
			n, _ := fmt.Sscanf(line, "@@ -%d,%d +%d,%d @@", &oldStart, &oldCount, &newStart, &newCount)
			if n == 4 {
				currentHunk.oldStart = oldStart
				currentHunk.oldCount = oldCount
				currentHunk.newStart = newStart
				currentHunk.newCount = newCount
			} else {
				n, _ = fmt.Sscanf(line, "@@ -%d +%d @@", &oldStart, &newStart)
				if n == 2 {
					currentHunk.oldStart = oldStart
					currentHunk.newStart = newStart
				}
			}
			continue
		}

		if currentHunk != nil {
			currentHunk.lines = append(currentHunk.lines, line)
		}
	}
	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	if len(hunks) == 0 {
		return source, fmt.Errorf("no hunks found in diff")
	}

	for i := len(hunks) - 1; i >= 0; i-- {
		h := hunks[i]
		if h.oldStart < 1 {
			continue
		}

		var result []string
		srcIdx := 0

		for srcIdx < h.oldStart-1 && srcIdx < len(sourceLines) {
			result = append(result, sourceLines[srcIdx])
			srcIdx++
		}

		hunkIdx := 0
		for hunkIdx < len(h.lines) && srcIdx < len(sourceLines) {
			line := h.lines[hunkIdx]
			if strings.HasPrefix(line, " ") {
				if srcIdx < len(sourceLines) {
					result = append(result, sourceLines[srcIdx])
					srcIdx++
				}
				hunkIdx++
			} else if strings.HasPrefix(line, "-") {
				srcIdx++
				hunkIdx++
			} else if strings.HasPrefix(line, "+") {
				result = append(result, line[1:])
				hunkIdx++
			} else {
				hunkIdx++
			}
		}

		for hunkIdx < len(h.lines) {
			line := h.lines[hunkIdx]
			if strings.HasPrefix(line, "+") {
				result = append(result, line[1:])
			}
			hunkIdx++
		}

		for srcIdx < len(sourceLines) {
			result = append(result, sourceLines[srcIdx])
			srcIdx++
		}

		sourceLines = result
	}

	return strings.Join(sourceLines, "\n"), nil
}

// makeFileResult creates a diffFileResult from two file paths and their diff output.
func makeFileResult(path1, path2, diffText string, diffLines int) diffFileResult {
	status := "different"
	if diffText == "" {
		status = "identical"
	}
	return diffFileResult{
		Path:      path1,
		Path2:     path2,
		Status:    status,
		DiffLines: diffLines,
		Diff:      diffText,
	}
}

// collectFiles walks a directory and returns sorted relative paths of all files,
// skipping common ignored directories (.git, node_modules, etc.).
func collectFiles(root string) []string {
	var files []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if info.IsDir() {
			if path != root && ignoredDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files
}

// matchFilePattern checks if relPath matches the given glob pattern.
// An empty pattern matches everything.
func matchFilePattern(pattern, relPath string) bool {
	if pattern == "" {
		return true
	}
	matched, _ := filepath.Match(pattern, relPath)
	if !matched {
		matched, _ = filepath.Match(pattern, filepath.Base(relPath))
	}
	return matched
}

// handlePathMode handles path:[p1,p2] where both are files or both are directories.
func handlePathMode(workDir, p1, p2, format, filePattern string) *types.ToolResult {
	resolved1, errMsg := resolveReadPath(p1, workDir)
	if errMsg != "" {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("path1: %s", errMsg),
			Tool:    "diff",
			RawResult: map[string]interface{}{
				"error": fmt.Sprintf("path1: %s", errMsg),
			},
		}
	}
	resolved2, errMsg := resolveReadPath(p2, workDir)
	if errMsg != "" {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("path2: %s", errMsg),
			Tool:    "diff",
			RawResult: map[string]interface{}{
				"error": fmt.Sprintf("path2: %s", errMsg),
			},
		}
	}

	fi1, err1 := os.Stat(resolved1)
	fi2, err2 := os.Stat(resolved2)
	if err1 != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("cannot access %s: %s", p1, err1.Error()),
			Tool:    "diff",
			RawResult: map[string]interface{}{
				"error": fmt.Sprintf("cannot access %s: %s", p1, err1.Error()),
			},
		}
	}
	if err2 != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("cannot access %s: %s", p2, err2.Error()),
			Tool:    "diff",
			RawResult: map[string]interface{}{
				"error": fmt.Sprintf("cannot access %s: %s", p2, err2.Error()),
			},
		}
	}

	isDir1 := fi1.IsDir()
	isDir2 := fi2.IsDir()

	if isDir1 && isDir2 {
		return diffDirectories(workDir, p1, p2, resolved1, resolved2, format, filePattern)
	} else if !isDir1 && !isDir2 {
		diffText, diffLines := diffTwoFiles2(workDir, p1, p2)
		status := "different"
		if diffText == "" {
			status = "identical"
		}
		rawSingle := map[string]interface{}{
			"path":       p1,
			"path2":      p2,
			"status":     status,
			"diff_lines": diffLines,
			"diff":       diffText,
		}
		if format == "json" {
			result := makeFileResult(p1, p2, diffText, diffLines)
			jsonBytes, _ := json.Marshal([]diffFileResult{result})
			return &types.ToolResult{
				Success:   true,
				Output:    string(jsonBytes),
				Tool:      "diff",
				RawResult: map[string]interface{}{"results": []map[string]interface{}{rawSingle}},
			}
		}
		if diffText == "" {
			return &types.ToolResult{
				Success:   true,
				Output:    fmt.Sprintf("=== %s vs %s ===\n⚠️ 文件相同，无差异", p1, p2),
				Tool:      "diff",
				RawResult: map[string]interface{}{"results": []map[string]interface{}{rawSingle}},
			}
		}
		if diffLines > 0 {
			return &types.ToolResult{
				Success:   true,
				Output:    fmt.Sprintf("=== %s vs %s ===\n📋 diff 结果 (%d 行差异)\n%s", p1, p2, diffLines, diffText),
				Tool:      "diff",
				RawResult: map[string]interface{}{"results": []map[string]interface{}{rawSingle}},
			}
		}
		return &types.ToolResult{
			Success:   true,
			Output:    fmt.Sprintf("=== %s vs %s ===\n%s", p1, p2, diffText),
			Tool:      "diff",
			RawResult: map[string]interface{}{"results": []map[string]interface{}{rawSingle}},
		}
	}

	kind1 := "file"
	if isDir1 {
		kind1 = "directory"
	}
	kind2 := "file"
	if isDir2 {
		kind2 = "directory"
	}
	return &types.ToolResult{
		Success: false,
		Error:   fmt.Sprintf("path types must match: %s is %s, %s is %s", p1, kind1, p2, kind2),
		Tool:    "diff",
		RawResult: map[string]interface{}{
			"error": fmt.Sprintf("path types must match: %s is %s, %s is %s", p1, kind1, p2, kind2),
		},
	}
}

// diffDirectories compares two directories and returns diff output for each paired file.
func diffDirectories(workDir, p1, p2, resolved1, resolved2, format, filePattern string) *types.ToolResult {
	files1 := collectFiles(resolved1)
	files2 := collectFiles(resolved2)

	set1 := make(map[string]bool, len(files1))
	for _, f := range files1 {
		set1[f] = true
	}
	set2 := make(map[string]bool, len(files2))
	for _, f := range files2 {
		set2[f] = true
	}

	// Union of all files
	allSet := make(map[string]bool)
	for f := range set1 {
		allSet[f] = true
	}
	for f := range set2 {
		allSet[f] = true
	}

	var allFiles []string
	for f := range allSet {
		allFiles = append(allFiles, f)
	}
	sort.Strings(allFiles)

	var results []diffFileResult
	var sections []string
	totalFiles := 0
	diffFiles := 0
	addedFiles := 0
	deletedFiles := 0

	for _, relPath := range allFiles {
		if !matchFilePattern(filePattern, relPath) {
			continue
		}

		totalFiles++
		path1 := filepath.ToSlash(filepath.Join(p1, relPath))
		path2 := filepath.ToSlash(filepath.Join(p2, relPath))

		if set1[relPath] && set2[relPath] {
			// File exists in both directories — perform diff
			diffText, diffLines := diffTwoFiles2(workDir, path1, path2)

			if format == "json" {
				results = append(results, makeFileResult(path1, path2, diffText, diffLines))
			} else {
				if diffText == "" {
					sections = append(sections, fmt.Sprintf("=== %s vs %s ===\n⚠️ 文件相同，无差异", path1, path2))
				} else {
					diffFiles++
					sections = append(sections, fmt.Sprintf("=== %s vs %s ===\n📋 diff 结果 (%d 行差异)\n%s", path1, path2, diffLines, diffText))
				}
			}
		} else if set2[relPath] {
			// File only exists in dir2 — added
			addedFiles++
			if format == "json" {
				results = append(results, diffFileResult{Path: "", Path2: path2, Status: "added"})
			} else {
				sections = append(sections, fmt.Sprintf("=== %s vs (added) ===\n✅ 文件新增: %s", path2, relPath))
			}
		} else {
			// File only exists in dir1 — deleted
			deletedFiles++
			if format == "json" {
				results = append(results, diffFileResult{Path: path1, Path2: "", Status: "deleted"})
			} else {
				sections = append(sections, fmt.Sprintf("=== %s vs (deleted) ===\n❌ 文件已删除: %s", path1, relPath))
			}
		}
	}

	// Build raw results
	var rawResults []map[string]interface{}
	for _, r := range results {
		rawResults = append(rawResults, map[string]interface{}{
			"path":       r.Path,
			"path2":      r.Path2,
			"status":     r.Status,
			"diff_lines": r.DiffLines,
			"diff":       r.Diff,
		})
	}

	if format == "json" {
		jsonBytes, _ := json.Marshal(results)
		return &types.ToolResult{
			Success:   true,
			Output:    string(jsonBytes),
			Tool:      "diff",
			RawResult: map[string]interface{}{"results": rawResults},
		}
	}

	header := fmt.Sprintf("📋 目录对比 — %s vs %s", p1, p2)
	summary := fmt.Sprintf("=== 统计 ===\n  总文件: %d, 差异文件: %d, 新增: %d, 删除: %d", totalFiles, diffFiles, addedFiles, deletedFiles)

	if len(sections) == 0 {
		output := fmt.Sprintf("%s\n\n%s\n\n%s", header, "  (无匹配文件)", summary)
		return &types.ToolResult{
			Success:   true,
			Output:    output,
			Tool:      "diff",
			RawResult: map[string]interface{}{"results": rawResults},
		}
	}

	output := header + "\n\n" + strings.Join(sections, "\n\n") + "\n\n" + summary
	return &types.ToolResult{
		Success:   true,
		Output:    output,
		Tool:      "diff",
		RawResult: map[string]interface{}{"results": rawResults},
	}
}

func init() {
	types.RegisterSimplify("diff", simplifyDiff)
	types.RegisterSimplify("patch", types.SimpleAction("patch"))
}

func simplifyDiff(argsJSON json.RawMessage, result string) string {
	var a struct {
		File1 string `json:"file1"`
		File2 string `json:"file2"`
		Path  []string `json:"path"`
		Files []struct {
			Path  string `json:"path"`
			Path2 string `json:"path2"`
		} `json:"files"`
	}
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "diff"
	}
	if len(a.Path) == 2 {
		return fmt.Sprintf("diff(%s, %s)", a.Path[0], a.Path[1])
	}
	if len(a.Files) > 0 {
		return fmt.Sprintf("diff(%d pairs)", len(a.Files))
	}
	return fmt.Sprintf("diff(%s, %s)", a.File1, a.File2)
}

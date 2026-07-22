package history

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/file"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
)

// ──────── history_rollback ────────

// HandleRollback restores files to a previous state.
// Accepts turn_id (required), paths (optional), file_pattern (optional),
// version (optional), dry_run (optional).
func HandleRollback(v *fileversions.Versioner, workDir, defaultTurnID string, args map[string]interface{}) *types.ToolResult {
	if v == nil {
		return &types.ToolResult{Success: false, Error: "file versioning is not available (history.db not initialized)", Tool: "history_rollback", RawResult: map[string]interface{}{}}
	}

	turnID := resolveTurnID(args, defaultTurnID)
	if turnID == "" {
		return &types.ToolResult{Success: false, Error: "turn_id is required and no active turn", Tool: "history_rollback", RawResult: map[string]interface{}{}}
	}

	dryRun, _ := args["dry_run"].(bool)
	versionArg, hasVersion := args["version"]
	filePattern, hasPattern := args["file_pattern"].(string)

	// If version is specified, roll back to that specific version
	if hasVersion {
		ver, err := toInt(versionArg)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("invalid version: %v", err), Tool: "history_rollback", RawResult: map[string]interface{}{}}
		}
		return rollbackToVersion(v, turnID, ver, dryRun)
	}

	// If file_pattern is specified, filter by glob
	if hasPattern && filePattern != "" {
		return rollbackByPattern(v, workDir, turnID, filePattern, dryRun)
	}

	// If paths are specified, restore specific paths
	rawPaths, hasPaths := args["paths"]
	if hasPaths && rawPaths != nil {
		return rollbackPaths(v, turnID, rawPaths)
	}

	// No paths, no version, no pattern: restore all
	return rollbackAll(v, turnID, dryRun)
}

func rollbackToVersion(v *fileversions.Versioner, turnID string, version int, dryRun bool) *types.ToolResult {
	vc, err := v.Take(turnID, version)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("query version: %v", err), Tool: "history_rollback", RawResult: map[string]interface{}{"turn_id": turnID, "version": version}}
	}
	if vc == nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("turn %s version %d not found", turnID, version), Tool: "history_rollback", RawResult: map[string]interface{}{"turn_id": turnID, "version": version}}
	}

	if dryRun {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("🔍 [DRY RUN] Would restore: %s → %s v%d (%d bytes)\n=== %s ===\n%s", vc.FilePath, turnID, version, len(vc.Content), vc.FilePath, string(vc.Content)),
			Tool:    "history_rollback",
			RawResult: map[string]interface{}{
				"restored": []string{vc.FilePath},
				"errors":   []string{},
				"dry_run":  true,
			},
		}
	}

	// Write content back
	if err := v.RestoreVersion(vc.ID); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("restore version: %v", err), Tool: "history_rollback", RawResult: map[string]interface{}{"turn_id": turnID, "version": version, "path": vc.FilePath}}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("↩️ 已恢复：%s", vc.FilePath),
		Tool:    "history_rollback",
		RawResult: map[string]interface{}{
			"restored": []string{vc.FilePath},
			"errors":   []string{},
		},
	}
}

func rollbackByPattern(v *fileversions.Versioner, workDir, turnID, pattern string, dryRun bool) *types.ToolResult {
	versions, err := v.List(turnID)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("query turn versions: %v", err), Tool: "history_rollback", RawResult: map[string]interface{}{"turn_id": turnID}}
	}

	records := versions[turnID]
	if len(records) == 0 {
		return &types.ToolResult{Success: true, Output: "No versions found for this turn.", Tool: "history_rollback", RawResult: map[string]interface{}{"restored": []string{}, "errors": []string{}}}
	}

	// Normalize pattern to forward slashes for consistency
	pattern = strings.ReplaceAll(pattern, "\\", "/")

	var matched []fileversions.VersionRecord
	seen := make(map[string]bool) // unique by file_uid to avoid duplicates

	for _, vr := range records {
		if seen[vr.FileUID] {
			continue
		}
		relPath := strings.ReplaceAll(vr.FilePath, "\\", "/")
		if matchedGlob := matchGlob(relPath, pattern); matchedGlob {
			seen[vr.FileUID] = true
			matched = append(matched, vr)
		}
	}

	if len(matched) == 0 {
		return &types.ToolResult{Success: true, Output: fmt.Sprintf("No files match pattern %q in turn %s", pattern, turnID), Tool: "history_rollback", RawResult: map[string]interface{}{"restored": []string{}, "errors": []string{}}}
	}

	var restored, skipped, errs []string
	for _, vr := range matched {
		if dryRun {
			skipped = append(skipped, fmt.Sprintf("🔍 %s (would restore)", vr.FilePath))
			continue
		}
		n, err := v.RestoreFileAndDelete(turnID, vr.FilePath)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", vr.FilePath, err))
		} else if n > 0 {
			restored = append(restored, vr.FilePath)
		} else {
			skipped = append(skipped, vr.FilePath)
		}
	}

	return formatRollbackResult("history_rollback", restored, skipped, errs)
}

func rollbackPaths(v *fileversions.Versioner, turnID string, rawPaths interface{}) *types.ToolResult {
	p, ok := rawPaths.([]interface{})
	if !ok {
		return &types.ToolResult{Success: false, Error: "paths must be an array of strings", Tool: "history_rollback", RawResult: map[string]interface{}{"restored": []string{}, "errors": []string{}}}
	}
	if len(p) == 0 {
		return rollbackAll(v, turnID, false)
	}

	var allRestored, allSkipped, allErrs []string
	for _, item := range p {
		s, ok := item.(string)
		if !ok || s == "" {
			allErrs = append(allErrs, "invalid path (must be non-empty string)")
			continue
		}
		restored, skipped, errs := rollbackSinglePath(v, turnID, s)
		allRestored = append(allRestored, restored...)
		allSkipped = append(allSkipped, skipped...)
		allErrs = append(allErrs, errs...)
	}
	return formatRollbackResult("history_rollback", allRestored, allSkipped, allErrs)
}

func rollbackSinglePath(v *fileversions.Versioner, turnID, path string) (restored, skipped, errs []string) {
	n, err := v.RestoreFileAndDelete(turnID, path)
	if err != nil {
		errs = append(errs, fmt.Sprintf("%s: %v", path, err))
		return
	}
	if n > 0 {
		restored = append(restored, path)
		return
	}

	// Try as directory prefix
	prefix := path
	if !strings.HasSuffix(prefix, "/") && !strings.HasSuffix(prefix, "\\") {
		prefix = prefix + "/"
	}
	versions, err := v.GetTurnVersionsByPathPrefix(turnID, prefix)
	if err != nil || len(versions) == 0 {
		skipped = append(skipped, path)
		return
	}

	for _, vr := range versions {
		n, err := v.RestoreFileAndDelete(turnID, vr.FilePath)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", vr.FilePath, err))
		} else if n > 0 {
			restored = append(restored, vr.FilePath)
		}
	}
	if len(restored) == 0 {
		skipped = append(skipped, path)
	}
	return
}

func rollbackAll(v *fileversions.Versioner, turnID string, dryRun bool) *types.ToolResult {
	if dryRun {
		versions, err := v.List(turnID)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("query turn versions: %v", err), Tool: "history_rollback", RawResult: map[string]interface{}{"turn_id": turnID}}
		}
		records := versions[turnID]
		if len(records) == 0 {
			return &types.ToolResult{Success: true, Output: "🔍 [DRY RUN] No files to restore for this turn.", Tool: "history_rollback", RawResult: map[string]interface{}{"restored": []string{}, "errors": []string{}, "dry_run": true}}
		}
		seen := make(map[string]bool)
		var paths []string
		for _, vr := range records {
			if seen[vr.FileUID] {
				continue
			}
			seen[vr.FileUID] = true
			paths = append(paths, vr.FilePath)
		}
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("🔍 [DRY RUN] Would restore %d file(s):\n  %s", len(paths), strings.Join(paths, "\n  ")),
			Tool:    "history_rollback",
			RawResult: map[string]interface{}{
				"restored": paths,
				"errors":   []string{},
				"dry_run":  true,
			},
		}
	}

	n, errs := v.RestoreTurnAndDelete(turnID)
	if len(errs) > 0 {
		var parts []string
		for _, e := range errs {
			parts = append(parts, e.Error())
		}
		var errMsgs []string
		for _, e := range errs {
			errMsgs = append(errMsgs, e.Error())
		}
		return &types.ToolResult{
			Success: false,
			Output:  fmt.Sprintf("❌ restored %d files with %d error(s): %s", n, len(errs), strings.Join(parts, "; ")),
			Tool:    "history_rollback",
			RawResult: map[string]interface{}{
				"restored": []string{},
				"errors":   errMsgs,
			},
		}
	}
	if n == 0 {
		return &types.ToolResult{
			Success: true,
			Output:  "No files to restore — no snapshots found for this turn.",
			Tool:    "history_rollback",
			RawResult: map[string]interface{}{
				"restored": []string{},
				"errors":   []string{},
			},
		}
	}
	return &types.ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("↩️ 已恢复 %d 个文件", n),
		Tool:      "history_rollback",
		RawResult: map[string]interface{}{"restored": []string{}, "errors": []string{}, "count": n},
	}
}

func formatRollbackResult(tool string, restored, skipped, errs []string) *types.ToolResult {
	var parts []string
	if len(restored) > 0 {
		parts = append(parts, fmt.Sprintf("✅ restored: [%s]", strings.Join(restored, ", ")))
	}
	if len(skipped) > 0 {
		parts = append(parts, fmt.Sprintf("skipped: [%s]", strings.Join(skipped, ", ")))
	}
	if len(errs) > 0 {
		parts = append(parts, fmt.Sprintf("❌ errors: [%s]", strings.Join(errs, "; ")))
	}

	var errStrs []string
	for _, e := range errs {
		errStrs = append(errStrs, e)
	}

	if len(errs) > 0 {
		return &types.ToolResult{
			Success: false,
			Output:  strings.Join(parts, "\n"),
			Tool:    tool,
			RawResult: map[string]interface{}{
				"restored": restored,
				"errors":   errStrs,
			},
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("↩️ 已恢复 %d 个文件", len(restored)),
		Tool:    tool,
		RawResult: map[string]interface{}{
			"restored": restored,
			"errors":   []string{},
		},
	}
}

// ──────── history_put ────────

// HandlePut saves the current file content as a versioned snapshot.
// Accepts: path (required), turn_id (optional, uses current if omitted).
func HandlePut(v *fileversions.Versioner, defaultTurnID string, args map[string]interface{}) *types.ToolResult {
	if v == nil {
		return &types.ToolResult{Success: false, Error: "file versioning is not available (history.db not initialized)", Tool: "history_put", RawResult: map[string]interface{}{}}
	}

	path, _ := args["path"].(string)
	if path == "" {
		return &types.ToolResult{Success: false, Error: "path is required", Tool: "history_put", RawResult: map[string]interface{}{}}
	}

	turnID := resolveTurnID(args, defaultTurnID)
	if turnID == "" {
		return &types.ToolResult{Success: false, Error: "no active turn", Tool: "history_put", RawResult: map[string]interface{}{"path": path}}
	}

	ver, err := v.Put(turnID, path)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to archive: %v", err), Tool: "history_put", RawResult: map[string]interface{}{"path": path, "turn_id": turnID}}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("💾 已存档：%s → v%d", path, ver),
		Tool:    "history_put",
		RawResult: map[string]interface{}{
			"path":    path,
			"turn_id": turnID,
			"version": ver,
		},
	}
}

// ──────── history_take ────────

// HandleTake retrieves version content for preview (no disk write).
// Accepts: turn_id (required), version (required).
func HandleTake(v *fileversions.Versioner, args map[string]interface{}) *types.ToolResult {
	if v == nil {
		return &types.ToolResult{Success: false, Error: "file versioning is not available (history.db not initialized)", Tool: "history_take", RawResult: map[string]interface{}{}}
	}

	turnID, _ := args["turn_id"].(string)
	if turnID == "" {
		return &types.ToolResult{Success: false, Error: "turn_id is required", Tool: "history_take", RawResult: map[string]interface{}{}}
	}

	versionArg, hasVersion := args["version"]
	if !hasVersion {
		return &types.ToolResult{Success: false, Error: "version is required", Tool: "history_take", RawResult: map[string]interface{}{"turn_id": turnID}}
	}

	ver, err := toInt(versionArg)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("invalid version: %v", err), Tool: "history_take", RawResult: map[string]interface{}{"turn_id": turnID, "version": versionArg}}
	}

	vc, err := v.Take(turnID, ver)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("query version: %v", err), Tool: "history_take", RawResult: map[string]interface{}{"turn_id": turnID, "version": ver}}
	}
	if vc == nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("%s v%d not found", turnID, ver), Tool: "history_take", RawResult: map[string]interface{}{"turn_id": turnID, "version": ver}}
	}

	// Parse and reformat timestamp for readability
	ts := vc.CreatedAt
	if t, err := time.Parse(time.RFC3339, vc.CreatedAt); err == nil {
		ts = t.Format("2006-01-02 15:04:05")
	}

	output := fmt.Sprintf("📋 %s v%d — %s (%s)\n══════════════════════════\n%s\n══════════════════════════",
		turnID, ver, vc.FilePath, ts, string(vc.Content))

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Tool:    "history_take",
		RawResult: map[string]interface{}{
			"turn_id":    turnID,
			"version":    ver,
			"path":       vc.FilePath,
			"content":    string(vc.Content),
			"created_at": ts,
		},
	}
}

// ──────── history_diff ────────

// HandleDiff compares two versions within the same turn.
// Accepts: turn_id (required), version_a (required), version_b (required).
func HandleDiff(v *fileversions.Versioner, args map[string]interface{}) *types.ToolResult {
	if v == nil {
		return &types.ToolResult{Success: false, Error: "file versioning is not available (history.db not initialized)", Tool: "history_diff", RawResult: map[string]interface{}{}}
	}

	turnID, _ := args["turn_id"].(string)
	if turnID == "" {
		return &types.ToolResult{Success: false, Error: "turn_id is required", Tool: "history_diff", RawResult: map[string]interface{}{}}
	}

	verA, errA := toInt(args["version_a"])
	verB, errB := toInt(args["version_b"])
	if errA != nil || errB != nil {
		return &types.ToolResult{Success: false, Error: "version_a and version_b are required (must be integers)", Tool: "history_diff", RawResult: map[string]interface{}{"turn_id": turnID}}
	}

	vcA, err := v.Take(turnID, verA)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("query version %d: %v", verA, err), Tool: "history_diff", RawResult: map[string]interface{}{"turn_id": turnID, "version_a": verA, "version_b": verB}}
	}
	vcB, err := v.Take(turnID, verB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("query version %d: %v", verB, err), Tool: "history_diff", RawResult: map[string]interface{}{"turn_id": turnID, "version_a": verA, "version_b": verB}}
	}

	if vcA == nil || vcB == nil {
		missing := verA
		if vcA == nil && vcB != nil {
			missing = verA
		} else if vcB == nil && vcA != nil {
			missing = verB
		} else {
			missing = 0 // both nil
		}
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("version %d not found in %s", missing, turnID), Tool: "history_diff", RawResult: map[string]interface{}{"turn_id": turnID, "version_a": verA, "version_b": verB}}
	}

	// Use the same file path for diff labels since both versions are of the same file
	filePath := vcA.FilePath

	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(vcA.Content), string(vcB.Content), true)
	dmp.DiffCleanupSemantic(diffs)

	diffText := file.BuildUnifiedDiff(
		fmt.Sprintf("v%d", verA),
		fmt.Sprintf("v%d", verB),
		diffs, 3,
	)

	// Count changed lines
	diffLines := 0
	for _, d := range diffs {
		if d.Type != diffmatchpatch.DiffEqual {
			diffLines += strings.Count(d.Text, "\n")
		}
	}

	output := fmt.Sprintf("📋 %s v%d vs v%d — %s\n%s", turnID, verA, verB, filePath, diffText)
	if diffLines == 0 {
		output = fmt.Sprintf("📋 %s v%d vs v%d — %s\n⚠️ 文件相同，无差异", turnID, verA, verB, filePath)
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Tool:    "history_diff",
		RawResult: map[string]interface{}{
			"turn_id":    turnID,
			"version_a":  verA,
			"version_b":  verB,
			"path":       filePath,
			"diff":       diffText,
			"diff_lines": diffLines,
		},
	}
}

// ──────── history_list ────────

// HandleList lists all version records, optionally filtered by turn_id.
// Accepts: turn_id (optional, lists all turns if omitted).
func HandleList(v *fileversions.Versioner, args map[string]interface{}) *types.ToolResult {
	if v == nil {
		return &types.ToolResult{Success: false, Error: "file versioning is not available (history.db not initialized)", Tool: "history_list", RawResult: map[string]interface{}{}}
	}

	turnID, _ := args["turn_id"].(string)

	result, err := v.List(turnID)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("query versions: %v", err), Tool: "history_list", RawResult: map[string]interface{}{}}
	}

	if len(result) == 0 {
		return &types.ToolResult{Success: true, Output: "📋 版本历史\n(empty)", Tool: "history_list", RawResult: map[string]interface{}{"turns": map[string]interface{}{}}}
	}

	// Sort turn IDs for consistent output
	var turnIDs []string
	for t := range result {
		turnIDs = append(turnIDs, t)
	}
	sort.Strings(turnIDs)

	rawTurns := make(map[string][]map[string]interface{})
	totalVersions := 0

	var sections []string
	sections = append(sections, "📋 版本历史")
	sections = append(sections, "")

	for _, t := range turnIDs {
		records := result[t]
		if len(records) == 0 {
			continue
		}
		sections = append(sections, fmt.Sprintf("=== %s ===", t))
		var turnRecords []map[string]interface{}
		for _, vr := range records {
			ts := vr.CreatedAt
			if parsed, err := time.Parse(time.RFC3339, vr.CreatedAt); err == nil {
				ts = parsed.Format("2006-01-02 15:04:05")
			}
			verStr := fmt.Sprintf("v%d", vr.Version)
			if vr.Version < 0 {
				verStr = "snap"
			}
			sections = append(sections, fmt.Sprintf("  %s  %s  %s", verStr, vr.FilePath, ts))
			turnRecords = append(turnRecords, map[string]interface{}{
				"version":    vr.Version,
				"path":       vr.FilePath,
				"created_at": ts,
			})
			totalVersions++
		}
		rawTurns[t] = turnRecords
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📋 版本历史：%d 个 turn，%d 个版本", len(rawTurns), totalVersions),
		Tool:    "history_list",
		RawResult: map[string]interface{}{
			"turns": rawTurns,
		},
	}
}

// ──────── Helpers ────────

// resolveTurnID extracts turn_id from args, falling back to defaultTurnID.
func resolveTurnID(args map[string]interface{}, defaultTurnID string) string {
	if tid, ok := args["turn_id"].(string); ok && tid != "" {
		return tid
	}
	return defaultTurnID
}

// toInt converts an interface{} to int, supporting float64 (JSON numbers) and int.
func toInt(v interface{}) (int, error) {
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case int64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("expected number, got %T", v)
	}
}

// matchGlob checks if a path matches a glob pattern.
// Supports basic glob patterns via filepath.Match, and ** for globstar.
func matchGlob(path, pattern string) bool {
	// Handle ** (globstar) recursively
	if strings.Contains(pattern, "**") {
		prefix, suffix, found := strings.Cut(pattern, "**")
		if !found {
			return false
		}
		prefix = strings.TrimSuffix(prefix, "/")
		suffix = strings.TrimPrefix(suffix, "/")

		// Check prefix match
		if prefix != "" {
			matched, _ := filepath.Match(prefix, path)
			if !matched && !strings.HasPrefix(path, prefix+"/") {
				return false
			}
		}

		// Check suffix match
		if suffix == "" {
			return true
		}

		// Try remaining path and each subdirectory
		remaining := path
		if prefix != "" && strings.HasPrefix(path, prefix+"/") {
			remaining = strings.TrimPrefix(path, prefix+"/")
		}
		if matched, _ := filepath.Match(suffix, remaining); matched {
			return true
		}
		parts := strings.Split(remaining, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			subPath := strings.Join(parts[i:], "/")
			if matched, _ := filepath.Match(suffix, subPath); matched {
				return true
			}
		}
		return false
	}

	matched, _ := filepath.Match(pattern, path)
	return matched
}

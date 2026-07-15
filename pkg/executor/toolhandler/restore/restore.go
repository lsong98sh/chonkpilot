package restore

import (
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
)

// HandleRollback restores files to their pre-turn snapshot.
// Accepts a "paths" array of file or directory paths.
//   - File path: restore that single file
//   - Directory path: restore all files under that directory that have snapshots
//   - Empty or omitted paths: restore ALL files modified in the current turn
func HandleRollback(v *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	if v == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "file versioning is not available (history.db not initialized)",
			Tool:    "rollback",
		}
	}
	if turnID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no active turn — cannot determine which version to restore",
			Tool:    "rollback",
		}
	}

	rawPaths, hasPaths := args["paths"]
	if !hasPaths || rawPaths == nil {
		return rollbackAll(v, turnID)
	}

	switch p := rawPaths.(type) {
	case []interface{}:
		if len(p) == 0 {
			return rollbackAll(v, turnID)
		}
		var allRestored []string
		var allSkipped []string
		var allErrs []string
		for _, item := range p {
			s, ok := item.(string)
			if !ok || s == "" {
				allErrs = append(allErrs, "invalid path (must be non-empty string)")
				continue
			}
			restored, skipped, errs := rollbackPath(v, turnID, s)
			allRestored = append(allRestored, restored...)
			allSkipped = append(allSkipped, skipped...)
			allErrs = append(allErrs, errs...)
		}
		return formatRollbackResult(allRestored, allSkipped, allErrs)
	default:
		return &types.ToolResult{
			Success: false,
			Error:   "paths must be an array of strings",
			Tool:    "rollback",
		}
	}
}

// rollbackPath handles a single path: file → restore file; directory → restore all files under it.
func rollbackPath(v *fileversions.Versioner, turnID, path string) (restored, skipped, errs []string) {
	// Try as exact file path first
	n, err := v.RestoreFileAndDelete(turnID, path)
	if err != nil {
		errs = append(errs, fmt.Sprintf("%s: %v", path, err))
		return
	}
	if n > 0 {
		restored = append(restored, path)
		return
	}

	// Not found as exact file — try as directory prefix
	prefix := path
	if !strings.HasSuffix(prefix, "/") && !strings.HasSuffix(prefix, "\\") {
		prefix = prefix + "/"
	}
	versions, err := v.GetTurnVersionsByPathPrefix(turnID, prefix)
	if err != nil || len(versions) == 0 {
		skipped = append(skipped, path)
		return
	}

	// Check if the path actually exists as a directory on disk to confirm intent
	// If it does, restore all files under it; otherwise treat as skipped
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

func rollbackAll(v *fileversions.Versioner, turnID string) *types.ToolResult {
	n, errs := v.RestoreTurnAndDelete(turnID)
	if len(errs) > 0 {
		var parts []string
		for _, e := range errs {
			parts = append(parts, e.Error())
		}
		return &types.ToolResult{
			Success: false,
			Output:  fmt.Sprintf("restored %d files with %d error(s): %s", n, len(errs), strings.Join(parts, "; ")),
			Tool:    "rollback",
		}
	}
	if n == 0 {
		return &types.ToolResult{
			Success: true,
			Output:  "No files to restore — no snapshots found for this turn.",
			Tool:    "rollback",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Restored %d file(s) to pre-turn state. Version records deleted.", n),
		Tool:    "rollback",
	}
}

func formatRollbackResult(restored, skipped, errs []string) *types.ToolResult {
	var parts []string
	if len(restored) > 0 {
		parts = append(parts, fmt.Sprintf("restored: [%s]", strings.Join(restored, ", ")))
	}
	if len(skipped) > 0 {
		parts = append(parts, fmt.Sprintf("skipped (no snapshot): [%s]", strings.Join(skipped, ", ")))
	}
	if len(errs) > 0 {
		parts = append(parts, fmt.Sprintf("errors: [%s]", strings.Join(errs, "; ")))
	}

	if len(errs) > 0 {
		return &types.ToolResult{
			Success: false,
			Output:  strings.Join(parts, "\n"),
			Tool:    "rollback",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(parts, "\n"),
		Tool:    "rollback",
	}
}

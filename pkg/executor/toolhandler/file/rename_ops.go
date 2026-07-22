package file

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
)

// HandleRename renames/moves one or more files or directories.
// Supports pairs mode (v1 compat) and rule-based mode (file_pattern + replace/prefix/suffix/extension).
func HandleRename(workDir string, versioner *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	dryRun, _ := args["dry_run"].(bool)
	overwrite, _ := args["overwrite"].(bool)

	// Rule-based mode: file_pattern + replace/prefix/suffix/extension
	if fp, ok := args["file_pattern"].(string); ok && fp != "" {
		replaceFrom := ""
		replaceTo := ""
		if replaceRaw, ok := args["replace"].(map[string]interface{}); ok {
			replaceFrom, _ = replaceRaw["from"].(string)
			replaceTo, _ = replaceRaw["to"].(string)
		}
		prefix, _ := args["prefix"].(string)
		suffix, _ := args["suffix"].(string)
		extension, _ := args["extension"].(string)

		// Find matching files
		files, err := findFilesByGlob(workDir, fp)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("glob error: %s", err.Error()), Tool: "rename", RawResult: []interface{}{}}
		}
		if len(files) == 0 {
			return &types.ToolResult{Success: false, Error: "no files matched the pattern", Tool: "rename", RawResult: []interface{}{}}
		}

		var outputs []string
		var errs []string
		var renameItems []map[string]interface{}

		for _, absPath := range files {
			relPath, _ := filepath.Rel(workDir, absPath)
			newPath := applyRenameRules(absPath, prefix, suffix, replaceFrom, replaceTo, extension)

			if absPath == newPath {
				errs = append(errs, fmt.Sprintf("=== %s ===\n❌ rename skipped: no change", relPath))
				renameItems = append(renameItems, map[string]interface{}{
					"from":    relPath,
					"to":      relPath,
					"success": false,
					"error":   "no change",
				})
				continue
			}

			// Check if target already exists
			if _, statErr := os.Stat(newPath); statErr == nil && !overwrite {
				errs = append(errs, fmt.Sprintf("=== %s ===\n❌ target already exists: %s (use overwrite=true to force)", relPath, filepath.Base(newPath)))
				renameItems = append(renameItems, map[string]interface{}{
					"from":    relPath,
					"to":      filepath.Base(newPath),
					"success": false,
					"error":   "target already exists",
				})
				continue
			}

			newRel, _ := filepath.Rel(workDir, newPath)

			if dryRun {
				outputs = append(outputs, fmt.Sprintf("=== %s ===\n🔍 [DRY RUN] rename %s → %s", relPath, relPath, newRel))
				renameItems = append(renameItems, map[string]interface{}{
					"from":    relPath,
					"to":      newRel,
					"success": true,
					"error":   "",
				})
				continue
			}

			snapshotBeforeWrite(versioner, absPath, workDir, turnID)

			// On Windows, case-only renames need a two-step approach
			if runtime.GOOS == "windows" && isCaseOnlyRename(absPath, newPath) {
				tmp := absPath + ".chonkpilot_rename_tmp"
				if err := os.Rename(absPath, tmp); err != nil {
					errs = append(errs, fmt.Sprintf("=== %s ===\n❌ %s → %s: %s", relPath, relPath, newRel, err))
					renameItems = append(renameItems, map[string]interface{}{
						"from":    relPath,
						"to":      newRel,
						"success": false,
						"error":   err.Error(),
					})
					continue
				}
				if err := os.Rename(tmp, newPath); err != nil {
					os.Rename(tmp, absPath) // recover
					errs = append(errs, fmt.Sprintf("=== %s ===\n❌ %s → %s: %s", relPath, relPath, newRel, err))
					renameItems = append(renameItems, map[string]interface{}{
						"from":    relPath,
						"to":      newRel,
						"success": false,
						"error":   err.Error(),
					})
					continue
				}
			} else {
				if err := os.Rename(absPath, newPath); err != nil {
					errs = append(errs, fmt.Sprintf("=== %s ===\n❌ %s → %s: %s", relPath, relPath, newRel, err))
					renameItems = append(renameItems, map[string]interface{}{
						"from":    relPath,
						"to":      newRel,
						"success": false,
						"error":   err.Error(),
					})
					continue
				}
			}

			outputs = append(outputs, fmt.Sprintf("=== %s ===\n✅ renamed %s → %s", relPath, relPath, newRel))
			renameItems = append(renameItems, map[string]interface{}{
				"from":    relPath,
				"to":      newRel,
				"success": true,
				"error":   "",
			})
		}

		result := strings.Join(append(outputs, errs...), "\n\n")
		success := len(errs) == 0
		return &types.ToolResult{
			Success:   success,
			Output:    result,
			Tool:      "rename",
			RawResult: renameItems,
		}
	}

	// Pairs mode (v1 compat)
	raw, ok := args["pairs"]
	if !ok {
		return &types.ToolResult{
			Success:   false,
			Error:     "arguments must be a JSON array",
			Tool:      "rename",
			RawResult: []interface{}{},
		}
	}
	rawPairs, ok := raw.([]interface{})
	if !ok || len(rawPairs) == 0 {
		return &types.ToolResult{
			Success:   false,
			Error:     "expected a non-empty array of {from, to} objects",
			Tool:      "rename",
			RawResult: []interface{}{},
		}
	}

	var outputs []string
	var errs []string
	var renameItems []map[string]interface{}

	for i, raw := range rawPairs {
		m, ok := raw.(map[string]interface{})
		if !ok {
			errs = append(errs, fmt.Sprintf("[%d]: expected object", i))
			continue
		}
		from, _ := m["from"].(string)
		to, _ := m["to"].(string)
		if from == "" || to == "" {
			errs = append(errs, fmt.Sprintf("[%d]: from and to are required", i))
			continue
		}

		fromResolved, errMsg := resolveWritePath(from, workDir)
		if errMsg != "" {
			errs = append(errs, fmt.Sprintf("=== %s ===\n❌ %s", from, errMsg))
			renameItems = append(renameItems, map[string]interface{}{
				"from": from, "to": to, "success": false, "error": errMsg,
			})
			continue
		}
		toResolved, errMsg := resolveWritePath(to, workDir)
		if errMsg != "" {
			errs = append(errs, fmt.Sprintf("=== %s ===\n❌ %s", to, errMsg))
			renameItems = append(renameItems, map[string]interface{}{
				"from": from, "to": to, "success": false, "error": errMsg,
			})
			continue
		}

		// Check overwrite
		if _, statErr := os.Stat(toResolved); statErr == nil && !overwrite {
			errs = append(errs, fmt.Sprintf("=== %s → %s ===\n❌ target already exists (use overwrite=true)", from, to))
			renameItems = append(renameItems, map[string]interface{}{
				"from": from, "to": to, "success": false, "error": "target already exists",
			})
			continue
		}

		// Snapshot before rename so the LLM can restore the old content
		snapshotBeforeWrite(versioner, fromResolved, workDir, turnID)

		// On Windows, case-only renames need a two-step approach
		// because the filesystem treats "foo.txt" and "Foo.txt" as the same name.
		if runtime.GOOS == "windows" && isCaseOnlyRename(fromResolved, toResolved) {
			tmp := fromResolved + ".chonkpilot_rename_tmp"
			if err := os.Rename(fromResolved, tmp); err != nil {
				errs = append(errs, fmt.Sprintf("=== %s → %s ===\n❌ %s", from, to, err))
				renameItems = append(renameItems, map[string]interface{}{
					"from": from, "to": to, "success": false, "error": err.Error(),
				})
				continue
			}
			if err := os.Rename(tmp, toResolved); err != nil {
				// Attempt to recover back to original
				os.Rename(tmp, fromResolved)
				errs = append(errs, fmt.Sprintf("=== %s → %s ===\n❌ %s", from, to, err))
				renameItems = append(renameItems, map[string]interface{}{
					"from": from, "to": to, "success": false, "error": err.Error(),
				})
				continue
			}
		} else {
			if err := os.Rename(fromResolved, toResolved); err != nil {
				errs = append(errs, fmt.Sprintf("=== %s → %s ===\n❌ %s", from, to, err))
				renameItems = append(renameItems, map[string]interface{}{
					"from": from, "to": to, "success": false, "error": err.Error(),
				})
				continue
			}
		}

		outputs = append(outputs, fmt.Sprintf("=== %s → %s ===\n✅ renamed", from, to))
		renameItems = append(renameItems, map[string]interface{}{
			"from": from, "to": to, "success": true, "error": "",
		})
	}

	if len(errs) > 0 {
		result := strings.Join(outputs, "\n\n")
		if result != "" {
			result += "\n\n"
		}
		result += strings.Join(errs, "\n")
		return &types.ToolResult{
			Success:   false,
			Output:    result,
			Tool:      "rename",
			RawResult: renameItems,
		}
	}
	return &types.ToolResult{
		Success:   true,
		Output:    strings.Join(outputs, "\n"),
		Tool:      "rename",
		RawResult: renameItems,
	}
}

// applyRenameRules applies prefix → suffix → replace → extension rules to a file path.
func applyRenameRules(path, prefix, suffix, replaceFrom, replaceTo, extension string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := base
	if ext != "" {
		name = strings.TrimSuffix(base, ext)
	}

	// Apply prefix
	newName := prefix + name

	// Apply suffix (before extension)
	newName = newName + suffix

	// Apply replace (in the name part only)
	if replaceFrom != "" {
		newName = strings.ReplaceAll(newName, replaceFrom, replaceTo)
	}

	// Apply extension (replaces existing extension)
	if extension != "" {
		ext = extension
	}

	return filepath.Join(dir, newName+ext)
}

// isCaseOnlyRename checks whether two paths differ only in case.
func isCaseOnlyRename(from, to string) bool {
	fromBase := filepath.Base(from)
	toBase := filepath.Base(to)
	fromDir := filepath.Dir(from)
	toDir := filepath.Dir(to)
	return strings.EqualFold(fromDir, toDir) &&
		strings.EqualFold(fromBase, toBase) &&
		fromBase != toBase
}

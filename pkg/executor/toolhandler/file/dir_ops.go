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
		outputs = append(outputs, fmt.Sprintf("created %s", p))
	}

	if len(errs) > 0 {
		result := strings.Join(outputs, "\n")
		if result != "" {
			result += "\n\n"
		}
		result += "errors:\n" + strings.Join(errs, "\n")
		return &types.ToolResult{
			Success: false,
			Output:  result,
			Tool:    "make_directory",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(outputs, "\n"),
		Tool:    "make_directory",
	}
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
// For directories, all files under the tree are snapshotted before removal.
// For files, the file is snapshotted before removal.
func HandleRemove(workDir string, versioner *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	raw, ok := args["paths"]
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   "arguments must be a JSON array",
			Tool:    "remove",
		}
	}
	rawPaths, ok := raw.([]interface{})
	if !ok || len(rawPaths) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "expected a non-empty array of path strings",
			Tool:    "remove",
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
		outputs = append(outputs, fmt.Sprintf("removed %s", p))
	}

	if len(errs) > 0 {
		result := strings.Join(outputs, "\n")
		if result != "" {
			result += "\n\n"
		}
		result += "errors:\n" + strings.Join(errs, "\n")
		return &types.ToolResult{
			Success: false,
			Output:  result,
			Tool:    "remove",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(outputs, "\n"),
		Tool:    "remove",
	}
}

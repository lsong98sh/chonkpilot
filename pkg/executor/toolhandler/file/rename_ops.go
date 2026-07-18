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
func HandleRename(workDir string, versioner *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	raw, ok := args["pairs"]
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   "arguments must be a JSON array",
			Tool:    "rename",
		}
	}
	rawPairs, ok := raw.([]interface{})
	if !ok || len(rawPairs) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "expected a non-empty array of {from, to} objects",
			Tool:    "rename",
		}
	}

	var outputs []string
	var errs []string

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
			errs = append(errs, fmt.Sprintf("%s: %s", from, errMsg))
			continue
		}
		toResolved, errMsg := resolveWritePath(to, workDir)
		if errMsg != "" {
			errs = append(errs, fmt.Sprintf("%s: %s", to, errMsg))
			continue
		}

		// Snapshot before rename so the LLM can restore the old content
		snapshotBeforeWrite(versioner, fromResolved, workDir, turnID)

		// On Windows, case-only renames need a two-step approach
		// because the filesystem treats "foo.txt" and "Foo.txt" as the same name.
		if runtime.GOOS == "windows" && isCaseOnlyRename(fromResolved, toResolved) {
			tmp := fromResolved + ".chonkpilot_rename_tmp"
			if err := os.Rename(fromResolved, tmp); err != nil {
				errs = append(errs, fmt.Sprintf("%s → %s: %s", from, to, err))
				continue
			}
			if err := os.Rename(tmp, toResolved); err != nil {
				// Attempt to recover back to original
				os.Rename(tmp, fromResolved)
				errs = append(errs, fmt.Sprintf("%s → %s: %s", from, to, err))
				continue
			}
		} else {
			if err := os.Rename(fromResolved, toResolved); err != nil {
				errs = append(errs, fmt.Sprintf("%s → %s: %s", from, to, err))
				continue
			}
		}

		outputs = append(outputs, fmt.Sprintf("renamed %s → %s", from, to))
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
			Tool:    "rename",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(outputs, "\n"),
		Tool:    "rename",
	}
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

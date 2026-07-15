package file

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
)

// HandleDiff generates a unified diff between two files.
func HandleDiff(workDir string, args map[string]interface{}) *types.ToolResult {
	file1, _ := args["file1"].(string)
	file2, _ := args["file2"].(string)

	if file1 == "" || file2 == "" {
		return &types.ToolResult{Success: false, Error: "both file1 and file2 are required", Tool: "diff"}
	}

	if !filepath.IsAbs(file1) {
		file1 = filepath.Join(workDir, file1)
	}
	if !filepath.IsAbs(file2) {
		file2 = filepath.Join(workDir, file2)
	}

	data1, err := os.ReadFile(file1)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to read %s: %s", file1, err.Error()), Tool: "diff"}
	}
	data2, err := os.ReadFile(file2)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to read %s: %s", file2, err.Error()), Tool: "diff"}
	}

	rel1 := file1
	rel2 := file2
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

	result := BuildUnifiedDiff(rel1, rel2, diffs, 3)
	if result == "" {
		result = "no differences"
	}

	return &types.ToolResult{
		Success: true,
		Output:  result,
		Tool:    "diff",
	}
}

// HandlePatch applies a unified diff to a file.
func HandlePatch(workDir string, versioner *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	target, _ := args["target"].(string)
	diffContent, _ := args["diff"].(string)

	if target == "" {
		return &types.ToolResult{Success: false, Error: "target file path is required", Tool: "patch"}
	}
	if diffContent == "" {
		return &types.ToolResult{Success: false, Error: "diff content is required", Tool: "patch"}
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(workDir, target)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to read target: %s", err.Error()), Tool: "patch"}
	}

	newContent, err := ApplyUnifiedDiff(string(data), diffContent)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("patch apply failed: %s", err.Error()), Tool: "patch"}
	}

	snapshotBeforeWrite(versioner, target, workDir, turnID)

	if err := os.WriteFile(target, []byte(newContent), 0644); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to write: %s", err.Error()), Tool: "patch"}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("patch applied to %s (%d bytes written)", target, len(newContent)),
		Tool:    "patch",
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

func init() {
	types.RegisterSimplify("diff", simplifyDiff)
	types.RegisterSimplify("patch", types.SimpleAction("patch"))
}

type diffArgs struct {
	File1 string `json:"file1"`
	File2 string `json:"file2"`
}

func simplifyDiff(argsJSON json.RawMessage, result string) string {
	var a diffArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "diff"
	}
	return fmt.Sprintf("diff(%s, %s)", a.File1, a.File2)
}

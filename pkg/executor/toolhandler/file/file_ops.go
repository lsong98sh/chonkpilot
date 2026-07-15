package file

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"github.com/chonkpilot/chonkpilot/pkg/executor/engine"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
)

// SanitizePath resolves a user-provided path and ensures it stays within workDir.
func SanitizePath(userPath, workDir string) (string, string) {
	if userPath == "" {
		return "", "path is required"
	}
	resolved := userPath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workDir, resolved)
	}
	resolved = filepath.Clean(resolved)

	if !strings.HasPrefix(resolved, filepath.Clean(workDir)+string(filepath.Separator)) && resolved != filepath.Clean(workDir) {
		return "", fmt.Sprintf("path %s is outside workspace %s", userPath, workDir)
	}
	return resolved, ""
}

// SkipDirs are directories that grep and search_files skip by default.
var SkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".trae":        true,
	".chonkpilot":  true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"build":        true,
	"dist":         true,
	".next":        true,
	".nuxt":        true,
}

// HandleReadFile reads the contents of one or more files.
func HandleReadFile(workDir string, args map[string]interface{}) *types.ToolResult {
	raw, ok := args["files"]
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   "arguments must be a JSON array",
			Tool:    "read_file",
		}
	}
	rawFiles, ok := raw.([]interface{})
	if !ok || len(rawFiles) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "expected a non-empty array of file objects",
			Tool:    "read_file",
		}
	}

	type fileReq struct {
		path  string
		start int
		limit int
	}

	var reqs []fileReq
	for i, raw := range rawFiles {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("[%d]: expected object", i),
				Tool:    "read_file",
			}
		}
		path, _ := m["path"].(string)
		if path == "" {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("[%d]: path is required", i),
				Tool:    "read_file",
			}
		}
		start, _ := m["start"].(float64)
		limit, _ := m["limit"].(float64)
		reqs = append(reqs, fileReq{path: path, start: int(start), limit: int(limit)})
	}

	var outputs []string
	var errs []string

	for _, req := range reqs {
		resolved, errMsg := SanitizePath(req.path, workDir)
		if errMsg != "" {
			errs = append(errs, fmt.Sprintf("%s: %s", req.path, errMsg))
			continue
		}

		content, err := readFileContent(resolved, workDir, req.start, req.limit)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", req.path, err))
			continue
		}

		outputs = append(outputs, fmt.Sprintf("=== %s ===\n%s", req.path, content))
	}

	if len(errs) > 0 {
		result := strings.Join(outputs, "\n\n")
		if result != "" {
			result += "\n\n"
		}
		result += "errors:\n" + strings.Join(errs, "\n")
		return &types.ToolResult{
			Success: false,
			Output:  result,
			Tool:    "read_file",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(outputs, "\n\n"),
		Tool:    "read_file",
	}
}

// readFileContent reads a single file and returns its content as text.
// Office docs are converted to markdown. Binary files return a data URI.
func readFileContent(resolved, workDir string, start, limit int) (string, error) {
	ext := strings.ToLower(filepath.Ext(resolved))
	switch ext {
	case ".pdf", ".docx", ".xlsx", ".pptx":
		return ReadFileAsMarkdown(resolved, workDir)
	default:
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", err
		}
		if IsBinaryBytes(data) {
			mimeType := http.DetectContentType(data)
			b64 := base64.StdEncoding.EncodeToString(data)
			return fmt.Sprintf("data:%s;base64,%s", mimeType, b64), nil
		}
		content := string(data)
		if start > 0 || limit > 0 {
			lines := strings.Split(content, "\n")
			startIdx := start
			if startIdx < 1 {
				startIdx = 1
			}
			if startIdx > len(lines) {
				startIdx = len(lines)
			}
			endIdx := len(lines)
			if limit > 0 {
				endIdx = startIdx - 1 + limit
				if endIdx > len(lines) {
					endIdx = len(lines)
				}
			}
			content = strings.Join(lines[startIdx-1:endIdx], "\n")
		}
		return content, nil
	}
}

// HandleWriteFile writes content to one or more files.
func HandleWriteFile(workDir string, codeIndexer *codeindex.Indexer, versioner *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	raw, ok := args["files"]
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   "arguments must be a JSON array",
			Tool:    "write_file",
		}
	}
	rawFiles, ok := raw.([]interface{})
	if !ok || len(rawFiles) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "expected a non-empty array of file objects",
			Tool:    "write_file",
		}
	}

	var outputs []string
	var errs []string

	for i, raw := range rawFiles {
		m, ok := raw.(map[string]interface{})
		if !ok {
			errs = append(errs, fmt.Sprintf("[%d]: expected object", i))
			continue
		}
		path, _ := m["path"].(string)
		content, _ := m["content"].(string)
		if path == "" || content == "" {
			errs = append(errs, fmt.Sprintf("[%d]: path and content are required", i))
			continue
		}
		contentType, _ := m["type"].(string)

		resolved, errMsg := SanitizePath(path, workDir)
		if errMsg != "" {
			errs = append(errs, fmt.Sprintf("%s: %s", path, errMsg))
			continue
		}

		dir := filepath.Dir(resolved)
		if err := os.MkdirAll(dir, 0755); err != nil {
			errs = append(errs, fmt.Sprintf("%s: failed to create directory: %s", path, err))
			continue
		}

		var data []byte
		if strings.HasPrefix(contentType, "base64") {
			decoded, err := base64.StdEncoding.DecodeString(content)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: failed to decode base64: %s", path, err))
				continue
			}
			data = decoded
		} else {
			data = []byte(content)
		}

		snapshotBeforeWrite(versioner, resolved, workDir, turnID)

		if err := os.WriteFile(resolved, data, 0644); err != nil {
			errs = append(errs, fmt.Sprintf("%s: failed to write: %s", path, err))
			continue
		}

		TriggerCodeIndex(codeIndexer, resolved)
		outputs = append(outputs, fmt.Sprintf("written %d bytes to %s", len(data), path))
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
			Tool:    "write_file",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(outputs, "\n"),
		Tool:    "write_file",
	}
}

// HandleReplace performs a find/replace in a file.
func HandleReplace(workDir string, engine *engine.Engine, codeIndexer *codeindex.Indexer, versioner *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	path, _ := args["path"].(string)
	oldStr, _ := args["old"].(string)
	newStr, _ := args["new"].(string)
	useRegex, _ := args["regex"].(bool)

	if path == "" {
		return &types.ToolResult{Success: false, Error: "path is required", Tool: "replace"}
	}
	if oldStr == "" && newStr == "" {
		return &types.ToolResult{Success: false, Error: "at least one of 'old' or 'new' must differ", Tool: "replace"}
	}

	resolved, errMsg := SanitizePath(path, workDir)
	if errMsg != "" {
		return &types.ToolResult{Success: false, Error: errMsg, Tool: "replace"}
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read file: %s", err.Error()),
			Tool:    "replace",
		}
	}
	if IsBinaryBytes(data) {
		return &types.ToolResult{Success: false, Error: "cannot replace in binary file", Tool: "replace"}
	}

	content := string(data)
	if oldStr == "" {
		content = newStr + content
	} else if useRegex {
		re, err := regexp.Compile(oldStr)
		if err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("invalid regex: %s", err.Error()),
				Tool:    "replace",
			}
		}
		content = re.ReplaceAllString(content, newStr)
	} else {
		content = strings.ReplaceAll(content, oldStr, newStr)
	}

	// Snapshot before write (first time this turn per file)
	snapshotBeforeWrite(versioner, resolved, workDir, turnID)

	if err := os.WriteFile(resolved, []byte(content), 0644); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write file: %s", err.Error()),
			Tool:    "replace",
		}
	}

	TriggerCodeIndex(codeIndexer, resolved)
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("replaced in %s (%d bytes written)", path, len(content)),
		Tool:    "replace",
	}
}

// TriggerCodeIndex marks a file as changed for deferred batch indexing.
// The actual Enqueue happens at turn end via FlushChangedFiles.
func TriggerCodeIndex(codeIndexer *codeindex.Indexer, path string) {
	if codeIndexer == nil {
		return
	}
	codeIndexer.MarkChanged(path)
}

// snapshotBeforeWrite snapshots a file before modification (first time per turn per file_uid).
func snapshotBeforeWrite(versioner *fileversions.Versioner, resolvedPath, workDir, turnID string) {
	if versioner == nil || turnID == "" {
		return
	}
	// Compute relative path for DB storage
	relPath := resolvedPath
	if filepath.IsAbs(resolvedPath) {
		if rel, err := filepath.Rel(workDir, resolvedPath); err == nil {
			relPath = rel
		}
	}
	versioner.Snapshot(turnID, relPath)
}

// HandleExecuteCommand executes a shell command.
func HandleExecuteCommand(workDir string, engine *engine.Engine, taskMgr interface {
	StartCommand(workDir, cmdStr string) string
}, args map[string]interface{}) *types.ToolResult {
	cmdStr, _ := args["command"].(string)
	if cmdStr == "" {
		return &types.ToolResult{Success: false, Error: "command is required", Tool: "execute_command"}
	}

	async, _ := args["async"].(bool)
	if async {
		taskID := taskMgr.StartCommand(workDir, cmdStr)
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("task_id=%s status=running", taskID),
			Tool:    "execute_command",
		}
	}

	result := engine.ExecuteShell(cmdStr)
	out := result.Output
	if out == "" {
		out = result.Error
	}
	return &types.ToolResult{
		Success: result.Success,
		Output:  strings.TrimSpace(out),
		Tool:    "execute_command",
	}
}

// ─── Simplify Types & Functions ───

type readFileItem struct {
	Path  string `json:"path"`
	Start int    `json:"start,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

func simplifyReadFile(argsJSON json.RawMessage, result string) string {
	var items []readFileItem
	if err := json.Unmarshal(argsJSON, &items); err != nil || len(items) == 0 {
		return "read_file"
	}
	paths := make([]string, len(items))
	for i, f := range items {
		paths[i] = f.Path
	}
	pathList := strings.Join(paths, ", ")
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil || !tr.Success {
		return fmt.Sprintf("read_file(%s) failed", pathList)
	}
	lines := strings.Count(tr.Output, "\n")
	return fmt.Sprintf("read_file(%s): %d lines", pathList, lines)
}

type writeFileItem struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Type    string `json:"type,omitempty"`
}

func simplifyWriteFile(argsJSON json.RawMessage, result string) string {
	var items []writeFileItem
	if err := json.Unmarshal(argsJSON, &items); err != nil || len(items) == 0 {
		return "write_file"
	}
	paths := make([]string, len(items))
	totalBytes := 0
	for i, f := range items {
		paths[i] = f.Path
		totalBytes += len(f.Content)
	}
	pathList := strings.Join(paths, ", ")
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil || !tr.Success {
		return fmt.Sprintf("write_file(%s) failed", pathList)
	}
	return fmt.Sprintf("write_file(%s): %d bytes", pathList, totalBytes)
}

type replaceArgs struct {
	Path     string `json:"path"`
	Old      string `json:"old"`
	New      string `json:"new"`
	UseRegex bool   `json:"regex,omitempty"`
}

func simplifyReplace(argsJSON json.RawMessage, result string) string {
	var a replaceArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "replace"
	}
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil || !tr.Success {
		return fmt.Sprintf("replace(%s) failed", a.Path)
	}
	oldPreview := types.TruncateStr(a.Old, 60)
	return fmt.Sprintf("replace(%s): replaced %q", a.Path, oldPreview)
}

// executeCommandArgs is used for simplifyExecuteCommand.
type executeCommandArgs struct {
	Command string `json:"command"`
	Async   bool   `json:"async,omitempty"`
}

func simplifyExecuteCommand(argsJSON json.RawMessage, result string) string {
	var a executeCommandArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "execute_command"
	}
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil {
		return fmt.Sprintf("execute_command(%s)", types.TruncateStr(a.Command, 80))
	}
	if a.Async {
		return fmt.Sprintf("execute_command(async): %s", tr.Output)
	}
	lines := 0
	if tr.Output != "" {
		lines = strings.Count(tr.Output, "\n")
	}
	if tr.Success {
		return fmt.Sprintf("execute_command(%s): ok, %d lines",
			types.TruncateStr(a.Command, 60), lines)
	}
	return fmt.Sprintf("execute_command(%s): failed, exit=%s",
		types.TruncateStr(a.Command, 60), types.TruncateStr(tr.Error, 80))
}

func init() {
	types.RegisterSimplify("read_file", simplifyReadFile)
	types.RegisterSimplify("write_file", simplifyWriteFile)
	types.RegisterSimplify("replace", simplifyReplace)
	types.RegisterSimplify("execute_command", simplifyExecuteCommand)
}

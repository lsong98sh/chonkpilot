package python

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/task"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleExecutePythonScript executes a Python script from inline content or an existing file.
// Supports pip dependency auto-install, CLI arguments, async mode, and custom interpreter paths.
func HandleExecutePythonScript(workDir string, taskMgr *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	dir, _ := args["dir"].(string)
	scriptContent, _ := args["scriptcontent"].(string)
	filePath, _ := args["file"].(string)

	// Parameter mutual exclusion check
	if scriptContent != "" && filePath != "" {
		return &types.ToolResult{
			Success: false,
			Error:   "scriptcontent 和 file 不能同时使用",
			Tool:    "execute_python_script",
			Output:  "❌ scriptcontent 和 file 不能同时使用",
			RawResult: map[string]interface{}{
				"error": "scriptcontent and file cannot be used together",
			},
		}
	}
	if scriptContent == "" && filePath == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "必须提供 scriptcontent 或 file",
			Tool:    "execute_python_script",
			Output:  "❌ 必须提供 scriptcontent 或 file",
			RawResult: map[string]interface{}{
				"error": "must provide scriptcontent or file",
			},
		}
	}

	// Default dir to workDir if not specified
	if dir == "" {
		dir = workDir
	}

	// Verify working directory exists
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("working directory does not exist or is not a directory: %s", dir),
			Tool:    "execute_python_script",
			Output:  fmt.Sprintf("❌ 工作目录不存在：%s", dir),
			RawResult: map[string]interface{}{
				"error": fmt.Sprintf("working directory does not exist: %s", dir),
			},
		}
	}

	// Detect Python interpreter
	pythonPath := detectPython(args["python"])
	if pythonPath == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "Python interpreter not found. Install Python and ensure it is in the system PATH, or provide a custom path via the 'python' parameter.",
			Tool:    "execute_python_script",
			Output:  "❌ 未找到 Python 解释器",
			RawResult: map[string]interface{}{
				"error": "Python interpreter not found",
			},
		}
	}

	// pip dependency installation
	if pipRaw, ok := args["pip"].([]interface{}); ok && len(pipRaw) > 0 {
		result := installPipPackages(pythonPath, dir, pipRaw)
		if result != nil {
			return result
		}
	}

	// Build command to execute
	scriptPath := filePath
	var isTempFile bool

	if filePath != "" {
		// Verify the file exists
		if fi, err := os.Stat(filePath); err != nil || fi.IsDir() {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("file does not exist or is not a directory: %s", filePath),
				Tool:    "execute_python_script",
				Output:  fmt.Sprintf("❌ 文件不存在：%s", filePath),
				RawResult: map[string]interface{}{
					"error": fmt.Sprintf("file does not exist: %s", filePath),
				},
			}
		}
	} else {
		// Write scriptcontent to a temp file
		scriptsDir := filepath.Join(workDir, ".ide", "tmp", "scripts")
		if err := os.MkdirAll(scriptsDir, 0755); err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to create scripts directory: %v", err),
				Tool:    "execute_python_script",
				Output:  "❌ 创建脚本目录失败",
				RawResult: map[string]interface{}{
					"error": err.Error(),
				},
			}
		}

		// Generate a unique temp filename
		randBytes := make([]byte, 8)
		if _, err := rand.Read(randBytes); err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to generate random filename: %v", err),
				Tool:    "execute_python_script",
				Output:  "❌ 生成随机文件名失败",
				RawResult: map[string]interface{}{
					"error": err.Error(),
				},
			}
		}
		scriptName := fmt.Sprintf("script_%s.py", hex.EncodeToString(randBytes))
		scriptPath = filepath.Join(scriptsDir, scriptName)
		isTempFile = true

		// Write script to temp file
		if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to write script file: %v", err),
				Tool:    "execute_python_script",
				Output:  "❌ 写入脚本文件失败",
				RawResult: map[string]interface{}{
					"error": err.Error(),
				},
			}
		}
	}

	// Build the command string
	cmdStr := buildCommand(pythonPath, scriptPath, args["args"])

	// Async mode
	if async, _ := args["async"].(bool); async {
		taskID := taskMgr.StartCommand(dir, cmdStr, nil)
		// In async mode, leave temp file for the running process
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("⚙️ Python 脚本已异步启动（task_id=%s）", taskID),
			Tool:    "execute_python_script",
			RawResult: map[string]interface{}{
				"task_id": taskID,
				"status":  "running",
				"async":   true,
			},
		}
	}

	// Sync mode: execute and wait
	cmd := exec.Command("cmd", "/c", cmdStr)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()

	// Clean up temp file (only for scriptcontent mode)
	if isTempFile {
		os.Remove(scriptPath)
	}

	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		errMsg := err.Error()
		exitCode := -1
		if ok {
			errMsg = strings.TrimSpace(string(exitErr.Stderr))
			if errMsg == "" {
				errMsg = fmt.Sprintf("exit code %d", exitErr.ExitCode())
			}
			exitCode = exitErr.ExitCode()
		}
		return &types.ToolResult{
			Success: false,
			Error:   errMsg,
			Output:  fmt.Sprintf("❌ Python 脚本执行失败\n\n%s", strings.TrimSpace(string(output))),
			Tool:    "execute_python_script",
			RawResult: map[string]interface{}{
				"error":     errMsg,
				"stdout":    strings.TrimSpace(string(output)),
				"exit_code": exitCode,
			},
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("✅ Python 脚本执行成功\n\n%s", strings.TrimSpace(string(output))),
		Tool:    "execute_python_script",
		RawResult: map[string]interface{}{
			"output": strings.TrimSpace(string(output)),
		},
	}
}

// detectPython returns the Python interpreter path.
// Priority: user-provided path > "python" from PATH > "python3" from PATH.
func detectPython(pythonArg interface{}) string {
	if pythonPath, ok := pythonArg.(string); ok && pythonPath != "" {
		return pythonPath
	}
	if _, err := exec.LookPath("python"); err == nil {
		return "python"
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return "python3"
	}
	return ""
}

// installPipPackages installs pip packages before script execution.
// Runs pip install for each package (idempotent — pip handles already-satisfied).
func installPipPackages(pythonPath, dir string, pipRaw []interface{}) *types.ToolResult {
	var failed []string
	for _, pkg := range pipRaw {
		pkgStr, ok := pkg.(string)
		if !ok || pkgStr == "" {
			continue
		}
		installCmd := exec.Command(pythonPath, "-m", "pip", "install", pkgStr)
		installCmd.Dir = dir
		if out, err := installCmd.CombinedOutput(); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %s\n%s", pkgStr, err, strings.TrimSpace(string(out))))
		}
	}
	if len(failed) > 0 {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("pip install 失败:\n%s", strings.Join(failed, "\n")),
			Tool:    "execute_python_script",
			Output:  "❌ pip 安装依赖失败",
			RawResult: map[string]interface{}{
				"error":  fmt.Sprintf("pip install failed: %s", strings.Join(failed, "; ")),
				"failed": failed,
			},
		}
	}
	return nil
}

// buildCommand constructs the shell command string for executing a Python script.
// Quotes the interpreter path and script path, and appends CLI args.
// buildCommand constructs the shell command string for executing a Python script.
// No quoting needed since exec.Command handles spaces correctly via the OS layer.
func buildCommand(pythonPath, scriptPath string, argsArg interface{}) string {
	var parts []string
	parts = append(parts, pythonPath)
	parts = append(parts, scriptPath)

	// Append CLI arguments
	if argsRaw, ok := argsArg.([]interface{}); ok {
		for _, a := range argsRaw {
			if aStr, ok := a.(string); ok {
				parts = append(parts, aStr)
			}
		}
	}

	return strings.Join(parts, " ")
}

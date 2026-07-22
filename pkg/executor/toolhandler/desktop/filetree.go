package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// FileTreeNode represents a single node in the IDE file tree.
type FileTreeNode struct {
	Name     string          `json:"name"`
	Path     string          `json:"path"`
	IsDir    bool            `json:"is_dir"`
	Expanded bool            `json:"expanded"`
	Children []*FileTreeNode `json:"children,omitempty"`
}

// HandleGetFileTreeStatus handles two modes via args["action"]:
//
//   - "capture" (default): emits filetree:capture event to IDE, asks the frontend
//     to snapshot the visible tree and save to .ide/filetree_state.json.
//     Uses WaitToolResult to block until the IDE sends a tool_result command
//     with the request UUID. Then reads the snapshot and returns it.
//
//   - "read_snapshot": reads the saved snapshot from .ide/filetree_state.json and
//     returns it in the same format as the original filesystem-based implementation.
func HandleGetFileTreeStatus(workDir string, writeEvent func(string, map[string]interface{}), waitToolResult func(uuid string) *types.ToolResult, args map[string]interface{}) *types.ToolResult {
	action, _ := args["action"].(string)
	if action == "" {
		action = "capture"
	}

	switch action {
	case "capture":
		return handleCapture(workDir, writeEvent, waitToolResult)
	case "read_snapshot":
		return readSnapshot(workDir)
	default:
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("unknown action: %s (supported: capture, read_snapshot)", action),
			Output:  fmt.Sprintf("❌ 文件树操作失败：未知 action '%s'", action),
			Tool:    "get_ide_filetree_status",
		}
}
}

func handleCapture(workDir string, writeEvent func(string, map[string]interface{}), waitToolResult func(uuid string) *types.ToolResult) *types.ToolResult {
	if waitToolResult == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "file tree capture requires daemon mode (waitToolResult not available)",
			Output:  "❌ 文件树捕获失败：需要守护进程模式",
			Tool:    "get_ide_filetree_status",
		}
	}

	requestID := fmt.Sprintf("filetree_%d", time.Now().UnixMilli())

	// Emit event to IDE → IDE forwards to frontend → frontend captures and saves
	if writeEvent != nil {
		writeEvent("filetree:capture", map[string]interface{}{
			"request_id": requestID,
			"work_dir":   workDir,
		})
	}

	// Block until IDE sends tool_result command via daemon stdin
	result := waitToolResult(requestID)
	if !result.Success {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("file tree capture failed: %s", result.Output),
			Output:  fmt.Sprintf("❌ 文件树捕获失败：%s", result.Output),
			Tool:    "get_ide_filetree_status",
		}
	}

	// Read the snapshot that the frontend saved
	return readSnapshot(workDir)
}

func readSnapshot(workDir string) *types.ToolResult {
	stateFile := filepath.Join(workDir, ".ide", "filetree_state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("file tree snapshot not found: %s", err.Error()),
			Output:  fmt.Sprintf("❌ 文件树快照未找到：%s", err.Error()),
			Tool:    "get_ide_filetree_status",
		}
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("invalid file tree snapshot: %s", err.Error()),
			Output:  fmt.Sprintf("❌ 文件树快照无效：%s", err.Error()),
			Tool:    "get_ide_filetree_status",
		}
	}

	snapshot, hasSnapshot := raw["snapshot"]
	if !hasSnapshot || snapshot == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "file tree snapshot not yet captured (frontend hasn't saved it yet)",
			Output:  "❌ 文件树快照尚未捕获（前端尚未保存）",
			Tool:    "get_ide_filetree_status",
		}
	}

	// Count stats from snapshot tree
	totalDirs := 0
	totalFiles := 0
	var countNodes func(n interface{})
	countNodes = func(n interface{}) {
		node, ok := n.(map[string]interface{})
		if !ok {
			return
		}
		if isDir, _ := node["is_dir"].(bool); isDir {
			totalDirs++
			if children, ok := node["children"].([]interface{}); ok {
				for _, c := range children {
					countNodes(c)
				}
			}
		} else {
			totalFiles++
		}
	}
	if children, ok := snapshot.(map[string]interface{})["children"].([]interface{}); ok {
		for _, c := range children {
			countNodes(c)
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📂 文件树已获取（%d 个目录，%d 个文件）", totalDirs, totalFiles),
		Tool:    "get_ide_filetree_status",
		RawResult: map[string]interface{}{
			"dir_count": totalDirs,
			"file_count": totalFiles,
			"tree":       snapshot,
		},
	}
}

// HandleSetFileTreeStatus handles set_ide_filetree_status tool.
// It sends a filetree:set event to the frontend with operate/target,
// blocks via waitToolResult, and returns success/failure.
func HandleSetFileTreeStatus(workDir string, writeEvent func(string, map[string]interface{}), waitToolResult func(uuid string) *types.ToolResult, args map[string]interface{}) *types.ToolResult {
	operate, _ := args["operate"].(string)
	target, _ := args["target"].(string)

	if operate == "" || target == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "operate and target parameters are required",
			Output:  "❌ 文件树操作失败：operate 和 target 参数必填",
			Tool:    "set_ide_filetree_status",
		}
	}

	if operate != "expand" && operate != "collapse" && operate != "select" {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("invalid operate: %s (supported: expand, collapse, select)", operate),
			Output:  fmt.Sprintf("❌ 文件树操作失败：无效操作 '%s'", operate),
			Tool:    "set_ide_filetree_status",
		}
	}

	if waitToolResult == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "set_ide_filetree_status requires daemon mode (waitToolResult not available)",
			Output:  "❌ 文件树操作失败：需要守护进程模式",
			Tool:    "set_ide_filetree_status",
		}
	}

	requestID := fmt.Sprintf("filetree_set_%d", time.Now().UnixMilli())

	// Emit event to IDE → forwards to frontend → frontend performs operation
	if writeEvent != nil {
		writeEvent("filetree:set", map[string]interface{}{
			"request_id": requestID,
			"operate":    operate,
			"target":     target,
			"work_dir":   workDir,
		})
	}

	// Block until IDE sends tool_result command via daemon stdin
	result := waitToolResult(requestID)
	if !result.Success {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("file tree set failed: %s", result.Output),
			Output:  fmt.Sprintf("❌ 文件树操作失败：%s", result.Output),
			Tool:    "set_ide_filetree_status",
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📂 文件树已%s：%s", operate, target),
		Tool:    "set_ide_filetree_status",
		RawResult: map[string]interface{}{
			"operate": operate,
			"target":  target,
		},
	}
}

package toolhandler

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// SimplifyToolCall turns a single tool_call + tool_result pair into a thinking text.
func SimplifyToolCall(toolName string, args json.RawMessage, result string) string {
	return types.SimplifyToolCall(toolName, args, result)
}

// SimplifyToolCallWithWorkDir is like SimplifyToolCall but writes long errors to a temp file
// when workDir is provided, so the LLM can read the full error via read_file.
func SimplifyToolCallWithWorkDir(toolName string, args json.RawMessage, result string, workDir string) string {
	return types.SimplifyToolCallWithWorkDir(toolName, args, result, workDir)
}

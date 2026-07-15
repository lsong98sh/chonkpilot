package llmresult

import (
	"fmt"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleGetLLMResult retrieves the assistant's response from a specific turn in a sub-session.
// Parameters:
//   - session_id: the sub-session ID
//   - turn_id: the turn ID
//
// Returns the assistant message content from that turn.
func HandleGetLLMResult(dbDir string, args map[string]interface{}) *types.ToolResult {
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "'session_id' is required",
			Tool:    "get_llm_result",
		}
	}

	turnID, _ := args["turn_id"].(string)
	if turnID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "'turn_id' is required",
			Tool:    "get_llm_result",
		}
	}

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to open database: %s", err.Error()),
			Tool:    "get_llm_result",
		}
	}
	defer db.Close(sqlDB)

	messages, err := db.GetMessagesByTurn(sqlDB, turnID)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to get messages: %s", err.Error()),
			Tool:    "get_llm_result",
		}
	}

	// Find the last assistant message in this turn
	var lastAssistant string
	for _, msg := range messages {
		if msg.Role == "assistant" && msg.Content != "" {
			lastAssistant = msg.Content
		}
	}

	if lastAssistant == "" {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("no assistant message found in turn %s (session %s)", turnID, sessionID),
			Tool:    "get_llm_result",
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  lastAssistant,
		Tool:    "get_llm_result",
	}
}

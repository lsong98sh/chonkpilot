package llmresult

import (
	"encoding/json"
	"fmt"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleGetLLMResult retrieves the assistant's response from specific turns in a sub-session.
// Parameters:
//   - session_id: the sub-session ID (required)
//   - turn_ids: array of turn IDs (max 20); or
//   - turn_id: single turn ID (backward compatibility)
//
// Returns a JSON object mapping each turn_id to its assistant text content, or an error message.
func HandleGetLLMResult(dbDir string, args map[string]interface{}) *types.ToolResult {
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "'session_id' is required",
			Tool:    "get_llm_result",
			Output:  "❌ 缺少 session_id 参数",
			RawResult: map[string]interface{}{
				"error": "'session_id' is required",
			},
		}
	}

	// Collect turn IDs: prefer turn_ids (array), fall back to turn_id (string)
	var turnIDs []string
	if rawIDs, ok := args["turn_ids"]; ok {
		switch v := rawIDs.(type) {
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					turnIDs = append(turnIDs, s)
				}
			}
		case []string:
			turnIDs = v
		}
	}
	if len(turnIDs) == 0 {
		if tid, ok := args["turn_id"].(string); ok && tid != "" {
			turnIDs = []string{tid}
		}
	}
	if len(turnIDs) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "'turn_ids' or 'turn_id' is required",
			Tool:    "get_llm_result",
			Output:  "❌ 缺少 turn_ids 或 turn_id 参数",
			RawResult: map[string]interface{}{
				"error": "'turn_ids' or 'turn_id' is required",
			},
		}
	}
	if len(turnIDs) > 20 {
		turnIDs = turnIDs[:20]
	}

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to open database: %s", err.Error()),
			Tool:    "get_llm_result",
			Output:  "❌ 打开数据库失败",
			RawResult: map[string]interface{}{
				"error":  err.Error(),
				"db_dir": dbDir,
			},
		}
	}
	defer db.Close(sqlDB)

	// Collect results for each turn_id
	results := make(map[string]interface{})
	for _, turnID := range turnIDs {
		messages, err := db.GetMessagesByTurn(sqlDB, turnID)
		if err != nil {
			results[turnID] = map[string]interface{}{
				"error": fmt.Sprintf("failed to get messages: %s", err.Error()),
			}
			continue
		}

		// Find the last assistant message in this turn
		var lastAssistant string
		for _, msg := range messages {
			if msg.Role == "assistant" && msg.Content != "" {
				lastAssistant = msg.Content
			}
		}

		if lastAssistant == "" {
			results[turnID] = map[string]interface{}{
				"error": fmt.Sprintf("no assistant message found in turn %s (session %s)", turnID, sessionID),
			}
		} else {
			results[turnID] = map[string]interface{}{
				"content": lastAssistant,
			}
		}
	}

	outputJSON, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to marshal results: %s", err.Error()),
			Tool:    "get_llm_result",
			Output:  "❌ 序列化结果失败",
			RawResult: map[string]interface{}{
				"error": err.Error(),
			},
		}
	}

	// Count successful vs error results
	totalTurns := len(turnIDs)
	successCount := 0
	for _, v := range results {
		if m, ok := v.(map[string]interface{}); ok {
			if _, hasContent := m["content"]; hasContent {
				successCount++
			}
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("✅ 获取到 %d/%d 个 turn 的 LLM 结果\n\n%s", successCount, totalTurns, string(outputJSON)),
		Tool:    "get_llm_result",
		RawResult: map[string]interface{}{
			"results":       results,
			"total_turns":   totalTurns,
			"success_count": successCount,
		},
	}
}

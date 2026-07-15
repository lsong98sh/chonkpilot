package models

import "time"

// Turn represents a single turn in a session. Acts as a grouping container
// for messages belonging to one user↔assistant interaction cycle.
// Actual message content is stored in the messages table.
type Turn struct {
	TurnID    string `json:"turn_id"`
	SessionID string `json:"session_id"`
	Score     int    `json:"score"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// TurnResult holds the result of executing a turn.
type TurnResult struct {
	TurnID string `json:"turn_id"`
	A      string `json:"a"`
	Score  int    `json:"score"`
}

// NewTurn creates a new Turn with generated ID and timestamps.
func NewTurn(turnID, sessionID string) *Turn {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Turn{
		TurnID:    turnID,
		SessionID: sessionID,
		Score:     0,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

package db

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// AddMessage inserts a new message record.
func AddMessage(db *sql.DB, msg *models.Message) error {
	_, err := db.Exec(
		`INSERT INTO messages (message_id, turn_id, role, type, content, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		msg.MessageID, msg.TurnID, msg.Role, msg.Type, msg.Content, msg.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to add message: %w", err)
	}
	return nil
}

// GetMessagesByTurn returns all messages for a turn ordered by created_at.
func GetMessagesByTurn(db *sql.DB, turnID string) ([]*models.Message, error) {
	rows, err := db.Query(
		`SELECT message_id, turn_id, role, COALESCE(type,'text'), COALESCE(content,''), created_at FROM messages WHERE turn_id = ? ORDER BY created_at ASC`,
		turnID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	defer rows.Close()

	var messages []*models.Message
	for rows.Next() {
		m := &models.Message{}
		if err := rows.Scan(&m.MessageID, &m.TurnID, &m.Role, &m.Type, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, m)
	}
	if messages == nil {
		messages = []*models.Message{}
	}
	return messages, rows.Err()
}

// GetMessagesBySession returns all messages across all turns in a session.
func GetMessagesBySession(db *sql.DB, sessionID string) ([]*models.Message, error) {
	rows, err := db.Query(
		`SELECT m.message_id, m.turn_id, m.role, COALESCE(m.type,'text'), COALESCE(m.content,''), m.created_at
		 FROM messages m
		 JOIN turns t ON t.turn_id = m.turn_id
		 WHERE t.session_id = ?
		 ORDER BY t.created_at ASC, m.created_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}
	defer rows.Close()

	var messages []*models.Message
	for rows.Next() {
		m := &models.Message{}
		if err := rows.Scan(&m.MessageID, &m.TurnID, &m.Role, &m.Type, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan session message: %w", err)
		}
		messages = append(messages, m)
	}
	if messages == nil {
		messages = []*models.Message{}
	}
	return messages, rows.Err()
}

// LatestSessionID returns the ID of the session that has the most recent message.
// Returns empty string if no messages exist.
func GetLatestSessionID(sqlDB *sql.DB) (string, error) {
	var sessionID string
	err := sqlDB.QueryRow(
		`SELECT t.session_id FROM messages m
		 JOIN turns t ON t.turn_id = m.turn_id
		 ORDER BY m.created_at DESC LIMIT 1`,
	).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get latest session: %w", err)
	}
	return sessionID, nil
}

// GetMessage returns a single message by ID.
func GetMessage(sqlDB *sql.DB, messageID string) (*models.Message, error) {
	m := &models.Message{}
	err := sqlDB.QueryRow(
		`SELECT message_id, turn_id, role, COALESCE(type,'text'), COALESCE(content,''), created_at FROM messages WHERE message_id = ?`,
		messageID,
	).Scan(&m.MessageID, &m.TurnID, &m.Role, &m.Type, &m.Content, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	return m, nil
}

// GetToolResultByCallID returns the tool result content for a given tool_call_id within a session.
func GetToolResultByCallID(sqlDB *sql.DB, sessionID, toolCallID string) (string, error) {
	rows, err := sqlDB.Query(
		`SELECT m.content FROM messages m
		 JOIN turns t ON t.turn_id = m.turn_id
		 WHERE t.session_id = ? AND m.role = 'tool' AND m.type = 'tool_result'`,
		sessionID,
	)
	if err != nil {
		return "", fmt.Errorf("failed to query tool results: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return "", fmt.Errorf("failed to scan tool result: %w", err)
		}
		var tp models.ToolResultPayload
		if err := json.Unmarshal([]byte(content), &tp); err == nil && tp.ToolCallID == toolCallID {
			return tp.Result, nil
		}
	}
	return "", fmt.Errorf("tool result not found: %s", toolCallID)
}

// nullString returns a *string for nullable SQL columns.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

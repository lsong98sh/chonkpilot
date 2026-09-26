package models

import "time"

// Session represents a conversation session.
type Session struct {
	SessionID    string `json:"session_id"`
	ParentID     string `json:"parent_id,omitempty"`
	WorkDir      string `json:"work_dir"`
	Title        string `json:"title"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	LastActivity string `json:"last_activity,omitempty"` // 最近一条消息时间（bbolt 下维护，替代 SQL 聚合查询）
}

// NewSession creates a new Session with generated ID and timestamps.
func NewSession(sessionID, parentID, workDir, title string) *Session {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Session{
		SessionID: sessionID,
		ParentID:  parentID,
		WorkDir:   workDir,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

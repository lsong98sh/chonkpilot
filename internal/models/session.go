package models

import "time"

// Session represents a conversation session.
type Session struct {
	SessionID string `json:"session_id"`
	ParentID  string `json:"parent_id,omitempty"`
	WorkDir   string `json:"work_dir"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
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

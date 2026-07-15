package db

import (
	"testing"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

func TestGetTurn(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)

	turn := models.NewTurn("turn-1", "test-session-1")
	if err := CreateTurn(db, turn); err != nil {
		t.Fatalf("CreateTurn() failed: %v", err)
	}

	got, err := GetTurn(db, "turn-1")
	if err != nil {
		t.Fatalf("GetTurn() failed: %v", err)
	}
	if got.TurnID != turn.TurnID {
		t.Errorf("TurnID = %q, want %q", got.TurnID, turn.TurnID)
	}
	if got.SessionID != turn.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, turn.SessionID)
	}
}

func TestGetTurn_NotFound(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := GetTurn(db, "non-existent-turn")
	if err == nil {
		t.Error("GetTurn() should return error for non-existent turn")
	}
}

func TestCreateTurn_DuplicateID(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)

	turn := models.NewTurn("turn-dup", "test-session-1")
	if err := CreateTurn(db, turn); err != nil {
		t.Fatalf("CreateTurn() failed: %v", err)
	}

	// Duplicate turn_id should fail (primary key constraint)
	dup := models.NewTurn("turn-dup", "test-session-1")
	if err := CreateTurn(db, dup); err == nil {
		t.Error("CreateTurn() should fail for duplicate turn_id")
	}
}

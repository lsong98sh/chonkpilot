package db

import (
	"testing"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

func TestCreateNote(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	note := models.NewNote("test-note", "This is a test note")
	if err := CreateNote(db, note); err != nil {
		t.Fatalf("CreateNote() failed: %v", err)
	}
}

func TestCreateNote_DuplicateTitle(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	note := models.NewNote("dup-title", "first content")
	if err := CreateNote(db, note); err != nil {
		t.Fatalf("CreateNote() failed: %v", err)
	}

	// Creating another note with the same title should fail
	dup := models.NewNote("dup-title", "second content")
	if err := CreateNote(db, dup); err == nil {
		t.Error("CreateNote() should fail for duplicate title")
	}
}

func TestGetNote(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	note := models.NewNote("get-test", "content for get test")
	if err := CreateNote(db, note); err != nil {
		t.Fatalf("CreateNote() failed: %v", err)
	}

	got, err := GetNote(db, "get-test")
	if err != nil {
		t.Fatalf("GetNote() failed: %v", err)
	}
	if got.Title != "get-test" {
		t.Errorf("Title = %q, want %q", got.Title, "get-test")
	}
	if got.Content != "content for get test" {
		t.Errorf("Content = %q, want %q", got.Content, "content for get test")
	}
}

func TestGetNote_NotFound(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := GetNote(db, "non-existent-note")
	if err == nil {
		t.Error("GetNote() should return error for non-existent note")
	}
}

func TestUpdateNote(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	note := models.NewNote("update-test", "original content")
	if err := CreateNote(db, note); err != nil {
		t.Fatalf("CreateNote() failed: %v", err)
	}

	if err := UpdateNote(db, "update-test", "updated content"); err != nil {
		t.Fatalf("UpdateNote() failed: %v", err)
	}

	got, err := GetNote(db, "update-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "updated content" {
		t.Errorf("Content = %q, want %q", got.Content, "updated content")
	}
}

func TestUpdateNote_NotFound(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	err := UpdateNote(db, "non-existent", "content")
	if err == nil {
		t.Error("UpdateNote() should return error for non-existent note")
	}
}

func TestDeleteNote(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	note := models.NewNote("delete-test", "to be deleted")
	if err := CreateNote(db, note); err != nil {
		t.Fatalf("CreateNote() failed: %v", err)
	}

	if err := DeleteNote(db, "delete-test"); err != nil {
		t.Fatalf("DeleteNote() failed: %v", err)
	}

	_, err := GetNote(db, "delete-test")
	if err == nil {
		t.Error("note should be deleted")
	}
}

func TestDeleteNote_NotFound(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	err := DeleteNote(db, "non-existent")
	if err == nil {
		t.Error("DeleteNote() should return error for non-existent note")
	}
}

func TestListNotes(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	notes, err := ListNotes(db)
	if err != nil {
		t.Fatalf("ListNotes() failed: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("expected 0 notes, got %d", len(notes))
	}

	n1 := models.NewNote("note-a", "content a")
	n2 := models.NewNote("note-b", "content b")
	if err := CreateNote(db, n1); err != nil {
		t.Fatal(err)
	}
	if err := CreateNote(db, n2); err != nil {
		t.Fatal(err)
	}

	notes, err = ListNotes(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Errorf("expected 2 notes, got %d", len(notes))
	}
}

func TestListNotes_OrderedByUpdatedAt(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	n1 := models.NewNote("first", "content")
	if err := CreateNote(db, n1); err != nil {
		t.Fatal(err)
	}

	n2 := models.NewNote("second", "content")
	if err := CreateNote(db, n2); err != nil {
		t.Fatal(err)
	}

	// Update the first note so its updated_at is newer
	if err := UpdateNote(db, "first", "updated content"); err != nil {
		t.Fatal(err)
	}

	notes, err := ListNotes(db)
	if err != nil {
		t.Fatal(err)
	}
	// The updated note should appear first (ORDER BY updated_at DESC)
	if len(notes) >= 2 && notes[0].Title != "first" {
		t.Errorf("expected 'first' to be first (most recently updated), got %q", notes[0].Title)
	}
}

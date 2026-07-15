package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// setupTestDB creates a temporary directory and SQLite database for testing.
func setupTestDB(t *testing.T) (workDir string, db *sql.DB, cleanup func()) {
	t.Helper()
	workDir, err := os.MkdirTemp("", "chonkpilot-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	// Create .ide directory
	ideDir := filepath.Join(workDir, ".ide")
	if err := os.MkdirAll(ideDir, 0755); err != nil {
		os.RemoveAll(workDir)
		t.Fatalf("failed to create .ide dir: %v", err)
	}

	dbPath := filepath.Join(ideDir, "ide.db")
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		os.RemoveAll(workDir)
		t.Fatalf("failed to open test db: %v", err)
	}

	// Run migrations
	if err := RunMigrations(d); err != nil {
		d.Close()
		os.RemoveAll(workDir)
		t.Fatalf("failed to run migrations: %v", err)
	}

	cleanup = func() {
		d.Close()
		os.RemoveAll(workDir)
	}

	return workDir, d, cleanup
}

func TestOpen(t *testing.T) {
	workDir, err := os.MkdirTemp("", "chonkpilot-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(workDir)

	ideDir := filepath.Join(workDir, ".ide")
	if err := os.MkdirAll(ideDir, 0755); err != nil {
		t.Fatalf("failed to create .ide dir: %v", err)
	}

	db, err := Open(workDir)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer Close(db)

	if db == nil {
		t.Fatal("Open() returned nil db")
	}
}

func TestRunMigrations(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	// Verify tables exist
	tables := []string{"sessions", "turns", "messages", "tasks", "rules", "config"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err == sql.ErrNoRows {
			t.Errorf("table %s does not exist", table)
		} else if err != nil {
			t.Errorf("error checking table %s: %v", table, err)
		}
	}
}

func TestCreateSession(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	if err := CreateSession(db, s); err != nil {
		t.Fatalf("CreateSession() failed: %v", err)
	}
}

func TestGetSession(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	if err := CreateSession(db, s); err != nil {
		t.Fatalf("CreateSession() failed: %v", err)
	}

	got, err := GetSession(db, "test-session-1")
	if err != nil {
		t.Fatalf("GetSession() failed: %v", err)
	}
	if got.SessionID != s.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, s.SessionID)
	}
	if got.WorkDir != s.WorkDir {
		t.Errorf("WorkDir = %q, want %q", got.WorkDir, s.WorkDir)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := GetSession(db, "non-existent")
	if err == nil {
		t.Error("GetSession() should return error for non-existent session")
	}
}

func TestListSessions(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	sessions, err := ListSessions(db)
	if err != nil {
		t.Fatalf("ListSessions() failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}

	// Create two sessions
	s1 := models.NewSession("s1", "", "/tmp/a", "Session A")
	s2 := models.NewSession("s2", "", "/tmp/b", "Session B")
	if err := CreateSession(db, s1); err != nil {
		t.Fatal(err)
	}
	if err := CreateSession(db, s2); err != nil {
		t.Fatal(err)
	}

	sessions, err = ListSessions(db)
	if err != nil {
		t.Fatalf("ListSessions() failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestDeleteSession(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	if err := CreateSession(db, s); err != nil {
		t.Fatalf("CreateSession() failed: %v", err)
	}

	if err := DeleteSession(db, "test-session-1"); err != nil {
		t.Fatalf("DeleteSession() failed: %v", err)
	}

	_, err := GetSession(db, "test-session-1")
	if err == nil {
		t.Error("session should be deleted")
	}
}

func TestCreateTurn(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)

	turn := models.NewTurn("turn-1", "test-session-1")
	if err := CreateTurn(db, turn); err != nil {
		t.Fatalf("CreateTurn() failed: %v", err)
	}
}

func TestGetTurnsBySession(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)

	t1 := models.NewTurn("turn-1", "test-session-1")
	t2 := models.NewTurn("turn-2", "test-session-1")
	CreateTurn(db, t1)
	CreateTurn(db, t2)

	turns, err := GetTurnsBySession(db, "test-session-1")
	if err != nil {
		t.Fatalf("GetTurnsBySession() failed: %v", err)
	}
	if len(turns) != 2 {
		t.Errorf("expected 2 turns, got %d", len(turns))
	}
}

func TestUpdateTurnResult(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)
	turn := models.NewTurn("turn-1", "test-session-1")
	CreateTurn(db, turn)

	if err := UpdateTurnResult(db, "turn-1", 5); err != nil {
		t.Fatalf("UpdateTurnResult() failed: %v", err)
	}

	// Verify by getting the turn
	turns, err := GetTurnsBySession(db, "test-session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}
	if turns[0].Score != 5 {
		t.Errorf("Score = %d, want 5", turns[0].Score)
	}
}

func TestAddMessage(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)
	turn := models.NewTurn("turn-1", "test-session-1")
	CreateTurn(db, turn)

	msg := models.NewMessage("msg-1", "turn-1", "user", "text", "Hello")
	if err := AddMessage(db, msg); err != nil {
		t.Fatalf("AddMessage() failed: %v", err)
	}
}

func TestGetMessagesByTurn(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)
	turn := models.NewTurn("turn-1", "test-session-1")
	CreateTurn(db, turn)

	m1 := models.NewMessage("msg-1", "turn-1", "user", "text", "Hello")
	m2 := models.NewMessage("msg-2", "turn-1", "assistant", "text", "Hi there!")
	AddMessage(db, m1)
	AddMessage(db, m2)

	msgs, err := GetMessagesByTurn(db, "turn-1")
	if err != nil {
		t.Fatalf("GetMessagesByTurn() failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
}

func TestCreateTask(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)
	turn := models.NewTurn("turn-1", "test-session-1")
	CreateTurn(db, turn)

	task := models.NewTask("task-1", "", "turn-1", "test-session-1", "Build", 0)
	if err := CreateTask(db, task); err != nil {
		t.Fatalf("CreateTask() failed: %v", err)
	}
}

func TestGetTaskByID(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)
	turn := models.NewTurn("turn-1", "test-session-1")
	CreateTurn(db, turn)
	task := models.NewTask("task-1", "", "turn-1", "test-session-1", "Build", 0)
	CreateTask(db, task)

	got, err := GetTaskByID(db, "task-1")
	if err != nil {
		t.Fatalf("GetTaskByID() failed: %v", err)
	}
	if got.Name != "Build" {
		t.Errorf("Name = %q, want %q", got.Name, "Build")
	}
}

func TestUpdateTaskStatus(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)
	turn := models.NewTurn("turn-1", "test-session-1")
	CreateTurn(db, turn)
	task := models.NewTask("task-1", "", "turn-1", "test-session-1", "Build", 0)
	CreateTask(db, task)

	if err := UpdateTaskStatus(db, "task-1", models.TaskStatusRunning, 50, ""); err != nil {
		t.Fatalf("UpdateTaskStatus() failed: %v", err)
	}

	got, _ := GetTaskByID(db, "task-1")
	if got.Status != models.TaskStatusRunning {
		t.Errorf("Status = %q, want %q", got.Status, models.TaskStatusRunning)
	}
	if got.Progress != 50 {
		t.Errorf("Progress = %d, want 50", got.Progress)
	}
}

func TestCancelTaskCascade(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)
	turn := models.NewTurn("turn-1", "test-session-1")
	CreateTurn(db, turn)

	p1 := models.NewTask("parent", "", "turn-1", "test-session-1", "Parent", 0)
	CreateTask(db, p1)
	c1 := models.NewTask("child1", "parent", "turn-1", "test-session-1", "Child 1", 1)
	CreateTask(db, c1)
	c2 := models.NewTask("child2", "parent", "turn-1", "test-session-1", "Child 2", 1)
	CreateTask(db, c2)

	if err := CancelTaskCascade(db, "parent"); err != nil {
		t.Fatalf("CancelTaskCascade() failed: %v", err)
	}

	parent, _ := GetTaskByID(db, "parent")
	if parent.Status != models.TaskStatusCancelled {
		t.Errorf("parent Status = %q, want %q", parent.Status, models.TaskStatusCancelled)
	}
	child1, _ := GetTaskByID(db, "child1")
	if child1.Status != models.TaskStatusCancelled {
		t.Errorf("child1 Status = %q", child1.Status)
	}
	child2, _ := GetTaskByID(db, "child2")
	if child2.Status != models.TaskStatusCancelled {
		t.Errorf("child2 Status = %q", child2.Status)
	}
}

func TestSetAndGetConfig(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	if err := SetConfig(db, "llm.provider", "openai"); err != nil {
		t.Fatalf("SetConfig() failed: %v", err)
	}

	val, err := GetConfig(db, "llm.provider")
	if err != nil {
		t.Fatalf("GetConfig() failed: %v", err)
	}
	if val != "openai" {
		t.Errorf("value = %q, want %q", val, "openai")
	}
}

func TestGetAllConfig(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	SetConfig(db, "key1", "value1")
	SetConfig(db, "key2", "value2")

	configs, err := GetAllConfig(db)
	if err != nil {
		t.Fatalf("GetAllConfig() failed: %v", err)
	}
	if len(configs) != 2 {
		t.Errorf("expected 2 configs, got %d", len(configs))
	}
	if configs["key1"] != "value1" {
		t.Errorf("key1 = %q", configs["key1"])
	}
}

func TestSaveAndGetRule(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	rule := &models.Rule{
		Name:     "go-style",
		Category: "language",
		Content:  "Use Go conventions",
	}
	if err := SaveRule(db, rule); err != nil {
		t.Fatalf("SaveRule() failed: %v", err)
	}

	got, err := GetRule(db, "go-style")
	if err != nil {
		t.Fatalf("GetRule() failed: %v", err)
	}
	if got.Category != "language" {
		t.Errorf("Category = %q", got.Category)
	}
}

func TestGetRulesByCategory(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	rules := []*models.Rule{
		{Name: "r1", Category: "lang", Content: "c1"},
		{Name: "r2", Category: "lang", Content: "c2"},
		{Name: "r3", Category: "other", Content: "c3"},
	}
	for _, r := range rules {
		SaveRule(db, r)
	}

	langRules, err := GetRulesByCategory(db, "lang")
	if err != nil {
		t.Fatalf("GetRulesByCategory() failed: %v", err)
	}
	if len(langRules) != 2 {
		t.Errorf("expected 2 lang rules, got %d", len(langRules))
	}
}

func TestDeleteRule(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	SaveRule(db, &models.Rule{Name: "test-rule", Category: "test", Content: "test"})
	if err := DeleteRule(db, "test-rule"); err != nil {
		t.Fatalf("DeleteRule() failed: %v", err)
	}

	_, err := GetRule(db, "test-rule")
	if err == nil {
		t.Error("rule should be deleted")
	}
}

func TestUnfinishedTasks(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)
	turn := models.NewTurn("turn-1", "test-session-1")
	CreateTurn(db, turn)

	t1 := models.NewTask("t1", "", "turn-1", "test-session-1", "Running Task", 0)
	CreateTask(db, t1)
	UpdateTaskStatus(db, "t1", models.TaskStatusRunning, 50, "")

	t2 := models.NewTask("t2", "", "turn-1", "test-session-1", "Paused Task", 0)
	CreateTask(db, t2)
	UpdateTaskStatus(db, "t2", models.TaskStatusPaused, 30, "")

	t3 := models.NewTask("t3", "", "turn-1", "test-session-1", "Completed Task", 0)
	CreateTask(db, t3)
	UpdateTaskStatus(db, "t3", models.TaskStatusCompleted, 100, "")

	unfinished, err := GetUnfinishedTasks(db)
	if err != nil {
		t.Fatalf("GetUnfinishedTasks() failed: %v", err)
	}
	if len(unfinished) != 2 {
		t.Errorf("expected 2 unfinished tasks, got %d", len(unfinished))
	}
}

func TestWithDB(t *testing.T) {
	workDir, err := os.MkdirTemp("", "chonkpilot-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	os.MkdirAll(filepath.Join(workDir, ".ide"), 0755)

	err = WithDB(workDir, func(d *sql.DB) error {
		return RunMigrations(d)
	})
	if err != nil {
		t.Fatalf("WithDB() failed: %v", err)
	}
}

func TestGetTasksByTurn(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)
	turn := models.NewTurn("turn-1", "test-session-1")
	CreateTurn(db, turn)

	CreateTask(db, models.NewTask("t1", "", "turn-1", "test-session-1", "Task 1", 0))
	CreateTask(db, models.NewTask("t2", "", "turn-1", "test-session-1", "Task 2", 0))

	tasks, err := GetTasksByTurn(db, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestGetTasksByParent(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()

	s := models.NewSession("test-session-1", "", "/tmp/test", "Test Session")
	CreateSession(db, s)
	turn := models.NewTurn("turn-1", "test-session-1")
	CreateTurn(db, turn)

	CreateTask(db, models.NewTask("parent", "", "turn-1", "test-session-1", "Parent", 0))
	CreateTask(db, models.NewTask("c1", "parent", "turn-1", "test-session-1", "Child 1", 1))
	CreateTask(db, models.NewTask("c2", "parent", "turn-1", "test-session-1", "Child 2", 1))

	children, err := GetTasksByParent(db, "parent")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 {
		t.Errorf("expected 2 children, got %d", len(children))
	}
}

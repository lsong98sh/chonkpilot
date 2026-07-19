package context

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

func setupContextTest(t *testing.T) (string, func()) {
	t.Helper()
	workDir, err := os.MkdirTemp("", "chonkpilot-ctx-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	ideDir := filepath.Join(workDir, ".ide")
	os.MkdirAll(ideDir, 0755)

	cleanup := func() {
		os.RemoveAll(workDir)
	}

	return workDir, cleanup
}

func TestNewManager(t *testing.T) {
	workDir, cleanup := setupContextTest(t)
	defer cleanup()

	m := NewManager(workDir)
	if m == nil {
		t.Fatal("NewManager() returned nil")
	}
	if m.keepFullTurns != 6 {
		t.Errorf("expected default keepFullTurns=6, got %d", m.keepFullTurns)
	}
}

func TestEstimateSimplifiedTokens(t *testing.T) {
	msgs := []*models.Message{
		{Role: "user", Content: "hello world"},                                                  // 11 chars
		{Role: "assistant", Content: "this is a longer response message for testing purposes"},   // 51 chars
		{Role: "user", Content: "short"},                                                         // 5 chars
	}

	total := EstimateSimplifiedTokens(msgs, "summary text") // 11 chars
	// (11+51+5)/4 + 11/4 = 67/4 + 11/4 = 16 + 2 = 18
	if total < 10 || total > 30 {
		t.Errorf("expected reasonable token estimate, got %d", total)
	}

	// Empty messages
	empty := EstimateSimplifiedTokens(nil, "")
	if empty != 0 {
		t.Errorf("expected 0 for empty input, got %d", empty)
	}
}

func TestGroupTurns(t *testing.T) {
	msgs := []*models.Message{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "first response", Type: "text"},
		{Role: "user", Content: "second prompt"},
		{Role: "assistant", Content: "second response", Type: "text"},
		{Role: "user", Content: "third prompt"},
	}

	turns := groupTurns(msgs)
	if len(turns) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(turns))
	}
	if len(turns[0]) != 2 {
		t.Errorf("turn 0: expected 2 messages, got %d", len(turns[0]))
	}
	if len(turns[1]) != 2 {
		t.Errorf("turn 1: expected 2 messages, got %d", len(turns[1]))
	}
	if len(turns[2]) != 1 {
		t.Errorf("turn 2: expected 1 message, got %d", len(turns[2]))
	}
}

func TestProcessFullTurn(t *testing.T) {
	turn := []*models.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there", Type: "text"},
	}

	result := processFullTurn(turn)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "user" || result[0].Content != "hello" {
		t.Error("first message should be user/hello")
	}
	if result[1].Role != "assistant" || result[1].Content != "hi there" {
		t.Error("second message should be assistant/hi there")
	}
}

func TestProcessSimplifiedTurn(t *testing.T) {
	// Turn with tool_call + tool_result
	turn := []*models.Message{
		{Role: "user", Content: "read the file"},
		{Role: "assistant", Content: "Let me read it", Type: "reasoning"},
		{Role: "assistant", Content: `{"tool_call_id":"tc1","name":"read_file","arguments":"{\"path\":\"main.go\"}"}`, Type: "tool_call"},
		{Role: "tool", Content: `{"tool_call_id":"tc1","name":"read_file","result":"package main\nfunc main() {}"}`},
	}

	result := processSimplifiedTurn(turn, "")
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (user + simplified assistant), got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Error("first message should be user")
	}
	if result[1].Role != "assistant" {
		t.Error("second message should be assistant")
	}
	if result[1].ReasoningContent == "" {
		t.Error("simplified turn should have reasoning content")
	}
}

func TestBuildLLMContext(t *testing.T) {
	m := NewManager("/tmp/test")
	m.SetKeepFullTurns(2)

	// 4 turns with keepFullTurns=2:
	// turn 3-4 = full, turn 1-2 = simplified
	msgs := []*models.Message{
		{Role: "user", Content: "turn 1"},
		{Role: "assistant", Content: "response 1", Type: "text"},
		{Role: "user", Content: "turn 2"},
		{Role: "assistant", Content: "response 2", Type: "text"},
		{Role: "user", Content: "turn 3"},
		{Role: "assistant", Content: "response 3", Type: "text"},
		{Role: "user", Content: "turn 4"},
		{Role: "assistant", Content: "response 4", Type: "text"},
	}

	result := m.BuildLLMContext(msgs, "test-session")
	if len(result) == 0 {
		t.Fatal("BuildLLMContext returned empty")
	}

	// Should include: turn1 user + turn1 simplified assistant + turn2 user + turn2 simplified assistant
	// + turn3 user + turn3 assistant + turn4 user + turn4 assistant = 8 messages
	// (no summary since session has no DB-backed summary)
	if len(result) != 8 {
		t.Errorf("expected 8 messages, got %d (full=2, remaining simplified)", len(result))
	}
}

func TestBuildLLMContextOneTurn(t *testing.T) {
	m := NewManager("/tmp/test")

	msgs := []*models.Message{
		{Role: "user", Content: "only turn"},
		{Role: "assistant", Content: "only response", Type: "text"},
	}

	result := m.BuildLLMContext(msgs, "test-session")
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
}

func TestBuildLLMContextEmpty(t *testing.T) {
	m := NewManager("/tmp/test")
	result := m.BuildLLMContext(nil, "test-session")
	if result != nil {
		t.Error("expected nil for empty input")
	}
}

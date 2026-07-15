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
}

func TestShouldCompress(t *testing.T) {
	workDir, cleanup := setupContextTest(t)
	defer cleanup()

	m := NewManager(workDir)

	// Few turns, few tokens — no compression
	if m.ShouldCompress(3, 1000) {
		t.Error("ShouldCompress(3, 1000) should be false")
	}

	// Many turns but few tokens — no compression (below min token threshold)
	if m.ShouldCompress(30, 1000) {
		t.Error("ShouldCompress(30, 1000) should be false (below min token threshold)")
	}

	// Many turns, many tokens — compression
	if !m.ShouldCompress(30, 50000) {
		t.Error("ShouldCompress(30, 50000) should be true")
	}

	// Few turns but massive tokens — compression
	if !m.ShouldCompress(3, 90000) {
		t.Error("ShouldCompress(3, 90000) should be true (exceeds max token threshold)")
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

	result := processSimplifiedTurn(turn)
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
	m.SetKeepFullTurns(1)
	m.SetKeepSimplifiedTurns(1)

	// 4 turns: current + 3 history
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

	// With keepFullTurns=1 and keepSimplifiedTurns=1:
	// turn 4 = full, turn 3 = simplified, turn 1-2 = summarized (no summary in DB → skipped)
	result := m.BuildLLMContext(msgs, "test-session")
	if len(result) == 0 {
		t.Fatal("BuildLLMContext returned empty")
	}

	// Should include: turn3 simplified (1 assistant msg) + turn4 full (user + assistant)
	if len(result) < 3 {
		t.Errorf("expected at least 3 messages, got %d", len(result))
	}
}

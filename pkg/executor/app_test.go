package executor

import (
	"os"
	"testing"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
)

// setupTestWorkDir creates a temp directory with .ide/ide.db and a test session.
func setupTestWorkDir(t *testing.T) (workDir string) {
	t.Helper()
	workDir, err := os.MkdirTemp("", "chonkpilot-executor-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(workDir) })

	// Create .ide/ide.db
	sqlDB, err := db.Open(workDir)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close(sqlDB)

	// Create test session
	s := models.NewSession("session-1", "", workDir, "Test Session")
	if err := db.CreateSession(sqlDB, s); err != nil {
		t.Fatalf("failed to create test session: %v", err)
	}
	return workDir
}

func TestParseArgs(t *testing.T) {
	workDir := setupTestWorkDir(t)

	args := []string{
		"--work-dir=" + workDir,
		"--prompt=Hello",
		"--session-id=session-1",
		"--llm-provider=openai",
		"--llm-model=gpt-4o",
		"--output=json",
	}
	ea, err := ParseArgs(args)
	if err != nil {
		t.Fatalf("ParseArgs() failed: %v", err)
	}
	if ea.WorkDir != workDir {
		t.Errorf("WorkDir = %q, want %q", ea.WorkDir, workDir)
	}
	if ea.Prompt != "Hello" {
		t.Errorf("Prompt = %q", ea.Prompt)
	}
	if ea.SessionID != "session-1" {
		t.Errorf("SessionID = %q", ea.SessionID)
	}
	if ea.TurnID != "" {
		t.Errorf("TurnID = %q, want empty", ea.TurnID)
	}
	if ea.LLMProtocol != "openai" {
		t.Errorf("LLMProtocol = %q", ea.LLMProtocol)
	}
	if ea.LLMModel != "gpt-4o" {
		t.Errorf("LLMModel = %q", ea.LLMModel)
	}
	if ea.OutputFormat != "json" {
		t.Errorf("OutputFormat = %q", ea.OutputFormat)
	}
}

func TestParseArgs_NoWorkDir(t *testing.T) {
	args := []string{"--prompt=hello"}
	ea, err := ParseArgs(args)
	if err != nil {
		t.Fatalf("ParseArgs() should not fail without --work-dir (falls back to Getwd): %v", err)
	}
	if ea.WorkDir == "" {
		t.Error("ParseArgs() should set WorkDir to current directory")
	}
}

func TestParseArgs_WithPromptFile(t *testing.T) {
	workDir := setupTestWorkDir(t)

	args := []string{
		"--work-dir=" + workDir,
		"--prompt-file=/tmp/prompt.txt",
	}
	ea, err := ParseArgs(args)
	if err != nil {
		t.Fatalf("ParseArgs() failed: %v", err)
	}
	if ea.PromptFile != "/tmp/prompt.txt" {
		t.Errorf("PromptFile = %q", ea.PromptFile)
	}
}

func TestTurnResult(t *testing.T) {
	r := TurnResult{
		TurnID: "turn-1",
		A:      "Answer",
		Score:  5,
	}
	if r.TurnID != "turn-1" {
		t.Errorf("TurnID = %q", r.TurnID)
	}
	if r.A != "Answer" {
		t.Errorf("A = %q", r.A)
	}
	if r.Score != 5 {
		t.Errorf("Score = %d", r.Score)
	}
}

func TestParseArgs_AllFields(t *testing.T) {
	workDir := setupTestWorkDir(t)

	args := []string{
		"--work-dir=" + workDir,
		"--prompt=Hello",
		"--system-prompt=Be helpful",
		"--session-id=session-1",
		"--pipe-path=/tmp/pipe",
		"--parent-pipe-path=/tmp/parent",
		"--tools=[{\"name\":\"test\"}]",
		"--output=json",
		"--llm-provider=openai",
		"--llm-model=gpt-4",
		"--llm-api-key=sk-test",
		"--llm-api-url=https://api.openai.com/v1",
		"--llm-config-file=/tmp/config.json",
	}
	ea, err := ParseArgs(args)
	if err != nil {
		t.Fatalf("ParseArgs() failed: %v", err)
	}
	if ea.WorkDir != workDir {
		t.Errorf("WorkDir = %q, want %q", ea.WorkDir, workDir)
	}
	if ea.Prompt != "Hello" {
		t.Error("Prompt mismatch")
	}
	if ea.SystemPrompt != "Be helpful" {
		t.Error("SystemPrompt mismatch")
	}
	if ea.SessionID != "session-1" {
		t.Error("SessionID mismatch")
	}
	if ea.TurnID != "" {
		t.Error("TurnID should be empty")
	}
	if ea.PipePath != "/tmp/pipe" {
		t.Error("PipePath mismatch")
	}
	if ea.ParentPipePath != "/tmp/parent" {
		t.Error("ParentPipePath mismatch")
	}
	if ea.Tools != `[{"name":"test"}]` {
		t.Error("Tools mismatch")
	}
	if ea.OutputFormat != "json" {
		t.Error("OutputFormat mismatch")
	}
	if ea.LLMProtocol != "openai" {
		t.Error("LLMProtocol mismatch")
	}
	if ea.LLMModel != "gpt-4" {
		t.Error("LLMModel mismatch")
	}
	if ea.LLMAPIKey != "sk-test" {
		t.Error("LLMAPIKey mismatch")
	}
	if ea.LLMAPIURL != "https://api.openai.com/v1" {
		t.Error("LLMAPIURL mismatch")
	}
	if ea.LLMConfigFile != "/tmp/config.json" {
		t.Error("LLMConfigFile mismatch")
	}
}

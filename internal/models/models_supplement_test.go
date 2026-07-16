package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewNote(t *testing.T) {
	note := NewNote("test-note", "test content")
	if note == nil {
		t.Fatal("NewNote returned nil")
	}
	if note.Title != "test-note" {
		t.Errorf("Title = %q, want %q", note.Title, "test-note")
	}
	if note.Content != "test content" {
		t.Errorf("Content = %q, want %q", note.Content, "test content")
	}
	if note.CreatedAt == "" {
		t.Error("CreatedAt should not be empty")
	}
	if note.UpdatedAt == "" {
		t.Error("UpdatedAt should not be empty")
	}
	// Verify timestamps are valid RFC3339
	_, err := time.Parse(time.RFC3339, note.CreatedAt)
	if err != nil {
		t.Errorf("CreatedAt is not valid RFC3339: %v", err)
	}
}

func TestLLMProvider_JSONRoundTrip(t *testing.T) {
	p := LLMProvider{
		Name:            "test",
		Protocol:        "openai",
		APIKey:          "sk-test-key",
		Model:           "gpt-4",
		BaseURL:         "https://api.openai.com/v1",
		Temperature:     0.7,
		MaxTokens:       4096,
		Thinking:        true,
		ReasoningEffort: "high",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var got LLMProvider
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if got.Name != p.Name {
		t.Errorf("Name = %q, want %q", got.Name, p.Name)
	}
	if got.Protocol != p.Protocol {
		t.Errorf("Protocol = %q, want %q", got.Protocol, p.Protocol)
	}
	if got.APIKey != p.APIKey {
		t.Errorf("APIKey = %q, want %q", got.APIKey, p.APIKey)
	}
	if got.Model != p.Model {
		t.Errorf("Model = %q, want %q", got.Model, p.Model)
	}
	if got.BaseURL != p.BaseURL {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, p.BaseURL)
	}
	if got.Temperature != p.Temperature {
		t.Errorf("Temperature = %v, want %v", got.Temperature, p.Temperature)
	}
	if got.MaxTokens != p.MaxTokens {
		t.Errorf("MaxTokens = %d, want %d", got.MaxTokens, p.MaxTokens)
	}
	if got.Thinking != p.Thinking {
		t.Errorf("Thinking = %v, want %v", got.Thinking, p.Thinking)
	}
	if got.ReasoningEffort != p.ReasoningEffort {
		t.Errorf("ReasoningEffort = %q, want %q", got.ReasoningEffort, p.ReasoningEffort)
	}
}

func TestLLMProvider_DefaultValues(t *testing.T) {
	p := LLMProvider{
		Name:     "default-test",
		Protocol: "anthropic",
		APIKey:   "sk-xxx",
	}

	if p.Temperature != 0 {
		t.Errorf("Temperature should default to 0, got %v", p.Temperature)
	}
	if p.MaxTokens != 0 {
		t.Errorf("MaxTokens should default to 0, got %d", p.MaxTokens)
	}
	if p.Thinking != false {
		t.Errorf("Thinking should default to false, got %v", p.Thinking)
	}
	if p.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort should default to empty, got %q", p.ReasoningEffort)
	}
}

func TestToolConfig(t *testing.T) {
	tc := ToolConfig{
		Name:        "my-tool",
		Type:        "shell",
		Command:     "echo hello",
		Description: "A test tool",
		Source:      "user",
	}

	if tc.Name != "my-tool" {
		t.Errorf("Name = %q", tc.Name)
	}
	if tc.Source != "user" {
		t.Errorf("Source = %q", tc.Source)
	}
}

func TestToolConfig_WithParameters(t *testing.T) {
	params := map[string]interface{}{
		"arg1": "value1",
		"arg2": 42,
	}
	tc := ToolConfig{
		Name:       "param-tool",
		Type:       "shell",
		Command:    "echo",
		Parameters: params,
	}

	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var got ToolConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	gotParams, ok := got.Parameters.(map[string]interface{})
	if !ok {
		t.Fatal("Parameters should be map[string]interface{}")
	}
	if gotParams["arg1"] != "value1" {
		t.Errorf("arg1 = %v", gotParams["arg1"])
	}
	if gotParams["arg2"] != float64(42) {
		t.Errorf("arg2 = %v", gotParams["arg2"])
	}
}

func TestUserConfig(t *testing.T) {
	uc := UserConfig{
		LLMs: []LLMProvider{
			{Name: "provider1", Protocol: "openai", APIKey: "key1"},
			{Name: "provider2", Protocol: "anthropic", APIKey: "key2"},
		},
		DefaultLLM:        1,
		Theme:             "dark",
		LastWorkDir:       "/home/user/project",
		ChromePath:        "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
		MaxToolIterations: 800,
		ResponseTimeout:   180,
		StreamTimeout:     180,
	}

	data, err := json.Marshal(uc)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var got UserConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if len(got.LLMs) != 2 {
		t.Errorf("LLMs count = %d, want 2", len(got.LLMs))
	}
	if got.DefaultLLM != 1 {
		t.Errorf("DefaultLLM = %d, want 1", got.DefaultLLM)
	}
	if got.MaxToolIterations != 800 {
		t.Errorf("MaxToolIterations = %d", got.MaxToolIterations)
	}
	if got.ResponseTimeout != 180 {
		t.Errorf("ResponseTimeout = %d", got.ResponseTimeout)
	}
}

func TestAgentConfig(t *testing.T) {
	ac := AgentConfig{
		Title:   "Code Reviewer",
		UseCase: "review",
		Prompt:  "Review the code for bugs",
		Source:  "user",
	}

	data, err := json.Marshal(ac)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var got AgentConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if got.Title != "Code Reviewer" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.UseCase != "review" {
		t.Errorf("UseCase = %q", got.UseCase)
	}
	if got.Prompt != "Review the code for bugs" {
		t.Errorf("Prompt = %q", got.Prompt)
	}
}

func TestToolCallPayload(t *testing.T) {
	p := ToolCallPayload{
		ToolCallID: "call-123",
		Name:       "read_file",
		Arguments:  `{"path": "/tmp/test.txt"}`,
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var got ToolCallPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if got.ToolCallID != "call-123" {
		t.Errorf("ToolCallID = %q", got.ToolCallID)
	}
	if got.Name != "read_file" {
		t.Errorf("Name = %q", got.Name)
	}
}

func TestToolResultPayload(t *testing.T) {
	p := ToolResultPayload{
		ToolCallID: "call-456",
		Name:       "run_command",
		Result:     "exit code 0",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var got ToolResultPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if got.ToolCallID != "call-456" {
		t.Errorf("ToolCallID = %q", got.ToolCallID)
	}
	if got.Result != "exit code 0" {
		t.Errorf("Result = %q", got.Result)
	}
}

func TestReasoningPayload(t *testing.T) {
	p := ReasoningPayload{
		Content: "I need to think step by step...",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var got ReasoningPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if got.Content != "I need to think step by step..." {
		t.Errorf("Content = %q, want %q", got.Content, "I need to think step by step...")
	}
}

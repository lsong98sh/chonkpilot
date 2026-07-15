package llmtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNewServer(t *testing.T) {
	srv := NewServer()
	if srv == nil {
		t.Fatal("NewServer() returned nil")
	}
	defer srv.Close()

	if srv.URL() == "" {
		t.Error("URL() should not be empty")
	}
}

func TestNonStreamResponse(t *testing.T) {
	srv := NewServer()
	defer srv.Close()

	body := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": "Tell me about Go"}
		],
		"stream": false
	}`

	resp, err := http.Post(srv.URL()+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	choices, ok := result["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		t.Fatal("no choices in response")
	}

	msg, ok := choices[0].(map[string]interface{})
	if !ok {
		t.Fatal("invalid choice format")
	}

	message, ok := msg["message"].(map[string]interface{})
	if !ok {
		t.Fatal("invalid message format")
	}

	content, ok := message["content"].(string)
	if !ok || content == "" {
		t.Error("content should not be empty")
	}

	if !strings.Contains(content, "Go") {
		t.Errorf("response should mention Go: %s", content)
	}
}

func TestStreamResponse(t *testing.T) {
	srv := NewServer()
	defer srv.Close()

	body := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": "Tell me about Go"}
		],
		"stream": true
	}`

	resp, err := http.Post(srv.URL()+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	responseStr := string(data)
	if !strings.Contains(responseStr, "[DONE]") {
		t.Error("streaming response should end with [DONE]")
	}

	if !strings.Contains(responseStr, "data:") {
		t.Error("streaming response should contain data: lines")
	}
}

func TestMultipleMessages(t *testing.T) {
	srv := NewServer()
	defer srv.Close()

	body := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "What is Python?"}
		],
		"stream": false
	}`

	resp, err := http.Post(srv.URL()+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	choices := result["choices"].([]interface{})
	msg := choices[0].(map[string]interface{})
	message := msg["message"].(map[string]interface{})
	content := message["content"].(string)

	if !strings.Contains(content, "Python") {
		t.Errorf("response should mention Python: %s", content)
	}
}

func TestChineseResponse(t *testing.T) {
	srv := NewServer()
	defer srv.Close()

	body := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": "你好"}
		],
		"stream": false
	}`

	resp, err := http.Post(srv.URL()+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	choices := result["choices"].([]interface{})
	msg := choices[0].(map[string]interface{})
	message := msg["message"].(map[string]interface{})
	content := message["content"].(string)

	if !strings.Contains(content, "ChonkPilot") {
		t.Errorf("response should contain ChonkPilot: %s", content)
	}
}

func TestNotFoundEndpoint(t *testing.T) {
	srv := NewServer()
	defer srv.Close()

	// POST to an unmatched path should return 404
	resp, err := http.Post(srv.URL()+"/v1/models", "application/json", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGenerateReply(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"Hello", "ChonkPilot"},
		{"Tell me about Go", "Go is"},
		{"What is Python?", "Python is"},
		{"I get an error", "debug"},
		{"你好", "ChonkPilot"},
		{"something random", "mock response"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("input=%s", tc.input[:min(10, len(tc.input))]), func(t *testing.T) {
			reply := generateReply(tc.input)
			if !strings.Contains(reply, tc.contains) {
				t.Errorf("generateReply(%q) = %q, should contain %q", tc.input, reply, tc.contains)
			}
		})
	}
}

func TestEscapeJSON(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"hello \"world\"", "hello \\\"world\\\""},
		{"line1\nline2", "line1\\nline2"},
		{"back\\slash", "back\\\\slash"},
	}

	for _, tc := range tests {
		got := escapeJSON(tc.input)
		if got != tc.want {
			t.Errorf("escapeJSON(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

package llm

import (
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot/pkg/executor/llm/llmtest"
	"go.uber.org/zap"
)

func TestNewClient(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	c := NewClient("", "", "", "", logger)
	if c == nil {
		t.Fatal("NewClient() returned nil")
	}
}

func TestNewClientWithOpenAI(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	c := NewClient("openai", "gpt-4o", "sk-test", "https://api.openai.com/v1", logger)
	if c == nil {
		t.Fatal("NewClient() returned nil")
	}
}

func TestNewClientWithClaude(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	c := NewClient("claude", "claude-3-opus", "sk-ant-test", "https://api.anthropic.com/v1", logger)
	if c == nil {
		t.Fatal("NewClient() returned nil")
	}
}

func TestMessage(t *testing.T) {
	msg := Message{
		Role:    "user",
		Content: "Hello, world!",
	}
	if msg.Role != "user" {
		t.Errorf("Role = %q", msg.Role)
	}
	if msg.Content != "Hello, world!" {
		t.Errorf("Content = %q", msg.Content)
	}
}

func TestStreamEvent(t *testing.T) {
	event := StreamEvent{
		Content: "Hello",
		Index:   0,
		Done:    false,
	}
	if event.Content != "Hello" {
		t.Errorf("Content = %q", event.Content)
	}
	if event.Index != 0 {
		t.Errorf("Index = %d", event.Index)
	}
	if event.Done {
		t.Error("Done should be false")
	}
}

// Integration tests using mock LLM server (no API key required)

func TestChatWithMockServer_NonStream(t *testing.T) {
	mockSrv := llmtest.NewServer()
	defer mockSrv.Close()

	logger, _ := zap.NewDevelopment()
	client := NewClient("openai", "gpt-4o", "no-key-needed", mockSrv.URL(), logger)

	messages := []Message{
		{Role: "user", Content: "Tell me about Go"},
	}

	ch, err := client.Chat(messages, ChatOptions{Stream: false})
	if err != nil {
		t.Fatalf("Chat() failed: %v", err)
	}

	var fullResponse strings.Builder
	for event := range ch {
		if event.Error != nil {
			t.Fatalf("stream error: %v", event.Error)
		}
		fullResponse.WriteString(event.Content)
	}

	result := fullResponse.String()
	if result == "" {
		t.Fatal("response should not be empty")
	}
	if !strings.Contains(result, "Go") {
		t.Errorf("response should mention Go, got: %s", result)
	}
}

func TestChatWithMockServer_Stream(t *testing.T) {
	mockSrv := llmtest.NewServer()
	defer mockSrv.Close()

	logger, _ := zap.NewDevelopment()
	client := NewClient("openai", "gpt-4o", "no-key-needed", mockSrv.URL(), logger)

	messages := []Message{
		{Role: "user", Content: "What is Python?"},
	}

	ch, err := client.Chat(messages, ChatOptions{Stream: true})
	if err != nil {
		t.Fatalf("Chat() failed: %v", err)
	}

	var chunks []string
	for event := range ch {
		if event.Error != nil {
			t.Fatalf("stream error: %v", event.Error)
		}
		chunks = append(chunks, event.Content)
	}

	fullResponse := strings.Join(chunks, "")
	if fullResponse == "" {
		t.Fatal("streaming response should not be empty")
	}
	if !strings.Contains(fullResponse, "Python") {
		t.Errorf("response should mention Python, got: %s", fullResponse)
	}
}

func TestChatWithMockServer_MultipleMessages(t *testing.T) {
	mockSrv := llmtest.NewServer()
	defer mockSrv.Close()

	logger, _ := zap.NewDevelopment()
	client := NewClient("openai", "gpt-4o", "no-key-needed", mockSrv.URL(), logger)

	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "What is Go language?"},
	}

	ch, err := client.Chat(messages, ChatOptions{Stream: false})
	if err != nil {
		t.Fatalf("Chat() failed: %v", err)
	}

	var fullResponse strings.Builder
	for event := range ch {
		if event.Error != nil {
			t.Fatalf("stream error: %v", event.Error)
		}
		fullResponse.WriteString(event.Content)
	}

	result := fullResponse.String()
	if !strings.Contains(result, "Go") && !strings.Contains(result, "language") {
		t.Errorf("response should mention Go, got: %s", result)
	}
}

func TestChatWithMockServer_Claude(t *testing.T) {
	mockSrv := llmtest.NewServer()
	defer mockSrv.Close()

	logger, _ := zap.NewDevelopment()
	client := NewClient("claude", "claude-3-opus", "no-key-needed", mockSrv.URL(), logger)

	messages := []Message{
		{Role: "user", Content: "Hello"},
	}

	ch, err := client.Chat(messages, ChatOptions{Stream: false})
	if err != nil {
		t.Fatalf("Chat() failed: %v", err)
	}

	var fullResponse strings.Builder
	for event := range ch {
		if event.Error != nil {
			t.Fatalf("stream error: %v", event.Error)
		}
		fullResponse.WriteString(event.Content)
	}

	result := fullResponse.String()
	if result == "" {
		t.Fatal("response should not be empty")
	}
}

func TestChatWithMockServer_Chinese(t *testing.T) {
	mockSrv := llmtest.NewServer()
	defer mockSrv.Close()

	logger, _ := zap.NewDevelopment()
	client := NewClient("openai", "gpt-4o", "no-key-needed", mockSrv.URL(), logger)

	messages := []Message{
		{Role: "user", Content: "你好"},
	}

	ch, err := client.Chat(messages, ChatOptions{Stream: true})
	if err != nil {
		t.Fatalf("Chat() failed: %v", err)
	}

	var chunks []string
	for event := range ch {
		if event.Error != nil {
			t.Fatalf("stream error: %v", event.Error)
		}
		chunks = append(chunks, event.Content)
	}

	fullResponse := strings.Join(chunks, "")
	if fullResponse == "" {
		t.Fatal("Chinese response should not be empty")
	}
}

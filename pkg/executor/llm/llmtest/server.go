// Package llmtest provides a dummy HTTP server that implements the OpenAI-compatible
// chat completions API for integration testing without requiring real API keys.
package llmtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

// Server is a mock LLM API server that implements OpenAI-compatible endpoints.
type Server struct {
	*httptest.Server
}

// ChatRequest mirrors the OpenAI chat completions request format.
type ChatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Stream bool `json:"stream"`
}

// NewServer creates a new mock LLM server that returns predefined responses.
// The server handles:
//   - POST /v1/chat/completions and /chat/completions (OpenAI-compatible)
//   - POST /v1/messages and /messages (Claude-compatible)
func NewServer() *Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		switch {
		case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/chat/completions":
			handleOpenAI(w, r)
		case r.URL.Path == "/v1/messages" || r.URL.Path == "/messages":
			handleClaude(w, r)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))

	return &Server{Server: srv}
}

// handleOpenAI processes OpenAI-compatible chat completion requests.
func handleOpenAI(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}

	lastUserMsg := extractLastUserMessage(req.Messages)
	if lastUserMsg == "" {
		lastUserMsg = "Hello"
	}

	if req.Stream {
		handleStreamResponse(w, lastUserMsg)
	} else {
		handleNonStreamResponse(w, lastUserMsg)
	}
}

// handleClaude processes Anthropic Claude-compatible message requests.
func handleClaude(w http.ResponseWriter, r *http.Request) {
	var claudeReq struct {
		Model     string `json:"model"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		System    string `json:"system,omitempty"`
		Stream    bool   `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&claudeReq); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}

	var chatMsgs []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	chatMsgs = claudeReq.Messages

	lastUserMsg := extractLastUserMessage(chatMsgs)
	if lastUserMsg == "" {
		lastUserMsg = "Hello"
	}

	// Claude response format
	w.Header().Set("Content-Type", "application/json")
	reply := generateReply(lastUserMsg)

	resp := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": reply,
			},
		},
	}
	json.NewEncoder(w).Encode(resp)
}

// extractLastUserMessage finds the last message with role "user".
func extractLastUserMessage(messages []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

// URL returns the base URL of the mock server.
func (s *Server) URL() string {
	return s.Server.URL
}

// handleStreamResponse sends a streaming SSE response.
func handleStreamResponse(w http.ResponseWriter, userMsg string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Generate a dummy response based on the user message
	reply := generateReply(userMsg)
	words := strings.Fields(reply)

	for i, word := range words {
		chunk := word
		if i < len(words)-1 {
			chunk += " "
		}

		resp := fmt.Sprintf(`data: {"choices":[{"delta":{"content":"%s"},"finish_reason":null}]}`+"\n\n", escapeJSON(chunk))
		fmt.Fprint(w, resp)
		flusher.Flush()
	}

	// Send the done signal
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleNonStreamResponse sends a regular JSON response.
func handleNonStreamResponse(w http.ResponseWriter, userMsg string) {
	w.Header().Set("Content-Type", "application/json")

	reply := generateReply(userMsg)

	resp := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": reply,
				},
				"finish_reason": "stop",
			},
		},
	}

	json.NewEncoder(w).Encode(resp)
}

// generateReply creates a dummy response based on the user's message.
func generateReply(userMsg string) string {
	userMsg = strings.ToLower(userMsg)

	switch {
	case strings.Contains(userMsg, "你好"):
		return "你好！我是 ChonkPilot AI 助手，有什么可以帮助你的吗？"
	case strings.Contains(userMsg, "hello"):
		return "Hello! I am ChonkPilot AI assistant. How can I help you today?"
	case strings.Contains(userMsg, "go") || strings.Contains(userMsg, "golang"):
		return "Go is a statically typed, compiled programming language designed at Google. It is known for its simplicity, efficiency, and built-in concurrency support."
	case strings.Contains(userMsg, "python"):
		return "Python is a high-level, interpreted programming language known for its readability and versatility. It's widely used in data science, web development, and automation."
	case strings.Contains(userMsg, "error") || strings.Contains(userMsg, "fail"):
		return "I understand you're encountering an error. Let me help you debug this issue step by step. First, could you share the error message and the relevant code snippet?"
	default:
		return fmt.Sprintf("This is a mock response to: \"%s\". In production, this would be generated by the actual LLM provider.", userMsg)
	}
}

// escapeJSON escapes special characters in a string for JSON embedding.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

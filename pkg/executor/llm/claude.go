package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ClaudeProvider implements the Provider interface for Anthropic Claude API.
type ClaudeProvider struct {
	Model   string
	APIKey  string
	BaseURL string
}

// ClaudeMessageRequest is the request body for Claude API.
type ClaudeMessageRequest struct {
	Model    string          `json:"model"`
	Messages []ClaudeMessage `json:"messages"`
	System   string          `json:"system,omitempty"`
	Stream   bool            `json:"stream"`
}

// ClaudeMessage represents a message in the Claude API format.
type ClaudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat implements the Provider interface.
func (p *ClaudeProvider) Chat(messages []Message, options ChatOptions) (<-chan StreamEvent, error) {
	if p.BaseURL == "" {
		p.BaseURL = "https://api.anthropic.com/v1"
	}
	model := options.Model
	if model == "" {
		model = p.Model
	}
	if model == "" {
		model = "claude-3-opus-20240229"
	}

	// Extract system message
	var systemPrompt string
	var claudeMessages []ClaudeMessage
	for _, msg := range messages {
		if msg.Role == "system" {
			systemPrompt = msg.Content
		} else {
			role := msg.Role
			if role == "assistant" {
				role = "assistant"
			}
			claudeMessages = append(claudeMessages, ClaudeMessage{Role: role, Content: msg.Content})
		}
	}

	reqBody := ClaudeMessageRequest{
		Model:    model,
		Messages: claudeMessages,
		System:   systemPrompt,
		Stream:   options.Stream,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/messages", p.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := makeLLMClient(options.ResponseTimeout)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}

	ch := make(chan StreamEvent, 100)

	if options.Stream {
		// Wrap response body with stream idle timeout reader
		if options.StreamTimeout > 0 {
			resp.Body = &streamIdleReader{
				reader:  resp.Body,
				timeout: options.StreamTimeout,
			}
		}
		go p.readStream(resp, ch)
	} else {
		go p.readResponse(resp, ch)
	}

	return ch, nil
}

func (p *ClaudeProvider) readStream(resp *http.Response, ch chan<- StreamEvent) {
	defer resp.Body.Close()
	defer close(ch)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		ch <- StreamEvent{Error: fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))}
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	index := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var event struct {
			Type  string `json:"type"`
			Delta *struct {
				Text string `json:"text"`
			} `json:"delta,omitempty"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "content_block_delta":
			if event.Delta != nil && event.Delta.Text != "" {
				ch <- StreamEvent{Content: event.Delta.Text, Index: index}
				index++
			}
		case "message_stop":
			ch <- StreamEvent{Done: true}
			return
		}
	}
}

func (p *ClaudeProvider) readResponse(resp *http.Response, ch chan<- StreamEvent) {
	defer resp.Body.Close()
	defer close(ch)

	var claudeResp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&claudeResp); err != nil {
		ch <- StreamEvent{Error: fmt.Errorf("failed to decode response: %w", err)}
		return
	}

	if claudeResp.Error != nil {
		ch <- StreamEvent{Error: fmt.Errorf("API error: %s", claudeResp.Error.Message)}
		return
	}

	var fullContent strings.Builder
	for _, block := range claudeResp.Content {
		fullContent.WriteString(block.Text)
	}
	ch <- StreamEvent{Content: fullContent.String(), Index: 0, Done: true}
}

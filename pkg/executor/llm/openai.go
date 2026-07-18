package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider implements the Provider interface for OpenAI-compatible APIs.
type OpenAIProvider struct {
	Model   string
	APIKey  string
	BaseURL string
}

// openAIToolDef describes a tool for OpenAI's API format.
type openAIToolDef struct {
	Type     string            `json:"type"`
	Function openAIToolFunction `json:"function"`
}

// openAIToolFunction describes a function within a tool definition for OpenAI.
type openAIToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// ChatRequest is the request body for OpenAI chat API.
type ChatRequest struct {
	Model            string          `json:"model"`
	Messages         []Message       `json:"messages"`
	Stream           bool            `json:"stream"`
	Temperature      float64         `json:"temperature,omitempty"`
	MaxTokens        int             `json:"max_tokens,omitempty"`
	Tools            []openAIToolDef `json:"tools,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"`
	ExtraBody        json.RawMessage `json:"extra_body,omitempty"`
}

// ChatResponse is the response body for non-streaming requests.
type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content,omitempty"`
			ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// openaiToolCall represents a tool call in OpenAI's streaming format.
type openaiToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Chat implements the Provider interface.
// streamIdleReader wraps an io.ReadCloser with per-read idle timeout.
// timer resets on every successful Read, so it enforces idle time between chunks.
type streamIdleReader struct {
	reader  io.ReadCloser
	timeout time.Duration
}

func (r *streamIdleReader) Read(p []byte) (int, error) {
	if r.timeout <= 0 {
		return r.reader.Read(p)
	}
	ch := make(chan struct{ n int; err error }, 1)
	go func() {
		n, err := r.reader.Read(p)
		ch <- struct{ n int; err error }{n, err}
	}()
	select {
	case res := <-ch:
		return res.n, res.err
	case <-time.After(r.timeout):
		return 0, fmt.Errorf("stream idle timeout: no data for %v", r.timeout)
	}
}

func (r *streamIdleReader) Close() error {
	return r.reader.Close()
}

// makeLLMClient creates an http.Client with configurable timeouts.
func makeLLMClient(firstTokenTimeout time.Duration, tlsHandshakeTimeout time.Duration) *http.Client {
	if tlsHandshakeTimeout <= 0 {
		tlsHandshakeTimeout = 30 * time.Second
	}
	transport := &http.Transport{
		ResponseHeaderTimeout: firstTokenTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		DisableKeepAlives:     true,
	}
	if firstTokenTimeout <= 0 {
		transport.ResponseHeaderTimeout = 180 * time.Second // default 3 min
	}
	return &http.Client{
		Timeout:   0, // no total request timeout (streaming may run long)
		Transport: transport,
	}
}

func (p *OpenAIProvider) Chat(messages []Message, options ChatOptions) (<-chan StreamEvent, error) {
	if p.BaseURL == "" {
		p.BaseURL = "https://api.openai.com/v1"
	}
	model := options.Model
	if model == "" {
		model = p.Model
	}
	if model == "" {
		model = "gpt-4o"
	}

	reqBody := ChatRequest{
		Model:       model,
		Messages:    messages,
		Stream:      options.Stream,
		Temperature: options.Temperature,
		MaxTokens:   options.MaxTokens,
	}

	// Enable thinking/reasoning chain (DeepSeek etc.)
	if options.Thinking {
		effort := options.ReasoningEffort
		if effort == "" {
			effort = "high"
		}
		reqBody.ReasoningEffort = effort
		reqBody.ExtraBody = json.RawMessage(`{"thinking":{"type":"enabled"}}`)
	}

	// Convert tools to OpenAI format
	if len(options.Tools) > 0 {
		reqBody.Tools = make([]openAIToolDef, 0, len(options.Tools))
		for _, t := range options.Tools {
			reqBody.Tools = append(reqBody.Tools, openAIToolDef{
				Type: "function",
				Function: openAIToolFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			})
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/chat/completions", p.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.APIKey))

	client := makeLLMClient(options.ResponseTimeout, options.TLSHandshakeTimeout)
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

func (p *OpenAIProvider) readStream(resp *http.Response, ch chan<- StreamEvent) {
	defer resp.Body.Close()
	defer close(ch)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		ch <- StreamEvent{Error: fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))}
		return
	}

	// Accumulated tool calls across stream chunks (keyed by index)
	type accToolCall struct {
		ID       string
		Type     string
		Name     string
		Argument string
	}
	accToolCalls := make(map[int]*accToolCall)

	scanner := bufio.NewScanner(resp.Body)
	index := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Flush accumulated tool calls
			if len(accToolCalls) > 0 {
				for _, tc := range accToolCalls {
					ch <- StreamEvent{
						ToolCall: &ToolCall{
							ID:   tc.ID,
							Type: tc.Type,
							Function: struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							}{
								Name:      tc.Name,
								Arguments: tc.Argument,
							},
						},
					}
				}
			}
			ch <- StreamEvent{Done: true}
			return
		}

		var streamResp struct {
			Choices []struct {
				Delta struct {
					Content          string            `json:"content"`
					ReasoningContent string            `json:"reasoning_content"`
					ToolCalls        []openaiToolCall  `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
			continue
		}
		if len(streamResp.Choices) == 0 {
			continue
		}
		choice := streamResp.Choices[0]

		// Handle tool calls
		for _, tc := range choice.Delta.ToolCalls {
			acc, ok := accToolCalls[tc.Index]
			if !ok {
				acc = &accToolCall{}
				accToolCalls[tc.Index] = acc
			}
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Type != "" {
				acc.Type = tc.Type
			}
			if tc.Function.Name != "" {
				acc.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.Argument += tc.Function.Arguments
			}
		}

		// Handle reasoning content (deepseek thinking chain)
		if choice.Delta.ReasoningContent != "" {
			ch <- StreamEvent{
				ReasoningContent: choice.Delta.ReasoningContent,
			}
		}

		// Handle text content
		if choice.Delta.Content != "" {
			ch <- StreamEvent{
				Content: choice.Delta.Content,
				Index:   index,
			}
			index++
		}

		// Handle finish
		if choice.FinishReason == "stop" {
			ch <- StreamEvent{Done: true}
			return
		}
		if choice.FinishReason == "tool_calls" {
			// Flush accumulated tool calls
			for _, tc := range accToolCalls {
				ch <- StreamEvent{
					ToolCall: &ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{
							Name:      tc.Name,
							Arguments: tc.Argument,
						},
					},
				}
			}
			ch <- StreamEvent{Done: true}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Error: fmt.Errorf("stream read error: %w", err)}
	}
}

func (p *OpenAIProvider) readResponse(resp *http.Response, ch chan<- StreamEvent) {
	defer resp.Body.Close()
	defer close(ch)

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		ch <- StreamEvent{Error: fmt.Errorf("failed to decode response: %w", err)}
		return
	}

	if chatResp.Error != nil {
		ch <- StreamEvent{Error: fmt.Errorf("API error: %s - %s", chatResp.Error.Code, chatResp.Error.Message)}
		return
	}

	if len(chatResp.Choices) > 0 {
		msg := chatResp.Choices[0].Message
		if len(msg.ToolCalls) > 0 {
			for i := range msg.ToolCalls {
				tc := &msg.ToolCalls[i]
				ch <- StreamEvent{
					ToolCall: tc,
				}
			}
		}
		if msg.Content != "" {
			ch <- StreamEvent{
				Content: msg.Content,
				Index:   0,
			}
		}
		if msg.ReasoningContent != "" {
			ch <- StreamEvent{
				ReasoningContent: msg.ReasoningContent,
			}
		}
		ch <- StreamEvent{Done: true}
	}
}

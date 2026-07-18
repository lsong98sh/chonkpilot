package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ─── Result types ────────────────────────────────────────────────────────────

// TurnResult contains the final output of a tool-loop turn.
type TurnResult struct {
	Content    string     // accumulated assistant text content
	Messages   []Message  // all messages accumulated (user+system, assistant, tool)
	Iterations int        // number of LLM Chat calls made
	Cancelled  bool       // true if cancelled via context
	ToolCalls  int        // total tool calls dispatched
}

// TurnConfig configures a single turn execution.
type TurnConfig struct {
	Messages            []Message
	Tools               []ToolDefinition
	MaxIter             int           // 0 = unlimited (infinite tool loop)
	MaxAttempts         int           // retry attempts per LLM call (1 = no retry)
	MaxTokens           int
	Temperature         float64
	Thinking            bool
	ReasoningEffort     string
	ResponseTimeout     time.Duration // timeout for first byte of LLM response
	StreamTimeout       time.Duration // timeout between stream chunks
	TLSHandshakeTimeout time.Duration // TLS handshake timeout
	RetryDelaySeconds   int           // seconds to wait between retry attempts
}

// DefaultConfig returns sane defaults for a TurnConfig.
func (c TurnConfig) fillDefaults() TurnConfig {
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 1
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = 65535
	}
	if c.Temperature <= 0 {
		c.Temperature = 0.7
	}
	if c.MaxIter < 0 {
		c.MaxIter = 0 // 0 = unlimited
	}
	// ResponseTimeout: time to wait for first byte after sending.
	// Zero means no timeout; set a sane default so "send and no response"
	// triggers retry rather than hanging indefinitely.
	if c.ResponseTimeout <= 0 {
		c.ResponseTimeout = 120 * time.Second
	}
	// StreamTimeout: max time between consecutive stream chunks.
	if c.StreamTimeout <= 0 {
		c.StreamTimeout = 60 * time.Second
	}
	if c.RetryDelaySeconds <= 0 {
		c.RetryDelaySeconds = 5
	}
	return c
}

// TurnCallbacks holds all optional callbacks for the turn loop.
// All are called synchronously from the Run goroutine.
type TurnCallbacks struct {
	// OnChunk is called for each stream chunk from the LLM.
	OnChunk func(chunk StreamEvent)

	// OnAssistantMsg is called after the full assistant message is built.
	// Return the tool call message IDs (for brief tracking) and an error.
	// If error is non-nil, the turn is aborted with that error.
	OnAssistantMsg func(msg Message) (toolCallMsgIDs []string, _ error)

	// OnToolCall is called before a tool is dispatched.
	// Return the tool call message ID (for brief tracking) and an error.
	// If error is non-nil, this tool call is skipped and an error is added to messages.
	OnToolCall func(tc ToolCall, args map[string]interface{}) (msgID string, _ error)

	// OnToolResult is called after tool dispatch with the result.
	// Return an error to abort the turn with that error.
	OnToolResult func(tc ToolCall, resultStr string, success bool) error

	// OnIterationStart is called before each Chat call.
	// Tools are reloaded each iteration if ReloadTools is set.
	OnIterationStart func(iter int)

	// ReloadTools is called each iteration to get updated tool definitions
	// (e.g., for add_tool/delete_tool changes). If nil, the original tool list is used.
	ReloadTools func() []ToolDefinition

	// OnIterationEnd is called after each iteration completes (after tool dispatch).
	// toolCallMsgIDs are the DB IDs of tool_call messages saved in this iteration.
	OnIterationEnd func(iter int, reasoningContent string, toolCallMsgIDs []string)

	// OnLLMError is called when an LLM call or stream error occurs.
	OnLLMError func(code, message string, retryable bool, attempt, maxAttempts int)

	// OnLLMRetry is called before a retry attempt.
	OnLLMRetry func(attempt, maxAttempts int, waitSeconds int)

	// OnComplete is called after the turn finishes successfully.
	OnComplete func(result *TurnResult)
}

// TurnRunner executes the LLM tool loop (Chat → stream → tool dispatch → repeat).
// It is safe for single-use only (one Run call per TurnRunner instance).
type TurnRunner struct {
	Client   *Client
	Logger   *zap.Logger
	CancelCx context.Context // optional cancellation context

	// Dispatch executes a tool and returns (resultJSON, success, error).
	// If error is non-nil, the turn is aborted.
	Dispatch func(toolName string, args map[string]interface{}, depth int) (string, bool, error)

	Callbacks TurnCallbacks
}

// Run executes the tool loop. Blocks until done, cancelled, or error.
func (r *TurnRunner) Run(config TurnConfig) (*TurnResult, error) {
	config = config.fillDefaults()

	messages := make([]Message, len(config.Messages))
	copy(messages, config.Messages)

	tools := config.Tools
	maxIter := config.MaxIter

	var fullContent strings.Builder
	totalIter := 0
	totalToolCalls := 0
	cancelled := false

	for iter := 0; maxIter == 0 || iter < maxIter; iter++ {
		totalIter = iter + 1

		// Check cancellation before each LLM call
		if r.isCancelled() {
			cancelled = true
			break
		}

		// Reload tools each iteration (for add_tool/delete_tool, etc.)
		tools = config.Tools
		if r.Callbacks.ReloadTools != nil {
			tools = r.Callbacks.ReloadTools()
		}

		// Notify iteration start
		if r.Callbacks.OnIterationStart != nil {
			r.Callbacks.OnIterationStart(iter)
		}

		var toolCalls []*ToolCall
		var textContent, reasoningContent strings.Builder

		// ── LLM call with retry ──
		llmSucceeded := false
		maxAttempts := config.MaxAttempts

	llmAttemptLoop:
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if r.isCancelled() {
				cancelled = true
				break llmAttemptLoop
			}

			stream, err := r.Client.Chat(messages, ChatOptions{
				Stream:              true,
				Tools:               tools,
				Temperature:         config.Temperature,
				MaxTokens:           config.MaxTokens,
				Thinking:            config.Thinking,
				ReasoningEffort:     config.ReasoningEffort,
				ResponseTimeout:     config.ResponseTimeout,
				StreamTimeout:       config.StreamTimeout,
				TLSHandshakeTimeout: config.TLSHandshakeTimeout,
			})
			if err != nil {
				code, retryable := classifyLLMError(err)
				isLastAttempt := attempt >= maxAttempts

				if !retryable || isLastAttempt {
					if r.Callbacks.OnLLMError != nil {
						r.Callbacks.OnLLMError(string(code), err.Error(), retryable, attempt, maxAttempts)
					}
					return &TurnResult{
						Content:    fullContent.String(),
						Messages:   messages,
						Iterations: totalIter,
						Cancelled:  false,
						ToolCalls:  totalToolCalls,
					}, fmt.Errorf("LLM %s: %s", code, err.Error())
				}

				// Retryable — emit events, wait, retry
				if r.Callbacks.OnLLMError != nil {
					r.Callbacks.OnLLMError(string(code), err.Error(), true, attempt, maxAttempts)
				}
				if r.Callbacks.OnLLMRetry != nil {
					r.Callbacks.OnLLMRetry(attempt, maxAttempts, config.RetryDelaySeconds)
				}
				r.sleepCancellable(time.Duration(config.RetryDelaySeconds) * time.Second)
				continue
			}

			// Stream reading loop — also retryable
			streamOk := true
		streamLoop:
			for chunk := range stream {
				if chunk.Error != nil {
					code, retryable := classifyLLMError(chunk.Error)
					isLastAttempt := attempt >= maxAttempts

					if !retryable || isLastAttempt {
						if r.Callbacks.OnLLMError != nil {
							r.Callbacks.OnLLMError(string(code), chunk.Error.Error(), retryable, attempt, maxAttempts)
						}
						return &TurnResult{
							Content:    fullContent.String(),
							Messages:   messages,
							Iterations: totalIter,
							Cancelled:  false,
							ToolCalls:  totalToolCalls,
						}, fmt.Errorf("LLM stream error: %s", chunk.Error.Error())
					}

					if r.Callbacks.OnLLMError != nil {
						r.Callbacks.OnLLMError(string(code), chunk.Error.Error(), true, attempt, maxAttempts)
					}
					if r.Callbacks.OnLLMRetry != nil {
						r.Callbacks.OnLLMRetry(attempt, maxAttempts, config.RetryDelaySeconds)
					}
					r.sleepCancellable(time.Duration(config.RetryDelaySeconds) * time.Second)
					streamOk = false
					break streamLoop
				}

				if chunk.ReasoningContent != "" {
					reasoningContent.WriteString(chunk.ReasoningContent)
				}
				if chunk.Content != "" {
					textContent.WriteString(chunk.Content)
				}
				if chunk.ToolCall != nil {
					toolCalls = append(toolCalls, chunk.ToolCall)
				}

				// Notify callback
				if r.Callbacks.OnChunk != nil {
					r.Callbacks.OnChunk(chunk)
				}
			}

			if streamOk {
				llmSucceeded = true
				break llmAttemptLoop
			}
			// stream error, retry next attempt
		}

		if !llmSucceeded {
			if cancelled {
				return &TurnResult{
					Content:    fullContent.String(),
					Messages:   messages,
					Iterations: totalIter,
					Cancelled:  true,
					ToolCalls:  totalToolCalls,
				}, fmt.Errorf("cancelled")
			}
			return &TurnResult{
				Content:    fullContent.String(),
				Messages:   messages,
				Iterations: totalIter,
				Cancelled:  false,
				ToolCalls:  totalToolCalls,
			}, fmt.Errorf("LLM call failed after %d attempts", maxAttempts)
		}

		// ── Build assistant message ──
		assistantMsg := Message{
			Role:             "assistant",
			Content:          textContent.String(),
			ReasoningContent: reasoningContent.String(),
		}
		if len(toolCalls) > 0 {
			assistantMsg.ToolCalls = make([]ToolCall, len(toolCalls))
			for i, tc := range toolCalls {
				assistantMsg.ToolCalls[i] = *tc
			}
		}

		messages = append(messages, assistantMsg)
		fullContent.WriteString(textContent.String())

		// Notify assistant message — get tool call message IDs for brief tracking
		var toolCallMsgIDs []string
		if r.Callbacks.OnAssistantMsg != nil {
			var err error
			toolCallMsgIDs, err = r.Callbacks.OnAssistantMsg(assistantMsg)
			if err != nil {
				return &TurnResult{
					Content:    fullContent.String(),
					Messages:   messages,
					Iterations: totalIter,
					Cancelled:  false,
					ToolCalls:  totalToolCalls,
				}, fmt.Errorf("OnAssistantMsg aborted turn: %w", err)
			}
		}

		// No tool calls → done
		if len(toolCalls) == 0 {
			break
		}

		// ── Process each tool call ──
		for _, tc := range toolCalls {
			// Parse arguments (supports both JSON object and JSON array inputs)
			var raw interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &raw); err != nil {
				r.Logger.Warn("turn_runner: failed to parse tool call arguments",
					zap.Error(err), zap.String("raw", tc.Function.Arguments))
				errMsg := Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    fmt.Sprintf(`{"error":"failed to parse arguments: %s"}`, err.Error()),
				}
				messages = append(messages, errMsg)
				continue
			}
			var args map[string]interface{}
			switch v := raw.(type) {
			case map[string]interface{}:
				args = v
			case []interface{}:
				args = map[string]interface{}{"": v}
			default:
				r.Logger.Warn("turn_runner: unexpected arguments type", zap.Any("type", fmt.Sprintf("%T", raw)))
				errMsg := Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    `{"error":"arguments must be a JSON object or array"}`,
				}
				messages = append(messages, errMsg)
				continue
			}

			r.Logger.Info("turn_runner: dispatching tool",
				zap.String("tool", tc.Function.Name),
				zap.String("tool_call_id", tc.ID),
				zap.Any("args", args),
			)

			var msgID string
			if r.Callbacks.OnToolCall != nil {
				var err error
				msgID, err = r.Callbacks.OnToolCall(*tc, args)
				if err != nil {
					errMsg := Message{
						Role:       "tool",
						ToolCallID: tc.ID,
						Content:    fmt.Sprintf(`{"error":"%s"}`, err.Error()),
					}
					messages = append(messages, errMsg)
					continue
				}
			}
			toolCallMsgIDs = append(toolCallMsgIDs, msgID)

			resultStr, success, err := r.Dispatch(tc.Function.Name, args, 0)
			if err != nil {
				return &TurnResult{
					Content:    fullContent.String(),
					Messages:   messages,
					Iterations: totalIter,
					Cancelled:  false,
					ToolCalls:  totalToolCalls,
				}, fmt.Errorf("tool dispatch %s failed: %w", tc.Function.Name, err)
			}
			totalToolCalls++

			// Notify tool result
			if r.Callbacks.OnToolResult != nil {
				if err := r.Callbacks.OnToolResult(*tc, resultStr, success); err != nil {
					return &TurnResult{
						Content:    fullContent.String(),
						Messages:   messages,
						Iterations: totalIter,
						Cancelled:  false,
						ToolCalls:  totalToolCalls,
					}, fmt.Errorf("OnToolResult aborted turn: %w", err)
				}
			}

			// Add tool result message
			toolResultMsg := Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    resultStr,
			}
			messages = append(messages, toolResultMsg)
		}

		// Notify iteration end (for brief extraction, etc.)
		if r.Callbacks.OnIterationEnd != nil {
			r.Callbacks.OnIterationEnd(iter, reasoningContent.String(), toolCallMsgIDs)
		}
	}

	result := &TurnResult{
		Content:    fullContent.String(),
		Messages:   messages,
		Iterations: totalIter,
		Cancelled:  cancelled,
		ToolCalls:  totalToolCalls,
	}

	if r.Callbacks.OnComplete != nil {
		r.Callbacks.OnComplete(result)
	}

	return result, nil
}

// isCancelled checks whether the cancellation context has fired.
func (r *TurnRunner) isCancelled() bool {
	if r.CancelCx != nil {
		select {
		case <-r.CancelCx.Done():
			return true
		default:
		}
	}
	return false
}

// sleepCancellable sleeps for the given duration but can be interrupted by cancellation.
func (r *TurnRunner) sleepCancellable(d time.Duration) {
	select {
	case <-time.After(d):
	case <-r.cancelCh():
	}
}

func (r *TurnRunner) cancelCh() <-chan struct{} {
	if r.CancelCx != nil {
		return r.CancelCx.Done()
	}
	return nil
}

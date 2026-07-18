package llm

import "strings"

// DefaultLLMErrorCode is a string code for LLM error classification.
type DefaultLLMErrorCode string

const (
	ErrLLMTimeout       DefaultLLMErrorCode = "timeout"
	ErrLLMNetwork       DefaultLLMErrorCode = "network"
	ErrLLMRateLimited   DefaultLLMErrorCode = "rate_limited"
	ErrLLMAuth          DefaultLLMErrorCode = "auth"
	ErrLLMAPI           DefaultLLMErrorCode = "api_error"
	ErrLLMContextLength DefaultLLMErrorCode = "context_length_exceeded"
	ErrLLMUnknown       DefaultLLMErrorCode = "unknown"
)

// classifyLLMError classifies an LLM error into a code and whether it is retryable.
// This is a default implementation used by TurnRunner internally.
// Callers can override via TurnCallbacks.ClassifyError for custom behavior.
func classifyLLMError(err error) (DefaultLLMErrorCode, bool) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "context deadline"):
		return ErrLLMTimeout, true
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "EOF"):
		return ErrLLMNetwork, true
	case strings.Contains(msg, "429") || strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "Too Many Requests"):
		return ErrLLMRateLimited, false
	case strings.Contains(msg, "401") || strings.Contains(msg, "403") ||
		strings.Contains(msg, "unauthorized") || strings.Contains(msg, "API key"):
		return ErrLLMAuth, false
	case strings.Contains(msg, "500") || strings.Contains(msg, "502"):
		return ErrLLMAPI, true
	case strings.Contains(msg, "503") || strings.Contains(msg, "service unavailable"):
		return ErrLLMAPI, false
	case strings.Contains(msg, "stream idle timeout"):
		return ErrLLMTimeout, true
	case strings.Contains(msg, "context_length_exceeded") || strings.Contains(msg, "context length"):
		return ErrLLMContextLength, false
	default:
		return ErrLLMUnknown, false
	}
}

package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// LLMErrorCode classifies LLM API errors.
type LLMErrorCode string

const (
	ErrLLMNetwork        LLMErrorCode = "ERR_LLM_NETWORK"
	ErrLLMTimeout        LLMErrorCode = "ERR_LLM_TIMEOUT"
	ErrLLMRateLimited    LLMErrorCode = "ERR_LLM_RATE_LIMITED"
	ErrLLMAuth           LLMErrorCode = "ERR_LLM_AUTH"
	ErrLLMAPI            LLMErrorCode = "ERR_LLM_API"
	ErrLLMStream         LLMErrorCode = "ERR_LLM_STREAM"
	ErrLLMContextLength  LLMErrorCode = "ERR_LLM_CONTEXT_LENGTH"
	ErrLLMUnknown        LLMErrorCode = "ERR_LLM_UNKNOWN"
)

// classifyLLMError classifies an LLM error into a code and whether it is retryable.
func classifyLLMError(err error) (LLMErrorCode, bool) {
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
		return ErrLLMRateLimited, false // rate limit — retry won't help immediately
	case strings.Contains(msg, "401") || strings.Contains(msg, "403") ||
		strings.Contains(msg, "unauthorized") || strings.Contains(msg, "API key"):
		return ErrLLMAuth, false
	case strings.Contains(msg, "500") || strings.Contains(msg, "502"):
		return ErrLLMAPI, true // server fault — retry may succeed
	case strings.Contains(msg, "503") || strings.Contains(msg, "service unavailable"):
		return ErrLLMAPI, false // server busy — retry is not meaningful
	case strings.Contains(msg, "stream idle timeout"):
		return ErrLLMTimeout, true
	case strings.Contains(msg, "context_length_exceeded") || strings.Contains(msg, "context length"):
		return ErrLLMContextLength, false
	default:
		return ErrLLMUnknown, false
	}
}

// resolveLLMConfig resolves LLM configuration using the chain:
// CLI params → --llm-config-file → ~/.chonkpilot/config.json → error.
// It modifies ea in-place.
// ReasoningEffort is only overridden from config if user didn't explicitly set --reasoning=on/off.
func resolveLLMConfig(ea *ExecutorArgs) error {
	// Step 1: CLI params directly set — if any is set, use them as-is
	if ea.LLMProtocol != "" || ea.LLMModel != "" || ea.LLMAPIKey != "" || ea.LLMAPIURL != "" {
		return nil
	}

	// applyReasoningFromConfig overrides thinking/reasoningEffort from a config only if user didn't explicitly set it
	applyReasoningFromConfig := func(cfg models.LLMProvider) {
		if !ea.ThinkingSet {
			ea.Thinking = cfg.Thinking
		}
		if ea.ReasoningEffort == "" {
			ea.ReasoningEffort = cfg.ReasoningEffort
		}
		// Apply optional numeric/behavioral fields (use non-zero values)
		if cfg.MaxTokens > 0 {
			ea.LLMMaxTokens = cfg.MaxTokens
		}
		if cfg.Temperature > 0 {
			ea.LLMTemperature = cfg.Temperature
		}
	}

	// Step 2: --llm-config-file
	if ea.LLMConfigFile != "" {
		data, err := os.ReadFile(ea.LLMConfigFile)
		if err != nil {
			return fmt.Errorf("failed to read --llm-config-file: %w", err)
		}
		if len(data) > 0 {
			var cfg models.LLMProvider
			if err := json.Unmarshal(data, &cfg); err != nil {
				return fmt.Errorf("invalid JSON in --llm-config-file: %w", err)
			}
			if cfg.Protocol != "" {
				ea.LLMProtocol = cfg.Protocol
			}
			if cfg.Model != "" {
				ea.LLMModel = cfg.Model
			}
			if cfg.APIKey != "" {
				ea.LLMAPIKey = cfg.APIKey
			}
			if cfg.BaseURL != "" {
				ea.LLMAPIURL = cfg.BaseURL
			}
			applyReasoningFromConfig(cfg)
			if ea.LLMProtocol != "" || ea.LLMModel != "" || ea.LLMAPIKey != "" {
				return nil
			}
		}
		// empty file → skip to next step
	}

	// Step 3: ~/.chonkpilot/config.json
	usr, err := user.Current()
	if err == nil {
		userConfigPath := filepath.Join(usr.HomeDir, ".chonkpilot", "config.json")
		data, err := os.ReadFile(userConfigPath)
		if err == nil && len(data) > 0 {
			var userCfg models.UserConfig
			if err := json.Unmarshal(data, &userCfg); err == nil && len(userCfg.LLMs) > 0 {
				idx := userCfg.DefaultLLM
				if idx < 0 || idx >= len(userCfg.LLMs) {
					idx = 0
				}
				llmCfg := userCfg.LLMs[idx]
				ea.LLMProtocol = llmCfg.Protocol
				ea.LLMModel = llmCfg.Model
				ea.LLMAPIKey = llmCfg.APIKey
				ea.LLMAPIURL = llmCfg.BaseURL
				applyReasoningFromConfig(llmCfg)
				if ea.LLMProtocol != "" || ea.LLMModel != "" || ea.LLMAPIKey != "" {
					return nil
				}
			}
		}
	}

	// Step 5: nothing found
	return fmt.Errorf("no LLM configuration found (checked CLI params, --llm-config-file, ~/.chonkpilot/config.json)")
}



package types

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ToolResult holds the result of executing a single tool.
type ToolResult struct {
	Success   bool        `json:"success"`
	Output    string      `json:"output"`
	Error     string      `json:"error,omitempty"`
	Tool      string      `json:"tool"`
	RawResult interface{} `json:"raw_result,omitempty"`
}

// ToolHandler is the signature for all built-in tool handlers.
type ToolHandler func(args map[string]interface{}, depth int) *ToolResult

// SimplifyFunc takes the tool call arguments (raw JSON) and the tool result string,
// and returns a concise thinking text that summarizes what the tool did.
type SimplifyFunc func(args json.RawMessage, result string) string

// ToolSimplifier registers a simplify function for a tool.
type ToolSimplifier struct {
	Tool     string
	Simplify SimplifyFunc
}

var (
	simplifiers   sync.Map
	simplifierOnce sync.Once
)

// RegisterSimplify registers a SimplifyFunc for a tool name.
func RegisterSimplify(toolName string, fn SimplifyFunc) {
	simplifiers.Store(toolName, fn)
}

// GetSimplify returns the registered SimplifyFunc for a tool name.
func GetSimplify(toolName string) (SimplifyFunc, bool) {
	fn, ok := simplifiers.Load(toolName)
	if !ok {
		return nil, false
	}
	return fn.(SimplifyFunc), true
}

func init() {
	simplifierOnce.Do(registerFallback)
}

// FormatToolCallArgs generates a compact argument summary from raw JSON args.
// Returns empty string if args cannot be parsed.
func FormatToolCallArgs(args json.RawMessage) string {
	var raw interface{}
	if err := json.Unmarshal(args, &raw); err != nil {
		return ""
	}

	switch v := raw.(type) {
	case map[string]interface{}:
		var parts []string
		for k, val := range v {
			if s, ok := val.(string); ok && len(s) > 80 {
				parts = append(parts, fmt.Sprintf("%s=...</%d chars>", k, len(s)))
			} else if s, ok := val.(string); ok {
				parts = append(parts, fmt.Sprintf("%s=%v", k, s))
			} else {
				parts = append(parts, fmt.Sprintf("%s=%v", k, val))
			}
		}
		if len(parts) > 3 {
			parts = parts[:3]
			return strings.Join(parts, ", ") + ", ..."
		}
		return strings.Join(parts, ", ")

	case []interface{}:
		return fmt.Sprintf("[%d items]", len(v))

	default:
		return ""
	}
}

func registerFallback() {
	RegisterSimplify("*fallback", func(args json.RawMessage, result string) string {
		argSummary := FormatToolCallArgs(args)

		var tr ToolResult
		resultSummary := ""
		if err := json.Unmarshal([]byte(result), &tr); err == nil {
			if !tr.Success {
				resultSummary = fmt.Sprintf("failed: %s", TruncateStr(tr.Error, 80))
			} else if tr.Output != "" {
				lines := strings.Count(tr.Output, "\n")
				if lines > 0 {
					resultSummary = fmt.Sprintf("ok (%d lines)", lines)
				} else {
					resultSummary = fmt.Sprintf("ok (%d chars)", len(tr.Output))
				}
			} else {
				resultSummary = "ok"
			}
		}

		if argSummary != "" {
			return fmt.Sprintf("used tool(%s) → %s", argSummary, resultSummary)
		}
		return fmt.Sprintf("used tool → %s", resultSummary)
	})
}

// SimplifyToolCall turns a single tool_call + tool_result pair into a thinking text.
func SimplifyToolCall(toolName string, args json.RawMessage, result string) string {
	return SimplifyToolCallWithWorkDir(toolName, args, result, "")
}

// SimplifyToolCallWithWorkDir is like SimplifyToolCall but writes long errors to a temp file
// when workDir is provided, so the LLM can read the full error via read_file.
func SimplifyToolCallWithWorkDir(toolName string, args json.RawMessage, result string, workDir string) string {
	var summary string
	if fn, ok := GetSimplify(toolName); ok {
		summary = fn(args, result)
	} else if fn, ok := GetSimplify("*fallback"); ok {
		summary = fn(args, result)
	} else {
		summary = fmt.Sprintf("used tool %s", toolName)
	}

	// For failed tools with long errors, write full error to a temp file
	if workDir != "" {
		var tr ToolResult
		if err := json.Unmarshal([]byte(result), &tr); err == nil && !tr.Success && len(tr.Error) > 500 {
			errDir := filepath.Join(workDir, ".ide", "tmp", "errors")
			if err := os.MkdirAll(errDir, 0755); err == nil {
				fileName := fmt.Sprintf("%s_%x.txt", toolName, uint64(time.Now().UnixNano()))
				errFile := filepath.Join(".ide", "tmp", "errors", fileName)
				fullPath := filepath.Join(workDir, errFile)
				if os.WriteFile(fullPath, []byte(tr.Error), 0644) == nil {
					// Replace the summary with a reference to the file
					return fmt.Sprintf("failed with error, see %s", filepath.ToSlash(errFile))
				}
			}
		}
	}

	return summary
}

// TruncateStr truncates a string to max length, appending "...".
func TruncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// RegisterToolSimplifier registers a ToolSimplifier.
func RegisterToolSimplifier(s ToolSimplifier) {
	RegisterSimplify(s.Tool, s.Simplify)
}

// SimpleAction returns a SimplifyFunc that just records the tool was invoked.
func SimpleAction(tool string) SimplifyFunc {
	return func(args json.RawMessage, result string) string {
		return tool
	}
}

// Simplifynothing is a no-op simplifier.
func Simplifynothing(args json.RawMessage, result string) string {
	return "tool operation"
}

// Wrap adapts a handler that doesn't need depth to the ToolHandler signature.
func Wrap(fn func(map[string]interface{}) *ToolResult) ToolHandler {
	return func(args map[string]interface{}, _ int) *ToolResult {
		return fn(args)
	}
}

// DepthAware adapts handlers that need the depth parameter.
func DepthAware(fn func(map[string]interface{}, int) *ToolResult) ToolHandler {
	return ToolHandler(fn)
}

// FormatToolResultJSON returns a compact JSON string for a tool result (for LLM consumption).
func FormatToolResultJSON(name string, res *ToolResult) string {
	out := map[string]interface{}{
		"tool":    name,
		"success": res.Success,
	}
	if res.Output != "" {
		out["output"] = res.Output
	}
	if res.Error != "" {
		out["error"] = res.Error
	}
	if res.RawResult != nil {
		out["result"] = res.RawResult
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// GetToolResultOutput extracts the Output field from a ToolResult JSON string.
func GetToolResultOutput(resultJSON string) string {
	var tr ToolResult
	if err := json.Unmarshal([]byte(resultJSON), &tr); err != nil {
		return ""
	}
	return tr.Output
}

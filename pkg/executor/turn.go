package executor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	icfg "github.com/chonkpilot/chonkpilot/internal/config"
	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/chrome"
	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/output"
	"github.com/chonkpilot/chonkpilot/pkg/executor/prompts"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/browser"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
	"go.uber.org/zap"
)

// executeTurn is a convenience wrapper around executeTurnWith.
func executeTurn(ea *ExecutorArgs, prompt, systemPrompt string, outWriter *output.Writer, logger *zap.Logger) (*TurnResult, error) {
	return executeTurnWith(ea, prompt, systemPrompt, outWriter, logger, nil, nil, nil)
}

// executeTurnWith performs turn execution with optional external dependencies.
//   - extSQLDB: if non-nil, use this shared DB connection instead of opening a new one
//   - extBrowserMgr: if non-nil, use this existing BrowserManager instead of creating a fresh one
//   - extCancelCtx: if non-nil, use as the base cancellation context
func executeTurnWith(ea *ExecutorArgs, prompt, systemPrompt string, outWriter *output.Writer, logger *zap.Logger, extSQLDB *sql.DB, extBrowserMgr *browser.BrowserManager, extCancelCtx context.Context) (*TurnResult, error) {
	// Set TEMP/TMP to .ide/tmp so all temp files go into the project's .ide directory
	setTempDir(ea.WorkDir, ea.TempIDEDir)

	// Load user config (Chrome path, MaxToolIterations, timeouts, LLM override)
	ucfg, chromeOK := loadUserConfig(ea, logger)

	// ── Codebase index config (cached per executor process) ──
	codebaseEnabled, codebaseExtensions := loadCodebaseConfig(ea)

	client := llm.NewClient(ea.LLMProtocol, ea.LLMModel, ea.LLMAPIKey, ea.LLMAPIURL, logger)
	logger.Debug("LLM client created",
		zap.String("apiKey", llm.MaskAPIKey(ea.LLMAPIKey)),
	)

	// Determine if running standalone (no pipe, stdout format)
	isStandalone := ea.PipePath == "" && ea.OutputFormat == "stdout"

	logger.Info("executeTurn config",
		zap.String("protocol", ea.LLMProtocol),
		zap.String("model", ea.LLMModel),
		zap.Int("maxTokens", ea.LLMMaxTokens),
		zap.Float64("temperature", ea.LLMTemperature),
		zap.Int("maxToolIterations", ea.LLMMaxToolIterations),
		zap.Duration("responseTimeout", ea.LLMResponseTimeout),
		zap.Duration("streamTimeout", ea.LLMStreamTimeout),
		zap.Int("retryCount", ea.RetryCount),
		zap.Int("retryDelay", ea.RetryDelay),
	)

	// Prepare messages: load history from DB if applicable
	var messages []llm.Message

	// ── Open project DB once for the entire turn ──
	var sqlDB *sql.DB
	var dbOwned bool // true if we opened the DB ourselves
	if extSQLDB != nil {
		sqlDB = extSQLDB
	} else if hasIDEConfig(ea) {
		sqlDB, _ = db.Open(ea.DBWorkDir())
		if sqlDB != nil {
			dbOwned = true
		}
	}
	if dbOwned {
		// Fresh executor process: clear any stale compress locks from previous crashes.
		db.DeleteConfigLike(sqlDB, "compress_lock:%")
		defer db.Close(sqlDB)
	}

	if sqlDB != nil && (ea.SessionID != "" || ea.TurnID != "") {
		// Ensure session+turn records exist in DB, write user message if needed
		ensureSessionAndTurn(ea, prompt, logger, sqlDB)

		// Load session history from DB for multi-turn context
		if dbMessages, err := loadSessionHistory(ea, ea.SessionID, sqlDB); err != nil {
			logger.Warn("Failed to load session history", zap.Error(err))
		} else {
			messages = append(messages, dbMessages...)
			logger.Info("Loaded session history",
				zap.Int("messages", len(dbMessages)),
				zap.Int("total_messages", len(messages)),
			)
		}
	}

	// Build system messages (prompts from DB, fixed prompt, tool usage, scenario, codebase notice)
	sysMessages, err := buildSystemMessages(ea, sqlDB, systemPrompt, prompt, ucfg, codebaseEnabled, codebaseExtensions, logger)
	if err != nil {
		return nil, err
	}
	messages = append(messages, sysMessages...)

	// Initialize tool handler (after ensureSessionAndTurn may have set ea.TurnID)
	handler := initToolHandler(ea, sqlDB, ucfg, chromeOK, codebaseEnabled, codebaseExtensions, client, outWriter, logger)
	if extBrowserMgr != nil {
		handler.Browser = extBrowserMgr
	}

	// Set up cancellation context
	cancelBase := context.Background()
	if extCancelCtx != nil {
		cancelBase = extCancelCtx
	}
	handler.SetCancelContext(cancelBase)

	// ── Build TurnRunner callbacks ──
	// These bridge the generic TurnRunner to executor-specific side effects
	// (DB persistence, IDE event emission, standalone stdout output).

	// Track section headers for standalone non-verbose output
	var sectionPrinted string // "thinking" | "reply" | "tool"

	runner := &llm.TurnRunner{
		Client:   client,
		Logger:   logger,
		CancelCx: handler.CancelCtx,
		Dispatch: func(toolName string, args map[string]interface{}, depth int) (string, bool, error) {
			result := handler.Dispatch(toolName, args, depth)
			resultStr := types.FormatToolResultJSON(toolName, result)
			return resultStr, result.Success, nil
		},
		Callbacks: llm.TurnCallbacks{
			// Reload tools each iteration (for add_tool/delete_tool changes)
			ReloadTools: func() []llm.ToolDefinition {
				return loadAllToolDefs(ea, logger, chromeOK)
			},

			OnIterationStart: func(iter int) {
				if isStandalone && !ea.Verbose {
					if iter == 0 {
						if prompt != "" {
							fmt.Fprintf(os.Stdout, "> %s\n\n", prompt)
						}
						fmt.Fprintf(os.Stdout, "正在思考...\n")
					} else {
						fmt.Fprintf(os.Stdout, "\n继续思考 (%d)...\n", iter+1)
					}
				}
			},

			OnChunk: func(chunk llm.StreamEvent) {
				if chunk.ReasoningContent != "" {
					if isStandalone {
						if ea.Verbose {
							writeSSEToStdout("", chunk.ReasoningContent, reasoningChunk)
						} else {
							if sectionPrinted != "thinking" {
								fmt.Fprintf(os.Stdout, "\n--- thinking ---\n")
								sectionPrinted = "thinking"
							}
							fmt.Fprint(os.Stdout, chunk.ReasoningContent)
						}
					} else {
						outWriter.WriteEvent("message_chunk", eventWithCtx(ea, map[string]interface{}{
							"content": chunk.ReasoningContent,
							"type":    "reasoning",
							"index":   chunk.Index,
						}))
					}
				}
				if chunk.Content != "" {
					if isStandalone {
						if ea.Verbose {
							writeSSEToStdout(chunk.Content, "", contentChunk)
						} else {
							if sectionPrinted == "thinking" {
								fmt.Fprintf(os.Stdout, "\n\n--- reply ---\n")
								sectionPrinted = "reply"
							} else if sectionPrinted != "reply" {
								fmt.Fprintf(os.Stdout, "\n--- reply ---\n")
								sectionPrinted = "reply"
							}
							fmt.Fprint(os.Stdout, chunk.Content)
						}
					} else {
						outWriter.WriteEvent("message_chunk", eventWithCtx(ea, map[string]interface{}{
							"content": chunk.Content,
							"type":    "text",
							"index":   chunk.Index,
						}))
					}
				}
			},

			OnAssistantMsg: func(msg llm.Message) ([]string, error) {
				// Persist assistant message to DB
				toolCallMsgIDs := saveAssistantMessage(ea, msg, logger, sqlDB)
				return toolCallMsgIDs, nil
			},

			OnToolCall: func(tc llm.ToolCall, args map[string]interface{}) (string, error) {
				if !isStandalone || ea.Verbose {
					argsJSON := json.RawMessage(tc.Function.Arguments)
					callSimplified := tc.Function.Name + "(" + types.FormatToolCallArgs(argsJSON) + ")"
					outWriter.WriteEvent("tool_call", eventWithCtx(ea, map[string]interface{}{
						"tool":         tc.Function.Name,
						"tool_call_id": tc.ID,
						"arguments":    tc.Function.Arguments,
						"simplified":   callSimplified,
					}))
				} else {
					if sectionPrinted != "tool" {
						fmt.Fprintf(os.Stdout, "\n--- tool ---\n")
						sectionPrinted = "tool"
					}
					argsJSON, _ := json.Marshal(args)
					fmt.Fprintf(os.Stdout, "→ %s(%s)\n", tc.Function.Name, string(argsJSON))
				}
				// No DB persistence here — the caller handles tool call msg IDs via OnAssistantMsg
				return "", nil
			},

			OnToolResult: func(tc llm.ToolCall, resultStr string, success bool) error {
				if !isStandalone || ea.Verbose {
					argsJSON := json.RawMessage(tc.Function.Arguments)
					resultSimplified := types.SimplifyToolCall(tc.Function.Name, argsJSON, resultStr)
					outWriter.WriteEvent("tool_result", eventWithCtx(ea, map[string]interface{}{
						"tool":         tc.Function.Name,
						"tool_call_id": tc.ID,
						"success":      success,
						"result":       resultStr,
						"simplified":   resultSimplified,
					}))
				} else {
					fmt.Fprintf(os.Stdout, "← %s\n", resultStr)
				}
				// Persist tool result message to DB
				toolResultMsg := llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    resultStr,
				}
				saveToolMessage(ea, toolResultMsg, tc.Function.Name, logger, sqlDB)
				return nil
			},

			OnIterationEnd: func(iter int, reasoningContent string, toolCallMsgIDs []string) {
				// Brief extraction: first 3 lines of reasoning content
				if reasoningContent != "" && len(toolCallMsgIDs) > 0 {
					brief := extractBrief(reasoningContent)
					for _, msgID := range toolCallMsgIDs {
						if err := db.UpdateMessageBrief(sqlDB, msgID, brief); err != nil {
							logger.Warn("failed to update tool_call brief", zap.String("message_id", msgID), zap.Error(err))
						}
					}
				}
			},

			OnLLMError: func(code, message string, retryable bool, attempt, maxAttempts int) {
				if outWriter != nil {
					payload := map[string]interface{}{
						"code":          code,
						"message":       message,
						"retryable":     retryable,
						"retry_count":   maxAttempts,
						"retry_attempt": attempt,
						"turn_id":       ea.TurnID,
						"session_id":    ea.SessionID,
					}
					outWriter.WriteEvent("llm_error", eventWithCtx(ea, payload))
				}
			},

			OnLLMRetry: func(attempt, maxAttempts int, waitSeconds int) {
				if outWriter != nil {
					outWriter.WriteEvent("llm_retry", eventWithCtx(ea, map[string]interface{}{
						"retry_attempt": attempt,
						"retry_count":   maxAttempts,
						"wait_seconds":  waitSeconds,
					}))
				}
			},
		},
	}

	// In verbose mode, print the assembled outgoing request
	if isStandalone && ea.Verbose {
		initialToolDefs := loadAllToolDefs(ea, logger, chromeOK)
		printOutgoingMessages(ea, messages, initialToolDefs)
	}

	maxAttempts := ea.RetryCount + 1
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	result, err := runner.Run(llm.TurnConfig{
		Messages:            messages,
		MaxIter:             ea.LLMMaxToolIterations,
		MaxAttempts:         maxAttempts,
		MaxTokens:           ea.LLMMaxTokens,
		Temperature:         ea.LLMTemperature,
		Thinking:            ea.Thinking,
		ReasoningEffort:     ea.ReasoningEffort,
		ResponseTimeout:     ea.LLMResponseTimeout,
		StreamTimeout:       ea.LLMStreamTimeout,
		TLSHandshakeTimeout: resolveTLSHandshakeTimeout(func() int {
			if ucfg != nil {
				return ucfg.LLMTLSHandshakeTimeout
			}
			return 0
		}()),
		RetryDelaySeconds: ea.RetryDelay,
	})

	fullResponseContent := ""
	if result != nil {
		fullResponseContent = result.Content
	}

	if err != nil {
		if !isStandalone || ea.Verbose {
			outWriter.WriteEvent("error", eventWithCtx(ea, map[string]interface{}{
				"code":    "ERR_TURN_FAILED",
				"message": err.Error(),
			}))
		}
		return nil, err
	}

	// Print separator in standalone mode
	if isStandalone {
		if ea.Verbose {
			writeSSEStop()
		} else {
			fmt.Fprintln(os.Stdout)
		}
	}

	// ── Flush code index changes ──
	// Batch-enqueue all files modified during this turn and wake the worker.
	n := handler.FlushCodeIndex()
	if n > 0 {
		logger.Info("codeindex: flushed changed files after tool loop", zap.Int("count", n))
	}

	// ── Write completion event (non-standalone mode or verbose) ──
	if ea.TurnID != "" {
		if !isStandalone || ea.Verbose {
			outWriter.WriteEvent("complete", eventWithCtx(ea, map[string]interface{}{
				"result": fullResponseContent,
				"score":  0,
			}))
		}
	}

	// ── Asynchronous summary generation ──
	// Runs in background; uses the same LLM client config.
	if hasIDEConfig(ea) && ea.SessionID != "" && ea.TurnID != "" {
		go generateSessionSummary(ea, logger)
	}

	return &TurnResult{
		TurnID: ea.TurnID,
		A:      fullResponseContent,
		Score:  0,
	}, nil
}

// defaultCodebaseExtensions is the fallback list when project config has none.
var defaultCodebaseExtensions = []string{".go", ".js", ".ts", ".jsx", ".tsx", ".vue", ".py", ".rs", ".java", ".c", ".cpp", ".h", ".hpp", ".cs", ".rb", ".php", ".swift", ".kt"}

// Per-process cache for codebase config — loaded once from DB and reused.
var (
	cachedCBEnabled    bool
	cachedCBExtensions []string
	cbConfigOnce       sync.Once
)

// loadCodebaseConfig reads codebase index config from the project DB.
// Results are cached for the lifetime of the executor process (sync.Once),
// so successive calls within the same turn don't re-open the DB.
func loadCodebaseConfig(ea *ExecutorArgs) (enabled bool, extensions []string) {
	cbConfigOnce.Do(func() {
		cachedCBExtensions = defaultCodebaseExtensions
		if !hasIDEConfig(ea) {
			return
		}
		sqlDB, err := db.Open(ea.DBWorkDir())
		if err != nil {
			return
		}
		defer db.Close(sqlDB)
		if v, err := db.GetConfig(sqlDB, "codebase_index.enabled"); err == nil && v == "true" {
			cachedCBEnabled = true
		}
		if v, err := db.GetConfig(sqlDB, "codebase_index.extensions"); err == nil && v != "" {
			var exts []string
			if json.Unmarshal([]byte(v), &exts) == nil && len(exts) > 0 {
				cachedCBExtensions = exts
			}
		}
	})
	return cachedCBEnabled, cachedCBExtensions
}

// resolveTLSHandshakeTimeout returns the TLS handshake timeout from UserConfig or the default 30s.
func resolveTLSHandshakeTimeout(tlsTimeoutSec int) time.Duration {
	if tlsTimeoutSec > 0 {
		return time.Duration(tlsTimeoutSec) * time.Second
	}
	return 30 * time.Second
}

// loadUserConfig loads user config from ~/.chonkpilot/config.json and applies LLM name override.
func loadUserConfig(ea *ExecutorArgs, logger *zap.Logger) (*models.UserConfig, bool) {
	var chromeOK bool
	ucfg, err := icfg.EnsureUserConfig()
	if err != nil {
		logger.Warn("could not load user config, using defaults", zap.Error(err))
	}
	if ucfg != nil {
		chromeOK = ucfg.ChromePath != "" && chrome.Verify(ucfg.ChromePath)
		if ucfg.MaxToolIterations > 0 {
			ea.LLMMaxToolIterations = ucfg.MaxToolIterations
		}
		if ucfg.ResponseTimeout > 0 {
			ea.LLMResponseTimeout = time.Duration(ucfg.ResponseTimeout) * time.Second
		}
		if ucfg.StreamTimeout > 0 {
			ea.LLMStreamTimeout = time.Duration(ucfg.StreamTimeout) * time.Second
		}

		// Apply --llm name override: select LLM by name from user config
		if ea.LLMName != "" {
			for _, llmCfg := range ucfg.LLMs {
				if llmCfg.Name == ea.LLMName {
					ea.LLMProtocol = llmCfg.Protocol
					ea.LLMModel = llmCfg.Model
					ea.LLMAPIKey = llmCfg.APIKey
					ea.LLMAPIURL = llmCfg.BaseURL
					if !ea.ThinkingSet {
						ea.Thinking = llmCfg.Thinking
					}
					if ea.ReasoningEffort == "" {
						ea.ReasoningEffort = llmCfg.ReasoningEffort
					}
					if llmCfg.MaxTokens > 0 {
						ea.LLMMaxTokens = llmCfg.MaxTokens
					}
					if llmCfg.Temperature > 0 {
						ea.LLMTemperature = llmCfg.Temperature
					}
					break
				}
			}
		}
	}

	// Apply --effort CLI override (takes precedence over config)
	if ea.Effort != "" {
		ea.ReasoningEffort = ea.Effort
	}

	return ucfg, chromeOK
}

// buildSystemMessages loads prompts from DB, assembles system messages including
// fixed prompt, tool usage, custom prompt, scenario, and codebase notice.
func buildSystemMessages(ea *ExecutorArgs, sqlDB *sql.DB, systemPrompt string, prompt string, ucfg *models.UserConfig, codebaseEnabled bool, codebaseExtensions []string, logger *zap.Logger) ([]llm.Message, error) {
	// ── Load prompts from DB (with embedded fallbacks) ──
	systemPromptText := prompts.DefaultSystemPrompt
	toolUsageText := prompts.DefaultToolUsage
	if sqlDB != nil {
		// Try project_prompts table first, fallback to legacy config table
		if v, err := db.GetProjectPrompt(sqlDB, "system_prompt"); err == nil && v != "" {
			systemPromptText = v
		} else if v, err := db.GetConfig(sqlDB, "system_prompt"); err == nil && v != "" {
			systemPromptText = v
		}
		if v, err := db.GetProjectPrompt(sqlDB, "tool_usage_prompt"); err == nil && v != "" {
			toolUsageText = v
		} else if v, err := db.GetConfig(sqlDB, "tool_usage_prompt"); err == nil && v != "" {
			toolUsageText = v
		}
		// Agent-specific system prompt overrides the global one
		if ea.Agent != "" {
			agents, err := db.GetProjectAgents(sqlDB)
			if err == nil {
				for _, a := range agents {
					if a.Title == ea.Agent && a.Prompt != "" {
						// Prepend common rules from DB (user-editable via add_agent)
						commonPrompt, _ := db.GetConfig(sqlDB, "agent.common.system_prompt")
						if commonPrompt != "" {
							systemPromptText = commonPrompt + "\n\n" + a.Prompt
						} else {
							systemPromptText = a.Prompt
						}
						// Agent-specific LLM override
						if a.LLMRef != "" && ucfg != nil {
							for _, llmCfg := range ucfg.LLMs {
								if llmCfg.Name == a.LLMRef {
									ea.LLMProtocol = llmCfg.Protocol
									ea.LLMModel = llmCfg.Model
									ea.LLMAPIKey = llmCfg.APIKey
									ea.LLMAPIURL = llmCfg.BaseURL
									if !ea.ThinkingSet {
										ea.Thinking = llmCfg.Thinking
									}
									if ea.ReasoningEffort == "" {
										ea.ReasoningEffort = llmCfg.ReasoningEffort
									}
									if llmCfg.MaxTokens > 0 {
										ea.LLMMaxTokens = llmCfg.MaxTokens
									}
									if llmCfg.Temperature > 0 {
										ea.LLMTemperature = llmCfg.Temperature
									}
									break
								}
							}
						}
						break
					}
				}
			}
		}
	}

	// ── Assemble system messages ──
	var messages []llm.Message

	// 1. Fixed prompt (always present)
	fixedPrompt := "你的名字是肥猫，一个人工智能助理。你运行在" + runtime.GOOS + "环境中。当前工作目录：" + ea.WorkDir

	// Append detected runtime environments
	if ucfg != nil {
		var envParts []string
		if ucfg.JavaPath != "" {
			envParts = append(envParts, "Java: "+ucfg.JavaPath)
		}
		if ucfg.PythonPath != "" {
			envParts = append(envParts, "Python: "+ucfg.PythonPath)
		}
		if ucfg.NodePath != "" {
			envParts = append(envParts, "Node.js: "+ucfg.NodePath)
		}
		if len(envParts) > 0 {
			fixedPrompt += "\n\n可用运行时环境：\n" + strings.Join(envParts, "\n")
		}
	}

	messages = append(messages, llm.Message{Role: "system", Content: fixedPrompt})

	// 2. Tool usage guide
	if toolUsageText != "" {
		messages = append(messages, llm.Message{Role: "system", Content: toolUsageText})
	}

	// 3. Custom system prompt (CLI --system-prompt takes precedence over DB)
	if systemPrompt == "" {
		systemPrompt = systemPromptText
	}

	// Inject template variables (${executor}, ${workdir})
	executorPath, _ := os.Executable()
	systemPrompt = strings.ReplaceAll(systemPrompt, "${executor}", executorPath)
	systemPrompt = strings.ReplaceAll(systemPrompt, "${workdir}", ea.WorkDir)

	if systemPrompt != "" {
		messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
	}

	// 3b. Scenario prompt: appended after the regular system prompt
	if scenarioPrompt := os.Getenv("CHONKPILOT_SCENARIO_PROMPT"); scenarioPrompt != "" {
		messages = append(messages, llm.Message{Role: "system", Content: scenarioPrompt})
	}

	// 4. Codebase index notice (if enabled)
	if codebaseEnabled {
		cbNotice := fmt.Sprintf("[代码库索引已启用] 你可以使用 query_codebase 工具查询项目结构、符号定义、文件依赖等。索引的文件类型：%s", strings.Join(codebaseExtensions, ", "))
		messages = append(messages, llm.Message{Role: "system", Content: cbNotice})
	}

	// Add user message (for non-IDE modes where prompt comes from CLI)
	if prompt != "" {
		messages = append(messages, llm.Message{Role: "user", Content: prompt})
	}

	return messages, nil
}

// initToolHandler creates and configures the tool handler with LLM config,
// UserConfig limits, security rules, chrome, file versioner, code indexer, and event callbacks.
func initToolHandler(ea *ExecutorArgs, sqlDB *sql.DB, ucfg *models.UserConfig, chromeOK bool, codebaseEnabled bool, codebaseExtensions []string, client *llm.Client, outWriter *output.Writer, logger *zap.Logger) *toolhandler.Handler {
	handler := toolhandler.NewHandler(ea.WorkDir, ea.DBWorkDir(), ea.SessionID, ea.TurnID, logger)
	handler.LLMProtocol = ea.LLMProtocol
	handler.LLMModel = ea.LLMModel
	handler.LLMAPIKey = ea.LLMAPIKey
	handler.LLMAPIURL = ea.LLMAPIURL
	handler.Thinking = ea.Thinking
	handler.ReasoningEffort = ea.ReasoningEffort
	// Apply configurable limits from UserConfig
	if ucfg != nil {
		if ucfg.ToolMaxDepth > 0 {
			handler.MaxDepth = ucfg.ToolMaxDepth
		}
		if ucfg.TaskPollIntervalMs > 0 {
			handler.TaskPollIntervalMs = ucfg.TaskPollIntervalMs
		}
		if ucfg.SearchMaxResults > 0 {
			handler.SearchMaxResults = ucfg.SearchMaxResults
		}
		if ucfg.FetchMaxBodySizeKB > 0 {
			handler.FetchMaxBodySizeKB = ucfg.FetchMaxBodySizeKB
		}
		if ucfg.BrowserWindowWidth > 0 {
			handler.Browser.WindowWidth = ucfg.BrowserWindowWidth
		}
		if ucfg.BrowserWindowHeight > 0 {
			handler.Browser.WindowHeight = ucfg.BrowserWindowHeight
		}
		if ucfg.BrowserLogCap > 0 {
			handler.Browser.LogCap = ucfg.BrowserLogCap
		}
	}
	// Propagate config values to subpackage-level variables (grep, fetch, search, etc.)
	handler.PropagateConfig()

	// Apply project_security rules from DB (if any entries configured)
	handler.SetSecurityFromDB()

	if !chromeOK {
		handler.SetNoChrome()
	}
	// Set up file versioner (best-effort: open versions.db)
	if v, err := fileversions.NewVersioner(ea.WorkDir); err == nil {
		handler.SetFileVersioner(v)
	} else {
		logger.Warn("file versions disabled", zap.Error(err))
	}
	// Set up codebase indexer if enabled
	if codebaseEnabled {
		codebaseDB, err := codeindex.OpenCodebaseDB(ea.DBWorkDir())
		if err == nil {
			// Create LLMCaller that wraps the client for non-streaming index analysis
			codeIndexTemp := 0.1
			if ucfg != nil && ucfg.CodeIndexTemperature > 0 {
				codeIndexTemp = ucfg.CodeIndexTemperature
			}
			caller := func(systemPrompt, userPrompt string) (string, error) {
				ch, err := client.Chat([]llm.Message{
					{Role: "system", Content: systemPrompt},
					{Role: "user", Content: userPrompt},
				}, llm.ChatOptions{
					Model:       ea.LLMModel,
					Temperature: codeIndexTemp,
					MaxTokens:   resolveMaxTokens(ea),
				})
				if err != nil {
					return "", err
				}
				var result strings.Builder
				for evt := range ch {
					if evt.Error != nil {
						return "", evt.Error
					}
					result.WriteString(evt.Content)
				}
				return result.String(), nil
			}
			idxer := codeindex.NewIndexer(codebaseDB, ea.WorkDir, codebaseExtensions, caller, logger)
			// DO NOT call idxer.Start() here — the IDE's Indexer runs continuously
			// and processes all queued items. The executor only enqueues files
			// via MarkChanged/FlushChangedFiles.
			handler.SetCodeIndexer(idxer)
			logger.Info("codebase indexer enabled for executor (enqueue only)",
				zap.Strings("extensions", codebaseExtensions),
				zap.Int("queue_items", func() int { p, i, f, fe := idxer.QueueStats(); return p + i + f + fe }()))
		} else {
			logger.Warn("failed to open codebase.db, indexing disabled", zap.Error(err))
		}
	}

	// Connect progress callback for run_tasks events
	if outWriter != nil {
		handler.SetOnProgress(func(data map[string]interface{}) {
			outWriter.WriteEvent("tool_progress", eventWithCtx(ea, data))
		})
		handler.SetWriteEvent(func(eventType string, payload map[string]interface{}) {
			outWriter.WriteEvent(eventType, eventWithCtx(ea, payload))
		})
	}

	return handler
}

package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	icfg "github.com/chonkpilot/chonkpilot/internal/config"
	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/pkg/chrome"
	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/output"
	"github.com/chonkpilot/chonkpilot/pkg/executor/prompts"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
	"go.uber.org/zap"
)

// executeTurn performs the actual turn execution with tool call loop support.
func executeTurn(ea *ExecutorArgs, prompt, systemPrompt string, outWriter *output.Writer, logger *zap.Logger) (*TurnResult, error) {
	// Set TEMP/TMP to .ide/tmp so all temp files go into the project's .ide directory
	setTempDir(ea.WorkDir, ea.TempIDEDir)

	// Apply global config defaults (Chrome path, MaxToolIterations, timeouts)
	// from ~/.chonkpilot/config.json — fills in defaults and saves back if needed.
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
					ea.LLMProvider = llmCfg.Provider
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

	// ── Codebase index config ──
	codebaseEnabled := false
	codebaseExtensions := []string{".go", ".js", ".ts", ".jsx", ".tsx", ".vue", ".py", ".rs", ".java", ".c", ".cpp", ".h", ".hpp", ".cs", ".rb", ".php", ".swift", ".kt"}
	if hasIDEConfig(ea) {
		if sqlDB, err := db.Open(ea.DBWorkDir()); err == nil {
			if v, err := db.GetConfig(sqlDB, "codebase_index.enabled"); err == nil && v == "true" {
				codebaseEnabled = true
			}
			if v, err := db.GetConfig(sqlDB, "codebase_index.extensions"); err == nil && v != "" {
				var exts []string
				if json.Unmarshal([]byte(v), &exts) == nil && len(exts) > 0 {
					codebaseExtensions = exts
				}
			}
			db.Close(sqlDB)
		}
	}

	client := llm.NewClient(ea.LLMProvider, ea.LLMModel, ea.LLMAPIKey, ea.LLMAPIURL, logger)

	// Determine if running standalone (no pipe, stdout format)
	isStandalone := ea.PipePath == "" && ea.OutputFormat == "stdout"

	logger.Info("executeTurn config",
		zap.String("provider", ea.LLMProvider),
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

	if hasIDEConfig(ea) && (ea.SessionID != "" || ea.TurnID != "") {
		// Ensure session+turn records exist in DB, write user message if needed
		ensureSessionAndTurn(ea, prompt, logger)

		// Load session history from DB for multi-turn context
		if dbMessages, err := loadSessionHistory(ea, ea.SessionID); err != nil {
			logger.Warn("Failed to load session history", zap.Error(err))
		} else {
			messages = append(messages, dbMessages...)
			logger.Info("Loaded session history",
				zap.Int("messages", len(dbMessages)),
				zap.Int("total_messages", len(messages)),
			)
		}
	}

	// ── Load prompts from DB (with embedded fallbacks) ──
	systemPromptText := prompts.DefaultSystemPrompt
	toolUsageText := prompts.DefaultToolUsage
	if hasIDEConfig(ea) {
		if sqlDB, err := db.Open(ea.DBWorkDir()); err == nil {
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
							break
						}
					}
				}
			}
			db.Close(sqlDB)
		}
	}

	// ── Assemble system messages ──
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

	// 4. Codebase index notice (if enabled)
	if codebaseEnabled {
		cbNotice := fmt.Sprintf("[代码库索引已启用] 你可以使用 query_codebase 工具查询项目结构、符号定义、文件依赖等。索引的文件类型：%s", strings.Join(codebaseExtensions, ", "))
		messages = append(messages, llm.Message{Role: "system", Content: cbNotice})
	}

	// Add user message (for non-IDE modes where prompt comes from CLI)
	if prompt != "" {
		messages = append(messages, llm.Message{Role: "user", Content: prompt})
	}

	// Initialize tool handler (after ensureSessionAndTurn may have set ea.TurnID)
	handler := toolhandler.NewHandler(ea.WorkDir, ea.DBWorkDir(), ea.SessionID, ea.TurnID, logger)
	handler.LLMProvider = ea.LLMProvider
	handler.LLMModel = ea.LLMModel
	handler.LLMAPIKey = ea.LLMAPIKey
	handler.LLMAPIURL = ea.LLMAPIURL
	handler.Thinking = ea.Thinking
	handler.ReasoningEffort = ea.ReasoningEffort
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
			caller := func(systemPrompt, userPrompt string) (string, error) {
				ch, err := client.Chat([]llm.Message{
					{Role: "system", Content: systemPrompt},
					{Role: "user", Content: userPrompt},
				}, llm.ChatOptions{
					Model:       ea.LLMModel,
					Temperature: 0.1, // low temp for deterministic extraction
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

	// Load initial tool definitions for verbose printing
	verboseToolDefs := loadAllToolDefs(ea, logger, chromeOK)
	// In verbose mode, print the assembled outgoing request
	if isStandalone && ea.Verbose {
		printOutgoingMessages(ea, messages, verboseToolDefs)
	}

	maxToolIterations := ea.LLMMaxToolIterations
	if maxToolIterations <= 0 {
		maxToolIterations = 800
	}
	var fullResponse strings.Builder

	// saveAccumulatedState persists partial assistant message and tool results to DB
	// before reporting an LLM error, so the accumulated work is not lost.
	saveAccumulatedState := func(textContent, reasoningContent string, toolMsgs []llm.Message) {
		if ea.TurnID == "" {
			return
		}
		if textContent != "" || reasoningContent != "" || len(toolMsgs) > 0 {
			// Save partial assistant message
			partialMsg := llm.Message{
				Role:             "assistant",
				Content:          textContent,
				ReasoningContent: reasoningContent,
			}
			saveAssistantMessage(ea, partialMsg, logger)
		}
		// Tool results are already saved in the tool processing loop (saveToolMessage)
		// No extra action needed — they are already persisted.
	}

	// emitLLMError emits a detailed LLM error event to IDE
	emitLLMError := func(code LLMErrorCode, message string, retryable bool, attempt, maxRetries int) {
		if outWriter != nil {
			payload := map[string]interface{}{
				"code":         string(code),
				"message":      message,
				"retryable":    retryable,
				"retry_count":  maxRetries,
				"retry_attempt": attempt,
				"turn_id":      ea.TurnID,
				"session_id":   ea.SessionID,
			}
			outWriter.WriteEvent("llm_error", eventWithCtx(ea, payload))
		}
	}

	// emitLLMRetry emits a retry progress event to IDE
	emitLLMRetry := func(attempt, maxRetries int, waitSeconds int) {
		if outWriter != nil {
			payload := map[string]interface{}{
				"retry_attempt": attempt,
				"retry_count":  maxRetries,
				"wait_seconds": waitSeconds,
			}
			outWriter.WriteEvent("llm_retry", eventWithCtx(ea, payload))
		}
	}

	for iter := 0; iter < maxToolIterations; iter++ {
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

		// Reload tool definitions each iteration so add_tool/delete_tool changes take effect
		toolDefs := loadAllToolDefs(ea, logger, chromeOK)

		var toolCalls []*llm.ToolCall
		var textContent strings.Builder
		var reasoningContent strings.Builder
		sectionPrinted := "" // tracks which section header was printed last

		// Call LLM with retry support (including stream errors)
		llmSucceeded := false
		var llmStream <-chan llm.StreamEvent
		maxRetries := ea.RetryCount + 1 // RetryCount = retries beyond the first attempt
		if maxRetries <= 0 {
			maxRetries = 1 // at least one attempt
		}

	llmAttemptLoop:
		for attempt := 1; attempt <= maxRetries; attempt++ {
			var err error
			maxTokens := ea.LLMMaxTokens
			if maxTokens <= 0 {
				maxTokens = 65535
			}
			temp := ea.LLMTemperature
			if temp <= 0 {
				temp = 0.7
			}
			llmStream, err = client.Chat(messages, llm.ChatOptions{
				Stream:            true,
				Tools:             toolDefs,
				Temperature:       temp,
				MaxTokens:         maxTokens,
				Thinking:          ea.Thinking,
				ReasoningEffort:   ea.ReasoningEffort,
				ResponseTimeout: ea.LLMResponseTimeout,
				StreamTimeout:     ea.LLMStreamTimeout,
			})
			if err != nil {
				// Initial LLM call failed — classify and handle
				code, retryable := classifyLLMError(err)
				isLastAttempt := attempt >= maxRetries

				// Save accumulated state before reporting error
				saveAccumulatedState(textContent.String(), reasoningContent.String(), nil)

				if !retryable || isLastAttempt {
					fmt.Fprintf(os.Stderr, "ERROR: LLM API call failed (iter=%d, attempt=%d/%d): %s\n",
						iter, attempt, maxRetries, err.Error())
					emitLLMError(code, err.Error(), retryable, attempt, maxRetries)
					return nil, fmt.Errorf("LLM %s: %s", code, err.Error())
				}

				// Retryable — emit error + retry event, wait, then continue loop
				fmt.Fprintf(os.Stderr, "WARN: LLM API call failed (iter=%d, attempt=%d/%d), retrying in %ds: %s\n",
					iter, attempt, maxRetries, ea.RetryDelay, err.Error())
				emitLLMError(code, err.Error(), true, attempt, maxRetries)
				emitLLMRetry(attempt, maxRetries, ea.RetryDelay)
				for i := 0; i < ea.RetryDelay; i++ {
					time.Sleep(1 * time.Second)
				}
				continue
			}

			// Stream reading loop — errors here are also retryable
			streamOk := true
		StreamLoop:
			for chunk := range llmStream {
				if chunk.Error != nil {
					code, retryable := classifyLLMError(chunk.Error)
					isLastAttempt := attempt >= maxRetries

					// Save partial state
					saveAccumulatedState(textContent.String(), reasoningContent.String(), nil)

					if !retryable || isLastAttempt {
						fmt.Fprintf(os.Stderr, "ERROR: LLM stream error (iter=%d, attempt=%d/%d): %s\n",
							iter, attempt, maxRetries, chunk.Error.Error())
						emitLLMError(code, chunk.Error.Error(), retryable, attempt, maxRetries)
						return nil, fmt.Errorf("LLM stream error: %s", chunk.Error.Error())
					}

					// Retryable stream error — emit retry event, wait, restart Chat call
					fmt.Fprintf(os.Stderr, "WARN: LLM stream error (iter=%d, attempt=%d/%d), retrying in %ds: %s\n",
						iter, attempt, maxRetries, ea.RetryDelay, chunk.Error.Error())
					emitLLMError(code, chunk.Error.Error(), true, attempt, maxRetries)
					emitLLMRetry(attempt, maxRetries, ea.RetryDelay)
					for i := 0; i < ea.RetryDelay; i++ {
						time.Sleep(1 * time.Second)
					}
					streamOk = false
					break StreamLoop
				}
				if chunk.ReasoningContent != "" {
					reasoningContent.WriteString(chunk.ReasoningContent)
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
					textContent.WriteString(chunk.Content)
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
				if chunk.ToolCall != nil {
					toolCalls = append(toolCalls, chunk.ToolCall)
				}
			}

			if streamOk {
				llmSucceeded = true
				break llmAttemptLoop
			}
			// stream failed, retry next attempt (re-enter retry loop)
		}

		if !llmSucceeded {
			return nil, fmt.Errorf("LLM call failed after %d attempts", maxRetries)
		}

		// Print stop/separator
		if isStandalone {
			if ea.Verbose {
				writeSSEStop()
			} else {
				fmt.Fprintln(os.Stdout)
			}
		}

		// Add assistant message with accumulated text and reasoning content
		assistantMsg := llm.Message{
			Role:             "assistant",
			Content:          textContent.String(),
			ReasoningContent: reasoningContent.String(),
		}
		if len(toolCalls) > 0 {
			assistantMsg.ToolCalls = make([]llm.ToolCall, len(toolCalls))
			for i, tc := range toolCalls {
				assistantMsg.ToolCalls[i] = *tc
			}
		}
		messages = append(messages, assistantMsg)
		fullResponse.WriteString(textContent.String())

		// Persist assistant message to DB
		saveAssistantMessage(ea, assistantMsg, logger)

		// No tool calls → we're done
		if len(toolCalls) == 0 {
			break
		}

		logger.Info("Processing tool calls",
			zap.Int("iteration", iter),
			zap.Int("tool_calls", len(toolCalls)),
		)

		// Process each tool call
		for _, tc := range toolCalls {
			// Parse arguments (supports both JSON object and JSON array inputs)
			var raw interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &raw); err != nil {
				logger.Warn("Failed to parse tool call arguments", zap.Error(err), zap.String("raw", tc.Function.Arguments))
				toolResultMsg := llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    fmt.Sprintf(`{"error":"failed to parse arguments: %s"}`, err.Error()),
				}
				messages = append(messages, toolResultMsg)
				saveToolMessage(ea, toolResultMsg, tc.Function.Name, logger)
				continue
			}
			var args map[string]interface{}
			switch v := raw.(type) {
			case map[string]interface{}:
				args = v
			case []interface{}:
				args = map[string]interface{}{"": v}
			default:
				logger.Warn("Unexpected arguments type", zap.Any("type", fmt.Sprintf("%T", raw)))
				toolResultMsg := llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    `{"error":"arguments must be a JSON object or array"}`,
				}
				messages = append(messages, toolResultMsg)
				saveToolMessage(ea, toolResultMsg, tc.Function.Name, logger)
				continue
			}

			logger.Info("Dispatching tool",
				zap.String("tool", tc.Function.Name),
				zap.String("tool_call_id", tc.ID),
				zap.Any("args", args),
			)

			// Report tool call event (suppressed in standalone non-verbose mode)
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
				// Non-verbose: concise tool section header on first call
				if sectionPrinted != "tool" {
					fmt.Fprintf(os.Stdout, "\n--- tool ---\n")
					sectionPrinted = "tool"
				}
				argsJSON, _ := json.Marshal(args)
				fmt.Fprintf(os.Stdout, "→ %s(%s)\n", tc.Function.Name, string(argsJSON))
			}

			// Dispatch tool
			result := handler.Dispatch(tc.Function.Name, args, 0)

			// Format result
			resultStr := types.FormatToolResultJSON(tc.Function.Name, result)

			// Report tool result event (suppressed in standalone non-verbose mode)
			if !isStandalone || ea.Verbose {
				argsJSON := json.RawMessage(tc.Function.Arguments)
				resultSimplified := types.SimplifyToolCall(tc.Function.Name, argsJSON, resultStr)
				outWriter.WriteEvent("tool_result", eventWithCtx(ea, map[string]interface{}{
					"tool":         tc.Function.Name,
					"tool_call_id": tc.ID,
					"success":      result.Success,
					"result":       resultStr,
					"simplified":   resultSimplified,
				}))
			} else {
				// Non-verbose: concise result line
				fmt.Fprintf(os.Stdout, "← %s\n", resultStr)
			}

			// Add and persist tool result message
			toolResultMsg := llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    resultStr,
			}
			messages = append(messages, toolResultMsg)
			saveToolMessage(ea, toolResultMsg, tc.Function.Name, logger)
		}
	}

	// ── Flush code index changes ──
	// Batch-enqueue all files modified during this turn and wake the worker.
	n := handler.FlushCodeIndex()
	if n > 0 {
		logger.Info("codeindex: flushed changed files after tool loop", zap.Int("count", n))
	}

	// ── Asynchronous summary generation ──
	// Runs in background; uses the same LLM client config.
	if hasIDEConfig(ea) && ea.SessionID != "" && ea.TurnID != "" {
		go generateSessionSummary(ea, logger)
	}

	return &TurnResult{
		TurnID: ea.TurnID,
		A:      fullResponse.String(),
		Score:  0,
	}, nil
}

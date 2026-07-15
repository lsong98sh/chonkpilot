package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/pkg/executor/discover"
	"github.com/chonkpilot/chonkpilot/pkg/executor/output"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/call_llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/tool"
	"go.uber.org/zap"
)

// ExecutorArgs holds parsed command-line arguments for the executor.
type ExecutorArgs struct {
	WorkDir          string
	PromptFile       string
	Prompt           string
	SystemPrompt     string
	SystemPromptFile string
	SessionID        string
	TurnID           string
	PipePath         string
	ParentPipePath   string
	PipeAddr         string
	Tools            string
	ToolsFile        string
	OutputFormat     string // stdout or json
	LLMProvider      string
	LLMModel         string
	LLMAPIKey        string
	LLMAPIURL        string
	LLMConfigFile    string
	Thinking         bool   // enable thinking mode (extra_body thinking.type)
	ThinkingSet      bool   // true if user explicitly set --reasoning=on/off
	ReasoningEffort  string // low/medium/high/max, from config
	Verbose          bool   // verbose output (standalone only)
	LogLevel         string // debug/info/warn/error
	Agent            string // agent name for agent-specific system prompt
	RetryCount       int    // max LLM retry attempts on transient errors (default 0 = no retry)
	RetryDelay       int    // seconds to wait between retries (default 5)
	LLMMaxTokens     int    // max output tokens per LLM call (default 65535)
	LLMTemperature   float64 // LLM temperature (default 0.7)
	LLMMaxToolIterations int // max tool call iterations per turn (default 800, 0=unlimited)
	LLMResponseTimeout time.Duration // time-to-first-token timeout
	LLMStreamTimeout     time.Duration // idle timeout between stream chunks

	// Runtime overrides (from chat page controls)
	LLMName string // --llm: select LLM provider by name from ~/.chonkpilot/config.json
	Effort  string // --effort: reasoning effort override (high/max)

	KeepFullTurns            int           // context compression: turns to keep fully raw (default 5)
	KeepSimplifiedTurns      int           // context compression: turns to keep simplified (default 15)
	ForceCompressThreshold   int           // token threshold for forced compression (default 80000)
	ForeachConcurrency       int           // parallel goroutines for foreach (1-10, default 5)
	ForeachMaxDepth          int           // max nested depth for foreach (1-10, default 5)
	FetchTimeout             int           // HTTP fetch timeout in seconds (default 300)
	MCPTimeout               int           // MCP HTTP request timeout in seconds (default 60)
	AskUserTimeout           int           // ask_user prompt timeout in seconds (default 300)

	// TempIDEDir is set when running without a real .ide directory.
	// Points to a temporary directory containing .ide/ide.db.
	// When set, all DB operations use this directory instead of WorkDir/.ide.
	TempIDEDir string
}

// DBWorkDir returns the directory to pass to db.Open().
// When running with a temp DB (no real .ide), returns the temp dir.
// Otherwise returns the real WorkDir.
func (ea *ExecutorArgs) DBWorkDir() string {
	if ea.TempIDEDir != "" {
		return ea.TempIDEDir
	}
	return ea.WorkDir
}

// TurnResult holds the execution result.
type TurnResult struct {
	TurnID string `json:"turn_id"`
	A      string `json:"a"`
	Score  int    `json:"score"`
}

// ParseArgs parses command-line arguments into ExecutorArgs.
func ParseArgs(args []string) (*ExecutorArgs, error) {
	ea := &ExecutorArgs{
		OutputFormat: "stdout",
		Thinking:     true,
	}
	var hasPromptFile, hasPrompt, hasTurnID bool

	for _, arg := range args {
		if strings.HasPrefix(arg, "--work-dir=") {
			ea.WorkDir = arg[len("--work-dir="):]
		} else if strings.HasPrefix(arg, "--prompt-file=") {
			hasPromptFile = true
			ea.PromptFile = arg[len("--prompt-file="):]
		} else if strings.HasPrefix(arg, "--prompt=") {
			hasPrompt = true
			ea.Prompt = arg[len("--prompt="):]
		} else if strings.HasPrefix(arg, "--system-prompt=") {
			ea.SystemPrompt = arg[len("--system-prompt="):]
		} else if strings.HasPrefix(arg, "--system-prompt-file=") {
			ea.SystemPromptFile = arg[len("--system-prompt-file="):]
		} else if strings.HasPrefix(arg, "--session-id=") {
			ea.SessionID = arg[len("--session-id="):]
		} else if strings.HasPrefix(arg, "--turn-id=") {
			hasTurnID = true
			ea.TurnID = arg[len("--turn-id="):]
		} else if strings.HasPrefix(arg, "--reasoning=") {
			val := arg[len("--reasoning="):]
			ea.ThinkingSet = true
			ea.Thinking = val == "on"
		} else if arg == "--verbose" {
			ea.Verbose = true
		} else if strings.HasPrefix(arg, "--log-level=") {
			ea.LogLevel = arg[len("--log-level="):]
		} else if strings.HasPrefix(arg, "--pipe-path=") {
			ea.PipePath = arg[len("--pipe-path="):]
		} else if strings.HasPrefix(arg, "--pipe-addr=") {
			ea.PipeAddr = arg[len("--pipe-addr="):]
		} else if strings.HasPrefix(arg, "--parent-pipe-path=") {
			ea.ParentPipePath = arg[len("--parent-pipe-path="):]
		} else if strings.HasPrefix(arg, "--tools=") {
			ea.Tools = arg[len("--tools="):]
		} else if strings.HasPrefix(arg, "--tools-file=") {
			ea.ToolsFile = arg[len("--tools-file="):]
		} else if strings.HasPrefix(arg, "--output=") {
			ea.OutputFormat = arg[len("--output="):]
		} else if strings.HasPrefix(arg, "--llm-provider=") {
			ea.LLMProvider = arg[len("--llm-provider="):]
		} else if strings.HasPrefix(arg, "--llm-model=") {
			ea.LLMModel = arg[len("--llm-model="):]
		} else if strings.HasPrefix(arg, "--llm-api-key=") {
			ea.LLMAPIKey = arg[len("--llm-api-key="):]
		} else if strings.HasPrefix(arg, "--llm-api-url=") {
			ea.LLMAPIURL = arg[len("--llm-api-url="):]
		} else if strings.HasPrefix(arg, "--llm-config-file=") {
			ea.LLMConfigFile = arg[len("--llm-config-file="):]
		} else if strings.HasPrefix(arg, "--llm=") {
			ea.LLMName = arg[len("--llm="):]
		} else if strings.HasPrefix(arg, "--think=") {
			val := arg[len("--think="):]
			ea.ThinkingSet = true
			ea.Thinking = val == "on"
		} else if strings.HasPrefix(arg, "--effort=") {
			ea.Effort = arg[len("--effort="):]
		} else if strings.HasPrefix(arg, "--agent=") {
			ea.Agent = arg[len("--agent="):]
		} else if strings.HasPrefix(arg, "--retry-count=") {
			v := arg[len("--retry-count="):]
			ea.RetryCount, _ = strconv.Atoi(v)
		} else if strings.HasPrefix(arg, "--retry-delay=") {
			v := arg[len("--retry-delay="):]
			ea.RetryDelay, _ = strconv.Atoi(v)
		} else if strings.HasPrefix(arg, "--keep-full-turns=") {
			v := arg[len("--keep-full-turns="):]
			ea.KeepFullTurns, _ = strconv.Atoi(v)
		} else if strings.HasPrefix(arg, "--keep-simplified-turns=") {
			v := arg[len("--keep-simplified-turns="):]
			ea.KeepSimplifiedTurns, _ = strconv.Atoi(v)
		} else if strings.HasPrefix(arg, "--force-compress-threshold=") {
			v := arg[len("--force-compress-threshold="):]
			ea.ForceCompressThreshold, _ = strconv.Atoi(v)
		} else if strings.HasPrefix(arg, "--foreach-concurrency=") {
			v := arg[len("--foreach-concurrency="):]
			ea.ForeachConcurrency, _ = strconv.Atoi(v)
		} else if strings.HasPrefix(arg, "--foreach-max-depth=") {
			v := arg[len("--foreach-max-depth="):]
			ea.ForeachMaxDepth, _ = strconv.Atoi(v)
		} else if strings.HasPrefix(arg, "--fetch-timeout=") {
			v := arg[len("--fetch-timeout="):]
			ea.FetchTimeout, _ = strconv.Atoi(v)
		} else if strings.HasPrefix(arg, "--mcp-timeout=") {
			v := arg[len("--mcp-timeout="):]
			ea.MCPTimeout, _ = strconv.Atoi(v)
		} else if strings.HasPrefix(arg, "--ask-user-timeout=") {
			v := arg[len("--ask-user-timeout="):]
			ea.AskUserTimeout, _ = strconv.Atoi(v)
		}
	}

	// Resolve work directory
	if ea.WorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("--work-dir is required and cannot detect current directory: %w", err)
		}
		ea.WorkDir = wd
	}

	// --prompt-file and --prompt are mutually exclusive
	if hasPromptFile && hasPrompt {
		return nil, fmt.Errorf("--prompt-file and --prompt are mutually exclusive")
	}
	// One of --prompt-file, --prompt, --turn-id is required
	if !hasPromptFile && !hasPrompt && !hasTurnID {
		return nil, fmt.Errorf("one of --prompt-file, --prompt, or --turn-id is required")
	}
	// --session-id requires --prompt or --prompt-file
	if ea.SessionID != "" && !hasPrompt && !hasPromptFile {
		return nil, fmt.Errorf("--session-id requires --prompt or --prompt-file")
	}

	hasTurn := hasTurnID && ea.TurnID != ""
	hasSession := ea.SessionID != ""

	// Check for real .ide/ide.db on disk
	hasRealIDE := false
	idePath := filepath.Join(ea.WorkDir, ".ide", "ide.db")
	if _, err := os.Stat(idePath); err == nil {
		hasRealIDE = true
	}

	// ── Case 1: IDE resume (--turn-id=<uuid>, no prompt) ──
	if hasTurn && !hasPrompt && !hasPromptFile {
		if !hasRealIDE {
			return nil, fmt.Errorf("--turn-id requires .ide directory with ide.db")
		}
		sqlDB, err := db.Open(ea.WorkDir)
		if err != nil {
			return nil, fmt.Errorf("failed to open .ide/ide.db: %w", err)
		}
		turn, err := db.GetTurn(sqlDB, ea.TurnID)
		db.Close(sqlDB)
		if err != nil || turn == nil {
			return nil, fmt.Errorf("turn %s not found in database", ea.TurnID)
		}
		ea.SessionID = turn.SessionID
		return ea, nil
	}

	// ── Case 2: Real .ide + session/turn → use real DB ──
	if hasRealIDE && (hasSession || hasTurn) {
		// Validate session exists in DB
		if hasSession {
			sqlDB, err := db.Open(ea.WorkDir)
			if err != nil {
				return nil, fmt.Errorf("failed to open .ide/ide.db: %w", err)
			}
			session, err := db.GetSession(sqlDB, ea.SessionID)
			db.Close(sqlDB)
			if err != nil || session == nil {
				return nil, fmt.Errorf("session %s not found in database", ea.SessionID)
			}
		}
		// Sub-executor create (--turn-id + prompt): turn intentionally doesn't exist yet,
		// ensureSessionAndTurn will create it later.
		return ea, nil
	}

	// ── Case 3: No real DB needed → create temp DB ──
	if err := createTempDB(ea); err != nil {
		return nil, err
	}
	return ea, nil
}

// Run is the main entry point for the Executor process.
func Run(args []string) error {
	// Check for help flag first
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			printHelp()
			return nil
		}
	}

	ea, err := ParseArgs(args)
	if err != nil {
		return fmt.Errorf("failed to parse args: %w", err)
	}

	// Ensure we have a DB (real .ide or temp) for unified code paths.
	// The --turn-id=create and --turn-id=<uuid> modes require a real .ide
	// and are rejected earlier. For all other modes, create a temp DB if
	// no real .ide exists.
	if !hasIDEConfig(ea) {
		if err := createTempDB(ea); err != nil {
			return fmt.Errorf("failed to create temp DB: %w", err)
		}
	}
	defer cleanupTempDB(ea)

	// Initialize logger with level
	logLevel := zap.NewAtomicLevel()
	switch strings.ToLower(ea.LogLevel) {
	case "debug":
		logLevel.SetLevel(zap.DebugLevel)
	case "info":
		logLevel.SetLevel(zap.InfoLevel)
	case "warn":
		logLevel.SetLevel(zap.WarnLevel)
	default:
		logLevel.SetLevel(zap.ErrorLevel) // default: error only
	}
	loggerCfg := zap.NewDevelopmentConfig()
	loggerCfg.Level = logLevel
	logger, err := loggerCfg.Build()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	defer logger.Sync()

	logger.Info("Executor starting",
		zap.String("work_dir", ea.WorkDir),
		zap.String("turn_id", ea.TurnID),
	)

	// Override log level from DB config if available
	dbLogPath := filepath.Join(ea.DBWorkDir(), ".ide", "ide.db")
	if _, statErr := os.Stat(dbLogPath); statErr == nil {
		if sqlDB, openErr := db.Open(ea.DBWorkDir()); openErr == nil {
			if lv, getErr := db.GetConfig(sqlDB, "logLevel"); getErr == nil && lv != "" {
				switch strings.ToLower(lv) {
				case "debug":
					logLevel.SetLevel(zap.DebugLevel)
				case "info":
					logLevel.SetLevel(zap.InfoLevel)
				case "warn":
					logLevel.SetLevel(zap.WarnLevel)
				case "error":
					logLevel.SetLevel(zap.ErrorLevel)
				}
			}
			db.Close(sqlDB)
		}
	}

	// Read prompt content
	promptContent := ""
	if ea.PromptFile != "" {
		data, err := os.ReadFile(ea.PromptFile)
		if err != nil {
			return fmt.Errorf("failed to read prompt file: %w", err)
		}
		if len(data) > 0 {
			promptContent = string(data)
		}
	}
	if promptContent == "" && ea.Prompt != "" {
		promptContent = ea.Prompt
	}
	// In IDE mode (--turn-id=<uuid>), prompt comes from DB, not CLI.
	// In create/standalone modes, prompt is required and validated in ParseArgs.
	if promptContent == "" && ea.TurnID == "" {
		return fmt.Errorf("no prompt provided (checked --prompt-file, --prompt)")
	}

	// Read system prompt if file provided
	systemPrompt := ea.SystemPrompt
	if ea.SystemPromptFile != "" {
		data, err := os.ReadFile(ea.SystemPromptFile)
		if err != nil {
			return fmt.Errorf("failed to read system prompt file: %w", err)
		}
		systemPrompt = string(data)
	}

	// Load retry config from DB (CLI args take precedence)
	if ea.RetryCount == 0 && hasIDEConfig(ea) {
		if sqlDB, err := db.Open(ea.DBWorkDir()); err == nil {
			if v, err := db.GetConfig(sqlDB, "retry_count"); err == nil && v != "" {
				if n, err2 := strconv.Atoi(v); err2 == nil {
					ea.RetryCount = n
				}
			}
			if v, err := db.GetConfig(sqlDB, "retry_delay"); err == nil && v != "" {
				if n, err2 := strconv.Atoi(v); err2 == nil {
					ea.RetryDelay = n
				}
			}
			db.Close(sqlDB)
		}
	}
	if ea.RetryDelay <= 0 {
		ea.RetryDelay = 5 // default 5s
	}

	// Apply global configuration to tool handlers
	applyGlobalConfig(ea)

	// Initialize output writer
	outWriter := output.NewWriter(ea.PipePath, ea.OutputFormat, ea.PipeAddr)
	defer outWriter.Close()

	// Resolve LLM configuration from the chain
	if err := resolveLLMConfig(ea); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err.Error())
		return fmt.Errorf("LLM configuration error: %w", err)
	}

	logger.Info("LLM config resolved",
		zap.String("provider", ea.LLMProvider),
		zap.String("model", ea.LLMModel),
		zap.Bool("reasoning", ea.Thinking),
		zap.String("reasoningEffort", ea.ReasoningEffort),
	)

	// Detect running mode
	mode := detectMode(ea)

	// Print banner and runtime info to stdout so user can see it
	printBanner(ea, mode)

	// Count tools
	nBuiltin := countBuiltinTools()

	// Print runtime info to stdout in standalone mode
	isStandalone := ea.PipePath == "" && ea.OutputFormat == "stdout"
	if isStandalone {
		reasoningStr := "off"
		if ea.Thinking {
			reasoningStr = "on"
		}
		if ea.ReasoningEffort != "" {
			reasoningStr += " (" + ea.ReasoningEffort + ")"
		}
		fmt.Fprintf(os.Stdout, "# Internal Tools: %d\n", nBuiltin)
		fmt.Fprintf(os.Stdout, "# reasoning=%s\n\n", reasoningStr)
	}

	// Process the turn
	result, err := executeTurn(ea, promptContent, systemPrompt, outWriter, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err.Error())
		if !isStandalone || ea.Verbose {
			outWriter.WriteEvent("error", eventWithCtx(ea, map[string]interface{}{
				"code":    "ERR_EXECUTION_FAILED",
				"message": err.Error(),
			}))
		}
		return err
	}

	// Write result
	if ea.TurnID != "" {
		if !isStandalone || ea.Verbose {
			outWriter.WriteEvent("complete", eventWithCtx(ea, map[string]interface{}{
				"result": result.A,
				"score":  result.Score,
			}))
		}
	}

	// In non-verbose standalone mode, print a summary line with session/turn IDs
	if isStandalone && !ea.Verbose && ea.TurnID != "" {
		if ea.SessionID != "" {
			fmt.Fprintf(os.Stdout, "\nSession: %s  Turn: %s\n", ea.SessionID, ea.TurnID)
		} else {
			fmt.Fprintf(os.Stdout, "\nTurn: %s\n", ea.TurnID)
		}
	}

	logger.Info("Executor finished", zap.String("turn_id", ea.TurnID))
	return nil
}

// detectMode determines the running mode based on .ide existence and args.
func detectMode(ea *ExecutorArgs) string {
	hasRealIDE := false
	idePath := filepath.Join(ea.WorkDir, ".ide", "ide.db")
	if _, err := os.Stat(idePath); err == nil {
		hasRealIDE = true
	}
	// Temp DB makes it effectively an "IDE" mode
	hasIDE := hasRealIDE || ea.TempIDEDir != ""

	switch {
	case hasIDE && ea.TurnID != "" && ea.SessionID != "":
		return "Session"
	case hasIDE && ea.SessionID != "":
		return "Session"
	default:
		return "Standalone"
	}
}

// printBanner prints the startup banner to stdout.
func printBanner(ea *ExecutorArgs, mode string) {
	fmt.Fprintf(os.Stdout, "肥猫启动中... V1.0 - %s 模式\n", mode)
	fmt.Fprintf(os.Stdout, "LLM: %s/%s\n", ea.LLMProvider, ea.LLMModel)
	reasoningStr := "off"
	if ea.Thinking {
		reasoningStr = "on"
	}
	if ea.ReasoningEffort != "" {
		reasoningStr += " (" + ea.ReasoningEffort + ")"
	}
	fmt.Fprintf(os.Stdout, "推理=%s\n\n", reasoningStr)
}

// applyGlobalConfig applies global configuration from ExecutorArgs to tool handlers.
func applyGlobalConfig(ea *ExecutorArgs) {
	if ea.ForeachConcurrency > 0 {
		call_llm.SetConcurrency(ea.ForeachConcurrency)
	}
	if ea.ForeachMaxDepth > 0 {
		call_llm.SetMaxDepth(ea.ForeachMaxDepth)
	}
	if ea.FetchTimeout > 0 {
		toolhandler.SetFetchTimeout(ea.FetchTimeout)
	}
	if ea.MCPTimeout > 0 {
		toolhandler.SetMCPTimeout(ea.MCPTimeout)
		tool.SetMCPTimeout(ea.MCPTimeout)
	}
	if ea.AskUserTimeout > 0 {
		call_llm.SetAskUserTimeout(ea.AskUserTimeout)
	}
}

// resolveMaxTokens returns the effective max output tokens from ExecutorArgs.
func resolveMaxTokens(ea *ExecutorArgs) int {
	if ea.LLMMaxTokens > 0 {
		return ea.LLMMaxTokens
	}
	return 65535 // default fallback
}

// printHelp prints the help message to stdout and exits.
func printHelp() {
	fmt.Fprint(os.Stdout, `chonkpilot-executor - 执行一个 LLM 轮次

运行模式（--prompt、--prompt-file、--turn-id 三选一）：
  --turn-id=<uuid>                IDE 模式：使用已有 turn（从 DB 加载 user message）
  --session-id=<id> --prompt=...  延续会话：加载历史，自动创建新 turn
  --prompt=...                    一次性会话：无 .ide 时创建临时 DB，有 .ide 时不污染项目 DB

参数：
  --work-dir=<path>               工作目录（默认当前目录）
  --prompt=<string>               用户提问内容
  --prompt-file=<path>            用户提问文件（与 --prompt 二选一）
  --turn-id=<id>                  轮次 ID（IDE 模式传入已有 uuid）
  --session-id=<id>               会话 ID（延续会话时使用）
  --system-prompt=<string>        系统提示词
  --system-prompt-file=<path>     系统提示词文件
  --tools=<json>                  自定义工具 JSON
  --tools-file=<path>             自定义工具文件
  --output=<format>               输出方式（stdout/json，默认 stdout）
  --pipe-path=<path>              命名管道路径
  --pipe-addr=<path>              子进程事件管道地址
  --reasoning=<on|off>            思考链（默认 on）
  --think=<on|off>                思考链（--reasoning 别名）
  --effort=<high|max>             推理强度（默认 high）
  --llm=<name>                    按名称选择 LLM 提供商（覆盖默认配置）
  --verbose                       详细输出模式
  --log-level=<level>             日志级别（debug/info/warn/error，默认 error）
  --llm-config-file=<path>        LLM 配置文件
  --llm-provider=<name>           LLM 提供商
  --llm-model=<model>             模型名称
  --llm-api-key=<key>             API Key
  --llm-api-url=<url>             API Endpoint
  --retry-count=<n>               LLM 调用失败重试次数（默认 0 = 不重试）
  --retry-delay=<s>               重试间隔秒数（默认 5）
  -h, --help                      显示此帮助

详情见 executor.md
`)
}

// countBuiltinTools returns the count of builtin tools.
func countBuiltinTools() int {
	d := discover.NewDiscoverer()
	return len(d.ListBuiltinTools())
}

// truncateStr truncates a string to max runes, appending "..." if truncated.
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

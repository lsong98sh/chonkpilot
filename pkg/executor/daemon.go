package executor

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/pkg/executor/output"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/browser"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// DaemonCommand is a command sent from IDE to the daemon executor via stdin.
type DaemonCommand struct {
	Cmd       string   `json:"cmd"`       // start_turn | cancel_session | shutdown | tool_result
	SessionID string   `json:"session_id,omitempty"`
	TurnID    string   `json:"turn_id,omitempty"`
	UUID      string   `json:"uuid,omitempty"`      // for tool_result routing
	Status    string   `json:"status,omitempty"`     // for tool_result
	Tool      string   `json:"tool,omitempty"`       // for tool_result
	LLM       string   `json:"llm,omitempty"`
	Think     string   `json:"think,omitempty"`
	Effort    string   `json:"effort,omitempty"`
	ExtraArgs []string `json:"extra_args,omitempty"`
}

// SessionWorker manages a single session's state across multiple turns.
// Each worker runs in its own goroutine and holds a persistent BrowserManager.
type SessionWorker struct {
	sessionID  string
	browserMgr *browser.BrowserManager
	cancelCtx  context.Context
	cancelFn   context.CancelFunc
	cmdChan    chan DaemonCommand
	done       chan struct{}
}

// Daemon is the long-lived executor that processes turns for multiple sessions.
type Daemon struct {
	workDir   string
	dbDir     string
	logger    *zap.Logger
	outWriter *output.Writer
	sqlDB     *sql.DB // shared DB connection for all workers
	eaBase    *ExecutorArgs // base args parsed from CLI flags

	mu          sync.Mutex
	workers     map[string]*SessionWorker          // sessionID → worker
	toolWaiters map[string]chan *types.ToolResult  // UUID → channel for tool_result routing
}

// RunDaemon starts the daemon loop.
// Reads JSON commands from stdin until "shutdown" is received.
func RunDaemon(args []string) error {
	// ── Parse base args (work-dir, output, log-level, etc.) ──
	ea, err := parseDaemonArgs(args)
	if err != nil {
		return fmt.Errorf("invalid daemon args: %w", err)
	}

	// ── Initialize logger ──
	logLevel := resolveLogLevel(ea.LogLevel)
	loggerCfg := zap.NewDevelopmentConfig()
	loggerCfg.Level = logLevel
	logger, err := loggerCfg.Build()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	defer logger.Sync()

	// ── Validate workDir ──
	if ea.WorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("work-dir is required")
		}
		ea.WorkDir = wd
	}

	logger.Info("Daemon starting",
		zap.String("work_dir", ea.WorkDir),
	)

	// ── Apply global config ──
	applyGlobalConfig(ea)

	// ── Initialize output writer (stdout → IDE) ──
	outWriter := output.NewWriter(ea.PipePath, ea.OutputFormat, ea.PipeAddr)
	defer outWriter.Close()

	// ── Open shared DB connection ──
	// Use DBWorkDir(): if TempIDEDir is set, use that; otherwise use WorkDir.
	sqlDB, err := openSharedDB(ea)
	if err != nil {
		logger.Warn("shared DB open failed, some features may be limited", zap.Error(err))
	}
	if sqlDB != nil {
		// Startup: clear stale compress locks from previous daemon crashes.
		_ = db.DeleteConfigLike(sqlDB, "compress_lock:%")
		defer sqlDB.Close()
	}

	// ── Create daemon ──
	d := &Daemon{
		workDir:   ea.WorkDir,
		dbDir:     ea.DBWorkDir(),
		logger:    logger,
		outWriter: outWriter,
		sqlDB:     sqlDB,
		eaBase:    ea,
		workers:   make(map[string]*SessionWorker),
	}

	// Write daemon_ready event
	outWriter.WriteEvent("daemon_ready", map[string]interface{}{
		"work_dir": ea.WorkDir,
	})

	// ── Main loop: read commands from stdin ──
	logger.Info("Daemon entering main loop (reading stdin)")
	scanner := bufio.NewScanner(os.Stdin)
	// 10MB buffer for long JSON result lines
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var cmd DaemonCommand
		if err := json.Unmarshal([]byte(line), &cmd); err != nil {
			logger.Warn("daemon: invalid command JSON", zap.String("line", truncateStr(line, 200)), zap.Error(err))
			continue
		}

		switch cmd.Cmd {
		case "start_turn":
			d.handleStartTurn(cmd, outWriter, logger)
		case "cancel_session":
			d.handleCancelSession(cmd, logger)
		case "tool_result":
			d.handleToolResult(cmd)
		case "shutdown":
			logger.Info("Daemon shutting down")
			d.shutdownAll()
			return nil
		default:
			logger.Warn("daemon: unknown command", zap.String("cmd", cmd.Cmd))
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Error("daemon stdin scanner error", zap.Error(err))
		return fmt.Errorf("daemon stdin error: %w", err)
	}

	return nil
}

// handleStartTurn handles a start_turn command.
func (d *Daemon) handleStartTurn(cmd DaemonCommand, outWriter *output.Writer, logger *zap.Logger) {
	if cmd.SessionID == "" || cmd.TurnID == "" {
		logger.Warn("daemon: start_turn missing session_id or turn_id", zap.Any("cmd", cmd))
		outWriter.WriteEvent("error", map[string]interface{}{
			"code":    "ERR_INVALID_COMMAND",
			"message": "start_turn requires session_id and turn_id",
		})
		return
	}

	// Get or create worker for this session
	worker := d.getOrCreateWorker(cmd.SessionID, logger)
	if worker == nil {
		outWriter.WriteEvent("error", map[string]interface{}{
			"session_id": cmd.SessionID,
			"code":       "ERR_WORKER_FAILED",
			"message":    "failed to create session worker",
		})
		return
	}

	// Enqueue the turn
	select {
	case worker.cmdChan <- cmd:
		logger.Debug("daemon: enqueued turn",
			zap.String("session_id", cmd.SessionID),
			zap.String("turn_id", cmd.TurnID),
		)
	default:
		logger.Warn("daemon: worker command channel full, dropping turn",
			zap.String("session_id", cmd.SessionID),
			zap.String("turn_id", cmd.TurnID),
		)
		outWriter.WriteEvent("error", map[string]interface{}{
			"session_id": cmd.SessionID,
			"turn_id":    cmd.TurnID,
			"code":       "ERR_WORKER_BUSY",
			"message":    "session worker is busy",
		})
	}
}

// handleCancelSession handles a cancel_session command.
func (d *Daemon) handleCancelSession(cmd DaemonCommand, logger *zap.Logger) {
	if cmd.SessionID == "" {
		logger.Warn("daemon: cancel_session missing session_id")
		return
	}

	d.mu.Lock()
	worker, ok := d.workers[cmd.SessionID]
	if ok {
		delete(d.workers, cmd.SessionID)
	}
	d.mu.Unlock()

	if !ok {
		logger.Debug("daemon: cancel_session for unknown session",
			zap.String("session_id", cmd.SessionID))
		return
	}

	logger.Info("daemon: cancelling session",
		zap.String("session_id", cmd.SessionID))
	worker.cancelFn()
	<-worker.done // Wait for cleanup
}

// getOrCreateWorker returns an existing SessionWorker or creates a new one.
func (d *Daemon) getOrCreateWorker(sessionID string, logger *zap.Logger) *SessionWorker {
	d.mu.Lock()
	defer d.mu.Unlock()

	if w, ok := d.workers[sessionID]; ok {
		return w
	}

	ctx, cancel := context.WithCancel(context.Background())
	bm := browser.NewBrowserManager()

	worker := &SessionWorker{
		sessionID:  sessionID,
		browserMgr: bm,
		cancelCtx:  ctx,
		cancelFn:   cancel,
		cmdChan:    make(chan DaemonCommand, 10), // buffer up to 10 turns
		done:       make(chan struct{}),
	}
	d.workers[sessionID] = worker

	go worker.loop(d, logger)

	logger.Info("daemon: created session worker",
		zap.String("session_id", sessionID))
	return worker
}

// shutdownAll cancels all workers and waits for them to finish.
func (d *Daemon) shutdownAll() {
	d.mu.Lock()
	workers := make([]*SessionWorker, 0, len(d.workers))
	for _, w := range d.workers {
		workers = append(workers, w)
	}
	d.workers = make(map[string]*SessionWorker)
	d.mu.Unlock()

	for _, w := range workers {
		w.cancelFn()
		<-w.done
	}
}

// loop is the main goroutine for a SessionWorker.
// It processes turns from cmdChan sequentially.
func (w *SessionWorker) loop(d *Daemon, logger *zap.Logger) {
	defer close(w.done)
	defer func() {
		logger.Info("daemon: session worker exited",
			zap.String("session_id", w.sessionID))
	}()

	for {
		select {
		case <-w.cancelCtx.Done():
			// Cleanup browser
			d.logger.Info("daemon: cleaning up browser for cancelled session",
				zap.String("session_id", w.sessionID))
			return

		case cmd, ok := <-w.cmdChan:
			if !ok {
				return
			}
			w.processTurn(d, cmd, logger)
		}
	}
}

// processTurn handles a single turn command.
func (w *SessionWorker) processTurn(d *Daemon, cmd DaemonCommand, logger *zap.Logger) {
	start := time.Now()
	logger.Info("daemon: processing turn",
		zap.String("session_id", cmd.SessionID),
		zap.String("turn_id", cmd.TurnID),
	)

	// ── Build ExecutorArgs from base + command ──
	ea := buildTurnArgs(d.eaBase, cmd)

	// Write turn_start event
	d.outWriter.WriteEvent("turn_start", map[string]interface{}{
		"session_id": cmd.SessionID,
		"turn_id":    cmd.TurnID,
	})

	// ── Execute the turn with shared DB and existing BrowserManager ──
	// Completion/error events are emitted inside executeTurnWith.
	waitFn := func(uuid string) *types.ToolResult {
		return d.waitToolResult(w.cancelCtx, uuid)
	}
	result, err := executeTurnWith(ea, "", "", d.outWriter, logger, d.sqlDB, w.browserMgr, w.cancelCtx, waitFn)

	elapsed := time.Since(start)
	if err != nil {
		logger.Error("daemon: turn failed",
			zap.String("session_id", cmd.SessionID),
			zap.String("turn_id", cmd.TurnID),
			zap.Error(err),
			zap.Duration("elapsed", elapsed),
		)
	}

	logger.Info("daemon: turn finished",
		zap.String("session_id", cmd.SessionID),
		zap.String("turn_id", cmd.TurnID),
		zap.Duration("elapsed", elapsed),
		zap.Bool("has_result", result != nil),
	)
}

// ─── Tool result routing ─────────────────────────────

// waitToolResult registers a UUID and blocks until the IDE sends a tool_result
// command via stdin with the matching UUID. Returns the tool result.
// If ctx is cancelled (e.g. by CancelSession), returns a cancelled result.
// Designed to be passed to the tool handler as WaitToolResult callback.
func (d *Daemon) waitToolResult(ctx context.Context, uuid string) *types.ToolResult {
	ch := make(chan *types.ToolResult, 1)
	d.mu.Lock()
	if d.toolWaiters == nil {
		d.toolWaiters = make(map[string]chan *types.ToolResult)
	}
	d.toolWaiters[uuid] = ch
	d.mu.Unlock()

	// Block until resolved or cancelled
	select {
	case result := <-ch:
		return result
	case <-ctx.Done():
		// Clean up waiter entry if not yet resolved
		d.mu.Lock()
		delete(d.toolWaiters, uuid)
		d.mu.Unlock()
		return &types.ToolResult{
			Success: false,
			Error:   "file tree capture cancelled",
			Tool:    "",
		}
	}
}

// handleToolResult handles a tool_result command from IDE.
// Routes the result to the goroutine waiting for the UUID.
func (d *Daemon) handleToolResult(cmd DaemonCommand) {
	d.mu.Lock()
	ch, ok := d.toolWaiters[cmd.UUID]
	if ok {
		delete(d.toolWaiters, cmd.UUID)
	}
	d.mu.Unlock()

	if !ok {
		d.logger.Warn("tool_result: unknown UUID", zap.String("uuid", cmd.UUID))
		return
	}

	success := cmd.Status == "completed" || cmd.Status == "done"
	ch <- &types.ToolResult{
		Success: success,
		Output:  cmd.Status,
		Tool:    cmd.Tool,
	}
}

// ─── Helpers ───────────────────────────────────────────────

// parseDaemonArgs parses CLI args for daemon mode.
// Only a subset of flags are relevant: work-dir, output, log-level, and config overrides.
func parseDaemonArgs(args []string) (*ExecutorArgs, error) {
	ea := &ExecutorArgs{
		OutputFormat: "json",
	}

	for _, arg := range args {
		if arg == "--internal" {
			continue
		}
		if strings.HasPrefix(arg, "--work-dir=") {
			ea.WorkDir = arg[len("--work-dir="):]
		} else if strings.HasPrefix(arg, "--output=") {
			ea.OutputFormat = arg[len("--output="):]
		} else if strings.HasPrefix(arg, "--log-level=") {
			ea.LogLevel = arg[len("--log-level="):]
		} else if strings.HasPrefix(arg, "--pipe-path=") {
			ea.PipePath = arg[len("--pipe-path="):]
		} else if strings.HasPrefix(arg, "--pipe-addr=") {
			ea.PipeAddr = arg[len("--pipe-addr="):]
		} else if strings.HasPrefix(arg, "--llm=") {
			ea.LLMName = arg[len("--llm="):]
		} else if strings.HasPrefix(arg, "--effort=") {
			ea.Effort = arg[len("--effort="):]
		} else if strings.HasPrefix(arg, "--think=") {
			val := arg[len("--think="):]
			ea.ThinkingSet = true
			ea.Thinking = val == "on"
		} else if strings.HasPrefix(arg, "--reasoning=") {
			val := arg[len("--reasoning="):]
			ea.ThinkingSet = true
			ea.Thinking = val == "on"
		} else if strings.HasPrefix(arg, "--retry-count=") {
			v := arg[len("--retry-count="):]
			ea.RetryCount, _ = strconvAtoi(v)
		} else if strings.HasPrefix(arg, "--retry-delay=") {
			v := arg[len("--retry-delay="):]
			ea.RetryDelay, _ = strconvAtoi(v)
		} else if strings.HasPrefix(arg, "--keep-full-turns=") {
			v := arg[len("--keep-full-turns="):]
			ea.KeepFullTurns, _ = strconvAtoi(v)
		} else if strings.HasPrefix(arg, "--compress-token-threshold=") {
			v := arg[len("--compress-token-threshold="):]
			ea.CompressTokenThreshold, _ = strconvAtoi(v)
		}
		// ignore prompt/turn-id/session-id — those come from stdin commands
	}

	return ea, nil
}

// buildTurnArgs merges daemon base args with a specific turn command.
func buildTurnArgs(base *ExecutorArgs, cmd DaemonCommand) *ExecutorArgs {
	ea := &ExecutorArgs{
		WorkDir:               base.WorkDir,
		TempIDEDir:            base.TempIDEDir,
		OutputFormat:          base.OutputFormat,
		PipePath:              base.PipePath,
		PipeAddr:              base.PipeAddr,
		LogLevel:              base.LogLevel,
		LLMName:               base.LLMName,
		Effort:                base.Effort,
		RetryCount:            base.RetryCount,
		RetryDelay:            base.RetryDelay,
		KeepFullTurns:          base.KeepFullTurns,
		CompressTokenThreshold: base.CompressTokenThreshold,
		SessionID:             cmd.SessionID,
		TurnID:                cmd.TurnID,
		ThinkingSet:           base.ThinkingSet,
		Thinking:              base.Thinking,
		ForeachConcurrency:    base.ForeachConcurrency,
		ForeachMaxDepth:       base.ForeachMaxDepth,
		FetchTimeout:          base.FetchTimeout,
		MCPTimeout:            base.MCPTimeout,
		AskUserTimeout:        base.AskUserTimeout,
		LLMMaxTokens:          base.LLMMaxTokens,
		LLMTemperature:        base.LLMTemperature,
		LLMMaxToolIterations:  base.LLMMaxToolIterations,
		LLMResponseTimeout:    base.LLMResponseTimeout,
		LLMStreamTimeout:      base.LLMStreamTimeout,
	}

	// Apply turn-level overrides from command
	if cmd.LLM != "" {
		ea.LLMName = cmd.LLM
	}
	if cmd.Think != "" {
		ea.Thinking = cmd.Think == "on"
		ea.ThinkingSet = true
	}
	if cmd.Effort != "" {
		ea.Effort = cmd.Effort
		ea.ReasoningEffort = cmd.Effort
	}

	return ea
}

// openSharedDB opens a shared DB connection for the daemon.
// Sets SetMaxOpenConns(1) for safe concurrent access.
func openSharedDB(ea *ExecutorArgs) (*sql.DB, error) {
	dbDir := ea.DBWorkDir()
	if dbDir == "" {
		return nil, nil
	}
	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return nil, fmt.Errorf("open shared DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	return sqlDB, nil
}

// resolveLogLevel converts a string log level to zap.AtomicLevel.
func resolveLogLevel(level string) zap.AtomicLevel {
	lvl := zap.NewAtomicLevel()
	switch strings.ToLower(level) {
	case "debug":
		lvl.SetLevel(zap.DebugLevel)
	case "info":
		lvl.SetLevel(zap.InfoLevel)
	case "warn":
		lvl.SetLevel(zap.WarnLevel)
	default:
		lvl.SetLevel(zap.ErrorLevel)
	}
	return lvl
}

// strconvAtoi is a wrapper for strconv.Atoi to avoid import in this file.
// Used by parseDaemonArgs.
func strconvAtoi(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

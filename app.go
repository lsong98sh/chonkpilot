//go:build windows
// +build windows

package main

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/embed"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/chrome"
	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/prompts"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
	"github.com/chonkpilot/chonkpilot/pkg/ide/config"
	"github.com/chonkpilot/chonkpilot/pkg/ide/process"
	"github.com/chonkpilot/chonkpilot/pkg/ide/recent"
	"github.com/chonkpilot/chonkpilot/pkg/ide/watcher"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// App is the main application struct for Wails bindings.
type App struct {
	ctx                context.Context
	workDir            string
	logger             *zap.Logger
	cfg                *config.ConfigManager
	userCfg            *config.UserConfigManager
	recentMgr          *recent.Manager
	em                 *process.ExecutorManager
	fw                 *watcher.FileWatcher
	codebaseIdxer      *codeindex.Indexer
	muSubscribers      sync.RWMutex
	sessionSubscribers map[string]bool // session_id → active subscriber
	codebasePollerStop chan struct{}    // stop signal for codebase status poller

	// pendingCancels tracks turnIDs whose StartChat goroutine should abort
	// before calling StartExecutor. Guards the race between CancelChat and
	// the async goroutine in StartChat.
	pendingCancels sync.Map // map[string]chan struct{}

	// optimizeMu serializes OptimizeAgentPrompt calls so only one runs at a time.
	optimizeMu sync.Mutex
}

// NewApp creates a new App.
func NewApp(workDir string, logger *zap.Logger, userCfg *config.UserConfigManager, recentMgr *recent.Manager, cfg *config.ConfigManager) *App {
	a := &App{
		workDir:            workDir,
		logger:             logger,
		cfg:                cfg,
		userCfg:            userCfg,
		recentMgr:          recentMgr,
		sessionSubscribers: make(map[string]bool),
		codebasePollerStop: make(chan struct{}),
	}
	a.em = process.NewExecutorManager(workDir, logger)
	a.fw = watcher.NewFileWatcher(workDir, logger)
	if err := a.fw.Start(); err != nil {
		logger.Warn("file watcher start failed", zap.Error(err))
	}
	a.em.SetOnEvent(func(eventType string, payload map[string]interface{}) {
		a.onExecutorEvent(eventType, payload)
	})
	return a
}

// startup is called by Wails when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.fw.SetPusher(&wailsEventPusher{ctx: ctx})
	go a.autoScanCodebase()
	go a.pollCodebaseStatus()

	// Seed default agents from embedded resources into project_agents table
	db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		if err := prompts.SeedAgents(sqlDB); err != nil {
			a.logger.Warn("failed to seed default agents", zap.Error(err))
		}
		return nil
	})

	// Seed default scenario from embedded system_scenario.txt into scenario.db if empty
	if _, err := a.GetScenarioList(); err == nil {
		sdb, err := db.OpenScenarioDB()
		if err == nil {
			scenarios, err := db.GetAllScenarios(sdb)
			if err == nil && len(scenarios) == 0 {
				defaultSc := &models.ScenarioConfig{
					Name:         "默认场景",
					Description:  "默认的场景配置",
					SystemPrompt: prompts.DefaultScenarioPrompt,
				}
				if err := db.SaveScenario(sdb, defaultSc); err != nil {
					a.logger.Warn("failed to seed default scenario", zap.Error(err))
				} else {
					a.logger.Info("seeded default scenario from system_scenario.txt")
					// Auto-select the first scenario as active
					_ = db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
						return db.SetConfig(sqlDB, "scenario_system_prompt", prompts.DefaultScenarioPrompt)
					})
				}
			}
			sdb.Close()
		}
	}

	// Extract embedded executor.exe to ~/.chonkpilot/bin/ if IDE is newer
	homeDir, _ := os.UserHomeDir() //nolint:errcheck
	if homeDir != "" {
		exeDir := filepath.Join(homeDir, ".chonkpilot", "bin")
		exePath := filepath.Join(exeDir, "executor.exe")

		// Get IDE's own mod time as version anchor
		ideExe, err := os.Executable()
		var ideModTime time.Time
		if err == nil {
			if fi, err := os.Stat(ideExe); err == nil {
				ideModTime = fi.ModTime()
			}
		}

		needExtract := false
		if fi, err := os.Stat(exePath); os.IsNotExist(err) {
			needExtract = true
		} else if err == nil && !ideModTime.IsZero() && fi.ModTime().Before(ideModTime) {
			// Existing executor is older than IDE → re-extract
			needExtract = true
			a.logger.Info("executor is stale, re-extracting",
				zap.Time("executor_mtime", fi.ModTime()),
				zap.Time("ide_mtime", ideModTime))
		}

		if needExtract {
			if p, err := embed.ExtractExecutor(exeDir); err != nil {
				a.logger.Warn("failed to extract embedded executor", zap.Error(err))
			} else {
				a.logger.Info("extracted embedded executor", zap.String("path", p))
				// Sync extracted executor's timestamp with IDE so we don't re-extract next time
				if !ideModTime.IsZero() {
					os.Chtimes(exePath, ideModTime, ideModTime)
				}
			}
		}
	}

	// Auto-detect Java/Python/Node.js environments
	a.detectRuntimeEnvironments()

	// Start executor daemon for long-lived browser sessions
	if err := a.em.StartDaemon(a.workDir); err != nil {
		a.logger.Warn("failed to start executor daemon", zap.Error(err))
	}

	runtime.LogInfo(a.ctx, "ChonkPilot IDE running")
}

// autoScanCodebase checks if codebase indexing is enabled and queue is empty,
// then triggers a full project scan and begins processing in the background.
func (a *App) autoScanCodebase() {
	// Already running, don't start another worker.
	if a.codebaseIdxer != nil {
		a.logger.Info("autoScanCodebase: worker already running")
		return
	}

	var enabled string
	db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		if err := sqlDB.QueryRow(`SELECT value FROM config WHERE key='codebase_index.enabled'`).Scan(&enabled); err != nil {
			a.logger.Warn("autoScanCodebase: cannot read codebase_index.enabled", zap.Error(err))
		}
		return nil
	})
	if enabled != "true" {
		return
	}

	var extensions string
	db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		if err := sqlDB.QueryRow(`SELECT value FROM config WHERE key='codebase_index.extensions'`).Scan(&extensions); err != nil {
			a.logger.Warn("autoScanCodebase: cannot read codebase_index.extensions", zap.Error(err))
		}
		return nil
	})
	if extensions == "" {
		extensions = ".go,.js,.ts,.jsx,.tsx,.vue,.py,.rs,.java,.c,.cpp,.h,.hpp,.cs,.rb,.php,.swift,.kt"
	}
	extList := strings.Split(extensions, ",")

	codebaseDB, err := codeindex.OpenCodebaseDB(a.workDir)
	if err != nil {
		a.logger.Warn("autoScanCodebase: cannot open codebase.db", zap.Error(err))
		return
	}

	var pending, indexing int
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='pending'`).Scan(&pending)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='indexing'`).Scan(&indexing)

	// Reset stale indexing records (from a previous crash/force-kill) back to pending
	if indexing > 0 {
		resetCount := indexing
		_, _ = codebaseDB.Exec(`UPDATE files SET status='pending', updated_at=datetime('now','localtime') WHERE status='indexing'`)
		pending += resetCount
		indexing = 0
		a.logger.Info("autoScanCodebase: reset stale indexing records to pending",
			zap.Int("reset", resetCount))
	}

	// No worker running. If queue is empty, do a full scan to enqueue files.
	if pending == 0 {
		scanner := codeindex.NewScanner(codebaseDB, a.workDir, extList, a.logger)
		a.logger.Info("autoScanCodebase: starting project scan")
		if err := scanner.ScanProject(); err != nil {
			a.logger.Warn("autoScanCodebase: scan failed", zap.Error(err))
			codebaseDB.Close()
			return
		}
		a.logger.Info("autoScanCodebase: scan complete")
	}
	codebaseDB.Close()

	// Start the queue worker to process pending items (may be from a previous run
	// where the worker didn't start, e.g. before the sqlite driver fix).
	procsDB, err := codeindex.OpenCodebaseDB(a.workDir)
	if err != nil {
		a.logger.Warn("autoScanCodebase: cannot open processor DB", zap.Error(err))
		return
	}

	if a.userCfg == nil {
		a.logger.Warn("autoScanCodebase: user config not available")
		procsDB.Close()
		return
	}
	cfg := a.userCfg.Get()
	if len(cfg.LLMs) == 0 {
		a.logger.Warn("autoScanCodebase: no LLM configured, cannot process queue")
		procsDB.Close()
		return
	}
	llmCfg := cfg.LLMs[cfg.DefaultLLM]

	client := llm.NewClient(llmCfg.Protocol, llmCfg.Model, llmCfg.APIKey, llmCfg.BaseURL, a.logger)
	caller := func(systemPrompt, userPrompt string) (string, error) {
		ch, err := client.Chat([]llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		}, llm.ChatOptions{
			Model:       llmCfg.Model,
			Temperature: 0.1,
			MaxTokens:   2048,
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

	idxer := codeindex.NewIndexer(procsDB, a.workDir, extList, caller, a.logger)
	idxer.Start()
	a.codebaseIdxer = idxer
	a.logger.Info("autoScanCodebase: queue processor started, processing pending items...")

	// Push initial status so frontend doesn't show all zeros
	a.pushCodebaseStatus()
}

// detectRuntimeEnvironments auto-detects Java/Python/Node.js paths and persists to global config.
func (a *App) detectRuntimeEnvironments() {
	if a.userCfg == nil {
		return
	}
	cfg := a.userCfg.Get()
	changed := false

	paths := map[string]*struct {
		field *string
		names []string // executable names to try
	}{
		"Java":   {field: &cfg.JavaPath, names: []string{"java.exe", "java"}},
		"Python": {field: &cfg.PythonPath, names: []string{"python.exe", "python3.exe", "python", "python3"}},
		"Node":   {field: &cfg.NodePath, names: []string{"node.exe", "node"}},
	}

	for label, p := range paths {
		if *p.field != "" {
			continue // already configured
		}
		found := ""
		for _, name := range p.names {
			if path, err := exec.LookPath(name); err == nil {
				found = path
				break
			}
		}
		if found == "" {
			// Fallback: check common install directories
			found = findCommonPath(label)
		}
		if found != "" {
			*p.field = found
			changed = true
			a.logger.Info("auto-detected runtime environment", zap.String("runtime", label), zap.String("path", found))
		} else {
			a.logger.Debug("runtime environment not found", zap.String("runtime", label))
		}
	}

	if changed {
		_ = a.userCfg.Update(cfg)
	}
}

// findCommonPath checks well-known install directories for the given runtime.
func findCommonPath(runtime string) string {
	// Common Windows install directories
	progFiles := []string{
		"C:\\Program Files",
		"C:\\Program Files (x86)",
		os.Getenv("LOCALAPPDATA") + "\\Programs",
		os.Getenv("USERPROFILE") + "\\scoop\\apps",
		os.Getenv("USERPROFILE") + "\\AppData\\Local\\Programs",
	}
	home := os.Getenv("USERPROFILE")

	switch runtime {
	case "Java":
		// Check JAVA_HOME
		if jh := os.Getenv("JAVA_HOME"); jh != "" {
			if _, err := os.Stat(filepath.Join(jh, "bin", "java.exe")); err == nil {
				return filepath.Join(jh, "bin", "java.exe")
			}
		}
		for _, dir := range progFiles {
			pattern := filepath.Join(dir, "Java", "jdk*", "bin", "java.exe")
			if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
				return matches[0]
			}
			pattern = filepath.Join(dir, "Java", "jre*", "bin", "java.exe")
			if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
				return matches[0]
			}
			pattern = filepath.Join(dir, "Eclipse", "Adoptium", "jdk*", "bin", "java.exe")
			if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
				return matches[0]
			}
		}
	case "Python":
		for _, dir := range progFiles {
			pattern := filepath.Join(dir, "Python", "Python*", "python.exe")
			if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
				return matches[0]
			}
			pattern = filepath.Join(dir, "Python", "python.exe")
			if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
				return matches[0]
			}
		}
		// Check Windows Store Python
		if home != "" {
			storePath := filepath.Join(home, "AppData", "Local", "Microsoft", "WindowsApps", "python.exe")
			if _, err := os.Stat(storePath); err == nil {
				return storePath
			}
		}
	case "Node":
		for _, dir := range progFiles {
			pattern := filepath.Join(dir, "nodejs", "node.exe")
			if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
				return matches[0]
			}
		}
		// Check nvm-windows
		if home != "" {
			nvmPath := filepath.Join(home, "AppData", "Roaming", "nvm", "*", "node.exe")
			if matches, _ := filepath.Glob(nvmPath); len(matches) > 0 {
				return matches[0]
			}
		}
	}
	return ""
}

// GetChromeStatus returns the current Chrome discovery status.
func (a *App) GetChromeStatus() map[string]interface{} {
	if a.userCfg == nil {
		return map[string]interface{}{"ok": false, "path": ""}
	}
	cfg := a.userCfg.Get()
	if cfg.ChromePath != "" && chrome.Verify(cfg.ChromePath) {
		return map[string]interface{}{"ok": true, "path": cfg.ChromePath}
	}
	a.logger.Info("Chrome path invalid/missing, attempting re-discovery")
	result := chrome.Discover()
	if result.Ok {
		cfg.ChromePath = result.Path
		_ = a.userCfg.Update(cfg)
		a.logger.Info("Chrome re-discovered", zap.String("path", result.Path))
		return map[string]interface{}{"ok": true, "path": result.Path}
	}
	if cfg.ChromePath != "" {
		cfg.ChromePath = ""
		_ = a.userCfg.Update(cfg)
	}
	return map[string]interface{}{"ok": false, "path": ""}
}

// StartCodebaseIndex triggers a full project scan and starts the queue processor.
// If the worker is already running, it wakes it up to re-check the queue.
func (a *App) StartCodebaseIndex() error {
	a.logger.Info("StartCodebaseIndex: manually triggering codebase scan")
	if a.codebaseIdxer != nil {
		a.codebaseIdxer.Wakeup()
		return nil
	}
	go a.autoScanCodebase()
	return nil
}

// GetCodebaseIndexStatus returns the codebase index queue status.
func (a *App) GetCodebaseIndexStatus() map[string]interface{} {
	codebaseDB, err := codeindex.OpenCodebaseDB(a.workDir)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error(), "pending": 0, "indexing": 0, "failed": 0}
	}
	defer codebaseDB.Close()

	var fileCount, symbolCount int
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='done'`).Scan(&fileCount)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM symbols`).Scan(&symbolCount)

	var pending, indexing, failed, failedExhausted, totalFiles int
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='pending'`).Scan(&pending)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='indexing'`).Scan(&indexing)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='failed' AND retry_count < 3`).Scan(&failed)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='failed' AND retry_count >= 3`).Scan(&failedExhausted)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&totalFiles)

	total := pending + indexing + failed + failedExhausted
	return map[string]interface{}{
		"ok":       total == 0,
		"files":    fileCount,
		"symbols":  symbolCount,
		"totalFiles": totalFiles,
		"pending":  pending,
		"indexing": indexing,
		"failed":   failed,
		"failed_exhausted": failedExhausted,
	}
}

// ClearCodebaseIndex clears all codebase index data and queue entries.
func (a *App) ClearCodebaseIndex() error {
	codebaseDB, err := codeindex.OpenCodebaseDB(a.workDir)
	if err != nil {
		return err
	}
	defer codebaseDB.Close()
	return codeindex.ClearAll(codebaseDB)
}

// ReindexCodebase clears all index data and triggers a full project re-scan.
// Returns the number of files enqueued, or 0 if codebase index is disabled.
func (a *App) ReindexCodebase() (int, error) {
	codebaseDB, err := codeindex.OpenCodebaseDB(a.workDir)
	if err != nil {
		return 0, err
	}
	defer codebaseDB.Close()
	if err := codeindex.ClearAll(codebaseDB); err != nil {
		return 0, err
	}

	// Re-open and scan
	codebaseDB2, err := codeindex.OpenCodebaseDB(a.workDir)
	if err != nil {
		return 0, err
	}
	defer codebaseDB2.Close()

	// Read extensions from project config
	extensions := ".go,.js,.ts,.jsx,.tsx,.vue,.py,.rs,.java,.c,.cpp,.h,.hpp,.cs,.rb,.php,.swift,.kt"
	var extStr string
	if sqlDB, err := db.Open(a.workDir); err == nil {
		if v, e := db.GetConfig(sqlDB, "codebase_index.extensions"); e == nil && v != "" {
			extStr = v
		}
		db.Close(sqlDB)
	}
	if extStr != "" {
		extensions = extStr
	}
	extList := strings.Split(extensions, ",")

	scanner := codeindex.NewScanner(codebaseDB2, a.workDir, extList, a.logger)
	if err := scanner.ScanProject(); err != nil {
		return 0, err
	}

	// Count what was enqueued
	var count int
	codebaseDB2.QueryRow(`SELECT COUNT(*) FROM files WHERE status='pending'`).Scan(&count)

	// Ensure the queue worker is running to process the pending files
	_ = a.StartCodebaseIndex()

	return count, nil
}

// ResetFailedCodebaseIndex resets all failed index items back to pending for retry.
func (a *App) ResetFailedCodebaseIndex() error {
	codebaseDB, err := codeindex.OpenCodebaseDB(a.workDir)
	if err != nil {
		return err
	}
	defer codebaseDB.Close()
	return codeindex.ResetFailedItems(codebaseDB)
}

// ─── File Version (snapshot/diff) Wails bindings ───

// GetFileVersions returns all version records for a file.
func (a *App) GetFileVersions(filePath string) ([]fileversions.VersionRecord, error) {
	v, err := fileversions.NewVersioner(a.workDir)
	if err != nil {
		return nil, err
	}
	defer v.Close()
	records, err := v.GetVersions(filePath)
	if err != nil {
		return nil, err
	}
	if records == nil {
		return []fileversions.VersionRecord{}, nil
	}
	return records, nil
}

// GetVersionContent returns the full version data (metadata + content) for a version ID.
func (a *App) GetVersionContent(versionID int64) (*fileversions.VersionContent, error) {
	v, err := fileversions.NewVersioner(a.workDir)
	if err != nil {
		return nil, err
	}
	defer v.Close()
	return v.GetVersionContent(versionID)
}

// RestoreVersion overwrites the current file with the content from the given version.
func (a *App) RestoreVersion(versionID int64) error {
	v, err := fileversions.NewVersioner(a.workDir)
	if err != nil {
		return err
	}
	defer v.Close()
	return v.RestoreVersion(versionID)
}

// shutdown is called by Wails when the app shuts down.
func (a *App) shutdown(ctx context.Context) {
	if a.codebasePollerStop != nil {
		close(a.codebasePollerStop)
	}
	if a.codebaseIdxer != nil {
		a.codebaseIdxer.Close()
	}
	if a.fw != nil {
		a.fw.Stop()
	}
	if a.em != nil {
		a.em.Stop()
	}
}

// pollCodebaseStatus periodically reads codebase.db and pushes status to frontend.
// The codebasePollerStop channel is initialized in NewApp. shutdown() closes it to
// signal termination. This function is called exactly once from startup().
func (a *App) pollCodebaseStatus() {

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	defer func() { a.codebasePollerStop = nil }()

	// Initial push right away
	a.pushCodebaseStatus()

	for {
		select {
		case <-a.codebasePollerStop:
			return
		case <-ticker.C:
			a.pushCodebaseStatus()
		}
	}
}

func (a *App) pushCodebaseStatus() {
	codebaseDB, err := codeindex.OpenCodebaseDB(a.workDir)
	if err != nil {
		return
	}
	defer codebaseDB.Close()

	var fileCount, symbolCount, totalFiles int
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='done'`).Scan(&fileCount)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM symbols`).Scan(&symbolCount)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&totalFiles)

	var pending, indexing, failed, failedExhausted int
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='pending'`).Scan(&pending)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='indexing'`).Scan(&indexing)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='failed' AND retry_count < 3`).Scan(&failed)
	codebaseDB.QueryRow(`SELECT COUNT(*) FROM files WHERE status='failed' AND retry_count >= 3`).Scan(&failedExhausted)

	payload := map[string]interface{}{
		"files":    fileCount,
		"symbols":  symbolCount,
		"totalFiles": totalFiles,
		"pending":  pending,
		"indexing": indexing,
		"failed":   failed,
		"failed_exhausted": failedExhausted,
	}
	runtime.EventsEmit(a.ctx, "codebase:status", payload)

	// Wake up the indexer worker if there are pending items
	if pending > 0 && a.codebaseIdxer != nil {
		a.codebaseIdxer.Wakeup()
	}
}

// wailsEventPusher adapts watcher.EventPusher to Wails runtime.Events.Emit.
type wailsEventPusher struct {
	ctx context.Context
}

func (p *wailsEventPusher) Push(event string, data interface{}) {
	runtime.EventsEmit(p.ctx, event, data)
}

// ─── Helper: push events to frontend ───────────────────────

func (a *App) push(event string, data interface{}) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, event, data)
	}
}

// ─── Exposed methods (Wails bindings) ──────────────────────

// GetHttpPort returns the port of the raw file HTTP server.
func (a *App) GetHttpPort() int {
	return rawHTTPPort
}

// OpenDevTools opens the Chrome DevTools window for debugging the WebView2.
// With -tags devtools, Wails enables AreDevToolsEnabled in WebView2,
// and Ctrl+Shift+F12 triggers OpenDevToolsWindow() in the accelerator callback.
func (a *App) OpenDevTools() {
	const (
		VK_CONTROL      = 0x11
		VK_SHIFT        = 0x10
		VK_F12          = 0x7B
		KEYEVENTF_KEYUP = 0x0002
	)
	user32 := syscall.NewLazyDLL("user32.dll")
	findWindow := user32.NewProc("FindWindowW")
	setForeground := user32.NewProc("SetForegroundWindow")
	keybdEvent := user32.NewProc("keybd_event")

	className, _ := syscall.UTF16PtrFromString("WailsWindow")
	hwnd, _, _ := findWindow.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd == 0 {
		a.logger.Warn("OpenDevTools: WailsWindow not found")
		return
	}

	// Bring window to foreground so keystrokes reach the right target
	setForeground.Call(hwnd)

	// Send Ctrl+Shift+F12 — Wails' AcceleratorKeyCallback checks
	// for this combination when devtoolsEnabled=true.
	keybdEvent.Call(VK_CONTROL, 0, 0, 0)
	keybdEvent.Call(VK_SHIFT, 0, 0, 0)
	keybdEvent.Call(VK_F12, 0, 0, 0)
	keybdEvent.Call(VK_F12, 0, KEYEVENTF_KEYUP, 0)
	keybdEvent.Call(VK_SHIFT, 0, KEYEVENTF_KEYUP, 0)
	keybdEvent.Call(VK_CONTROL, 0, KEYEVENTF_KEYUP, 0)

	a.logger.Info("OpenDevTools: sent Ctrl+Shift+F12 keystrokes")
}

// GetWorkDir returns the current work directory.
func (a *App) GetWorkDir() string {
	return a.workDir
}

// SubscribeSession registers interest in a sub-session's updates.
// The frontend receives session:refresh events when new messages are written.
func (a *App) SubscribeSession(sessionID string) {
	a.muSubscribers.Lock()
	a.sessionSubscribers[sessionID] = true
	a.muSubscribers.Unlock()
	a.logger.Debug("session subscription added", zap.String("session_id", sessionID))
}

// UnsubscribeSession removes interest in a sub-session's updates.
func (a *App) UnsubscribeSession(sessionID string) {
	a.muSubscribers.Lock()
	delete(a.sessionSubscribers, sessionID)
	a.muSubscribers.Unlock()
	a.logger.Debug("session subscription removed", zap.String("session_id", sessionID))
}

// ─── Executor event routing → Wails Events ────────────────
//
// All LLM-related events go through a single "llm:event" channel.
// Frontend components filter by session_id in the payload.
// Non-LLM events (executor_done, ask_user) use dedicated channels.

func (a *App) onExecutorEvent(eventType string, payload map[string]interface{}) {
	// Log executor→frontend event for diagnostic
	pType, _ := payload["type"].(string)
	sID, _ := payload["session_id"].(string)
	tID, _ := payload["turn_id"].(string)
	content, _ := payload["content"].(string)
	cLen := len(content)
	cPreview := ""
	if cLen > 0 {
		if cLen > 50 {
			cPreview = content[:50] + "..."
		} else {
			cPreview = content
		}
	}
	a.logger.Info("[EVTLOG] IDE→Frontend",
		zap.String("et", eventType),
		zap.String("type", pType),
		zap.Int("clen", cLen),
		zap.String("preview", cPreview),
		zap.String("session_id", sID),
		zap.String("turn_id", tID),
	)
	// Inject the original executor event type so the frontend can distinguish
	// message_chunk from complete, error, llm_error, progress, etc.
	if _, ok := payload["_event_type"]; !ok {
		payload["_event_type"] = eventType
	}

	// Normalize tool_call/tool_result event type field for frontend
	switch eventType {
	case "tool_call":
		payload["type"] = "tool_call"
	case "tool_result":
		payload["type"] = "tool_result"
	}

	switch eventType {
	case "executor_done":
		a.push("chat:executor_done", payload)
	case "ask_user":
		a.push("ask_user", payload)
	case "complete", "error":
		// Turn ended — notify frontend via llm:event for live updates,
		// and session:refresh so session tree can reload DB-persisted state.
		a.push("llm:event", payload)
		if sessionID, ok := payload["session_id"].(string); ok && sessionID != "" {
			a.push("session:refresh", map[string]interface{}{
				"session_id": sessionID,
			})
		}
	default:
		// All LLM stream events (message_chunk, tool_call, tool_result,
		// tool_progress, llm_error, llm_retry, progress, etc.)
		a.push("llm:event", payload)
	}
}

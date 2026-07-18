//go:build windows
// +build windows

package main

import (
	"embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chonkpilot/chonkpilot/pkg/ide/config"
	"github.com/chonkpilot/chonkpilot/pkg/ide/folder"
	"github.com/chonkpilot/chonkpilot/pkg/ide/recent"
	"github.com/chonkpilot/chonkpilot/pkg/ide/workspace"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

//go:embed all:frontend/dist
var assets embed.FS

// lazyLogWriter opens the log file only during Write(), closes immediately after.
// This keeps the file unlocked between writes so external tools can read/delete/rotate it.
type lazyLogWriter struct {
	path string
}

func (w *lazyLogWriter) Write(p []byte) (int, error) {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Write(p)
}

func (w *lazyLogWriter) Sync() error { return nil }

func checkWebView2() error {
	tryKeys := []string{
		`SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`,
		`SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`,
		`SOFTWARE\WOW6432Node\Microsoft\Edge\WebView2`,
		`SOFTWARE\Microsoft\Edge\WebView2`,
	}
	for _, key := range tryKeys {
		k, err := syscall.UTF16PtrFromString(key)
		if err != nil {
			continue
		}
		var h syscall.Handle
		if err := syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, k, 0, syscall.KEY_READ, &h); err == nil {
			syscall.RegCloseKey(h)
			return nil
		}
	}
	return fmt.Errorf("WebView2 Runtime is not installed.\n\nPlease install it from:\n  https://developer.microsoft.com/en-us/microsoft-edge/webview2/\n\nOr run this command in PowerShell (admin):\n  winget install Microsoft.Edge.WebView2.Runtime")
}

var (
	workDir              string
	logger               *zap.Logger
	fsLogger             *zap.Logger
	recentMgr            *recent.Manager
	rawHTTPPort          int
	rawFileServer        *http.Server
)

func main() {
	// Initialize
	tStart := time.Now()

	var err error
	logger, err = zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger init error: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("ChonkPilot IDE starting...")

	// Check WebView2 Runtime
	if err := checkWebView2(); err != nil {
		logger.Fatal("WebView2 Runtime check failed", zap.Error(err))
	}

	// User config (NewUserConfigManager internally calls EnsureUserConfig for Chrome/defaults)
	userCfg, err := config.NewUserConfigManager(logger)
	if err != nil {
		logger.Warn("user config init warning", zap.Error(err))
	}

	// Reconfigure logger with user-configured log level
	logLevel := zapcore.DebugLevel
	if userCfg != nil {
		gc := userCfg.Get()
		if gc.LogLevel != "" {
			switch strings.ToLower(gc.LogLevel) {
			case "debug":
				logLevel = zapcore.DebugLevel
			case "info":
				logLevel = zapcore.InfoLevel
			case "warn":
				logLevel = zapcore.WarnLevel
			case "error":
				logLevel = zapcore.ErrorLevel
			}
		}
	}
	devCfg := zap.NewDevelopmentConfig()
	devCfg.Level = zap.NewAtomicLevelAt(logLevel)
	if l, err := devCfg.Build(); err == nil {
		logger = l
	}

	// Recent manager
	userHome, _ := os.UserHomeDir()
	recentMgr = recent.NewManager(userHome)
	_ = recentMgr.Init()

	// Determine work directory
	if len(os.Args) > 1 {
		for i, arg := range os.Args[1:] {
			if arg == "-dir" && i+1 < len(os.Args)-1 {
				workDir = os.Args[i+2]
				break
			}
		}
	}
	if workDir == "" {
		wd, err := recentMgr.GetDefaultWorkDir()
		if err == nil && wd != "" {
			workDir = wd
		}
	}
	if workDir == "" {
		workDir, err = folder.PickFolder("Select Project Directory")
		if err != nil {
			logger.Fatal("folder picker failed", zap.Error(err))
		}
		if workDir == "" {
			return
		}
	}
	logger.Info("Work directory", zap.String("dir", workDir))

	// Workspace init
	initializer := workspace.NewInitializer(workDir, logger)
	if err := initializer.Init(); err != nil {
		logger.Fatal("workspace init failed", zap.Error(err))
	}

	// Add file logging to .ide/logs/app.log (lazy: only opens during writes, releases between)
	// This allows external tools to freely read, delete, or rotate the log file.
	lazyLogPath := filepath.Join(workDir, ".ide", "logs", "app.log")
	// ensure dir exists
	_ = os.MkdirAll(filepath.Dir(lazyLogPath), 0755)
	fileCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		zapcore.AddSync(&lazyLogWriter{path: lazyLogPath}),
		logLevel,
	)
	// Combine stderr + file cores
	existingCore := logger.Core()
	multiCore := zapcore.NewTee(existingCore, fileCore)
	logger = zap.New(multiCore, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	logger.Info("File logging enabled", zap.String("path", lazyLogPath))

	// Config
	cfg := config.NewConfigManager(workDir, logger)
	if err := cfg.Load(); err != nil {
		logger.Warn("config load failed", zap.Error(err))
	}

	// Lock recent
	lockFile, err := recentMgr.LockAndCreate(workDir)
	if err != nil {
		logger.Warn("recent lock failed", zap.Error(err))
	}

	// Start HTTP server for /raw/ file serving (binary preview for images, PDF, etc.)
	startRawFileServer(workDir)

	// Create the App
	app := NewApp(workDir, logger, userCfg, recentMgr, cfg)

	logger.Info("TIMING startup", zap.Duration("elapsed", time.Since(tStart)))

	// Run Wails
	err = wails.Run(&options.App{
		Title:     "Chonk Pilot",
		Width:     1280,
		Height:    800,
		MinWidth:  800,
		MinHeight: 600,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 30, G: 30, B: 30, A: 1},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		logger.Fatal("Wails run failed", zap.Error(err))
	}

	// Cleanup
	if lockFile != nil {
		recentMgr.ReleaseAndTouch(lockFile)
	}
}

func startRawFileServer(dir string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logger.Warn("raw file server listen failed", zap.Error(err))
		return
	}
	rawHTTPPort = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	workDirSrv := http.FileServer(http.Dir(dir))
	mux.Handle("/raw/", corsMiddleware(logMiddleware(http.StripPrefix("/raw/", workDirSrv))))

	rawFileServer = &http.Server{Handler: mux}
	go func() {
		if err := rawFileServer.Serve(ln); err != http.ErrServerClosed {
			logger.Warn("raw file server stopped", zap.Error(err))
		}
	}()
	logger.Info("Raw file server started", zap.Int("port", rawHTTPPort))
}

// logMiddleware logs each HTTP request to the raw file server.
// corsMiddleware adds CORS headers so WebView2 (origin http://wails.localhost)
// can fetch raw files via the HTTP server.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(lrw, r)
		logger.Info("raw file request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", lrw.statusCode),
		)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

package watcher

import (
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// EventPusher is the minimal interface for pushing events to the frontend.
type EventPusher interface {
	Push(event string, data interface{})
}

// FileWatcher monitors file changes in the work directory.
type FileWatcher struct {
	workDir string
	watcher *fsnotify.Watcher
	logger  *zap.Logger
	pusher  EventPusher
	done    chan struct{}
}

// NewFileWatcher creates a new FileWatcher.
func NewFileWatcher(workDir string, logger *zap.Logger) *FileWatcher {
	return &FileWatcher{
		workDir: workDir,
		logger:  logger,
		done:    make(chan struct{}),
	}
}

// SetPusher sets the event pusher (bridge) for pushing file events to the frontend.
func (fw *FileWatcher) SetPusher(pusher EventPusher) {
	fw.pusher = pusher
}

// Start begins watching the work directory for file changes.
func (fw *FileWatcher) Start() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	fw.watcher = w

	// Add work directory and subdirectories (excluding .ide, .git, node_modules)
	added := 0
	walkErr := filepath.Walk(fw.workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".ide" || name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			if addErr := w.Add(path); addErr != nil {
				fw.logger.Warn("watcher add dir failed", zap.String("dir", path), zap.Error(addErr))
				return nil // continue walking, don't abort
			}
			added++
		}
		return nil
	})
	if walkErr != nil {
		fw.logger.Warn("file walk error", zap.Error(walkErr))
	}
	fw.logger.Info("FileWatcher started", zap.Int("directories", added))

	go fw.loop()
	return nil
}

func (fw *FileWatcher) loop() {
	// Debounce per directory: map[parentDir]lastEventTime
	debounce := make(map[string]time.Time)
	debounceInterval := 300 * time.Millisecond

	// Periodic cleanup: remove debounce entries older than 5 seconds
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}

			// Debounce by parent directory: skip duplicate bursts within 300ms
			parentDir := filepath.Dir(event.Name)
			now := time.Now()
			if last, ok := debounce[parentDir]; ok && now.Sub(last) < debounceInterval {
				continue
			}
			debounce[parentDir] = now

			// Push to frontend
			if fw.pusher != nil {
				op := "modified"
				if event.Op&fsnotify.Create != 0 {
					op = "created"
				} else if event.Op&fsnotify.Remove != 0 {
					op = "deleted"
				} else if event.Op&fsnotify.Rename != 0 {
					op = "renamed"
				}
				fw.pusher.Push("file:changed", map[string]interface{}{
					"path": event.Name,
					"op":   op,
					"dir":  parentDir,
				})
			}

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			fw.logger.Error("Watcher error", zap.Error(err))
			// Notify frontend of watcher error
			if fw.pusher != nil {
				fw.pusher.Push("file:watcher-error", map[string]interface{}{
					"error": err.Error(),
				})
			}
		case <-ticker.C:
			// Clean up stale debounce entries (older than 5 seconds)
			cutoff := time.Now().Add(-5 * time.Second)
			for dir, t := range debounce {
				if t.Before(cutoff) {
					delete(debounce, dir)
				}
			}
		case <-fw.done:
			return
		}
	}
}

// Stop stops the file watcher.
func (fw *FileWatcher) Stop() {
	close(fw.done)
	if fw.watcher != nil {
		fw.watcher.Close()
	}
}

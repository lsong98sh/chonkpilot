package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// EventPusher is the minimal interface for pushing events to the frontend.
type EventPusher interface {
	Push(event string, data interface{})
}

// FileNode describes a single file/directory entry pushed to the frontend.
type FileNode struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// FileWatcher monitors file changes only in directories that have been
// explicitly registered via WatchDir (lazy, expand-driven watching).
//
// Events are deduplicated in a queue and processed on a 60ms tick.
// For each changed directory, its current contents are read and pushed
// as a "file:dir-contents" event so the frontend can do incremental updates.
type FileWatcher struct {
	workDir string
	watcher *fsnotify.Watcher
	logger  *zap.Logger
	pusher  EventPusher

	// Track which directories are currently being watched
	watched map[string]bool
	mu      sync.RWMutex

	// Event queue: dedup by file path
	queue   map[string]fsnotify.Event
	queueMu sync.Mutex
	timer   *time.Timer

	done chan struct{}
}

// NewFileWatcher creates a new FileWatcher.
func NewFileWatcher(workDir string, logger *zap.Logger) *FileWatcher {
	return &FileWatcher{
		workDir: workDir,
		logger:  logger,
		watched: make(map[string]bool),
		queue:   make(map[string]fsnotify.Event),
		done:    make(chan struct{}),
	}
}

// SetPusher sets the event pusher (bridge) for pushing file events to the frontend.
func (fw *FileWatcher) SetPusher(pusher EventPusher) {
	fw.pusher = pusher
}

// Start begins the file watcher. Only the root directory is initially watched.
func (fw *FileWatcher) Start() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	fw.watcher = w

	// Watch root directory so we can detect new top-level files/dirs
	if err := w.Add(fw.workDir); err != nil {
		fw.logger.Warn("watch root failed", zap.String("dir", fw.workDir), zap.Error(err))
	}

	// Root is always watched
	fw.mu.Lock()
	fw.watched[fw.workDir] = true
	fw.mu.Unlock()

	fw.logger.Info("FileWatcher started, root dir watched")
	go fw.loop()
	return nil
}

// WatchDir adds a directory to the watcher (called when tree node expands).
// The frontend calls getFileTreeChildren via API, not via watcher push.
func (fw *FileWatcher) WatchDir(path string) {
	fw.mu.Lock()
	if fw.watched[path] {
		fw.mu.Unlock()
		return
	}
	fw.watched[path] = true
	fw.mu.Unlock()

	if fw.watcher != nil {
		if err := fw.watcher.Add(path); err != nil {
			fw.logger.Warn("watch add failed", zap.String("dir", path), zap.Error(err))
		} else {
			fw.logger.Debug("watch added", zap.String("dir", path))
		}
	}
}

// UnwatchDir removes a directory from the watcher (called when tree node collapses).
// If recursive=true, also removes all watched subdirectories with dirPath/ prefix.
func (fw *FileWatcher) UnwatchDir(dirPath string, recursive bool) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	if recursive {
		// Find all watched dirs with given prefix
		prefix := dirPath
		if !strings.HasSuffix(prefix, string(filepath.Separator)) {
			prefix += string(filepath.Separator)
		}
		for watchedPath := range fw.watched {
			if watchedPath == dirPath || strings.HasPrefix(watchedPath, prefix) {
				delete(fw.watched, watchedPath)
				if fw.watcher != nil {
					if err := fw.watcher.Remove(watchedPath); err != nil {
						if !strings.Contains(err.Error(), "non-existent") {
							fw.logger.Warn("watch remove failed", zap.String("dir", watchedPath), zap.Error(err))
						}
					}
				}
			}
		}
	} else {
		if !fw.watched[dirPath] {
			return
		}
		delete(fw.watched, dirPath)
		if fw.watcher != nil {
			if err := fw.watcher.Remove(dirPath); err != nil {
				if !strings.Contains(err.Error(), "non-existent") {
					fw.logger.Warn("watch remove failed", zap.String("dir", dirPath), zap.Error(err))
				}
			}
		}
	}
}

// pushDirContents reads the directory and pushes a file:dir-contents event.
func (fw *FileWatcher) pushDirContents(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var children []FileNode
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") || strings.HasSuffix(name, "~") || strings.HasPrefix(name, "~$") {
			continue
		}
		children = append(children, FileNode{
			Name:  name,
			Path:  filepath.Join(dir, name),
			IsDir: e.IsDir(),
		})
	}
	if fw.pusher != nil {
		fw.pusher.Push("file:dir-contents", map[string]interface{}{
			"dir":      dir,
			"children": children,
		})
	}
}

// enqueue adds a fsnotify event to the dedup queue.
func (fw *FileWatcher) enqueue(event fsnotify.Event) {
	fw.queueMu.Lock()
	fw.queue[event.Name] = event
	fw.queueMu.Unlock()
	fw.armTimer()
}

// snapshotQueue atomically copies and clears the event queue.
func (fw *FileWatcher) snapshotQueue() map[string]fsnotify.Event {
	fw.queueMu.Lock()
	if len(fw.queue) == 0 {
		fw.queueMu.Unlock()
		return nil
	}
	batch := fw.queue
	fw.queue = make(map[string]fsnotify.Event)
	fw.queueMu.Unlock()
	return batch
}

// armTimer starts the 60ms processing timer.
func (fw *FileWatcher) armTimer() {
	fw.queueMu.Lock()
	if fw.timer != nil {
		fw.timer.Stop()
	}
	fw.timer = time.AfterFunc(60*time.Millisecond, fw.processBatch)
	fw.queueMu.Unlock()
}

// processBatch drains the event queue and sends aggregated updates.
func (fw *FileWatcher) processBatch() {
	batch := fw.snapshotQueue()
	if len(batch) == 0 {
		return
	}

	// Collect unique parent directories from events
	dirsToRefresh := make(map[string]bool)
	for path, evt := range batch {
		parentDir := filepath.Dir(path)

		// Only refresh directories that are currently being watched
		fw.mu.RLock()
		watched := fw.watched[parentDir]
		fw.mu.RUnlock()
		if !watched {
			continue
		}

		// For file modifications, also push a file:changed event (for open file refresh)
		if evt.Op&fsnotify.Write != 0 {
			if fw.pusher != nil {
				op := "modified"
				fw.pusher.Push("file:changed", map[string]interface{}{
					"path": path,
					"op":   op,
					"dir":  parentDir,
				})
			}
		}

		// Check if this is a new subdirectory (added to tracked dir)
		if evt.Op&fsnotify.Create != 0 {
			if fi, err := os.Stat(path); err == nil && fi.IsDir() {
				// Auto-watch the new directory so files inside it are tracked
				fw.mu.Lock()
				if !fw.watched[path] {
					fw.watched[path] = true
					if fw.watcher != nil {
						fw.watcher.Add(path)
					}
				}
				fw.mu.Unlock()
				// Also refresh the parent so the frontend sees the new child
				dirsToRefresh[parentDir] = true
				parentDir = path
			}
		}

		// Clean up stale watched entries when a directory is renamed or deleted
		if evt.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
			fw.mu.Lock()
			if fw.watched[path] {
				delete(fw.watched, path)
				if fw.watcher != nil {
					fw.watcher.Remove(path) // clean up fsnotify handle, ignore error
				}
			}
			fw.mu.Unlock()
		}
		dirsToRefresh[parentDir] = true
	}

	// Push new contents for each changed directory
	for dir := range dirsToRefresh {
		fw.pushDirContents(dir)
	}

	// If more events arrived during processing, schedule another round
	fw.queueMu.Lock()
	if len(fw.queue) > 0 && fw.timer == nil {
		fw.timer = time.AfterFunc(60*time.Millisecond, fw.processBatch)
	}
	fw.queueMu.Unlock()
}

func (fw *FileWatcher) loop() {
	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}
			fw.enqueue(event)

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			fw.logger.Error("Watcher error", zap.Error(err))
			if fw.pusher != nil {
				fw.pusher.Push("file:watcher-error", map[string]interface{}{
					"error": err.Error(),
				})
			}

		case <-fw.done:
			return
		}
	}
}

// Stop stops the file watcher and removes all watched directories.
func (fw *FileWatcher) Stop() {
	fw.queueMu.Lock()
	if fw.timer != nil {
		fw.timer.Stop()
		fw.timer = nil
	}
	fw.queueMu.Unlock()
	close(fw.done)
	if fw.watcher != nil {
		fw.watcher.Close()
	}
}

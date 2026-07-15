package codeindex

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// LLMCaller is a function that calls the LLM with a system prompt and user prompt,
// and returns the full text response (non-streaming).
type LLMCaller func(systemPrompt, userPrompt string) (string, error)

// Indexer manages the codebase index with a queue-based background worker.
type Indexer struct {
	db       *sql.DB
	caller   LLMCaller
	logger   *zap.Logger
	workDir  string
	extMap   map[string]bool // allowed extensions (with dot, e.g. ".go")

	workerStop chan struct{}
	workerWg   sync.WaitGroup
	wakeup     chan struct{} // signal the worker to re-check the queue
	started    bool
	mu         sync.Mutex

	// concurrency control
	workerConcurrency int     // number of parallel LLM calls (default 5)
	batchSize         int     // how many items to dequeue at once (default 100)

	// deferred change tracking (MarkChanged / FlushChangedFiles)
	changedFiles map[string]struct{}
	changedMu    sync.Mutex
}

// NewIndexer creates a new Indexer.
func NewIndexer(codebaseDB *sql.DB, workDir string, extensions []string, caller LLMCaller, logger *zap.Logger) *Indexer {
	extMap := make(map[string]bool)
	for _, ext := range extensions {
		e := strings.TrimSpace(ext)
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extMap[strings.ToLower(e)] = true
	}
	return &Indexer{
		db:                codebaseDB,
		caller:            caller,
		logger:            logger,
		workDir:           workDir,
		extMap:            extMap,
		workerStop:        make(chan struct{}),
		wakeup:            make(chan struct{}, 1),
		workerConcurrency: 5,
		batchSize:         100,
		changedFiles:      make(map[string]struct{}),
	}
}

// NewScanner creates an Indexer in scan-only mode (no worker, no LLM caller).
func NewScanner(codebaseDB *sql.DB, workDir string, extensions []string, logger *zap.Logger) *Indexer {
	return NewIndexer(codebaseDB, workDir, extensions, nil, logger)
}

// ──────── Lifecycle ────────

// Start starts the background queue worker.
func (idx *Indexer) Start() {
	idx.mu.Lock()
	if idx.started {
		idx.mu.Unlock()
		return
	}
	idx.started = true
	idx.mu.Unlock()

	idx.workerWg.Add(1)
	go idx.workerLoop()
	idx.logger.Info("codeindex worker started")
}

// Stop stops the background queue worker gracefully.
func (idx *Indexer) Stop() {
	idx.mu.Lock()
	if !idx.started {
		idx.mu.Unlock()
		return
	}
	idx.started = false
	idx.mu.Unlock()

	close(idx.workerStop)
	idx.workerWg.Wait()
	idx.logger.Info("codeindex worker stopped")
}

// Wakeup signals the worker to re-check the queue immediately.
func (idx *Indexer) Wakeup() {
	select {
	case idx.wakeup <- struct{}{}:
	default:
	}
}

// Close stops the worker and closes the database.
func (idx *Indexer) Close() {
	idx.Stop()
	if idx.db != nil {
		idx.db.Close()
	}
}

// ──────── Settings ────────

// SetConcurrency sets the number of parallel LLM calls (clamped to 1-20).
func (idx *Indexer) SetConcurrency(n int) {
	if n < 1 {
		n = 1
	} else if n > 20 {
		n = 20
	}
	idx.workerConcurrency = n
}

// SetBatchSize sets the batch size for dequeuing (clamped to 1-500).
func (idx *Indexer) SetBatchSize(n int) {
	if n < 1 {
		n = 1
	} else if n > 500 {
		n = 500
	}
	idx.batchSize = n
}

// ──────── Deferred Change Tracking ────────

// MarkChanged records a file as changed for deferred batch indexing.
// This is called by write_file/replace during the tool loop.
// The actual Enqueue happens in FlushChangedFiles at turn end.
func (idx *Indexer) MarkChanged(path string) {
	if !idx.IsAllowedExtension(path) {
		return
	}
	// Convert to relative path for storage
	relPath := path
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(idx.workDir, path); err == nil {
			relPath = rel
		}
	}
	idx.changedMu.Lock()
	idx.changedFiles[relPath] = struct{}{}
	idx.changedMu.Unlock()
}

// FlushChangedFiles batch-enqueues all changed files to DB and wakes the worker.
// Returns the number of files flushed.
func (idx *Indexer) FlushChangedFiles() int {
	idx.changedMu.Lock()
	if len(idx.changedFiles) == 0 {
		idx.changedMu.Unlock()
		return 0
	}
	paths := make([]string, 0, len(idx.changedFiles))
	for p := range idx.changedFiles {
		paths = append(paths, p)
	}
	idx.changedFiles = make(map[string]struct{})
	idx.changedMu.Unlock()

	enqueued := 0
	for _, p := range paths {
		if err := EnqueueFile(idx.db, p, idx.workDir); err != nil {
			idx.logger.Warn("codeindex enqueue failed",
				zap.String("path", p), zap.Error(err))
		} else {
			enqueued++
		}
	}

	if enqueued > 0 {
		idx.logger.Info("codeindex flushed changed files",
			zap.Int("total", len(paths)),
			zap.Int("enqueued", enqueued))
		idx.Wakeup()
	}

	return enqueued
}

// ──────── Clear ────────

// ClearQueue removes all pending/indexing queue entries.
func (idx *Indexer) ClearQueue() error {
	return ClearQueue(idx.db)
}

// ClearAll removes all index data and queue entries.
func (idx *Indexer) ClearAll() error {
	return ClearAll(idx.db)
}

// ──────── Queue Worker ────────

func (idx *Indexer) workerLoop() {
	defer idx.workerWg.Done()

	iterCount := 0
	sem := make(chan struct{}, idx.workerConcurrency)

	for {
		select {
		case <-idx.workerStop:
			return
		default:
		}

		// Periodic cleanup: every 50 iterations, remove orphan indices
		iterCount++
		if iterCount%50 == 0 {
			idx.cleanupStaleIndices()
		}

		// Dequeue a batch of pending items
		items, err := DequeueBatch(idx.db, idx.batchSize)
		if err != nil {
			idx.logger.Warn("codeindex dequeue error", zap.Error(err))
			time.Sleep(time.Second)
			continue
		}
		if len(items) == 0 {
			// Nothing to do, wait for wakeup or stop, or poll periodically
			select {
			case <-idx.workerStop:
				return
			case <-idx.wakeup:
			case <-time.After(30 * time.Second):
			}
			continue
		}

		idx.logger.Info("codeindex processing batch",
			zap.Int("batch_size", len(items)),
			zap.Int("concurrency", idx.workerConcurrency))

		// Process items concurrently, each goroutine writes its own DB result
		var wg sync.WaitGroup
		for _, item := range items {
			select {
			case <-idx.workerStop:
				return
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(qi QueueItem) {
				defer func() {
					<-sem
					wg.Done()
				}()

				idx.logger.Info("codeindex processing file", zap.String("path", qi.FilePath))

				if err := idx.processFile(&qi); err != nil {
					idx.logger.Warn("codeindex file failed",
						zap.String("path", qi.FilePath),
						zap.Error(err))
					MarkQueueFailed(idx.db, qi.FilePath)
				} else {
					MarkQueueDone(idx.db, qi.FilePath)
				}
			}(item)
		}
		wg.Wait()
	}
}

// cleanupStaleIndices removes index entries for files that no longer exist on disk.
func (idx *Indexer) cleanupStaleIndices() {
	rows, err := idx.db.Query(`SELECT path FROM files`)
	if err != nil {
		return
	}
	defer rows.Close()

	var cleaned int
	for rows.Next() {
		var relPath string
		rows.Scan(&relPath)
		fullPath := relPath
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(idx.workDir, fullPath)
		}
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			DeleteFileIndex(idx.db, relPath)
			cleaned++
		}
	}

	if cleaned > 0 {
		idx.logger.Info("codeindex: cleaned up stale indices",
			zap.Int("removed", cleaned))
	}
}

func (idx *Indexer) processFile(item *QueueItem) error {
	// Resolve path
	fullPath := item.FilePath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(idx.workDir, fullPath)
	}

	// File no longer exists → clean up index and queue
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		idx.logger.Info("codeindex: file deleted, cleaning up index",
			zap.String("path", item.FilePath))
		DeleteFileIndex(idx.db, item.FilePath)
		return nil
	}

	// Read file content
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	content := string(data)

	// Compute checksum
	checksum := fmt.Sprintf("%x", sha256.Sum256(data))

	// Load old index if exists
	oldIndex, _ := GetFileIndex(idx.db, item.FilePath)

	// Truncate content for LLM
	analysisContent := idx.truncateContent(content)

	// Build prompt
	userPrompt := idx.buildAnalyzePrompt(item.FilePath, analysisContent, oldIndex)
	systemPrompt := `You are a codebase indexer. Analyze the provided code file and extract structured information.
Output ONLY valid JSON conforming to the following schema:
{
  "language": "go",
  "summary": "brief description of what this file does",
  "imports": ["os", "fmt"],
  "exports": ["FuncName", "StructName"],
  "external_deps": ["github.com/xxx/yyy"],
  "symbols": [
    {
      "kind": "function|class|component|interface|struct|type|const|var",
      "name": "SymbolName",
      "exported": true,
      "signature": "func Foo() error",
      "doc_summary": "what this symbol does"
    }
  ]
}
If the old index is provided, use it as reference and only update what changed. Omit unchanged fields. Keep the summary and doc_summary short (1-2 sentences).`

	resp, err := idx.caller(systemPrompt, userPrompt)
	if err != nil {
		return fmt.Errorf("LLM analysis: %w", err)
	}

	// Parse JSON
	fi := parseIndexResponse(resp, item.FilePath)
	return SaveFileIndex(idx.db, fi, checksum)
}

// ──────── Enqueue ────────

// Enqueue adds a file to the pending index queue.
func (idx *Indexer) Enqueue(path string) {
	if !idx.IsAllowedExtension(path) {
		return
	}

	relPath := path
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(idx.workDir, path); err == nil {
			relPath = rel
		}
	}

	if err := EnqueueFile(idx.db, relPath, idx.workDir); err != nil {
		idx.logger.Warn("codeindex enqueue failed",
			zap.String("path", relPath),
			zap.Error(err))
	}
}

// ──────── Initial Scan ────────

// ScanProject walks the project directory and enqueues all matching files.
func (idx *Indexer) ScanProject() error {
	idx.logger.Info("codeindex starting project scan", zap.String("dir", idx.workDir))

	skipDirs := map[string]bool{
		"node_modules": true,
		".git":         true,
		".svn":         true,
		"__pycache__":  true,
		".next":        true,
		"dist":         true,
		"build":        true,
		"vendor":       true,
		".tox":         true,
	}

	var enqueued int
	err := filepath.WalkDir(idx.workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if idx.IsAllowedExtension(path) {
			idx.Enqueue(path)
			enqueued++
		}
		return nil
	})

	idx.logger.Info("codeindex scan complete",
		zap.Int("enqueued", enqueued),
		zap.Error(err))
	return err
}

// ──────── Helpers ────────

// IsAllowedExtension checks if a file path's extension should be indexed.
func (idx *Indexer) IsAllowedExtension(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return idx.extMap[ext]
}

// DB returns the underlying SQLite handle.
func (idx *Indexer) DB() *sql.DB {
	return idx.db
}

// WorkDir returns the project root directory.
func (idx *Indexer) WorkDir() string {
	return idx.workDir
}

// Extensions returns the allowed extension list.
func (idx *Indexer) Extensions() []string {
	exts := make([]string, 0, len(idx.extMap))
	for ext := range idx.extMap {
		exts = append(exts, ext)
	}
	return exts
}

// QueueStats returns current queue counts.
func (idx *Indexer) QueueStats() (pending, indexing, failed, failedExhausted int) {
	return QueueCounts(idx.db)
}

// ──────── Prompt Building ────────

func (idx *Indexer) buildAnalyzePrompt(path, content string, oldIndex *FileIndex) string {
	var b strings.Builder
	b.WriteString("Analyze this file:\n")
	b.WriteString(fmt.Sprintf("Path: %s\n", path))
	b.WriteString(fmt.Sprintf("Extension: %s\n", filepath.Ext(path)))
	b.WriteString("\n--- Content ---\n")
	b.WriteString(content)
	b.WriteString("\n--- End Content ---\n")

	if oldIndex != nil {
		oldJSON, _ := json.MarshalIndent(oldIndex, "", "  ")
		b.WriteString("\n--- Old Index (reference) ---\n")
		b.WriteString(string(oldJSON))
		b.WriteString("\n--- End Old Index ---\n")
	}
	return b.String()
}

func (idx *Indexer) truncateContent(content string) string {
	if len(content) > 50*1024 {
		return ""
	}
	return content
}

// parseIndexResponse attempts to parse JSON from LLM response.
func parseIndexResponse(resp, path string) *FileIndex {
	fi := &FileIndex{Path: path}

	if idx := strings.Index(resp, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(resp[start:], "```"); end >= 0 {
			jsonStr := strings.TrimSpace(resp[start : start+end])
			var fi2 FileIndex
			if err := json.Unmarshal([]byte(jsonStr), &fi2); err == nil {
				fi2.Path = path
				return &fi2
			}
		}
	}
	if idx := strings.Index(resp, "{"); idx >= 0 {
		if end := strings.LastIndex(resp, "}"); end > idx {
			jsonStr := resp[idx : end+1]
			var fi2 FileIndex
			if err := json.Unmarshal([]byte(jsonStr), &fi2); err == nil {
				fi2.Path = path
				return &fi2
			}
		}
	}
	return fi
}

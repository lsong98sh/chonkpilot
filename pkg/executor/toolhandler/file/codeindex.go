package file

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleQueryCodebase dispatches query_codebase tool calls.
func HandleQueryCodebase(codeIndexer *codeindex.Indexer, args map[string]interface{}) *types.ToolResult {
	if codeIndexer == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "codebase indexing is not enabled for this project",
			Tool:    "query_codebase",
		}
	}

	queryType, _ := args["query"].(string)
	db := codeIndexer.DB()
	workDir := codeIndexer.WorkDir()

	// If no query type but has target/content, treat as "list"
	if queryType == "" {
		target, _ := args["target"].(string)
		content, _ := args["content"].(string)
		if target != "" || content != "" {
			return queryFileList(db, workDir, target, content)
		}
	}

	switch queryType {
	case "overview":
		return queryOverview(db, codeIndexer)
	case "search":
		keywords, _ := args["keywords"].(string)
		return querySearch(db, workDir, keywords)
	case "symbol":
		name, _ := args["name"].(string)
		return querySymbol(db, name)
	case "file":
		path, _ := args["path"].(string)
		return queryFile(db, codeIndexer, path)
	case "deps":
		path, _ := args["path"].(string)
		return queryDeps(db, workDir, path)
	case "usages":
		name, _ := args["name"].(string)
		fileFilter, _ := args["file"].(string)
		return queryUsages(db, workDir, name, fileFilter)
	default:
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("unknown query type: %s (supported: overview, search, symbol, file, deps, usages)", queryType),
			Tool:    "query_codebase",
		}
	}
}

func queryOverview(db *sql.DB, codeIndexer *codeindex.Indexer) *types.ToolResult {
	overview, err := codeindex.GetOverview(db)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "query_codebase"}
	}

	pending, indexing, failed, failedExhausted := codeIndexer.QueueStats()

	var statusLine string
	if pending > 0 || indexing > 0 {
		statusLine = fmt.Sprintf("⚠  Indexing in progress: %d pending, %d indexing, %d failed, %d exhausted\n", pending, indexing, failed, failedExhausted)
	} else {
		statusLine = "✓  All queued files indexed.\n"
	}

	overviewJSON, _ := json.MarshalIndent(overview, "", "  ")
	return &types.ToolResult{
		Success: true,
		Output:  statusLine + "Codebase Overview:\n" + string(overviewJSON),
		Tool:    "query_codebase",
	}
}

func querySearch(db *sql.DB, workDir, keywords string) *types.ToolResult {
	if keywords == "" {
		return &types.ToolResult{Success: false, Error: "keywords is required for search query", Tool: "query_codebase"}
	}
	results, err := codeindex.SearchFiles(db, keywords)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "query_codebase"}
	}
	if len(results) == 0 {
		return &types.ToolResult{Success: true, Output: "No matching files found.", Tool: "query_codebase"}
	}

	// Validate each result file still exists; clean up stale indices
	valid := make([]codeindex.FileIndex, 0, len(results))
	for _, fi := range results {
		if validateFileExists(db, workDir, fi.Path) {
			valid = append(valid, fi)
		}
	}

	if len(valid) == 0 {
		return &types.ToolResult{Success: true, Output: "No matching files found (all were stale and cleaned up).", Tool: "query_codebase"}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d files matching \"%s\":\n\n", len(valid), keywords))
	for _, fi := range valid {
		b.WriteString(fmt.Sprintf("  %s  (%s)\n", fi.Path, fi.Language))
		if fi.Summary != "" {
			b.WriteString(fmt.Sprintf("      %s\n", fi.Summary))
		}
	}
	return &types.ToolResult{Success: true, Output: b.String(), Tool: "query_codebase"}
}

func querySymbol(db *sql.DB, name string) *types.ToolResult {
	if name == "" {
		return &types.ToolResult{Success: false, Error: "name is required for symbol query", Tool: "query_codebase"}
	}
	symbols, err := codeindex.FindSymbol(db, name)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "query_codebase"}
	}
	if len(symbols) == 0 {
		return &types.ToolResult{Success: true, Output: fmt.Sprintf("No symbols found matching \"%s\".", name), Tool: "query_codebase"}
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d symbols matching \"%s\":\n", len(symbols), name))
	for _, s := range symbols {
		b.WriteString(fmt.Sprintf("  [%s] %s\n", s.Kind, s.Name))
		if s.Signature != "" {
			b.WriteString(fmt.Sprintf("       %s\n", s.Signature))
		}
		if s.DocSummary != "" {
			b.WriteString(fmt.Sprintf("       %s\n", s.DocSummary))
		}
	}
	return &types.ToolResult{Success: true, Output: b.String(), Tool: "query_codebase"}
}

func queryFile(db *sql.DB, codeIndexer *codeindex.Indexer, path string) *types.ToolResult {
	if path == "" {
		return &types.ToolResult{Success: false, Error: "path is required for file query", Tool: "query_codebase"}
	}

	// Validate file still exists; if not, cleanup and return
	workDir := codeIndexer.WorkDir()
	if !validateFileExists(db, workDir, path) {
		return &types.ToolResult{Success: true, Output: fmt.Sprintf("File \"%s\" no longer exists on disk (stale index cleaned up).", path), Tool: "query_codebase"}
	}

	queuedPaths, _ := codeindex.ListQueuedPaths(db)
	isQueued := false
	for _, qp := range queuedPaths {
		if qp == path {
			isQueued = true
			break
		}
	}

	fi, err := codeindex.GetFileIndex(db, path)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "query_codebase"}
	}
	if fi == nil {
		if isQueued {
			return &types.ToolResult{Success: true, Output: fmt.Sprintf("⏳ File \"%s\" is queued for indexing (pending in queue).", path), Tool: "query_codebase"}
		}
		return &types.ToolResult{Success: true, Output: fmt.Sprintf("File \"%s\" is not indexed yet.", path), Tool: "query_codebase"}
	}

	header := ""
	if isQueued {
		header = "⚠  This file has pending changes queued for re-indexing.\n"
	}

	out, _ := json.MarshalIndent(fi, "", "  ")
	return &types.ToolResult{Success: true, Output: header + string(out), Tool: "query_codebase"}
}

func queryDeps(db *sql.DB, workDir, path string) *types.ToolResult {
	if path == "" {
		return &types.ToolResult{Success: false, Error: "path is required for deps query", Tool: "query_codebase"}
	}

	// Validate file still exists; if not, cleanup and return
	if !validateFileExists(db, workDir, path) {
		return &types.ToolResult{Success: true, Output: fmt.Sprintf("File \"%s\" no longer exists on disk (stale index cleaned up).", path), Tool: "query_codebase"}
	}

	fi, err := codeindex.GetFileIndex(db, path)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "query_codebase"}
	}
	if fi == nil {
		return &types.ToolResult{Success: true, Output: fmt.Sprintf("File \"%s\" is not indexed.", path), Tool: "query_codebase"}
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Dependencies for %s:\n", path))
	if len(fi.Imports) > 0 {
		b.WriteString(fmt.Sprintf("  Internal: %s\n", strings.Join(fi.Imports, ", ")))
	}
	if len(fi.ExternalDeps) > 0 {
		b.WriteString(fmt.Sprintf("  External: %s\n", strings.Join(fi.ExternalDeps, ", ")))
	}
	if len(fi.Exports) > 0 {
		b.WriteString(fmt.Sprintf("  Exports: %s\n", strings.Join(fi.Exports, ", ")))
	}
	return &types.ToolResult{Success: true, Output: b.String(), Tool: "query_codebase"}
}

func queryUsages(db *sql.DB, workDir, name, fileFilter string) *types.ToolResult {
	if name == "" {
		return &types.ToolResult{Success: false, Error: "name is required for usages query", Tool: "query_codebase"}
	}

	if fileFilter != "" {
		// Validate file still exists
		if !validateFileExists(db, workDir, fileFilter) {
			return &types.ToolResult{Success: true, Output: fmt.Sprintf("File \"%s\" no longer exists on disk (stale index cleaned up).", fileFilter), Tool: "query_codebase"}
		}
		fi, err := codeindex.GetFileIndex(db, fileFilter)
		if err != nil || fi == nil {
			return &types.ToolResult{Success: true, Output: fmt.Sprintf("File \"%s\" not indexed.", fileFilter), Tool: "query_codebase"}
		}
		for _, imp := range fi.Imports {
			if strings.Contains(strings.ToLower(imp), strings.ToLower(name)) {
				out, _ := json.MarshalIndent(fi, "", "  ")
				return &types.ToolResult{Success: true, Output: fmt.Sprintf("References found in %s:\n%s", fileFilter, string(out)), Tool: "query_codebase"}
			}
		}
		for _, exp := range fi.Exports {
			if strings.Contains(strings.ToLower(exp), strings.ToLower(name)) {
				return &types.ToolResult{Success: true, Output: fmt.Sprintf("Symbol \"%s\" is exported by %s.", name, fileFilter), Tool: "query_codebase"}
			}
		}
		return &types.ToolResult{Success: true, Output: fmt.Sprintf("No references to \"%s\" found in %s.", name, fileFilter), Tool: "query_codebase"}
	}

	results, err := codeindex.SearchFiles(db, name)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "query_codebase"}
	}
	if len(results) == 0 {
		return &types.ToolResult{Success: true, Output: fmt.Sprintf("No usages found for \"%s\".", name), Tool: "query_codebase"}
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Files referencing \"%s\":\n", name))
	for _, fi := range results {
		b.WriteString(fmt.Sprintf("  %s (%s)\n", fi.Path, fi.Language))
		if fi.Summary != "" {
			b.WriteString(fmt.Sprintf("      %s\n", fi.Summary))
		}
	}
	return &types.ToolResult{Success: true, Output: b.String(), Tool: "query_codebase"}
}

// ──────── File Validation ────────

// validateFileExists checks whether a file still exists on disk.
// If not, it cleans up the index entry and returns false.
func validateFileExists(db *sql.DB, workDir, relPath string) bool {
	fullPath := relPath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(workDir, fullPath)
	}
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		// File no longer on disk — clean up stale index
		codeindex.DeleteFileIndex(db, relPath)
		return false
	}
	return true
}

// ──────── queryFileList (target + content) ────────

// queryFileList finds files by target path pattern and/or content search.
// target supports ** globstar matching against relative file paths.
// content does SQL LIKE search on file summaries.
// Only one of target/content needs to be non-empty (OR logic; both = AND).
func queryFileList(db *sql.DB, workDir, target, content string) *types.ToolResult {
	var results []codeindex.FileIndex

	if content != "" {
		var err error
		results, err = codeindex.SearchFiles(db, content)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error(), Tool: "query_codebase"}
		}
	} else {
		// No content filter — get all done files
		rows, err := db.Query(`SELECT path, language, summary FROM files WHERE status='done' ORDER BY path LIMIT 100`)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error(), Tool: "query_codebase"}
		}
		defer rows.Close()
		for rows.Next() {
			var fi codeindex.FileIndex
			if err := rows.Scan(&fi.Path, &fi.Language, &fi.Summary); err != nil {
				return &types.ToolResult{Success: false, Error: err.Error(), Tool: "query_codebase"}
			}
			results = append(results, fi)
		}
	}

	// Filter by target glob pattern if set
	if target != "" {
		var filtered []codeindex.FileIndex
		for _, fi := range results {
			if matchTargetGlob(fi.Path, target) {
				filtered = append(filtered, fi)
			}
		}
		results = filtered
	}

	// Build output
	if len(results) == 0 {
		msg := "No matching files found in codebase index."
		if target != "" && content != "" {
			msg = fmt.Sprintf("No files matching target=\"%s\" and content=\"%s\" in codebase index.", target, content)
		} else if target != "" {
			msg = fmt.Sprintf("No files matching target=\"%s\" in codebase index.", target)
		} else if content != "" {
			msg = fmt.Sprintf("No files matching content=\"%s\" in codebase index.", content)
		}
		return &types.ToolResult{Success: true, Output: msg, Tool: "query_codebase"}
	}

	var b strings.Builder
	if target != "" && content != "" {
		b.WriteString(fmt.Sprintf("Found %d files matching target=\"%s\" and content=\"%s\":\n\n", len(results), target, content))
	} else if target != "" {
		b.WriteString(fmt.Sprintf("Found %d files matching target=\"%s\":\n\n", len(results), target))
	} else {
		b.WriteString(fmt.Sprintf("Found %d files matching content=\"%s\":\n\n", len(results), content))
	}
	for _, fi := range results {
		b.WriteString(fmt.Sprintf("  %s  (%s)\n", fi.Path, fi.Language))
		if fi.Summary != "" {
			b.WriteString(fmt.Sprintf("      %s\n", fi.Summary))
		}
	}
	return &types.ToolResult{Success: true, Output: b.String(), Tool: "query_codebase"}
}

// matchTargetGlob checks if a relative path matches a glob pattern.
// Supports ** (globstar) for recursive directory matching.
func matchTargetGlob(path, pattern string) bool {
	// Normalize separators
	path = strings.ReplaceAll(path, "\\", "/")
	pattern = strings.ReplaceAll(pattern, "\\", "/")

	if strings.Contains(pattern, "**") {
		prefix, suffix, _ := strings.Cut(pattern, "**")
		prefix = strings.TrimSuffix(prefix, "/")
		suffix = strings.TrimPrefix(suffix, "/")

		if prefix != "" {
			if !strings.HasPrefix(path, prefix+"/") && path != prefix {
				return false
			}
			path = strings.TrimPrefix(path, prefix+"/")
			path = strings.TrimPrefix(path, prefix)
		}

		if suffix == "" {
			return true
		}

		// Match suffix as glob against the remaining path
		// Try full remaining path, then each suffix segment
		if matched, _ := filepath.Match(suffix, path); matched {
			return true
		}
		parts := strings.Split(path, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			subPath := strings.Join(parts[i:], "/")
			if matched, _ := filepath.Match(suffix, subPath); matched {
				return true
			}
		}
		return false
	}

	// Standard glob (no **)
	matched, _ := filepath.Match(pattern, path)
	return matched
}

func init() {
	types.RegisterSimplify("query_codebase", types.SimpleAction("query_codebase"))
	types.RegisterSimplify("convert", types.SimpleAction("convert"))
}

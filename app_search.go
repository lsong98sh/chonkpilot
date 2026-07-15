//go:build windows
// +build windows

package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"go.uber.org/zap"
)

// SearchResult represents a single file search result.
type SearchResult struct {
	Path      string `json:"path"`
	MatchType string `json:"matchType"` // "filename", "path", "symbol", "content"
	Snippet   string `json:"snippet"`
	Line      int    `json:"line,omitempty"`
}

// SearchProjectFiles searches files in the work directory.
// If codebase index is enabled, searches via codeindex DB (filenames, symbols).
// Otherwise, searches by file path using filepath.Walk.
func (a *App) SearchProjectFiles(query string) []SearchResult {
	if query = strings.TrimSpace(query); query == "" {
		return nil
	}

	// Check if codebase index is enabled
	enabled := ""
	db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		_ = sqlDB.QueryRow(`SELECT value FROM config WHERE key='codebase_index.enabled'`).Scan(&enabled)
		return nil
	})

	if enabled == "true" {
		results := a.searchWithCodeIndex(query)
		// Code index only covers indexed extensions; supplement with walk for
		// unindexed files (e.g. extensionless files like "env_report").
		walkResults := a.searchByWalk(query)
		seen := make(map[string]bool, len(results))
		for _, r := range results {
			seen[r.Path] = true
		}
		for _, r := range walkResults {
			if !seen[r.Path] {
				results = append(results, r)
				seen[r.Path] = true
			}
			if len(results) >= 15 {
				break
			}
		}
		if len(results) > 15 {
			results = results[:15]
		}
		return results
	}
	return a.searchByWalk(query)
}

func (a *App) searchWithCodeIndex(query string) []SearchResult {
	codebaseDB, err := codeindex.OpenCodebaseDB(a.workDir)
	if err != nil {
		a.logger.Warn("searchWithCodeIndex: cannot open codebase.db", zap.Error(err))
		return a.searchByWalk(query) // fallback
	}
	defer codebaseDB.Close()

	seen := make(map[string]bool)
	var results []SearchResult

	like := "%" + strings.ToLower(query) + "%"

	// Search by file path
	rows, err := codebaseDB.Query(
		`SELECT path, summary FROM files WHERE LOWER(path) LIKE ? LIMIT 10`, like)
	if err == nil {
		for rows.Next() {
			var path, summary string
			rows.Scan(&path, &summary)
			absPath := filepath.Join(a.workDir, path)
			if seen[absPath] {
				continue
			}
			seen[absPath] = true
			mt := "path"
			name := filepath.Base(path)
			if strings.Contains(strings.ToLower(name), strings.ToLower(query)) {
				mt = "filename"
			}
			snippet := summary
			if len(snippet) > 120 {
				snippet = snippet[:120] + "..."
			}
			results = append(results, SearchResult{
				Path:      absPath,
				MatchType: mt,
				Snippet:   snippet,
			})
		}
		rows.Close()
	}

	// Search by symbol name (type names, method names, function names)
	symRows, err := codebaseDB.Query(
		`SELECT s.name, s.kind, s.file_path FROM symbols s
		 WHERE LOWER(s.name) LIKE ? ORDER BY s.file_path LIMIT 15`, like)
	if err == nil {
		for symRows.Next() {
			var name, kind, filePath string
			symRows.Scan(&name, &kind, &filePath)
			absPath := filepath.Join(a.workDir, filePath)
			if seen[absPath] {
				continue
			}
			seen[absPath] = true
			results = append(results, SearchResult{
				Path:      absPath,
				MatchType: "symbol",
				Snippet:   fmt.Sprintf("%s: %s", kind, name),
			})
			if len(results) >= 15 {
				break
			}
		}
		symRows.Close()
	}

	if len(results) > 15 {
		results = results[:15]
	}
	return results
}

// VCSInfo describes version control systems detected in the work directory.
type VCSInfo struct {
	Git bool `json:"git"`
	Svn bool `json:"svn"`
}

// GetVCSInfo checks for .git and .svn directories in the work directory.
func (a *App) GetVCSInfo() VCSInfo {
	info := VCSInfo{}
	if _, err := os.Stat(filepath.Join(a.workDir, ".git")); err == nil {
		info.Git = true
	}
	if _, err := os.Stat(filepath.Join(a.workDir, ".svn")); err == nil {
		info.Svn = true
	}
	return info
}

func (a *App) searchByWalk(query string) []SearchResult {
	lower := strings.ToLower(query)
	var results []SearchResult

	filepath.Walk(a.workDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(a.workDir, path)
		if !strings.Contains(strings.ToLower(rel), lower) {
			return nil
		}
		mt := "path"
		if strings.Contains(strings.ToLower(fi.Name()), lower) {
			mt = "filename"
		}
		results = append(results, SearchResult{
			Path:      path,
			MatchType: mt,
			Snippet:   rel,
		})
		if len(results) >= 15 {
			return filepath.SkipAll
		}
		return nil
	})

	return results
}

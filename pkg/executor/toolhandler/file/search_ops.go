package file

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// ContainsGlobstar returns true if pattern contains ** (globstar).
func ContainsGlobstar(pattern string) bool {
	return strings.Contains(pattern, "**")
}

// matchGlobstarSuffix checks whether rel (relative path from walk root) matches
// the suffix after ** in a glob pattern. The suffix is treated as a glob pattern
// itself (e.g. "*.md" matches "readme.md", "docs/readme.md").
func matchGlobstarSuffix(rel, suffix string) bool {
	if suffix == "" {
		return true
	}
	suffix = strings.ReplaceAll(suffix, "\\", "/")
	rel = strings.ReplaceAll(rel, "\\", "/")

	// Extract basename of rel for basename-pattern matching
	base := rel
	if idx := strings.LastIndex(rel, "/"); idx >= 0 {
		base = rel[idx+1:]
	}

	// Match basename against suffix glob (handles **/*.md, **/foo.go, etc.)
	if matched, _ := filepath.Match(suffix, base); matched {
		return true
	}

	// Try full rel as suffix (handles **/sub/*.md matching "a/sub/readme.md")
	if strings.HasSuffix(rel, "/"+suffix) {
		return true
	}

	// Try suffix as a path-prefix pattern of rel (handles **/test/*.go matching "test/main.go")
	if matched, _ := filepath.Match(suffix, rel); matched {
		return true
	}

	return false
}

// searchWalkGlob walks from root and collects all files that match the globstar pattern.
func searchWalkGlob(root string, pattern string) ([]string, error) {
	// Split at first **
	prefix, suffix, _ := strings.Cut(pattern, "**")
	prefix = strings.TrimSuffix(prefix, "/")
	prefix = strings.TrimSuffix(prefix, "\\")
	suffix = strings.TrimPrefix(suffix, "/")
	suffix = strings.TrimPrefix(suffix, "\\")

	// Determine walk root
	walkRoot := filepath.Join(root, prefix)

	var matches []string
	err := filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if SkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(walkRoot, path)
		if err != nil || rel == "" {
			return nil
		}
		if matchGlobstarSuffix(rel, suffix) {
			matches = append(matches, path)
		}
		return nil
	})
	return matches, err
}

// ──────── Grep ────────

// HandleGrep searches file contents using a regular expression.
func HandleGrep(workDir string, args map[string]interface{}) *types.ToolResult {
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return &types.ToolResult{Success: false, Error: "parameter 'pattern' (regex string) is required", Tool: "grep"}
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("invalid regex: %s", err.Error()), Tool: "grep"}
	}

	// Optional file name glob pattern(s) — comma-separated for multiple patterns
	filePatterns := parseFilePatterns(args)

	// Optional path prefix filter (subdir)
	pathPrefix, _ := args["path"].(string)
	if pathPrefix != "" && !filepath.IsAbs(pathPrefix) {
		pathPrefix = filepath.Join(workDir, pathPrefix)
	} else if pathPrefix == "" {
		pathPrefix = workDir
	}

	// LLM can also set limit; cap at 1000
	maxMatches := 200
	if v, ok := args["limit"].(float64); ok {
		limit := int(v)
		if limit < 0 {
			m := &types.ToolResult{Success: false, Error: "limit must be non-negative", Tool: "grep"}
			return m
		}
		maxMatches = limit
		if maxMatches == 0 {
			maxMatches = 200
		}
		if maxMatches > 1000 {
			maxMatches = 1000
		}
	}

	type match struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Content string `json:"content"`
	}

	var matches []match

	walkFn := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && SkipDirs[info.Name()] {
			return filepath.SkipDir
		}
		if info.IsDir() {
			return nil
		}
		if maxMatches > 0 && len(matches) >= maxMatches {
			return filepath.SkipDir
		}

		// Apply file glob pattern(s) if provided
		if len(filePatterns) > 0 {
			matched := false
			for _, fp := range filePatterns {
				if m, _ := filepath.Match(fp, filepath.Base(path)); m {
					matched = true
					break
				}
			}
			if !matched {
				return nil
			}
		}

		relPath, _ := filepath.Rel(workDir, path)
		if relPath == "" {
			relPath = path
		}

		// Open file and stream line by line
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		// Binary detection: check first 1024 bytes for null byte
		header := make([]byte, 1024)
		n, _ := io.ReadFull(f, header)
		if n > 0 {
			if bytes.IndexByte(header[:n], 0) >= 0 {
				return nil
			}
		}
		// Seek back to beginning for scanner
		if _, err := f.Seek(0, 0); err != nil {
			return nil
		}

		scanner := bufio.NewScanner(f)
		// Increase buffer for long lines (default 64KB, use 1MB)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if maxMatches > 0 && len(matches) >= maxMatches {
				break
			}
			line := scanner.Text()
			if re.MatchString(line) {
				matches = append(matches, match{
					File:    relPath,
					Line:    lineNum,
					Content: strings.TrimSpace(line),
				})
			}
		}
		return nil
	}

	_ = filepath.Walk(pathPrefix, walkFn)

	if len(matches) == 0 {
		return &types.ToolResult{Success: true, Output: "no matches found", Tool: "grep"}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Found %d matches:\n\n", len(matches))
	for _, m := range matches {
		fmt.Fprintf(&out, "%s:%d:\t%s\n", m.File, m.Line, m.Content)
	}

	return &types.ToolResult{Success: true, Output: out.String(), Tool: "grep"}
}

// parseFilePatterns extracts file_pattern from args. Supports comma-separated string patterns.
func parseFilePatterns(args map[string]interface{}) []string {
	fp, _ := args["file_pattern"].(string)
	if fp == "" {
		return nil
	}
	var patterns []string
	for _, p := range strings.Split(fp, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			patterns = append(patterns, p)
		}
	}
	return patterns
}

var fetchTimeout = 300 * time.Second

// SetFetchTimeout sets the HTTP fetch timeout in seconds (clamped to >= 10).
func SetFetchTimeout(seconds int) {
	if seconds < 10 {
		seconds = 10
	}
	fetchTimeout = time.Duration(seconds) * time.Second
}

// ──────── Fetch ────────

// HandleFetch performs an HTTP request and writes output to a file if body exceeds threshold.
func HandleFetch(workDir string, args map[string]interface{}, config FetchConfig) *types.ToolResult {
	url, ok := args["url"].(string)
	if !ok || url == "" {
		return &types.ToolResult{Success: false, Error: "parameter 'url' is required", Tool: "fetch"}
	}

	method := "GET"
	if m, ok := args["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	var bodyReader io.Reader
	if bodyStr, ok := args["body"].(string); ok && bodyStr != "" {
		bodyReader = bytes.NewReader([]byte(bodyStr))
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create request: %s", err.Error()),
			Tool:    "fetch",
		}
	}

	if headers, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if vs, ok := v.(string); ok {
				req.Header.Set(k, vs)
			}
		}
	}

	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "ChonkPilot/1.0")
	}
	if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	maxBodySize := config.MaxBodySize
	if maxBodySize <= 0 {
		maxBodySize = 100 * 1024 // default 100KB
	}

	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("request failed: %s", err.Error()),
			Tool:    "fetch",
		}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read response: %s", err.Error()),
			Tool:    "fetch",
		}
	}

	var headerBuf strings.Builder
	for k, vals := range resp.Header {
		for _, v := range vals {
			headerBuf.WriteString(fmt.Sprintf("%s: %s\n", k, v))
		}
	}
	headers := strings.TrimSpace(headerBuf.String())
	bodyStr := string(respBody)

	// Always write body to file (response is retained in output for small responses)
	outputDir := filepath.Join(workDir, ".ide", "tmp")
	if err := os.MkdirAll(outputDir, 0755); err == nil {
		outputPath := filepath.Join(outputDir, fmt.Sprintf("fetch_%d_output.txt", time.Now().UnixMilli()))
		os.WriteFile(outputPath, respBody, 0644)

		if len(bodyStr) > maxBodySize {
			outputMsg := fmt.Sprintf("HTTP %d %s\n\n=== Headers ===\n%s\n\n=== Body (saved to file) ===\nResponse body (%d bytes) written to: %s",
				resp.StatusCode, resp.Status, headers, len(respBody), outputPath)
			return &types.ToolResult{
				Success: resp.StatusCode < 500,
				Output:  outputMsg,
				Tool:    "fetch",
				RawResult: map[string]interface{}{
					"status_code": resp.StatusCode,
					"content_type": resp.Header.Get("Content-Type"),
					"body_length":  len(respBody),
					"output_file":  outputPath,
				},
			}
		}
	}

	result := fmt.Sprintf("HTTP %d %s\n\n=== Headers ===\n%s\n\n=== Body ===\n%s", resp.StatusCode, resp.Status, headers, bodyStr)

	return &types.ToolResult{
		Success: resp.StatusCode < 500,
		Output:  result,
		Tool:    "fetch",
		RawResult: map[string]interface{}{
			"status_code": resp.StatusCode,
			"content_type": resp.Header.Get("Content-Type"),
			"body_length": len(respBody),
		},
	}
}

// FetchConfig holds configurable parameters for HTTP fetch.
type FetchConfig struct {
	MaxBodySize int // bytes; 0 = default 10KB (responses larger than this are written to .ide/tmp/)
}

// DefaultFetchConfig returns sensible defaults.
func DefaultFetchConfig() FetchConfig {
	return FetchConfig{
		MaxBodySize: 10 * 1024, // 10KB: inline small responses, write larger to file
	}
}

// ──────── Search/Glob Files ────────

// HandleSearchFiles searches for files by glob pattern.
// Supports ** (globstar) for recursive directory matching.
// Optional preview=N reads first N lines of each file with comment/content stats.
func HandleSearchFiles(workDir string, args map[string]interface{}) *types.ToolResult {
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return &types.ToolResult{Success: false, Error: "parameter 'pattern' (glob string) is required", Tool: "search_files"}
	}

	// Optional root directory
	root := workDir
	if r, ok := args["root"].(string); ok && r != "" {
		if filepath.IsAbs(r) {
			root = r
		} else {
			root = filepath.Join(workDir, r)
		}
	}

	// Optional preview lines
	preview := 0
	if v, ok := args["preview"].(float64); ok {
		preview = int(v)
		if preview < 0 {
			preview = 0
		}
		if preview > 50 {
			preview = 50
		}
	}

	var matches []string
	var err error

	if ContainsGlobstar(pattern) {
		matches, err = searchWalkGlob(root, pattern)
	} else {
		matches, err = filepath.Glob(filepath.Join(root, pattern))
	}

	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("invalid glob: %s", err.Error()), Tool: "search_files"}
	}

	if len(matches) == 0 {
		return &types.ToolResult{Success: true, Output: "no files match the pattern", Tool: "search_files"}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Found %d files:\n", len(matches))

	if preview > 0 {
		out.WriteString("\n")
	}

	for _, m := range matches {
		rel, _ := filepath.Rel(root, m)
		out.WriteString("  " + rel + "\n")

		if preview > 0 {
			previewContent(m, &out, preview)
		}
	}

	return &types.ToolResult{Success: true, Output: out.String(), Tool: "search_files"}
}

// previewContent reads first N lines of a file and writes a formatted preview to out.
func previewContent(path string, out *strings.Builder, n int) {
	f, err := os.Open(path)
	if err != nil {
		out.WriteString("    (unable to read)\n")
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for i := 0; i < n && scanner.Scan(); i++ {
		line := scanner.Text()
		// Truncate long lines for display
		display := line
		if len(display) > 120 {
			display = display[:120] + "..."
		}
		out.WriteString("    " + display + "\n")
	}
}

// HandleGlobFiles is an alias to HandleSearchFiles for compatibility.
var HandleGlobFiles = HandleSearchFiles

// HandleListDirectory lists files and subdirectories in a directory.
func HandleListDirectory(workDir string, args map[string]interface{}) *types.ToolResult {
	raw, ok := args["paths"]
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   "arguments must be a JSON array",
			Tool:    "list_directory",
		}
	}
	rawPaths, ok := raw.([]interface{})
	if !ok || len(rawPaths) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "expected a non-empty array of path strings",
			Tool:    "list_directory",
		}
	}

	var outputs []string
	var errs []string

	for _, raw := range rawPaths {
		p, ok := raw.(string)
		if !ok || p == "" {
			errs = append(errs, "invalid path (must be non-empty string)")
			continue
		}

		dir := p
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(workDir, dir)
		}
		dir = filepath.Clean(dir)

		entries, err := os.ReadDir(dir)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", p, err))
			continue
		}

		var buf strings.Builder
		for _, e := range entries {
			prefix := ""
			if e.IsDir() {
				prefix = "[dir] "
			}
			info, _ := e.Info()
			if info != nil {
				fmt.Fprintf(&buf, "%s%s (%d bytes)\n", prefix, e.Name(), info.Size())
			} else {
				fmt.Fprintf(&buf, "%s%s\n", prefix, e.Name())
			}
		}
		outputs = append(outputs, fmt.Sprintf("=== %s ===\n%s", p, strings.TrimSpace(buf.String())))
	}

	if len(errs) > 0 {
		result := strings.Join(outputs, "\n\n")
		if result != "" {
			result += "\n\n"
		}
		result += "errors:\n" + strings.Join(errs, "\n")
		return &types.ToolResult{
			Success: false,
			Output:  result,
			Tool:    "list_directory",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(outputs, "\n\n"),
		Tool:    "list_directory",
	}
}

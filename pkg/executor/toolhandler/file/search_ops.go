package file

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// Package-level config — set by executor at startup from UserConfig.
var (
	GrepMaxResults        int = 200  // default max grep matches
	cancelCtx             context.Context // cancellation context, set by Handler.PropagateConfig
)

// SetCancelCtx sets the cancellation context for long-running operations.
func SetCancelCtx(ctx context.Context) {
	cancelCtx = ctx
}

// checkCancel returns the cancellation error if the context has been cancelled,
// or nil if the context is still active or not set.
func checkCancel() error {
	if cancelCtx != nil {
		select {
		case <-cancelCtx.Done():
			return cancelCtx.Err()
		default:
		}
	}
	return nil
}

var (
	GrepMaxResultsCap     int = 1000 // hard cap
	FetchDownloadMaxBytes int = 100 * 1024 // max HTTP fetch download size (100KB)
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
		// Check cancellation every entry
		if err := checkCancel(); err != nil {
			return err
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

// grepMatch represents a single grep match result.
type grepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// clampMaxMatches clamps a limit to valid range [1, GrepMaxResultsCap] with default GrepMaxResults.
func clampMaxMatches(limit int) int {
	if limit < 0 {
		return GrepMaxResults
	}
	if limit == 0 {
		return GrepMaxResults
	}
	if limit > GrepMaxResultsCap {
		return GrepMaxResultsCap
	}
	return limit
}

// ──────── Grep ────────

// HandleGrep searches file contents using a regular expression.
// Supports only_files (return file paths only), context (lines before/after each match),
// file_pattern (glob), path (subdirectory limit), limit/max_matches (max results).
func HandleGrep(workDir string, args map[string]interface{}) *types.ToolResult {
	pattern, _ := args["pattern"].(string)
	onlyFiles, _ := args["only_files"].(bool)

	// Pattern is required when not in only_files mode
	if pattern == "" && !onlyFiles {
		return &types.ToolResult{
			Success: false,
			Error:   "parameter 'pattern' (regex string) is required when only_files is false",
			Tool:    "grep",
			Output:  "❌ 缺少 pattern 参数",
			RawResult: map[string]interface{}{
				"error": "parameter 'pattern' is required when only_files is false",
			},
		}
	}

	// Context lines before/after each match
	contextLines := 0
	if v, ok := args["context"].(float64); ok {
		contextLines = int(v)
		if contextLines < 0 {
			contextLines = 0
		}
	}

	var re *regexp.Regexp
	var reErr error
	if !onlyFiles && pattern != "" {
		re, reErr = regexp.Compile(pattern)
		if reErr != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("invalid regex: %s", reErr.Error()),
				Tool:    "grep",
				Output:  "❌ 无效的正则表达式",
				RawResult: map[string]interface{}{
					"error": reErr.Error(),
				},
			}
		}
	}

	// Optional file name glob pattern(s) — comma-separated for multiple patterns
	filePatterns := parseFilePatterns(args)

	// In only_files mode: if no file_pattern but pattern is set, use pattern as file glob
	if onlyFiles && len(filePatterns) == 0 && pattern != "" {
		filePatterns = []string{pattern}
	}

	// Optional path prefix filter (subdir)
	pathPrefix, _ := args["path"].(string)
	if pathPrefix != "" {
		resolved, errMsg := resolveReadPath(pathPrefix, workDir)
		if errMsg != "" {
			return &types.ToolResult{
				Success: false,
				Error:   errMsg,
				Tool:    "grep",
				Output:  "❌ 路径解析错误",
				RawResult: map[string]interface{}{
					"error": errMsg,
				},
			}
		}
		pathPrefix = resolved
	} else {
		pathPrefix = workDir
	}

	// LLM can also set limit; cap at GrepMaxResultsCap
	maxMatches := GrepMaxResults
	if v, ok := args["limit"].(float64); ok {
		maxMatches = clampMaxMatches(int(v))
	} else if v, ok := args["limit"].(int); ok {
		maxMatches = clampMaxMatches(v)
	}
	// max_matches is an alias for limit (supports int from Go map literals and float64 from JSON)
	if v, ok := args["max_matches"].(float64); ok {
		maxMatches = clampMaxMatches(int(v))
	} else if v, ok := args["max_matches"].(int); ok {
		maxMatches = clampMaxMatches(v)
	}

	var matches []grepMatch

	walkFn := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Check cancellation every directory entry
		if err := checkCancel(); err != nil {
			return err
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

		if onlyFiles {
			// only_files mode: just collect matching file paths (filePatterns already handles glob)
			matches = append(matches, grepMatch{File: relPath})
			return nil
		}

		// Open file and detect binary
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

		if contextLines > 0 && re != nil {
			// Context mode: read all lines, find matches, expand to context ranges
			fileMatches := grepWithContext(f, re, contextLines, maxMatches)
			for _, m := range fileMatches {
				m.File = relPath
				matches = append(matches, m)
			}
			return nil
		}

		// Standard streaming mode (no context)
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if lineNum%500 == 0 {
				if err := checkCancel(); err != nil {
					return err
				}
			}
			if maxMatches > 0 && len(matches) >= maxMatches {
				break
			}
			line := scanner.Text()
			if re != nil && re.MatchString(line) {
				matches = append(matches, grepMatch{
					File:    relPath,
					Line:    lineNum,
					Content: strings.TrimSpace(line),
				})
			}
		}
		return nil
	}

	walkErr := filepath.Walk(pathPrefix, walkFn)

	// Check if cancelled during walk
	if errors.Is(walkErr, context.Canceled) {
		return &types.ToolResult{
			Success:   true,
			Output:    fmt.Sprintf("⚠️ grep 已取消。找到 %d 个部分匹配。", len(matches)),
			Tool:      "grep",
			RawResult: map[string]interface{}{"matches": matches, "cancelled": true, "count": len(matches)},
		}
	}

	if len(matches) == 0 {
		return &types.ToolResult{
			Success:   true,
			Output:    "⚠️ 未找到匹配",
			Tool:      "grep",
			RawResult: map[string]interface{}{"matches": []grepMatch{}, "count": 0},
		}
	}

	var out strings.Builder
	if onlyFiles {
		fmt.Fprintf(&out, "Found %d files:\n", len(matches))
		for _, m := range matches {
			fmt.Fprintf(&out, "  %s\n", m.File)
		}
	} else if contextLines > 0 {
		// Context output: group by file, show ranges with line numbers
		out.WriteString(formatContextMatches(matches, contextLines))
	} else {
		fmt.Fprintf(&out, "Found %d matches:\n\n", len(matches))
		for _, m := range matches {
			fmt.Fprintf(&out, "%s:%d:\t%s\n", m.File, m.Line, m.Content)
		}
	}

	// Build raw matches for AI consumption
	var rawMatches []map[string]interface{}
	for _, m := range matches {
		rawMatches = append(rawMatches, map[string]interface{}{
			"file":    m.File,
			"line":    m.Line,
			"content": m.Content,
		})
	}

	emoji := "✅"
	summary := fmt.Sprintf("grep 搜索完成：找到 %d 个匹配", len(matches))
	output := fmt.Sprintf("%s %s\n\n%s", emoji, summary, out.String())

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Tool:    "grep",
		RawResult: map[string]interface{}{
			"matches": rawMatches,
			"count":   len(matches),
		},
	}
}

// fileContainsPattern checks if a file contains at least one match for the given regex.
func fileContainsPattern(path string, re *regexp.Regexp) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Binary detection
	header := make([]byte, 1024)
	n, _ := io.ReadFull(f, header)
	if n > 0 {
		if bytes.IndexByte(header[:n], 0) >= 0 {
			return false
		}
	}
	if _, err := f.Seek(0, 0); err != nil {
		return false
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		if re.MatchString(scanner.Text()) {
			return true
		}
	}
	return false
}

// grepWithContext reads a file, finds all regex matches, and returns matches with
// surrounding context lines. Overlapping/adjacent context ranges are merged.
func grepWithContext(f *os.File, re *regexp.Regexp, context, maxMatches int) []grepMatch {
	// Read all lines into memory
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		if err := checkCancel(); err != nil {
			break
		}
		lines = append(lines, scanner.Text())
	}

	// Find all matching line numbers
	var matchLines []int
	for i, line := range lines {
		if re.MatchString(line) {
			matchLines = append(matchLines, i+1) // 1-based
			if maxMatches > 0 && len(matchLines) >= maxMatches {
				break
			}
		}
	}

	if len(matchLines) == 0 {
		return nil
	}

	// Expand to context ranges and merge overlaps
	type intRange struct{ start, end int }
	var ranges []intRange
	for _, ml := range matchLines {
		start := ml - context
		if start < 1 {
			start = 1
		}
		end := ml + context
		if end > len(lines) {
			end = len(lines)
		}
		if len(ranges) == 0 {
			ranges = append(ranges, intRange{start, end})
			continue
		}
		// Merge if overlapping or adjacent (gap <= 1)
		last := &ranges[len(ranges)-1]
		if start <= last.end+1 {
			if end > last.end {
				last.end = end
			}
		} else {
			ranges = append(ranges, intRange{start, end})
		}
	}

	// Build match entries for each range
	var matches []grepMatch
	for _, r := range ranges {
		var rangeContent strings.Builder
		for i := r.start - 1; i < r.end; i++ {
			rangeContent.WriteString(fmt.Sprintf("%d\t%s\n", i+1, lines[i]))
		}
		matches = append(matches, grepMatch{
			Line:    r.start,
			Content: strings.TrimRight(rangeContent.String(), "\n"),
		})
	}
	return matches
}

// formatContextMatches formats context-aware results grouped by file.
func formatContextMatches(matches []grepMatch, context int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d matches:\n", len(matches))
	currentFile := ""
	for _, m := range matches {
		if m.File != currentFile {
			if currentFile != "" {
				b.WriteString("\n")
			}
			currentFile = m.File
		}
		b.WriteString(fmt.Sprintf("%s:%d-%d:\n%s\n", m.File, m.Line, m.Line+strings.Count(m.Content, "\n"), m.Content))
	}
	return b.String()
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

// findFilesByGlob returns absolute paths of files matching the given glob pattern.
// Supports ** (globstar) for recursive matching. Used by replace and remove for file_pattern mode.
func findFilesByGlob(workDir, pattern string) ([]string, error) {
	var matches []string
	if ContainsGlobstar(pattern) {
		var err error
		matches, err = searchWalkGlob(workDir, pattern)
		if err != nil {
			return nil, err
		}
	} else {
		matches, err := filepath.Glob(filepath.Join(workDir, pattern))
		if err != nil {
			return nil, err
		}
		return matches, nil
	}
	return matches, nil
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

// getEncoding returns the encoding.Encoding for the given name string.
// Supported: utf-8, gbk, big5, shift_jis, euc-jp, euc-kr, iso-8859-1.
// Returns nil for utf-8 (no conversion needed).
func getEncoding(name string) encoding.Encoding {
	switch strings.ToLower(name) {
	case "gbk", "gb2312", "gb18030":
		return simplifiedchinese.GBK
	case "big5":
		return traditionalchinese.Big5
	case "shift_jis", "shift-jis", "sjis":
		return japanese.ShiftJIS
	case "euc-jp", "eucjp":
		return japanese.EUCJP
	case "euc-kr", "euckr":
		return korean.EUCKR
	case "iso-8859-1", "latin1":
		return charmap.ISO8859_1
	default:
		return nil // utf-8 or unknown, treat as utf-8
	}
}

// HandleFetch performs an HTTP request with enhanced capabilities: file download,
// multipart form upload, cookie support, timeout control, redirect control, and encoding.
func HandleFetch(workDir string, args map[string]interface{}, config FetchConfig) *types.ToolResult {
	url, ok := args["url"].(string)
	if !ok || url == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "parameter 'url' is required",
			Tool:    "fetch",
			Output:  "❌ 缺少 url 参数",
			RawResult: map[string]interface{}{
				"error": "parameter 'url' is required",
			},
		}
	}

	method := "GET"
	if m, ok := args["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	// ── resolve save_as path ──
	saveAs := ""
	if sa, ok := args["save_as"].(string); ok && sa != "" {
		if filepath.IsAbs(sa) {
			saveAs = sa
		} else {
			saveAs = filepath.Join(workDir, sa)
		}
	}

	// ── build request body ──
	var bodyReader io.Reader
	var contentType string

	// form/form_files: multipart/form-data
	if form, ok := args["form"].(map[string]interface{}); ok && len(form) > 0 {
		method = "POST"
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// text fields
		for k, v := range form {
			if vs, ok := v.(string); ok {
				writer.WriteField(k, vs)
			}
		}

		// file fields
		if formFiles, ok := args["form_files"].([]interface{}); ok {
			for _, f := range formFiles {
				fm, ok := f.(map[string]interface{})
				if !ok {
					continue
				}
				field, _ := fm["field"].(string)
				fpath, _ := fm["path"].(string)
				if field == "" || fpath == "" {
					continue
				}
				absPath := fpath
				if !filepath.IsAbs(absPath) {
					absPath = filepath.Join(workDir, absPath)
				}
				file, err := os.Open(absPath)
				if err != nil {
					return &types.ToolResult{
						Success: false,
						Error:   fmt.Sprintf("failed to open form file %s: %s", fpath, err.Error()),
						Tool:    "fetch",
						Output:  fmt.Sprintf("❌ 无法打开表单文件 %s", fpath),
						RawResult: map[string]interface{}{
							"error": fmt.Sprintf("failed to open form file %s: %s", fpath, err.Error()),
						},
					}
				}
				part, err := writer.CreateFormFile(field, filepath.Base(absPath))
				if err != nil {
					file.Close()
					return &types.ToolResult{
						Success: false,
						Error:   fmt.Sprintf("failed to create form file %s: %s", fpath, err.Error()),
						Tool:    "fetch",
						Output:  fmt.Sprintf("❌ 无法创建表单文件字段 %s", fpath),
						RawResult: map[string]interface{}{
							"error": fmt.Sprintf("failed to create form file %s: %s", fpath, err.Error()),
						},
					}
				}
				io.Copy(part, file)
				file.Close()
			}
		}

		writer.Close()
		bodyReader = body
		contentType = writer.FormDataContentType()
	} else if bodyStr, ok := args["body"].(string); ok && bodyStr != "" {
		bodyReader = bytes.NewReader([]byte(bodyStr))
	}

	// ── build context with timeout ──
	timeoutSec := 30
	if t, ok := args["timeout"].(float64); ok && t >= 10 {
		timeoutSec = int(t)
	} else if t, ok := args["timeout"].(int); ok && t >= 10 {
		timeoutSec = t
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create request: %s", err.Error()),
			Tool:    "fetch",
			Output:  "❌ 创建 HTTP 请求失败",
			RawResult: map[string]interface{}{
				"error": err.Error(),
			},
		}
	}

	// ── headers ──
	if headers, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if vs, ok := v.(string); ok {
				req.Header.Set(k, vs)
			}
		}
	}

	// ── cookies ──
	if cookies, ok := args["cookies"].(map[string]interface{}); ok && len(cookies) > 0 {
		var cookieParts []string
		for k, v := range cookies {
			if vs, ok := v.(string); ok {
				cookieParts = append(cookieParts, fmt.Sprintf("%s=%s", k, vs))
			}
		}
		if len(cookieParts) > 0 {
			req.Header.Set("Cookie", strings.Join(cookieParts, "; "))
		}
	}

	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "ChonkPilot/1.0")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	maxBodySize := config.MaxBodySize
	if maxBodySize <= 0 {
		maxBodySize = 100 * 1024 // default 100KB
	}

	// ── build HTTP client ──
	followRedirect := true
	if fr, ok := args["follow_redirect"].(bool); ok {
		followRedirect = fr
	}

	client := &http.Client{}
	if !followRedirect {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		// Check for context deadline exceeded (timeout)
		if errors.Is(err, context.DeadlineExceeded) {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("request timed out after %ds", timeoutSec),
				Tool:    "fetch",
				Output:  fmt.Sprintf("❌ 请求超时（%ds）", timeoutSec),
				RawResult: map[string]interface{}{
					"error":      fmt.Sprintf("request timed out after %ds", timeoutSec),
					"timeout":    timeoutSec,
					"status":     "timeout",
				},
			}
		}
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("request failed: %s", err.Error()),
			Tool:    "fetch",
			Output:  "❌ HTTP 请求失败",
			RawResult: map[string]interface{}{
				"error": err.Error(),
			},
		}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read response: %s", err.Error()),
			Tool:    "fetch",
			Output:  "❌ 读取响应体失败",
			RawResult: map[string]interface{}{
				"error": err.Error(),
			},
		}
	}

	// ── encoding conversion ──
	encName := "utf-8"
	if enc, ok := args["encoding"].(string); ok && enc != "" {
		encName = enc
	}
	if enc := getEncoding(encName); enc != nil {
		decoded, _, err := transform.String(enc.NewDecoder(), string(respBody))
		if err == nil {
			respBody = []byte(decoded)
		}
	}

	// ── save_as: download mode ──
	if saveAs != "" {
		dir := filepath.Dir(saveAs)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to create directory for save_as: %s", err.Error()),
				Tool:    "fetch",
				Output:  fmt.Sprintf("❌ 创建保存目录失败：%s", saveAs),
				RawResult: map[string]interface{}{
					"error":    err.Error(),
					"saved_to": saveAs,
				},
			}
		}
		if err := os.WriteFile(saveAs, respBody, 0644); err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to write save_as file: %s", err.Error()),
				Tool:    "fetch",
				Output:  fmt.Sprintf("❌ 写入文件失败：%s", saveAs),
				RawResult: map[string]interface{}{
					"error":    err.Error(),
					"saved_to": saveAs,
				},
			}
		}
		sizeMB := float64(len(respBody)) / (1024 * 1024)
		outputMsg := fmt.Sprintf("✅ 已下载到 %s (%.2fMB)，状态码 %d", saveAs, sizeMB, resp.StatusCode)
		return &types.ToolResult{
			Success: resp.StatusCode < 500,
			Output:  outputMsg,
			Tool:    "fetch",
			RawResult: map[string]interface{}{
				"status_code": resp.StatusCode,
				"content_type": resp.Header.Get("Content-Type"),
				"body_length":  len(respBody),
				"saved_to":     saveAs,
			},
		}
	}

	// ── normal mode: format response ──
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

// HandleListDirectory lists files and subdirectories in a directory.
func HandleListDirectory(workDir string, args map[string]interface{}) *types.ToolResult {
	raw, ok := args["paths"]
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   "arguments must be a JSON array",
			Tool:    "list_directory",
			Output:  "❌ 缺少 paths 参数",
			RawResult: map[string]interface{}{
				"error": "arguments must be a JSON array",
			},
		}
	}
	rawPaths, ok := raw.([]interface{})
	if !ok || len(rawPaths) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "expected a non-empty array of path strings",
			Tool:    "list_directory",
			Output:  "❌ paths 必须是非空数组",
			RawResult: map[string]interface{}{
				"error": "expected a non-empty array of path strings",
			},
		}
	}

	// Default recursive = true (v2 change: was false in v1)
	recursive := true
	if v, ok := args["recursive"].(bool); ok {
		recursive = v
	}

	typeFilter, _ := args["type"].(string) // "file", "dir", or "all" (default)
	sortBy, _ := args["sort"].(string)     // "name" (default), "size", or "date"

	// depth: recursion limit (0 = unlimited)
	depth := 0
	if d, ok := args["depth"].(float64); ok && d > 0 {
		depth = int(d)
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
		resolved, errMsg := resolveReadPath(dir, workDir)
		if errMsg != "" {
			errs = append(errs, fmt.Sprintf("%s: %s", p, errMsg))
			continue
		}
		dir = resolved

		if recursive {
			out, err := listDirectoryRecursive(dir, workDir, typeFilter, sortBy, depth)
			if errors.Is(err, context.Canceled) {
				outputs = append(outputs, fmt.Sprintf("=== %s ===\n(listing cancelled)", p))
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %s", p, err))
				continue
			}
			outputs = append(outputs, out)
		} else {
			entries, err := os.ReadDir(dir)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %s", p, err))
				continue
			}

			var buf strings.Builder
			// Single-level tree: render each entry as a tree leaf
			dirCount := 0
			fileCount := 0
			var totalSize int64
			for i, e := range entries {
				isLast := i == len(entries)-1
				connector := "├── "
				if isLast {
					connector = "└── "
				}
				info, _ := e.Info()
				var size int64
				if info != nil {
					size = info.Size()
					totalSize += size
				}
				if e.IsDir() {
					dirCount++
					fmt.Fprintf(&buf, "%s%s/ (%s)\n", connector, e.Name(), formatFileSize(size))
				} else {
					fileCount++
					fmt.Fprintf(&buf, "%s%s (%s)\n", connector, e.Name(), formatFileSize(size))
				}
			}
			stats := fmt.Sprintf("📋 目录列表 — %s\n=== 统计 ===\n  目录: %d 个，文件: %d 个，总计: %s\n=== 内容 ===\n%s",
				p, dirCount, fileCount, formatFileSize(totalSize), strings.TrimRight(buf.String(), "\n"))
			outputs = append(outputs, stats)
		}
	}

	if len(errs) > 0 {
		result := strings.Join(outputs, "\n\n")
		if result != "" {
			result += "\n\n"
		}
		result += "errors:\n" + strings.Join(errs, "\n")
		return &types.ToolResult{
			Success: false,
			Output:  fmt.Sprintf("❌ 目录列表存在错误（%d 个错误）\n\n%s", len(errs), result),
			Tool:    "list_directory",
			RawResult: map[string]interface{}{
				"paths":  rawPaths,
				"errors": errs,
			},
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("✅ 已列出 %d 个目录\n\n%s", len(rawPaths), strings.Join(outputs, "\n\n")),
		Tool:    "list_directory",
		RawResult: map[string]interface{}{
			"paths": rawPaths,
		},
	}
}

// listDirectoryRecursive walks dir recursively and returns a formatted listing.
// Output format: tree structure with 📋 prefix header, stats, and tree connectors.
// Stops after maxListDirEntries to prevent unbounded output.
const maxListDirEntries = 5000

func listDirectoryRecursive(dir, workDir, typeFilter, sortBy string, maxDepth int) (string, error) {
	var entries []fileEntry
	count := 0
	dirCount := 0
	fileCount := 0
	var totalSize int64

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if count%500 == 0 {
			if err := checkCancel(); err != nil {
				return err
			}
		}
		if path == dir {
			return nil
		}

		// Check depth limit
		rel, _ := filepath.Rel(dir, path)
		if maxDepth > 0 {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		if d.IsDir() && SkipDirs[d.Name()] {
			return filepath.SkipDir
		}

		// Apply type filter
		if typeFilter == "file" && d.IsDir() {
			return nil
		}
		if typeFilter == "dir" && !d.IsDir() {
			return nil
		}

		info, _ := d.Info()
		var size int64
		if info != nil {
			size = info.Size()
			totalSize += size
		}
		if d.IsDir() {
			dirCount++
		} else {
			fileCount++
		}
		entries = append(entries, fileEntry{
			name:  rel,
			isDir: d.IsDir(),
			size:  size,
			modTime: func() time.Time { if info != nil { return info.ModTime() }; return time.Time{} }(),
		})
		count++
		return nil
	})
	if err != nil {
		return "", err
	}

	// Sort entries
	switch sortBy {
	case "size":
		sort.Slice(entries, func(i, j int) bool { return entries[i].size < entries[j].size })
	case "date":
		sort.Slice(entries, func(i, j int) bool { return entries[i].modTime.After(entries[j].modTime) })
	case "type":
		sort.Slice(entries, func(i, j int) bool {
			extI := strings.ToLower(filepath.Ext(entries[i].name))
			extJ := strings.ToLower(filepath.Ext(entries[j].name))
			if extI != extJ {
				return extI < extJ
			}
			return strings.ToLower(entries[i].name) < strings.ToLower(entries[j].name)
		})
	default: // "name"
		sort.Slice(entries, func(i, j int) bool {
			ai := strings.ToLower(entries[i].name)
			aj := strings.ToLower(entries[j].name)
			return ai < aj
		})
	}

	// Build tree output
	var buf strings.Builder
	baseName := filepath.Base(dir)

	// Render tree using the relative paths from walk
	// Group by directory for tree rendering
	dirTree := buildDirTree(entries)
	renderTree(&buf, dirTree, "", baseName)

	limitNote := ""
	if count >= maxListDirEntries {
		limitNote = fmt.Sprintf("\n... (limit of %d entries reached)", maxListDirEntries)
	}

	content := strings.TrimRight(buf.String(), "\n") + limitNote
	stats := fmt.Sprintf("📋 目录列表 — %s\n=== 统计 ===\n  目录: %d 个，文件: %d 个，总计: %s\n=== 内容 ===\n%s",
		baseName, dirCount, fileCount, formatFileSize(totalSize), content)
	return stats, nil
}

// treeNode represents a single node in the directory tree.
type treeNode struct {
	name     string
	isDir    bool
	size     int64
	modTime  time.Time
	children []*treeNode
}

// buildDirTree converts a flat sorted entry list into a tree structure.
func buildDirTree(entries []fileEntry) *treeNode {
	root := &treeNode{name: "", isDir: true}

	for _, e := range entries {
		parts := strings.Split(e.name, string(filepath.Separator))
		node := root
		for i, part := range parts {
			isLast := i == len(parts)-1
			found := false
			for _, child := range node.children {
				if child.name == part {
					node = child
					found = true
					break
				}
			}
			if !found {
				child := &treeNode{name: part, isDir: !isLast || e.isDir, size: e.size, modTime: e.modTime}
				node.children = append(node.children, child)
				node = child
			}
		}
	}
	return root
}

// renderTree renders a treeNode structure as a tree-formatted string.
func renderTree(buf *strings.Builder, node *treeNode, prefix string, name string) {
	if name != "" {
		buf.WriteString(name)
		if node.isDir {
			buf.WriteString("/")
		}
		buf.WriteString("\n")
	}

	for i, child := range node.children {
		isLast := i == len(node.children)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}
		buf.WriteString(prefix + connector)
		buf.WriteString(child.name)
		if child.isDir {
			buf.WriteString("/")
		}
		buf.WriteString(fmt.Sprintf(" (%s)", formatFileSize(child.size)))
		if !child.modTime.IsZero() {
			buf.WriteString(fmt.Sprintf("  %s", child.modTime.Format("2006-01-02")))
		}
		buf.WriteString("\n")

		if len(child.children) > 0 {
			// Recurse into this child to render its grandchildren
			renderTree(buf, child, childPrefix, "")
		}
	}
}

type fileEntry struct {
	name    string
	isDir   bool
	size    int64
	modTime time.Time
}

func formatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	} else if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	} else {
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
}

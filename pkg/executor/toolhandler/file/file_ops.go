package file

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"github.com/chonkpilot/chonkpilot/pkg/executor/engine"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
	"github.com/sergi/go-diff/diffmatchpatch"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// SanitizePath resolves a user-provided path and ensures it stays within workDir.
func SanitizePath(userPath, workDir string) (string, string) {
	if userPath == "" {
		return "", "path is required"
	}
	resolved := userPath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workDir, resolved)
	}
	resolved = filepath.Clean(resolved)

	if !strings.HasPrefix(resolved, filepath.Clean(workDir)+string(filepath.Separator)) && resolved != filepath.Clean(workDir) {
		return "", fmt.Sprintf("path %s is outside workspace %s", userPath, workDir)
	}
	return resolved, ""
}

// SkipDirs are directories that grep and search_files skip by default.
var SkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".trae":        true,
	".chonkpilot":  true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"build":        true,
	"dist":         true,
	".next":        true,
	".nuxt":        true,
}

// ─── Project Security Checker ─────────────────────────────────

// securityChecker is the package-level security checker, set by Handler at startup.
// nil means no restrictions (backward compatible).
var securityChecker *SecurityChecker

// SetSecurityChecker sets the package-level security checker.
// Call once at startup before any tool invocation.
func SetSecurityChecker(sc *SecurityChecker) {
	securityChecker = sc
}

// SecurityEntry represents one project_security rule.
type SecurityEntry struct {
	Dir      string
	Writable bool
}

// SecurityChecker validates file operations against project security rules.
// If no entries are configured, all operations are allowed (backward compatible).
// If entries exist, read/write operations must target paths within allowed dirs.
// Use NewSecurityChecker to create from DB result entries.
type SecurityChecker struct {
	entries []SecurityEntry
}

// NewSecurityChecker creates a SecurityChecker from project_security entries.
// supply nil/empty entries for "allow all" mode.
// The workDir is always implicitly allowed for both read and write.
func NewSecurityChecker(entries []map[string]interface{}, workDir string) *SecurityChecker {
	sc := &SecurityChecker{
		entries: []SecurityEntry{
			{Dir: filepath.Clean(workDir), Writable: true}, // workDir 默认支持读写
		},
	}
	for _, e := range entries {
		if dir, ok := e["dir"].(string); ok && dir != "" {
			resolved := dir
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(workDir, resolved)
			}
			resolved = filepath.Clean(resolved)
			writable, _ := e["writable"].(bool)
			sc.entries = append(sc.entries, SecurityEntry{
				Dir:      resolved,
				Writable: writable,
			})
		}
	}
	return sc
}

// hasEntries returns true if any security rules are configured.
func (sc *SecurityChecker) hasEntries() bool {
	return len(sc.entries) > 0
}

// isAllowed checks if the resolved path falls within any allowed directory.
// If allowedDirs is empty, all paths are allowed.
func (sc *SecurityChecker) isAllowed(path string) bool {
	if !sc.hasEntries() {
		return true
	}
	cleanPath := filepath.Clean(path)
	for _, e := range sc.entries {
		// Check if path is inside this entry's directory
		if cleanPath == e.Dir || strings.HasPrefix(cleanPath, e.Dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// CanRead checks if a resolved path can be read.
func (sc *SecurityChecker) CanRead(path string) bool {
	return sc.isAllowed(path)
}

// CanWrite checks if a resolved path can be written.
func (sc *SecurityChecker) CanWrite(path string) bool {
	if !sc.hasEntries() {
		return true
	}
	cleanPath := filepath.Clean(path)
	for _, e := range sc.entries {
		if !e.Writable {
			continue
		}
		if cleanPath == e.Dir || strings.HasPrefix(cleanPath, e.Dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveReadPath sanitizes a user-provided path and checks project security for read.
// Returns resolved absolute path or empty string with error message.
func resolveReadPath(userPath, workDir string) (string, string) {
	resolved, errMsg := SanitizePath(userPath, workDir)
	if errMsg != "" {
		return "", errMsg
	}
	if securityChecker != nil && !securityChecker.CanRead(resolved) {
		return "", fmt.Sprintf("path %s is not allowed for read by project security rules", userPath)
	}
	return resolved, ""
}

// resolveWritePath sanitizes a user-provided path and checks project security for write.
// Returns resolved absolute path or empty string with error message.
func resolveWritePath(userPath, workDir string) (string, string) {
	resolved, errMsg := SanitizePath(userPath, workDir)
	if errMsg != "" {
		return "", errMsg
	}
	if securityChecker != nil && !securityChecker.CanWrite(resolved) {
		return "", fmt.Sprintf("path %s is not allowed for write by project security rules", userPath)
	}
	return resolved, ""
}

// ─── Language Detection ──────────────────────────────────────

// getLanguageFromExt maps a file extension to a human-readable language name.
func getLanguageFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".go":
		return "Go"
	case ".js", ".mjs", ".cjs":
		return "JavaScript"
	case ".ts":
		return "TypeScript"
	case ".jsx":
		return "JSX"
	case ".tsx":
		return "TSX"
	case ".py":
		return "Python"
	case ".java":
		return "Java"
	case ".rs":
		return "Rust"
	case ".c", ".h":
		return "C"
	case ".cpp", ".hpp", ".cc", ".cxx", ".hh":
		return "C++"
	case ".cs":
		return "C#"
	case ".rb":
		return "Ruby"
	case ".php":
		return "PHP"
	case ".swift":
		return "Swift"
	case ".kt", ".kts":
		return "Kotlin"
	case ".scala":
		return "Scala"
	case ".r", ".rdata", ".rmd":
		return "R"
	case ".m", ".mm":
		return "Objective-C"
	case ".pl", ".pm":
		return "Perl"
	case ".lua":
		return "Lua"
	case ".hs":
		return "Haskell"
	case ".clj", ".cljs", ".cljc":
		return "Clojure"
	case ".ex", ".exs":
		return "Elixir"
	case ".erl", ".hrl":
		return "Erlang"
	case ".dart":
		return "Dart"
	case ".zig":
		return "Zig"
	case ".sh", ".bash", ".zsh":
		return "Shell"
	case ".ps1":
		return "PowerShell"
	case ".bat", ".cmd":
		return "Batch"
	case ".sql":
		return "SQL"
	case ".html", ".htm":
		return "HTML"
	case ".css", ".scss", ".sass", ".less":
		return "CSS"
	case ".json":
		return "JSON"
	case ".xml", ".xsl", ".xsd", ".xslt":
		return "XML"
	case ".yaml", ".yml":
		return "YAML"
	case ".md", ".markdown":
		return "Markdown"
	case ".toml":
		return "TOML"
	case ".ini", ".cfg", ".conf":
		return "INI"
	case ".vue":
		return "Vue"
	case ".svelte":
		return "Svelte"
	case ".dockerfile":
		return "Dockerfile"
	case ".makefile", ".mk":
		return "Makefile"
	case ".gradle":
		return "Gradle"
	case ".proto":
		return "Protobuf"
	case ".tex":
		return "LaTeX"
	case ".dockerignore", ".gitignore", ".npmignore", ".eslintignore":
		return "Ignore"
	default:
		return "Unknown"
	}
}

// ─── Encoding ────────────────────────────────────────────────

// readWithEncoding decodes byte data using the specified encoding name.
// Supported names: "auto", "utf-8", "utf8", "gbk", "gb2312", "gb18030",
// "shift-jis", "shift_jis", "euc-jp", "big5", "euc-kr".
func readWithEncoding(data []byte, encName string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(encName))
	if name == "" || name == "auto" {
		return detectAndDecode(data)
	}

	var enc encoding.Encoding
	switch name {
	case "utf-8", "utf8":
		return string(data), nil
	case "gbk", "gb2312", "cp936":
		enc = simplifiedchinese.GBK
	case "gb18030":
		enc = simplifiedchinese.GB18030
	case "shift-jis", "shift_jis", "cp932":
		enc = japanese.ShiftJIS
	case "euc-jp":
		enc = japanese.EUCJP
	case "big5", "cp950":
		enc = traditionalchinese.Big5
	case "euc-kr":
		enc = korean.EUCKR
	default:
		// Unknown encoding, fall back to raw string
		return string(data), nil
	}

	decoder := enc.NewDecoder()
	result, _, err := transform.String(decoder, string(data))
	if err != nil {
		return string(data), nil // fallback on error
	}
	return result, nil
}

// detectAndDecode attempts to detect encoding and decode.
func detectAndDecode(data []byte) (string, error) {
	if utf8.Valid(data) {
		return string(data), nil
	}
	// Try GBK first (common for Chinese environments)
	decoder := simplifiedchinese.GBK.NewDecoder()
	result, _, err := transform.String(decoder, string(data))
	if err == nil && hasVisibleContent(result) {
		return result, nil
	}
	// Try Shift-JIS
	decoder = japanese.ShiftJIS.NewDecoder()
	result, _, err = transform.String(decoder, string(data))
	if err == nil && hasVisibleContent(result) {
		return result, nil
	}
	// Fall back to raw string
	return string(data), nil
}

// hasVisibleContent checks if a string has meaningful content beyond replacement characters.
func hasVisibleContent(s string) bool {
	return strings.Count(s, string(utf8.RuneError)) < len(s)/2
}

// ─── Read File (v2.0) ────────────────────────────────────────

// fileReq represents a single file read request with all supported options.
type fileReq struct {
	path        string
	start       int
	limit       int
	lineNumbers bool
	tail        int
	encoding    string
	infoOnly    bool
	ranges      [][2]int
}

// HandleReadFile reads the contents of one or more files.
func HandleReadFile(workDir string, args map[string]interface{}) *types.ToolResult {
	raw, ok := args["files"]
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   "arguments must be a JSON array",
			Tool:    "read_file",
		}
	}
	rawFiles, ok := raw.([]interface{})
	if !ok || len(rawFiles) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "expected a non-empty array of file objects",
			Tool:    "read_file",
		}
	}

	var reqs []fileReq
	for i, raw := range rawFiles {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("[%d]: expected object", i),
				Tool:    "read_file",
			}
		}
		path, _ := m["path"].(string)
		if path == "" {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("[%d]: path is required", i),
				Tool:    "read_file",
			}
		}

		req := fileReq{path: path}

		if start, ok := m["start"].(float64); ok {
			req.start = int(start)
		}
		if limit, ok := m["limit"].(float64); ok {
			req.limit = int(limit)
		}
		if lineNumbers, ok := m["line_numbers"].(bool); ok {
			req.lineNumbers = lineNumbers
		}
		if tail, ok := m["tail"].(float64); ok {
			req.tail = int(tail)
		}
		if enc, ok := m["encoding"].(string); ok {
			req.encoding = enc
		}
		if info, ok := m["info"].(bool); ok {
			req.infoOnly = info
		}
		if rawRanges, ok := m["ranges"].([]interface{}); ok {
			for _, rawRange := range rawRanges {
				if r, ok := rawRange.([]interface{}); ok && len(r) == 2 {
					s, _ := r[0].(float64)
					e, _ := r[1].(float64)
					req.ranges = append(req.ranges, [2]int{int(s), int(e)})
				}
			}
		}

		reqs = append(reqs, req)
	}

	var outputs []string
	var errs []string

	for _, req := range reqs {
		result, err := formatSingleFile(req, workDir)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", req.path, err))
			continue
		}
		outputs = append(outputs, result)
	}

	// Build structured RawResult for each file
	var fileInfos []map[string]interface{}
	for _, req := range reqs {
		fi := buildReadFileInfo(req, workDir)
		fileInfos = append(fileInfos, fi)
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
			Tool:    "read_file",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(outputs, "\n\n"),
		Tool:    "read_file",
		RawResult: map[string]interface{}{
			"files": fileInfos,
		},
	}
}

// buildReadFileInfo extracts structured metadata from a fileReq for RawResult.
func buildReadFileInfo(req fileReq, workDir string) map[string]interface{} {
	info := map[string]interface{}{
		"path":      req.path,
		"language":  "Unknown",
		"lines":     0,
		"size":      0,
		"encoding":  "unknown",
		"modified":  "",
		"truncated": false,
	}
	resolved, errMsg := resolveReadPath(req.path, workDir)
	if errMsg != "" {
		info["error"] = errMsg
		return info
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		info["error"] = err.Error()
		return info
	}
	info["size"] = fi.Size()
	info["modified"] = fi.ModTime().Format(time.RFC3339)
	ext := strings.ToLower(filepath.Ext(resolved))
	info["language"] = getLanguageFromExt(ext)

	rawData, err := os.ReadFile(resolved)
	if err != nil {
		info["error"] = err.Error()
		return info
	}
	info["encoding"] = detectEncodingName(rawData, req.encoding)
	lines := strings.Count(string(rawData), "\n")
	if len(rawData) > 0 && !strings.HasSuffix(string(rawData), "\n") {
		lines++
	}
	info["lines"] = lines

	// Compute whether the output is truncated based on req
	truncated := (req.start > 0 || req.limit > 0 || req.tail > 0 || len(req.ranges) > 0) && lines > 0
	if req.infoOnly {
		truncated = false
		info["content"] = "" // info only, no content
	} else if truncated {
		// Read the actual displayed content from the formatted output
		content, err := readFileContent(resolved, workDir, req)
		if err == nil {
			info["content"] = content
		}
	} else {
		content, err := readFileContent(resolved, workDir, req)
		if err == nil {
			info["content"] = content
		}
	}
	info["truncated"] = truncated
	return info
}

// formatSingleFile handles reading and formatting one file request.
func formatSingleFile(req fileReq, workDir string) (string, error) {
	resolved, errMsg := resolveReadPath(req.path, workDir)
	if errMsg != "" {
		return "", fmt.Errorf("%s", errMsg)
	}

	fi, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}

	ext := strings.ToLower(filepath.Ext(resolved))
	lang := getLanguageFromExt(ext)
	filename := filepath.Base(resolved)

	// Determine encoding for metadata display
	rawData, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	detectedEnc := detectEncodingName(rawData, req.encoding)

	if req.infoOnly {
		return formatInfoBanner(filename, resolved, fi, lang, detectedEnc), nil
	}

	// Read and process content
	content, err := readFileContent(resolved, workDir, req)
	if err != nil {
		return "", err
	}

	return formatContentBanner(filename, resolved, fi, lang, content, req), nil
}

// detectEncodingName determines the human-readable encoding name for display.
func detectEncodingName(data []byte, preferred string) string {
	if preferred != "" && preferred != "auto" {
		return preferred
	}
	if utf8.Valid(data) {
		return "UTF-8"
	}
	// Try common encodings
	decoder := simplifiedchinese.GBK.NewDecoder()
	if _, _, err := transform.String(decoder, string(data)); err == nil {
		return "GBK"
	}
	decoder = japanese.ShiftJIS.NewDecoder()
	if _, _, err := transform.String(decoder, string(data)); err == nil {
		return "Shift-JIS"
	}
	decoder = traditionalchinese.Big5.NewDecoder()
	if _, _, err := transform.String(decoder, string(data)); err == nil {
		return "Big5"
	}
	return "Unknown"
}

// formatInfoBanner returns the metadata-only banner for info mode.
func formatInfoBanner(filename, resolved string, fi os.FileInfo, lang, encoding string) string {
	lines := countLinesInFile(resolved)
	modTime := fi.ModTime().Format("2006-01-02 15:04:05")

	return fmt.Sprintf("%s\n📄  %s  ·  %s  ·  %d行  ·  %s\n    路径: %s\n    编码: %s\n    修改时间: %s\n%s",
		headerSeparator,
		filename, lang, lines, formatFileSize(fi.Size()),
		resolved,
		encoding,
		modTime,
		headerSeparator,
	)
}

// countLinesInFile counts total lines in a file without reading it fully into memory.
func countLinesInFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	if len(data) == 0 {
		return 0
	}
	count := 1
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	return count
}

// formatContentBanner wraps file content with header and footer banners.
func formatContentBanner(filename, resolved string, fi os.FileInfo, lang, content string, req fileReq) string {
	totalLines := countLinesInFile(resolved)
	charCount := len(content)

	// Compute extra info for footer
	var footerExtra string
	if len(req.ranges) > 0 {
		footerExtra = fmt.Sprintf(" · %d段", len(req.ranges))
	} else if req.tail > 0 {
		footerExtra = fmt.Sprintf(" · 仅显示最后%d行", req.tail)
	} else if req.start > 0 || req.limit > 0 {
		shownLines := strings.Count(content, "\n")
		if content != "" && !strings.HasSuffix(content, "\n") {
			shownLines++
		}
		if shownLines < totalLines {
			footerExtra = fmt.Sprintf(" · 仅显示部分")
		}
	}

	header := fmt.Sprintf("%s\n📄  %s  ·  %s  ·  %d行  ·  %s\n    路径: %s\n%s",
		headerSeparator,
		filename, lang, totalLines, formatFileSize(fi.Size()),
		resolved,
		headerSeparator)

	footer := fmt.Sprintf("%s\n📎  end of %s (%d行 · %d字符%s)\n%s",
		footerSeparator,
		filename, totalLines, charCount, footerExtra,
		footerSeparator)

	return header + "\n" + content + "\n" + footer
}

const headerSeparator = "═══════════════════════════════════════════════════════════════"
const footerSeparator = "───────────────────────────────────────────────────────────────"

// ─── Read File Content (Core) ────────────────────────────────

// readFileContent reads a single file and returns its content as text.
// It handles encoding detection/decoding, line slicing (start/limit, tail, ranges),
// and optional line number prefixing. Office docs are delegated to ReadFileAsMarkdown.
func readFileContent(resolved, workDir string, req fileReq) (string, error) {
	ext := strings.ToLower(filepath.Ext(resolved))
	switch ext {
	case ".pdf", ".docx", ".xlsx", ".pptx":
		return ReadFileAsMarkdown(resolved, workDir)
	default:
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", err
		}
		if IsBinaryBytes(data) {
			mimeType := http.DetectContentType(data)
			b64 := base64.StdEncoding.EncodeToString(data)
			return fmt.Sprintf("data:%s;base64,%s", mimeType, b64), nil
		}

		// Decode with specified encoding
		content, err := readWithEncoding(data, req.encoding)
		if err != nil {
			return "", err
		}

		lines := strings.Split(content, "\n")
		// Remove trailing empty line if file ends with \n
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		// Select lines based on parameters
		var selectedLines []string
		var baseLineNum int // 1-based line number of the first selected line in the original file

		switch {
		case len(req.ranges) > 0:
			// Multi-segment ranges mode
			var segments []string
			for ri, r := range req.ranges {
				startIdx := clampRange(r[0], 1, len(lines))
				endIdx := clampRange(r[1], startIdx, len(lines))
				segmentLines := lines[startIdx-1 : endIdx]
				segLabel := fmt.Sprintf("── Range %d: 第 %d-%d 行 ──", ri+1, startIdx, endIdx)
				if req.lineNumbers {
					segContent := addLineNumbers(segmentLines, startIdx)
					segments = append(segments, segLabel+"\n"+segContent)
				} else {
					segments = append(segments, segLabel+"\n"+strings.Join(segmentLines, "\n"))
				}
			}
			return strings.Join(segments, "\n\n"), nil

		case req.tail > 0:
			// Tail mode: read last N lines
			tailN := req.tail
			if tailN > len(lines) {
				tailN = len(lines)
			}
			selectedLines = lines[len(lines)-tailN:]
			baseLineNum = len(lines) - tailN + 1

		case req.start > 0 || req.limit > 0:
			// start/limit mode
			startIdx := req.start
			if startIdx < 1 {
				startIdx = 1
			}
			if startIdx > len(lines) {
				startIdx = len(lines)
			}
			endIdx := len(lines)
			if req.limit > 0 {
				endIdx = startIdx - 1 + req.limit
				if endIdx > len(lines) {
					endIdx = len(lines)
				}
			}
			selectedLines = lines[startIdx-1 : endIdx]
			baseLineNum = startIdx

		default:
			// Full file
			selectedLines = lines
			baseLineNum = 1
		}

		if len(selectedLines) == 0 {
			return "", nil
		}

		if req.lineNumbers {
			return addLineNumbers(selectedLines, baseLineNum), nil
		}
		return strings.Join(selectedLines, "\n"), nil
	}
}

// clampRange ensures v is within [min, max].
func clampRange(v, minVal, maxVal int) int {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

// addLineNumbers prefixes each line with a right-aligned line number.
// The padding width is determined by the last line number.
func addLineNumbers(lines []string, startLineNum int) string {
	lastLineNum := startLineNum + len(lines) - 1
	width := len(fmt.Sprintf("%d", lastLineNum))
	if width < 2 {
		width = 2
	}

	var b strings.Builder
	for i, line := range lines {
		lineNum := startLineNum + i
		b.WriteString(fmt.Sprintf("%*d  %s", width, lineNum, line))
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// writeFileResult holds the result of writing one file.
type writeFileResult struct {
	path     string
	bytes    int
	err      string
	newFn    bool   // true if the file did not exist before
	backup   string // backup path, if backup was taken
	diff     string // unified diff text (if show_diff=true)
	stat     string // diff stat (if show_diff="stat")
	dryRun   bool
	noChange bool   // true if content is identical to existing file
}

// HandleWriteFile writes content to one or more files (v2.0).
// Supports: show_diff (true|"stat"), backup, dry_run, template variables.
func HandleWriteFile(workDir string, codeIndexer *codeindex.Indexer, versioner *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	raw, ok := args["files"]
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   "arguments must be a JSON array",
			Tool:    "write_file",
		}
	}
	rawFiles, ok := raw.([]interface{})
	if !ok || len(rawFiles) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "expected a non-empty array of file objects",
			Tool:    "write_file",
		}
	}

	// Top-level defaults (inherited by each file entry)
	topShowDiff, _ := args["show_diff"].(bool)
	topShowDiffStat := false
	if sdStr, ok := args["show_diff"].(string); ok && sdStr == "stat" {
		topShowDiffStat = true
		topShowDiff = true
	}
	topBackup, _ := args["backup"].(bool)
	dryRun, _ := args["dry_run"].(bool)
	topTemplate, _ := args["template"].(map[string]interface{})
	format, _ := args["format"].(string)

	var results []writeFileResult

	for i, raw := range rawFiles {
		m, ok := raw.(map[string]interface{})
		if !ok {
			results = append(results, writeFileResult{err: fmt.Sprintf("[%d]: expected object", i)})
			continue
		}
		path, _ := m["path"].(string)
		content, _ := m["content"].(string)
		if path == "" {
			results = append(results, writeFileResult{path: path, err: "path is required"})
			continue
		}

		contentType, _ := m["type"].(string)

		// Per-file overrides for show_diff / backup / template
		fileShowDiff := topShowDiff
		fileShowDiffStat := topShowDiffStat
		if sd, ok := m["show_diff"].(bool); ok {
			fileShowDiff = sd
			fileShowDiffStat = false
		}
		if sdStr, ok := m["show_diff"].(string); ok && sdStr == "stat" {
			fileShowDiff = true
			fileShowDiffStat = true
		}
		fileBackup := topBackup
		if b, ok := m["backup"].(bool); ok {
			fileBackup = b
		}

		// Merge template (file-level wins)
		mergedTemplate := make(map[string]interface{})
		for k, v := range topTemplate {
			mergedTemplate[k] = v
		}
		if ft, ok := m["template"].(map[string]interface{}); ok {
			for k, v := range ft {
				mergedTemplate[k] = v
			}
		}

		// Apply template variable substitution
		if len(mergedTemplate) > 0 {
			content = applyTemplate(content, mergedTemplate)
		}

		resolved, errMsg := resolveWritePath(path, workDir)
		if errMsg != "" {
			results = append(results, writeFileResult{path: path, err: errMsg})
			continue
		}

		dir := filepath.Dir(resolved)
		if err := os.MkdirAll(dir, 0755); err != nil {
			results = append(results, writeFileResult{path: path, err: fmt.Sprintf("failed to create directory: %s", err)})
			continue
		}

		var data []byte
		if strings.HasPrefix(contentType, "base64") {
			decoded, err := base64.StdEncoding.DecodeString(content)
			if err != nil {
				results = append(results, writeFileResult{path: path, err: fmt.Sprintf("failed to decode base64: %s", err)})
				continue
			}
			data = decoded
		} else {
			data = []byte(content)
		}

		// Check if file exists (for new file detection and backup)
		existingData, readErr := os.ReadFile(resolved)
		isNewFile := readErr != nil

		// Content unchanged check: if new data matches existing, skip writing
		isNoChange := !isNewFile && bytes.Equal(existingData, data)
		if isNoChange {
			results = append(results, writeFileResult{
				path:     path,
				bytes:    len(data),
				newFn:    false,
				noChange: true,
			})
			continue
		}

		// Compute diff if needed (before writing)
		var diffText string
		var diffStat string
		if fileShowDiff && !isNewFile {
			dmp := diffmatchpatch.New()
			diffs := dmp.DiffMain(string(existingData), string(data), true)
			dmp.DiffCleanupSemantic(diffs)
			addLines := 0
			delLines := 0
			for _, d := range diffs {
				lineCount := strings.Count(d.Text, "\n")
				switch d.Type {
				case diffmatchpatch.DiffInsert:
					addLines += lineCount
				case diffmatchpatch.DiffDelete:
					delLines += lineCount
				}
			}
			diffStat = fmt.Sprintf("+%d行 / -%d行", addLines, delLines)
			if !fileShowDiffStat {
				diffText = BuildUnifiedDiff(path, path, diffs, 3)
			}
		}

		// dry_run: preview only
		if dryRun {
			results = append(results, writeFileResult{
				path:  path,
				bytes: len(data),
				newFn: isNewFile,
				diff:  diffText,
				stat:  diffStat,
				dryRun: true,
			})
			continue
		}

		// backup: copy existing file to .bak
		backupPath := ""
		if fileBackup && !isNewFile {
			bp := resolved + ".bak"
			if err := os.WriteFile(bp, existingData, 0644); err == nil {
				backupPath = path + ".bak"
			}
		}

		// Snapshot for rollback
		snapshotBeforeWrite(versioner, resolved, workDir, turnID)

		if err := os.WriteFile(resolved, data, 0644); err != nil {
			results = append(results, writeFileResult{path: path, err: fmt.Sprintf("failed to write: %s", err)})
			continue
		}

		TriggerCodeIndex(codeIndexer, resolved)
		results = append(results, writeFileResult{
			path:   path,
			bytes:  len(data),
			newFn:  isNewFile,
			backup: backupPath,
			diff:   diffText,
			stat:   diffStat,
		})
	}

	// Build structured RawResult for all files
	var rawResults []map[string]interface{}
	for _, r := range results {
		item := map[string]interface{}{
			"path": r.path,
		}
		if r.err != "" {
			item["status"] = "failed"
			item["error"] = r.err
		} else if r.dryRun {
			item["status"] = "dry_run"
			item["bytes"] = r.bytes
			item["is_new"] = r.newFn
			if r.diff != "" {
				item["diff"] = r.diff
			}
			if r.stat != "" {
				item["stat"] = r.stat
			}
		} else if r.noChange {
			item["status"] = "no_change"
			item["bytes"] = r.bytes
		} else {
			item["status"] = "ok"
			item["bytes"] = r.bytes
			item["is_new"] = r.newFn
			if r.backup != "" {
				item["backup"] = r.backup
			}
			if r.diff != "" {
				item["diff"] = r.diff
			}
			if r.stat != "" {
				item["stat"] = r.stat
			}
		}
		rawResults = append(rawResults, item)
	}

	// Build output
	if format == "json" {
		var jsonResults []map[string]interface{}
		for _, r := range results {
			item := map[string]interface{}{
				"path": r.path,
			}
			if r.err != "" {
				item["status"] = "failed"
				item["error"] = r.err
			} else if r.dryRun {
				item["status"] = "dry_run"
				item["bytes"] = r.bytes
				item["is_new"] = r.newFn
				if r.diff != "" {
					item["diff"] = r.diff
				}
				if r.stat != "" {
					item["stat"] = r.stat
				}
			} else if r.noChange {
				item["status"] = "no_change"
				item["bytes"] = r.bytes
			} else {
				item["status"] = "ok"
				item["bytes"] = r.bytes
				item["is_new"] = r.newFn
				if r.backup != "" {
					item["backup"] = r.backup
				}
				if r.diff != "" {
					item["diff"] = r.diff
				}
				if r.stat != "" {
					item["stat"] = r.stat
				}
			}
			jsonResults = append(jsonResults, item)
		}
		jsonBytes, _ := json.Marshal(jsonResults)
		return &types.ToolResult{
			Success:   true,
			Output:    string(jsonBytes),
			Tool:      "write_file",
			RawResult: map[string]interface{}{"files": rawResults},
		}
	}

	// Build text output
	var sections []string
	var errs []string
	for _, r := range results {
		if r.err != "" {
			errs = append(errs, r.err)
			continue
		}

		var itemParts []string
		if r.dryRun {
			// Dry-run output
			if r.newFn {
				itemParts = append(itemParts, fmt.Sprintf("🔍 [DRY RUN] 将写入 %s (%d bytes) — 新文件", r.path, r.bytes))
			} else {
				itemParts = append(itemParts, fmt.Sprintf("🔍 [DRY RUN] 将写入 %s (%d bytes)", r.path, r.bytes))
				if r.diff != "" {
					itemParts = append(itemParts, r.diff)
				} else if r.stat != "" {
					itemParts = append(itemParts, fmt.Sprintf("   (%s)", r.stat))
				}
			}
			itemParts = append(itemParts, "   未执行实际写入。去掉 dry_run 重试以生效。")
		} else if r.noChange {
			// Content unchanged
			itemParts = append(itemParts, fmt.Sprintf("⚠️ %s 内容相同，未修改", r.path))
		} else {
			// Normal write output
			base := fmt.Sprintf("✅ wrote %s (%d bytes)", r.path, r.bytes)
			if r.newFn {
				base += " — 新文件"
			}
			if r.backup != "" {
				base += fmt.Sprintf("。备份: %s", r.backup)
			}
			if r.stat != "" {
				base += fmt.Sprintf(" — %s", r.stat)
			}
			itemParts = append(itemParts, base)
			if r.diff != "" {
				itemParts = append(itemParts, r.diff)
			}
		}
		sections = append(sections, strings.Join(itemParts, "\n"))
	}

	if len(errs) > 0 {
		result := strings.Join(sections, "\n\n")
		if result != "" {
			result += "\n\n"
		}
		result += "errors:\n" + strings.Join(errs, "\n")
		return &types.ToolResult{
			Success:   false,
			Output:    result,
			Tool:      "write_file",
			RawResult: map[string]interface{}{"files": rawResults},
		}
	}

	// Single file: no section wrapper
	if len(sections) == 1 {
		return &types.ToolResult{
			Success:   true,
			Output:    sections[0],
			Tool:      "write_file",
			RawResult: map[string]interface{}{"files": rawResults},
		}
	}

	// Multi-file: wrap with summary
	successCount := 0
	for _, r := range results {
		if r.err == "" {
			successCount++
		}
	}
	output := fmt.Sprintf("✅ 写入 %d 个文件：\n\n", successCount)
	for i, s := range sections {
		if i > 0 {
			output += "\n\n"
		}
		// Extract path from result
		path := results[i].path
		output += fmt.Sprintf("=== %s ===\n%s", path, s)
	}
	return &types.ToolResult{
		Success:   true,
		Output:    output,
		Tool:      "write_file",
		RawResult: map[string]interface{}{"files": rawResults},
	}
}

// applyTemplate replaces {{varName}} placeholders in content with values from tmpl.
func applyTemplate(content string, tmpl map[string]interface{}) string {
	re := regexp.MustCompile(`\{\{(\w+)\}\}`)
	return re.ReplaceAllStringFunc(content, func(match string) string {
		name := match[2 : len(match)-2]
		if val, ok := tmpl[name]; ok {
			return fmt.Sprint(val)
		}
		return match
	})
}

// HandleReplace performs a find/replace in files.
// Supports three modes: 'files' array (multi-file precise), 'file_pattern' glob (batch), 'path' (single-file legacy).
// Priority: files > file_pattern > path.
func HandleReplace(workDir string, engine *engine.Engine, codeIndexer *codeindex.Indexer, versioner *fileversions.Versioner, turnID string, args map[string]interface{}) *types.ToolResult {
	oldStr, _ := args["old"].(string)
	newStr, _ := args["new"].(string)
	useRegex, _ := args["regex"].(bool)
	dryRun, _ := args["dry_run"].(bool)

	if oldStr == "" && newStr == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "at least one of 'old' or 'new' must differ",
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"error": "at least one of old or new must differ",
			},
		}
	}

	// Multi-file precise mode: files array (highest priority)
	if rawFiles, ok := args["files"].([]interface{}); ok && len(rawFiles) > 0 {
		return replaceByFiles(workDir, codeIndexer, versioner, turnID, rawFiles, oldStr, newStr, useRegex, dryRun)
	}

	// Multi-file mode: file_pattern glob
	if fp, ok := args["file_pattern"].(string); ok && fp != "" {
		return replaceByPattern(workDir, engine, codeIndexer, versioner, turnID, fp, oldStr, newStr, useRegex, dryRun)
	}

	// Legacy single-file mode
	path, _ := args["path"].(string)
	if path == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "provide 'path', 'file_pattern', or 'files' for replace",
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"error": "provide path, file_pattern, or files for replace",
			},
		}
	}

	return replaceSingleFile(workDir, codeIndexer, versioner, turnID, path, oldStr, newStr, useRegex, dryRun)
}

// replaceByPattern applies replacement to all files matching a glob pattern.
func replaceByPattern(workDir string, engine *engine.Engine, codeIndexer *codeindex.Indexer, versioner *fileversions.Versioner, turnID, filePattern, oldStr, newStr string, useRegex, dryRun bool) *types.ToolResult {
	// Find matching files using grep's file matching logic
	files, err := findFilesByGlob(workDir, filePattern)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("file_pattern error: %s", err.Error()),
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"error":   fmt.Sprintf("file_pattern error: %s", err.Error()),
				"pattern": filePattern,
			},
		}
	}
	if len(files) == 0 {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("✅ 未找到匹配模式 '%s' 的文件", filePattern),
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"files":       []string{},
				"total":       0,
				"dry_run":     dryRun,
			},
		}
	}

	var fileResults []map[string]interface{}
	var totalReplacements int
	for _, f := range files {
		relPath, _ := filepath.Rel(workDir, f)
		resolved := f

		data, err := os.ReadFile(resolved)
		if err != nil {
			fileResults = append(fileResults, map[string]interface{}{
				"path":  relPath,
				"error": fmt.Sprintf("read error: %s", err.Error()),
			})
			continue
		}
		if IsBinaryBytes(data) {
			continue
		}

		content := string(data)
		var newContent string
		var count int

		if oldStr == "" {
			// Prepend mode
			newContent = newStr + content
			count = 1
		} else if useRegex {
			re, err := regexp.Compile(oldStr)
			if err != nil {
				fileResults = append(fileResults, map[string]interface{}{
					"path":  relPath,
					"error": fmt.Sprintf("regex error: %s", err.Error()),
				})
				continue
			}
			newContent = re.ReplaceAllString(content, newStr)
			count = len(re.FindAllString(content, -1))
		} else {
			newContent = strings.ReplaceAll(content, oldStr, newStr)
			count = strings.Count(content, oldStr)
		}

		if content == newContent {
			fileResults = append(fileResults, map[string]interface{}{
				"path":       relPath,
				"count":      0,
				"no_changes": true,
			})
			continue
		}

		totalReplacements += count
		fileResult := map[string]interface{}{
			"path":  relPath,
			"count": count,
		}
		if dryRun {
			fileResult["status"] = "dry_run"
		} else {
			snapshotBeforeWrite(versioner, resolved, workDir, turnID)
			if err := os.WriteFile(resolved, []byte(newContent), 0644); err != nil {
				fileResult["error"] = fmt.Sprintf("write error: %s", err.Error())
				fileResults = append(fileResults, fileResult)
				continue
			}
			TriggerCodeIndex(codeIndexer, resolved)
			fileResult["status"] = "done"
		}
		fileResults = append(fileResults, fileResult)
	}

	// Count actually changed files
	changedCount := 0
	for _, r := range fileResults {
		if c, ok := r["count"].(int); ok && c > 0 {
			changedCount++
		}
	}

	prefix := ""
	if dryRun {
		prefix = "[DRY RUN] "
	}

	var outParts []string
	for _, r := range fileResults {
		if errMsg, ok := r["error"].(string); ok && errMsg != "" {
			outParts = append(outParts, fmt.Sprintf("  %s: %s", r["path"], errMsg))
		} else if nc, ok := r["no_changes"].(bool); ok && nc {
			outParts = append(outParts, fmt.Sprintf("  %s: no changes", r["path"]))
		} else if c, ok := r["count"].(int); ok {
			if dryRun {
				outParts = append(outParts, fmt.Sprintf("  %s: %d replacement(s) (dry run)", r["path"], c))
			} else {
				outParts = append(outParts, fmt.Sprintf("  %s: %d replacement(s)", r["path"], c))
			}
		}
	}

	emoji := "✅"
	if dryRun {
		emoji = "🔍"
	}
	summary := fmt.Sprintf("%s %s%s 个文件，共 %d 处替换:\n%s", emoji, prefix, fmt.Sprintf("%d", changedCount), totalReplacements, strings.Join(outParts, "\n"))
	return &types.ToolResult{
		Success: true,
		Output:  summary,
		Tool:    "replace",
		RawResult: map[string]interface{}{
			"files":  fileResults,
			"total":  totalReplacements,
			"dry_run": dryRun,
		},
	}
}

// replaceSingleFile handles the original single-file replace mode.
func replaceSingleFile(workDir string, codeIndexer *codeindex.Indexer, versioner *fileversions.Versioner, turnID, path, oldStr, newStr string, useRegex, dryRun bool) *types.ToolResult {
	resolved, errMsg := resolveWritePath(path, workDir)
	if errMsg != "" {
		return &types.ToolResult{
			Success: false,
			Error:   errMsg,
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"path":  path,
				"error": errMsg,
			},
		}
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read file: %s", err.Error()),
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"path":  path,
				"error": err.Error(),
			},
		}
	}
	if IsBinaryBytes(data) {
		return &types.ToolResult{
			Success: false,
			Error:   "cannot replace in binary file",
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"path":  path,
				"error": "cannot replace in binary file",
			},
		}
	}

	content := string(data)
	var newContent string
	var count int

	if oldStr == "" {
		newContent = newStr + content
		count = 1
	} else if useRegex {
		re, err := regexp.Compile(oldStr)
		if err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("invalid regex: %s", err.Error()),
				Tool:    "replace",
				RawResult: map[string]interface{}{
					"path":  path,
					"error": fmt.Sprintf("invalid regex: %s", err.Error()),
				},
			}
		}
		newContent = re.ReplaceAllString(content, newStr)
		count = len(re.FindAllString(content, -1))
	} else {
		newContent = strings.ReplaceAll(content, oldStr, newStr)
		count = strings.Count(content, oldStr)
	}

	if content == newContent {
		if dryRun {
			return &types.ToolResult{
				Success: true,
				Output:  fmt.Sprintf("[DRY RUN] %s: no matches found", path),
				Tool:    "replace",
				RawResult: map[string]interface{}{
					"path":       path,
					"count":      0,
					"no_changes": true,
					"dry_run":    true,
				},
			}
		}
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("no changes in %s", path),
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"path":       path,
				"count":      0,
				"no_changes": true,
			},
		}
	}

	if dryRun {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("[DRY RUN] %s: %d replacement(s) would be made", path, count),
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"path":    path,
				"count":   count,
				"dry_run": true,
			},
		}
	}

	snapshotBeforeWrite(versioner, resolved, workDir, turnID)
	if err := os.WriteFile(resolved, []byte(newContent), 0644); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write file: %s", err.Error()),
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"path":  path,
				"error": err.Error(),
			},
		}
	}

	TriggerCodeIndex(codeIndexer, resolved)
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("✅ replaced in %s (%d 处替换)", path, count),
		Tool:    "replace",
		RawResult: map[string]interface{}{
			"path":  path,
			"count": count,
		},
	}
}

// replaceByFiles applies replacement to files specified in the files array.
// Each file entry can override old/new/regex; omitted values inherit from top-level args.
func replaceByFiles(workDir string, codeIndexer *codeindex.Indexer, versioner *fileversions.Versioner, turnID string, rawFiles []interface{}, topOld, topNew string, topRegex, dryRun bool) *types.ToolResult {
	var rawResults []map[string]interface{}
	totalReplacements := 0

	for i, raw := range rawFiles {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("files[%d]: expected object", i),
				Tool:    "replace",
				RawResult: map[string]interface{}{
					"error": fmt.Sprintf("files[%d]: expected object", i),
				},
			}
		}

		path, _ := m["path"].(string)
		if path == "" {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("files[%d]: path is required", i),
				Tool:    "replace",
				RawResult: map[string]interface{}{
					"error": fmt.Sprintf("files[%d]: path is required", i),
				},
			}
		}

		// Inheritance: file-level overrides top-level
		oldStr := topOld
		newStr := topNew
		useRegex := topRegex

		if v, ok := m["old"].(string); ok {
			oldStr = v
		}
		if v, ok := m["new"].(string); ok {
			newStr = v
		}
		if v, ok := m["regex"].(bool); ok {
			useRegex = v
		}

		if oldStr == "" && newStr == "" {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("files[%d]: both 'old' and 'new' are empty (no inherited value either)", i),
				Tool:    "replace",
				RawResult: map[string]interface{}{
					"error": fmt.Sprintf("files[%d]: both old and new are empty", i),
				},
			}
		}

		resolved, errMsg := resolveWritePath(path, workDir)
		if errMsg != "" {
			rawResults = append(rawResults, map[string]interface{}{
				"path":  path,
				"error": errMsg,
			})
			continue
		}

		data, err := os.ReadFile(resolved)
		if err != nil {
			rawResults = append(rawResults, map[string]interface{}{
				"path":  path,
				"error": fmt.Sprintf("read error: %s", err.Error()),
			})
			continue
		}
		if IsBinaryBytes(data) {
			rawResults = append(rawResults, map[string]interface{}{
				"path":  path,
				"error": "cannot replace in binary file",
			})
			continue
		}

		content := string(data)
		var newContent string
		var count int

		if oldStr == "" {
			newContent = newStr + content
			count = 1
		} else if useRegex {
			re, err := regexp.Compile(oldStr)
			if err != nil {
				rawResults = append(rawResults, map[string]interface{}{
					"path":  path,
					"error": fmt.Sprintf("regex error: %s", err.Error()),
				})
				continue
			}
			newContent = re.ReplaceAllString(content, newStr)
			count = len(re.FindAllString(content, -1))
		} else {
			newContent = strings.ReplaceAll(content, oldStr, newStr)
			count = strings.Count(content, oldStr)
		}

		if content == newContent {
			rawResults = append(rawResults, map[string]interface{}{
				"path":       path,
				"count":      0,
				"no_changes": true,
			})
			continue
		}

		totalReplacements += count
		fr := map[string]interface{}{
			"path":  path,
			"count": count,
		}
		if dryRun {
			fr["status"] = "dry_run"
		} else {
			snapshotBeforeWrite(versioner, resolved, workDir, turnID)
			if err := os.WriteFile(resolved, []byte(newContent), 0644); err != nil {
				fr["error"] = fmt.Sprintf("write error: %s", err.Error())
				rawResults = append(rawResults, fr)
				continue
			}
			TriggerCodeIndex(codeIndexer, resolved)
			fr["status"] = "done"
		}
		rawResults = append(rawResults, fr)
	}

	// Build summary
	var changed []string
	var noMatch []string
	var errs []string
	for _, r := range rawResults {
		if errMsg, ok := r["error"].(string); ok && errMsg != "" {
			errs = append(errs, fmt.Sprintf("%s: %s", r["path"], errMsg))
		} else if c, ok := r["count"].(int); ok && c == 0 {
			noMatch = append(noMatch, r["path"].(string))
		} else {
			changed = append(changed, fmt.Sprintf("%s: %d", r["path"], r["count"]))
		}
	}

	var parts []string
	if len(changed) > 0 {
		if dryRun {
			parts = append(parts, fmt.Sprintf("🔍 [DRY RUN] 将修改 %d 个文件的 %d 处匹配：\n  %s\n  未执行实际修改。去掉 dry_run 重试以生效。", len(changed), totalReplacements, strings.Join(changed, "\n  ")))
		} else if len(changed) == 1 {
			parts = append(parts, fmt.Sprintf("✅ %s (%d 处替换)", changed[0], totalReplacements))
		} else {
			parts = append(parts, fmt.Sprintf("✅ %d 个文件 (%s) — 共 %d 处替换", len(changed), strings.Join(changed, ", "), totalReplacements))
		}
	}
	if len(noMatch) > 0 {
		parts = append(parts, fmt.Sprintf("⚠️ 无匹配文件: %d 个: %s", len(noMatch), strings.Join(noMatch, ", ")))
	}
	if len(errs) > 0 {
		parts = append(parts, "errors:\n  "+strings.Join(errs, "\n  "))
	}

	if len(changed) == 0 && len(errs) > 0 {
		return &types.ToolResult{
			Success: false,
			Output:  strings.Join(parts, "\n"),
			Tool:    "replace",
			RawResult: map[string]interface{}{
				"files":  rawResults,
				"total":  totalReplacements,
				"dry_run": dryRun,
			},
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(parts, "\n"),
		Tool:    "replace",
		RawResult: map[string]interface{}{
			"files":  rawResults,
			"total":  totalReplacements,
			"dry_run": dryRun,
		},
	}
}

// TriggerCodeIndex marks a file as changed for deferred batch indexing.
// The actual Enqueue happens at turn end via FlushChangedFiles.
func TriggerCodeIndex(codeIndexer *codeindex.Indexer, path string) {
	if codeIndexer == nil {
		return
	}
	codeIndexer.MarkChanged(path)
}

// snapshotBeforeWrite snapshots a file before modification (first time per turn per file_uid).
func snapshotBeforeWrite(versioner *fileversions.Versioner, resolvedPath, workDir, turnID string) {
	if versioner == nil || turnID == "" {
		return
	}
	// Compute relative path for DB storage
	relPath := resolvedPath
	if filepath.IsAbs(resolvedPath) {
		if rel, err := filepath.Rel(workDir, resolvedPath); err == nil {
			relPath = rel
		}
	}
	versioner.Snapshot(turnID, relPath)
}

// ─── Simplify Types & Functions ───

type readFileItem struct {
	Path        string   `json:"path"`
	Start       int      `json:"start,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	LineNumbers bool     `json:"line_numbers,omitempty"`
	Tail        int      `json:"tail,omitempty"`
	Encoding    string   `json:"encoding,omitempty"`
	Info        bool     `json:"info,omitempty"`
	Ranges      [][]int  `json:"ranges,omitempty"`
}

func simplifyReadFile(argsJSON json.RawMessage, result string) string {
	var items []readFileItem
	if err := json.Unmarshal(argsJSON, &items); err != nil || len(items) == 0 {
		return "read_file"
	}
	paths := make([]string, len(items))
	for i, f := range items {
		paths[i] = f.Path
	}
	pathList := strings.Join(paths, ", ")
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil || !tr.Success {
		return fmt.Sprintf("read_file(%s) failed", pathList)
	}
	lines := strings.Count(tr.Output, "\n")
	return fmt.Sprintf("read_file(%s): %d lines", pathList, lines)
}

type writeFileItem struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Type    string `json:"type,omitempty"`
}

func simplifyWriteFile(argsJSON json.RawMessage, result string) string {
	var items []writeFileItem
	if err := json.Unmarshal(argsJSON, &items); err != nil || len(items) == 0 {
		return "write_file"
	}
	paths := make([]string, len(items))
	totalBytes := 0
	for i, f := range items {
		paths[i] = f.Path
		totalBytes += len(f.Content)
	}
	pathList := strings.Join(paths, ", ")
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil || !tr.Success {
		return fmt.Sprintf("write_file(%s) failed", pathList)
	}
	return fmt.Sprintf("write_file(%s): %d bytes", pathList, totalBytes)
}

type replaceArgs struct {
	Path     string `json:"path"`
	Old      string `json:"old"`
	New      string `json:"new"`
	UseRegex bool   `json:"regex,omitempty"`
}

func simplifyReplace(argsJSON json.RawMessage, result string) string {
	var a replaceArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "replace"
	}
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil || !tr.Success {
		return fmt.Sprintf("replace(%s) failed", a.Path)
	}
	oldPreview := types.TruncateStr(a.Old, 60)
	return fmt.Sprintf("replace(%s): replaced %q", a.Path, oldPreview)
}

// executeCommandArgs is used for simplifyExecuteCommand.
type executeCommandArgs struct {
	Command string `json:"command"`
	Async   bool   `json:"async,omitempty"`
}

func simplifyExecuteCommand(argsJSON json.RawMessage, result string) string {
	var a executeCommandArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "execute_command"
	}
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil {
		return fmt.Sprintf("execute_command(%s)", types.TruncateStr(a.Command, 80))
	}
	if a.Async {
		return fmt.Sprintf("execute_command(async): %s", tr.Output)
	}
	lines := 0
	if tr.Output != "" {
		lines = strings.Count(tr.Output, "\n")
	}
	if tr.Success {
		return fmt.Sprintf("execute_command(%s): ok, %d lines",
			types.TruncateStr(a.Command, 60), lines)
	}
	return fmt.Sprintf("execute_command(%s): failed, exit=%s",
		types.TruncateStr(a.Command, 60), types.TruncateStr(tr.Error, 80))
}

func init() {
	types.RegisterSimplify("read_file", simplifyReadFile)
	types.RegisterSimplify("write_file", simplifyWriteFile)
	types.RegisterSimplify("replace", simplifyReplace)
	types.RegisterSimplify("execute_command", simplifyExecuteCommand)
}

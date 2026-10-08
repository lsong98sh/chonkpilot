package fileops

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
)

// readReq 是 file_read 的单文件请求。
type readReq struct {
	path        string
	start       int
	limit       int
	lineNumbers bool
	tail        int
	infoOnly    bool
	ranges      [][2]int
}

// HandleReadFile 读取一个或多个文件（v2 契约）。
// 结构化输出（无展示文本）：顶层 {status, files:[...]}；每文件含 path/md5/encoding/lines/size/content（或 error）。
func HandleReadFile(workDir string, args map[string]interface{}) *cli.Result {
	raw, ok := args["files"]
	if !ok {
		return cli.Err("file_read", "arguments must be a JSON array")
	}
	rawFiles, ok := raw.([]interface{})
	if !ok || len(rawFiles) == 0 {
		return cli.Err("file_read", "expected a non-empty array of file objects")
	}

	var reqs []readReq
	for i, raw := range rawFiles {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return cli.Err("file_read", fmt.Sprintf("[%d]: expected object", i))
		}
		path, _ := m["path"].(string)
		if path == "" {
			return cli.Err("file_read", fmt.Sprintf("[%d]: path is required", i))
		}
		// 参数级路径违规（R-11）→ 顶层错误，不降级为单文件 error 条目
		if msg := ValidateField(fmt.Sprintf("files[%d].path", i), path); msg != "" {
			return cli.Err("file_read", msg)
		}
		req := readReq{path: path}
		if v, ok := m["start"].(float64); ok {
			req.start = int(v)
		}
		if v, ok := m["limit"].(float64); ok {
			req.limit = int(v)
		}
		if v, ok := m["line_numbers"].(bool); ok {
			req.lineNumbers = v
		}
		if v, ok := m["tail"].(float64); ok {
			req.tail = int(v)
		}
		if v, ok := m["info"].(bool); ok {
			req.infoOnly = v
		}
		if rawRanges, ok := m["ranges"].([]interface{}); ok {
			for _, rr := range rawRanges {
				if r, ok := rr.([]interface{}); ok && len(r) == 2 {
					s, _ := r[0].(float64)
					e, _ := r[1].(float64)
					req.ranges = append(req.ranges, [2]int{int(s), int(e)})
				}
			}
		}
		reqs = append(reqs, req)
	}

	var fileInfos []map[string]interface{}
	var errs []string
	for _, req := range reqs {
		info, err := readOne(req, workDir)
		if err != nil {
			if info == nil {
				info = infoMap(req.path, err.Error())
			}
			errs = append(errs, fmt.Sprintf("%s: %s", req.path, err))
		}
		fileInfos = append(fileInfos, info)
	}

	status := "success"
	var payload map[string]interface{}
	if len(errs) > 0 {
		status = "fail"
		payload = map[string]interface{}{
			"status": status,
			"error":  strings.Join(errs, "\n"),
			"files":  fileInfos,
		}
	} else {
		payload = map[string]interface{}{
			"status": status,
			"files":  fileInfos,
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return cli.Err("file_read", "marshal result: "+err.Error())
	}
	if status == "fail" {
		return &cli.Result{Success: false, Output: string(data), Error: strings.Join(errs, "\n"), Tool: "file_read"}
	}
	return cli.Ok("file_read", string(data), nil)
}

// readOne 读取单个文件，返回结构化 info（path/md5/encoding/lines/size/content 等）；错误时 info 带 error。
func readOne(req readReq, workDir string) (map[string]interface{}, error) {
	resolved, errMsg := ResolvePath(req.path, workDir)
	if errMsg != "" {
		return infoMap(req.path, errMsg), fmt.Errorf("%s", errMsg)
	}
	// agentbox 沙箱（仅隔离开启时生效）：允许目录外的读一律拒绝
	if err := sandboxErr(resolved, false); err != nil {
		return infoMap(req.path, err.Error()), err
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return infoMap(req.path, err.Error()), fmt.Errorf("stat file: %w", err)
	}
	// 读操作**不加跨进程锁**：读是共享语义，与并发写无锁冲突；且锁文件落在被读文件同目录，
	// 在 agentbox 只读目录下会因无法创建锁文件而误伤合法读（C-03）。写侧（fileops 修改类动作）
	// 仍在写前/写后持锁，互斥语义由写侧保证。
	info := map[string]interface{}{
		"path": req.path, "lines": 0, "size": fi.Size(),
		"encoding": "unknown", "modified": fi.ModTime().Format(time.RFC3339), "truncated": false,
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		info["error"] = err.Error()
		return info, fmt.Errorf("read file: %w", err)
	}
	info["md5"], _ = fileMD5(resolved)
	info["encoding"] = EncodingName(data)
	info["lines"] = countLines(data)

	if req.infoOnly {
		return info, nil
	}

	content, err := readContent(data, req)
	if err != nil {
		info["error"] = err.Error()
		return info, err
	}
	info["content"] = content
	info["truncated"] = req.start > 0 || req.limit > 0 || req.tail > 0 || len(req.ranges) > 0
	return info, nil
}

func infoMap(path, errMsg string) map[string]interface{} {
	return map[string]interface{}{"path": path, "error": errMsg}
}

// countLines 统计逻辑行数（与 readContent 同语义：结尾换行不产生额外空行）。
func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	s := strings.TrimRight(string(data), "\n")
	if s == "" {
		return 1 // 文件仅由换行组成 → 视为 1 个空行
	}
	return strings.Count(s, "\n") + 1
}

// readContent 读取并切片文件内容（start/limit、tail、ranges、全文）。
func readContent(data []byte, req readReq) (string, error) {
	if IsBinaryBytes(data) {
		return DataURI(data), nil
	}
	content := DecodeText(data)
	lines := strings.Split(content, "\n")
	// 去掉末尾空行（文件以 \n 结尾）
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var selected []string
	switch {
	case len(req.ranges) > 0:
		var segs []string
		for ri, r := range req.ranges {
			startIdx := clamp(r[0], 1, len(lines))
			endIdx := clamp(r[1], startIdx, len(lines))
			seg := lines[startIdx-1 : endIdx]
			label := fmt.Sprintf("-- segment %d: lines %d-%d --", ri+1, startIdx, endIdx)
			if req.lineNumbers {
				segs = append(segs, label+"\n"+addLineNumbers(seg, startIdx))
			} else {
				segs = append(segs, label+"\n"+strings.Join(seg, "\n"))
			}
		}
		return strings.Join(segs, "\n\n"), nil
	case req.tail > 0:
		tailN := req.tail
		if tailN > len(lines) {
			tailN = len(lines)
		}
		selected = lines[len(lines)-tailN:]
	case req.start > 0 || req.limit > 0:
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
		selected = lines[startIdx-1 : endIdx]
	default:
		selected = lines
	}

	if req.lineNumbers {
		base := 1
		if req.start > 0 {
			base = req.start
		} else if req.tail > 0 {
			base = len(lines) - req.tail + 1
		}
		return addLineNumbers(selected, base), nil
	}
	return strings.Join(selected, "\n"), nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func addLineNumbers(lines []string, start int) string {
	width := len(fmt.Sprintf("%d", start+len(lines)-1))
	if width < 2 {
		width = 2
	}
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%*d  %s", width, start+i, line)
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

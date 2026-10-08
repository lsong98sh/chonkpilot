// Package fetch 实现 fetch 工具：HTTP 请求 / 下载（自持实现，零主仓库依赖）。
//
// 能力（对齐 chonkpilot HandleFetch）:
//   - method / body / form(form_files) / headers / cookies
//   - save_as 下载（落盘路径须绝对或以 ~/ 开头，R-11）
//   - readTimeout 总超时（响应头 + body 全程；<10 秒视为 10 秒）
//   - follow_redirect / encoding 转码
//   - 响应体读取硬上限 maxFetchBytes（32MB，防无上限读入内存）；超限截断后仍由 executor
//     统一层接管（见 internal/cli maxOutputBytes：>200KB 落临时文件）
//
// 与 chonkpilot 的差异（共通 executor 定位）:
//   - 文件操作参数路径强约束（R-11）：save_as / form_files[].path 须为绝对路径或以 ~/
//     开头的用户目录路径，相对路径即报错（见 internal/fileops/pathcheck.go）
//   - 无取消检查（同步一次完成）
//   - 无 datadir：不写结果文件，超长由 executor 统一层处理
//   - 文案内联（无 i18n）
package fetch

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fileops"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// maxFetchBytes 是响应体读取硬上限（32MB）：防无上限读入内存；超限截断后仍走 executor
// 统一层「>200KB 落临时文件」通道（见 internal/cli maxOutputBytes）。
const maxFetchBytes = 32 << 20

// HandleFetch 执行 HTTP 请求。
func HandleFetch(workDir string, args map[string]interface{}) *cli.Result {
	url, _ := args["url"].(string)
	if url == "" {
		return cli.Err("web_fetch", "parameter 'url' is required")
	}

	method := "GET"
	if m, _ := args["method"].(string); m != "" {
		method = strings.ToUpper(m)
	}

	// save_as 路径（R-11：须绝对或以 ~/ 开头；违规整体失败）
	saveAs := ""
	if sa, _ := args["save_as"].(string); sa != "" {
		resolved, msg := fileops.ValidateFilePath(sa)
		if msg != "" {
			return cli.Err("web_fetch", "save_as："+msg)
		}
		// agentbox 沙箱（仅隔离开启时生效）：落盘路径须在允许写目录内
		if err := agentbox.Check(resolved, true); err != nil {
			return cli.Err("web_fetch", "save_as："+err.Error())
		}
		saveAs = resolved
	}

	// 请求体：form/form_files → multipart；body → 原始串
	var bodyReader io.Reader
	var contentType string
	if form, ok := args["form"].(map[string]interface{}); ok && len(form) > 0 {
		method = "POST"
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		for k, v := range form {
			if s, ok := v.(string); ok {
				writer.WriteField(k, s)
			}
		}
		if files, ok := args["form_files"].([]interface{}); ok {
			for i, f := range files {
				fm, ok := f.(map[string]interface{})
				if !ok {
					continue
				}
				field, _ := fm["field"].(string)
				fpath, _ := fm["path"].(string)
				if field == "" || fpath == "" {
					continue
				}
				// form_files[].path（R-11：须绝对或以 ~/ 开头；违规整体失败）
				abs, msg := fileops.ValidateFilePath(fpath)
				if msg != "" {
					return cli.Err("web_fetch", fmt.Sprintf("form_files[%d].path：%s", i, msg))
				}
				// agentbox 沙箱（仅隔离开启时生效）：上传文件须在允许读目录内
				if err := agentbox.Check(abs, false); err != nil {
					return cli.Err("web_fetch", fmt.Sprintf("form_files[%d].path：%s", i, err.Error()))
				}
				fh, err := os.Open(abs)
				if err != nil {
					return cli.Err("web_fetch", fmt.Sprintf("failed to open form file %s: %s", fpath, err))
				}
				part, err := writer.CreateFormFile(field, filepath.Base(abs))
				if err != nil {
					fh.Close()
					return cli.Err("web_fetch", fmt.Sprintf("failed to create form file %s: %s", fpath, err))
				}
				_, _ = io.Copy(part, fh)
				fh.Close()
			}
		}
		writer.Close()
		bodyReader = body
		contentType = writer.FormDataContentType()
	} else if b, ok := args["body"].(string); ok && b != "" {
		bodyReader = bytes.NewReader([]byte(b))
	}

	// readTimeout：总超时（响应头 + body 全程）；显式给出即生效，隐式下限 10s（C-09，
	// 与 web_fetch.tool.md / 本文件头注一致：小于 10 秒按 10 秒处理；未给出 → 默认 300）。
	readTimeoutSec := 300
	if t, ok := args["readTimeout"].(float64); ok && t > 0 {
		readTimeoutSec = int(t)
		if readTimeoutSec < 10 {
			readTimeoutSec = 10
		}
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return cli.Err("web_fetch", fmt.Sprintf("failed to create request: %s", err))
	}

	if headers, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if s, ok := v.(string); ok {
				req.Header.Set(k, s)
			}
		}
	}
	if cookies, ok := args["cookies"].(map[string]interface{}); ok && len(cookies) > 0 {
		var parts []string
		for k, v := range cookies {
			if s, ok := v.(string); ok {
				parts = append(parts, k+"="+s)
			}
		}
		if len(parts) > 0 {
			req.Header.Set("Cookie", strings.Join(parts, "; "))
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

	followRedirect := true
	if fr, ok := args["follow_redirect"].(bool); ok {
		followRedirect = fr
	}
	client := &http.Client{Timeout: time.Duration(readTimeoutSec) * time.Second}
	if !followRedirect {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "Client.Timeout") {
			return cli.Err("web_fetch", fmt.Sprintf("no data received within %ds read timeout", readTimeoutSec))
		}
		return cli.Err("web_fetch", fmt.Sprintf("request failed: %s", err))
	}
	defer resp.Body.Close()

	// 响应体读取（硬上限 maxFetchBytes；+1 探测是否超限）
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return cli.Err("web_fetch", fmt.Sprintf("failed to read response: %s", err))
	}
	truncated := len(data) > maxFetchBytes
	if truncated {
		data = data[:maxFetchBytes]
	}

	// encoding 转码
	if encName, _ := args["encoding"].(string); encName != "" {
		if enc := getEncoding(encName); enc != nil {
			if decoded, _, err := transform.String(enc.NewDecoder(), string(data)); err == nil {
				data = []byte(decoded)
			}
		}
	}

	// save_as 下载模式（主动重定向参数：进程产物直写文件）
	if saveAs != "" {
		if err := os.MkdirAll(filepath.Dir(saveAs), 0755); err != nil {
			return cli.Err("web_fetch", fmt.Sprintf("failed to create directory for save_as: %s", err))
		}
		if err := os.WriteFile(saveAs, data, 0644); err != nil {
			return cli.Err("web_fetch", fmt.Sprintf("failed to write save_as file: %s", err))
		}
		sizeMB := float64(len(data)) / (1024 * 1024)
		msg := fmt.Sprintf("📥 已下载到 %s（%.2f MB，HTTP %d）", saveAs, sizeMB, resp.StatusCode)
		if truncated {
			msg += "（响应体超过 32MB，已截断）"
		}
		return cli.Ok("web_fetch", msg, map[string]interface{}{
			"status_code": resp.StatusCode, "content_type": resp.Header.Get("Content-Type"),
			"body_length": len(data), "saved_to": saveAs,
		})
	}

	// 普通模式：headers + body（超限由统一层封装）
	var headerBuf strings.Builder
	for k, vals := range resp.Header {
		for _, v := range vals {
			headerBuf.WriteString(fmt.Sprintf("%s: %s\n", k, v))
		}
	}
	headers := strings.TrimSpace(headerBuf.String())
	bodyStr := string(data)
	msg := fmt.Sprintf("✅ HTTP %d %s\n%s\n\n%s", resp.StatusCode, resp.Status, headers, bodyStr)
	if truncated {
		msg += "\n...（响应体超过 32MB，已截断）"
	}
	return cli.Ok("web_fetch", msg, map[string]interface{}{
		"status_code": resp.StatusCode, "content_type": resp.Header.Get("Content-Type"),
		"body_length": len(data),
	})
}

// getEncoding 按名称返回编码器（nil = utf-8 或未知，原样处理）。
func getEncoding(name string) encoding.Encoding {
	switch strings.ToLower(strings.TrimSpace(name)) {
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
		return nil
	}
}

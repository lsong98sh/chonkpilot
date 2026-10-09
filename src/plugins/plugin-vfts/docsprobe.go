// docsprobe.go：探测「文档转换服务」（mcps/markitdown）——**只读状态文件 + 探活，不 spawn**。
//
// 用户定稿约束：插件**不启动**转换服务（由用户在 MCP 配置页手动注册/启动），只探测：
//  1. 读状态文件（两处都探）：① <exeDir>/mcps/markitdown/state.json ② <data-dir>/mcps/markitdown/state.json
//     （Windows data-dir = %LOCALAPPDATA%\chonkpilot；exeDir = 本进程 exe 所在目录 = 产品安装根）；
//  2. 校验状态文件的 pid 与 `GET /vfts/health` 返回的 pid 一致且 ok → 视为可用，取 {port, token, version}。
//
// 探测失败一律当作「未运行」（不报错、不阻塞）；结果按 TTL 缓存（复探最长 30s 间隔，不高频轮询）。
//
// 安全：状态文件含 token，**只入内存、绝不写日志**（日志只记 port/version）。
package vfts

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	// docsProbeTTL 探测结果缓存时长（复探最长间隔；索引前另有强制复探）。
	docsProbeTTL = 30 * time.Second
	// docStateRel 状态文件相对路径（exeDir / data-dir 下同构）。
	docStateRel = "mcps/markitdown/state.json"
	// docTextMaxBytes 单文件转换文本上限（默认 2 MiB，与转换服务输出上限一致）。
	docTextMaxBytes = int64(2) << 20
	// docsHealthTimeout /vfts/health 探活超时（本地回环，短超时避免阻塞）。
	docsHealthTimeout = 2 * time.Second
)

// docService 探测到的转换服务（可用时的接入信息）。
type docService struct {
	Port    int
	Token   string // 仅内存；绝不写日志
	Version string
}

// docStatePaths 状态文件候选路径（去重、保序）。
func docStatePaths() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		key := filepath.Clean(p)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, key)
	}
	if exe, err := os.Executable(); err == nil {
		add(filepath.Join(filepath.Dir(exe), filepath.FromSlash(docStateRel)))
	}
	add(filepath.Join(dataDir(), filepath.FromSlash(docStateRel)))
	return out
}

// dataDir 用户级数据根（Windows = %LOCALAPPDATA%\chonkpilot）。
func dataDir() string {
	if os.Getenv("LOCALAPPDATA") != "" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "chonkpilot")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "AppData", "Local", "chonkpilot")
	}
	return ""
}

// docState 状态文件内容（见 src/mcps/markitdown/README.md §2）。
type docState struct {
	Pid     int    `json:"pid"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	Version string `json:"version"`
}

// readDocState 读状态文件（失败 → ok=false）。
func readDocState(path string) (docState, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return docState{}, false
	}
	var st docState
	if json.Unmarshal(b, &st) != nil || st.Port <= 0 || st.Pid <= 0 {
		return docState{}, false
	}
	return st, true
}

// docHealth `GET /vfts/health` 应答（无需 token）。
type docHealth struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	Pid     int    `json:"pid"`
	Port    int    `json:"port"`
}

var docsHealthClient = &http.Client{Timeout: docsHealthTimeout}

// probeDocHealth 探活指定端口（失败 → ok=false）。
func probeDocHealth(port int) (docHealth, bool) {
	var h docHealth
	url := fmt.Sprintf("http://127.0.0.1:%d/vfts/health", port)
	resp, err := docsHealthClient.Get(url)
	if err != nil {
		return h, false
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	if dec.Decode(&h) != nil || !h.OK {
		return h, false
	}
	return h, true
}

// probeConverterOnce 单次探测：两处状态文件逐个试 → pid 校验通过即视为可用。
// 失败一律返回 nil（不报错）；日志只记 port/version（**不记 token**）。
func probeConverterOnce(logf func(string, ...any)) *docService {
	return probeConverterPaths(docStatePaths(), logf)
}

// probeConverterPaths 按给定状态文件候选路径探测（抽为独立函数便于单测）。
func probeConverterPaths(paths []string, logf func(string, ...any)) *docService {
	for _, path := range paths {
		st, ok := readDocState(path)
		if !ok {
			continue
		}
		h, ok := probeDocHealth(st.Port)
		if !ok || h.Pid != st.Pid {
			continue // 探活失败 / pid 不一致（陈旧状态文件）→ 视作未运行
		}
		ver := h.Version
		if ver == "" {
			ver = st.Version
		}
		if logf != nil {
			logf("vfts: 文档转换服务已就绪（port=%d version=%s）", st.Port, ver)
		}
		return &docService{Port: st.Port, Token: st.Token, Version: ver}
	}
	return nil
}

// docBaseURL 转换服务基址（无尾部斜杠）。
func (d *docService) baseURL() string {
	if d == nil || d.Port <= 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", d.Port)
}

// probeDocsService 探测（TTL 缓存）：force=true 跳过缓存（索引前 / 开关变更后复探）。
func (p *Vfts) probeDocsService(force bool) *docService {
	p.probeMu.Lock()
	defer p.probeMu.Unlock()
	if !force && !p.probeAt.IsZero() && time.Since(p.probeAt) < docsProbeTTL {
		return p.probeCache
	}
	p.probeCache = probeConverterOnce(p.logf)
	p.probeAt = time.Now()
	return p.probeCache
}

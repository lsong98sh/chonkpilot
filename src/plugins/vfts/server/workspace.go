// Package server 实现 chonkpilot-vfts 引擎 lib：
// 基于 zvec（C-API）FTS 全文索引的 workdir 工作区，索引落 <workdir>/.chonkpilot/vfts/，
// 提供 configure / index / query / status 四个工具面。
//
// 阶段 1 只做 FTS（零 embedding 依赖）：索引内容 = 纯文本（源码 + .txt + .md）。
// 向量语义检索（HNSW）后置为阶段 2。
package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	zvec "github.com/zvec-ai/zvec-go"
)

// Meta 工作区元信息（meta.json），跨进程可读（status 与就绪门控用）。
type Meta struct {
	Enabled       bool     `json:"enabled"`
	State         string   `json:"state"` // "" 未初始化 | indexing | ready | error
	ProgressDone  int      `json:"progressDone"`
	ProgressTotal int      `json:"progressTotal"`
	Err           string   `json:"err,omitempty"`
	LastIndexedAt int64    `json:"lastIndexedAt"`
	SkipDirs      []string `json:"skipDirs,omitempty"`
	Exts          []string `json:"exts,omitempty"`
	FileCount     int      `json:"fileCount"`
	ChunkCount    int      `json:"chunkCount"`
	Tokenizer     string   `json:"tokenizer"`
	// NextPK 是增量索引已分配的最大文档主键（纯数字字符串，跨调用单调递增，
	// 避免与全量重建的 1..N 主键冲突）。全量重建会重置集合，故同步重置为 N。
	NextPK int64 `json:"nextPK,omitempty"`
}

// Workspace 引擎内部工作区：Dir=项目根；索引与元信息落在 Store。
type Workspace struct {
	Dir   string // 规范绝对路径（'/' 分隔）
	Store string // Dir/.chonkpilot/vfts

	mu    sync.Mutex // 保护 meta / coll 指针
	recMu sync.Mutex // 就绪/重建单飞：同一 workdir 不平行执行
	zmu   sync.Mutex // zvec 集合操作串行（底层句柄非并发安全）
	coll  *zvec.Collection
	meta  Meta
}

const (
	metaName  = "meta.json"
	collDir   = "collection"
	tokenizer = "standard" // FTS 分词器：standard（中英文均可命中，无需外置词典）
)

var wsMu sync.Mutex
var wsReg = map[string]*Workspace{} // Dir → Workspace（进程内常驻复用）

// StoreDir 返回工作区落盘目录。
func StoreDir(dir string) string {
	return filepath.ToSlash(filepath.Join(dir, ".chonkpilot", "vfts"))
}

// collPath zvec 集合目录（OS 原生分隔符，供 C-API 使用）。
func (w *Workspace) collPath() string {
	return filepath.Join(filepath.FromSlash(w.Store), collDir)
}

// Open 获取（进程内复用或从盘恢复）指定 workdir 的工作区。
func Open(dir string) (*Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("abs %s: %w", dir, err)
	}
	abs = filepath.ToSlash(abs)
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", abs, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", abs)
	}
	wsMu.Lock()
	defer wsMu.Unlock()
	if w, ok := wsReg[abs]; ok {
		return w, nil
	}
	w := &Workspace{Dir: abs, Store: StoreDir(abs)}
	if err := os.MkdirAll(filepath.FromSlash(w.Store), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir store: %w", err)
	}
	_ = w.loadMeta()
	wsReg[abs] = w
	return w, nil
}

// Drop 释放进程内缓存（测试用）。
func Drop(dir string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	abs = filepath.ToSlash(abs)
	wsMu.Lock()
	defer wsMu.Unlock()
	if w, ok := wsReg[abs]; ok {
		w.zmu.Lock()
		if w.coll != nil {
			_ = w.coll.Close()
			w.coll = nil
		}
		w.zmu.Unlock()
		delete(wsReg, abs)
	}
}

func (w *Workspace) Meta() Meta {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.meta
}

func (w *Workspace) loadMeta() error {
	b, err := os.ReadFile(filepath.Join(filepath.FromSlash(w.Store), metaName))
	if err != nil {
		return err // 首次无 meta 属正常
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("meta.json 解析失败: %w", err)
	}
	w.meta = m
	return nil
}

// ReloadMeta 从盘重读 meta（跨进程/跨调用刷新状态视图）。
func (w *Workspace) ReloadMeta() error {
	return w.loadMeta()
}

func (w *Workspace) saveMeta() error {
	w.mu.Lock()
	b, err := json.MarshalIndent(w.meta, "", "  ")
	w.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(filepath.FromSlash(w.Store), metaName), b)
}

// writeFileAtomic 临时文件 + rename 原子落盘。
func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Status 返回工作区状态视图（工具/plugin 查询用）。
type Status struct {
	Workdir       string   `json:"workdir"`
	State         string   `json:"state"`
	Enabled       bool     `json:"enabled"`
	ProgressDone  int      `json:"progressDone"`
	ProgressTotal int      `json:"progressTotal"`
	Err           string   `json:"err,omitempty"`
	LastIndexedAt int64    `json:"lastIndexedAt"`
	SkipDirs      []string `json:"skipDirs,omitempty"`
	Exts          []string `json:"exts,omitempty"`
	IndexedFiles  int      `json:"indexedFiles"`
	ChunkCount    int      `json:"chunkCount"`
	Tokenizer     string   `json:"tokenizer"`
	Loaded        bool     `json:"loaded"` // 当前进程内存已载入集合
}

func (w *Workspace) Status() Status {
	w.mu.Lock()
	m := w.meta
	loaded := w.coll != nil
	w.mu.Unlock()
	exts := m.Exts
	if len(exts) == 0 {
		exts = defaultExts()
	}
	return Status{Workdir: w.Dir, State: m.State, Enabled: m.Enabled,
		ProgressDone: m.ProgressDone, ProgressTotal: m.ProgressTotal, Err: m.Err,
		LastIndexedAt: m.LastIndexedAt, SkipDirs: append([]string{}, m.SkipDirs...),
		Exts: append([]string{}, exts...), IndexedFiles: m.FileCount,
		ChunkCount: m.ChunkCount, Tokenizer: tokenizer, Loaded: loaded}
}

// abs 相对 → workdir 绝对（'/' 分隔）。
func (w *Workspace) abs(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, w.Dir+"/") {
		return p
	}
	if strings.HasPrefix(p, "/") {
		return w.Dir + p
	}
	return w.Dir + "/" + p
}

// norm 把入参（绝对或相对）归一为 store 用的相对形式。
func (w *Workspace) norm(p string) string {
	if p == "" {
		return ""
	}
	s := filepath.ToSlash(p)
	if strings.HasPrefix(strings.ToLower(s), strings.ToLower(w.Dir)+"/") {
		s = s[len(w.Dir)+1:]
	}
	return cleanPath(s)
}

// CloseAll 关闭全部进程内集合并释放 zvec 资源（进程退出前调用）。
func CloseAll() {
	wsMu.Lock()
	for _, w := range wsReg {
		w.zmu.Lock()
		if w.coll != nil {
			_ = w.coll.Close()
			w.coll = nil
		}
		w.zmu.Unlock()
	}
	wsMu.Unlock()
	shutdownZvec()
}

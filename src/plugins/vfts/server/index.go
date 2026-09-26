package server

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	zvec "github.com/zvec-ai/zvec-go"
)

// 索引规模约束（对齐 codegraph 的 maxFileBytes 语义）。
var maxFileBytes = 8 << 20 // 8MB

const (
	maxChunkChars = 4000 // 单块最大字符数
	maxChunkLines = 80   // 单块最大行数
)

// ErrNotInitialized 未初始化（查询门控用）。
var ErrNotInitialized = fmt.Errorf("全文索引未初始化（请先 vfts_index）")

// defaultSkipDirs 默认跳过的目录名（与 codegraph 一致，必须排除 .chonkpilot 自身索引目录）。
func defaultSkipDirs() []string {
	return []string{".git", ".svn", ".hg", "node_modules", "__pycache__",
		".venv", "venv", ".trae", ".chonkpilot", "dist", "build",
		".next", ".nuxt", "out", "target", "vendor"}
}

// Configure 设置 enabled / exts / skip_dirs（引擎侧同步状态，可见性门控由 plugin 完成）。
// exts / skipDirs 传 nil 表示不改。
func (w *Workspace) Configure(enabled *bool, exts, skipDirs []string) error {
	w.mu.Lock()
	if enabled != nil {
		w.meta.Enabled = *enabled
	}
	if exts != nil {
		w.meta.Exts = normalizeExts(exts)
	}
	if skipDirs != nil {
		w.meta.SkipDirs = append([]string{}, skipDirs...)
	}
	w.mu.Unlock()
	return w.saveMeta()
}

// normalizeExts 归一扩展名列表（去空、补点、小写、去重、排序）。
func normalizeExts(exts []string) []string {
	set := newExtSet(exts)
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// skipSet 目录名跳过集合（默认 ∪ 用户 skip_dirs）。
func (w *Workspace) skipSet() map[string]bool {
	m := map[string]bool{}
	for _, d := range defaultSkipDirs() {
		m[d] = true
	}
	w.mu.Lock()
	for _, d := range w.meta.SkipDirs {
		if d != "" {
			m[d] = true
		}
	}
	w.mu.Unlock()
	return m
}

// extSet 生效的扩展名集合（未配置 → 默认集）。
func (w *Workspace) extSet() map[string]bool {
	w.mu.Lock()
	exts := w.meta.Exts
	w.mu.Unlock()
	if len(exts) == 0 {
		exts = defaultExts()
	}
	return newExtSet(exts)
}

// fileEntry 扫描到的待索引文件。
type fileEntry struct {
	path  string // 相对 workdir（'/' 分隔）
	size  int64
	mtime int64
}

// collectFiles 扫描受支持文本文件清单（含 stat）。
func (w *Workspace) collectFiles() ([]fileEntry, error) {
	skips := w.skipSet()
	exts := w.extSet()
	var out []fileEntry
	err := filepath.WalkDir(filepath.FromSlash(w.Dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != filepath.FromSlash(w.Dir) && skips[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !exts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > int64(maxFileBytes) {
			return nil
		}
		rel := relOf(filepath.FromSlash(w.Dir), p)
		out = append(out, fileEntry{path: rel, size: info.Size(), mtime: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// chunksOf 读取单文件并切分为文档块（二进制/超限/读失败返回错误或空）。
func (w *Workspace) chunksOf(e fileEntry) ([]chunk, error) {
	abs := filepath.FromSlash(joinPath(w.Dir, e.path))
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	if isBinary(data) {
		return nil, fmt.Errorf("binary file skipped")
	}
	text := normalizeText(string(data))
	lines := strings.Split(text, "\n")
	var out []chunk
	i := 0
	for i < len(lines) {
		start := i
		var buf strings.Builder
		for i < len(lines) {
			line := lines[i]
			if buf.Len() > 0 && (buf.Len()+len(line) > maxChunkChars || i-start >= maxChunkLines) {
				break
			}
			buf.WriteString(line)
			buf.WriteString("\n")
			i++
			if buf.Len() >= maxChunkChars {
				break
			}
		}
		txt := buf.String()
		if strings.TrimSpace(txt) == "" {
			continue
		}
		out = append(out, chunk{
			path: e.path,
			line: start + 1,
			text: txt,
		})
	}
	return out, nil
}

// setState 更新 meta 状态并落盘。
func (w *Workspace) setState(state string) {
	w.mu.Lock()
	w.meta.State = state
	w.mu.Unlock()
	_ = w.saveMeta()
}

func (w *Workspace) markError(msg string) {
	w.mu.Lock()
	w.meta.State = "error"
	w.meta.Err = msg
	w.mu.Unlock()
	_ = w.saveMeta()
}

// IndexedFile 单文件的索引结果（插件回写 file_list 清单用）。
type IndexedFile struct {
	Path   string   `json:"path"`          // 相对 workdir（'/' 分隔）
	Key    string   `json:"key,omitempty"` // 插件侧唯一 key（原样透传）
	DocIDs []string `json:"doc_ids"`       // 该文件在 zvec 中的文档主键（按文件删除用）
	Chunks int      `json:"chunks"`        // 该文件的块数
}

// IndexResult 一次索引（全量/增量）的处理计数与逐文件结果。
// 依据 zvec-go v0.7.0：文档主键为字符串（SetPK(string)），按文件删除 = Delete(pks []string)。
type IndexResult struct {
	Mode          string        `json:"mode"`          // full | incremental
	Added         int           `json:"added"`         // 新增（原无块 → 有块）文件数
	Updated       int           `json:"updated"`       // 更新（先删旧块再重建）文件数
	Removed       int           `json:"removed"`       // 删除文件数（remove 项含旧块）
	RemovedChunks int           `json:"removedChunks"` // 删除的旧块数
	Chunks        int           `json:"chunks"`        // 本次新写入块数
	Indexed       []IndexedFile `json:"indexed"`
}

// IncrementalFile 待增量索引的文件：先按 doc_ids 删除旧块，再重新切块写入。
type IncrementalFile struct {
	Path   string   `json:"path"`
	Key    string   `json:"key,omitempty"`
	DocIDs []string `json:"doc_ids,omitempty"`
}

// IncrementalRemove 待删除的文件（仅按 doc_ids 删旧块，不重新索引）。
type IncrementalRemove struct {
	Key    string   `json:"key,omitempty"`
	DocIDs []string `json:"doc_ids,omitempty"`
}

// Initialize 全量重建 FTS 索引（同步，直到完成并落盘）。
// 重复调用为幂等重建：先关闭并清空集合目录，再重新建索引。
// exts / skipDirs 传非 nil 时先更新配置。返回逐文件 doc_ids（插件重建清单用）。
func (w *Workspace) Initialize(exts, skipDirs []string) (*IndexResult, error) {
	if exts != nil || skipDirs != nil {
		if err := w.Configure(nil, exts, skipDirs); err != nil {
			return nil, err
		}
	}
	w.mu.Lock()
	w.meta.State = "indexing"
	w.meta.Err = ""
	w.meta.ProgressDone = 0
	w.mu.Unlock()
	if err := w.saveMeta(); err != nil {
		return nil, err
	}

	entries, err := w.collectFiles()
	if err != nil {
		w.markError(fmt.Sprintf("scan: %v", err))
		return nil, err
	}
	total := len(entries)
	w.mu.Lock()
	w.meta.ProgressTotal = total
	w.mu.Unlock()

	// 关闭旧集合并清空落盘目录（全量重建）
	w.zmu.Lock()
	defer w.zmu.Unlock()
	if w.coll != nil {
		_ = w.coll.Close()
		w.coll = nil
	}
	if err := os.RemoveAll(w.collPath()); err != nil {
		w.markError(fmt.Sprintf("clean store: %v", err))
		return nil, err
	}
	// zvec CreateAndOpen 要求目标路径不存在：仅保证父目录存在，不预建集合目录。
	if err := os.MkdirAll(filepath.FromSlash(w.Store), 0o755); err != nil {
		w.markError(fmt.Sprintf("mkdir store: %v", err))
		return nil, err
	}
	coll, err := createCollection(w.collPath())
	if err != nil {
		w.markError(fmt.Sprintf("create collection: %v", err))
		return nil, err
	}

	files, chunks := 0, 0
	seq := 0
	indexed := make([]IndexedFile, 0, len(entries))
	for i, e := range entries {
		cs, cerr := w.chunksOf(e)
		var ids []string
		if cerr == nil && len(cs) > 0 {
			ids = make([]string, 0, len(cs))
			for j := range cs {
				seq++
				cs[j].pk = strconv.Itoa(seq)
				ids = append(ids, cs[j].pk)
			}
			if err := insertChunks(coll, cs); err != nil {
				_ = coll.Close()
				w.markError(fmt.Sprintf("insert %s: %v", e.path, err))
				return nil, err
			}
			files++
			chunks += len(cs)
		}
		indexed = append(indexed, IndexedFile{Path: e.path, DocIDs: ids, Chunks: len(cs)})
		if (i+1)%50 == 0 {
			w.mu.Lock()
			w.meta.ProgressDone = i + 1
			w.mu.Unlock()
			_ = w.saveMeta()
		}
	}
	if err := coll.Flush(); err != nil {
		_ = coll.Close()
		w.markError(fmt.Sprintf("flush: %v", err))
		return nil, err
	}
	w.coll = coll

	w.mu.Lock()
	w.meta.State = "ready"
	w.meta.Err = ""
	w.meta.ProgressDone = total
	w.meta.ProgressTotal = total
	w.meta.FileCount = files
	w.meta.ChunkCount = chunks
	w.meta.Tokenizer = tokenizer
	w.meta.LastIndexedAt = time.Now().UnixNano()
	w.meta.NextPK = int64(seq) // 全量重建后主键序列重置为 N
	w.mu.Unlock()
	if err := w.saveMeta(); err != nil {
		return nil, err
	}
	return &IndexResult{Mode: "full", Added: files, Chunks: chunks, Indexed: indexed}, nil
}

// Incremental 按文件增量：先按 files.doc_ids ∪ removes.doc_ids 删除旧块，再对 files
// 逐文件重新切块写入（主键用单调递增的纯数字串，避免与既有主键冲突）。
// 集合不存在时新建（等价首次索引）；不传 files/remove 请改用 Initialize（全量重建）。
func (w *Workspace) Incremental(files []IncrementalFile, removes []IncrementalRemove) (*IndexResult, error) {
	w.recMu.Lock()
	defer w.recMu.Unlock()
	w.mu.Lock()
	w.meta.State = "indexing"
	w.meta.Err = ""
	w.meta.ProgressDone = 0
	w.meta.ProgressTotal = len(files)
	w.mu.Unlock()
	if err := w.saveMeta(); err != nil {
		return nil, err
	}

	w.zmu.Lock()
	defer w.zmu.Unlock()
	coll, err := w.ensureCollLocked()
	if err != nil {
		w.markError(fmt.Sprintf("open collection: %v", err))
		return nil, err
	}

	// 1) 收集待删旧块主键（更新文件旧块 + 删除文件旧块，去重）
	delIDs := make([]string, 0, len(files)+len(removes))
	seen := map[string]bool{}
	addDel := func(ids []string) {
		for _, id := range ids {
			if id != "" && !seen[id] {
				seen[id] = true
				delIDs = append(delIDs, id)
			}
		}
	}
	for _, f := range files {
		addDel(f.DocIDs)
	}
	for _, r := range removes {
		addDel(r.DocIDs)
	}
	removedChunks := len(delIDs)
	if removedChunks > 0 {
		if err := deleteChunks(coll, delIDs); err != nil {
			w.markError(fmt.Sprintf("delete old chunks: %v", err))
			return nil, err
		}
	}

	// 2) 逐文件重建块
	res := &IndexResult{Mode: "incremental", RemovedChunks: removedChunks, Indexed: make([]IndexedFile, 0, len(files))}
	base := time.Now().UnixNano()
	if w.Meta().NextPK > base {
		base = w.Meta().NextPK
	}
	seq := base
	fileDelta := 0
	for _, f := range files {
		rel := w.norm(f.Path)
		if rel == "" {
			continue
		}
		cs, cerr := w.chunksOf(fileEntry{path: rel})
		var ids []string
		if cerr == nil && len(cs) > 0 {
			ids = make([]string, 0, len(cs))
			for j := range cs {
				seq++
				cs[j].pk = strconv.FormatInt(seq, 10)
				ids = append(ids, cs[j].pk)
			}
			if err := insertChunks(coll, cs); err != nil {
				w.markError(fmt.Sprintf("insert %s: %v", rel, err))
				return nil, err
			}
		}
		res.Indexed = append(res.Indexed, IndexedFile{Path: rel, Key: f.Key, DocIDs: ids, Chunks: len(cs)})
		res.Chunks += len(cs)
		if len(f.DocIDs) > 0 {
			res.Updated++
			if len(ids) == 0 {
				fileDelta-- // 内容变成空/二进制 → 该文件不再有块
			}
		} else if len(ids) > 0 {
			res.Added++
			fileDelta++
		}
	}
	if err := coll.Flush(); err != nil {
		w.markError(fmt.Sprintf("flush: %v", err))
		return nil, err
	}

	// 3) 删除文件计数（按 remove 项含旧块计），并更新聚合计数
	for _, r := range removes {
		if len(r.DocIDs) > 0 {
			res.Removed++
			fileDelta--
		}
	}

	w.mu.Lock()
	w.meta.State = "ready"
	w.meta.Err = ""
	w.meta.ProgressDone = len(files)
	w.meta.ProgressTotal = len(files)
	if w.meta.FileCount+fileDelta >= 0 {
		w.meta.FileCount += fileDelta
	}
	if w.meta.ChunkCount+res.Chunks-removedChunks >= 0 {
		w.meta.ChunkCount += res.Chunks - removedChunks
	}
	w.meta.Tokenizer = tokenizer
	w.meta.LastIndexedAt = time.Now().UnixNano()
	if seq > w.meta.NextPK {
		w.meta.NextPK = seq
	}
	w.mu.Unlock()
	if err := w.saveMeta(); err != nil {
		return nil, err
	}
	return res, nil
}

// ensureCollLocked 确保集合已打开（须持 zmu）：内存有则复用；盘上有则打开；否则新建。
func (w *Workspace) ensureCollLocked() (*zvec.Collection, error) {
	if w.coll != nil {
		return w.coll, nil
	}
	if _, err := os.Stat(w.collPath()); err == nil {
		coll, err := openCollection(w.collPath())
		if err != nil {
			return nil, err
		}
		w.coll = coll
		return coll, nil
	}
	// zvec CreateAndOpen 要求目标路径不存在：仅保证父目录存在。
	if err := os.MkdirAll(filepath.FromSlash(w.Store), 0o755); err != nil {
		return nil, err
	}
	coll, err := createCollection(w.collPath())
	if err != nil {
		return nil, err
	}
	w.coll = coll
	return coll, nil
}

// EnsureReady 查询前的统一就绪处理：确认集合可用并返回状态。
// state 返回 ready/indexing/not_initialized/error。
func (w *Workspace) EnsureReady() (string, error) {
	w.recMu.Lock()
	defer w.recMu.Unlock()
	w.mu.Lock()
	st := w.meta.State
	errMsg := w.meta.Err
	has := w.coll != nil
	w.mu.Unlock()

	if !has {
		if _, err := os.Stat(w.collPath()); err != nil {
			return "not_initialized", ErrNotInitialized
		}
		coll, err := openCollection(w.collPath())
		if err != nil {
			return "error", fmt.Errorf("打开 vfts 集合失败: %w", err)
		}
		w.mu.Lock()
		w.coll = coll
		w.mu.Unlock()
	}
	switch st {
	case "error":
		return "error", fmt.Errorf("索引出错：%s", errMsg)
	case "indexing":
		return "indexing", nil
	case "ready":
		return "ready", nil
	default:
		return "not_initialized", ErrNotInitialized
	}
}

// Query 执行 FTS 检索，返回文件级命中（去重后按相关度，最多 topK 个文件）。
// match = 自然语言匹配串；expr = 布尔/高级表达式（二者至少其一）。
func (w *Workspace) Query(match, expr string, topK int, pathFilter string) ([]ftsHit, error) {
	if topK <= 0 {
		topK = 20
	}
	w.zmu.Lock()
	defer w.zmu.Unlock()
	coll := w.coll
	if coll == nil {
		return nil, ErrNotInitialized
	}
	// 多取文档块，再做文件级去重，保证 topK 个文件有候选
	rawTopK := topK * 5
	if rawTopK > 500 {
		rawTopK = 500
	}
	hits, err := searchChunks(coll, match, expr, rawTopK)
	if err != nil {
		return nil, err
	}
	filter := strings.ToLower(w.norm(pathFilter))
	seen := map[string]bool{}
	out := make([]ftsHit, 0, topK)
	for _, h := range hits {
		if filter != "" && !strings.Contains(strings.ToLower(h.Path), filter) {
			continue
		}
		if seen[h.Path] {
			continue
		}
		seen[h.Path] = true
		h.Path = w.abs(h.Path)
		out = append(out, h)
		if len(out) >= topK {
			break
		}
	}
	return out, nil
}

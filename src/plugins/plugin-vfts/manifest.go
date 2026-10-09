// manifest.go：vfts 增量的清单编排——扫描候选文件 → 读 file_list → diff 判定 →
// 调引擎按文件增量 → 回写 file_list 与状态。
//
// 分层：清单（file_list，项目级 prj 库）由本插件经 persist 读写；引擎只接收
// 「要索引 / 要删除的文件清单」。增量判定（用户拍板）：
//
//	① 新文件（表里无 key）        → 待索引
//	② size+mtime 未变             → 跳过（不算 md5，省 IO）
//	③ 变化 → 算 md5：与表一致     → 仅更新 mtime（不重建索引）
//	                  与表不一致   → 待索引（先按旧 doc_ids 删块）
//	④ 表里有、本次扫描没有         → 待删除（按 doc_ids 删块）
package vfts

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ignore "github.com/chonkpilot/chonkpilot-ignore"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-plugin/dataclient"
)

// data 面（文件清单，persist 订阅；域 filelist）。
const (
	subjectFileListList = msgkeys.TopicDataFilelistList
	subjectFileListPut  = msgkeys.TopicDataFilelistPut
	subjectFileListDel  = msgkeys.TopicDataFilelistDel
)

// maxFileBytes 单文件上限（与引擎 maxFileBytes / codegraph 同口径）。
const maxFileBytes = 8 << 20 // 8MB

// docExts 文档类扩展名（Office/PDF）——与引擎 docExts 同口径（独立一组，仅 docs 开启时参与）。
func docExts() []string { return []string{".docx", ".xlsx", ".pptx", ".pdf"} }

// isDocPath 是否文档类路径（按扩展名，大小写不敏感）。
func isDocPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx", ".xlsx", ".pptx", ".pdf":
		return true
	}
	return false
}

// docScanCtx 文档类清单扫描上下文（开关 / 单文件上限 / 解析器版本）。
// enabled=false → 清单不收集文档类（与引擎 collectFiles 同口径，保证清单与索引集合一致）。
type docScanCtx struct {
	enabled       bool
	maxBytes      int64
	parserVersion string
}

// tagMD5 文档类行的 md5 字段携带解析器版本，形如 `<md5hex>@<parserVersion>`
// （**复用 md5 口径**：不改 file_list 表结构；非文档类行不加后缀、语义不变）。
func tagMD5(md5hex, parserVersion string) string {
	if parserVersion == "" {
		return md5hex
	}
	return md5hex + "@" + parserVersion
}

// splitMD5Tag 拆分 `md5hex@parserVersion`（无后缀 → parserVersion 为空）。
func splitMD5Tag(field string) (md5hex, parserVersion string) {
	if i := strings.LastIndex(field, "@"); i >= 0 {
		return field[:i], field[i+1:]
	}
	return field, ""
}

// fileRec 是 file_list 表一行（字段与 persist 域一致，snake_case）。
type fileRec struct {
	Key       string   `json:"key"`
	Path      string   `json:"path"`
	Size      int64    `json:"size"`
	MTime     string   `json:"mtime"`
	MD5       string   `json:"md5"`
	DocIDs    []string `json:"doc_ids"`
	Chunks    int      `json:"chunks"`
	IndexedAt string   `json:"indexed_at"`
}

// scanEntry 扫描到的候选文件（绝对路径 ' / ' 分隔）。
type scanEntry struct {
	path  string
	size  int64
	mtime time.Time
}

// indexTask 待索引文件（含旧块主键 old，供引擎先删后插）。
type indexTask struct {
	key   string
	path  string
	size  int64
	mtime time.Time
	md5   string   // 情形③已算出；情形①为空（回写时补算）
	old   []string // 旧 doc_ids（新文件为空）
}

// manifestDiff 增量判定结果（纯数据，便于单测）。
type manifestDiff struct {
	toIndex  []indexTask
	toTouch  []fileRec // 仅 mtime 变化（md5 未变）→ 只回写 mtime/size
	toRemove []fileRec // 表里有、本次未扫到 → 待删除
	skipped  int       // size+mtime 未变 → 跳过
}

// syncStats 一次增量同步的计数（供状态回写 vfts.status）。
type syncStats struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Removed int `json:"removed"`
	Skipped int `json:"skipped"`
}

// engineIndexedFile 引擎 vfts_index 应答中的逐文件结果。
type engineIndexedFile struct {
	Path   string   `json:"path"`
	Key    string   `json:"key"`
	DocIDs []string `json:"doc_ids"`
	Chunks int      `json:"chunks"`
}

// engineIndexResult 引擎 vfts_index 应答（全量/增量共形）。
type engineIndexResult struct {
	Mode    string              `json:"mode"`
	Added   int                 `json:"added"`
	Updated int                 `json:"updated"`
	Removed int                 `json:"removed"`
	Files   int                 `json:"files"`
	Chunks  int                 `json:"chunks"`
	Indexed []engineIndexedFile `json:"indexed"`
}

// ─── 工具函数（纯/半纯，便于单测）─────────────────────────

// keyOf 文件唯一 key = sha1(绝对路径) 前 8 字节 → 16 hex（与内容无关、稳定；R-11 绝对路径口径）。
func keyOf(absPath string) string {
	sum := sha1.Sum([]byte(filepath.ToSlash(filepath.Clean(absPath))))
	return hex.EncodeToString(sum[:8])
}

// mtimeStr 统一 mtime 文本口径（RFC3339Nano，UTC 保证跨时区稳定比较）。
func mtimeStr(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// md5File 计算文件内容 md5（十六进制小写）。
func md5File(path string) (string, error) {
	f, err := os.Open(filepath.FromSlash(path))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// absOf 引擎返回的相对路径（'/' 分隔）→ 绝对路径（'/' 分隔）。
func absOf(workDir, rel string) string {
	if rel == "" {
		return ""
	}
	s := filepath.ToSlash(rel)
	if strings.HasPrefix(s, "/") || (len(s) > 1 && s[1] == ':') { // 已是绝对路径（Unix / Windows 盘符）
		return filepath.ToSlash(filepath.Clean(filepath.FromSlash(s)))
	}
	return filepath.ToSlash(filepath.Join(filepath.FromSlash(workDir), filepath.FromSlash(s)))
}

// diffManifest 计算增量差异（纯函数；hashFn 仅在情形③调用，省 IO）。
// doc.parserVersion 仅作用于**文档类**行：清单行 md5 后缀（解析器版本）变化 → 该行失效重建。
func diffManifest(scanned map[string]scanEntry, prior map[string]fileRec, hashFn func(string) (string, error), doc docScanCtx) manifestDiff {
	var d manifestDiff
	for key, e := range scanned {
		old, ok := prior[key]
		if !ok { // ① 新文件
			d.toIndex = append(d.toIndex, indexTask{key: key, path: e.path, size: e.size, mtime: e.mtime})
			continue
		}
		isDoc := isDocPath(e.path)
		_, oldPV := splitMD5Tag(old.MD5)
		pvStale := isDoc && oldPV != doc.parserVersion
		if e.size == old.Size && mtimeStr(e.mtime) == old.MTime && !pvStale { // ② 未变 → 跳过
			d.skipped++
			continue
		}
		oldMD5, _ := splitMD5Tag(old.MD5)
		h, err := hashFn(e.path) // ③ 变化 → 算 md5
		if err == nil && oldMD5 != "" && h == oldMD5 && !pvStale {
			touch := old
			touch.Size = e.size
			touch.MTime = mtimeStr(e.mtime)
			d.toTouch = append(d.toTouch, touch)
			continue
		}
		if isDoc {
			h = tagMD5(h, doc.parserVersion)
		}
		d.toIndex = append(d.toIndex, indexTask{
			key: key, path: e.path, size: e.size, mtime: e.mtime, md5: h, old: old.DocIDs,
		})
	}
	for key, old := range prior { // ④ 表里有、扫描没有 → 待删除
		if _, ok := scanned[key]; !ok {
			d.toRemove = append(d.toRemove, old)
		}
	}
	sort.Slice(d.toIndex, func(i, j int) bool { return d.toIndex[i].key < d.toIndex[j].key })
	sort.Slice(d.toTouch, func(i, j int) bool { return d.toTouch[i].Key < d.toTouch[j].Key })
	sort.Slice(d.toRemove, func(i, j int) bool { return d.toRemove[i].Key < d.toRemove[j].Key })
	return d
}

// scanFiles 扫描候选文件（按 ext 集与排除规则），key = keyOf(绝对路径)。
// 排除走 ignore.WalkDir（与引擎 collectFiles 同一实现 + 同一规则来源），
// 目录命中忽略即不下降、文件命中即跳过（gitignore 语义）。
// 文档类（docExts）**独立成组**：仅当 doc.enabled 时收集，单文件上限 = doc.maxBytes
// （与引擎 collectFiles 同口径 → 清单集合与索引集合严格一致）。
func scanFiles(workDir string, exts, rules []string, stackGitignore bool, doc docScanCtx) (map[string]scanEntry, error) {
	extSet := map[string]bool{}
	for _, e := range exts {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extSet[e] = true
	}
	// 文档类不经普通文本通道（即使被写进 vfts.exts），支持只由 doc.enabled 决定
	for _, e := range docExts() {
		delete(extSet, e)
	}
	var docSet map[string]bool
	if doc.enabled {
		docSet = map[string]bool{}
		for _, e := range docExts() {
			docSet[e] = true
		}
	}
	docLimit := doc.maxBytes
	if docLimit <= 0 {
		docLimit = maxFileBytes
	}
	out := map[string]scanEntry{}
	root := filepath.Clean(filepath.FromSlash(workDir))
	opts := ignore.Options{StackGitignore: stackGitignore, UserRules: rules}
	err := ignore.WalkDir(root, opts, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		isDoc := docSet[ext]
		limit := int64(maxFileBytes)
		if isDoc {
			limit = docLimit
		} else if !extSet[ext] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > limit {
			return nil
		}
		abs := filepath.ToSlash(filepath.Clean(p))
		out[keyOf(abs)] = scanEntry{path: abs, size: info.Size(), mtime: info.ModTime()}
		return nil
	})
	return out, err
}

// ─── persist 面（file_list 读写）─────────────────────────

// fileListAll 读该实例的 file_list 全量（按 key 索引）。
func (p *Vfts) fileListAll(inst string) (map[string]fileRec, error) {
	res, err := dataclient.Emit(p.deps.Bus, subjectFileListList, map[string]any{"instance_id": inst})
	if err != nil {
		return nil, err
	}
	raw, _ := res["list"].([]any)
	out := make(map[string]fileRec, len(raw))
	for _, x := range raw {
		b, _ := json.Marshal(x)
		var rec fileRec
		if json.Unmarshal(b, &rec) == nil && rec.Key != "" {
			out[rec.Key] = rec
		}
	}
	return out, nil
}

// fileListPut 单条 upsert（按 key）。
func (p *Vfts) fileListPut(inst string, rec fileRec) error {
	b, _ := json.Marshal(rec)
	data := map[string]any{}
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}
	_, err := dataclient.Emit(p.deps.Bus, subjectFileListPut, map[string]any{"instance_id": inst, "data": data})
	return err
}

// fileListDel 按 key 批量删除。
func (p *Vfts) fileListDel(inst string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	arr := make([]any, 0, len(keys))
	for _, k := range keys {
		arr = append(arr, k)
	}
	_, err := dataclient.Emit(p.deps.Bus, subjectFileListDel, map[string]any{
		"instance_id": inst, "data": map[string]any{"keys": arr},
	})
	return err
}

// fileListPutBatch 批量 upsert（一次总线往返，对齐 fileListDel 的批量形态）；空集合 → 零动作。
func (p *Vfts) fileListPutBatch(inst string, recs []fileRec) error {
	if len(recs) == 0 {
		return nil
	}
	entries := make([]any, 0, len(recs))
	for _, rec := range recs {
		b, _ := json.Marshal(rec)
		var data map[string]any
		if json.Unmarshal(b, &data) != nil {
			continue
		}
		entries = append(entries, data)
	}
	_, err := dataclient.Emit(p.deps.Bus, subjectFileListPut, map[string]any{
		"instance_id": inst, "data": map[string]any{"entries": entries},
	})
	return err
}

// ─── 编排 ────────────────────────────────────────────────

// incrementalSync 增量同步该 workdir：扫描 → 读清单 → diff → 引擎增量 → 回写清单。
// doc = 文档类扫描上下文（开关/上限/解析器版本）；须持 r.cmu。
func (p *Vfts) incrementalSync(r *workRec, ctx context.Context, exts, rules []string, stackGitignore bool, doc docScanCtx) (*syncStats, error) {
	inst := p.instanceForWorkdir(r.workDir)
	if inst == "" {
		return nil, errors.New("无活跃实例，无法读写 file_list")
	}
	scanned, err := scanFiles(r.workDir, exts, rules, stackGitignore, doc)
	if err != nil {
		return nil, err
	}
	prior, err := p.fileListAll(inst)
	if err != nil {
		return nil, err
	}
	diff := diffManifest(scanned, prior, md5File, doc)

	st := &syncStats{Skipped: diff.skipped + len(diff.toTouch), Removed: len(diff.toRemove)}
	for _, t := range diff.toIndex {
		if len(t.old) > 0 {
			st.Updated++
		} else {
			st.Added++
		}
	}

	// 有实际处理才调引擎（全跳过 = 零 IO 引擎调用）
	byKey := map[string]engineIndexedFile{}
	if len(diff.toIndex) > 0 || len(diff.toRemove) > 0 {
		args := map[string]any{"workdir": r.workDir}
		if len(diff.toIndex) > 0 {
			files := make([]any, 0, len(diff.toIndex))
			for _, t := range diff.toIndex {
				item := map[string]any{"path": t.path, "key": t.key}
				if len(t.old) > 0 {
					item["doc_ids"] = stringsToAny(t.old)
				}
				files = append(files, item)
			}
			args["files"] = files
		}
		if len(diff.toRemove) > 0 {
			rem := make([]any, 0, len(diff.toRemove))
			for _, t := range diff.toRemove {
				item := map[string]any{"key": t.Key}
				if len(t.DocIDs) > 0 {
					item["doc_ids"] = stringsToAny(t.DocIDs)
				}
				rem = append(rem, item)
			}
			args["remove"] = rem
		}
		text, err := p.engineCall(ctx, "vfts_index", args)
		if err != nil {
			return nil, err
		}
		var res engineIndexResult
		if err := json.Unmarshal([]byte(text), &res); err != nil {
			return nil, errors.New("vfts_index 应答解析失败: " + err.Error())
		}
		for _, f := range res.Indexed {
			byKey[f.Key] = f
		}
	}

	// 回写清单：索引结果 → put；仅 mtime 变 → put；待删除 → del。批量一次总线往返。
	now := time.Now().UTC().Format(time.RFC3339)
	touched := make([]fileRec, 0, len(diff.toIndex)+len(diff.toTouch))
	for _, t := range diff.toIndex {
		m := t.md5
		if m == "" {
			raw, _ := md5File(t.path)
			if isDocPath(t.path) {
				raw = tagMD5(raw, doc.parserVersion)
			}
			m = raw
		}
		f := byKey[t.key]
		touched = append(touched, fileRec{
			Key: t.key, Path: t.path, Size: t.size, MTime: mtimeStr(t.mtime),
			MD5: m, DocIDs: f.DocIDs, Chunks: f.Chunks, IndexedAt: now,
		})
	}
	touched = append(touched, diff.toTouch...)
	if err := p.fileListPutBatch(inst, touched); err != nil {
		return nil, err
	}
	delKeys := make([]string, 0, len(diff.toRemove))
	for _, rec := range diff.toRemove {
		delKeys = append(delKeys, rec.Key)
	}
	if err := p.fileListDel(inst, delKeys); err != nil {
		return nil, err
	}
	return st, nil
}

// rebuildManifest 全量重建后重建清单：引擎应答的 indexed 已含 doc_ids；
// 本插件补 size/mtime/md5，并清掉本次未覆盖的旧行。须持 r.cmu。
func (p *Vfts) rebuildManifest(r *workRec, res engineIndexResult, exts, rules []string, stackGitignore bool, doc docScanCtx) error {
	inst := p.instanceForWorkdir(r.workDir)
	if inst == "" {
		return errors.New("无活跃实例，无法读写 file_list")
	}
	scanned, err := scanFiles(r.workDir, exts, rules, stackGitignore, doc)
	if err != nil {
		return err
	}
	prior, err := p.fileListAll(inst)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	keep := map[string]bool{}
	recs := make([]fileRec, 0, len(res.Indexed))
	for _, f := range res.Indexed {
		abs := absOf(r.workDir, f.Path)
		if abs == "" {
			continue
		}
		key := keyOf(abs)
		keep[key] = true
		e, ok := scanned[key]
		if !ok { // 引擎扫到但插件扫描过滤掉（极端口径差）：仍按磁盘 stat 记录
			info, serr := os.Stat(filepath.FromSlash(abs))
			if serr != nil {
				continue
			}
			e = scanEntry{path: abs, size: info.Size(), mtime: info.ModTime()}
		}
		m, _ := md5File(e.path)
		if isDocPath(e.path) {
			m = tagMD5(m, doc.parserVersion)
		}
		recs = append(recs, fileRec{
			Key: key, Path: e.path, Size: e.size, MTime: mtimeStr(e.mtime),
			MD5: m, DocIDs: f.DocIDs, Chunks: f.Chunks, IndexedAt: now,
		})
	}
	if err := p.fileListPutBatch(inst, recs); err != nil {
		return err
	}
	var stale []string
	for key := range prior {
		if !keep[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	return p.fileListDel(inst, stale)
}

// mergeSyncStatus 把增量计数并入引擎状态 JSON 并回写 vfts.status（保留 state 等原字段）。
// docSvc 非 nil 时补 docsPort（引擎状态本身已含 docsService/docsSkipped/docsFailed）。
func (p *Vfts) mergeSyncStatus(r *workRec, st *syncStats, docSvc *docService) {
	m := map[string]any{}
	if json.Unmarshal([]byte(r.state), &m) != nil || m == nil {
		m = map[string]any{}
	}
	m["total"] = m["indexedFiles"]
	m["added"] = st.Added
	m["updated"] = st.Updated
	m["removed"] = st.Removed
	m["skipped"] = st.Skipped
	m["chunks"] = m["chunkCount"]
	if docSvc != nil {
		m["docsPort"] = docSvc.Port
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	r.state = string(b)
	p.saveStatusRaw(r, r.state)
}

// effectiveExts 取引擎状态里的生效扩展名（未配置时 = 引擎默认集）；
// 状态不可解析时回落用户配置值。
func effectiveExts(state string, fallback []string) []string {
	var s struct {
		Exts []string `json:"exts"`
	}
	if json.Unmarshal([]byte(state), &s) == nil && len(s.Exts) > 0 {
		return s.Exts
	}
	return fallback
}

// stringsToAny []string → []any（JSON 载荷用）。
func stringsToAny(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

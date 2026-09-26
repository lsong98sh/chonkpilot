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
)

// data 面（文件清单，persist 订阅；域 filelist）。
const (
	subjectFileListList = "data-filelist-list"
	subjectFileListPut  = "data-filelist-put"
	subjectFileListDel  = "data-filelist-del"
)

// maxFileBytes 单文件上限（与引擎 maxFileBytes / codegraph 同口径）。
const maxFileBytes = 8 << 20 // 8MB

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
func diffManifest(scanned map[string]scanEntry, prior map[string]fileRec, hashFn func(string) (string, error)) manifestDiff {
	var d manifestDiff
	for key, e := range scanned {
		old, ok := prior[key]
		if !ok { // ① 新文件
			d.toIndex = append(d.toIndex, indexTask{key: key, path: e.path, size: e.size, mtime: e.mtime})
			continue
		}
		if e.size == old.Size && mtimeStr(e.mtime) == old.MTime { // ② 未变 → 跳过
			d.skipped++
			continue
		}
		h, err := hashFn(e.path) // ③ 变化 → 算 md5
		if err == nil && old.MD5 != "" && h == old.MD5 {
			touch := old
			touch.Size = e.size
			touch.MTime = mtimeStr(e.mtime)
			d.toTouch = append(d.toTouch, touch)
			continue
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

// defaultSkipDirs 与引擎 server.defaultSkipDirs 对齐（插件扫描须与引擎同口径）。
func defaultSkipDirs() []string {
	return []string{".git", ".svn", ".hg", "node_modules", "__pycache__",
		".venv", "venv", ".trae", ".chonkpilot", "dist", "build",
		".next", ".nuxt", "out", "target", "vendor"}
}

// scanFiles 扫描候选文件（按 ext 集与跳过目录），key = keyOf(绝对路径)。
func scanFiles(workDir string, exts, skipDirs []string) (map[string]scanEntry, error) {
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
	skip := map[string]bool{}
	for _, d := range defaultSkipDirs() {
		skip[d] = true
	}
	for _, d := range skipDirs {
		if d != "" {
			skip[d] = true
		}
	}
	out := map[string]scanEntry{}
	root := filepath.Clean(filepath.FromSlash(workDir))
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !extSet[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileBytes {
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
	res, err := dataEmit(p.deps.Bus, subjectFileListList, map[string]any{"instance_id": inst})
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
	_, err := dataEmit(p.deps.Bus, subjectFileListPut, map[string]any{"instance_id": inst, "data": data})
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
	_, err := dataEmit(p.deps.Bus, subjectFileListDel, map[string]any{
		"instance_id": inst, "data": map[string]any{"keys": arr},
	})
	return err
}

// ─── 编排 ────────────────────────────────────────────────

// incrementalSync 增量同步该 workdir：扫描 → 读清单 → diff → 引擎增量 → 回写清单。
// 须持 r.cmu。
func (p *Vfts) incrementalSync(r *workRec, ctx context.Context, exts, skipDirs []string) (*syncStats, error) {
	inst := p.instanceForWorkdir(r.workDir)
	if inst == "" {
		return nil, errors.New("无活跃实例，无法读写 file_list")
	}
	scanned, err := scanFiles(r.workDir, exts, skipDirs)
	if err != nil {
		return nil, err
	}
	prior, err := p.fileListAll(inst)
	if err != nil {
		return nil, err
	}
	diff := diffManifest(scanned, prior, md5File)

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

	// 回写清单：索引结果 → put；仅 mtime 变 → put；待删除 → del
	now := time.Now().UTC().Format(time.RFC3339)
	for _, t := range diff.toIndex {
		m := t.md5
		if m == "" {
			m, _ = md5File(t.path)
		}
		f := byKey[t.key]
		if err := p.fileListPut(inst, fileRec{
			Key: t.key, Path: t.path, Size: t.size, MTime: mtimeStr(t.mtime),
			MD5: m, DocIDs: f.DocIDs, Chunks: f.Chunks, IndexedAt: now,
		}); err != nil {
			return nil, err
		}
	}
	for _, rec := range diff.toTouch {
		if err := p.fileListPut(inst, rec); err != nil {
			return nil, err
		}
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
func (p *Vfts) rebuildManifest(r *workRec, res engineIndexResult, exts, skipDirs []string) error {
	inst := p.instanceForWorkdir(r.workDir)
	if inst == "" {
		return errors.New("无活跃实例，无法读写 file_list")
	}
	scanned, err := scanFiles(r.workDir, exts, skipDirs)
	if err != nil {
		return err
	}
	prior, err := p.fileListAll(inst)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	keep := map[string]bool{}
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
		if err := p.fileListPut(inst, fileRec{
			Key: key, Path: e.path, Size: e.size, MTime: mtimeStr(e.mtime),
			MD5: m, DocIDs: f.DocIDs, Chunks: f.Chunks, IndexedAt: now,
		}); err != nil {
			return err
		}
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
func (p *Vfts) mergeSyncStatus(r *workRec, st *syncStats) {
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

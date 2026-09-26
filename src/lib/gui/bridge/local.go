// IDE 能力本地实现（61-消息一览 §1 gui.*）：布局/最近目录/检索/VCS/初始化数据/打开目录。
// 原 /call 注册（dataCalls）2026-09-04 清零：callX 处理器保留为本文件 guimsg.go 的消息面
// 内部实现（init-data/ui.save/recent.list/dir.*/console.open/pick-executable/vcs.info/search），
// 不再有 RPC 入口。
//
// 数据源（A6 收敛态）：gui.* 状态（layout/window/ui/opened-files/filetree-*）落 prj config 表
// 键值面——读写一律经总线 dataViaPersist 调 persist（data-prj-config-list/load/save），
// 桥不再持 prj/usr 库句柄（原 prjDB/configGet/configSet 直连已删）。记录结构保持
// {"v":"<json>"} 不变。recent_dirs 落 usr config 表**自由键**通道（P2-7/G-05；读写经
// data-user-config-load/save，跨进程/重启持久化，见 recordRecentDir）。
// persist 恒在线（应答毫秒级）；应答异常/超时（dataViaPersist 3s 兜底）时读取按缺省空值、
// 保存丢弃（不阻塞 UI，与历史 _ = err 风格一致）——init-data 永远成功返回本地能力
// （treeData/workDir），文件树等不因落库失败而异常。
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-gui/internal/folder"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// callOpenInConsole 在系统控制台（cmd 新窗口）打开到指定路径所在目录。
func callOpenInConsole(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	path := ""
	if len(params) > 0 {
		_ = json.Unmarshal(params[0], &path)
	}
	dir := path
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		dir = filepath.Dir(path)
	}
	if dir == "" {
		dir = b.workDir
	}
	cmd := exec.Command("cmd", "/k", "start", "cmd", "/k", "cd", "/d", dir)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("open console: %w", err)
	}
	_ = cmd.Process.Release()
	return json.Marshal(map[string]string{"code": "OK", "path": dir})
}

// mapParam 取第 idx 个参数并解码为 map（缺参/坏参 → nil）。
func mapParam(params []json.RawMessage, idx int) map[string]any {
	if len(params) <= idx {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(params[idx], &m); err != nil {
		return nil
	}
	return m
}

// ── LoadInitData ──────────────────────────────────────────────

// callLoadInitData 返回启动初始化数据：
// {treeData, expandedKeys, selectedKey, workDir, filetreeWidth, layout, ui, openedFile, openedFiles}
// 布局/UI/已开文件/展开键均经总线 persist 读取（prjConfigList 一把取全表）；读取失败 →
// 相关字段空值，本地能力（treeData/workDir）不受影响。
func callLoadInitData(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	cfg := b.prjConfigList()

	// 布局：layout.<字段> 细 key（v6）；无细 key 时回落 legacy 整块 "layout"
	layout := prefixedConfig(cfg, "layout.")
	if len(layout) == 0 {
		layout = legacyBlob(cfg, "layout")
	}
	// UI 偏好（theme/locale）= 用户级，取 data-user-config-load
	ui := map[string]any{}
	if uc, err := readUserConfig(b); err == nil {
		for _, k := range []string{"theme", "locale"} {
			if v, ok := uc[k]; ok {
				ui[k] = v
			}
		}
	}

	// 打开文件恢复（文件已删除不恢复）
	openedFiles := []string{}
	if s := sval(cfg["opened-files"]); s != "" {
		_ = json.Unmarshal([]byte(s), &openedFiles)
	}
	kept := openedFiles[:0]
	for _, p := range openedFiles {
		if strings.HasPrefix(p, "db://") {
			kept = append(kept, p)
			continue
		}
		if _, err := os.Stat(p); err == nil {
			kept = append(kept, p)
		}
	}
	openedFiles = kept
	openedFile := ""
	if len(openedFiles) > 0 {
		openedFile = openedFiles[0]
	}

	// 文件树：根级直接读磁盘，展开键命中的目录预载 children（前端恢复时只置 expanded 标志、
	// 不发 filesys.list，故必须后端预载，否则展开目录显示为空——I-52）；展开键/选中从 DB 恢复。
	expanded := []string{}
	if s := sval(cfg["filetree-expanded-key"]); s != "" {
		_ = json.Unmarshal([]byte(s), &expanded)
	}
	selected := ""
	if s := sval(cfg["filetree-selected-path"]); s != "" {
		selected = filepath.Join(b.workDir, s)
	}
	// 展开键存相对路径 → 转绝对（正斜杠归一，与 readDirNodes 产出的 path 一致）
	absExpanded := make([]string, 0, len(expanded))
	expandedSet := make(map[string]bool, len(expanded))
	for _, rel := range expanded {
		if rel == "" {
			continue
		}
		abs := filepath.ToSlash(filepath.Join(b.workDir, rel))
		absExpanded = append(absExpanded, abs)
		expandedSet[abs] = true
	}

	out := map[string]any{
		"treeData":      readDirNodesExpanded(b.workDir, expandedSet),
		"expandedKeys":  absExpanded,
		"selectedKey":   selected,
		"workDir":       b.workDir,
		"filetreeWidth": 260,
		"layout":        layout,
		"ui":            ui,
		"openedFile":    openedFile,
		"openedFiles":   openedFiles,
	}
	// 可选增补（只增不改，2026-09-19）：GUI 文件日志目录（供前端/用户定位排查日志）。
	// 未挂文件 sink（logDir 空）→ 不带该字段，等价旧行为。
	if b.logDir != "" {
		out["logDir"] = b.logDir
	}
	return json.Marshal(out)
}

// readDirNodes 读目录一层节点（隐藏条目跳过；os.ReadDir 已按名称字母序返回）。
// label 供树直接渲染；name 供 loadDirChildren 的 treeNode 转换（前端两种消费方式并存）。
// path 统一正斜杠（ToSlash），与前端 normalizeTreeDataPaths 一致，避免 findNode/展开检查分隔符不匹配。
func readDirNodes(dir string) []map[string]any {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, map[string]any{
			"name":   e.Name(),
			"label":  e.Name(),
			"path":   filepath.ToSlash(filepath.Join(dir, e.Name())),
			"is_dir": e.IsDir(),
		})
	}
	return out
}

// readDirNodesExpanded 在 readDirNodes 基础上，为命中 expandedSet（绝对路径，正斜杠归一）
// 的目录递归预载 children——前端重启恢复只按 expandedKeys 置 expanded=true、不再发
// filesys.list，若不预载则展开目录显示为空（I-52）。未展开目录不带 children（保持懒加载）。
func readDirNodesExpanded(dir string, expandedSet map[string]bool) []map[string]any {
	nodes := readDirNodes(dir)
	for _, n := range nodes {
		isDir, _ := n["is_dir"].(bool)
		if !isDir {
			continue
		}
		p, _ := n["path"].(string)
		if p != "" && expandedSet[p] {
			n["children"] = readDirNodesExpanded(filepath.FromSlash(p), expandedSet)
		}
	}
	return nodes
}

// ── 保存类 ────────────────────────────────────────────────────
// 各 save 落 prj config 表经总线 persist（data-prj-config-save）；persist 侧按 key 路由
// 分层：个人运行态（layout.*/window.*/filetree-*/opened-*）落 prjusr（12-数据层）。
// 细 key 形态（layout.<字段> / window.<字段>）使 prj 可只预设部分项、其余继承个人值。
// 保存失败静默丢弃（不阻塞 UI；persist 恒在线，正常不触发）。

func callSaveLayoutState(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	for k, v := range mapParam(params, 0) {
		_ = b.prjConfigSave("layout."+k, cfgScalar(v))
	}
	return nil, nil
}

func callSaveWindowState(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	for k, v := range mapParam(params, 0) {
		_ = b.prjConfigSave("window."+k, cfgScalar(v))
	}
	return nil, nil
}

// callSaveUIState 保存 UI 偏好：theme/locale 属**用户级**（12-数据层，跨项目个人偏好），
// 走 data-user-config-save（persist 逐 key 落 usr 库）；v6 起不再使用整块 "ui" 键。
func callSaveUIState(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	m := mapParam(params, 0)
	data := map[string]any{}
	for _, k := range []string{"theme", "locale"} {
		if v, ok := m[k]; ok {
			data[k] = v
		}
	}
	if len(data) == 0 {
		return nil, nil
	}
	raw, _ := json.Marshal(map[string]any{"data": data})
	_, _ = b.dataCall("data-user-config-save", raw)
	return nil, nil
}

func callSaveFileTreeState(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	m := mapParam(params, 0)
	if dirs, ok := m["expanded_dirs"].([]any); ok {
		rel := make([]string, 0, len(dirs))
		for _, d := range dirs {
			if ds, ok := d.(string); ok && ds != "" {
				if r, err := filepath.Rel(b.workDir, ds); err == nil {
					rel = append(rel, r)
				}
			}
		}
		raw, _ := json.Marshal(rel)
		_ = b.prjConfigSave("filetree-expanded-key", string(raw))
	}
	if sel, ok := m["selected_path"].(string); ok && sel != "" {
		if r, err := filepath.Rel(b.workDir, sel); err == nil {
			_ = b.prjConfigSave("filetree-selected-path", r)
		}
	}
	return nil, nil
}

func callSaveOpenedFiles(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	var paths []string
	if len(params) > 0 {
		_ = json.Unmarshal(params[0], &paths)
	}
	if paths == nil {
		paths = []string{}
	}
	raw, _ := json.Marshal(paths)
	_ = b.prjConfigSave("opened-files", string(raw))
	return nil, nil
}

// ── 最近目录 / 打开目录 ─────────────────────────────────────

// recentDirsCap 最近目录上限（对齐原 usr config recent_dirs 的 10 条截断）。
const recentDirsCap = 10

// recentDirsKey 是 usr config 表自由键通道中承载最近目录的键（P2-7/G-05）。
const recentDirsKey = "recent_dirs"

// recordRecentDir 记录最近打开的目录：读-改-写 usr config 自由键 recent_dirs
// （data-user-config-{load,save}；跨进程/重启持久化）。前插 + 去重 + 10 条截断，
// 值 = JSON 数组字符串；读取失败按空表、保存失败静默丢弃（不阻塞 UI）。
func (b *Bridge) recordRecentDir(dir string) {
	if dir == "" {
		return
	}
	dirs := append([]string{dir}, b.recentDirsList()...)
	seen := map[string]bool{}
	kept := dirs[:0]
	for _, d := range dirs {
		if seen[d] {
			continue
		}
		seen[d] = true
		kept = append(kept, d)
	}
	if len(kept) > recentDirsCap {
		kept = kept[:recentDirsCap]
	}
	raw, _ := json.Marshal(kept)
	body, _ := json.Marshal(map[string]any{"data": map[string]any{recentDirsKey: string(raw)}})
	_, _ = b.dataCall("data-user-config-save", body)
}

// recentDirsList 读 usr config 自由键 recent_dirs（JSON 数组字符串）→ 最近目录快照；
// 无记录/读取失败/坏值 → 空表。
func (b *Bridge) recentDirsList() []string {
	uc, err := readUserConfig(b)
	if err != nil {
		return []string{}
	}
	s, _ := uc[recentDirsKey].(string)
	if s == "" {
		return []string{}
	}
	var dirs []string
	if json.Unmarshal([]byte(s), &dirs) != nil {
		return []string{}
	}
	return dirs
}

func callGetRecentDirs(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	return json.Marshal(map[string]any{"dirs": b.recentDirsList()})
}

// openNewWindow 以新进程打开目录（fire-and-forget）。
func openNewWindow(dir string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	cmd := exec.Command(exePath, "--work-dir="+dir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open new window: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}

func callOpenDir(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	dir := ""
	if len(params) > 0 {
		_ = json.Unmarshal(params[0], &dir)
	}
	if dir == "" {
		return nil, fmt.Errorf("path required")
	}
	if err := openNewWindow(dir); err != nil {
		return nil, err
	}
	b.recordRecentDir(dir)
	return json.Marshal(map[string]string{"code": "OK", "message": "New window opened", "path": dir})
}

func callOpenDirDialog(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	selected, err := folder.PickFolder("Select Project Directory")
	if err != nil {
		return nil, fmt.Errorf("pick folder: %w", err)
	}
	if selected == "" {
		return json.Marshal(map[string]string{"code": "CANCELLED", "message": "User cancelled"})
	}
	if err := openNewWindow(selected); err != nil {
		return nil, err
	}
	b.recordRecentDir(selected)
	return json.Marshal(map[string]string{"code": "OK", "message": "New window opened", "path": selected})
}

// callPickExecutable 打开系统文件选择框挑选可执行文件（纯 picker，无 openNewWindow/记目录等副作用）。
// 成功 → {path}；用户取消 → {path:""}（对齐 61-消息一览 §1 dir.open-dialog 的 {path?} 形态，
// 取消=无 path；因无副作用不需 CANCELLED code 区分，调用方以 path 空串判定取消即可）。
func callPickExecutable(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	selected, err := folder.PickFile("Select Executable", "Executable files\x00*.exe\x00All files\x00*.*\x00\x00")
	if err != nil {
		return nil, fmt.Errorf("pick file: %w", err)
	}
	return json.Marshal(map[string]string{"path": selected})
}

// ── VCS / 检索 ──────────────────────────────────────────────

// callGetVCSInfo 探测工作目录 VCS 类型 + git 可执行性 → {git, svn, gitInstalled}。
// git = work-dir 为 git 仓库（.git 存在）；gitInstalled = 系统可执行 git（与 history 插件
// 的 exec.LookPath("git") 门槛一致，二者缺一即文件历史不可用）。
func callGetVCSInfo(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	git := false
	if _, err := os.Stat(filepath.Join(b.workDir, ".git")); err == nil {
		git = true
	}
	svn := false
	if _, err := os.Stat(filepath.Join(b.workDir, ".svn")); err == nil {
		svn = true
	}
	gitInstalled := false
	if _, err := exec.LookPath("git"); err == nil {
		gitInstalled = true
	}
	return json.Marshal(map[string]any{"git": git, "svn": svn, "gitInstalled": gitInstalled})
}

// skipDirs 检索跳过的重型目录（同 codeindex 默认集）。
var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".svn": true, "dist": true,
	"build": true, "vendor": true, ".chonkpilot": true, "out": true,
	"target": true, ".venv": true, "__pycache__": true,
}

// 检索源与约束（gui.search 聚合三源，对齐 42 §2 (27)⑥ / 41 D-12）：
//   - file     文件名/路径匹配（始终可用，作为兜底）
//   - vfts     全文内容命中（启用且就绪时，带片段/行号）
//   - codegraph 符号命中（启用且就绪时）
//
// 排序：文件名精确命中 > vfts 内容 > codegraph 符号 > 文件路径子串；总量 ≤ searchMaxResults。
// 任一引擎未启用/未就绪/超时/失败 → 静默跳过该源，绝不使 gui.search 失败。
const (
	searchSourceFile      = "file"
	searchSourceVfts      = "vfts"
	searchSourceCodegraph = "codegraph"
	searchMaxResults      = 20
	searchToolTimeout     = 3 * time.Second // 单个引擎源调用超时（超时即跳过）
)

func callSearchProjectFiles(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	query := ""
	if len(params) > 0 {
		_ = json.Unmarshal(params[0], &query)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return json.Marshal([]any{})
	}
	lower := strings.ToLower(query)

	// ① 文件名/路径匹配（source=file）：始终可用
	hits := searchFileSource(b.workDir, lower, searchMaxResults)
	// ② vfts 全文（source=vfts）③ codegraph 符号（source=codegraph）：未启用/未就绪静默跳过
	hits = append(hits, searchVftsSource(b, query, searchMaxResults)...)
	hits = append(hits, searchCodegraphSource(b, query, searchMaxResults)...)

	// 排序：文件名精确命中 > vfts 内容 > codegraph 符号 > 文件路径子串；再按 path 去重 + 截断
	sort.SliceStable(hits, func(i, j int) bool { return searchRank(hits[i]) < searchRank(hits[j]) })
	results := dedupeSearchResults(hits)
	if len(results) > searchMaxResults {
		results = results[:searchMaxResults]
	}
	if results == nil {
		results = []map[string]any{}
	}
	return json.Marshal(results)
}

// searchRank 结果优先级（越小越靠前）：文件名命中 0 > vfts 1 > codegraph 2 > 文件路径子串 3。
func searchRank(it map[string]any) int {
	if mt, _ := it["matchType"].(string); mt == "filename" {
		return 0
	}
	switch it["source"] {
	case searchSourceVfts:
		return 1
	case searchSourceCodegraph:
		return 2
	}
	return 3
}

// searchFileSource 文件名/路径匹配（source=file，matchType=filename/path）。
func searchFileSource(workDir, lower string, limit int) []map[string]any {
	var results []map[string]any
	_ = filepath.Walk(workDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			if skipDirs[fi.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(workDir, path)
		if !strings.Contains(strings.ToLower(filepath.ToSlash(rel)), lower) {
			return nil
		}
		mt := "path"
		if strings.Contains(strings.ToLower(fi.Name()), lower) {
			mt = "filename"
		}
		results = append(results, map[string]any{
			"path":      path,
			"name":      fi.Name(),
			"matchType": mt,
			"source":    searchSourceFile,
			"snippet":   "",
		})
		if len(results) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return results
}

// searchVftsSource 调 vfts_query（经 gateway mcp-tools-call）取全文命中（source=vfts）。
// vfts_query 返回 {workdir,total,hits:[{path,line,snippet,score}]}。
func searchVftsSource(b *Bridge, query string, limit int) []map[string]any {
	text, ok := searchToolCall(b, "vfts_query", map[string]any{"match": query, "topK": limit}, searchToolTimeout)
	if !ok {
		return nil
	}
	var resp struct {
		Hits []struct {
			Path    string  `json:"path"`
			Line    int     `json:"line"`
			Snippet string  `json:"snippet"`
			Score   float32 `json:"score"`
		} `json:"hits"`
	}
	if json.Unmarshal([]byte(text), &resp) != nil {
		return nil // 未启用/未就绪等非命中应答 → 跳过
	}
	out := make([]map[string]any, 0, len(resp.Hits))
	for _, h := range resp.Hits {
		if h.Path == "" {
			continue
		}
		out = append(out, map[string]any{
			"path":      h.Path,
			"name":      filepath.Base(filepath.FromSlash(h.Path)),
			"matchType": "content",
			"source":    searchSourceVfts,
			"line":      h.Line,
			"snippet":   h.Snippet,
		})
	}
	return out
}

// searchCodegraphSource 调 codegraph_symbol_search 取符号命中（source=codegraph）。
// codegraph_symbol_search 返回 {total,symbols:[{file,name,line,kind,signature,...}]}。
func searchCodegraphSource(b *Bridge, query string, limit int) []map[string]any {
	text, ok := searchToolCall(b, "codegraph_symbol_search", map[string]any{"query": query, "limit": limit}, searchToolTimeout)
	if !ok {
		return nil
	}
	var resp struct {
		Symbols []struct {
			File      string `json:"file"`
			Name      string `json:"name"`
			Line      int    `json:"line"`
			Kind      string `json:"kind"`
			Signature string `json:"signature"`
		} `json:"symbols"`
	}
	if json.Unmarshal([]byte(text), &resp) != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(resp.Symbols))
	for _, s := range resp.Symbols {
		if s.File == "" {
			continue
		}
		out = append(out, map[string]any{
			"path":      s.File,
			"name":      s.Name,
			"matchType": "symbol",
			"source":    searchSourceCodegraph,
			"line":      s.Line,
			"snippet":   s.Signature,
		})
	}
	return out
}

// searchToolCall 经 gateway（mcp-tools-call）调用已注册查询工具，带超时。
// 引擎未启用/未就绪/超时/调用失败一律返回 ok=false（调用方静默跳过该源）。
func searchToolCall(b *Bridge, tool string, args map[string]any, timeout time.Duration) (string, bool) {
	if b.bus == nil {
		return "", false
	}
	// 防环：本实例发出的 mcp-tools-call 不回投前端（与 publishV 同语义）。
	b.markPublished("mcp-tools-call")
	payload := map[string]any{
		"instance_id": b.instanceID,
		"work_dir":    b.workDir,
		"name":        tool,
		"arguments":   args,
	}
	done := make(chan *mq.Value, 1)
	go func() {
		done <- b.bus.Emit(context.Background(), "mcp-tools-call", payload).Wait()
	}()
	select {
	case v := <-done:
		if v == nil || v.Err() != nil {
			return "", false
		}
		return toolResultText(v.Result), true
	case <-time.After(timeout):
		return "", false // 超时：放弃该源（后台 goroutine 自然结束）
	}
}

// toolResultText 从 gateway 写回的 v.Result（{content:[{type:text,text}]} 形态）提取文本。
func toolResultText(res any) string {
	if res == nil {
		return ""
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	if s, ok := m["content"].(string); ok {
		return s
	}
	if arr, ok := m["content"].([]any); ok {
		for _, c := range arr {
			if cm, ok := c.(map[string]any); ok {
				if t, ok := cm["text"].(string); ok {
					return t
				}
			}
		}
	}
	if s, ok := m["text"].(string); ok {
		return s
	}
	return ""
}

// dedupeSearchResults 按 path 去重（保留先出现者，来源优先级 file > vfts > codegraph）。
func dedupeSearchResults(items []map[string]any) []map[string]any {
	seen := map[string]bool{}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		p, _ := it["path"].(string)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, it)
	}
	return out
}

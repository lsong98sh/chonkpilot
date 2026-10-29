// Package filesys 是文件服务（61-消息一览 §2）：
// 文件查询/写入/变更跟踪，基于 mq.Bus 门面，纯发布/订阅（promise 语义）。
//
//	请求方发 filesys.*（经桥 publishV）→ 服务方处理并写回 Value.Result / Value.Errors
//	变更事件经 filesys.changed / filesys.watch-error 广播
//
// 信任边界（G-21）：操作的**基目录（work_dir）只按 `instance-register` 登记的
// instance → work_dir 绑定解析**，一律不采信请求载荷里的 work_dir（载荷字段保留
// 仅为契约兼容）—— 调用方无法借 payload 把读写指到绑定之外的目录。
package filesys

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/chonkpilot/chonkpilot-workspace"
	"github.com/lsong98sh/chonkpilot-util/pathx"
)

const maxTextBytes = 512 * 1024

// Filesys 是文件服务实例。
type Filesys struct {
	bus  mq.Bus
	subs []mq.Sub
	wm   *watchManager

	// pm 是工作区管理（唯一权威）：实例 → work_dir 的解析、工作区开闭事件、
	// 路径边界校验一律走它。本包**不再自持 instance → work_dir 绑定**（G-21 不变：
	// 请求载荷里的 work_dir 只作契约字段保留，绝不采信）。
	pm *workspace.Manager
}

// New 构建文件服务（未启动，调用 Start 后开始订阅）。pm = 工作区管理（必填）。
func New(bus mq.Bus, pm *workspace.Manager) *Filesys {
	return &Filesys{bus: bus, wm: newWatchManager(bus, pm), pm: pm}
}

// Start 订阅全部 filesys.* 主题并开始处理请求。
func (f *Filesys) Start() error {
	for _, s := range []struct {
		subject string
		h       func(context.Context, string, *mq.Value) error
	}{
		{msgkeys.TopicFilesysList, f.onList},
		{msgkeys.TopicFilesysContent, f.onContent},
		{msgkeys.TopicFilesysCreate, f.onCreate},
		{msgkeys.TopicFilesysMkdir, f.onMkdir},
		{msgkeys.TopicFilesysRemove, f.onRemove},
		{msgkeys.TopicFilesysRename, f.onRename},
		{msgkeys.TopicFilesysCopy, f.onCopy},
		{msgkeys.TopicFilesysWatch, f.onWatch},
		{msgkeys.TopicFilesysUnwatch, f.onUnwatch},
		// 工作区生命周期（PM，61 §2.5）：open → 对工作区根建立**一次**监听（子树由
		// fswatcher 递归接管）；close → 撤销。监听范围不再由前端的展开/折叠驱动。
		{msgkeys.TopicWorkdirOpened, f.onWorkdirOpened},
		{msgkeys.TopicWorkdirClosed, f.onWorkdirClosed},
		// 「不显示的目录」（filetree.hide-dirs）变更广播（D-39 层2）：复用既有 data-prj-config-refresh，
		// 零新增消息 → 本实例 work_dir 的 watcher 即时重新过滤（见 onPrjConfigRefresh）。
		{msgkeys.TopicDataPrjConfigRefresh, f.onPrjConfigRefresh},
	} {
		sub, err := f.bus.On(s.subject, 0, s.h)
		if err != nil {
			f.Stop()
			return err
		}
		f.subs = append(f.subs, sub)
	}
	return nil
}

// Stop 退订全部消息并关闭 watcher。
func (f *Filesys) Stop() {
	for _, s := range f.subs {
		_ = s.Unsubscribe()
	}
	f.subs = nil
	if f.wm != nil {
		f.wm.Close()
	}
}

// ─── 工作区生命周期（PM 驱动；G-21 信任边界不变）─────────

// onWorkdirOpened 工作区可用：对工作区根建立**一次**监听（子树由 fswatcher 递归接管）。
// 监听范围 = 整个工作区，不再依赖前端的展开/折叠（索引/历史同样消费 filesys.changed）。
func (f *Filesys) onWorkdirOpened(_ context.Context, _ string, v *mq.Value) error {
	var msg struct {
		WorkDir    string `json:"work_dir"`
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(v.Payload, &msg)
	if msg.WorkDir == "" {
		return nil
	}
	if err := f.wm.open(msg.WorkDir, msg.InstanceID); err != nil {
		slog.Warn("filesys workdir watch failed", "work_dir", msg.WorkDir, "err", err)
	}
	return nil
}

// onWorkdirClosed 工作区关闭：撤销监听并释放 watcher（**不删盘、不清索引**）。
func (f *Filesys) onWorkdirClosed(_ context.Context, _ string, v *mq.Value) error {
	var msg struct {
		WorkDir string `json:"work_dir"`
	}
	_ = json.Unmarshal(v.Payload, &msg)
	if msg.WorkDir == "" {
		return nil
	}
	f.wm.close(msg.WorkDir)
	return nil
}

// onPrjConfigRefresh 处理 data-prj-config-refresh：本实例 work_dir 的「不显示的目录」
// （filetree.hide-dirs）变更即时生效 —— 更新缓存并重新过滤该 work_dir 的 watch 集合。
// 复用既有广播（persist 对 prj-config 域按同 work_dir 全部实例逐个下发），**零新增消息**。
// 载荷无 `list`（无法取到值）时忽略，保持现值。
func (f *Filesys) onPrjConfigRefresh(_ context.Context, _ string, v *mq.Value) error {
	var msg struct {
		InstanceID string            `json:"instance_id"`
		List       map[string]string `json:"list"`
	}
	if json.Unmarshal(v.Payload, &msg) != nil || msg.List == nil {
		return nil
	}
	wd, ok := f.workDir(msg.InstanceID)
	if !ok {
		return nil
	}
	f.wm.applyHidden(wd, parseHiddenDirs(msg.List[hiddenDirsKey]))
	return nil
}

// workDir 解析请求所属实例绑定的工作目录（唯一来源 = PM）：未登记 / 未绑定 → ok=false。
// **不回落请求载荷的 work_dir** —— 回落会把刚关上的越权口子重新打开。
func (f *Filesys) workDir(instanceID string) (string, bool) {
	sp, ok := f.pm.Resolve(instanceID)
	if !ok || sp.WorkDir == "" {
		return "", false
	}
	return sp.WorkDir, true
}


// ─── 请求处理 ────────────────────────────────────────────

// reqBase 是 filesys.* 请求的公共字段。
type reqBase struct {
	WorkDir string `json:"work_dir"`
	Path    string `json:"path"`
	Dir     string `json:"dir"`
	Name    string `json:"name"`
	Content string `json:"content"`
	NewName string `json:"new_name"`
	DestDir string `json:"dest_dir"`
}

// onList 处理 filesys.list：目录子项快照。
func (f *Filesys) onList(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		// 契约字段 work_dir 保留但**不采信**：基目录一律取实例绑定（G-21）。
		WorkDir    string `json:"work_dir"`
		InstanceID string `json:"instance_id"`
		Path       string `json:"path"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Errors = append(v.Errors, errBadRequest(err.Error()))
		return nil
	}
	wd, ok := f.workDir(req.InstanceID)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("work dir not bound for instance"))
		return nil
	}
	p, ok := absPath(wd, req.Path)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("path outside work dir"))
		return nil
	}
	// 首次访问为该 work_dir 发起一次异步加载「不显示的目录」清单（非阻塞；后续 list 即用新清单）。
	f.wm.ensureLoaded(wd, req.InstanceID)
	v.Result = map[string]any{"path": p, "is_dir": true, "children": listDirNodes(p, f.wm.hiddenDirsOf(wd))}
	return nil
}

// onContent 处理 filesys.content：读文本文件内容。
func (f *Filesys) onContent(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		WorkDir    string `json:"work_dir"`
		InstanceID string `json:"instance_id"`
		Path       string `json:"path"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Errors = append(v.Errors, errBadRequest(err.Error()))
		return nil
	}
	wd, ok := f.workDir(req.InstanceID)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("work dir not bound for instance"))
		return nil
	}
	p, ok := absPath(wd, req.Path)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("path outside work dir"))
		return nil
	}
	content, truncated, err := readTextFile(p)
	if err != nil {
		v.Errors = append(v.Errors, errRead(err.Error()))
		return nil
	}
	v.Result = map[string]any{"path": p, "kind": "file", "content": content, "truncated": truncated}
	return nil
}

// onCreate 处理 filesys.create：新建文件。
func (f *Filesys) onCreate(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		WorkDir    string `json:"work_dir"`
		InstanceID string `json:"instance_id"`
		Dir        string `json:"dir"`
		Name       string `json:"name"`
		Content    string `json:"content"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Errors = append(v.Errors, errBadRequest(err.Error()))
		return nil
	}
	wd, ok := f.workDir(req.InstanceID)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("work dir not bound for instance"))
		return nil
	}
	dir, ok := absPath(wd, req.Dir)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("path outside work dir"))
		return nil
	}
	if req.Name == "" {
		v.Errors = append(v.Errors, errCreate("name required"))
		return nil
	}
	np := filepath.Join(dir, req.Name)
	if !withinWorkDirReal(wd, np) {
		v.Errors = append(v.Errors, errForbidden("target outside work dir"))
		return nil
	}
	if _, err := os.Stat(np); err == nil {
		v.Errors = append(v.Errors, errExists("file already exists"))
		return nil
	}
	if err := os.WriteFile(np, []byte(req.Content), 0644); err != nil {
		v.Errors = append(v.Errors, errCreate(err.Error()))
		return nil
	}
	v.Result = map[string]any{"ok": true, "path": np}
	return nil
}

// onMkdir 处理 filesys.mkdir：新建目录。
func (f *Filesys) onMkdir(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		WorkDir    string `json:"work_dir"`
		InstanceID string `json:"instance_id"`
		Dir        string `json:"dir"`
		Name       string `json:"name"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Errors = append(v.Errors, errBadRequest(err.Error()))
		return nil
	}
	wd, ok := f.workDir(req.InstanceID)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("work dir not bound for instance"))
		return nil
	}
	dir, ok := absPath(wd, req.Dir)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("path outside work dir"))
		return nil
	}
	if req.Name == "" {
		v.Errors = append(v.Errors, errMkdir("name required"))
		return nil
	}
	np := filepath.Join(dir, req.Name)
	if !withinWorkDirReal(wd, np) {
		v.Errors = append(v.Errors, errForbidden("target outside work dir"))
		return nil
	}
	if _, err := os.Stat(np); err == nil {
		v.Errors = append(v.Errors, errExists("dir already exists"))
		return nil
	}
	if err := os.Mkdir(np, 0755); err != nil {
		v.Errors = append(v.Errors, errMkdir(err.Error()))
		return nil
	}
	v.Result = map[string]any{"ok": true, "path": np}
	return nil
}

// onRemove 处理 filesys.remove：删除文件/目录树。
func (f *Filesys) onRemove(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		WorkDir    string `json:"work_dir"`
		InstanceID string `json:"instance_id"`
		Path       string `json:"path"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Errors = append(v.Errors, errBadRequest(err.Error()))
		return nil
	}
	wd, ok := f.workDir(req.InstanceID)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("work dir not bound for instance"))
		return nil
	}
	p, ok := absPath(wd, req.Path)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("path outside work dir"))
		return nil
	}
	// 空 path（absPath 回落为 work_dir）或等价于 work_dir 的 path 一律拒绝：绝不允许删整个工作目录。
	if req.Path == "" || samePath(p, wd) {
		v.Errors = append(v.Errors, errForbidden("refuse to remove work dir"))
		return nil
	}
	if err := os.RemoveAll(p); err != nil {
		v.Errors = append(v.Errors, errRemove(err.Error()))
		return nil
	}
	v.Result = map[string]any{"ok": true, "path": p}
	return nil
}

// onRename 处理 filesys.rename：重命名/移动。
func (f *Filesys) onRename(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		WorkDir    string `json:"work_dir"`
		InstanceID string `json:"instance_id"`
		Path       string `json:"path"`
		NewName    string `json:"new_name"`
		Overwrite  int    `json:"overwrite"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Errors = append(v.Errors, errBadRequest(err.Error()))
		return nil
	}
	wd, ok := f.workDir(req.InstanceID)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("work dir not bound for instance"))
		return nil
	}
	p, ok := absPath(wd, req.Path)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("path outside work dir"))
		return nil
	}
	if req.NewName == "" {
		v.Errors = append(v.Errors, errRename("new_name required"))
		return nil
	}
	var np string
	switch {
	case filepath.IsAbs(req.NewName):
		np = filepath.Clean(req.NewName)
	case strings.ContainsAny(req.NewName, `/\`):
		// 相对**多段** new_name：以 work_dir 为基准解析。旧实现对含分隔符者只做 Clean，
		// 结果仍是相对路径 → withinWorkDir(wd, np) 恒 false，导致合法相对多段名被一律拒绝。
		np = filepath.Join(wd, req.NewName)
	default:
		np = filepath.Join(filepath.Dir(p), req.NewName)
	}
	if !withinWorkDirReal(wd, np) {
		v.Errors = append(v.Errors, errForbidden("target outside work dir"))
		return nil
	}
	if np == p {
		v.Result = map[string]any{"ok": true, "path": np}
		return nil
	}
	if _, err := os.Stat(np); err == nil && req.Overwrite == 0 {
		v.Errors = append(v.Errors, errExists("target already exists"))
		return nil
	}
	if err := os.Rename(p, np); err != nil {
		v.Errors = append(v.Errors, errRename(err.Error()))
		return nil
	}
	v.Result = map[string]any{"ok": true, "path": np}
	return nil
}

// onCopy 处理 filesys.copy：复制到目标。
// dest_dir 缺省 = 源文件所在目录；new_name 提供 = 改名复制（同目录"副本"由前端算好
// 文件名传入）；两者都缺省 = 复制到源目录同名（无意义，按 np==p 幂等返回 ok）。
func (f *Filesys) onCopy(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		WorkDir    string `json:"work_dir"`
		InstanceID string `json:"instance_id"`
		Path       string `json:"path"`
		DestDir    string `json:"dest_dir"`
		NewName    string `json:"new_name"`
		Overwrite  int    `json:"overwrite"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Errors = append(v.Errors, errBadRequest(err.Error()))
		return nil
	}
	wd, ok := f.workDir(req.InstanceID)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("work dir not bound for instance"))
		return nil
	}
	p, ok := absPath(wd, req.Path)
	if !ok {
		v.Errors = append(v.Errors, errForbidden("path outside work dir"))
		return nil
	}
	dd := req.DestDir
	if dd == "" {
		dd = filepath.Dir(p)
	} else if !filepath.IsAbs(dd) {
		dd = filepath.Join(wd, dd)
	}
	if !withinWorkDirReal(wd, dd) {
		v.Errors = append(v.Errors, errForbidden("dest outside work dir"))
		return nil
	}
	np := filepath.Join(dd, filepath.Base(p))
	if req.NewName != "" {
		// new_name 必须是**裸文件名**：拒绝 "."/".." 与含分隔符者（防 `Join(dd,"..")` 越权写出）。
		if req.NewName == "." || req.NewName == ".." || strings.ContainsAny(req.NewName, `/\`) {
			v.Errors = append(v.Errors, errCopy("new_name must be a bare file name"))
			return nil
		}
		np = filepath.Join(dd, req.NewName)
	}
	if !withinWorkDirReal(wd, np) {
		v.Errors = append(v.Errors, errForbidden("target outside work dir"))
		return nil
	}
	if np == p {
		v.Result = map[string]any{"ok": true, "path": np}
		return nil
	}
	// 嵌套复制防护（R7-C-5）：目标在源目录内部时，copyTree 先 MkdirAll(dst) 再 ReadDir(src)，
	// 新建的目标目录会被递归吸入 → 无限嵌套复制。与 fileops 的 CPY/MOV 同款判定拒绝。
	if fi, serr := os.Stat(p); serr == nil && fi.IsDir() && isWithinDir(np, p) {
		v.Errors = append(v.Errors, errCopy("target inside source dir"))
		return nil
	}
	if _, err := os.Stat(np); err == nil && req.Overwrite == 0 {
		v.Errors = append(v.Errors, errExists("target already exists"))
		return nil
	}
	if err := copyTree(p, np); err != nil {
		v.Errors = append(v.Errors, errCopy(err.Error()))
		return nil
	}
	v.Result = map[string]any{"ok": true, "path": np}
	return nil
}

// onWatch 处理 filesys.watch：**兼容保留，无副作用**。
//
// 监听范围已由工作区生命周期驱动（workdir-opened → 整个工作区一次监听），前端展开/折叠
// 不再影响监听。契约字段（含 recursive）保留，见 61 §2.3。
func (f *Filesys) onWatch(_ context.Context, _ string, _ *mq.Value) error { return nil }

// onUnwatch 处理 filesys.unwatch：**兼容保留，无副作用**（折叠目录不得停止监听）。
func (f *Filesys) onUnwatch(_ context.Context, _ string, _ *mq.Value) error { return nil }


// ─── 工具函数 ────────────────────────────────────────────

// absPath 把 path 转为绝对路径（相对 workDir 则 join），并校验在 workDir 内。
// 实现唯一来源 = workspace（词法 + 符号链接/目录联接后的真实路径双重复检）。
// 返回的仍是词法绝对路径（不替换为真实路径，保持对外 path 形状不变）。
func absPath(wd, p string) (string, bool) {
	return workspace.AbsWithin(wd, p)
}

// withinWorkDirReal / resolveRealPath 是既有调用点的转发（唯一实现 = workspace）。
func withinWorkDirReal(base, path string) bool { return workspace.Within(base, path) }

func resolveRealPath(p string) string { return workspace.Real(p) }

// samePath 判定两个路径是否指向同一目录 / 文件：词法 Clean 后（大小写不敏感，兼容 Windows）相同，
// 或解析符号链接 / 目录联接后的真实路径相同。
// utilguard:allow: 已用 pathx.Same 做词法比较，第二条 realpath 比较无法由纯函数替代。
func samePath(a, b string) bool {
	if pathx.Same(a, b) {
		return true
	}
	return strings.EqualFold(resolveRealPath(a), resolveRealPath(b))
}

// isWithinDir 报告 child 是否严格位于 parent 目录内部（Clean 后按分隔符前缀判定，
// 大小写不敏感以兼容 Windows；R7-C-5 复制嵌套防护用）。
// utilguard:allow: 这是「包含」判定（pathx.Within 用 filepath.Rel，Windows 大小写语义不同），
// 且额外要求严格子路径，不能直接替换。
func isWithinDir(child, parent string) bool {
	cp := filepath.Clean(parent)
	cc := filepath.Clean(child)
	if strings.EqualFold(cc, cp) {
		return false
	}
	return strings.HasPrefix(strings.ToLower(cc), strings.ToLower(cp)+string(filepath.Separator))
}

// readTextFile 读文本文件（超限截断）。
//
// 返回 (content, truncated, err)：
//   - 仅接受**常规文件**（FIFO / 设备节点等可致无界阻塞，直接拒绝并报错）；
//   - 读取失败**返回错误**（不再静默回退为空内容 —— 区分「读取失败」与「空文件」）。
func readTextFile(path string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	if !st.Mode().IsRegular() {
		return "", false, fmt.Errorf("not a regular file: %s", path)
	}
	// 上限截断（512KB）：按 UTF-8 边界收尾（避免切断多字节字符）+ ReadFull 处理短读
	// （单次 f.Read 不保证读满，R8-C-2）。
	truncated := st.Size() > maxTextBytes
	limit := st.Size()
	if truncated {
		limit = maxTextBytes
	}
	buf := make([]byte, limit)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", false, err
	}
	return string(trimIncompleteUTF8(buf[:n])), truncated, nil
}

// trimIncompleteUTF8 丢弃末尾不完整的 UTF-8 多字节序列（截断边界落在字符中间时不留残字节乱码）。
func trimIncompleteUTF8(b []byte) []byte {
	for n := 1; n <= 4 && n <= len(b); n++ {
		if utf8.RuneStart(b[len(b)-n]) {
			if r, size := utf8.DecodeRune(b[len(b)-n:]); r == utf8.RuneError && size <= 1 {
				return b[:len(b)-n] // 该起始字节起的序列不完整 → 丢弃
			}
			return b
		}
	}
	return b
}

// maxCopyDepth 是 copyTree 递归下钻的深度上限（兜底防符号链接 / 绑定挂载成环导致的无限递归；
// 正常目录树深度远小于此）。
const maxCopyDepth = 64

// copyTree 递归复制文件/目录（src → dst；目标已存在时覆盖）。
//
// **不跟随符号链接 + 深度上限（R8-C-3）**：递归下钻一律用 os.Lstat / DirEntry 类型判定（**不
// 解引用链接**），符号链接条目**跳过**（不复制）；并设深度上限兜底。否则 work_dir 内自指/成环的
// symlink 会让原实现（os.Stat 跟随链接）无限递归至 OOM。
func copyTree(src, dst string) error {
	return copyTreeInto(src, dst, 0)
}

func copyTreeInto(src, dst string, depth int) error {
	if depth > maxCopyDepth {
		return fmt.Errorf("copy: 目录嵌套过深（>%d）: %s", maxCopyDepth, src)
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil // 不跟随符号链接（防成环）
	}
	if !fi.IsDir() {
		return copyFile(src, dst)
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue // 符号链接条目跳过（不跟随；Type 未知时由 copyTreeInto 内 Lstat 兜底识别）
		}
		if err := copyTreeInto(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// copyFile 复制单个文件。
//
// 出错时**清理半截目标文件**；仅走单一 Close 路径（原实现 `defer out.Close()` 与显式 `out.Close()`
// 构成**双重关闭**）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst) // 清理半截目标（半写文件不得残留）
		return err
	}
	return out.Close()
}

// listDirNodes 读目录浅层子项（隐藏/临时条目跳过），返回前端 tree 格式。
// hiddenDirs = 「不显示的目录」清单（与 watcher 递归同源，见 shouldHideDirName）。
func listDirNodes(dir string, hiddenDirs []string) []map[string]any {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if filterDirEntry(e.Name(), e.IsDir(), hiddenDirs) {
			continue
		}
		node := map[string]any{
			"name":   e.Name(),
			"path":   filepath.ToSlash(filepath.Join(dir, e.Name())),
			"is_dir": e.IsDir(),
		}
		if fi, err := e.Info(); err == nil {
			node["size"] = fi.Size()
			node["mtime"] = fi.ModTime().Format("2006-01-02 15:04:05")
		}
		out = append(out, node)
	}
	return out
}

// shouldHideDirName 判定**目录名**是否「不显示」：文件树显示过滤与 watcher 递归监听的
// **同一判据**（层2 同源）——凡是文件树不显示的目录，watcher 也不监听，反之亦然。
//   - "." 开头：恒隐藏（.git / .svn / .chonkpilot 等，不可配置放开）；
//   - 命中「不显示的目录」清单（目录名匹配、任意层级）。
func shouldHideDirName(name string, hiddenDirs []string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	for _, d := range hiddenDirs {
		if name == d {
			return true
		}
	}
	return false
}

// filterDirEntry 判断目录子项是否应对前端隐藏（文件树列表过滤）：
//   - 目录：shouldHideDirName（点开头 + 「不显示的目录」清单）—— 与 watcher 递归同源；
//   - 文件："."/"~$" 前缀；"~"/"-wal"/"-shm" 后缀（原口径不变，仅作用于文件）。
func filterDirEntry(name string, isDir bool, hiddenDirs []string) bool {
	if isDir {
		return shouldHideDirName(name, hiddenDirs)
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~$") {
		return true
	}
	if strings.HasSuffix(name, "~") || strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") {
		return true
	}
	return false
}

// ─── 错误构造 ────────────────────────────────────────────

func errForbidden(msg string) error {
	return mapError("forbidden", msg)
}

func errCreate(msg string) error {
	return mapError("create_failed", msg)
}

func errMkdir(msg string) error {
	return mapError("mkdir_failed", msg)
}

func errRemove(msg string) error {
	return mapError("remove_failed", msg)
}

func errRename(msg string) error {
	return mapError("rename_failed", msg)
}

func errCopy(msg string) error {
	return mapError("copy_failed", msg)
}

func errExists(msg string) error {
	return mapError("exists", msg)
}

// errRead 读失败（非常规文件 / IO 错误）；码取 61 §2.5 明列的 IO_ERROR 家族。
func errRead(msg string) error {
	return mapError("io_error", msg)
}

// errBadRequest 请求载荷无法解析（非 JSON / 结构不符）；失败不再静默为「空成功」，须回错误让调用方可见。
func errBadRequest(msg string) error {
	return mapError("bad_request", msg)
}

type mapErr map[string]string

func (e mapErr) Error() string { return e["message"] }

func mapError(code, message string) error {
	return mapErr{"code": code, "message": message}
}

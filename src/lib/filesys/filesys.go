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
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

const maxTextBytes = 512 * 1024

// Filesys 是文件服务实例。
type Filesys struct {
	bus  mq.Bus
	subs []mq.Sub
	wm   *watchManager

	// insts 是自持的实例视图：instance_id → work_dir（自 instance-register 登记，
	// 与 persist 同构）。请求的 work_dir **一律由本表解析**，不采信请求载荷（G-21）：
	// payload 里的 work_dir 只作契约字段保留，越权方无法借它把操作基目录指到绑外目录。
	mu    sync.Mutex
	insts map[string]string
}

// New 构建文件服务（未启动，调用 Start 后开始订阅）。
func New(bus mq.Bus) *Filesys {
	return &Filesys{bus: bus, wm: newWatchManager(bus), insts: map[string]string{}}
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
		// instance 生命周期（与 persist 同构）：自持「instance → work_dir」绑定，见 insts。
		// 只订阅 register/exit —— filesys 与发布方（GUI 桥 / httpapi / CLI）恒同进程同总线，
		// 绑定生命周期 = 进程生命周期，无分离形态心跳超时回收的需求（persist 的 sweep 是为了
		// 释放失效的库连接；filesys 无此资源）→ 心跳（instance-heartbeat）无消费方，不订阅。
		{msgkeys.TopicInstanceRegister, f.onInstanceRegister},
		{msgkeys.TopicInstanceExit, f.onInstanceExit},
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

// ─── 实例绑定（自持；G-21 信任边界）─────────────────────

// onInstanceRegister 登记实例工作目录绑定（61-消息一览 §4.1；与 persist 同构）。
// 发布方 = 入口可信代码（GUI 桥 / httpapi / CLI）；客户端上行**不能**注入本主题
// （两个入口的分派表都不含它 → 白名单外丢弃），故绑定值不可由客户端伪造。
func (f *Filesys) onInstanceRegister(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		InstanceID string `json:"instance_id"`
		WorkDir    string `json:"work_dir"`
	}
	_ = json.Unmarshal(v.Payload, &req)
	if req.InstanceID == "" || req.WorkDir == "" {
		return nil
	}
	f.mu.Lock()
	f.insts[req.InstanceID] = req.WorkDir
	f.mu.Unlock()
	return nil
}

// onInstanceExit 释放实例绑定（幂等：重复调用无副作用）。
func (f *Filesys) onInstanceExit(_ context.Context, _ string, v *mq.Value) error {
	var msg struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(v.Payload, &msg)
	if msg.InstanceID == "" {
		return nil
	}
	f.mu.Lock()
	delete(f.insts, msg.InstanceID)
	f.mu.Unlock()
	return nil
}

// workDir 解析请求所属实例绑定的工作目录：未登记 / 未绑定 → ok=false（调用方一律拒绝）。
// **不回落请求载荷的 work_dir** —— 回落会把刚关上的越权口子重新打开。
func (f *Filesys) workDir(instanceID string) (string, bool) {
	if instanceID == "" {
		return "", false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	wd, ok := f.insts[instanceID]
	return wd, ok && wd != ""
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
	v.Result = map[string]any{"path": p, "is_dir": true, "children": listDirNodes(p)}
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
	content, truncated := readTextFile(p)
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
	if !withinWorkDir(wd, np) {
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
	if !withinWorkDir(wd, np) {
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
	if filepath.IsAbs(req.NewName) || strings.ContainsAny(req.NewName, `/\`) {
		np = filepath.Clean(req.NewName)
		if !withinWorkDir(wd, np) {
			v.Errors = append(v.Errors, errForbidden("target outside work dir"))
			return nil
		}
	} else {
		np = filepath.Join(filepath.Dir(p), req.NewName)
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
	if !withinWorkDir(wd, dd) {
		v.Errors = append(v.Errors, errForbidden("dest outside work dir"))
		return nil
	}
	np := filepath.Join(dd, filepath.Base(p))
	if req.NewName != "" {
		if strings.ContainsAny(req.NewName, `/\`) {
			v.Errors = append(v.Errors, errCopy("new_name must be a bare file name"))
			return nil
		}
		np = filepath.Join(dd, req.NewName)
	}
	if np == p {
		v.Result = map[string]any{"ok": true, "path": np}
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

// onWatch 处理 filesys.watch：声明关注目录（recursive=true 时递归子目录；fire-and-forget，无 result）。
// instance_id 为声明者实例（桥注入），记入 watcher 供 filesys.changed/watch-error 广播携带。
func (f *Filesys) onWatch(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		WorkDir    string `json:"work_dir"`
		InstanceID string `json:"instance_id"`
		Path       string `json:"path"`
		Recursive  bool   `json:"recursive"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		return nil
	}
	wd, ok := f.workDir(req.InstanceID)
	if !ok {
		return nil
	}
	p, ok := absPath(wd, req.Path)
	if !ok {
		return nil
	}
	_ = f.wm.watch(wd, p, req.Recursive, req.InstanceID)
	return nil
}

// onUnwatch 处理 filesys.unwatch：取消关注目录（fire-and-forget，无 result）。
func (f *Filesys) onUnwatch(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		WorkDir    string `json:"work_dir"`
		InstanceID string `json:"instance_id"`
		Path       string `json:"path"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		return nil
	}
	wd, ok := f.workDir(req.InstanceID)
	if !ok {
		return nil
	}
	p, ok := absPath(wd, req.Path)
	if !ok {
		return nil
	}
	f.wm.unwatch(wd, p)
	return nil
}

// ─── 工具函数 ────────────────────────────────────────────

// absPath 把 path 转换为绝对路径（相对 workDir 则 join），并校验在 workDir 内。
func absPath(wd, p string) (string, bool) {
	if p == "" {
		return wd, true
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(wd, p)
	}
	if !withinWorkDir(wd, p) {
		return "", false
	}
	return p, true
}

// withinWorkDir 校验 path 在 base 内（安全边界）。
func withinWorkDir(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// readTextFile 读文本文件（超限截断）。
func readTextFile(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", false
	}
	if st.Size() > maxTextBytes {
		buf := make([]byte, maxTextBytes)
		n, _ := f.Read(buf)
		return string(buf[:n]), true
	}
	buf := make([]byte, st.Size())
	n, _ := f.Read(buf)
	return string(buf[:n]), false
}

// copyTree 递归复制文件/目录（src → dst；目标已存在时覆盖）。
func copyTree(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
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
		if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyFile 复制单个文件。
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
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// listDirNodes 读目录浅层子项（隐藏/临时条目跳过），返回前端 tree 格式。
func listDirNodes(dir string) []map[string]any {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if filterDirEntry(e.Name()) {
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

// filterDirEntry 判断目录子项是否应对前端隐藏。规则（I-44，对齐既有 FilterDirEntry 口径）：
//   - "." 开头：隐藏项（.chonkpilot / .ide 等）
//   - "~$" 开头：Office 锁文件（~$xxx.docx）
//   - "~" 结尾：编辑器备份文件
//   - "-wal" / "-shm" 结尾：SQLite WAL / SHM 边车文件
func filterDirEntry(name string) bool {
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

type mapErr map[string]string

func (e mapErr) Error() string { return e["message"] }

func mapError(code, message string) error {
	return mapErr{"code": code, "message": message}
}

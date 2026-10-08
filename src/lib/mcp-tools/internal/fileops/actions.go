// actions.go — filesys_run DSL 动作实现。
//
// 复用 chonkpilot-lib/dsl 引擎：RPL/APD/PTC/INS/DEL/MOV/CPY 注册为 Raw 动作，
// 参数原文整体透传（与 llm_run/desktop_run/browser_run 同一引擎），本文件负责
// 解析 #"路径" 句柄与 "字符串" 参数并执行文件操作。
//
// 语义（定稿 2026-09-04）：
//
//	RPL #"path" "search" "replace"    全局替换所有出现（search 为空报错）
//	APD #"path" "content"             追加到文件尾（文件非空且末尾无换行时先补 \n）
//	PTC #"path" "<unified diff>"      应用补丁（applyUnifiedDiff）
//	INS #"path" "content"             创建文件（已存在 → 失败）
//	DEL #"path" "search"              删除所有包含 search 的行
//	DEL #"path"                       删除文件/目录
//	MOV #"from" #"to"                 移动/重命名（目录 = 先复制、成功后逐文件删除源）
//	CPY #"from" #"to"                 复制（文件或目录）
//
// 一致性规则：
//   - 修改/删除已有文件、新建文件、移动/复制（源+目标两侧）前加跨进程锁
//     （多路径按字典序加锁防 ABBA 死锁；30s 内重试，超时真失败），成功后解锁
//   - md5 期望（顶层 md5 参数，锁内首触时校验一次）不一致 → 记 fails，后续跳过该文件
//   - 单操作失败不整体失败：失败进 fails（{file, op, error}），其余继续执行
//   - 返回聚合：modified / created（各含 path,type,size,mtime,md5；modified 另含 before→after diff）/ deleted / fails
package fileops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
)

// failItem 单项失败明细。
type failItem struct {
	File  string `json:"file"`
	Op    string `json:"op"`
	Error string `json:"error"`
}

// fileEntry 结果中「新增 / 修改」文件条目的元信息（大小 / 时间 / md5）。
// 目录条目仅 path/type/mtime（size=0、md5 省略）。
type fileEntry struct {
	Path  string `json:"path"`
	Type  string `json:"type"`           // file | dir
	Size  int64  `json:"size"`           // 字节数（目录为 0）
	Mtime string `json:"mtime"`          // 修改时间（RFC3339）
	MD5   string `json:"md5,omitempty"`  // 文件内容 md5（目录省略）
	Diff  string `json:"diff,omitempty"` // 仅 modified：before→after 的 unified diff
}

// fileRef 记录新增文件的展示路径与解析后绝对路径（结果组装时再 stat/md5）。
type fileRef struct {
	disp     string
	resolved string
}

// fileEntryOf 组装单条条目（stat + md5）；stat 失败（如已被后续动作删除）返回 ok=false。
func fileEntryOf(disp, resolved, diff string) (fileEntry, bool) {
	st, err := os.Stat(resolved)
	if err != nil {
		return fileEntry{}, false
	}
	e := fileEntry{Path: disp, Mtime: st.ModTime().Format(time.RFC3339), Diff: diff}
	if st.IsDir() {
		e.Type = "dir"
		return e, true
	}
	e.Type = "file"
	e.Size = st.Size()
	if sum, merr := fileMD5(resolved); merr == nil {
		e.MD5 = sum
	}
	return e, true
}

// createdEntries 把新增文件引用转为结果条目（stat + md5）。
func createdEntries(refs []fileRef) []fileEntry {
	out := make([]fileEntry, 0, len(refs))
	for _, r := range refs {
		if e, ok := fileEntryOf(r.disp, r.resolved, ""); ok {
			out = append(out, e)
		}
	}
	return out
}

// editCtx 收集脚本执行状态（动作可能并发执行，字段受 mu 保护）。
type editCtx struct {
	workDir   string
	expectMD5 map[string]string

	mu           sync.Mutex
	created      []fileRef
	deleted      []string
	fails        []failItem
	originals    map[string]string // resolved → 该文件首个修改动作前的文本（供最终 diff）
	disp         map[string]string // resolved → 展示路径（用户输入原样）
	voided       map[string]string // resolved → 校验失败原因（md5/二进制），后续动作静默跳过
	pathViolated bool              // R-11：出现过参数级路径违规（相对路径）→ 整体失败
	sandboxHit   bool              // agentbox：出现过沙箱越界（允许目录外读写）→ 整体失败
	sandboxMsgs  []string          // agentbox：越界拒绝的可诊断消息（去重前）
}

func newEditCtx(workDir string, expectMD5 map[string]string) *editCtx {
	return &editCtx{
		workDir:   workDir,
		expectMD5: expectMD5,
		originals: map[string]string{},
		disp:      map[string]string{},
		voided:    map[string]string{},
	}
}

func (ec *editCtx) addCreated(disp, resolved string) {
	ec.mu.Lock()
	ec.created = append(ec.created, fileRef{disp: disp, resolved: resolved})
	ec.mu.Unlock()
}

func (ec *editCtx) addDeleted(p string) {
	ec.mu.Lock()
	ec.deleted = append(ec.deleted, p)
	ec.mu.Unlock()
}

func (ec *editCtx) addFail(file, op, msg string) {
	ec.mu.Lock()
	ec.fails = append(ec.fails, failItem{File: file, Op: op, Error: msg})
	ec.mu.Unlock()
}

// resolve 解析用户路径（R-11 强校验：绝对路径或 ~/ 开头；相对路径拒绝）。
// 违规时置 pathViolated 标记，供 HandleFileManager 判定「整体失败」。
func (ec *editCtx) resolve(p string) (string, string) {
	resolved, errMsg := ResolvePath(p, ec.workDir)
	if errMsg != "" {
		ec.mu.Lock()
		ec.pathViolated = true
		ec.mu.Unlock()
	}
	return resolved, errMsg
}

// pathViolation 报告本次执行是否出现过参数级路径违规（R-11）。
func (ec *editCtx) pathViolation() bool {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return ec.pathViolated
}

// guard 是 agentbox 沙箱强制点（filesys_run 全部文件动作）：
// write=true 要求路径落在「可写目录（递归）」内；false 要求落在「可读目录（递归）」内。
// 越界 → 记入 sandboxMsgs + 置 sandboxHit（顶层整体失败），返回拒绝错误。
// 沙箱未启用 → 直接放行（不产生任何额外开销）。
func (ec *editCtx) guard(disp, abs string, write bool) error {
	if err := sandboxErr(abs, write); err != nil {
		ec.mu.Lock()
		ec.sandboxHit = true
		ec.sandboxMsgs = append(ec.sandboxMsgs, fmt.Sprintf("%s：%s", disp, err.Error()))
		ec.mu.Unlock()
		return err
	}
	return nil
}

// sandboxViolation 报告本次执行是否出现过沙箱越界，并返回拒绝消息（去重）。
func (ec *editCtx) sandboxViolation() (bool, []string) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	if !ec.sandboxHit {
		return false, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ec.sandboxMsgs))
	for _, m := range ec.sandboxMsgs {
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return true, out
}

// fileVerbs 是 filesys_run 的全部文件操作动词（每个动词参数内的 #"path" 均须满足 R-11）。
var fileVerbs = []string{"RPL", "APD", "PTC", "INS", "DEL", "MOV", "CPY"}

// fileVerbSet 便于 O(1) 判定。
var fileVerbSet = func() map[string]bool {
	m := map[string]bool{}
	for _, v := range fileVerbs {
		m[v] = true
	}
	return m
}()

// prevalidateScriptPaths 在执行前预校验 DSL 内**字面**文件句柄（R-11）：
// 返回违规消息列表（空 = 通过）。两类来源：
//   - 动作动词参数内的 `#"路径"`（RPL/APD/... 为 Raw，逐个 token 校验）；
//   - 核心语句的文件句柄（LOOP/SET 数据源、IF exist、访问器、`=> 目标`、TYPEOF/ENTRY/
//     SPLIT/JOIN/PUSH 的值）——经 dsl.CollectHandleRefs 收集。
//
// 含 {{}} 插值的句柄跳过（插值结果在执行时由 ScriptFS 兜底校验），避免动态路径误报。
// 字面相对路径据此在起执行前被拒绝，不产生任何文件改动。
func prevalidateScriptPaths(script *dsl.Script) []string {
	var msgs []string
	// 核心语句的文件句柄（含 IF 条件、LOOP/SET 数据源、=> 目标）
	for _, ref := range dsl.CollectHandleRefs(script) {
		if strings.Contains(ref.Path, "{{") {
			continue
		}
		label := fmt.Sprintf("第 %d 行 %s", ref.Line, ref.Desc)
		if msg := ValidateField(label, ref.Path); msg != "" {
			msgs = append(msgs, msg)
		}
	}
	var walk func(sts []dsl.Stmt)
	walk = func(sts []dsl.Stmt) {
		for _, st := range sts {
			switch t := st.(type) {
			case *dsl.ActionStmt:
				if !t.RawArgsMode || !fileVerbSet[strings.ToUpper(t.Verb)] {
					continue
				}
				toks, err := tokenizeFileArgs(t.RawArgs)
				if err != nil {
					continue // 语法错误交由引擎报
				}
				label := fmt.Sprintf("第 %d 行 %s", t.Line(), t.Verb)
				for _, tk := range toks {
					if !tk.file || strings.Contains(tk.text, "{{") {
						continue
					}
					if msg := ValidateField(label, tk.text); msg != "" {
						msgs = append(msgs, msg)
					}
				}
			case *dsl.IfStmt:
				walk(t.Block)
			case *dsl.LoopStmt:
				walk(t.Block)
			case *dsl.ParallelStmt:
				walk(t.Block)
			}
		}
	}
	walk(script.Stmts)
	return msgs
}

// fileEditActions 注册 filesys_run 全部动词（Raw：参数原文透传）。
func fileEditActions(ec *editCtx) []dsl.Action {
	acts := make([]dsl.Action, 0, len(fileVerbs))
	for _, v := range fileVerbs {
		verb := v
		acts = append(acts, dsl.Action{
			Name: verb,
			Raw:  true,
			Run: func(sc *dsl.Scope, raw string) (string, error) {
				toks, err := tokenizeFileArgs(raw)
				if err != nil {
					return "", fmt.Errorf("%s 参数错误：%s", verb, err)
				}
				if err := ec.dispatch(verb, toks); err != nil {
					return "", fmt.Errorf("%s：%s", verb, err)
				}
				return "", nil
			},
		})
	}
	return acts
}

// dispatch 按动词分发执行。
func (ec *editCtx) dispatch(verb string, toks []argTok) error {
	switch verb {
	case "RPL":
		return ec.rpl(toks)
	case "APD":
		return ec.apd(toks)
	case "PTC":
		return ec.ptc(toks)
	case "INS":
		return ec.ins(toks)
	case "DEL":
		return ec.del(toks)
	case "MOV":
		return ec.mov(toks)
	case "CPY":
		return ec.cpy(toks)
	}
	return fmt.Errorf("未知操作 %s", verb)
}

// ─── 参数解析 ──────────────────────────────────────────

// argTok 参数 token：file=true 表示 #"路径" 句柄，否则为 "字符串"。
type argTok struct {
	file bool
	text string
}

// tokenizeFileArgs 把动作参数原文拆分为 token 序列。
// 仅接受 #"路径"（句柄）与 "文本"（字符串）两种；引号内支持 \" \\ \n \t \r 转义。
func tokenizeFileArgs(raw string) ([]argTok, error) {
	var out []argTok
	i, n := 0, len(raw)
	for i < n {
		c := raw[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '#':
			if i+1 >= n || raw[i+1] != '"' {
				return nil, fmt.Errorf("文件句柄格式应为 #\"路径\"")
			}
			val, ni, err := scanQuote(raw, i+1, n)
			if err != nil {
				return nil, err
			}
			out = append(out, argTok{file: true, text: val})
			i = ni
		case c == '"':
			val, ni, err := scanQuote(raw, i, n)
			if err != nil {
				return nil, err
			}
			out = append(out, argTok{text: val})
			i = ni
		default:
			return nil, fmt.Errorf("意外字符 %q（参数须为 #\"路径\" 或 \"文本\"）", string(c))
		}
	}
	return out, nil
}

// scanQuote 从 qIdx（指向 "）读取引号内容到闭合，返回解码文本与结束下标。
func scanQuote(s string, qIdx, n int) (string, int, error) {
	var b strings.Builder
	i := qIdx + 1
	closed := false
	for i < n {
		c := s[i]
		if c == '\\' && i+1 < n {
			switch nx := s[i+1]; nx {
			case '"', '\\':
				b.WriteByte(nx)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte('\\')
				b.WriteByte(nx)
			}
			i += 2
			continue
		}
		if c == '"' {
			closed = true
			i++
			break
		}
		b.WriteByte(c)
		i++
	}
	if !closed {
		return "", 0, fmt.Errorf("字符串未闭合")
	}
	return b.String(), i, nil
}

// ─── 修改核心 ──────────────────────────────────────────

// modify 对已存在文本文件执行内容修改：加锁 → 首触检查（快照/二进制/md5）→
// apply（输入当前文本返回新文本与是否变更）→ 写盘解锁。
// 文件级失败一律记 fails 并返回 nil（不中断后续操作）。
func (ec *editCtx) modify(verb, disp, resolved string, apply func(cur string) (string, bool, error)) error {
	release, lerr := acquireLock(resolved, lockRetryCount)
	if lerr != nil {
		ec.addFail(disp, verb, "加锁失败（30s 内重试未果）："+lerr.Error())
		return nil
	}
	defer release()

	// 该文件已因校验失败被标记 → 静默跳过（首触时已报）
	ec.mu.Lock()
	if _, void := ec.voided[resolved]; void {
		ec.mu.Unlock()
		return nil
	}
	_, firstTouch := ec.originals[resolved]
	ec.mu.Unlock()

	if !firstTouch {
		data, rerr := os.ReadFile(resolved)
		if rerr != nil {
			ec.addFail(disp, verb, "读取失败："+rerr.Error())
			return nil
		}
		if IsBinaryBytes(data) {
			ec.markVoided(resolved, "二进制文件不支持文本编辑")
			ec.addFail(disp, verb, "二进制文件不支持文本编辑")
			return nil
		}
		if e := ec.checkMD5(disp, resolved); e != nil {
			ec.markVoided(resolved, e.Error())
			ec.addFail(disp, verb, e.Error())
			return nil
		}
		ec.mu.Lock()
		ec.originals[resolved] = DecodeText(data)
		ec.disp[resolved] = disp
		ec.mu.Unlock()
	}

	// 读当前盘内容（首触后到本动作前，同文件前序动作可能已写盘）并 apply
	data, rerr := os.ReadFile(resolved)
	if rerr != nil {
		ec.addFail(disp, verb, "读取失败："+rerr.Error())
		return nil
	}
	cur := DecodeText(data)
	after, changed, aerr := apply(cur)
	if aerr != nil {
		ec.addFail(disp, verb, aerr.Error())
		return nil
	}
	if changed && after != cur {
		if werr := os.WriteFile(resolved, []byte(after), 0644); werr != nil {
			ec.addFail(disp, verb, "写入失败："+werr.Error())
		}
	}
	return nil
}

func (ec *editCtx) markVoided(resolved, reason string) {
	ec.mu.Lock()
	ec.voided[resolved] = reason
	ec.mu.Unlock()
}

// checkMD5 锁内校验期望 md5（key：展示路径或绝对路径任一匹配）。
func (ec *editCtx) checkMD5(disp, resolved string) error {
	expected := ec.expectMD5[disp]
	if expected == "" {
		expected = ec.expectMD5[resolved]
	}
	if expected == "" {
		return nil
	}
	actual, merr := fileMD5(resolved)
	if merr != nil || actual != expected {
		return fmt.Errorf("MD5 不一致，其他进程已修改，请重新 read 后进行 patch")
	}
	return nil
}

// ─── RPL / APD / PTC ───────────────────────────────────

// RPL #"path" "search" "replace" — 全局替换。
func (ec *editCtx) rpl(toks []argTok) error {
	if len(toks) != 3 || !toks[0].file {
		return fmt.Errorf("用法：RPL #\"路径\" \"查找文本\" \"替换文本\"")
	}
	disp, search, replace := toks[0].text, toks[1].text, toks[2].text
	if search == "" {
		return fmt.Errorf("查找文本不能为空")
	}
	resolved, errMsg := ec.resolve(disp)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	if err := ec.guard(disp, resolved, true); err != nil {
		return err
	}
	return ec.modify("RPL", disp, resolved, func(cur string) (string, bool, error) {
		if !strings.Contains(cur, search) {
			return cur, false, fmt.Errorf("未找到匹配内容 %q（请先 file_read 确认现状）", search)
		}
		return strings.ReplaceAll(cur, search, replace), true, nil
	})
}

// APD #"path" "content" — 追加到文件尾。
func (ec *editCtx) apd(toks []argTok) error {
	if len(toks) != 2 || !toks[0].file {
		return fmt.Errorf("用法：APD #\"路径\" \"追加内容\"")
	}
	disp, content := toks[0].text, toks[1].text
	resolved, errMsg := ec.resolve(disp)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	if err := ec.guard(disp, resolved, true); err != nil {
		return err
	}
	return ec.modify("APD", disp, resolved, func(cur string) (string, bool, error) {
		if content == "" {
			return cur, false, nil
		}
		sep := ""
		if cur != "" && !strings.HasSuffix(cur, "\n") {
			sep = "\n"
		}
		return cur + sep + content, true, nil
	})
}

// PTC #"path" "<unified diff>" — 应用补丁。
func (ec *editCtx) ptc(toks []argTok) error {
	if len(toks) != 2 || !toks[0].file {
		return fmt.Errorf("用法：PTC #\"路径\" \"<unified diff 文本>\"")
	}
	disp, diffText := toks[0].text, toks[1].text
	resolved, errMsg := ec.resolve(disp)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	if err := ec.guard(disp, resolved, true); err != nil {
		return err
	}
	return ec.modify("PTC", disp, resolved, func(cur string) (string, bool, error) {
		patched, err := applyUnifiedDiff(cur, diffText)
		if err != nil {
			return cur, false, err
		}
		if patched == cur {
			return cur, false, fmt.Errorf("diff 未产生变化（无 @@ hunk 或补丁已应用）")
		}
		return patched, true, nil
	})
}

// ─── INS / DEL ─────────────────────────────────────────

// INS #"path" "content" — 创建文件。
// 锁目标路径收口 TOCTOU（C-27）：否则并发 INS 双双「stat 不存在」后竞写同一目标。
func (ec *editCtx) ins(toks []argTok) error {
	if len(toks) != 2 || !toks[0].file {
		return fmt.Errorf("用法：INS #\"路径\" \"文件内容\"")
	}
	disp, content := toks[0].text, toks[1].text
	resolved, errMsg := ec.resolve(disp)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	if err := ec.guard(disp, resolved, true); err != nil {
		return err
	}
	release, lerr := acquireLock(resolved, lockRetryCount)
	if lerr != nil {
		ec.addFail(disp, "INS", "加锁失败（30s 内重试未果）："+lerr.Error())
		return nil
	}
	defer release()
	if _, serr := os.Stat(resolved); serr == nil {
		ec.addFail(disp, "INS", "目标已存在（如需修改请用 RPL/PTC/APD）")
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		ec.addFail(disp, "INS", "创建父目录失败："+err.Error())
		return nil
	}
	if err := os.WriteFile(resolved, []byte(content), 0644); err != nil {
		ec.addFail(disp, "INS", "写入失败："+err.Error())
		return nil
	}
	ec.addCreated(disp, resolved)
	return nil
}

// DEL #"path" ["search"] — 删行（带 search）或删除文件/目录（仅句柄）。
func (ec *editCtx) del(toks []argTok) error {
	if len(toks) < 1 || len(toks) > 2 || !toks[0].file {
		return fmt.Errorf("用法：DEL #\"路径\" [\"删除行关键字\"]")
	}
	disp := toks[0].text
	resolved, errMsg := ec.resolve(disp)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	if err := ec.guard(disp, resolved, true); err != nil {
		return err
	}
	if len(toks) == 1 {
		return ec.delEntry(disp, resolved)
	}
	search := toks[1].text
	if search == "" {
		return fmt.Errorf("删除行关键字不能为空")
	}
	return ec.modify("DEL", disp, resolved, func(cur string) (string, bool, error) {
		lines := strings.Split(cur, "\n")
		kept := make([]string, 0, len(lines))
		removed := 0
		for _, ln := range lines {
			if strings.Contains(ln, search) {
				removed++
			} else {
				kept = append(kept, ln)
			}
		}
		if removed == 0 {
			return cur, false, fmt.Errorf("未找到包含 %q 的行", search)
		}
		return strings.Join(kept, "\n"), true, nil
	})
}

// delEntry 删除文件或目录（文件/目录均先加锁；目录整树删除）。
func (ec *editCtx) delEntry(disp, resolved string) error {
	fi, serr := os.Stat(resolved)
	if serr != nil {
		ec.addFail(disp, "DEL", "文件不存在："+serr.Error())
		return nil
	}
	// 目录与文件对齐加同一把跨进程锁（锁文件 = 目标同级的 <path>.chonk.lock）：
	// 避免与并发写/删除竞态（C-15）。
	release, lerr := acquireLock(resolved, lockRetryCount)
	if lerr != nil {
		ec.addFail(disp, "DEL", "加锁失败（30s 内重试未果）："+lerr.Error())
		return nil
	}
	defer release()
	if fi.IsDir() {
		if err := os.RemoveAll(resolved); err != nil {
			ec.addFail(disp, "DEL", "删除目录失败："+err.Error())
			return nil
		}
		ec.addDeleted(disp)
		return nil
	}
	if err := os.Remove(resolved); err != nil {
		ec.addFail(disp, "DEL", "删除失败："+err.Error())
		return nil
	}
	ec.addDeleted(disp)
	return nil
}

// ─── MOV / CPY ─────────────────────────────────────────

// acquireLocks 按确定顺序获取多个路径的跨进程锁（C-27）：
//   - 路径先 Clean 去重（锁不可重入：同一路径重复 acquire 会自锁重试 30s 后失败）；
//   - 加锁序 = 路径字典序（全局全序）：MOV/CPY 同时锁源与目标时，所有并发方按同一顺序
//     加锁，杜绝 ABBA 死锁；
//   - 任一加锁失败 → 释放已获取的锁后返回错误。
func acquireLocks(paths ...string) (func(), error) {
	uniq := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = filepath.Clean(p)
		if seen[p] {
			continue
		}
		seen[p] = true
		uniq = append(uniq, p)
	}
	sort.Strings(uniq)
	var releases []func()
	for _, p := range uniq {
		release, err := acquireLock(p, lockRetryCount)
		if err != nil {
			for _, r := range releases {
				r()
			}
			return nil, err
		}
		releases = append(releases, release)
	}
	return func() {
		for _, r := range releases {
			r()
		}
	}, nil
}

// MOV #"from" #"to" — 文件重命名；目录 = 复制 + 逐文件删除源（copy 成功才动源）。
func (ec *editCtx) mov(toks []argTok) error {
	if len(toks) != 2 || !toks[0].file || !toks[1].file {
		return fmt.Errorf("用法：MOV #\"源路径\" #\"目标路径\"")
	}
	fromD, toD := toks[0].text, toks[1].text
	from, errMsg := ec.resolve(fromD)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	to, errMsg := ec.resolve(toD)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	// agentbox 沙箱：源（读+删）与目标（写）均须在可写目录内
	if err := ec.guard(fromD, from, true); err != nil {
		return err
	}
	if err := ec.guard(toD, to, true); err != nil {
		return err
	}
	fi, serr := os.Stat(from)
	if serr != nil {
		ec.addFail(fromD, "MOV", "源不存在："+serr.Error())
		return nil
	}
	// 先建目标父目录：锁文件（O_EXCL 创建于目标路径旁）要求父目录已存在，
	// MkdirAll 幂等且仅建目录，不削弱锁保护的写语义（C-27）。
	if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
		ec.addFail(fromD, "MOV", "创建目标目录失败："+err.Error())
		return nil
	}
	// 源+目标两侧同锁（C-27）：目标侧此前无锁，与并发写/删除竞态；字典序加锁防 ABBA。
	release, lerr := acquireLocks(from, to)
	if lerr != nil {
		ec.addFail(fromD, "MOV", "加锁失败（30s 内重试未果）："+lerr.Error())
		return nil
	}
	defer release()
	if fi.IsDir() {
		return ec.movDir(fromD, toD, from, to)
	}
	return ec.movFile(fromD, toD, from, to)
}

// movFile 单文件：rename（锁由 mov 统一持有：源+目标）；失败（跨卷/占用）回退 copy + delete。
func (ec *editCtx) movFile(fromD, toD, from, to string) error {
	if rerr := os.Rename(from, to); rerr != nil {
		// 跨卷/占用 → 复制回退（源锁持有期间复制保持一致性）
		if _, cerr := copyFile(from, to, ""); cerr != "" {
			ec.addFail(fromD, "MOV", "移动失败（rename 与复制回退均失败）："+rerr.Error()+" / "+cerr)
			return nil
		}
		if derr := os.Remove(from); derr != nil {
			ec.addFail(fromD, "MOV", "已复制到目标但源删除失败（可手动清理）："+derr.Error())
			return nil
		}
	}
	ec.addCreated(toD, to)
	ec.addDeleted(fromD)
	return nil
}

// movDir 目录：先整树复制到目标，成功后逐文件删除源（锁由 mov 统一持有：源根+目标根，
// 目录 MOV/CPY 不逐项加锁、以两根路径为界，C-27）；
// 源删除的部分失败列具体文件进 fails（不影响其余删除，数据已在目标不丢失）。
func (ec *editCtx) movDir(fromD, toD, from, to string) error {
	if err := copyDirRecursive(from, to); err != nil {
		ec.addFail(fromD, "MOV", "复制目录失败（源未改动）："+err.Error())
		return nil
	}
	for _, f := range removeDirTree(from) {
		ec.addFail(f, "MOV", "源删除失败（已复制到目标，可手动清理）："+f)
	}
	ec.addCreated(toD, to)
	ec.addDeleted(fromD)
	return nil
}

// CPY #"from" #"to" — 文件（锁源读）或目录（整树）复制。
func (ec *editCtx) cpy(toks []argTok) error {
	if len(toks) != 2 || !toks[0].file || !toks[1].file {
		return fmt.Errorf("用法：CPY #\"源路径\" #\"目标路径\"")
	}
	fromD, toD := toks[0].text, toks[1].text
	from, errMsg := ec.resolve(fromD)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	to, errMsg := ec.resolve(toD)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	// agentbox 沙箱：源（读+删）与目标（写）均须在可写目录内
	if err := ec.guard(fromD, from, true); err != nil {
		return err
	}
	if err := ec.guard(toD, to, true); err != nil {
		return err
	}
	fi, serr := os.Stat(from)
	if serr != nil {
		ec.addFail(fromD, "CPY", "源不存在："+serr.Error())
		return nil
	}
	// 先建目标父目录：锁文件（O_EXCL 创建于目标路径旁）要求父目录已存在，
	// MkdirAll 幂等且仅建目录，不削弱锁保护的写语义（C-27）。
	if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
		ec.addFail(fromD, "CPY", "创建目标目录失败："+err.Error())
		return nil
	}
	// 源+目标两侧同锁（C-27）：目标侧此前无锁，与并发写/删除竞态；字典序加锁防 ABBA。
	// 目录与文件一致（目录以源根+目标根两把锁为界，不逐项加锁）。
	release, lerr := acquireLocks(from, to)
	if lerr != nil {
		ec.addFail(fromD, "CPY", "加锁失败（30s 内重试未果）："+lerr.Error())
		return nil
	}
	defer release()
	if fi.IsDir() {
		if err := copyDirRecursive(from, to); err != nil {
			ec.addFail(fromD, "CPY", "复制目录失败："+err.Error())
			return nil
		}
		ec.addCreated(toD, to)
		return nil
	}
	if _, cerr := copyFile(from, to, ""); cerr != "" {
		ec.addFail(fromD, "CPY", "复制失败："+cerr)
		return nil
	}
	ec.addCreated(toD, to)
	return nil
}

// removeDirTree 深度优先逐项删除；先子后父，残留（被占用）路径收集返回。
func removeDirTree(root string) []string {
	var failed []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return []string{root}
	}
	for _, e := range entries {
		p := filepath.Join(root, e.Name())
		if e.IsDir() {
			failed = append(failed, removeDirTree(p)...)
		} else if err := os.Remove(p); err != nil {
			failed = append(failed, p)
		}
	}
	if len(failed) == 0 {
		if err := os.Remove(root); err != nil {
			failed = append(failed, root)
		}
	}
	return failed
}

// ─── 结果聚合 ──────────────────────────────────────────

// finish 汇总：modified = 各被改文件的 before（首个修改动作前快照）→ 盘上最终内容 diff；
// 每条附 size / mtime / md5（fileEntry）。
func (ec *editCtx) finish() []fileEntry {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	keys := make([]string, 0, len(ec.originals))
	for r := range ec.originals {
		keys = append(keys, r)
	}
	sort.Strings(keys)

	var mods []fileEntry
	for _, r := range keys {
		data, err := os.ReadFile(r)
		if err != nil || IsBinaryBytes(data) {
			continue
		}
		after := DecodeText(data)
		before := ec.originals[r]
		if before == after {
			continue
		}
		p := ec.disp[r]
		if e, ok := fileEntryOf(p, r, unifiedDiff(p, before, after)); ok {
			mods = append(mods, e)
		}
	}
	return mods
}

// snapshot 供结果组装读取（避免裸取 map 并发读写冲突）。
func (ec *editCtx) snapshot() (created []fileRef, deleted []string, fails []failItem) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	created = append([]fileRef{}, ec.created...)
	deleted = append([]string{}, ec.deleted...)
	fails = append([]failItem{}, ec.fails...)
	return
}

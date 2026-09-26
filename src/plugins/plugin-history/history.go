// Package history 是文件历史插件（git 快照，server 内嵌形态）。
//
// 定位（2026-09-02 决策）：history 不再自持存储（废弃 history.db），直接用 git——
// 只有 work-dir 是 git 仓库才启用；无仓库 → 提示用户 git init（不提交）。
// 触发：主 session（parents 空）turn 开始（turn-start）与完成（llm-complete）时，
// 检查 work-dir 未提交变更，有 → add -A（.chonkpilot 已 gitignore）→ commit 快照；
// 无变更 → 跳过（零开销）。子轮次（parents 非空）不触发。
// **门控（2026-09-19，42 §2 (125)）：默认不开启** —— 只有 prj-config `history.enabled` 显式
// `"true"` 才提交（缺失/非法 = 关闭）；避免在用户仓库产生未预期的 git 提交。
package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/winproc"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// 订阅主题（相对主题 session-turn-start / session-complete；chonk. 前缀在 mq
// 初始化 Options.Prefix 注入一次，业务层不出现 chonk. 字面）。
const (
	turnStartSubject   = "session-turn-start"
	llmCompleteSubject = "session-complete"
	// instance-register：实例注册后异步回读 history.enabled 门控。
	instanceRegisterSubject = "instance-register"
	// data 面（persist 订阅）：history.enabled 实时同步 + 单键回读。
	prjConfigRefreshSubject = "data-prj-config-refresh"
	prjConfigLoadSubject    = "data-prj-config-load"
	// prj-config 键：history.enabled（"true"/"false"；**仅显式 "true" 才开启**，
	// 缺失/非法 → 视为关闭，见 [42 §2 (125)]）。
	historyEnabledKey = "history.enabled"
	dataTimeout       = 5 * time.Second
)

// History 是 git 快照插件（server 插件钩子实现）。
type History struct {
	deps    plugin.Deps
	im      *instance.Manager
	subs    []mq.Sub
	mu      sync.Mutex // 串行化提交 + notRepo 访问
	notRepo map[string]bool

	gateMu   sync.Mutex      // gate/gateBusy 访问
	gate     map[string]bool // instanceID → 是否启用（存在 = 已回读；缺失 = 未回读 → 关闭）
	gateBusy map[string]bool // instanceID → 回读中（去重异步回读）
}

// New 构建 history 插件。
func New() *History {
	return &History{
		notRepo:  make(map[string]bool),
		gate:     make(map[string]bool),
		gateBusy: make(map[string]bool),
	}
}

// Name 插件名。
func (h *History) Name() string { return "history" }

// Start 订阅 turn-start / llm-complete（server 全部插件加载后由宿主调用）。
func (h *History) Start(d plugin.Deps) error {
	h.deps = d
	logf := d.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if _, err := exec.LookPath("git"); err != nil {
		logf("history: git 不可用（%v）——文件历史禁用，安装 git 后生效", err)
		return nil // git 缺失：功能禁用，不视为启动失败
	}
	h.im = instance.New(d.Bus)
	if err := h.im.Start(); err != nil {
		return err
	}
	// instance-register → 异步回读 history.enabled（门控；**缺失/非法 = 关闭**）。
	regSub, err := d.Bus.On(instanceRegisterSubject, 0, func(_ context.Context, subj string, v *mq.Value) error {
		h.onInstanceRegister(subj, v.Payload)
		return nil
	})
	if err != nil {
		return err
	}
	h.subs = append(h.subs, regSub)
	// data-prj-config-refresh → history.enabled 开关实时同步（复用既有主题，不新增 MQ）。
	cfgSub, err := d.Bus.On(prjConfigRefreshSubject, 0, h.onPrjConfigRefresh)
	if err != nil {
		return err
	}
	h.subs = append(h.subs, cfgSub)
	for _, s := range []struct {
		subject string
		h       mq.Handler
	}{
		{turnStartSubject, h.onTurnStart},
		{llmCompleteSubject, h.onComplete},
	} {
		sub, err := d.Bus.On(s.subject, 0, func(_ context.Context, subj string, v *mq.Value) error {
			s.h(subj, v.Payload)
			return nil
		})
		if err != nil {
			return err
		}
		h.subs = append(h.subs, sub)
	}
	logf("history: git 快照就绪（turn-start / llm-complete → 主会话变更提交；**默认关闭**，仅 prj-config %s=\"true\" 时提交）", historyEnabledKey)
	return nil
}

// logf 取日志函数（未装配时静默）。
func (h *History) logf() func(string, ...any) {
	if h.deps.Logf != nil {
		return h.deps.Logf
	}
	return func(string, ...any) {}
}

// gateFromValue 判定 prj-config history.enabled 的配置值是否启用文件历史。
// **口径（42 §2 (125)）：只有显式 "true" 才开启**；缺失 / "" / "false" / 其他非法值 → 关闭
// （默认不开启；避免"缺省即视为启用"在用户仓库产生未预期的 git 提交）。
func gateFromValue(val string) bool {
	return val == "true"
}

// gateEnabled 判断实例是否启用文件历史：**只有显式开启（回读到 "true"）才为真**；
// 未回读（gate 缺失）→ 关闭（默认不开启）。缺失时异步触发一次回读。
func (h *History) gateEnabled(instanceID string) bool {
	h.gateMu.Lock()
	enabled, ok := h.gate[instanceID]
	if !ok && !h.gateBusy[instanceID] {
		h.gateBusy[instanceID] = true
		go h.readGate(instanceID)
	}
	h.gateMu.Unlock()
	return ok && enabled
}

// onInstanceRegister 实例注册 → 触发 history.enabled 异步回读。
func (h *History) onInstanceRegister(_ string, payload []byte) {
	var ev struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(payload, &ev) != nil || ev.InstanceID == "" {
		return
	}
	h.gateEnabled(ev.InstanceID) // 仅触发回读；返回值此处不用
}

// readGate 经 data-prj-config-load 回读 history.enabled：只有显式 "true" 落 gate=true；
// 缺失/非法/读失败 → 关闭（默认不开启）。
func (h *History) readGate(instanceID string) {
	val, err := prjConfigReadKey(h.deps.Bus, instanceID, historyEnabledKey)
	enabled := err == nil && gateFromValue(val)
	h.gateMu.Lock()
	delete(h.gateBusy, instanceID)
	h.gate[instanceID] = enabled
	h.gateMu.Unlock()
	if err != nil {
		h.logf()("history: 读 prj-config %s 失败（instance=%s）：%v（按默认关闭）", historyEnabledKey, instanceID, err)
		return
	}
	h.logf()("history: %s=%q（instance=%s）→ 门控 %v", historyEnabledKey, val, instanceID, enabled)
}

// onPrjConfigRefresh 处理 data-prj-config-refresh：只关心 history.enabled。
// 广播载荷 {instance_id?, id, op, list}（persist.go refresh 在 instanceID 非空时携带 instance_id，
// 见 chonkpilot-data/persist/persist.go:754-758）——本实现忽略 instance_id：v1 限制：
// 单宿主典型形态为单实例/单 workdir，直接把开关应用到全部已知实例；
// 多 workdir 并存时按同一开关刷新（按 instance 差异化留待 v2）。
func (h *History) onPrjConfigRefresh(_ context.Context, _ string, v *mq.Value) error {
	var ev struct {
		ID   string         `json:"id"`
		Op   string         `json:"op"`
		List map[string]any `json:"list"`
	}
	if json.Unmarshal(v.Payload, &ev) != nil || ev.ID != historyEnabledKey {
		return nil
	}
	// 只有显式 "true" 才开启；缺失/""/非法/op=delete → 关闭（默认不开启）。
	enabled := false
	if ev.List != nil {
		if raw, ok := ev.List[historyEnabledKey]; ok {
			enabled = gateFromValue(strval(raw))
		}
	}
	h.gateMu.Lock()
	for _, inst := range h.im.List() {
		h.gate[inst.ID] = enabled
	}
	h.gateMu.Unlock()
	h.logf()("history: prj-config %s → %v（op=%s）", historyEnabledKey, enabled, ev.Op)
	return nil
}

// mainOnly 返回事件的 instance/session + 是否主会话（parents 为空）。
func mainOnly(payload []byte) (instanceID, session string, ok bool) {
	var ev struct {
		InstanceID string   `json:"instance_id"`
		Session    string   `json:"session"`
		Parents    []string `json:"parents"`
	}
	_ = json.Unmarshal(payload, &ev)
	if ev.InstanceID == "" || ev.Session == "" {
		return "", "", false
	}
	if len(ev.Parents) > 0 {
		return "", "", false // 子轮次不触发
	}
	return ev.InstanceID, ev.Session, true
}

func (h *History) onTurnStart(_ string, payload []byte) {
	if id, sess, ok := mainOnly(payload); ok {
		h.commitSnapshot(id, sess)
	}
}

func (h *History) onComplete(_ string, payload []byte) {
	if id, sess, ok := mainOnly(payload); ok {
		h.commitSnapshot(id, sess)
	}
}

// commitSnapshot 主会话 turn 边界提交：work-dir 有未提交变更 → add + commit。
func (h *History) commitSnapshot(instanceID, session string) {
	if !h.gateEnabled(instanceID) {
		return // prj-config history.enabled 未显式 "true" → 关闭文件历史（默认不开启）
	}
	rec, ok := h.im.Lookup(instanceID)
	if !ok || rec.WorkDir == "" {
		return
	}
	wd := rec.WorkDir
	logf := h.deps.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.notRepo[wd] {
		return
	}
	if _, err := os.Stat(filepath.Join(wd, ".git")); err != nil {
		h.notRepo[wd] = true
		logf("history: %s 非 git 仓库——git init 后启用文件历史", wd)
		return
	}
	out, err := runGit(wd, "status", "--porcelain")
	if err != nil {
		logf("history: git status %s: %v", wd, err)
		return
	}
	if strings.TrimSpace(out) == "" {
		return // 无变更，跳过
	}
	ensureGitignore(wd)
	if _, err := runGit(wd, "add", "-A"); err != nil {
		logf("history: git add %s: %v", wd, err)
		return
	}
	if _, err := runGit(wd, "commit", "-m",
		fmt.Sprintf("chonk: snapshot (session=%s, files=%d)", session, len(strings.Split(strings.TrimSpace(out), "\n")))); err != nil {
		logf("history: git commit %s: %v", wd, err)
		return
	}
	logf("history: committed snapshot at %s (session=%s)", wd, session)
}

// runGit 执行 git -C wd <args...>，合并输出返回。
func runGit(wd string, args ...string) (string, error) {
	all := append([]string{"-C", wd}, args...)
	cmd := exec.Command("git", all...)
	// 宿主为 windowsgui 无控制台：隐藏 git.exe 的控制台窗口，避免快照时闪黑窗。
	cmd.SysProcAttr = winproc.SysProcAttr()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ensureGitignore 保证 work-dir .gitignore 含 .chonkpilot/（chonkpilot 数据目录不入库）。
func ensureGitignore(wd string) {
	p := filepath.Join(wd, ".gitignore")
	raw, err := os.ReadFile(p)
	if err == nil && strings.Contains(string(raw), ".chonkpilot") {
		return
	}
	// 不存在或未忽略 → 追加（创建）
	add := ""
	if err != nil {
		add = "# chonkpilot\n"
	}
	add += ".chonkpilot/\n"
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(add)
}

// ─── prj-config 回读（data-* 请求-响应，镜像 codegraph 模式）──

var reqSeq atomic.Uint64

func newReqID() string {
	return fmt.Sprintf("history-%d-%d", time.Now().UnixNano(), reqSeq.Add(1))
}

// strval 任意值 → 字符串（配置 list 值归一）。
func strval(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

// dataEmit 向 data-<域>-<动作> 请求面发一次请求并等应答（persist 把应答发布到请求同主题，
// 须按 req_id 关联收敛）。
func dataEmit(bus mq.Bus, subject string, req map[string]any) (map[string]any, error) {
	req["req_id"] = newReqID()
	type reply struct {
		result map[string]any
		err    error
	}
	done := make(chan reply, 1)
	sub, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			ReqID  string         `json:"req_id"`
			OK     *bool          `json:"ok"`
			Result map[string]any `json:"result"`
			Error  string         `json:"error"`
		}
		if json.Unmarshal(v.Payload, &m) != nil || m.ReqID != req["req_id"] || m.OK == nil {
			return nil // 他人请求/应答忽略
		}
		if !*m.OK {
			msg := m.Error
			if msg == "" {
				msg = "persist error"
			}
			done <- reply{err: errors.New(msg)}
			return nil
		}
		done <- reply{result: m.Result}
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	if f := bus.Emit(context.Background(), subject, req); f.Wait().Err() != nil {
		return nil, f.Wait().Err()
	}
	select {
	case r := <-done:
		return r.result, r.err
	case <-time.After(dataTimeout):
		return nil, fmt.Errorf("%s via persist 应答超时", subject)
	}
}

// prjConfigReadKey 读 prj-config 单键：load 应答 result.data = 值字符串（键不存在 → ""）。
func prjConfigReadKey(bus mq.Bus, instanceID, key string) (string, error) {
	res, err := dataEmit(bus, prjConfigLoadSubject, map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"id": key},
	})
	if err != nil {
		return "", err
	}
	val, _ := res["data"].(string)
	return val, nil
}

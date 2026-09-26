// Package agentbox 是 agentbox 沙箱的**策略面**（决策见 42-决策记录 §2 (104)/(109)，
// 设计见 docs/spec/10-architecture/14-安全域-agentbox.md）：
//
//   - **策略来源** = `security-*`（项目级信任目录：一条 = {dir, writable}，语义 = 「可读 /
//     可写目录」且**递归**——允许目录下的所有子路径一并放行）；
//   - **作用对象** = 仅「扫描到的 runtime」（mcp-server 扫描出的执行器工具）：宿主 spawn
//     executor 时把该策略经环境变量 `CHONKPILOT_SANDBOX`（JSON 数组）注入子进程，executor
//     在**自己进程内**用它强制拦截越界读写；
//   - **默认不启用**：未设置 / 空串 / 非法 JSON → 策略为 nil → 一律放行（与未引入本包前
//     行为逐字节等价，避免影响既有用户）。
//
// 本包**不**做 I/O，只做策略换算（security-* → 允许集）、路径判定与环境编解码；
// 强制点在各消费方（executor 的文件操作 choke point / gateway 的 spawn 环境）。
//
// 未实现（如实标注）：① 符号链接解析（`EvalSymlinks`）——判定按**字面路径**归一，不追
// 软链；② 审计落 DataDir（14 §5 规划）——拒绝仅记 stderr 一行（宿主捕获落日志），不写文件。
package agentbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
)

// EnvSandbox 是 agentbox 策略的传递环境变量名：值为策略 JSON（[]Rule）。
// 宿主（mcp-server spawn executor / gateway spawn stdio 上游）注入；未设置 = 不启用隔离。
// 说明：不经 MQ 主题传递（约束：不新增 MQ 主题），仅子进程环境。
const EnvSandbox = "CHONKPILOT_SANDBOX"

// Rule 是单条允许目录（对齐 `security-*` 的 value 形状 `{"dir","writable"}`）：
// dir = 目录（绝对路径；`~/` 开头按宿主用户目录展开）；writable = 是否允许写（可写必然可读）。
type Rule struct {
	Dir      string `json:"dir"`
	Writable bool   `json:"writable"`
}

// ErrDenied 是越界拒绝的哨兵错误（调用方可用 errors.Is 判定）。
var ErrDenied = errors.New("agentbox: 路径越界")

// DeniedError 是越界拒绝的可诊断错误：带被拒路径、操作（读/写）与当前允许目录集。
type DeniedError struct {
	Path    string
	Write   bool
	Allowed []Rule
}

// Error 构造可诊断消息（含全部允许目录，便于 LLM/用户定位该配哪些 security-* 条目）。
func (e *DeniedError) Error() string {
	op := "读"
	if e.Write {
		op = "写"
	}
	dirs := "-"
	if len(e.Allowed) > 0 {
		parts := make([]string, 0, len(e.Allowed))
		for _, r := range e.Allowed {
			s := r.Dir
			if r.Writable {
				s += "(rw)"
			} else {
				s += "(ro)"
			}
			parts = append(parts, s)
		}
		dirs = strings.Join(parts, ", ")
	}
	return fmt.Sprintf("授权失败（agentbox）：禁止%s %s —— 该路径不在允许的%s目录（递归）内；允许目录：%s",
		op, e.Path, op, dirs)
}

// Is 支持 errors.Is(err, ErrDenied)。
func (e *DeniedError) Is(target error) bool { return target == ErrDenied }

// Policy 是归一化后的允许目录集（递归）。nil 指针 = 未启用隔离（一律放行）。
type Policy struct {
	rules []Rule
}

// New 把 `security-*` 条目换算为策略：逐条归一化目录（空/不可解析 → 丢弃），保序。
// 返回的 Policy 即便 rules 为空也「已启用」——**空允许集 = 全拒**（用户显式开启隔离但未配
// 目录时的严格语义；未配置开关时调用方**不应**构造 Policy，见 InitFromEnv 的空串分支）。
func New(rules []Rule) *Policy {
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		d := normalizeDir(r.Dir)
		if d == "" {
			continue
		}
		out = append(out, Rule{Dir: d, Writable: r.Writable})
	}
	return &Policy{rules: out}
}

// Enabled 报告策略是否已启用（nil = 未启用）。
func (p *Policy) Enabled() bool { return p != nil }

// Rules 返回允许目录副本（审计/诊断用）。
func (p *Policy) Rules() []Rule {
	if p == nil {
		return nil
	}
	return append([]Rule{}, p.rules...)
}

// Parse 解析策略 JSON（[]Rule）；空串 → (nil, nil) = 不启用；非法 JSON → 错误。
func Parse(raw string) (*Policy, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	var rules []Rule
	if err := json.Unmarshal([]byte(s), &rules); err != nil {
		return nil, fmt.Errorf("agentbox: 策略 JSON 解析失败: %w", err)
	}
	return New(rules), nil
}

// Marshal 输出策略 JSON（nil → ""）；供宿主注入环境变量与诊断打印。
func (p *Policy) Marshal() string {
	if p == nil {
		return ""
	}
	b, err := json.Marshal(p.rules)
	if err != nil {
		return ""
	}
	return string(b)
}

// Allowed 判定路径是否放行（write=true 时要求该路径落在**可写**目录内）。
// 未启用（nil）→ 恒放行。
func (p *Policy) Allowed(path string, write bool) bool {
	if p == nil {
		return true
	}
	return p.match(path, write)
}

// Check 判定路径；越界返回 *DeniedError（可实现 errors.Is(err, ErrDenied)）。
// 未启用（nil）或放行 → nil。
func (p *Policy) Check(path string, write bool) error {
	if p == nil {
		return nil
	}
	if p.match(path, write) {
		return nil
	}
	return &DeniedError{Path: path, Write: write, Allowed: p.Rules()}
}

// match 是判定核心：把目标路径归一为绝对 Clean 形态，逐条与允许目录做**递归包含**判定
// （相同 或 以 `<dir><分隔符>` 为前缀）。Windows 下大小写不敏感（双侧小写后比较）；
// 盘符不同 / 目录外 → 前缀不成立 → 拒绝。`..` 已由 Clean 收敛，不区分相对/绝对输入
// （相对输入按进程 cwd 归一；R-11 已保证工具参数为绝对或 ~/ 开头，此处仅兜底）。
func (p *Policy) match(path string, write bool) bool {
	target, ok := normalizeTarget(path)
	if !ok {
		return false
	}
	for _, r := range p.rules {
		if write && !r.Writable {
			continue
		}
		if contains(r.Dir, target) {
			return true
		}
	}
	return false
}

// normalizeDir 归一允许目录：`~` 展开 → Abs(Clean)。
func normalizeDir(dir string) string {
	d := strings.TrimSpace(dir)
	if d == "" {
		return ""
	}
	if d == "~" || strings.HasPrefix(d, "~/") || strings.HasPrefix(d, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		d = filepath.Join(home, strings.TrimLeft(strings.TrimPrefix(d, "~"), `/\`))
	}
	abs, err := filepath.Abs(d)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

// normalizeTarget 归一被判定路径：非空 → Abs(Clean)。
func normalizeTarget(path string) (string, bool) {
	s := strings.TrimSpace(path)
	if s == "" {
		return "", false
	}
	abs, err := filepath.Abs(s)
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

// contains 报告 target 是否等于 dir 或位于 dir 之下（递归；Windows 大小写不敏感）。
func contains(dir, target string) bool {
	d, t := dir, target
	if runtime.GOOS == "windows" {
		d, t = strings.ToLower(d), strings.ToLower(t)
	}
	if t == d {
		return true
	}
	return strings.HasPrefix(t, d+string(filepath.Separator))
}

// ─── 进程级当前策略（executor / 上游 server 进程内生效）─────────────────

// current 是进程级当前策略（nil = 不启用隔离）。executor 每次进程启动读一次环境变量。
var current atomic.Pointer[Policy]

// Set 覆盖进程级策略（nil = 不启用）；供 InitFromEnv 与测试使用。
func Set(p *Policy) { current.Store(p) }

// Current 返回进程级策略（nil = 不启用）。
func Current() *Policy { return current.Load() }

// InitFromEnv 读取 EnvSandbox 并设为进程级策略：
//   - 未设置 / 空串 → 不启用（返回 nil，放行一切；**默认兼容**）；
//   - 非法 JSON → 不启用 + stderr 记一行（不因隔离引入崩溃或静默失败）；
//   - 合法 → 启用（空数组 = 全拒，见 New）。
func InitFromEnv() *Policy {
	p, err := Parse(os.Getenv(EnvSandbox))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[agentbox] %s 解析失败，本次不启用隔离: %v\n", EnvSandbox, err)
		Set(nil)
		return nil
	}
	Set(p)
	return p
}

// Check 用进程级策略判定路径（未启用 → nil）。拒绝时记一行 stderr 审计（可诊断信息）。
func Check(path string, write bool) error {
	p := current.Load()
	if p == nil {
		return nil
	}
	err := p.Check(path, write)
	if err != nil {
		// 审计（14 §5「审计落 DataDir」的低成本替代）：stderr 一行，由宿主捕获落日志。
		fmt.Fprintf(os.Stderr, "[agentbox] %v\n", err)
	}
	return err
}

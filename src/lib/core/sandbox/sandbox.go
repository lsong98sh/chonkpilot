// Package sandbox 提供**进程级写隔离**：以 Low Integrity Level 启动子进程，
// 并用 Job Object 约束其资源与进程树。
//
// 设计约束（2026-10-21 定，见 docs/tool-plane/工具面重构与Gateway二次优化设计方案.md）：
//
//   - **只限制写**：Low IL 进程写不了 Medium IL 对象；**读不受限**（Windows IL 语义）。
//     「禁止读敏感文件」不在本包，属应用层 `agentbox` 的 DenyRead 黑名单。
//   - **不修改任何 DACL**：只改子进程令牌 + Job。允许写目录需提前打 Low IL 标签
//     （[EnsureLowIL]），否则子进程对它也只能读不能写。
//   - **Job Object 不能限制读写**：它只表达"进程数 / 内存 / CPU / 进程树生命周期"，
//     没有任何文件路径字段（POC 已实测：Job 内进程照常写越界目录）。
//   - 非 Windows → [ErrUnsupportedPlatform]，调用方据此 fail-closed 或显式降级。
//
// 与 `agentbox` 的分工：
//
//	进程级（本包）：Low IL → 保证「写不出允许目录」（OS 强制，不可绕过）
//	应用级（agentbox）：路径黑名单 → 保证「不去读 ~/.ssh」（可绕过，兜底）
package sandbox

import (
	"errors"
	"runtime"
	"strings"
)

// ErrUnsupportedPlatform 表示当前平台不支持进程级写隔离。
var ErrUnsupportedPlatform = errors.New("sandbox: 当前平台不支持进程级写隔离")

// Limits 是 Job Object 的资源约束；零值 = 不限制该项。
type Limits struct {
	// MaxProcesses 限制 Job 内同时存活的进程数（0 = 不限）。
	MaxProcesses int
	// MaxMemoryBytes 限制 Job 内进程的总提交内存（0 = 不限）。
	MaxMemoryBytes uint64
}

// Spec 是一次 spawn 的沙箱参数。
type Spec struct {
	// WritableRoots 是允许写的目录（须已通过 [EnsureLowIL] 打标）。
	// 本包不据此施加限制——限制由 Low IL 承担；该字段仅用于调用方预检与审计。
	WritableRoots []string
	// Limits 是 Job 的资源约束。
	Limits Limits
}

// Rule 是安全目录配置项（与 `security-*` 配置的 value 形状一致）。
type Rule struct {
	Dir      string `json:"dir"`
	Writable bool   `json:"writable"`
}

// WritableRoots 从规则集提取可写目录（TrimSpace、跳过空 Dir、去重、保序）。
// Windows 下按大小写不敏感去重（盘符/路径名不区分大小写），其它平台原样比较。
func WritableRoots(rules []Rule) []string {
	var out []string
	seen := make(map[string]bool, len(rules))
	for _, r := range rules {
		if !r.Writable {
			continue
		}
		d := strings.TrimSpace(r.Dir)
		if d == "" {
			continue
		}
		key := d
		if runtime.GOOS == "windows" {
			key = strings.ToLower(d)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

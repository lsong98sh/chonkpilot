// sandboxpolicy.go —— 按 work_dir 解析**可写白名单**（SANDBOX-WRITE-1 P2 / 方案 §5.5.2）。
//
// 用途：插件桥 `plugin/v1/sandbox.resolve{work_dir}`（宿主实现 = 本文件的
// [Server.SandboxPolicyForWorkDir]）。定时任务执行前没有 `tools/call` 上下文，拿不到 gateway
// 下发的 `_meta.chonkpilot.sandbox`，改由插件主动向宿主拉取。
//
// 口径（C2）：
//   - 命中在册实例（同 work_dir）→ 用**内存态** prj `security-*`（securityRules）+ 内置默认项；
//   - 未命中在册实例 → **只用**内置默认项（按 P2 口径，不短开库）；
//   - 读失败 → 返回错误（插件按 fail-open 处理：不隔离执行并留痕），不猜一份空策略。
package server

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lsong98sh/chonkpilot-framework/host/core/lockfile"
	"github.com/lsong98sh/chonkpilot-util/pathx"
)

// SandboxPolicyForWorkDir 返回该 work_dir 的可写白名单（去重、保序）。
//
// 内置默认项（不落库）：work_dir 本身、`<work_dir>/.chonkpilot/tmp`。
// 用户配置项：该 work_dir 在册实例的 prj `security-*`（只写白名单，递归）。
//
// 返回 error 仅表示「策略读不到」（security-* 读取失败）；work_dir 为空属参数错误。
func (s *Server) SandboxPolicyForWorkDir(workDir string) ([]string, error) {
	wd := strings.TrimSpace(workDir)
	if wd == "" {
		return nil, fmt.Errorf("sandbox.resolve: work_dir 必填")
	}
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	add := func(dir string) {
		d := strings.TrimSpace(dir)
		if d == "" {
			return
		}
		key := strings.ToLower(filepath.Clean(d))
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, d)
	}

	// 内置默认项（C2）。
	add(wd)
	add(filepath.Join(wd, lockfile.ProjectDataDirName, "tmp"))

	// 在册实例 → 复用内存态 securityRules（与 gateway 同一份读取口径）。
	for _, info := range s.im.List() {
		if !pathx.Same(info.WorkDir, wd) {
			continue
		}
		rules, ok := s.securityRules(info.ID)
		if !ok {
			return nil, fmt.Errorf("sandbox.resolve: 读取 security-* 失败（instance=%s）", info.ID)
		}
		for _, r := range rules {
			if r.Writable {
				add(r.Dir)
			}
		}
		break
	}
	return out, nil
}

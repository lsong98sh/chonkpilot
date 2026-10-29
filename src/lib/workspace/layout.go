// Package workspace 是「工作区管理」（PM）：进程内单例 lib，位于基座最底层。
//
// 它持有 work_dir 维度的**动态、不落库**状态：
//   - 工作区注册表与引用计数（首个实例注册 → workdir-opened；最后一个退出 → workdir-closed）；
//   - 物理布局的唯一算法（workDir / .chonkpilot / users/<OS 用户>）；
//   - 路径解析与边界校验（词法 + 真实路径，防符号链接 / 目录联接越界）。
//
// 边界（见 docs/spec/20-modules/2I-工作区管理.md）：
//   - 能力面 = Go 函数调用（同步）；事件面 = MQ（workdir-opened / workdir-closed）。
//   - workdir-closed **只发事件**：不清索引、不删数据、不删目录。
//   - 不做业务清算（取消 turn / 释放锁 / 注销 capability 仍归各消费者）。
//   - 不管窗口与「关闭 / 隐藏」的交互策略。
//   - 路径校验是**内部纪律**，不是安全边界（in-proc 插件可直接绕过 OS 调用）。
package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/lsong98sh/chonkpilot-framework/host/core/paths"
)

// usrRootOverride 是 prjusr 数据根的覆盖值（测试隔离 / 便携部署）。
// 对齐 data.SetDataHome 的 dataRoot 语义：非空时优先于 `<workDir>/.chonkpilot`。
var usrRootOverride atomic.Pointer[string]

// SetUsrRootOverride 设置 prjusr 数据根覆盖（空串 = 取消覆盖）。
func SetUsrRootOverride(root string) {
	r := strings.TrimSpace(root)
	usrRootOverride.Store(&r)
}

// UserDirName 返回当前 OS 用户目录名（`C:\Users\13661` → `13661`），用作 `users/<name>`。
// 取 home 的 basename 而非 `USERNAME`（域账号 / 改名场景两者不一致）；非法名回落 `default`。
//
// 唯一实现：`data.UserDirName` 已改为转发本函数。
func UserDirName() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "default"
	}
	name := filepath.Base(filepath.Clean(home))
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\:*?"<>|`) {
		return "default"
	}
	return name
}

// Layout 是某个工作区的物理布局（全部为绝对路径）。
type Layout struct {
	// WorkDir 是工作区根（Clean 后，保持调用方传入的原始大小写）。
	WorkDir string
	// WorkDirReal 是 WorkDir 解析符号链接 / 目录联接后的真实路径（解析失败 = 空串）。
	WorkDirReal string
	// PrjDir 是项目数据根 = `<workDir>/.chonkpilot`（prj 层所在目录）。
	PrjDir string
	// PrjUsrDir 是个人运行态数据根（prjusr）：dataDir 非空 → dataDir；否则
	// `<workDir>/.chonkpilot/users/<OS 用户名>`（SetUsrRootOverride 可覆盖其前缀）。
	PrjUsrDir string
	// DataDir 是 instance-register 透传的数据目录（desktop 恒空）。
	DataDir string
}

// LayoutOf 计算工作区布局（纯函数，无 IO）。
//
// dataDir 语义与 data.PrjUsrPath 完全一致（唯一实现）：
//   - 非空 → paths.ResolveDir(dataDir, workDir)（临时 / 覆盖形态，与 prj 同根）；
//   - 空   → `<workDir>/.chonkpilot`（可被 SetUsrRootOverride 覆盖）/users/<OS 用户名>。
func LayoutOf(workDir, dataDir string) Layout {
	wd := filepath.Clean(workDir)
	l := Layout{
		WorkDir:     wd,
		WorkDirReal: Real(wd),
		PrjDir:      filepath.Join(wd, ".chonkpilot"),
		DataDir:     dataDir,
	}
	if dataDir != "" {
		l.PrjUsrDir = paths.ResolveDir(dataDir, wd)
		return l
	}
	root := l.PrjDir
	if p := usrRootOverride.Load(); p != nil && *p != "" {
		root = *p
	}
	l.PrjUsrDir = filepath.Join(root, "users", UserDirName())
	return l
}

// AbsWithin 把 p 转为绝对路径（相对 base 则 join），并校验其仍在 base 内。
// 返回的仍是**词法**绝对路径（不替换为真实路径，保持对外 path 形状不变）。
func AbsWithin(base, p string) (string, bool) {
	if p == "" {
		return base, true
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	if !Within(base, p) {
		return "", false
	}
	return p, true
}

// Within 校验 path 在 base 内：先词法（WithinLexical），再解析符号链接 / 目录联接后的真实路径。
func Within(base, path string) bool {
	if !WithinLexical(base, path) {
		return false
	}
	return WithinLexical(Real(base), Real(path))
}

// WithinLexical 校验 path 在 base 内（纯词法边界；base/path 均为绝对路径）。
func WithinLexical(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// Real 返回 p 解析符号链接 / Windows 目录联接后的真实路径。
// 采用逐段 os.Readlink 解析：filepath.EvalSymlinks 不解析 Windows 目录联接（mount point），
// 而 os.Readlink 对符号链接与目录联接均返回目标，故自行逐段解析；无法解析的段原样拼回。
// 链接自环 / 过深（>64 次替换）→ 返回空串（fail-closed：Within 判为不在 base 内）。
func Real(p string) string {
	if p == "" || !filepath.IsAbs(p) {
		return p
	}
	cur := p
	for i := 0; i < 64; i++ {
		next, changed := resolveLinksOnce(cur)
		if !changed {
			return next
		}
		cur = next
	}
	return ""
}

// resolveLinksOnce 对 p 逐段前进，遇到可 Readlink 的组件（符号链接 / 目录联接）即用其目标替换
// （相对目标按链接所在目录解析），返回解析后路径与是否发生过替换。
func resolveLinksOnce(p string) (string, bool) {
	vol := filepath.VolumeName(p)
	cur := vol + string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(p, vol), string(filepath.Separator))
	changed := false
	for _, seg := range parts {
		if seg == "" {
			continue
		}
		next := filepath.Join(cur, seg)
		if tgt, err := os.Readlink(next); err == nil {
			if !filepath.IsAbs(tgt) {
				tgt = filepath.Join(cur, tgt)
			}
			cur = filepath.Clean(tgt)
			changed = true
			continue
		}
		cur = next
	}
	return cur, changed
}

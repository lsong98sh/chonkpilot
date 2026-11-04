// Package workspace 是「工作区管理」（PM）：进程内单例 lib，位于基座最底层。
//
// 它持有 work_dir 维度的**动态、不落库**状态：
//   - 工作区打开态（首个实例注册 → workdir-opened；最后一个退出 → workdir-closed）；
//   - 实例视图**委托**共通 `instance.Manager`（单一数据源，P5 步骤 1 / C15）；
//   - 物理布局与路径边界算法转发 `workspace/layout`（唯一实现）。
//
// 边界（见 docs/spec/20-modules/2I-工作区管理.md）：
//   - 能力面 = Go 函数调用（同步）；事件面 = MQ（workdir-opened / workdir-closed）。
//   - workdir-closed **只发事件**：不清索引、不删数据、不删目录。
//   - 不做业务清算（取消 turn / 释放锁 / 注销 capability 仍归各消费者）。
//   - 不管窗口与「关闭 / 隐藏」的交互策略。
//   - 路径校验是**内部纪律**，不是安全边界（in-proc 插件可直接绕过 OS 调用）。
package workspace

import (
	"github.com/chonkpilot/chonkpilot-workspace/layout"
)

// Layout 是某个工作区的物理布局（唯一实现 = layout.Layout）。
type Layout = layout.Layout

// LayoutOf 计算工作区布局（薄封装，实现 = layout.Of）。
func LayoutOf(workDir, dataDir string) Layout { return layout.Of(workDir, dataDir) }

// SetUsrRootOverride 设置 prjusr 数据根覆盖（薄封装，实现 = layout.SetUsrRootOverride）。
func SetUsrRootOverride(root string) { layout.SetUsrRootOverride(root) }

// UserDirName 返回当前 OS 用户名目录名（薄封装，实现 = layout.UserDirName）。
func UserDirName() string { return layout.UserDirName() }

// AbsWithin 把 p 转为绝对路径并校验其仍在 base 内（薄封装，实现 = layout.AbsWithin）。
func AbsWithin(base, p string) (string, bool) { return layout.AbsWithin(base, p) }

// Within 校验 path 在 base 内（词法 + 真实路径；薄封装，实现 = layout.Within）。
func Within(base, path string) bool { return layout.Within(base, path) }

// WithinLexical 校验 path 在 base 内（纯词法；薄封装，实现 = layout.WithinLexical）。
func WithinLexical(base, path string) bool { return layout.WithinLexical(base, path) }

// Real 返回 p 解析符号链接 / 目录联接后的真实路径（薄封装，实现 = layout.Real）。
func Real(p string) string { return layout.Real(p) }

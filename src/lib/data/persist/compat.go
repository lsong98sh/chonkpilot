// **转发面**：跨 module 消费方（`chonkpilot-llm` / 插件 / 测试工程 / 外壳）在用、但实现已下沉
// `chonkpilot-data/internal/...` 的符号在此按**别名 / 薄转发**保持可达（Go 的 `internal` 门禁
// 不允许模块外直接 import 下沉后的包）。
//
// 口径（23 §7 · 41 G-36）：
//   - **优先改走门面**：域行为一律经 `facade.API`（各域门面面），本文件**不承载任何逻辑**；
//   - **转发仅限「非域行为的契约/helper」**：工具消息 content 契约（role=tool 的
//     {call,result,async} 形状，被 llm server 读写消息时直接解析）、capability 根路径规则
//     （装配层按同一规则解析三级根，避免路径规则分叉）、记忆库类别名清单（system prompt 指引
//     列举的数据源）、记录值转字符串 helper（测试断言用）。
//
// 转发清单（下沉前 → 现实现）：
//
//	Sval / SvalOf                 → internal/kernel.Sval
//	ToolContent 家族 / ParseToolContent → internal/kernel（role=tool content 契约）
//	ToolStatus*（生命周期常量）    → internal/kernel
//	CapUserRoot / CapProjectRoot  → internal/capfs（三级 capability 根规则）
//	MemoryUserCategory / MemoryCategoryNames → internal/memory
package persist

import (
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-data/internal/memory"
)

// ── 记录值 helper（internal/kernel）──────────────────────────────

// Sval 把 Record 值转字符串（Record 由 json.Unmarshal 产生：string/float64/bool/nil）。
func Sval(v any) string { return kernel.Sval(v) }

// ── role=tool 消息 content 契约（internal/kernel）─────────────────

// ToolContent 是 role=tool 消息 content 的 JSON 结构（存储/消息面契约）。
type ToolContent = kernel.ToolContent

// ToolCallContent 是工具发起内容。
type ToolCallContent = kernel.ToolCallContent

// ToolResultContent 是工具结果。
type ToolResultContent = kernel.ToolResultContent

// ToolAsyncContent 是"转异步/转后台"信息。
type ToolAsyncContent = kernel.ToolAsyncContent

// ToolPair status 常量（tool_pair 消息生命周期）。
const (
	ToolStatusPending     = kernel.ToolStatusPending
	ToolStatusRunning     = kernel.ToolStatusRunning
	ToolStatusProvisional = kernel.ToolStatusProvisional
	ToolStatusCompleted   = kernel.ToolStatusCompleted
	ToolStatusFailed      = kernel.ToolStatusFailed
	ToolStatusCancelled   = kernel.ToolStatusCancelled
	ToolStatusInterrupted = kernel.ToolStatusInterrupted
)

// ParseToolContent 解析 role=tool 消息 content（新结构优先，旧 ToolPairPayload 自动归一）。
func ParseToolContent(content string) (ToolContent, bool) { return kernel.ParseToolContent(content) }

// ── 三级 capability 根规则（internal/capfs）──────────────────────

// CapUserRoot 用户级 capability 根（~/.chonkpilot/capability；usrPath 注入时随其所在目录）。
// 装配层按同一规则解析三级根，避免路径规则分叉。
func CapUserRoot(usrPath string) string { return capfs.UserRoot(usrPath) }

// CapProjectRoot 项目级 capability 根（<workdir>/.chonkpilot/capability）。
func CapProjectRoot(workDir string) string { return capfs.ProjectRoot(workDir) }

// ── 记忆库类别清单（internal/memory）─────────────────────────────

// MemoryUserCategory 是唯一用户级记忆类别（不可配置；跨项目偏好）。
const MemoryUserCategory = memory.MemoryUserCategory

// MemoryCategoryNames 返回**项目级**记忆类别名（中文，去 .md；供 system prompt 带出指引列举）。
func MemoryCategoryNames() []string { return memory.MemoryCategoryNames() }

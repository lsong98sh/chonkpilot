/**
 * 「涉及文件变动」per-tool 选项的纯逻辑（usr 键 `tool_async` 的 `touch_files` 字段）。
 *
 * 语义（用户口径 2026-09-28 / 2026-11-14）：
 *   - 该选项决定 `plugin-history` 的**前置打点钩子**（`history-pre-tool-hook`）是否在某工具执行前打检查点；
 *   - 打点 → 列表标「打点」；不打点 → 标「不打点」。
 *
 * 缺省来源（优先级从高到低）：
 *   1. 契约声明 `_meta.touch_files`（tool.md 的 `touch_files: true|false`）→ 直接采用；
 *   2. 无契约声明 → 现有按来源启发式（**与后端 gateway `resolveTouchFiles` 同源**，改一处须同步另一处）：
 *      - **self 节点内置工具**（`_meta.server.alias|node === 'self'`）：仅 `filesys_run` / `script_run` 涉及，
 *        其余内置工具（file_read / file_find / web_fetch / browser_run / desktop_run …）不涉及；
 *      - **dir 节点 / 第三方 MCP / 无法判定（无 `_meta.server`）**：保守按**涉及**（true），不丢安全。
 *
 * 显式配置（usr `tool_async[<暴露名>].touch_files`，布尔）优先于缺省；未配置 → 缺省。
 * 标错只会让检查点**粒度变粗**（轮末补点仍在、`git diff` 一致性校验仍生效），不丢安全。
 */
import { stripToolPrefix } from './toolSource.js'

/** self 节点标识（=`_meta.server.alias|node` 的 self 取值；见 toolSource.js）。 */
export const SELF_NODE_ID = 'self'

/** 「涉及文件变动」的 self 内置工具白名单（后端 `selfTouchFiles` 同源；仅在契约未声明时兜底）。 */
export const DEFAULT_TOUCH_TOOLS = ['filesys_run', 'script_run']

/** 是否 self 节点内置工具（无法判定 → false）。 */
function isSelfTool(server) {
  const s = server || null
  if (!s) return false
  return s.alias === SELF_NODE_ID || s.node === SELF_NODE_ID
}

/**
 * 该工具的缺省「涉及文件变动」。
 * @param {string} exposedName 工具暴露名（配置键）
 * @param {object|null} server `_meta.server`（含 alias/node/category）
 * @param {object|null|undefined} meta 工具 `_meta`（含契约 `touch_files` 声明）
 * @returns {boolean} true = 涉及（打点）/ false = 不涉及（不打点）
 */
export function defaultTouchFiles(exposedName, server, meta) {
  if (meta && typeof meta.touch_files === 'boolean') return meta.touch_files
  if (!isSelfTool(server)) return true
  return DEFAULT_TOUCH_TOOLS.includes(stripToolPrefix(exposedName, server))
}

/**
 * 生效值：显式 `touch_files` 优先，否则缺省。
 * @param {object|null|undefined} entry usr `tool_async[<暴露名>]` 项（可含 touch_files）
 */
export function effectiveTouchFiles(entry, exposedName, server, meta) {
  if (entry && typeof entry.touch_files === 'boolean') return entry.touch_files
  return defaultTouchFiles(exposedName, server, meta)
}

/** 打点（checkpoint）派生 = 生效值（true → 打点）。 */
export function checkpointEnabled(entry, exposedName, server, meta) {
  return effectiveTouchFiles(entry, exposedName, server, meta)
}

/**
 * 待落库的 `touch_files` 值：与缺省一致 → `undefined`（不写入，保持配置干净），
 * 否则 = 布尔值。供 `buildEntry` 组装 usr 键项。
 */
export function touchFilesOverride(value, exposedName, server, meta) {
  return value === defaultTouchFiles(exposedName, server, meta) ? undefined : value
}
/**
 * 索引 / codebase 状态 → 状态栏徽标模型（纯函数，便于单测）。
 *
 * 数据源 = prj 配置**既有键**（零新增消息面）：
 *   - codegraph：`enable-codegraph`（"true"/"false"）+ `codegraph.status`
 *   - vfts     ：`enable-vfts`      + `vfts.status`
 * 状态 JSON 文本的键（两引擎同源口径）：
 *   - 引擎 `Status`（`src/plugins/codegraph/server/index.go` / `src/plugins/vfts/server/workspace.go`）：
 *     `state`（"" 未初始化 | not_initialized | indexing | ready | error）· `progressDone` · `progressTotal`
 *     · `indexedFiles` · `indexedSymbols`（codegraph）· `chunkCount`（vfts）· `lastIndexedAt`（UnixNano）
 *     · `err`（omitempty）· `exts` · `skipDirs` · `loaded`
 *   - codegraph 插件额外回写：`phase`（configure|index）与错误态 `message`
 *     （`src/plugins/plugin-codegraph/codegraph.go` phaseStatusJSON / saveStatusErr）
 *
 * 展示口径（用户口径）：
 *   开关非 "true" 或无键 → 「未启用」；state=indexing（或已见进度/阶段）→ 「索引中」+「N/total」（total>0）
 *   ；state=ready → 「完成」；state=error → 「失败」；开关启用但状态未落定 → 「未初始化」。
 *   徽标 = [图标] + 状态文本（图标与文案由组件拼装）；tone 在组件内映射 variables.css 的 token。
 */

// 状态键 → 字色 tone（tone 在组件内映射 variables.css 的 token）
const TONE = {
  idx_disabled: 'disabled',
  idx_uninitialized: 'disabled',
  idx_indexing: 'progress',
  idx_ready: 'ready',
  idx_error: 'error',
}

/** 数值化（非有限数 → 0；避免 undefined/NaN 直接渲染）。 */
function num(v) {
  const n = Number(v)
  return Number.isFinite(n) ? n : 0
}

/** 解析插件的状态 JSON 文本；失败/缺键/非对象 → null（不抛错、不打日志）。 */
export function parseStatus(raw) {
  if (typeof raw !== 'string' || raw.trim() === '') return null
  try {
    const o = JSON.parse(raw)
    return o && typeof o === 'object' && !Array.isArray(o) ? o : null
  } catch (_) {
    return null
  }
}

/** 状态键（idx_*）判定：开关门控优先，其后按 state / 进度 / 阶段收敛。 */
function stateKey(enabled, s) {
  if (!enabled) return 'idx_disabled'
  const st = s && typeof s.state === 'string' ? s.state : ''
  if (st === 'ready') return 'idx_ready'
  if (st === 'error') return 'idx_error'
  if (st === 'indexing') return 'idx_indexing'
  // state 尚未落定为 indexing，但已可见进度/阶段 → 判定为正在索引（引擎在建索引的中途快照）
  if (s && (num(s.progressTotal) > 0 || s.phase)) return 'idx_indexing'
  return 'idx_uninitialized'
}

/**
 * 单个引擎徽标模型。
 * @param {object} p
 * @param {string|undefined} p.enabledRaw - 开关原始值（仅 "true" 视为启用）
 * @param {string|undefined} p.statusRaw  - 状态 JSON 文本
 * @param {(key: string) => string} p.t   - i18n 取值函数
 * @returns {{tone: string, text: string}}
 */
export function indexBadge({ enabledRaw, statusRaw, t }) {
  const enabled = enabledRaw === 'true'
  const s = parseStatus(statusRaw)
  const key = stateKey(enabled, s)
  const done = num(s && s.progressDone)
  const total = num(s && s.progressTotal)

  let text = t('statusBar.' + key)
  if (key === 'idx_indexing' && total > 0) text += ` ${done}/${total}`

  return { tone: TONE[key], text }
}

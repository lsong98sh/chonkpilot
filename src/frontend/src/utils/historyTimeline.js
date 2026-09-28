/**
 * historyTimeline — 文件历史（检查点）只读时间轴的纯逻辑（可直接单测）。
 *
 * 数据来源 = prj-config 内部键（后端契约，只读展示 + 一个写入口）：
 *   - `history.status.<slug>`   = JSON 文本 {"enabled","mode":"active|fused|off","repo","dirty",
 *                        "failCount","checkpointCount","bytes","lastCheckpointAt",
 *                        "lastDurationMs","lastError"}（slug = 根会话；**按会话**，I-135）
 *   - `history.timeline.<slug>` = JSON 文本，**最新在前**的数组
 *                        [{"n":-1,"id","ts","tool","session","files","added","removed"} …]（后端最多回 200 条）
 *   - `history.clear`    = 写入口（写 JSON `{"ts","session":"<根会话>"}` → 后端清**该会话**链；I-136）
 *
 * 兜底口径：解析失败 / 键缺失 / 类型不符 → 一律当作「未启用 / 无数据」，
 * **不抛错、不刷 console.error**（只读展示，异常静默降级）。
 */

// 保留策略默认值（与后端一致：history.checkpoint_keep 默认 500 / checkpoint_ttl_days 默认 7）。
export const DEFAULT_CHECKPOINT_KEEP = 500
export const DEFAULT_CHECKPOINT_TTL_DAYS = 7

// 会话级键前缀（与后端 `statusKeyPrefix` / `timelineKeyPrefix` 对齐；落 prjusr）。
export const HISTORY_STATUS_PREFIX = 'history.status.'
export const HISTORY_TIMELINE_PREFIX = 'history.timeline.'

/**
 * 根会话 → 链 slug（**与后端 `chainSlug` 同口径**：trim 后把非 `[A-Za-z0-9._-]` 字符折为 `_`；
 * 空白 / 折名后为空 / `.` / `..` → `'default'`）。会话 id 为 ASCII（uuid）→ 恒等映射。
 */
export function chainSlug(root) {
  const s = String(root ?? '').trim()
  if (s === '') return 'default'
  const folded = s.replace(/[^A-Za-z0-9._-]/g, '_')
  if (folded === '' || folded === '.' || folded === '..') return 'default'
  return folded
}

/** 该会话的状态键（history.status.<slug>）。 */
export function historyStatusKey(slug) {
  return HISTORY_STATUS_PREFIX + slug
}

/** 该会话的时间线键（history.timeline.<slug>）。 */
export function historyTimelineKey(slug) {
  return HISTORY_TIMELINE_PREFIX + slug
}

/** JSON 文本 → 对象；非法 / 缺失 / 非对象（数组也算非对象）→ null。 */
export function parseStatus(raw) {
  if (!raw || typeof raw !== 'string') return null
  try {
    const o = JSON.parse(raw)
    return o && typeof o === 'object' && !Array.isArray(o) ? o : null
  } catch (_) {
    return null
  }
}

/** JSON 文本 → 数组；非法 / 缺失 / 非数组 → []（过滤非对象项）。 */
export function parseTimeline(raw) {
  if (!raw || typeof raw !== 'string') return []
  try {
    const a = JSON.parse(raw)
    return Array.isArray(a) ? a.filter((x) => x && typeof x === 'object') : []
  } catch (_) {
    return []
  }
}

/**
 * 相对编号：最新一步 = -1（列表最新在前 → 索引 0 即 -1）。
 * 不依赖载荷 n 字段，按数组顺序归一，保证「-1 恒为最新」。
 */
export function relativeNumber(index) {
  return -(Number(index) + 1)
}

/** 状态模式归一：active / fused / off（缺失 / 非法 → off）。 */
export function statusMode(status) {
  const m = status && status.mode
  return m === 'active' || m === 'fused' ? m : 'off'
}

/** 是否已启用（enabled 为 true 且 mode ≠ off）。 */
export function statusEnabled(status) {
  return !!status && status.enabled === true && statusMode(status) !== 'off'
}

/** 人类可读体积（B / KB / MB / GB）；非法 / 非正 → "0 B"。 */
export function formatBytes(bytes) {
  const n = Number(bytes)
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  if (n < 1024) return `${Math.round(n)} B`
  const kb = n / 1024
  if (kb < 1024) return `${kb >= 10 ? Math.round(kb) : Math.round(kb * 10) / 10} KB`
  const mb = kb / 1024
  if (mb < 1024) return `${mb >= 10 ? Math.round(mb) : Math.round(mb * 10) / 10} MB`
  const gb = mb / 1024
  return `${gb >= 10 ? Math.round(gb) : Math.round(gb * 10) / 10} GB`
}

/** ISO 时间串 → 本地 `YYYY-MM-DD HH:mm:ss`；非法 / 缺失 → ''（原样返回无法解析的串）。 */
export function formatTime(iso) {
  if (!iso || typeof iso !== 'string') return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** 保留策略展示值：键缺失 / 空串 → 回落默认；其余原样（字符串数字）。 */
export function retentionValue(raw, def) {
  const s = String(raw ?? '').trim()
  return s === '' ? String(def) : s
}

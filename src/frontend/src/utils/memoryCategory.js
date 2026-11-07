/**
 * 记忆类别键（category）净化 · 前端体验层。
 *
 * 权威口径 = 后端 `capfs.ValidMemoryCategoryName`（A-37 单源）：
 *   - 非空、≤64 字符、无首尾空白、无空白/控制字符；
 *   - 禁路径分隔符与 Windows 保留字符 `/ \ : * ? " < > |`；
 *   - 首字符非 `.`；非 Windows 保留设备名（CON/PRN/AUX/NUL/COM1-9/LPT1-9）。
 *
 * 向导的类别显示名（`m.name`，en-US 下如 "Project overview"）含空格 → 直接当 category 会被
 * 后端拒绝。落库前统一经本函数转成安全键；UI 仍显示 `name`，键只用于 payload / 文件路径。
 */

const ILLEGAL_CHARS = /[/\\:*?"<>|]/g
// 空白 + C0/C1 控制字符（\s 无法覆盖全部控制字符）
const SPACE_OR_CONTROL = /[\s\u0000-\u001f\u007f-\u009f]/g
const RESERVED_DEVICE = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])$/i
const MAX_CATEGORY_RUNES = 64

/** 净化单个类别名（不做去重）：空白/控制字符 → `-`，移除非法字符，禁 `.` 开头。 */
function sanitizeCategoryName(name) {
  let s = String(name == null ? '' : name).trim()
  s = s.replace(SPACE_OR_CONTROL, '-')
  s = s.replace(ILLEGAL_CHARS, '')
  // 首字符非 '.'（避免 "." / ".." / 隐藏文件）。
  s = s.replace(/^\.+/, '')
  if (RESERVED_DEVICE.test(s)) s = 'cat-' + s
  // 后端上限 64 字符（按码点计）。
  if (Array.from(s).length > MAX_CATEGORY_RUNES) s = Array.from(s).slice(0, MAX_CATEGORY_RUNES).join('')
  return s
}

/** 自定义类别无可用名时的稳定回退：`custom-<id 后缀>`（id 形如 `custom-<timestamp>`）。 */
function fallbackCustomKey(m) {
  const id = String(m && m.id != null ? m.id : '').replace(/[^A-Za-z0-9_-]/g, '')
  const suffix = id.replace(/^custom-?/, '').slice(-24) || 'default'
  return 'custom-' + suffix
}

/**
 * 生成记忆类别键。
 *
 * @param {{id?: string, name?: string, preset?: boolean}} m 记忆类别（向导 memoryCats 项）
 * @param {Set<string>} [used] 已占用键集合；传入时保证返回键唯一（同名追加 `-2`、`-3`…）
 * @returns {string} 可安全落为 `<category>.md` 的类别键
 */
export function memoryCategoryKey(m, used) {
  const src = m || {}
  // 预设：用稳定 id（已是 kebab）；自定义：用显示名净化。
  const base = src.preset
    ? (sanitizeCategoryName(src.id) || 'preset')
    : (sanitizeCategoryName(src.name) || fallbackCustomKey(src))
  if (!(used instanceof Set)) return base
  if (!used.has(base)) {
    used.add(base)
    return base
  }
  let n = 2
  while (used.has(base + '-' + n)) n += 1
  const key = base + '-' + n
  used.add(key)
  return key
}
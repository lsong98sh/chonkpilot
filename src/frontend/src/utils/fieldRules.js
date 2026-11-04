/**
 * 字段校验规则（INPUTVAL-1 · 前端体验层）。
 *
 * 权威口径 = `docs/input-validation/输入校验专题.md` §3（v1 · 2026-10-21 已拍板）；
 * 后端同源实现 = `src/lib/data/internal/config/validate.go`。
 * 前端校验只用于防误操作，**后端是唯一可信边界**（超限由后端拒绝，不静默截断）。
 */

// 长度上限（Unicode 字符 = 码点，不是字节）。
export const MAX_NAME = 64 // 名称 / 服务名 / 场景名 / 目录名 / Agent 名
export const MAX_TITLE = 128 // 会话标题 / 原语标题 / 文件名 / 模型名
export const MAX_URL = 2048 // baseURL / MCP url
export const MAX_PATH = 512 // 路径 / cwd / 工具链路径 / 信任目录
export const MAX_KEY = 512 // apiKey
export const MAX_TEXT = 262144 // 提示词 / 正文 / 记忆内容（256 KB 字符）

// 数值范围。
export const TEMP_MIN = 0
export const TEMP_MAX = 2
export const OUT_TOKEN_MIN = 256
export const OUT_TOKEN_MAX = 1000000
export const CTX_TOKEN_MAX = 1000000000
export const TOOL_ITER_MAX = 2000
export const TIMEOUT_MAX = 600 // responseTimeout / streamTimeout（>0）
// LLM 能力档位（D-65）：双维度各 0~5，一位小数（步进 0.1）；0 = 不具备。
export const LLM_CAPABILITY_MIN = 0
export const LLM_CAPABILITY_MAX = 5
export const LLM_CAPABILITY_STEP = 0.1
// 能力双维度键（2026-11-04 用户口径）：reasoning = 推理能力 / vision = 识图能力。
export const LLM_CAPABILITY_DIMS = ['reasoning', 'vision']
// 旧声明式 `capabilities` 具备 → 档位映射值（旧口径无档位信息 → 中位档 3.0；与后端读侧同口径）。
export const LLM_CAPABILITY_LEGACY_DIM = 3

// 聊天输入 / 附件（已拍板）。
export const MAX_CHAT_CHARS = 16384
export const MAX_ATTACHMENT_BYTES = 20 * 1024 * 1024 // 单文件 ≤ 20 MB
export const MAX_ATTACHMENTS = 9 // 单条 < 10 个 → 最多 9 个

// charLen 按 Unicode 码点计数（与后端 utf8.RuneCountInString 同口径）。
export function charLen(v) {
  return Array.from(String(v == null ? '' : v)).length
}

export function trimStr(v) {
  return String(v == null ? '' : v).trim()
}

export function isBlank(v) {
  return trimStr(v) === ''
}

export function tooLong(v, max) {
  return charLen(v) > max
}

// HTTP(S) 地址（只判结构，不判可达性）。
export function isHttpUrl(v) {
  const s = trimStr(v)
  return /^https?:\/\//i.test(s) && s.length <= MAX_URL
}

// 绝对路径（Windows 盘符 / UNC / POSIX 根）。
export function isAbsolutePath(v) {
  const s = trimStr(v)
  if (!s) return false
  if (s.length > MAX_PATH) return false
  if (/^[a-zA-Z]:[\\/]/.test(s)) return true
  if (s.startsWith('\\\\')) return true
  return s.startsWith('/')
}

// MCP 服务名 / 标识符：字母开头，仅字母数字下划线。
export function isIdentifier(v) {
  return /^[A-Za-z][A-Za-z0-9_]*$/.test(trimStr(v))
}

// 场景目录名：字母 / 数字 / - / _。
export function isDirId(v) {
  return /^[A-Za-z0-9_-]+$/.test(trimStr(v))
}

// 控制字符（允许 \t \n \r；用于名称 / 标题类）。
export function hasControlChar(v) {
  return /[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/.test(String(v == null ? '' : v))
}

// 数值范围判定（nil / 空串 = 未填 → true，由「必填」另行判定）。
export function intInRange(v, min, max) {
  if (v === '' || v === null || v === undefined) return true
  const n = Number(v)
  return Number.isInteger(n) && n >= min && n <= max
}

// LLM 能力档位（D-65）：0~5 的一位小数（步进 0.1）；空 = 未填（未声明档位，允许）。
export function isValidCapability(v) {
  if (v === '' || v === null || v === undefined) return true
  const n = Number(v)
  if (!Number.isFinite(n) || n < LLM_CAPABILITY_MIN || n > LLM_CAPABILITY_MAX) return false
  return Math.abs(n * 10 - Math.round(n * 10)) < 1e-9
}

// 能力双维度归一：非数字 / 缺项 → 0，越界 → 夹到 0~5（读侧容错；写侧仍由 isValidCapability 把关）。
export function normalizeCapability(cap) {
  const pick = (v) => {
    const n = Number(v)
    if (!Number.isFinite(n)) return 0
    return Math.min(LLM_CAPABILITY_MAX, Math.max(LLM_CAPABILITY_MIN, n))
  }
  const src = cap && typeof cap === 'object' ? cap : {}
  return { reasoning: pick(src.reasoning), vision: pick(src.vision) }
}

// 由能力双维度**派生**旧声明式 `capabilities`（档位 > 0 即含该项）——与后端
// `DeriveLLMCapabilities` 同口径：ChatPanel 截图门控等旧读侧契约继续可用。
export function deriveCapabilities(cap) {
  const dims = normalizeCapability(cap)
  const out = []
  if (dims.reasoning > 0) out.push('reasoning')
  if (dims.vision > 0) out.push('vision')
  return out
}

// 旧声明式 `capabilities` → 能力双维度（具备 = 中位档 3.0；与后端读侧 `capabilityLegacyDim` 同口径）。
// 传入 null（记录完全未声明能力）→ 推理按中位档 3.0、识图 0（与新增默认一致，避免存量记录在
// auto 模式下被判「不具备」而失去候选资格）；显式空数组 → 两项均 0。
export function capabilityFromLegacyCaps(caps) {
  if (!Array.isArray(caps)) {
    return { reasoning: LLM_CAPABILITY_LEGACY_DIM, vision: 0 }
  }
  return {
    reasoning: caps.includes('reasoning') ? LLM_CAPABILITY_LEGACY_DIM : 0,
    vision: caps.includes('vision') ? LLM_CAPABILITY_LEGACY_DIM : 0,
  }
}

// 附件：单文件大小 + 单条数量（前端提示与拦截；后端另有上限复核）。
export function attachmentTooLarge(size) {
  return Number(size) > MAX_ATTACHMENT_BYTES
}

export function attachmentCountExceeded(count) {
  return Number(count) > MAX_ATTACHMENTS
}

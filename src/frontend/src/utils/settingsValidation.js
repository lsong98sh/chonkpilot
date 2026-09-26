/**
 * settingsValidation — 设置面「数值输入前置校验」纯函数（可直接单测）。
 *
 * 背景（用户视角缺陷批 2 · 设置面 ③）：项目级 `timeout_sec` / `max_concurrency` 原为无校验
 * 文本框；后端读取口径（chonkpilot-llm/server/server.go loadExecConfig）为：
 *
 *   if v := prjConfigValue(...,"timeout_sec"); v != "" {
 *       if n, err := strconv.Atoi(v); err == nil && n > 0 { timeoutSec = n }
 *   }
 *   // max_concurrency 同口径
 *
 * 即：**只有 `strconv.Atoi` 可解析且 > 0 的整数才生效**；非数字 / 非正整数一律静默回落默认
 * （300 / 16）→ 用户"保存成功但无效"。故前端按**同一语义**前置校验：必须为整数且 > 0。
 * 后端对上限无约束 → 前端不设上限（避免臆造约束）。
 */

/**
 * 校验「正整数」（对齐后端 Atoi + n > 0 的口径，允许前后空白与显式正负号）。
 * @returns {{ ok: true, value: number } | { ok: false, reason: 'empty'|'notInteger'|'notPositive' }}
 */
export function validatePositiveInt(raw) {
  const s = String(raw ?? '').trim()
  if (s === '') return { ok: false, reason: 'empty' }
  if (!/^[+-]?\d+$/.test(s)) return { ok: false, reason: 'notInteger' }
  const n = Number(s)
  if (!Number.isSafeInteger(n) || n <= 0) return { ok: false, reason: 'notPositive' }
  return { ok: true, value: n }
}

/** 校验失败 → 用户可见文案。t = i18n 的 t；reason 见上。 */
export function positiveIntErrorText(t, reason) {
  return reason === 'empty'
    ? t('config.feedback.requiredInt')
    : t('config.feedback.invalidPositiveInt')
}

/**
 * 校验「非负整数」——usr `retryCount` 专用口径（批 2 收尾 · C）。
 * 后端 chonkpilot-llm/server/server.go loadLLMRuntimeConfig：
 *   `if n, ok := configInt(d, "retryCount"); ok && n >= 0 { retryCount = n }`
 * 即 **显式 0 合法**（= 不重试，P0-A 存在性判定）；负数 / 非整数 / 键缺失 → 回落默认 2。
 * 前端按**同一语义**前置校验：整数且 >= 0；后端对上限无约束 → 前端不设上限。
 * @returns {{ ok: true, value: number } | { ok: false, reason: 'empty'|'notInteger'|'negative' }}
 */
export function validateNonNegativeInt(raw) {
  const s = String(raw ?? '').trim()
  if (s === '') return { ok: false, reason: 'empty' }
  if (!/^[+-]?\d+$/.test(s)) return { ok: false, reason: 'notInteger' }
  const n = Number(s)
  if (!Number.isSafeInteger(n) || n < 0) return { ok: false, reason: 'negative' }
  return { ok: true, value: n }
}

/** 非负整数校验失败 → 用户可见文案（0 合法，故与「正整数」文案区分）。 */
export function nonNegativeIntErrorText(t, reason) {
  return reason === 'negative'
    ? t('config.feedback.invalidNonNegativeInt')
    : t('config.feedback.requiredInt')
}

/**
 * settingsFeedback — 设置面「保存/加载反馈」统一口径（纯函数，可直接单测）。
 *
 * 背景（用户视角缺陷批 2 · 设置面）：
 *   - ① 路径键「需重启生效」无提示：usr 路径键保存后需重启应用才生效
 *     （后端仅 prj 执行配置走 onPrjConfigRefresh → loadExecConfig 热生效，usr 路径键不重跑）；
 *     对照口径 = 既有「即时生效」提示（projectConfig.log_saved / log_level_hint）。
 *   - ④ 静默失败：设置页加载/保存失败仅 console.*，用户不可见。
 *   - ⑤ 反馈不统一：同为「已保存」，各页文案与生效范围标注不一。
 *
 * 口径：
 *   - APPLY_INSTANT = 保存即生效（无需重启）；APPLY_RESTART = 需重启应用后生效。
 *   - 失败文案须**可行动**：带失败原因摘要（err.message / err.code），无原因则给兜底。
 * 说明：本模块只做「文案选择」，不改任何保存时机。
 */

// 生效范围常量（供页面显式标注，避免各处硬编码字符串）
export const APPLY_INSTANT = 'instant'
export const APPLY_RESTART = 'restart'

/** 保存成功文案键：按生效范围选择。 */
export function savedKey(apply) {
  if (apply === APPLY_RESTART) return 'config.feedback.savedRestart'
  if (apply === APPLY_INSTANT) return 'config.feedback.savedInstant'
  return 'config.feedback.saved'
}

/** 保存成功文案（含生效范围标注）。 */
export function savedText(t, apply) {
  return t(savedKey(apply))
}

/** 从错误对象/字符串提取可读原因摘要（无则空串）。 */
export function errorReason(err) {
  if (!err) return ''
  if (typeof err === 'string') return err
  const msg = err.message || err.code || ''
  return msg ? String(msg) : ''
}

/** 保存失败文案（可行动：含原因摘要）。 */
export function saveFailedText(t, err) {
  const reason = errorReason(err)
  return reason
    ? t('config.feedback.saveFailed', { error: reason })
    : t('config.feedback.saveFailedUnknown')
}

/** 加载失败文案（可行动：指明是哪一项 + 原因摘要）。item = 已翻译的项目名。 */
export function loadFailedText(t, item, err) {
  const reason = errorReason(err)
  return reason
    ? t('config.feedback.loadFailed', { item, error: reason })
    : t('config.feedback.loadFailedUnknown', { item })
}

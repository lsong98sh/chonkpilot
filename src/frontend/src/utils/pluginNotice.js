/**
 * 插件失败提示的 i18n 映射（[41 I-117]，2026-09-20）。
 *
 * 宿主（`chonkpilot-llm/server/pluginnotice.go`）经**既有通知面** `tool-notify` 投递
 * `notice=plugin-failure`，payload 携带**结构化字段** `plugin` / `kind` / `reason`
 * （`message` = 宿主生成的中文**兜底**文案，payload 只增不改，见 61 §4.3）。
 * 前端据此按 key 映射本地化文案（`pluginFailure.*`，zh-CN + en-US 双语齐备）；
 * **映射缺失**（未登记插件 / 未登记类别）→ 回落宿主 `message` 原文（不伪造文案）。
 */
const NS = 'pluginFailure'

/**
 * 取插件失败提示文案。
 * @param {object} data - `tool-notify` 载荷（plugin / kind / reason / message / text）
 * @param {(key: string, named?: object) => string} t - i18n 翻译函数
 * @param {(key: string) => boolean} te - i18n 键存在判定（vue-i18n 的 te）
 * @returns {string} 本地化文案；未登记插件 → 宿主原文
 */
export function pluginFailureText(data, t, te) {
  const d = data || {}
  const fallback = d.message || d.text || ''
  const plugin = String(d.plugin || '').trim()
  // 插件未登记（无 i18n 短语）→ 回落宿主原文（Go 侧中文 message）
  if (!plugin || !te(NS + '.what.' + plugin)) return fallback
  // 类别未登记 / 缺失 → 省略阶段片段（不显示原始键名）
  const kind = String(d.kind || '').trim()
  const phase = kind && te(NS + '.kind.' + kind) ? t(NS + '.phase', { phase: t(NS + '.kind.' + kind) }) : ''
  const reason = String(d.reason || '').trim() || t(NS + '.reason_unknown')
  return t(NS + '.message', { what: t(NS + '.what.' + plugin), phase, reason })
}

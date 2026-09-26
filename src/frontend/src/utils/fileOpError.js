/**
 * 文件操作失败原因文案（用户视角缺陷批 1 – 问题 C）。
 *
 * 文件树删除/移动原先失败只 `console.error`（用户看不到任何反馈，界面也不回滚呈现"已成功"）。
 * 本模块只做「异常 → 可展示原因」的翻译，独立于 mq/vue，便于单测。
 * - 用户取消：MessageBox.confirm 以字符串 `'cancel'` reject（见 components/ui/MessageBox.js）
 *   → 返回空串，调用方不提示；
 * - filesys 错误对象：优先 message，其次 code（如 'exists' 同名冲突）；
 * - 其它：fallback（调用方传 i18n 的「未知错误」）。
 */

/**
 * @param {any} e - 捕获的异常
 * @param {string} [fallback] - 无可用原因时的兜底文案
 * @returns {string} 可展示的原因（空串 = 不提示）
 */
export function fileOpErrorText(e, fallback = '') {
  if (e === 'cancel') return ''
  if (typeof e === 'string') return e
  const reason = (e && (e.message || e.code)) || ''
  return reason || fallback
}

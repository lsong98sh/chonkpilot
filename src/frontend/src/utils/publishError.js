/**
 * publishError — httpapi `/publish` 稳定错误码 → i18n 文案（D-41 optimizeError 同口径）。
 *
 * 后端（src/lib/llm/httpapi/publish.go）在错误信封的**顶层**（413 超限）或 `result` 内
 * 附稳定 `code`；本模块把 `publishError.<code>` 交给**已注册的 translator** 取本地化文案。
 *
 * 依赖反转（重要）：本模块位于 utils 层，被 node 单测直接 import，**不得**静态引入
 * vue-i18n / locale JSON（node ESM 无法解析）。translator 由 `plugins/i18n` 在浏览器
 * 启动时注册（`setPublishErrorTranslator`）；未注册（node 测试）→ 返回 ''，调用方回落后端
 * 原文，行为与既有完全一致。
 */
let translator = null

/**
 * 注册 translator（key → 文案；未命中返回 ''）。由 `plugins/i18n` 启动时调用一次。
 * @param {(key: string) => string} fn
 */
export function setPublishErrorTranslator(fn) {
  translator = typeof fn === 'function' ? fn : null
}

/**
 * @param {object} backend 后端结果信封 { ok, result, errors, code? }
 * @returns {string} 本地化文案；无可用 code / 未注册 translator 时返回 ''
 */
export function resolvePublishError(backend) {
  const b = backend || {}
  const p = b.result && typeof b.result === 'object' ? b.result : {}
  const code = b.code || p.code
  if (!code || !translator) return ''
  try {
    return translator('publishError.' + code) || ''
  } catch (_) {
    return ''
  }
}

/**
 * 视图分派（URL 唯一判据）—— 24-多窗口模型设计方案 §6.1 · U-2 已决。
 *
 * 对话窗口由 URL 承载视图与会话绑定，形如：
 *   <appOrigin>/?session-id=<sid>#chat
 *
 * 判据（**不注入 `window.__ck.view`**，宿主零视图判断）：
 *   - `#chat` **且** 带 `session-id` → `ChatOnlyView`（仅 chat）；
 *   - 其余 → `MainLayout`（现状）。
 *
 * 服务端无需为 URL 做任何事：`serveStatic` 只读 `r.URL.Path`（query 不进 Path），hash 不发服务端。
 *
 * 本模块为**叶子模块**（无 import、无副作用）→ 可直跑单测，也可被 App.vue 安全引用。
 */

/** 视图标识：`chat` = 纯对话窗口；`main` = 主窗口（完整布局）。 */
export const VIEW_CHAT = 'chat'
export const VIEW_MAIN = 'main'

/** 对话窗口的 URL hash 标记（24 §1.1）。 */
export const CHAT_HASH = '#chat'

/**
 * 解析 URL（search + hash）→ 视图分派结果。
 * @param {string} search `location.search`（含前导 `?`，可缺省）
 * @param {string} hash `location.hash`（含前导 `#`，可缺省）
 * @returns {{view: 'chat'|'main', sessionId: string}} sessionId 仅在 view=chat 时非空
 */
export function parseViewRoute(search = '', hash = '') {
  const params = new URLSearchParams(String(search || ''))
  const sessionId = params.get('session-id') || ''
  const isChat = String(hash || '') === CHAT_HASH && sessionId !== ''
  return { view: isChat ? VIEW_CHAT : VIEW_MAIN, sessionId: isChat ? sessionId : '' }
}

/**
 * 视图分派（URL 唯一判据）—— 24-多窗口模型设计方案 §6.1 · U-2 已决。
 *
 * 窗口由 URL 承载视图与会话绑定，形如：
 *   <appOrigin>/?session-id=<sid>#chat     对话窗口
 *   <appOrigin>/?work-dir=<wd>#ask         Ask 中心（独立待办窗口，2026-11-04）
 *
 * 可选 `dialog-id=<did>`（G-105，2026-11-03）：宿主带 dialog_id 直开时由 URL 透传，
 * chat 窗口据此直连该 dialog（免 session→dialog 反查自举空库）。
 * 可选 `draft=<text>`（Ask 中心「聊一聊」）：chat 窗口挂载后把文本注入输入框（不发送）。
 *
 * 判据（**不注入 `window.__ck.view`**，宿主零视图判断）：
 *   - `#ask` → `AskCenterView`（Ask 中心，无会话绑定）；
 *   - `#chat` **且** 带 `session-id` → `ChatOnlyView`（仅 chat）；
 *   - 其余 → `MainLayout`（现状）。
 *
 * 服务端无需为 URL 做任何事：`serveStatic` 只读 `r.URL.Path`（query 不进 Path），hash 不发服务端。
 *
 * 本模块为**叶子模块**（无 import、无副作用）→ 可直跑单测，也可被 App.vue 安全引用。
 */

/** 视图标识：`chat` = 纯对话窗口；`ask` = Ask 中心；`log` = 日志窗口；`main` = 主窗口。 */
export const VIEW_CHAT = 'chat'
export const VIEW_ASK = 'ask'
export const VIEW_LOG = 'log'
export const VIEW_MAIN = 'main'

/** 对话窗口的 URL hash 标记（24 §1.1）。 */
export const CHAT_HASH = '#chat'

/** Ask 中心窗口的 URL hash 标记（独立待办窗口，2026-11-04）。 */
export const ASK_HASH = '#ask'

/** 日志窗口的 URL hash 标记（PRODSIM-1：宿主以 `<appOrigin>/#log` 打开，默认隐藏）。 */
export const LOG_HASH = '#log'

/**
 * 解析 URL（search + hash）→ 视图分派结果。
 * @param {string} search `location.search`（含前导 `?`，可缺省）
 * @param {string} hash `location.hash`（含前导 `#`，可缺省）
 * @returns {{view: 'chat'|'ask'|'log'|'main', sessionId: string, dialogId: string, draft?: string}}
 *   sessionId/dialogId 仅在 view=chat 时非空；draft 仅在 chat 且 URL 带 `draft` 时出现（可选返回键，
 *   不改变既有调用方的 deep-equal 期望）。
 */
export function parseViewRoute(search = '', hash = '') {
  const h = String(hash || '')
  // 日志窗口优先判定（无 session、不参与项目视图）。
  if (h === LOG_HASH) return { view: VIEW_LOG, sessionId: '', dialogId: '' }
  const params = new URLSearchParams(String(search || ''))
  if (h === ASK_HASH) return { view: VIEW_ASK, sessionId: '', dialogId: '' }
  const sessionId = params.get('session-id') || ''
  const isChat = h === CHAT_HASH && sessionId !== ''
  const dialogId = isChat ? (params.get('dialog-id') || '') : ''
  const route = { view: isChat ? VIEW_CHAT : VIEW_MAIN, sessionId: isChat ? sessionId : '', dialogId }
  if (isChat) {
    const draft = params.get('draft') || ''
    if (draft) route.draft = draft
  }
  return route
}
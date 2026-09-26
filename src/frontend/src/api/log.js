/**
 * 前端日志工具（本地 console 钩子）。
 * 后端 LogWeb 未注册 → 不上报，仅保留本地 console 输出兜底（全局钩子引用保留）。
 */

/**
 * Log a message locally（web 端 console）。
 * @param {'info'|'warn'|'error'|'debug'} level - Log level
 * @param {string} message - Log message (include stack trace for errors)
 * @returns {Promise<void>}
 */
export async function logWeb(level, message) {
  if (level === 'error') console.error('[web-log]', message)
  else if (level === 'warn') console.warn('[web-log]', message)
}

/**
 * Convenience: log an Error object (includes stack trace).
 */
export function logError(context, error) {
  const msg = `[${context}] ${error?.message || error}`
  const stack = error?.stack || ''
  console.error(msg + '\n' + stack, error)
}

export default { logWeb, logError }

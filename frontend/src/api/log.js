/**
 * Frontend-to-backend logging utility.
 * Sends log messages to Go's LogWeb, which writes to .ide/log/web.log.
 */

/**
 * Log a message to .ide/log/web.log via the Go backend.
 * @param {'info'|'warn'|'error'|'debug'} level - Log level
 * @param {string} message - Log message (include stack trace for errors)
 * @returns {Promise<void>}
 */
export async function logWeb(level, message) {
  try {
    if (window.runtime && window.runtime.Call) {
      await window.runtime.Call('LogWeb', level, message)
    }
  } catch (e) {
    // Silently fail — logging should never break the app
    console.warn('[logWeb] Failed to send log:', e.message || e)
  }
}

/**
 * Convenience: log an Error object (includes stack trace).
 */
export function logError(context, error) {
  const msg = `[${context}] ${error?.message || error}`
  const stack = error?.stack || ''
  logWeb('error', msg + '\n' + stack)
  console.error(msg, error)
}

export default { logWeb, logError }

/**
 * useAuth —— 登录 / 注册 / 登出（61-消息一览 §4.6 认证域；阶段 2b-2）。
 *
 * 契约（61 §4.6，**payload 零新增字段**；前端**不接触令牌** —— 由入口承载）：
 *   - 登录 `login-in {username, password}` → `{ok, user}`（令牌经入口 Set-Cookie / 桥持有，
 *     **不进前端 payload**）；
 *   - 注册 `login-register {username, password}` → `{ok, user}`（注册成功即自动登录）；
 *   - 登出 `login-out {}` → `{ok}`；
 *   - 错误码：`login-failed`（用户名或密码错）/ `login-username-taken`（注册重名）。
 *
 * 首个视图分派（61 §4.6：`authed` 由**服务端**读凭证判定 → 前端**零额外往返**）：
 *   `signedIn` 初值 = `!requireAuth() || authed()`（首屏注入 `window.__ck` 读数，
 *   见 utils/instanceState.js）。desktop（requireAuth=false）恒已就绪 → 直接进主页；
 *   gui / browser 未认证 → 登录视图。
 *
 * 模块级单例（App 分派视图 + Toolbar 登出入口 + 登录视图共用同一份状态）。
 */
import { ref } from 'vue'
import mq from '../utils/mq.js'
import { authed as injectedAuthed, requireAuth as injectedRequireAuth } from '../utils/instanceState.js'

/** 登录失败（用户名或密码错；61 §4.6 `login-failed`）。 */
export const LOGIN_FAILED = 'login-failed'
/** 注册重名（61 §4.6 `login-username-taken`）。 */
export const LOGIN_USERNAME_TAKEN = 'login-username-taken'

const signedIn = ref(!injectedRequireAuth() || injectedAuthed())
const user = ref(null)
const busy = ref(false)
const errorCode = ref('')

/** 从 `/publish` 应答信封取错误码（result.error 优先，其次 errors[0]；无 → ''）。 */
function backendError(res) {
  const backend = res && res.backend
  const result = backend && backend.result
  const errs = (backend && backend.errors) || []
  return String((result && result.error) || (errs.length ? errs[0] : '') || '')
}

/** 取应答 result（非对象 → null）。 */
function backendResult(res) {
  const result = res && res.backend && res.backend.result
  return result && typeof result === 'object' ? result : null
}

/**
 * 发一条 login-*（请求-响应）并收敛状态。
 * @param {'login-in'|'login-register'} topic
 * @returns {Promise<boolean>} 是否成功
 */
async function callLogin(topic, username, password) {
  busy.value = true
  errorCode.value = ''
  try {
    const res = await mq.emit(topic, { username, password })
    const code = backendError(res)
    if (code) {
      errorCode.value = code
      return false
    }
    const result = backendResult(res)
    if (!result || result.ok !== true) {
      // 无应答 / 形态异常（如旧后端未接线）：按登录失败提示（**不静默**）。
      errorCode.value = LOGIN_FAILED
      return false
    }
    user.value = result.user || null
    signedIn.value = true
    return true
  } finally {
    busy.value = false
  }
}

/** 登录（`login-in`）。 */
export function login(username, password) {
  return callLogin('login-in', username, password)
}

/** 注册（`login-register`；注册成功即自动登录）。 */
export function register(username, password) {
  return callLogin('login-register', username, password)
}

/**
 * 登出（`login-out {}`）：清令牌（服务端删行 + 入口清 cookie / 删文件）→ 复位前端状态。
 * 注：**不**在此处理实例状态（认领复位由 App 的认领实例统一做，见 resetClaim）。
 */
export async function signOut() {
  busy.value = true
  try {
    await mq.emit('login-out', {})
  } finally {
    busy.value = false
    user.value = null
    // 登出后是否仍算「已认证」由**形态**决定：desktop 免鉴权（无需登录视图）；
    // 认证形态（gui/browser）→ 回登录视图。
    signedIn.value = !injectedRequireAuth()
    errorCode.value = ''
  }
}

/**
 * 回登录视图（用于 `instance-unauthorized`：令牌失效 / 未登录 —— 61 §4.6 明确「跳登录」）。
 * 注：`instance-forbidden` **不得**调用本函数（61 §4.6：提示换目录，**不跳登录**）。
 */
export function markSignedOut() {
  signedIn.value = !injectedRequireAuth()
  user.value = null
}

/** 当前登录用户（未登录 / desktop → null）。 */
export function currentUser() {
  return user.value
}

/** 认证状态读数 + 动作（模块级单例）。 */
export function useAuth() {
  return { signedIn, user, busy, errorCode, login, register, signOut, markSignedOut }
}

/**
 * useInstanceClaim —— 实例认领 + 心跳续期（61 §4.1 ①②；阶段 2a 前端骨架，2b-2 接入登录/选目录）。
 *
 * 流程（61 §4.6「流程」）：`登录 → 选目录 → instance-claim{user?, work_dir?} → 校验 →
 * claim 成功后服务端广播 instance-register → 前端起 heartbeat`。
 *   - **desktop**（`requireAuth=false`）：无登录/选目录 → App 启动即认领（不带参数；
 *     work_dir 取服务端启动参数）；
 *   - **gui / browser**（`requireAuth=true`）：由 `views/auth/AuthView.vue` 在登录成功后
 *     提交 `{user, work_dir}` 认领（本模块只负责发送与状态收敛）。
 *
 * 契约（61 §4.1 表；**payload 零新增字段**）：
 *   - 请求 `instance-claim {user?, work_dir?}` —— 请求-响应：应答经 publish promise 回发起者；
 *   - 成功 `{instance_id, work_dir, data_dir}` → 存本地（`utils/instanceState.js`，业务 payload
 *     注入据它取值）+ 分离形态起 30s 心跳；
 *   - 失败 `{ok:false, error}`（61 §4.6）：`instance-unauthorized` → **回登录视图**；
 *     `instance-forbidden` → 提示无权访问该目录（**不跳登录**）；
 *   - 心跳 `instance-heartbeat {instance_id}`（30s；payload 由 mq 统一注入 instance_id）。
 *
 * 心跳门控（**保留既有 `-tags split` 语义**，61 §4.1 ②）：**仅分离形态**（首屏注入
 * `__ck.form` = gui / browser）发布；desktop（桌面单体默认构建）不发布、服务端亦不判超时。
 *
 * 模块级单例状态（App 启动/登录后认领一次；其它消费者可读同一份）。
 */
import { ref } from 'vue'
import mq from '../utils/mq.js'
import { MsgTopics } from '../events/msgkeys.js'
import { isSplitForm, setClaimedInstance } from '../utils/instanceState.js'

/** 心跳周期 30s（61 §4.1 ②；服务端 90s 未收即回收 —— 取值单一来源 = chonkpilot-lib/heartbeat）。 */
export const HEARTBEAT_INTERVAL_MS = 30000

/** 认领状态。 */
export const CLAIM_IDLE = 'idle' // 未开始
export const CLAIM_INFLIGHT = 'claiming' // 请求中
export const CLAIM_OK = 'claimed' // 认领成功（可用）
export const CLAIM_UNANSWERED = 'unanswered' // 无应答（未接线 / 无服务端）→ 降级放行（仅告警）
export const CLAIM_UNAUTHORIZED = 'unauthorized' // 未登录 / 令牌失效（2a 占位提示；2b 接登录视图）
export const CLAIM_FORBIDDEN = 'forbidden' // 无权访问该 work_dir（**不跳登录**）
export const CLAIM_FAILED = 'failed' // 其他错误码（原样带出）

const state = ref(CLAIM_IDLE)
const instance = ref(null)
const errorCode = ref('')

let heartbeatTimer = null
let inflight = null

/** 停止心跳定时器（幂等）。 */
export function stopHeartbeat() {
  if (heartbeatTimer === null) return
  clearInterval(heartbeatTimer)
  heartbeatTimer = null
}

/**
 * 起 30s 心跳（仅分离形态；重复调用幂等 —— 先停后起）。
 * payload = {} → mq 统一注入 instance_id → 线上载荷即 `{instance_id}`（61 §4.1 ②）。
 */
export function startHeartbeat() {
  stopHeartbeat()
  if (!isSplitForm()) return // desktop（桌面单体默认构建）：不发布心跳（-tags split 门控同口径）
  heartbeatTimer = setInterval(() => {
    mq.emit(MsgTopics.instanceHeartbeat, {})
  }, HEARTBEAT_INTERVAL_MS)
}

/** 认领失败分派（61 §4.6 错误码 → 显式状态；未知码原样带出，不静默）。 */
function applyFailure(code) {
  errorCode.value = code
  if (code === 'instance-unauthorized') {
    state.value = CLAIM_UNAUTHORIZED
  } else if (code === 'instance-forbidden') {
    state.value = CLAIM_FORBIDDEN
  } else {
    state.value = CLAIM_FAILED
  }
  return false
}

/**
 * 复位认领（登出时调用；阶段 2b-2）：停心跳 + 清已认领实例 + 回 IDLE。
 * 登出后必须复位 —— 否则旧实例上下文会被后续业务 payload 继续携带（服务端已回收该实例 →
 * 前端应重新走「登录 → 选目录 → claim」）。
 */
export function resetClaim() {
  stopHeartbeat()
  setClaimedInstance(null)
  instance.value = null
  errorCode.value = ''
  state.value = CLAIM_IDLE
  inflight = null
}

/**
 * 发起一次 instance-claim（并发调用共享同一次在飞请求）。
 * @param {object} payload `{user?, work_dir?}`（均为提议；2a 前端不带 —— 认证/选目录属 2b）
 * @returns {Promise<boolean>} 是否认领成功
 */
export function claim(payload = {}) {
  if (inflight) return inflight
  inflight = (async () => {
    state.value = CLAIM_INFLIGHT
    errorCode.value = ''
    try {
      const res = await mq.emit(MsgTopics.instanceClaim, payload)
      const backend = res && res.backend
      const result = backend && backend.result
      // 失败（61 §4.6：复用 {ok,error} 信封）：错误码优先取 result.error，其次取 errors[0]
      // （桥/httpapi 的 /publish 响应把服务端 v.Errors 收集在 errors）。
      const errs = (backend && backend.errors) || []
      const code = (result && result.error) || (errs.length ? errs[0] : '')
      if (code) return applyFailure(String(code))
      if (!result || !result.instance_id) {
        // 无应答（如 `--no-server` 的 UI 注入模式 / 后端未接线）：**不静默** —— 告警后降级放行，
        // 不阻塞进入主视图（认领未生效，payload 仍用首屏注入的 instance_id）。
        console.warn('[instance-claim] 无应答（未接入该消息面），降级进入主视图', res)
        state.value = CLAIM_UNANSWERED
        return false
      }
      instance.value = setClaimedInstance(result)
      state.value = CLAIM_OK
      startHeartbeat() // 分离形态：claim 成功后起 30s 续期
      return true
    } finally {
      inflight = null
    }
  })()
  return inflight
}

/** 认领状态读取（模块级单例；App 与其它消费者共享）。 */
export function useInstanceClaim() {
  return { state, instance, errorCode, claim, startHeartbeat, stopHeartbeat, resetClaim }
}

/**
 * useInstanceClaim —— 实例认领（61 §4.1 ①；PRODSIM-1 后唯一形态 desktop）。
 *
 * 流程：`项目主窗口启动 → instance-claim{work_dir}（客户端提议）→ 命中实例登记即绑定 →
 * 服务端广播 instance-register`。无登录、无心跳（`isSplitForm()` 恒 false → 不发布）。
 *
 * 契约（61 §4.1 表；**payload 零新增字段**）：
 *   - 请求 `instance-claim {user?, work_dir?}` —— 请求-响应：应答经 publish promise 回发起者；
 *   - 成功 `{instance_id, work_dir, data_dir}` → 存本地（`utils/instanceState.js`，业务 payload
 *     注入据它取值）；
 *   - 失败 `{ok:false, error}`：`instance-forbidden` = 该 work_dir 未被实例登记；
 *   - 心跳 `instance-heartbeat {instance_id}`：主题保留，唯一形态 desktop **不发布**。
 *
 * 模块级单例状态（App 启动后认领一次；其它消费者可读同一份）。
 */
import { ref } from 'vue'
import mq from '@ckfw/utils/mq.js'
import { MsgTopics } from '../events/msgkeys.js'
import { isSplitForm, setClaimedInstance } from '@ckfw/utils/instanceState.js'

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
 * 起心跳（仅分离形态；重复调用幂等 —— 先停后起）。周期 30s。
 * payload = {} → mq 统一注入 instance_id → 线上载荷即 `{instance_id}`（61 §4.1 ②）。
 */
export function startHeartbeat() {
  stopHeartbeat()
  if (!isSplitForm()) return // desktop（桌面单体 / flash 薄壳）：不发布心跳（-tags split 门控同口径）
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

/** 认领在「后端尚未就绪」时的重试（2026-11-02）：首帧请求常早于 persist/实例登记就绪
 *  → 此前直接降级放行，随后所有 data-* 调用再各自等满 15s 超时（表现为「会话初始化失败」
 *  + 界面卡顿）。这里在**无应答**时做有限次重试，成功即继续，避免整串请求连锁超时。 */
export const CLAIM_RETRY_LIMIT = 5
export const CLAIM_RETRY_DELAY_MS = 500
/** 单次认领超时：用短超时快失败快重试（默认 15s 会让「后端未就绪」白等整轮）。 */
export const CLAIM_ATTEMPT_TIMEOUT_MS = 5000

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
      for (let attempt = 0; attempt <= CLAIM_RETRY_LIMIT; attempt++) {
        const outcome = await claimOnce(payload)
        if (outcome !== CLAIM_UNANSWERED) return outcome === CLAIM_OK
        // 无应答 = 后端还没起来：等一拍再试（最后一次不再等待，直接降级放行）
        if (attempt < CLAIM_RETRY_LIMIT) {
          await new Promise((r) => setTimeout(r, CLAIM_RETRY_DELAY_MS))
          state.value = CLAIM_INFLIGHT
        }
      }
      console.warn('[instance-claim] 后端持续无应答 → 降级进入主视图（认领未生效，payload 仍用首屏注入的 instance_id）')
      state.value = CLAIM_UNANSWERED
      return false
    } finally {
      inflight = null
    }
  })()
  return inflight
}

/** 单次认领：返回 `CLAIM_OK`（成功）/ `CLAIM_UNANSWERED`（无应答）/ 失败状态（已落 errorCode）。 */
async function claimOnce(payload) {
  const res = await mq.emit(MsgTopics.instanceClaim, payload, { timeout: CLAIM_ATTEMPT_TIMEOUT_MS })
  const backend = res && res.backend
  const result = backend && backend.result
  // 失败（61 §4.6：复用 {ok,error} 信封）：错误码优先取 result.error，其次取 errors[0]
  // （桥/httpapi 的 /publish 响应把服务端 v.Errors 收集在 errors）。
  const errs = (backend && backend.errors) || []
  const code = (result && result.error) || (errs.length ? errs[0] : '')
  if (code) {
    applyFailure(String(code))
    return state.value
  }
  if (!result || !result.instance_id) {
    // 无应答（如 `--no-server` 的 UI 注入模式 / 后端未接线 / 启动期尚未就绪）：由调用方重试
    return CLAIM_UNANSWERED
  }
  instance.value = setClaimedInstance(result)
  state.value = CLAIM_OK
  startHeartbeat() // 分离形态：claim 成功后起 30s 续期
  return CLAIM_OK
}

/** 认领状态读取（模块级单例；App 与其它消费者共享）。 */
export function useInstanceClaim() {
  return { state, instance, errorCode, claim, startHeartbeat, stopHeartbeat, resetClaim }
}

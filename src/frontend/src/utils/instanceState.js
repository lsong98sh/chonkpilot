/**
 * 运行形态 / 认证标记 / 已认领实例 —— 前端零身份逻辑的唯一读数口（61 §4.6 首屏注入 +
 * §4.1 instance-claim；阶段 2a 建立，2b-2 接线认证）。
 *
 * **首屏注入**（服务端/宿主侧判定，`injectBootstrap` / GUI serveStatic；**不使用 UA 判形态**
 * —— UA 可伪造，仅作日志诊断）：`window.__ck = {form, requireAuth, authed, instanceId}`：
 *   - `form` ∈ `desktop` | `gui` | `browser`（三形态；`desktop` = 桌面单体、`gui` = GUI 客户端
 *     + 独立 server）。
 *     缺失（旧构建）→ 回落：`window.__ckHttpBridge`（浏览器 shim 标记，见 utils/runtimeForm.js）
 *     存在 = browser，否则 = desktop；
 *   - `requireAuth` = **形态决定**（desktop=false；gui/browser=true）；
 *   - `authed` = **服务端读凭证判定**（browser 读 cookie / GUI 桥取持有令牌 → 有效 = true）；
 *     前端据此分派登录视图 / 主视图，**零额外往返**；
 *   - `instanceId` = 入口绑定的实例 id（与既有 `window.__chonkpilotInstanceId` 同值；
 *     **未认证时亦注入**：它是事件过滤 / `/show` URL / payload 注入的既有依赖，
 *     且**不构成凭据**（22 §2）——前端**不据 instanceId 判身份**）。
 *
 * **已认领实例** = `instance-claim` 应答（§4.1 ①：`{instance_id, work_dir, data_dir}`）——
 * 前端只**存**（不参与判定）：认领前 payload 的 instance_id 取注入值，认领后取服务端应答值
 * （22 §5：身份由服务端绑定，前端零身份逻辑）。
 *
 * 本模块为**叶子模块**（不 import 任何前端模块）→ 可被 `utils/mq.js` 安全引用（无循环依赖）。
 */

/** 三形态取值（61 §4.6）。 */
export const FORMS = ['desktop', 'gui', 'browser']

/** 首屏注入对象（`window.__ck`；未注入 → null）。 */
function injected() {
  return (typeof window !== 'undefined' && window.__ck) || null
}

/** 运行形态（注入值优先；缺失时按 shim 标记回落）。 */
export function runtimeForm() {
  const f = injected() && injected().form
  if (FORMS.includes(f)) return f
  return (typeof window !== 'undefined' && window.__ckHttpBridge === true) ? 'browser' : 'desktop'
}

/**
 * 是否分离形态（= 宿主编译开关 `-tags split` 的前端口径）：
 * desktop（桌面单体）→ 不发布 instance-heartbeat、服务端不据心跳判超时；
 * gui / browser（分离形态）→ 前端 SPA 发布（61 §4.1 ②：发布侧 = 前端）。
 */
export function isSplitForm() {
  return runtimeForm() !== 'desktop'
}

/** 服务端配置：是否要求认证（61 §4.6 形态决定：desktop=false；gui/browser=true）。 */
export function requireAuth() {
  const v = injected() && injected().requireAuth
  return v === true
}

/** 服务端判定：当前是否已认证（读凭证判定并注入；**前端不读 cookie / 不读 UA**）。 */
export function authed() {
  const v = injected() && injected().authed
  return v === true
}

/** 首屏注入的实例 id（入口绑定；未注入 → ''）。 */
export function injectedInstanceId() {
  const id = injected() && injected().instanceId
  if (typeof id === 'string' && id) return id
  return (typeof window !== 'undefined' && window.__chonkpilotInstanceId) || ''
}

// ── 已认领实例（instance-claim 应答；模块级单例）──

let claimed = null

/**
 * 记录 `instance-claim` 成功应答（§4.1 ①：`{instance_id, work_dir, data_dir}`）。
 * 非法/空应答 → 清空（不落半截状态）。
 */
export function setClaimedInstance(instance) {
  const id = instance && instance.instance_id
  claimed = id
    ? {
        instanceId: String(id),
        workDir: instance.work_dir || '',
        dataDir: instance.data_dir || '',
      }
    : null
  return claimed
}

/** 已认领实例（未认领 → null）。 */
export function claimedInstance() {
  return claimed
}

/** 已认领实例 id（未认领 → ''）。 */
export function claimedInstanceId() {
  return claimed ? claimed.instanceId : ''
}

/**
 * 当前实例 id（61 §0 第 21/45 行：业务 payload 必带 instance_id）：
 * **认领优先**（服务端绑定为准），未认领时回落首屏注入值。
 */
export function currentInstanceId() {
  return claimedInstanceId() || injectedInstanceId()
}

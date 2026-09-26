/**
 * userConfigApply —— usr 配置**下行变更事件 → 本窗口即时应用**的纯逻辑
 * （24-多窗口模型设计方案 §6.5 · 31 WIN-021 · MW-T27）。
 *
 * 消息面（唯一准则 = 61-消息一览 §3.1）：`data-user-config-changed`
 *   - payload `{data:{…}}`（与 `data-user-config-save` **同形**）；
 *   - **不带 `instance_id`** → 全局：每个窗口的桥各自转发 → **所有窗口都收到**；
 *   - 由服务方在 `data-user-config-save` / `-delete` **成功后**发出。
 *
 * 本模块只做**载荷解析与取值**（无 Vue / 无 DOM / 无副作用）→ 可直接 `npm test` 直测；
 * 真正的应用（DOM 属性 / i18n）在接线层 `composables/useUserConfigSync.js`。
 */

/** 从事件载荷取出 usr 配置对象（`{data:{…}}`）；容忍 JSON 字符串 / 直给对象 / 空 → null。 */
export function configFromChangedEvent(raw) {
  let v = raw
  if (typeof v === 'string' && v !== '') {
    try {
      v = JSON.parse(v)
    } catch (_) {
      return null
    }
  }
  if (!v || typeof v !== 'object') return null
  const data = v.data !== undefined ? v.data : v
  return data && typeof data === 'object' ? data : null
}

/**
 * 取本窗口需**应用**的项（theme / locale）：
 * 只认**非空字符串** —— 载荷可能只带变更键（save/delete 的受影响键），
 * 缺键不是"清空"（不得据此把本窗口主题/语言重置）。
 * @returns {{theme: string, locale: string}} 空串 = 该项未携带，调用方不动作
 */
export function themeLocaleFromConfig(cfg) {
  const pick = (k) => (cfg && typeof cfg[k] === 'string' ? cfg[k] : '')
  return { theme: pick('theme'), locale: pick('locale') }
}

/**
 * useUserConfigSync —— usr 配置（theme / locale）**跨窗口即时同步**接线层
 * （24-多窗口模型设计方案 §6.5 · 31 WIN-021 · MW-T27）。
 *
 * 消息面（唯一准则 = 61-消息一览 §3.1）：`data-user-config-changed {data:{…}}`
 *   - 服务方在 `data-user-config-save` / `-delete` **成功后**下行广播；
 *   - **不带 `instance_id`** → 每个窗口的桥各自转发 → **每个窗口都收到**（发起窗口幂等）；
 *   - 订阅后把 theme / locale **即时应用**到本窗口（无需重启、无需重开窗）。
 *
 * 纪律（前端规则）：
 *   - 状态逻辑一律走 `useXxx` composable；**禁** `watch` / `watchEffect` 监听 props/store
 *     （本模块只有一条 `mq.on` 订阅，无隐式依赖）。
 *   - 订阅为**模块级单例**（每窗口一份 JS 上下文）：多处调用不重复注册。
 *   - 应用**不回写 DB** —— theme/locale 的持久化由用户操作（Toolbar / 设置页）承担；
 *     事件驱动的回写会形成 save → changed → save 自激（见 `plugins/i18n.js` 的 `applyLocale`）。
 *
 * 纯逻辑（载荷解析/取值）在 `utils/userConfigApply.js`（可直跑单测）。
 */
import { ref } from 'vue'
import mq from '../utils/mq.js'
import { EventNames } from '../events/event-names'
import { applyLocale } from '../plugins/i18n'
import { configFromChangedEvent, themeLocaleFromConfig } from '../utils/userConfigApply.js'

/** 本窗口当前生效主题（事件同步；Toolbar 的主题勾选态以此为准）。 */
const theme = ref('light')

let subscribed = false

/** 应用一份 usr 配置到本窗口（幂等；仅动本窗口 DOM / i18n）。 */
function applyConfig(cfg) {
  const { theme: th, locale: lc } = themeLocaleFromConfig(cfg)
  if (th) {
    theme.value = th
    document.documentElement.setAttribute('data-theme', th)
    try { localStorage.setItem('chonkpilot-theme', th) } catch (_) {}
  }
  if (lc) applyLocale(lc)
}

/** 订阅一次宿主下行广播（模块级单例）。 */
function ensureSubscribed() {
  if (subscribed) return
  subscribed = true
  mq.on(EventNames.userConfigChanged, (raw) => applyConfig(configFromChangedEvent(raw)))
}

/**
 * 接线入口：确保本窗口已订阅 usr 配置下行广播。
 * @returns {{theme: import('vue').Ref<string>}} 本窗口生效主题（与事件同步）
 */
export function useUserConfigSync() {
  ensureSubscribed()
  return { theme }
}

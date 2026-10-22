/**
 * useUserConfig —— usr 配置的**单次加载 + 变更增量**缓存（BRIDGE-2 T14 用户层）。
 *
 * 口径（61 §3.1）：
 *   - 首次需要时 `data-user-config-load` 一次 → 全量 `{data}`；
 *   - 之后只订阅 `data-user-config-changed {data:{…}}`，按 key 合并（无则增、有则改）；
 *   - 模块级单例（每窗口一份 JS 上下文）：多处调用不重复 load / 不重复订阅。
 *
 * 纪律：状态逻辑走 useXxx composable；不 watch props/store。
 */
import { ref, readonly } from 'vue'
import mq from '../utils/mq.js'
import { EventNames } from '../events/event-names'
import { getUserConfig } from '../api/config.js'
import { configFromChangedEvent, mergeUserConfig } from '../utils/userConfigApply.js'

/** 当前 usr 配置（合并视图；值以最后一次 load / changed 为准）。 */
const state = ref({})
/** 是否已完成首次全量加载。 */
const loaded = ref(false)

let loading = null
let subscribed = false

function merge(next) {
  state.value = mergeUserConfig(state.value, next)
}

function ensureSubscribed() {
  if (subscribed) return
  subscribed = true
  // data-user-config-changed 不带 instance_id（全局）；每个窗口各收到一次。
  mq.on(EventNames.userConfigChanged, (raw) => merge(configFromChangedEvent(raw)))
}

/** 首次全量加载（并发调用共享同一 Promise；已加载 = 直接返回缓存）。 */
async function ensureLoaded() {
  if (loaded.value) return state.value
  if (!loading) {
    loading = getUserConfig()
      .then((cfg) => {
        merge(cfg || {})
        loaded.value = true
        return state.value
      })
      .catch(() => state.value)
      .finally(() => { loading = null })
  }
  return loading
}

/**
 * 接线入口。
 * @returns {{userConfig: import('vue').Ref<object>, loaded: import('vue').Ref<boolean>, ensureLoaded: () => Promise<object>}}
 */
export function useUserConfig() {
  ensureSubscribed()
  return {
    userConfig: readonly(state),
    loaded: readonly(loaded),
    ensureLoaded,
  }
}

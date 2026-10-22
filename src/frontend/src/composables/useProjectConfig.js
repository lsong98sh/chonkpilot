/**
 * useProjectConfig —— prj 配置的**单次加载 + refresh 增量**缓存（BRIDGE-2 T14 项目层）。
 *
 * 口径（61 §3.1）：
 *   - 首次需要时 `data-prj-config-list` 一次 → 全量 key→value；
 *   - 之后只订阅既有 `data-prj-config-refresh`：载荷带 `list`（domain 全量）→ 直接合并；
 *     不带 `list` → 重新 load 一次兜底；
 *   - 模块级单例（每窗口一份 JS 上下文）：多处调用不重复 load / 不重复订阅。
 *
 * 纪律：状态逻辑走 useXxx composable；不 watch props/store；订阅回调走显式 `subscribe`。
 */
import { ref, readonly } from 'vue'
import { onDataRefresh } from '../utils/dataClient'
import { getAllConfig } from '../api/config.js'
import { mergeUserConfig } from '../utils/userConfigApply.js'

/** 当前 prj 配置（key → 字符串值）。 */
const state = ref({})
/** 是否已完成首次全量加载。 */
const loaded = ref(false)

let loading = null
let subscribed = false
const listeners = new Set()

function apply(next) {
  state.value = mergeUserConfig(state.value, next)
  for (const cb of listeners) {
    try { cb(state.value) } catch (_) { /* 单个订阅方异常不阻断其余 */ }
  }
}

function ensureSubscribed() {
  if (subscribed) return
  subscribed = true
  onDataRefresh('prj-config', (payload) => {
    const list = payload && payload.list
    if (list && typeof list === 'object' && !Array.isArray(list)) {
      apply(list)
      return
    }
    ensureLoaded(true).catch(() => {})
  })
}

/** 首次全量加载（并发共享同一 Promise；已加载 = 返回缓存）。force = 重新读一次。 */
async function ensureLoaded(force = false) {
  if (loaded.value && !force) return state.value
  if (!loading) {
    loading = getAllConfig()
      .then((res) => {
        const cfg = (res && (res.config || res)) || {}
        apply(cfg)
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
 * @returns {{
 *   projectConfig: import('vue').Ref<object>,
 *   loaded: import('vue').Ref<boolean>,
 *   ensureLoaded: (force?: boolean) => Promise<object>,
 *   subscribe: (cb: (cfg: object) => void) => () => void
 * }}
 */
export function useProjectConfig() {
  ensureSubscribed()
  return {
    projectConfig: readonly(state),
    loaded: readonly(loaded),
    ensureLoaded,
    subscribe(cb) {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
  }
}

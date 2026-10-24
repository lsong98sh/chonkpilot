/**
 * 插件页就绪态（PLUGIN-UI-1 前端体验收口）：未就绪 / 资源不可用 → 占位 + 自动重试。
 *
 * 背景：插件页面资源由插件进程经 UDS 提供（`plugin/v1/ui.asset.get`，路由统一 `/plugins/<id>/…`），
 * 插件未就绪时宿主一律回 404（`src/lib/gui/pluginassets.go` + `pluginmgr.UIAsset` 只放行 `running`）——
 * iframe 里只会是白屏 / 浏览器错误页。用户在插件启动前点开配置页时看不到任何解释。
 *
 * 本 composable 负责（**零新增 MQ 主题**）：
 *   ① 挂 iframe 前先看插件 `status`（既有 `usePlugins` 快照 / `plugin-status` 事件推送）；
 *   ② 再对资源做一次 HEAD 探活 —— HTTP 404 的 iframe **也会触发 `load`**，只靠 `onerror` 不可靠；
 *   ③ 未就绪 → 占位（组件渲染图标 + 文案 + 手动「重试」）；
 *   ④ 插件状态转 `running`（`subscribePluginChange` 事件）→ 自动重试，上限 `AUTO_RETRY_LIMIT` 次；
 *      超过上限后只保留手动重试。
 *
 * 约束：
 *   - **零 watch / watchEffect**（项目规则）：状态变化全部来自 `subscribePluginChange` 的推事件；
 *   - 已成功加载（`stage === 'ready'`）后不再被状态事件拉回占位，避免页面被偶然的降级事件清空。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { pluginById, subscribePluginChange } from './usePlugins'
import { pluginUIPageURL } from '../components/plugin/pluginUiRoute.js'

/** 首次加载超时（毫秒）：超时即视为未就绪（iframe 常因 404 空文档而**不**触发 load 之外的可观测信号）。 */
export const LOAD_TIMEOUT_MS = 3000
/** 自动重试上限（「状态转 running」与「探活失败」两类触发共用同一预算）。 */
export const AUTO_RETRY_LIMIT = 3
/** 探活失败后自动重试的延迟（毫秒）——给插件启动一点余量。 */
export const AUTO_RETRY_DELAY_MS = 800

/** 宿主只在该状态下经 UDS 提供资源（`pluginmgr.UIAsset` 的唯一放行值）。 */
const READY_STATUS = 'running'

/**
 * @param {{pluginId: string, pageId: string, entry: string}} props 组件 props
 * @returns 渲染所需状态 + iframe 回调 + 手动重试
 */
export function usePluginReadiness(props) {
  /** 'idle'（无 entry）| 'checking'（探活）| 'loading'（iframe 已挂、未 load）| 'ready' | 'not-ready' */
  const stage = ref(props.entry ? 'checking' : 'idle')
  /** 已消耗的自动重试次数（手动重试不占预算）。 */
  const attempts = ref(0)
  /** 未就绪原因：'status'（插件状态非 running）| 'asset'（探活 404）| 'timeout' | 'error'。 */
  const reason = ref('')
  /** 重载代次（拼 `?t=` 绕过宿主短缓存 / 浏览器缓存）。 */
  const seq = ref(0)

  let loadTimer = null
  let retryTimer = null
  let probeToken = 0
  let disposed = false
  let unsubscribe = null

  const plugin = computed(() => pluginById(props.pluginId))
  const status = computed(() => (plugin.value && plugin.value.status) || '')
  const detail = computed(() => (plugin.value && plugin.value.detail) || '')

  const src = computed(() => {
    if (!props.entry) return 'about:blank'
    const base = pluginUIPageURL(props.pluginId, props.entry)
    const query = `p=${encodeURIComponent(props.pageId)}` + (seq.value ? `&t=${seq.value}` : '')
    return `${base}?${query}`
  })

  const showFrame = computed(() => stage.value === 'loading' || stage.value === 'ready')
  const showLoading = computed(() => stage.value === 'checking' || stage.value === 'loading')
  const showPlaceholder = computed(() => stage.value === 'not-ready')
  const autoExhausted = computed(() => attempts.value >= AUTO_RETRY_LIMIT)
  const canRetry = computed(() => stage.value !== 'checking' && stage.value !== 'loading')

  function clearLoadTimer() {
    if (loadTimer) {
      clearTimeout(loadTimer)
      loadTimer = null
    }
  }

  function clearRetryTimer() {
    if (retryTimer) {
      clearTimeout(retryTimer)
      retryTimer = null
    }
  }

  function clearTimers() {
    clearLoadTimer()
    clearRetryTimer()
  }

  /** 插件状态明确非 running（资源一律经插件进程提供，与状态强相关）。 */
  function blockedByStatus() {
    return !!(status.value && status.value !== READY_STATUS)
  }

  function fail(kind) {
    clearTimers()
    reason.value = kind
    stage.value = 'not-ready'
  }

  function beginLoad() {
    clearLoadTimer()
    reason.value = ''
    stage.value = 'loading'
    loadTimer = setTimeout(() => {
      loadTimer = null
      if (stage.value !== 'loading') return
      fail('timeout')
    }, LOAD_TIMEOUT_MS)
  }

  /** HEAD 探活：200 → 资源已就绪；404 / 网络异常 → 未就绪（不阻塞，交占位 + 自动重试）。 */
  async function probe() {
    if (typeof fetch !== 'function') return true
    const token = ++probeToken
    let ok = false
    try {
      const res = await fetch(src.value, { method: 'HEAD', cache: 'no-store' })
      ok = !!(res && res.ok)
    } catch (_) {
      ok = false
    }
    if (disposed || token !== probeToken) return false
    return ok
  }

  function scheduleAutoRetry() {
    // 仅「插件自称 running 但资源仍未就绪」（启动尾段）才按延迟自试；
    // 状态未知 / 非 running 一律等 `plugin-status` 事件驱动，避免在插件启动前空耗自动重试预算。
    if (status.value !== READY_STATUS) return
    if (attempts.value >= AUTO_RETRY_LIMIT) return
    clearRetryTimer()
    retryTimer = setTimeout(() => {
      retryTimer = null
      if (disposed || stage.value !== 'not-ready') return
      if (blockedByStatus()) return // 等 `plugin-status` 事件驱动，不轮询
      attempts.value += 1
      reload()
    }, AUTO_RETRY_DELAY_MS)
  }

  async function check() {
    if (!props.entry) {
      stage.value = 'idle'
      return
    }
    if (blockedByStatus()) {
      fail('status')
      return
    }
    stage.value = 'checking'
    const ok = await probe()
    if (disposed || stage.value !== 'checking') return
    if (ok) {
      beginLoad()
      return
    }
    fail('asset')
    scheduleAutoRetry()
  }

  /** 重新探活并重挂 iframe（代次 +1 → `?t=` 绕过缓存）。 */
  function reload() {
    clearTimers()
    probeToken += 1
    reason.value = ''
    seq.value += 1
    stage.value = 'checking'
    check()
  }

  /** 手动「重试」（不限次数；不占自动重试预算）。 */
  function retry() {
    if (!canRetry.value) return
    reload()
  }

  /** iframe `load`（含 404 文档：故 ready 只代表"文档已交回"，资源可用性由探活 + 状态保证）。 */
  function onFrameLoad() {
    clearLoadTimer()
    reason.value = ''
    stage.value = 'ready'
  }

  /** iframe `error`（跨源 / 资源级失败；多数 404 不会走到这里）。 */
  function onFrameError() {
    if (stage.value === 'ready') return
    fail('error')
  }

  /** 插件清单 / 状态变更（`usePlugins` 事件推送；**不用 watch**）。 */
  function onPluginChange(id) {
    if (id !== props.pluginId) return
    if (stage.value === 'ready') return
    if (status.value === READY_STATUS) {
      if (stage.value === 'not-ready' && attempts.value < AUTO_RETRY_LIMIT) {
        attempts.value += 1
        reload()
      }
      return
    }
    if (blockedByStatus() && (stage.value === 'loading' || stage.value === 'checking')) {
      fail('status')
    }
  }

  onMounted(() => {
    unsubscribe = subscribePluginChange(onPluginChange)
    check()
  })

  onBeforeUnmount(() => {
    disposed = true
    clearTimers()
    if (unsubscribe) unsubscribe()
    unsubscribe = null
  })

  return {
    stage,
    reason,
    src,
    status,
    detail,
    showFrame,
    showLoading,
    showPlaceholder,
    autoExhausted,
    canRetry,
    autoRetryLimit: AUTO_RETRY_LIMIT,
    onFrameLoad,
    onFrameError,
    retry,
  }
}

/**
 * useSessionSearch：会话内查找（CHAT-028）的状态逻辑。
 *
 * 通道（61 §1 `vfts.session.query` 的同名插件动作）：插件把 `vfts.session.query` 注册为
 * **插件 UI 动作**（`plugin.ui.rpc`，见 28-plugins §3A.2 / codegraph main.go），不订阅总线；
 * 故前端经通用 RPC 主题 `plugin-call` 转发：
 *   `mq.emit('plugin-call', { id:'codegraph', method:'rpc',
 *      params:{ method:'vfts.session.query', params:{ instance_id, session_id, query, limit } } })`
 *   → `{ok, results:[{session_id, dialog_id, turn_id, message_id, role, ts, snippet, score}]}`，
 *   `hits` 已按 **ts 升序**（= 会话内 ↑↓ 连续跳转口径）；`ok:false`（索引未启用等）→
 *   查找条内一行提示，不弹错误框。
 *
 * 交互口径：输入即查（250ms 防抖，序号作废在途应答）· `第 n/N 个` · ↑↓ 环形跳转 · Esc 关闭清高亮。
 * 定位/高亮交给调用方注入的 `reveal(hit, maxPages)`（MessageList `revealMessage`）：
 * 目标未加载时由 MessageList 自动向上翻页，超过上限返回 `{found:false, pages}` → 本 composable
 * 置 `olderHint`（提示「该命中较早，继续向上加载」）。
 *
 * 禁止 watch 监听 props/store（项目硬规则）：所有状态变更均由 UI 事件/方法调用驱动。
 */
import { ref, computed } from 'vue'
import mq, { currentInstanceId } from '@ckfw/utils/mq.js'

const DEBOUNCE_MS = 250
const MAX_REVEAL_PAGES = 5
const TOP_K = 200

/**
 * @param {object} opts
 * @param {() => string} opts.sessionId 当前会话 id（getter，避免持有过期会话）
 * @param {(hit, maxPages) => Promise<{found:boolean, pages:number}>} [opts.reveal] 定位到命中消息
 */
export function useSessionSearch({ sessionId, reveal } = {}) {
  const open = ref(false)
  const query = ref('')
  const hits = ref([])
  const index = ref(-1)
  const loading = ref(false)
  const searched = ref(false) // 本次关键字是否已收到应答（区分「查完无命中」与「防抖等待中」）
  const errorHint = ref('')   // i18n key（如 chatSearch.not_enabled）；空 = 无提示
  const olderHint = ref(false)

  let debounceTimer = null
  let seq = 0 // 查询代次：输入变更/关闭时递增 → 丢弃过期应答

  const total = computed(() => hits.value.length)
  const activeHit = computed(() => (index.value >= 0 ? (hits.value[index.value] || null) : null))

  function currentSessionId() {
    const v = typeof sessionId === 'function' ? sessionId() : sessionId
    return v ? String(v) : ''
  }

  function cancelDebounce() {
    if (debounceTimer) {
      clearTimeout(debounceTimer)
      debounceTimer = null
    }
  }

  function clearResults() {
    hits.value = []
    index.value = -1
    searched.value = false
    errorHint.value = ''
    olderHint.value = false
  }

  /** 定位第 i 个命中（滚动 + 高亮由 reveal 回调完成）。 */
  async function focusIndex(i) {
    if (i < 0 || i >= hits.value.length) return
    index.value = i
    olderHint.value = false
    const hit = hits.value[i]
    if (typeof reveal !== 'function' || !hit) return
    let r = null
    try {
      r = await reveal(hit, MAX_REVEAL_PAGES)
    } catch (e) {
      console.warn('[useSessionSearch] reveal failed:', e)
      return
    }
    if (r && r.found === false && (Number(r.pages) || 0) > 0) olderHint.value = true
  }

  /** 发起查询（内部：防抖回调 / 重新打开时调用）。 */
  async function runQuery() {
    const sid = currentSessionId()
    const match = query.value.trim()
    if (!open.value || !sid || !match) return
    const mySeq = ++seq
    loading.value = true
    errorHint.value = ''
    olderHint.value = false
    let nextIndex = -1
    try {
      const env = await mq.emit('plugin-call', {
        id: 'codegraph',
        method: 'rpc',
        params: {
          method: 'vfts.session.query',
          params: {
            instance_id: currentInstanceId(),
            session_id: sid,
            query: match,
            limit: TOP_K,
          },
        },
      })
      if (mySeq !== seq) return
      const backend = env && env.backend
      if (!backend || backend.ok === false) {
        hits.value = []
        index.value = -1
        errorHint.value = 'chatSearch.not_enabled'
        searched.value = true
        return
      }
      const wrapped = backend.result
      // `plugin-call` 应答 = `{ok, result:<插件结果>}`；兼容直接回插件结果。
      const res = wrapped && Object.prototype.hasOwnProperty.call(wrapped, 'result')
        ? wrapped.result
        : wrapped
      searched.value = true
      if (!res || res.ok !== true) {
        // 索引未启用 / 插件不可用：条内一行提示（不弹错误框）
        hits.value = []
        index.value = -1
        errorHint.value = 'chatSearch.not_enabled'
        return
      }
      const list = Array.isArray(res.results) ? res.results : []
      hits.value = list.filter((h) => h && h.message_id)
      index.value = hits.value.length > 0 ? 0 : -1
      nextIndex = index.value
    } catch (e) {
      if (mySeq !== seq) return
      console.warn('[useSessionSearch] vfts.session.query failed:', e)
      hits.value = []
      index.value = -1
      errorHint.value = 'chatSearch.not_enabled'
      return
    } finally {
      if (mySeq === seq) loading.value = false
    }
    if (mySeq !== seq) return
    if (nextIndex >= 0) await focusIndex(nextIndex)
  }

  /** 打开查找条（已有文字则立即重查）。 */
  function openBar() {
    if (open.value) return
    open.value = true
    if (query.value.trim()) runQuery()
  }

  /** 关闭：清空关键字/命中/高亮（Esc 与切会话共用）。 */
  function close() {
    cancelDebounce()
    seq++
    open.value = false
    query.value = ''
    loading.value = false
    clearResults()
  }

  /** 输入变更：清旧结果 + 250ms 防抖查询。 */
  function onInput(v) {
    query.value = String(v == null ? '' : v)
    cancelDebounce()
    seq++
    loading.value = false
    clearResults()
    if (!query.value.trim()) return
    debounceTimer = setTimeout(() => {
      debounceTimer = null
      runQuery()
    }, DEBOUNCE_MS)
  }

  /** 下一个命中（环形）。 */
  function next() {
    if (!hits.value.length) return
    const i = index.value < 0 ? 0 : (index.value + 1) % hits.value.length
    return focusIndex(i)
  }

  /** 上一个命中（环形）。 */
  function prev() {
    if (!hits.value.length) return
    const i = index.value < 0 ? hits.value.length - 1 : (index.value - 1 + hits.value.length) % hits.value.length
    return focusIndex(i)
  }

  return {
    open, query, hits, index, loading, searched, errorHint, olderHint,
    total, activeHit,
    openBar, close, onInput, next, prev,
  }
}

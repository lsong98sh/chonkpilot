<template>
  <div class="plugin-page-host">
    <div v-if="!entry" class="pph-state">
      <Icon name="warning-filled" :size="28" />
      <p>{{ $t('layoutFree.plugin.pageMissing', { id: pluginId }) }}</p>
    </div>
    <template v-else>
      <iframe
        v-if="showFrame"
        ref="frameRef"
        class="pph-frame"
        :src="src"
        sandbox="allow-scripts allow-forms allow-same-origin"
        referrerpolicy="no-referrer"
        @load="onFrameLoad"
        @error="onFrameError"
      />
      <div v-if="showLoading" class="pph-state pph-overlay">
        <Icon name="loading" :size="22" class="is-loading" />
        <p>{{ $t('common.loading') }}</p>
      </div>
      <!-- PLUGIN-UI-1：插件未就绪 / 资源不可用（宿主对 /plugins/<id>/… 一律 404）→ 明确解释 + 重试 -->
      <div v-else-if="showPlaceholder" class="pph-state pph-placeholder">
        <Icon name="warning-filled" :size="28" />
        <p class="pph-title">{{ $t('layoutFree.plugin.notReadyTitle', { id: pluginId }) }}</p>
        <p class="pph-hint">{{ $t(reasonKey) }}</p>
        <p v-if="status" class="pph-meta">
          {{ $t('layoutFree.plugin.notReadyStatus', { status }) }}<template v-if="detail"> · {{ detail }}</template>
        </p>
        <p v-if="autoExhausted" class="pph-meta">{{ $t('layoutFree.plugin.autoRetryExhausted', { n: autoRetryLimit }) }}</p>
        <button type="button" class="pph-retry" :disabled="!canRetry" @click="retry">
          {{ $t('layoutFree.plugin.retry') }}
        </button>
      </div>
    </template>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import mq from '../../utils/mq'
import Icon from '../../components/icon/Icon.vue'
import { subscribeTheme, themeSnapshot } from '../../composables/useTheme.js'
import { usePluginReadiness } from '../../composables/usePluginReadiness.js'
import { pluginAction } from '../../utils/pluginAction.js'

const props = defineProps({
  pluginId: { type: String, required: true },
  pageId: { type: String, required: true },
  entry: { type: String, default: '' },
  title: { type: String, default: '' },
})

const frameRef = ref(null)

/**
 * 就绪态 + 自动重试（PLUGIN-UI-1 前端体验收口）：未就绪时**不挂 iframe**（避免白屏），
 * 由占位页解释原因并提供「重试」；插件转 `running` 后自动重试（上限见 `AUTO_RETRY_LIMIT`）。
 */
const {
  src,
  status,
  detail,
  reason,
  showFrame,
  showLoading,
  showPlaceholder,
  autoExhausted,
  canRetry,
  autoRetryLimit,
  onFrameLoad: onReadyLoad,
  onFrameError,
  retry,
} = usePluginReadiness(props)

/** 未就绪文案：按原因选 i18n key（状态未就绪 / 资源 404 / 加载超时 / 加载错误）。 */
const REASON_KEY = {
  status: 'notReadyStatusHint',
  asset: 'notReadyAssetHint',
  timeout: 'notReadyTimeoutHint',
  error: 'notReadyErrorHint',
}
const reasonKey = computed(() => 'layoutFree.plugin.' + (REASON_KEY[reason.value] || 'notReadyAssetHint'))

function reply(id, ok, result, errors) {
  const win = frameRef.value?.contentWindow
  if (!win) return
  win.postMessage({ ckPlugin: 1, id, type: 'result', ok, result, errors }, '*')
}

async function call(pluginMethod, params) {
  const res = await mq.emit(pluginMethod, params)
  const env = res?.backend
  if (!env || env.ok === false) {
    throw new Error((env?.errors || ['plugin call failed']).join('; '))
  }
  return env.result
}

async function dispatch(method, params = {}) {
  switch (method) {
    case 'config.get':
      return call('plugin-config-get', { id: props.pluginId })
    case 'config.set':
      return call('plugin-config-set', { id: props.pluginId, values: params.values || params })
    case 'config.reset':
    case 'data.read':
    case 'data.write':
    case 'rpc':
    case 'log':
      return call('plugin-call', { id: props.pluginId, method, params })
    // PLUGIN-UI-1：动作面（`POST /plugins/<id>/api/<action>`，请求头带来源插件 id）。
    case 'action':
      return pluginAction(props.pluginId, params.action, params.payload)
    case 'open-url':
      if (params.url) window.open(String(params.url), '_blank', 'noopener')
      return { ok: true }
    case 'close':
      mq.emit('preview-tab-close-this', {})
      return { ok: true }
    default:
      throw new Error(`unknown method: ${method}`)
  }
}

async function onMessage(e) {
  const win = frameRef.value?.contentWindow
  if (!win || e.source !== win) return
  const d = e.data
  if (!d || d.ckPlugin !== 1 || d.type !== 'call') return
  try {
    const result = await dispatch(d.method, d.params || {})
    reply(d.id, true, result)
  } catch (err) {
    reply(d.id, false, null, [err?.message || String(err)])
  }
}

/**
 * 主题下发（THEME-1）：iframe `load` 后推一次当前主题快照，主题变化时再推（幂等，不重建 iframe）。
 * 协议沿用 ckPlugin 信封：`{ckPlugin:1, type:'theme', theme:{id, scheme, tokens}}`
 * （插件页 `public/shared/ck-theme.js` 收到后注入 `#ck-theme`）。
 */
function pushTheme() {
  const win = frameRef.value?.contentWindow
  if (!win) return
  try {
    win.postMessage({ ckPlugin: 1, type: 'theme', theme: themeSnapshot() }, '*')
  } catch (_) {
    /* iframe 尚未就绪：忽略，load 后会再推 */
  }
}

/* ── 受控 mq 入口（PLUGIN-UI-1 第二段）────────────────────────────────
 * 同源 iframe 可直接取 `window.ckMq`；只暴露 on / off / emit（**不**裸暴露宿主 mq 对象）。
 * 该 frame 注册的全部订阅登记在 frameSubs：**重新 load 注入前 / 组件卸载 / 页面 beforeunload**
 * 一律 off，避免回调打到已销毁的 frame。
 */
const frameSubs = [] // [{ topic, cb, off }]
let boundWin = null

function clearFrameSubs() {
  while (frameSubs.length) {
    const s = frameSubs.pop()
    try {
      s.off()
    } catch (_) {
      /* 已注销：忽略 */
    }
  }
}

function makeCkMq(win) {
  const live = () => boundWin === win
  return {
    on(topic, cb) {
      if (!live() || !topic || typeof cb !== 'function') return () => {}
      const off = mq.on(String(topic), (payload) => {
        if (!live()) return
        try {
          cb(payload)
        } catch (e) {
          console.warn('[plugin] ckMq.on 回调异常:', e)
        }
      })
      frameSubs.push({ topic: String(topic), cb, off })
      return off
    },
    off(topic, cb) {
      const t = String(topic)
      for (let i = frameSubs.length - 1; i >= 0; i -= 1) {
        const s = frameSubs[i]
        if (s.topic !== t) continue
        if (cb && s.cb !== cb) continue
        frameSubs.splice(i, 1)
        try {
          s.off()
        } catch (_) {
          /* 已注销：忽略 */
        }
      }
    },
    emit(topic, payload) {
      if (!live()) return Promise.resolve(null)
      return mq.emit(topic, payload)
    },
  }
}

/** 在 iframe `load` 后注入 ckMq（先退掉旧 frame 的全部订阅，再绑定新实现）。 */
function injectCkMq() {
  const win = frameRef.value?.contentWindow
  if (!win) return
  clearFrameSubs()
  boundWin = win
  const api = makeCkMq(win)
  try {
    // 插件页引入 `public/shared/ck-mq.js` 时用它的注入点（可补投注入前的排队调用）；
    // 未引入则直接挂 `window.ckMq`。
    if (typeof win.__ckMqBind === 'function') win.__ckMqBind(api)
    else win.ckMq = api
  } catch (_) {
    /* 跨源 / 已销毁 frame：忽略（不阻塞页面加载） */
  }
}

function onFrameLoad() {
  onReadyLoad()
  injectCkMq()
  pushTheme()
}

let unsubUI = null
let unsubTheme = null
onMounted(() => {
  window.addEventListener('message', onMessage)
  window.addEventListener('beforeunload', clearFrameSubs)
  unsubUI = mq.on('plugin-ui-event', (ev) => {
    if (!ev || ev.id !== props.pluginId) return
    frameRef.value?.contentWindow?.postMessage(
      { ckPlugin: 1, type: 'event', topic: ev.topic, payload: ev.payload },
      '*',
    )
  })
  unsubTheme = subscribeTheme(() => pushTheme())
})
onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage)
  window.removeEventListener('beforeunload', clearFrameSubs)
  clearFrameSubs()
  boundWin = null
  if (unsubUI) unsubUI()
  if (unsubTheme) unsubTheme()
})
</script>

<style scoped>
.plugin-page-host {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
.pph-frame {
  flex: 1;
  width: 100%;
  height: 100%;
  border: 0;
  background: var(--bg-primary, #fff);
}
.pph-state {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--text-muted);
  font-size: 13px;
}
.pph-overlay {
  position: absolute;
  inset: 0;
  background: var(--bg-primary, #fff);
}
/* 未就绪占位（PLUGIN-UI-1）：仅用主题 token，随主题切换 */
.pph-placeholder {
  gap: 10px;
  padding: 24px;
  text-align: center;
}
.pph-title {
  margin: 0;
  font-size: 14px;
  color: var(--fg-primary);
}
.pph-hint {
  max-width: 420px;
  margin: 0;
  line-height: 1.6;
  color: var(--fg-secondary);
}
.pph-meta {
  margin: 0;
  font-size: 12px;
  color: var(--text-muted);
}
.pph-retry {
  margin-top: 4px;
  padding: 4px 14px;
  font-size: 12px;
  color: var(--fg-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  cursor: pointer;
  transition: color var(--transition-fast), border-color var(--transition-fast);
}
.pph-retry:hover:not(:disabled) {
  color: var(--accent);
  border-color: var(--accent);
}
.pph-retry:disabled {
  opacity: 0.5;
  cursor: default;
}
</style>

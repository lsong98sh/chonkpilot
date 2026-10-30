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
import { pluginHost } from './host.js'
import { pluginUIComponents } from './components.js'
import { subscribePluginTheme as subscribeTheme, pluginThemeSnapshot as themeSnapshot } from './theme.js'
import { usePluginReadiness } from './usePluginReadiness.js'
import { pluginAction } from './pluginAction.js'

// 宿主端口适配：mq 由依赖注入，设计系统组件由宿主注册。
const mq = {
  on: (...args) => pluginHost().mq.on(...args),
  emit: (...args) => pluginHost().mq.emit(...args),
}
const { Icon } = pluginUIComponents()

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

/* ── 受控 mq 入口（PLUGIN-UI-1 第二段；2026-10-30 定稿）──────────────
 * ckMq 已归平台基座（`@ckfw/plugin-ui/ckMq.js`，宿主 `main.js` 装配时自挂 `window.ckMq`）：
 * 插件页自己调 `window.top.ckMq.new(window)` 注册订阅（路由键 = 订阅方 window 引用），
 * 宿主**不再注入实现**（原 `__ckMqBind` / `win.ckMq = api` / `frameSubs` 已删除）。
 * 本组件只负责**注销**：frame 重载由插件页 `new(win)` 隐式驳回旧表；卸载 / 页面 `beforeunload`
 * 由这里 `release(contentWindow)`，避免宿主 mq 继续持有已销毁 frame 的回调。
 */
function releaseFrame() {
  const win = frameRef.value?.contentWindow
  if (!win) return
  try {
    window.ckMq?.release(win)
  } catch (_) {
    /* 跨源 / 已销毁 frame：忽略 */
  }
}

function onFrameLoad() {
  onReadyLoad()
  pushTheme()
}

let unsubUI = null
let unsubTheme = null
onMounted(() => {
  window.addEventListener('message', onMessage)
  window.addEventListener('beforeunload', releaseFrame)
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
  window.removeEventListener('beforeunload', releaseFrame)
  releaseFrame()
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

<!--
  栏视图条目 `chrome-warn`（UILAYOUT-1 裁决 1/2 / §7 P4①）：Chrome 未检测告警。
  **条件显示**由宿主视图自持（组件自身在无告警时不渲染任何 DOM）——
  机制层不引入 `when`/条件字段协议（裁决 2：实时/条件项一律做成栏视图）。
  判据（既有真实信号，见 utils/chromeStatus.js）：gui.toolchain.detect 的 chrome 行 ∪ usr chromePath。
  点击 → 既有主题 `preview-tab-open {kind:'settings-paths'}`（与 legacy Toolbar 同口径）。
-->
<template>
  <button
    v-if="chromeMissing"
    class="chrome-warn"
    :title="$t('config.chromeTip')"
    @click="openPaths"
  >
    <Icon name="warning-filled" :size="15" color="var(--warning, #e8a317)" />
  </button>
</template>

<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { Icon } from '../../components/icon'
import { detectToolchains, getUserConfig } from '../../api/config'
import { isChromeMissing } from '../../utils/chromeStatus'
import { onDataRefresh } from '../../utils/dataClient'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const chromeMissing = ref(false)

async function checkChrome() {
  try {
    const tools = await detectToolchains()
    const chrome = (tools || []).find((x) => x && x.id === 'chrome') || {}
    const res = await getUserConfig()
    const cfg = (res && res.config) || res || {}
    chromeMissing.value = isChromeMissing({ detectedPath: chrome.path, userChromePath: cfg.chromePath })
  } catch (_) {
    chromeMissing.value = false
  }
}

function openPaths() {
  mq.emit(EventNames.previewTabOpen, { kind: 'settings-paths' })
}

let offRefresh = null
onMounted(() => {
  checkChrome()
  // 路径配置页保存 chromePath → data-user-config-refresh 广播 → 重新判定
  offRefresh = onDataRefresh('user-config', checkChrome)
})
onUnmounted(() => { if (typeof offRefresh === 'function') offRefresh() })
</script>

<style scoped>
.chrome-warn {
  background: transparent;
  border: 1px solid transparent;
  height: 28px;
  padding: 0 6px;
  display: inline-flex;
  align-items: center;
  cursor: pointer;
  -webkit-app-region: no-drag;
}
.chrome-warn:hover {
  background: var(--bg-hover);
}
</style>

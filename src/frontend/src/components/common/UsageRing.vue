<template>
  <!-- 上下文占用百分比环（输入框下方）：分子 = 最近一次发给 LLM 的输入 token，
       分母 = 该次调用对应 LLM 的上下文窗口（maxContextToken）。
       始终显示（无用量时为空环）；尺寸 = 一行文字高度，环内不写字；悬停展开明细。 -->
  <div
    class="usage-ring"
    :class="['tone-' + tone, { 'is-actionable': !!usage && !limit }]"
    :aria-label="ariaText"
    tabindex="0"
    @click="onRingClick"
    @keydown.enter="onRingClick"
  >
    <svg class="usage-ring-svg" viewBox="0 0 36 36" role="img">
      <circle class="usage-ring-track" cx="18" cy="18" r="15.5" pathLength="100" />
      <circle
        class="usage-ring-bar"
        cx="18"
        cy="18"
        r="15.5"
        pathLength="100"
        :stroke-dasharray="`${sweep} 100`"
      />
    </svg>

    <div class="usage-ring-tip" role="tooltip">
      <div class="tip-title">{{ $t('chat.usage_ring_title') }}</div>
      <template v-if="usage">
        <div class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_prompt') }}</span>
          <span class="tip-v">{{ usedText }}<span v-if="isEstimate" class="tip-note"> · {{ $t('chat.usage_send_pending') }}</span></span>
        </div>
        <div v-if="pct != null" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_ring_ratio') }}</span>
          <span class="tip-v">{{ formatPercent(pct) }} / {{ formatTokens(limit) }}</span>
        </div>
        <!-- 未配置上下文窗口：点击直接打开当前 LLM 的配置页（并定位到该 provider 的编辑框）。 -->
        <div v-if="!limit" class="tip-row tip-warn">
          <span class="tip-k">{{ $t('chat.usage_max_context') }}</span>
          <button type="button" class="tip-link" @click.stop="$emit('open-config')">
            {{ $t('chat.usage_no_context_cap') }}
          </button>
        </div>
        <div v-if="deviation != null" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_deviation') }}</span>
          <span class="tip-v">{{ formatSignedPercent(deviation) }}</span>
        </div>
        <div v-if="usage.prompt_tokens && usage.est_prompt_tokens" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_estimate') }}</span>
          <span class="tip-v">{{ formatTokens(usage.est_prompt_tokens) }}</span>
        </div>
        <div v-if="usage.cached_tokens" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_cached') }}</span>
          <span class="tip-v">{{ formatTokens(usage.cached_tokens) }}<span v-if="cachedPct != null" class="tip-note"> ({{ formatPercent(cachedPct) }})</span></span>
        </div>
        <div v-if="usage.completion_tokens" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_completion') }}</span>
          <span class="tip-v">{{ formatTokens(usage.completion_tokens) }}</span>
        </div>
        <div v-if="usage.reasoning_tokens" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_reasoning') }}</span>
          <span class="tip-v">{{ formatTokens(usage.reasoning_tokens) }}</span>
        </div>
        <div v-if="usage.total_tokens" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_total') }}</span>
          <span class="tip-v">{{ formatTokens(usage.total_tokens) }}</span>
        </div>
        <div v-if="usage.max_output_token" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_max_output') }}</span>
          <span class="tip-v">{{ formatTokens(usage.max_output_token) }}</span>
        </div>
        <div v-if="usage.model || usage.llm" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_model') }}</span>
          <span class="tip-v">{{ usage.model || usage.llm }}<span v-if="usage.model && usage.llm" class="tip-note"> · {{ usage.llm }}</span></span>
        </div>
        <div v-if="usage.step" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_step') }}</span>
          <span class="tip-v">{{ $t('chat.usage_step_value', { n: usage.step }) }}</span>
        </div>
        <div v-if="timeText" class="tip-row">
          <span class="tip-k">{{ $t('chat.usage_time') }}</span>
          <span class="tip-v">{{ timeText }}</span>
        </div>
      </template>
      <div v-else class="tip-empty">{{ $t('chat.usage_empty') }}</div>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  cachePercent, formatPercent, formatTokens, formatSignedPercent,
  toneOf, usageDeviation, usageLimit, usagePercent, usageTokens,
} from '../../utils/llmUsage'

const { t } = useI18n()

const props = defineProps({
  /** `llm-usage` 合并后的会话状态；空 = 尚未发生 LLM step（环仍显示，只是空环）。 */
  usage: { type: Object, default: null },
})

const emit = defineEmits(['open-config'])

const pct = computed(() => usagePercent(props.usage))
const limit = computed(() => usageLimit(props.usage))
const used = computed(() => usageTokens(props.usage))
const tone = computed(() => toneOf(pct.value))
const isEstimate = computed(() => !(Number(props.usage && props.usage.prompt_tokens) > 0))
const usedText = computed(() => formatTokens(used.value))
const deviation = computed(() => usageDeviation(props.usage))
const cachedPct = computed(() => cachePercent(props.usage))
const sweep = computed(() => {
  if (pct.value == null) return 0
  // 最小可见弧（0.1% 也要能看见“有请求发生”）；满环不超出 100。
  return Math.max(1.5, Math.min(100, pct.value))
})
const ariaText = computed(() => props.usage
  ? t('chat.usage_aria', {
    used: usedText.value,
    max: limit.value ? formatTokens(limit.value) : t('chat.usage_unknown'),
  })
  : t('chat.usage_empty'))
const timeText = computed(() => {
  const ts = props.usage && props.usage.ts
  if (!ts) return ''
  const ms = Date.parse(ts)
  return Number.isNaN(ms) ? String(ts) : new Date(ms).toLocaleTimeString()
})

// 未配置上下文窗口时，环本身就是「去配置」入口（tooltip 内另有同义链接）；无用量数据 → 无动作。
function onRingClick() {
  if (!props.usage) return
  if (!limit.value) emit('open-config')
}
</script>

<style scoped>
.usage-ring {
  position: relative;
  /* 直径 ≈ 一行文字高度（无中心文字；始终显示，空数据 = 空环） */
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: default;
  user-select: none;
}
/* 未配置上下文窗口 = 可点击（打开该 LLM 的配置页） */
.usage-ring.is-actionable {
  cursor: pointer;
}
.usage-ring.is-actionable:focus-visible {
  outline: 1px solid var(--accent, #409eff);
  outline-offset: 2px;
  border-radius: 4px;
}
.usage-ring-svg {
  width: 18px;
  height: 18px;
  transform: rotate(-90deg);
}
.usage-ring-track,
.usage-ring-bar {
  fill: none;
  stroke-width: 4;
}
.usage-ring-track {
  stroke: var(--border, #e5e6eb);
  opacity: 0.7;
}
.usage-ring-bar {
  stroke-linecap: round;
  transition: stroke-dasharray 0.25s ease, stroke 0.2s ease;
}
.tone-normal .usage-ring-bar { stroke: var(--accent, #409eff); }
.tone-warn .usage-ring-bar { stroke: var(--warning, #e6a23c); }
.tone-danger .usage-ring-bar { stroke: var(--danger, #f56c6c); }
.tone-muted .usage-ring-bar { stroke: var(--text-muted, #999); }

/* 悬停明细：向上展开（工具栏在底部，避免被视口裁切） */
.usage-ring-tip {
  display: none;
  position: absolute;
  bottom: calc(100% + 8px);
  left: 0;
  z-index: 30;
  min-width: 220px;
  max-width: 300px;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-elevated, var(--bg-panel, #fff));
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.16);
  font-size: 11px;
  line-height: 1.55;
  color: var(--text-primary, #333);
  white-space: normal;
}
.usage-ring:hover .usage-ring-tip,
.usage-ring:focus-within .usage-ring-tip {
  display: block;
}
/* 悬停桥：tooltip 与环之间有 8px 间隙，移动鼠标穿过去会丢失 hover → 用透明伪元素填满间隙
   （伪元素属于 tooltip，指针经过它时 `.usage-ring:hover` 仍成立）。 */
.usage-ring-tip::before {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: -8px;
  height: 8px;
}
.tip-title {
  font-weight: 600;
  margin-bottom: 4px;
  color: var(--text-primary, #333);
}
.tip-empty {
  color: var(--text-muted, #999);
  font-size: 11px;
}
.tip-row {
  display: flex;
  gap: 10px;
  justify-content: space-between;
}
.tip-k {
  color: var(--text-muted, #999);
  flex: 0 0 auto;
}
.tip-v {
  text-align: right;
  word-break: break-all;
}
.tip-note {
  color: var(--text-muted, #999);
  font-size: 10.5px;
}
.tip-warn .tip-v {
  color: var(--warning, #e6a23c);
}
/* tooltip 内的「去配置」链接（未配置上下文窗口时点击 → 打开该 LLM 编辑框） */
.tip-link {
  margin-left: auto;
  padding: 0;
  border: 0;
  background: none;
  color: var(--accent, #409eff);
  font: inherit;
  font-size: 11px;
  cursor: pointer;
  text-decoration: underline;
  text-underline-offset: 2px;
}
.tip-link:hover {
  opacity: 0.8;
}
</style>
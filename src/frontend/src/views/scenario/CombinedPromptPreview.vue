<template>
  <div class="combined-preview">
    <div class="preview-hint">{{ $t('scenario.preview.hint') }}</div>

    <!-- 全局层：后端代码写死（身份 / 运行环境）——前端不编造文案，仅标注以后端为准 -->
    <section class="preview-layer">
      <div class="layer-title">{{ $t('scenario.preview.global_layer') }}</div>
      <div class="layer-note">{{ $t('scenario.preview.global_backend_note') }}</div>
    </section>

    <!-- 场景层 = description + 代码按 agents 自动拼接的成员段（25 §3） -->
    <section class="preview-layer">
      <div class="layer-title">{{ $t('scenario.preview.scenario_layer') }}</div>
      <pre class="layer-body">{{ layers.scenario.content || $t('scenario.preview.empty') }}</pre>
    </section>

    <!-- agent 层 = 当前 agent 的 prompt（主 agent 为默认取用者） -->
    <section class="preview-layer">
      <div class="layer-title">
        {{ $t('scenario.preview.agent_layer') }}
        <span v-if="layers.agent.name" class="layer-title-agent">{{ layers.agent.name }}</span>
      </div>
      <pre class="layer-body">{{ layers.agent.content || $t('scenario.preview.empty') }}</pre>
    </section>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { buildPromptLayers } from '../../utils/promptLayers'

const props = defineProps({
  // 场景描述（场景层首段）
  description: { type: String, default: '' },
  // 场景 agents（成员段来源）
  agents: { type: Array, default: () => [] },
  // 当前 agent（agent 层；缺省取主 agent）
  agent: { type: Object, default: null },
})

// 只读展示：按 25 §3 拼好的三层结果（全局 / 场景 / agent 分别可见）
const layers = computed(() => buildPromptLayers({
  description: props.description,
  agents: props.agents,
  agent: props.agent,
}))
</script>

<style scoped>
.combined-preview {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 12px 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.preview-hint {
  flex-shrink: 0;
  font-size: 11px;
  line-height: 1.5;
  color: var(--text-secondary, #666);
  padding: 6px 8px;
  border: 1px solid var(--border, #dcdfe6);
  border-radius: var(--border-radius, 4px);
  background: var(--bg-secondary, #fafafa);
}

.preview-layer {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.layer-title {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--text-secondary, #666);
  display: flex;
  align-items: center;
  gap: 6px;
}

.layer-title-agent {
  font-weight: 500;
  color: var(--text-muted, #999);
}

.layer-note {
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-muted, #999);
  padding: 6px 8px;
  border: 1px dashed var(--border, #dcdfe6);
  border-radius: var(--border-radius, 4px);
}

.layer-body {
  margin: 0;
  padding: 8px 10px;
  min-height: 40px;
  font-family: inherit;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--text-primary, #333);
  background: var(--bg-primary, #fff);
  border: 1px solid var(--border, #dcdfe6);
  border-radius: var(--border-radius, 4px);
}
</style>

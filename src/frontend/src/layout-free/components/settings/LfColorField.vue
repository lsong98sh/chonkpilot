<template>
  <div class="color-field">
    <div class="cf-row">
      <!-- 取色器：视觉上与自定义输入联动 -->
      <label class="cf-swatch-wrap" :title="model || '跟随主题（无背景色）'">
        <input class="cf-native" type="color" :value="nativeValue" @input="onNative" />
        <span class="cf-swatch" :style="{ background: model || 'transparent' }">
          <span v-if="!model" class="cf-slash" />
        </span>
      </label>
      <input
        class="cf-input"
        type="text"
        :value="model ?? ''"
        :placeholder="placeholder"
        @input="onText"
      />
      <button class="cf-clear" title="清除（跟随主题）" v-mq:[EventNames.containerClick].click="() => emitColor('')">
        <LfIcon name="x" :size="12" />
      </button>
    </div>
    <div class="cf-presets">
      <button
        v-for="c in presets"
        :key="c.value"
        class="cf-chip"
        :class="{ active: (model ?? '') === c.value }"
        :style="{ background: c.value || 'transparent' }"
        :title="c.label"
        v-mq:[EventNames.containerClick].click="() => emitColor(c.value)"
      >
        <span v-if="!c.value" class="cf-slash" />
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { EventNames } from '../../events/event-names'

/**
 * 背景色选择：预设色块 + 取色器 + 手写值（任意 CSS 颜色）+ 清除。
 * 空字符串 = 跟随主题（不写背景）。
 */
const props = defineProps({
  /** 当前颜色（'' = 跟随主题） */
  model: { type: String, default: '' },
  placeholder: { type: String, default: '跟随主题，或填 #fff / var(--lf-bar)' },
})

const emit = defineEmits(['update:modelValue', 'color'])

const presets = [
  { value: '', label: '跟随主题' },
  { value: '#ffffff', label: '白' },
  { value: '#fbfcfe', label: '浅灰白' },
  { value: '#f3f5f9', label: '浅灰' },
  { value: '#e8efff', label: '淡蓝' },
  { value: '#eaf7ef', label: '淡绿' },
  { value: '#fff4e5', label: '淡橙' },
  { value: '#fdeaea', label: '淡红' },
  { value: '#2b3446', label: '深灰蓝' },
]

/** 原生取色器只认 #rrggbb，别的值先兜底成白 */
const nativeValue = computed(() => (/^#[0-9a-f]{6}$/i.test(props.model) ? props.model : '#ffffff'))

/**
 * 同时发 update:modelValue 与 color 两个事件。
 * 用 `color`（全小写、无冒号）作为**主用**事件，避免模板里
 * `@update:model-value` 这类带冒号的大小写匹配歧义。
 */
function emitColor(v) {
  emit('update:modelValue', v)
  emit('color', v)
}

function onNative(e) {
  emitColor(e.target.value)
}

function onText(e) {
  emitColor(e.target.value)
}
</script>

<style scoped>
.color-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.cf-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.cf-swatch-wrap {
  position: relative;
  flex: none;
  width: 26px;
  height: 24px;
  cursor: pointer;
  display: inline-flex;
}

.cf-native {
  position: absolute;
  inset: 0;
  opacity: 0;
  cursor: pointer;
  border: none;
  padding: 0;
}

.cf-swatch {
  position: relative;
  flex: none;
  width: 26px;
  height: 24px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}

.cf-input {
  flex: 1;
  min-width: 0;
  height: 24px;
  padding: 0 7px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  font-size: 12px;
}

.cf-clear {
  flex: none;
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  background: #fff;
  color: var(--lf-text-weak);
  cursor: pointer;
}

.cf-clear:hover {
  border-color: var(--lf-accent);
  color: var(--lf-accent);
}

.cf-presets {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.cf-chip {
  position: relative;
  width: 22px;
  height: 22px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  cursor: pointer;
  overflow: hidden;
  padding: 0;
}

.cf-chip.active {
  border-color: var(--lf-accent);
  box-shadow: 0 0 0 2px var(--lf-accent-soft);
}

.cf-slash {
  position: absolute;
  inset: 0;
  pointer-events: none;
}

.cf-slash::after {
  content: '';
  position: absolute;
  left: -2px;
  right: -2px;
  top: 50%;
  height: 1px;
  background: #d05555;
  transform: rotate(-20deg);
}
</style>

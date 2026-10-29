<template>
  <span class="ck-file-icon" :style="boxStyle" />
</template>

<script setup>
import { computed } from 'vue'
import { fileIconUrl } from './fileIcons'

const props = defineProps({
  // 文件名（含扩展名）或图标名（如 'md'），由 fileIcons.js 解析
  name: { type: String, default: '' },
  size: { type: [Number, String], default: 14 },
})

const url = computed(() => fileIconUrl(props.name))

// 图标源 viewBox = 72x96（宽高比 0.75），按高度对齐行高
const boxStyle = computed(() => ({
  width: Math.round(Number(props.size) * 0.75) + 'px',
  height: Number(props.size) + 'px',
  backgroundImage: url.value ? `url("${url.value}")` : 'none',
}))
</script>

<style scoped>
.ck-file-icon {
  display: inline-block;
  flex-shrink: 0;
  background-size: contain;
  background-position: center;
  background-repeat: no-repeat;
  /* 亮度/饱和度可调：改 --ck-file-icon-filter 即可（默认不改色） */
  filter: var(--ck-file-icon-filter, none);
}
/* 暗色 / nord 主题下图标略提亮，避免彩色图标发闷 */
[data-theme="dark"] .ck-file-icon,
[data-theme="nord"] .ck-file-icon {
  --ck-file-icon-filter: brightness(1.15) saturate(1.05);
}
</style>

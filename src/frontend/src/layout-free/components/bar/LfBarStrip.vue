<template>
  <div class="bar-strip" :class="[`bar-strip-${position}`, { vertical: isVertical }]">
    <LfBarZones
      :name="zonesName"
      :items="items"
      :empty-text="emptyText"
      :background="background"
      :zone-background="zoneBackground"
      :default-side-width="defaultSideWidth"
    >
      <template #right-extra>
        <slot name="extra" />
      </template>
    </LfBarZones>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import LfBarZones from './LfBarZones.vue'

/**
 * 一条可放在**上 / 下 / 左 / 右**四边的工具条。
 * 内容与「顶部工具栏 / 状态栏」完全一致（三个区 + 背景色），
 * 因此抽屉 / 区域 / 容器的四向工具栏直接复用它。
 *
 * 竖直时（左 / 右）：条目自动变成「仅图标、竖排、紧凑」形态（CSS 覆盖）。
 */
const props = defineProps({
  /** 条目列表（含 zone 字段） */
  items: { type: Array, default: () => [] },
  /** 位置：top | bottom | left | right */
  position: { type: String, default: 'top' },
  /** 区宽持久化用的名字（同一个工具栏在不同位置可各自记忆宽度） */
  name: { type: String, required: true },
  emptyText: { type: String, default: '' },
  /** 整条栏背景色（'' = 跟随主题） */
  background: { type: String, default: '' },
  /** 每个区背景色 { left, center, right } */
  zoneBackground: { type: Object, default: () => ({ left: '', center: '', right: '' }) },
  defaultSideWidth: { type: Number, default: 200 },
})

const isVertical = computed(() => props.position === 'left' || props.position === 'right')
const zonesName = computed(() => `${props.name}-${props.position}`)
</script>

<style scoped>
.bar-strip {
  flex: none;
  min-width: 0;
  min-height: 0;
  background: var(--lf-bar);
}

/* 上下：整条 100% 宽，高度贴合内容 */
.bar-strip-top,
.bar-strip-bottom {
  width: 100%;
  height: 30px;
}

.bar-strip-top {
  border-bottom: 1px solid var(--lf-border);
}

.bar-strip-bottom {
  border-top: 1px solid var(--lf-border);
}

/* 左右：整条 100% 高，宽度贴合内容 */
.bar-strip-left,
.bar-strip-right {
  height: 100%;
  width: 34px;
}

.bar-strip-left {
  border-right: 1px solid var(--lf-border);
}

.bar-strip-right {
  border-left: 1px solid var(--lf-border);
}

.bar-strip .bar-zones {
  width: 100%;
  height: 100%;
}
</style>

<style>
/* 竖直工具条：条目竖排、图标居中、隐藏文字（非 scoped 以便覆盖子组件样式） */
.bar-strip.vertical .bar-zone {
  flex-direction: column;
  justify-content: flex-start;
  align-items: center;
  gap: 2px;
  padding: 3px 0;
  overflow-x: hidden;
  overflow-y: auto;
}

.bar-strip.vertical .bar-item-shell {
  width: 100%;
  justify-content: center;
}

.bar-strip.vertical .bar-btn {
  width: 26px;
  padding: 0;
  justify-content: center;
}

.bar-strip.vertical .bar-btn .lbl {
  display: none;
}

.bar-strip.vertical .bar-btn .caret {
  display: none;
}

.bar-strip.vertical .bar-empty {
  writing-mode: vertical-rl;
  padding: 6px 0;
}
</style>

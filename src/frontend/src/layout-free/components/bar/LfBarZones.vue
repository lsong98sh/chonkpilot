<template>
  <div ref="rootRef" class="bar-zones" :style="barStyle">
    <LfSplitPanel direction="horizontal" :panes="panes" @size-changed="onSizeChanged">
      <template #pane-0>
        <div class="bar-zone zone-left" :style="zoneStyle('left')">
          <LfBarItem v-for="item in itemsOf('left')" :key="item.id" :item="item" align="left" />
          <span v-if="!itemsOf('left').length && emptyText" class="bar-empty">{{ emptyText }}</span>
        </div>
      </template>
      <template #pane-1>
        <div class="bar-zone zone-center" :style="zoneStyle('center')">
          <LfBarItem v-for="item in itemsOf('center')" :key="item.id" :item="item" align="left" />
        </div>
      </template>
      <template #pane-2>
        <div class="bar-zone zone-right" :style="zoneStyle('right')">
          <LfBarItem v-for="item in itemsOf('right')" :key="item.id" :item="item" align="right" />
          <!-- 附加插槽（如「内容设置」按钮）永远贴在整条栏的最右边 -->
          <slot name="right-extra" />
        </div>
      </template>
    </LfSplitPanel>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, ref } from 'vue'
import LfBarItem from './LfBarItem.vue'

/**
 * 栏（工具栏 / 状态栏 / 自定义工具栏）三个区：靠左区 · 中间区 · 靠右区。
 * 区之间可拖拽调宽（1px 发丝线 + 5px 命中区，见 LfSplitPanel）；
 * 宽度按区名持久化到 localStorage，刷新后保持。
 *
 * 背景色：`background` 是整条栏，`zoneBackground` 是每个区（盖住整条栏的颜色）。
 */
const props = defineProps({
  /** 栏标识，用于持久化区宽 */
  name: { type: String, required: true },
  /** 条目列表（含 zone 字段） */
  items: { type: Array, required: true },
  /** 运行时附加条目（插件状态栏；不写入布局文档） */
  extraItems: { type: Array, default: () => [] },
  emptyText: { type: String, default: '' },
  /** 整条栏背景色（'' = 跟随主题） */
  background: { type: String, default: '' },
  /** 每个区的背景色 { left, center, right }（'' = 跟随栏） */
  zoneBackground: { type: Object, default: () => ({ left: '', center: '', right: '' }) },
  /** 靠左区 / 靠右区默认宽度 */
  defaultSideWidth: { type: Number, default: 260 },
  /** 靠左区默认宽度（缺省回落 defaultSideWidth） */
  defaultLeftWidth: { type: Number, default: 0 },
  /** 靠右区默认宽度（缺省回落 defaultSideWidth） */
  defaultRightWidth: { type: Number, default: 0 },
  minSideWidth: { type: Number, default: 60 },
  maxSideWidth: { type: Number, default: 900 },
})

const STORE_PREFIX = 'layout-free:bar-zones:'
const rootRef = ref(null)

/** 整条栏背景（空 = 不写，跟随主题） */
const barStyle = computed(() => (props.background ? { background: props.background } : {}))

/** 某个区的背景色 */
function zoneStyle(zone) {
  const c = props.zoneBackground && props.zoneBackground[zone]
  return c ? { background: c } : {}
}

function loadWidths() {
  try {
    const raw = localStorage.getItem(STORE_PREFIX + props.name)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

const saved = loadWidths()
const leftWidth = ref(saved?.left ?? (props.defaultLeftWidth > 0 ? props.defaultLeftWidth : props.defaultSideWidth))
const rightWidth = ref(saved?.right ?? (props.defaultRightWidth > 0 ? props.defaultRightWidth : props.defaultSideWidth))

function itemsOf(zone) {
  return [...props.items, ...props.extraItems].filter(
    (i) => i.visible !== false && (i.zone ?? 'left') === zone,
  )
}

const panes = computed(() => [
  { id: 'left', size: leftWidth.value, min: props.minSideWidth, max: props.maxSideWidth },
  { id: 'center', flex: true, min: 40 },
  { id: 'right', size: rightWidth.value, min: props.minSideWidth, max: props.maxSideWidth },
])

/** 拖拽结束：按实测宽度回写并持久化 */
function onSizeChanged() {
  const zones = rootRef.value?.querySelectorAll('.bar-zone')
  if (!zones || zones.length !== 3) return
  leftWidth.value = Math.round(zones[0].getBoundingClientRect().width)
  rightWidth.value = Math.round(zones[2].getBoundingClientRect().width)
  persist()
}

let timer = null
function persist() {
  clearTimeout(timer)
  timer = setTimeout(() => {
    try {
      localStorage.setItem(
        STORE_PREFIX + props.name,
        JSON.stringify({ left: leftWidth.value, right: rightWidth.value }),
      )
    } catch {
      /* 忽略存储失败 */
    }
  }, 300)
}

onBeforeUnmount(() => clearTimeout(timer))
</script>

<style scoped>
.bar-zones {
  width: 100%;
  height: 100%;
  min-width: 0;
}

.bar-zone {
  display: flex;
  align-items: center;
  gap: 2px;
  min-width: 0;
  height: 100%;
  overflow: hidden;
}

.zone-left {
  justify-content: flex-start;
}

.zone-center {
  justify-content: center;
}

.zone-right {
  justify-content: flex-end;
}

.bar-empty {
  color: var(--lf-text-weak);
  font-size: 12px;
  padding: 0 6px;
  white-space: nowrap;
}

/* 栏的右边附加插槽（如「内容设置」）永远不被挤掉 */
.bar-zone.zone-right > :last-child:not(.bar-item-shell) {
  flex: none;
}
</style>

<style>
/* 栏里三个区之间的 resizer 不显示边框色（只保留 hover / 拖拽的命中反馈） */
.bar-zones .split-resizer {
  background: transparent;
}
</style>

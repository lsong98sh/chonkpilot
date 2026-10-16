<template>
  <div class="bar-item-shell">
    <!-- view：栏里内嵌一个 view，宽 / 高可配 -->
    <div v-if="item.kind === 'view'" class="bar-view" :class="{ grow: item.grow }" :style="viewStyle" :title="item.tooltip || item.label">
      <LfViewHost :view-ref="viewRef" />
    </div>

    <!-- 按钮 -->
    <button
      v-else-if="item.kind === 'button'"
      class="bar-btn"
      :class="[`style-${item.style}`]"
      :title="item.tooltip || item.label"
      :disabled="item.disabled"
      v-mq:[topic].click="() => payload"
    >
      <LfIcon v-if="item.style !== 'text'" :name="item.icon ?? ''" :size="15" fallback="·" />
      <span v-if="item.style !== 'icon'" class="lbl">{{ item.label }}</span>
    </button>

    <!-- 菜单 -->
    <LfDropdown v-else :name="`bar-${item.id}`" :min-width="200" :align="align">
      <template #trigger>
        <button
          class="bar-btn"
          :class="[`style-${item.style}`, { active: isOpen }]"
          :title="item.tooltip || item.label"
          :disabled="item.disabled"
          v-mq:[EventNames.dropdownToggle].click="{ key: `bar-${item.id}` }"
        >
          <LfIcon v-if="item.style !== 'text'" :name="item.icon ?? ''" :size="15" fallback="·" />
          <span v-if="item.style !== 'icon'" class="lbl">{{ item.label }}</span>
          <LfIcon class="caret" name="chevron-down" :size="11" />
        </button>
      </template>
      <LfBarMenuTree :nodes="item.children ?? []" />
    </LfDropdown>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import LfDropdown from '../../components/ui/LfDropdown.vue'
import LfBarMenuTree from './LfBarMenuTree.vue'
import LfViewHost from '../../views/LfViewHost.vue'
import { EventNames } from '../../events/event-names'
import { commandToMq } from '../../utils/commands'
import { useEditStore } from '../../stores/edit'

const props = defineProps({
  /** 栏条目（button | menu | view） */
  item: { type: Object, required: true },
  /** 弹层对齐方向：left | right */
  align: { type: String, default: 'left' },
})

const edit = useEditStore()
const isOpen = computed(() => edit.dropdowns[`bar-${props.item.id}`] === true)

const route = computed(() => commandToMq(props.item.command))
const topic = computed(() => route.value?.topic ?? EventNames.containerClick)
const payload = computed(() => route.value?.payload ?? null)

/* ── view 条目 ── */
const viewRef = computed(() => ({
  viewKey: props.item.viewKey,
  instanceKey: props.item.instanceKey ?? null,
}))

/** 尺寸：给了宽 / 高就定尺；grow = true 时撑满所在区（不写尺寸） */
const viewStyle = computed(() => {
  if (props.item.grow) return {}
  const s = {}
  const w = Number(props.item.width)
  const h = Number(props.item.height)
  if (Number.isFinite(w) && w > 0) s.width = `${w}px`
  if (Number.isFinite(h) && h > 0) s.height = `${h}px`
  return s
})
</script>

<style scoped>
.bar-item-shell {
  display: inline-flex;
  align-items: center;
}

/* 栏里内嵌的 view（如搜索框）：定尺或撑满 */
.bar-view {
  display: flex;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}

.bar-view.grow {
  flex: 1;
}

.bar-view > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.bar-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 26px;
  padding: 0 8px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: transparent;
  color: var(--lf-text);
  white-space: nowrap;
}

.bar-btn:hover:not(:disabled) {
  background: var(--lf-accent-soft);
  border-color: #cddcff;
}

.bar-btn.active {
  background: var(--lf-accent-soft);
  border-color: var(--lf-accent);
}

.bar-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.bar-btn .caret {
  opacity: 0.65;
}
</style>

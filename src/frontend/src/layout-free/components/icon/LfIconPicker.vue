<template>
  <LfDropdown :name="`icon-${nameKey}`" :min-width="300" :max-height="420" align="left">
    <template #trigger>
      <button
        class="icon-picker-trigger"
        :title="value ? `图标：${value}` : '选择图标'"
        v-mq:[EventNames.dropdownToggle].click="{ key: `icon-${nameKey}` }"
      >
        <LfIcon :name="value" :size="15" fallback="·" />
        <span class="caret">▾</span>
      </button>
    </template>

    <div class="icon-popup">
      <div class="ip-head">
        <input
          type="text"
          class="ip-search"
          placeholder="搜索图标（英文名，如 layout / folder / chat）"
          :value="query"
          @input="query = $event.target.value"
        />
        <span class="ip-count">{{ filtered.length }}</span>
        <button class="ip-clear" title="清除图标" v-mq:[EventNames.containerClick].click="() => pick('')">✕</button>
      </div>

      <div class="ip-grid">
        <button
          v-for="n in filtered"
          :key="n"
          class="ip-cell"
          :class="{ active: n === value }"
          :title="n"
          v-mq:[EventNames.containerClick].click="() => pick(n)"
        >
          <LfIcon :name="n" :size="16" fallback="·" />
        </button>
      </div>
      <div v-if="!filtered.length" class="ip-empty">没有匹配的图标</div>
    </div>
  </LfDropdown>
</template>

<script setup>
import { computed, ref } from 'vue'
import LfDropdown from '../../components/ui/LfDropdown.vue'
import { getLayoutDeps } from '../../deps.js'
import { useEditStore } from '../../stores/edit'
import { EventNames } from '../../events/event-names'

defineProps({
  /** 当前图标值（图标名） */
  value: { type: String, default: '' },
  /** 下拉唯一 key 后缀（一般传条目 id，保证多个选择器互不干扰） */
  nameKey: { type: String, required: true },
})

const emit = defineEmits(['pick'])

const edit = useEditStore()
const query = ref('')

/** 可选图标名清单由宿主注入（components/icon/icons.js，去重：不再内联 icon-set.js） */
const filtered = computed(() => {
  const names = getLayoutDeps().iconNames ?? []
  const q = query.value.trim().toLowerCase()
  return q ? names.filter((n) => n.includes(q)) : names
})

function pick(name) {
  emit('pick', name)
  // 选完即收起选择器（与菜单条目一致的交互）
  edit.closeDropdowns()
}
</script>

<style scoped>
.icon-picker-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 3px;
  height: 25px;
  min-width: 44px;
  padding: 0 6px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  background: #fff;
  color: var(--lf-text);
  cursor: pointer;
  flex: none;
}

.icon-picker-trigger:hover {
  border-color: var(--lf-accent);
  color: var(--lf-accent);
}

.icon-picker-trigger .caret {
  font-size: 9px;
  opacity: 0.7;
}

.icon-popup {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 380px;
  overflow: auto;
}

.ip-head {
  display: flex;
  gap: 4px;
  align-items: center;
  position: sticky;
  top: 0;
  background: #fff;
  padding-bottom: 4px;
  z-index: 1;
}

.ip-search {
  flex: 1;
  height: 24px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  padding: 0 6px;
  font-size: 12px;
}

.ip-count {
  font-size: 11px;
  color: var(--lf-text-weak);
  min-width: 22px;
  text-align: right;
}

.ip-clear {
  width: 24px;
  height: 24px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  background: #fff;
  color: var(--lf-text-weak);
  cursor: pointer;
}

.ip-clear:hover {
  border-color: #d33;
  color: #d33;
}

/* 每行固定 8 个，点击即选 */
.ip-grid {
  display: grid;
  grid-template-columns: repeat(8, minmax(0, 1fr));
  gap: 4px;
}

.ip-cell {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 30px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: transparent;
  color: var(--lf-text);
  cursor: pointer;
}

.ip-cell:hover {
  background: var(--lf-accent-soft);
  color: var(--lf-accent);
}

.ip-cell.active {
  border-color: var(--lf-accent);
  background: var(--lf-accent-soft);
  color: var(--lf-accent);
}

.ip-empty {
  font-size: 12px;
  color: var(--lf-text-weak);
  padding: 8px;
  text-align: center;
}
</style>

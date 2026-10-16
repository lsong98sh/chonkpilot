<template>
  <LfDropdown :name="`icon-${nameKey}`" :min-width="316" :max-height="380" align="left">
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

    <div class="icon-picker">
      <div class="ip-head">
        <input
          type="text"
          class="ip-search"
          placeholder="搜索图标（英文名，如 layout / folder / chat）"
          :value="query"
          @input="query = $event.target.value"
        />
        <button class="ip-clear" title="清除图标" v-mq:[EventNames.containerClick].click="() => pick('')">✕</button>
      </div>

      <div v-for="g in filteredGroups" :key="g.group" class="ip-group">
        <div class="ip-group-title">{{ g.group }}</div>
        <div class="ip-grid">
          <button
            v-for="n in g.icons"
            :key="n"
            class="ip-cell"
            :class="{ active: n === value }"
            :title="n"
            v-mq:[EventNames.containerClick].click="() => pick(n)"
          >
            <LfIcon :name="n" :size="17" />
          </button>
        </div>
      </div>
      <div v-if="!filteredGroups.length" class="ip-empty">没有匹配的图标</div>

      <div class="ip-foot">
        <span class="ip-hint">也可直接填 emoji / 图片地址 / &lt;svg&gt; 串</span>
      </div>
    </div>
  </LfDropdown>
</template>

<script setup>
import { computed, ref } from 'vue'
import LfDropdown from '../../components/ui/LfDropdown.vue'
import { getLayoutDeps } from '../../deps.js'
import { useEditStore } from '../../stores/edit'
import { EventNames } from '../../events/event-names'

const props = defineProps({
  /** 当前图标值（名称 / svg / 图片地址 / emoji） */
  value: { type: String, default: '' },
  /** 下拉唯一 key 后缀（一般传条目 id，保证多个选择器互不干扰） */
  nameKey: { type: String, required: true },
})

const emit = defineEmits(['pick'])

const edit = useEditStore()
const query = ref('')

const filteredGroups = computed(() => {
  // 图标名清单由宿主注入（去重：不再内联 icon-set.js）
  const names = getLayoutDeps().iconNames ?? []
  const groups = [{ group: '图标', icons: names }]
  const q = query.value.trim().toLowerCase()
  if (!q) return groups
  return groups.map((g) => ({ ...g, icons: g.icons.filter((n) => n.includes(q)) })).filter(
    (g) => g.icons.length,
  )
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

.icon-picker {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 340px;
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

.ip-group-title {
  font-size: 11px;
  color: var(--lf-text-weak);
  margin: 4px 0 2px;
  font-weight: 600;
}

.ip-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(28px, 1fr));
  gap: 3px;
}

.ip-cell {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
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

.ip-foot {
  border-top: 1px solid var(--lf-border);
  padding-top: 4px;
  position: sticky;
  bottom: 0;
  background: #fff;
}

.ip-hint {
  font-size: 11px;
  color: var(--lf-text-weak);
}
</style>

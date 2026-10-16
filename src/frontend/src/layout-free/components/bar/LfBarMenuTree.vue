<template>
  <div class="bar-menu-tree">
    <template v-for="node in visibleNodes" :key="node.id">
      <!-- 有子节点：子菜单（子菜单不需要命令）。用 anchor="right" 真正做成**侧面飞框**，
           挂在触发条目右侧（右侧空间不够时 LfDropdown 会自动翻到左侧）。 -->
      <LfDropdown
        v-if="isSubmenu(node)"
        :name="`menu-${node.id}`"
        :min-width="180"
        anchor="right"
        submenu
        open-on-hover
      >
        <template #trigger>
          <button
            class="menu-row has-child"
            :class="{ active: edit.dropdowns[`menu-${node.id}`] }"
            v-mq:[EventNames.dropdownToggle].click="(e) => ({ key: `menu-${node.id}`, keep: openAncestors(e) })"
          >
            <LfIcon class="ico" :name="node.icon || 'folder-open'" :size="13" />
            <span class="lbl">{{ node.label }}</span>
            <LfIcon class="arrow" name="chevron-right" :size="11" />
          </button>
        </template>
        <LfBarMenuTree :nodes="node.children" />
      </LfDropdown>

      <!-- 叶子：有命令 → 发布 mq 并关闭下拉；无命令 → 禁用 -->
      <button
        v-else
        class="menu-row"
        :class="{ disabled: !mqRoute(node) }"
        :disabled="!mqRoute(node)"
        v-mq:[mqTopic(node)].click="() => onLeafClick(node)"
      >
        <LfIcon class="ico" :name="node.icon || 'dot'" :size="13" />
        <span class="lbl">{{ node.label }}</span>
      </button>
    </template>
    <div v-if="!visibleNodes.length" class="menu-empty">（空菜单）</div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import LfDropdown from '../../components/ui/LfDropdown.vue'
import { EventNames } from '../../events/event-names'
import { commandToMq } from '../../utils/commands'
import { useEditStore } from '../../stores/edit'

defineOptions({ name: 'LfBarMenuTree' })

const props = defineProps({
  /** 菜单条目树 */
  nodes: { type: Array, required: true },
})

const edit = useEditStore()

const visibleNodes = computed(() => props.nodes.filter((n) => n.visible !== false))

/** 有子节点即子菜单（不需要命令） */
function isSubmenu(node) {
  return Array.isArray(node.children) && node.children.length > 0
}

function mqRoute(node) {
  return commandToMq(node.command)
}

function mqTopic(node) {
  return mqRoute(node)?.topic ?? EventNames.containerClick
}

function mqPayload(node) {
  return mqRoute(node)?.payload ?? null
}

/** 点击叶子条目：先收起整条下拉，再执行命令 */
function onLeafClick(node) {
  edit.closeDropdowns()
  return mqPayload(node)
}

/** 当前展开的祖先下拉链：沿 DOM 祖先链收集 data-dropdown-name（展开子菜单时保持父菜单不关） */
function openAncestors(e) {
  const out = []
  let el = e && e.target ? e.target.parentElement : null
  while (el) {
    const name = el.dataset ? el.dataset.dropdownName : ''
    if (name) out.push(name)
    el = el.parentElement
  }
  return out
}
</script>

<style scoped>
.bar-menu-tree {
  display: flex;
  flex-direction: column;
}

.menu-row {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 5px 8px;
  border: none;
  border-radius: 5px;
  background: transparent;
  text-align: left;
  color: var(--lf-text);
  font-size: 13px;
  cursor: pointer;
}

.menu-row:hover:not(:disabled),
.menu-row.active {
  background: var(--lf-accent-soft);
  color: var(--lf-accent);
}

.menu-row:disabled,
.menu-row.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.menu-row .lbl {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.menu-row .arrow {
  font-size: 10px;
  color: var(--lf-text-weak);
}

.menu-empty {
  color: var(--lf-text-weak);
  font-size: 11px;
  padding: 4px 8px;
}
</style>

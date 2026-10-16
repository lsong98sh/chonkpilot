<template>
  <div class="toolbar-editor">
    <!-- 左：具名工具栏列表 -->
    <aside class="te-left">
      <div class="pane-head">
        <span class="ico"><LfIcon name="wrench" :size="13" /></span>
        <b>自定义工具栏</b>
        <span class="grow" />
        <span class="lf-tag">{{ toolbars.length }} 个</span>
      </div>
      <div class="pane-body">
        <div
          v-for="(t, i) in toolbars"
          :key="t.id"
          class="tb-row"
          :class="{ selected: t.id === selectedId }"
          v-mq:[EventNames.toolbarSelect].click="{ id: t.id }"
        >
          <LfIcon class="ico" name="wrench" :size="13" />
          <span class="lbl">{{ t.label }}</span>
          <span class="cnt">{{ t.items.length }}</span>
          <button class="mini" title="上移" :disabled="i === 0" v-mq:[EventNames.toolbarMove].click.stop="{ id: t.id, delta: -1 }">
            ↑
          </button>
          <button
            class="mini"
            title="下移"
            :disabled="i === toolbars.length - 1"
            v-mq:[EventNames.toolbarMove].click.stop="{ id: t.id, delta: 1 }"
          >
            ↓
          </button>
          <button class="mini danger" title="删除" v-mq:[EventNames.toolbarRemove].click.stop="{ id: t.id }">
            ✕
          </button>
        </div>
        <div v-if="!toolbars.length" class="zone-empty">
          （还没有自定义工具栏）
        </div>
      </div>
      <div class="pane-foot">
        <button class="lf-btn primary" v-mq:[EventNames.toolbarAdd].click="{ label: '自定义工具栏' }">
          <LfIcon name="plus" :size="13" /> 新建工具栏
        </button>
      </div>
    </aside>

    <div class="te-right">
      <template v-if="current">
        <section class="te-detail">
          <div class="pane-head">
            <span class="ico"><LfIcon name="sliders-horizontal" :size="13" /></span>
            <b>工具栏设置</b>
            <span class="grow" />
            <span class="mono">{{ current.id }}</span>
          </div>
          <div class="pane-body">
            <div class="lf-field">
              <label>名称</label>
              <input
                type="text"
                :value="current.label"
                placeholder="例如：文件工具"
                @input="patch({ label: $event.target.value })"
              />
            </div>
            <p class="tip">
              这个工具栏可以挂到<strong>抽屉的任意一边</strong>，也可以挂到<strong>任意区域的任意一边</strong>
              （内容设置 → 布局编辑 → 区域设置）。
            </p>
            <div class="lf-field">
              <label>被谁引用</label>
              <div v-if="usage.length" class="usage-list">
                <span v-for="u in usage" :key="u" class="usage-chip">{{ u }}</span>
              </div>
              <div v-else class="usage-empty">暂未被引用（可以删除）</div>
            </div>
          </div>
        </section>

        <!-- 下半：条目编辑（与顶部工具栏 / 状态栏完全同一套编辑器） -->
        <section class="te-items">
          <LfBarEditor :bar-key="customBarKey(current.id)" />
        </section>
      </template>

      <div v-else class="te-empty">
        <LfIcon name="wrench" :size="24" />
        <div>左侧新建一个自定义工具栏，然后为它添加按钮 / 菜单。</div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted } from 'vue'
import LfBarEditor from './LfBarEditor.vue'
import { useConfigStore, customBarKey } from '../../stores/config'
import { useEditStore } from '../../stores/edit'
import { EventNames } from '../../events/event-names'
import { getMq } from '../../deps.js'

/** 具名自定义工具栏管理：可挂到抽屉 / 区域的任意一边；条目编辑复用 LfBarEditor */
const config = useConfigStore()
const edit = useEditStore()

const toolbars = computed(() => config.toolbars)
const selectedId = computed(() => edit.selectedToolbarId)
const current = computed(() => toolbars.value.find((t) => t.id === selectedId.value) ?? null)
const usage = computed(() => (current.value ? config.toolbarUsage(current.value.id) : []))

/**
 * 列表变化后保证有选中项。
 * 原为 `watch(toolbars, { immediate: true })` —— 改为挂载兜底 + 订阅端收敛
 * （`lf-toolbar-remove` 已在 `stores/mqCommands` 内选中下一项），不侦听 state。
 */
function ensureSelection() {
  if (!toolbars.value.some((t) => t.id === selectedId.value)) {
    edit.selectToolbar(toolbars.value[0]?.id ?? '')
  }
}
onMounted(ensureSelection)

function patch(obj) {
  if (current.value) getMq().emit(EventNames.toolbarPatch, { id: current.value.id, patch: obj })
}
</script>

<style scoped>
.toolbar-editor {
  display: flex;
  min-height: 0;
  height: 100%;
  width: 100%;
}

.te-left {
  flex: none;
  width: 264px;
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--lf-border);
  background: #fbfcfe;
  min-height: 0;
}

/* 与 LfBarEditor 同款的窗格头 / 体 / 脚（scoped，故在此重复声明） */
.pane-head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  padding: 0 8px;
  border-bottom: 1px solid var(--lf-border);
  background: #fbfcfe;
  font-size: 12px;
}

.pane-head .grow {
  flex: 1;
}

.pane-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 6px 8px;
}

.pane-foot {
  flex: none;
  display: flex;
  gap: 6px;
  padding: 6px;
  border-top: 1px solid var(--lf-border);
  background: #fbfcfe;
}

.zone-empty {
  font-size: 12px;
  color: var(--lf-text-weak);
  padding: 6px 10px;
}

.te-right {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.te-detail {
  flex: none;
  display: flex;
  flex-direction: column;
  border-bottom: 1px solid var(--lf-border);
  /* 用固定 height + min-height 而非 max-height：被兄弟挤压时内容不会溢出重叠 */
  height: 208px;
  min-height: 208px;
  overflow: hidden;
}

.te-detail .pane-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
}

.te-left .pane-foot {
  flex: none;
}

.te-items {
  flex: 1;
  min-height: 0;
  display: flex;
}

.te-items > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.tb-row {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 30px;
  padding: 0 6px 0 10px;
  border-radius: 6px;
  cursor: pointer;
}

.tb-row:hover {
  background: var(--lf-tree-hover);
}

.tb-row.selected {
  background: var(--lf-select);
  box-shadow: inset 2px 0 0 var(--lf-select-border);
}

.tb-row .ico {
  color: var(--lf-accent);
  display: inline-flex;
}

.tb-row .lbl {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tb-row .cnt {
  font-size: 11px;
  color: var(--lf-text-weak);
}

.tb-row .mini {
  width: 20px;
  height: 20px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--lf-text-weak);
  font-size: 11px;
  line-height: 1;
}

.tb-row .mini:hover:not(:disabled) {
  background: #e2e7f0;
  color: var(--lf-accent);
}

.tb-row .mini.danger:hover {
  background: #ffeaea;
  color: #d33;
}

.te-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--lf-text-weak);
  font-size: 13px;
  padding: 20px;
  text-align: center;
}

.usage-list {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.usage-chip {
  font-size: 11px;
  color: var(--lf-accent);
  background: var(--lf-accent-soft);
  border-radius: 10px;
  padding: 2px 8px;
}

.usage-empty {
  font-size: 11px;
  color: var(--lf-text-weak);
}

.mono {
  font-family: Consolas, monospace;
  font-size: 11px;
  color: var(--lf-text-weak);
}
</style>

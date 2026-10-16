<template>
  <div class="s-drawer-host">
    <aside class="s-drawer">
      <div class="pane-head">
        <span class="ico"><LfIcon name="panel-right" :size="13" /></span>
        <b>具名抽屉</b>
        <span class="grow" />
        <button class="lf-btn" v-mq:[EventNames.drawerAdd].click>＋ 添加抽屉</button>
      </div>

      <div class="pane-body">
        <div
          v-for="(d, i) in config.drawers"
          :key="d.id"
          class="drawer-row"
          :class="{ selected: edit.selectedDrawerId === d.id }"
          v-mq:[EventNames.drawerSelect].click="{ id: d.id }"
        >
          <span class="ico"><LfIcon name="panel-right" :size="13" /></span>
          <span class="lbl">{{ d.label }}</span>
          <span class="pos">{{ positionLabel(d.position) }}</span>
          <button class="mini" title="上移" :disabled="i === 0" v-mq:[EventNames.drawerMove].click.stop="{ id: d.id, delta: -1 }">↑</button>
          <button
            class="mini"
            title="下移"
            :disabled="i === config.drawers.length - 1"
            v-mq:[EventNames.drawerMove].click.stop="{ id: d.id, delta: 1 }"
          >
            ↓
          </button>
          <button class="mini danger" title="删除" v-mq:[EventNames.drawerRemove].click.stop="{ id: d.id }">✕</button>
        </div>
        <div v-if="!config.drawers.length" class="lf-empty">还没有抽屉，点击「添加抽屉」创建。</div>
      </div>
    </aside>

    <section class="s-drawer-detail">
      <div class="pane-head">
        <span class="ico"><LfIcon name="sliders-horizontal" :size="13" /></span>
        <b>抽屉设置</b>
        <span class="grow" />
        <span v-if="current" class="lf-tag">{{ current.id }}</span>
      </div>
      <div class="pane-body">
        <div v-if="!current" class="lf-empty">在左侧选择一个抽屉进行编辑。</div>
        <template v-else>
          <div class="lf-field">
            <label>抽屉名称</label>
            <input type="text" :value="current.label" @input="patch({ label: $event.target.value })" />
          </div>
          <div class="lf-field">
            <label>抽屉图标</label>
            <div class="icon-line">
              <LfIconPicker
                :value="current.icon ?? ''"
                :name-key="`drawer-${current.id}`"
                @pick="(n) => patch({ icon: n })"
              />
              <input
                type="text"
                :value="current.icon ?? ''"
                placeholder="图标名 / emoji / 图片地址"
                @input="patch({ icon: $event.target.value })"
              />
              <span class="icon-preview">
                <LfIcon :name="current.icon ?? ''" :size="16" fallback="—" />
              </span>
            </div>
          </div>
          <div class="lf-field">
            <label>弹出位置</label>
            <div class="seg">
              <button
                v-for="p in positions"
                :key="p.value"
                class="seg-btn"
                :class="{ active: current.position === p.value }"
                v-mq:[EventNames.containerClick].click="() => selectPosition(p.value)"
              >
                {{ p.label }}
              </button>
            </div>
          </div>
          <div class="lf-field">
            <label>尺寸（{{ current.position === 'left' || current.position === 'right' ? '宽度' : '高度' }} px）</label>
            <input type="number" min="160" max="1200" :value="current.size" @input="patch({ size: Number($event.target.value) || 360 })" />
          </div>
          <div class="lf-field inline">
            <label>
              <input type="checkbox" :checked="current.modal !== false" @change="patch({ modal: $event.target.checked })" />
              全局蒙板（模态）
            </label>
          </div>

          <div class="group-title">自定义工具栏（可选）</div>
          <div class="lf-field">
            <label>挂载的工具栏</label>
            <select
              :value="current.toolbarId ?? ''"
              @change="setToolbar({ toolbarId: $event.target.value || null })"
            >
              <option value="">不挂载工具栏</option>
              <option v-for="t in toolbars" :key="t.id" :value="t.id">
                {{ t.label }}（{{ t.items.length }} 个条目）
              </option>
            </select>
            <p class="tip">
              工具栏可在「自定义工具栏」页签里新建和编辑条目；这里只决定挂哪一个、挂在哪一边。
            </p>
          </div>
          <div v-if="current.toolbarId" class="lf-field">
            <label>工具栏位置</label>
            <div class="seg">
              <button
                v-for="p in TOOLBAR_POSITIONS"
                :key="p.value"
                class="seg-btn"
                :class="{ active: (current.toolbarPosition ?? 'top') === p.value }"
                v-mq:[EventNames.drawerSetToolbar].click="{ drawerId: current.id, toolbarPosition: p.value }"
              >
                <LfIcon :name="p.icon" :size="13" /> {{ p.label }}
              </button>
            </div>
            <p class="tip">左右两侧会以「仅图标」的紧凑竖直工具条呈现。</p>
          </div>

          <div class="group-title">抽屉内容（view，可多个 → 抽屉内标签页）</div>
          <LfTabsEditor
            :model="current.tabs ?? []"
            allowed-any
            host-node-id="drawer"
            :owner="{ type: 'drawer', id: current.id }"
            :slot="{ prefix: `drawer:${current.id}`, label: `抽屉「${current.label}」` }"
            title="抽屉内容"
            empty-text="抽屉至少需要一个内容 view。"
            @change="(tabs) => setTabs(tabs)"
          />
        </template>
      </div>
    </section>
  </div>
</template>

<script setup>
import { computed, onMounted } from 'vue'
import LfTabsEditor from './LfTabsEditor.vue'
import LfIconPicker from '../../components/icon/LfIconPicker.vue'
import { useConfigStore } from '../../stores/config'
import { useEditStore } from '../../stores/edit'
import { TOOLBAR_POSITIONS } from '../../utils/regions'
import { EventNames } from '../../events/event-names'

const config = useConfigStore()
const edit = useEditStore()

const positions = [
  { value: 'left', label: '左侧' },
  { value: 'right', label: '右侧' },
  { value: 'top', label: '顶部' },
  { value: 'bottom', label: '底部' },
]

const current = computed(() => config.drawers.find((d) => d.id === edit.selectedDrawerId) ?? null)
const toolbars = computed(() => config.toolbars)

/**
 * 抽屉列表变化后保证有选中项（新增/删除后不落到空面板）。
 * 原为 `watch(config.drawers, { immediate: true })` —— 拆成两处、均非侦听（§4.2 禁 watch）：
 *   ① 挂载时兜底一次（等价 immediate）；
 *   ② 删除抽屉的兜底选中收敛到订阅端 `stores/mqCommands`（`lf-drawer-remove`）。
 */
function ensureSelection() {
  if (!config.drawers.some((d) => d.id === edit.selectedDrawerId)) {
    edit.selectDrawer(config.drawers[0]?.id ?? '')
  }
}
onMounted(ensureSelection)

function positionLabel(p) {
  return positions.find((x) => x.value === p)?.label ?? p
}

function patch(obj) {
  if (!current.value) return
  config.patchDrawer(current.value.id, obj)
}

/** 挂载 / 换绑自定义工具栏（未指定位置时保持原位置） */
function setToolbar(obj) {
  if (!current.value) return
  config.setDrawerToolbar(current.value.id, obj)
}

function selectPosition(value) {
  patch({ position: value })
}

function setTabs(tabs) {
  patch({ tabs })
}
</script>

<style scoped>
.s-drawer-host {
  display: flex;
  height: 100%;
  min-height: 0;
  background: var(--lf-bg);
}

.s-drawer {
  width: 280px;
  flex: none;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: #fff;
  border-right: 1px solid var(--lf-border);
}

.s-drawer-detail {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: #fff;
}

.pane-head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 6px;
  height: 32px;
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
  padding: 8px;
}

.drawer-row {
  display: flex;
  align-items: center;
  gap: 5px;
  height: 26px;
  padding: 0 4px 0 8px;
  border-radius: 5px;
  cursor: pointer;
}

.drawer-row:hover {
  background: var(--lf-tree-hover);
}

.drawer-row.selected {
  background: var(--lf-select);
  box-shadow: inset 0 0 0 1px var(--lf-select-border);
}

.drawer-row .lbl {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.drawer-row .pos {
  font-size: 10px;
  color: var(--lf-text-weak);
  background: #eef1f6;
  border-radius: 8px;
  padding: 0 5px;
}

.drawer-row .mini {
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  background: #fff;
  color: var(--lf-text-weak);
  height: 20px;
  min-width: 20px;
  line-height: 1;
}

.drawer-row .mini:hover:not(:disabled) {
  border-color: var(--lf-accent);
  color: var(--lf-accent);
}

.drawer-row .mini:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.drawer-row .mini.danger:hover {
  border-color: #d33;
  color: #d33;
}

.group-title {
  font-weight: 600;
  font-size: 12px;
  margin: 12px 0 6px;
  display: flex;
  align-items: center;
  gap: 6px;
}

.group-title::before {
  content: '';
  width: 3px;
  height: 12px;
  border-radius: 2px;
  background: var(--lf-accent);
}

.seg {
  display: inline-flex;
  background: #eef1f6;
  padding: 3px;
  border-radius: 7px;
  gap: 2px;
}

.seg-btn {
  height: 24px;
  padding: 0 12px;
  border: none;
  border-radius: 5px;
  background: transparent;
  color: var(--lf-text-weak);
  cursor: pointer;
}

.seg-btn.active {
  background: #fff;
  color: var(--lf-accent);
  font-weight: 600;
  box-shadow: 0 1px 3px rgba(20, 30, 50, 0.12);
}

.lf-field.inline {
  flex-direction: row;
  gap: 16px;
}

.lf-field.inline label {
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--lf-text);
}

.lf-field input[type='number'] {
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--lf-border-strong);
  border-radius: var(--lf-radius);
  width: 100%;
}

.icon-line {
  display: flex;
  align-items: center;
  gap: 6px;
}

.icon-line input {
  flex: 1;
  min-width: 0;
}

.icon-preview {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 25px;
  border: 1px dashed var(--lf-border-strong);
  border-radius: 5px;
  color: var(--lf-accent);
  flex: none;
}
</style>

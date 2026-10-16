<template>
  <div class="area-settings">
    <div class="as-head">
      <span class="ico"><LfIcon name="layout-panel-left" :size="13" /></span>
      <b>区域设置</b>
      <span class="lf-tag">{{ kindText }}</span>
      <!-- 三个页签：基本 / 内容 / 操作 —— 每个页签内容都短，任何一屏都不需要滚动 -->
      <div class="as-tabs">
        <button
          v-for="t in tabs"
          :key="t.key"
          class="as-tab"
          :class="{ active: tab === t.key }"
          v-mq:[EventNames.containerClick].click="() => setTab(t.key)"
        >
          <LfIcon :name="t.icon" :size="12" />{{ t.label }}
        </button>
      </div>
      <span class="grow" />
      <span class="id mono">{{ selectedId || '—' }}</span>
    </div>

    <div v-if="!node" class="lf-empty">在左侧布局树或右上预览中选择一个节点。</div>

    <!-- ═══ 容器节点（split）═══ -->
    <div v-else-if="node.type === 'split'" class="as-body">
      <template v-if="tab === 'basic'">
        <div class="as-group">
          <div class="group-title">背景色</div>
          <LfColorField :model="node.background ?? ''" @color="setBackground" />
          <p class="tip">空 = 跟随主题。容器背景渲染在子区域之下。</p>
        </div>
        <div class="as-group">
          <div class="group-title">容器</div>
          <div class="lf-field">
            <label>分割方向</label>
            <div class="seg">
              <button
                class="seg-btn"
                :class="{ active: node.direction === 'row' }"
                v-mq:[EventNames.containerClick].click="() => setDirection('row')"
              >
                <LfIcon name="columns-2" :size="13" /> 左右
              </button>
              <button
                class="seg-btn"
                :class="{ active: node.direction === 'column' }"
                v-mq:[EventNames.containerClick].click="() => setDirection('column')"
              >
                <LfIcon name="rows-2" :size="13" /> 上下
              </button>
            </div>
          </div>
          <div class="lf-field">
            <label>子区域（{{ node.children.length }}）</label>
            <div class="children">
              <button
                v-for="(c, i) in node.children"
                :key="c.id"
                class="child-chip"
                :class="{ active: edit.selectedNodeId === c.id }"
                v-mq:[EventNames.layoutSelectNode].click="{ nodeId: c.id }"
              >
                {{ i + 1 }}. {{ childLabel(c) }}
              </button>
            </div>
          </div>
        </div>
      </template>

      <template v-else-if="tab === 'content'">
        <div class="as-group">
          <div class="group-title">自定义工具栏（可选，挂在该容器某一边）</div>
          <div class="lf-field">
            <label>挂载的工具栏</label>
            <select :value="node.toolbar?.toolbarId ?? ''" @change="setToolbar({ toolbarId: $event.target.value || null })">
              <option value="">不挂载工具栏</option>
              <option v-for="t in toolbars" :key="t.id" :value="t.id">
                {{ t.label }}（{{ t.items.length }} 个条目）
              </option>
            </select>
            <p class="tip">工具栏在「自定义工具栏」页签里新建与编辑条目；容器工具栏渲染在内容之前。</p>
          </div>
          <div v-if="node.toolbar?.toolbarId" class="lf-field">
            <label>工具栏位置</label>
            <div class="seg">
              <button
                v-for="p in TOOLBAR_POSITIONS"
                :key="p.value"
                class="seg-btn"
                :class="{ active: (node.toolbar.position ?? 'top') === p.value }"
                v-mq:[EventNames.areaSetToolbar].click="{ nodeId: node.id, toolbarId: node.toolbar.toolbarId, position: p.value }"
              >
                <LfIcon :name="p.icon" :size="13" /> {{ p.label }}
              </button>
            </div>
          </div>
          <p class="tip">容器本身不承载 view；具体内容由它的子区域决定（见「基本」页签）。</p>
        </div>
      </template>

      <template v-else>
        <div class="as-group">
          <div class="group-title">分割</div>
          <div class="btn-row">
            <button class="lf-btn primary" v-mq:[EventNames.layoutSplit].click="{ nodeId: node.id, direction: 'row' }">
              <LfIcon name="split-square-horizontal" :size="13" /> 左右一分为二
            </button>
            <button class="lf-btn primary" v-mq:[EventNames.layoutSplit].click="{ nodeId: node.id, direction: 'column' }">
              <LfIcon name="split-square-vertical" :size="13" /> 上下一分为二
            </button>
            <button class="lf-btn" v-mq:[EventNames.layoutAddArea].click="{ nodeId: node.id }">
              <LfIcon name="plus" :size="13" /> 容器内追加区域
            </button>
          </div>
          <p class="tip">分割后区域一分为二，形成两个区。也可以在布局树 / 预览里对任意节点点右键，选择「分割」。</p>
        </div>
        <div class="as-group">
          <div class="group-title">顺序（与同级交换）</div>
          <div class="btn-row">
            <button class="lf-btn" :disabled="!sibling?.canPrev" v-mq:[EventNames.areaSwap].click="{ nodeId: node.id, delta: -1 }">
              <LfIcon :name="swapLabels.prevIcon" :size="13" /> {{ swapLabels.prev }}
            </button>
            <button class="lf-btn" :disabled="!sibling?.canNext" v-mq:[EventNames.areaSwap].click="{ nodeId: node.id, delta: 1 }">
              <LfIcon :name="swapLabels.nextIcon" :size="13" /> {{ swapLabels.next }}
            </button>
          </div>
          <p v-if="sibling" class="tip">当前是同级第 {{ sibling.index + 1 }} / {{ sibling.total }} 个。</p>
        </div>
      </template>
    </div>

    <!-- ═══ 区域（叶子）═══ -->
    <div v-else class="as-body">
      <!-- 基本 -->
      <template v-if="tab === 'basic'">
        <div class="as-group">
          <div class="group-title">背景色</div>
          <LfColorField :model="node.background ?? ''" @color="setBackground" />
          <p class="tip">空 = 跟随主题。</p>
        </div>
        <div class="as-group">
          <div class="lf-field">
            <label>区域标题（设计态显示；运行态不显示）</label>
            <input type="text" :value="node.title ?? ''" placeholder="例如：工作区" @input="onTitle" />
          </div>
          <div class="lf-field">
            <label>区域类型</label>
            <div class="kind-row">
              <button
                v-for="k in REGION_KINDS"
                :key="k.value"
                class="kind-btn"
                :class="{ active: kind === k.value }"
                v-mq:[EventNames.areaSetKind].click="{ nodeId: node.id, kind: k.value }"
              >
                <span class="ki"><LfIcon :name="k.icon" :size="15" /></span>
                <span class="kl">{{ k.label }}</span>
                <span class="kh">{{ k.hint }}</span>
              </button>
            </div>
          </div>
          <div v-if="kind !== 'single'" class="lf-field">
            <label>标签栏位置</label>
            <div class="seg">
              <button
                v-for="p in TAB_POSITIONS"
                :key="p.value"
                class="seg-btn"
                :class="{ active: tabPos === p.value }"
                v-mq:[EventNames.areaSetTabPosition].click="{ nodeId: node.id, position: p.value }"
              >
                <LfIcon :name="p.icon" :size="13" /> {{ p.label }}
              </button>
            </div>
          </div>
        </div>
        <div class="as-group">
          <div class="group-title">尺寸约束（{{ sizeAxis === 'width' ? '宽度' : '高度' }}）</div>
          <p class="tip">
            父容器{{
              parentDir === 'row'
                ? '左右排布 → 约束『宽度』'
                : parentDir === 'column'
                  ? '上下排布 → 约束『高度』'
                  : '是根节点'
            }}。<b>最大 = 最小</b> 时该区域锁定，分割条不可拖拽。
          </p>
          <div class="size-row">
            <div class="lf-field">
              <label>最小</label>
              <input type="number" min="40" placeholder="不限制" :value="minPx" @change="setConstraint({ min: $event.target.value })" />
            </div>
            <div class="lf-field">
              <label>最大</label>
              <input type="number" min="40" placeholder="不限制" :value="maxPx" @change="setConstraint({ max: $event.target.value })" />
            </div>
            <div class="lf-field">
              <label>当前</label>
              <div class="cur-size">{{ currentPx }} px</div>
            </div>
          </div>
          <div class="btn-row">
            <button class="lf-btn" v-mq:[EventNames.containerClick].click="() => setConstraint({ min: '', max: '' })">
              <LfIcon name="unlock" :size="13" /> 不限制
            </button>
            <button class="lf-btn" v-mq:[EventNames.containerClick].click="() => lockSize()">
              <LfIcon name="lock" :size="13" /> 锁定为当前尺寸
            </button>
          </div>
          <p v-if="isFixed" class="warn">已锁定：该区域尺寸固定为 {{ minPx }} px，相邻分割条不可拖拽。</p>
        </div>
      </template>

      <!-- 内容 -->
      <template v-else-if="tab === 'content'">
        <div v-if="kind === 'single'" class="as-group">
          <div class="lf-field">
            <label>view</label>
            <select :value="node.view?.viewKey ?? ''" @change="onView">
              <option value="">空白区域（不放置 view）</option>
              <optgroup label="单例 view">
                <template v-for="v in viewOptions" :key="v.key">
                  <option v-if="v.mode === 'singleton'" :value="v.key" :disabled="v.disabled">
                    {{ v.label }}{{ v.disabled ? v.takenLabel : '' }}
                  </option>
                </template>
              </optgroup>
              <optgroup label="多实例 view（需选择配套 key）">
                <template v-for="v in viewOptions" :key="`m-${v.key}`">
                  <option v-if="v.mode === 'multi'" :value="v.key">{{ v.label }}</option>
                </template>
              </optgroup>
            </select>
          </div>
          <p v-if="currentDef" class="desc"><LfIcon :name="currentDef.icon" :size="13" /> {{ currentDef.description }}</p>
          <div v-if="currentDef?.mode === 'multi'" class="lf-field">
            <label>配套变量 key</label>
            <select :value="node.view?.instanceKey ?? ''" @change="onKey">
              <option value="">不使用 key</option>
              <option v-for="k in keyList" :key="k.key" :value="k.key">
                {{ k.label }}（{{ k.key }}）当前值：{{ k.value }}
              </option>
            </select>
          </div>
          <p v-if="currentDef?.mode === 'multi' && !node.view?.instanceKey" class="warn">
            {{ currentDef.label }} 是多实例 view，必须选择配套 key，否则无法保存。
          </p>
        </div>

        <div v-else class="as-group">
          <p class="tip">
            {{
              kind === 'fixed-tabs'
                ? '这些标签页固定显示在该区域里。'
                : '容器运行期由命令装入 view；这里配置的是打开时就有的预置标签页，可留空。'
            }}
          </p>
          <LfTabsEditor
            :model="node.tabs ?? []"
            :host-node-id="node.id"
            :owner="{ type: 'area', id: node.id }"
            :slot="{ prefix: `area:${node.id}`, label: `区域「${node.title || '区域'}」` }"
            :title="kind === 'fixed-tabs' ? '固定标签页' : '预置内容'"
            :empty-text="
              kind === 'fixed-tabs' ? '固定 tab 容器至少需要一个标签页。' : '（可留空：运行期由命令装入 view）'
            "
            @change="onTabs"
          />
        </div>

        <div class="as-group">
          <div class="group-title">自定义工具栏（可选，挂在该区域某一边）</div>
          <div class="lf-field">
            <label>挂载的工具栏</label>
            <select :value="node.toolbar?.toolbarId ?? ''" @change="setToolbar({ toolbarId: $event.target.value || null })">
              <option value="">不挂载工具栏</option>
              <option v-for="t in toolbars" :key="t.id" :value="t.id">
                {{ t.label }}（{{ t.items.length }} 个条目）
              </option>
            </select>
          </div>
          <div v-if="node.toolbar?.toolbarId" class="lf-field">
            <label>工具栏位置</label>
            <div class="seg">
              <button
                v-for="p in TOOLBAR_POSITIONS"
                :key="p.value"
                class="seg-btn"
                :class="{ active: (node.toolbar.position ?? 'top') === p.value }"
                v-mq:[EventNames.areaSetToolbar].click="{ nodeId: node.id, toolbarId: node.toolbar.toolbarId, position: p.value }"
              >
                <LfIcon :name="p.icon" :size="13" /> {{ p.label }}
              </button>
            </div>
            <p class="tip">左右两侧会以「仅图标」的紧凑竖直工具条呈现。</p>
          </div>
        </div>
      </template>

      <!-- 操作 -->
      <template v-else>
        <div class="as-group">
          <div class="group-title">分割</div>
          <div class="btn-row">
            <button class="lf-btn primary" v-mq:[EventNames.layoutSplit].click="{ nodeId: node.id, direction: 'row' }">
              <LfIcon name="split-square-horizontal" :size="13" /> 左右一分为二
            </button>
            <button class="lf-btn primary" v-mq:[EventNames.layoutSplit].click="{ nodeId: node.id, direction: 'column' }">
              <LfIcon name="split-square-vertical" :size="13" /> 上下一分为二
            </button>
          </div>
          <p class="tip">分割会把当前区域一分为二，产生一个新的空白区域。</p>
        </div>
        <div class="as-group">
          <div class="group-title">顺序（与同级交换）</div>
          <div class="btn-row">
            <button class="lf-btn" :disabled="!sibling?.canPrev" v-mq:[EventNames.areaSwap].click="{ nodeId: node.id, delta: -1 }">
              <LfIcon :name="swapLabels.prevIcon" :size="13" /> {{ swapLabels.prev }}
            </button>
            <button class="lf-btn" :disabled="!sibling?.canNext" v-mq:[EventNames.areaSwap].click="{ nodeId: node.id, delta: 1 }">
              <LfIcon :name="swapLabels.nextIcon" :size="13" /> {{ swapLabels.next }}
            </button>
          </div>
          <p v-if="sibling" class="tip">当前是同级第 {{ sibling.index + 1 }} / {{ sibling.total }} 个。</p>
        </div>
        <div class="as-group">
          <div class="group-title">删除</div>
          <div class="btn-row">
            <button
              class="lf-btn danger"
              :disabled="leafCount <= 1"
              v-mq:[EventNames.layoutRemoveArea].click="{ nodeId: node.id }"
            >
              <LfIcon name="trash-2" :size="13" /> 删除该区域
            </button>
          </div>
          <p class="tip">布局至少保留一个区域。</p>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, onUpdated, ref } from 'vue'
import LfTabsEditor from './LfTabsEditor.vue'
import LfColorField from './LfColorField.vue'
import { useConfigStore } from '../../stores/config'
import { useEditStore } from '../../stores/edit'
import { useKeysStore } from '../../stores/keys'
import { getView } from '../../config/views'
import { findNode, isSplit } from '../../utils/layout'
import {
  REGION_KINDS,
  TAB_POSITIONS,
  TOOLBAR_POSITIONS,
  leafSizeRange,
  regionKind,
  regionKindMeta,
  tabPosition,
} from '../../utils/regions'
import { EventNames } from '../../events/event-names'
import { getMq } from '../../deps.js'

const config = useConfigStore()
const edit = useEditStore()
const keys = useKeysStore()

/** 区域设置的三个页签 —— 内容都短，任何一屏都不用滚动 */
const tabs = [
  { key: 'basic', label: '基本', icon: 'info' },
  { key: 'content', label: '内容', icon: 'layers' },
  { key: 'op', label: '操作', icon: 'wrench' },
]
const tab = ref('basic')

function setTab(key) {
  tab.value = key
}

const selectedId = computed(() => edit.selectedNodeId)
const node = computed(() => findNode(config.draft.layout, edit.selectedNodeId))
const leafCount = computed(() => config.leaves.length)

const kind = computed(() => regionKind(node.value))
const tabPos = computed(() => tabPosition(node.value))
const kindText = computed(() => {
  const n = node.value
  if (!n) return '未选择'
  if (isSplit(n)) return '容器节点'
  return regionKindMeta(n).label
})

/** 单容器可选 view（单例被别处占用则禁用） */
const viewOptions = computed(() => config.viewOptions(node.value ? `area:${node.value.id}` : ''))
const currentDef = computed(() =>
  kind.value === 'single' && node.value?.view ? getView(node.value.view.viewKey) : undefined,
)
const keyList = computed(() => keys.list)

/* ── 尺寸约束 ──────────────────────────────────────── */

const parentDir = computed(() => (node.value ? config.parentDirection(node.value.id) : null))
const sizeAxis = computed(() => (parentDir.value === 'column' ? 'height' : 'width'))
const range = computed(() => leafSizeRange(node.value, parentDir.value ?? 'row'))
const minPx = computed(() => node.value?.[sizeAxis.value === 'width' ? 'minWidth' : 'minHeight'] ?? '')
const maxPx = computed(() => node.value?.[sizeAxis.value === 'width' ? 'maxWidth' : 'maxHeight'] ?? '')
const isFixed = computed(() => range.value.fixed)

/** 当前实际尺寸：从设计态预览里量测 */
function measure() {
  const id = node.value?.id
  if (!id) return 0
  const el = document.querySelector(`.le-preview [data-leaf-id="${id}"]`)
  if (!el) return 0
  const r = el.getBoundingClientRect()
  return Math.round(sizeAxis.value === 'width' ? r.width : r.height)
}
const currentPx = ref(0)

/**
 * 「当前实际尺寸」从设计态预览里量测。
 * 原为 `watch([nodeId, open, toolbar, min/max, tab], …, { immediate: true })` ——
 * 这些依赖任一变化都会引起本组件重渲染，故改为 `onMounted`（等价 immediate）+ `onUpdated`
 * 量测，不侦听 props/state（§4.2 禁 watch）。
 * 只在量到的值真的变化时写 ref，避免「写 ref → 再更新 → 再量测」的空转。
 */
function refreshCurrentPx() {
  const px = measure()
  if (px !== currentPx.value) currentPx.value = px
}
onMounted(() => nextTick(refreshCurrentPx))
onUpdated(refreshCurrentPx)

/* ── 同级交换 ──────────────────────────────────────── */

const sibling = computed(() => (node.value ? config.siblingInfo(node.value.id) : null))
const swapLabels = computed(() => {
  const vertical = sibling.value?.direction === 'column'
  return vertical
    ? { prev: '与前一个交换（上移）', next: '与后一个交换（下移）', prevIcon: 'arrow-up', nextIcon: 'arrow-down' }
    : { prev: '与前一个交换（左移）', next: '与后一个交换（右移）', prevIcon: 'arrow-left', nextIcon: 'arrow-right' }
})

const toolbars = computed(() => config.toolbars)

/* ── 所有改动都发布 mq 主题（组件不直连 store，见 stores/mqCommands.js）── */

function setConstraint(patch) {
  if (!node.value) return
  getMq().emit(EventNames.areaSetConstraint, {
    nodeId: node.value.id,
    direction: parentDir.value ?? 'row',
    ...patch,
  })
}

function lockSize() {
  if (!node.value) return
  const dir = parentDir.value ?? 'row'
  const r = leafSizeRange(node.value, dir)
  const cur = measure() || r.max || r.min
  getMq().emit(EventNames.areaSetConstraint, { nodeId: node.value.id, direction: dir, min: cur, max: cur })
}

function setBackground(color) {
  if (node.value) getMq().emit(EventNames.nodeSetBackground, { nodeId: node.value.id, color })
}

function setToolbar(patch) {
  // 复用 areaSetToolbar（它本来就同时服务区域与容器），不再单独开一条主题
  if (node.value) getMq().emit(EventNames.areaSetToolbar, { nodeId: node.value.id, ...patch })
}

function childLabel(child) {
  if (isSplit(child)) return `容器（${child.direction === 'row' ? '左右' : '上下'} · ${child.children.length}）`
  if (child.title) return child.title
  return regionKindMeta(child).label
}

function setDirection(direction) {
  if (node.value) getMq().emit(EventNames.splitSetDirection, { nodeId: node.value.id, direction })
}

function onTitle(e) {
  if (node.value) getMq().emit(EventNames.areaSetTitle, { nodeId: node.value.id, title: e.target.value })
}

function onView(e) {
  if (node.value) getMq().emit(EventNames.areaSetView, { nodeId: node.value.id, viewKey: e.target.value })
}

function onKey(e) {
  if (node.value) {
    getMq().emit(EventNames.areaSetInstanceKey, { nodeId: node.value.id, instanceKey: e.target.value || null })
  }
}

function onTabs(next) {
  if (node.value) getMq().emit(EventNames.areaSetTabs, { nodeId: node.value.id, tabs: next })
}
</script>

<style scoped>
.area-settings {
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: 100%;
}

.as-head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding: 0 10px;
  border-bottom: 1px solid var(--lf-border);
  background: #fbfcfe;
}

.as-head .grow {
  flex: 1;
}

.as-head .id {
  font-size: 11px;
  color: var(--lf-text-weak);
}

.as-tabs {
  display: inline-flex;
  gap: 2px;
  margin-left: 10px;
  background: #eef1f6;
  padding: 2px;
  border-radius: 7px;
}

.as-tab {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 22px;
  padding: 0 10px;
  border: none;
  border-radius: 5px;
  background: transparent;
  color: var(--lf-text-weak);
  font-size: 12px;
  cursor: pointer;
}

.as-tab.active {
  background: #fff;
  color: var(--lf-accent);
  font-weight: 600;
  box-shadow: 0 1px 3px rgba(20, 30, 50, 0.12);
}

.as-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 10px;
}

.as-group {
  border: 1px solid var(--lf-border);
  border-radius: 8px;
  padding: 10px;
  margin-bottom: 10px;
  background: #fff;
}

.group-title {
  font-weight: 600;
  font-size: 12px;
  margin-bottom: 8px;
  color: var(--lf-text);
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

.kind-row {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.kind-btn {
  flex: 1;
  min-width: 132px;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 3px;
  padding: 8px 10px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 8px;
  background: #fff;
  cursor: pointer;
  text-align: left;
}

.kind-btn:hover {
  border-color: var(--lf-accent);
}

.kind-btn.active {
  border-color: var(--lf-accent);
  background: var(--lf-accent-soft);
}

.kind-btn .ki {
  display: inline-flex;
  color: var(--lf-accent);
}

.kind-btn .kl {
  font-weight: 600;
  font-size: 12px;
  color: var(--lf-text);
}

.kind-btn .kh {
  font-size: 11px;
  color: var(--lf-text-weak);
}

.btn-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
}

.desc,
.tip {
  font-size: 11px;
  color: var(--lf-text-weak);
  line-height: 1.6;
  margin: 6px 0 0;
}

.desc {
  display: flex;
  align-items: center;
  gap: 5px;
}

.warn {
  font-size: 11px;
  color: #b23c00;
  background: #fff3e8;
  border: 1px solid #f5d3b8;
  border-radius: 6px;
  padding: 5px 8px;
  margin: 6px 0 0;
}

.seg {
  display: inline-flex;
  background: #eef1f6;
  padding: 3px;
  border-radius: 7px;
  gap: 2px;
}

.seg-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
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

.children {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.child-chip {
  height: 24px;
  padding: 0 10px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 14px;
  background: #fff;
  color: var(--lf-text);
  font-size: 12px;
  cursor: pointer;
}

.child-chip.active {
  border-color: var(--lf-accent);
  background: var(--lf-accent-soft);
  color: var(--lf-accent);
}

.lf-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.mono {
  font-family: Consolas, monospace;
}

.lf-btn.danger {
  color: #c62828;
  border-color: #eab6b6;
}

.lf-btn.danger:hover:not(:disabled) {
  background: #ffeaea;
  border-color: #c62828;
}

.size-row {
  display: flex;
  align-items: flex-end;
  gap: 8px;
}

.size-row .lf-field {
  flex: 1;
  min-width: 0;
}

.cur-size {
  height: 25px;
  display: flex;
  align-items: center;
  font-size: 12px;
  color: var(--lf-text-weak);
  font-family: Consolas, monospace;
}
</style>

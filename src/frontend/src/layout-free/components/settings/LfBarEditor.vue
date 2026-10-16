<template>
  <div class="bar-editor">
    <!-- 左：条目列表（按区分组）+ 新建三类条目 -->
    <aside class="be-left">
      <div class="pane-head">
        <span class="ico"><LfIcon :name="bar === 'statusbar' ? 'activity' : 'wrench'" :size="13" /></span>
        <b>{{ title }}</b>
        <span class="grow" />
        <span class="lf-tag">{{ items.length }} 个条目</span>
      </div>
      <div class="pane-body">
        <template v-for="zone in zones" :key="zone.value">
          <div class="zone-title">{{ zone.label }}区<span class="zcount">{{ itemsOf(zone.value).length }}</span></div>
          <div
            v-for="item in itemsOf(zone.value)"
            :key="item.id"
            class="item-row"
            :class="{ selected: item.id === selectedId }"
            v-mq:[EventNames.barItemSelect].click="{ bar: bar, id: item.id }"
          >
            <LfIcon class="ico" :name="itemIcon(item)" :size="13" fallback="·" />
            <span class="lbl">{{ item.label || kindLabel(item.kind) }}</span>
            <span class="kind">{{ kindLabel(item.kind) }}</span>
            <button
              v-if="zone.value !== 'left'"
              class="mini"
              :title="`移到${prevZoneLabel(zone.value)}`"
              v-mq:[EventNames.barItemZone].click.stop="{ bar: bar, id: item.id, zone: prevZone(zone.value) }"
            >
              ←
            </button>
            <button
              v-if="zone.value !== 'right'"
              class="mini"
              :title="`移到${nextZoneLabel(zone.value)}`"
              v-mq:[EventNames.barItemZone].click.stop="{ bar: bar, id: item.id, zone: nextZone(zone.value) }"
            >
              →
            </button>
            <button class="mini" title="上移" v-mq:[EventNames.barItemMove].click.stop="{ bar: bar, id: item.id, delta: -1 }">
              ↑
            </button>
            <button class="mini" title="下移" v-mq:[EventNames.barItemMove].click.stop="{ bar: bar, id: item.id, delta: 1 }">
              ↓
            </button>
            <button class="mini danger" title="删除" v-mq:[EventNames.barItemRemove].click.stop="{ bar: bar, id: item.id }">
              ✕
            </button>
          </div>
          <div v-if="!itemsOf(zone.value).length" class="zone-empty">（空）</div>
        </template>
      </div>
      <div class="pane-foot">
        <button class="lf-btn" v-mq:[EventNames.barItemAdd].click="{ bar: bar, kind: 'button', zone: 'left' }">
          <LfIcon name="square" :size="12" /> 按钮
        </button>
        <button class="lf-btn" v-mq:[EventNames.barItemAdd].click="{ bar: bar, kind: 'menu', zone: 'left' }">
          <LfIcon name="menu" :size="12" /> 菜单
        </button>
        <button class="lf-btn" v-mq:[EventNames.barItemAdd].click="{ bar: bar, kind: 'view', zone: 'left' }">
          <LfIcon name="search" :size="12" /> 视图
        </button>
      </div>
    </aside>

    <!-- 右：按条目类型只渲染对应的那一套编辑界面 -->
    <section class="be-right">
      <!-- 栏级页签：条目 / 背景（整条栏 + 每个区） -->
      <div class="be-subtabs">
        <button
          v-for="t in subTabs"
          :key="t.key"
          class="be-subtab"
          :class="{ active: subTab === t.key }"
          v-mq:[EventNames.containerClick].click="() => (subTab = t.key)"
        >
          <LfIcon :name="t.icon" :size="12" />{{ t.label }}
        </button>
      </div>

      <!-- 背景：整条栏 + 三个区 -->
      <div v-if="subTab === 'bg'" class="be-bg">
        <div class="bg-group">
          <div class="group-title">整条栏背景</div>
          <LfColorField :model="barObject.background ?? ''" @color="patchBarBackground" />
        </div>
        <div class="bg-zones">
          <div v-for="z in zones" :key="z.value" class="bg-group">
            <div class="group-title">{{ z.label }}区背景（覆盖整条栏）</div>
            <LfColorField
              :model="barObject.zoneBackground?.[z.value] ?? ''"
              @color="(c) => patchZoneBackground(z.value, c)"
            />
          </div>
        </div>
      </div>

      <template v-else>
      <div v-if="!current" class="be-empty">
        <LfIcon name="mouse-pointer-click" :size="22" />
        <div>在左侧选择一个条目；或用底部按钮新建「按钮 / 菜单 / 视图」。</div>
      </div>
      <!-- ① 按钮 -->
      <template v-else-if="current.kind === 'button'">
        <div class="pane-head">
          <span class="ico"><LfIcon name="square" :size="13" /></span>
          <b>按钮编辑</b>
          <span class="grow" />
          <span class="kind-tag">按钮</span>
        </div>
        <div class="pane-body">
          <div class="form-cols">
            <div class="col">
              <div class="group-title">外观</div>
              <div class="lf-field">
                <label>文本</label>
                <input type="text" :value="current.label" @input="patch({ label: $event.target.value })" />
              </div>
              <div class="lf-field">
                <label>图标</label>
                <div class="icon-line">
                  <LfIconPicker :value="current.icon ?? ''" :name-key="`bar-${current.id}`" @pick="(n) => patch({ icon: n })" />
                  <input
                    type="text"
                    :value="current.icon ?? ''"
                    placeholder="图标名 / emoji / 图片地址"
                    @input="patch({ icon: $event.target.value })"
                  />
                  <span class="icon-preview"><LfIcon :name="current.icon ?? ''" :size="16" fallback="—" /></span>
                </div>
                <div class="icon-presets">
                  <button
                    v-for="ic in iconPresets"
                    :key="ic"
                    class="icon-chip"
                    :class="{ active: current.icon === ic }"
                    :title="ic"
                    v-mq:[EventNames.containerClick].click="() => patch({ icon: ic })"
                  >
                    <LfIcon :name="ic" :size="14" />
                  </button>
                </div>
              </div>
              <div class="lf-field">
                <label>按钮样式</label>
                <div class="seg">
                  <button
                    v-for="s in styles"
                    :key="s.key"
                    class="seg-btn"
                    :class="{ active: current.style === s.key }"
                    v-mq:[EventNames.containerClick].click="() => patch({ style: s.key })"
                  >
                    {{ s.label }}
                  </button>
                </div>
              </div>
              <div class="lf-field">
                <label>提示文字（tooltip）</label>
                <input type="text" :value="current.tooltip ?? ''" @input="patch({ tooltip: $event.target.value })" />
              </div>
              <div class="lf-field inline">
                <label>
                  <input type="checkbox" :checked="current.visible !== false" @change="patch({ visible: $event.target.checked })" />
                  显示
                </label>
                <label>
                  <input type="checkbox" :checked="!!current.disabled" @change="patch({ disabled: $event.target.checked })" />
                  禁用
                </label>
              </div>
            </div>

            <div class="col">
              <div class="group-title">位置</div>
              <div class="lf-field">
                <label>所在区</label>
                <div class="seg">
                  <button
                    v-for="z in zones"
                    :key="z.value"
                    class="seg-btn"
                    :class="{ active: (current.zone ?? 'left') === z.value }"
                    v-mq:[EventNames.barItemZone].click="{ bar: bar, id: current.id, zone: z.value }"
                  >
                    {{ z.label }}
                  </button>
                </div>
              </div>
              <div class="group-title">命令（必填）</div>
              <LfCommandEditor
                :model="current.command ?? null"
                :slot-key="`bar:${bar}:${current.id}`"
                @change="(cmd) => patch({ command: cmd })"
              />
            </div>
          </div>
        </div>
      </template>

      <!-- ② 菜单：左右分栏（左树 / 右编辑），顶层与二级同一套 -->
      <template v-else-if="current.kind === 'menu'">
        <div class="pane-head">
          <span class="ico"><LfIcon name="menu" :size="13" /></span>
          <b>菜单编辑 · {{ current.label }}</b>
          <span class="grow" />
          <button class="lf-btn" v-mq:[EventNames.menuNodeAdd].click="{ bar: bar, itemId: current.id, parentId: null, sub: false }">
            ＋ 条目
          </button>
          <button class="lf-btn" v-mq:[EventNames.menuNodeAdd].click="{ bar: bar, itemId: current.id, parentId: null, sub: true }">
            ＋ 子菜单
          </button>
        </div>
        <div class="menu-split">
          <!-- 左树：点选 -->
          <div class="ms-tree">
            <div class="ms-title">菜单结构</div>
            <div class="ms-body">
              <LfMenuNodeList :nodes="current.children ?? []" :bar="bar" :item-id="current.id" :depth="0" />
              <div v-if="!(current.children ?? []).length" class="lf-empty">菜单还没有条目。</div>
            </div>
          </div>
          <!-- 右编辑：所选条目的属性 + 命令 -->
          <div class="ms-edit">
            <div class="ms-title">
              条目设置{{ currentMenuNode ? ` · ${currentMenuNode.label}` : '' }}
            </div>
            <div class="ms-body">
              <div v-if="!currentMenuNode" class="lf-empty">在左侧点选一个菜单条目进行编辑。</div>
              <template v-else>
                <div class="mn-label-row">
                  <label>条目文本</label>
                  <input type="text" :value="currentMenuNode.label" @input="patchMenu({ label: $event.target.value })" />
                  <label>图标</label>
                  <LfIconPicker
                    :value="currentMenuNode.icon ?? ''"
                    :name-key="`node-${currentMenuNode.id}`"
                    @pick="(n) => patchMenu({ icon: n })"
                  />
                  <input
                    type="text"
                    class="icon-input"
                    :value="currentMenuNode.icon ?? ''"
                    @input="patchMenu({ icon: $event.target.value })"
                  />
                  <label>可见</label>
                  <input
                    type="checkbox"
                    :checked="currentMenuNode.visible !== false"
                    @change="patchMenu({ visible: $event.target.checked })"
                  />
                </div>
                <div class="mn-sub-row">
                  <label>
                    <input
                      type="checkbox"
                      :checked="!!currentMenuNode.children?.length"
                      v-mq:[EventNames.menuNodeSub].change="
                        () => ({
                          bar,
                          itemId: current.id,
                          nodeId: currentMenuNode.id,
                          sub: !currentMenuNode.children?.length,
                        })
                      "
                    />
                    作为子菜单（子菜单不需要命令，可继续添加下级条目）
                  </label>
                </div>
                <template v-if="currentMenuNode.children?.length">
                  <p class="tip">该条目是子菜单，命令配置在其子条目上。</p>
                </template>
                <template v-else>
                  <div class="group-title">命令（必填）</div>
                  <LfCommandEditor
                    :model="currentMenuNode.command ?? null"
                    :slot-key="`bar:${bar}:${current.id}#${currentMenuNode.id}`"
                    @change="(cmd) => patchMenu({ command: cmd })"
                  />
                </template>
              </template>
            </div>
          </div>
          <!-- 菜单本身的外观（缩进放在最右，避免打断「树 → 编辑」的视线） -->
          <div class="ms-own">
            <div class="ms-title">菜单外观</div>
            <div class="ms-body">
              <div class="lf-field">
                <label>文本</label>
                <input type="text" :value="current.label" @input="patch({ label: $event.target.value })" />
              </div>
              <div class="lf-field">
                <label>图标</label>
                <div class="icon-line">
                  <LfIconPicker :value="current.icon ?? ''" :name-key="`bar-${current.id}`" @pick="(n) => patch({ icon: n })" />
                  <input type="text" :value="current.icon ?? ''" @input="patch({ icon: $event.target.value })" />
                </div>
              </div>
              <div class="lf-field">
                <label>按钮样式</label>
                <div class="seg">
                  <button
                    v-for="s in styles"
                    :key="s.key"
                    class="seg-btn"
                    :class="{ active: current.style === s.key }"
                    v-mq:[EventNames.containerClick].click="() => patch({ style: s.key })"
                  >
                    {{ s.label }}
                  </button>
                </div>
              </div>
              <div class="lf-field">
                <label>所在区</label>
                <div class="seg">
                  <button
                    v-for="z in zones"
                    :key="z.value"
                    class="seg-btn"
                    :class="{ active: (current.zone ?? 'left') === z.value }"
                    v-mq:[EventNames.barItemZone].click="{ bar: bar, id: current.id, zone: z.value }"
                  >
                    {{ z.label }}
                  </button>
                </div>
              </div>
              <div class="lf-field">
                <label>提示文字（tooltip）</label>
                <input type="text" :value="current.tooltip ?? ''" @input="patch({ tooltip: $event.target.value })" />
              </div>
              <div class="lf-field inline">
                <label>
                  <input type="checkbox" :checked="current.visible !== false" @change="patch({ visible: $event.target.checked })" />
                  显示
                </label>
              </div>
            </div>
          </div>
        </div>
      </template>

      <!-- ③ 视图：内嵌一个 view，必须定义宽 / 高 -->
      <template v-else>
        <div class="pane-head">
          <span class="ico"><LfIcon name="search" :size="13" /></span>
          <b>视图编辑</b>
          <span class="grow" />
          <span class="kind-tag">视图</span>
        </div>
        <div class="pane-body">
          <div class="lf-field">
            <label>view</label>
            <select :value="current.viewKey ?? ''" @change="chooseViewKey($event.target.value)">
              <option value="">（未选择）</option>
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
          <div v-if="currentDef?.mode === 'multi'" class="lf-field">
            <label>配套变量 key</label>
            <select :value="current.instanceKey ?? ''" @change="patch({ instanceKey: $event.target.value || null })">
              <option value="">不使用 key</option>
              <option v-for="k in keyList" :key="k.key" :value="k.key">
                {{ k.label }}（{{ k.key }}）
              </option>
            </select>
          </div>
          <p v-if="currentDef" class="desc"><LfIcon :name="currentDef.icon" :size="13" /> {{ currentDef.description }}</p>

          <div class="group-title">尺寸</div>
          <div class="lf-field inline">
            <label>
              <input type="checkbox" :checked="!!current.grow" @change="patch({ grow: $event.target.checked })" />
              撑满所在区（不写死宽 / 高）
            </label>
          </div>
          <div v-if="!current.grow" class="size-row">
            <div class="lf-field">
              <label>宽度（px）</label>
              <input
                type="number"
                min="40"
                :value="current.width"
                @change="patch({ width: Number($event.target.value) || 0 })"
              />
            </div>
            <div class="lf-field">
              <label>高度（px）</label>
              <input
                type="number"
                min="24"
                :value="current.height"
                @change="patch({ height: Number($event.target.value) || 0 })"
              />
            </div>
          </div>
          <p class="tip">view 条目直接在栏里内嵌渲染（例如搜索框），不再是一个按钮。</p>

          <div class="group-title">位置与外观</div>
          <div class="lf-field">
            <label>所在区</label>
            <div class="seg">
              <button
                v-for="z in zones"
                :key="z.value"
                class="seg-btn"
                :class="{ active: (current.zone ?? 'left') === z.value }"
                v-mq:[EventNames.barItemZone].click="{ bar: bar, id: current.id, zone: z.value }"
              >
                {{ z.label }}
              </button>
            </div>
          </div>
          <div class="lf-field">
            <label>提示文字（tooltip）</label>
            <input type="text" :value="current.tooltip ?? ''" @input="patch({ tooltip: $event.target.value })" />
          </div>
          <div class="lf-field inline">
            <label>
              <input type="checkbox" :checked="current.visible !== false" @change="patch({ visible: $event.target.checked })" />
              显示
            </label>
          </div>
        </div>
      </template>
      </template>
    </section>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import LfCommandEditor from './LfCommandEditor.vue'
import LfMenuNodeList from './LfMenuNodeList.vue'
import LfColorField from './LfColorField.vue'
import LfIconPicker from '../../components/icon/LfIconPicker.vue'
import { useConfigStore } from '../../stores/config'
import { useEditStore } from '../../stores/edit'
import { useKeysStore } from '../../stores/keys'
import { getView } from '../../config/views'
import { EventNames } from '../../events/event-names'
import { getMq } from '../../deps.js'

const props = defineProps({
  /** 栏标识：topToolbar | statusbar | custom:<toolbarId>（旧写法 'toolbar' 自动映射） */
  barKey: { type: String, default: '' },
})

const config = useConfigStore()
const edit = useEditStore()
const keys = useKeysStore()

/** 兼容旧调用（bar="toolbar" → topToolbar） */
const bar = computed(() => (props.barKey === 'toolbar' ? 'topToolbar' : props.barKey))

const zones = [
  { value: 'left', label: '靠左' },
  { value: 'center', label: '中间' },
  { value: 'right', label: '靠右' },
]

const styles = [
  { key: 'icon', label: '仅图片' },
  { key: 'text', label: '仅文本' },
  { key: 'icon-text', label: '图片+文本' },
]

const iconPresets = [
  'settings', 'sliders-horizontal', 'wrench', 'plug', 'bot', 'folder-open', 'list-tree', 'messages-square',
  'gallery-horizontal', 'gallery-vertical', 'panel-right', 'save', 'search', 'filter', 'refresh-cw', 'trash-2',
  'eye', 'eye-off', 'lock', 'external-link', 'plus', 'ellipsis', 'chevron-down', 'command', 'terminal', 'activity',
]

const title = computed(() => config.barLabel(bar.value))
const items = computed(() => config.barList(bar.value))
const selectedId = computed(() => edit.selectedBarItem[bar.value] ?? '')
const current = computed(() => items.value.find((i) => i.id === selectedId.value) ?? null)

/** 栏本身的配置对象（背景色 / 每区背景色都在这里） */
const barObject = computed(() => config.barObject(bar.value) ?? {})

/** 整条栏背景色：走 mq（组件的交互一律发主题，不直连 store） */
function patchBarBackground(color) {
  getMq().emit(EventNames.barSetBackground, { bar: bar.value, color })
}

/**
 * 切换视图条目内嵌的 view。
 * 必须同时处理 instanceKey：从多实例（如 search 带 searchBox）切到单例时，
 * 残留的 key 会让「配套变量 key」下拉被 v-if 隐藏 —— 用户看不到也删不掉。
 */
function chooseViewKey(viewKey) {
  if (!current.value) return
  const def = getView(viewKey)
  const multi = def?.mode === 'multi'
  patch({
    viewKey,
    instanceKey: multi ? (current.value.instanceKey ?? keys.list[0]?.key ?? null) : null,
  })
}

/** 某个区的背景色：走 mq */
function patchZoneBackground(zone, color) {
  getMq().emit(EventNames.zoneSetBackground, { bar: bar.value, zone, color })
}

/** 栏级页签：条目 / 背景 */
const subTabs = [
  { key: 'items', label: '条目', icon: 'list' },
  { key: 'bg', label: '背景', icon: 'palette' },
]
const subTab = ref('items')

const currentMenuNode = computed(() => {
  const item = current.value
  if (!item || item.kind !== 'menu' || !edit.selectedMenuNodeId) return null
  return config.findMenuNode(item.id, edit.selectedMenuNodeId, bar.value)
})

/* ── 视图条目的 view 选择 ── */
const viewOptions = computed(() => config.viewOptions(current.value ? `bar:${bar.value}:${current.value.id}` : ''))
const currentDef = computed(() => (current.value?.viewKey ? getView(current.value.viewKey) : undefined))
const keyList = computed(() => keys.list)

function itemsOf(zone) {
  return items.value.filter((i) => (i.zone ?? 'left') === zone)
}

function kindLabel(kind) {
  return kind === 'menu' ? '菜单' : kind === 'view' ? '视图' : '按钮'
}

function itemIcon(item) {
  if (item.icon) return item.icon
  if (item.kind === 'menu') return 'menu'
  if (item.kind === 'view') return 'search'
  return 'square'
}

function nextZone(zone) {
  const idx = zones.findIndex((z) => z.value === zone)
  return zones[Math.min(zones.length - 1, idx + 1)].value
}

function prevZone(zone) {
  const idx = zones.findIndex((z) => z.value === zone)
  return zones[Math.max(0, idx - 1)].value
}

function nextZoneLabel(zone) {
  return zones.find((z) => z.value === nextZone(zone))?.label ?? ''
}

function prevZoneLabel(zone) {
  return zones.find((z) => z.value === prevZone(zone))?.label ?? ''
}

/**
 * 条目列表变化后保证有选中项（不落到空面板）。
 * 原为 `watch(items, { immediate: true })` —— 改为挂载兜底 + 订阅端收敛
 * （`lf-bar-item-remove` 已在 `stores/mqCommands` 内选中同区首项），不侦听 state（§4.2 禁 watch）。
 */
function ensureSelection() {
  const list = items.value
  if (!list.some((i) => i.id === selectedId.value)) {
    edit.selectedBarItem[bar.value] = list[0]?.id ?? ''
  }
}
onMounted(ensureSelection)

function patch(obj) {
  if (!current.value) return
  config.patchBarItem(bar.value, current.value.id, obj)
}

function patchMenu(obj) {
  if (!current.value || !currentMenuNode.value) return
  config.patchMenuNode(bar.value, current.value.id, currentMenuNode.value.id, obj)
}
</script>

<style scoped>
.bar-editor {
  display: flex;
  min-height: 0;
  height: 100%;
  width: 100%;
}

.be-left {
  flex: none;
  width: 264px;
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--lf-border);
  background: #fbfcfe;
  min-height: 0;
}

.be-right {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.be-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--lf-text-weak);
  font-size: 13px;
  padding: 24px;
  text-align: center;
}

/* 栏级页签：条目 / 背景 */
.be-subtabs {
  flex: none;
  display: flex;
  gap: 2px;
  padding: 5px 8px;
  border-bottom: 1px solid var(--lf-border);
  background: #f7f9fc;
}

.be-subtab {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 12px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--lf-text-weak);
  font-size: 12px;
  cursor: pointer;
}

.be-subtab.active {
  background: #fff;
  color: var(--lf-accent);
  font-weight: 600;
  box-shadow: 0 1px 3px rgba(20, 30, 50, 0.12);
}

/* 背景页签 */
.be-bg {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 12px;
}

.bg-group {
  border: 1px solid var(--lf-border);
  border-radius: 8px;
  padding: 10px;
  margin-bottom: 10px;
  background: #fff;
}

.bg-zones {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}

.bg-zones .bg-group {
  margin-bottom: 0;
}

/* ── 按钮编辑器：两列表单 ── */
.form-cols {
  display: flex;
  gap: 16px;
  align-items: flex-start;
}

.form-cols .col {
  flex: 1;
  min-width: 0;
}

/* ── 菜单编辑器：左树 / 中编辑 / 右外观 ── */
.menu-split {
  flex: 1;
  min-height: 0;
  display: flex;
}

.ms-tree,
.ms-edit,
.ms-own {
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}

.ms-tree {
  flex: none;
  width: 280px;
  border-right: 1px solid var(--lf-border);
  background: #fbfcfe;
}

.ms-edit {
  flex: 1;
}

.ms-own {
  flex: none;
  width: 262px;
  border-left: 1px solid var(--lf-border);
  background: #fbfcfe;
}

.ms-title {
  flex: none;
  height: 28px;
  display: flex;
  align-items: center;
  padding: 0 10px;
  font-size: 12px;
  font-weight: 600;
  color: var(--lf-text-weak);
  border-bottom: 1px solid var(--lf-border);
  background: #f7f9fc;
}

.ms-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 8px 10px;
}

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
  padding: 10px;
}

.pane-foot {
  flex: none;
  display: flex;
  gap: 5px;
  padding: 6px;
  border-top: 1px solid var(--lf-border);
  background: #fbfcfe;
  flex-wrap: wrap;
}

.pane-foot .lf-btn {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.pane-head .kind-tag {
  font-size: 11px;
  color: var(--lf-accent);
  background: var(--lf-accent-soft);
  border-radius: 8px;
  padding: 1px 7px;
}

.zone-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--lf-text-weak);
  padding: 8px 10px 4px;
  font-weight: 600;
}

.zone-title .zcount {
  color: var(--lf-accent);
  background: var(--lf-accent-soft);
  border-radius: 8px;
  padding: 0 5px;
}

.zone-empty {
  font-size: 11px;
  color: var(--lf-text-weak);
  padding: 0 10px 4px;
}

.item-row {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 28px;
  padding: 0 6px 0 10px;
  border-radius: 6px;
  cursor: pointer;
}

.item-row:hover {
  background: var(--lf-tree-hover);
}

.item-row.selected {
  background: var(--lf-select);
  box-shadow: inset 2px 0 0 var(--lf-select-border);
}

.item-row .ico {
  color: var(--lf-accent);
  display: inline-flex;
}

.item-row .lbl {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item-row .kind {
  font-size: 10px;
  color: var(--lf-text-weak);
  background: #eef1f6;
  border-radius: 8px;
  padding: 0 5px;
}

.mini {
  width: 20px;
  height: 20px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--lf-text-weak);
  font-size: 11px;
  line-height: 1;
}

.mini:hover:not(:disabled) {
  background: #e2e7f0;
  color: var(--lf-accent);
}

.mini.danger:hover {
  background: #ffeaea;
  color: #d33;
}

.icon-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.icon-line {
  display: flex;
  align-items: center;
  gap: 6px;
}

.icon-line input {
  flex: 1;
  min-width: 0;
  height: 25px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  padding: 0 6px;
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

.icon-presets {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.icon-chip {
  height: 24px;
  min-width: 24px;
  font-size: 13px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.size-row {
  display: flex;
  gap: 10px;
}

.size-row .lf-field {
  flex: 1;
}

.desc {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: var(--lf-text-weak);
  margin: 4px 0 0;
}

.mn-label-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}

.mn-label-row input[type='text'] {
  flex: 1;
  min-width: 120px;
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--lf-border-strong);
  border-radius: var(--lf-radius);
}

.mn-label-row .icon-input {
  flex: none;
  width: 120px;
}

.mn-sub-row {
  margin-bottom: 8px;
}

.tip {
  font-size: 11px;
  color: var(--lf-text-weak);
  line-height: 1.6;
  margin: 4px 0 0;
}

.group-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  font-size: 12px;
  margin: 10px 0 8px;
  color: var(--lf-text);
}

.group-title::before {
  content: '';
  width: 3px;
  height: 12px;
  border-radius: 2px;
  background: var(--lf-accent);
}
</style>

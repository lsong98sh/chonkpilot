<template>
  <div ref="barRef" class="tabbed" :class="[`tabs-${position}`, { vertical: isVertical }]">
    <!-- 标签栏：外层 overflow:hidden 裁剪，内层 max-content 保持自然宽 -->
    <div class="tabbar">
      <div ref="innerRef" class="tab-scroll">
        <button
          v-for="tab in displayTabs"
          :key="tab.key"
          class="tab"
          :class="{ active: tab.key === activeKey, pinned: tab.pinned }"
          :data-tab-key="tab.key"
          :data-pinned="tab.pinned === true ? 'true' : 'false'"
          :title="tab.title"
          v-mq:[tab.topic].click="tab.payload"
          v-mq:[tab.topic].dblclick="() => pinPayload(tab)"
          v-mq:[EventNames.tabMenuOpen].contextmenu="(e) => ctxPayload(tab, e)"
        >
          <LfIcon v-if="tab.pinned" class="tab-pin" name="pin" :size="11" />
          <LfIcon :name="tab.icon" :size="13" class="tab-ico" />
          <span class="tab-txt">{{ tab.label }}</span>
          <span v-if="tab.instanceKey" class="tab-key">{{ tab.instanceKey }}</span>
          <button
            v-if="tab.closable"
            class="tab-close"
            :title="tr('container.tabsClose')"
            v-mq:[EventNames.tabsClose].click.stop="{ scope: ctxScope, ownerId: ctxOwnerId, key: tab.key }"
          >
            ✕
          </button>
        </button>
        <div v-if="!tabs.length" class="tab-placeholder">{{ emptyText || tr('container.tabsEmpty') }}</div>
      </div>

      <!-- 溢出触发：绝对定位覆盖在末端（不占 flex 位，避免"有按钮→更挤"回路） -->
      <div
        v-if="needMore"
        ref="moreRef"
        class="tab-more"
        :class="{ open: moreOpen }"
        :title="tr('container.tabsMoreTip')"
        v-mq:[EventNames.containerClick].click="() => toggleMore()"
      >
        <LfIcon name="ellipsis" :size="14" />
        <span v-if="hiddenCount" class="tab-more-count">{{ hiddenCount }}</span>
      </div>

      <div v-if="showCloseActive" class="tab-actions">
        <button
          class="lf-btn ghost tiny"
          :title="tr('container.tabsCloseActive')"
          :disabled="activeIndex < 0"
          v-mq:[closeActiveTopic].click="closeActivePayload"
        >
          <LfIcon name="x" :size="12" />
        </button>
      </div>
    </div>

    <!-- 内容 -->
    <div class="tab-body">
      <slot />
    </div>

    <!-- 溢出弹框：列出全部标签页；选中 → 该项移到首位 -->
    <Teleport to="body">
      <div
        v-if="moreOpen && displayTabs.length"
        :ref="setMorePopRef"
        class="tab-more-pop"
        :style="morePopStyle"
        v-mq:[EventNames.containerClick].mousedown.stop
      >
        <div class="tab-more-title">{{ tr('container.tabsAll', { count: displayTabs.length }) }}</div>
        <div class="tab-more-list">
          <div
            v-for="tab in displayTabs"
            :key="tab.key"
            class="tab-more-item"
            :class="{ active: tab.key === activeKey }"
            :title="tab.title"
            v-mq:[EventNames.tabsMoveFront].click="() => selectFromMore(tab)"
          >
            <LfIcon v-if="tab.pinned" class="tab-pin" name="pin" :size="11" />
            <LfIcon :name="tab.icon" :size="13" />
            <span class="tab-more-name">{{ tab.label }}</span>
            <span v-if="tab.instanceKey" class="tab-key">{{ tab.instanceKey }}</span>
            <button
              v-if="tab.closable"
              class="tab-more-close"
              :title="tr('container.tabsClose')"
              v-mq:[EventNames.tabsClose].click.stop="{ scope: ctxScope, ownerId: ctxOwnerId, key: tab.key }"
            >
              ✕
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 标签页右键菜单：固定 / 关闭 / 关闭右侧 / 关闭其他 / 关闭全部
         ⚠️ 必须按 ownerId 作用域化：`edit.tabMenu` 是全局单例，布局里同时存在多个
         tab 容器（自由 + 固定）时，不加判断会让**每个**容器都渲染一份菜单，
         同坐标叠在一起 —— 最上面那份可能是 menuIndex = -1 的空菜单，菜单项全灰、点了没反应。 -->
    <Teleport to="body">
      <div
        v-if="menu.visible && menu.ownerId === ctxOwnerId"
        class="tab-ctx-mask"
        v-mq:[EventNames.tabMenuClose].mousedown
      >
        <div
          class="tab-ctx"
          :style="{ left: `${menu.x}px`, top: `${menu.y}px` }"
          v-mq:[EventNames.containerClick].mousedown.stop
        >
          <button class="tab-ctx-item" v-mq:[EventNames.tabsPin].click="() => runMenu()">
            <LfIcon :name="menuTabPinned ? 'pin-off' : 'pin'" :size="12" />
            {{ menuTabPinned ? tr('container.menuUnpin') : tr('container.menuPin') }}
          </button>
          <div class="tab-ctx-sep" />
          <button class="tab-ctx-item" v-mq:[EventNames.tabsCloseThis].click="() => runMenu()">
            <LfIcon name="x" :size="12" /> {{ tr('container.menuCloseThis') }}
          </button>
          <button
            class="tab-ctx-item"
            :disabled="!canCloseLeft"
            v-mq:[EventNames.tabsCloseLeft].click="() => runMenu()"
          >
            <LfIcon name="arrow-left-to-line" :size="12" /> {{ tr('container.menuCloseLeft') }}
          </button>
          <button
            class="tab-ctx-item"
            :disabled="!canCloseRight"
            v-mq:[EventNames.tabsCloseRight].click="() => runMenu()"
          >
            <LfIcon name="arrow-right-to-line" :size="12" /> {{ tr('container.menuCloseRight') }}
          </button>
          <button
            class="tab-ctx-item"
            :disabled="!canCloseOthers"
            v-mq:[EventNames.tabsCloseOthers].click="() => runMenu()"
          >
            <LfIcon name="copy" :size="12" /> {{ tr('container.menuCloseOthers') }}
          </button>
          <div class="tab-ctx-sep" />
          <button class="tab-ctx-item danger" v-mq:[EventNames.tabsCloseAll].click="() => runMenu()">
            <LfIcon name="trash-2" :size="12" /> {{ tr('container.menuCloseAll') }}
          </button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, onUpdated, ref } from 'vue'
import { useEditStore } from '../../stores/edit'
import { EventNames } from '../../events/event-names'
import { tr } from '../../deps.js'

/**
 * tab 容器外壳：**标签栏位置可配置（上/下/左/右）** + **溢出 "..." 弹框** + **标签右键菜单**。
 *
 * 溢出机制（照抄 chonkpilot 的 components/tabs/TabBar.vue）：
 *   - 外层 `.tabbar`   = ResizeObserver 观察尺寸 + `overflow:hidden` 裁剪（不出现滚动条）；
 *   - 内层 `.tab-scroll` = `width:max-content`（内容自然宽，不收缩、不隐藏多余标签）；
 *   - 尺寸变化 → 判「**最后一个标签的位置**」是否越过外层可用宽 → 决定是否出现 "..."；
 *   - "..." 绝对定位覆盖在末端（自带底色遮住下方内容），**不占 flex 位**（判定无反馈回路）；
 *   - 弹框列出**全部**标签；选中某项 → 该项移到首位、其余保持相对顺序顺移
 *     （只改内部显示顺序 + 派发选中事件，不改外部传入的 tabs 顺序）。
 */
const props = defineProps({
  /** [{ key, label, icon, instanceKey, title, closable, pinned, topic, payload, closeTopic, closePayload }] */
  tabs: { type: Array, default: () => [] },
  /** 当前激活 key（'' = 无） */
  activeKey: { type: String, default: '' },
  /** 当前激活下标（-1 = 无），用于「关闭当前」按钮 */
  activeIndex: { type: Number, default: -1 },
  /** top | bottom | left | right */
  position: { type: String, default: 'top' },
  emptyText: { type: String, default: '' },
  showCloseActive: { type: Boolean, default: false },
  closeActiveTopic: { type: String, default: '' },
  closeActivePayload: { type: [Object, Function, String, Number, Boolean], default: null },
  /** 右键菜单 / 溢出弹框作用域：'free'（自由容器 key = tabId）| 'fixed'（固定容器 key = 标签 key） */
  ctxScope: { type: String, default: 'free' },
  /** 作用域所有者 id（自由容器 = 区域 id / 固定容器 = 节点 id） */
  ctxOwnerId: { type: String, default: '' },
})

const edit = useEditStore()
const isVertical = computed(() => props.position === 'left' || props.position === 'right')

const barRef = ref(null)
const innerRef = ref(null)
const moreRef = ref(null)
const morePopRef = ref(null)
const morePopStyle = ref({})

/** 溢出判定结果 */
const needMore = ref(false)
/** 被裁掉的标签数量（按钮角标） */
const hiddenCount = ref(0)

const moreOpen = computed(() => edit.tabMore[props.ctxOwnerId] === true)
const menu = computed(() => edit.tabMenu)

/* ── 显示顺序 ──
   直接用外部传入的顺序（`props.tabs`）。**不在组件内维护「显示顺序快照」**：
   一旦外部因为「固定排到最前」「关闭标签」等原因重排，快照会把它挡掉，
   出现 DOM 与 store 不一致。溢出弹框的「选中置首」改成派发 mq 命令，由 store 真正重排。 */
const displayTabs = computed(() => props.tabs || [])

/** 标签按钮集合（跳过 placeholder 这类非标签子元素） */
function tabEls() {
  const inner = innerRef.value
  if (!inner) return []
  return Array.from(inner.querySelectorAll('.tab'))
}

/* ── 溢出判定：最后一个标签是否越过外层可用宽 ───────────── */
/** 只在判定结果真的变化时写 ref，避免 onUpdated → 写 ref → 再更新 的递归 */
function setNeedMore(next, hidden) {
  if (needMore.value !== next) needMore.value = next
  if (hiddenCount.value !== hidden) hiddenCount.value = hidden
}

function recompute() {
  const bar = barRef.value
  const inner = innerRef.value
  if (!bar || !inner || !props.tabs.length) {
    setNeedMore(false, 0)
    return
  }
  const barRect = bar.getBoundingClientRect()
  const els = tabEls()
  const last = els[els.length - 1]
  const vertical = isVertical.value
  const padRight = parseFloat(getComputedStyle(bar).paddingRight) || 0
  const padBottom = parseFloat(getComputedStyle(bar).paddingBottom) || 0
  const actionsW = props.showCloseActive ? 30 : 0
  const limit = vertical
    ? barRect.bottom - padBottom
    : barRect.right - padRight - actionsW

  if (!last) {
    setNeedMore(false, 0)
    return
  }
  const overflow = (vertical ? last.getBoundingClientRect().bottom : last.getBoundingClientRect().right) > limit + 0.5
  if (!overflow) {
    setNeedMore(false, 0)
    return
  }
  const hidden = els.filter((el) => {
    const r = el.getBoundingClientRect()
    return (vertical ? r.bottom : r.right) > limit + 0.5
  }).length
  setNeedMore(true, hidden)
}

/* ── 溢出弹框定位：锚定 "..." 按钮 + 夹到视口内 + 空间不足翻转 ── */
function computeMorePop() {
  const btn = moreRef.value
  const pop = morePopRef.value
  if (!btn || !pop) return
  const r = btn.getBoundingClientRect()
  const pw = pop.offsetWidth || 260
  const ph = pop.offsetHeight || 0
  const gap = 6
  let left = r.right - pw
  left = Math.max(4, Math.min(left, window.innerWidth - pw - 4))
  let top
  if (props.position === 'top' || props.position === 'left') {
    top = r.bottom + gap
    if (top + ph > window.innerHeight - 4) top = r.top - ph - gap
  } else {
    top = r.top - ph - gap
    if (top < 4) top = r.bottom + gap
  }
  const next = {
    position: 'fixed',
    top: `${Math.max(4, Math.min(top, window.innerHeight - ph - 4))}px`,
    left: `${left}px`,
  }
  // 只在真的变化时写：`onUpdated` 里重算，值不变则不写 → 不会自我触发递归
  if (morePopStyle.value.top === next.top && morePopStyle.value.left === next.left) return
  morePopStyle.value = next
}

/** 溢出弹框挂载即定位（替代原「watch(打开态)」：挂载点即打开点） */
function setMorePopRef(el) {
  morePopRef.value = el
  if (el) nextTick(computeMorePop)
}

function toggleMore() {
  edit.toggleTabMore(props.ctxOwnerId)
  if (edit.tabMore[props.ctxOwnerId]) nextTick(computeMorePop)
}

/** 弹框里选中：派发「移到首位」命令（由 store 真正重排，见 mqCommands 的 tabs-move-front） */
function selectFromMore(tab) {
  edit.closeTabMore()
  return { scope: props.ctxScope, ownerId: props.ctxOwnerId, key: tab.key }
}

/* ── 右键菜单 ──────────────────────────────────────────── */
function ctxPayload(tab, e) {
  // 打开菜单（写 edit store），并把点击坐标带上，由 tab-menu 订阅者落到菜单位置
  edit.openTabMenu(props.ctxScope, props.ctxOwnerId, tab.key, e.clientX, e.clientY)
  return { scope: props.ctxScope, ownerId: props.ctxOwnerId, key: tab.key, x: e.clientX, y: e.clientY }
}

/** 菜单项通用载荷：先取值、再收起菜单（顺序不能反，否则读到空菜单态） */
function runMenu() {
  const m = menu.value
  const payload = { scope: m.scope, ownerId: m.ownerId, key: m.key }
  edit.closeTabMenu()
  return payload
}

/* ── 右键菜单可用性（对应 VS Code / 参考工程的口径：没有可关的就禁用）── */

/** 菜单当前指向的标签下标（-1 = 找不到） */
const menuIndex = computed(() => {
  const key = menu.value.key
  if (!key) return -1
  return displayTabs.value.findIndex((t) => t.key === key)
})

/** 菜单指向的标签是否已固定（决定菜单文案：固定 / 取消固定） */
const menuTabPinned = computed(() => displayTabs.value[menuIndex.value]?.pinned === true)

function hasClosable(list) {
  return list.some((t) => t.closable !== false)
}

/** 左侧有可关闭的标签 */
const canCloseLeft = computed(() => {
  const i = menuIndex.value
  return i > 0 && hasClosable(displayTabs.value.slice(0, i))
})

/** 右侧有可关闭的标签 */
const canCloseRight = computed(() => {
  const i = menuIndex.value
  if (i < 0) return false
  return hasClosable(displayTabs.value.slice(i + 1))
})

/** 除自己外还有可关闭的标签 */
const canCloseOthers = computed(() => {
  const i = menuIndex.value
  if (i < 0) return false
  return hasClosable(displayTabs.value.filter((_, idx) => idx !== i))
})

function pinPayload(tab) {
  return { scope: props.ctxScope, ownerId: props.ctxOwnerId, key: tab.key }
}

/* ── 生命周期：ResizeObserver + 窗口 resize + 点外关闭 ──── */
let ro = null

function onWindowResize() {
  recompute()
  if (moreOpen.value) computeMorePop()
}

/**
 * 点外关闭右键菜单。
 * 注意：菜单项自身**不能**在这里被关掉 —— 不拦截的话 mousedown 先把菜单卸载，
 * 随后的 click 就永远到不了菜单项（命令不会执行）。
 */
function onDocMouseDown(e) {
  if (!menu.value.visible) return
  const t = e.target
  if (t && t.closest && (t.closest('.tab-ctx') || t.closest('.tab-ctx-mask'))) return
  edit.closeTabMenu()
}

onMounted(() => {
  try {
    ro = new ResizeObserver(recompute)
    if (barRef.value) ro.observe(barRef.value)
    if (innerRef.value) ro.observe(innerRef.value)
  } catch (e) {
    console.warn('[LfTabbedContainer] ResizeObserver unavailable', e)
  }
  document.addEventListener('mousedown', onDocMouseDown)
  window.addEventListener('resize', onWindowResize)
  nextTick(recompute)
})

/**
 * 「更新后重新量测」（替代原 2 条 watch）：`onUpdated` 即「本组件 DOM 更新完成」钩子，
 * 而原两条 watch 的依赖（tabs 长度 / key 串 / position / showCloseActive）都会引起本组件重渲染
 * → 语义等价，且不需要侦听 props（§4.2 禁 watch）。
 * 不会递归：`setNeedMore` 只在值变化时写 ref，`computeMorePop` 也只在位置变化时写。
 */
onUpdated(() => {
  recompute()
  if (moreOpen.value) computeMorePop()
})

onBeforeUnmount(() => {
  if (ro) {
    ro.disconnect()
    ro = null
  }
  document.removeEventListener('mousedown', onDocMouseDown)
  window.removeEventListener('resize', onWindowResize)
  // 只关自己的溢出弹框（不带 ownerId 会把别的容器的弹框一起关掉）
  edit.closeTabMore(props.ctxOwnerId)
})
</script>

<style scoped>
.tabbed {
  display: flex;
  height: 100%;
  min-height: 0;
  min-width: 0;
  background: #fff;
}

.tabbed.tabs-top,
.tabbed.tabs-bottom {
  flex-direction: column;
}

.tabbed.tabs-left,
.tabbed.tabs-right {
  flex-direction: row;
}

.tabbed.tabs-bottom .tabbar {
  order: 2;
  border-bottom: none;
  border-top: 1px solid var(--lf-border);
}

.tabbed.tabs-right .tabbar {
  order: 2;
  border-right: none;
  border-left: 1px solid var(--lf-border);
}

/* 外层：观察尺寸 + 裁剪（内层内容自然宽，多余标签被裁但不隐藏） */
.tabbar {
  flex: none;
  display: flex;
  align-items: stretch;
  background: #f3f5f9;
  border-bottom: 1px solid var(--lf-border);
  min-height: 28px;
  min-width: 28px;
  position: relative;
  overflow: hidden;
}

/* 内层：内容自然宽（max-content），不收缩、不裁切 —— 末位标签位置可被外层判定读到 */
.tab-scroll {
  display: flex;
  align-items: stretch;
  gap: 2px;
  width: max-content;
  flex: none;
  padding: 2px 6px 0;
}

.vertical .tabbar {
  flex-direction: column;
}

.vertical .tab-scroll {
  flex-direction: column;
  padding: 6px 2px 0 0;
  height: max-content;
}

.tab {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 8px;
  border: 1px solid transparent;
  border-bottom: none;
  border-radius: 6px 6px 0 0;
  background: #e8ecf3;
  color: var(--lf-text-weak);
  white-space: nowrap;
  font-size: 12px;
  cursor: pointer;
  max-width: 200px;
  flex: none;
}

.tab.active {
  background: #fff;
  color: var(--lf-text);
  border-color: var(--lf-border);
  font-weight: 600;
}

.tab.pinned {
  background: #dde7ff;
  border-color: #b9ccff;
}

.tab.pinned .tab-txt {
  font-style: italic;
}

/* 固定标签排在最前：加一条左侧竖条做视觉分组（竖排时自动变成上边横条） */
.tab.pinned::before {
  content: '';
  width: 2px;
  height: 12px;
  border-radius: 1px;
  background: var(--lf-accent);
  flex: none;
}

.tab-pin {
  color: var(--lf-accent);
  opacity: 0.85;
}

.tab-txt {
  overflow: hidden;
  text-overflow: ellipsis;
}

.tab-key {
  font-size: 10px;
  color: var(--lf-accent);
  background: var(--lf-accent-soft);
  border-radius: 8px;
  padding: 0 4px;
}

.tab-close {
  font-size: 10px;
  opacity: 0.5;
  padding: 0 2px;
  border: none;
  background: transparent;
  color: inherit;
  line-height: 1;
  border-radius: 3px;
  cursor: pointer;
}

.tab-close:hover {
  opacity: 1;
  color: #d33;
}

/* 竖排：标签去掉底边圆角 */
.vertical .tab {
  border-bottom: 1px solid transparent;
  border-radius: 6px;
  max-width: none;
}

.tabs-left .tab {
  border-radius: 0 6px 6px 0;
  border-left: 2px solid transparent;
}

.tabs-left .tab.active {
  border-left-color: var(--lf-accent);
}

.tabs-right .tab {
  border-radius: 6px 0 0 6px;
  border-right: 2px solid transparent;
}

.tabs-right .tab.active {
  border-right-color: var(--lf-accent);
}

.tab-placeholder {
  font-size: 11px;
  color: var(--lf-text-weak);
  padding: 4px 2px;
}

/**
 * 溢出触发按钮：绝对定位覆盖末端（不占 flex 位），自带底色遮住下方内容。
 * 注意：这里必须是**撑满整条标签栏**的覆盖层（而不只是按钮本身），
 * 否则居中定位的按钮会盖住下层的标签、吃掉它们的点击（点标签没反应）。
 */
.tab-more {
  position: absolute;
  right: 0;
  top: 0;
  bottom: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 30px;
  padding: 0 4px;
  cursor: pointer;
  color: var(--lf-text-weak);
  background: #f3f5f9;
  box-shadow: inset 1px 0 0 var(--lf-border);
}

.vertical .tab-more {
  right: auto;
  left: 0;
  top: auto;
  bottom: 0;
  width: 100%;
  min-width: 0;
  height: 26px;
  box-shadow: inset 0 1px 0 var(--lf-border);
}

.tab-more > * {
  pointer-events: none;
}

.tab-more:hover,
.tab-more.open {
  background: var(--lf-accent-soft);
  color: var(--lf-accent);
}

.tab-more-count {
  font-size: 10px;
  line-height: 1;
}

.tab-actions {
  flex: none;
  display: flex;
  align-items: center;
  padding: 0 4px;
}

.vertical .tab-actions {
  padding: 4px 0;
}

.tiny {
  height: 20px;
  padding: 0 5px;
}

.tab-body {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
}

.tab-body > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

/* ── 溢出弹框 ── */
.tab-more-pop {
  width: 260px;
  max-height: 320px;
  display: flex;
  flex-direction: column;
  background: #fff;
  border: 1px solid var(--lf-border-strong);
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.14);
  z-index: 3000;
  overflow: hidden;
}

.tab-more-title {
  flex: none;
  padding: 7px 12px;
  font-size: 12px;
  color: var(--lf-text-weak);
  border-bottom: 1px solid var(--lf-border);
}

.tab-more-list {
  overflow-y: auto;
  max-height: 280px;
}

.tab-more-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  cursor: pointer;
  font-size: 13px;
  color: var(--lf-text-weak);
}

.tab-more-item:hover {
  background: var(--lf-tree-hover);
}

.tab-more-item.active {
  color: var(--lf-accent);
}

.tab-more-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tab-more-close {
  width: 16px;
  height: 16px;
  line-height: 1;
  border: none;
  padding: 0;
  background: transparent;
  text-align: center;
  border-radius: 3px;
  color: var(--lf-text-weak);
  flex: none;
  font-size: 10px;
  cursor: pointer;
}

.tab-more-close:hover {
  background: #ffe6e6;
  color: #d33;
}

/* ── 标签右键菜单 ── */
.tab-ctx-mask {
  position: fixed;
  inset: 0;
  z-index: 3100;
}

.tab-ctx {
  position: fixed;
  min-width: 150px;
  background: #fff;
  border: 1px solid var(--lf-border-strong);
  border-radius: 8px;
  box-shadow: 0 10px 30px rgba(20, 30, 50, 0.2);
  padding: 4px;
}

.tab-ctx-item {
  display: flex;
  align-items: center;
  gap: 7px;
  width: 100%;
  padding: 6px 10px;
  border: none;
  border-radius: 5px;
  background: transparent;
  color: var(--lf-text);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}

.tab-ctx-item:hover {
  background: var(--lf-accent-soft);
  color: var(--lf-accent);
}

.tab-ctx-item:disabled {
  opacity: 0.42;
  cursor: not-allowed;
  color: var(--lf-text-weak);
}

.tab-ctx-item:disabled:hover {
  background: transparent;
  color: var(--lf-text-weak);
}

.tab-ctx-item.danger {
  color: #c62828;
}

.tab-ctx-item.danger:hover {
  background: #ffeaea;
}

.tab-ctx-sep {
  height: 1px;
  margin: 4px 8px;
  background: var(--lf-border);
}
</style>

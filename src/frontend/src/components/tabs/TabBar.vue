<template>
  <!-- 通用页签栏（页签列表 + 溢出 "..." 弹框 + 右键菜单）；配合内容栈（v-show）使用。
       溢出机制（2026-09-16 用户口径）：
         外层 `.tb-bar`   = ResizeObserver 观察尺寸 + `overflow:hidden` 裁剪；
         内层 `.tb-inner` = `width:max-content`（内容自然宽，不收缩、**不隐藏**多余页签）；
         尺寸变化 → 判「**最后一个页签的位置**」（右边界是否越过外层可用宽）→ 决定是否出现 "..." 按钮；
         需要时按钮**绝对定位**覆盖在右侧（自带底色遮住下方内容），**不占 flex 位**；
         多余页签不隐藏 → 最后一个页签可能被遮住一半（预期行为），它同时出现在弹框列表里；
         弹框列出**全部**页签；选中某项 → 该项移到首位、其余保持相对顺序顺移
         （**只改内部显示顺序，除选中事件外不通知外部**，不改 `props.tabs`）。
       位置可配：position=top（栏在上，用于面板子页签）| bottom（栏在下，preview 文件页签）。 -->
  <div ref="barRef" class="tb-bar" :class="'tb-' + position" @contextmenu.prevent>
    <div ref="innerRef" class="tb-inner">
      <div
        v-for="tab in displayTabs"
        :key="tab.key"
        class="tb-tab"
        :class="{
          active: tab.key === modelValue,
          'is-temporary': tab.temporary,
        }"
        :title="tab.title || tab.name"
        @click="$emit('update:modelValue', tab.key)"
        @dblclick="$emit('pin', tab.key)"
        @contextmenu.prevent.stop="openMenu(tab, $event)"
      >
        <span class="tb-name">{{ tab.name }}</span>
        <span
          v-if="tab.closable !== false"
          class="tb-close"
          @click.stop="$emit('close', tab.key)"
        >×</span>
      </div>
    </div>
    <div
      v-if="showMore && needMore"
      ref="moreRef"
      class="tb-more"
      :class="{ open: moreOpen }"
      :title="moreTitle"
      @click.stop="toggleMore"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <circle cx="5" cy="12" r="1.5" fill="currentColor" stroke="none" />
        <circle cx="12" cy="12" r="1.5" fill="currentColor" stroke="none" />
        <circle cx="19" cy="12" r="1.5" fill="currentColor" stroke="none" />
      </svg>
    </div>
    <Teleport to="body">
      <div v-if="moreOpen && displayTabs.length > 0" ref="morePopRef" class="tb-more-pop" :style="morePopStyle">
        <div class="tb-more-title">{{ moreTitle }}</div>
        <div class="tb-more-list">
          <div
            v-for="tab in displayTabs"
            :key="tab.key"
            class="tb-more-item"
            :class="{ active: tab.key === modelValue }"
            :title="tab.title || tab.name"
            @click="setFirst(tab.key)"
          >
            <span class="tb-more-name">{{ tab.name }}</span>
            <span v-if="tab.closable !== false" class="tb-more-close" @click.stop="$emit('close', tab.key)">×</span>
          </div>
        </div>
      </div>
    </Teleport>
    <Teleport to="body">
      <!-- 右键菜单：**单一 MQ 路径** —— 四项只经 v-mq 触发（本组件不再 emit 本地直连动作），
           订阅侧由使用方完成（预览区 CodeView 订阅 preview-tab-close-* 四个主题）。
           容器 @click="closeMenu" 负责点后收起菜单（菜单内 @mousedown.stop 挡住了外部关闭）。 -->
      <div
        v-if="menu.visible"
        class="tb-ctx"
        :style="{ left: menu.x + 'px', top: menu.y + 'px' }"
        @contextmenu.prevent
        @mousedown.stop
        @click="closeMenu"
      >
        <div class="tb-ctx-item" v-mq:[mqTopics.closeThis].click="{ key: menu.key }">{{ closeThisLabel }}</div>
        <div class="tb-ctx-item" v-mq:[mqTopics.closeRight].click="{ key: menu.key }">{{ closeRightLabel }}</div>
        <div class="tb-ctx-item" v-mq:[mqTopics.closeOthers].click="{ key: menu.key }">{{ closeOthersLabel }}</div>
        <div class="tb-ctx-sep" />
        <div class="tb-ctx-item danger" v-mq:[mqTopics.closeAll].click="{ key: menu.key }">{{ closeAllLabel }}</div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { EventNames } from '../../events/event-names'

const props = defineProps({
  // 页签数据：{ key, name, title?, temporary?, closable? }
  tabs: { type: Array, default: () => [] },
  modelValue: { type: String, default: '' },
  position: { type: String, default: 'bottom' }, // top | bottom
  showMore: { type: Boolean, default: true },
  // 右键菜单四项的 MQ 事件（**唯一动作路径**，订阅侧 = 使用方）：
  // 形状 { closeThis, closeRight, closeOthers, closeAll }，缺省/缺项回落本组件通用家族（tab-ctx-*）；
  // 预览区（CodeView）传 preview-tab-close-* 家族 → 与测试脚本 mq_emit 的主题一致。
  ctxTopics: { type: Object, default: null },
})
// 右键菜单不再本地直连动作（收敛为单一 MQ 路径）：仅保留页签自身交互（选中/双击固定/× 关闭）。
const emit = defineEmits([
  'update:modelValue', 'close', 'pin',
])

const { t } = useI18n()

const mqTopics = computed(() => {
  const o = props.ctxTopics || {}
  return {
    closeThis: o.closeThis || EventNames.tabCtxThis,
    closeRight: o.closeRight || EventNames.tabCtxRight,
    closeOthers: o.closeOthers || EventNames.tabCtxOthers,
    closeAll: o.closeAll || EventNames.tabCtxAll,
  }
})

const moreTitle = computed(() => t('codeview.tab_more'))
const closeThisLabel = computed(() => t('common.tab_close_this'))
const closeRightLabel = computed(() => t('common.tab_close_right'))
const closeOthersLabel = computed(() => t('common.tab_close_others'))
const closeAllLabel = computed(() => t('common.tab_close_all'))

const barRef = ref(null)
const innerRef = ref(null)
// 溢出判定结果：内容（最后页签）右边界越过外层可用宽 → 需要 "..." 按钮
const needMore = ref(false)
// 弹框开关（按钮与弹框交互沿用旧实现：点外关闭 + 锚定按钮定位）
const moreOpen = ref(false)
const moreRef = ref(null)
const morePopRef = ref(null)
const morePopStyle = ref({})

// ── 内部显示顺序（弹框选中 → 置首）──────────────────────
// `orderKeys` = ref(null) → 跟随 `props.tabs` 原顺序（未重排过）；
// 非 null 时按它输出，并**过滤已删除的 key、追加新增的 key**（props 变化不丢页签）。
const orderKeys = ref(null)
const displayTabs = computed(() => {
  const list = props.tabs || []
  if (!orderKeys.value) return list
  const rest = new Map(list.map(x => [x.key, x]))
  const out = []
  for (const k of orderKeys.value) {
    const x = rest.get(k)
    if (x) { out.push(x); rest.delete(k) }  // 已删除的 key 自动跳过；重复 key 只取一次
  }
  for (const x of list) if (rest.has(x.key)) out.push(x)  // 新增的 key 按 props 原顺序追加
  return out
})
// props.tabs 变化 → 同步内部顺序（清理已删 key）+ 重新判定溢出
function syncOrder() {
  if (orderKeys.value) orderKeys.value = displayTabs.value.map(x => x.key)
}

// 溢出判定：口径 = **最后一个页签的位置**（其右边界 > 外层可用宽右边界 → 需要按钮）。
// 按钮为绝对定位、不占布局宽 → 判定不引入反馈回路（不会出现"有按钮→更挤→更多按钮"）。
function recompute() {
  const bar = barRef.value
  const inner = innerRef.value
  if (!bar || !inner || !props.tabs.length) { needMore.value = false; return }
  const last = inner.querySelector('.tb-tab:last-child')
  const padRight = parseFloat(getComputedStyle(bar).paddingRight) || 0
  const availRight = bar.getBoundingClientRect().right - padRight
  if (!last) {
    // 页签尚未渲染（无布局）：退化为内容宽比较
    needMore.value = inner.scrollWidth > bar.clientWidth
    return
  }
  needMore.value = last.getBoundingClientRect().right > availRight + 0.5
}

// 弹框选中：把该项移到**内部顺序首位**，其余保持相对顺序顺移；
// 只 emit 选中事件（`update:modelValue`）+ 关闭弹框 —— **不通知外部顺序变化、不改 `props.tabs`**。
function setFirst(key) {
  const cur = displayTabs.value.map(x => x.key)
  if (!cur.includes(key)) return
  orderKeys.value = [key, ...cur.filter(k => k !== key)]
  emit('update:modelValue', key)
  moreOpen.value = false
  nextTick(recompute)
}

// 溢出页签弹框定位：贴近 "..." 触发按钮 + 夹到视口内（不越界）；
// 空间不足时翻转方向（bottom 栏优先向上、top 栏优先向下）。
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
  if (props.position === 'top') {
    top = r.bottom + gap
    if (top + ph > window.innerHeight - 4) top = r.top - ph - gap
  } else {
    top = r.top - ph - gap
    if (top < 4) top = r.bottom + gap
  }
  morePopStyle.value = {
    position: 'fixed',
    top: Math.max(4, Math.min(top, window.innerHeight - ph - 4)) + 'px',
    left: left + 'px',
  }
}

function toggleMore() {
  moreOpen.value = !moreOpen.value
  if (moreOpen.value) nextTick(computeMorePop)
}

function onWindowResize() {
  recompute()
  if (moreOpen.value) computeMorePop()
}

const menu = ref({ visible: false, x: 0, y: 0, key: null })
function openMenu(tab, e) {
  // 右键菜单：夹到视口内，避免贴边越界
  const MW = 168
  const MH = 150
  let x = e.clientX
  let y = e.clientY
  if (x + MW > window.innerWidth) x = window.innerWidth - MW - 8
  if (y + MH > window.innerHeight) y = window.innerHeight - MH - 8
  menu.value = { visible: true, x: Math.max(4, x), y: Math.max(4, y), key: tab.key }
}
function closeMenu() { menu.value.visible = false }

function onDocMouseDown() { closeMenu() }

let ro = null
let w = null
onMounted(() => {
  try {
    ro = new ResizeObserver(recompute)
    // 外层尺寸变化（+ 内层内容宽变化 = 页签增删/改名）都要重判溢出；
    // 按钮为绝对定位不占布局宽 → 观测不引入反馈回路。
    if (barRef.value) ro.observe(barRef.value)
    if (innerRef.value) ro.observe(innerRef.value)
  } catch (e) { console.warn('[TabBar] ResizeObserver unavailable', e) }
  // props.tabs 变化 → 同步内部显示顺序（清理已删 key）+ 重新判定溢出
  // 注：必须用 getter 形式（`() => props.tabs`）；直接传数组会被 Vue 当作「多个 source」逐项展开，
  // 页签增删时不会触发 → 按钮/顺序都会停在旧状态（实跑发现的缺陷）。
  w = watch(() => props.tabs, () => nextTick(() => { syncOrder(); recompute() }), { deep: true })
  document.addEventListener('mousedown', onDocMouseDown)
  window.addEventListener('resize', onWindowResize)
  nextTick(recompute)
})
onUnmounted(() => {
  if (ro) { ro.disconnect(); ro = null }
  if (w) { w(); w = null }
  document.removeEventListener('mousedown', onDocMouseDown)
  window.removeEventListener('resize', onWindowResize)
})
</script>

<style scoped>
.tb-bar {
  display: flex;
  align-items: center;
  gap: 0;
  padding: 2px 6px;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
  height: 26px;
  /* 外层：ResizeObserver 观察 + 裁剪（内层内容自然宽，多余页签被裁但不隐藏） */
  position: relative;
  overflow: hidden;
  min-width: 0;
}
.tb-bar.tb-top {
  border-top: none;
  border-bottom: 1px solid var(--border);
}
/* 内层：内容自然宽（max-content），不收缩、不裁切 —— 页签位置可被外层"最后页签位置"判定读到 */
.tb-inner {
  display: flex;
  align-items: center;
  gap: 2px;
  width: max-content;
  flex: none;
}
.tb-tab {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0 8px;
  height: 22px;
  max-width: 180px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
  /* 非激活页签文字：走主题 token --tab-inactive-fg（A6，2026-09-24 用户口径）——
     与激活页签（--text-primary）逐 scheme 均有可见差别（light 更淡 / dark·nord 更暗）。 */
  color: var(--tab-inactive-fg);
  background: transparent;
  flex-shrink: 0;
  border: 1px solid transparent;
  white-space: nowrap;
}
.tb-tab.is-temporary .tb-name { font-style: italic; }
.tb-tab:hover { background: var(--bg-hover, rgba(0, 0, 0, 0.04)); }
.tb-tab.active {
  background: var(--bg-surface);
  color: var(--text-primary);
  border-color: var(--border);
}
.tb-name {
  overflow: hidden;
  text-overflow: ellipsis;
}
.tb-close {
  width: 14px;
  height: 14px;
  line-height: 12px;
  text-align: center;
  border-radius: 3px;
  font-size: 12px;
  color: var(--text-muted);
  flex-shrink: 0;
  visibility: hidden;
  opacity: 0;
  transition: opacity 0.12s;
}
.tb-tab:hover .tb-close { visibility: visible; opacity: 1; }
/* 危险态用主题 token（原 #f56c6c 为硬编码浅红，dark/nord 下与整体不协调）：
   实心填充用 --danger 底 + 主题最底层文字色（light 近白 / dark·nord 深色），对比度最佳。 */
.tb-close:hover { background: var(--danger); color: var(--bg-primary); }
/* 溢出触发按钮：**绝对定位覆盖**在右侧（不占 flex 位），自带底色遮住下方内容；
   外层 overflow:hidden 也不裁它（按钮完全落在栏内）。 */
.tb-more {
  position: absolute;
  right: 0;
  top: 50%;
  transform: translateY(-50%);
  z-index: 2;
  width: 30px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  cursor: pointer;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  box-shadow: inset 1px 0 0 var(--border);
}
.tb-more:hover, .tb-more.open {
  background: color-mix(in srgb, var(--text-primary) 8%, var(--bg-secondary));
  color: var(--text-primary);
}
.tb-more-pop {
  position: fixed;
  width: 260px;
  max-height: 320px;
  display: flex;
  flex-direction: column;
  background: var(--bg-surface, #fff);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.14);
  z-index: 3000;
  overflow: hidden;
}
.tb-more-title {
  flex-shrink: 0;
  padding: 7px 12px;
  font-size: 12px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border);
}
.tb-more-list { overflow-y: auto; max-height: 280px; }
.tb-more-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  cursor: pointer;
  font-size: 13px;
  /* 溢出弹框里的页签同为「页签列表」：非激活文字与页签栏同口径（A6） */
  color: var(--tab-inactive-fg);
}
.tb-more-item:hover { background: var(--bg-hover, rgba(0, 0, 0, 0.04)); }
.tb-more-item.active { color: var(--accent); }
.tb-more-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tb-more-close {
  width: 14px;
  height: 14px;
  line-height: 12px;
  text-align: center;
  border-radius: 3px;
  color: var(--text-secondary);
  flex-shrink: 0;
}
.tb-more-close:hover { background: color-mix(in srgb, var(--text-primary) 8%, transparent); color: var(--danger, #e53e3e); }
.tb-ctx {
  position: fixed;
  z-index: 1000;
  min-width: 140px;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.15);
  padding: 4px;
}
.tb-ctx-item {
  padding: 6px 12px;
  border-radius: 4px;
  font-size: 13px;
  color: var(--text-primary);
  cursor: pointer;
  white-space: nowrap;
}
.tb-ctx-item:hover { background: var(--bg-hover, rgba(0, 0, 0, 0.06)); }
.tb-ctx-item.danger { color: var(--danger); }
.tb-ctx-item.danger:hover { background: color-mix(in srgb, var(--danger) 10%, transparent); }
.tb-ctx-sep { height: 1px; margin: 4px 8px; background: var(--border); }
</style>

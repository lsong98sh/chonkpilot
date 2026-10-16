<template>
  <div
    ref="triggerRef"
    class="b-dropdown-trigger"
    :data-dropdown-name="name"
    v-mq:[EventNames.dropdownToggle].mouseenter="hoverPayload"
  >
    <slot name="trigger" :open="isOpen" />
    <Teleport to="body">
      <div
        v-if="isOpen"
        :ref="setPopRef"
        class="b-dropdown-popper"
        :class="{ 'b-dropdown-sub': submenu }"
        :data-dropdown-name="name"
        :style="popperStyle"
        v-mq:[EventNames.containerClick].mousedown.stop
      >
        <slot :close="close" />
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useEditStore } from '../../stores/edit'
import { EventNames } from '../../events/event-names'

/**
 * 下拉菜单容器：弹层 Teleport 到 body，并按触发元素实测位置固定定位（fixed）。
 *
 * 支持嵌套（子菜单）：每个下拉有自己的 name。
 *   - 子菜单默认 **hover 自动展开**（`openOnHover`），鼠标移上去就出侧面飞框；
 *     仍保留 click 切换，方便键盘 / 触屏用户。
 *   - 展开子菜单时用 `keep` 保留祖先链，不会把父菜单关掉；
 *   - 点击弹层内部不关闭；点击弹层外关闭全链。
 */
const props = defineProps({
  /** 下拉的唯一 key */
  name: { type: String, required: true },
  /** 弹层相对触发元素的水平对齐：left | right（仅 anchor='under' 时有意义） */
  align: { type: String, default: 'left' },
  /**
   * 弹出方向：
   *   'under'（默认）主下拉，挂在触发元素下方；
   *   'right' / 'left' 子菜单飞框，挂在触发元素**侧面**（右侧不够自动翻到左侧）。
   */
  anchor: { type: String, default: 'under' },
  /** 弹层最小宽度（px） */
  minWidth: { type: Number, default: 160 },
  /** 弹层最大高度（px） */
  maxHeight: { type: Number, default: 360 },
  /** 是否为子菜单（父弹层链路上的下拉不因自己打开而关闭） */
  submenu: { type: Boolean, default: false },
  /** 鼠标移入触发元素时是否自动展开（子菜单用；主下拉保持点击展开） */
  openOnHover: { type: Boolean, default: false },
})

const edit = useEditStore()
const triggerRef = ref(null)
const popRef = ref(null)
const rect = ref(null)
/** 上一次量到的弹层尺寸（用于侧向翻转判断） */
const popSize = ref({ w: 0, h: 0 })

const isOpen = computed(() => edit.dropdowns[props.name] === true)
const isFlyout = computed(() => props.anchor === 'right' || props.anchor === 'left')

const popperStyle = computed(() => {
  const r = rect.value
  if (!r) return { position: 'fixed', visibility: 'hidden' }
  const width = Math.max(props.minWidth, isFlyout.value ? 0 : r.width)
  const gap = 2

  if (isFlyout.value) {
    // 子菜单飞框：贴触发元素的右（左）侧，纵向与触发元素顶部对齐
    const w = popSize.value.w || width
    const h = popSize.value.h || 0
    // ⚠️ 翻转判断要用**父弹层**的边界，不能用触发条目自己的 right ——
    //    子菜单条目本身有缩进和箭头，它的 right 比父弹层窄一截，
    //    拿条目 right 去判断会以为「右边放不下」而误翻到左侧。
    const parentEl = triggerRef.value && triggerRef.value.closest ? triggerRef.value.closest('.b-dropdown-popper') : null
    const pr = parentEl ? parentEl.getBoundingClientRect() : r
    const parentLeft = pr.left
    const parentRight = pr.right

    let left
    if (props.anchor === 'right') {
      left = parentRight + gap
      if (left + w > window.innerWidth - 4) {
        // 右侧放不下 → 翻到父弹层左侧；左侧也放不下就贴视口右边
        const flipped = parentLeft - w - gap
        left = flipped >= 4 ? flipped : Math.max(4, window.innerWidth - w - 4)
      }
    } else {
      left = parentLeft - w - gap
      if (left < 4) left = parentRight + gap
    }
    left = Math.max(4, Math.min(left, Math.max(4, window.innerWidth - w - 4)))
    // 纵向：与触发条目顶对齐（略微上提），超出视口再上移
    let top = r.top - 6
    if (h && top + h > window.innerHeight - 4) top = Math.max(4, window.innerHeight - h - 4)
    top = Math.max(4, top)
    return {
      position: 'fixed',
      top: `${Math.round(top)}px`,
      left: `${Math.round(left)}px`,
      minWidth: `${width}px`,
      maxHeight: `${Math.min(props.maxHeight, window.innerHeight - top - 8)}px`,
      zIndex: '10001',
    }
  }

  let left = props.align === 'right' ? r.right - width : r.left
  // 越界时贴到视口内
  left = Math.max(4, Math.min(left, window.innerWidth - width - 4))
  const top = Math.min(r.bottom + 4, Math.max(4, window.innerHeight - 40))
  return {
    position: 'fixed',
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`,
    minWidth: `${width}px`,
    maxHeight: `${props.maxHeight}px`,
    zIndex: props.submenu ? '10001' : '10000',
  }
})

function measure() {
  const el = triggerRef.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const cur = rect.value
  // 只在**真的变化**时写 reactive：
  // 函数式 ref 每轮 patch 都会被重入（Vue `renderer#patch` 末尾无条件 `setRef`），
  // 若此处无条件写 `rect`，而 `popperStyle` 又依赖它 → 渲染自身触发的依赖被再次改写
  // → 微任务级无限更新循环（生产版 Vue 无 DEV 的递归上限守卫，表现为页面主线程**永久卡死**，
  // WebView2 `ExecuteScript` 一律超时）。同口径见 `LfTabbedContainer#computeMorePop`。
  if (cur && cur.left === r.left && cur.top === r.top &&
      cur.width === r.width && cur.height === r.height &&
      cur.right === r.right && cur.bottom === r.bottom) {
    return
  }
  rect.value = r
}

/** 量弹层自身尺寸（飞框翻转要用） */
function measurePop() {
  const el = popRef.value
  if (!el) return
  const r = el.getBoundingClientRect()
  if (!r.width || !r.height) return
  // 同上：值不变则不写，避免弹层自身尺寸的重复写入自触发渲染
  if (popSize.value.w === r.width && popSize.value.h === r.height) return
  popSize.value = { w: r.width, h: r.height }
}

function close() {
  edit.closeDropdowns()
}

/**
 * mouseenter 的 payload：只在 `openOnHover` 时给出「打开」意图 + 祖先链。
 * 鼠标移入时若已经展开，返回 false 让订阅方直接跳过（避免重复 setState 触发重渲染）。
 */
function hoverPayload(e) {
  if (!props.openOnHover) return false
  if (isOpen.value) return false
  return { key: props.name, keep: ancestorNames(e), open: true }
}

function onDocMouseDown(e) {
  const names = Object.keys(edit.dropdowns)
  if (!names.length) return
  const el = triggerRef.value
  if (el && el.contains(e.target)) return
  // 任意弹层 / 任意下拉触发器内部点击都不在此处关闭：
  //  - 弹层 Teleport 到 body，不是本组件的 DOM 子树；
  //  - 子菜单触发器在父弹层内部，若在 mousedown 阶段就把父菜单关掉，
  //    子菜单触发器会随父弹层一起卸载，click 就永远不会到达（子菜单打不开）。
  const t = e.target
  if (t.closest && (t.closest('.b-dropdown-popper') || t.closest('.b-dropdown-trigger'))) return
  // 点空白处：整条链一起关掉（不需要「保留祖先」）
  edit.closeDropdowns()
}

/** 当前展开的祖先下拉 name（沿 DOM 祖先链上的 data-dropdown-name 回溯） */
function ancestorNames(e) {
  const out = []
  let el = e && e.target ? e.target.parentElement : null
  while (el) {
    const name = el.dataset ? el.dataset.dropdownName : ''
    // 自己的 name 不算祖先
    if (name && name !== props.name) out.push(name)
    el = el.parentElement
  }
  return out
}

function onViewportChange() {
  if (isOpen.value) measure()
}

/**
 * 弹层挂载即量测（替代原 `watch(isOpen)`）。
 * 展开态由 mq（`lf-ui-dropdown-toggle`）驱动，弹层是 `v-if` 渲染 ——
 * 挂载这一刻就是「刚打开」，用函数式 ref 在这时量测，不需要侦听状态变化。
 *
 * ⚠️ 函数式 ref **不只挂载时调用**：元素每轮 patch 都会重入本函数（Vue 在 `patch` 末尾
 * 无条件 `setRef`）→ 量测写入必须幂等（见 `measure` / `measurePop` 的值比较），
 * 否则会自触发渲染形成死循环。
 */
function setPopRef(el) {
  popRef.value = el
  if (!el) return
  measure()
  nextTick(measurePop)
}

onMounted(() => {
  if (isOpen.value) measure()
  document.addEventListener('mousedown', onDocMouseDown, true)
  window.addEventListener('resize', onViewportChange)
  window.addEventListener('scroll', onViewportChange, true)
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocMouseDown, true)
  window.removeEventListener('resize', onViewportChange)
  window.removeEventListener('scroll', onViewportChange, true)
})
</script>

<style scoped>
.b-dropdown-trigger {
  position: relative;
  display: inline-flex;
}

.b-dropdown-popper {
  background: #fff;
  border: 1px solid var(--lf-border-strong);
  border-radius: 8px;
  box-shadow: 0 10px 30px rgba(20, 30, 50, 0.18);
  padding: 6px;
  overflow-y: auto;
  overflow-x: hidden;
  box-sizing: border-box;
}

.b-dropdown-sub {
  box-shadow: 0 12px 34px rgba(20, 30, 50, 0.22);
}
</style>

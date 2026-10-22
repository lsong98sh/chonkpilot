/**
 * 运行时单例 view 的「视图池」（WIN-027）。
 *
 * 目的：切换布局时**不销毁 / 不重建**单例 view —— 把 view 实例从布局树里提出来，
 * 由 `<LfViewPool>` **各挂载一次**；叶子 / 标签页 / 抽屉 / 栏条目只渲染空槽位
 * （`.lf-view-slot`）并把 DOM 目标注册到这里，池用 `<Teleport :to>` 把实例 DOM
 * 搬进当前槽位。切换布局 = 槽位变化 → DOM 移动，组件实例保持不变。
 *
 * 约定：
 *   - key = `${viewKey}|${instanceKey ?? ''}`（与页签稳定 key 同口径）；
 *   - 同一 key 有多个落点时**以最后一次注册为准**（重复打开同一单例时只有激活项拿得到）；
 *   - 槽位卸载只清「仍指向自己」的落点，避免把新槽位误清；
 *   - 已请求过的 key **常驻**（落到池内停车场隐藏保活），会话期内不主动释放。
 */
import { reactive } from 'vue'

export function viewPoolKey(viewKey, instanceKey) {
  return `${viewKey || ''}|${instanceKey ?? ''}`
}

export const viewPool = reactive({
  /** key -> { el, viewRef } */
  targets: {},
  /** 已请求挂载过的 key（顺序 = 首次请求顺序；池按此渲染） */
  keys: [],
  /** key -> 实例挂载次数（诊断：切换布局前后不变 = 未重建） */
  mounts: {},
})

/** 池内实例每次真正挂载时 +1（诊断计数；不参与业务） */
export function bumpViewMount(viewKey) {
  if (!viewKey) return
  viewPool.mounts[viewKey] = (viewPool.mounts[viewKey] || 0) + 1
}

export function registerViewTarget(key, el, viewRef) {
  if (!key || !el) return
  viewPool.targets[key] = { el, viewRef }
  if (!viewPool.keys.includes(key)) viewPool.keys.push(key)
}

export function unregisterViewTarget(key, el) {
  const cur = viewPool.targets[key]
  if (!cur) return
  if (el && cur.el !== el) return
  delete viewPool.targets[key]
}

/** 应用退出 / 池卸载：清空全部落点与常驻 key（下次挂载按需重来） */
export function resetViewPool() {
  viewPool.targets = {}
  viewPool.keys = []
  viewPool.mounts = {}
  animEls.clear()
}

export function targetOf(key) {
  return viewPool.targets[key]?.el ?? null
}

export function viewRefOf(key) {
  return viewPool.targets[key]?.viewRef ?? null
}

/** 该 viewKey 当前是否有落点（已在某个区域 / 页签 / 抽屉 / 栏位显示） */
export function hasViewTarget(viewKey) {
  if (!viewKey) return false
  return Object.keys(viewPool.targets).some((k) => splitKey(k).viewKey === viewKey)
}

/** key → { viewKey, instanceKey }（池渲染组件用；viewKey 从 key 反解） */
export function splitKey(key) {
  const i = String(key).indexOf('|')
  return i < 0 ? { viewKey: key, instanceKey: null } : { viewKey: key.slice(0, i), instanceKey: key.slice(i + 1) || null }
}

/* 只读诊断入口：L4 用「挂载次数切换前后不变」证明单例 view 未被重建 */
if (typeof window !== 'undefined') {
  window.__ckViewPool = {
    get mounts() {
      return { ...viewPool.mounts }
    },
    get targets() {
      return Object.keys(viewPool.targets)
    },
    get parked() {
      return viewPool.keys.filter((k) => !viewPool.targets[k])
    },
  }
}

/* ── 切换动画（FLIP）：位移 + 淡入，尊重 prefers-reduced-motion ── */

const animEls = new Map()

/** 池内每个 view 的动画包裹元素（由 LfViewPool 用函数式 ref 注册） */
export function setAnimEl(key, el) {
  if (el) animEls.set(key, el)
  else animEls.delete(key)
}

/** 布局切换前：抓取各 view 的当前位置 */
export function captureViewRects() {
  const out = new Map()
  animEls.forEach((el, key) => {
    const r = el.getBoundingClientRect()
    if (r.width > 0 && r.height > 0) out.set(key, r)
  })
  return out
}

/**
 * 布局切换后（DOM 已更新）：按旧位置做 FLIP。
 * 尺寸差异大时**不做缩放**（避免文字变形），只做位移与透明度。
 */
export function playViewTransitions(before) {
  if (!before || !before.size) return
  if (typeof window === 'undefined' || typeof requestAnimationFrame !== 'function') return
  try {
    if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
  } catch {
    /* 忽略 matchMedia 不可用 */
  }
  requestAnimationFrame(() => {
    animEls.forEach((el, key) => {
      const prev = before.get(key)
      if (!prev || typeof el.animate !== 'function') return
      const next = el.getBoundingClientRect()
      if (!next.width || !next.height) return // 落停车场（隐藏）→ 不播
      const dx = prev.left - next.left
      const dy = prev.top - next.top
      if (Math.abs(dx) < 1 && Math.abs(dy) < 1) return
      el.animate(
        [
          { transform: `translate(${dx}px, ${dy}px)`, opacity: 0.55 },
          { transform: 'translate(0px, 0px)', opacity: 1 },
        ],
        { duration: 200, easing: 'cubic-bezier(0.2, 0, 0, 1)' },
      )
    })
  })
}

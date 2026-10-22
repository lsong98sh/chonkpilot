import { cloneTree, collectLeaves, createLeaf, findLeaf, findNode, isSplit, removeNode, splitNode } from '../utils/layout'
import { isTabsContainer, sortPinnedFirst } from '../utils/regions'
import { STORAGE_KEY, readStorage } from '../config/config-normalize'
import { saveLayoutDoc } from '../deps.js'
import { useRuntimeStore } from './runtime'

/**
 * 布局树相关的 store 动作：初始化、布局编辑、区域（三种类型）、尺寸约束、同级交换。
 *
 * 通过 `...createLayoutActions()` 展开进 config store —— 动作里的 `this` 仍然指向 store，
 * 所以可以直接 `this.draft` / `this.markDirty()`。分文件只是为了别让单个 store 超过 1000 行。
 *
 * ⚠️ 这里是**逐字搬运**原有实现，不是重写：这些动作的边界处理（最后一个区域不可删、
 * `delete` 非响应式所以要重建对象、约束只落在一条轴上）都是踩过坑之后的结果。
 */
/**
 * 是否存在「有效可见」叶子：自身可见且所有祖先可见（UILAYOUT-2）。
 * 容器隐藏 → 其子树一律不算可见（子节点 visible 原样保留，但不参与判定）。
 */
export function hasVisibleLeaf(node, ancestorVisible = true) {
  if (!node) return false
  const self = ancestorVisible && node.visible !== false
  if (!self) return false
  if (!isSplit(node)) return true
  return (node.children ?? []).some((c) => hasVisibleLeaf(c, self))
}

export function createLayoutActions() {
  return {
    /* ---------------- 初始化 ---------------- */

    init() {
      const stored = readStorage()
      if (stored) {
        this.live = cloneTree(stored)
        this.draft = cloneTree(stored)
        this.savedJson = JSON.stringify(stored, null, 2)
      }
    },

    /**
     * 标记「草稿有未保存改动」。
     *
     * ⚠️ 这里**不再清空 messages**：以前任何一次 action（包括什么都没改的边界情况）
     * 都会顺手清掉上一次的校验错误，于是「保存被拦下」的错误列表会被随便一次点击抹掉，
     * 用户根本来不及看。错误信息的清空时机改到：save() 成功、revert()、resetAll()。
     */
    markDirty() {
      this.dirty = true
    },

    /* ---------------- 布局编辑 ---------------- */

    split(nodeId, direction) {
      this.draft.layout = splitNode(this.draft.layout, nodeId, direction).root
      this.markDirty()
    },

    addLeafToSplit(nodeId) {
      const node = findNode(this.draft.layout, nodeId)
      if (!node || !isSplit(node)) return
      const leaf = createLeaf(null, '')
      leaf.kind = 'single'
      node.children.push(leaf)
      const n = node.children.length
      node.sizes = Array.from({ length: n }, () => 1 / n)
      this.markDirty()
    },

    removeLeaf(nodeId) {
      const res = removeNode(this.draft.layout, nodeId)
      if (res) {
        this.draft.layout = res
        this.markDirty()
      } else {
        this.messages = ['至少保留一个区域，无法删除最后一个区域。']
      }
    },

    /**
     * 写入某个容器的子区占比。
     * target: 'draft'（内容设置预览里拖 → 改草稿，保存后生效）
     *       | 'live' （首页运行态拖 → 直接改生效布局，拖拽结束由 saveLiveLayout 落盘）
     */
    setSplitSizes(nodeId, sizes, target = 'draft') {
      const node = findNode(target === 'live' ? this.live.layout : this.draft.layout, nodeId)
      if (!node || !isSplit(node)) return
      node.sizes = sizes
      if (target === 'live') this.savedJson = JSON.stringify(this.live, null, 2)
      else this.markDirty()
    },

    /**
     * 运行态拖完分割条后落盘：把 live 配置写回（localStorage 兜底 + 宿主 `layout.free`）。
     * 不走 save()（那是编辑器的「校验 + 应用草稿」语义），但同样是配置持久化的出口。
     */
    saveLiveLayout() {
      this.savedJson = JSON.stringify(this.live, null, 2)
      try {
        localStorage.setItem(STORAGE_KEY, this.savedJson)
        this.dirty = false
      } catch {
        /* 忽略存储失败 */
      }
      // 宿主落库（§3.9 / P3③）：`gui.ui.save { layout: { free } }`；失败只提示，不阻断交互
      const wrote = saveLayoutDoc(this.live)
      if (wrote && typeof wrote.then === 'function') {
        wrote.then((r) => {
          if (r && r.ok === false) useRuntimeStore().setHint(`布局未落库：${r.error || '未知原因'}`)
        })
      }
    },

    setSplitDirection(nodeId, direction) {
      const node = findNode(this.draft.layout, nodeId)
      if (node && isSplit(node)) {
        node.direction = direction
        this.markDirty()
      }
    },

    setLeafTitle(nodeId, title) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf) return
      leaf.title = title || undefined
      this.markDirty()
    },

    /* ---------------- 区域显隐（WIN-025 / UILAYOUT-2） ----------------
       语义：`visible === false` = 隐藏；缺省 / true = 可见。
       - 叶子与容器同权（容器隐藏 = 整棵子树不显示，子节点 visible 原样保留）
       - `hideLocked === true` → 禁止**直接**隐藏（祖先连带不受限）
       - 至少保留一个「有效可见」叶子（自身与所有祖先都可见）
       隐藏**不删除节点**（保活），只由渲染层 `v-show` + 尺寸过滤承担。 */

    /** 设计态：改草稿（保存后生效）；叶子 / 容器均可 */
    setLeafVisible(nodeId, visible) {
      return this.setNodeVisible(nodeId, visible, 'draft')
    },

    /** 显隐通用入口（target: draft | live） */
    setNodeVisible(nodeId, visible, target = 'draft') {
      const root = target === 'live' ? this.live.layout : this.draft.layout
      const node = findNode(root, nodeId)
      if (!node) return false
      if (visible === false && node.hideLocked === true) return false
      const backup = node.visible
      node.visible = visible ? undefined : false
      if (!hasVisibleLeaf(root)) {
        node.visible = backup
        this.messages = ['至少需要保留一个可见区域（全部隐藏时无法保存）。']
        return false
      }
      if (target === 'live') this.saveLiveLayout()
      else this.markDirty()
      return true
    },

    /** 运行态：直接改 live 并落盘 */
    setLiveLeafVisible(nodeId, visible) {
      return this.setNodeVisible(nodeId, visible, 'live')
    },

    /** 设计态：设置「不允许隐藏」标记（叶子 / 容器均可） */
    setNodeHideLocked(nodeId, locked) {
      const node = findNode(this.draft.layout, nodeId)
      if (!node) return false
      node.hideLocked = locked ? true : undefined
      this.markDirty()
      return true
    },

    /** 运行态按 viewKey 切换区域显隐（顶栏 area-toggles；不写死 nodeId） */
    toggleLiveLeafByView(viewKey) {
      if (!viewKey) return false
      const leaf = collectLeaves(this.live.layout).find(
        (l) => l.view?.viewKey === viewKey || (l.tabs ?? []).some((t) => t.viewKey === viewKey),
      )
      if (!leaf) return false
      return this.setLiveLeafVisible(leaf.id, leaf.visible === false)
    },

    /* ---------------- 区域（三种类型） ---------------- */

    /** 切换区域类型：single | fixed-tabs | free-tabs */
    setRegionKind(nodeId, kind) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf || !['single', 'fixed-tabs', 'free-tabs'].includes(kind)) return
      if (kind === 'single') {
        leaf.kind = 'single'
        leaf.tabs = undefined
        if (!leaf.view) leaf.view = null
      } else {
        leaf.kind = kind
        leaf.view = undefined
        if (!Array.isArray(leaf.tabs)) leaf.tabs = []
      }
      this.markDirty()
    },

    /** 单容器：设置 view（viewKey 为空 → 空白区域） */
    setAreaView(nodeId, viewKey) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf) return
      leaf.kind = 'single'
      leaf.tabs = undefined
      leaf.view = viewKey ? { viewKey, instanceKey: null } : null
      this.markDirty()
    },

    setAreaInstanceKey(nodeId, instanceKey) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf || leaf.kind !== 'single' || !leaf.view) return
      leaf.view = { ...leaf.view, instanceKey: instanceKey || null }
      this.markDirty()
    },

    /** tab 容器的标签栏位置 */
    setAreaTabPosition(nodeId, position) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf || !isTabsContainer(leaf)) return
      leaf.tabPosition = ['top', 'bottom', 'left', 'right'].includes(position) ? position : 'top'
      this.markDirty()
    },

    /** tab 容器（固定 / 自由）的标签页 */
    setAreaTabs(nodeId, tabs) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf || !isTabsContainer(leaf)) return
      leaf.tabs = tabs
      this.markDirty()
    },

    addAreaTab(nodeId) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf || !isTabsContainer(leaf)) return
      leaf.tabs = [...(leaf.tabs ?? []), { viewKey: 'file-browser', instanceKey: null, label: '' }]
      this.markDirty()
    },

    updateAreaTab(nodeId, index, patch) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf?.tabs?.[index]) return
      leaf.tabs[index] = { ...leaf.tabs[index], ...patch }
      this.markDirty()
    },

    removeAreaTab(nodeId, index) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf?.tabs) return
      leaf.tabs.splice(index, 1)
      this.markDirty()
    },

    moveAreaTab(nodeId, index, delta) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf?.tabs) return
      const next = index + delta
      if (next < 0 || next >= leaf.tabs.length) return
      const [t] = leaf.tabs.splice(index, 1)
      leaf.tabs.splice(next, 0, t)
      this.markDirty()
    },

    /** 把第 index 个标签页移到首位（溢出弹框选中；与自由 tab 容器行为一致） */
    moveAreaTabToFront(nodeId, index) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf?.tabs || index <= 0 || index >= leaf.tabs.length) return
      const [t] = leaf.tabs.splice(index, 1)
      leaf.tabs.unshift(t)
      this.markDirty()
    },

    /**
     * 切换固定 tab 容器里标签页的固定态。
     * **固定后立刻排到最前**（横排 = 最左 / 竖排 = 最上）；解除固定后留在原位。
     */
    toggleAreaTabPin(nodeId, index) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf?.tabs?.[index]) return
      leaf.tabs[index] = { ...leaf.tabs[index], pinned: !leaf.tabs[index].pinned }
      leaf.tabs = sortPinnedFirst(leaf.tabs)
      this.markDirty()
    },

    /**
     * 批量关闭固定 tab 容器里的标签页：mode = 'this' | 'left' | 'right' | 'others' | 'all'。
     * 固定（pinned）的标签页不会被关闭。
     */
    closeAreaTabs(nodeId, index, mode) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf?.tabs) return
      // index < 0（key 解析不到）时：'right' 的 `i <= -1` 恒假会退化成「关掉所有未固定标签」，
      // 'left'/'this' 的过滤条件恒真则会「什么都不关」。两种都不是用户意图 → 直接返回。
      if (mode !== 'all' && index < 0) return
      const keep = (t) => t.pinned === true
      const list = leaf.tabs
      let next = list
      if (mode === 'all') next = list.filter(keep)
      else if (mode === 'this') next = list.filter((t, i) => i !== index || keep(t))
      else if (mode === 'others') next = list.filter((t, i) => i === index || keep(t))
      else if (mode === 'left') next = list.filter((t, i) => i >= index || keep(t))
      else if (mode === 'right') next = list.filter((t, i) => i <= index || keep(t))
      // 没有真的改到东西就不要置脏（否则「未保存」会误报，revert 还会丢掉真实改动）
      if (next.length === list.length && next.every((t, i) => t === list[i])) return
      leaf.tabs = next
      this.markDirty()
    },

    /* ---------------- 区域尺寸约束 / 同级交换 ---------------- */

    /**
     * 设置区域尺寸约束。
     * direction: 'row'（左右排布 → 宽）| 'column'（上下排布 → 高）
     * patch: { min, max }（像素；空 / 非法值 → 该约束不存在，即不限）
     *
     * 注意：Vue 3 里 `delete obj[key]` **不是响应式的** —— 直接删字段会让界面读到旧值，
     * 于是下一次输入会拿陈旧的值把前一次覆盖掉。
     * 所以这里显式重建 leaf 对象，只保留有效约束，并按父容器替换。
     */
    setLeafConstraint(nodeId, direction, patch) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf) return
      const isRow = direction === 'row'
      const minKey = isRow ? 'minWidth' : 'minHeight'
      const maxKey = isRow ? 'maxWidth' : 'maxHeight'
      const clean = (v) => {
        const n = Number(v)
        return v === null || v === undefined || v === '' || !Number.isFinite(n) || n <= 0
          ? null
          : Math.round(n)
      }

      const next = { ...leaf }
      delete next.minWidth
      delete next.maxWidth
      delete next.minHeight
      delete next.maxHeight
      const min = clean(patch.min !== undefined ? patch.min : leaf[minKey])
      const max = clean(patch.max !== undefined ? patch.max : leaf[maxKey])
      if (min !== null) next[minKey] = min
      if (max !== null) next[maxKey] = max

      const root = this.draft.layout
      if (root.id === nodeId) {
        this.draft.layout = next
      } else {
        const parent = this.parentOf(nodeId)
        if (parent) {
          const i = parent.children.findIndex((c) => c.id === nodeId)
          if (i >= 0) parent.children[i] = next
        }
      }
      this.markDirty()
    },

    /** 尺寸两态（UILAYOUT-2）：`flex = true` → 吃掉剩余空间；否则按占比 + min/max */
    setLeafFlex(nodeId, flex) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf) return false
      leaf.flex = flex ? 1 : undefined
      this.markDirty()
      return true
    },

    /** 与同级相邻兄弟交换位置（delta = -1 向前 / +1 向后） */
    swapSibling(nodeId, delta) {
      const parent = this.parentOf(nodeId)
      if (!parent) return false
      const idx = parent.children.findIndex((c) => c.id === nodeId)
      const target = idx + delta
      if (idx < 0 || target < 0 || target >= parent.children.length) return false
      const [node] = parent.children.splice(idx, 1)
      parent.children.splice(target, 0, node)
      this.markDirty()
      return true
    },

    /** 同级兄弟信息（用于「交换左右/上下」按钮的可用性） */
    siblingInfo(nodeId) {
      const parent = this.parentOf(nodeId)
      if (!parent) return null
      const idx = parent.children.findIndex((c) => c.id === nodeId)
      return {
        direction: parent.direction,
        index: idx,
        total: parent.children.length,
        canPrev: idx > 0,
        canNext: idx >= 0 && idx < parent.children.length - 1,
      }
    },
  }
}

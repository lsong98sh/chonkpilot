import { defineStore } from 'pinia'
import { allViews, getView } from '../config/views'
import { defaultConfig, createBar, createToolbar } from '../config/defaultConfig'
import { cloneTree, collectLeaves, findLeaf, findNode, isLeaf, isSplit, walk } from '../utils/layout'
import { regionKind, regionViews, TOOLBAR_POSITIONS } from '../utils/regions'
import {
  BAR_ZONES,
  STORAGE_KEY,
  barLabelOf,
  collectToolbarRefs,
  customBarKey,
  migrateBarKey,
  normalize,
  readStorage,
  resolveBarArray,
  resolveBarObject,
} from '../config/config-normalize'
import { createLayoutActions, hasVisibleLeaf } from './config-layout-actions'
import { createBarActions } from './config-bar-actions'
import { createDrawerActions } from './config-drawer-actions'

/**
 * 向后兼容再导出：这些纯函数以前定义在本文件里，
 * 外部（组件 / 诊断脚本 / 测试）一直从 `../stores/config` 引它们。
 * 新代码建议直接从 `../config/config-normalize` 引。
 */
export {
  BAR_ZONES,
  STORAGE_KEY,
  barLabelOf,
  collectToolbarRefs,
  customBarKey,
  migrateBarKey,
  normalize,
  readStorage,
  resolveBarArray,
  resolveBarObject,
}

export const useConfigStore = defineStore('config', {
  state: () => {
    const initial = defaultConfig()
    return {
      live: normalize(cloneTree(initial)),
      draft: normalize(cloneTree(initial)),
      savedJson: JSON.stringify(initial, null, 2),
      messages: [],
      dirty: false,
    }
  },
  getters: {
    leaves: (state) => collectLeaves(state.draft.layout),
    /** 自由 tab 容器区域 */
    freeTabsLeaves() {
      return this.leaves.filter((l) => regionKind(l) === 'free-tabs')
    },
    liveFreeTabsLeaves: (state) => collectLeaves(state.live.layout).filter((l) => regionKind(l) === 'free-tabs'),
    drawers: (state) => state.draft.drawers,
    liveDrawers: (state) => state.live.drawers,
    drawerById: (state) => (id) => state.draft.drawers.find((d) => d.id === id) ?? null,
    liveDrawerById: (state) => (id) => state.live.drawers.find((d) => d.id === id) ?? null,

    /** 具名自定义工具栏（草稿 / 生效） */
    toolbars: (state) => state.draft.toolbars,
    liveToolbars: (state) => state.live.toolbars,
    toolbarById: (state) => (id) => state.draft.toolbars.find((t) => t.id === id) ?? null,
    liveToolbarById: (state) => (id) => state.live.toolbars.find((t) => t.id === id) ?? null,
    /** 某个自定义工具栏被谁引用（区域 / 容器 / 抽屉三处都要算） */
    toolbarUsage: (state) => (id) => collectToolbarRefs(state.draft, id),

    /** 栏条目（barKey：topToolbar | statusbar | custom:<id>） */
    barItems: (state) => (barKey, zone) =>
      resolveBarArray(state.draft, barKey).filter((i) => (i.zone ?? 'left') === zone),
    barList: (state) => (barKey) => resolveBarArray(state.draft, barKey),
    barLabel: (state) => (barKey) => barLabelOf(state.draft, barKey),

    /**
     * 取「当前应当渲染」的栏对象。
     * 与 `liveXxx` 的区别：**背景色取草稿**，这样在编辑器里改颜色立刻看得到效果
     * （背景色是纯视觉属性，不会打乱布局，提前预览没有风险）。
     * 结构 / 条目仍然只在保存后生效 —— 与 `live` 其余部分保持一致。
     */
    renderBar: (state) => (barKey) => {
      const live = resolveBarObject(state.live, barKey)
      const draft = resolveBarObject(state.draft, barKey)
      return {
        ...live,
        background: draft.background ?? '',
        zoneBackground: draft.zoneBackground ?? { left: '', center: '', right: '' },
      }
    },

    /** 定义在某个工具栏上的背景（按工具栏 id，供抽屉 / 区域 / 容器用） */
    renderToolbarBackground: (state) => (toolbarId) => {
      const draft = state.draft.toolbars.find((t) => t.id === toolbarId)
      const live = state.live.toolbars.find((t) => t.id === toolbarId)
      const src = draft ?? live
      if (!src) return { background: '', zoneBackground: { left: '', center: '', right: '' } }
      return {
        background: src.background ?? '',
        zoneBackground: src.zoneBackground ?? { left: '', center: '', right: '' },
      }
    },

    /** 节点（区域 / 容器）背景色：取草稿，保证改一下就能立刻在首页看到 */
    liveNodeBackground: (state) => (nodeId) => {
      const walkFind = (n) => {
        if (!n) return ''
        if (n.id === nodeId) return n.background ?? ''
        for (const c of n.children ?? []) {
          const hit = walkFind(c)
          if (hit) return hit
        }
        return ''
      }
      return walkFind(state.draft.layout)
    },

    /**
     * 首页渲染用的布局：**结构取 live，背景色取草稿**。
     * 背景色是纯视觉属性，提前预览没有风险；其余（分割 / 删除 / 条目）仍然只在保存后生效。
     * 只克隆到需要的深度，且只在草稿背景色真的不同时才克隆。
     */
    liveLayoutWithDraftBackgrounds(state) {
      const bgById = new Map()
      const index = (n) => {
        if (!n) return
        bgById.set(n.id, n.background ?? '')
        ;(n.children ?? []).forEach(index)
      }
      index(state.draft.layout)

      const applyBg = (n) => {
        const bg = bgById.get(n.id) ?? ''
        const kids = (n.children ?? []).map(applyBg)
        if (bg === (n.background ?? '') && !kids.some((k, i) => k !== n.children[i])) return n
        const copy = { ...n, background: bg }
        if (n.children) copy.children = kids
        return copy
      }
      return applyBg(state.live.layout)
    },

    /**
     * 单例 view 的占用登记：viewKey -> [{ slot, label }]
     *   area:<leafId> / area:<leafId>#<tabIndex> / drawer:<id>#<i> / bar:<bar>:<itemId>[#<nodeId>]
     */
    singletonUsage(state) {
      const usage = new Map()
      const add = (viewKey, slot, label) => {
        const def = getView(viewKey)
        // 自由 tab 容器是「容器」，不占用单例名额
        if (!def || def.mode !== 'singleton') return
        if (!usage.has(viewKey)) usage.set(viewKey, [])
        usage.get(viewKey).push({ slot, label })
      }

      const scanTabs = (tabs, slotOf, where) => {
        ;(tabs ?? []).forEach((t, i) => add(t.viewKey, slotOf(i), `${where} · 标签页 ${i + 1}`))
      }

      collectLeaves(state.draft.layout).forEach((leaf) => {
        const kind = regionKind(leaf)
        const where = `区域「${leaf.title || kind}」`
        if (kind === 'single') {
          if (leaf.view) add(leaf.view.viewKey, `area:${leaf.id}`, where)
        } else if (kind === 'fixed-tabs') {
          scanTabs(leaf.tabs, (i) => `area:${leaf.id}#${i}`, where)
        }
      })

      state.draft.drawers.forEach((d) => scanTabs(d.tabs, (i) => `drawer:${d.id}#${i}`, `抽屉「${d.label}」`))

      const scanNodes = (nodes, bar, item, barLabel) => {
        ;(nodes ?? []).forEach((n) => {
          if (n.children?.length) {
            scanNodes(n.children, bar, item, barLabel)
            return
          }
          if (n.command?.type === 'open-dialog' || n.command?.type === 'open-tabs') {
            add(n.command.viewKey, `bar:${bar}:${item.id}#${n.id}`, `${barLabel}「${item.label} / ${n.label}」`)
          }
        })
      }
      const scanBar = (items, bar, barLabel) => {
        ;(items ?? []).forEach((item) => {
          if (item.kind === 'button') {
            if (item.command?.type === 'open-dialog' || item.command?.type === 'open-tabs') {
              add(item.command.viewKey, `bar:${bar}:${item.id}`, `${barLabel}「${item.label}」`)
            }
          } else {
            scanNodes(item.children, bar, item, barLabel)
          }
        })
      }
      scanBar(state.draft.topToolbar?.items, 'topToolbar', '顶部工具栏')
      scanBar(state.draft.statusbar?.items, 'statusbar', '状态栏')
      state.draft.toolbars.forEach((t) => {
        scanBar(t.items, customBarKey(t.id), `工具栏「${t.label}」`)
      })

      return usage
    },

    /**
     * 取某个「标签页宿主」的当前标签页数组。
     * 宿主只有两种：`{ ownerType: 'area', ownerId }` 与 `{ ownerType: 'drawer', ownerId }`。
     * 统一成一个入口，是为了让标签页的 4 个 mq 主题（add / update / remove / move）
     * 不必为区域与抽屉各写一套订阅。
     */
    tabsOf: (state) => (ownerType, ownerId) => {
      if (ownerType === 'drawer') {
        return state.draft.drawers.find((d) => d.id === ownerId)?.tabs ?? []
      }
      return findLeaf(state.draft.layout, ownerId)?.tabs ?? []
    },

    /** 可选 view 列表（单例被别处占用即禁用 + 提示） */
    viewOptions: (state) => (slotKey, excludeKeys = []) => {
      const usage = state.singletonUsage
      return allViews().map((v) => {
        if (excludeKeys.includes(v.key)) return { ...v, hidden: true, disabled: true, takenLabel: '' }
        const taken = v.mode === 'singleton' ? (usage.get(v.key) ?? []).filter((w) => w.slot !== slotKey) : []
        return {
          ...v,
          hidden: false,
          disabled: taken.length > 0,
          takenLabel: taken.length ? `（已被 ${taken[0].label} 使用）` : '',
        }
      })
    },

    usedByOthers: (state) => (viewKey, slotKey) => {
      const list = state.singletonUsage.get(viewKey) ?? []
      if (!slotKey) return list
      return list.filter((w) => w.slot !== slotKey)
    },
  },
  actions: {
    ...createLayoutActions(),

    ...createBarActions(),

    ...createDrawerActions(),

    /* ---------------- 校验与保存 ---------------- */

    validate() {
      const errors = []
      const drawerIds = new Set(this.draft.drawers.map((d) => d.id))
      const toolbarIds = new Set(this.draft.toolbars.map((t) => t.id))

      const checkView = (ref, where) => {
        const def = getView(ref.viewKey)
        if (!def) {
          errors.push(`${where} 引用了不存在的 view：${ref.viewKey}`)
          return null
        }
        if (def.mode === 'multi' && !ref.instanceKey) {
          errors.push(`${where} 的 ${def.label} 是多实例 view，必须选择配套 key。`)
        }
        return def
      }

      /** 区域挂载的自定义工具栏必须存在；尺寸约束必须 min ≤ max */
      const checkToolbarRef = (ref, where) => {
        if (ref?.toolbarId && !toolbarIds.has(ref.toolbarId)) {
          errors.push(`${where}挂载的自定义工具栏已被删除。`)
        }
      }

      // 容器（split）也允许挂自定义工具栏，也要校验悬空引用
      walk(this.draft.layout, (node) => {
        if (node.type !== 'split') return
        checkToolbarRef(node.toolbar, `容器「${node.id}」`)
      })

      collectLeaves(this.draft.layout).forEach((leaf) => {
        const kind = regionKind(leaf)
        const where = `区域「${leaf.title || kind}」`
        checkToolbarRef(leaf.toolbar, where)
        for (const [minKey, maxKey, axis] of [
          ['minWidth', 'maxWidth', '宽度'],
          ['minHeight', 'maxHeight', '高度'],
        ]) {
          const lo = Number(leaf[minKey])
          const hi = Number(leaf[maxKey])
          if (Number.isFinite(lo) && Number.isFinite(hi) && lo > hi) {
            errors.push(`${where}的${axis}约束最小值大于最大值。`)
          }
        }
        if (kind === 'single') {
          if (leaf.view) checkView(leaf.view, where)
          return
        }
        if (kind === 'fixed-tabs' && !(leaf.tabs ?? []).length) {
          errors.push(`${where} 是固定 tab 容器，至少需要一个标签页。`)
        }
        ;(leaf.tabs ?? []).forEach((t, i) => checkView(t, `${where} 第 ${i + 1} 个标签页`))
      })

      // 区域显隐（WIN-025 / UILAYOUT-2）：至少保留一个「有效可见」叶子
      // （自身可见且所有祖先可见；容器隐藏时其子树不计入）
      if (!hasVisibleLeaf(this.draft.layout)) {
        errors.push('至少需要保留一个可见区域（全部隐藏时无法保存）。')
      }

      this.draft.drawers.forEach((d) => {
        const where = `抽屉「${d.label}」`
        if (!['left', 'right', 'top', 'bottom'].includes(d.position)) errors.push(`${where} 的弹出位置不合法。`)
        if (!d.tabs?.length) errors.push(`${where} 至少需要一个内容 view。`)
        if (d.toolbarId && !toolbarIds.has(d.toolbarId)) errors.push(`${where}挂载的自定义工具栏已被删除。`)
        ;(d.tabs ?? []).forEach((t, i) => checkView(t, `${where} 第 ${i + 1} 个内容`))
      })

      const validateCommand = (cmd, where) => {
        if (!cmd || !cmd.type) {
          errors.push(`${where}未配置命令。`)
          return
        }
        if (cmd.type === 'close-tabs-active') {
          if (cmd.targetLeafId && !findLeaf(this.draft.layout, cmd.targetLeafId)) {
            errors.push(`${where}命令指向的自由 tab 容器已不存在。`)
          }
          return
        }
        if (cmd.type === 'open-drawer') {
          if (!cmd.drawerId) errors.push(`${where}命令未选择抽屉。`)
          else if (!drawerIds.has(cmd.drawerId)) errors.push(`${where}命令引用的抽屉已被删除。`)
          if (cmd.viewKey) checkView({ viewKey: cmd.viewKey, instanceKey: cmd.instanceKey }, `${where}命令`)
          return
        }
        // 〔UILAYOUT-1 裁决 1〕发布 MQ 动作：只要求填写**既有主题**；载荷不限形
        // （机制层不认识宿主主题，无法在此校验存在性 —— 由适配层配置侧保证「只用既有主题」）。
        if (cmd.type === 'publish-mq') {
          if (!cmd.topic) errors.push(`${where}的「发布 MQ 动作」未填写主题。`)
          return
        }
        const def = checkView(cmd, `${where}命令`)
        if (!def) return
        if (cmd.type === 'open-tabs') {
          if (cmd.targetLeafId) {
            const target = findLeaf(this.draft.layout, cmd.targetLeafId)
            if (!target) errors.push(`${where}命令的目标区域已不存在。`)
            else if (regionKind(target) !== 'free-tabs') {
              errors.push(`${where}命令只能把 view 打开到「自由 tab 容器」。`)
            }
          } else if (!this.freeTabsLeaves.length) {
            errors.push(`${where}要使用「打开到自由 tab 容器」，布局中至少需要一个自由 tab 容器区域。`)
          }
        }
      }

      const checkItems = (items, barLabel) => {
        items.forEach((item) => {
          const where = `${barLabel}「${item.label}」`
          if (item.kind === 'button') {
            validateCommand(item.command, where)
            return
          }
          // 「视图」条目内嵌一个 view：也要走 checkView（否则删掉被引用的 view、
          // 或给多实例 view 不填 key 都能保存成功 —— 和单容器区域的行为不一致）
          if (item.kind === 'view') {
            if (!item.viewKey) {
              errors.push(`${where}未选择 view。`)
              return
            }
            checkView({ viewKey: item.viewKey, instanceKey: item.instanceKey }, where)
            return
          }
          const walkMenu = (nodes, path) => {
            nodes.forEach((n) => {
              const here = `${path} / ${n.label}`
              // 子菜单（有 children）不需要命令
              if (n.children?.length) walkMenu(n.children, here)
              else validateCommand(n.command, `${barLabel}菜单${here}`)
            })
          }
          walkMenu(item.children ?? [], `${barLabel}「${item.label}」`)
        })
      }
      checkItems(this.draft.topToolbar?.items ?? [], '顶部工具栏')
      checkItems(this.draft.statusbar?.items ?? [], '状态栏')
      this.draft.toolbars.forEach((t) => checkItems(t.items, `工具栏「${t.label}」`))
      return errors
    },

    save() {
      const errors = this.validate()
      if (errors.length) {
        this.messages = errors
        return { ok: false, errors }
      }
      const snapshot = normalize(cloneTree(this.draft))
      this.live = snapshot
      this.savedJson = JSON.stringify(snapshot, null, 2)
      try {
        localStorage.setItem(STORAGE_KEY, this.savedJson)
      } catch {
        /* 忽略存储失败 */
      }
      this.dirty = false
      this.messages = []
      return { ok: true, errors: [] }
    },

    /** 只把草稿规范化为快照（不应用 / 不落盘）—— 布局档案保存用（WIN-026） */
    snapshotDraft() {
      return normalize(cloneTree(this.draft))
    },

    revert() {
      this.draft = cloneTree(this.live)
      this.dirty = false
      this.messages = []
    },

    resetAll() {
      this.draft = normalize(cloneTree(defaultConfig()))
      this.dirty = true
      this.messages = []
    },

    importJson(text) {
      try {
        const parsed = JSON.parse(text)
        if (!parsed.layout) return { ok: false, error: 'JSON 缺少 layout 字段' }
        this.draft = normalize(parsed)
        this.dirty = true
        return { ok: true }
      } catch (e) {
        return { ok: false, error: e.message }
      }
    },

    exportJson() {
      return JSON.stringify(this.draft, null, 2)
    },

    /** 区域摘要（树 / 设置区显示） */
    leafSummary(nodeId) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf) return '—'
      const kind = regionKind(leaf)
      if (kind === 'single') {
        if (!leaf.view) return '空白区域'
        const def = getView(leaf.view.viewKey)
        const base = def?.label ?? leaf.view.viewKey
        return leaf.view.instanceKey ? `${base} · ${leaf.view.instanceKey}` : base
      }
      const views = regionViews(leaf)
      const label = kind === 'fixed-tabs' ? '固定 tab' : '自由 tab'
      return `${label} · ${views.length} 项`
    },

    nodeKind(nodeId) {
      const node = findNode(this.draft.layout, nodeId)
      if (!node) return 'none'
      return isLeaf(node) ? 'area' : 'container'
    },

    /** 父容器（用于同级交换 / 约束方向判断） */
    parentOf(nodeId) {
      const walkFind = (node) => {
        if (!isSplit(node)) return null
        if (node.children.some((c) => c.id === nodeId)) return node
        for (const child of node.children) {
          const found = walkFind(child)
          if (found) return found
        }
        return null
      }
      return walkFind(this.draft.layout)
    },

    /** 区域所在父容器的排布方向（'row' | 'column' | null） */
    parentDirection(nodeId) {
      return this.parentOf(nodeId)?.direction ?? null
    },

    /* ---------------- 具名自定义工具栏 ---------------- */

    addToolbar(label) {
      const tb = createToolbar(label)
      this.draft.toolbars.push(tb)
      this.markDirty()
      return tb.id
    },

    removeToolbar(id) {
      const used = this.toolbarUsageRefs(id)
      if (used.length) {
        this.messages = [`该工具栏仍被 ${used.join('、')} 引用，请先改绑其它工具栏。`]
        return false
      }
      const idx = this.draft.toolbars.findIndex((t) => t.id === id)
      if (idx >= 0) this.draft.toolbars.splice(idx, 1)
      this.markDirty()
      return true
    },

    /** 引用统计（与 getter `toolbarUsage` 共用同一个纯函数，避免判断分叉） */
    toolbarUsageRefs(id) {
      return collectToolbarRefs(this.draft, id)
    },

    patchToolbar(id, patch) {
      const tb = this.draft.toolbars.find((t) => t.id === id)
      if (!tb) return
      Object.assign(tb, patch)
      this.markDirty()
    },

    moveToolbar(id, delta) {
      const idx = this.draft.toolbars.findIndex((t) => t.id === id)
      const next = idx + delta
      if (idx < 0 || next < 0 || next >= this.draft.toolbars.length) return
      const [t] = this.draft.toolbars.splice(idx, 1)
      this.draft.toolbars.splice(next, 0, t)
      this.markDirty()
    },

    /** 区域挂载 / 卸载自定义工具栏（toolbarId 为空 = 不挂载） */
    setAreaToolbar(nodeId, patch) {
      const leaf = findLeaf(this.draft.layout, nodeId)
      if (!leaf) return
      const current = leaf.toolbar ?? { toolbarId: null, position: 'top' }
      const next = { ...current, ...patch }
      if (!next.toolbarId) leaf.toolbar = null
      else {
        if (!TOOLBAR_POSITIONS.some((p) => p.value === next.position)) next.position = 'top'
        leaf.toolbar = next
      }
      this.markDirty()
    },

    /** 抽屉挂载自定义工具栏 */
    setDrawerToolbar(drawerId, patch) {
      const drawer = this.draft.drawers.find((d) => d.id === drawerId)
      if (!drawer) return
      if ('toolbarId' in patch) drawer.toolbarId = patch.toolbarId || null
      if ('toolbarPosition' in patch) drawer.toolbarPosition = patch.toolbarPosition || 'top'
      if (!TOOLBAR_POSITIONS.some((p) => p.value === drawer.toolbarPosition)) drawer.toolbarPosition = 'top'
      this.markDirty()
    },
  },
})

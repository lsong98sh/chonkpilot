import { defineStore } from 'pinia'
import { sortPinnedFirst } from '../utils/regions'

/**
 * 运行时状态：自由 tab 容器的标签页、抽屉、当前激活区域、提示。
 * 它是「保存后的 JSON」在渲染期的现场数据，不参与配置持久化。
 * 所有变更都由 mq 主题驱动（见 stores/mqCommands.js）。
 */
let tabSeq = 0

export const useRuntimeStore = defineStore('runtime', {
  state: () => ({
    /** 自由 tab 容器（区域 id）-> 已装入的标签页 */
    tabs: {},
    /** 区域 id -> 当前激活标签页 */
    activeTab: {},
    /** 固定 tab 容器（区域 id）-> 当前激活下标 */
    activeFixedTab: {},
    /** 当前激活（最后点击）的区域 id */
    activeLeafId: '',
    /** 抽屉列表（按打开顺序；同一抽屉只出现一次） */
    drawers: [],
    /** 状态栏左侧提示文字 */
    hint: '就绪',
  }),
  getters: {
    tabsOf: (state) => (leafId) => {
      if (!leafId) return []
      return state.tabs[leafId] ?? []
    },
    activeTabOf: (state) => (leafId) => {
      if (!leafId) return null
      const list = state.tabs[leafId] ?? []
      if (!list.length) return null
      const id = state.activeTab[leafId]
      return list.find((t) => t.tabId === id) ?? list[list.length - 1]
    },
  },
  actions: {
    setActiveLeaf(id) {
      this.activeLeafId = id
    },

    ensureTabs(leafId) {
      if (!this.tabs[leafId]) this.tabs[leafId] = []
    },

    /** 把一个 view 装入自由 tab 容器（已存在则激活） */
    openTab(leafId, ref, source = 'command') {
      this.ensureTabs(leafId)
      const list = this.tabs[leafId]
      const label = ref.label || ''
      const existing = list.find(
        (t) => t.viewKey === ref.viewKey && t.instanceKey === ref.instanceKey && (t.label ?? '') === label,
      )
      if (existing) {
        this.activeTab[leafId] = existing.tabId
        this.hint = `已激活标签页：${existing.label || existing.viewKey}`
        return existing.tabId
      }
      tabSeq += 1
      const tabId = `tab_${tabSeq}`
      // 固定的标签始终留在最前（新开的插在它们后面）
      list.push({ ...ref, tabId, source })
      this.tabs[leafId] = sortPinnedFirst(list)
      this.activeTab[leafId] = tabId
      this.hint = `已打开：${ref.label || ref.viewKey}`
      return tabId
    },

    closeTab(leafId, tabId) {
      const list = this.tabs[leafId]
      if (!list) return
      const idx = list.findIndex((t) => t.tabId === tabId)
      if (idx < 0) return
      list.splice(idx, 1)
      if (this.activeTab[leafId] === tabId) {
        const next = list[Math.min(idx, list.length - 1)]
        this.activeTab[leafId] = next ? next.tabId : ''
      }
    },

    closeActiveTab(leafId) {
      const tab = this.activeTabOf(leafId)
      if (tab) this.closeTab(leafId, tab.tabId)
    },

    activateTab(leafId, tabId) {
      this.activeTab[leafId] = tabId
    },

    /**
     * 固定 tab 容器切换 / 记录激活标签页。
     * 存的是**稳定 key**（`viewKey|instanceKey`），不是数组下标 ——
     * 下标会在编辑期增删标签时平移，导致「激活的内容莫名换掉」。
     * 兼容传数字下标的旧调用（`LfFixedTabsView` 会把它解析回稳定 key）。
     */
    setFixedTab(nodeId, keyOrIndex) {
      this.activeFixedTab[nodeId] = keyOrIndex
    },

    /** 把某个自由 tab 移到首位（溢出弹框选中；只改显示顺序，不改配置） */
    moveTabToFront(leafId, tabId) {
      const list = this.tabs[leafId]
      if (!list) return
      const idx = list.findIndex((t) => t.tabId === tabId)
      if (idx <= 0) return
      const [t] = list.splice(idx, 1)
      list.unshift(t)
    },

    /**
     * 切换自由 tab 的固定态。
     * **固定后立刻排到最前**（横排 = 最左 / 竖排 = 最上）；解除固定后留在原位，不再回排。
     */
    togglePinTab(leafId, tabId) {
      const list = this.tabs[leafId]
      const tab = (list ?? []).find((t) => t.tabId === tabId)
      if (!tab) return
      tab.pinned = !tab.pinned
      this.tabs[leafId] = sortPinnedFirst(list)
    },

    /**
     * 批量关闭自由 tab：mode = 'this' | 'left' | 'right' | 'others' | 'all'。
     * 固定（pinned）的标签页不会被关闭。
     */
    closeTabs(leafId, tabId, mode) {
      const list = this.tabs[leafId]
      if (!list) return
      const idx = list.findIndex((t) => t.tabId === tabId)
      // tabId 找不到时 idx = -1：'right' 的 `i <= -1` 恒假会变成「全关」，
      // 'left'/'this' 的过滤条件恒真则会「什么都不关」。两种都不是用户意图 → 直接返回。
      if (mode !== 'all' && idx < 0) return
      const keep = (t) => t.pinned === true
      let next = list
      if (mode === 'all') next = list.filter(keep)
      else if (mode === 'this') next = list.filter((t) => t.tabId !== tabId || keep(t))
      else if (mode === 'others') next = list.filter((t) => t.tabId === tabId || keep(t))
      else if (mode === 'left') next = list.filter((t, i) => i >= idx || keep(t))
      else if (mode === 'right') next = list.filter((t, i) => i <= idx || keep(t))
      if (next === list) return
      this.tabs[leafId] = next
      if (!next.some((t) => t.tabId === this.activeTab[leafId])) {
        this.activeTab[leafId] = next.length ? next[next.length - 1].tabId : ''
      }
    },

    /**
     * 清理已不存在的区域所持有的标签页（保存配置后调用）。
     * 用「重建对象」而不是 `delete`：嵌套对象的属性删除不一定触发依赖，
     * 保存后旧区域的标签页会残留在内存里（同 id 复用时看到上一份配置的标签页）。
     */
    pruneTabs(liveLeafIds) {
      const nextTabs = {}
      const nextActive = {}
      Object.keys(this.tabs).forEach((id) => {
        if (liveLeafIds.includes(id)) {
          nextTabs[id] = this.tabs[id]
          nextActive[id] = this.activeTab[id] ?? ''
        }
      })
      this.tabs = nextTabs
      this.activeTab = nextActive
      // 固定容器的激活下标也一并回收（之前完全没清理）
      Object.keys(this.activeFixedTab).forEach((id) => {
        if (!liveLeafIds.includes(id)) delete this.activeFixedTab[id]
      })
    },

    /* ---------------- 抽屉 ---------------- */

    openDrawer(d) {
      const existing = this.drawers.find((x) => x.drawerId === d.drawerId)
      if (existing) {
        existing.viewKey = d.viewKey ?? existing.viewKey
        existing.instanceKey = d.instanceKey ?? existing.instanceKey
        // 运行期抽屉（未在布局文档登记）允许命令自带标题/图标（插件动作按 view 标题打开抽屉）。
        existing.label = d.label ?? existing.label
        existing.icon = d.icon ?? existing.icon
        this.hint = `抽屉已激活：${d.drawerId}`
        return existing.drawerId
      }
      this.drawers.push({
        drawerId: d.drawerId,
        viewKey: d.viewKey ?? null,
        instanceKey: d.instanceKey ?? null,
        label: d.label ?? '',
        icon: d.icon ?? '',
      })
      this.hint = `抽屉已打开：${d.drawerId}`
      return d.drawerId
    },

    closeDrawer(drawerId) {
      const idx = this.drawers.findIndex((d) => d.drawerId === drawerId)
      if (idx >= 0) this.drawers.splice(idx, 1)
    },

    closeAllDrawers() {
      this.drawers = []
    },

    toggleDrawer(drawerId) {
      if (this.drawers.some((d) => d.drawerId === drawerId)) this.closeDrawer(drawerId)
      else this.openDrawer({ drawerId })
    },

    setHint(text) {
      this.hint = text
    },
  },
})

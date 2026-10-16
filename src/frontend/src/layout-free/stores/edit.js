import { defineStore } from 'pinia'

/** 内容设置编辑器的 UI 状态（不参与配置持久化） */
export const useEditStore = defineStore('edit', {
  state: () => ({
    open: false,
    /** 'layout' | 'topToolbar' | 'statusbar' | 'toolbars' | 'drawer' */
    tab: 'layout',
    /** 布局树 / 预览中选中的节点 */
    selectedNodeId: '',
    /** 树中折叠的容器（缺省展开） */
    collapsed: {},
    /** 每个栏各自记录选中的条目（key = barKey：topToolbar | statusbar | custom:<id>） */
    selectedBarItem: {},
    /** 菜单树中选中的条目 */
    selectedMenuNodeId: '',
    /** 抽屉页签中选中的抽屉 */
    selectedDrawerId: '',
    /** 自由 tabs 页签中选中的容器 */
    selectedFreeTabGroupId: '',
    /** 工具栏页签中选中的自定义工具栏 */
    selectedToolbarId: '',
    /** 右键菜单 */
    contextMenu: { visible: false, x: 0, y: 0, nodeId: '' },
    /** JSON 抽屉 */
    showJson: false,
    /** 下拉菜单：key -> 是否展开 */
    dropdowns: {},
    /** tab 溢出弹框：ownerId -> 是否展开（LfTabbedContainer 内部状态） */
    tabMore: {},
    /** tab 右键菜单 */
    tabMenu: { visible: false, x: 0, y: 0, scope: 'free', ownerId: '', key: '' },
  }),
  getters: {
    selectedBarItemId: (state) => state.selectedBarItem[state.tab === 'statusbar' ? 'statusbar' : 'topToolbar'] ?? '',
    isCollapsed: (state) => (nodeId) => state.collapsed[nodeId] === true,
    isTabMoreOpen: (state) => (ownerId) => state.tabMore[ownerId] === true,
  },
  actions: {
    openEditor(tab = 'layout') {
      this.open = true
      this.tab = tab
      this.contextMenu.visible = false
      this.dropdowns = {}
    },
    closeEditor() {
      this.open = false
      this.contextMenu.visible = false
      this.showJson = false
      this.dropdowns = {}
    },
    select(nodeId) {
      this.selectedNodeId = nodeId ?? ''
    },
    toggleCollapse(nodeId) {
      this.collapsed[nodeId] = !this.collapsed[nodeId]
    },
    collapseAll(nodeIds) {
      nodeIds.forEach((id) => {
        this.collapsed[id] = true
      })
    },
    expandAll() {
      this.collapsed = {}
    },
    selectBarItem(bar, id) {
      this.selectedBarItem[bar] = id
      this.selectedMenuNodeId = ''
    },
    selectMenuNode(nodeId) {
      this.selectedMenuNodeId = nodeId ?? ''
    },
    selectDrawer(id) {
      this.selectedDrawerId = id ?? ''
    },
    selectToolbar(id) {
      this.selectedToolbarId = id ?? ''
    },
    selectFreeTabGroup(id) {
      this.selectedFreeTabGroupId = id ?? ''
    },
    openContextMenu(nodeId, x, y) {
      this.contextMenu = { visible: true, x, y, nodeId }
    },
    closeContextMenu() {
      this.contextMenu.visible = false
    },
    /**
     * 下拉开合（同一时刻只保留「一条链」的下拉）。
     * keepNames：需要保持展开的祖先下拉（子菜单展开时传入父菜单 name 链，否则父菜单会被关掉）。
     * forceOpen：鼠标 hover 展开用 —— 明确要求「打开」而不是切换；
     *            已经打开时直接 return，省掉一次无意义的 state 写入与重渲染。
     */
    toggleDropdown(key, keepNames = [], forceOpen = false) {
      const already = this.dropdowns[key] === true
      if (forceOpen && already) return
      const next = forceOpen ? true : !already
      const keep = new Set([...keepNames, key])
      const out = {}
      keep.forEach((n) => {
        if (n && this.dropdowns[n]) out[n] = true
      })
      if (next) out[key] = true
      this.dropdowns = out
    },
    closeDropdowns() {
      this.dropdowns = {}
    },
    /* ---------------- tab 溢出弹框 / 右键菜单 ---------------- */

    toggleTabMore(ownerId) {
      const next = !this.tabMore[ownerId]
      this.tabMore = {}
      if (next) this.tabMore[ownerId] = true
      this.tabMenu.visible = false
    },
    /**
     * 收起 tab 溢出弹框。
     * 传 ownerId 只关自己那一个（组件卸载时用）；不传则全关（点选后收起用）。
     */
    closeTabMore(ownerId) {
      if (ownerId) {
        const next = { ...this.tabMore }
        delete next[ownerId]
        this.tabMore = next
        return
      }
      this.tabMore = {}
    },
    openTabMenu(scope, ownerId, key, x, y) {
      const MW = 168
      const MH = 190
      const nx = x + MW > window.innerWidth ? window.innerWidth - MW - 8 : x
      const ny = y + MH > window.innerHeight ? window.innerHeight - MH - 8 : y
      this.tabMenu = {
        visible: true,
        x: Math.max(4, nx),
        y: Math.max(4, ny),
        scope,
        ownerId,
        key,
      }
      this.tabMore = {}
    },
    closeTabMenu() {
      this.tabMenu.visible = false
    },
  },
})

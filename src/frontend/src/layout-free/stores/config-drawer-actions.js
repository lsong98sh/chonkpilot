import { defaultConfig } from '../config/defaultConfig'

/**
 * 具名抽屉相关的 store 动作：抽屉本身 + 抽屉里的内容 view。
 * 通过 `...createDrawerActions()` 展开进 config store —— `this` 仍指向 store。
 * ⚠️ 逐字搬运原有实现。
 */
export function createDrawerActions() {
  return {
    /* ---------------- 抽屉 ---------------- */

    addDrawer() {
      const n = this.draft.drawers.length + 1
      const drawer = {
        id: uid('drawer'),
        label: `抽屉 ${n}`,
        position: 'right',
        size: 380,
        modal: true,
        tabs: [{ viewKey: 'file-browser', instanceKey: null, label: '' }],
      }
      this.draft.drawers.push(drawer)
      this.markDirty()
      return drawer.id
    },

    removeDrawer(id) {
      const idx = this.draft.drawers.findIndex((d) => d.id === id)
      if (idx >= 0) this.draft.drawers.splice(idx, 1)
      this.markDirty()
    },

    patchDrawer(id, patch) {
      const drawer = this.draft.drawers.find((d) => d.id === id)
      if (!drawer) return
      Object.assign(drawer, patch)
      this.markDirty()
    },

    moveDrawer(id, delta) {
      const idx = this.draft.drawers.findIndex((d) => d.id === id)
      const next = idx + delta
      if (idx < 0 || next < 0 || next >= this.draft.drawers.length) return
      const [d] = this.draft.drawers.splice(idx, 1)
      this.draft.drawers.splice(next, 0, d)
      this.markDirty()
    },

    addDrawerTab(drawerId) {
      const drawer = this.draft.drawers.find((d) => d.id === drawerId)
      if (!drawer) return
      drawer.tabs = drawer.tabs ?? []
      drawer.tabs.push({ viewKey: 'file-browser', instanceKey: null, label: '' })
      this.markDirty()
    },

    /** 整体写入抽屉的内容 view 数组（标签页主题带 tabs 时走这里） */
    setDrawerTabs(drawerId, tabs) {
      const drawer = this.draft.drawers.find((d) => d.id === drawerId)
      if (!drawer) return
      drawer.tabs = Array.isArray(tabs) ? tabs : []
      this.markDirty()
    },

    updateDrawerTab(drawerId, index, patch) {
      const drawer = this.draft.drawers.find((d) => d.id === drawerId)
      if (!drawer?.tabs?.[index]) return
      drawer.tabs[index] = { ...drawer.tabs[index], ...patch }
      this.markDirty()
    },

    removeDrawerTab(drawerId, index) {
      const drawer = this.draft.drawers.find((d) => d.id === drawerId)
      if (!drawer?.tabs || index < 0 || index >= drawer.tabs.length) return
      drawer.tabs.splice(index, 1)
      this.markDirty()
    },

    moveDrawerTab(drawerId, index, delta) {
      const drawer = this.draft.drawers.find((d) => d.id === drawerId)
      if (!drawer?.tabs) return
      const next = index + delta
      if (next < 0 || next >= drawer.tabs.length) return
      const [t] = drawer.tabs.splice(index, 1)
      drawer.tabs.splice(next, 0, t)
      this.markDirty()
    },

  }
}


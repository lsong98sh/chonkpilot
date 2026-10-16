import { findNode } from '../utils/layout'
import { createBarItem, createMenuNode } from '../config/defaultConfig'
import { BAR_ZONES, migrateBarKey } from '../config/config-normalize'

/**
 * 栏（顶部工具栏 / 状态栏 / 自定义工具栏）相关的 store 动作：
 * 三种栏是同一套结构 `{ background, zoneBackground, items }`，所以读写路径共用。
 *
 * 通过 `...createBarActions()` 展开进 config store —— `this` 仍指向 store。
 * ⚠️ 逐字搬运原有实现，不是重写。
 */
export function createBarActions() {
  return {
    /* ---------------- 工具栏 / 状态栏条目 ---------------- */

    /** barKey：topToolbar | statusbar | custom:<toolbarId>（旧写法 toolbar 自动映射） */
    barObject(barKey) {
      const key = migrateBarKey(barKey)
      if (key === 'statusbar') return this.draft.statusbar
      if (key === 'topToolbar') return this.draft.topToolbar
      if (typeof key === 'string' && key.startsWith('custom:')) {
        return this.draft.toolbars.find((t) => t.id === key.slice(7)) ?? null
      }
      return null
    },

    /** 栏的条目数组 */
    barArray(barKey) {
      const obj = this.barObject(barKey)
      if (!obj) return []
      if (!Array.isArray(obj.items)) obj.items = []
      return obj.items
    },

    /** 设置整条栏的背景色（'' = 跟随主题） */
    setBarBackground(barKey, color) {
      const obj = this.barObject(barKey)
      if (!obj) return
      obj.background = typeof color === 'string' ? color : ''
      this.markDirty()
    },

    /** 设置栏里某个区的背景色（zone = left | center | right） */
    setZoneBackground(barKey, zone, color) {
      const obj = this.barObject(barKey)
      if (!obj || !BAR_ZONES.includes(zone)) return
      if (!obj.zoneBackground || typeof obj.zoneBackground !== 'object') {
        obj.zoneBackground = { left: '', center: '', right: '' }
      }
      obj.zoneBackground[zone] = typeof color === 'string' ? color : ''
      this.markDirty()
    },

    /** 设置节点（区域 / 容器）背景色 */
    setNodeBackground(nodeId, color) {
      const node = findNode(this.draft.layout, nodeId)
      if (!node) return
      node.background = typeof color === 'string' ? color : ''
      this.markDirty()
    },

    addBarItem(barKey, kind, zone = 'left') {
      const item = createBarItem(kind, zone)
      this.barArray(barKey).push(item)
      this.markDirty()
      return item.id
    },

    removeBarItem(bar, id) {
      const list = this.barArray(bar)
      const idx = list.findIndex((i) => i.id === id)
      if (idx >= 0) list.splice(idx, 1)
      this.markDirty()
    },

    moveBarItem(bar, id, delta) {
      const list = this.barArray(bar)
      const item = list.find((i) => i.id === id)
      if (!item) return
      const sameZone = list.filter((i) => i.zone === item.zone)
      const pos = sameZone.findIndex((i) => i.id === id)
      const target = sameZone[pos + delta]
      if (!target) return
      const a = list.indexOf(item)
      const b = list.indexOf(target)
      list.splice(a, 1)
      // 删除 a 之后，原本在 a 之后的元素下标都左移 1 —— 不补偿的话「下移」会跳两位
      list.splice(a < b ? b - 1 : b, 0, item)
      this.markDirty()
    },

    setBarItemZone(bar, id, zone) {
      const item = this.barArray(bar).find((i) => i.id === id)
      if (!item || !BAR_ZONES.includes(zone)) return
      item.zone = zone
      this.markDirty()
    },

    patchBarItem(bar, id, patch) {
      const item = this.barArray(bar).find((i) => i.id === id)
      if (!item) return
      Object.assign(item, patch)
      this.markDirty()
    },

    findMenuNode(itemId, nodeId, bar) {
      const item = this.barArray(bar).find((i) => i.id === itemId)
      if (!item) return null
      const search = (nodes) => {
        if (!nodes) return null
        for (const n of nodes) {
          if (n.id === nodeId) return n
          const found = search(n.children)
          if (found) return found
        }
        return null
      }
      return search(item.children)
    },

    addMenuNode(bar, itemId, parentId, sub = false) {
      const item = this.barArray(bar).find((i) => i.id === itemId)
      if (!item) return null
      const node = createMenuNode(sub)
      if (!parentId) {
        item.children = item.children ?? []
        item.children.push(node)
      } else {
        const parent = this.findMenuNode(itemId, parentId, bar)
        if (parent) {
          parent.children = parent.children ?? []
          parent.children.push(node)
        }
      }
      this.markDirty()
      return node.id
    },

    removeMenuNode(bar, itemId, nodeId) {
      const item = this.barArray(bar).find((i) => i.id === itemId)
      if (!item) return
      const remove = (nodes) => {
        if (!nodes) return false
        const idx = nodes.findIndex((n) => n.id === nodeId)
        if (idx >= 0) {
          nodes.splice(idx, 1)
          return true
        }
        return nodes.some((n) => remove(n.children))
      }
      if (remove(item.children)) this.markDirty()
    },

    moveMenuNode(bar, itemId, nodeId, delta) {
      const item = this.barArray(bar).find((i) => i.id === itemId)
      if (!item) return
      // 语义：返回值 = 「真的移动了」。找到节点但已经在边界（上/下移不动）也算没移动，
      // 否则点一下边界按钮就会把「未保存」点亮，随后 revert 会丢掉真实改动。
      const move = (nodes) => {
        if (!nodes) return false
        const idx = nodes.findIndex((n) => n.id === nodeId)
        if (idx >= 0) {
          const next = idx + delta
          if (next < 0 || next >= nodes.length) return false
          const [n] = nodes.splice(idx, 1)
          nodes.splice(next, 0, n)
          return true
        }
        return nodes.some((n) => move(n.children))
      }
      if (move(item.children)) this.markDirty()
    },

    patchMenuNode(bar, itemId, nodeId, patch) {
      const node = this.findMenuNode(itemId, nodeId, bar)
      if (!node) return
      Object.assign(node, patch)
      this.markDirty()
    },

    /** 子菜单 ⇄ 叶子条目（子菜单不需要命令） */
    setMenuNodeSub(bar, itemId, nodeId, sub) {
      const node = this.findMenuNode(itemId, nodeId, bar)
      if (!node) return
      const isSub = Array.isArray(node.children)
      // 已经是目标形态 → 不置脏（放到「有改动」判断之后）
      if (isSub === sub) return
      if (sub) {
        node.children = []
        node.command = null
      } else {
        node.children = undefined
      }
      this.markDirty()
    },
  }
}

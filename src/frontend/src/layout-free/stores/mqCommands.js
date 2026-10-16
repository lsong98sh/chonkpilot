import { h } from 'vue'
import { EventNames } from '../events/event-names'
import { useConfigStore } from '../stores/config'
import { useRuntimeStore } from '../stores/runtime'
import { useEditStore } from '../stores/edit'
import { useKeysStore } from '../stores/keys'
import { getDialog, getMq, saveLayoutDoc } from '../deps.js'
import LfDialogView from '../views/LfDialogView.vue'
import { viewLabel } from '../config/views'
import { regionKind } from '../utils/regions'
import { collectLeaves, findLeaf } from '../utils/layout'

/**
 * MQ 命令装配：**所有**交互（UI 行为、编辑动作、运行期命令）都在这里订阅。
 * 由 `engine.install(app)` 装配一次；组件模板只发布 mq 主题，不写 @click 逻辑。
 *
 * 两条依赖（mq / dialog）都经门面注入（`deps.js`），机制层不 import 宿主代码：
 *   - mq     → 宿主 mq（缺省 = 内置 standalone）；
 *   - dialog → 宿主既有全局对话框服务（**去重**：不再自建第二份 DialogManager）。
 */
const unsubs = []

/** 已打开的对话框句柄：机制层自编号 id -> { id, close() } */
const dialogHandles = new Map()
let dialogSeq = 0

/** 卸载全部订阅（engine.install 幂等 / 测试复用） */
export function uninstallMqCommands() {
  while (unsubs.length) {
    const off = unsubs.pop()
    if (typeof off === 'function') off()
  }
  dialogHandles.clear()
}

export function installMqCommands() {
  const mq = getMq()
  const config = useConfigStore()
  const runtime = useRuntimeStore()
  const edit = useEditStore()
  const keys = useKeysStore()
  const on = (topic, cb) => unsubs.push(mq.on(topic, cb))

  /** 自由 tab 容器区域：指定 → 当前激活区域（若也是自由 tab 容器）→ 第一个 */
  function resolveTabsTarget(targetLeafId) {
    if (targetLeafId && findLeaf(config.live.layout, targetLeafId)) return targetLeafId
    const leaves = collectLeaves(config.live.layout)
    const active = runtime.activeLeafId
    const activeLeaf = leaves.find((l) => l.id === active)
    if (activeLeaf && regionKind(activeLeaf) === 'free-tabs') return activeLeaf.id
    return leaves.find((l) => regionKind(l) === 'free-tabs')?.id ?? null
  }

  function openViewDialog(d) {
    if (!d?.viewKey) return null
    const title = d.title || viewLabel(d.viewKey)
    const dialog = getDialog()
    if (!dialog || typeof dialog.show !== 'function') {
      // 对话框服务由宿主注入（去重：复用既有 DialogManager）；未注入 → 明确提示，不静默失败
      runtime.setHint('对话框服务未注入（createLayoutEngine({ dialog })）')
      return null
    }
    // 宿主既有 dialog.show(componentOrVNode, options)：内容 view 以 VNode 传入（携带 props）
    const handle = dialog.show(
      h(LfDialogView, { viewKey: d.viewKey, instanceKey: d.instanceKey ?? null }),
      {
        title,
        modal: true,
        resizable: true,
        closable: true,
        draggable: true,
        width: 620,
        height: 'min(72vh, 560px)',
      },
    )
    // 机制层自编号（宿主服务不暴露 id）；close 包一层以回收句柄表
    const id = `lfdlg_${(dialogSeq += 1)}`
    dialogHandles.set(id, {
      id,
      close() {
        try {
          handle?.close?.()
        } finally {
          dialogHandles.delete(id)
        }
      },
    })
    // 〔UILAYOUT-1 裁决 4（2026-10-16）〕宿主 `DialogManager` 的关闭透出（`handle.onClose`）：
    // 用户经 DialogShell 关闭 / 程序关闭都会回调 → 回收机制层句柄表 + 广播 `lf-layout-close-dialog`
    // 作为**可观察通知**（订阅方对已移除句柄是 no-op → 幂等，不会二次关闭）。
    handle?.onClose?.(() => {
      if (!dialogHandles.has(id)) return
      dialogHandles.delete(id)
      mq.emit(EventNames.dialogClose, { id, source: 'host' })
    })
    runtime.setHint(`对话框已打开：${title}`)
    return id
  }

  /* ── 运行期：命令 ──────────────────────────────────── */

  on(EventNames.dialogOpen, (d) => openViewDialog(d))
  on(EventNames.dialogClose, (d) => dialogHandles.get(d.id)?.close())
  on(EventNames.dialogCloseTop, () => {
    const ids = Array.from(dialogHandles.keys())
    const last = ids[ids.length - 1]
    if (last) dialogHandles.get(last)?.close()
  })

  on(EventNames.tabsOpen, (d) => {
    const target = resolveTabsTarget(d.targetLeafId)
    if (!target) {
      runtime.setHint('布局中没有自由 tab 容器区域')
      return false
    }
    runtime.openTab(target, { viewKey: d.viewKey, instanceKey: d.instanceKey ?? null }, 'command')
    return true
  })

  on(EventNames.tabsCloseActive, (d) => {
    const target = resolveTabsTarget(d.targetLeafId)
    if (!target) {
      runtime.setHint('布局中没有自由 tab 容器区域')
      return false
    }
    runtime.closeActiveTab(target)
    return true
  })

  on(EventNames.drawerOpen, (d) => runtime.openDrawer(d))
  on(EventNames.drawerClose, (d) => runtime.closeDrawer(d.drawerId))
  on(EventNames.drawerToggle, (d) => runtime.toggleDrawer(d.drawerId))

  /* ── 运行期：标签页操作 ─────────────────────────────── */

  on(EventNames.tabsActivate, (d) => runtime.activateTab(d.leafId, d.tabId))
  on(EventNames.tabsClose, (d) => {
    // 两种调用形态：自由容器 { leafId, tabId }；溢出弹框关闭 { scope, ownerId, key }
    if (d.scope === 'fixed') {
      config.removeAreaTab(d.ownerId, fixedTabIndex(d.ownerId, d.key))
      return
    }
    if (d.scope === 'free') {
      runtime.closeTab(d.ownerId, d.key)
      return
    }
    runtime.closeTab(d.leafId, d.tabId)
  })
  // ⚠️ LfFixedTabsView 的 payload 用 `ownerId`（与标签右键菜单统一），不是 `nodeId`
  on(EventNames.fixedTabsActivate, (d) => runtime.setFixedTab(d.ownerId ?? d.nodeId, d.key ?? d.index))

  /* ── 运行期：标签页右键菜单 / 溢出弹框（自由 + 固定 tab 容器同一套语义）── */

  /** 固定容器的标签页 key = `${viewKey}|${instanceKey}`（稳定，不含下标） → 反解出下标 */
  function fixedTabIndex(nodeId, key) {
    const leaf = config.leaves.find((l) => l.id === nodeId)
    return (leaf?.tabs ?? []).findIndex((t) => `${t.viewKey}|${t.instanceKey ?? ''}` === key)
  }

  on(EventNames.tabsCloseThis, (d) => {
    if (d.scope === 'fixed') config.closeAreaTabs(d.ownerId, fixedTabIndex(d.ownerId, d.key), 'this')
    else runtime.closeTabs(d.ownerId, d.key, 'this')
  })
  on(EventNames.tabsCloseOthers, (d) => {
    if (d.scope === 'fixed') config.closeAreaTabs(d.ownerId, fixedTabIndex(d.ownerId, d.key), 'others')
    else runtime.closeTabs(d.ownerId, d.key, 'others')
  })
  on(EventNames.tabsCloseLeft, (d) => {
    if (d.scope === 'fixed') config.closeAreaTabs(d.ownerId, fixedTabIndex(d.ownerId, d.key), 'left')
    else runtime.closeTabs(d.ownerId, d.key, 'left')
  })
  on(EventNames.tabsCloseRight, (d) => {
    if (d.scope === 'fixed') config.closeAreaTabs(d.ownerId, fixedTabIndex(d.ownerId, d.key), 'right')
    else runtime.closeTabs(d.ownerId, d.key, 'right')
  })
  on(EventNames.tabsCloseAll, (d) => {
    if (d.scope === 'fixed') config.closeAreaTabs(d.ownerId, 0, 'all')
    else runtime.closeTabs(d.ownerId, '', 'all')
  })
  on(EventNames.tabsPin, (d) => {
    if (d.scope === 'fixed') config.toggleAreaTabPin(d.ownerId, fixedTabIndex(d.ownerId, d.key))
    else runtime.togglePinTab(d.ownerId, d.key)
  })

  /** 溢出弹框选中 → 移到首位（顺带激活它）。
   *  自由容器：真正重排运行时标签数组；固定容器：只切换激活（不动配置里的标签顺序）。 */
  on(EventNames.tabsMoveFront, (d) => {
    if (d.scope === 'fixed') {
      // 固定容器只切激活（不动配置里的标签顺序）。d.key 已经是稳定 key，直接存。
      runtime.setFixedTab(d.ownerId, d.key)
    } else {
      runtime.moveTabToFront(d.ownerId, d.key)
      runtime.activateTab(d.ownerId, d.key)
    }
  })

  on(EventNames.tabMenuOpen, (d) => {
    edit.openTabMenu(d.scope, d.ownerId, d.key, d.x ?? 0, d.y ?? 0)
  })
  on(EventNames.tabMenuClose, () => edit.closeTabMenu())

  /* ── 编辑器：开关 / 保存 ───────────────────────────── */

  on(EventNames.editorOpen, (d) => edit.openEditor(d?.tab ?? 'layout'))
  on(EventNames.editorClose, () => edit.closeEditor())
  on(EventNames.editorTabSelect, (d) => {
    edit.tab = d.tab
    edit.showJson = false
  })
  on(EventNames.editorJsonToggle, () => {
    edit.showJson = !edit.showJson
  })
  on(EventNames.editorSave, async () => {
    const res = config.save()
    if (res.ok) {
      runtime.pruneTabs(collectLeaves(config.live.layout).map((l) => l.id))
      // 落库（§3.9 / P3③）：`gui.ui.save { layout: { free } }` → prj 配置 `layout.free`。
      // 落库失败 → **保持编辑器打开**并提示（§6「编辑器保存时后端失败」：live 已生效但没入库）。
      const wrote = await saveLayoutDoc(config.live)
      if (wrote && wrote.ok === false) {
        runtime.setHint(`配置已应用，但未落库：${wrote.error || '未知原因'}`)
        return { ok: false, errors: [String(wrote.error || '未落库')] }
      }
      runtime.setHint('配置已保存并应用')
      // 保存成功后自动关闭内容设置编辑器
      edit.closeEditor()
    } else {
      // 校验拦下时**保持编辑器打开**，并把原因说清楚（否则用户只看到「点了没反应」）
      const n = res.errors.length
      const first = res.errors[0] ?? '未知原因'
      runtime.setHint(n > 1 ? `保存被拦截（${n} 项）：${first} 等` : `保存被拦截：${first}`)
    }
    return res
  })
  on(EventNames.editorRevert, () => {
    config.revert()
    runtime.setHint('已放弃草稿，回到已生效配置')
  })
  on(EventNames.editorReset, () => {
    config.resetAll()
    edit.select('')
    runtime.setHint('已恢复默认配置（尚未保存）')
  })
  on(EventNames.editorImportJson, (d) => {
    const res = config.importJson(d.text)
    runtime.setHint(res.ok ? '已从 JSON 导入到草稿，保存后生效' : `导入失败：${res.error}`)
    return res
  })

  /* ── 编辑器：布局结构 ─────────────────────────────── */

  on(EventNames.layoutSelectNode, (d) => edit.select(d.nodeId ?? ''))
  on(EventNames.layoutSplit, (d) => {
    config.split(d.nodeId, d.direction)
    edit.select(d.nodeId)
  })
  on(EventNames.layoutAddArea, (d) => config.addLeafToSplit(d.nodeId))
  on(EventNames.layoutRemoveArea, (d) => {
    config.removeLeaf(d.nodeId)
    edit.select('')
  })
  on(EventNames.layoutSetSizes, (d) => config.setSplitSizes(d.nodeId, d.sizes, d.target ?? 'draft'))
  on(EventNames.splitSetDirection, (d) => config.setSplitDirection(d.nodeId, d.direction))

  /* ── 编辑器：区域（三种类型）──────────────────────── */

  on(EventNames.areaSetTitle, (d) => config.setLeafTitle(d.nodeId, d.title))
  on(EventNames.areaSetKind, (d) => config.setRegionKind(d.nodeId, d.kind))
  on(EventNames.areaSetTabPosition, (d) => config.setAreaTabPosition(d.nodeId, d.position))
  on(EventNames.areaSetView, (d) => config.setAreaView(d.nodeId, d.viewKey))
  on(EventNames.areaSetInstanceKey, (d) => config.setAreaInstanceKey(d.nodeId, d.instanceKey))
  on(EventNames.areaSetTabs, (d) => config.setAreaTabs(d.nodeId, d.tabs))
  on(EventNames.areaSetConstraint, (d) => config.setLeafConstraint(d.nodeId, d.direction, { min: d.min, max: d.max }))
  on(EventNames.areaSwap, (d) => config.swapSibling(d.nodeId, d.delta))
  on(EventNames.areaSetToolbar, (d) =>
    config.setAreaToolbar(d.nodeId, { toolbarId: d.toolbarId ?? null, position: d.position }),
  )
  on(EventNames.drawerSetToolbar, (d) =>
    config.setDrawerToolbar(d.drawerId, { toolbarId: d.toolbarId, toolbarPosition: d.toolbarPosition }),
  )
  /**
   * 标签页的增删改移：**区域**与**抽屉**只有宿主不同，语义完全一致。
   * 统一按 `ownerType` 分发，避免两套主题各写一遍。
   * 兼容 `{ nodeId }` / `{ drawerId }` 旧写法。
   * payload 带 `tabs`（完整结果）时按整体写入；只带 index / delta 时走粒度动作。
   */
  const ownerOf = (d) => (d.ownerType === 'drawer' ? 'drawer' : 'area')
  const ownerIdOf = (d) => d.ownerId ?? d.nodeId ?? d.drawerId ?? ''
  const writeTabs = (d, tabs) => {
    if (ownerOf(d) === 'drawer') config.setDrawerTabs(ownerIdOf(d), tabs)
    else config.setAreaTabs(ownerIdOf(d), tabs)
  }

  on(EventNames.tabsAdd, (d) => {
    if (Array.isArray(d.tabs)) writeTabs(d, d.tabs)
    else if (ownerOf(d) === 'drawer') config.addDrawerTab(ownerIdOf(d))
    else config.addAreaTab(ownerIdOf(d))
  })
  on(EventNames.tabsRemove, (d) => {
    if (Array.isArray(d.tabs)) writeTabs(d, d.tabs)
    else if (ownerOf(d) === 'drawer') config.removeDrawerTab(ownerIdOf(d), d.index)
    else config.removeAreaTab(ownerIdOf(d), d.index)
  })
  on(EventNames.tabsMove, (d) => {
    if (Array.isArray(d.tabs)) writeTabs(d, d.tabs)
    else if (ownerOf(d) === 'drawer') config.moveDrawerTab(ownerIdOf(d), d.index, d.delta)
    else config.moveAreaTab(ownerIdOf(d), d.index, d.delta)
  })
  on(EventNames.tabsUpdate, (d) => {
    if (ownerOf(d) === 'drawer') {
      config.updateDrawerTab(ownerIdOf(d), d.index, d.patch)
      // 反向派发历史别名，让「抽屉标签页」也有可观察的发布方
      mq.emit(EventNames.drawerTabUpdate, d)
    } else {
      config.updateAreaTab(ownerIdOf(d), d.index, d.patch)
    }
  })

  /* ── 编辑器：栏条目 / 菜单树 ──────────────────────── */

  on(EventNames.barItemAdd, (d) => {
    const id = config.addBarItem(d.bar, d.kind, d.zone ?? 'left')
    edit.selectBarItem(d.bar, id)
  })
  on(EventNames.barItemRemove, (d) => {
    const wasSelected = edit.selectedBarItem[d.bar] === d.id
    config.removeBarItem(d.bar, d.id)
    // 原为 `watch(items)` 的「删除后兜底选中」—— 改为订阅端一处收敛（§4.2 禁 watch）
    if (wasSelected) edit.selectBarItem(d.bar, config.barList(d.bar)[0]?.id ?? '')
  })
  on(EventNames.barItemMove, (d) => config.moveBarItem(d.bar, d.id, d.delta))
  on(EventNames.barItemZone, (d) => config.setBarItemZone(d.bar, d.id, d.zone))
  on(EventNames.barItemSelect, (d) => edit.selectBarItem(d.bar, d.id))
  on(EventNames.barSetBackground, (d) => config.setBarBackground(d.bar, d.color))
  on(EventNames.zoneSetBackground, (d) => config.setZoneBackground(d.bar, d.zone, d.color))
  on(EventNames.nodeSetBackground, (d) => config.setNodeBackground(d.nodeId, d.color))
  // nodeSetToolbar 与 areaSetToolbar 是同一件事（都写 node.toolbar），保留 areaSetToolbar 一条即可
  on(EventNames.menuNodeAdd, (d) => {
    const id = config.addMenuNode(d.bar, d.itemId, d.parentId, d.sub)
    if (id) edit.selectMenuNode(id)
  })
  on(EventNames.menuNodeRemove, (d) => config.removeMenuNode(d.bar, d.itemId, d.nodeId))
  on(EventNames.menuNodeMove, (d) => config.moveMenuNode(d.bar, d.itemId, d.nodeId, d.delta))
  on(EventNames.menuNodeSelect, (d) => edit.selectMenuNode(d.nodeId ?? ''))
  on(EventNames.menuNodeSub, (d) => config.setMenuNodeSub(d.bar, d.itemId, d.nodeId, d.sub))

  /* ── 编辑器：抽屉 ─────────────────────────────────── */

  on(EventNames.drawerAdd, () => {
    const id = config.addDrawer()
    edit.selectDrawer(id)
  })
  on(EventNames.drawerRemove, (d) => {
    const wasSelected = edit.selectedDrawerId === d.id
    config.removeDrawer(d.id)
    // 原为 `watch(drawers)` 的兜底选中（immediate）—— 改为订阅端收敛 + 组件 onMounted 兜底
    if (wasSelected) edit.selectDrawer(config.drawers[0]?.id ?? '')
  })
  on(EventNames.drawerMove, (d) => config.moveDrawer(d.id, d.delta))
  on(EventNames.drawerSelect, (d) => edit.selectDrawer(d.id ?? ''))
  // 抽屉标签页：与区域标签页是同一套语义，只是宿主不同 → 转发成通用主题，
  // 由上面那批 handler 统一分发（两条入口行为保证一致）。
  const forwardDrawerTab = (topic, payload) =>
    mq.emit(topic, { ...payload, ownerType: 'drawer', ownerId: payload.drawerId })
  on(EventNames.drawerTabAdd, (d) => forwardDrawerTab(EventNames.tabsAdd, d))
  on(EventNames.drawerTabRemove, (d) => forwardDrawerTab(EventNames.tabsRemove, d))
  on(EventNames.drawerTabMove, (d) => forwardDrawerTab(EventNames.tabsMove, d))
  // drawerTabUpdate 由 tabsUpdate 的抽屉分支反向派发，这里不再转发（避免重复写入）

  /* ── 编辑器：具名自定义工具栏 ─────────────────────── */

  on(EventNames.toolbarAdd, (d) => {
    const id = config.addToolbar(d.label)
    edit.selectToolbar(id)
    edit.selectBarItem(`custom:${id}`, '')
  })
  on(EventNames.toolbarMove, (d) => config.moveToolbar(d.id, d.delta))
  on(EventNames.toolbarSelect, (d) => {
    edit.selectToolbar(d.id ?? '')
    if (d.id) edit.selectBarItem(`custom:${d.id}`, '')
  })
  on(EventNames.toolbarPatch, (d) => config.patchToolbar(d.id, d.patch))
  on(EventNames.toolbarRemove, (d) => {
    const ok = config.removeToolbar(d.id)
    if (ok && edit.selectedToolbarId === d.id) {
      const next = config.toolbars[0]?.id ?? ''
      edit.selectToolbar(next)
      if (next) edit.selectBarItem(`custom:${next}`, '')
    }
  })

  /* ── 纯 UI 行为 ──────────────────────────────────── */

  on(EventNames.keysSetValue, (d) => {
    if (d?.key) keys.setValue(d.key, d.value ?? '')
  })
  on(EventNames.dropdownToggle, (d) => {
    if (!d || !d.key) return
    // hover 展开会带上 open: true；click 切换不带（走 toggle 语义）
    edit.toggleDropdown(d.key, d.keep ?? [], d.open === true)
  })
  on(EventNames.dropdownCloseAll, () => edit.closeDropdowns())
  on(EventNames.contextMenuSelect, (d) => {
    const nodeId = d.nodeId || edit.contextMenu.nodeId
    edit.closeContextMenu()
    switch (d.action) {
      case 'split-row':
        config.split(nodeId, 'row')
        break
      case 'split-column':
        config.split(nodeId, 'column')
        break
      case 'add-area':
        config.addLeafToSplit(nodeId)
        break
      case 'remove-area':
        config.removeLeaf(nodeId)
        break
      case 'swap-prev':
        config.swapSibling(nodeId, -1)
        break
      case 'swap-next':
        config.swapSibling(nodeId, 1)
        break
      case 'open-settings':
        edit.openEditor('layout')
        edit.select(nodeId)
        break
      default:
        break
    }
  })
  on(EventNames.contextMenuClose, () => edit.closeContextMenu())
}

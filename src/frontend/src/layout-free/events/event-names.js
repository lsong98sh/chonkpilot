/**
 * 事件通道名唯一事实源。
 * 约定：发布方与订阅方必须使用相同字面量值；新增事件时两处同步添加。
 *
 * 命名域：
 *   layout-* / area-* / tabs-* / bar-* / menu-* / drawer-* = 布局与内容设置编辑动作
 *   layout-open-*、tabs-activate …                        = 运行期命令与标签页操作
 *   editor-*                                              = 内容设置编辑器
 *   ui-*                                                  = 纯 UI 行为（下拉 / 右键菜单 / 变量）
 */
export const EventNames = {
  /* ── 编辑器开关 ── */
  editorOpen: 'lf-editor-open', // { tab }
  editorClose: 'lf-editor-close',
  editorTabSelect: 'lf-editor-tab-select', // { tab }
  editorSave: 'lf-editor-save',
  editorRevert: 'lf-editor-revert',
  editorReset: 'lf-editor-reset',
  editorJsonToggle: 'lf-editor-json-toggle',
  editorImportJson: 'lf-editor-import-json', // { text }

  /* ── 布局结构 ── */
  layoutSelectNode: 'lf-layout-select-node', // { nodeId }
  layoutSplit: 'lf-layout-split', // { nodeId, direction }
  layoutAddArea: 'lf-layout-add-area', // { nodeId }
  layoutRemoveArea: 'lf-layout-remove-area', // { nodeId }
  layoutSetSizes: 'lf-layout-set-sizes', // { nodeId, sizes, target? }
  splitSetDirection: 'lf-split-set-direction', // { nodeId, direction: 'row' | 'column' }

  /* ── 区域（三种类型：single / fixed-tabs / free-tabs）── */
  areaSetTitle: 'lf-area-set-title', // { nodeId, title }
  areaSetKind: 'lf-area-set-kind', // { nodeId, kind }
  areaSetTabPosition: 'lf-area-set-tab-position', // { nodeId, position } top|bottom|left|right
  areaSetView: 'lf-area-set-view', // { nodeId, viewKey } 仅 single
  areaSetInstanceKey: 'lf-area-set-instance-key', // { nodeId, instanceKey }
  areaSetTabs: 'lf-area-set-tabs', // { nodeId, tabs } tab 容器的整体替换
  areaSetConstraint: 'lf-area-set-constraint', // { nodeId, direction, min, max } 尺寸约束
  areaSetFlex: 'lf-area-set-flex', // { nodeId, flex } 数值 / 弹性两态（UILAYOUT-2）
  areaSetVisible: 'lf-area-set-visible', // { nodeId, visible } 设计态：区域显示 / 隐藏（写草稿）
  areaSetHideLocked: 'lf-area-set-hide-locked', // { nodeId, locked } 「不允许隐藏」标记（叶子 / 容器）
  areaToggleVisible: 'lf-area-toggle-visible', // { viewKey?, nodeId? } 运行态：按 viewKey 或 nodeId 切换并落盘
  /* ── 布局档案（UILAYOUT-2 / WIN-026；适配层订阅）── */
  layoutProfileNew: 'lf-layout-profile-new', // {} 打开「新建布局」对话框
  layoutProfileCreate: 'lf-layout-profile-create', // { name, from } from = 档案 id | 'blank'
  layoutProfileSwitch: 'lf-layout-profile-switch', // { id }
  layoutEdit: 'lf-layout-edit', // { id? } 编辑当前（缺省）或新建后的档案
  layoutProfileRename: 'lf-layout-profile-rename', // { id, name }
  layoutProfileDelete: 'lf-layout-profile-delete', // { id }
  layoutProfileRestore: 'lf-layout-profile-restore', // { id? } 缺省 = 当前活动档案
  areaSwap: 'lf-area-swap', // { nodeId, delta } 与同级相邻兄弟交换位置
  areaSetToolbar: 'lf-area-set-toolbar', // { nodeId, toolbarId, position } 区域自定义工具栏
  drawerSetToolbar: 'lf-drawer-set-toolbar', // { drawerId, toolbarId, toolbarPosition }
  tabsAdd: 'lf-area-tabs-add', // { nodeId }
  tabsRemove: 'lf-area-tabs-remove', // { nodeId, index }
  tabsMove: 'lf-area-tabs-move', // { nodeId, index, delta }
  tabsUpdate: 'lf-area-tabs-update', // { nodeId, index, patch }

  /* ── 工具栏 / 状态栏条目 ── */
  barItemAdd: 'lf-bar-item-add', // { bar, kind, zone }
  barItemRemove: 'lf-bar-item-remove', // { bar, id }
  barItemMove: 'lf-bar-item-move', // { bar, id, delta }
  barItemZone: 'lf-bar-item-zone', // { bar, id, zone }
  barItemSelect: 'lf-bar-item-select', // { bar, id }
  barSetBackground: 'lf-bar-set-background', // { bar, color } 整条栏背景色（'' = 跟随主题）
  zoneSetBackground: 'lf-zone-set-background', // { bar, zone, color } 栏里某个区的背景色
  nodeSetBackground: 'lf-node-set-background', // { nodeId, color } 区域 / 容器的背景色
  menuNodeAdd: 'lf-menu-node-add', // { bar, itemId, parentId, sub }
  menuNodeRemove: 'lf-menu-node-remove', // { bar, itemId, nodeId }
  menuNodeMove: 'lf-menu-node-move', // { bar, itemId, nodeId, delta }
  menuNodeSelect: 'lf-menu-node-select', // { nodeId }
  menuNodeSub: 'lf-menu-node-sub', // { bar, itemId, nodeId, sub }

  /* ── 抽屉配置 ── */
  drawerAdd: 'lf-drawer-add',
  drawerRemove: 'lf-drawer-remove', // { id }
  drawerMove: 'lf-drawer-move', // { id, delta }
  drawerSelect: 'lf-drawer-select', // { id }
  drawerTabAdd: 'lf-drawer-tab-add', // { drawerId }
  drawerTabRemove: 'lf-drawer-tab-remove', // { drawerId, index }
  drawerTabMove: 'lf-drawer-tab-move', // { drawerId, index, delta }
  drawerTabUpdate: 'lf-drawer-tab-update', // { drawerId, index, patch }

  /* ── 具名自定义工具栏（供抽屉 / 区域挂载）── */
  toolbarAdd: 'lf-toolbar-add', // { label }
  toolbarRemove: 'lf-toolbar-remove', // { id }
  toolbarMove: 'lf-toolbar-move', // { id, delta }
  toolbarSelect: 'lf-toolbar-select', // { id }
  toolbarPatch: 'lf-toolbar-patch', // { id, patch }

  /* ── 运行期命令（工具栏 / 状态栏条目触发）── */
  dialogOpen: 'lf-layout-open-dialog', // { viewKey, instanceKey, title }
  dialogClose: 'lf-layout-close-dialog', // { id }
  dialogCloseTop: 'lf-layout-close-top-dialog',
  tabsOpen: 'lf-layout-open-tabs', // { targetLeafId, viewKey, instanceKey } 打开到自由 tab 容器
  tabsCloseActive: 'lf-layout-close-tabs-active', // { targetLeafId }
  drawerOpen: 'lf-layout-open-drawer', // { drawerId, viewKey, instanceKey, label?, icon? }（label/icon 仅运行期未登记抽屉用）
  drawerClose: 'lf-layout-close-drawer', // { drawerId }
  drawerToggle: 'lf-layout-toggle-drawer', // { drawerId }

  /* ── 运行期标签页操作 ── */
  tabsActivate: 'lf-tabs-activate', // { leafId, tabId }
  tabsClose: 'lf-tabs-close', // { leafId, tabId }
  fixedTabsActivate: 'lf-fixed-tabs-activate', // { nodeId, index }
  tabsPin: 'lf-tabs-pin', // { scope: 'free'|'fixed', ownerId, key } 切换固定
  tabsCloseThis: 'lf-tabs-close-this', // { scope, ownerId, key }
  tabsCloseLeft: 'lf-tabs-close-left', // { scope, ownerId, key }
  tabsCloseRight: 'lf-tabs-close-right', // { scope, ownerId, key }
  tabsCloseOthers: 'lf-tabs-close-others', // { scope, ownerId, key }
  tabsCloseAll: 'lf-tabs-close-all', // { scope, ownerId, key }
  tabsMoveFront: 'lf-tabs-move-front', // { scope, ownerId, key } 溢出弹框选中 → 移到首位
  tabMenuOpen: 'lf-ui-tab-menu-open', // { scope, ownerId, key } + 坐标在 edit store
  tabMenuClose: 'lf-ui-tab-menu-close',

  /* ── 纯 UI 行为 ── */
  keysSetValue: 'lf-keys-set-value', // { key, value }
  dropdownToggle: 'lf-ui-dropdown-toggle', // { key, keep?: string[] } keep = 需要保持展开的祖先下拉
  dropdownCloseAll: 'lf-ui-dropdown-close-all',
  containerClick: 'lf-ui-container-click', // 弹层内部点击：仅停止冒泡
  contextMenuSelect: 'lf-ui-context-menu-select', // { action, nodeId }
  contextMenuClose: 'lf-ui-context-menu-close',
}

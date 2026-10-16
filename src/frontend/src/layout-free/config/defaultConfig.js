import { createLeaf, createSplit, uid } from '../utils/layout'

/**
 * 栏（顶部工具栏 / 状态栏 / 自定义工具栏）的配置结构：
 *   { background: '', zoneBackground: { left: '', center: '', right: '' }, items: [] }
 * 注意：**不是数组**。顶部工具栏 / 状态栏与自定义工具栏结构一致，
 * 这样「背景色 / 每区背景色 / 条目」三件事在三种栏上是同一套读写路径。
 */
export function createBar(background = '') {
  return { background, zoneBackground: { left: '', center: '', right: '' }, items: [] }
}

/** 默认布局：左侧（文件 + 自由 tab 容器），右侧（主对话） */
export function defaultLayout() {
  const leftTop = createLeaf(null, '文件')
  leftTop.id = 'leaf_file'
  leftTop.kind = 'single'
  leftTop.view = { viewKey: 'file-browser', instanceKey: null }
  leftTop.minWidth = 160
  leftTop.maxWidth = 640

  const leftBottom = createLeaf(null, '工作区')
  leftBottom.id = 'leaf_tabs'
  leftBottom.kind = 'free-tabs'
  leftBottom.tabs = []
  leftBottom.minHeight = 140
  leftBottom.maxHeight = 1600

  const left = createSplit('column', [leftTop, leftBottom])
  left.id = 'split_left'

  const right = createLeaf(null, '主对话')
  right.id = 'leaf_chat'
  right.kind = 'single'
  right.view = { viewKey: 'chat', instanceKey: null }
  right.minWidth = 240

  return createSplit('row', [left, right])
}

/**
 * 栏条目工厂。三类条目（kind）：
 *   'button' 按钮：图标 + 文本，点击执行命令
 *   'menu'   菜单：树形条目，叶子执行命令（子菜单不需要命令）
 *   'view'   视图：栏里内嵌一个 view（如搜索框），需要定义宽 / 高
 */
export function createBarItem(kind, zone = 'left') {
  const base = {
    id: uid(kind === 'button' ? 'btn' : kind === 'menu' ? 'menu' : 'view'),
    kind,
    zone,
    visible: true,
    tooltip: '',
  }
  if (kind === 'menu') {
    return { ...base, label: '新菜单', icon: 'menu', style: 'text', command: null, children: [] }
  }
  if (kind === 'view') {
    return {
      ...base,
      label: '搜索框',
      viewKey: 'search',
      /* search 是多实例 view → 需要一个配套 key */
      instanceKey: 'searchBox',
      width: 240,
      height: 26,
      /* 是否撑满所在区（默认按宽高定尺） */
      grow: false,
    }
  }
  return {
    ...base,
    label: '新按钮',
    icon: 'square',
    style: 'icon-text',
    command: null,
    children: undefined,
  }
}

/** 菜单条目工厂（sub=true 为子菜单，子菜单不需要命令） */
export function createMenuNode(sub = false) {
  return {
    id: uid(sub ? 'sub' : 'mi'),
    label: sub ? '新子菜单' : '新条目',
    icon: sub ? 'folder-open' : 'dot',
    command: null,
    children: sub ? [] : undefined,
  }
}

/**
 * 具名工具栏工厂。
 * 用途：① 抽屉某条边的自定义工具栏；② 容器 / 区域某条边的自定义工具栏。
 * 自身结构与「顶部工具栏 / 状态栏」一致（靠左区 / 中间区 / 靠右区 + 条目 + 背景色）。
 */
export function createToolbar(label) {
  return { id: uid('toolbar'), label: label || '自定义工具栏', ...createBar() }
}

export function defaultToolbars() {
  const tb = createToolbar('文件工具')
  tb.id = 'tb_detail'
  tb.items = [
    {
      id: 'tb_detail_refresh',
      kind: 'button',
      zone: 'left',
      label: '刷新',
      icon: 'refresh-cw',
      style: 'icon',
      tooltip: '刷新当前内容',
      visible: true,
      command: { type: 'open-tabs', targetLeafId: null, viewKey: 'task-tree', instanceKey: null },
    },
    {
      id: 'tb_detail_settings',
      kind: 'button',
      zone: 'right',
      label: '项目配置',
      icon: 'settings',
      style: 'icon',
      tooltip: '打开项目配置页',
      visible: true,
      command: { type: 'open-dialog', viewKey: 'project-config', instanceKey: null },
    },
  ]
  return [tb]
}

export function defaultTopToolbar() {
  const bar = createBar()
  bar.items = [
    {
      id: 'tb_more',
      kind: 'menu',
      zone: 'right',
      label: '更多',
      icon: 'ellipsis',
      style: 'icon',
      tooltip: '更多工具',
      visible: true,
      children: [
        {
          id: 'tb_m_mcp',
          label: 'MCP 配置页',
          icon: 'plug',
          command: { type: 'open-dialog', viewKey: 'mcp-config', instanceKey: null },
        },
        {
          id: 'tb_m_proj',
          label: '项目配置页',
          icon: 'settings',
          command: { type: 'open-dialog', viewKey: 'project-config', instanceKey: null },
        },
        {
          // 子菜单：不需要命令，只承载下级条目
          id: 'tb_m_more',
          label: '更多配置',
          icon: 'folder-open',
          children: [
            {
              id: 'tb_m_more_llm',
              label: 'LLM 配置页',
              icon: 'bot',
              command: { type: 'open-dialog', viewKey: 'llm-config', instanceKey: null },
            },
            {
              id: 'tb_m_more_drawer',
              label: '文件浏览（抽屉）',
              icon: 'folder-open',
              command: { type: 'open-drawer', drawerId: 'drawer_files' },
            },
          ],
        },
        {
          id: 'tb_m_tasks',
          label: '任务树 → 自由 tab 容器',
          icon: 'list-tree',
          command: { type: 'open-tabs', targetLeafId: null, viewKey: 'task-tree', instanceKey: null },
        },
      ],
    },
  ]
  return bar
}

/** 兼容旧名 */
export const defaultToolbar = defaultTopToolbar

export function defaultStatusbar() {
  const bar = createBar()
  bar.items = [
    {
      id: 'sb_tasks',
      kind: 'button',
      zone: 'left',
      label: '任务',
      icon: 'list-tree',
      style: 'icon-text',
      tooltip: '在自由 tab 容器打开任务树',
      visible: true,
      command: { type: 'open-tabs', targetLeafId: null, viewKey: 'task-tree', instanceKey: null },
    },
    {
      id: 'sb_mcp',
      kind: 'button',
      zone: 'right',
      label: 'MCP',
      icon: 'plug',
      style: 'text',
      tooltip: 'MCP 配置',
      visible: true,
      command: { type: 'open-dialog', viewKey: 'mcp-config', instanceKey: null },
    },
  ]
  return bar
}

/** 默认具名抽屉：右侧「文件」抽屉（带自定义工具栏）+ 底部「任务」抽屉 */
export function defaultDrawers() {
  return [
    {
      id: 'drawer_files',
      label: '文件',
      icon: 'folder-open',
      position: 'right',
      size: 380,
      modal: true,
      toolbarId: 'tb_detail',
      toolbarPosition: 'bottom',
      tabs: [{ viewKey: 'file-browser', instanceKey: null, label: '文件浏览' }],
    },
    {
      id: 'drawer_tasks',
      label: '任务',
      icon: 'clipboard-list',
      position: 'bottom',
      size: 260,
      modal: false,
      toolbarId: null,
      toolbarPosition: 'top',
      tabs: [
        { viewKey: 'task-tree', instanceKey: null, label: '任务树' },
        { viewKey: 'task-detail', instanceKey: 'taskId', label: '任务详情' },
      ],
    },
  ]
}

export function defaultConfig() {
  return {
    version: 6,
    layout: defaultLayout(),
    topToolbar: defaultTopToolbar(),
    statusbar: defaultStatusbar(),
    toolbars: defaultToolbars(),
    drawers: defaultDrawers(),
  }
}

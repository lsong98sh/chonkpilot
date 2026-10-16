import { useConfigStore } from '../stores/config'
import { useRuntimeStore } from '../stores/runtime'
import { viewLabel } from '../config/views'
import { regionKind } from '../utils/regions'
import { EventNames } from '../events/event-names'
import { getMq } from '../deps.js'
import { tr } from '../deps.js'

/** 自由 tab 容器区域 → 展示名（targetLeafId 为空表示自动） */
export function describeTabsTarget(targetLeafId) {
  const config = useConfigStore()
  const runtime = useRuntimeStore()
  const leaves = config.freeTabsLeaves
  if (targetLeafId) {
    const leaf = leaves.find((l) => l.id === targetLeafId)
    return leaf?.title || targetLeafId
  }
  const active = leaves.find((l) => l.id === runtime.activeLeafId)
  if (active) return `${active.title || '自由 tab 容器'}（当前区域）`
  return leaves[0] ? `${leaves[0].title || '自由 tab 容器'}（第一个）` : '无可用容器'
}

/**
 * 命令 → MQ 主题 + payload（所有命令统一走消息中心执行）。
 *   { type: 'open-dialog', viewKey, instanceKey, title? }
 *   { type: 'open-tabs',   viewKey, instanceKey, targetLeafId? }
 *   { type: 'open-drawer', drawerId, viewKey?, instanceKey? }
 *   { type: 'close-tabs-active', targetLeafId? }
 */
export function commandToMq(cmd) {
  if (!cmd || !cmd.type) return null
  switch (cmd.type) {
    case 'open-dialog':
      return {
        topic: EventNames.dialogOpen,
        payload: { viewKey: cmd.viewKey, instanceKey: cmd.instanceKey ?? null, title: cmd.title || '' },
      }
    case 'open-tabs':
      return {
        topic: EventNames.tabsOpen,
        payload: {
          targetLeafId: cmd.targetLeafId ?? null,
          viewKey: cmd.viewKey,
          instanceKey: cmd.instanceKey ?? null,
        },
      }
    case 'open-drawer':
      return {
        topic: EventNames.drawerOpen,
        payload: { drawerId: cmd.drawerId, viewKey: cmd.viewKey ?? null, instanceKey: cmd.instanceKey ?? null },
      }
    case 'toggle-drawer':
      return { topic: EventNames.drawerToggle, payload: { drawerId: cmd.drawerId } }
    case 'close-tabs-active':
      return { topic: EventNames.tabsCloseActive, payload: { targetLeafId: cmd.targetLeafId ?? null } }
    case 'publish-mq':
      // 〔UILAYOUT-1 裁决 1（2026-10-16）〕条目可直接发布一条**既有宿主主题** + 载荷。
      // 机制层不认识任何宿主主题，只把配置里的 `{topic, payload}` 原样交给 mq；
      // **零新增/零改形**（配置里只允许引用既有主题）。缺 topic → 无效命令（按钮点击 no-op / 菜单项禁用）。
      return cmd.topic ? { topic: cmd.topic, payload: cmd.payload ?? {} } : null
    default:
      return null
  }
}

/** 通过 MQ 执行一条命令（所有入口统一走这里）。 */
export function runCommand(cmd) {
  const runtime = useRuntimeStore()
  const route = commandToMq(cmd)
  if (!route) {
    runtime.setHint('该条目未配置命令')
    return { ok: false, message: '该条目未配置命令' }
  }
  getMq().emit(route.topic, route.payload)
  return { ok: true, message: describeCommand(cmd) }
}

/** 命令的中文描述，用于编辑器摘要 */
export function describeCommand(cmd) {
  if (!cmd) return '未配置命令'
  const config = useConfigStore()
  switch (cmd.type) {
    case 'open-dialog':
      return `对话框打开 ${viewLabel(cmd.viewKey)}`
    case 'open-tabs':
      return `在自由 tab 容器「${describeTabsTarget(cmd.targetLeafId)}」打开 ${viewLabel(cmd.viewKey)}`
    case 'open-drawer': {
      const drawer = config.drawerById(cmd.drawerId)
      const label = drawer ? drawer.label : cmd.drawerId || '未选择抽屉'
      return `打开抽屉「${label}」${cmd.viewKey ? ` · ${viewLabel(cmd.viewKey)}` : ' · 默认内容'}`
    }
    case 'close-tabs-active':
      return `关闭自由 tab 容器「${describeTabsTarget(cmd.targetLeafId)}」当前标签页`
    case 'toggle-drawer': {
      const drawer = config.drawerById(cmd.drawerId)
      const label = drawer ? drawer.label : cmd.drawerId || '未选择抽屉'
      return `开 / 关抽屉「${label}」（已打开则关闭）`
    }
    case 'publish-mq':
      return cmd.topic
        ? tr('command.describe.publishMq', { topic: cmd.topic })
        : tr('command.describe.publishMqEmpty')
    default:
      return `未知命令类型：${cmd.type}`
  }
}

/** 命令类型清单（编辑器下拉用） */
export const COMMAND_TYPES = [
  { value: 'open-dialog', label: '1. 以对话框方式打开 view' },
  { value: 'open-tabs', label: '2. 将 view 在某个自由 tab 容器打开' },
  { value: 'open-drawer', label: '3. 打开抽屉（抽屉内容 = view）' },
  { value: 'toggle-drawer', label: '3b. 开 / 关抽屉（同一个按钮切换）' },
  { value: 'close-tabs-active', label: '4. 关闭目标自由 tab 容器的当前标签页' },
  { value: 'publish-mq', label: '5. 发布 MQ 动作（既有主题 + 载荷）' },
]

/** 区域的 view 清单（用于占用登记等） */
export function viewsOfLeaf(leaf) {
  if (!leaf || regionKind(leaf) !== 'single') return leaf?.tabs ?? []
  return leaf.view ? [leaf.view] : []
}

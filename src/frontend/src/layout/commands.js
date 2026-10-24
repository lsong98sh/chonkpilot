/**
 * 命令模型与落点映射（UILAYOUT-1 §3.5 · §7 P3②）。
 *
 * 口径（§3.5「已定 = 收敛为一条命令」）：对外统一
 *   `open-view { viewKey, instanceKey, target }`，`target ∈ auto|dialog|tabs|drawer|<leafId>|<drawerId>`
 * 参考工程原有 4 类命令（`open-dialog` / `open-tabs` / `open-drawer` / `close-tabs-active`）
 * **作为编辑器里的语法糖保留**（编辑 UI 不变），落点解析集中在本文件一处。
 *
 * 落点（§3.5 映射表）——**一律落到既有主题，零新增**：
 *   | 命令目标                                    | 落点                                        |
 *   |---------------------------------------------|---------------------------------------------|
 *   | 配置页 / 场景页 / 记忆用户偏好（功能页类）   | `preview-tab-open { kind }`                 |
 *   | 打开文件                                    | `file-open { path, temporary:true }`        |
 *   | 打开会话                                    | `session-select { session_id }`             |
 *   | 注册表里的 layout 区块（explorer/preview/…） | 机制层 `lf-layout-open-tabs`（自由页签区）  |
 *   | 具名抽屉                                    | 机制层 `lf-layout-open-drawer`              |
 *   | 关激活页签（`close-tabs-active`）           | `preview-tab-close-this`                    |
 *   | `host:'dialog'`（一期为空）                 | **拒绝并提示**（配置页一律走功能页）        |
 *
 * **命令可达范围（P3 取舍，必须如实知悉）**：
 *   1. `selection.activeFile` 是**只读镜像**（P2 裁决 1 / §4.1 ③「一期不动预览页签模型」）→
 *      命令**不能反向驱动** `CodeView` 的页签集合；功能页/文件只能经既有 `preview-tab-open` /
 *      `file-open` 主题"请求"预览区打开（CodeView 自己决定开合）；
 *   2. 一期**没有 free-tabs 区域**（默认文档 = 现布局等价结构）→ 注册表 layout 区块的
 *      `open-tabs` 落点会由机制层提示「布局中没有自由 tab 容器区域」；
 *   3. P3 无命令源（栏条目化属 P4）→ 本模块只提供**解析 + 执行**入口，
 *      `registerCommandSubscribers` 供 P4 接线（见该函数注释里"单执行者"约束）。
 *
 * 本模块依赖：机制层 `EventNames`（纯常量）+ 宿主 `EventNames` + 定义表 → 可在 node:test 直跑。
 */
import mq from '../utils/mq'
import { EventNames } from '../events/event-names'
import { EventNames as LfEventNames } from '../layout-free/events/event-names'
import { definitionOf, hostOf } from './view-definitions.js'
import { lfT } from './i18n.js'

/** `open-view` 的 target 取值（`<leafId>` / `<drawerId>` 由调用方直接传入字符串） */
export const TARGET = { AUTO: 'auto', DIALOG: 'dialog', TABS: 'tabs', DRAWER: 'drawer' }

/** 功能页类逻辑目标（→ `preview-tab-open{kind}`；与 `CodeView.specialComponents` 键一一对应） */
export const PREVIEW_TAB_TARGETS = [
  'settings-llm',
  'settings-mcp',
  'settings-tool-async',
  'settings-tool-sandbox',
  'settings-paths',
  'settings-params',
  'settings-project',
  'scenario',
  'memory-user-pref',
  // 长任务详情页（STB-1）：状态栏条目 → preview 功能页（零新增消息主题）
  'status-memory',
  'status-compress',
  'status-index',
  // 插件管理页（PLUGIN-1）：插件栏 → preview 功能页
  'plugin-manager',
]

/** 文件 / 会话逻辑目标（非注册表 view；供宿主侧命令使用，§3.5 表） */
export const FILE_TARGET = 'file'
export const SESSION_TARGET = 'session'

function reject(reasonKey) {
  return { ok: false, reasonKey, reason: lfT(`command.reject.${reasonKey}`) }
}

/**
 * 4 类语法糖 → 统一 `open-view` / `close-tabs-active` 归一形态。
 * @returns {{type:string, viewKey?:string, instanceKey?:string|null, target?:string}|null}
 */
export function normalizeCommand(cmd) {
  if (!cmd || !cmd.type) return null
  const viewKey = cmd.viewKey || ''
  const instanceKey = cmd.instanceKey ?? null
  switch (cmd.type) {
    case 'open-view':
      return {
        type: 'open-view',
        viewKey,
        instanceKey,
        target: cmd.target || TARGET.AUTO,
        drawerId: cmd.drawerId ?? '',
        params: cmd.params ?? null,
      }
    case 'open-dialog':
      return { type: 'open-view', viewKey, instanceKey, target: TARGET.DIALOG, params: cmd.params ?? null }
    case 'open-tabs':
      return {
        type: 'open-view',
        viewKey,
        instanceKey,
        target: cmd.targetLeafId || TARGET.TABS,
        params: cmd.params ?? null,
      }
    case 'open-drawer':
      return {
        type: 'open-view',
        viewKey,
        instanceKey,
        target: cmd.drawerId || TARGET.DRAWER,
        drawerId: cmd.drawerId || '',
        params: cmd.params ?? null,
      }
    case 'close-tabs-active':
      return { type: 'close-tabs-active', target: cmd.targetLeafId || TARGET.AUTO }
    default:
      return null
  }
}

/** 自由页签区的落点（功能页类 → 预览区功能页；文件/会话 → 既有主题；注册表 layout 区块 → 机制层） */
function tabsRoute(viewKey, instanceKey, targetLeafId) {
  const def = definitionOf(viewKey)
  // 插件 iframe view：若声明 preview 容器 → 预览区插件页（CodeView 自管页签）
  if (def?.plugin && Array.isArray(def.containers) && def.containers.includes('preview')) {
    return {
      ok: true,
      topic: EventNames.previewTabOpen,
      payload: {
        kind: 'plugin-page',
        plugin: def.plugin.id,
        page: def.plugin.view,
        title: def.label,
      },
    }
  }
  if (PREVIEW_TAB_TARGETS.includes(viewKey)) {
    return { ok: true, topic: EventNames.previewTabOpen, payload: { kind: viewKey } }
  }
  if (viewKey === FILE_TARGET) {
    const path = instanceKey || ''
    if (!path) return reject('fileNeedPath')
    return { ok: true, topic: EventNames.fileOpen, payload: { path, temporary: true } }
  }
  if (viewKey === SESSION_TARGET) {
    const sessionId = instanceKey || ''
    if (!sessionId) return reject('sessionNeedId')
    return { ok: true, topic: EventNames.sessionSelect, payload: { session_id: sessionId } }
  }
  const host = hostOf(viewKey)
  if (host === 'layout') {
    // 机制层自由页签区（`lf-layout-open-tabs` → `runtime.openTab`）
    return {
      ok: true,
      topic: LfEventNames.tabsOpen,
      payload: { targetLeafId: targetLeafId || null, viewKey, instanceKey },
    }
  }
  if (host === 'bar') return reject('barViewNotInTabs')
  return reject('unknownTarget')
}

/**
 * `open-view` 落点解析（不执行；纯函数）。
 * @param {{viewKey:string, instanceKey?:string|null, target?:string, drawerId?:string}} cmd 归一后的命令
 * @param {{hasDrawer?: (id:string)=>boolean}} [opts] 抽屉存在性探针（缺省 false）
 * @returns {{ok:true, topic:string, payload:object}|{ok:false, reasonKey:string, reason:string}}
 */
export function resolveOpenView(cmd = {}, { hasDrawer } = {}) {
  const viewKey = String(cmd.viewKey || '')
  if (!viewKey) return reject('missingView')
  const instanceKey = cmd.instanceKey ?? null
  const target = cmd.target || TARGET.AUTO
  const def = definitionOf(viewKey)

  // ① 对话框目标：host:'dialog' 或动态 view 声明 containers 含 dialog
  if (target === TARGET.DIALOG) {
    if (def && (def.host === 'dialog' || (Array.isArray(def.containers) && def.containers.includes('dialog')))) {
      return { ok: true, topic: LfEventNames.dialogOpen, payload: { viewKey, instanceKey, title: '' } }
    }
    return reject('noDialogHost')
  }

  // ② 具名抽屉：`target:'drawer'` 或 target = 已知 drawerId
  const isDrawerTarget = target === TARGET.DRAWER || (typeof hasDrawer === 'function' && hasDrawer(target))
  if (isDrawerTarget) {
    const drawerId = cmd.drawerId || (target === TARGET.DRAWER ? '' : target)
    if (!drawerId) return reject('drawerNeedId')
    if (def?.plugin && Array.isArray(def.containers) && !def.containers.includes('drawer')) {
      return reject('noDrawerHost')
    }
    return { ok: true, topic: LfEventNames.drawerOpen, payload: { drawerId, viewKey, instanceKey } }
  }

  // ③ 自由页签区（auto | tabs | 具体 leafId）
  const targetLeafId = target === TARGET.AUTO || target === TARGET.TABS ? null : target
  return tabsRoute(viewKey, instanceKey, targetLeafId)
}

/**
 * 统一命令解析（语法糖归一 + 落点解析）。
 * @returns {{ok:true, topic:string, payload:object, command:object}|{ok:false, reasonKey:string, reason:string}}
 */
export function resolveCommand(cmd, opts) {
  const norm = normalizeCommand(cmd)
  if (!norm) return reject('missingCommand')
  if (norm.type === 'close-tabs-active') {
    // §3.5：落到现有「关激活页签」（预览区页签模型不动）
    return { ok: true, topic: EventNames.previewTabCloseThis, payload: {}, command: norm }
  }
  const route = resolveOpenView(norm, opts)
  return route.ok ? { ...route, command: norm } : route
}

/**
 * 执行一条命令（解析 → 发既有主题）。
 * @returns 解析结果（`{ok:false}` 时未发消息；失败原因在 `reason`）
 */
export function executeCommand(cmd, { bus = mq, ...opts } = {}) {
  const route = resolveCommand(cmd, opts)
  if (!route.ok) return route
  try {
    bus.emit(route.topic, route.payload)
  } catch (e) {
    return reject('emitFailed')
  }
  return route
}

/** 命令摘要（编辑器 / 状态栏提示用；i18n）。 */
export function describeCommand(cmd) {
  const norm = normalizeCommand(cmd)
  if (!norm) return lfT('command.describe.none')
  if (norm.type === 'close-tabs-active') return lfT('command.describe.closeActive')
  const label = definitionOf(norm.viewKey)?.label || norm.viewKey
  return lfT('command.describe.openView', { view: label })
}

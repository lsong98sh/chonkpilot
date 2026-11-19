/**
 * hostState —— 主程序状态注册（阶段1「前端状态注册 v1」，2026-11-19）。
 *
 * 口径：
 *   - canonical key 是宿主状态的唯一注册名；消费者经 `ckStore.ref(key)` 只读；
 *   - `selection.js` 是 owner：各 setter 通过 `publishHostState()` 把值投影到注册表；
 *   - 兼容 alias（layout-free 既有 key）：`project` / `sessionId` / `filePath` / `taskId`
 *     解引用到 canonical；alias 标记 `hidden:true`，不出现在 layout-free 变量下拉里，
 *     但 `ckStore.ref('sessionId')` 仍可用；
 *   - **禁 watch / watchEffect / $subscribe**：通知由 `ckStore#set` 内部手动触发 listeners。
 *
 * 本模块幂等：重复调用 `installHostStates()` 不会覆盖已有状态，只按传入的 selection
 * 同步当前值（未创建的状态才创建）。
 */
import { alias, create, set, ckStore } from '../utils/ckStore.js'

/**
 * canonical 状态定义。defaultValue 与既有 selection / layout-free 默认值保持一致。
 * `persist` 暂为 none（本阶段不做跨会话状态持久化）。
 */
const HOST_STATES = [
  {
    key: 'workdir',
    label: '当前项目',
    description: '当前打开的项目根目录',
    defaultValue: '',
  },
  {
    key: 'selected-session',
    label: '会话 ID',
    description: '当前主会话标识',
    defaultValue: '',
  },
  {
    key: 'active-file-path',
    label: '文件路径',
    description: '预览区当前激活文件的路径',
    defaultValue: '',
  },
  {
    key: 'active-task-id',
    label: '任务 ID',
    description: '任务详情视图当前打开的任务标识',
    defaultValue: '',
  },
  {
    key: 'explorer-mode',
    label: '左侧模式',
    description: '左侧资源面板当前模式',
    defaultValue: 'workspaces',
  },
  {
    key: 'mcpServer',
    label: 'MCP 服务',
    description: 'MCP 配置页定位的服务名',
    defaultValue: 'filesystem',
  },
  {
    key: 'model',
    label: '模型',
    description: 'LLM 配置页定位的模型',
    defaultValue: 'deepseek-chat',
  },
  {
    key: 'tabGroup',
    label: '标签组',
    description: '自由 tabs 区的分组标识',
    defaultValue: 'main',
  },
  {
    key: 'searchBox',
    label: '搜索框',
    description: '内嵌搜索框（视图条目）的实例标识',
    defaultValue: 'main',
  },
]

/** layout-free 既有 key → canonical 状态（alias 不出现在变量下拉，但读取/写入仍可用）。 */
const HOST_ALIASES = [
  { key: 'project', target: 'workdir', label: '当前项目', description: '当前打开的项目标识', defaultValue: '' },
  { key: 'sessionId', target: 'selected-session', label: '会话 ID', description: '对话会话标识', defaultValue: '' },
  { key: 'filePath', target: 'active-file-path', label: '文件路径', description: '文件浏览器 / 编辑器定位的文件', defaultValue: '' },
  { key: 'taskId', target: 'active-task-id', label: '任务 ID', description: '任务详情视图使用的任务标识', defaultValue: '' },
]

/** 从 selection（Pinia store）读取 canonical 状态的当前值；无映射 → undefined。 */
function selectionValue(selection, key) {
  if (!selection) return undefined
  switch (key) {
    case 'workdir':
      return String(selection.workDir || '')
    case 'selected-session':
      return String(selection.activeSession?.sessionId || '')
    case 'active-file-path':
      return String(selection.activeFile?.path || '')
    case 'active-task-id':
      return String(selection.activeTask?.taskId || '')
    case 'explorer-mode':
      return String(selection.explorerMode || '')
    default:
      return undefined
  }
}

/** 当前已注册状态 key 集合（避免重复 create 触发告警）。 */
function stateKeySet() {
  return new Set(ckStore.listStateKeys())
}

/**
 * 安装主程序状态（幂等）。
 *
 * @param {object} [selection] Pinia selection store（可选；传入时用其当前值做 initial / 同步值）
 * @returns {typeof ckStore}
 */
export function installHostStates(selection) {
  const existing = stateKeySet()

  for (const def of HOST_STATES) {
    const current = selectionValue(selection, def.key)
    const initial = current !== undefined ? current : def.defaultValue

    if (!existing.has(def.key)) {
      create({
        key: def.key,
        initial,
        defaultValue: def.defaultValue,
        label: def.label,
        description: def.description,
        owner: 'host',
        scope: 'window',
        persist: 'none',
        schema: 'string',
      })
    } else if (current !== undefined) {
      // 已安装：按 owner（selection）当前值同步，不重复注册 / 不告警。
      set(def.key, current)
    }
  }

  for (const def of HOST_ALIASES) {
    if (!existing.has(def.key)) {
      alias(def.key, def.target, {
        label: def.label,
        description: def.description,
        defaultValue: def.defaultValue,
        hidden: true,
      })
    }
  }

  return ckStore
}

/**
 * 把 owner 侧的状态值写入注册表（selection setter 调用）。
 * 未安装 hostState 时 `set` 返回 false —— 安全 no-op（测试 / 嵌入式场景）。
 *
 * @param {string} key canonical 或 alias key
 * @param {any} value
 * @returns {boolean}
 */
export function publishHostState(key, value) {
  try {
    return set(key, value)
  } catch (e) {
    if (typeof console !== 'undefined' && typeof console.warn === 'function') {
      console.warn(`[hostState] 发布状态失败 key=${key}: ${(e && e.message) || e}`)
    }
    return false
  }
}
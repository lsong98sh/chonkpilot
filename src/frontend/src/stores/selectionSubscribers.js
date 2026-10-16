/**
 * selectionSubscribers —— 跨 view 共享状态的**mq 写入点**（UILAYOUT-1 §3.7 / §7 P2②）。
 *
 * 口径（§3.7 写入规则 · [17-前端状态与MQ] §2）：
 *   - 跨 view 状态**只由 mq 订阅者写**；组件只读不写（私有状态除外）；
 *   - **零新增主题**：只订阅既有主题（`event-names.js` 既有键，逐字未改）；
 *   - 写入 = 收到消息 → 写 store（**无 watch / 无 $subscribe**）。
 *
 * 订阅清单（既有主题 → 写哪个字段）：
 *   - `session-changed{ session_id }`      → `selection.activeSession.sessionId`（当前**主**会话）
 *   - `subsession-changed{ session_id }`   → `selection.activeTask.sessionId`（当前子会话；清 taskId）
 *   - `task-detail-open{ task_id }`        → `selection.activeTask.taskId`（任务详情叠加态）
 *
 * 未纳入本模块的三处写入点（各有其因，见 §3.7 / §6；均**不新增主题**）：
 *   - `explorerMode`   ← `filetree-mode-select` / `filetree-mode-toggle`：**单一写入者**（ExplorerPane
 *     自身），无"各自为政"双源；订阅保留在 ExplorerPane（`setMode` 受理四模式后写 store）——
 *     其四模式受理逻辑为既有源码守卫（`sessionNavCopy.test.js` / `extensionsPane.test.js`）所锁定；
 *   - `activeFile` / `activeSelection` ← CodeView 页签本地变更 / 选区变更（本节主题载荷为空，
 *     生产者只能是 CodeView 本体；§6「`selection.activeFile` 镜像时机」）；
 *   - `workDir`        ← `gui.init-data` 应答（**非事件**，无既有主题携带 work_dir）→ 唯一漏斗
 *     `api/file.js#setWorkDir`（FileTree / CodeView 消费点调用）。
 *
 * 注册 / 注销：
 *   - 注册 = `main.js` 引导期（`app.use(createPinia())` 之后、`mount` 之前）调 `registerSelectionSubscribers()`；
 *   - 注销 = 返回的 `unregisterSelectionSubscribers()`（应用生命周期内常驻；测试用例用其收尾）。
 */
import mq from '../utils/mq.js'
import { EventNames } from '../events/event-names.js'
import { useSelectionStore } from './selection.js'

/** 已注册的退订函数（模块级：注册幂等，防多处调用重复订阅）。 */
let _unsubs = []

/** 取载荷里的字符串字段（缺省/空 → ''；载荷可为 undefined / null / 非对象）。 */
function strOf(payload, key) {
  const v = payload && typeof payload === 'object' ? payload[key] : ''
  return v ? String(v) : ''
}

/**
 * 注册跨 view 状态订阅（幂等；每窗口一份 JS 上下文 = 一份订阅）。
 * @returns {() => void} 退订函数（等价 `unregisterSelectionSubscribers`）
 */
export function registerSelectionSubscribers() {
  if (_unsubs.length) return unregisterSelectionSubscribers
  const store = useSelectionStore()
  _unsubs = [
    // 当前主会话（App 启动 / 新建会话 / SessionsPane / 子聊窗口回切 等既有生产者不变）
    mq.on(EventNames.sessionChanged, (p) => store.setActiveSession(strOf(p, 'session_id'))),
    // 当前子会话（SessionTree 点选 / 自动选中最新 / TaskDetailView 返回 等既有生产者不变）
    mq.on(EventNames.subsessionChanged, (p) => store.setActiveSubSession(strOf(p, 'session_id'))),
    // 任务详情叠加态（会话树任务行 / 消息卡「查看任务详情」）
    mq.on(EventNames.taskDetailOpen, (p) => store.setActiveTaskId(strOf(p, 'task_id'))),
  ]
  return unregisterSelectionSubscribers
}

/** 退订全部跨 view 状态订阅（幂等）。 */
export function unregisterSelectionSubscribers() {
  for (const fn of _unsubs) fn()
  _unsubs = []
}

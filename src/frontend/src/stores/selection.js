/**
 * selection —— 跨 view 共享状态（UILAYOUT-1 §3.7 / §7 P2①）
 *
 * **唯一真相源**：workDir · activeFile · activeSession · activeTask · explorerMode · activeSelection
 * ——把原先散落在各 view 内部 ref / 模块变量里的跨 view 状态收敛到一处（消除"两个 view 各自
 * 持有一份会话/文件状态"的双源问题；见 §7 "P2 必须先于 P3/P4"）。
 *
 * 边界（§3.7 硬约束）：
 *   - **只放被 ≥2 个 view 读/写**的状态；单 view 私有状态（如 CodeView 的页签集合、SessionTree
 *     的 node_id 高亮）继续留组件内；
 *   - **写入点**：mq 订阅者（`selectionSubscribers.js`）+ 无 mq 主题可依的两处漏斗
 *     （`api/file.js#setWorkDir` ← gui.init-data；CodeView 页签本地变更 → activeFile/activeSelection 镜像）；
 *   - **组件只读**（`storeToRefs` / getter），不直接写（私有状态除外）；
 *   - **禁 watch**（含 `$subscribe`）：状态推进一律 = 收到消息 → 写 store（[17-前端状态与MQ] §2/§3）；
 *   - getter 为**纯派生**：禁发 mq / 禁异步 / 禁写 state（[17] §3）。
 *
 * 不入本 store（§3.7 判据：已是模块级单例 / 服务对象，迁入只增耦合）：
 *   `useTaskView`（任务树/节点数据）· `useQueueStatus` / `indexStatus` · `askUserManager` ·
 *   `DialogManager` · `useUserConfigSync`（theme/locale 跨窗口共享 ref）· 区块显隐（属布局域）。
 */
import { defineStore } from 'pinia'

/** 左侧（ExplorerPane）四模式（与既有 filetree-mode-select 载荷取值一致）。 */
export const EXPLORER_MODES = ['project', 'sessions', 'memory', 'extensions']

/** 左侧默认模式。 */
export const DEFAULT_EXPLORER_MODE = 'project'

export const useSelectionStore = defineStore('selection', {
  state: () => ({
    /** 当前项目根（原 `api/file.js` 模块级 `_workDir`）；`api/file.js` 的 filesys 请求据此填充 work_dir。 */
    workDir: '',
    /** 预览区当前激活**文件**（只读镜像；功能页页签激活时为空 —— 一期 CodeView 自管页签，§4.1 ③）。 */
    activeFile: { path: '', renderType: '' },
    /** 当前**主**会话（`session-changed` 载荷；空 = 未选中/新建中）。 */
    activeSession: { sessionId: '' },
    /**
     * 当前**任务区选中项**（右侧 SessionChat / SessionTree）：
     *   - `sessionId` ← `subsession-changed`（子会话身份，选中即清空 taskId）；
     *   - `taskId`    ← `task-detail-open`（任务节点主键，叠加在 sessionId 之上）。
     * 〔字段细化〕设计稿 §3.7 记为 `activeTask{taskId}`；因 `subsession-changed` 载荷是
     * `session_id` 而 `task-detail-open` 是 `task_id`，两项均需收敛（SessionChat 现状同时持有），
     * 故本 store 以 `{sessionId, taskId}` 承载（纯增字段，不动既有载荷）。
     */
    activeTask: { sessionId: '', taskId: '' },
    /** 左侧模式（ExplorerPane 四模式）。 */
    explorerMode: DEFAULT_EXPLORER_MODE,
    /**
     * 预览区选中文本快照（`preview-selection-to-chat` 语义）。
     * `range` = 预留槽（结构化区间；一期只落 `text` —— 该主题载荷为空，生产者只能是 CodeView，
     * DOM→字符偏移映射属后续增强，故恒为 null）。
     */
    activeSelection: { text: '', range: null },
  }),

  getters: {
    // ── 纯读派生（等价 computed；禁副作用）──
    activeFilePath: (s) => s.activeFile.path,
    activeFileRenderType: (s) => s.activeFile.renderType,
    hasActiveFile: (s) => !!s.activeFile.path,

    activeSessionId: (s) => s.activeSession.sessionId,
    hasActiveSession: (s) => !!s.activeSession.sessionId,

    activeTaskId: (s) => s.activeTask.taskId,
    activeTaskSessionId: (s) => s.activeTask.sessionId,
    hasActiveTask: (s) => !!s.activeTask.taskId,

    hasActiveSelection: (s) => !!s.activeSelection.text,
    isProjectMode: (s) => s.explorerMode === 'project',
  },

  actions: {
    /** 工作目录（原 `api/file.js` 的模块级 `_workDir` 唯一落点）。 */
    setWorkDir(dir) {
      this.workDir = dir ? String(dir) : ''
    },

    /** 预览激活文件镜像（`{path, renderType}`；空 = 无文件激活）。 */
    setActiveFile(file) {
      const f = file || {}
      this.activeFile = { path: String(f.path || ''), renderType: String(f.renderType || '') }
    },

    /** 当前主会话（`session-changed`）。 */
    setActiveSession(sessionId) {
      this.activeSession = { sessionId: sessionId ? String(sessionId) : '' }
    },

    /** 子会话选中（`subsession-changed`）：选中子会话即清掉任务详情叠加态（与既有 SessionChat 同口径）。 */
    setActiveSubSession(sessionId) {
      this.activeTask = { sessionId: sessionId ? String(sessionId) : '', taskId: '' }
    },

    /** 任务详情叠加（`task-detail-open`）：仅改 taskId，保留当前子会话身份。 */
    setActiveTaskId(taskId) {
      this.activeTask = { ...this.activeTask, taskId: taskId ? String(taskId) : '' }
    },

    /** 左侧模式（非 EXPLORER_MODES 取值一律忽略 —— 防脏值写坏渲染分支）。 */
    setExplorerMode(mode) {
      if (EXPLORER_MODES.includes(mode)) this.explorerMode = mode
    },

    /** 预览区选中文本快照（空文本 = 无选区）。 */
    setActiveSelection(sel) {
      const s = sel || {}
      this.activeSelection = {
        text: String(s.text || ''),
        range: s.range === undefined ? null : s.range,
      }
    },

    /**
     * 状态复位（**项目/实例边界**）：把六项跨 view 状态清回默认。
     * 调用点 = ① 项目就地切换（`utils/projectReinit.js` 序列，切项目即重置"当前会话与任务树"，
     * 见该模块 §覆盖范围）；② 实例认领复位（登出，`useInstanceClaim.resetClaim`）。
     * —— 组件重挂不再自带"状态归零"（状态已外置），故边界处必须显式复位，
     * 否则旧项目/旧身份的值会残留给新组件（首次渲染闪现旧会话/旧文件）。
     */
    reset() {
      this.workDir = ''
      this.activeFile = { path: '', renderType: '' }
      this.activeSession = { sessionId: '' }
      this.activeTask = { sessionId: '', taskId: '' }
      this.explorerMode = DEFAULT_EXPLORER_MODE
      this.activeSelection = { text: '', range: null }
    },
  },
})

/**
 * useTaskStatus — 任务节点「状态 → 图标 / 配色 / 动画 / 文案」的**唯一映射**（I-94）。
 *
 * 状态字段的真实来源（以代码为准，勿改口径）：
 *   - `data-tasktree-list` 节点（落库行，chonkpilot-data/persist/persist_tasktree.go）行内
 *     **同时有 `status` 与 `state`**：
 *       · `status` = 前端可见口径（chonkpilot-task/store.go `frontVisibleStatus`）——
 *         其中 `detached` / `awaiting` 被**塌缩为 `running`**（P3 相位门控，为兼容旧前端）；
 *       · `state`  = 任务层权威细态（8 态）。
 *   - `tasks.*` 事件增量（useTaskView.nodeFromEvent）：节点 `status` = 事件 state（细态）。
 *   - `data-tasktree-tasks`：`status` / `state` 并存，`status` 同为塌缩口径。
 *
 * 因此**展示态** = `status`；**仅当** `status` 因塌缩为 `running` 而丢失 `detached` / `awaiting`
 * 相位时，回读层权威 `state` 细化。其余情形一律以 `status` 为准——避免事件增量合并时读到
 * 陈旧 `state`（事件节点不写 state，merge 后可能残留旧值）。
 *
 * 8 态口径（chonkpilot-task/task.go）：
 *   pending / running / awaiting / detached / done / error / cancelled / interrupted；
 * `closed` 是逻辑删除标记（**非** state）：后端 list/tasks **默认过滤**，前端按新口径请求
 * `include_closed: true` 取回后**只读灰色展示**（42 §2 (126)）；本映射不参与 `closed` 判定
 * （节点样式/「已关闭」标签由 `views/tasks/SessionTreeNode.vue` 依 `node.closed` 决定）。
 *
 * 说明：本模块不依赖 Vue，便于直接单测（useTaskStatus.test.js）。
 */

/** 状态 → 展示元数据。icon 为空 = 不渲染图标（保持既有「未知/空闲不显示」行为）。 */
export const TASK_STATE_META = {
  // 既有 4 态：图标 / 配色 / 动画均与改造前逐值一致，行为保持不变
  running:     { icon: 'loading',             color: 'var(--accent)',         spin: true,  pulse: false, label: 'taskView.state_running' },
  error:       { icon: 'circle-close-filled', color: 'var(--danger)',         spin: false, pulse: false, label: 'taskView.state_error' },
  done:        { icon: 'circle-check-filled', color: '#67c23a',               spin: false, pulse: false, label: 'taskView.state_done' },
  stopped:     { icon: 'circle-close-filled', color: '#bfbfbf',               spin: false, pulse: false, label: 'taskView.state_stopped' },
  // 新增（I-94）
  detached:    { icon: 'arrow-right',         color: 'var(--text-secondary)', spin: false, pulse: false, label: 'taskView.state_detached' },
  awaiting:    { icon: 'warning-filled',      color: 'var(--warning)',        spin: false, pulse: true,  label: 'taskView.state_awaiting' },
  pending:     { icon: 'clock',               color: '#bfbfbf',               spin: false, pulse: false, label: 'taskView.state_pending' },
  interrupted: { icon: 'refresh',             color: '#bfbfbf',               spin: false, pulse: false, label: 'taskView.state_interrupted' },
  // 任务层真实取消态是 cancelled（旧链路的 stopped 为历史死值，此处两者同貌保留）
  cancelled:   { icon: 'circle-close-filled', color: '#bfbfbf',               spin: false, pulse: false, label: 'taskView.state_cancelled' },
  // 未知 / 空闲：不渲染图标
  idle:        { icon: '',                    color: '',                      spin: false, pulse: false, label: '' },
}

/** 被 `frontVisibleStatus` 塌缩为 running 的细态集合（仅这些需要回读 state 细化）。 */
const FRONT_COLLAPSED = { detached: true, awaiting: true }

/**
 * 解析节点**展示态**（见文件头口径）。
 * @param {{status?: string, state?: string}} node tasktree 节点 / 任务快照
 * @returns {string} 展示态（未知 → 'idle'）
 */
export function resolveTaskState(node) {
  const coarse = (node && node.status) || 'idle'
  const fine = node && node.state
  if (coarse === 'running' && fine && FRONT_COLLAPSED[fine]) return fine
  return coarse
}

/** 取展示元数据（未知态回落 idle = 无图标）。 */
export function stateMetaFor(state) {
  return TASK_STATE_META[state] || TASK_STATE_META.idle
}

export function useTaskStatus() {
  return { TASK_STATE_META, resolveTaskState, stateMetaFor }
}

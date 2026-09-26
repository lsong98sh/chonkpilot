/**
 * useAwaitingArbitration — 任务区「待裁决」判定与动作可用性（I-103）。
 *
 * 背景（I-103）：前端工具卡来自 `role=tool` 结果行（`chonkpilot-data/persist/view.go` 的
 * `MessageView`），而到达超时点待裁决（层 `state=awaiting`）时**尚无结果行** → 刷新/切会话后
 * 无卡可挂 → 裁决条消失（`MessageList.vue` 的 `restoreAwaitingArbitration` 恒 `missing`）。
 * 本模块供**任务区**（`views/tasks/SessionTreeNode.vue`）**直接按 `awaiting` 渲染裁决条**使用，
 * 不依赖消息卡；数据来源 = 层权威 `state=awaiting` + `data-tasktree-tasks` 的
 * `awaiting{reason,timeout_s,options}`（I-99 已补；前端经 `useTaskView.refresh` 读入 `tasks` 快照）。
 *
 * `options` 实际取值（gateway）：`never` = `["wait","cancel"]`；`manual` = `["detach","cancel"]`
 * （见 `chonkpilot-mcp-gateway/gateway/mcpgateway.go:851-853`）。
 *
 * 纯函数、不依赖 Vue（同 useTaskStatus 模式）→ 可直接 node:test 单测。
 */

/**
 * 取待裁决明细。
 * 仅当节点**展示态 == `awaiting`**（由调用方传 `useTaskStatus.resolveTaskState` 的展示态：层
 * `frontVisibleStatus` 把 `awaiting` 塌缩为 `running` 时回读层权威 `state`）**且**带非空 `options`
 * 时返回对象，否则 `null`（不渲染裁决条）。明细优先取运行态快照（`data-tasktree-tasks` 的
 * `awaiting`，I-99），回落节点自身字段。
 *
 * @param {string} displayState 节点展示态（`resolveTaskState` 结果；仅 'awaiting' 渲染）
 * @param {{awaiting?: object}} [node] 任务树节点 / 任务快照
 * @param {{awaiting?: object}|null} [runtime] 运行态快照（`useTaskView.nodeRuntime`）
 * @returns {{reason?: string, timeout_s?: number, options: string[]}|null}
 */
export function resolveAwaiting(displayState, node, runtime) {
  if (displayState !== 'awaiting') return null
  const aw = (runtime && runtime.awaiting) || (node && node.awaiting)
  if (!aw || !Array.isArray(aw.options) || aw.options.length === 0) return null
  return aw
}

/**
 * 动作可用性：按 `awaiting.options` 决定可用项。
 * 「转后台」另需 `tool_call_id`（`task-background` 载荷 = `{tool_call_id}`，桥自动补 instance_id）；
 * 无 `tool_call_id`（如仅核心工具 id）时不可转后台。
 *
 * @param {{options?: string[]}|null} awaiting
 * @param {boolean} hasToolCallId 是否有可用的 LLM tool-call id
 * @returns {{wait: boolean, detach: boolean, cancel: boolean}}
 */
export function awaitingActionsFor(awaiting, hasToolCallId) {
  const opts = awaiting && Array.isArray(awaiting.options) ? awaiting.options : []
  return {
    wait: opts.includes('wait'),
    detach: opts.includes('detach') && !!hasToolCallId,
    cancel: opts.includes('cancel'),
  }
}

export function useAwaitingArbitration() {
  return { resolveAwaiting, awaitingActionsFor }
}

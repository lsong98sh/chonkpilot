/**
 * useDslView — DSL 作业展示（DSL-3）前端数据面。
 *
 * 数据源 = **既有 tasktree 面**（`useTaskView` 的 nodes / tasks 快照，经 data-tasktree-list / -tasks
 * 与 tasks.* 事件填充）—— **不新增 MQ 主题**（61 为准）：DSL 静态语句树、步骤执行记录、$RETURN
 * 两态均由后端在 tasktree 节点上以**新字段**承载（契约见交付报告），前端对未就绪字段容错。
 *
 * 口径（[42 §2 (251)/(252)]）：
 *   - 折叠容器节点（LOOP/PARALLEL）由任务树渲染（SessionTreeNode，静态语句、不随迭代增长）；
 *   - 步骤表格 = 每次执行一行（`No` 跨迭代累加）→ `stepsOf(jobId)`；
 *   - 取消 = **作业级**（单一动作，不做行级单步）→ 复用既有 `task-stop`，按 **`tool_call_id`** 取消
 *     （服务端 `onTaskStop` 归一到**持有 exec 句柄的 `dsl_run` 工具节点** → `CancelExec` → 杀执行器进程；
 *     该工具节点子树含 `dsl_job`，故作业级级联仍完整）。无 `tool_call_id` 时回落 `task_id`。
 *
 * 本 composable 复用 `useTaskView`（模块级单例）→ 无独立状态、无 watch（组件间仍走事件）。
 */
import { useTaskView } from './useTaskView'
import mq from '../utils/mq'
import { EventNames } from '../events/event-names'
import { stepsFromNodes, returnInfo, isDslJob } from '../utils/dslView'

export function useDslView() {
  const { nodes, getNode } = useTaskView()

  /** 全部 tasktree 节点（扁平数组；响应式读取 → 调用方可放进 computed）。 */
  function allNodes() {
    const out = []
    for (const n of nodes.values()) out.push(n)
    return out
  }

  /** 某节点是否 DSL 作业根（供 SessionChat 分流：作业 → 步骤面板，否则任务详情）。 */
  function isJobNode(nodeId) {
    return isDslJob(nodeId ? getNode(nodeId) : null)
  }

  /** 作业的步骤执行记录（表格行；`No` 跨迭代累加）。后端字段未就绪 → []。 */
  function stepsOf(jobId) {
    if (!jobId) return []
    return stepsFromNodes(jobId, allNodes(), getNode(jobId))
  }

  /** 作业的 `$RETURN` 结果两态（inline / file）；无 → null。 */
  function returnOf(jobId) {
    return jobId ? returnInfo(getNode(jobId)) : null
  }

  /**
   * 作业级取消（单一动作，作业终止；服务端恒按子树级联）。
   *
   * **按 `tool_call_id` 取消**（而非 `task_id = dsl_job`）：`dsl_run` 的 gateway 执行句柄（gw_task_id）
   * 挂在 **`dsl_run` 工具节点**上（`llm/server` 侧 `setGwTask(node.TaskID, ...)`），而 `dsl_job` 是它的
   * **子节点**；服务端 `CancelSubtree` 只向下收集 exec 句柄 → 用 `task_id=dsl_job` 打不到工具节点、
   * 杀不掉执行器进程。`onTaskStop` 对 `tool_call_id` 会反查归一到该工具节点（`findByToolCall`），
   * 其子树含 `dsl_job` → 级联取消完整且能真正打断。无 `tool_call_id` 时回落 `task_id`（行为不变）。
   */
  function cancelJob(jobId) {
    if (!jobId) return
    const n = getNode(jobId)
    const callId = (n && n.tool_call_id) || ''
    if (callId) {
      mq.emit(EventNames.taskStop, { tool_call_id: callId, cascade: 'all' })
      return
    }
    mq.emit(EventNames.taskStop, { task_id: jobId, cascade: 'all' })
  }

  return { isJobNode, stepsOf, returnOf, cancelJob }
}

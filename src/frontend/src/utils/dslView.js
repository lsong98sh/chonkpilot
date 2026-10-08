/**
 * dslView — DSL 展示（DSL-3）纯逻辑叶子模块（无 Vue / 无 import / 无副作用）。
 *
 * 口径（[42 §2 (251)] / [49 DSL-3]）：
 *   - 任务树节点 = **DSL 静态语句**，不随迭代次数增长 —— `LOOP` / `PARALLEL` 为**折叠容器节点**
 *     （带进度徽标「第 N 轮 / 共 M 轮」），内部步骤执行记录**不进树**（树恒为那 2~3 个语句节点）；
 *   - 右侧面板 = **步骤表格**（列 No / 状态 / 目的 / 耗时 / 时间 + 操作列），每行 = **一次执行**
 *     （循环内步骤 `No` 跨迭代累加，可回看历次）；
 *   - `$RETURN` 结果通道（[42 §2 (249)]）两态 = inline（≤64K 内容）/ file（文件名 + 大小）。
 *
 * 数据来源 = **后端 tasktree 节点**（既有 data-tasktree-list / -tasks 面）的**新字段**
 * （kind / loop_current / loop_total / steps / return_kind 等，见交付报告「后端字段契约」）。前端对未就绪字段一律**容错**
 * （缺省即空 / 不渲染），故模块可在后端落地前独立运行与单测。
 *
 * 本模块是**叶子模块**（可直跑 node --test，见 test/dslView.test.js）。
 */

/** 作业根节点 kind（DSL 作业容器）。 */
export const DSL_JOB_KIND = 'dsl_job'
/** 循环折叠容器 kind。 */
export const DSL_LOOP_KIND = 'dsl_loop'
/** 并行折叠容器 kind。 */
export const DSL_PARALLEL_KIND = 'dsl_parallel'
/** 单次执行记录 kind（仅出现在步骤表格，不进树）。 */
export const DSL_STEP_KIND = 'dsl_step'

/**
 * DSL 展示消费的 tasktree **扩展字段**（实时事件 / 查询结果共同的透传清单）：
 *   - 容器进度：`loop_current` / `loop_total`（折叠容器徽标，SessionTreeNode）；
 *   - 步骤执行记录：`steps`（DslStepsPanel 表格，`No` 跨迭代累加）；
 *   - 影子节点标记：`shadow`（后端若产出则归入步骤表格）；
 *   - `$RETURN` 结果两态：`return_kind` / `return_inline` / `return_file` / `return_size`。
 * 该清单是 DSL 展示的**输入契约**唯一事实源（实时事件透传与查询读取共用）。
 */
export const DSL_FIELDS = [
  'loop_current', 'loop_total', 'steps', 'shadow',
  'return_kind', 'return_inline', 'return_file', 'return_size',
]

/**
 * 把 DSL 扩展字段从来源（`tasks.*` 事件 payload / tasktree 节点）**原样拷入**目标节点。
 * 来源含则带（含 `false` / 空串），缺省**不写** → 配合增量合并（`{...prev, ...node}`）
 * 不误清既有字段。叶子纯函数（供 useTaskView.nodeFromEvent 复用）。
 * @param {object} src 来源（事件 payload / 节点）
 * @param {object} dst 目标节点（同引用写入）
 * @returns {object} dst（便于链式）
 */
export function copyDslFields(src, dst) {
  if (!src || typeof src !== 'object' || !dst || typeof dst !== 'object') return dst
  for (const f of DSL_FIELDS) {
    if (src[f] !== undefined) dst[f] = src[f]
  }
  return dst
}

/** 是否折叠容器节点（LOOP / PARALLEL）。 */
export function isDslContainer(node) {
  const k = node && node.kind
  return k === DSL_LOOP_KIND || k === DSL_PARALLEL_KIND
}

/** 是否 DSL 作业根节点。 */
export function isDslJob(node) {
  return !!node && node.kind === DSL_JOB_KIND
}

/** 容器图标名（LOOP=循环 refresh / PARALLEL=并行 collection）。 */
export function containerIcon(node) {
  return node && node.kind === DSL_PARALLEL_KIND ? 'collection' : 'refresh'
}

/** 数值归一（非有限数 / 负数 → null）。 */
function toInt(v) {
  const n = Number(v)
  return Number.isFinite(n) && n >= 0 ? Math.trunc(n) : null
}

/**
 * 折叠容器进度徽标数据（「第 N 轮 / 共 M 轮」）。
 * 仅容器节点、且后端给了 loop_current / loop_total 之一时返回；否则 null（不渲染徽标）。
 * @returns {{current: (number|null), total: (number|null)}|null}
 */
export function containerProgress(node) {
  if (!isDslContainer(node)) return null
  const current = toInt(node.loop_current)
  const total = toInt(node.loop_total)
  if (current == null && total == null) return null
  return { current, total }
}

/**
 * `$RETURN` 结果两态（[42 §2 (249)]）：inline（文本）/ file（文件名 + 大小）；无 → null。
 * @returns {{kind:'inline', text:string}|{kind:'file', name:string, size:(number|null)}|null}
 */
export function returnInfo(node) {
  if (!node) return null
  if (node.return_kind === 'file' || node.return_file) {
    const name = node.return_file
    if (!name) return null
    return { kind: 'file', name: String(name), size: toInt(node.return_size) }
  }
  const inline = node.return_inline
  if (node.return_kind === 'inline' || (inline != null && inline !== '')) {
    return { kind: 'inline', text: String(inline == null ? '' : inline) }
  }
  return null
}

/** 耗时格式化（毫秒 → `Ns` / `Mm Ss`）；非法 → 空串。 */
export function fmtElapsedMs(ms) {
  const n = Number(ms)
  if (!Number.isFinite(n) || n < 0) return ''
  const sec = Math.floor(n / 1000)
  if (sec < 60) return `${sec}s`
  const m = Math.floor(sec / 60)
  return `${m}m ${sec % 60}s`
}

/** 时间格式化（ISO → `HH:MM:SS` 本地时）；非法 → 空串。 */
export function fmtTime(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const p = (x) => String(x).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/**
 * 归一化步骤执行记录 → 表格行（`No` 跨迭代累加、按 `No` 升序）。
 * 记录自带 `no` 优先；缺失则按输入顺序 1..N 兜底（容错）。
 * @param {Array<object>} records 后端 step 记录（job.steps / shadow 节点）
 * @returns {Array<{no:number,status:string,purpose:string,elapsedMs:(number|null),createdAt:string,sessionId:string,statementId:string}>}
 */
export function normalizeSteps(records) {
  const list = Array.isArray(records) ? records : []
  const rows = list.map((r, i) => {
    const rec = r && typeof r === 'object' ? r : {}
    const no = toInt(rec.no)
    return {
      no: no == null ? i + 1 : no,
      _order: i,
      status: rec.status || rec.state || 'idle',
      purpose: rec.purpose || rec.title || '',
      elapsedMs: rec.elapsed_ms != null ? rec.elapsed_ms : (rec.elapsed != null ? rec.elapsed : null),
      createdAt: rec.created_at || rec.started_at || '',
      sessionId: rec.session_id || '',
      statementId: rec.statement_id || rec.parent_node_id || '',
    }
  })
  rows.sort((a, b) => (a.no - b.no) || (a._order - b._order))
  return rows.map(({ _order, ...row }) => row)
}

/**
 * 收集作业的步骤执行记录（容错）。
 *   ① `job.steps`（后端直给，扁平列表，首选）；
 *   ② 否则从 tasktree 节点聚合：以 jobId 为根，收集后代中 **步骤记录节点**
 *      （`kind === dsl_step` 或 `shadow === true`）—— 这些节点不进树（树只渲染静态语句），
 *      仅在本表格可见（[42 §2 (252)] 子会话隐藏口径）。
 * 非数组 / 空 → []（不抛错，UI 显空态）。
 * @param {string} jobId 作业根节点 id
 * @param {Array<object>} nodes 扁平 tasktree 节点数组
 * @param {object|null} jobNode 作业根节点（可空）
 */
export function stepsFromNodes(jobId, nodes, jobNode) {
  if (jobNode && Array.isArray(jobNode.steps)) return normalizeSteps(jobNode.steps)
  if (!jobId) return []
  const list = Array.isArray(nodes) ? nodes : []
  const childrenOf = {}
  for (const n of list) {
    const p = n.parent_node_id || ''
    ;(childrenOf[p] || (childrenOf[p] = [])).push(n)
  }
  const collected = []
  const stack = [jobId]
  const seen = new Set()
  while (stack.length) {
    const cur = stack.pop()
    if (!cur || seen.has(cur)) continue
    seen.add(cur)
    for (const c of childrenOf[cur] || []) {
      if (c.kind === DSL_STEP_KIND || c.shadow === true) collected.push(c)
      if (c.node_id) stack.push(c.node_id)
    }
  }
  // 无显式 No：按创建时间升序还原跨迭代执行顺序（时间缺失回退原序）
  collected.sort((a, b) => String(a.created_at || '').localeCompare(String(b.created_at || '')))
  return normalizeSteps(collected)
}

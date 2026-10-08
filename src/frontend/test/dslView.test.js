/**
 * DSL-3「DSL 展示」前端骨架（2026-10-07）。
 *
 * 覆盖：① 纯逻辑叶子模块 `utils/dslView.js`（折叠容器判定 / 进度徽标 / 步骤归一化 / $RETURN 两态）；
 *      ② 视图分派 `utils/viewRoute.js`（chat/main）；③ 源码接线守卫
 *      （App.vue 分派 / SessionChat 分流 / DslStepsPanel 列与操作 / 新窗口复用主 chat 多窗口）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

import {
  DSL_JOB_KIND, DSL_LOOP_KIND, DSL_PARALLEL_KIND, DSL_STEP_KIND,
  isDslContainer, isDslJob, containerIcon, containerProgress,
  returnInfo, normalizeSteps, stepsFromNodes, fmtElapsedMs, fmtTime,
} from '../src/utils/dslView.js'
import {
  parseViewRoute,
  VIEW_MAIN, VIEW_CHAT,
} from '../src/utils/viewRoute.js'

const here = dirname(fileURLToPath(import.meta.url))
const feSrc = join(here, '..', 'src')
const read = (rel) => readFileSync(join(feSrc, rel), 'utf8')

// ── ① dslView：折叠容器 / 进度徽标 ────────────────────────────────

test('容器判定：仅 LOOP / PARALLEL 为折叠容器；dsl_job 为作业根', () => {
  assert.equal(isDslContainer({ kind: DSL_LOOP_KIND }), true)
  assert.equal(isDslContainer({ kind: DSL_PARALLEL_KIND }), true)
  assert.equal(isDslContainer({ kind: DSL_JOB_KIND }), false)
  assert.equal(isDslContainer({ kind: 'llm' }), false)
  assert.equal(isDslContainer(null), false)
  assert.equal(isDslJob({ kind: DSL_JOB_KIND }), true)
  assert.equal(isDslJob({ kind: DSL_LOOP_KIND }), false)
})

test('容器图标：LOOP=refresh / PARALLEL=collection', () => {
  assert.equal(containerIcon({ kind: DSL_LOOP_KIND }), 'refresh')
  assert.equal(containerIcon({ kind: DSL_PARALLEL_KIND }), 'collection')
})

test('进度徽标：容器 + loop_current/loop_total → {current,total}；非容器或缺字段 → null', () => {
  assert.deepEqual(containerProgress({ kind: DSL_LOOP_KIND, loop_current: 3, loop_total: 10000 }), { current: 3, total: 10000 })
  assert.deepEqual(containerProgress({ kind: DSL_LOOP_KIND, loop_current: 3 }), { current: 3, total: null })
  assert.deepEqual(containerProgress({ kind: DSL_PARALLEL_KIND, loop_total: 4 }), { current: null, total: 4 })
  assert.equal(containerProgress({ kind: DSL_LOOP_KIND }), null)
  assert.equal(containerProgress({ kind: 'llm', loop_current: 3 }), null) // 非容器不显示徽标
})

// ── ① dslView：$RETURN 两态 ───────────────────────────────────────

test('$RETURN：inline 文本 / file 文件名+大小 / 无 → null', () => {
  assert.deepEqual(returnInfo({ return_kind: 'inline', return_inline: 'hello' }), { kind: 'inline', text: 'hello' })
  assert.deepEqual(returnInfo({ return_inline: 'x' }), { kind: 'inline', text: 'x' }) // 无 kind 但带内容
  assert.deepEqual(returnInfo({ return_kind: 'file', return_file: 'dsl-return-a.md', return_size: 70000 }),
    { kind: 'file', name: 'dsl-return-a.md', size: 70000 })
  assert.deepEqual(returnInfo({ return_file: 'f.md' }), { kind: 'file', name: 'f.md', size: null })
  assert.equal(returnInfo({}), null)
  assert.equal(returnInfo(null), null)
})

// ── ① dslView：步骤归一化（No 跨迭代累加） ────────────────────────

test('normalizeSteps：No 跨迭代累加、按 No 升序；缺 No 按输入序兜底', () => {
  const rows = normalizeSteps([
    { no: 2, status: 'done', purpose: 'b', elapsed_ms: 1500, created_at: '2026-10-07T01:00:02Z', session_id: 's2' },
    { no: 1, status: 'done', purpose: 'a', elapsed_ms: 500, created_at: '2026-10-07T01:00:01Z', session_id: 's1' },
  ])
  assert.deepEqual(rows.map((r) => r.no), [1, 2])
  assert.equal(rows[0].sessionId, 's1')
  assert.equal(rows[0].elapsedMs, 500)
  assert.equal(rows[1].purpose, 'b')

  const noNo = normalizeSteps([{ purpose: 'x' }, { purpose: 'y' }])
  assert.deepEqual(noNo.map((r) => r.no), [1, 2])
  assert.equal(noNo[0].status, 'idle')
  assert.deepEqual(normalizeSteps(null), [])
})

test('stepsFromNodes：优先 job.steps；否则聚合 shadow / dsl_step 后代节点（按时间还原顺序）', () => {
  const job = { node_id: 'job1', kind: DSL_JOB_KIND, steps: [{ no: 1, purpose: 'p', session_id: 's1' }] }
  assert.deepEqual(stepsFromNodes('job1', [], job).map((r) => r.no), [1])

  // 无 steps：从后代节点聚合（shadow=true 或 dsl_step），按 created_at 升序 → No 1..N
  const nodes = [
    { node_id: 'job1', kind: DSL_JOB_KIND, parent_node_id: '' },
    { node_id: 'stA', kind: DSL_LOOP_KIND, parent_node_id: 'job1' }, // 静态容器（不进表格）
    { node_id: 'r2', kind: DSL_STEP_KIND, parent_node_id: 'stA', created_at: '2026-10-07T01:00:02Z', session_id: 's2' },
    { node_id: 'r1', kind: DSL_STEP_KIND, parent_node_id: 'stA', created_at: '2026-10-07T01:00:01Z', session_id: 's1' },
  ]
  const rows = stepsFromNodes('job1', nodes, nodes[0])
  assert.equal(rows.length, 2)
  assert.deepEqual(rows.map((r) => r.sessionId), ['s1', 's2'])
  assert.deepEqual(rows.map((r) => r.no), [1, 2]) // 无显式 No → 按时间
})

// ── ① dslView：格式化 ─────────────────────────────────────────────

test('fmtElapsedMs / fmtTime：时间格式化（非法 → 空串）', () => {
  assert.equal(fmtElapsedMs(500), '0s')
  assert.equal(fmtElapsedMs(1500), '1s')
  assert.equal(fmtElapsedMs(65000), '1m 5s')
  assert.equal(fmtElapsedMs(-1), '')
  assert.equal(fmtElapsedMs(undefined), '')
  assert.equal(fmtTime(''), '')
  assert.equal(fmtTime('not-a-date'), '')
  assert.match(fmtTime('2026-10-07T03:04:05Z'), /^\d{2}:\d{2}:\d{2}$/)
})

// ── ② 视图分派（chat / main）────────────────────────────────────────

test('parseViewRoute：`#chat` + session-id → 对话窗口；其余 → 主窗口', () => {
  assert.deepEqual(parseViewRoute('', ''), { view: VIEW_MAIN, sessionId: '' })
  assert.deepEqual(parseViewRoute('?session-id=s1', '#chat'), { view: VIEW_CHAT, sessionId: 's1' })
  assert.deepEqual(parseViewRoute('?session-id=s1', ''), { view: VIEW_MAIN, sessionId: '' })
})

// ── ③ 源码接线守卫 ────────────────────────────────────────────────

test('App.vue：仅按 URL 分派对话窗口 / 主布局（无 DSL 只读窗口分支）', () => {
  const app = read('App.vue')
  assert.match(app, /<ChatOnlyView\s+v-else-if="ready && chatOnly"\s+:session-id="route\.sessionId"/)
  assert.match(app, /<MainLayout\s+v-else-if="ready"/)
  assert.doesNotMatch(app, /DslStepOnlyView|parseDslStepRoute|dsl-step/, 'DSL 新窗口改复用主 chat 多窗口，只读路由已移除')
})

test('SessionChat：DSL 作业 → DslStepsPanel（步骤表格），否则任务详情 / 会话', () => {
  const v = read('views/tasks/SessionChat.vue')
  assert.match(v, /<DslStepsPanel\s+v-if="dslJobId"\s+:key="dslJobId"\s+:job-id="dslJobId"\s*\/>/,
    'DSL 作业节点 → 右侧渲染步骤表格（:key 换作业即重挂载，无需 watch）')
  assert.match(v, /const dslJobId = computed\(\(\) => \(taskId\.value && isJobNode\(taskId\.value\) \? taskId\.value : ''\)\)/)
  assert.match(v, /<MessageList[^>]*direction="bottom"/, '既有子会话只读分支保留')
})

test('DslStepsPanel：列 No/状态/目的/耗时/时间 + 操作列（查看/新窗口/取消）+ preview 只读', () => {
  const v = read('views/dsl/DslStepsPanel.vue')
  for (const key of ['dsl_col_no', 'dsl_col_status', 'dsl_col_purpose', 'dsl_col_elapsed', 'dsl_col_time', 'dsl_col_actions']) {
    assert.match(v, new RegExp(`taskView\\.${key}`), `缺列 ${key}`)
  }
  for (const key of ['dsl_action_view', 'dsl_action_new_window', 'dsl_action_cancel']) {
    assert.match(v, new RegExp(`taskView\\.${key}`), `缺操作 ${key}`)
  }
  assert.match(v, /openSessionChatWindow\(row\.sessionId\)/, '新窗口 = 复用主 chat 多窗口（gui.window.open-chat）')
  assert.doesNotMatch(v, /window\.open\(/, '不再自开页签（复用主 chat 多窗口）')
  assert.match(v, /cancelJob\(props\.jobId\)/, '取消 = 作业级（单一动作）')
  assert.match(v, /<MessageList[\s\S]*?:viewer="true"[\s\S]*?direction="bottom"/, 'preview 区只读显示该轮 chat')
})

test('useDslView：取消复用既有 task-stop（按 tool_call_id 归一到持 exec 的工具节点），不新增 MQ 主题', () => {
  const c = read('composables/useDslView.js')
  // 主路径：按 tool_call_id 取消（服务端 onTaskStop 归一 → 持 exec 的 dsl_run 工具节点 → CancelExec 杀进程）
  assert.match(c, /mq\.emit\(EventNames\.taskStop,\s*\{\s*tool_call_id:\s*callId,\s*cascade:\s*'all'\s*\}\)/)
  // 回落：无 tool_call_id → task_id（作业级级联，行为不变）
  assert.match(c, /mq\.emit\(EventNames\.taskStop,\s*\{\s*task_id:\s*jobId,\s*cascade:\s*'all'\s*\}\)/)
  assert.match(c, /const callId = \(n && n\.tool_call_id\) \|\| ''/, '取节点 tool_call_id')
  assert.doesNotMatch(c, /mq\.emit\(\s*'data-dsl/, '复用既有 tasktree 面，不自造新主题')
})

test('SessionTreeNode：折叠容器进度徽标（LOOP/PARALLEL），节点不随迭代增长', () => {
  const v = read('views/tasks/SessionTreeNode.vue')
  assert.match(v, /v-if="progressLabel"\s+class="node-progress"/, '容器须渲染进度徽标')
  assert.match(v, /containerProgress\(props\.node\)/, '进度徽标数据 = containerProgress')
})

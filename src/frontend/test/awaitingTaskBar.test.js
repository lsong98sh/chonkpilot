/**
 * 任务区「待裁决裁决条」守卫（I-103）——「刷新/切会话后无卡片也能裁决」的等价断言。
 *
 * 断层（已核实）：前端工具卡来自 `role=tool` 结果行（`chonkpilot-data/persist/view.go`），而到达
 * 超时点待裁决（层 `state=awaiting`）时**工具仍在飞、尚无结果行** → 刷新/切会话后无卡可挂
 * （`MessageList.restoreAwaitingArbitration` 恒 `missing`）→ 裁决条消失。本批在**任务区**
 * （`views/tasks/SessionTreeNode.vue`）**直接按 `awaiting` 渲染裁决条**，不依赖消息卡。
 *
 * 前端暂无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→ 分两层：
 *   ① **纯逻辑行为断言**：`composables/useAwaitingArbitration.js`（不依赖 Vue，可直接 import）；
 *   ② **源码接线守卫**：锁定「数据来源 ↔ 渲染条件 ↔ 三个动作的既有事件/载荷」三者对齐。
 *
 * 注：本文件放在 src 之外（frontend/test/），以免 node 内置模块导入进入 tsconfig 对 src 下
 * js/vue 的类型检查范围。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { resolveAwaiting, awaitingActionsFor } from '../src/composables/useAwaitingArbitration.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const readSrc = (rel) => readFileSync(join(srcDir, rel), 'utf8')

// ── ① 纯逻辑：待裁决判定 ────────────────────────────────────────────

test('state=awaiting 且有 options → 取到待裁决明细（渲染判据）', () => {
  // 展示态由 useTaskStatus.resolveTaskState 给出（层 frontVisibleStatus 把 awaiting 塌缩为 running，
  // 该函数已回读层权威 state 细化 —— 此处直接传细化后的展示态）
  const node = { status: 'running', state: 'awaiting' }
  const runtime = { awaiting: { reason: 'timeout', timeout_s: 30, options: ['detach', 'cancel'] } }
  const aw = resolveAwaiting('awaiting', node, runtime)
  assert.ok(aw, 'awaiting 态 + 非空 options 必须返回明细')
  assert.equal(aw.timeout_s, 30)
})

test('无 awaiting → 不渲染（非 awaiting 态 / 缺 options / 空 options / 终态）', () => {
  // 非 awaiting 展示态
  assert.equal(resolveAwaiting('running', { status: 'running', state: 'running' }, {}), null)
  // 缺 awaiting 字段
  assert.equal(resolveAwaiting('awaiting', { status: 'running' }, {}), null)
  // options 缺失 / 空数组 → 无可用裁决项 → 不渲染
  assert.equal(resolveAwaiting('awaiting', { status: 'running' }, { awaiting: {} }), null)
  assert.equal(resolveAwaiting('awaiting', { status: 'running' }, { awaiting: { options: [] } }), null)
  // 已终态（层状态已推进 → 展示态非 awaiting）→ 即使残留 awaiting 快照也不渲染
  assert.equal(
    resolveAwaiting('cancelled', { status: 'cancelled' }, { awaiting: { options: ['cancel'] } }),
    null
  )
})

test('动作可用项按 options 决定：never=[wait,cancel] / manual=[detach,cancel]', () => {
  assert.deepEqual(awaitingActionsFor({ options: ['wait', 'cancel'] }, true), {
    wait: true, detach: false, cancel: true,
  })
  assert.deepEqual(awaitingActionsFor({ options: ['detach', 'cancel'] }, true), {
    wait: false, detach: true, cancel: true,
  })
})

test('转后台需 tool_call_id：缺失则不可用（载荷硬要求 {tool_call_id}）', () => {
  assert.equal(awaitingActionsFor({ options: ['detach', 'cancel'] }, false).detach, false)
})

// ── ② 源码接线守卫 ────────────────────────────────────────────────

test('渲染条件：SessionTreeNode 以 awaiting 存在为唯一判据（无 awaiting 不渲染）', () => {
  const vue = readSrc('views/tasks/SessionTreeNode.vue')
  assert.match(vue, /v-if="awaiting"\s+class="node-awaiting"/, '裁决条显示条件 = awaiting 存在')
  // 新口径（[42 §2 (126)]）：已关闭节点只读 → 不渲染裁决条；非关闭仍由 resolveAwaiting 求得
  assert.match(vue, /const awaiting = computed\(\(\) =>\s*\n\s*isClosed\.value \? null : resolveAwaiting\(taskState\.value, props\.node, runtime\.value\)\)/,
    'awaiting 须由 resolveAwaiting(展示态, 节点, 运行态) 求得，且已关闭节点不渲染')
  assert.match(vue, /const actions = computed\(\(\) => awaitingActionsFor\(awaiting\.value/,
    '按钮可用项须走 awaitingActionsFor(awaiting, hasToolCallId)')
  assert.match(vue, /v-if="actions\.wait"/, '等待按钮按 actions.wait')
  assert.match(vue, /v-if="actions\.detach"/, '转后台按钮按 actions.detach')
  assert.match(vue, /v-if="actions\.cancel"/, '取消按钮按 actions.cancel')
})

test('数据来源：SessionTree 拉 data-tasktree-tasks（awaiting 随 refresh 入 tasks 快照）', () => {
  const vue = readSrc('views/tasks/SessionTree.vue')
  assert.match(vue, /const \{ loadTree, refresh, nodeTree, nodeTimeOf \} = useTaskView\(\)/,
    'SessionTree 须取 refresh')
  assert.match(vue, /await Promise\.all\(\[loadTree\(topId, 'full'\), refresh\('', topId\)\]\)/,
    '载入任务树须并行刷新任务快照（awaiting 来源 = data-tasktree-tasks，I-99）')
  // 运行态读取（awaiting 明细载体）
  const node = readSrc('views/tasks/SessionTreeNode.vue')
  assert.match(node, /nodeRuntime\(props\.node\.node_id\)/, '运行态明细须经 nodeRuntime 取')
})

test('取消动作 → 发出 task-stop{task_id}（与任务树 ▍ 停止同一入口）', () => {
  const vue = readSrc('views/tasks/SessionTreeNode.vue')
  const m = vue.match(/async function onAwaitCancel\s*\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(m, '未找到 onAwaitCancel')
  assert.match(m[0], /mq\.emit\(EventNames\.taskStop,\s*\{\s*task_id:/,
    '取消须发 task-stop 且载荷含 task_id（server 任务节点 id）')
  assert.match(m[0], /clearAwaiting\(tid\)/, '取消成功后须清本地 awaiting（即时消隐，避免残留）')
})

test('转后台动作 → 发出既有 task-background{tool_call_id}（不新增 MQ 主题）', () => {
  const vue = readSrc('views/tasks/SessionTreeNode.vue')
  const m = vue.match(/async function onAwaitDetach\s*\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(m, '未找到 onAwaitDetach')
  assert.match(m[0], /mq\.emit\(MsgTopics\.taskBackground,\s*\{\s*\[FieldKeys\.tool_call_id\]:\s*callId\s*\}/,
    '转后台须发既有 task-background{tool_call_id}（与工具行同动作）')
  assert.match(vue, /EventNames\.toolsWait,\s*\{\s*\[FieldKeys\.tool_call_id\]:\s*callId\s*\}/,
    '等待须发既有 mcp-tools-wait{tool_call_id}')
})

test('去重：同一 tool_call_id 已有卡片 → 卡片沿用仲裁条，任务区节点条让位（不重复渲染）', () => {
  // 卡片渲染判据 = message.arbitration（MessageItem）；节点渲染判据 = awaiting（SessionTreeNode）。
  // 去重接线 = MessageList 把裁决挂到卡片（tryAttachArbitration 命中）时清节点本地 awaiting →
  // 「卡片存在时沿用卡片，卡片不存在时才由节点渲染」。
  const list = readSrc('views/chat/MessageList.vue')
  const m = list.match(/function tryAttachArbitration\(arb, callId, tid\)\s*\{[\s\S]*?\n\}/)
  assert.ok(m, '未找到 tryAttachArbitration')
  assert.match(m[0], /msg\.arbitration = arb/, '卡片存在时沿用卡片仲裁条')
  assert.match(m[0], /clearAwaiting\(msg\.task_id/, '挂卡成功后须清节点 awaiting（节点条让位）')
  // 卡片条与节点条各自的显示条件（二者不会同源出现两条）
  const item = readSrc('views/chat/MessageItem.vue')
  assert.match(item, /v-if="message\.arbitration"/, '卡片条显示条件 = message.arbitration')
})

test('useTaskView：导出 clearAwaiting，且确实清除 tasks/nodes 上的 awaiting', () => {
  const view = readSrc('composables/useTaskView.js')
  assert.match(view, /function clearAwaiting\(taskId\)/, '须定义 clearAwaiting')
  const m = view.match(/function clearAwaiting\(taskId\)\s*\{[\s\S]*?\n\}/)
  assert.match(m[0], /delete t\.awaiting/, '须清除 tasks 快照上的 awaiting')
  assert.match(m[0], /delete n\.awaiting/, '须清除 nodes 节点上的 awaiting')
  assert.match(view, /clearAwaiting,/, '须导出 clearAwaiting')
})

test('i18n：三条动作文案复用既有 chat.* 键（zh-CN + en-US 双语均在）', () => {
  for (const loc of ['zh-CN', 'en-US']) {
    const j = JSON.parse(readSrc('locales/' + loc + '/chat.json'))
    for (const k of ['timeout_arbitrate', 'timeout_wait', 'timeout_detach', 'tool_stop']) {
      assert.ok(typeof j[k] === 'string' && j[k].length > 0, `${loc} 缺 chat.${k}`)
    }
  }
})

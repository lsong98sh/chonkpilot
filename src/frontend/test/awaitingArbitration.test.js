/**
 * 超时信号恢复接线守卫（I-99；2026-11-15 manual 新语义）——
 * 「切会话/刷新后按任务列表 `awaiting` 恢复可转异步入口」的等价断言。
 *
 * manual 新语义：阈值到达后 gateway **继续同步等待**，只发
 * `mcp-tools-timeout{mode:"manual", blocking:true, options:[detach,cancel]}`；
 * 前端把它挂成 `message.manualThreshold`（高亮转异步图标），**不再挂 awaiting 裁决条**。
 * 旧版遗留的层 `awaiting` 任务仍按「阈值已到」恢复（不恢复裁决条）。
 *
 * 前端暂无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→ 用**源码守卫**
 * 锁定「事件字段 ↔ 前端读法 ↔ 渲染条件」三者对齐，防回归到「视图只产出 state/exec_json、
 * 前端仍读 t.awaiting → 刷新/切会话入口永不恢复」。覆盖：
 *   ① 视图契约：persist 侧 `data-tasktree-tasks` 增补 `awaiting` 对象（含 options/timeout_s）；
 *   ② 前端读法：MessageList.restoreAwaitingArbitration 读 `t.awaiting.options` / `t.awaiting.timeout_s`；
 *   ③ 无该字段不渲染：`if (!t || !t.awaiting) continue` 短路；
 *   ④ 有该字段渲染：tryAttachThreshold 命中卡片即 `msg.manualThreshold = thr`；
 *   ⑤ 渲染条件：MessageItem 阈值高亮 = `message.manualThreshold`；停止按钮 = `canCancel`。
 *
 * 注：本文件放在 src 之外（frontend/test/），以免 node 内置模块导入进入 tsconfig 对 src 下
 * js/vue 的类型检查范围。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { resolveSrc } from './support/feSource.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoRoot = join(here, '..', '..')
const readSrc = (rel) => readFileSync(resolveSrc(rel), 'utf8')

test('视图契约：data-tasktree-tasks 增补 awaiting{options,timeout_s}（persist 侧）', () => {
  // 阶段 4 门面化第四批（41 G-36）：视图构造在 tasktree 域实现，
  // 载荷输出在 facade/wire/domains.go（消息面形状）；awaiting 明细解析在同域实现。
  // 阶段 4 internal 下沉：域实现已由 persist 下沉 internal/tasktree（信封在 persist/envelope.go）。
  const impl = readFileSync(join(repoRoot, 'lib', 'data', 'internal', 'tasktree', 'tasktree.go'), 'utf8')
  assert.match(impl, /item\.Awaiting\s*=\s*awaitingView\(/, 'tasks 视图须在 awaiting 态补挂 awaiting 明细')
  const wireSrc = readFileSync(join(repoRoot, 'lib', 'data', 'facade', 'wire', 'domains.go'), 'utf8')
  assert.match(wireSrc, /row\["awaiting"\]\s*=\s*t\.Awaiting/, 'tasks 载荷须在 awaiting 非 nil 时输出 awaiting 对象')
  const go = readFileSync(join(repoRoot, 'lib', 'data', 'internal', 'tasktree', 'tasktree.go'), 'utf8')
  assert.match(go, /"options":\s*opts/, 'awaiting 对象须含 options')
  assert.match(go, /aw\["timeout_s"\]\s*=\s*ts/, 'awaiting 对象须含 timeout_s')
})

test('前端读法：MessageList.restoreAwaitingArbitration 按阈值恢复（不挂裁决条）', () => {
  const vue = readSrc('views/chat/MessageList.vue')
  const m = vue.match(/async function restoreAwaitingArbitration\s*\([\s\S]*?\n\}/)
  assert.ok(m, '未找到 restoreAwaitingArbitration')
  const body = m[0]
  // ③ 无 awaiting 字段 → 跳过（不发生任何恢复）
  assert.match(body, /if\s*\(\s*!t\s*\|\|\s*!t\.awaiting\s*\)\s*continue/, '无 t.awaiting 须 continue（不渲染）')
  // ② 读 t.awaiting.options（数组）/ t.awaiting.timeout_s（字段名与视图对齐）
  assert.match(body, /Array\.isArray\(t\.awaiting\.options\)/, 'options 须取自 t.awaiting.options')
  assert.match(body, /timeout_s:\s*t\.awaiting\.timeout_s/, 'timeout_s 须取自 t.awaiting.timeout_s')
  // ④ 旧 awaiting 任务恢复为「阈值已到」提示，不再恢复裁决条
  assert.match(body, /tryAttachThreshold\(threshold,\s*callId,\s*tid\)/, 'awaiting 应恢复为阈值提示（tryAttachThreshold）')
  assert.doesNotMatch(body, /tryAttachArbitration\(arb,\s*callId,\s*tid\)/, 'manual 新语义不得恢复 awaiting 裁决条')
  // ⑤ 渲染判据 = message.manualThreshold
  assert.match(vue, /msg\.manualThreshold\s*=\s*thr/, '补挂须写 message.manualThreshold（渲染判据）')
})

test('阈值事件：blocking=true 只挂 manualThreshold，不挂 arbitration', () => {
  const vue = readSrc('views/chat/MessageList.vue')
  const m = vue.match(/function onToolTimeout\s*\(d\)\s*\{[\s\S]*?\n\}/)
  assert.ok(m, '未找到 onToolTimeout')
  const body = m[0]
  assert.match(body, /d\.blocking === true/, 'manual 阈值须按 blocking 识别')
  assert.match(body, /tryAttachThreshold\(threshold,\s*callId,\s*tid\)/, 'manual 阈值须走阈值挂载')
  assert.match(body, /pendingArbitration\.set\(key,\s*\{\s*kind:\s*'threshold'/, '未命中卡片须缓存 threshold 类型')
  assert.match(vue, /msg\.manualThreshold\s*=\s*thr/, '阈值挂载写 message.manualThreshold')
})

test('渲染条件：legacy 裁决提示仍以 message.arbitration 为判据；停止按钮改为所有在飞工具', () => {
  const item = readSrc('views/chat/MessageItem.vue')
  // 历史 awaiting（升级前遗留）仍可渲染提示条
  assert.match(item, /v-if="message\.arbitration"\s+class="arbitration-hint"/, 'legacy 裁决提示 = message.arbitration 存在')
  // 停止按钮不再只限裁决态：canCancel = pending/running 且有 id（或 legacy arbitration）
  assert.match(item, /v-if="canCancel"[\s\S]{0,300}@click\.stop="arbitrationCancel"/, '停止按钮判据 = canCancel')
  assert.match(item, /const canCancel = computed\(\(\) => \{[\s\S]*?s === 'pending' \|\| s === 'running'[\s\S]*?task_id \|\| props\.message\.tool_call_id/)
  // 阈值高亮：message.manualThreshold
  assert.match(item, /'threshold-hit': !!message\.manualThreshold/, '转异步图标须按 manualThreshold 高亮')
})

test('透传：useTaskView.refresh 拉 tasks 快照并 upsert（awaiting 原样进 taskMap）', () => {
  const view = readSrc('composables/useTaskView.js')
  const m = view.match(/async function refresh\s*\([\s\S]*?\n\}/)
  assert.ok(m, '未找到 refresh')
  assert.match(m[0], /tasktreeReq\('tasks'/, 'refresh 走 data-tasktree-tasks')
  assert.match(m[0], /upsert\(t\)/, '列表项须 upsert 进 tasks（awaiting 随 payload 透传）')
  assert.match(view, /tasks\.set\(payload\.task_id,\s*normalize\(\{\s*\.\.\.prev,\s*\.\.\.payload\s*\}\)\)/,
    'upsert 须保留 payload 字段（awaiting 不被丢弃）')
})

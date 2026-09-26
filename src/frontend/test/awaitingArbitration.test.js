/**
 * 待裁决恢复接线守卫（I-99）——「切会话/刷新后按任务列表 `awaiting` 补挂裁决条」的等价断言。
 *
 * 前端暂无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→ 用**源码守卫**
 * 锁定「视图字段 ↔ 前端读法 ↔ 渲染条件」三者对齐，防回归到「视图只产出 state/exec_json、
 * 前端仍读 t.awaiting → 刷新/切会话裁决条永不补挂」。覆盖：
 *   ① 视图契约：persist 侧 `data-tasktree-tasks` 增补 `awaiting` 对象（含 options/timeout_s）；
 *   ② 前端读法：MessageList.restoreAwaitingArbitration 读 `t.awaiting.options` / `t.awaiting.timeout_s`；
 *   ③ 无该字段不渲染：`if (!t || !t.awaiting) continue` 短路；
 *   ④ 有该字段渲染：tryAttachArbitration 命中卡片即 `msg.arbitration = arb`；
 *   ⑤ 渲染条件：MessageItem 以 `message.arbitration` 存在为唯一判据显示裁决条。
 *
 * 注：本文件放在 src 之外（frontend/test/），以免 node 内置模块导入进入 tsconfig 对 src 下
 * js/vue 的类型检查范围。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoRoot = join(here, '..', '..')
const readSrc = (rel) => readFileSync(join(srcDir, rel), 'utf8')

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

test('前端读法 + 渲染判据：MessageList.restoreAwaitingArbitration', () => {
  const vue = readSrc('views/chat/MessageList.vue')
  const m = vue.match(/async function restoreAwaitingArbitration\s*\([\s\S]*?\n\}/)
  assert.ok(m, '未找到 restoreAwaitingArbitration')
  const body = m[0]
  // ③ 无 awaiting 字段 → 跳过（不渲染裁决条）
  assert.match(body, /if\s*\(\s*!t\s*\|\|\s*!t\.awaiting\s*\)\s*continue/, '无 t.awaiting 须 continue（不渲染）')
  // ② 读 t.awaiting.options（数组）/ t.awaiting.timeout_s（字段名与视图对齐）
  assert.match(body, /Array\.isArray\(t\.awaiting\.options\)/, 'options 须取自 t.awaiting.options')
  assert.match(body, /timeout_s:\s*t\.awaiting\.timeout_s/, 'timeout_s 须取自 t.awaiting.timeout_s')
  // ④/⑤ 有 awaiting → 补挂（tryAttachArbitration 命中卡片即渲染裁决条）
  assert.match(body, /tryAttachArbitration\(arb,\s*callId,\s*tid\)/, '有 awaiting 须走 tryAttachArbitration 补挂')
  assert.match(vue, /msg\.arbitration\s*=\s*arb/, '补挂须写 message.arbitration（渲染判据）')
})

test('渲染条件：MessageItem 以 message.arbitration 存在为唯一判据', () => {
  const item = readSrc('views/chat/MessageItem.vue')
  assert.match(item, /v-if="message\.arbitration"\s+class="arbitration-hint"/, '裁决条显示条件 = message.arbitration 存在')
  assert.match(item, /v-if="message\.arbitration"[\s\S]{0,300}@click\.stop="arbitrationCancel"/, '裁决按钮同以 message.arbitration 为判据')
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

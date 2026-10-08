/**
 * 消息渲染时点守卫（T5 方案 B，2026-09-24）——「user 气泡落库回执后渲染 + 「加载中」占位移出 messages」。
 *
 * 背景（[42 §2 (148)] ③）：`MessageList.onSessionChanged → loadMessages` **整体替换** `messages`
 * → 会冲掉「尚未落库的乐观气泡」（实跑 CF1 `bubble='' cancel=True`）。方案 B：
 *   ① user 气泡**不再乐观插入** —— 等 `llm-start` 的 `/publish` 应答（= 桥同步受理并落库
 *      session/turn 的「落库回执」）到达后再渲染；
 *   ② 「加载中」占位移出 `messages` 数组（独立 `pending` 状态）→ 历史全量替换不再影响它。
 *
 * 前端暂无组件级测试运行器（`npm test` = node:test 直跑）→ 以**源码守卫**锁定时序与状态归属。覆盖：
 *   ① sessionMessages：`pending` 为独立 ref（不在 messages 内）；`loadMessages` 不写 `pending`；
 *      真实内容/终态一律 `hidePending()`；`pushPending/removePending/resolvePending/findPending` 已清除；
 *   ② MessageList：`doSend` / `sendSameTurnContinue` 为 async，`await ack` **之后**才 `pushUserBubble`；
 *      占位在 `v-for` **之外**按 `v-if="pending"` 渲染；`turnStarts` 不再含 `!m.pending` 死判据；
 *   ③ api/chat：`publishLLMStart` / `publishLLMContinue` 返回 `{ turn, ack }`。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const readSrc = (rel) => readFileSync(join(srcDir, rel), 'utf8')

function fnBody(src, header) {
  const i = src.indexOf(header)
  assert.ok(i >= 0, `未找到 ${header}`)
  const start = src.indexOf('{', i)
  assert.ok(start >= 0, `未找到 ${header} 的函数体起点`)
  let depth = 0
  for (let j = start; j < src.length; j++) {
    const ch = src[j]
    if (ch === '{') depth++
    else if (ch === '}') {
      depth--
      if (depth === 0) return src.slice(start, j + 1)
    }
  }
  assert.fail(`未解析 ${header} 函数体`)
}

test('① sessionMessages：「加载中」占位是独立状态，loadMessages 不碰它', () => {
  const src = readSrc('utils/sessionMessages.js')
  assert.match(src, /const pending = ref\(false\)/, 'pending 须为独立 ref')
  assert.match(src, /const pendingStatus = ref\(/, 'pendingStatus 须为独立 ref')
  const load = fnBody(src, 'async function loadMessages(sessionId)')
  assert.doesNotMatch(load, /pending/, 'loadMessages（历史整体替换）不得改动 pending')
  assert.match(load, /messages\.value = res\.messages\.map\(dbMsgToView\)/, '历史回填仍整体替换 messages')
  // 真实内容 / 终态一律收起占位
  assert.match(fnBody(src, 'function handleToken(data)'), /hidePending\(\)/, 'handleToken 须收起占位')
  assert.match(fnBody(src, 'function handleDone()'), /hidePending\(\)/, 'handleDone 须收起占位')
  assert.match(fnBody(src, 'function handleError(data)'), /hidePending\(\)/, 'handleError 须收起占位')
  // 旧「占位气泡进 messages」实现须彻底移除（不留死代码）
  for (const gone of ['pushPending', 'removePending', 'resolvePending', 'findPending']) {
    assert.ok(!src.includes(gone), `旧实现 ${gone} 须已删除`)
  }
  // 导出面
  assert.match(src, /^\s*pending,\r?\n\s*pendingStatus,/m, '须导出 pending/pendingStatus')
  assert.match(src, /hidePending,\r?\n\s*\}/, '须导出 hidePending')
})

test('② MessageList：user 气泡在「落库回执」之后渲染（不再乐观插入）', () => {
  const vue = readSrc('views/chat/MessageList.vue')
  for (const [hdr, tail] of [['async function doSend(batch)', 'doSend'], ['async function sendSameTurnContinue()', 'sendSameTurnContinue']]) {
    const body = fnBody(vue, hdr)
    const iAck = body.indexOf('await ack')
    const iPush = body.indexOf('pushUserBubble(')
    assert.ok(iAck >= 0, `${tail} 须 await 落库回执`)
    assert.ok(iPush > iAck, `${tail} 须在 await ack **之后**才渲染 user 气泡`)
  }
  // 占位在 messages 渲染列表之外（v-for 之后单独一行，v-if="pending"）
  const list = vue.match(/v-for="\(msg, i\) in messages"[\s\S]*?\/>\r?\n\s*<!--[\s\S]*?-->\r?\n\s*<MessageItem\r?\n\s*v-if="pending"/)
  assert.ok(list, '占位须在 messages 的 v-for 之外按 v-if="pending" 独立渲染')
  // DOM 契约不变（既有用例/探针按此断言）
  assert.match(vue, /:key="'pending'"/, '占位行 key 须稳定为 pending')
  assert.match(vue, /pendingStatus: pendingStatus\.value/, '占位状态须来自独立 pendingStatus')
  // 死判据清除：占位已不在 messages，turnStarts / retryText 不再需要 m.pending
  assert.doesNotMatch(vue, /!m\.pending/, 'turnStarts 的 !m.pending 死判据须移除')
  assert.doesNotMatch(vue, /\|\| m\.pending/, 'retryText 的 m.pending 死判据须移除')
})

test('③ api/chat：llm-start 发布返回 { turn, ack }（ack = /publish 应答 promise）', () => {
  const src = readSrc('api/chat.js')
  assert.match(src, /const ack = mq\.emit\(EventNames\.llmStart, \{/, 'ack 须为 llm-start 的 mq.emit promise')
  assert.match(src, /return \{ turn, ack \}/, 'publishLLMStart 须返回 { turn, ack }')
  assert.match(src, /return \{ turn: turnId \|\| '', ack \}/, 'publishLLMContinue 须返回 { turn, ack }')
})

test('④ sessionMessages：加载带代次守卫（切换会话丢弃过期批次，不污染当前会话）', () => {
  const src = readSrc('utils/sessionMessages.js')
  assert.match(src, /let loadGen = 0/, '须有加载代次计数 loadGen')
  // 首屏加载：发请求前推进代次；await 后校验，过期即丢弃（不写任何状态）
  const load = fnBody(src, 'async function loadMessages(sessionId)')
  assert.match(load, /const gen = \+\+loadGen/, 'loadMessages 须在发起请求前推进代次')
  assert.match(load, /if \(gen !== loadGen\) return/, 'loadMessages 须在 await 后校验代次并丢弃过期批次')
  // 加载更多：快照当前代次；await 后校验，过期即丢弃
  const more = fnBody(src, 'async function loadMoreMessages()')
  assert.match(more, /const gen = loadGen/, 'loadMoreMessages 须快照当前代次')
  assert.match(more, /if \(gen !== loadGen\) return/, 'loadMoreMessages 须在 await 后校验代次并丢弃过期批次')
  // teardown：推进代次使在途加载全部失效
  assert.match(fnBody(src, 'function teardown()'), /loadGen\+\+/, 'teardown 须推进代次使在途加载失效')
})

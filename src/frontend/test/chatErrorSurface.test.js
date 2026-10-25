/**
 * ERRUX-1（错误呈现去重 + 下方提示条增强）+ RETRY-1（可见重试）前端契约守卫（2026-10-17）。
 *
 * 用户口径：
 *   ① 错误**只在下方提示条**显示（聊天流内不再渲染错误气泡；两处重复是实测反馈）；
 *   ② 提示条须含「查看错误信息」（展开原始错误）+ retry 次数；
 *   ③ 自动重试可见（「正在重试 n/N（等待 s 秒）」），成功不残留，终态才出错误。
 *
 * 前端无组件级运行器（`npm test` = node:test 直跑）→ 与 uxBatch2 同法：读源码断言 + 真实 i18n 键校验。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

test('ERRUX-1①：聊天流内不再渲染错误气泡（消息数据仍保留在数据流）', () => {
  const item = read('views/chat/MessageItem.vue')
  assert.doesNotMatch(item, /class="[^"]*error-bubble/, 'MessageItem 不得再渲染 .error-bubble')
  assert.doesNotMatch(item, /class="[^"]*error-text/, 'MessageItem 不得再渲染 .error-text')
  assert.match(item, /v-else-if="message\.type === 'error'"><\/template>/,
    'type=error 分支须保留（数据流）但**不渲染内容**')
  // 头像/时间戳也不得为错误行渲染（否则留下无内容的"Chonk + 时间"空行）
  assert.match(item, /message\.type !== 'error' && message\.type !== 'user-notify'/, '错误行须跳过头像/时间戳')
  // 数据流保留：handleError 仍 push type:'error'（复制/历史/回放/诊断需要）
  const sm = read('utils/sessionMessages.js')
  const fn = sm.match(/function handleError\(data\)\s*\{[\s\S]*?\n  \}/)
  assert.ok(fn, '未找到 handleError')
  assert.match(fn[0], /type: 'error'/, '错误消息仍在数据流（只是不渲染）')
  assert.match(fn[0], /content: cls\.detail/, '原始错误串保留（诊断）')
})

test('ERRUX-1②：下方提示条 = 摘要 + 查看错误信息 + retry 次数', () => {
  const src = read('views/chat/ChatPanel.vue')
  const bar = src.match(/<div v-if="llmFail" class="llm-fail-hint llm-error-hint">[\s\S]*?<\/div>/)
  assert.ok(bar, '未找到终态错误提示条（.llm-fail-hint.llm-error-hint）')
  assert.match(bar[0], /\$t\(llmFail\.key, llmFail\.params\)|llmFail\.summary/, '须显示人话错误摘要')
  assert.match(bar[0], /chat\.retry_count_label/, '须显示 retry 次数（已重试 n/N 次）')
  assert.match(bar[0], /chat\.error_detail_toggle/, '须有「查看错误信息」展开入口')
  assert.match(bar[0], /chat\.error_detail_hide/, '展开后须可收起')
  assert.match(bar[0], /v-if="llmFailDetailOpen"/, '原始错误须按需展开')
  assert.match(bar[0], /llmFail\.raw/, '展开内容 = 原始错误全文')
  // 摘要来源 = 既有分类映射（不新增错误来源主题）
  assert.match(src, /import \{ classifyError \} from '\.\.\/\.\.\/utils\/errorMessage'/, '须复用既有 classifyError')
  assert.match(src, /classifyError\(\{ message: d\.message, code: d\.code \}\)/, '终态 message+code 一并分类')
})

test('RETRY-1③：订阅重试中间态并区分终态（llm-retry / llm-complete）', () => {
  const src = read('views/chat/ChatPanel.vue')
  const sub = src.match(/mq\.on\(EventNames\.llmRetry,[\s\S]*?\n  \}\)/)
  assert.ok(sub, '未订阅 llm-retry（session-retry → 前端 llm-retry）')
  assert.match(sub[0], /data\.retry_attempt/, '中间态须读 retry_attempt')
  assert.match(sub[0], /data\.retry_count/, '中间态须读 retry_count')
  assert.match(sub[0], /data\.retry_wait_ms/, '等待值须取 retry_wait_ms（不再用 wait_seconds）')
  assert.match(sub[0], /chat\.llm_retry/, '须使用既有 chat.llm_retry 文案（含等待秒数）')
  assert.match(sub[0], /startRetryCountdown\(attempt, maxRetries, waitSec\)/, '中间态写入提示条（经倒计时函数，不落聊天流）')
  // 提示条写入点：renderRetryText 内以 chat.llm_retry 渲染（attempt/max/wait），倒计时逐秒调用它
  const renderFn = src.match(/function renderRetryText\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(renderFn, '未找到 renderRetryText')
  assert.match(renderFn[0], /retryText\.value = t\('chat\.llm_retry'/, '提示条文案须经 chat.llm_retry 渲染（不落聊天流）')
  const countdownFn = src.match(/function startRetryCountdown\([\s\S]*?\n\}/)
  assert.ok(countdownFn, '未找到 startRetryCountdown')
  assert.match(countdownFn[0], /renderRetryText\(\)/, '倒计时须逐秒刷新提示条')
  // 终态：llm-complete 清进度（成功不残留）+ error 才置错误条
  const done = src.match(/function onLlmCompleteHint\(d\)\s*\{[\s\S]*?\n\}/)
  assert.ok(done, '未找到 onLlmCompleteHint')
  assert.match(done[0], /retryText\.value = ''/, '终态到达须清空重试进度（成功不残留）')
  assert.match(done[0], /d\.status !== 'error'/, '成功/取消 → 清空错误提示')
  assert.match(done[0], /retry_attempt/, '终态须携带真实 retry 计数')
  assert.match(done[0], /retry_count/, '终态须携带允许的最大次数')
  // 前端不再对可重试错误静默续写（否则重试仍不可见）
  const list = read('views/chat/MessageList.vue')
  assert.match(list, /const autoRetry = unfinished && !wasError/, '错误终态不得再自动续写（改由后端可见重试承担）')
})

test('RETRY-1/ERRUX-1：i18n zh/en 齐备（文案含重试次数/等待秒数/详情）', () => {
  for (const loc of LOCALES) {
    const chat = readLocale(loc, 'chat.json')
    for (const k of ['llm_retry', 'retry_count_label', 'error_detail_toggle', 'error_detail_hide']) {
      assert.ok(typeof chat[k] === 'string' && chat[k].trim().length > 0, `${loc} 缺 chat.${k}`)
    }
    for (const p of ['{attempt}', '{max}', '{wait}']) {
      assert.ok(chat.llm_retry.includes(p), `${loc} chat.llm_retry 缺占位符 ${p}`)
    }
    for (const p of ['{n}', '{max}']) {
      assert.ok(chat.retry_count_label.includes(p), `${loc} chat.retry_count_label 缺占位符 ${p}`)
    }
  }
  assert.match(readLocale('zh-CN', 'chat.json').llm_retry, /正在重试/, 'zh 文案须为「正在重试」')
  assert.match(readLocale('en-US', 'chat.json').llm_retry, /Retrying/i, 'en 文案须为 Retrying')
})

test('ERRUX-1/RETRY-1：改动文件无 watch/watchEffect（项目规则）', () => {
  for (const rel of ['views/chat/ChatPanel.vue', 'views/chat/MessageItem.vue', 'views/chat/MessageList.vue']) {
    assert.doesNotMatch(read(rel), /\bwatch(Effect)?\s*\(/, `${rel} 不得用 watch/watchEffect`)
  }
})

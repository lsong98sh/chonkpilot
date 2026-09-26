/**
 * UX 评审 P0-1 / P0-2 / P1-7 / P1-9 修复守卫（2026-09-26）。
 *
 * 前端无组件级运行器（`npm test` = node:test 直跑）→ `.vue` 源码 + CSS 规则守卫：
 *   P0-1 消息气泡/消息项加 max-width（min(760px, 100%)）且内容列居中 —— 修「1280 宽正文铺满整行」；
 *   P0-2 输入动作区允许换行（flex-wrap）—— 修「chat 列压到 280px 时控件溢出被裁」；
 *   P1-7 turn 导航少轮次不渲染 + 底色走主题 token —— 修「白占 22px + 硬编码 rgba(0,0,0,0.02)」；
 *   P1-9 chatOnly 下隐藏整条 panel-header —— 修「纯对话窗口多余标题条」（WIN-018）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

/** 取 `<style scoped>` 之后、指定选择器的 CSS 规则体（首个 `\n}` 截止）。 */
function cssBlock(src, selector) {
  const css = src.slice(src.indexOf('<style scoped>'))
  const re = new RegExp(selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = css.match(re)
  return m ? m[1] : null
}

// ═══════════════════════════════════════════════════════════════
// P0-1 消息气泡/消息项 max-width + 内容列居中
// ═══════════════════════════════════════════════════════════════
test('P0-1：消息项加 max-width 且内容列居中（不破坏用户/助手左右对齐）', () => {
  const src = read('views/chat/MessageItem.vue')
  const block = cssBlock(src, '.message-item')
  assert.ok(block, '未找到 .message-item 规则')
  assert.match(block, /max-width:\s*min\(760px,\s*100%\)/, '气泡/消息项须有 max-width: min(760px, 100%)')
  assert.match(block, /margin:\s*0 auto/, '内容列须居中（margin: 0 auto）')
  // 既有左右对齐语义必须保留
  assert.match(src, /\.message-item\.user,\s*\n\.message-item\.tool\s*\{\s*\n\s*align-items:\s*flex-end/,
    '用户/工具仍右对齐')
  assert.match(src, /\.message-item\.assistant\s*\{\s*\n\s*align-items:\s*flex-start/, '助手仍左对齐')
  assert.match(src, /\.message-item\.user-notify\s*\{\s*\n\s*align-items:\s*flex-start/, '通知仍左对齐')
})

// ═══════════════════════════════════════════════════════════════
// P0-2 输入动作区换行（280px 下不裁）
// ═══════════════════════════════════════════════════════════════
test('P0-2：输入动作区 flex-wrap + 右组不收缩（发送键始终可见）', () => {
  const src = read('views/chat/InputBox.vue')
  const actions = cssBlock(src, '.input-actions')
  assert.ok(actions, '未找到 .input-actions 规则')
  assert.match(actions, /flex-wrap:\s*wrap/, '.input-actions 须允许换行')
  assert.match(actions, /gap:\s*6px/, '.input-actions 须设组间 gap')
  const left = cssBlock(src, '.input-actions-left')
  assert.ok(left, '未找到 .input-actions-left 规则')
  assert.match(left, /flex-wrap:\s*wrap/, '左侧组须自身可换行')
  assert.match(left, /min-width:\s*0/, '左侧组须可收缩（min-width: 0）')
  const right = cssBlock(src, '.input-actions-right')
  assert.ok(right, '未找到 .input-actions-right 规则')
  assert.match(right, /margin-left:\s*auto/, '右组换行后仍靠右')
  assert.match(right, /flex-shrink:\s*0/, '右组（发送键）不被压缩')
})

// ═══════════════════════════════════════════════════════════════
// P1-7 turn 导航：阈值 + 主题 token 底色
// ═══════════════════════════════════════════════════════════════
test('P1-7：turn 导航轮次少时不渲染（不占宽/不画 border）', () => {
  const src = read('views/chat/MessageList.vue')
  assert.match(src, /const MIN_NAV_TURNS = 3/, '阈值常量 = 3（< 3 轮隐藏）')
  assert.match(src, /v-if="turnStarts\.length >= MIN_NAV_TURNS"/,
    'turn-nav 仅在轮次 ≥ MIN_NAV_TURNS 时渲染（不占宽、不画 border）')
  assert.doesNotMatch(src, /v-if="turnStarts\.length > 0"/, '旧的无条件（>0）渲染须移除')
  // 固定列语义保留（既有测试已锁 width/border，此处确认未被误删）
  const nav = cssBlock(src, '.turn-nav')
  assert.ok(nav, '未找到 .turn-nav 规则')
  assert.match(nav, /flex-shrink:\s*0/, '导航列仍为固定列')
})

test('P1-7：turn 导航底色走主题 token（不再硬编码 rgba）', () => {
  const src = read('views/chat/MessageList.vue')
  const nav = cssBlock(src, '.turn-nav')
  assert.ok(nav, '未找到 .turn-nav 规则')
  assert.match(nav, /background:\s*color-mix\(in srgb,\s*var\(--text-primary\) 2%,\s*transparent\)/,
    '底色须为主题 token 混色')
  assert.doesNotMatch(src, /rgba\(0, 0, 0, 0\.02\)/, '硬编码 rgba(0,0,0,0.02) 须移除')
})

// ═══════════════════════════════════════════════════════════════
// P1-9 纯对话窗口（chatOnly）保留「精简标题条」= 仅场景 Tag
// ═══════════════════════════════════════════════════════════════
test('P1-9：chatOnly 下标题条精简（仅保留场景 Tag，其余隐藏；无 watch）', () => {
  const src = read('views/chat/ChatPanel.vue')
  // 标题条两种形态共用外壳，chatOnly 加 panel-header-mini
  assert.match(src, /<div class="panel-header" :class="\{ 'panel-header-mini': chatOnly \}">/,
    '标题条须由 chatOnly 切到精简形态（用户裁决 c）')
  // 非场景部分（标题/会话号/新建会话/新建对话窗口/留白）包在 v-if="!chatOnly" 内
  assert.match(src, /<template v-if="!chatOnly">/, '非场景部分须随 chatOnly 隐藏')
  assert.match(src, /\.panel-header-mini\s*\{[\s\S]*?justify-content:\s*flex-start/,
    '精简标题条须左对齐（无 header-spacer 撑开）')
  // 场景入口（Popover/Tag）留在标题条内 → 两种形态都可见（chatOnly 的唯一切换入口）
  assert.match(src, /v-mq:\[EventNames\.scenarioSelect\]\.click/, '场景选择入口须保留')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '禁 watch/watchEffect（规则：状态用 composable / 事件订阅）')
})

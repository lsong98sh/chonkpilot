/**
 * AskUser Step 1 · 前端契约单测（S1-1 / S1-2 / S1-3 / S1-4 / S1-6，2026-10-29）。
 *
 * 口径：`docs/ask-user/AskUser专题设计方案.md` §4.1～§4.6 / §7 / §9；消息面以 61 为准。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 与 guiShowDialog / windowResizeHandles
 * 同法：**源级断言**（SFC / 模块源码契约）+ 纯函数单测（`dialogPosition.test.js`）。
 *
 * 覆盖：
 *   S1-1 主窗口才有「聊一聊」：卡片按钮受 `canChat` 控制；`canChat` 判据 = 既有视图分派（URL）；
 *   S1-2 点击 → 切会话（含 `gui.window.open-chat` 归属）+ defer 回填 + 问题引用入输入框（不发送）；
 *   S1-3/S1-4 `session-ask` / `ask-user-reply` 双向 `dialog_id`（传输 → 管理项 → 回带）；
 *   S1-6 ask 弹窗按**屏幕工作区**居中（DialogShell 开关 + 无位置持久化）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { EventNames } from '../src/events/event-names.js'
import { SessionAskKeys, AskUserReplyKeys, FieldKeys } from '../src/events/msgkeys.js'
import { readSrc } from './support/feSource.mjs'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..', 'src')
const read = (rel) => readSrc(rel)

const CONTENT = read('views/chat/AskUserContent.vue')
const DIALOG = read('views/chat/AskUserDialog.vue')
const MANAGER = read('utils/askUserManager.js')
const SHELL = read('components/dialog/DialogShell.vue')
const OPTIONS = read('types/dialog.js')
const ROUTE = read('utils/viewRoute.js')

// ── S1-1 主窗口才有「聊一聊」────────────────────────────────────────────

test('S1-1 卡片：按钮受 canChat 控制（对话窗口不渲染）+ 独立事件', () => {
  assert.match(CONTENT, /v-if="canChat"[\s\S]{0,200}class="chat-btn"/, '「聊一聊」须按 canChat 渲染')
  assert.match(CONTENT, /v-mq:\[EventNames\.askUserChat\]\.click="\{ uid \}"/, '须经既有事件化通道（v-mq）触发')
  assert.match(CONTENT, /canChat: \{ type: Boolean, default: false \}/, 'canChat 缺省 false（非主窗口不渲染）')
  assert.match(CONTENT, /defineEmits\(\['answer', 'busy', 'skip', 'cancel', 'chat'\]\)/, '须声明 chat 出口')
  assert.match(CONTENT, /mq\.on\(EventNames\.askUserChat/, '组件须订阅并按 uid 过滤 chat 事件')
})

test('S1-1 判定复用既有「是否主窗口」能力（视图分派 = URL 唯一判据，不新造窗口判定）', () => {
  assert.match(MANAGER, /import \{ parseViewRoute, VIEW_MAIN \} from '\.\/viewRoute\.js'/, '须复用 viewRoute')
  assert.match(MANAGER, /get canChat\(\)[\s\S]{0,400}parseViewRoute\(window\.location\.search, window\.location\.hash\)\.view === VIEW_MAIN/,
    'canChat 须等于「视图 = main」')
  assert.match(ROUTE, /export const VIEW_MAIN = 'main'/, 'viewRoute 须保留 main 视图标识')
  // 管理项把 canChat 透传给卡片（唯一来源，避免两处判定分叉）
  assert.match(MANAGER, /canChat: this\.canChat,/, '弹窗须透传 canChat')
})

test('S1-1 事件名登记：ask-user-chat（前端本地 UI 事件，非消息面）', () => {
  assert.equal(EventNames.askUserChat, 'ask-user-chat')
  assert.equal(EventNames.chatInsertText, 'chat-insert-text')
})

// ── S1-2 聊一聊：切会话 + defer 回填 + 问题引用 ─────────────────────────

test('S1-2 chatAbout：定位 (dialog, session) → 切会话 → defer 回填 → 问题引用入输入框', () => {
  assert.match(MANAGER, /async chatAbout\(item\)/, '须有 chatAbout 动作')
  // 仅主窗口
  assert.match(MANAGER, /async chatAbout\(item\) \{\s*\n\s*if \(!item \|\| !this\.canChat\) return/, 'chatAbout 须守住「仅主窗口」')
  // 会话存在性（不存在 / 已删除 → 可见提示，不炸）
  assert.match(MANAGER, /await getSession\(sid\)/, '须按会话定位（会话已删除 → 提示）')
  assert.match(MANAGER, /ask_user_chat_session_missing/, '会话缺失须给可见提示')
  // 切换会话（复用既有链路）
  assert.match(MANAGER, /await this\.revealSession\(sid\)/, '须先切到发起会话')
  // defer 回填（不把答案当最终作答；不新增字段，用既有 answer 表达）
  assert.match(MANAGER, /this\.submitAnswer\(item, t\('chat\.ask_user_chat_defer_answer'\)\)/, 'defer 语义须经既有应答通道回填')
  assert.doesNotMatch(MANAGER, /defer:\s*true/, '未决项确认前不得新增 ask-user-reply 字段（设计 §8）')
  // 问题引用入输入框 + 不自动发送
  assert.match(MANAGER, /mq\.emit\(EventNames\.chatInsertText, \{[\s\S]{0,160}ask_user_chat_quote/, '问题引用须经 chat-insert-text 进输入框')
  assert.doesNotMatch(MANAGER, /chatAbout[\s\S]{0,2000}EventNames\.chatSend/, '「聊一聊」不得自动发送')
  // 幂等
  assert.match(MANAGER, /if \(item\.chatting\) return/, '重复点击须幂等')
})

test('S1-2 revealSession：已开对话窗口 → gui.window.open-chat 激活；否则主窗口切会话', () => {
  assert.match(MANAGER, /async revealSession\(sessionId\)/, '须有 revealSession')
  assert.match(MANAGER, /EventNames\.guiWindowList/, '须先查已开对话窗口（归属判定）')
  assert.match(MANAGER, /windowForSession\(windows, sessionId\)/, '归属判定复用 chatWindows 纯逻辑')
  assert.match(MANAGER, /EventNames\.guiWindowOpenChat, \{ session_id: sessionId \}/, '已开窗 → 幂等激活该窗口')
  assert.match(MANAGER, /await setActiveSessionID\(sessionId\)/, '未开窗 → 主窗口切会话（data-session-active-set）')
  assert.match(MANAGER, /EventNames\.sessionChanged, \{ session_id: sessionId \}/, '切会话须广播 session-changed（驱动会话树/任务区）')
})

// ── S1-3 / S1-4 dialog_id 双向 ─────────────────────────────────────────

test('S1-3 下行链路：ask-user 载荷的 dialog_id 透传到管理项', () => {
  assert.match(DIALOG, /dialogId: data\.dialog_id \|\| ''/, 'AskUserDialog 须透传 dialog_id')
  assert.match(MANAGER, /dialogId: data\.dialogId \|\| data\.dialog_id \|\| ''/, '管理项须保存 dialogId')
})

test('S1-4 上行链路：ask-user-reply 原样回带同一 dialog_id', () => {
  assert.match(MANAGER, /mq\.emit\(EventNames\.askReply, \{[\s\S]{0,300}dialog_id: item\.dialogId \|\| ''/, '应答须回带 dialog_id')
})

test('S1-3/S1-4 契约：schema 两主题 dialog_id 必带（generated msgkeys 已含）', () => {
  const schema = JSON.parse(readFileSync(join(HERE, '..', '..', '..', 'docs', 'spec', '60-reference', '61-messages.schema.json'), 'utf8'))
  const byTopic = new Map(schema.topics.map((t) => [t.topic, t]))
  const ask = byTopic.get('session-ask')
  assert.ok(ask, '契约须收录 session-ask')
  assert.deepEqual(ask.event.dialog_id, { type: 'string', required: true }, 'session-ask.dialog_id 必带')
  const reply = byTopic.get('ask-user-reply')
  assert.ok(reply, '契约须收录 ask-user-reply')
  assert.deepEqual(reply.payload.dialog_id, { type: 'string', required: true }, 'ask-user-reply.dialog_id 必带')
  // 生成物对账（不得手改）
  assert.equal(SessionAskKeys.dialog_id, 'dialog_id')
  assert.equal(AskUserReplyKeys.dialog_id, 'dialog_id')
  assert.equal(FieldKeys.dialog_id, 'dialog_id')
})

// ── S1-6 ask 弹窗居中于屏幕工作区 ──────────────────────────────────────

test('S1-6 弹窗开关：默认仍是窗口内居中（其它弹窗零变化），ask 显式开启', () => {
  assert.match(OPTIONS, /centerOnScreen: false/, '缺省 false（既有弹窗行为不变）')
  assert.match(MANAGER, /centerOnScreen: true/, 'ask 弹窗须开启屏幕工作区居中')
})

test('S1-6 DialogShell：按 screen.avail* 计算 + 视口 clamp，且不持久化位置', () => {
  assert.match(SHELL, /import \{ computeScreenCenteredPosition \} from '\.\.\/\.\.\/utils\/dialogPosition\.js'/, '须复用纯函数')
  assert.match(SHELL, /availLeft: s\.availLeft/, '须读屏幕工作区 availLeft')
  assert.match(SHELL, /window\.screenX/, '须扣掉窗口在屏幕上的位置')
  assert.match(SHELL, /if \(resolvedOptions\.value\.centerOnScreen\) \{[\s\S]{0,400}computeScreenCenteredPosition/, 'initPosition 须按屏幕工作区')
  assert.match(SHELL, /let autoCentered = resolvedOptions\.value\.centerOnScreen \|\| !resolvedOptions\.value\.position/,
    '开启后必须保留自动居中（实测尺寸再居中，直到用户拖拽）')
  // 不记忆上次拖拽位置（无 localStorage / sessionStorage 持久化）
  assert.doesNotMatch(SHELL, /(localStorage|sessionStorage)\s*\.\s*(get|set|remove)Item/, 'DialogShell 不得持久化弹窗位置')
  assert.doesNotMatch(MANAGER, /(localStorage|sessionStorage)\s*\.\s*(get|set|remove)Item/, 'ask 管理项不得持久化弹窗位置')
})

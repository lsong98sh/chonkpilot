/**
 * Ask 中心（独立待办窗口，2026-11-04）· 前端契约单测。
 *
 * 口径：docs/ask-user/AskUser专题设计方案.md §4.2 / §4.3 / §6.1；消息面以 61 为准
 * （gui.window.open-ask / gui.ask.list / gui.ask.remove / gui.chat.insert-text /
 *   ask-center-add / ask-center-remove / ask-center-subscribe / ask-center-unsubscribe）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 与 askUserChat.test.js 同法：
 * **源级断言**（SFC / 模块源码契约）+ 纯函数（viewRoute）+ schema 对账。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { EventNames } from '../src/events/event-names.js'
import { MsgTopics, GuiWindowOpenAskKeys, GuiAskListKeys, GuiAskRemoveKeys, GuiChatInsertTextKeys, AskCenterAddKeys, AskCenterRemoveKeys, AskCenterSubscribeKeys, AskResolveKeys, GuiWindowListKeys } from '../src/events/msgkeys.js'
import { parseViewRoute, VIEW_ASK, VIEW_CHAT } from '../src/utils/viewRoute.js'
import { readSrc } from './support/feSource.mjs'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readSrc(rel)

const APP = read('App.vue')
const DIALOG = read('views/chat/AskUserDialog.vue')
const CENTER = read('views/ask/AskCenterView.vue')
const ASK_CHAT = read('views/ask/AskChatPanel.vue')
const CHAT_ONLY = read('views/chat/ChatOnlyView.vue')
const MANAGER = read('utils/askUserManager.js')
const CHAT_API = read('api/chat.js')

test('视图分派：#ask → Ask 中心（无会话绑定）；#chat 仍为对话窗口', () => {
  assert.equal(parseViewRoute('', '#ask').view, VIEW_ASK)
  assert.equal(parseViewRoute('?work-dir=C%3A%5Cws', '#ask').view, VIEW_ASK)
  assert.equal(parseViewRoute('?session-id=s1', '#chat').view, VIEW_CHAT)
  assert.equal(parseViewRoute('', '').view, 'main')
})

test('App.vue：Ask 中心按 URL 挂载，且不触发主窗口初始化 / 布局预取', () => {
  assert.match(APP, /import AskCenterView from '\.\/views\/ask\/AskCenterView\.vue'/)
  assert.match(APP, /<AskCenterView v-if="ready && askOnly" \/>/)
  assert.match(APP, /const askOnly = route\.view === VIEW_ASK/)
  assert.match(APP, /if \(!ready\.value \|\| chatOnly \|\| askOnly \|\| logOnly\) return/)
  assert.match(APP, /if \(!chatOnly && !askOnly\) prefetchInitData\(\)/)
})

test('AskUserDialog：先路由到 Ask 中心；宿主不支持才回落既有弹窗', () => {
  assert.match(DIALOG, /EventNames\.guiWindowOpenAsk/)
  assert.match(DIALOG, /async function routeToAskCenter\(/)
  assert.match(DIALOG, /result && result\.ok === true/)
  assert.match(DIALOG, /if \(await routeToAskCenter\(ask\)\) return/)
  assert.match(DIALOG, /askUserManager\.enqueue\(/)
})

test('AskCenterView：左列待办 + 右侧详情 + 四个出口', () => {
  assert.match(CENTER, /class="ask-list"/)
  assert.match(CENTER, /v-for="item in items"/)
  assert.match(CENTER, /class="ask-detail"/)
  assert.match(CENTER, /<AskUserContent[\s\S]{0,600}:can-chat="true"/)
  assert.match(CENTER, /@answer="onAnswer"/)
  assert.match(CENTER, /@skip="onSkip"/)
  assert.match(CENTER, /@cancel="onCancel"/)
  assert.match(CENTER, /@chat="onChat"/)
})

test('AskCenterView：宿主待办表读取 / 摘除 / 新增推送 / 聊一聊面板', () => {
  assert.match(CENTER, /EventNames\.guiAskList/)
  assert.match(CENTER, /EventNames\.guiAskRemove/)
  assert.match(CENTER, /EventNames\.askCenterSubscribe/)
  assert.match(CENTER, /EventNames\.askCenterUnsubscribe/)
  assert.match(CENTER, /EventNames\.askCenterAdd/)
  assert.match(CENTER, /EventNames\.askCenterRemove/)
  // 聊一聊 = 内嵌聊天面板（不再 defer 回填 / 不再 gui.chat.insert-text）
  assert.match(CENTER, /<AskChatPanel/)
  assert.match(CENTER, /chatMode\.value = true/)
  assert.doesNotMatch(CENTER, /EventNames\.guiChatInsertText/)
  assert.doesNotMatch(CENTER, /ask_user_chat_defer_answer/)
  // 完成 = ask-resolve（不通知 LLM）
  assert.match(CENTER, /EventNames\.askResolve/)
  assert.match(CENTER, /async function onChatDone\(/)
  assert.match(CENTER, /:context="current\.context"/)
  assert.match(CENTER, /:no-timeout="deadlineMs\(current\.expires_at\) === 0"/)
  assert.match(CENTER, /sendAskReply\(item, answer\)/)
  // server expires_at 是 RFC3339 字符串；测试夹具是 unix ms → 两种都要解析
  assert.match(CENTER, /Date\.parse\(v\)/)
})

test('AskChatPanel：内嵌会话视图 + 以源会话实例发送 + 完成/返回', () => {
  assert.match(ASK_CHAT, /<MessageList[\s\S]{0,200}:session-id="ask\.session"/)
  assert.match(ASK_CHAT, /:dialog-id="ask\.dialog_id"/)
  assert.match(ASK_CHAT, /EventNames\.messageSend/)
  assert.match(ASK_CHAT, /instanceId: props\.ask\.owner_instance \|\| props\.ask\.instance_id \|\| ''/)
  assert.match(ASK_CHAT, /@click="\$emit\('done'\)"/)
  assert.match(ASK_CHAT, /@click="\$emit\('back'\)"/)
})

test('publishInboxAdd：可选 instance_id（Ask 中心以源会话实例发送）', () => {
  assert.match(CHAT_API, /export function publishInboxAdd\([^)]*instanceId = ''\)/)
  assert.match(CHAT_API, /if \(instanceId\) payload\.instance_id = instanceId/)
})

test('AskUserManager：sendAskReply 为弹窗与 Ask 中心共用（dialog_id 原样回带）', () => {
  assert.match(MANAGER, /export function sendAskReply\(item, answer\)/)
  assert.match(MANAGER, /ask_id: item\.askId \|\| item\.ask_id \|\| ''/)
  assert.match(MANAGER, /dialog_id: item\.dialogId \|\| item\.dialog_id \|\| ''/)
})

test('ChatOnlyView：URL draft（聊一聊）在挂载后注入输入框，不自动发送', () => {
  assert.match(CHAT_ONLY, /draft: \{ type: String, default: '' \}/)
  assert.match(CHAT_ONLY, /mq\.emit\(EventNames\.chatInsertText, \{ text: props\.draft \}\)/)
  assert.doesNotMatch(CHAT_ONLY, /chatSend[\s\S]{0,80}draft/, 'draft 不得自动发送')
})

test('契约：Ask 中心九主题 + gui.window.list.ask_window_id 已收录并生成 msgkeys', () => {
  const schema = JSON.parse(readFileSync(join(HERE, '..', '..', '..', 'docs', 'spec', '60-reference', '61-messages.schema.json'), 'utf8'))
  const byTopic = new Map(schema.topics.map((t) => [t.topic, t]))
  for (const topic of ['gui.window.open-ask', 'gui.ask.list', 'gui.ask.remove', 'gui.chat.insert-text', 'ask-center-add', 'ask-center-remove', 'ask-center-subscribe', 'ask-center-unsubscribe', 'ask-resolve']) {
    assert.ok(byTopic.get(topic), `契约缺 ${topic}`)
  }
  assert.equal(MsgTopics.guiWindowOpenAsk, 'gui.window.open-ask')
  assert.equal(MsgTopics.guiAskList, 'gui.ask.list')
  assert.equal(MsgTopics.guiAskRemove, 'gui.ask.remove')
  assert.equal(MsgTopics.askCenterSubscribe, 'ask-center-subscribe')
  assert.equal(MsgTopics.askCenterUnsubscribe, 'ask-center-unsubscribe')
  assert.equal(MsgTopics.guiChatInsertText, 'gui.chat.insert-text')
  assert.equal(MsgTopics.askCenterAdd, 'ask-center-add')
  assert.equal(MsgTopics.askCenterRemove, 'ask-center-remove')
  assert.equal(MsgTopics.askResolve, 'ask-resolve')
  // 生成物对账（不得手改）
  assert.equal(GuiWindowOpenAskKeys.ask, 'ask')
  assert.equal(GuiAskListKeys.asks, 'asks')
  assert.equal(GuiAskRemoveKeys.ask_id, 'ask_id')
  assert.equal(AskCenterSubscribeKeys.instance_id, 'instance_id')
  assert.equal(AskCenterSubscribeKeys.scope, 'scope')
  assert.equal(GuiChatInsertTextKeys.work_dir, 'work_dir')
  assert.equal(AskCenterAddKeys.ask, 'ask')
  assert.equal(AskCenterAddKeys.work_dir, 'work_dir')
  assert.equal(AskCenterAddKeys.received_at, 'received_at')
  assert.equal(AskCenterAddKeys.seq, 'seq')
  assert.equal(AskCenterRemoveKeys.ask_id, 'ask_id')
  assert.equal(AskCenterRemoveKeys.reason, 'reason')
  assert.equal(AskResolveKeys.ask_id, 'ask_id')
  assert.equal(AskResolveKeys.reason, 'reason')
  assert.equal(GuiWindowListKeys.ask_window_id, 'ask_window_id')
  // session-ask：owner_instance 必带；expires_at 可缺省（无超时）
  const ask = byTopic.get('session-ask').event
  assert.equal(ask.owner_instance.required, true)
  assert.equal(ask.expires_at.required, false)
  assert.ok(ask.context)
})

test('EventNames：Ask 中心本地事件已登记', () => {
  assert.equal(EventNames.guiWindowOpenAsk, 'gui.window.open-ask')
  assert.equal(EventNames.guiAskList, 'gui.ask.list')
  assert.equal(EventNames.guiAskRemove, 'gui.ask.remove')
  assert.equal(EventNames.askCenterSubscribe, 'ask-center-subscribe')
  assert.equal(EventNames.askCenterUnsubscribe, 'ask-center-unsubscribe')
  assert.equal(EventNames.guiChatInsertText, 'gui.chat.insert-text')
  assert.equal(EventNames.askCenterAdd, 'ask-center-add')
  assert.equal(EventNames.askCenterRemove, 'ask-center-remove')
  assert.equal(EventNames.askResolve, 'ask-resolve')
})
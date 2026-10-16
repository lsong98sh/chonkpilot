/**
 * SUBCHAT-3 阶段 G —— 群聊窗口（只读展示板）纯逻辑 + 接线守卫测试（2026-10-16）。
 *
 * 依据：`docs/agent-chat/群聊会话设计方案.md` §4（窗口 UI）· §9 D14/D23/D28/D29/D30/D41/D47/D48 ·
 *       `docs/spec/24-多窗口模型设计方案.md` §6.4（T-1…T-5）· `61-消息一览` §3.1/§3.2/§4.2/§4.3。
 *
 * 覆盖：
 *  1) `utils/groupchat.js`：**work_session id 公式与后端 `groupWorkSessionID` 对账**（FNV-1a 用
 *     规范测试向量独立校验）、会话 id 净化、主 agent 靠左判定；
 *  2) `utils/chatWindows.js`：**群聊窗口单独额度**（群聊 / 普通两类计数，互不联动；不传 `count`
 *     时既有行为不变）；
 *  3) 接线守卫（源码断言）：ChatOnlyView 按 `kind` 分派群聊展示板（且**保留** ChatPanel 行）、
 *     展示板只读（无输入框）+ 三类动作消息字面量、**头像三态映射**、**收敛点亮「可以结束了」（G5）**、
 *     右侧分栏**不另开窗口**（D29）、**零 `watch`** 全文件扫描、宿主侧群聊额度判定落点。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  GROUP_CHAT_KIND,
  agentDisplayName,
  fnv32aHex,
  groupWorkSessionId,
  isGroupChatKind,
  isLeftAligned,
  sanitizeIdSeg,
} from '../src/utils/groupchat.js'
import {
  MAX_CHAT_WINDOWS,
  MAX_GROUP_CHAT_WINDOWS,
  atGroupChatWindowLimit,
  chatEntryState,
  countGroupChatWindows,
  countNormalChatWindows,
} from '../src/utils/chatWindows.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = (p) => readFileSync(join(HERE, '..', 'src', p), 'utf8')
const ROOT = (p) => readFileSync(join(HERE, '..', '..', '..', p), 'utf8')

// ── 1) 群聊纯逻辑 ────────────────────────────────────────────────

test('群聊会话类型：`group_chat` 判定', () => {
  assert.equal(GROUP_CHAT_KIND, 'group_chat')
  assert.equal(isGroupChatKind('group_chat'), true)
  assert.equal(isGroupChatKind('sub_chat'), false)
  assert.equal(isGroupChatKind(''), false)
  assert.equal(isGroupChatKind(null), false)
})

test('FNV-1a(32) 与规范测试向量一致（work_session id 公式的哈希部分）', () => {
  assert.equal(fnv32aHex(''), '811c9dc5')
  assert.equal(fnv32aHex('a'), 'e40c292c')
  assert.equal(fnv32aHex('b'), 'e70c2de5')
  assert.equal(fnv32aHex('foobar'), 'bf9cf968')
})

test('会话 id 安全段净化（复刻后端 sanitizeIDSeg）', () => {
  assert.equal(sanitizeIdSeg('agent-a_1'), 'agent-a_1')
  assert.equal(sanitizeIdSeg('scn/a'), 'scn_a')
  assert.equal(sanitizeIdSeg('审查员'), '___')
  assert.equal(sanitizeIdSeg(''), 'agent')
})

test('work_session id = work-<群聊>-<净化 agent>-<fnv32a 8hex>（确定性、可区分、与后端同式）', () => {
  const id = groupWorkSessionId('g-1', 'scn/a')
  assert.match(id, /^work-g-1-scn_a-[0-9a-f]{8}$/)
  assert.equal(id, groupWorkSessionId('g-1', 'scn/a'), '同 (群聊, agent) 恒同 id（跨轮复用）')
  assert.notEqual(groupWorkSessionId('g-1', 'scn/a'), groupWorkSessionId('g-1', 'scn/b'))
  assert.notEqual(groupWorkSessionId('g-1', 'a'), groupWorkSessionId('g-2', 'a'))
  assert.equal(groupWorkSessionId('g-1', 'a'), `work-g-1-a-${fnv32aHex('a')}`)
})

test('agent 展示名取末段（`<场景id>/<agent名>` → 裸名）', () => {
  assert.equal(agentDisplayName('scn/审查员'), '审查员')
  assert.equal(agentDisplayName('审查员'), '审查员')
  assert.equal(agentDisplayName(''), '')
})

test('主 agent ≡ 「用户」（D47）：其发言靠左；参与者发言靠右；系统提示不参与左右', () => {
  const main = { sender_agent: 'scn/main', role: 'user', content: '布置任务' }
  const part = { sender_agent: 'scn/worker', role: 'assistant', content: '结论' }
  const notice = { role: 'system', content: '（因达上限终止）' }
  assert.equal(isLeftAligned(main, 'scn/main'), true)
  assert.equal(isLeftAligned(part, 'scn/main'), false)
  assert.equal(isLeftAligned(notice, 'scn/main'), false)
  assert.equal(isLeftAligned(null, 'scn/main'), false)
})

// ── 2) 窗口额度：群聊单独计上限 5（D28/D30）─────────────────────

const W = (id, sid) => ({ window_id: id, session_id: sid })

test('群聊窗口单独额度 = 5（不与普通对话窗口联动）', () => {
  assert.equal(MAX_CHAT_WINDOWS, 5)
  assert.equal(MAX_GROUP_CHAT_WINDOWS, 5)
  const kinds = { g1: 'group_chat', g2: 'group_chat', c1: '', s1: 'sub_chat' }
  const kindOf = (sid) => kinds[sid] || ''
  const windows = [W('w1', 'g1'), W('w2', 'g2'), W('w3', 'c1'), W('w4', 's1')]
  assert.equal(countGroupChatWindows(windows, kindOf), 2)
  assert.equal(countNormalChatWindows(windows, kindOf), 2)
  // 群聊 5 个（上限）→ 群聊入口置灰；普通窗口仍有 3 个额度（互不联动）
  const many = [
    W('a', 'g1'), W('b', 'g2'), W('c', 'g3'), W('d', 'g4'), W('e', 'g5'), W('f', 'c1'),
  ]
  const kinds2 = { g1: 'group_chat', g2: 'group_chat', g3: 'group_chat', g4: 'group_chat', g5: 'group_chat', c1: '' }
  const kindOf2 = (sid) => kinds2[sid] || ''
  assert.equal(atGroupChatWindowLimit(many, kindOf2), true)
  assert.equal(countNormalChatWindows(many, kindOf2), 1)
  const stGroup = chatEntryState({
    windows: many, sessionId: 'g6', max: MAX_GROUP_CHAT_WINDOWS,
    count: countGroupChatWindows(many, kindOf2),
  })
  assert.equal(stGroup.action, 'disabled')
  assert.equal(stGroup.reason, 'limit')
  const stNormal = chatEntryState({
    windows: many, sessionId: 'c9', max: MAX_CHAT_WINDOWS,
    count: countNormalChatWindows(many, kindOf2),
  })
  assert.equal(stNormal.action, 'open', '普通对话窗口额度不受群聊窗口影响')
})

test('入口态：不传 count 时行为与改前一致（既有回归口径）', () => {
  const full = ['a', 'b', 'c', 'd', 'e'].map((s, i) => W('w' + i, s))
  assert.equal(chatEntryState({ windows: full, sessionId: 'new', mainSessionId: 'a' }).reason, 'limit')
  assert.equal(chatEntryState({ windows: full.slice(0, 4), sessionId: 'new', mainSessionId: 'a' }).action, 'open')
  // 非法 kindOf → 全部按普通窗口计（不抛）
  assert.equal(countGroupChatWindows(full, null), 0)
  assert.equal(countNormalChatWindows(full, null), 5)
})

// ── 3) 接线守卫 ─────────────────────────────────────────────────

test('ChatOnlyView：按 kind 分派（群聊 → 展示板），ChatPanel 行保留且参数不变', () => {
  const v = SRC('views/chat/ChatOnlyView.vue')
  assert.match(v, /<ChatPanel\s+:session-id="sessionId"\s+:chat-only="true"\s*\/>/,
    '普通会话 / 单聊仍走 ChatPanel（参数不变）')
  assert.match(v, /<GroupChatBoard\s+:session-id="sessionId"\s*\/>/, '群聊 → GroupChatBoard（绑定 URL 会话）')
  assert.match(v, /isGroupChatKind\(sessionKind\.value\)/, '分派依据 = 会话 kind（data-session-get）')
  assert.doesNotMatch(v, /\bwatch(Effect)?\s*\(/, '禁 watch/watchEffect')
})

test('群聊前端（组件 / 接线层 / 叶子模块）零 `watch` / `watchEffect`（项目硬约束）', () => {
  const files = [
    'views/chat/GroupChatBoard.vue',
    'views/chat/GroupChatCloseDialog.vue',
    'views/chat/ChatOnlyView.vue',
    'composables/useGroupChat.js',
    'composables/useSessionKinds.js',
    'composables/useChatWindows.js',
    'utils/groupchat.js',
    'utils/chatWindows.js',
  ]
  for (const f of files) {
    assert.doesNotMatch(SRC(f), /\bwatch(Effect)?\s*\(/, f + '：禁 watch/watchEffect')
  }
})

test('GroupChatBoard：只读展示板（无输入框）+ 三类动作消息字面量正确', () => {
  const v = SRC('views/chat/GroupChatBoard.vue')
  assert.doesNotMatch(v, /InputBox|contenteditable/, '人不能发言 → 不渲染输入框（D14）')
  assert.match(v, /MsgTopics\.subchatParticipantState/, '头像状态订阅 subchat-participant-state（D16）')
  assert.match(v, /continueGroupChat\(props\.sessionId\)/, '「继续」= subchat-continue（D50/断点补跑）')
  assert.match(v, /finishSubChat\(props\.sessionId\)/, '「终止」= subchat-finish（D37/N22）')
  assert.match(v, /mq\.emit\(EventNames\.guiWindowStatus,\s*\{\s*command:\s*'close'\s*\}\)/,
    '关窗经 gui.window.status{command:close}（暂停 = 仅关窗 → instance-exit 停机）')
  assert.match(v, /<MessageList[\s\S]*:viewer="true"/, '右侧分栏 = MessageList 只读视图（D29/D48）')
  assert.match(v, /groupWorkSessionId\(props\.sessionId,/, '右侧分栏按确定性公式取 work_session id')
  assert.match(v, /lifecycle === SUBCHAT_READY \? \$t\('chat\.subchat_ready_hint'\)/,
    'G5：收敛（lifecycle=ready）→ 点亮「可以结束了」（D10/D52）')
  assert.match(v, /\.gb-state\.lit/, 'G5：点亮态有高亮样式')
  // 头像三态映射（D16）：thinking → 转圈动画 / spoken → 打勾 / idle → 转停
  assert.match(v, /s === PARTICIPANT_THINKING\) return 'thinking'/, 'thinking → 转圈')
  assert.match(v, /s === 'spoken'\) return 'spoken'/, 'spoken → 有发言')
  assert.match(v, /s === 'idle'\) return 'idle'/, 'idle → 转停（无关）')
  assert.match(v, /@keyframes gb-spin/, 'thinking 有小图标旋转动画')
  assert.match(v, /\.gb-avatar\.spoken \.gb-avatar-mark/, 'spoken 有打勾标记')
  // 右侧分栏**不占窗口数**（D29）：展示板从不调 `gui.window.open-chat`（不另开窗口）
  assert.doesNotMatch(v, /open-chat|openChat\(/, '右侧分栏不另开窗口（D29）')
  // 群聊窗口**不**写活动会话 / 不写几何（对话窗口通用约束，24 §3.3 C5）
  assert.doesNotMatch(v, /setActiveSessionID|session-active-set|layout:/)
})

test('宿主侧：群聊窗口单独额度落点（registry.go / window.go / bridge.SessionKind）', () => {
  const reg = ROOT('src/lib/gui/registry.go')
  assert.match(reg, /maxGroupChatWindows/, '群聊窗口独立上限取自 window.go 常量')
  assert.match(reg, /groupSessions/, '按会话类别登记 → 分类计数（普通窗口计数口径不变）')
  assert.match(reg, /br\.SessionKind\(sessionID\)\s*==\s*groupChatKind/, '建窗前经只读 data-session-get 判 kind')
  const win = ROOT('src/lib/gui/window.go')
  assert.match(win, /const maxChatWindows = 5/, '普通对话窗口上限不变（≤5）')
  assert.match(win, /const maxGroupChatWindows = 5/, '群聊窗口单独上限 = 5')
  const data = ROOT('src/lib/gui/bridge/data.go')
  assert.match(data, /func \(b \*Bridge\) SessionKind\(sessionID string\) string/, '桥提供只读 kind 查询')
  assert.match(data, /msgkeys\.TopicDataSessionGet/, '复用既有 data-session-get（零消息面变更）')
})

test('入口图标区分 / 额度文案：三处入口按 kind 区分（单聊 vs 群聊）', () => {
  assert.match(SRC('views/sessions/SessionsPane.vue'), /isGroupChatKind\(c\.kind\)\s*\?\s*'group'\s*:\s*'chat-dot-round'/,
    '会话列表下挂按 kind 换图标（D41）')
  assert.match(SRC('views/chat/SubChatBar.vue'), /isGroupChatKind\(it\[FieldKeys\.kind\]\)/, '状态条按 kind 区分')
  assert.match(SRC('views/chat/SubChatStartupDialog.vue'), /isGroupChatKind\(it\[FieldKeys\.kind\]\)/, '启动提醒按 kind 区分')
  assert.match(SRC('components/icon/icons.js'), /'group':/, '新增 group 图标')
  const zh = JSON.parse(SRC('locales/zh-CN/chat.json'))
  const en = JSON.parse(SRC('locales/en-US/chat.json'))
  assert.ok(zh.group_chat_window_limit && en.group_chat_window_limit, '群聊窗口上限**单独一条**文案（zh/en）')
  assert.ok(zh.group_chat_close_hint && en.group_chat_close_hint, '关窗选择文案（zh/en）')
})

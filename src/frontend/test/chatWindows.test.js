/**
 * MW-9 / MW-10 —— 纯对话窗口「前端视图 + 开启入口」守卫与纯逻辑测试（2026-09-25）。
 *
 * 依据：24-多窗口模型设计方案 §6（前端改造）· §6.3（标题）· §6.4（T-1…T-5）；
 *       31-窗口与布局 WIN-017~020（含 WIN-019-S06/S07）；61-消息一览 §1（payload 零新增字段）。
 *
 * 覆盖：
 *  1) 纯逻辑（可直跑）：URL 视图分派（`utils/viewRoute.js`）、入口置灰/激活判定
 *     （`utils/chatWindows.js`：已开窗 / 达上限 / 主窗当前会话 / 幂等并入与关闭解除）；
 *  2) 接线守卫（源码断言）：App.vue 按 URL 分派 ChatOnlyView；ChatOnlyView 仅 chat +
 *     不写几何 + 不调 `session-active-*` + 标题走 `gui.window.set-title`；
 *     ChatPanel 的 T-1（仅主窗口、达上限置灰）；SessionsPane 的 T-2/T-3/T-4/T-5；
 *     消息主题字面量与 61 §1 一致，`open-chat` 入参 = `{session_id}`。
 *
 * 说明：`composables/useChatWindows.js` 含 `vue` / `utils/mq` 无扩展名导入 → Node ESM 不可直载，
 * 故其正确性由「纯逻辑单测 + 接线守卫」共同覆盖（与 layoutPrefetch.test.js 同口径）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { VIEW_CHAT, VIEW_MAIN, parseViewRoute } from '../src/utils/viewRoute.js'
import {
  MAX_CHAT_WINDOWS,
  chatEntryState,
  atChatWindowLimit,
  windowForSession,
  mergeOpenedWindow,
  removeClosedWindow,
} from '../src/utils/chatWindows.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = (p) => readFileSync(join(HERE, '..', 'src', p), 'utf8')

// ── 1) URL 视图分派（24 §6.1 / U-2：以 URL 为准）──────────────────

test('视图分派：`?session-id=…#chat` → chat（绑定该会话）', () => {
  const r = parseViewRoute('?session-id=abc123', '#chat')
  assert.equal(r.view, VIEW_CHAT)
  assert.equal(r.sessionId, 'abc123')
})

test('视图分派：缺 session-id / 缺 #chat / 空 URL → 主窗口', () => {
  assert.deepEqual(parseViewRoute('#chat', '#chat'), { view: VIEW_MAIN, sessionId: '' }) // 只命中 hash → 非对话窗口
  assert.deepEqual(parseViewRoute('?session-id=abc123', ''), { view: VIEW_MAIN, sessionId: '' })
  assert.deepEqual(parseViewRoute('', '#chat'), { view: VIEW_MAIN, sessionId: '' })
  assert.deepEqual(parseViewRoute('', ''), { view: VIEW_MAIN, sessionId: '' })
  assert.deepEqual(parseViewRoute('?foo=1', '#chat'), { view: VIEW_MAIN, sessionId: '' })
})

test('视图分派：hash 必须精确等于 #chat（#chat2 / 大小写不合规 → 主窗口）', () => {
  assert.equal(parseViewRoute('?session-id=s1', '#chat2').view, VIEW_MAIN)
  assert.equal(parseViewRoute('?session-id=s1', '#Chat').view, VIEW_MAIN)
})

test('视图分派：session-id 经 URL 解码（宿主 url.QueryEscape 语义）', () => {
  const r = parseViewRoute('?session-id=s%20a%2Fb', '#chat')
  assert.equal(r.view, VIEW_CHAT)
  assert.equal(r.sessionId, 's a/b')
})

// ── 2) 入口态判定（I-1：已开窗 / 主窗当前会话 / 上限）─────────────

const W = (id, sid) => ({ window_id: id, session_id: sid })

test('I-1 ①：该 session = 主窗口当前活动会话 → 置灰（拒开）', () => {
  const st = chatEntryState({ windows: [], sessionId: 's1', mainSessionId: 's1' })
  assert.equal(st.action, 'disabled')
  assert.equal(st.reason, 'main-active')
})

test('I-1 ②：该 session 已被对话窗口绑定 → 激活（不新建）', () => {
  const st = chatEntryState({ windows: [W('w1', 's1')], sessionId: 's1', mainSessionId: 's9' })
  assert.equal(st.action, 'activate')
  assert.equal(st.windowId, 'w1')
})

test('达上限（≤5，不含主窗口）→ 新建入口置灰；未达上限 → 建窗', () => {
  assert.equal(MAX_CHAT_WINDOWS, 5)
  const full = ['a', 'b', 'c', 'd', 'e'].map((s, i) => W('w' + i, s))
  assert.equal(atChatWindowLimit(full), true)
  const stFull = chatEntryState({ windows: full, sessionId: 'new', mainSessionId: 'a' })
  assert.equal(stFull.action, 'disabled')
  assert.equal(stFull.reason, 'limit')
  const st4 = chatEntryState({ windows: full.slice(0, 4), sessionId: 'new', mainSessionId: 'a' })
  assert.equal(st4.action, 'open')
  assert.equal(st4.reason, '')
})

test('入口态：空 session_id → 置灰（不可开窗）', () => {
  const st = chatEntryState({ windows: [], sessionId: '', mainSessionId: '' })
  assert.equal(st.action, 'disabled')
  assert.equal(st.reason, 'no-session')
})

test('windowForSession / atChatWindowLimit：按 session 反查与计数（非法入参不抛）', () => {
  assert.equal(windowForSession([W('w1', 's1')], 's1'), 'w1')
  assert.equal(windowForSession([W('w1', 's1')], 's2'), '')
  assert.equal(windowForSession(null, 's1'), '')
  assert.equal(windowForSession([W('w1', 's1')], ''), '')
  assert.equal(atChatWindowLimit(null), false)
  assert.equal(atChatWindowLimit([], 5), false)
})

test('幂等并入 / 关闭解除：activated 不新增；关闭按 window_id 与 session_id 双向移除', () => {
  const empty = []
  const one = mergeOpenedWindow(empty, { window_id: 'w1', session_id: 's1', activated: false })
  assert.deepEqual(one, [W('w1', 's1')])
  assert.deepEqual(empty, [], '纯函数不改原数组')
  // 幂等：命中已开窗（activated=true）→ 不新增；重复登记同样不新增
  assert.deepEqual(mergeOpenedWindow(one, { window_id: 'w1', session_id: 's1', activated: true }), [W('w1', 's1')])
  assert.deepEqual(mergeOpenedWindow(one, { window_id: 'w1', session_id: 's1', activated: false }), [W('w1', 's1')])
  // ok=false（无 window_id）→ 不登记
  assert.deepEqual(mergeOpenedWindow(one, { ok: false, activated: false }), [W('w1', 's1')])
  // gui.window.closed → 解除占用
  assert.deepEqual(removeClosedWindow(one, { window_id: 'w1', session_id: 's1' }), [])
  assert.deepEqual(removeClosedWindow([W('w1', 's1'), W('w2', 's2')], { session_id: 's2' }), [W('w1', 's1')])
})

// ── 3) 消息面字面量（61 §1；不许改字段）─────────────────────────

test('消息主题与 61 §1 逐字一致，open-chat 入参 = {session_id}', () => {
  const names = SRC('events/event-names.js')
  assert.match(names, /guiWindowOpenChat:\s*'gui\.window\.open-chat'/)
  assert.match(names, /guiWindowList:\s*'gui\.window\.list'/)
  assert.match(names, /guiWindowClosed:\s*'gui\.window\.closed'/)
  assert.match(names, /guiWindowSetTitle:\s*'gui\.window\.set-title'/)

  const c = SRC('composables/useChatWindows.js')
  assert.match(c, /mq\.emit\(EventNames\.guiWindowOpenChat,\s*\{\s*session_id:\s*sessionId\s*\}\)/,
    'open-chat payload 必须为 {session_id}（61 §1，零新增字段）')
  assert.match(c, /mq\.emit\(EventNames\.guiWindowList,\s*\{\s*\}\)/, 'list payload = {}')
  assert.match(c, /mq\.on\(EventNames\.guiWindowClosed/, '订阅 gui.window.closed 解除占用')
  assert.match(c, /mergeOpenedWindow\(/, 'open-chat 成功 → 并入本地清单（activated 不新增）')
  assert.match(c, /removeClosedWindow\(/, 'gui.window.closed → 移除本地清单')
  assert.match(c, /MAX_CHAT_WINDOWS/, '上限取自纯模块（与宿主 ≤5 同值）')
  // 开窗前先 session-ensure（新会话 id 由前端分配，24 §6.4）
  const newChat = c.match(/export async function openNewChatWindow[\s\S]*?\n\}/)
  assert.ok(newChat, '未找到 openNewChatWindow')
  assert.match(newChat[0], /newSessionId\(\)/, '新会话 id 由前端分配')
  assert.ok(newChat[0].indexOf('ensureSession(sid)') < newChat[0].indexOf('openChat(sid)'),
    '必须先 session-ensure 再 open-chat')
})

// ── 4) 接线守卫：App.vue 视图分派 ────────────────────────────────

test('App.vue：按 URL 分派 ChatOnlyView，且对话窗口不触发主窗口会话初始化', () => {
  const app = SRC('App.vue')
  assert.match(app, /import\s*\{\s*parseViewRoute,\s*VIEW_CHAT\s*\}\s*from\s*'\.\/utils\/viewRoute\.js'/,
    'App.vue 必须用 URL 分派纯模块（不注入 __ck.view）')
  assert.match(app, /parseViewRoute\(window\.location\.search,\s*window\.location\.hash\)/,
    '分派来源 = location.search + location.hash')
  assert.match(app, /<ChatOnlyView\s+v-else-if="ready && chatOnly"\s+:session-id="route\.sessionId"/,
    '命中 #chat → ChatOnlyView 且传入 URL 的 session-id')
  assert.match(app, /<MainLayout\s+v-else-if="ready"/, '其余仍走主布局')
  // init()（读活动会话 + 广播 session-changed）在对话窗口必须跳过
  const init = app.match(/async function init\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(init, '未找到 init()')
  assert.match(init[0], /if\s*\(!ready\.value\s*\|\|\s*chatOnly\)\s*return/, '对话窗口跳过活动会话初始化')
  // 对话窗口不预取布局
  assert.match(app, /if\s*\(!chatOnly\)\s*prefetchInitData\(\)/, '对话窗口不预取 init-data（不读几何）')
})

// ── 5) 接线守卫：ChatOnlyView（仅 chat / 不持久化几何 / 标题）────

test('ChatOnlyView：整页仅 ChatPanel（绑定 URL session-id）+ 原生标题栏（不自绘）', () => {
  const v = SRC('views/chat/ChatOnlyView.vue')
  assert.match(v, /<ChatPanel\s+:session-id="sessionId"\s+:chat-only="true"\s*\/>/,
    '内容 = ChatPanel 且绑定 URL 会话、标记对话窗口')
  assert.doesNotMatch(v, /MainLayout|Toolbar|ExplorerPane|StatusBar|SessionTree/,
    '不得出现 toolbar/文件树/预览/任务/状态栏')
  // AskUserDialog = 纯 mq 监听器（无模板）→ 不构成可视节点，仅保证该窗口可应答 ask_user（可跑 tools）
  assert.match(v, /<AskUserDialog\s*\/>/, 'ask-user 监听器（无可视节点）随对话窗口挂载')
  assert.doesNotMatch(v, /webkit-app-region|-webkit-app-region|\bwindowToggle\b|guiWindowStatus/,
    '原生标题栏（宿主 Frameless:false）→ 前端不画自绘标题栏/拖动区/窗口按钮')
})

test('ChatOnlyView：不落几何、不调 session-active-*，标题走 gui.window.set-title', () => {
  const v = SRC('views/chat/ChatOnlyView.vue')
  assert.doesNotMatch(v, /gui\.ui\.save|saveLayoutState|window\.c|layout:/,
    '对话窗口不写 window.*/layout.*（24 §3.3 C5）')
  assert.doesNotMatch(v, /session-active-set|session-active-get|setActiveSessionID/,
    '对话窗口不调用 session-active-*（24 §3.3 C5）')
  assert.match(v, /mq\.emit\(EventNames\.guiWindowSetTitle,\s*\{\s*title\s*\}\)/,
    '标题经 gui.window.set-title 推送（仅对话窗口）')
  assert.match(v, /claimedInstance\(\)/, '缺省标题的 workdir 取已认领实例（零额外往返）')
  assert.match(v, /新会话/, '无摘要 → 缺省「新会话 + <workdir 目录名>」（24 §6.3 C7）')
  assert.match(v, /title === lastTitle/, '标题变化才推送（幂等）')
})

test('会话标题变更广播：ChatOnlyView 订阅 data-session-title-changed（61 §3.2 · #12）', () => {
  const names = SRC('events/event-names.js')
  assert.match(names, /sessionTitleChanged:\s*'data-session-title-changed'/,
    '主题字面量与 61 §3.2 一致')
  const v = SRC('views/chat/ChatOnlyView.vue')
  assert.match(v, /mq\.on\(EventNames\.sessionTitleChanged,/, '对话窗口订阅标题变更下行广播')
  assert.match(v, /d\.session_id\s*!==\s*props\.sessionId/,
    '按本窗绑定会话过滤（其它会话改名不波及本窗）')
  assert.match(v, /applyTitle\(d\.title\)/, '载荷直接携带新标题 → 应用（不再往返 data-session-get）')
  assert.doesNotMatch(v, /\bwatch(Effect)?\s*\(/, '禁 watch/watchEffect（规则：事件经 mq.on）')
})

// ── 6) 接线守卫：T-1（主 chat 头部，仅主窗口）────────────────────

test('T-1：ChatPanel 头部「新建对话窗口」仅主窗口可见 + 达上限置灰', () => {
  const cp = SRC('views/chat/ChatPanel.vue')
  // P1-9（2026-09-26 UX 评审 + 用户裁决 c）：chatOnly 下**标题条精简**——只留**场景 Tag**；
  // T-1 /「新建会话」/面板标题/会话号 等**非场景元素**包在 `<template v-if="!chatOnly">` 内随之隐藏。
  assert.match(cp, /<div class="panel-header" :class="\{ 'panel-header-mini': chatOnly \}">/,
    '标题条按 chatOnly 切精简形态（仅场景 Tag）')
  assert.match(cp, /<template v-if="!chatOnly">/,
    '非场景元素（含 T-1 与「新建会话」）随 chatOnly 隐藏')
  const btn = cp.match(/<!-- T-1[\s\S]*?<\/Button>/)
  assert.ok(btn, '未找到 T-1 入口')
  assert.match(btn[0], /:disabled="atLimit"/, '达上限（≤5）→ 置灰')
  assert.match(btn[0], /handleNewChatWindow/, '点击 → 新建对话窗口')
  assert.match(cp, /openNewChatWindow\(\)/, 'T-1 走 useChatWindows.openNewChatWindow（新会话 + ensure）')
  assert.match(cp, /class="new-session-btn icon-btn"/, '「新建会话」入口仍在标题条内（chatOnly 下随非场景部分隐藏）')
})

test('ChatPanel：对话窗口绑定 URL 会话（不读/不写活动会话、不跟随 session-changed）', () => {
  const cp = SRC('views/chat/ChatPanel.vue')
  assert.match(cp, /sessionId:\s*\{\s*type:\s*String,\s*default:\s*''\s*\}/, '新增 sessionId 参数')
  assert.match(cp, /chatOnly:\s*\{\s*type:\s*Boolean,\s*default:\s*false\s*\}/, '新增 chatOnly 参数')
  assert.match(cp, /if\s*\(props\.chatOnly\s*&&\s*boundSessionId\)\s*\{[\s\S]{0,80}currentSessionId\.value\s*=\s*boundSessionId/,
    '对话窗口：启动即绑定 URL 会话')
  const sub = cp.match(/const unsubSessionChanged[\s\S]*?permanentUnsubs\.push\(unsubSessionChanged\)/)
  assert.ok(sub, '未找到 session-changed 订阅')
  assert.match(sub[0], /if\s*\(props\.chatOnly\)\s*return/, '不跟随主窗口会话切换')
  const ensure = cp.match(/async function ensureSessionId\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(ensure, '未找到 ensureSessionId')
  assert.match(ensure[0], /if\s*\(props\.chatOnly\)\s*return\s+boundSessionId/, '对话窗口不自分配/切换会话')
})

// ── 7) 接线守卫：T-2/T-3/T-4/T-5（SessionsPane）─────────────────

test('T-2：会话列表标题栏「新建对话窗口」图标（达上限置灰）', () => {
  const p = SRC('views/sessions/SessionsPane.vue')
  const row = p.match(/<div class="sp-row">[\s\S]*?\n    <\/div>/)
  assert.ok(row, '未找到会话列表标题栏')
  assert.match(row[0], /sp-new-chat/, 'T-2 入口在列表标题栏（sp-row）内')
  assert.match(row[0], /:disabled="atLimit"/, '达上限 → 置灰')
  assert.match(row[0], /handleNewChatWindow/, '点击 → 新会话开窗')
})

test('T-3：列表项「用新窗口打开」— 主窗当前会话/达上限置灰，已开窗点击激活', () => {
  const p = SRC('views/sessions/SessionsPane.vue')
  const btn = p.match(/<!-- T-3[\s\S]*?<\/Button>/)
  assert.ok(btn, '未找到 T-3 入口')
  assert.match(btn[0], /:disabled="isChatEntryDisabled\(session\)"/, '置灰依据 = I-1 入口态')
  assert.match(btn[0], /:title="chatEntryTitle\(session\)"/, '置灰/激活原因有提示')
  assert.match(btn[0], /handleOpenChatWindow\(session\)/, '点击 → 开窗/激活')
  assert.match(p, /entryStateFor\(session && session\.session_id, currentSessionId\.value\)/,
    '置灰判定传入主窗当前会话（判定在前端）')
  const title = p.match(/function chatEntryTitle\(session\)\s*\{[\s\S]*?\n\}/)
  assert.ok(title, '未找到 chatEntryTitle')
  assert.match(title[0], /main-active[\s\S]*chat_window_main_active/, '主窗当前会话 → 置灰文案')
  assert.match(title[0], /limit[\s\S]*chat_window_limit/, '达上限 → 置灰文案')
  assert.match(title[0], /opened[\s\S]*activate_chat_window/, '已开窗 → 激活文案')
})

test('T-4 / T-5：主窗点击已绑定会话 → 不切主窗，改为激活该窗口', () => {
  const p = SRC('views/sessions/SessionsPane.vue')
  const sel = p.match(/async function handleSelect\(session\)\s*\{[\s\S]*?\n\}/)
  assert.ok(sel, '未找到 handleSelect')
  assert.ok(sel[0].indexOf('windowForSession(') >= 0, 'T-4/T-5 依据 = 已开窗口清单（只含对话窗口）')
  assert.ok(sel[0].indexOf('openSessionChatWindow(') < sel[0].indexOf('setActiveSessionID('),
    '已开窗 → 先激活窗口；不落到主窗切换（I-1 推论）')
  assert.match(p, /refreshWindows\(\)/, 'load 同时拉取 gui.window.list（置灰初始依据）')
})

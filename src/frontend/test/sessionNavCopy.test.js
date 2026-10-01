/**
 * 会话导航搬迁 + 对话导航（P3-C1，2026-09-24）前端守卫。
 *
 * 覆盖：
 *  1) 左侧导航「项目 · 知识库 · 会话」页签顺序 + 原抽屉内容迁入（SessionDrawer 已删、无残留引用）
 *  2) 复制口径（用户已定）：**不含 system**、仅对话、**全部对话（非压缩后）**、与发给 LLM 一致（json-array）
 *  3) 对话导航圆点：**专用一列**（与内容分开）、**不滚动**、上限 10；原「向上/向下箭头」浮窗已移除
 *
 * 真机行为（两行 + 三点省略号 / hover 图标在文本下方 / 圆点列不滚动）由 L4 与 test_turn_nav 覆盖。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { buildCopyMessages, serializeCopyMessages, toLlmMessage } from '../src/utils/sessionCopy.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

// ── 1) 左侧导航：会话页签 ─────────────────────────────────────────

test('导航：左侧导航含「项目 · 会话 · 记忆 · 扩展」且顺序正确', () => {
  const pane = read('views/filetree/ExplorerPane.vue')
  const idxProject = pane.indexOf("mode === 'project'")
  const idxSessions = pane.indexOf("mode === 'sessions'")
  const idxMemory = pane.indexOf("mode === 'memory'")
  const idxExtensions = pane.indexOf("mode === 'extensions'")
  assert.ok(idxProject >= 0 && idxSessions > idxProject && idxMemory > idxSessions && idxExtensions > idxMemory,
    '页签顺序应为 项目 → 会话 → 记忆 → 扩展')
  assert.match(pane, /fileTree\.mode_sessions/, '会话页签文案应走 i18n')
  assert.match(pane, /fileTree\.mode_memory/, '记忆页签文案应走 i18n')
  assert.match(pane, /fileTree\.mode_extensions/, '扩展页签文案应走 i18n')
  assert.match(pane, /<MemoryPane/, '记忆页签内容 = MemoryPane')
  assert.match(pane, /<SessionsPane/, '会话页签内容 = SessionsPane（原抽屉内容迁入）')
  assert.match(pane, /<ExtensionsPane/, '扩展页签内容 = ExtensionsPane')
  assert.match(pane, /m === 'sessions'/, 'setMode 应受理 sessions')
  assert.match(pane, /m === 'memory'/, 'setMode 应受理 memory')
  assert.match(pane, /m === 'extensions'/, 'setMode 应受理 extensions')
  // 原「知识库」「工具」两个一级分段已删除（合并进扩展页）
  assert.doesNotMatch(pane, /mode === 'knowledge'/, '「知识库」一级分段已删除')
  assert.doesNotMatch(pane, /mode === 'tools'/, '「工具」一级分段已删除')
  for (const loc of LOCALES) {
    const ft = readLocale(loc, 'fileTree.json')
    assert.ok(ft.mode_sessions, loc + ' 应有 mode_sessions 文案')
    assert.ok(ft.mode_memory, loc + ' 应有 mode_memory 文案')
    assert.ok(ft.mode_extensions, loc + ' 应有 mode_extensions 文案')
  }
})

test('导航：原会话抽屉已移除（文件删除 + 无残留引用）', () => {
  const layout = read('views/layout/MainLayout.vue')
  assert.doesNotMatch(layout, /SessionDrawer/, 'MainLayout 不得再引用抽屉')
  assert.match(layout, /EventNames\.sessionsOpen[\s\S]{0,120}filetreeModeSelect/,
    '工具栏 Sessions 入口应改为切到左侧「会话」页签')
  const names = read('events/event-names.js')
  assert.doesNotMatch(names, /sessionDrawerClose/, 'session-drawer-close 已无生产者/消费者 → 移除')
  const pane = read('views/sessions/SessionsPane.vue')
  assert.match(pane, /session-card/, '会话卡片类名沿用（回归用例可继续定位）')
  // 两行 + 超出省略号
  assert.match(pane, /text-overflow: ellipsis/, '标题/元信息行应超出省略（三个点）')
  // hover → 图标显示在文本下方（actions 行在 info 之后 + 卡片为纵向）
  assert.match(pane, /\.session-card:hover \.session-actions/, 'hover 显示操作图标')
  assert.match(pane, /flex-direction: column/, '卡片纵向排布 → 图标在文本下方')
  // 新建按钮 = 图标（第一行）
  assert.match(pane, /sp-row[\s\S]{0,400}circle-plus/, '第一行应为图标形式的新建按钮')
})

test('导航：复制为 json-array 且不含 system（copy 口径 + 取数来源）', () => {
  const head = { role: 'system', content: '【记忆库】指引' }
  const turn1 = [
    head,
    { role: 'user', kind: 'text', content: 'q1' },
    { role: 'assistant', content: 'a1', tool_calls: [{ id: 't1', type: 'function', name: 'f', arguments: '{}' }] },
    { role: 'tool', tool_call_id: 't1', content: 'r1', meta: { x: 1 } },
  ]
  const turn2 = [
    { role: 'system', content: '[已压缩早前对话] xxx' },
    { role: 'user', kind: 'text', content: 'q2' },
  ]
  const msgs = buildCopyMessages([turn1, turn2])
  assert.equal(msgs.length, 4, '应保留全部对话消息（含 tool），仅去 system')
  assert.ok(msgs.every(m => m.role !== 'system'), '不含 system')
  assert.deepEqual(msgs.map(m => m.role), ['user', 'assistant', 'tool', 'user'], '顺序 = 轮次升序（turn1 → turn2）')
  assert.deepEqual(msgs[0], { role: 'user', content: 'q1' }, '线格式只留协议字段（kind 不外发）')
  assert.ok(Array.isArray(msgs[1].tool_calls), 'assistant 的 tool_calls 保留')
  assert.equal(msgs[2].tool_call_id, 't1', 'tool 结果保留 tool_call_id')
  assert.equal(msgs[2].meta, undefined, '_meta 等内部字段不外发')
  assert.equal(msgs[3].content, 'q2', '第二轮的对话也在（全部对话）')
  // 形式 = 与发给 LLM 一致 → json-array（可 JSON.parse 回数组）
  const text = serializeCopyMessages(msgs)
  const back = JSON.parse(text)
  assert.ok(Array.isArray(back), '序列化为 json-array')
  assert.equal(back.length, msgs.length)
  assert.equal(toLlmMessage(null), null, '空消息不产出')
})

test('导航：复制取数 = 逐轮原始消息（非压缩后快照）', () => {
  const pane = read('views/sessions/SessionsPane.vue')
  assert.match(pane, /listAllTurns\(/, '应取全部轮次')
  assert.match(pane, /getTurnMessages\(/, '应按轮次读原始消息（data-session-load-messages）')
  assert.match(pane, /buildCopyMessages/, '口径由共享纯函数承担')
  const api = read('api/session.js')
  assert.match(api, /'load-messages'/, '走既有 data-session-load-messages 面（零新增消息面）')
  // 取数外形守卫（防漂移）：data-session-history 应答 = {messages:{turns,has_more}}；轮次主键 = turn_id
  assert.match(api, /r\.messages[\s\S]{0,40}turns/, 'history 应答外形 = {messages:{turns}}（wire.TurnHistoryResult）')
  assert.doesNotMatch(api, /r\.turns/, '不得直接读 r.turns（轮次在 messages.turns 之下）')
  assert.match(api, /turns\[0\]\.turn_id/, '翻页游标 = turn_id（wire.TurnToWire）')
  assert.match(pane, /turn\.turn_id/, '按 turn.turn_id 逐轮取消息')
  const copy = read('utils/sessionCopy.js')
  assert.match(copy, /role \|\| ''\) === 'system'/, '过滤 system')
})

// ── 2) 对话导航圆点 + 箭头移除 ────────────────────────────────────

test('对话导航：圆点独立成列（不滚动）且上限 10，向上/向下箭头已移除', () => {
  const ml = read('views/chat/MessageList.vue')
  assert.match(ml, /MAX_NAV_CIRCLES\s*=\s*10/, '圆点上限 = 10')
  assert.match(ml, /<div class="message-body"[^>]*>/, '应新增对话区容器（导航列/行 + 内容区）')
  const idxNav = ml.indexOf('class="turn-nav"')
  const idxList = ml.indexOf('class="message-list"')
  assert.ok(idxNav > 0 && idxList > idxNav, 'turn-nav 应在 .message-list **之外**（与内容分开、不随内容滚动）')
  assert.doesNotMatch(ml, /scroll-float/, '向上/向下箭头浮窗已移除')
  assert.doesNotMatch(ml, /polyline points="5,14 12,7 19,14"/, '上箭头 SVG 已移除')
  assert.doesNotMatch(ml, /polyline points="5,10 12,17 19,10"/, '下箭头 SVG 已移除')
  const css = ml.slice(ml.indexOf('<style scoped>'))
  assert.match(css, /\.turn-nav \{[\s\S]*?flex-shrink: 0/, '导航列为固定列（不参与滚动）')
  assert.match(css, /\.turn-nav \{[\s\S]*?position: relative/, '导航列相对定位（弹层锚点）')
  for (const loc of LOCALES) {
    const common = readLocale(loc, 'common.json')
    assert.equal(common.scroll_to_top, undefined, loc + ' 回收 scroll_to_top 文案')
    assert.equal(common.scroll_to_bottom, undefined, loc + ' 回收 scroll_to_bottom 文案')
  }
})

// ── 2b) 导航方向可配（CHAT-013-S06 / [42 §2 (154)]）──────────────
// 组件参数 `direction`（top/bottom/left/right，**无用户配置项**）：默认 right；
// 主 chat = right、子会话 = bottom；每条方向同步切 flex 方向 / 尺寸轴 / 弹层方向 / 边框方向。

test('对话导航方向：组件参数 direction + 主 chat=right / 子会话=bottom', () => {
  const ml = read('views/chat/MessageList.vue')
  assert.match(ml, /direction:\s*\{[\s\S]*?default:\s*'right'/, 'direction 组件参数默认 right')
  assert.match(ml, /'top',\s*'bottom',\s*'left',\s*'right'/, 'direction 取值 top/bottom/left/right')
  // 主 chat（ChatPanel）显式 right；子会话（SessionChat viewer）显式 bottom
  assert.match(read('views/chat/ChatPanel.vue'), /<MessageList[^>]*direction="right"/, '主 chat = right')
  assert.match(read('views/tasks/SessionChat.vue'), /<MessageList[^>]*direction="bottom"/, '子会话 = bottom')
  // 每条方向的 4 处同步切换（flex 方向 / 尺寸轴 width↔height / 弹层方向 / 边框方向）
  const css = ml.slice(ml.indexOf('<style scoped>'))
  assert.match(css, /\.message-body\.dir-left \{ flex-direction: row/, 'dir-left: flex 横排')
  assert.match(css, /\.dir-left \.turn-nav \{[\s\S]*?flex-direction: column[\s\S]*?width: 22px[\s\S]*?border-right/, 'dir-left: 竖列 + width + 右边框')
  assert.match(css, /\.dir-left \.turn-nav-pop \{ left: calc\(100% \+ 8px\)/, 'dir-left: 弹层向右弹')
  assert.match(css, /\.dir-right \.turn-nav \{[\s\S]*?flex-direction: column[\s\S]*?width: 22px[\s\S]*?border-left/, 'dir-right: 竖列 + width + 左边框')
  assert.match(css, /\.dir-right \.turn-nav-pop \{ right: calc\(100% \+ 8px\)/, 'dir-right: 弹层向左弹')
  assert.match(css, /\.message-body\.dir-top \{ flex-direction: column/, 'dir-top: flex 竖排')
  assert.match(css, /\.dir-top \.turn-nav \{[\s\S]*?flex-direction: row[\s\S]*?height: 22px[\s\S]*?border-bottom/, 'dir-top: 横行 + height + 下边框')
  assert.match(css, /\.dir-top \.turn-nav-pop \{ top: calc\(100% \+ 8px\)/, 'dir-top: 弹层向下弹')
  assert.match(css, /\.dir-bottom \.turn-nav \{[\s\S]*?flex-direction: row[\s\S]*?height: 22px[\s\S]*?border-top/, 'dir-bottom: 横行 + height + 上边框')
  assert.match(css, /\.dir-bottom \.turn-nav-pop \{ bottom: calc\(100% \+ 8px\)/, 'dir-bottom: 弹层向上弹')
})

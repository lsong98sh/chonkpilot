/**
 * 跨 view 共享状态收敛单测（UILAYOUT-1 §7 P2④：写 store 的 mq 订阅点 + getter 派生 + 双源消除守卫）。
 *
 * 口径（docs/spec/50-testing/50-测试体系.md §1）：**发送 mq 消息驱动**（`mq.emit` 走既有主题，
 * 零新增主题、零 payload 改形）→ 断言共享状态收敛结果（store 值 / getter 派生 / 退订）。
 * 另附源码守卫（前端无组件级运行器，`npm test` = node:test 直跑，与 layoutFreeMechanism 同法）：
 *   · `stores/selectionSubscribers.js` 只引用既有 `EventNames` 键（无裸主题字面量 = 零新增主题）
 *   · 四个 view 的本地状态 ref 已删（双源已消除）+ `api/file.js` 的 `_workDir` 已收敛
 */
import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { createPinia, setActivePinia } from 'pinia'
import { useSelectionStore, EXPLORER_MODES, DEFAULT_EXPLORER_MODE } from '../src/stores/selection.js'
import { registerSelectionSubscribers, unregisterSelectionSubscribers } from '../src/stores/selectionSubscribers.js'
import mq from '../src/utils/mq.js'
import { EventNames } from '../src/events/event-names.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = (rel) => readFileSync(join(HERE, '..', 'src', rel), 'utf8')

// `mq.emit` 会经 `/publish` 发后端（Node 下无同源服务）→ 统一桩掉 fetch（结果信封形状同桥）。
const realFetch = globalThis.fetch
before(() => {
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ ok: true, result: {}, errors: [] }) })
})
after(() => {
  unregisterSelectionSubscribers()
  globalThis.fetch = realFetch
})

/** 每个用例：全新 Pinia（隔离 store 实例）+ 重挂订阅（等价引导期注册）。 */
function setup() {
  unregisterSelectionSubscribers()
  setActivePinia(createPinia())
  const store = useSelectionStore()
  registerSelectionSubscribers()
  return store
}

// ── 默认值（§3.7 状态清单）─────────────────────────────────────────────

test('默认值：workDir / activeFile / activeSession / activeTask / explorerMode / activeSelection', () => {
  unregisterSelectionSubscribers()
  setActivePinia(createPinia())
  const s = useSelectionStore()
  assert.equal(s.workDir, '', 'workDir 默认空（未取 init-data 前）')
  assert.deepEqual({ ...s.activeFile }, { path: '', renderType: '' })
  assert.deepEqual({ ...s.activeSession }, { sessionId: '' })
  assert.deepEqual({ ...s.activeTask }, { sessionId: '', taskId: '' })
  assert.equal(s.explorerMode, 'project', '左侧默认「项目」模式')
  assert.equal(DEFAULT_EXPLORER_MODE, 'project')
  assert.deepEqual(EXPLORER_MODES, ['project', 'sessions', 'memory', 'extensions'])
  assert.deepEqual({ ...s.activeSelection }, { text: '', range: null })
})

// ── 写入点（mq 消息驱动 → store）───────────────────────────────────────

test('写入点①：发 session-changed → activeSession（主会话唯一真相源；空载荷 = 清空）', async () => {
  const s = setup()
  await mq.emit(EventNames.sessionChanged, { session_id: 's-1' })
  assert.equal(s.activeSession.sessionId, 's-1')
  assert.equal(s.activeSessionId, 's-1')
  assert.equal(s.hasActiveSession, true)

  await mq.emit(EventNames.sessionChanged, { session_id: null })
  assert.equal(s.activeSession.sessionId, '', '新建/无会话 → 清空（既有语义：session_id 空）')
  assert.equal(s.hasActiveSession, false)
})

test('写入点②：subsession-changed 选子会话（清 taskId）；task-detail-open 叠加（保留 sessionId）', async () => {
  const s = setup()
  await mq.emit(EventNames.taskDetailOpen, { task_id: 't-1' })
  assert.deepEqual({ ...s.activeTask }, { sessionId: '', taskId: 't-1' }, '任务详情先于子会话时仅落 taskId')
  assert.equal(s.hasActiveTask, true)
  assert.equal(s.activeTaskId, 't-1')

  await mq.emit(EventNames.subsessionChanged, { session_id: 'ss-2' })
  assert.deepEqual({ ...s.activeTask }, { sessionId: 'ss-2', taskId: '' }, '选中子会话 = 返回会话模式（清 taskId）')
  assert.equal(s.activeTaskSessionId, 'ss-2')
  assert.equal(s.hasActiveTask, false)

  await mq.emit(EventNames.taskDetailOpen, { task_id: 't-3' })
  assert.deepEqual({ ...s.activeTask }, { sessionId: 'ss-2', taskId: 't-3' }, '任务详情叠加在子会话之上')

  // 载荷缺字段 / 非对象 → 归零，不抛（既有载荷最小形态）
  await mq.emit(EventNames.subsessionChanged, {})
  assert.deepEqual({ ...s.activeTask }, { sessionId: '', taskId: '' })
})

test('写入点③：explorerMode 经 store action 受理四模式（非法取值忽略，防脏值写坏渲染分支）', () => {
  const s = setup()
  for (const m of EXPLORER_MODES) {
    s.setExplorerMode(m)
    assert.equal(s.explorerMode, m)
  }
  s.setExplorerMode('knowledge')
  assert.equal(s.explorerMode, 'extensions', '「知识库」等非法取值不得写入')
})

test('写入点④：workDir / activeFile / activeSelection（无 mq 主题的两处漏斗 + CodeView 镜像）', () => {
  const s = setup()
  s.setWorkDir('E:/proj')
  assert.equal(s.workDir, 'E:/proj')
  s.setWorkDir('')
  assert.equal(s.workDir, '')

  s.setActiveFile({ path: 'E:/proj/a.md', renderType: 'markdown' })
  assert.equal(s.activeFilePath, 'E:/proj/a.md')
  assert.equal(s.activeFileRenderType, 'markdown')
  assert.equal(s.hasActiveFile, true)
  s.setActiveFile()
  assert.equal(s.hasActiveFile, false)
  assert.deepEqual({ ...s.activeFile }, { path: '', renderType: '' })

  s.setActiveSelection({ text: '选中片段' })
  assert.equal(s.hasActiveSelection, true)
  assert.equal(s.activeSelection.text, '选中片段')
  assert.equal(s.activeSelection.range, null, 'range 一期不采集（恒 null）')
  s.setActiveSelection({ text: 'x', range: { start: 1, end: 2 } })
  assert.deepEqual({ ...s.activeSelection.range }, { start: 1, end: 2 }, '显式传入 range 时保留')
  s.setActiveSelection(undefined)
  assert.equal(s.hasActiveSelection, false)
})

test('退订：unregisterSelectionSubscribers 后消息不再写 store（订阅生命周期可回收）', async () => {
  const s = setup()
  await mq.emit(EventNames.sessionChanged, { session_id: 's-9' })
  assert.equal(s.activeSession.sessionId, 's-9')
  unregisterSelectionSubscribers()
  await mq.emit(EventNames.sessionChanged, { session_id: 's-10' })
  assert.equal(s.activeSession.sessionId, 's-9', '退订后不再受理')
})

test('reset：项目/实例边界复位六项（组件重挂不再自带归零）', async () => {
  const s = setup()
  await mq.emit(EventNames.sessionChanged, { session_id: 's-1' })
  await mq.emit(EventNames.subsessionChanged, { session_id: 'ss-1' })
  s.setWorkDir('E:/proj')
  s.setActiveFile({ path: 'E:/proj/a.md', renderType: 'markdown' })
  s.setExplorerMode('memory')
  s.setActiveSelection({ text: 'x' })

  s.reset()
  assert.equal(s.workDir, '')
  assert.deepEqual({ ...s.activeFile }, { path: '', renderType: '' })
  assert.deepEqual({ ...s.activeSession }, { sessionId: '' })
  assert.deepEqual({ ...s.activeTask }, { sessionId: '', taskId: '' })
  assert.equal(s.explorerMode, 'project')
  assert.deepEqual({ ...s.activeSelection }, { text: '', range: null })
})

test('监听 mq 消息断言：订阅者收到的载荷与既有生产者一致（未改主题/载荷形状）', async () => {
  const s = setup()
  const seen = []
  const un = mq.on(EventNames.sessionChanged, (p) => seen.push({ ...p }))
  await mq.emit(EventNames.sessionChanged, { session_id: 's-7' })
  un()
  assert.deepEqual(seen, [{ session_id: 's-7' }], '同一主题、同一载荷：订阅者只读 session_id，不新增/改写字段')
  assert.equal(s.activeSession.sessionId, 's-7')
})

// ── 零新增主题 / 落点守卫（源码）───────────────────────────────────────

test('零新增主题：selectionSubscribers 只订阅既有 EventNames 键，无裸主题字面量', () => {
  const src = SRC('stores/selectionSubscribers.js')
  assert.doesNotMatch(src, /mq\.on\(\s*['"]/, '不得用裸主题字面量（须走 EventNames）')
  const subscribed = [...src.matchAll(/mq\.on\(EventNames\.(\w+)/g)].map((m) => m[1]).sort()
  assert.deepEqual(subscribed, ['sessionChanged', 'subsessionChanged', 'taskDetailOpen'],
    '跨 view 状态的 mq 写入点仅这三个既有主题')

  // 主题字面量逐字未改（既有消费者不变）
  assert.equal(EventNames.sessionChanged, 'session-changed')
  assert.equal(EventNames.subsessionChanged, 'subsession-changed')
  assert.equal(EventNames.taskDetailOpen, 'task-detail-open')
  assert.equal(EventNames.filetreeModeSelect, 'filetree-mode-select')
  // payload 未被改写：订阅者只读既有字段名（session_id / task_id）
  assert.match(src, /strOf\(p, 'session_id'\)/)
  assert.match(src, /strOf\(p, 'task_id'\)/)
  // 禁 watch 类（含 $subscribe）
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(|\$subscribe\s*\(/)
  assert.doesNotMatch(SRC('stores/selection.js'), /\bwatch(Effect)?\s*\(|\$subscribe\s*\(/)
})

test('注册落点：main.js 于 Pinia 安装后、mount 前注册跨 view 状态订阅', () => {
  const main = SRC('main.js')
  const iPinia = main.indexOf('app.use(createPinia())')
  const iReg = main.indexOf('registerSelectionSubscribers()')
  const iMount = main.indexOf("app.mount('#app')")
  assert.ok(iPinia >= 0 && iReg > iPinia && iMount > iReg, '注册须在 createPinia 之后、mount 之前')
  assert.match(main, /from '\.\/stores\/selectionSubscribers'/)
})

test('复位落点：项目就地切换（App 重初始化序列）与登出（Toolbar）都清共享状态', () => {
  // 项目切换：reset 须早于 nextTick（整树重挂），否则新项目首帧闪现旧项目状态
  const app = SRC('App.vue')
  const handler = app.match(/setProjectReinitHandler\(async \(\) => \{[\s\S]*?\n\}\)/)
  assert.ok(handler, '未找到项目重初始化序列')
  const iReset = handler[0].indexOf('useSelectionStore().reset()')
  const iTick = handler[0].indexOf('await nextTick()')
  assert.ok(iReset >= 0 && iTick > iReset, 'reset 须在重挂（nextTick）之前')

  const tb = SRC('views/toolbar/Toolbar.vue')
  const logout = tb.match(/async function onLogout\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(logout, '未找到 onLogout')
  assert.match(logout[0], /await signOut\(\)[\s\S]{0,80}resetClaim\(\)[\s\S]{0,200}useSelectionStore\(\)\.reset\(\)/,
    '登出须复位跨 view 共享状态')
})

test('P2③ 双源已消除：相关 view / api 改为读 store，本地状态 ref 与模块变量已删', () => {
  // 注：`views/sessions/SessionsPane.vue` 的 `currentSessionId` 仍是组件内**副本**（会话行 active
  // 高亮 / 删除判定 / 开窗入口态）——已按 **UILAYOUT-1 裁决 1（2026-10-16）** 定口径：
  // `load()` 读 store 的 `activeSession.sessionId`，**store 非空时不得**用后端 `session-active-get`
  // 覆盖（store 为空才兜底）→ 见下方专测「裁决 1」。
  // ChatPanel：主会话（原 `const currentSessionId = ref(null)`）
  const chat = SRC('views/chat/ChatPanel.vue')
  assert.match(chat, /useSelectionStore\(\)/)
  assert.match(chat, /const currentSessionId = computed\(\(\) => activeSession\.value\.sessionId \|\| null\)/)
  assert.doesNotMatch(chat, /const currentSessionId = ref\(/)
  assert.doesNotMatch(chat, /currentSessionId\.value\s*=/, '组件不得再直接写共享状态')

  // SessionTree：主会话（原 `const currentTopId = ref(null)`）
  const tree = SRC('views/tasks/SessionTree.vue')
  assert.match(tree, /const currentTopId = computed\(\(\) => activeSession\.value\.sessionId \|\| null\)/)
  assert.doesNotMatch(tree, /const currentTopId = ref\(/)

  // SessionChat：子会话 / 任务详情（原两个 ref）
  const sess = SRC('views/tasks/SessionChat.vue')
  assert.match(sess, /const sessionId = computed\(\(\) => activeTask\.value\.sessionId \|\| null\)/)
  assert.match(sess, /const taskId = computed\(\(\) => activeTask\.value\.taskId \|\| null\)/)
  assert.doesNotMatch(sess, /const sessionId = ref\(/)
  assert.doesNotMatch(sess, /const taskId = ref\(/)

  // ExplorerPane：左侧模式（原 `const mode = ref('project')`）
  const pane = SRC('views/filetree/ExplorerPane.vue')
  assert.match(pane, /const \{ explorerMode: mode \} = storeToRefs\(selection\)/)
  assert.doesNotMatch(pane, /const mode = ref\(/)

  // CodeView：激活文件镜像（只读派生，唯一激活入口 setActiveKey）
  const cv = SRC('views/codeview/CodeView.vue')
  assert.match(cv, /function mirrorActiveFile\(\)/)
  assert.match(cv, /selection\.setActiveFile\(\{ path: tab\.path, renderType: tab\.renderType \}\)/)
  assert.match(cv, /function setActiveKey\(key\)/)
  assert.doesNotMatch(cv, /v-model="activeKey"/, 'TabBar 选中须经 setActiveKey（同步镜像）')
  assert.match(cv, /@update:model-value="setActiveKey"/)

  // api/file.js：模块级 `_workDir` 收敛进 store
  const file = SRC('api/file.js')
  assert.doesNotMatch(file, /\b_workDir\b/, '模块级 workDir 缓存变量应已删除')
  assert.doesNotMatch(file, /(let|var|const)\s+\w*[Ww]orkDir\s*=/, '不得残留 workDir 模块变量')
  assert.match(file, /useSelectionStore\(\)\.setWorkDir\(dir\)/, 'setWorkDir = 唯一写入漏斗')
  assert.match(file, /function currentWorkDir\(\)/)
  assert.match(file, /\[FieldKeys\.work_dir\]: currentWorkDir\(\)/)
})

// ── UILAYOUT-1 裁决 1（2026-10-16）：SessionsPane 第三份 currentSessionId → store 优先兜底 ──

test('裁决 1：SessionsPane.load() store 优先，后端 session-active-get 仅兜底（store 非空不得覆盖）', () => {
  const src = SRC('views/sessions/SessionsPane.vue')
  assert.match(src, /const selection = useSelectionStore\(\)/, '须接入共享 store（唯一真相源）')
  const m = src.match(/async function load\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(m, '未找到 SessionsPane.load()')
  const body = m[0]
  assert.match(body, /const storeSid = selection\.activeSessionId/,
    'load() 第一取值源须为 store 的 activeSession.sessionId')
  assert.match(body, /currentSessionId\.value = storeSid \|\| active\?\.session_id \|\| null/,
    '写入口径须 = store 优先、后端兜底（store 非空时不覆盖）')
  assert.doesNotMatch(body, /currentSessionId\.value\s*=\s*active\?\.session_id/,
    '不得无条件用后端 session-active-get 回写（会清掉在飞的共享会话）')
})

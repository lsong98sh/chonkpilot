/**
 * 前端聊天/任务区交互收口（2026-09-27）源码守卫。
 *
 * 覆盖：
 *   ① 〔改（UILAYOUT-1 裁决 2，2026-10-16）〕chat / task / filetree **三区去掉显隐功能** →
 *      三区始终渲染（`MainLayout.vue` 不再有 `chatOpen/filetreeOpen/taskOpen` 开关 ref、不再向
 *      `SplitPanel` 传 `visible`、不再订阅 `chat-toggle/filetree-toggle/tasks-toggle`、不再读写
 *      `layout.*Open` 持久化字段）；工具卡片入口（③b）仍复用既有主题（收到即 no-op，契约零增删改）；
 *      任务区不再「默认收起」（首屏减负口径随显隐功能一并取消）。
 *   ② 工具栏三显隐开关已移除（无 UI 入口）；MQ 主题定义保留（零增删改）。
 *   ③ 工具卡「查看任务详情」入口：显示判据 = **确有后台任务**（`message.task_id` ∪ 转后台返回的 task_id）→
 *      纯同步工具不显示；点击 = **复用既有事件**「先展开任务面板（tasks-toggle）→ 再定位
 *      （task-detail-open / subsession-changed）」，**不新增 MQ 主题**；
 *   ③c 定位高亮：SessionTree 按 node_id 滚动到可见 + ~1.2s 一次性高亮（`--accent-bg` token）；
 *   ④ 截图失败必须提示（catch / 上传失败 → `message.error`，debug 日志保留）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→ 沿用**源码守卫**法
 * （与 toolStopWiring / awaitingTaskBar 同法）；L4 行为面见 `src/test/chonkpilot-gui/systest/`。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const LOCALES = ['zh-CN', 'en-US']
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))

// ═══════════════════════════════════════════════════════════════
// ① 三区常显（显隐功能已去掉；UILAYOUT-1 裁决 2，2026-10-16）
// ═══════════════════════════════════════════════════════════════
test('① 三区常显：无显隐 ref / 无 visible 传参 / 无显隐订阅 / 不读不写 layout.*Open', () => {
  const ml = read('views/layout/MainLayout.vue')
  // 显隐开关 ref 与传参：移除
  assert.doesNotMatch(ml, /const (chatOpen|filetreeOpen|taskOpen) = ref\(/, '三区显隐 ref 须已移除')
  assert.doesNotMatch(ml, /visible:\s*(chatOpen|filetreeOpen|taskOpen)\.value/, '不得再向 SplitPanel 传显隐')
  // 显隐主题：不再订阅（主题定义保留在 event-names，见 ②）
  assert.doesNotMatch(ml, /mq\.on\(EventNames\.(chatToggle|filetreeToggle|tasksToggle)/,
    '三区显隐主题须不再订阅（主题本身零增删改）')
  // 持久化字段：不再读（旧键忽略、无迁移）/ 不再写
  assert.doesNotMatch(ml, /l\.(chatOpen|taskOpen|filetreeOpen)/, 'applyLayout 不得再读三显隐字段')
  assert.doesNotMatch(ml, /(chatOpen|taskOpen|filetreeOpen)\s*:/, '不得再读写三显隐字段（layout.<k>）')
  assert.doesNotMatch(ml, /(chatOpen|filetreeOpen|taskOpen)\.value/, '不得再持有三显隐状态')
  // 旧硬编码布局已删除（UILAYOUT-2-A）：不再有 chat/task/filetree pane 定义
  assert.doesNotMatch(ml, /\{ id: 'chat', size: effChatWidth\.value/, 'legacy chat pane 须已删除')
  assert.doesNotMatch(ml, /\{ id: 'bottom', size: taskHeight\.value/, 'legacy task pane 须已删除')
  assert.doesNotMatch(ml, /id: 'filetree',[\s\S]{0,120}visible: !narrow\.value/, 'legacy filetree pane 须已删除')
  // 唯一容器形态
  assert.match(ml, /<ChonkLayout \/>/, '只保留自由布局容器')
  assert.doesNotMatch(ml, /\bwatch(Effect)?\s*\(/, '不得使用 watch / watchEffect')
})

// ═══════════════════════════════════════════════════════════════
// ② 工具栏三显隐开关已移除（MQ 主题定义保留）
// ═══════════════════════════════════════════════════════════════
test('② 默认顶栏无三区显隐入口；event-names 三主题保留（契约零增删改）', () => {
  const tb = read('layout/persist.js')
  for (const ev of ['tasks-toggle', 'filetree-toggle', 'chat-toggle']) {
    assert.doesNotMatch(tb, new RegExp(ev), `默认顶栏不得再有三区显隐入口（${ev}）`)
  }
  // MQ 主题零增删改：定义保留（前端事件名 + 后端消息常量两侧都不动）
  const names = read('events/event-names.js')
  for (const [k, lit] of [['chatToggle', 'chat-toggle'], ['filetreeToggle', 'filetree-toggle'], ['tasksToggle', 'tasks-toggle']]) {
    assert.match(names, new RegExp('\\b' + k + ":\\s*'" + lit + "'"), `event-names 须保留既有 ${k}`)
  }
})

// ═══════════════════════════════════════════════════════════════
// ③ 工具卡「查看任务详情」
// ═══════════════════════════════════════════════════════════════
test('③ 显示判据 = 确有后台任务（message.task_id ∪ 转后台返回的 task_id）；纯同步工具不显示', () => {
  const src = read('views/chat/MessageItem.vue')
  assert.match(src, /props\.message\.task_id \|\| detachedTaskId\.value/,
    '判据须 = message.task_id ∪ 转后台返回的 task_id')
  assert.match(src, /const hasBackgroundTask = computed\(\(\) => !!taskDetailId\.value\)/, '未找到显示判据')
  assert.match(src, /<Button\s+v-if="hasBackgroundTask"/, '按钮以 hasBackgroundTask 为唯一显示条件')
  assert.match(src, /class="task-detail-btn"[\s\S]{0,120}<Icon name="list"/,
    '图标须用 list（与顶部「任务」开关同款）')
  assert.match(src, /size="mini"[\s\S]{0,60}\btext\b/, '按钮须为 size="mini" + text 形态')
  assert.match(src, /:title="\$t\('chat\.view_task_detail'\)"/, 'title 须走新增 i18n 键')
})

test('③b 点击动作 = 复用既有事件「先展开面板 → 再定位」，不新增 MQ 主题/payload 字段', () => {
  const src = read('views/chat/MessageItem.vue')
  const m = src.match(/async function locateTaskDetail\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(m, '未找到 locateTaskDetail')
  assert.match(m[0], /mq\.emit\(EventNames\.tasksToggle\)/, '面板已收起 → 先发既有 tasks-toggle 打开')
  assert.match(m[0], /mq\.emit\(EventNames\.subsessionChanged,\s*\{\s*session_id:\s*node\.session_id\s*\}\)/,
    '子会话节点 → 既有 subsession-changed{session_id}（左树选中）')
  assert.match(m[0], /mq\.emit\(EventNames\.taskDetailOpen,\s*\{\s*task_id:\s*tid\s*\}\)/,
    '任务节点 → 既有 task-detail-open{task_id}（右侧任务详情）')
  // 三个事件名均为 event-names 既有主题（未新增）
  const names = read('events/event-names.js')
  for (const k of ['tasksToggle', 'subsessionChanged', 'taskDetailOpen']) {
    assert.match(names, new RegExp('\\b' + k + ":\\s*'"), `event-names 须含既有 ${k}`)
  }
})

test('③c 定位高亮：按 node_id 滚动到可见 + ~1.2s 一次性高亮（--accent-bg token）', () => {
  const tree = read('views/tasks/SessionTree.vue')
  assert.match(tree, /async function focusRow\(nodeId\)/, '未找到 focusRow')
  assert.match(tree, /setTimeout\(\(\) => \{[\s\S]*?\},\s*1200\)/, '一次性高亮须约 1.2s 后清除（淡出）')
  assert.match(tree, /scrollIntoView\(\{\s*block:\s*'nearest'\s*\}\)/, '须滚动目标行到可见')
  assert.match(tree, /mq\.on\(EventNames\.taskDetailOpen/, '任务节点定位须订阅既有 task-detail-open')
  assert.match(tree, /querySelector\(`\[data-node-id=/, '须按 node_id 定位目标行')
  const node = read('views/tasks/SessionTreeNode.vue')
  assert.match(node, /:data-node-id="node\.node_id"/, '行须带 data-node-id 供定位')
  assert.match(node, /'var\(--accent-bg\)'/, '高亮色须走 --accent-bg token')
})

// ═══════════════════════════════════════════════════════════════
// ④ 截图失败必须提示
// ═══════════════════════════════════════════════════════════════
test('④ 截图失败/上传失败 → message.error（成功不提示；debug 日志保留；i18n 双语齐备）', () => {
  const src = read('views/chat/ChatPanel.vue')
  const hits = src.match(/message\.error\(t\('chat\.screenshot_failed'\)\)/g) || []
  assert.ok(hits.length >= 2, `截图 catch 与上传失败两处均须可见提示（实测 ${hits.length}）`)
  assert.match(src, /console\.warn\('\[ChatPanel\] screenshot failed:'/, '截图失败 debug 日志保留')
  assert.match(src, /console\.warn\('\[ChatPanel\] screenshot upload failed:'/, '上传失败 debug 日志保留')
  // 成功路径不得提示（用户口径：只有失败必须提示）
  assert.doesNotMatch(src, /message\.(error|success)\(t\('chat\.screenshot'\)\)/, '成功截图不提示')
  for (const loc of LOCALES) {
    const j = readLocale(loc, 'chat.json')
    for (const k of ['screenshot_failed', 'view_task_detail']) {
      assert.ok(typeof j[k] === 'string' && j[k].trim().length > 0, `${loc} 缺 chat.${k}`)
    }
  }
})

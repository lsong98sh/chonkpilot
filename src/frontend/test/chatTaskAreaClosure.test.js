/**
 * 前端聊天/任务区交互收口（2026-09-27）源码守卫。
 *
 * 覆盖：
 *   ① 任务区默认收起（`MainLayout.vue taskOpen = ref(false)`；**已保存值仍以保存值为准** → 持久化/恢复不变）；
 *   ② 顶部三开关（任务/文件树/聊天）「图标 + 文字」（i18n 双语齐备；元素顺序/类名不变 → test_toolbar 下标定位仍成立）；
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
// ① 任务区默认收起
// ═══════════════════════════════════════════════════════════════
test('① 任务区默认收起：taskOpen 默认 false，已保存值仍以保存值为准', () => {
  const ml = read('views/layout/MainLayout.vue')
  assert.match(ml, /const taskOpen = ref\(false\)/, '默认值须改为 false（首屏减负）')
  assert.match(ml, /if \(typeof l\.taskOpen === 'boolean'\) taskOpen\.value = l\.taskOpen/,
    'applyLayout 仍须按保存值恢复（持久化/恢复不变）')
  assert.match(ml, /taskOpen: taskOpen\.value/, 'measureLayout 仍须回写 taskOpen（保存链不断）')
  assert.match(ml, /mq\.on\(EventNames\.tasksToggle, \(\) => \{ taskOpen\.value = !taskOpen\.value/,
    '顶部开关仍是切换语义（收起态点击一次即展开）')
  assert.doesNotMatch(ml, /\bwatch(Effect)?\s*\(/, '不得使用 watch / watchEffect')
})

// ═══════════════════════════════════════════════════════════════
// ② 三开关「图标 + 文字」
// ═══════════════════════════════════════════════════════════════
test('② 任务/文件树/聊天三开关：图标 + 文字，且顺序与类名不变（下标定位仍成立）', () => {
  const tb = read('views/toolbar/Toolbar.vue')
  // 顺序保持 tasks → filetree → chat（.toolbar-right .tb-btn 下标 3/4/5）
  let last = -1
  for (const ev of ['EventNames.tasksToggle', 'EventNames.filetreeToggle', 'EventNames.chatToggle']) {
    const i = tb.indexOf(`v-mq:[${ev}].click`)
    assert.ok(i > last, `toolbar-right 顺序须保持 tasks→filetree→chat（${ev}）`)
    last = i
  }
  for (const k of ['tasks', 'filetree', 'chat']) {
    const re = new RegExp('<span class="tb-label">\\{\\{ \\$t\\(\'toolbar\\.' + k + '\'\\) \\}\\}</span>')
    assert.match(tb, re, `开关 ${k} 须内部补文字节点（i18n，仅加文字、不改类名/顺序）`)
  }
  for (const loc of LOCALES) {
    const j = readLocale(loc, 'toolbar.json')
    for (const k of ['tasks', 'filetree', 'chat']) {
      assert.ok(typeof j[k] === 'string' && j[k].trim().length > 0, `${loc} 缺 toolbar.${k}`)
    }
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

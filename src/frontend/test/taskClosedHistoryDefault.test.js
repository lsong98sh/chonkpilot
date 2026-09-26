/**
 * 「已关闭任务显示」+「history 默认不开启」批（2026-09-19，[42 §2 (125)/(126)]）前端守护。
 *
 * ① closed 显示：请求侧带 include_closed（后端默认过滤语义不变）→ 呈现（灰色 + 「已关闭」标签、
 *    同列表内、级联子节点一并可见）→ 只读（无停止/裁决/恢复动作）→ 关闭后节点保留（不再本地移除）。
 * ② history 默认关：后端只认显式 "true"；前端开关缺省为关 + 开启前确认（取消回退、不写库）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * 源码接线守卫（与 configSurface / awaitingTaskBar 同法；Go 判定读源码，同 awaitingArbitration.test.js）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoRoot = join(here, '..', '..')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readRepo = (rel) => readFileSync(join(repoRoot, rel), 'utf8')
const readLocale = (loc, name) =>
  JSON.parse(readFileSync(join(srcDir, 'locales', loc, name), 'utf8'))

const LOCALES = ['zh-CN', 'en-US']

// ── ① closed 显示 ────────────────────────────────────────────────

test('① 请求侧：loadTree / refresh 带 include_closed:true（后端默认过滤不变）', () => {
  const view = read('composables/useTaskView.js')
  const load = view.match(/async function loadTree\(topSession, mode\)\s*\{[\s\S]*?\n\}/)
  assert.ok(load, '未找到 loadTree')
  assert.match(load[0], /tasktreeReq\('list',\s*\{[\s\S]*?include_closed: true/,
    'loadTree 须带 include_closed:true')
  const rf = view.match(/async function refresh\(sessionId = '', topSession = '', opts = \{\}\)\s*\{[\s\S]*?\n\}/)
  assert.ok(rf, '未找到 refresh')
  assert.match(rf[0], /tasktreeReq\('tasks',\s*\{[\s\S]*?include_closed: true/,
    'refresh 须带 include_closed:true')
})

test('① 关闭本地保留：deleteTreeNode 标 closed、不再从列表移除', () => {
  const view = read('composables/useTaskView.js')
  const m = view.match(/async function deleteTreeNode\(nodeId\)\s*\{[\s\S]*?\n\}/)
  assert.ok(m, '未找到 deleteTreeNode')
  assert.match(m[0], /tasktreeReq\('delete',\s*\{\s*node_id:\s*nodeId\s*\}\)/, '仍走后端逻辑删除')
  assert.match(m[0], /n\.closed = true/, '节点须标 closed（保留展示）')
  assert.match(m[0], /t\.closed = true/, '运行态须标 closed')
  assert.doesNotMatch(m[0], /removeNode\(/, '关闭不再从本地移除（否则即使带 include_closed 也看不到）')
})

test('① 呈现：节点灰色 + 「已关闭」标签；只读（无停止 / 无裁决条）', () => {
  const vue = read('views/tasks/SessionTreeNode.vue')
  assert.match(vue, /:class="\{ active: node\.node_id === activeId, closed: isClosed \}"/,
    '关闭态须加 closed 类（灰色次级样式）')
  assert.match(vue, /v-if="isClosed"\s+class="node-closed-tag"/, '须显示「已关闭」标签')
  assert.match(vue, /const isClosed = computed\(\(\) => props\.node\.closed === true\)/,
    'closed 判定读节点 closed 字段')
  assert.match(vue, /const isRunning = computed\(\(\) => !isClosed\.value && status\.value === 'running'\)/,
    '已关闭节点不得显示停止动作')
  assert.match(vue, /isClosed\.value \? null : resolveAwaiting/, '已关闭节点不渲染裁决条')
  assert.match(vue, /\.node-header\.closed \.node-title/, '须有灰色样式')
})

test('① 详情：已关闭 → 只读提示、无停止/关闭动作', () => {
  const vue = read('components/task/TaskDetailView.vue')
  assert.match(vue, /const isClosed = computed\(/, '须有 closed 判定')
  assert.match(vue, /v-if="isClosed" class="td-closed-hint"/, '须显示只读提示')
  assert.match(vue, /taskView\.closed_readonly_hint/, '只读提示须走 i18n')
  const foot = vue.match(/<div class="td-footer">[\s\S]*?<\/div>/)
  assert.ok(foot, '未找到 td-footer')
  assert.match(foot[0], /<template v-else>/, '非关闭态才显示动作按钮（关闭态只读）')
})

test('① 级联子节点一并显示：closed 沿子树传播（parent_node_id 遍历）', () => {
  const view = read('composables/useTaskView.js')
  const m = view.match(/async function deleteTreeNode\(nodeId\)\s*\{[\s\S]*?\n\}/)
  assert.match(m[0], /m\.parent_node_id === cur/, '须沿 parent_node_id 级联标 closed')
})

test('① i18n：closed_label / closed_readonly_hint 双语齐备', () => {
  for (const loc of LOCALES) {
    const j = readLocale(loc, 'taskView.json')
    for (const k of ['closed_label', 'closed_readonly_hint']) {
      assert.ok(typeof j[k] === 'string' && j[k].length > 0, `${loc} taskView 缺 ${k}`)
    }
  }
})

// ── ② history 默认不开启 ─────────────────────────────────────────

test('② 后端判定：只有显式 "true" 才开启（缺失/非法/读失败 = 关闭）', () => {
  const go = readRepo('plugins/plugin-history/history.go')
  assert.match(go, /func gateFromValue\(val string\) bool \{\s*\n\s*return val == "true"/,
    '仅显式 "true" 为开启')
  assert.match(go, /return ok && enabled/, '未回读（gate 缺失）默认关闭')
  assert.match(go, /enabled := err == nil && gateFromValue\(val\)/, '读失败 / 缺失 = 关闭')
  assert.match(go, /enabled := false\s*\n\s*if ev\.List != nil/, 'refresh 默认关闭（仅显式 true 开）')
})

test('② 前端：开关默认态与后端一致（缺省=关），开启前确认（取消回退、不写库）', () => {
  const src = read('views/settings/HistoryConfig.vue')
  assert.match(src, /const historyEnabled = ref\(false\)/, '缺省必须为关')
  assert.match(src, /c\['history\.enabled'\] === 'true'/, '只有显式 true 视为开启')
  assert.doesNotMatch(src, /!== 'false'/, '不得再按「非 false 即开」判定')
  assert.match(src, /await confirm\(t\('historyConfig\.enable_confirm'\)/, '开启须先确认（副作用提示）')
  assert.match(src, /historyEnabled\.value = false\s*\n\s*return/, '取消须回退开关且不写库')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, 'HistoryConfig 不得使用 watch/watchEffect')
})

test('② i18n：historyConfig 新增键（默认关说明 + 开启确认）双语齐备', () => {
  for (const loc of LOCALES) {
    const j = readLocale(loc, 'historyConfig.json')
    for (const k of ['desc_default_off', 'enable_confirm_title', 'enable_confirm']) {
      assert.ok(typeof j[k] === 'string' && j[k].length > 0, `${loc} historyConfig 缺 ${k}`)
    }
  }
})

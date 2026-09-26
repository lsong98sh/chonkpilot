/**
 * G-45 ① + ⑤c agent 工具过滤开关语义单测（2026-09-25 拍板 A）。
 *
 * 口径（见 41 G-45 ① / 42 §2 (165)）：`filterTools` **不持久化**（门面 DTO / capfs 无该字段），
 * 后端判据 = **`tools` 非空即白名单**（**空 = 不限制**）→ 关闭过滤开关时**必须同时清空 `tools`**，
 * 否则「开关已关但数组残留」会让后端仍静默限制；打开开关**不清空**（保留用户可编辑状态）。
 * ⑤c：载入时须**按 `tools` 反推回填** `filterTools`，否则「过滤开启且 tools 非空」重载后
 * 勾选框显示为关（提示不限制）而后端仍限制 → **显示面误导**。
 *
 * 覆盖：① 关闭 → tools 清空 ② 打开 → 不清空 ③ 保存载荷中 tools 为空数组 + 落点守卫
 * ④ 载入回填 tools 非空 → filterTools true ⑤ tools 空/缺失/非法 → false
 * ⑥ 载入回填 ↔ 关闭/打开 往返自洽。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { filterToolsTogglePatch, filterToolsLoadPatch, normalizeTools } from '../src/utils/agentToolFilter.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

// ① 关闭过滤开关 → tools 被清空（空数组 = 后端「不限制」语义）
test('① 关闭过滤开关 → tools 清空为空数组', () => {
  const agent = { filterTools: true, tools: ['self_file_read', 'self_shell_run'] }
  const patch = filterToolsTogglePatch(agent)
  assert.deepEqual(patch, { filterTools: false, tools: [] })
  const next = { ...agent, ...patch }
  assert.equal(next.filterTools, false)
  assert.deepEqual(next.tools, [])
  // 纯函数：不改入参
  assert.deepEqual(agent.tools, ['self_file_read', 'self_shell_run'])
})

// ② 打开过滤开关 → 不清空（补丁不含 tools 键；既有残留选择也不丢）
test('② 打开过滤开关 → 不清空 tools', () => {
  const empty = { filterTools: false, tools: [] }
  const p1 = filterToolsTogglePatch(empty)
  assert.deepEqual(p1, { filterTools: true })
  assert.ok(!('tools' in p1), '打开时补丁不得含 tools 键（不清空）')
  assert.deepEqual({ ...empty, ...p1 }.tools, [])

  // 异常态（历史/其它入口残留）：开关关闭但 tools 非空 → 打开不得丢用户选择
  const legacy = { filterTools: false, tools: ['a', 'b'] }
  const p2 = filterToolsTogglePatch(legacy)
  assert.deepEqual({ ...legacy, ...p2 }.tools, ['a', 'b'])
})

// ③ 保存载荷中 tools 为空数组（父级替换 agent 后，保存仅剥 _key/id，tools 原样透传）
test('③ 保存载荷中 tools 为空数组', () => {
  const agent = { name: 'x', filterTools: true, tools: ['a'], llmRef: '', _key: 'k-1', id: 'tmp' }
  // 编辑页关闭开关：emit('update:agent', { ...agent, ...patch }) → 父级整对象替换
  const patched = { ...agent, ...filterToolsTogglePatch(agent) }
  // 保存映射（ScenarioEditDialog.handleSave）：仅剥 `_key`/`id`
  const stripForSave = ({ _key, id, ...rest }) => rest
  const payload = stripForSave(patched)
  assert.deepEqual(payload.tools, [])
  // 其余字段原样透传（仅清空白名单，不误伤）
  assert.equal(payload.name, 'x')
  assert.equal(payload.llmRef, '')
})

// ④ 载入回填（G-45 ⑤c）：tools 非空 → filterTools 载入为 true（显示态 = 后端白名单）
test('④ 载入回填：tools 非空 → filterTools 载入为 true', () => {
  const persisted = { name: 'x', tools: ['self_file_read', 'self_shell_run'] } // filterTools 不持久化 → 无该键
  const patch = filterToolsLoadPatch(persisted)
  assert.deepEqual(patch, { tools: ['self_file_read', 'self_shell_run'], filterTools: true })
  const loaded = { ...persisted, ...patch }
  assert.equal(loaded.filterTools, true)
  // 纯函数：不改入参（仍无 filterTools 键）
  assert.ok(!('filterTools' in persisted))
})

// ⑤ 载入回填：tools 空 / [] / 缺失 / 非法 → filterTools 载入为 false（后端不限制 → 显示为关，不再误导）
test('⑤ 载入回填：tools 空/缺失/非法 → filterTools 载入为 false', () => {
  for (const t of [undefined, null, [], '[]', 'not-json', '42', 42, {}, false, 0]) {
    const patch = filterToolsLoadPatch({ tools: t })
    assert.equal(patch.filterTools, false, `tools=${JSON.stringify(t)} 应回填为 false`)
    assert.deepEqual(patch.tools, [])
  }
  // legacy JSON 字符串数组 → 归一为数组，且判据与数组一致
  assert.deepEqual(filterToolsLoadPatch({ tools: '["a","b"]' }), { tools: ['a', 'b'], filterTools: true })
  // 无 tools 键（纯新对象）→ 不限制
  assert.deepEqual(filterToolsLoadPatch({}), { tools: [], filterTools: false })
  assert.deepEqual(filterToolsLoadPatch(null), { tools: [], filterTools: false })
  // 判据 = 元素个数（非空元素不参与过滤）
  assert.deepEqual(filterToolsLoadPatch({ tools: [''] }), { tools: [''], filterTools: true })
})

// ⑥ 往返自洽：载入回填 ↔ filterToolsTogglePatch（开 → 关 → 开）
test('⑥ 往返自洽：载入回填 ↔ 开关补丁（开→关→开，形态不丢）', () => {
  // 载入：过滤开启且 tools 非空（修复前显示为「关」= 误导）→ 回填为开
  let agent = { name: 'x', ...filterToolsLoadPatch({ tools: ['a', 'b'] }) }
  assert.deepEqual(agent, { name: 'x', tools: ['a', 'b'], filterTools: true })

  // 关：清空 tools（后端「不限制」）
  agent = { ...agent, ...filterToolsTogglePatch(agent) }
  assert.deepEqual(agent, { name: 'x', tools: [], filterTools: false })

  // 再开：不清空、tools 保持数组形态（可继续编辑）
  agent = { ...agent, ...filterToolsTogglePatch(agent) }
  assert.deepEqual(agent, { name: 'x', tools: [], filterTools: true })
  assert.ok(Array.isArray(agent.tools))

  // 该状态（开但空选）落库重载：tools 空 = 后端不限制 → 回填为关，显示与后端一致
  assert.equal(filterToolsLoadPatch(agent).filterTools, false)
  // 有选择落库重载：tools 非空 = 后端白名单 → 回填为开，显示与后端一致
  assert.equal(filterToolsLoadPatch({ tools: ['a'] }).filterTools, true)
  // 回填幂等：已回填态再次回填结果不变
  const once = { ...agent, ...filterToolsLoadPatch({ tools: ['a', 'b'] }) }
  assert.deepEqual({ ...once, ...filterToolsLoadPatch(once) }, once)
})

// 归一化：normalizeTools 是 tools→数组的唯一实现（AgentEditor / ScenarioEditDialog 共用）
test('归一化：normalizeTools 容忍 legacy 字符串、非法值统一为 []', () => {
  const arr = ['a']
  assert.equal(normalizeTools(arr), arr) // 数组原样（零拷贝）
  assert.deepEqual(normalizeTools('["a","b"]'), ['a', 'b'])
  for (const bad of [undefined, null, 'x', '1', 1, {}, [], false, 0]) {
    assert.deepEqual(normalizeTools(bad), [])
  }
})

// 落点守卫：编辑页 on-change 走同一纯函数；保存载荷仅剥前端专用键（tools 原样透传）；
// 载入路径（loadAgents）按 tools 反推回填 filterTools
test('落点守卫：AgentEditor 开关 on-change + ScenarioEditDialog 保存载荷 + 载入回填', () => {
  const editor = read('views/scenario/AgentEditor.vue')
  assert.match(editor, /@change="toggleFilterTools"/)
  assert.match(editor, /import \{ filterToolsTogglePatch, normalizeTools \} from '\.\.\/\.\.\/utils\/agentToolFilter'/)
  assert.match(editor, /const patch = filterToolsTogglePatch\(props\.agent\)/)
  assert.match(editor, /emit\('update:agent', \{ \.\.\.props\.agent, \.\.\.patch \}\)/)
  // 开关不再单独写 filterTools（须与 tools 同批写，避免中间态残留）
  assert.doesNotMatch(editor, /updateField\('filterTools'/)

  const dialog = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(dialog, /agents\.value\.map\(\(\{ _key, id, \.\.\.rest \}\) => rest\)/)
  assert.match(dialog, /import \{ filterToolsLoadPatch, normalizeTools \} from '\.\.\/\.\.\/utils\/agentToolFilter'/)
  // 载入路径：现仅 loadAgents（props.scenario.agents）一条 ——
  // 「恢复默认（回填上一级）」路径已于 2026-09-26 随场景恢复默认入口摘除而删除（37 SCEN-008）
  assert.equal((dialog.match(/\.\.\.filterToolsLoadPatch\(a\)/g) || []).length, 1)
  assert.doesNotMatch(dialog, /(async\s+)?function\s+handleRestoreDefault|await\s+resolveUpperSource\(/, '场景「恢复默认/回填上一级」不得残留实现')
  // 展示归一化复用同一实现
  assert.match(dialog, /tools: normalizeTools\(agent\.tools\)/)
})

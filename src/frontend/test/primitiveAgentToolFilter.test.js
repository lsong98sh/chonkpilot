/**
 * 智能体原语编辑器的「启用工具过滤」开关 —— 无工具智能体回弹缺陷回归（42 §2 (214)）。
 *
 * 缺陷（已实测复现）：PrimitivePanel.vue 的 `agentModel.filterTools` 由 `tools.length > 0`
 * **持续派生**，且 `onAgentUpdate` **不回写** `filterTools` → 打开**无 tools** 的智能体（如出厂
 * `代码审查.agent.md`）→ 工具页签 → 勾选「启用工具过滤」→ **立即回弹**（`.no-filter-hint` 常驻、
 * 候选树永不出现，无法展开候选去勾选第一个工具）。
 *
 * 修法（与场景侧同一口径，单源 utils/agentToolFilter）：
 *   - 开关态改为**本地 ref**（不派生）：载入时按 `tools` 反推（`filterToolsLoadPatch`）；
 *   - `onAgentUpdate` 回写 `agentFilterTools.value = !!a.filterTools`（不落 meta —— filterTools 不持久化）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 本文件 = `*.vue` 源码守卫 + 状态机纯逻辑断言。
 *
 * 覆盖：
 *  1) 根因修复守卫：filterTools 为本地态（禁派生）+ 载入回填 + update:agent 回写
 *  2) 无工具智能体：勾选开关**保持开**（不因 tools 空而回弹）+ 关闭仍生效 + 勾选首工具可保存
 *  3) 展示门控：AgentEditor 候选树 / no-filter-hint 由 filterTools 决定
 *  4) 场景侧不受影响：ScenarioEditDialog 仍为「载入一次性回填 + 整对象回写」（禁派生）
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { filterToolsLoadPatch, filterToolsTogglePatch } from '../src/utils/agentToolFilter.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

const PANEL = 'views/knowledge/PrimitivePanel.vue'
const EDITOR = 'views/scenario/AgentEditor.vue'
const DIALOG = 'views/scenario/ScenarioEditDialog.vue'

test('PrimitivePanel：filterTools 为本地态（禁派生）+ 载入回填 + update:agent 回写', () => {
  const panel = read(PANEL)
  // 单一来源：载入回填复用共享纯函数（口径与场景侧逐字一致）
  assert.match(panel, /import \{ filterToolsLoadPatch, normalizeTools \} from '\.\.\/\.\.\/utils\/agentToolFilter'/)
  // 开关态 = 本地 ref，agentModel 读该态（不再由 tools 派生）
  assert.match(panel, /const agentFilterTools = ref\(false\)/)
  assert.match(panel, /filterTools: agentFilterTools\.value,/)
  // 禁止旧缺陷写法：由 tools.length 持续派生（无工具时打开即被反推回弹）
  assert.doesNotMatch(panel, /filterTools:\s*tools\.length\s*>\s*0/)
  // 载入（fillForm）与「恢复」快照回滚：均按 tools 反推回填
  assert.match(panel, /agentFilterTools\.value = filterToolsLoadPatch\(\{ tools: \(doc\.meta \|\| \{\}\)\.tools \}\)\.filterTools/)
  assert.match(panel, /agentFilterTools\.value = filterToolsLoadPatch\(\{ tools: \(snap\.meta \|\| \{\}\)\.tools \}\)\.filterTools/)
  // AgentEditor 开关 emit update:agent → 回写本地开关态
  assert.match(panel, /agentFilterTools\.value = !!a\.filterTools/)
  // 前端铁律：不得用 watch / watchEffect
  assert.doesNotMatch(panel, /watch\(|watchEffect\(/)
})

test('无工具智能体：勾选开关保持开（状态机纯逻辑，回弹守卫）', () => {
  // 镜像 PrimitivePanel 的开关态流转：打开无 tools 的出厂智能体（代码审查.agent.md）→ 载入回填
  let agent = { tools: [], filterTools: filterToolsLoadPatch({ tools: [] }).filterTools }
  assert.equal(agent.filterTools, false, '无 tools 智能体载入时开关为关')

  // 勾选「启用工具过滤」（AgentEditor.toggleFilterTools → emit { ...agent, ...patch }，打开不清空 tools）
  agent = { ...agent, ...filterToolsTogglePatch(agent) }
  assert.equal(agent.filterTools, true, '勾选后开关必须保持开')
  assert.deepEqual(agent.tools, [], '打开开关不清空 tools（尚无选择）')
  // onAgentUpdate 回写本地态 → 开关保持开；旧实现（派生 tools.length>0）此处会 = false（回弹）
  let agentFilterTools = !!agent.filterTools
  assert.equal(agentFilterTools, true)
  assert.equal(agent.tools.length > 0, false, '旧派生口径 = false（回弹）——新本地态口径 = true（保持）')

  // 勾选首个工具（AgentEditor.toggleTool → updateField('tools', [...])）→ 候选保留、开关仍开
  agent = { ...agent, tools: [...agent.tools, 'self_file_read'] }
  agentFilterTools = !!agent.filterTools
  assert.deepEqual(agent.tools, ['self_file_read'])
  assert.equal(agentFilterTools, true)
  // 保存后重载：tools 非空 → 载入回填为开（行为与现状一致）
  assert.equal(filterToolsLoadPatch({ tools: agent.tools }).filterTools, true)

  // 关闭开关仍生效：filterTools=false 且清空 tools（后端「不限制」语义）
  const off = filterToolsTogglePatch(agent)
  assert.deepEqual(off, { filterTools: false, tools: [] })
  assert.equal(!!{ ...agent, ...off }.filterTools, false)
})

test('AgentEditor：候选树 / no-filter-hint 由 filterTools 门控（开关开 → 树可见、提示消失）', () => {
  const ed = read(EDITOR)
  // 候选树：filterTools 且候选非空 → 展示（开关开、无工具智能体展开后可见）
  assert.match(ed, /v-if="agent\.filterTools && allToolCategories\.length > 0" class="tool-tree"/)
  // 无过滤提示：仅在 filterTools=off 时展示（开关开 → 消失）
  assert.match(ed, /v-if="!agent\.filterTools" class="no-filter-hint"/)
})

test('场景侧不受影响：ScenarioEditDialog 仍为一次性回填 + 整对象回写（禁派生）', () => {
  const dlg = read(DIALOG)
  assert.match(dlg, /\.\.\.filterToolsLoadPatch\(a\)/, '载入路径仍按 tools 反推回填')
  assert.match(dlg, /agents\.value\[selectedAgentIdx\.value\] = \{[\s\S]{0,80}\.\.\.updatedAgent,/, '整对象回写（含 filterTools）')
  assert.doesNotMatch(dlg, /filterTools:\s*tools\.length\s*>\s*0/, '场景侧不得派生 filterTools')
})

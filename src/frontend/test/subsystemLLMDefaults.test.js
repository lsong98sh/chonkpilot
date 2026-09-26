/**
 * SL-5 · 用户配置 · LLM 页「子系统默认模型」5 个下拉（2026-09-25）：
 *   口径来源 = `docs/spec/40-roadmap/40-演进计划.md` §SL（SL-C3/C4/C5/C6/C9）
 *            + `docs/spec/60-reference/64-配置项一览.md` §3（5 键 usr 表）
 *            + `docs/spec/00-overview/02-配置层级.md` §5（仅 usr 层级）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ `*.vue` 源码守卫 + 词条实键校验
 * （与 llmConfigFields / uxBatch1/2/3 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']
const PAGE = 'views/config/SettingsLLMPage.vue'
const SELECT = 'components/ui/Select.vue'

// 5 键 = 子系统默认 LLM（键名/标签口径，64 §3）：
//   llm.promptOptimise 提示词优化 · llm.memory 记忆系统 · llm.compress 压缩上下文
//   llm.analysis 分析系统（备用） · llm.decision 决策系统（备用）
const KEYS = ['llm.promptOptimise', 'llm.memory', 'llm.compress', 'llm.analysis', 'llm.decision']
const STANDBY_KEYS = ['llm.analysis', 'llm.decision']

// ═══════════════════════════════════════════════════════════════
// ① 5 个下拉齐备 + 分组标题
// ═══════════════════════════════════════════════════════════════
test('① LLM 页含「子系统默认模型」分组与 5 个键（键名 + i18n 标签键）', () => {
  const src = read(PAGE)
  assert.match(src, /config\.llm\.subsystemDefaults/, '须有分组标题 config.llm.subsystemDefaults')
  for (const k of KEYS) {
    assert.ok(src.includes(`'${k}'`), `须含键名 ${k}（usr 键，64 §3）`)
    assert.ok(
      src.includes(`labelKey: 'config.llm.${k.slice('llm.'.length)}'`),
      `须含 ${k} 的 i18n 标签键`,
    )
  }
  // 5 个下拉 = 由同一 subsystems 列表 v-for 渲染（不重复手写）。
  assert.match(src, /v-for="s in subsystems"/, '5 个下拉须由 subsystems 列表渲染')
  assert.match(src, /const subsystems = \[/, '须有 subsystems 常量表')
})

// ═══════════════════════════════════════════════════════════════
// ② 空值 = 回落 defaultLLM：可选空选项 + 「跟随默认」文案
// ═══════════════════════════════════════════════════════════════
test('② 空选项可选并显示「跟随默认（{name}）」，写入空串即回落 defaultLLM', () => {
  const src = read(PAGE)
  // 空选项文案含默认 provider 名（followLabel = config.llm.followDefault + 默认名/系统默认）。
  assert.match(src, /config\.llm\.followDefault/, '空选项须用「跟随默认」文案')
  assert.match(src, /defaultLLMKey\.value \|\| t\('config\.llm\.systemDefaultName'\)/, '文案须含默认 provider 名（缺省回落「系统默认」）')
  assert.match(src, /:placeholder="followLabel"/, '空选项须绑定 followLabel')
  assert.match(src, /placeholder-selectable/, '空选项须可被选中（否则无法重置为「跟随默认」）')
  // 写：空串原样落库（键已注册 llmref；空串 = 回落 defaultLLM，SL-C3）。
  assert.match(src, /saveUserConfig\(\{\s*\[key\]: value\s*\}\)/, '写回须走既有 saveUserConfig，空串原样')
  // 读：值形态兼容（字符串 name / 旧 int 索引），走既有 config API（非新增消息主题）。
  assert.match(src, /function resolveRef\(v\)/, '须有 llmref 归一（兼容旧 int 索引）')
  assert.match(src, /typeof v === 'number' && v >= 0 && v < llms\.value\.length/, '旧 int 索引 → llms[v].name')
  assert.match(src, /from '\.\.\/\.\.\/api\/config'/, '须走既有 api/config（getUserConfig/saveUserConfig）')
})

test('② 选项来源 = usr 已配置的 provider 列表（并补当前显式值，避免悬空引用留白）', () => {
  const src = read(PAGE)
  const opts = src.slice(src.indexOf('function subsysOptions'), src.indexOf('// 保存即热生效'))
  assert.match(opts, /llms\.value\.map\(it => it\.name\)/, '选项须取自 usr llms 的 name 列表')
  assert.match(opts, /!names\.includes\(cur\)/, '当前显式值不在列表时须补入（防下拉留白）')
})

test('② 读侧展示：等于默认 provider 时显示「跟随默认」（数据层读侧已补默认值）', () => {
  const src = read(PAGE)
  const disp = src.slice(src.indexOf('function subsysDisplay'), src.indexOf('function subsysOptions'))
  assert.match(disp, /name === defaultLLMKey\.value \? '' : name/, '等于默认 provider → 显示空选项（跟随默认）')
})

// ═══════════════════════════════════════════════════════════════
// ③ 分析 / 决策 = 预留位：标「备用」，仍可配置（不加禁用）
// ═══════════════════════════════════════════════════════════════
test('③ 分析 / 决策标「备用」且保持可配置（不新增禁用逻辑）', () => {
  const src = read(PAGE)
  const standbyCount = (src.match(/standby: true/g) || []).length
  assert.equal(standbyCount, 2, 'analysis / decision 须各标 standby: true（恰 2 处）')
  for (const k of STANDBY_KEYS) {
    assert.ok(src.includes(`{ key: '${k}', labelKey: 'config.llm.${k.slice(4)}', standby: true }`), `${k} 须标 standby`)
  }
  assert.match(src, /config\.llm\.standby\b/, '备用标签须用 config.llm.standby 文案')
  assert.match(src, /config\.llm\.standbyHint/, '备用须有说明（尚未实现/预留位）')
  // 预留位仍可配置：本页不给子系统下拉加 disabled。
  assert.doesNotMatch(src, /:disabled/, '子系统下拉不得加禁用（可配、暂无消费方）')
})

// ═══════════════════════════════════════════════════════════════
// ④ 热生效（SL-C9）：无需重启；粒度 = 下一个轮次（不承诺当前轮中断）
// ═══════════════════════════════════════════════════════════════
test('④ 保存即热生效（APPLY_INSTANT）；提示生效粒度 = 下一个轮次，不承诺当前轮中断', () => {
  const src = read(PAGE)
  assert.match(src, /savedText\(t, APPLY_INSTANT\)/, '保存反馈须用「即时生效（无需重启）」口径')
  assert.doesNotMatch(src, /中断/, '不得承诺当前轮中断（生效粒度 = 下一个轮次）')
  assert.match(src, /config\.llm\.subsystemHint/, '须有生效方式提示（文案在 i18n）')
  const zh = readLocale('zh-CN', 'config.json').llm
  assert.match(zh.subsystemHint, /下一个轮次/, 'zh 提示须说明生效粒度 = 下一个轮次')
})

// ═══════════════════════════════════════════════════════════════
// ⑤ 通信与规范：零新增消息主题、禁直调 window.go、禁 watch
// ═══════════════════════════════════════════════════════════════
test('⑤ 零新增消息主题 / 不直调 window.go / 不使用 watch', () => {
  const src = read(PAGE)
  assert.doesNotMatch(src, /mq\.emit\(/, '不得新增 mq.emit 主题（读写走既有 data-user-config-* 经 api/config）')
  assert.doesNotMatch(src, /window\.go/, '禁止直调 window.go.*')
  assert.doesNotMatch(src, /\bwatch(Effect)?\(/, '禁止 watch/watchEffect（用 computed/事件）')
})

// ═══════════════════════════════════════════════════════════════
// ⑥ Select 组件：空选项可选（新增 prop，缺省行为不变）
// ═══════════════════════════════════════════════════════════════
test('⑥ Select 组件支持可选空选项（placeholderSelectable，缺省 false 行为不变）', () => {
  const src = read(SELECT)
  assert.match(src, /:disabled="!placeholderSelectable"/, '占位 option 的 disabled 须由 placeholderSelectable 控制')
  assert.match(src, /placeholderSelectable: Boolean/, '须声明 placeholderSelectable 布尔 prop（缺省 false = 旧行为）')
})

// ═══════════════════════════════════════════════════════════════
// ⑦ i18n：10 条词条双语齐备；zh 逐字
// ═══════════════════════════════════════════════════════════════
test('⑦ i18n：子系统默认模型词条双语齐备', () => {
  const need = [
    'subsystemDefaults', 'subsystemHint', 'followDefault',
    'promptOptimise', 'memory', 'compress', 'analysis', 'decision',
    'standby', 'standbyHint',
  ]
  for (const loc of LOCALES) {
    const llm = readLocale(loc, 'config.json').llm
    for (const k of need) {
      assert.ok(typeof llm[k] === 'string' && llm[k].trim().length > 0, `${loc} 缺 config.llm.${k}`)
    }
    assert.ok(llm.followDefault.includes('{name}'), `${loc} followDefault 须含 {name} 占位符`)
  }
  const zh = readLocale('zh-CN', 'config.json').llm
  assert.equal(zh.subsystemDefaults, '子系统默认模型', 'zh 分组标题须逐字一致')
  assert.equal(zh.promptOptimise, '提示词优化')
  assert.equal(zh.memory, '记忆系统')
  assert.equal(zh.compress, '压缩上下文')
  assert.equal(zh.analysis, '分析系统')
  assert.equal(zh.decision, '决策系统')
  assert.equal(zh.standby, '备用')
})

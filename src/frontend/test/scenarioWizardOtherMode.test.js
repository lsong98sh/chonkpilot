/**
 * P1（2026-11-05）：向导「其他」模式逃生舱 + 文案事实性。
 *
 * 背景：
 *   - 向导无任何 LLM 推荐调用：模式由探测二值决定，技术栈是静态默认；原文案承诺
 *     「LLM 推荐/预填」属虚假承诺 → 改为「项目扫描 + 默认值」。
 *   - 「其他」模式的「进入场景编辑」原先只调 closeDialog（死按钮）→ 关闭向导后应打开
 *     既有 ScenarioEditDialog（新建口径），保存成功广播 scenarioReload；不承诺补写
 *     project_spec.md（spec 写入仍只走向导生成路径）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 源码 / 文案守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readSrc } from './support/feSource.mjs'

const WIZARD = 'views/scenario/ScenarioWizardDialog.vue'
const LOCALES = ['zh-CN', 'en-US']

/** 取函数体（`function name(...) {` 到首个顶格 `}`）。 */
function fnBody(src, name) {
  const re = new RegExp('(?:async )?function ' + name + '\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = src.match(re)
  return m ? m[1] : null
}

test('其他模式：按钮接 enterScenarioEditor，不再直接 closeDialog', () => {
  const src = readSrc(WIZARD)
  const block = src.match(/otherModeHint[\s\S]*?otherModeEnter[\s\S]*?<\/Button>/)
  assert.ok(block, '未找到「其他」模式的进入按钮')
  assert.match(block[0], /@click="enterScenarioEditor"/, '按钮须打开场景编辑器')
  assert.doesNotMatch(block[0], /@click="closeDialog"/, '不得只关闭向导（死按钮）')
})

test('其他模式：先关向导，再打开既有 ScenarioEditDialog（新建口径）', () => {
  const src = readSrc(WIZARD)
  assert.match(src, /import \{ dialog \} from '@ckfw\/components\/dialog\/index\.js'/)
  assert.match(src, /import ScenarioEditDialog from '\.\/ScenarioEditDialog\.vue'/)

  const fn = fnBody(src, 'enterScenarioEditor')
  assert.ok(fn, '未找到 enterScenarioEditor')
  // DialogManager.close 同步卸载：先关向导可避免两层弹窗叠层。
  assert.match(fn, /closeDialog\(\)[\s\S]*dialog\.show\(h\(ScenarioEditDialog/, '须先关闭向导再打开编辑器')
  assert.match(fn, /scenario: prefill/)
  assert.match(fn, /isNew: true/, '其他模式是从零手工配置 → 新建口径')
  assert.match(fn, /mq\.emit\(EventNames\.scenarioReload, \{\}\)/, '保存成功后须广播场景刷新')
})

test('其他模式：从场景编辑器内打开的向导只关闭回退，不再叠新编辑器', () => {
  const src = readSrc(WIZARD)
  const fn = fnBody(src, 'enterScenarioEditor')
  assert.match(src, /origin: \{ type: String, default: '' \}/, '向导须接收 origin')
  assert.match(fn, /if \(props\.origin === 'editor'\) \{\s*closeDialog\(\)\s*return/, '编辑器来源须直接关闭回退')

  const edit = readSrc('views/scenario/ScenarioEditDialog.vue')
  assert.match(edit, /openScenarioWizard\(\{ origin: 'editor' \}\)/, '编辑器【向导】按钮须标记来源')
  const helper = readSrc('views/scenario/scenarioWizard.js')
  assert.match(helper, /origin: payload\.origin \|\| ''/, '打开助手须透传 origin')
})

test('向导文案不再承诺 LLM 推荐/预填（zh/en）', () => {
  for (const loc of LOCALES) {
    const s = JSON.parse(readSrc(`locales/${loc}/wizard.json`))
    for (const [path, label] of [
      ['info.banner', 'Step1 说明'],
      ['mode.banner', 'Step2 说明'],
      ['mode.defaultHint', '一键默认'],
      ['stack.bannerA', 'Step3 新构建说明'],
    ]) {
      const [group, key] = path.split('.')
      const text = s[group]?.[key] || ''
      assert.ok(text, `${loc} 缺 ${path}`)
      assert.doesNotMatch(text, /LLM|llm/, `${loc} ${label}不得再写 LLM 推荐：${text}`)
      assert.match(text, /默认|default|scan|扫描/, `${loc} ${label}须说明来源为扫描/默认值：${text}`)
    }
  }
})

test('其他模式文案不再承诺补写 project_spec.md（zh/en）', () => {
  for (const loc of LOCALES) {
    const s = JSON.parse(readSrc(`locales/${loc}/wizard.json`))
    const hint = s.stack?.otherModeHint || ''
    assert.ok(hint, `${loc} 缺 stack.otherModeHint`)
    assert.doesNotMatch(hint, /project_spec/, `${loc} otherModeHint 不得承诺补写 project_spec：${hint}`)
  }
})
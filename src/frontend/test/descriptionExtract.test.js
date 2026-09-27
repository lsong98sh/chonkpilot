/**
 * 「从正文提取描述」纯函数 + 相关落点守卫（2026-09-27 用户口径）。
 *
 * 提取规则：取正文首个非空、非 markdown 标题（#/> 开头）的段落，去行内 markdown 标记与多余空白，
 * 超长按字数截断加省略号；无可用段落 → 空串。真机（描述页签按钮）由 L4 覆盖，
 * 本文件校验纯函数行为 + 组件/文案落点。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { extractDescriptionFromContent } from '../src/utils/descriptionExtract.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

test('描述提取：跳过 markdown 标题，取首个正文段落并去行内标记', () => {
  const content = '# 标题\n\n**首**段`含`_标记_ 与 [链接](http://x) ![图](a.png)。\n\n第二段'
  assert.equal(extractDescriptionFromContent(content), '首段含标记 与 链接 图。')
  // 引用块（> 开头）同样不作为描述来源
  assert.equal(extractDescriptionFromContent('> 引用\n\n真正描述'), '真正描述')
  // 多行段落折叠为单行空白
  assert.equal(extractDescriptionFromContent('行一\n行二   行三'), '行一 行二 行三')
})

test('描述提取：超长按字数截断并追加省略号；空/纯标题/纯空白 → 空串', () => {
  const long = 'a'.repeat(250)
  const got = extractDescriptionFromContent(long)
  assert.equal(got.length, 201, '200 字 + 省略号')
  assert.equal(got, 'a'.repeat(200) + '…')
  assert.equal(extractDescriptionFromContent(long, 5), 'aaaaa…', 'limit 可配')
  assert.equal(extractDescriptionFromContent(''), '')
  assert.equal(extractDescriptionFromContent('   \n  \n '), '')
  assert.equal(extractDescriptionFromContent('# 只有标题\n\n## 还是标题'), '')
  assert.equal(extractDescriptionFromContent(undefined), '')
})

test('描述提取：落点守卫（纯函数模块 / 撑满样式 / 文案）', () => {
  // 提取逻辑为独立 utils 纯函数（node:test 可直测，不依赖 Vue）
  const mod = read('utils/descriptionExtract.js')
  assert.match(mod, /export function extractDescriptionFromContent/)
  assert.doesNotMatch(mod, /import .*vue/, '纯函数模块不依赖 Vue')
  // 描述页签按钮文案两语齐备
  for (const loc of ['zh-CN', 'en-US']) {
    const js = JSON.parse(read('locales/' + loc + '/knowledgeList.json'))
    assert.ok(js.extract_from_content && js.extract_confirm && js.extract_no_content, loc + ' 应有提取描述文案')
  }
})

test('描述提取：弹窗助手（promptList 复用 KeyValueEditor list 模式 / promptInput 多行选项）', () => {
  const mb = read('components/ui/MessageBox.js')
  assert.match(mb, /import KeyValueEditor from '\.\/KeyValueEditor\.vue'/, 'promptList 复用 KeyValueEditor')
  assert.match(mb, /export function promptList/)
  assert.match(mb, /mode: 'list'/, 'list 行编辑模式')
  assert.match(mb, /options\.multiline/, 'promptInput 支持多行 Textarea')
  const idx = read('components/ui/index.js')
  assert.match(idx, /export \{ confirm, promptInput, promptList \}/, 'promptList 对外导出')
  assert.doesNotMatch(mb, /watch\(|watchEffect\(/, '弹窗助手不使用 watch')
})

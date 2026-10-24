/**
 * vfts 分词器改 jieba（P2-10）前端面单测：
 *   ① composable useVftsDict 走 61 生成的三主题（vfts.dict.get / vfts.dict.set / vfts.reindex）；
 *   ② 〔UIREG-1〕配置页已并入 codebase 插件（前端页面删除）→ 该页组件级守卫移除；
 *   ③ 生成物 msgkeys.js 已含三主题（契约同步的机器可读证据）；
 *   ④ i18n 新增键 zh-CN / en-US 齐备。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 纯源码守卫（同 configSurface.test.js）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) =>
  JSON.parse(readFileSync(join(srcDir, 'locales', loc, name), 'utf8'))

test('① useVftsDict 走 61 三主题且无 watch', () => {
  const src = read('composables/useVftsDict.js')
  assert.match(src, /MsgTopics\.vftsDictGet/, '须用生成的主题常量 vftsDictGet')
  assert.match(src, /MsgTopics\.vftsDictSet/, '须用生成的主题常量 vftsDictSet')
  assert.match(src, /MsgTopics\.vftsReindex/, '须用生成的主题常量 vftsReindex')
  assert.match(src, /mq\.emit\(/, '须经 mq.emit 发布（无 RPC）')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, 'composable 不得使用 watch/watchEffect')
})

test('③ msgkeys.js 已生成 vfts 管理面三主题', () => {
  const keys = read('events/msgkeys.js')
  for (const t of ["'vfts.dict.get'", "'vfts.dict.set'", "'vfts.reindex'"]) {
    assert.ok(keys.includes(t), `msgkeys.js 缺主题 ${t}`)
  }
})

test('④ i18n：vfts 词典 / 重新索引键 zh-CN / en-US 齐备', () => {
  const keys = ['index_reindex', 'index_reindex_started', 'dict_section', 'dict_hint',
    'dict_dir_label', 'dict_dir_unavailable', 'dict_base_label', 'dict_word_count',
    'dict_user_placeholder', 'dict_save', 'dict_saved']
  for (const loc of ['zh-CN', 'en-US']) {
    const pc = readLocale(loc, 'projectConfig.json')
    for (const k of keys) assert.ok(pc[k], `${loc} projectConfig 缺 ${k}`)
  }
})

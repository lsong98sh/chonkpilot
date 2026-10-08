/**
 * vfts 分词器改 jieba（P2-10）前端面单测：
 *   ① composable useVftsDict 走 61 生成的三主题（vfts.dict.get / vfts.dict.set / vfts.reindex）；
 *   ② VftsConfig 配置页暴露「系统级词典」区块 + 「重新索引」按钮，且不使用 watch/watchEffect；
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

test('② VftsConfig 暴露系统级词典区块 + 重新索引按钮', () => {
  const src = read('views/settings/VftsConfig.vue')
  assert.match(src, /useVftsDict\(/, '须使用 useVftsDict composable（状态逻辑外置）')
  assert.match(src, /projectConfig\.index_reindex/, '缺「重新索引」按钮文案')
  assert.match(src, /handleReindex/, '缺重新索引入口处理')
  assert.match(src, /projectConfig\.dict_section/, '缺系统级词典区块')
  assert.match(src, /projectConfig\.dict_save/, '缺自定义词保存按钮')
  assert.match(src, /projectConfig\.dict_hint/, '缺词典口径提示（系统级/必须重建）')
  assert.match(src, /v-model="userDict"/, '缺自定义词编辑输入')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, 'VftsConfig 不得使用 watch/watchEffect')
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

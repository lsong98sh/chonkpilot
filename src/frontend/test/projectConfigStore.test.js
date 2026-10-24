/**
 * BRIDGE-2 T14 项目层：prj 配置「load 一次 + refresh 增量」store 接线守卫。
 *
 * 依据：61 §3.1（`data-prj-config-refresh`：单键 `{id,op,list?}` / 批量 `{ids,op,list}`）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { mergeUserConfig } from '../src/utils/userConfigApply.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = (p) => readFileSync(join(HERE, '..', 'src', p), 'utf8')

test('store：首次 getAllConfig 一次 + 订阅 data-prj-config-refresh 增量', () => {
  const src = SRC('composables/useProjectConfig.js')
  assert.match(src, /getAllConfig\(\)/)
  assert.match(src, /onDataRefresh\('prj-config'/)
  assert.match(src, /ensureLoaded/)
  assert.match(src, /mergeUserConfig/)
  assert.doesNotMatch(src, /\bwatch\s*\(/)
  assert.doesNotMatch(src, /setConfig|setConfigs|data-prj-config-save/)
})

test('store：refresh 带 list 时直接合并（不再发一次 list 请求）', () => {
  const src = SRC('composables/useProjectConfig.js')
  assert.match(src, /payload\.list/)
  assert.match(src, /apply\(list\)/)
})

test('索引徽标：改用 usePrjConfigSnapshot，不再自行 getAllConfig / 直接订阅 prj refresh', () => {
  const src = SRC('layout/bar-views/IndexBadgeView.vue')
  assert.match(src, /usePrjConfigSnapshot\(\)/)
  assert.doesNotMatch(src, /getAllConfig/)
  assert.doesNotMatch(src, /onDataRefresh\('prj-config'/)
})

test('merge 语义：refresh 增量有则覆盖、无则新增', () => {
  assert.deepEqual(
    mergeUserConfig(
      { 'memory.enabled': 'false', 'plugins.codebase.vfts.enabled': 'true' },
      { 'memory.enabled': 'true' },
    ),
    { 'memory.enabled': 'true', 'plugins.codebase.vfts.enabled': 'true' },
  )
})

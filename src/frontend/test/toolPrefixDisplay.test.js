/**
 * D2「工具名前缀剥离（display-only）」单测（2026-09-24）。
 *
 * 生成规则（勿改）= gateway `registry.go:288 applyPrefix`：暴露名 = `ns + <原名>`，
 * `ns` 默认 = `<节点 ID> + "_"`（self 节点 ID="self" → `self_<契约名>`；dir 节点 → `<节点名>_<原名>`）。
 * 前端据 `tools-list` 每项 `_meta.server{alias,node}`（= 节点 ID，`registry.serverInfoOf`）**反解**：
 * `utils/toolSource.js stripToolPrefix`（缺 `_meta` → 原样，不猜不误剥）。
 *
 * 展示落点（3 处，display-only）：`row.name` 仍作配置键 / `data-tool`，`:title` 保留**完整暴露名**
 * （重名可区分）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { stripToolPrefix } from '../src/utils/toolSource.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

// ① 剥前缀显示原名（self / dir 节点恒加前缀）
test('① stripToolPrefix：self_ / <节点名>_ 前缀剥离为原名', () => {
  assert.equal(stripToolPrefix('self_file_read', { alias: 'self', node: 'self' }), 'file_read')
  assert.equal(stripToolPrefix('mynode_query', { alias: 'mynode', node: 'mynode' }), 'query')
  // alias / node 任一命中即剥（二者通常同值；node 单独给出也生效）
  assert.equal(stripToolPrefix('self_x', { alias: '', node: 'self' }), 'x')
})

// ② 重名场景仍可区分：不同节点同名工具剥离后显示名相同，但 title 保留完整暴露名
test('② 重名可区分：三处展示均保留完整暴露名 title', () => {
  assert.equal(stripToolPrefix('nodeA_search', { alias: 'nodeA' }), 'search')
  assert.equal(stripToolPrefix('nodeB_search', { alias: 'nodeB' }), 'search')
  for (const rel of [
    'views/scenario/AgentEditor.vue',
    'views/config/SettingsToolAsyncPage.vue',
    'views/config/SettingsToolSandboxPage.vue',
  ]) {
    assert.match(read(rel), /:title="(tool|row)\.name"/, `${rel} 须保留完整暴露名 title（重名可区分）`)
  }
})

// ③ `_meta` 缺省 / 前缀不匹配 → 原样显示（不猜、不误剥）
test('③ 无 _meta.server 或前缀不匹配 → 原样返回', () => {
  assert.equal(stripToolPrefix('self_file_read', null), 'self_file_read')
  assert.equal(stripToolPrefix('self_file_read', {}), 'self_file_read')
  assert.equal(stripToolPrefix('file_read', { alias: 'self' }), 'file_read', '前缀不匹配不误剥')
  assert.equal(stripToolPrefix('', { alias: 'self' }), '')
})

// 展示落点：三处均剥前缀展示；配置键 / data-tool 仍用原名
test('展示落点：三处剥前缀展示，配置键与 data-tool 不变', () => {
  assert.match(read('views/scenario/AgentEditor.vue'), /stripToolPrefix\(tool\.name, tool\.server\)/)
  assert.match(read('views/config/SettingsToolAsyncPage.vue'), /stripToolPrefix\(row\.name, row\.server\)/)
  assert.match(read('views/config/SettingsToolSandboxPage.vue'), /stripToolPrefix\(row\.name, row\.server\)/)
  assert.match(read('views/config/SettingsToolAsyncPage.vue'), /:data-tool="row\.name"/)
  assert.match(read('views/config/SettingsToolSandboxPage.vue'), /:data-tool="row\.name"/)
})

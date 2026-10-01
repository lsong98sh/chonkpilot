/**
 * MCP 配置四级文件化（2026-10-01）前端守卫：
 *   ① MCP 编辑弹窗「基本信息」页签新增**级别选择**（app/user/project/prjusr；文案 = fileTree.kb_level_*），
 *      保存载荷带 `level`（落到 `<级别根>/capability/mcps/<名>.json`）；
 *   ② 宿主设置页读写改走**新数据层 mcp 域**（data-mcp-{list,save,delete} + data-mcp-refresh 刷新），
 *      不再整表替换 usr KV `mcpServers`；列表新增「级别」列；
 *   ③ 纯逻辑：DEFAULT_MCP.level 缺省 user；api/config.js 暴露 listMcpServers/saveMcpServer/deleteMcpServer；
 *   ④ i18n：config.mcp.level/levelHint + fileTree.kb_level_* 双语齐备；无 watch/watchEffect。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 纯逻辑直调 + `*.vue` 源码守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { DEFAULT_MCP } from '../src/config/defaults.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(readFileSync(join(srcDir, 'locales', loc, name), 'utf8'))
const LOCALES = ['zh-CN', 'en-US']

test('① EditMCPDialog：级别选择（四级，文案 = fileTree.kb_level_*）+ 保存写 level', () => {
  const dlg = read('views/config/EditMCPDialog.vue')
  assert.match(dlg, /<Select v-model="level" :options="levelOptions" \/>/, '基本信息页签须有级别选择器')
  assert.match(dlg, /\['app', 'user', 'project', 'prjusr'\]/, '级别选项须为四级')
  assert.match(dlg, /t\('fileTree\.kb_level_' \+ k\)/, '级别文案须复用 fileTree.kb_level_*')
  assert.match(dlg, /config\.mcp\.level/, '级别 label 须用 i18n')
  assert.match(dlg, /const level = ref\(normalizeLevel\(props\.initialData\.level\)\)/, '须按初始 level 回填')
  assert.match(dlg, /localData\.level = level\.value/, 'handleSave 须提交 level')
  assert.doesNotMatch(dlg, /\bwatch(Effect)?\s*\(/, '弹窗不得使用 watch/watchEffect')
})

test('② SettingsMCPPage：读写走 mcp 域（不再整表替换 usr KV）+ 级别列 + refresh 刷新', () => {
  const page = read('views/config/SettingsMCPPage.vue')
  assert.match(page, /import \{[^}]*listMcpServers[^}]*saveMcpServer[^}]*deleteMcpServer[^}]*\} from '\.\.\/\.\.\/api\/config'/,
    '须经 api/config 的 mcp 域函数读写')
  assert.match(page, /await listMcpServers\(\)/, '列表须读 mcp 域 list')
  assert.match(page, /await saveMcpServer\(/, '保存须走 mcp 域 save')
  assert.match(page, /await deleteMcpServer\(/, '删除须走 mcp 域 delete')
  assert.match(page, /onDataRefresh\('mcp', loadConfig\)/, '须订阅 data-mcp-refresh 即时刷新')
  assert.doesNotMatch(page, /saveUserConfig\(\{\s*mcpServers/, '不得再整表替换 usr KV mcpServers')
  // 列表新增「级别」列
  assert.match(page, /label: t\('config\.mcp\.level'\), prop: '_level'/, '列表须有「级别」列')
  assert.match(page, /t\('scenario\.level\.' \+ k\)/, '级别展示文案须复用 scenario.level.*')
  assert.doesNotMatch(page, /\bwatch(Effect)?\s*\(/, '页面不得使用 watch/watchEffect')
})

test('③ 纯逻辑：DEFAULT_MCP.level = user；api/config.js 暴露 mcp 域函数（data-mcp-*）', () => {
  assert.equal(DEFAULT_MCP.level, 'user', 'DEFAULT_MCP.level 须缺省 user')

  const api = read('api/config.js')
  assert.match(api, /export async function listMcpServers\(\)/, '缺 listMcpServers')
  assert.match(api, /export function saveMcpServer\(server, oldName, oldLevel\)/, '缺 saveMcpServer')
  assert.match(api, /export function deleteMcpServer\(name\)/, '缺 deleteMcpServer')
  assert.match(api, /dataClient\.list\('mcp'\)/, 'list 须走 data-mcp-list')
  assert.match(api, /dataClient\.save\('mcp', data\)/, 'save 须走 data-mcp-save')
  assert.match(api, /dataClient\.remove\('mcp', name\)/, 'delete 须走 data-mcp-delete')
  assert.match(api, /data\.old_name = oldName/, '改名/移级须带 old_name')
  assert.match(api, /data\.old_level = oldLevel/, '改名/移级须带 old_level')
})

test('④ i18n：config.mcp.level/levelHint + fileTree.kb_level_* 双语齐备', () => {
  for (const loc of LOCALES) {
    const cfg = readLocale(loc, 'config.json')
    assert.ok(cfg.mcp && typeof cfg.mcp.level === 'string' && cfg.mcp.level.length > 0, `${loc} 缺 config.mcp.level`)
    assert.ok(typeof cfg.mcp.levelHint === 'string' && cfg.mcp.levelHint.length > 0, `${loc} 缺 config.mcp.levelHint`)
    const ft = readLocale(loc, 'fileTree.json')
    for (const k of ['kb_level_app', 'kb_level_user', 'kb_level_project', 'kb_level_prjusr']) {
      assert.ok(typeof ft[k] === 'string' && ft[k].length > 0, `${loc} 缺 fileTree.${k}`)
    }
  }
})

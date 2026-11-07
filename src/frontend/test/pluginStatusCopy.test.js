/**
 * PLUGIN-LOAD-1 D11：插件状态文案统一（2026-11-05）。
 *
 * 口径（设计稿 §2 D11）：
 *   - 只显示状态文字：运行中 / 启动中 / 启动失败 / 已停止 / 已禁用 / 未安装；
 *   - **不加 detail 行**；
 *   - 插件页（PluginPageHost）与插件维护页（PluginManagerPage）共用同一套 i18n 词表
 *     `layoutFree.plugin.status*`，不硬编码中文。
 *
 * 前端无组件级运行器（`npm test` = node:test 直跑）→ 源码守卫 + i18n 齐备断言。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { readSrc } from './support/feSource.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const pkgSrc = join(here, '..', 'packages', 'plugin-ui', 'src')
const readPkg = (name) => readFileSync(join(pkgSrc, name), 'utf8')

const STATUS_KEYS = [
  'statusMissing', 'statusStarting', 'statusRunning', 'statusError', 'statusStopped', 'statusDisabled',
]

test('插件维护页：状态文案走 i18n 词表，不硬编码中文', () => {
  const page = readPkg('PluginManagerPage.vue')
  assert.match(page, /const \{ t \} = useI18n\(\)/, '须使用 useI18n 取 t')
  assert.match(page, /STATUS_KEY = \{/, '须用状态 → i18n key 映射')
  for (const key of ['statusStarting', 'statusRunning', 'statusError', 'statusStopped', 'statusDisabled']) {
    assert.ok(page.includes(key), `插件维护页缺状态映射 ${key}`)
  }
  assert.match(page, /t\('layoutFree\.plugin\.' \+ key\)/, '文案须走 layoutFree.plugin 词表')
  for (const zh of ['运行中', '启动中', '注册中', '降级', '失败', '已停止', '已禁用']) {
    assert.equal(page.includes(`'${zh}'`), false, `插件维护页不得硬编码「${zh}」`)
  }
})

test('插件维护页：不渲染 detail 行（D11）', () => {
  const page = readPkg('PluginManagerPage.vue')
  assert.doesNotMatch(page, /p\.detail/, '不得再渲染 p.detail')
  assert.doesNotMatch(page, /pm-status-detail/, 'detail 样式须一并删除')
})

test('插件页 / 维护页：状态覆盖一致（含 discovered/registering/stopping）', () => {
  const page = readPkg('PluginManagerPage.vue')
  const host = readPkg('PluginPageHost.vue')
  for (const status of ['discovered', 'registering', 'starting', 'running', 'degraded', 'error', 'stopping', 'stopped', 'disabled']) {
    assert.match(page, new RegExp(`\\b${status}:`), `维护页缺状态 ${status}`)
    assert.match(host, new RegExp(`\\b${status}:`), `插件页缺状态 ${status}`)
  }
  // 两个组件都不得把状态原样透出（未知状态回落展示原文是允许的，但已知状态必须映射）
  assert.match(page, /STATUS_KEY\[p\.status\]/)
  assert.match(host, /STATUS_KEY\[status\.value\]/)
})

test('i18n：layoutFree.plugin.status* 中英齐备', () => {
  for (const loc of ['zh-CN', 'en-US']) {
    const json = JSON.parse(readSrc(`locales/${loc}/layoutFree.json`))
    const plugin = json.plugin || {}
    for (const k of STATUS_KEYS) {
      assert.ok(typeof plugin[k] === 'string' && plugin[k].trim(), `${loc} 缺 layoutFree.plugin.${k}`)
    }
  }
})
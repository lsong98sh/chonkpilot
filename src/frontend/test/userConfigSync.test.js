/**
 * MW-12 / WIN-021 —— usr 配置（theme / locale）跨窗口**即时同步**纯逻辑 + 接线守卫（2026-09-25）。
 *
 * 依据：61-消息一览 §3.1（`data-user-config-changed`：payload `{data:{…}}`、**不带 instance_id**
 * = 全局投递）· 24-多窗口模型设计方案 §6.5 · 31-窗口与布局 WIN-021（S01 主题 / S02 语言）。
 *
 * 覆盖：
 *  1) 纯逻辑（可直跑）：事件载荷解析（`utils/userConfigApply.js`）—— `{data:{…}}` / JSON 字符串
 *     / 直给对象 / 空与垃圾值；取值只认非空字符串（缺键 ≠ 清空）。
 *  2) 主题字面量与 61 §3.1 一致（`events/event-names.js`）。
 *  3) 接线守卫（源码断言）：App.vue 订阅（每个窗口都应用）；Toolbar 主题态取自同一 composable；
 *     composable 只 `mq.on` 订阅 + **不回写 DB**（防 save → changed → save 自激）；
 *     `plugins/i18n.js` 的本地应用与持久化分离。
 *
 * 说明：`composables/useUserConfigSync.js` 含 `vue` / `utils/mq` 无扩展名导入 → Node ESM 不可
 * 直载，故其正确性由「纯逻辑单测 + 接线守卫」共同覆盖（与 chatWindows.test.js 同口径）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { configFromChangedEvent, themeLocaleFromConfig } from '../src/utils/userConfigApply.js'
import { EventNames } from '../src/events/event-names.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = (p) => readFileSync(join(HERE, '..', 'src', p), 'utf8')

// ── 1) 事件载荷解析（61 §3.1：payload `{data:{…}}`）──────────────────

test('载荷解析：`{data:{….}}`（服务方下行形态）→ 取内层配置对象', () => {
  assert.deepEqual(configFromChangedEvent({ data: { theme: 'dark' } }), { theme: 'dark' })
})

test('载荷解析：JSON 字符串（emitRemote 信封内含字符串）→ 解析后取内层', () => {
  assert.deepEqual(configFromChangedEvent('{"data":{"locale":"en-US"}}'), { locale: 'en-US' })
})

test('载荷解析：无 data 包裹（直给配置对象）→ 原样返回', () => {
  assert.deepEqual(configFromChangedEvent({ theme: 'nord' }), { theme: 'nord' })
})

test('载荷解析：空 / 非对象 / 坏 JSON → null（调用方跳过，不误清本窗口状态）', () => {
  for (const bad of [null, undefined, '', 'not-json', 42, true]) {
    assert.equal(configFromChangedEvent(bad), null, `bad=${JSON.stringify(bad)}`)
  }
  assert.equal(configFromChangedEvent({ data: null }), null)
})

test('取值：只认非空字符串 theme / locale（缺键 = 未携带，不动作）', () => {
  assert.deepEqual(themeLocaleFromConfig({ theme: 'dark', locale: 'en-US' }), {
    theme: 'dark',
    locale: 'en-US',
  })
  assert.deepEqual(themeLocaleFromConfig({ theme: 'dark' }), { theme: 'dark', locale: '' })
  assert.deepEqual(themeLocaleFromConfig({ locale: '' }), { theme: '', locale: '' })
  assert.deepEqual(themeLocaleFromConfig({ theme: 1, locale: null }), { theme: '', locale: '' })
  assert.deepEqual(themeLocaleFromConfig(null), { theme: '', locale: '' })
})

// ── 2) 主题字面量（与 Go 侧广播 / 61 §3.1 逐字一致）────────────────

test('主题字面量 = data-user-config-changed（61 §3.1）', () => {
  assert.equal(EventNames.userConfigChanged, 'data-user-config-changed')
})

// ── 3) 接线守卫（源码断言）───────────────────────────────────────

test('App.vue 订阅一次 useUserConfigSync（每个窗口都应用：含无 Toolbar 的对话窗口）', () => {
  const app = SRC('App.vue')
  assert.match(app, /import \{ useUserConfigSync \} from '\.\/composables\/useUserConfigSync'/)
  assert.match(app, /useUserConfigSync\(\)/)
})

test('Toolbar 主题勾选态取自同一 composable（事件 → 本窗口状态同步）', () => {
  const tb = SRC('views/toolbar/Toolbar.vue')
  assert.match(tb, /import \{ useUserConfigSync \} from '\.\.\/\.\.\/composables\/useUserConfigSync'/)
  assert.match(tb, /const \{ theme: currentTheme \} = useUserConfigSync\(\)/)
})

test('composable：mq.on 订阅 + 应用 theme/locale；**不回写 DB**（防自激）', () => {
  const c = SRC('composables/useUserConfigSync.js')
  assert.match(c, /mq\.on\(EventNames\.userConfigChanged/)
  assert.match(c, /data-theme/)
  assert.match(c, /applyLocale\(/)
  assert.ok(!/saveUIState|setLocale\(/.test(c), 'composable 不得回写 DB（save → changed → save 自激）')
})

test('plugins/i18n：本地应用（applyLocale）与持久化（setLocale）分离', () => {
  const i18n = SRC('plugins/i18n.js')
  assert.match(i18n, /export function applyLocale\(locale\)/)
  assert.match(i18n, /export function setLocale\(locale\) \{\n {2}applyLocale\(locale\)/)
})

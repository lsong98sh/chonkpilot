/**
 * PLUGIN-LOAD-1 D16/D17 · CK Store（前端全局 getter 仓库）与状态栏抽屉。
 *
 * 覆盖：
 *   ① 注册 / 读取 / 列表 / 注销；未注册 → null（不抛错）；重复注册保留先注册者；
 *   ② getter 抛错 / Promise reject → get 失败（抽屉该行显示错误，不影响其它行）；
 *   ③ `window.ckStore` 装配（iframe 经 `window.top.ckStore.get` 读取）；
 *   ④ 源码守卫：装配点（main.js）/ 状态栏内置条目 / 抽屉视图 / 视图定义登记 / 文案齐备。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readSrc } from './support/feSource.mjs'
import { register, unregister, get, listKeys, ckStore, installGlobalCkStore } from '../src/utils/ckStore.js'

test('CK Store：注册 / 读取 / 列表 / 注销', async () => {
  const off = register('test.value', () => 42)
  assert.equal(await get('test.value'), 42)
  assert.ok(listKeys().includes('test.value'))
  off()
  assert.ok(!listKeys().includes('test.value'), 'disposer 应注销该 key')

  register('test.async', async () => 'async-value')
  assert.equal(await get('test.async'), 'async-value')
  unregister('test.async')
  assert.equal(await get('test.async'), null, '未注册 → null（不抛错）')
})

test('CK Store：重复注册保留先注册者并告警', async () => {
  const warns = []
  const orig = console.warn
  console.warn = (...args) => warns.push(args.join(' '))
  try {
    const offA = register('test.dup', () => 'first')
    register('test.dup', () => 'second')
    assert.equal(await get('test.dup'), 'first')
    assert.ok(warns.some((w) => w.includes('test.dup')), '重复注册应告警')
    offA()
  } finally {
    console.warn = orig
  }
})

test('CK Store：getter 抛错 → get reject（抽屉行内展示错误）', async () => {
  register('test.boom', () => {
    throw new Error('boom')
  })
  await assert.rejects(() => get('test.boom'), /boom/)
  unregister('test.boom')
})

test('CK Store：installGlobalCkStore 挂 window.ckStore（幂等）', () => {
  const saved = globalThis.window
  globalThis.window = {}
  try {
    const a = installGlobalCkStore()
    const b = installGlobalCkStore()
    assert.equal(a, ckStore)
    assert.equal(globalThis.window.ckStore, ckStore)
    assert.equal(a, b, '重复装配保留先挂载的实例')
  } finally {
    if (saved === undefined) delete globalThis.window
    else globalThis.window = saved
  }
})

test('装配守卫：main.js 安装 CK Store 并注册示范 getter', () => {
  const main = readSrc('main.js')
  assert.ok(main.includes('installGlobalCkStore()'), 'main.js 必须装配 window.ckStore')
  for (const key of ['workspace.current', 'chat.currentSession', 'preview.currentFile']) {
    assert.ok(main.includes(`ckStore.register('${key}'`), `缺少示范 getter: ${key}`)
  }
})

test('状态栏守卫：内置 CK Store 图标（右侧常驻，不写布局文档）', () => {
  const bar = readSrc('layout/ChonkStatusBar.vue')
  assert.ok(bar.includes("viewKey: 'ck-store'"), '状态栏必须内置 ck-store 条目')
  assert.ok(bar.includes("zone: 'right'"), '内置条目应在右侧区')
  assert.ok(bar.includes(':extra-items="extraItems"'), '内置 / 插件条目经 extra-items 渲染')
})

test('抽屉守卫：列表 = 已注册 key + 值，打开求值一次 + 刷新按钮', () => {
  const view = readSrc('layout/bar-views/CkStoreView.vue')
  assert.ok(view.includes("from '../../utils/ckStore'"), '抽屉必须读 CK Store 单例')
  assert.ok(view.includes('ckStore.listKeys()'), '列表来源 = 已注册 key')
  assert.ok(view.includes('ckStore.get('), '值来源 = get')
  assert.ok(view.includes('刷新'), '必须有手动刷新按钮')
  assert.ok(view.includes('r.error'), 'getter 失败的行显示错误文本')
})

test('视图定义 / 注册表：ck-store 已登记为栏视图', () => {
  const def = readSrc('layout/view-definitions.js')
  const reg = readSrc('layout/views.registry.js')
  assert.ok(/key:\s*'ck-store'[\s\S]*?host:\s*'bar'/.test(def), 'view-definitions 应声明 host=bar')
  assert.ok(reg.includes("{ key: 'ck-store', component: CkStoreView }"), 'views.registry 应注册组件')
  for (const lang of ['zh-CN', 'en-US']) {
    assert.ok(readSrc(`locales/${lang}/layoutFree.json`).includes('"ckStore"'), `${lang} 缺少 view.ckStore 文案`)
  }
})

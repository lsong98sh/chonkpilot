/**
 * B2 · 布局恢复「启动期预取」白盒 + 接线守卫（2026-09-22）。
 *
 * 背景（决策 [42 §2] B2）：主视图（MainLayout）挂载受**实例认领**（instance-claim）门控，
 * 布局/UI 恢复用的 `gui.init-data` 过去在 MainLayout.onMounted 才发起 → 「认领往返 +
 * init-data 往返」串行；且恢复落点晚于宿主 ready（默认布局闪现）。修法 = 引导期
 * （App setup）**与认领并行**发起一次预取（`prefetchInitData`），主视图挂载时消费
 * （`loadInitDataPrefetched`）→ 恢复随首帧生效。
 *
 * 覆盖（白盒，node:test 直跑）：
 *   ① 预取成功 → 消费取到**同一结果**，且不产生第二次往返（并行预取不改变既有语义）；
 *   ② 预取失败 → 消费时**回退实时读取**（行为同改前的 `loadInitData`）；
 *   ③ 单飞（并发/重复 prefetch 共享同一次在飞请求）+ 消费一次性 + 无预取直接读取；
 *   ④ 接线守卫：`App.vue` 桌面分支在 `claim()` **之前同步**发起预取（不 await → 并行）；
 *      `MainLayout.vue` 在 onMounted 消费预取并把 `layout` 交给 `applyLayout`；
 *      `api/file.js` 的 prefetch/consume 委托同一状态机（不退回各自实现）。
 *
 * 直测对象 = **纯模块** `src/utils/initDataPrefetch.js`（状态机）；`api/file.js` 只做接线，
 * 故其正确性由 ④ 的源码守卫覆盖（`api/file.js` 含 `../utils/mq` 无扩展名导入，Node ESM 不可直载）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { createInitDataPrefetch } from '../src/utils/initDataPrefetch.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = (p) => readFileSync(join(HERE, '..', 'src', p), 'utf8')

test('B2 预取成功 → 消费取同一结果且不重复往返（并行预取不改变语义）', async () => {
  let n = 0
  const s = createInitDataPrefetch(() => Promise.resolve({ layout: { chatWidth: 500 }, ui: { locale: 'zh-CN' }, n: ++n }))
  const p1 = s.prefetch()
  const p2 = s.prefetch() // 并发/重复调用 → 共享同一次在飞请求
  assert.equal(p1, p2, 'prefetch 单飞：返回同一在飞 promise')
  assert.equal(n, 1, '预取只发生一次读取')

  const got = await s.consume()
  assert.deepEqual(got.layout, { chatWidth: 500 }, '消费取到预取结果（落盘布局值）')
  assert.deepEqual(got.ui, { locale: 'zh-CN' })
  assert.equal(n, 1, '消费不产生第二次读取（并行预取未改变往返次数）')
})

test('B2 预取失败 → 消费回退实时读取（行为同改前 loadInitData）', async () => {
  let n = 0
  const s = createInitDataPrefetch(() => {
    n += 1
    return n === 1 ? Promise.reject(new Error('boom')) : Promise.resolve({ layout: { chatWidth: 333 } })
  })
  const p = s.prefetch()
  await p.then(() => { throw new Error('预取应失败') }, () => {}) // 失败不阻断引导
  assert.equal(n, 1)
  const got = await s.consume()
  assert.deepEqual(got.layout, { chatWidth: 333 }, '失败后消费 → 实时读取兜底')
  assert.equal(n, 2, '失败路径确实重读一次')
})

test('B2 消费一次性；无预取（gui/browser 形态）直接实时读取', async () => {
  let n = 0
  const s = createInitDataPrefetch(() => Promise.resolve({ layout: { chatWidth: 400 }, n: ++n }))
  await s.consume()
  assert.equal(n, 1, '无预取 → 消费即实时读取')
  s.prefetch()
  await s.consume() // 消费预取
  await s.consume() // 已消费 → 回落实时读取
  assert.equal(n, 3, '预取消费一次 + 两次回退')
})

test('B2 接线守卫：App 桌面分支在 claim 之前同步发起预取（并行），且不 await', () => {
  const app = SRC('App.vue')
  assert.match(app, /import\s*\{\s*prefetchInitData\s*\}\s*from\s*'\.\/api\/file'/, 'App.vue 必须引入 prefetchInitData')
  const m = app.match(/if\s*\(!requireAuth\(\)\)\s*\{[\s\S]*?\n\}/)
  assert.ok(m, '未找到桌面分支 if (!requireAuth())')
  const branch = m[0]
  const iPref = branch.indexOf('prefetchInitData()')
  const iClaim = branch.indexOf('claim()')
  assert.ok(iPref >= 0, '桌面分支必须发起 init-data 预取')
  assert.ok(iClaim > iPref, '预取必须先于 claim 发起（并行，而非串行在后）')
  assert.ok(!/await\s+prefetchInitData\(\)/.test(branch), '预取不得 await（否则与认领串行，抵消优化）')
})

test('B2 接线守卫：MainLayout 在 onMounted 消费预取结果并 applyLayout', () => {
  const ml = SRC('views/layout/MainLayout.vue')
  assert.match(ml, /import\s*\{[^}]*loadInitDataPrefetched[^}]*\}\s*from\s*'\.\.\/\.\.\/api\/file'/,
    'MainLayout 必须引入 loadInitDataPrefetched')
  assert.ok(!/import\s*\{[^}]*\bloadInitData\b[^}]*\}/.test(ml), '恢复路径不得退回实时读取入口 loadInitData')
  const m = ml.match(/onMounted\s*\(\(\)\s*=>\s*\{[\s\S]*?\n\}\)/)
  assert.ok(m, '未找到 onMounted')
  assert.match(m[0], /loadInitDataPrefetched\(\)/, 'onMounted 必须消费预取')
  assert.match(m[0], /applyLayout\(r\.layout/, '预取结果的 layout 必须交给 applyLayout')
})

test('B2 接线守卫：api/file.js 的 prefetch/consume 委托同一状态机（单飞+一次性）', () => {
  const f = SRC('api/file.js')
  assert.match(f, /import\s*\{\s*createInitDataPrefetch\s*\}\s*from\s*'\.\.\/utils\/initDataPrefetch'/,
    'api/file.js 必须复用 initDataPrefetch 状态机')
  assert.match(f, /createInitDataPrefetch\(\(\)\s*=>\s*guiReq\('init-data'/, '状态机的 load = gui.init-data 往返')
  const prefetch = f.match(/export function prefetchInitData\(\)\s*\{[\s\S]*?\n\}/)
  const consume = f.match(/export function loadInitDataPrefetched\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(prefetch && /_initData\.prefetch\(\)/.test(prefetch[0]), 'prefetchInitData 必须委托 state.prefetch')
  assert.ok(consume && /_initData\.consume\(\)/.test(consume[0]), 'loadInitDataPrefetched 必须委托 state.consume')
  assert.match(f, /export function loadInitData\(\)\s*\{\s*return guiReq\('init-data', \{\}\)/, 'loadInitData 保持实时读取')
})

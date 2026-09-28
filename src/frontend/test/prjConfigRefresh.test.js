/**
 * prj-config 变更广播消费侧收敛器守卫（[41 I-138] 收敛）：
 *   - `utils/prjConfigRefresh.js`：**突发合并**（N 条广播 → 1 次重载）+ **按键过滤**（命中 `ids` 任一
 *     或回落单键 `id`）+ **保存守卫**；
 *   - `composables/usePrjConfigRefresh.js`：把合并器接到既有 `data-prj-config-refresh` 订阅；
 *   - 4 个设置页统一经该 composable（源码守卫见 uxBatch4Fix.test.js A9b）。
 *
 * 背景：`data-prj-config-save` 报文兼容**单键** `{data:{key,value}}` 与**批量** `{data:{entries}}`
 * （2026-09-28）；后端写入后广播 `data-prj-config-refresh`：单键 = 1 条 `{id, op, list}`、
 * **批量 = 1 条** `{id, ids, op, list}`（`src/lib/data/internal/config/config_facade.go`
 * 整批一次 RefreshScopedKeys）。本模块在**消费侧**兜底收敛（防零散广播仍触发多次重载）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { createPrjConfigCoalescer, makePrjKeysMatcher } from '../src/utils/prjConfigRefresh.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

/** 受控时钟：手动收集/触发调度任务（确定性断言，不依赖真实定时器）。 */
function makeClock() {
  let id = 0
  const tasks = new Map()
  return {
    schedule: (fn) => { const t = ++id; tasks.set(t, fn); return t },
    cancel: (t) => { tasks.delete(t) },
    runAll: () => { const fns = [...tasks.values()]; tasks.clear(); fns.forEach((f) => f()) },
    pending: () => tasks.size,
  }
}

test('makePrjKeysMatcher：精确 ∪ 前缀命中；缺 id 保守命中', () => {
  const match = makePrjKeysMatcher(['memory.enabled'], ['memory.category.'])
  assert.equal(match({ id: 'memory.enabled' }), true, '精确键须命中')
  assert.equal(match({ id: 'memory.category.项目概要' }), true, '前缀键须命中')
  assert.equal(match({ id: 'memory.prompt.项目概要' }), false, '未列入前缀 → 不命中')
  assert.equal(match({ id: 'codegraph.status' }), false, '无关键 → 不命中')
  assert.equal(match({}), true, '广播无键名 → 保守命中（宁可多刷不漏刷）')
  assert.equal(makePrjKeysMatcher()({ id: 'anything' }), false, '无关注键（空）→ 全部不命中')
})

test('makePrjKeysMatcher：批量广播按键集 ids（命中任一即命中；缺省回落单键 id）', () => {
  const match = makePrjKeysMatcher(['compress_token_threshold'], ['memory.category.'])
  // ids 任一精确命中（首键不在关注集内也应命中）
  assert.equal(match({ id: 'aaa', ids: ['aaa', 'compress_token_threshold'] }), true, 'ids 任一精确命中')
  // ids 任一前缀命中
  assert.equal(match({ id: 'aaa', ids: ['aaa', 'memory.category.项目概要'] }), true, 'ids 任一前缀命中')
  // ids 全部不命中 → 不命中
  assert.equal(match({ id: 'aaa', ids: ['aaa', 'bbb'] }), false, 'ids 全不命中 → 不命中')
  // 无 ids → 回落单键 id 判定（向后兼容旧广播）
  assert.equal(match({ id: 'memory.enabled' }), false, '无 ids 时回落单键 id')
  // ids 空数组 → 回落单键 id
  assert.equal(match({ id: 'compress_token_threshold', ids: [] }), true, 'ids 空数组回落单键 id')
})

test('合并：同一突发内 N 条命中广播 → 只重载 1 次（读最终快照）', () => {
  const clock = makeClock()
  let n = 0
  const c = createPrjConfigCoalescer(() => { n++ }, { schedule: clock.schedule, cancel: clock.cancel })
  c.onRefresh({ id: 'a' })
  c.onRefresh({ id: 'a' })
  c.onRefresh({ id: 'a' })
  assert.equal(clock.pending(), 1, '突发只留 1 个挂起重载')
  assert.equal(n, 0, '调度未触发前不重载')
  clock.runAll()
  assert.equal(n, 1, '突发结束后恰好重载 1 次')
  // 下一突发可再次调度（非一次性）
  c.onRefresh({ id: 'a' })
  clock.runAll()
  assert.equal(n, 2, '后续突发可再重载')
})

test('键过滤：无关键广播不触发重载', () => {
  const clock = makeClock()
  let n = 0
  const c = createPrjConfigCoalescer(() => { n++ }, {
    match: makePrjKeysMatcher(['a'], ['b.']), schedule: clock.schedule, cancel: clock.cancel,
  })
  c.onRefresh({ id: 'c' })      // 无关键
  c.onRefresh({ id: 'b.x' })    // 前缀命中
  assert.equal(clock.pending(), 1, '无关键被丢弃，仅命中项挂起')
  clock.runAll()
  assert.equal(n, 1)
})

test('保存守卫：保存中丢弃；调度后进入保存 → 触发时刻再判、跳过', () => {
  const clock = makeClock()
  let n = 0
  let saving = true
  const c = createPrjConfigCoalescer(() => { n++ }, {
    isSaving: () => saving, schedule: clock.schedule, cancel: clock.cancel,
  })
  c.onRefresh({ id: 'a' })
  assert.equal(clock.pending(), 0, '保存中 → 不安排重载')
  saving = false
  c.onRefresh({ id: 'a' })
  assert.equal(clock.pending(), 1)
  saving = true                 // 调度后、触发前重新进入保存
  clock.runAll()
  assert.equal(n, 0, '触发时刻仍保存 → 跳过（二道防线）')
})

test('dispose：取消挂起重载（组件卸载不再回调）', () => {
  const clock = makeClock()
  let n = 0
  const c = createPrjConfigCoalescer(() => { n++ }, { schedule: clock.schedule, cancel: clock.cancel })
  c.onRefresh({ id: 'a' })
  assert.equal(clock.pending(), 1)
  c.dispose()
  assert.equal(clock.pending(), 0, 'dispose 须取消挂起任务')
  clock.runAll()
  assert.equal(n, 0)
})

test('composable：接到既有 data-prj-config-refresh + 复用合并器/匹配器', () => {
  const comp = read('composables/usePrjConfigRefresh.js')
  assert.match(comp, /export function usePrjConfigRefresh/, '须导出 usePrjConfigRefresh')
  assert.match(comp, /import \{ onDataRefresh \} from '\.\.\/utils\/dataClient'/, '须复用既有订阅面')
  assert.match(comp, /onDataRefresh\('prj-config'/, '须订阅既有 data-prj-config-refresh（零新增主题）')
  assert.match(comp, /import \{ createPrjConfigCoalescer, makePrjKeysMatcher \} from '\.\.\/utils\/prjConfigRefresh'/,
    '须复用纯逻辑合并器/匹配器')
  assert.match(comp, /return \(\) => \{[\s\S]*?unsubscribe\(\)[\s\S]*?dispose\(\)/,
    '须返回退订 + 取消挂起（供 onUnmounted 收集）')
})

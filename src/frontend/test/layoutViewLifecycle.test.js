/**
 * 视图池生命周期（阶段2：过渡期保留 / 旧独有 view 销毁）—— 纯逻辑 + 源码守卫。
 *
 * 背景：WIN-027 的视图池曾把用过的 view 永久挂在「停车场」保活，切换布局后
 * 旧布局独有的 view 永不销毁（MQ 订阅 / 定时器 / iframe 泄漏）。
 * 现口径：布局结构真正切换 / 保存应用后，保留新布局需要的 key；
 * 旧独有 key 先保留 `VIEW_TRANSITION_MS`（参与 FLIP），到期无落点才从池中移除。
 *
 * 同一布局内切页签 / 切区域类型不调用保留策略 → 既有 keepalive 不受影响。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { register } from 'node:module'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

// layout-free 内部导入无扩展名 → 先注册解析钩子（与 layoutAdapter.test.js 同口径）
register('./support/vue-sfc-hooks.mjs', import.meta.url)
await new Promise((r) => setTimeout(r, 0))

const FW = '../../../../chonkpilot-framework/host/web/src'
const {
  collectViewKeys,
  collectDocViewKeys,
  createLeaf,
  createSplit,
} = await import(`${FW}/layout-free/utils/layout.js`)
const {
  VIEW_TRANSITION_MS,
  registerViewTarget,
  unregisterViewTarget,
  retainViewKeys,
  disposeViewKey,
  cancelDispose,
  pendingDisposals,
  resetViewPool,
  viewPool,
} = await import(`${FW}/layout-free/stores/viewPool.js`)

/* ── 池 key 收集 ─────────────────────────────────────── */

test('collectViewKeys：leaf.view + leaf.tabs，instanceKey null/undefined → 空串', () => {
  const single = createLeaf({ viewKey: 'explorer', instanceKey: null })
  const fixed = {
    id: 'fixed',
    type: 'leaf',
    kind: 'fixed-tabs',
    tabs: [{ viewKey: 'chat', instanceKey: null }, { viewKey: 'session', instanceKey: 'abc' }],
  }
  const free = { id: 'free', type: 'leaf', kind: 'free-tabs', tabs: [{ viewKey: 'preview' }] }
  const root = createSplit('row', [single, fixed, free])

  assert.deepEqual(collectViewKeys(root), ['explorer|', 'chat|', 'session|abc', 'preview|'])

  // 允许累积到调用方传入的 out（去重）
  const out = ['explorer|']
  assert.equal(collectViewKeys(root, out), out)
  assert.deepEqual(out, ['explorer|', 'chat|', 'session|abc', 'preview|'])
})

test('collectDocViewKeys：layout + bars + menus + toolbars + drawers 去重', () => {
  const doc = {
    layout: createSplit('row', [
      createLeaf({ viewKey: 'explorer', instanceKey: null }),
      {
        id: 'fixed',
        type: 'leaf',
        kind: 'fixed-tabs',
        tabs: [{ viewKey: 'chat', instanceKey: null }, { viewKey: 'session', instanceKey: 'abc' }],
      },
    ]),
    topToolbar: {
      items: [
        { id: 'tb_recent', kind: 'view', viewKey: 'recent-dirs', instanceKey: null },
        {
          id: 'tb_menu',
          kind: 'menu',
          children: [
            { id: 'm1', label: 'x' },
            {
              id: 'm2',
              kind: 'menu',
              children: [{ id: 'm3', kind: 'view', viewKey: 'search', instanceKey: undefined }],
            },
          ],
        },
        { id: 'tb_btn', kind: 'button' },
      ],
    },
    statusbar: {
      items: [{ id: 'sb_memory', kind: 'view', viewKey: 'memory-token', instanceKey: null }],
    },
    toolbars: [
      {
        id: 'tb_task',
        items: [
          { id: 't1', kind: 'view', viewKey: 'chat', instanceKey: null }, // 与 layout 重复
          { id: 't2', kind: 'view', viewKey: 'compress-status' },
        ],
      },
    ],
    drawers: [
      { id: 'drawer_task', tabs: [{ viewKey: 'task-detail', instanceKey: 'taskId' }] },
      { id: 'drawer_files', tabs: [{ viewKey: 'explorer', instanceKey: null }] }, // 重复
    ],
  }

  assert.deepEqual(collectDocViewKeys(doc), [
    'explorer|',
    'chat|',
    'session|abc',
    'recent-dirs|',
    'search|',
    'memory-token|',
    'compress-status|',
    'task-detail|taskId',
  ])
})

/* ── retainViewKeys ─────────────────────────────────── */

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const fakeEl = () => ({})

function seed(...keys) {
  resetViewPool()
  for (const key of keys) registerViewTarget(key, fakeEl(), {})
}

test(`VIEW_TRANSITION_MS = 240（比 FLIP 200ms 略长）`, () => {
  assert.equal(VIEW_TRANSITION_MS, 240)
})

test('retainViewKeys：在保留集合内的 key → 取消 pending dispose 并保留', async () => {
  seed('a|')
  unregisterViewTarget('a|', viewPool.targets['a|'].el)
  retainViewKeys([], { delay: 20 })
  assert.deepEqual(pendingDisposals(), ['a|'])

  const res = retainViewKeys(['a|'], { delay: 20 })
  assert.deepEqual(res.kept, ['a|'])
  assert.deepEqual(res.scheduled, [])
  assert.deepEqual(pendingDisposals(), [])

  await sleep(40)
  assert.deepEqual(viewPool.keys, ['a|'], 'overlap key 的 pending timer 须被取消，不能销毁')
  resetViewPool()
})

test('retainViewKeys：无 target 且不在集合内 → delay 后从池中移除', async () => {
  seed('old|')
  unregisterViewTarget('old|', viewPool.targets['old|'].el)
  registerViewTarget('new|', fakeEl(), {})

  const res = retainViewKeys(['new|'], { delay: 25 })
  assert.deepEqual(res.kept, ['new|'])
  assert.deepEqual(res.scheduled, ['old|'])
  assert.deepEqual(pendingDisposals(), ['old|'])
  assert.ok(viewPool.keys.includes('old|'), 'delay 之前旧 view 仍在池内')

  await sleep(50)
  assert.deepEqual(viewPool.keys, ['new|'])
  assert.deepEqual(pendingDisposals(), [])
  resetViewPool()
})

test('retainViewKeys：仍有 target 的旧 key 立即保留（运行期页签 / 抽屉）', async () => {
  seed('live-tab|')
  const res = retainViewKeys([], { delay: 20 })
  assert.deepEqual(res.kept, ['live-tab|'])
  assert.deepEqual(res.scheduled, [])
  await sleep(40)
  assert.deepEqual(viewPool.keys, ['live-tab|'])
  resetViewPool()
})

test('retainViewKeys：timer 到期前重新 register target → 不销毁', async () => {
  seed('back|')
  unregisterViewTarget('back|', viewPool.targets['back|'].el)
  retainViewKeys([], { delay: 30 })
  assert.deepEqual(pendingDisposals(), ['back|'])

  registerViewTarget('back|', fakeEl(), {})
  assert.deepEqual(pendingDisposals(), [], '重新注册须立刻取消 timer')

  await sleep(60)
  assert.deepEqual(viewPool.keys, ['back|'])
  assert.ok(viewPool.targets['back|'], '布局切回后 key 保持挂载')
  resetViewPool()
})

test('disposeViewKey：仍有 target → 拒绝销毁；无 target → 移除', () => {
  seed('busy|')
  assert.equal(disposeViewKey('busy|'), false)
  assert.deepEqual(viewPool.keys, ['busy|'])

  unregisterViewTarget('busy|', viewPool.targets['busy|'].el)
  assert.equal(disposeViewKey('busy|'), true)
  assert.deepEqual(viewPool.keys, [])
  assert.equal(disposeViewKey('busy|'), false, '重复销毁幂等')
})

test('retainViewKeys 幂等：同一 key 已有 timer 不重复排；cancelDispose 可取消', async () => {
  seed('x|')
  unregisterViewTarget('x|', viewPool.targets['x|'].el)

  retainViewKeys([], { delay: 40 })
  const second = retainViewKeys([], { delay: 40 })
  assert.deepEqual(second.scheduled, [], '已有 timer 不重复排')
  assert.deepEqual(pendingDisposals(), ['x|'])

  assert.equal(cancelDispose('x|'), true)
  assert.equal(cancelDispose('x|'), false)
  await sleep(60)
  assert.deepEqual(viewPool.keys, ['x|'], '取消后不再销毁')
  resetViewPool()
})

test('resetViewPool：清空 pending timer / keys / targets', async () => {
  seed('a|')
  unregisterViewTarget('a|', viewPool.targets['a|'].el)
  registerViewTarget('b|', fakeEl(), {})
  retainViewKeys(['b|'], { delay: 40 })
  assert.deepEqual(pendingDisposals(), ['a|'])

  resetViewPool()
  assert.deepEqual(pendingDisposals(), [])
  assert.deepEqual(viewPool.keys, [])
  assert.deepEqual(viewPool.targets, {})

  await sleep(60)
  assert.deepEqual(viewPool.keys, [], 'reset 后 timer 不得再改池')
})

/* ── 源码守卫：只在布局结构真正切换 / 保存应用时接线 ─── */

const HERE = dirname(fileURLToPath(import.meta.url))
const FW_SRC = join(HERE, '..', '..', '..', '..', 'chonkpilot-framework', 'host', 'web', 'src')
const read = (...p) => readFileSync(join(FW_SRC, ...p), 'utf8')

test('接线：profiles.apply 在 nextTick 里先 retain，再 playViewTransitions', () => {
  const src = read('layout', 'profiles.js')
  const apply = src.match(/apply\(id\)\s*\{[\s\S]*?\n    \},/)
  assert.ok(apply, '未找到 apply(id)')
  assert.match(apply[0], /nextTick\(\(\)\s*=>\s*\{[\s\S]*retainViewKeys\(collectDocViewKeys\(doc\)\)[\s\S]*playViewTransitions\(before\)/)
  const retainIdx = apply[0].indexOf('retainViewKeys(collectDocViewKeys(doc))')
  const playIdx = apply[0].indexOf('playViewTransitions(before)')
  assert.ok(retainIdx >= 0 && retainIdx < playIdx, '先 retain（取消新布局 key 的待销毁），再 FLIP')
})

test('接线：profileEditorSave 应用当前档案时 retain + FLIP（非当前档案不 retain）', () => {
  const src = read('layout', 'profiles.js')
  const save = src.match(/export async function profileEditorSave[\s\S]*?\n\}/)
  assert.ok(save, '未找到 profileEditorSave')
  assert.match(save[0], /nextTick\(\(\)\s*=>\s*\{[\s\S]*retainViewKeys\(collectDocViewKeys\(snapshot\)\)/)
  assert.match(save[0], /captureViewRects\(\)[\s\S]*applyDocToConfig\(config, snapshot\)/)
  // 非当前档案分支不应出现 retain
  const elseBranch = save[0].slice(save[0].indexOf('} else {'))
  assert.ok(!elseBranch.includes('retainViewKeys'), '只保存档案不切换时不得动视图池')
})

test('接线：saveEditor 传统保存成功后 retain（nextTick）', () => {
  const src = read('layout-free', 'stores', 'actions.js')
  assert.match(src, /import\s*\{[^}]*nextTick[^}]*\}\s*from\s*'vue'/, '需 import nextTick')
  const save = src.match(/export async function saveEditor[\s\S]*?\n\}/)
  assert.ok(save, '未找到 saveEditor')
  assert.match(
    save[0],
    /if \(res\.ok\)\s*\{[\s\S]*nextTick\(\(\)\s*=>\s*retainViewKeys\(collectDocViewKeys\(config\.live\)\)\)/,
  )
})

test('layout-free 仍无 watch / watchEffect / $subscribe（§4.2）', () => {
  const root = join(FW_SRC, 'layout-free')
  const walk = (dir, out = []) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      const p = join(dir, e.name)
      if (e.isDirectory()) walk(p, out)
      else if (/\.(vue|js|mjs)$/.test(e.name)) out.push(p)
    }
    return out
  }
  const offenders = []
  for (const f of walk(root)) {
    const text = readFileSync(f, 'utf8')
    if (/^\s*watch(Effect)?\s*\(/m.test(text)) offenders.push(f)
    for (const m of text.matchAll(/import\s*\{([^}]*)\}\s*from\s*'vue'/g)) {
      if (/(^|,)\s*watch(Effect)?\s*(,|$)/.test(m[1])) offenders.push(f)
    }
    if (/\$subscribe\s*\(/.test(text)) offenders.push(f)
  }
  assert.deepEqual(offenders, [])
})
/**
 * TabBar 页签栏同步守卫（WP5 审查修复，2026-10-08）。
 *
 * 项目硬规则：组件**不得**用 watch/watchEffect 监听 props（隐式依赖 + 性能开销）。
 * TabBar 原先以 `watch(() => props.tabs, …)` 驱动内部顺序同步与溢出重判 —— 改为：
 * `displayTabs` 计算属性已反应式派生自 props.tabs，组件因此重渲染后由 **onUpdated**
 * 生命周期调用 `syncOrder()` + `recompute()`（与 watch 等价，但无隐式 props 依赖）。
 *
 * 前端无组件级运行器（`npm test` = node:test 直跑）→ 以源码守卫锁定。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readFileSync(join(here, '..', 'src', rel), 'utf8')

test('TabBar：不得 watch props（硬规则），改由 onUpdated 驱动同步', () => {
  const src = read('components/tabs/TabBar.vue')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, 'TabBar 不得使用 watch/watchEffect 监听 props')
  assert.match(src, /onUpdated\(\(\) => \{ syncOrder\(\); recompute\(\) \}\)/,
    'props.tabs 变化须经 onUpdated（重渲染后）触发 syncOrder + recompute')
  // 幂等：无变化不写 orderKeys（否则 onUpdated 内自更新会成环）
  const sync = src.match(/function syncOrder\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(sync, '未找到 syncOrder')
  assert.match(sync[0], /if \(next\.length === cur\.length && next\.every/, 'syncOrder 须幂等（无变化不写）')
})

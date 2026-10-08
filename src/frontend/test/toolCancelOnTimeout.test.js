/**
 * 「超时自动取消」per-tool 选项（usr 键 `tool_async.<工具>.cancel_on_timeout`，配置⑤ —— I-83
 * B1/B4；规格见 `docs/spec/10-architecture/18-工具异步超时与取消.md` §7）。
 *
 * 语义（与后端同源，改一处须同步另一处）：
 *   - 仅 **数值 > 0** 写库（生效秒数）；**留空 / 0 = 关闭 → 不写该字段**（默认 0 = 不取消）；
 *   - 仅 gateway 消费（到超时点直接取消，不等用户裁决），**不透出契约 `_meta`**；
 *   - 终止动作按执行线：spawned = kill + respawn · 内嵌执行器 = 协作式 · 纯远程 = 仅断请求。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 页面 / locale 源码守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']
const PAGE = 'views/config/SettingsToolAsyncPage.vue'

test('① 列与键：数值输入列 + usr 键 tool_async / cancel_on_timeout', () => {
  const src = read(PAGE)
  assert.match(src, /#cancelTimeout=/, '须有「超时自动取消」列槽')
  assert.match(src, /cancel_on_timeout/, '须绑定 cancel_on_timeout')
  assert.match(src, /const CFG_KEY = 'tool_async'/, 'usr 键须为 tool_async（零消息面变更）')
})

test('② 仅 > 0 写库；留空 / 0 = 关闭（不写库、清显示）', () => {
  const src = read(PAGE)
  // 提交（buildEntry）：仅 > 0 才落键
  assert.match(src, /if \(Number\.isFinite\(cot\) && cot > 0\) e\.cancel_on_timeout = cot/,
    '仅 >0 才写库')
  // 失焦：空 / 0 → 清显示（不写库）
  assert.match(src, /if \(raw === '' \|\| raw === null \|\| raw === undefined \|\| n === 0\) \{[\s\S]{0,80}row\.cancelOnTimeout = ''/,
    '空 / 0 须清显示（关闭）')
  // 回读：仅 > 0 显示
  assert.match(src, /Number\(u\.cancel_on_timeout\) > 0/, '回读仅 >0 显示')
})

test('③ i18n 双语齐备且非空（列名 / 占位 / 提示）', () => {
  for (const loc of LOCALES) {
    const cfg = readLocale(loc, 'config.json')
    for (const k of ['cancelOnTimeout', 'cancelOnTimeoutPlaceholder', 'cancelOnTimeoutHint']) {
      const v = cfg?.toolAsync?.[k]
      assert.ok(typeof v === 'string' && v.trim(), `${loc} 缺 config.toolAsync.${k}`)
    }
  }
})

test('④ 规范守卫：本页无 watch / watchEffect（composables 优先）', () => {
  assert.doesNotMatch(read(PAGE), /watch\(|watchEffect\(/)
})

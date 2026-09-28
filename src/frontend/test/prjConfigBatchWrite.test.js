/**
 * 项目配置**批量写**（2026-09-28，[61-消息一览 §3.1]）前端守卫：
 *   - `api/config.js` 的 `setConfigs(entries)`：一次调用 = **1 条** `data-prj-config-save`
 *     （报文 `{data:{entries:{…}}}`）；`setConfig` 单键路径**不变**（`{data:{key,value}}`）；
 *   - 4 个设置页（Context / Codegraph / Vfts / History）保存路径改**一次批量写**
 *     （1 次保存请求 → 后端 1 条 refresh），不得再逐键 `setConfig`。
 *
 * 前端无组件级运行器（`npm test` = node:test 直跑）、且前端模块用无扩展名 import（node ESM 不可解析）
 * → 沿用本项目 `*.js`/`*.vue` **源码守卫**口径（与 historyCheckpoint / configSurface 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

/** 取函数体（`function name(...) {` 到首个顶格 `}`）。 */
function fnBody(src, name) {
  const re = new RegExp('(?:async )?function ' + name + '\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = src.match(re)
  return m ? m[1] : null
}

test('api/config.js：setConfigs 一次批量写（报文 {data:{entries}}）；setConfig 单键不变', () => {
  const src = read('api/config.js')
  const batch = fnBody(src, 'setConfigs')
  assert.ok(batch, '未找到 setConfigs')
  assert.match(batch, /dataClient\.save\('prj-config', \{ entries \}\)/, 'setConfigs 须一次 save（报文 {data:{entries}}）')
  const single = fnBody(src, 'setConfig')
  assert.ok(single, '未找到 setConfig')
  assert.match(single, /dataClient\.save\('prj-config', \{ key, value \}\)/, 'setConfig 单键报文不得改动')
  // dataClient.save = 1 次 dataRequest（= 1 条 data-prj-config-save 请求）
  const dc = read('utils/dataClient.js')
  assert.match(dc, /save: \(domain, data, opts\) => dataRequest\(domain, 'save', \{ data \}, opts\)/, 'save 须为单次请求')
})

// ── 4 个设置页：保存路径 = 一次批量写 ──────────────────────────────
// keysFn = 承载批量键字面量的函数（vfts 把「改动的键」收集抽在 collectIndexChanges）。
const PAGES = [
  { file: 'views/settings/ContextConfig.vue', fn: 'handleSave', keysFn: 'handleSave', keys: ["'memory.enabled'"] },
  { file: 'views/settings/CodegraphConfig.vue', fn: 'handleIndexSave', keysFn: 'handleIndexSave', keys: ["'codegraph.exts'", "'codegraph.skip-dirs'", "'codegraph.stack-gitignore'"] },
  { file: 'views/settings/VftsConfig.vue', fn: 'handleIndexSave', keysFn: 'collectIndexChanges', keys: ["'vfts.exts'", "'vfts.skip-dirs'", "'vfts.stack-gitignore'"] },
  { file: 'views/settings/HistoryConfig.vue', fn: 'handleRetentionSave', keysFn: 'handleRetentionSave', keys: ["'history.checkpoint_keep'", "'history.checkpoint_ttl_days'"] },
]

test('4 设置页：保存路径走一次批量写 setConfigs（1 次保存请求 → 1 条 refresh）', () => {
  for (const { file, fn, keysFn, keys } of PAGES) {
    const src = read(file)
    assert.match(src, /import \{[^}]*\bsetConfigs\b[^}]*\} from '\.\.\/\.\.\/api\/config'/,
      `${file} 须从 api/config 引入 setConfigs`)
    const body = fnBody(src, fn)
    assert.ok(body, `${file} 未找到 ${fn}`)
    const writes = (body.match(/setConfigs\(/g) || []).length
    assert.equal(writes, 1, `${file} ${fn} 须恰好 1 次 setConfigs（批量写）`)
    assert.doesNotMatch(body, /await setConfig\(/, `${file} ${fn} 不得逐键 setConfig（须批量写）`)
    const keyBody = fnBody(src, keysFn)
    assert.ok(keyBody, `${file} 未找到 ${keysFn}`)
    for (const k of keys) {
      assert.ok(keyBody.includes(k), `${file} ${keysFn} 批量 entries 缺键 ${k}`)
    }
  }
})

test('信号语义键仍单独写（codegraph.action / history.clear 不被并入批量）', () => {
  // 动作信号键保持单键写 → 同值重复写仍各发 1 条 refresh（插件据此执行动作）
  assert.match(read('views/settings/CodegraphConfig.vue'), /return setConfig\('codegraph\.action', action\)/,
    'codegraph.action 仍单独写（信号键）')
  assert.match(read('views/settings/HistoryConfig.vue'),
    /setConfig\('history\.clear', JSON\.stringify\(\{ ts: new Date\(\)\.toISOString\(\), session: sessionSlug\.value \}\)\)/,
    'history.clear 仍单独写（信号键；值 = JSON {ts,session}，只清本会话链）')
})

test('VftsConfig：清空项仍走 delete（回落引擎默认语义不变）', () => {
  const src = read('views/settings/VftsConfig.vue')
  const body = fnBody(src, 'handleIndexSave')
  assert.ok(body, '未找到 handleIndexSave')
  assert.match(body, /deleteConfig\(k\)/, '清空项须走 deleteConfig（保留「清空 = 删键回落默认」语义）')
  const collect = fnBody(src, 'collectIndexChanges')
  assert.ok(collect, '未找到 collectIndexChanges')
  assert.match(collect, /clears\.push\(it\.key\)/, '空串文本项须入 clears（删键）')
})

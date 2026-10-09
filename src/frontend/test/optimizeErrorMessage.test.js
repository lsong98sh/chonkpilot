/**
 * optimize-error 文案键化（D-41，方案 A，2026-10-09）。
 *
 * 断言：
 *   1) 后端静态错误码 ↔ 前端 i18n 命名空间 `optimizeError.*` 一一对应（键集 = 已知码清单，双语齐备）；
 *   2) 带参错误带对应占位符（name/status/message），与后端 payload 参数对齐；
 *   3) config.js 的 resolveOptimizeError 按 code 译、message 兜底（源码级守卫）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const localesDir = join(here, '..', 'src', 'locales')
const read = (locale) =>
  JSON.parse(readFileSync(join(localesDir, locale, 'optimizeError.json'), 'utf8'))

// 与 src/lib/gui/bridge/optimize.go 的错误码常量一一对应（改动须同步本清单）。
const CODES = [
  'prompt_required', 'config_read_failed', 'no_llm_config', 'llm_not_set',
  'llm_not_found', 'llm_config_invalid', 'llm_model_missing',
  'request_build_failed', 'llm_request_failed', 'llm_status_error', 'llm_empty_response',
]

test('optimizeError：双语键集 = 后端错误码清单', () => {
  for (const locale of ['zh-CN', 'en-US']) {
    const keys = Object.keys(read(locale)).sort()
    assert.deepEqual(keys, [...CODES].sort(), `${locale} optimizeError 键集与后端错误码不一致`)
  }
})

test('optimizeError：带参错误含对应占位符', () => {
  for (const locale of ['zh-CN', 'en-US']) {
    const m = read(locale)
    assert.match(m.llm_not_found, /\{name\}/, `${locale} llm_not_found 须含 {name}`)
    assert.match(m.llm_status_error, /\{status\}/, `${locale} llm_status_error 须含 {status}`)
    for (const k of ['config_read_failed', 'llm_config_invalid', 'request_build_failed', 'llm_request_failed', 'llm_status_error']) {
      assert.match(m[k], /\{message\}/, `${locale} ${k} 须含 {message}`)
    }
  }
})

test('config.js：resolveOptimizeError 按 code 译、message 兜底', () => {
  const src = readFileSync(join(here, '..', 'src', 'api', 'config.js'), 'utf8')
  assert.match(src, /export function resolveOptimizeError/, '须导出 resolveOptimizeError')
  assert.match(src, /'optimizeError\.' \+ code/, '须以 optimizeError.<code> 为 i18n 键')
  assert.match(src, /i18n\.global\.te\(/, '须先判 code 是否有译文')
  assert.match(src, /return p\.message \|\| 'Unknown error'/, '未识别 code 须回落 message')
  assert.match(src, /onError\(resolveOptimizeError\(payload\)\)/, 'onError 须传本地化文案')
})

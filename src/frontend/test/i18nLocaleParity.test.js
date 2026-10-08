/**
 * 15-国际化 · i18n 资源一致性（2026-10-05）。
 *
 * 目标：对 `src/frontend/src/locales/{zh-CN,en-US}/*.json` 做**静态对账**——两语言
 * **命名空间文件集一致**、**每个文件的叶子 key 集一致**（无缺失 / 无孤儿）、**叶子值非空**。
 *
 * 与实现的关系（`src/frontend/src/plugins/i18n.js`）：i18n 以 `locale → { namespace: messages }`
 * 装配，故「命名空间 = 同名 JSON 文件」。文件集或 key 集漂移 = 某语言缺翻译（运行时回落
 * `fallbackLocale='zh-CN'` 而静默）或存在永不命中的孤儿 key。
 *
 * 边界：只做**集合级**校验（不比较文案语义 / 不校验值类型）；不 import i18n.js（其依赖 vue-i18n
 * 与 .vue 编译产物，node --test 不可直接加载）→ 直接读 JSON 源文件。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const localesDir = join(here, '..', 'src', 'locales')
const LOCALES = ['zh-CN', 'en-US']

/** 读取某语言的 `<file> → JSON 对象` 映射。 */
function loadLocale(locale) {
  const dir = join(localesDir, locale)
  const out = new Map()
  for (const f of readdirSync(dir).filter((n) => n.endsWith('.json'))) {
    out.set(f, JSON.parse(readFileSync(join(dir, f), 'utf8')))
  }
  return out
}

/** 递归展开为「点分路径 → 叶子值」；空对象视为一个无叶子的命名空间（由文件集对账兜底）。 */
function flatten(obj, prefix = '', acc = new Map()) {
  for (const [k, v] of Object.entries(obj || {})) {
    const path = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, path, acc)
    else acc.set(path, v)
  }
  return acc
}

const loaded = Object.fromEntries(LOCALES.map((l) => [l, loadLocale(l)]))
const [zh, en] = LOCALES.map((l) => loaded[l])

test('i18n：两语言命名空间文件集一致', () => {
  const zhFiles = [...zh.keys()].sort()
  const enFiles = [...en.keys()].sort()
  const missingInEn = zhFiles.filter((f) => !en.has(f))
  const orphanInEn = enFiles.filter((f) => !zh.has(f))
  assert.deepEqual(missingInEn, [], `en-US 缺文件：${missingInEn.join(', ')}`)
  assert.deepEqual(orphanInEn, [], `en-US 孤儿文件（zh-CN 无）：${orphanInEn.join(', ')}`)
})

test('i18n：每个命名空间中英 key 集一致（无缺失 / 无孤儿）', () => {
  const problems = []
  for (const file of [...zh.keys()].sort()) {
    if (!en.has(file)) continue // 文件集缺失已由上一用例覆盖
    const zhKeys = new Set(flatten(zh.get(file)).keys())
    const enKeys = new Set(flatten(en.get(file)).keys())
    const missing = [...zhKeys].filter((k) => !enKeys.has(k))
    const orphan = [...enKeys].filter((k) => !zhKeys.has(k))
    if (missing.length) problems.push(`${file}: en-US 缺 key ${missing.join(', ')}`)
    if (orphan.length) problems.push(`${file}: en-US 孤儿 key ${orphan.join(', ')}`)
  }
  assert.deepEqual(problems, [], `key 集不一致：\n${problems.join('\n')}`)
})

test('i18n：叶子值非空（中英各自）', () => {
  const problems = []
  for (const locale of LOCALES) {
    for (const [file, json] of loaded[locale]) {
      for (const [k, v] of flatten(json)) {
        if (typeof v === 'string' && v.trim() === '') problems.push(`${locale}/${file}: ${k} 为空串`)
      }
    }
  }
  assert.deepEqual(problems, [], `存在空翻译：\n${problems.join('\n')}`)
})

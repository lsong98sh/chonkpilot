/**
 * 64-配置项一览 · 源码引用守卫（2026-11-05）。
 *
 * 目标：文档以「文件 + 符号」为主，但会引用大量源码文件。代码迁移后路径/行号极易漂移，
 * 本测试只做**存在性 + 行号越界**两项机械校验（不做符号级匹配，避免代码移位导致测试脆断）：
 *   - 提取**所有带目录的源码路径**（go/vue/js/mjs/py/ps1；可选 `:行号[-行号]`）；
 *     代码围栏跳过；含 `~` `<` `>` `*` `$` 空格 / `://` 的路径跳过；capability/initdata/
 *     public/examples/docs/.chonkpilot 等明显数据路径跳过。
 *   - 按 64 头部声明的简写建立显式前缀基准（顺序尝试，命中即止）：
 *       internal/adaptor/ → chonkpilot-agent/router/src
 *       internal/         → src/lib/data
 *       llm/server/       → src/lib
 *       mcp-server/server/→ src/lib
 *       gui/bridge/       → src/lib
 *       views|utils|composables/ → src/frontend/src
 *       layout/           → chonkpilot-framework/host/web/src
 *       src/…             → 主仓根
 *       chonkpilot-…      → 同级仓根
 *       <plugin>/src/…    → 同级仓 chonkpilot-plugins/<plugin>/
 *   - 无法匹配任何基准的带目录路径 → 计入失败（提示补全限定路径或调整基准）。
 *   - 文档中明确声明「不存在/已删除」的历史路径（REMOVED_PATHS）反向断言：必须仍不存在。
 *
 * 若新增引用指向不存在文件 / 越界行号 → 回写 64（去掉行号或改为完整路径），
 * 不要在这里放宽断言。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { REPO_ROOT, readRepo } from './support/feSource.mjs'

const DOC = 'docs/spec/60-reference/64-配置项一览.md'
const SIBLING_ROOT = resolve(REPO_ROOT, '..')

// 文档明确标注“不存在/已删除”的历史路径：只做反向断言（不得重新出现）。
const REMOVED_PATHS = new Set([
  'src/lib/data/resolve.go',
  'src/lib/gui/main.go',
])

// 简写前缀 → 基准目录（路径保留前缀，直接拼在基准之后）。
const BASES = [
  ['internal/adaptor/', join(SIBLING_ROOT, 'chonkpilot-agent/router/src')],
  ['internal/', join(REPO_ROOT, 'src/lib/data')],
  ['llm/server/', join(REPO_ROOT, 'src/lib')],
  ['mcp-server/server/', join(REPO_ROOT, 'src/lib')],
  ['gui/bridge/', join(REPO_ROOT, 'src/lib')],
  ['views/', join(REPO_ROOT, 'src/frontend/src')],
  ['utils/', join(REPO_ROOT, 'src/frontend/src')],
  ['composables/', join(REPO_ROOT, 'src/frontend/src')],
  ['layout/', join(SIBLING_ROOT, 'chonkpilot-framework/host/web/src')],
  ['src/', REPO_ROOT],
  ['chonkpilot-', SIBLING_ROOT],
]

// 路径 + 可选行号；`(?![\w])` 防止把 config.json 误识别为 config.js。
const REF = /([A-Za-z0-9_.\-/]+\.(?:mjs|go|vue|js|py|ps1))(?![\w])(?::(\d+)(?:-(\d+))?)?/g

/** 是否跳过该候选路径（非源码 / 数据 / 示例）。 */
function skipPath(p) {
  if (!p.includes('/')) return true
  if (/[~<>*$\s]/.test(p)) return true
  if (p.includes('://')) return true
  if (/(^|\/)(capability|initdata|public|examples?|docs)\//.test(p)) return true
  if (/\.chonkpilot\//.test(p)) return true
  return false
}

/** 提取文档中所有源码路径引用（带行号时解析行号范围）。 */
function collectRefs(text) {
  const out = []
  let inFence = false
  text.split(/\r?\n/).forEach((line, i) => {
    if (/^\s*```/.test(line)) {
      inFence = !inFence
      return
    }
    if (inFence) return
    REF.lastIndex = 0
    let m
    while ((m = REF.exec(line))) {
      const p = m[1]
      if (skipPath(p)) continue
      out.push({
        docLine: i + 1,
        path: p,
        start: m[2] ? +m[2] : null,
        end: m[3] ? +m[3] : null,
      })
    }
  })
  return out
}

/** 按显式前缀基准解析；无法匹配任何基准 → null。 */
function resolveRef(p) {
  for (const [prefix, root] of BASES) {
    if (p.startsWith(prefix)) return join(root, p)
  }
  // <plugin>/src/... → 同级仓 chonkpilot-plugins/<plugin>/...
  if (/^[A-Za-z0-9_-]+\/src\//.test(p)) return join(SIBLING_ROOT, 'chonkpilot-plugins', p)
  return null
}

test('64-配置项一览：源码路径必须存在，带行号时不得越界', (t) => {
  const refs = collectRefs(readRepo(DOC))
  assert.ok(refs.length > 0, '未提取到任何源码路径引用（解析规则或文档格式异常）')
  t.diagnostic(`源码路径引用 ${refs.length} 处`)

  const bad = []
  const seen = new Set()
  for (const r of refs) {
    const key = `${r.path}:${r.start ?? ''}-${r.end ?? ''}`
    if (seen.has(key)) continue
    seen.add(key)

    if (REMOVED_PATHS.has(r.path)) {
      if (existsSync(join(REPO_ROOT, r.path))) {
        bad.push(`L${r.docLine} ${r.path} → 文档声明已删除，但文件重新出现`)
      }
      continue
    }

    const file = resolveRef(r.path)
    if (!file) {
      bad.push(`L${r.docLine} ${r.path} → 无法匹配任何简写基准（请补全限定路径或调整基准）`)
      continue
    }
    if (!existsSync(file) || !statSync(file).isFile()) {
      bad.push(`L${r.docLine} ${r.path} → 文件不存在（解析为 ${file}）`)
      continue
    }
    if (r.start != null) {
      const total = readFileSync(file, 'utf8').split(/\r?\n/).length
      const end = r.end ?? r.start
      if (r.start < 1 || end < r.start || r.start > total || end > total) {
        bad.push(`L${r.docLine} ${r.path}:${r.start}${end !== r.start ? `-${end}` : ''} → 行号越界（实际 ${total} 行）`)
      }
    }
  }

  assert.deepEqual(bad, [], `64 源码引用守卫失败：\n${bad.join('\n')}`)
})
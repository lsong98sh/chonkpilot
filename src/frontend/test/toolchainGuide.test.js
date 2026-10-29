/**
 * 工具链安装引导 + 版本展示（P2-3 ②③④，2026-10-28）。
 *
 *   A. 纯函数 `normalizeVersion` / `belowMinVersion` / `versionBadge`（版本规范化展示 +
 *      **低于最小版本判定**，六态）—— 全部在**前端**算，`gui.toolchain.detect` 载荷不变；
 *   B. 静态引导表（每个 runtime 都有官方下载页 + winget 命令；未知名 → null）；
 *   C. 源级守卫：**只引导、不安装**（无下载 / 安装 / 执行命令的能力调用），
 *      且路径页确实消费 `versionBadge` / `guideFor` 并渲染安装说明入口。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { TOOLCHAIN_GUIDES, guideFor, normalizeVersion, belowMinVersion, versionBadge } from '../src/utils/toolchainGuide.js'
import { readSrc } from './support/feSource.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readSrc(rel)

// ── A. 版本规范化 / 低于判定（纯函数）────────────────────────────────
test('normalizeVersion：真实探测输出样本 → 规范化点分版本；解析不出 → 原样（trim）', () => {
  const cases = [
    ['go version go1.26.0 windows/amd64', '1.26.0'],
    ['Python 3.12.10', '3.12.10'],
    ['v22.22.1', '22.22.1'],
    ['rustc 1.83.0 (abc 2024-11-26)', '1.83.0'],
    ['java version "21.0.1" 2024-10-15 LTS', '21.0.1'],
    // 解析不出（无「≥2 段点分数字」）→ 原样 trim 返回（不丢信息）
    ['no version here', 'no version here'],
    // 单个数段（x86_64 的 86）不算版本，继续找后续点分段
    ['gcc (x86_64-posix-seh-rev0, Built by MinGW-W64 project) 13.2.0', '13.2.0'],
    // 空串 / 非字符串 → 空串（未探测到）
    ['', ''],
    [null, ''],
    // Chrome 目录名形态（已是纯点分）
    ['152.0.7977.84', '152.0.7977.84'],
    // 点后非数字 → 只取到点前
    ['1.2. beta', '1.2'],
  ]
  for (const [raw, want] of cases) {
    assert.equal(normalizeVersion(raw), want, `normalizeVersion(${JSON.stringify(raw)})`)
  }
})

test('belowMinVersion：低于 / 等于 / 高于 / 未配置 / 缺段补 0 / 非法 minVersion 六态', () => {
  const cases = [
    ['低于', '1.25.0', '1.26.0', true],
    ['等于', '1.26.0', '1.26.0', false],
    ['高于', '1.27.1', '1.26.0', false],
    ['未配置', '1.25.0', '', false],
    ['缺段补 0', '1.26', '1.26.0.1', true],
    ['非法 minVersion', '1.25.0', 'latest', false],
  ]
  for (const [name, cur, min, want] of cases) {
    assert.equal(belowMinVersion(cur, min), want, `${name}: belowMinVersion(${cur}, ${min})`)
  }
  // 当前版本不可解析（规范化失败的原样串）→ 不提示
  assert.equal(belowMinVersion('no version here', '1.0.0'), false)
  // 非字符串输入 → 不提示（宁可不提）
  assert.equal(belowMinVersion(null, '1.0.0'), false)
  assert.equal(belowMinVersion('1.0.0', undefined), false)
})

test('versionBadge：{ norm, belowMin } 展示态（原始串 → 规范化 + 下限）', () => {
  assert.deepEqual(versionBadge('Python 3.11.9', '3.12.0'), { norm: '3.11.9', belowMin: true })
  assert.deepEqual(versionBadge('Python 3.12.10', '3.12.0'), { norm: '3.12.10', belowMin: false })
  // 未配置最小版本（min 空）→ 不提示
  assert.deepEqual(versionBadge('v22.22.1', ''), { norm: '22.22.1', belowMin: false })
  // 未探测到 / 空 → 全空、不提示（UI 不渲染版本）
  assert.deepEqual(versionBadge('', '3.12.0'), { norm: '', belowMin: false })
  assert.deepEqual(versionBadge(null, null), { norm: '', belowMin: false })
  // minVersion 允许带前缀 v
  assert.deepEqual(versionBadge('1.25.0', 'v1.26.0'), { norm: '1.25.0', belowMin: true })
})

// ── B. 安装引导静态表 ────────────────────────────────────────────
test('TOOLCHAIN_GUIDES：每个 runtime 都有官方下载页（https）与 winget 命令', () => {
  const runtimes = ['python', 'node', 'go', 'rust', 'java', 'c', 'chrome', 'powershell', 'web']
  for (const id of runtimes) {
    const g = guideFor(id)
    assert.ok(g, `缺 ${id} 的安装引导`)
    assert.match(g.downloadUrl, /^https:\/\//, `${id}.downloadUrl 须为 https 官方页`)
    assert.ok(g.winget && g.winget.startsWith('winget install '), `${id} 须给 winget 命令串`)
    assert.ok(!('download' in g) && !('install' in g), `${id} 不得声明下载/安装动作`)
  }
  // 浏览器：web 与探测 id chrome 同一份引导
  assert.equal(guideFor('web'), guideFor('chrome'))
  // 未知名 → null（UI 不渲染入口）
  assert.equal(guideFor('no-such-runtime'), null)
  assert.equal(guideFor(''), null)
  // 表本身冻结（静态、不可运行期改写）
  assert.ok(Object.isFrozen(TOOLCHAIN_GUIDES))
})

// ── C. 源级守卫：只引导、不安装 ───────────────────────────────────
test('toolchainGuide.js：无下载 / 安装 / 子进程执行能力', () => {
  // 先剥注释（文档注释里明确写了「禁止 child_process」之类字样，不算调用）
  const src = read('utils/toolchainGuide.js')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\/\/[^\n]*/g, '')
  for (const forbidden of [/child_process/, /\bfetch\s*\(/, /\bdownload\s*\(/, /\binstall\s*\(/, /new\s+Function/, /\beval\s*\(/]) {
    assert.doesNotMatch(src, forbidden, `安装引导不得包含 ${forbidden}`)
  }
  // 打开页面 = window.open（外部导航），复制 = clipboard（只进剪贴板）
  assert.match(src, /window\.open\(/, '打开官方下载页须走 window.open')
  assert.match(src, /navigator\.clipboard\.writeText\(/, '复制命令须走剪贴板')
})

test('SettingsPathsPage：消费 versionBadge / guideFor 并渲染安装说明入口（无 watch）', () => {
  const page = read('views/config/SettingsPathsPage.vue')
  assert.match(page, /import \{[^}]*versionBadge[^}]*\} from '\.\.\/\.\.\/utils\/toolchainGuide'/, '须从统一工具模块取展示/判定')
  assert.match(page, /versionBadge\(/, '须用 versionBadge 计算展示态')
  assert.match(page, /guideFor\(/, '须用 guideFor 取安装引导')
  assert.match(page, /class="version-warn"/, '低于最小版本须有小提示图标')
  assert.match(page, /config\.page\.versionBelowMin/, '提示文案须含「要求 ≥ x，当前 y」')
  assert.match(page, /config\.page\.installGuide/, '每个条目须有「安装说明」入口')
  assert.match(page, /openGuide\(/, '须有打开官方下载页的动作')
  assert.match(page, /copyWinget\(/, '须有复制 winget 命令的动作')
  // 前端规则：不得用 watch/watchEffect 监听
  assert.doesNotMatch(page, /\bwatch(Effect)?\s*\(/, 'SettingsPathsPage 不得使用 watch/watchEffect')
})

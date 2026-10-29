/**
 * 工具链「安装引导」静态表 + 版本展示纯函数（P2-3 ②③④，2026-10-28）。
 *
 * 纪律（用户口径）：**只做引导，不做安装** —— 每个 runtime 只给「官方下载页」与「winget 命令」，
 * 由用户自己打开页面 / 粘贴命令执行。本文件**禁止**任何下载 / 安装 / 执行命令的能力
 * （无 child_process / 无 fetch / 无 download() / 无 install() 调用，见 `test/toolchainGuide.test.js`）。
 *
 * runtime 键与工具链探测 id 对齐（`src/lib/gui/bridge/toolchain.go toolchainCandidates`）：
 * python / node / go / rust / java / c / chrome；另含 `powershell`（系统自带 shell）与
 * `web`（浏览器 = Chrome/Edge，`chrome` 为其别名）。
 */
const WEB_GUIDE = Object.freeze({
  downloadUrl: 'https://www.google.com/chrome/',
  winget: 'winget install --id Google.Chrome -e',
})

export const TOOLCHAIN_GUIDES = Object.freeze({
  python: Object.freeze({
    downloadUrl: 'https://www.python.org/downloads/windows/',
    winget: 'winget install --id Python.Python.3.12 -e',
  }),
  node: Object.freeze({
    downloadUrl: 'https://nodejs.org/en/download',
    winget: 'winget install --id OpenJS.NodeJS.LTS -e',
  }),
  go: Object.freeze({
    downloadUrl: 'https://go.dev/dl/',
    winget: 'winget install --id GoLang.Go -e',
  }),
  rust: Object.freeze({
    downloadUrl: 'https://rustup.rs/',
    winget: 'winget install --id Rustlang.Rustup -e',
  }),
  java: Object.freeze({
    downloadUrl: 'https://adoptium.net/temurin/releases/',
    winget: 'winget install --id EclipseAdoptium.Temurin.21.JDK -e',
  }),
  c: Object.freeze({
    downloadUrl: 'https://www.msys2.org/',
    winget: 'winget install --id MSYS2.MSYS2 -e',
  }),
  powershell: Object.freeze({
    downloadUrl: 'https://learn.microsoft.com/powershell/scripting/install/installing-powershell-on-windows',
    winget: 'winget install --id Microsoft.PowerShell -e',
  }),
  // 浏览器（web）与探测 id `chrome` 同一份引导
  web: WEB_GUIDE,
  chrome: WEB_GUIDE,
})

/** guideFor 取某 runtime 的安装引导（无该项 → null，UI 不渲染入口）。 */
export function guideFor(id) {
  return TOOLCHAIN_GUIDES[id] || null
}

/**
 * normalizeVersion 从探测输出抽取规范化的点分数字版本（如 `go version go1.26.0 windows/amd64`
 * → `1.26.0`、`Python 3.12.10` → `3.12.10`、`v22.22.1` → `22.22.1`）：
 *   - 取**第一个**「≥2 段、段非空、全数字」的连续 `数字(.数字)*` 段；
 *   - 前缀（`v` / `go` / `Python` / `node ` 等）不用特判 —— 只取数字与点，字母自然被跳过；
 *   - 解析不出 → 返回 **trim 后的原串**（绝不丢信息；展示层另有 `version` 原始字段）。
 *
 * 语义与**前端唯一实现**（`gui.toolchain.detect` 载荷保持 `{id,name,path,version}` 原样，
 * 规范化 / 下限判定都在前端算）。
 */
export function normalizeVersion(raw) {
  const s = typeof raw === 'string' ? raw : ''
  for (let i = 0; i < s.length; i++) {
    if (!isDigit(s.charCodeAt(i))) continue
    let out = ''
    let dots = 0
    let j = i
    while (j < s.length) {
      if (isDigit(s.charCodeAt(j))) {
        out += s[j]
        j++
        continue
      }
      // 点后必须紧跟数字（`1.2.` → 只取到 `1.2`）
      if (s[j] === '.' && j + 1 < s.length && isDigit(s.charCodeAt(j + 1))) {
        out += '.'
        dots++
        j++
        continue
      }
      break
    }
    if (dots > 0) return out
    i = j // 本段只有单个数（如 `x86_64` 里的 `86`）→ 跳过继续找
  }
  return s.trim()
}

function isDigit(code) {
  return code >= 48 && code <= 57
}

/** parseVersionParts 把点分版本串解析为数值段（容忍前缀 `v`；任一段非数字 / 空段 → null）。 */
function parseVersionParts(s) {
  const body = s.trim().replace(/^v/, '')
  if (!body) return null
  const out = []
  for (const p of body.split('.')) {
    if (!p) return null
    let n = 0
    for (let i = 0; i < p.length; i++) {
      const code = p.charCodeAt(i)
      if (!isDigit(code)) return null
      n = n * 10 + (code - 48)
      if (n > 1e9) return null // 离谱值按非法处理（与 Go 侧口径一致）
    }
    out.push(n)
  }
  return out
}

/**
 * belowMinVersion 判定规范化版本是否**低于**最小要求（逐段数值比较、缺段补 0）。
 * 未配置（min 空）/ 任一侧不是纯点分数字（含非法 minVersion）→ false（**宁可不提示**）。
 */
export function belowMinVersion(cur, min) {
  const a0 = typeof cur === 'string' ? cur.trim() : ''
  const b0 = typeof min === 'string' ? min.trim() : ''
  if (!a0 || !b0) return false
  const a = parseVersionParts(a0)
  const b = parseVersionParts(b0)
  if (!a || !b) return false
  const n = Math.max(a.length, b.length)
  for (let i = 0; i < n; i++) {
    const x = i < a.length ? a[i] : 0
    const y = i < b.length ? b[i] : 0
    if (x !== y) return x < y
  }
  return false
}

/**
 * versionBadge 由单行探测结果 + usr 最小版本配置算展示态（**纯函数**，无消息面）：
 *   - `norm`：规范化版本（`normalizeVersion(raw)`；解析不出 → trim 后的原串）；
 *   - `belowMin`：**低于最小版本**（仅当配置了 `minVersion` 且规范化版本更低 → true）。
 *
 * 语义：**仅提示**，不阻断任何工具调用 / 命令执行。
 */
export function versionBadge(raw, minVersion) {
  const norm = normalizeVersion(raw)
  return { norm, belowMin: belowMinVersion(norm, minVersion) }
}

/** openGuidePage 在默认浏览器打开官方下载页（WebView2 外部导航；不下载任何文件）。 */
export function openGuidePage(url) {
  if (!url) return false
  window.open(String(url), '_blank', 'noopener')
  return true
}

/** copyGuideCommand 复制 winget 命令（只进剪贴板；**不执行**）。 */
export async function copyGuideCommand(cmd) {
  await navigator.clipboard.writeText(String(cmd))
  return true
}

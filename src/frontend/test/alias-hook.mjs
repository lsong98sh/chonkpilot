/**
 * node:test 解析钩子（TOPO-1 §2.22.2 B 档）。
 *
 * 三件事（**只作用于测试进程**，不引入第二套 node_modules）：
 *   1. `@ckfw/<path>` → 同级仓 `chonkpilot-framework/host/web/src/<path>`（主仓源码里的 vite alias）；
 *   2. 无扩展名相对导入 → 按 `.js` → `.vue` → `/index.js` 补全（等价 vite/打包器行为；
 *      主仓与 framework 两侧都有打包器风格的省略扩展名写法）；
 *   3. framework 源码里的裸包（`vue` / `pinia` / `vue-i18n`，均为 peerDependencies）→
 *      以**主仓** `src/frontend/package.json` 为父重新解析，命中主仓 node_modules 的那一份，
 *      保证 vue / pinia 只有单实例。
 *
 * `.vue` 的编译由测试文件自行 `register('./support/vue-sfc-hooks.mjs')`（后注册者先执行，
 * 会覆盖本钩子的同名解析），本钩子只负责把路径补全。
 */
import { existsSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'

const FW_ROOT = fileURLToPath(new URL('../../../../chonkpilot-framework/host/web/src/', import.meta.url))
const FE_PKG = new URL('../package.json', import.meta.url).href

/** 省略扩展名的 URL → 按 `.js` → `.vue` → `/index.js` 补全；补不出 → null。 */
function complete(url) {
  if (/\.[a-z0-9]+$/i.test(url.pathname)) return url
  for (const ext of ['.js', '.vue']) {
    const candidate = new URL(url.href + ext)
    if (existsSync(fileURLToPath(candidate))) return candidate
  }
  const index = new URL(url.href.replace(/\/?$/, '/index.js'))
  if (existsSync(fileURLToPath(index))) return index
  return null
}

function hit(url) {
  return {
    url: url.href,
    shortCircuit: true,
    ...(url.pathname.endsWith('.vue') ? { format: 'module' } : {}),
  }
}

export async function resolve(specifier, context, next) {
  // ① framework 别名
  if (specifier === '@ckfw' || specifier.startsWith('@ckfw/')) {
    const rel = specifier === '@ckfw' ? 'index.js' : specifier.slice('@ckfw/'.length)
    const url = complete(pathToFileURL(FW_ROOT + rel))
    return url ? hit(url) : next(specifier, context)
  }
  // ② 相对导入：补齐扩展名（仅当原样解析会失败）
  if (specifier.startsWith('.') && context.parentURL) {
    const url = complete(new URL(specifier, context.parentURL))
    if (url) return hit(url)
    return next(specifier, context)
  }
  // ③ framework 源码的裸包 → 回主仓解析（单实例 vue / pinia）
  if (
    !specifier.startsWith('node:') &&
    !specifier.startsWith('/') &&
    context.parentURL &&
    context.parentURL.includes('/chonkpilot-framework/')
  ) {
    return next(specifier, { ...context, parentURL: FE_PKG })
  }
  return next(specifier, context)
}

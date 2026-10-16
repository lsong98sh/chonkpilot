/**
 * 测试支撑 · `.vue` SFC 直载（node:test 下挂真实组件）。
 *
 * 前端无组件级运行器（`npm test` = `node --test test/*.test.js` 直跑），既有约定是
 * **源码守卫**（见 `layoutFreeMechanism.test.js` / `tabBarPropsSync.test.js`）。但「区块保活」
 * （UILAYOUT-1 §3.11）是**运行期行为**断言，源码守卫证明不了 → 这里用 Node 的
 * `module.register()` 挂一条最小 SFC 加载钩子（`@vue/compiler-sfc` 由 `vue` 带入），
 * 让测试能 import 真实 `.vue` 组件并在**无 DOM 的自定义渲染器**（`headless.js`）里挂载。
 *
 * 只做三件事：把 `.vue` 的 `resolve` 指到同路径、`load` 时编译成 ESM、缓存编译结果。
 * 不处理 `<style>`（断言不涉及样式）。
 */
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { parse, compileScript } from '@vue/compiler-sfc'

const cache = new Map()

/** 无扩展名的相对导入 → 按 `.js` → `.vue` → `/index.js` 顺序补全（模拟打包器行为） */
function withExtension(url) {
  if (/\.[a-z0-9]+$/i.test(url.pathname)) return null
  for (const ext of ['.js', '.vue']) {
    const candidate = new URL(url.href + ext)
    if (existsSync(fileURLToPath(candidate))) return candidate
  }
  const index = new URL(url.href.replace(/\/?$/, '/index.js'))
  if (existsSync(fileURLToPath(index))) return index
  return null
}

export async function resolve(specifier, context, nextResolve) {
  if (typeof specifier === 'string' && (specifier.startsWith('.') || specifier.startsWith('/'))) {
    const url = new URL(specifier, context.parentURL)
    const completed = withExtension(url)
    if (completed) {
      return {
        url: completed.href,
        shortCircuit: true,
        ...(completed.pathname.endsWith('.vue') ? { format: 'module' } : {}),
      }
    }
    if (url.pathname.endsWith('.vue')) return { url: url.href, shortCircuit: true, format: 'module' }
  }
  return nextResolve(specifier, context)
}

export async function load(url, context, nextLoad) {
  if (!url.endsWith('.vue')) return nextLoad(url, context)
  if (cache.has(url)) return { format: 'module', source: cache.get(url), shortCircuit: true }

  const filename = fileURLToPath(url)
  const source = readFileSync(filename, 'utf8')
  const { descriptor, errors } = parse(source, { filename })
  if (errors && errors.length) throw new Error(`[vue-sfc-hooks] parse 失败：${filename}\n${errors[0]}`)
  if (!descriptor.scriptSetup) throw new Error(`[vue-sfc-hooks] 仅支持 <script setup>：${filename}`)

  // inlineTemplate：模板编进 setup（组件自带 render，无需运行时模板编译）
  const compiled = compileScript(descriptor, { id: url, inlineTemplate: true })
  cache.set(url, compiled.content)
  return { format: 'module', source: compiled.content, shortCircuit: true }
}

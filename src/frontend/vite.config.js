import { fileURLToPath, URL } from 'node:url'
import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const here = fileURLToPath(new URL('.', import.meta.url))

// 单入口（PRODSIM-1 产物简化）：唯一产物 `chonkpilot.exe` = WebView2 宿主，前端 dist 以
// `//go:embed all:frontend/dist` 编进 exe（壳 = `src/desktop`）。
//   - 入口 = index.html（dev server 同用）；
//   - 产物 = `src/desktop/frontend/dist`（go:embed 落点；由 build-desktop.ps1 构建投放）。
// browser 形态（HTTP 入口 / 独立 index.html 产物）已随 PRODSIM-1 删除。
//
// 平台基座前端包（TOPO-1 §2.22.2，B 档）：窗口 chrome / 布局引擎 / 布局档案 / 通用控件 / MQ 客户端
// 在**源码级**从同级仓 `chonkpilot-framework/host/web/src` 编译（该包不产 dist、不带 node_modules）：
//   - `@ckfw` → 该目录（主仓→framework 单向依赖，framework 不 import 主仓任何业务模块）；
//   - `resolve.dedupe` 保证 vue / pinia 只有一份实例（否则响应式与 store 会分裂）；
//   - `server.fs.allow` 放开仓外源码目录（dev server 需读该目录）。
const frameworkWeb = resolve(here, '../../../chonkpilot-framework/host/web/src')

export default defineConfig(() => ({
  plugins: [vue()],
  base: './',
  resolve: {
    alias: [{ find: '@ckfw', replacement: frameworkWeb }],
    dedupe: ['vue', 'pinia', 'vue-i18n'],
  },
  server: {
    fs: { allow: [resolve(here, '.'), frameworkWeb] },
  },
  build: {
    outDir: resolve(here, '../desktop/frontend/dist'),
    assetsDir: 'assets',
    emptyOutDir: true,
    rollupOptions: {
      input: { app: resolve(here, 'index.html') },
    },
  },
}))

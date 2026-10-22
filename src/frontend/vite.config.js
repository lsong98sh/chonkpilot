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
export default defineConfig(() => ({
  plugins: [vue()],
  base: './',
  build: {
    outDir: resolve(here, '../desktop/frontend/dist'),
    assetsDir: 'assets',
    emptyOutDir: true,
    rollupOptions: {
      input: { app: resolve(here, 'index.html') },
    },
  },
}))

import { fileURLToPath, URL } from 'node:url'
import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const here = fileURLToPath(new URL('.', import.meta.url))

// 双入口（决策 42 §2 (115) / 19 §3.2；产物路径按 [41 D-28] 重整）：
//   mode=embed   → 入口 embed.html（GUI/WebView2 形态），产物落 src/gui/frontend/dist
//                  （客户端壳 src/gui 的 //go:embed all:frontend/dist 落点；桌面单体壳
//                   src/desktop/frontend/dist 由 build-desktop.ps1 从该目录**镜像**过去；
//                   脚本构建后把 embed.html 复制改名为 index.html）
//   其它(browser) → 入口 index.html（HTTP 入口 / 浏览器直接访问），产物落
//                  **src/server/frontend/dist**（2026-09-21：browser 静态面 go:embed 进
//                   chonkpilot-server.exe 的落点，见 src/server/frontend.go；`--web-root`
//                   仅作外部目录覆盖）
// 两形态产物目录隔离：同名 index.html 不会互相覆盖。
export default defineConfig(({ mode }) => {
  const embed = mode === 'embed'
  return {
    plugins: [vue()],
    base: './',
    build: {
      outDir: embed ? resolve(here, '../gui/frontend/dist') : resolve(here, '../server/frontend/dist'),
      assetsDir: 'assets',
      emptyOutDir: true,
      rollupOptions: {
        input: { app: resolve(here, embed ? 'embed.html' : 'index.html') },
      },
    },
  }
})

/**
 * 运行形态判据（2026-09-19，browser 形态目录选择接线）。
 *
 * browser 形态 = 页面由服务端 HTTP 入口（`chonkpilot-server --http-addr`）托管：其 index.html
 * 内联注入 shim `__ck_bridge.js`（该 shim 置 `window.__ckHttpBridge = true`，见
 * `chonkpilot-llm/httpapi/ck_http_bridge.js`）。
 * GUI（WebView2）形态只注入 `window.__chonkpilotInstanceId`（`chonkpilot-gui/main.go`），
 * **不加载 shim** → 该标记不存在。
 *
 * 注意：`window.__chonkpilotInstanceId` 两种形态都注入，**不能**用作形态判据。
 */
export function isBrowserForm() {
  return typeof window !== 'undefined' && window.__ckHttpBridge === true
}

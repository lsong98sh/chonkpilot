/**
 * Chrome 可用性判据（工具栏告警用）。
 *
 * **真实后端信号（以代码为准）**：
 *   ① `gui.toolchain.detect`（chonkpilot-gui/bridge/toolchain.go）→ 系统级工具链探测，
 *      含 chrome 行 {id:"chrome", path, version}；未安装 → path 为空。
 *   ② usr 配置标量 `chromePath`（用户可手动覆盖；64-配置项一览 §4.1）。
 *   ③ 执行期 `no_chrome`（chonkpilot-mcp-tools/internal/browser/run.go）—— 仅在调用
 *      `browser_run` 时返回，**前端不可查询**。
 *
 * 说明（如实记录）：项目规则提及「系统未装 Chrome → `web_*` 工具被过滤 + noChrome 守卫」，
 * 但当前代码**无 `web_*` 工具、无工具面过滤**；浏览器自动化入口为 `browser_run`，其
 * 无 Chrome 只在执行期报 `no_chrome`。故前端告警只能基于 ①∪②（探测结果 + 手动配置）。
 */

// isChromeMissing：既未探测到 Chrome，也未手动配置 chromePath → 视为不可用。
// 任一非空即视为可用（不告警）。
export function isChromeMissing({ detectedPath, userChromePath } = {}) {
  const detected = String(detectedPath || '').trim()
  const configured = String(userChromePath || '').trim()
  return detected === '' && configured === ''
}

/**
 * 沙箱两页「状态互见」计数（数据源 = usr 配置既有键）。
 *
 * 背景（用户口径）：`SettingsMCPPage`（server 级 `mcps[].sandbox`，仅 stdio）与
 * `SettingsToolSandboxPage`（executor 级 `tool_sandbox`）互不显示对方状态 → 在两页各补一行摘要。
 * 全部为既有 usr 键的纯计数，**零新增 MQ 主题**。
 */

// isStdioServer：与 SettingsMCPPage 的 transportName 推断一致 ——
// 显式 transport 优先；缺省：有 url → http，否则 stdio。
function isStdioServer(s) {
  const tp = String((s && s.transport) || '').trim().toLowerCase()
  if (tp) return tp === 'stdio'
  return !String((s && s.url) || '').trim()
}

// countServerSandboxOn：server 级（mcps[].sandbox === true 且仅 stdio 可隔离）已开启数量。
export function countServerSandboxOn(mcpServers) {
  return (Array.isArray(mcpServers) ? mcpServers : []).filter(
    (s) => !!s && s.sandbox === true && isStdioServer(s)
  ).length
}

// parseToolSandboxMap：usr `tool_sandbox` 允许对象形态与 JSON 字符串形态（自由键按字符串回读）。
export function parseToolSandboxMap(v) {
  let obj = v
  if (typeof obj === 'string') {
    try { obj = JSON.parse(obj) } catch (_) { return {} }
  }
  return obj && typeof obj === 'object' && !Array.isArray(obj) ? obj : {}
}

// countToolSandboxOn：executor 级 `tool_sandbox` 显式开启（=== true）数量（= 已开启的执行器数）。
export function countToolSandboxOn(map) {
  const m = parseToolSandboxMap(map)
  return Object.values(m).filter((v) => v === true).length
}

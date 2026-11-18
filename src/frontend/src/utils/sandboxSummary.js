/**
 * 沙箱「状态互见」计数 + `tool_sandbox` 形状解析（数据源 = 既有配置面）。
 *
 * `tool_sandbox` **管理单位 = 被 spawn 的执行体**（2026-10-12 重整）：
 *   - executors：内置 executor，key = executor **exe 基名**（chonkpilot-*-executor.exe）；
 *   - mcps：外部 MCP，key = MCP 条目名。
 * 形状 = `{"executors":{"<exe基名>":bool},"mcps":{"<mcp名>":bool}}`。
 * 旧形状已移除：读取只接受 `executors` / `mcps`。
 */

// isStdioServer：与 MCP 页 transportName 推断一致 —— 显式 transport 优先；缺省：有 url → http，否则 stdio。
export function isStdioServer(s) {
  const tp = String((s && s.transport) || '').trim().toLowerCase()
  if (tp) return tp === 'stdio'
  return !String((s && s.url) || '').trim()
}

// copyBoolMap：把 src 中的布尔键值拷入 dst（忽略非布尔 / 非对象）。
function copyBoolMap(src, dst) {
  if (!src || typeof src !== 'object') return
  for (const [k, v] of Object.entries(src)) {
    if (v === true || v === false) dst[k] = v
  }
}

// parseToolSandboxMap：归一 usr `tool_sandbox` 为 `{ executors, mcps }`（对象 / JSON 字符串形态）。
// 非法 / 非对象 / 旧形状 → 空表。
export function parseToolSandboxMap(v) {
  let obj = v
  if (typeof obj === 'string') {
    try { obj = JSON.parse(obj) } catch (_) { return { executors: {}, mcps: {} } }
  }
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return { executors: {}, mcps: {} }
  const out = { executors: {}, mcps: {} }
  copyBoolMap(obj.executors, out.executors)
  copyBoolMap(obj.mcps, out.mcps)
  return out
}

// countToolSandboxOn：开关显式开启（=== true）总数（executors + mcps）。
export function countToolSandboxOn(v) {
  const m = parseToolSandboxMap(v)
  return Object.values(m.executors).filter((x) => x === true).length +
    Object.values(m.mcps).filter((x) => x === true).length
}

// countMCPsOn：mcps 子表中开启（=== true）数量（外部 MCP 开关）。
export function countMCPsOn(v) {
  const m = parseToolSandboxMap(v)
  return Object.values(m.mcps).filter((x) => x === true).length
}

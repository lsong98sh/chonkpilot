/**
 * 沙箱「状态互见」计数 + `tool_sandbox` 形状解析（数据源 = 既有配置面）。
 *
 * `tool_sandbox` **管理单位 = 被 spawn 的执行体**（2026-10-12 重整）：
 *   - executors：内置 executor，key = executor **exe 基名**（chonkpilot-*-executor.exe）；
 *   - mcps：外部 MCP，key = MCP 条目名。
 * 形状 = `{"executors":{"<exe基名>":bool},"mcps":{"<mcp名>":bool}}`。
 * 兼容**旧形状** `{"core":bool,"desktop":bool,"browser":bool}` → 读取时迁移（core → tools）。
 */

// 旧形状（executor 类别）→ 新形状（exe 基名）映射（与后端 mcp-server ParseToolSandbox 一致）。
// 2026-10-21（GATEWAY-2/L1）：DSL 退役 —— `core` 只映射到 tools-executor；
// desktop / browser 已改为插件，旧键被忽略（不产生单位）。
const LEGACY_CATEGORY_UNITS = {
  core: ['chonkpilot-tools-executor.exe'],
}

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

// parseToolSandboxMap：归一 usr `tool_sandbox` 为 `{ executors, mcps }`（对象 / JSON 字符串形态；
// 旧形状读取时迁移）。非法 / 非对象 → 空表。
export function parseToolSandboxMap(v) {
  let obj = v
  if (typeof obj === 'string') {
    try { obj = JSON.parse(obj) } catch (_) { return { executors: {}, mcps: {} } }
  }
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return { executors: {}, mcps: {} }
  const out = { executors: {}, mcps: {} }
  // 新形状（出现 executors / mcps 任一键即按新形状解析）
  if (obj.executors !== undefined || obj.mcps !== undefined) {
    copyBoolMap(obj.executors, out.executors)
    copyBoolMap(obj.mcps, out.mcps)
    return out
  }
  // 旧形状（executor 类别）→ 迁移为 exe 基名键
  for (const [cat, exes] of Object.entries(LEGACY_CATEGORY_UNITS)) {
    if (obj[cat] === true || obj[cat] === false) {
      for (const exe of exes) out.executors[exe] = obj[cat]
    }
  }
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

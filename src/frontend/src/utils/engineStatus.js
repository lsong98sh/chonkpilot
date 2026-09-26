/**
 * 内置引擎（codegraph / vfts）**真实可得**的运行时口径。
 *
 * 现状核实（以代码为准，chonkpilot-plugin-codegraph / chonkpilot-plugin-vfts）：
 *   - 插件内 `registered bool`（工具是否已注册到 gateway）与 `clients[workdir]`（引擎子进程）
 *     均为**内存态，未落盘、未随任何消息下发** → 前端**拿不到「引擎子进程运行中/未运行」信号**。
 *   - 前端可得的真实信号只有两条：
 *       ① 配置态：prj `enable-codegraph` / `enable-vfts` 开关；
 *       ② 运行时态：客户端能力面 `tools-list` 是否含该引擎注册的查询工具
 *          （插件 `syncTools` 在「存在启用中的 workdir」时经 `tools/register` 注册，
 *            停用/退出即 `tools/unregister`）—— 名称见 callgate.go 的 queryTools。
 *
 * **暴露名口径**：内置能力源（self 节点）经 gateway `applyPrefix` 前缀 `self_`
 *   （self 节点 entry.ID = "self"）→ 实测工具面名为 `self_codegraph_symbol_search` /
 *   `self_vfts_query`（L4 `run_index_gate.py` 断言；插件注册的工具同样经 self 节点聚合）。
 *   故匹配同时接受「裸名」与「`<任意前缀>_<裸名>`」（`endsWith`）。
 *
 * 因此本模块只提供「工具是否已注册」（真实可得），**不伪造「进程运行中」**。
 */

// 引擎注册到 gateway 的查询工具名（callgate.go queryTools 全集；裸名前缀见上）。
export const ENGINE_TOOLS = {
  codegraph: [
    'codegraph_symbol_search',
    'codegraph_get_symbol_info',
    'codegraph_get_dependency_graph',
    'codegraph_find_circular_deps',
    'codegraph_analyze_complexity',
    'codegraph_get_module_summary',
  ],
  vfts: ['vfts_query'],
}

// nameMatches：暴露名 == 裸名，或以 `_<裸名>` 结尾（覆盖 `self_` 前缀）。
function nameMatches(name, base) {
  return name === base || name.endsWith('_' + base)
}

// hasEngineTools：tools-list 是否含该引擎的查询工具（= 引擎工具已注册到工具面）。
export function hasEngineTools(tools, engine) {
  const names = ENGINE_TOOLS[engine]
  if (!names) return false
  const present = (Array.isArray(tools) ? tools : []).map((t) => (t && t.name) || '').filter(Boolean)
  return names.some((base) => present.some((n) => nameMatches(n, base)))
}

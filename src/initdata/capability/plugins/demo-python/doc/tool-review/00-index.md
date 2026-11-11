# demo-python 工具审查索引

审查范围：`src/initdata/capability/plugins/demo-python`（Python 标准库样例插件）。manifest 只把 `demo_echo` 列为 hot；本目录只为 `demo_echo` 出正式文档，另外 3 个工具的可见性结论见下。

| 工具 | 结论 | P0 | P1 | P2 | 文档 |
|---|---|---|---|---|---|
| demo_echo | 可用但不宜作模板（required 不校验、截断不可恢复、配置键不可达） | 0 | 2 | 7 | [demo_echo.md](demo_echo.md) |
| **小计** | | **0** | **2** | **7** | |

## 核对结果

- `manifest.json:20-24`：`mcp.hot_tools=["demo_echo"]`、`timeout_sec=60`、`namespace="-"`、`origin="builtin"`、`category="demo"`；`product:false`、`autostart:false`；`manifest.json:26-28` 的 `config:{"scope":"plugin"}` **无 `fields`**。
- `plugin.mcp.register` 运行时上报 `hot_tools:["demo_echo"]`（`plugin.py:463`），与 manifest 一致。
- 工具面由 `plugin.py:78-116` 的 `TOOLS` 数组代码构造（无 `.tool.md`），**schema 以代码为准**。
- `TOOLS` 实际有 4 个：`demo_echo`、`demo_time`、`demo_counter`、`demo_llm`（`plugin.py:78-116`）；`tools/list` 恒返回全部 4 个（`:171-172`）。
- `plugin.json` 是极简启动文件（`id/cmd/enabled/name/icon`，`enabled:false`），与 `manifest.json` 不是同一份契约；README 把两者混称（见 `demo_echo.md` §6）。
- `config.fields` 为空 → 配置页零字段，`configSet` 拒绝未声明键（`chonkpilot-framework/plugin/src/mgr/api.go:481-518`），`greeting/echoLimit/flaky` 实际不可配置。

## 非 hot 工具（demo_time / demo_counter / demo_llm）的 LLM 可见性结论

**缺省不可见；只有被显式白名单纳入时才可见。**

- gateway 注册插件 MCP server 的全部工具，但只给 `hot_tools` 命中的工具写 `_meta.hot=true`（`chonkpilot-agent/gateway/src/registry.go:92-104,257-264`）；`demo_time/demo_counter/demo_llm` 不在 `["demo_echo"]` 中 → `Hot=false`。
- llm-server 缺省下发面 = `visibleTools` 再筛 `Hot`（`chonkpilot/src/lib/llm/server/server.go:282-295`），所以这 3 个工具**不进入 LLM 工具列表**。
- 白名单分支不看 hot：`turnCtx.llmTools()` 用 `allowedTools` 直接从 `visibleTools` 取，命中即下发（`src/lib/llm/server/turn.go:1307-1318`；注释明确 T1 修正 `server.go:264-265`）。因此场景/agent 的 `tools` 白名单或用户配置一旦包含它们，LLM 就能看到并调用。
- UI/「工具」页与 gateway `tools/list` 也始终能看到全部 4 个（hot 只是 `_meta` 标记），所以“LLM 不可见 ≠ 不可调用”：缺省（未配白名单）时 llm-server 的调用处只做白名单判定，不校验工具是否在 hot 下发面（`src/lib/llm/server/turn.go:1436-1468`），模型若已知/猜到 `demo_time` 等名字仍可经 gateway 路由执行。
- 按任务要求，未为这 3 个工具单独出文档（缺省不可见）。若后续把 `demo_llm` 等加入默认白名单，需按同一模板补 3 份简短文档，尤其 `demo_llm`（插件内调宿主 LLM，成本/超时 60s 阈值、`plugin/v1/llm.step` 失败路径都未审查）。

## 模板风险（跨工具）

1. **hot 缺省值危险**：`plugin.mcp.register` 若省略 `hot_tools`，PluginManager 会依次回落 manifest → `["*"]`（`framework/plugin/src/mgr/api.go:331-337`），即全部工具变 hot 并直达 LLM。Go SDK 更进一步：`startMCP` 把所有自注册工具名无条件追加进 `hot_tools`（`sdk/plugin.go:558`），`HotTool()` 的“只标记部分”语义实际失效。Python 样例显式列 `demo_echo` 是正确写法，复用模板时必须保留。
2. **配置面与代码默认值脱节**：Python 侧读了 `greeting/echoLimit/flaky`，manifest 却没有 `config.fields` → 三键永远取默认值（`plugin.py:58`）；模板复制时若照抄，会把“看似可配、实际不可配”的坑一起带走。
3. **README 滞后于 manifest**：README 仍称 `plugin.json` 是完整 manifest、声明无 `ui.views`、只列 3 个工具（`README.md:8,17,25,33`），与当前 `manifest.json`（4 工具 + `ui.views`）不一致。

## 范围差异

- 分配清单只要求 `demo_echo`，已完成；额外核实了 `TOOLS` 4 项与 hot 过滤链路（见上），判断另外 3 个工具无需单独文档。
- 未执行 Python 进程/MCP server；未启动宿主。所有结论基于 `plugin.py`、`manifest.json`、`plugin.json`、README、`chonkpilot-framework` 与 `chonkpilot` 的 gateway/llm-server 源码。
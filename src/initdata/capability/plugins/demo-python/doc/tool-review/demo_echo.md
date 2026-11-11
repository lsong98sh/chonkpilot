---
plugin: demo-python
tool: demo_echo
contract: programmatic
handler: src/initdata/capability/plugins/demo-python/plugin.py:192
reviewed_at: 2026-11-08
status: verified
---

# demo_echo 审查

## 1. 一句话结论

可用但**不适合作为模板**：业务极简，返回可解析；但 `text` 的必填/类型完全不校验（空值与 `null` 都返回 success），截断上限 200 字符不可恢复、且 `echoLimit/greeting` 因 manifest 未声明 `config.fields` 而实际不可配置，输出的前缀/截断规则也没有写进工具描述。

## 2. 调用链

LLM → llm-server `toolsForLLM`（缺省只下发 `_meta.hot=true`，`chonkpilot/src/lib/llm/server/server.go:285-295`）→ gateway `doCall`（`chonkpilot-agent/gateway/src/registry.go:257-264` 按 `hot_tools` 写 `_meta.hot`；`entry.TimeoutSec` 取 manifest 60s → mode=`auto`、阈值 60s，`mcpgateway.go:933-958`）→ 插件 MCP Streamable HTTP `POST /mcp`（`plugin.py:126-144`，Bearer 校验 `:129-131`）→ `_rpc`（`:158-184`）→ `tools/list` 恒回 `TOOLS`（`:171-172`）→ `tools/call` → `call_tool`（`:191-207`）→ `demo_echo` 分支（`:192-197`）→ `{"content":[{"type":"text","text":...}],"isError":false}`（`:181`）。

## 3. 契约面

无 `.tool.md`；工具与 schema 都在 Python 里**由代码构造**（`plugin.py:78-116` 的 `TOOLS` 数组），以代码为准。

- `demo_echo` schema：`{type:"object", properties:{text:{type:"string", description:"要回显的文本"}}, required:["text"]}`（`plugin.py:79-87`）；无 `additionalProperties:false`、无 `maxLength`、无 `outputSchema`、无 annotations。
- 注册声明：manifest `mcp.hot_tools=["demo_echo"]`、`timeout_sec=60`、`namespace="-"`、`origin="builtin"`、`category="demo"`（`manifest.json:20-24`）；运行时 `plugin.mcp.register` 也上报 `hot_tools:["demo_echo"]`（`plugin.py:463`）。两处一致。
- 工具描述只有一句“回显文本（演示 MCP 工具与配置读取）”（`plugin.py:81`），**没有**说明：输出带 `greeting` 前缀、是纯文本而非 JSON、超过 200 字符会被截断并追加 `…(truncated)`、`text` 必填但运行时并不校验。
- 默认值（代码）：`greeting="hi from python"`、`echoLimit=200`、`flaky=false`（`plugin.py:58`），来自 `CHONKPILOT_PLUGIN_CONFIG`/`SECRETS` JSON（`:46-62`）。
- **配置面断链**：manifest `config` 只有 `{"scope":"plugin"}`，没有 `fields`（`manifest.json:26-28`）；宿主 `configGet` 的字段来自 `manifest().Config.Fields`（`chonkpilot-framework/plugin/src/mgr/api.go:471-498`），`configSet` 还会拒绝未声明键（`:506-518`）。因此 `greeting/echoLimit/flaky` 既不出现在配置页，也不能经 `config.set` 写入。

## 4. 实现面

- 参数：`text = str(args.get("text",""))`（`plugin.py:193`）——`None → "None"`，数字/数组/对象走 Python `str()`；**没有** required/类型检查。
- 上限：`limit = int(cfg("echoLimit",200) or 200)`（`:194`）；`0/None` 回落 200；负数会把 `text[:-n]` 当截断（`:195-196`）；非数字抛异常 → 被 `tools/call` 捕获成 `isError=true`。
- 截断：`text[:limit] + "…(truncated)"`（`:196`）——按字符计，不返回原长度、无 offset/续读；追加后实际长度 = limit+14。
- 输出：`"%s: %s" % (greeting, text)`（`:197`），前缀恒存在。
- 副作用：无写盘/网络/进程副作用；仅成功时 `_counters["tools"] += 1`（`:179-180`，加锁）。
- 并发：`ThreadingHTTPServer` 每连接一线程（`:34,426-428`）；`demo_echo` 无共享可变状态；计数在 `_lock` 下自增。
- 资源/限制：读 `Content-Length` 后整包 `json.loads`（`:133-134`），**无请求体大小上限**；无并发/线程上限；无每调用超时（网关 60s 仅对其自身生效，Python 侧不会中断）。
- 幂等：是（纯读）；重试安全。
- 清理：无临时资源；MCP server 为 daemon 线程，随进程退出。

## 5. 返回面：LLM 实际看到的所有形态

| 形态 | isError | LLM 看到的完整文本 |
|---|---|---|
| 正常 | false | `hi from python: <text>`（`plugin.py:197,181`） |
| `text` 缺失/空串 | **false** | `hi from python: `（`:193`；required 未生效）——与真实回显不可区分 |
| `text: null` | **false** | `hi from python: None`（`:193`） |
| 非字符串（数字/数组/对象） | false | `hi from python: <str(value)>`（如 `hi from python: [1, 2]`） |
| 超 200 字符 | false | `hi from python: <前200字符>…(truncated)`；无原长度/无续读方式（`:194-196`） |
| 工具内异常（如 echoLimit 非数字、arguments 非对象） | true | 纯文本 `错误: <Python 异常文本>`（`:182-183`） |
| 未知工具名 | true | `错误: unknown tool: <name>`（`:207`）；网关只会调用 `tools/list` 中的名字 |
| JSON 解析失败 | 无工具结果 | HTTP 400 + JSON-RPC `-32700`（`:136-137`）；gateway 侧表现为协议/传输错误 [推断] |
| Bearer 不匹配 | 无工具结果 | HTTP 401，body `{"error":"unauthorized"}`（`:129-131`）[推断 gateway 文案] |
| `GET /mcp` | 无工具结果 | HTTP 405 `{"error":"GET not supported (stateless JSON mode)"}`（`:146-148`） |
| 未知 JSON-RPC 方法 | 无工具结果 | HTTP 200 + `-32601 unknown method: X`（`:184`） |
| 插件未启用/未拉起 | 无工具 | `autostart:false`、`product:false`；不注册则 LLM 工具面没有 `demo_echo`，无法调用 |
| 插件进程崩溃 | 无工具结果 | 连接失败/工具消失，由 gateway 报错 [推断]；无本工具文案 |
| 异步已受理 | false（gateway 层） | 仅当调用 >60s（echo 不可能）：`{status:"pending",task_id}` + 终态经 `mcp-tasks-report`（`mcpgateway.go:1095-1102`） |
| 沙箱拒绝 | — | 无写操作、无路径参数，不适用 |

体积：输出最长约 200+前缀+14 字符，**不会爆上下文**——这是本工具在返回面唯一的正面属性。

## 6. 一致性与问题

| 级别 | 问题 | 证据 | 影响 |
|---|---|---|---|
| P1 | 截断不可恢复：只追加 `…(truncated)`，不返回原长度、无 offset/续读；上限 200 也未写进描述 | `plugin.py:194-196,81` | 模型收到截断文本后无法取回剩余内容；长文本回显任务只能改走 `file_write` 等工具 |
| P1 | `required:["text"]` 不生效：缺失/空串/`null` 全部 `isError=false`，输出空回显或 `None` | `plugin.py:82-86,193,181` | 模型漏填参数时会得到“成功”的空结果，无法区分“回显为空”与“参数没传” |
| P2 | 配置键 `greeting/echoLimit/flaky` 未在 `config.fields` 声明；`configSet` 拒绝未声明键，配置页无字段 | `manifest.json:26-28`；`plugin.py:58`；`mgr/api.go:481-518`；`webui/config.html:92-116,138-139` | 截断上限/前缀/降级演练实际固定，用户与模型都无法发现或修改 |
| P2 | 描述/schema 未声明输出不变量（前缀、纯文本、截断标记），且无 `additionalProperties:false`/`maxLength` | `plugin.py:79-87` | 模型无法预判输出格式；多余参数与超长 `text` 被静默接受 |
| P2 | README 与 manifest/plugin.json 三处不一致：README 称 `plugin.json` 是完整 manifest、不再声明 `ui.views`、只列 3 个工具；实际 `manifest.json` 声明 4 个工具 + `ui.views`，`plugin.json` 是极简启动文件（`enabled:false`） | `README.md:8,17,25,33`；`manifest.json:29-44`；`plugin.json:1-7` | 以样例为模板的开发者会照抄过期说明；`demo_llm` 被文档表格遗漏 |
| P2 | `tools/list` 恒返 4 个工具但只有 `demo_echo` 是 hot；网关 tools/list/UI 能看到全部，LLM 缺省只看 hot，白名单可让另外 3 个直达 LLM | `plugin.py:78-116,171-172,463`；`gateway/registry.go:92-104`；`llm/server/server.go:285-295`、`turn.go:1307-1318` | 模板读者会误以为"注册了就对 LLM 可见"；`demo_llm` 可经白名单绕开 hot 直接给模型（其内部可调 LLM，成本面需注意） |
| P2 | 无请求体/并发上限；`ThreadingHTTPServer` 每连接一线程；超长 `text` 先整包读入再截断 | `plugin.py:119-156,133-134,426-428,194-196` | 作为模板复制到生产插件会继承 DoS 面 |
| P2 | 错误只有自由文本 `错误: <异常>`，无 code/hint；JSON-RPC 层错误与工具业务错误混用 | `plugin.py:182-184` | 模型无法按错误类型决定重试/换工具；本地化文案也不利于提示词匹配 |
| P2 | 工具名 `demo_echo` 为裸名（`namespace="-"` 禁前缀、`origin=builtin`） | `gateway/registry.go:312-319`；`manifest.json:20,24` | 全局唯一命名空间；模板复制后易与其它插件工具撞名 |

## 7. LLM 视角优化 / 改善 / 增强

| 建议 | 类型 | LLM 收益 | 成本 | 兼容性风险 | 可行性 |
|---|---|---|---|---|---|
| 输出改为 `{"echo":"...","prefix":"...","truncated":bool,"original_chars":N,"limit":N}` | 改善 | 可判断截断、可自行决定是否改用 file 工具 | 低（1 文件） | 返回结构变更（demo 消费者少） | 建议现在做 |
| 校验 `text` 必填且为字符串；空串返回明确错误或显式 `empty:true` | 优化 | 消除“成功但空”的误判 | 低 | 行为收紧，非 breaking | 建议现在做 |
| 在 manifest `config.fields` 声明 `greeting/echoLimit`（并设 min/max），写进 schema description | 改善 | 上限/前缀可发现、可调整 | 低 | 仅新增字段 | 建议现在做 |
| 增加 `offset`/`maxChars` 参数支持续读，或返回 `next_offset` | 增强 | 长文本可恢复 | 中 | 新增可选参数 | 排期做 |
| 错误统一为 `{"error":{"code","message","hint"}}` | 改善 | 可恢复、可分类 | 中 | 返回结构变更 | 排期做 |
| 请求体上限 + 连接/线程上限（或换 `socketserver` 限流） | 优化 | 作为模板不带 DoS 面 | 低-中 | 无 | 建议现在做 |
| 给 `TOOLS` 补 `outputSchema`/annotations（`readOnlyHint:true`），或在 README 明确“schema 由代码构造” | 改善 | 模型可发现只读语义 | 低 | 无 | 建议现在做 |
| 保留 `demo_time/demo_counter/demo_llm` 但不注册为工具（或明确 hot 设计说明） | 优化 | 避免模板误复制“多工具只 hot 一个”的隐性模式 | 低 | 移除会破坏演示 | 仅记录 |

## 8. 建议的测试/验收（不执行）

1. `tools/call demo_echo {}` / `{"text":""}` / `{"text":null}` → 期望 `isError=true` 或显式空语义；当前三者均 success。
2. `{"text":"<300 字符>"}` → 检查长度、`…(truncated)`、是否有原长度；验证续读改造。
3. `{"text":123}` / `{"text":[1,2]}` → 记录 `str()` 强转行为。
4. 手工把 `echoLimit` 写成非数字/负数（绕过 configSet）→ 观察 `isError` 文案与 `text[:-n]` 行为。
5. `config.get` / `config.set {echoLimit:50}` → 期望配置页有字段；当前字段为空且 set 被拒。
6. 关掉插件进程后从 LLM 侧观察工具是否消失；重启后 `plugin.mcp.register` 是否恢复 hot。
7. 直接把 `demo_time/demo_counter/demo_llm` 加入场景白名单 → 确认它们会出现在 LLM 工具面（验证“白名单绕过 hot”）。
8. 发送超大 `text`（如 5MB）→ 观察内存/线程与截断前读取行为。

## 9. 基线修正（仅当共通基线不成立时）

- 基线 §5 契约加载不适用：本工具**没有 `.tool.md`**，也不是 `contract.Register`，而是 Python 手写 JSON-RPC；`required` 只是给模型看的提示，运行时不校验（`plugin.py:191-197`）。
- 基线 §6 成立方式不同：manifest `timeout_sec=60` 经 mgr 透传为 gateway `entry.TimeoutSec`（`mgr/api.go:345-354`、`plugin_servers.go:24-31`），无 `_meta.async` → mode=`auto`、阈值 60s，不是 `async:always`。
- 基线 §3/§4 不适用：`demo_echo` 无路径参数、无写操作、无副作用，沙箱与路径规则无关。
- 基线 §7 产物：`demo-python` 是源码型样例（`product:false`、`autostart:false`），无 dist 产物，不存在产物漂移问题。
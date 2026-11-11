# Python 演示插件（plugin + MCP）

一个**只用 Python 标准库**写的 ChonkPilot 插件，用来演示《Plugin & MCP-Server 开发指南》里
规范的全部能力。可作为「非 Go 插件」的最小参考实现。

```text
capability/plugins/demo-python/
├── manifest.json        # 完整 manifest（插件自读并在 plugin.hello 上报）：runtime / mcp / config / ui / permissions
├── plugin.json          # 极简启动文件（宿主据此拉起）：id / cmd / enabled / name / icon
├── plugin.py            # 插件本体：通道 + MCP + RPC + 数据面
├── tools/demo_echo.tool.md  # 工具契约：策略（async/timeout/on_timeout/hot）+ `# 摘要` + `# 参数` schema
├── llm_bridge_proxy.py  # LLM 桥接入口的 loopback→UDS 字节代理（demo，与插件运行无关）
├── webui/config.html    # iframe 配置页样例（config.get/set + rpc + 事件流）
└── README.md            # 本文件
```

> `manifest.json` 才是完整 manifest（`ui.views` / `ui.assets` / `permissions` 等都在这里）；
> `plugin.json` 只是启动参数（`id/cmd/enabled/name/icon`），不是完整 manifest，二者分别维护。
> 本样例无 exe / 无法 embed，`webui/` 同时作为页面写法参考。

## 运行前提

- Python 3.9+（**注意**：CPython 在 Windows 上**没有 `socket.AF_UNIX`**；本目录的 `llm_bridge_proxy.py` 已内置 WinSock 兜底，Windows 亦可直接运行）；
- `python` 在 PATH 上（或把 `manifest.json` 的 `runtime.exe` 改成绝对路径）；
- 插件默认 `autostart: false`：在「插件」页启用后由宿主拉起。

启用方式：插件管理页 → `Python 演示插件` → 启用。验证走 MCP 工具 `demo_echo`（唯一 hot，默认直接进
LLM 工具面）/ `demo_time` / `demo_counter` / `demo_llm`；后三个缺省只对 gateway/UI 可见，需显式白名单才会下发给模型。

## 演示了规范的哪些能力

| 规范能力 | 实现位置（`plugin.py`） | 说明 |
|---|---|---|
| 通道握手 | `main()` → `plugin.hello` / `plugin.register` | 连 `CHONKPILOT_PLUGIN_ADDR`（AF_UNIX），行分隔 JSON 帧 |
| MCP 注册 | `start_mcp()` + `plugin.mcp.register` | Streamable HTTP（POST `/mcp`，JSON-RPC，Bearer），空端口自选 |
| MCP 工具 | `TOOLS` / `MCPHandler._rpc` / `call_tool` | `demo_echo`（hot）/ `demo_time` / `demo_counter` / `demo_llm` |
| 工具契约 | `tools/*.tool.md` + `build_tools()` | md 提供 name/描述/schema/hot/async/timeout/on_timeout；无 md 的工具保留代码定义 |
| 配置读取 | `load_config()` / `cfg()` | 读 `CONFIG`/`SECRETS`；密钥只由宿主掩码回显 |
| 配置热更新 | `_handle("plugin.config.changed")` | 保存后重读文件并推送 `demo-event` |
| iframe RPC | `rpc()`（`echo`/`stats`/`llm`/`bridge`/`counter.reset`） | 来自 `plugin.ui.rpc`，参数在 `payload.params` |
| 事件订阅 | `plugin.register.subscriptions` + `_dispatch` | 订阅 `session-complete`，计数并推送 |
| 私有数据面 | `counter_get/counter_add` | `plugin/v1/data.read|write` + `storage.self`（需 manifest 授权） |
| **LLM 桥** | `llm_call()` / 工具 `demo_llm` / rpc `llm` | `plugin/v1/llm.step`：插件不持有 apiKey，提示词经通道转发 |
| **传输可选 UDS / TCP** | `Bridge.__init__` | `ADDR` 裸路径 = AF_UNIX；`tcp://host:port` = TCP（同一帧协议） |
| 反向推送 | `publish()` → `plugin.publish` | 只允许 `plugin-event`，前端 `ckPlugin.onEvent` 接收 |
| 状态上报 | `plugin.status.report` | `running` / `degraded`（开 `flaky` 可演练） |
| 健康应答 | `plugin.sys.ping` | 宿主 `runtime.health` 周期性反向 ping |
| 优雅退出 | `plugin.shutdown` | 宿主先发 ctl，`grace_ms` 内未退则 kill |

## 手工调试

```powershell
# 1) 直接跑（宿主必须先起着，否则没有 ADDR）
$env:CHONKPILOT_PLUGIN_ADDR = "$env:LOCALAPPDATA\chonkpilot\plugin.sock"
python -u plugin.py
# 2) MCP 端点：stdout 会打印 ready: mcp=http://127.0.0.1:<port>/mcp
#    用宿主的内置日志确认 plugin.mcp.register 成功（<dataDir>/plugins/demo-python/logs/plugin.log）
# 3) 走 TCP 通道（宿主以 tcp:// 监听时同样可用）
$env:CHONKPILOT_PLUGIN_ADDR = "tcp://127.0.0.1:<port>"
python -u plugin.py     # 日志首行 bridge connected: tcp://127.0.0.1:<port>
```

## LLM 桥接入口（UDS）与 TCP 代理

系统的 LLM 兼容入口只监听 UDS（默认 `%LOCALAPPDATA%\chonkpilot\llm.sock`，`CHONK_LLM_UDS` 可覆盖），
**不再开放任何 TCP 端口**（原 `127.0.0.1:5669` 已删除）。直连 UDS：

```python
import httpx
# CPython 在 Windows 无 AF_UNIX：直连需自行用 WinSock/ctypes 或直接用下面的 proxy；
# Linux/macOS 可直接：
transport = httpx.HTTPTransport(uds=r"%LOCALAPPDATA%\chonkpilot\llm.sock")
r = httpx.post("http://unix/llm/default/v1/chat/completions", json=body, transport=transport)
```

只能用 TCP base_url 的 SDK / 语言，起本目录的代理（**字节级转发、不解析 HTTP**）：

```powershell
python llm_bridge_proxy.py --listen 127.0.0.1:5669
# 再把 SDK 的 base_url 指到 http://127.0.0.1:5669/llm/<provider>/v1
```

代理默认只绑 loopback（非 loopback 拒绝启动）；身份不随代理传递，按调用方限流 / 审计需在应用层做。

## 用这份 demo 写自己的插件

1. 复制本目录 → 改 `manifest.json` 的 `id`/`name`/`runtime`（完整 manifest）与 `plugin.json` 的 `id`/`name`/`cmd`（启动参数）；
2. `plugin.py` 里替换 `call_tool`（MCP 工具执行）以及 `rpc`（配置页方法）；工具契约放 `tools/<name>.tool.md`（`# 基本信息` YAML + `# 摘要` + `# 参数` JSON），`build_tools()` 会用它覆盖代码里的 fallback 定义；
3. 需要宿主数据时在 `permissions.data` 声明 `resource.op`（如 `session.snapshot.get`）；
4. 需要密钥时在 `manifest.json` 的 `config.fields` 标 `"secret": true`；
5. 页面只需要 `webui/` 下的静态文件 + `/plugin-ui/_sdk/plugin.js`，**不要**引外部 CDN。

> 与 Go 插件的差别：Go 用 `pluginsdk` 自动完成握手/MCP 自举；Python 需自己实现（本 demo 即模板）。
> 协议细节以 `docs/plugin-platform/插件与MCP-Server开发指南.md` 为准。

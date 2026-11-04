# Plugin & MCP-Server 开发指南（v1.1）

> 适用：ChonkPilot `PLUGIN-1` 插件平台。**本文是插件开发与实现的唯一操作手册**；设计背景见
> [插件与MCP统一设计方案](插件与MCP统一设计方案.md)。
> 状态：🟡 随实现同步（2026-10-22 起；2026-10-23 对齐实际 SDK / pluginmgr / 数据面并补 §14/§15）。
> 示例与代码以**同级仓 `chonkpilot-plugins/*`** 为准（2026-10-24 起主仓 `src/plugins/` 已整体迁出；SDK/宿主运行时 = `chonkpilot-framework/plugin`）。

---

## 1. 五分钟接入（Go）

```text
plugins/hello/
  plugin.json              # 极简启动信息
  manifest.json            # 完整 manifest（插件自读上报）
  chonkpilot-hello.exe
  webui/config.html        # 可选
```

```go
// main.go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "os"

    "github.com/chonkpilot/chonkpilot-plugin-sdk/pluginsdk"
)

func main() {
    p, err := pluginsdk.New(pluginsdk.Options{
        ID: "hello", Name: "Hello", Version: "1.0.0",
    })
    if err != nil {
        slog.Error("hello: 连接插件桥失败", "err", err)
        os.Exit(1)
    }
    defer p.Close()

    // 1) 暴露一个 MCP 工具（插件内起 Streamable HTTP，宿主自动登记到 gateway）
    p.Tool("hello_say", "打招呼",
        map[string]any{
            "type":       "object",
            "properties": map[string]any{"name": map[string]any{"type": "string"}},
        },
        func(ctx context.Context, args map[string]any, cx pluginsdk.CallContext) (any, error) {
            return map[string]any{"text": "hello " + fmt.Sprint(args["name"]), "instance": cx.InstanceID}, nil
        })
    p.HotTool("hello_say") // 注入 LLM；不调用则仅宿主/插件可调

    // 2) 订阅一条事件（可选）
    p.Subscribe("session-complete", func(_ context.Context, topic string, payload []byte) {
        slog.Info("event", "topic", topic, "payload", string(payload))
    })

    // 3) 处理 iframe / 状态栏发起的调用（可选）
    p.OnRequest("status", func(_ context.Context, _ map[string]any) (any, error) {
        return map[string]any{"ok": true}, nil
    })

    // 4) 进入事件循环；收到 plugin.shutdown 或 ctx 取消后返回
    if err := p.Run(context.Background()); err != nil {
        slog.Error("hello: 退出", "err", err)
    }
}
```

> API 速查（`pluginsdk`）：
> `Tool(name, desc, schema, fn)` 注册工具 · `HotTool(name)` 标记注入 LLM ·
> `OnMCPServer(fn)` 在 SDK 自建 MCP server 上直挂既有 `RegisterTools` · `Subscribe(pattern, fn)` 订阅总线事件 ·
> `OnRequest(name, fn)` 处理 `plugin.ui.rpc` / `plugin.command` · `Call` / `CallInto` 调宿主能力面 ·
> `DataRead` / `DataWrite` 业务数据面 · `ReportStatus(state, detail)` · `Config()` 读配置 · `Run(ctx)` 主循环。

`plugin.json`（极简启动信息，PLUGIN-LOAD-1 D12）：

```json
{
  "id": "hello",
  "cmd": ["chonkpilot-hello.exe"],
  "enabled": true,
  "name": "Hello",
  "icon": "plug"
}
```

`manifest.json`（完整 manifest：由插件进程自读并在 `plugin.hello` 上报；宿主只校验 `id`）：

```json
{
  "schema": "chonkpilot.plugin/v1",
  "id": "hello",
  "name": "Hello",
  "version": "1.0.0",
  "mcp": { "mode": "http", "namespace": "-", "hot_tools": ["hello_say"], "category": "demo" },
  "ui": {
    "views": [
      { "id": "config", "title": "Hello", "entry": "webui/config.html",
        "containers": ["preview", "tab", "drawer", "dialog"] }
    ]
  },
  "config": { "fields": [] }
}
```

把这套目录放到 `<exeDir>/plugins/hello/`（或用户级 `~/.chonkpilot/plugins/hello/`），
启动主程序即可在「插件」页看到它。

---

## 2. 插件包规范

### 2.1 目录

```text
<级别根>/plugins/<id>/
  plugin.json      # 必填：极简启动信息（id / cmd / enabled / name / icon）
  manifest.json    # 必填：完整 manifest（插件自读，hello 上报）
  <exe>            # 可选：缺省按 chonkpilot-plugin-<id>.exe 约定
  webui/           # 可选：iframe 页面（只读静态资源）
  README.md        # 可选
```

四级根（同名最具体级整条覆盖）：`prjusr > project > user > app`。

| 级别 | 路径 |
|------|------|
| app | `<exeDir>/plugins/<id>/` |
| user | `~/.chonkpilot/plugins/<id>/` |
| project | `<workDir>/.chonkpilot/plugins/<id>/` |
| prjusr | `<prjusr 数据根>/plugins/<id>/` |

插件私有数据：`<dataDir>/plugins/<id>/`（宿主创建并通过 env 注入）。

### 2.2 字段

**`plugin.json`（极简；宿主发现期唯一读取源，只校验 ID）**

| 字段 | 必填 | 说明 |
|------|------|------|
| `id` | | 目录名；写了必须一致 |
| `cmd` | | 启动命令：字符串或数组（exe + args）；缺省 `chonkpilot-plugin-<id>.exe` |
| `enabled` | | 缺省 true；仍以 `<数据根>/plugins/enabled.json`（设置页开关）为最终开关 |
| `name` / `icon` | | 上报前 UI 展示用（上报后以 manifest 为准） |

**`manifest.json`（完整 manifest；插件进程自读，`plugin.hello.manifest` 上报）**

| 字段 | 必填 | 说明 |
|------|------|------|
| `id` | ✅ | 必须等于插件 id（宿主只校验这一项） |
| `schema` / `name` / `version` / `description` / `icon` / `publisher` | | UI 展示 |
| `product` | | `true` = 产品插件（受信；`origin=builtin`） |
| `runtime.restart` | | `{backoff_ms,max_backoff_ms,max_fails}`（宿主默认 1000/30000/3，上报值 hello 后生效） |
| `runtime.health` | | `{interval_ms,timeout_ms,fail_threshold}`（默认 15000/3000/3）；宿主周期性反向 `plugin.sys.ping`，连续失败达阈值即 kill（由守护退避重启）。SDK 已内置应答 |
| `runtime.stop` | | `{grace_ms}`（默认 5000） |
| `mcp.mode` | | `http`（默认）/ `tools` / `none` |
| `mcp.namespace` | | `"-"` = 原样（默认）；其他值 = 前缀 |
| `mcp.hot_tools` | | 缺省 `["*"]`；工具名数组 |
| `mcp.timeout_sec` | | 缺省 60 |
| `mcp.category` / `mcp.origin` | | 缺省 `plugin` / `user`；产品插件填 `builtin` |
| `mcp.tools[]` | `tools` 模式 ✅ | `{name,description,schema,hot,idempotent}` |
| `config.fields[]` | | `{key,type,default,secret,required,min,max,label}` |
| `ui.views[]` | | `{id,title,icon,entry,containers,default_size}`；`id=config` 视为**配置视图**（插件维护卡片显示「配置」按钮） |
| `ui.menus[]` | | `{location:"plugin",label,icon,view}` |
| `ui.statusbar[]` | | `{id,zone,icon,tooltip,command}` |
| `ui.actions[]` | | 插件维护卡片的**动作按钮**：`{id,label,icon,tooltip,style,view,container}` 或 `{…,command}`（见 §7.4） |
| `ui.rpc[]` | | 允许 iframe 调用的插件方法名 |
| `permissions.data[]` | | `resource.op[:selector]`（见 §6.1） |
| `permissions.hooks[]` | | 允许订阅的主题 / hook stage |
| `permissions.llm` | | `{step:true}` |
| `permissions.session[]` | | `chat.send` / `chat.interject` / `tools.call` … |
| `permissions.network[]` | | 声明用途（审计/宿主代理校验） |

**硬规则**

1. `plugin.json` 若写 `id` 必须等于目录名（宿主只校验 ID）；上报的 `manifest.id` 必须等于插件 id，否则 hello 失败、进程退出。
2. `mcp.mode` **不得为 `stdio`**：插件进程由宿主独占管理，stdlib 会再 spawn 一份并独立 kill。
3. `mcp.mode=http` 上报的 `url` 必须是 `http://127.0.0.1:<port>`，并带 token。
4. `webui` 资源只从插件目录 `webui/` 读取；`entry` 不得含 `..`。
5. 配置只允许写 `config.fields[].key` 声明的键。

---

## 3. 生命周期与环境变量

宿主按 `plugin.json` 的 `cmd` spawn，注入：

| 环境变量 | 说明 |
|----------|------|
| `CHONKPILOT_PLUGIN_ADDR` | 插件桥地址（UDS 路径 / `tcp://host:port`） |
| `CHONKPILOT_PLUGIN_ID` | 插件 id |
| `CHONKPILOT_PLUGIN_ROOT` | 插件目录（只读资源根） |
| `CHONKPILOT_PLUGIN_DATA_DIR` | 私有数据目录（可写，`<dataDir>/plugins/<id>/`） |
| `CHONKPILOT_PLUGIN_CONFIG` | 非密配置 JSON（`<DATA_DIR>/config.json`，宿主写、插件只读） |
| `CHONKPILOT_PLUGIN_SECRETS` | 密配置 JSON（`<DATA_DIR>/secrets.json`，0600） |
| `CHONKPILOT_PLUGIN_WORK_DIR` | **可选**：宿主当前未注入；需要时从工具上下文 `_meta.chonkpilot.work_dir` 取 |
| `CHONKPILOT_PLUGIN_TOKEN` | **可选**：预置 MCP Bearer token；缺省由 SDK 自行生成随机值 |

工作目录 = 插件目录（`cmd.Dir`）；`CHONKPILOT_PLUGIN_ROOT` 只读。

状态机（宿主侧 `plugin-status` 可观测）：

```text
disabled ──启用──> starting ──hello+register──> running ──shutdown──> stopped
                      │  ▲                                          ▲
                      │  └─退避重启（restart.max_fails 内）──────────┘
                      └──超过次数──> error（管理页可「重试」= 重新启用）
degraded = 插件自报（plugin.status.report），不触发重启
```

- 插件一律由宿主 spawn（`external` 模式已删除，2026-11-02 / PLUGIN-LOAD-1 D14）。
- 退出：宿主先发 `plugin.shutdown`（ctl），`runtime.stop.grace_ms` 内未退则 `Process.Kill`。

**注意**：宿主每次 spawn 都会注入全新 `ADDR`；插件不得缓存旧地址。

---

## 4. 通信协议（UDS/TCP）

### 4.1 帧

一行一个 JSON：

```json
{"k":"req","id":1,"name":"plugin.hello","payload":{...}}
{"k":"res","id":1,"ok":true,"result":{...}}
{"k":"evt","name":"session-complete","payload":{...}}
{"k":"ctl","name":"plugin.shutdown","payload":{"grace_ms":5000}}
```

### 4.2 握手

```text
plugin.hello    {id,name,version,features?}
                → {plugin_id,instance_id,version,methods[]}
plugin.register {features?,subscriptions[]?,ui?}   → {ok,plugin_id,instance_id,subscriptions}
plugin.ready    {}                                 → {ok}        # 可选：声明已就绪
```

- `plugin.hello` 后宿主分配 `instance_id`（`plugin:<id>:<n>`），**同一连接内 id 不可变更**；
- `ui` 段 = manifest `ui` 同构的运行时覆盖（`plugin.ui.register` 走同一入口）；
- 晚连接插件会自动收到一次 `server-ready` 事件（宿主就绪时补发）。

### 4.3 插件 → 宿主：能力方法

控制帧（`controlHandlers`，不经业务注册表）：

| 方法 | 说明 |
|------|------|
| `plugin.hello` / `plugin.register` / `plugin.ready` / `plugin.exit` | 握手 / 退出 |
| `plugin.subscribe` / `plugin.unsubscribe` | 事件订阅（也可在 register 里批量给） |
| `plugin.sys.ping` / `plugin.sys.info` | 健康 / 方法面 |
| `plugin.filter.register` / `plugin.tool.bind` / `plugin.tool.unbind` | 同步钩子 / 工具绑定 |
| `plugin/v1/instance.claim` / `instance.list` | 实例 |
| ~~`plugin.index.take|ack|pending|workdirs`~~ | **2026-10-25 退场（D-71）**：待索引队列已删除；插件订阅 `filesys.changed` 自管索引 |
| `plugin.mcp.register` / `plugin.mcp.unregister` | 登记 MCP（§5） |
| `plugin.ui.register` | 登记视图/菜单/状态栏（§7） |
| `plugin.status.report` | `{state,detail}`（`running`/`degraded`/`error`） |
| `plugin.publish` | 仅 `plugin-event`（推给自己的 iframe） |
| `plugin/v1/data.read` / `plugin/v1/data.write` | 统一业务数据面（§6.1） |

业务能力（白名单映射到既有 MQ 主题，未列出不可达）：

| 前缀 | 方法 |
|------|------|
| LLM | `plugin/v1/llm.step`（`{prompt,system?,instance_id?,llm?}` → `{text}`） |
| 会话 | `session.start/send/cancel/list/get/history/content/snapshot/rename/delete/active` · 写：`session.snapshot.set` / `session.summary.set` / `session.messages` / `session.context` |
| 插话 | `session.inbox.add/list/discard/promote` |
| 任务 | `task.tree/tasks/stop/background/verify` · `tool.retry` · `ask.reply` |
| 配置 | `config.user.get/load` · `config.project.get/load/set/delete` · ~~`config.filelist.list`~~（2026-10-25 退场：清单改插件自持本地文件） · `config.memory.list` · `config.mcp.list` · `config.scenario.list` · `config.prompt.get/load/set` |
| 知识 | `knowledge.list/read` |
| 文件 | `file.list/read/write/mkdir/remove/rename/copy`（限 workdir） |
| 工具 | `tools.list/call/wait/timeout/register/unregister` · `tool.notify` |
| 资产 | `prompts.list/get` · `resources.list/read` · `servers.list/get/register/unregister` · `gateway.check` |
| 运维 | `memory.flush` · `vfts.reindex` · `vfts.dict.set` · `prompt.optimise` |
| 宿主策略 | `instance.resolve{work_dir}`（→ `{found, instance_id?}`）· `sandbox.resolve{work_dir}`（→ `{writable_dirs}`；定时任务等**无 tools/call 上下文**的场景用）· `workdir.ensure{work_dir, hidden?}`（→ `{instance_id, work_dir, opened}`；未打开的项目由宿主**隐藏打开**并等 `instance-register` ≤12s；超时/失败回明确错误） |

> **项目已打开保证（SANDBOX-WRITE-1 P3，2026-11-03）**：后台类插件（定时任务等）在需要项目数据面/
> 用户白名单之前调 `plugin/v1/workdir.ensure{work_dir, hidden:true}`：
> - 已打开 → 返回主实例 id（`opened=false`，不建窗）；
> - 未打开 → 宿主隐藏打开项目（C13/C17：正常打开流程——`instance-register` / `workdir-opened` /
>   通知各插件 / 索引照常，只是窗口不显示）→ 等 `instance-register` → `opened=true`；
> - 失败/超时 → **明确错误**：调用方应「本轮跳过 + 记错 + 下轮重试」，**不要把任务置为失效**。
>
> **写隔离（SANDBOX-WRITE-1，2026-11-03）**：插件处理写操作时可从两处拿「可写白名单」——
> ① 工具调用上下文 `CallContext.Sandbox`（= gateway 下发的 `_meta.chonkpilot.sandbox{enabled, writable_dirs}`）；
> ② 无调用上下文时（如定时任务执行前）调 `plugin/v1/sandbox.resolve{work_dir}`（先用 `workdir.ensure`
> 保证项目已打开 → 才能拿到用户配置的 prj `security-*`，否则只有内置默认项）。
> 拿到后按白名单做写路径校验（`chonkpilot-util/sandbox.Policy.AllowsPath`）或对脚本子进程施加
> Low IL + Job（`scriptrun.Options.Sandbox`，执行前先 `sandbox.EnsureLowILCached`）。
> **拿不到策略 → fail-open**（不阻断业务，但必须在自身状态/运行记录里写明原因）。

> **未声明权限 = 拒绝**（`permission_denied`）。方法名与 payload 以本文为准，新增须同步
> `docs/spec/60-reference/61-消息一览.md` 或协议附录。

### 4.4 订阅事件

```json
{"subscriptions":[{"pattern":"session-compress","mode":"async"}]}
```

常用主题：`session-turn-start` · `session-complete` · `session-compress` · `session-interrupted` ·
`subchat-opened` · `tool-notify` · `filesys.changed` · `data-<domain>-refresh` · `instance-*` ·
`server-ready`。

### 4.5 宿主 → 插件（反向请求）

| 名称 | 载荷 | 说明 |
|------|------|------|
| `plugin.tool.call` | `{subject,payload:{tool,args,context}}` | `tools` 模式工具调用 |
| `plugin.tool.cancel` | `{call_id}` | 取消 |
| `plugin.filter.run` | `{stage,payload}` | 同步钩子 |
| `plugin.ui.rpc` | `{method,params,req_id}` | iframe 发起的插件方法 |
| `plugin.command` | `{name,params}` | 状态栏/管理页按钮 |
| `plugin.config.changed` | `{keys:[...]}` | 配置已保存 |
| `plugin.shutdown` | `{grace_ms}` | 退出 |

---

## 5. MCP-Server 接入

### 5.1 三种模式

| mode | 做法 | 工具名 |
|------|------|--------|
| `http`（推荐） | 插件内起 Streamable HTTP（`127.0.0.1:0` + Bearer），上报 `{url,token}` | 由 `namespace` 决定（默认原样） |
| `tools` | 不起 HTTP；用 `plugin.mcp.register{mode:"tools",tools:[...]}` 声明 schema（**宿主当前返回「暂未实现」，请用 `http`**） | 原样（需宿主注册规则同步） |
| `none` | 无工具面 | — |

**禁止 `stdio`**：见设计稿 §5.7.1。

### 5.2 上报示例

```json
{"url":"http://127.0.0.1:53127/mcp","token":"<random>","hot_tools":["web_search"]}
```

宿主收到后调用 gateway `servers/register`：

```json
{ "name": "plugin-websearch", "kind": "server",
  "mcp_server": { "url":"http://127.0.0.1:53127/mcp", "transport":"http",
                  "headers":{"Authorization":"Bearer <random>"},
                  "namespace":"-", "hot_tools":["web_search"],
                  "timeout":120, "category":"search", "origin":"builtin" } }
```

### 5.3 调用上下文

`origin=builtin` 的下游在每次 `tools/call` 收到：

```json
{ "_meta": { "chonkpilot": {
    "instance_id":"...", "work_dir":"...", "data_dir":"...",
    "session":"...", "turn":"...", "tool_call_id":"...",
    "top_session":"...", "parent":"...", "purpose":"..."
} } }
```

多项目插件必须按 `instance_id → work_dir` 路由。

### 5.4 取消

gateway 取消 → HTTP 请求 ctx 取消；插件 handler 必须尊重 `ctx`，并释放子进程/查询。

### 5.5 复用既有 `RegisterTools(srv *mcp.Server)`

已有引擎（codegraph / vfts 等）若已导出 `RegisterTools(srv *mcp.Server) error`，不必重写工具：
用 `p.OnMCPServer(fn)` 直接在 SDK 自建的 MCP server 上挂载，`p.MCPHot(...)` 声明注入 LLM 的工具名。

```go
p.OnMCPServer(func(srv *mcp.Server) error {
    if err := codegraph.RegisterTools(srv); err != nil { return err }
    return vfts.RegisterTools(srv)
})
p.MCPHot("codegraph_symbol_search", "vfts_query")
```

注意：
- `OnMCPServer` 注册的工具 SDK **无法自动识别** hot 标记 → 必须 `MCPHot` 或 manifest `mcp.hot_tools` 显式声明；
- 只要 `Tool` 或 `OnMCPServer` 任一存在，`Run` 就会起 MCP HTTP 并登记（`mcp.mode=http`）。

### 5.6 插件内直调自己的工具（配置页）

配置页的「数据读写」推荐两步：iframe → `ckPlugin.call('rpc',{method:'tool.call',params:{name,args}})` → 插件
`OnRequest("tool.call", ...)` 内部直调引擎导出函数（如 `codegraph.Call(ctx,name,args)`），不必绕 MCP 传输。

---

## 6. 宿主能力面

### 6.1 业务数据（`plugin/v1/data.read|write`）

```json
{"resource":"session","op":"snapshot.get","instance_id":"gui-1",
 "params":{"session_id":"s-01"}}
```

`resource` + `op` 拼成 `resource.op` 精确匹配下表（闭集，未列出 = `args_invalid`）：

| `resource` | `op` | 读/写 | 说明 |
|------------|------|-------|------|
| `session` | `list` / `get` / `history` / `content` / `context` / `messages` | 读 | `messages` = `data-session-load-messages` |
| `session` | `snapshot.get` | 读 | 返回 `{snapshot}`（无快照 = `null`） |
| `session` | `snapshot.set` | 写 | `{session_id,snapshot}`；`snapshot` 形状 = `{history[],snapshot_turn}` |
| `session` | `summary.set` | 写 | 会话摘要 |
| `session` | `inbox.list` / `add` / `discard` / `promote` | 读/写 | 插话 |
| `task` | `tree` / `tasks` | 读 | 任务树 |
| `task` | `stop` / `background` / `verify` | 写 | 任务控制 |
| `config` | `prj.get` / `prj.set` / `prj.delete` | 读/写 | 项目配置 |
| `config` | `usr.get` | 读 | 密钥字段恒掩码 |
| `prompt` | `get` / `set` | 读/写 | `summary_prompt` 等 |
| ~~`filelist`~~ | ~~`list` / `put` / `del`~~ | — | **2026-10-25 退场**：索引清单改插件自持本地文件 `<workdir>/.chonkpilot/vfts/file_list.json`（不再经能力面） |
| `memory` | `list` / `read` / `write` | 读/写 | 记忆 |
| `knowledge` | `list` / `read` | 读 | 只读 |
| `filesys` | `list` / `read` / `write` / `mkdir` / `remove` / `rename` / `copy` | 读/写 | 限 workdir |
| `storage.self` | `get` / `set` / `del` / `list` | 读/写 | 插件私有 KV（`params.key` 仅 `[A-Za-z0-9._-]{1,64}`） |

权限串 = `resource.op`（如 `session.snapshot.set`）；`resource.*` 与 `*` 为通配。

**应答形状**：`{ok:true, result:<数据面 result>}`；数据面自身失败 → `{ok:false,error}` 转 `internal` 错误。

**禁止**直连 `chonkpilot.db` / 写 `capability/` 内部文件；写操作一律经本面，宿主做归属、
字段、状态机校验。

### 6.2 Hook

| Hook | 注册 | 语义 |
|------|------|------|
| `llm.before_start` | `plugin.filter.register` | 可 replace/drop |
| `llm.before_send` | 同上 | 可改写消息 |
| `llm.before_simple` | 同上 | `llm-simple` 前 |
| `tool.before_call` | 同上 / 工具 `pre_hook_subject` | 失败 = 拒绝调用 |
| 事件 | `plugin.subscribe` | 异步观察 |

### 6.3 LLM

`plugin/v1/llm.step`：单次、无会话、无工具；60s；`instance_id` 决定 usr 配置与默认模型。
插件**拿不到 apiKey**。

### 6.4 会话

| 场景 | 方法 |
|------|------|
| 单聊 | `session.start` + `session.send` |
| 子聊/多聊 | `subchat.open` + `subchat.finish` |
| 插话 | `session.inbox.add`（`insert`/`next_turn`） |
| 取消 | `session.cancel` / `tool.stop` |
| 调工具 | `tools.call` / `tools.wait` |
| 群聊 | 🔵 待补 `group.open` |

---

## 7. UI 规范

### 7.1 视图

```json
"ui": { "views": [
  { "id":"config", "title":"联网搜索", "icon":"search",
    "entry":"webui/config.html",
    "containers":["preview","tab","drawer","dialog"],
    "default_size":{"width":760,"height":560} } ] }
```

- 宿主注册为 viewKey `plugin:<id>:<view>`；可用 `open-view{viewKey,target}` 打开到
  `tabs` / `drawer` / `dialog`；`preview` 走 `preview-tab-open{kind:'plugin-page',plugin,page}`。
- iframe 以 `sandbox="allow-scripts allow-forms"` 加载（无 `allow-same-origin`）。

### 7.2 iframe ↔ 宿主桥

```js
// iframe → 父
parent.postMessage({ ckPlugin: 1, id: "r1", type: "call", method: "config.get", params: {} }, "*");
// 父 → iframe
{ ckPlugin: 1, id: "r1", type: "result", ok: true, result: { values: {...} } }
```

允许方法：`config.get` / `config.set` / `config.reset` / `data.read` / `data.write` /
`rpc`（白名单 = `ui.rpc`）/ `open-url` / `close` / `log`。

#### 7.2.1 实例归属与定向投递（PLUGIN-INSTANCE-1）

- **前端取法**：宿主打开插件页时 URL 带 `?p=<page>&instance_id=<承载窗口实例>`；插件页首帧读取该参数。
  `instance_id` **不是凭据**，可用于 URL。**禁止**把它当插件自身的 `instance_id`（插件握手拿到的是
  `plugin:<id>:<n>` 命名空间，两者不可混用）。
- **后端取法**：
  - **有来源**（事件 / 工具调用 / 订阅回调）：直接用消息里的 `instance_id`；
  - **无来源**（定时任务等）：`plugin/v1/instance.resolve{work_dir}` 解析**同 workdir 的主窗口实例**
    （SDK：`ResolveMainInstance`），未命中返回 false。
- **定向推送**：`plugin.publish` 增**可选** `instance_id`（SDK：`PublishTo` / `PublishUI`）；
  宿主广播时提升到顶层 `{id, instance_id?, topic, payload}`，缺省 = 广播；无来源场景用
  `PublishForWorkDir(workDir, topic, payload)`，未命中 → 丢弃并记日志（**不回落广播**）。
- UI 事件不排队、不重放：目标窗口关闭期间的事件丢失；投递目标只覆盖承载窗口所在实例。

### 7.3 状态栏

```json
"ui": { "statusbar": [
  { "id":"websearch", "zone":"right", "icon":"search", "tooltip":"联网搜索",
    "command": { "target":"host", "action":"open-view", "view":"status" } } ] }
```

`command.target=plugin` → 宿主发 `plugin.command{name,params}` 给插件。

### 7.4 插件维护卡片（`ui.actions`）

插件维护视图（工具栏「插件」按钮 / 设置菜单「插件维护」→ `preview-tab-open{kind:'plugin-manager'}`）
为每个插件渲染一张卡片：**名称 / 版本 / 状态**、**启动命令**（`plugin.json.cmd`），
**说明**（`description`）、**启用开关**、**配置按钮**（仅当注册了 `ui.views` 中 `id=config` 的视图）、
以及插件自己注册的**动作按钮**（如「索引进度」打开进度详情）。

动作按钮与布局栏按钮同形：**按钮名 + 图标 + 命令 + 打开容器**，两种写法：

```json
"ui": { "actions": [
  // ① 便捷式：打开本插件已注册的 view，container 决定打开位置
  { "id":"index-status", "label":"索引进度", "style":"icon-text",
    "icon":"<svg viewBox=\"0 0 16 16\">…</svg>",     // 宿主图标名 或 内联 SVG
    "tooltip":"打开代码索引进度与最近一次索引结果",
    "view":"status", "container":"drawer" },

  // ② 命令式：直接下发布局命令（与栏按钮同一命令对象）
  { "id":"reindex", "label":"重建索引", "icon":"refresh", "style":"icon-text",
    "command": { "type":"publish-mq", "topic":"plugin-command",
                 "payload": { "name":"reindex", "params": {} } } } ] }
```

| 字段 | 必填 | 说明 |
|------|------|------|
| `id` | ✅ | 插件内唯一 |
| `label` | ✅ | 按钮名（缺省回落 `id`） |
| `icon` | | 宿主图标名（`components/icon/icons.js` 的键）或内联 `<svg>…</svg>` |
| `tooltip` | | 悬浮说明 |
| `style` | | `icon` / `icon-text`（缺省）/ `text`（与栏按钮同口径） |
| `view` | ① ✅ | 目标 view id，必须已在 `ui.views` 注册 |
| `container` | | ① 的打开位置：`preview`（缺省）/ `tab` / `drawer` / `dialog`；缺省 = 该 view `containers[0]` |
| `command` | ② ✅ | 布局命令对象：`open-dialog` / `open-tabs` / `open-drawer` / `toggle-drawer` / `close-tabs-active` / `publish-mq`；`plugin-command` 载荷缺 `id` 时自动补当前插件 id |

约束（宿主 `loadManifest` 校验）：`id` 非空且唯一；`view` 与 `command` 至少一个；`view` 必须已注册；
`container` 仅限上述四值；`style` 仅限上述三值。校验失败 → 该插件不加载（`status=error`，日志可见）。

---

## 8. 配置与密钥

**配置保持四级（2026-10-24 第二版）**：插件的默认参数按**四级配置**（`system` / `usr` / `prj` / `prjusr`）存放，**写哪一级由插件自己的配置页决定**；能力面**按 scope 提供方法**：`plugin/v1/config.user.set` / `config.project.set` / `config.prjusr.set`（与 `get` / `load` / `delete` 对称）。manifest 的 `config.scope` 只作**声明**（平台不据此选级、不做强制校验）；「密钥恒落 usr」只是存储位置惯例。`plugin-config-get/set`（插件管理页用、只按 `{id}`）读写的仍是**插件自身默认参数**，与本条不冲突。
（2026-10-24 第二版：此前「配置不分级 / 只有一层 / 删 `config.scope`」的表述**作废**——分级照旧，只是选级权交给插件配置页。）

**权威存储 = 数据库**（2026-10-23）：usr 层 `chonkpilot.db` 两桶 `plugin_config` / `plugin_secret`
（key = 插件 id）。`<dataDir>/plugins/<id>/` 下的 JSON 只是**运行时快照**（宿主在 spawn / 保存配置时
从库里物化），插件侧读取方式不变；库不可用时自动降级为纯文件态。

```text
<dataDir>/plugins/<id>/
  config.json      # 运行时快照（非密；权威在 DB 的 plugin_config）
  secrets.json     # 运行时快照（密，0600；权威在 DB 的 plugin_secret）
  storage/         # storage.self 私有 KV（<key>.json）
  logs/plugin.log  # 插件 stdout/stderr + iframe log
```

链路（配置页 → 工具执行）：

```text
iframe config.set
  → plugin-config-set（pluginmgr）
  → chonkpilot.db[plugin_config/plugin_secret]  ← 权威
  → 物化 config.json / secrets.json
  → 广播 plugin-config-changed + 反向 plugin.config.changed
  → SDK reloadConfig → p.Config()
  → 工具 handler 每次调用读取（默认参数/上限/密钥）→ 实际执行
```

> 历史文件态（无 DB 行）会在首次读取时**自动回灌入库**（一次性迁移，日志 `plugin-config migrated`）。

- 合并顺序：`config.fields[].default` → DB 非密行 → DB 密行；
- `config.get` 对 `secret:true` 字段只回掩码（`abcd****wxyz`）；保存时传空/掩码 = 不修改；
- `config.set` 拒绝未声明的键（`config_invalid`），`required` 缺失直接失败；
- 保存 = 原子写（`.tmp` + rename）+ 广播 `plugin-config-changed` + 异步发 `plugin.config.changed`；
- 插件读 `CHONKPILOT_PLUGIN_CONFIG` / `CHONKPILOT_PLUGIN_SECRETS`（SDK `Config()` 已封装，收到变更会自动重载）。

---

## 9. 权限

```json
"permissions": {
  "data": ["session.snapshot.get", "session.snapshot.set", "config.prj.get", "storage.self"],
  "hooks": ["session-compress"],
  "llm": {"step": true},
  "session": ["chat.send", "chat.interject", "tools.call"],
  "network": ["api.deepseek.com"]
}
```

- `data` 是**当前唯一强制校验项**：`plugin/v1/data.read|write` 逐次比对 `resource.op`
  （支持 `resource.*` / `*`）；未命中 → `permission_denied`。
- 其余段（`hooks` / `llm` / `session` / `network`）目前为**声明 + 审计**，不做逐次拦截。
- **不是 OS 沙箱**：插件以当前用户运行，能读写用户可访问文件、能直连网络；
  `network` 仅用于审计与后续代理校验。

---

## 10. 日志、错误、幂等

- 日志：stdout/stderr 由宿主重定向到 `<dataDir>/plugins/<id>/logs/plugin.log`（追加、不滚动）；
  iframe 侧 `ckPlugin.call('log',{message})` 追加 `[ui]` 行。**不要**自己再开日志窗口。
- 错误码（`pluginbridge.Code*`）：`args_invalid` / `plugin_unregistered` / `plugin_unauthorized` /
  `permission_denied` / `method_not_found` / `mcp_conflict` / `internal`。
  `config_invalid` 由插件管理面返回（非桥错误码）。
- 幂等要求：`plugin.mcp.register/unregister`、`plugin.ui.register`、`config.set` 可重复调用；
  工具调用由 gateway 决定是否重试（插件声明 `idempotent` 时才安全）。
- **宿主未实现**并发上限与耗时熔断：单插件长耗时调用会占用该连接的请求处理，
  插件必须自行限流并把阻塞工作放 goroutine（工具 handler 已按调用并发执行）。

---

## 11. 构建与打包

| 产物 | 构建 |
|------|------|
| 产品插件 exe + webui + manifest | `build-plugins.ps1`（由 `build-desktop.ps1` 调用） |
| 主程序 | `.\build-desktop.ps1` |

```text
dist/plugins/<id>/
  plugin.json          # 源：src/initdata/capability/plugins/<id>/plugin.json（极简）
  manifest.json        # 源：src/initdata/capability/plugins/<id>/manifest.json（完整，插件自读）
  chonkpilot-<id>.exe  # 源：build/plugin-apps/<id>/（build-plugins.ps1 产出）
  webui/               # 源：src/initdata/capability/plugins/<id>/webui/
  zvec_c_api.dll       # 仅 codegraph：必须与 exe 同目录（vfts 引擎运行时加载）
```

- `build-plugins.ps1` → staging `build/plugin-apps/<id>/`；`build-desktop.ps1` 步骤 4.5 先复制
  plugin.json / manifest.json / webui 到 `dist/plugins/<id>/`，再调 `build-plugins.ps1` 把 exe 覆盖进去。
- codegraph 插件 = codegraph 引擎 + vfts 引擎合并（CGO：tree-sitter + zvec），构建脚本已注入 zvec 头/库路径。
- 禁止把插件私有数据 / 密钥写入产物目录。

---

## 12. 自测清单

1. `plugin.hello` + `plugin.register` 成功，`sys.info` 有方法面。
2. `plugin-list` 能看到插件、状态为 `running`。
3. `tools-list` 能看到工具；`web_search` 之类调用可执行并带 `_meta.chonkpilot`。
4. 禁用 → 进程退出、工具注销、菜单/状态栏消失。
5. 崩溃 → 退避重启 → 工具面恢复。
6. 配置保存 → 重启后仍生效；密钥只显示掩码。
7. iframe 页打开、`config.get/set`、`rpc` 正常；未授权方法被拒。
8. 宿主重启 → `plugin-list` 快照立即可用；插件重新 spawn 后经 hello 上报 manifest 收敛
   （hello → `plugin.mcp.register` → `running`）。
9. 杀进程 → 退避重启（`restart.max_fails` 内）；连续 `health` ping 失败同样触发重启。

---

## 13. 参考实现

| 插件 | 说明 |
|------|------|
| `chonkpilot-plugins/web` | 三个联网工具共用一个配置页：`web_search`（DeepSeek 检索）/ `web_fetch`（DeepSeek 抓取 Markdown）/ `web_download`（本地直连：原始内容或下载；重定向可配） |
| `chonkpilot-plugins/codegraph` | codegraph + vfts 合一（`OnMCPServer` 复用引擎 `RegisterTools`；配置页经 `tool.call` 直调） |
| ~~`chonkpilot-plugins/compress`~~ | **压缩已内建（非插件）**：会话侧压缩内核（`chonkpilot-agent/session`）+ `llm/server` 编排；无插件运行时形态 |
| `chonkpilot-plugins/browser` | 浏览器插件：桥接层 + 同目录引擎 exe（WebView2，UDS `start/stop/operation`），按实例复用 context；**不注册 MCP 工具**（原 `browser_run` 已删除） |
| `chonkpilot-plugins/desktop` | 桌面插件：同构（Win32/UIA 引擎）；**不注册 MCP 工具**（原 `desktop_run` 已删除） |
| `chonkpilot-plugins/file` | 文件系列 11 工具：解析 `tools/file/*.tool.md` 契约 → 注册 MCP 工具 → 进程内 `toolset.Dispatch`；默认参数（purpose/encoding/targets 上限）可配 |
| `chonkpilot-plugins/task` | 任务系列：`task_run` 执行 / `task_llm` 单次 LLM / `task_status` 任务树 / `task_stop` 终止 |
| `chonkpilot-plugins/mcp` | MCP 系列：`mcp_find` / `mcp_load` / `mcp_invoke`（全部经宿主能力面，不直连 gateway 内部） |
| `src/initdata/capability/plugins/demo-python` | **Python 插件 + MCP 演示项目**（仅标准库，覆盖本文全部章节；见该目录 `README.md`） |
| `chonkpilot-plugins/{browser,desktop}/engine` | 上述两个插件的**引擎**本体（仍只讲自有 UDS，由桥接层分别拉起/转发） |

> **工具归属变更（2026-10-23）**：`file_*`（11）、`mcp_*`（3）、`script_run`、`codegraph_*`（8）、`vfts_query`
> 的契约已从 `capability/tools/` 移除（`file` 仍保留源文件供 file 插件解析，但不再进入 core capability 根），
> 由对应插件注册。core 只剩平台域工具（`server` 组）。

---

## 14. 异常与重启恢复（强制）

宿主对插件的托管语义（与 [71-编程规范 §3.3](../spec/70-conventions/71-编程规范.md) 同口径）：

| 问题 | 宿主行为 | 插件要求 |
|------|----------|----------|
| 状态落在哪 | 宿主内存态（`status/detail/pid/restarts`）；**持久化** = 启用表 `enabled.json` + 插件自己的 `config.json`/`secrets.json`/`storage` | 业务状态必须落 `DATA_DIR`（崩溃后自行重建），不得只放内存 |
| 输入是否受理即持久化 | 宿主不代插件持久化 | 不可丢的输入（待索引文件、待上报数据）必须**写盘后再 ack** |
| 崩溃瞬间丢什么 | 内存态全丢；正在执行的工具调用由 gateway 收到连接断开 → 工具报错 | 工具调用需可重试/幂等（或用 `storage` 记检查点） |
| 非终态由谁收口 | `supervise` 退避重启（`max_fails` 后置 `error`）；宿主退出时 `shutdown` → `grace` → kill | 启动时清理自己的 `*.tmp` / 未完成作业（重启恢复） |
| 重启后重建还是收敛 | 重新 spawn → 插件重新 `hello/register/mcp.register` → 工具面自动恢复 | 重建内存索引/队列（从 `DATA_DIR` 读） |
| 副作用语义 | 至少一次：崩溃后会被重新拉起 | 重复执行必须幂等（注册/配置写天然幂等；写盘用原子替换） |

**验收底线**：① kill 插件进程 → 管理页状态变 `starting` 后回 `running`，工具面恢复；
② 宿主重启 → `plugin-list` 快照立即可用，启用表生效；③ `error` 状态可在管理页「重试」恢复。

---

## 15. 测试（mq 驱动 + mq 断言）

口径见 [50-测试体系](../spec/50-testing/50-测试体系.md) §1：**一切经消息驱动、经消息断言**。

| 层 | 做什么 | 断言 |
|----|--------|------|
| L1 纯函数 | manifest 校验 / 配置合并与掩码 / `DataTopic` 闭集 | 表驱动，无进程 |
| L2 进程内 | 桩插件（`pluginsdk` 连同一个进程内桥）→ 注册 → MCP 登记 | `plugin-list` 出现 `running`；`tools/list` 含工具；禁用后工具消失 |
| L3 进程外 | 真 exe（web / codegraph / compress） | 崩溃退避重启；配置落盘后重启仍生效；`_meta.chonkpilot` 透传 |
| L4 UI | iframe 打开 / `config.get/set` / `rpc` / 未授权方法 | postMessage 应答 `ok`；拒绝项返回错误文案 |

本地手工验收（无自动化时）：

```powershell
# 1) 构建
.\build-plugins.ps1 ; .\build-desktop.ps1
# 2) 启动后：插件页应出现 web / codegraph / compress / file / task / mcp / browser / desktop
# 3) 关掉某个插件 exe（任务管理器）→ 观察 status 从 running → starting → running
# 4) 改配置（密钥只显示掩码）→ 重启 → 值保留；插件日志见 <dataDir>/plugins/<id>/logs/plugin.log
```

---

## 16. 复用层（插件框架）

**只写业务**：下列能力都由 SDK 提供，插件里不要再各写一份。

| 需求 | 用这个 | 说明 |
|------|--------|------|
| 参数取值 | `pluginsdk.Str / Int / Bool` | 任意 JSON 值 → string/int/bool（含 `json.Number`、数字字符串） |
| 剥 `{ok,result}` 信封 | `pluginsdk.Unwrap / UnwrapMap` | 数据面 / 工具调用应答最多 3 层，调用方不必数层数 |
| 补默认参数 | `pluginsdk.MergeMissing(args, defaults)` | 显式入参优先，永不覆盖 |
| 调 LLM | `(*Plugin).LLM(ctx, prompt, system, timeout)` | 走 `plugin/v1/llm.step` 并直接返回文本（插件不持有 apiKey） |
| 标准 RPC | `pluginsdk.PingHandler(v)` / `StatusHandler(v, build)` | 统一 `{ok,version,...}` 形状，管理页/健康检查共用 |
| 契约即工具 | `pluginsdk/contract`：`LoadDir` / `Register` / `HotNames` | 把 `*.tool.md` 直接变成 MCP 工具（file 插件即此模式） |
| 桥接既有引擎 | `pluginsdk/engineclient`：`New` / `Ensure` / `Call` | legacy pluginhost 的 UDS 行 JSON（start/stop/operation/ping），browser/desktop 共用 |
| 工具注册 / 配置 / 事件 / 反向调用 | `pluginsdk.Plugin` | `Tool` / `HotTool` / `OnMCPServer` / `OnRequest` / `Subscribe` / `Call` / `DataRead` / `DataWrite` |

典型插件骨架（≈30 行）：

```go
p, _ := pluginsdk.New(pluginsdk.Options{ID: "x", Name: "X", Version: "1.0.0"})
p.Tool("x_do", "做事", schema, func(ctx context.Context, args map[string]any, cx pluginsdk.CallContext) (any, error) {
    text, err := p.LLM(ctx, pluginsdk.Str(args["prompt"]), "", 2*time.Minute)
    return map[string]any{"text": text}, err
})
p.HotTool("x_do")
p.OnRequest("ping", pluginsdk.PingHandler("1.0.0"))
p.Run(context.Background())
```

参考实现分层（按"复用程度"从高到低）：

| 插件 | 复用了什么 | 自己写了什么 |
|------|------------|--------------|
| `file` | `contract` + `toolset` + 全部 helpers | 默认参数合并（约 30 行） |
| `browser` / `desktop` | `engineclient` + helpers | 动作映射 + context 复用（各约 120 行） |
| `task` | `p.LLM` + `DataWrite` + helpers | 命令执行与输出限流（约 100 行） |
| `mcp` | `tools.list` / `tools.call` + helpers | 检索/加载/调用编排（约 150 行） |
| `web` | `p.Config` + helpers | DeepSeek 检索 / DeepSeek 抓取 / 本地直连（原始内容或下载，重定向可配） |
| `codegraph` | `OnMCPServer` + 引擎 `RegisterTools` | 引擎装配（约 40 行） |
| `compress` | `p.LLM` + `DoCompress` | DTO 映射（业务） |

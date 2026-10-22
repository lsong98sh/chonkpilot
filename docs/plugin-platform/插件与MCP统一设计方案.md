# 插件与 MCP 统一设计方案（Plugin Platform）

> 状态：🔵 **设计稿（待评审，未实现）**
> 关联：[28-plugins](../spec/20-modules/28-plugins.md) · [2C-UI插件](../spec/20-modules/2C-UI插件.md) · [25-mcp-server](../spec/20-modules/25-mcp-server.md) · [26-mcp-gateway](../spec/20-modules/26-mcp-gateway.md) · [27-mcp-tools](../spec/20-modules/27-mcp-tools.md) · [29-codegraph](../spec/20-modules/29-codegraph.md) · [2B-vfts](../spec/20-modules/2B-vfts.md) · [61-消息一览](../spec/60-reference/61-消息一览.md) · [工具面重构与Gateway二次优化设计方案](../tool-plane/工具面重构与Gateway二次优化设计方案.md)
> 代码面：`src/lib/mcp-tools` · `src/lib/gateway` · `src/lib/llm/{coretools,pluginbridge,pluginhost}` · `src/plugins/{plugin-*,codegraph,vfts}` · `src/frontend/src/{layout,views/codeview}` · `src/initdata/capability`

---

## 1. 问题与目标

### 1.1 现状（调研结论）

仓库里现在有**四种互不相通的"扩展形态"**，同一能力（如 vfts / codegraph）同时以两种形态存在：

| 形态 | 代表 | 进程 | 通信 | 工具面 | 配置 | UI |
|------|------|------|------|--------|------|----|
| 内嵌 Hook | compress / memory / history / **plugin-codegraph** / **plugin-vfts** | 无（编译进 server） | 同进程 MQ | 经 `mcp-tools-register` 动态注册 | prj 配置键 + 专用设置页 | 有两个专用页签 |
| 引擎 exe（MCP stdio） | `src/plugins/codegraph`、`src/plugins/vfts` | 独立，懒起常驻 | MCP stdio | 自己 `AddTool`（不落 `.tool.md`） | `configure/initialize` 指令 | 无 |
| 常驻 UDS 插件 | browser / desktop | 独立，常驻 | 自定义 UDS 协议 | **无**（不接 gateway） | 仅 CLI/`start` 参数 | 无 |
| 第三方 MCP | `capability/mcps/<名>.json` | 外部 / 由 gateway spawn | stdio / HTTP / SSE | MCP `tools/list` | 固定 DTO + 固定表单 | 仅 MCP 设置页 |

由此产生的结构性缺口（均已核对代码）：

1. **无插件注册中心**：`DefaultPlugins` 是编译期硬编码清单（[adapters.go](file:///e:/BizWorks/chonkpilot/src/lib/appwire/adapters.go#L15-L28)），Hooks 只有 `Name()`，没有 icon / version / entry / manifest。
2. **无动态菜单**：菜单完全由前端布局文档 `layout.free` 驱动（[persist.js](file:///e:/BizWorks/chonkpilot/src/frontend/src/layout/persist.js#L140-L181)），后端无法注入菜单项。
3. **preview 区不能承载外来页面**：功能页是硬编码白名单（[CodeView.vue](file:///e:/BizWorks/chonkpilot/src/frontend/src/views/codeview/CodeView.vue#L273-L289)），iframe 只服务文件 `rawUrl`，没有"按插件加载页面"的通道。
4. **UI 插件能力仅存在于规划**：[2C-UI插件](../spec/20-modules/2C-UI插件.md) 全篇 🔵 未实现，`ui-plugin-*` 仅在 [61](../spec/60-reference/61-消息一览.md) 的"预留主题族"登记，代码零命中。
5. **配置容器是闭集**：`McpServer` DTO 往返会丢弃任何未知键（[mcp.go](file:///e:/BizWorks/chonkpilot/src/lib/data/internal/mcp/mcp.go#L94-L117)），第三方 MCP 无法保存自定义配置。
6. **进程监管真空**：browser / desktop 的宿主 supervisor 只在设计稿里，代码无 spawn 调用点；而 indexer 侧已有完整的 `pluginhost.Manager`（单实例 + 1s→30s 退避 + 最多 3 次，[host.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/pluginhost/host.go#L189-L228)）——有轮子没用上。
7. **同名不同义**：`src/lib/mcp-tools/internal/pluginhost`（UDS 会话池）与 `src/lib/llm/pluginhost`（进程监管器）同名，统一时必须先消歧。

### 1.2 目标（可验收）

| 编号 | 目标 | 验收口径 |
|------|------|----------|
| G1 | **插件 = 可执行体 + manifest**，MCP 是插件对外能力的标准协议，UI 是插件贡献的声明式产物 | 新增一个插件只增加"插件目录"（manifest + exe + 可选页面资源），**不改主程序源码** |
| G2 | **动态插件菜单**：所有已注册插件在顶栏「插件」菜单聚合展示，禁用即隐藏 | 插件注册后菜单项出现（无需重启前端）；`ui-plugin-set-enabled=false` 后入口消失 |
| G3 | **插件配置页在 preview 区打开**：点击菜单 → preview 页签；配置保存即生效 | 表单态由 JSON-Schema 渲染；保存后下一次工具调用读到新值（无需重启） |
| G4 | **第三方 MCP 可"拉起后注册"**：HTTP MCP 启动后 POST 注册，即获得菜单 + 工具面 | 用桩服务验证：注册→`tools-list` 出现工具→菜单出现；host 重启后菜单保留并自动重连 |
| G5 | **websearch 插件（仅 DeepSeek）**：独立 MCP server + 自己的 Key 配置 | 工具 `web_search` 可见可调；Key 仅存本机、UI 掩码、不入日志；未配置 Key 时给出可诊断错误 |
| G6 | **codegraph + vfts 收敛为一个 `codebase` 插件**：不再是两个 MCP server + 两个 indexer | 9 个工具名与 schema 不变（LLM 无感）；1 个菜单入口、1 个配置页（页签）、1 个 MCP server 进程、1 个 indexer 进程 |
| G7 | **统一生命周期**：托管进程统一由 `PluginManager` 拉起/守护/停止 | browser/desktop 的"无人拉起"缺口修复；崩溃退避重启；状态经 `ui-plugin-status` 可见 |

### 1.3 非目标

- 不做插件市场 / 在线安装 / 签名校验（本期只做本地目录发现 + 手工安装）。
- 不做 JS bundle 型 UI（沙箱编译）与 MCP-UI / MCP Apps 规范兼容（列入后续演进）。
- 不改 `layout.free` 布局文档结构（用户已保存的布局不做迁移）。
- 不合并 9 个 codegraph/vfts executor（"一工具一 executor"是工具面既定设计，见 [工具面方案](../tool-plane/工具面重构与Gateway二次优化设计方案.md)）。
- 不动 compress / memory / history 的实现（仅在 P4 接入注册表做展示与开关）。

---

## 2. 总体方案

**一句话**：引入**插件平台**——一份 `plugin.json` 描述"怎么启动、暴露哪些 MCP 工具、贡献哪些 UI 入口与页面、需要哪些配置"；宿主侧新增**一个** `PluginManager` 统一发现/拉起/注册/守护，MCP gateway 负责工具面，前端新增**一个**通用菜单组件与**一个**通用页面宿主。此后任何新能力（联网搜索、代码索引、第三方 MCP）都只是插件目录里的数据。

```text
                 ┌──────────────────────── 主程序（一次实现，之后不改）────────────────────────┐
                 │                                                                              │
  capability/    │   PluginManager（发现/合并/校验/拉起/守护/注册表）                             │
  plugins/       │     │            │                    │                  │                    │
  <id>/plugin.json──▶│  inproc      │ managed-stdio      │ managed-http     │ external-http      │
                 │     │            │                    │                  │                    │
                 │     ▼            ▼                    ▼                  ▼                    │
                 │  内嵌 Hook    spawn 子进程          spawn 子进程       只连接不进进程          │
                 │     │            │                    │                  │                    │
                 │     └────────────┴───────► MCP gateway ◄─────────────────┘                    │
                 │                              （tools / prompts / resources）                  │
                 │                                                                              │
                 │   PluginRegistry ──ui-plugin-*──▶ 前端                                       │
                 │        │                            ├─ 顶栏「插件」菜单（动态聚合）             │
                 │        └─ 页面 schema/iframe ──────▶ └─ preview 页签（PluginPageHost）          │
                 └──────────────────────────────────────────────────────────────────────────────┘
```

分层职责：

| 层 | 职责 | 不做什么 |
|----|------|----------|
| manifest（插件侧） | 声明启动方式、MCP 传输、工具/页面/菜单、配置 schema、权限 | 不写宿主代码 |
| PluginManager（宿主） | 目录发现、四级合并、生命周期、健康检查、注册表、持久快照 | 不解析工具语义 |
| MCP gateway | 工具/资源聚合、命名与冲突、调用路由、取消阶梯 | 不做 UI |
| 前端插件宿主 | 菜单聚合、preview 页签、schema 表单、iframe 桥 | 不认识具体插件 |

**关键取舍**：插件工具契约**不再落** `src/initdata/capability/tools/*.tool.md`，由插件 MCP server 自己 `tools/list`（与现有 codegraph/vfts 引擎"工具经 go-sdk AddTool 直挂、不落契约"一致，[build-vfts.ps1](file:///e:/BizWorks/chonkpilot/build-vfts.ps1#L13-L14)）。这是"新增插件不改主程序"的前提。

---

## 3. 详细设计

### 3.1 插件模型与 manifest

#### 3.1.1 目录与作用域

插件包放在四级 `capability/plugins/<id>/`（与 `capability/mcps` 同级、同"最具体级整条覆盖"语义）：

| 级别 | 路径 | 用途 |
|------|------|------|
| app | `<appDir>/capability/plugins/<id>/` | 出厂内置插件（websearch、codebase） |
| user | `<userDir>/capability/plugins/<id>/` | 用户安装插件 |
| project | `<workdir>/.chonkpilot/capability/plugins/<id>/` | 项目级插件（团队共享） |
| prjusr | `<dataDir>/.../prjusr/capability/plugins/<id>/` | 本机覆盖 + 密钥 + 注册快照（不入库/不同步） |

插件包内容：

```text
capability/plugins/websearch/
  plugin.json            # 清单（唯一入口）
  config.schema.json     # 配置表单结构（可内联在 plugin.json）
  chonkpilot-websearch-mcp-server.exe    # 可选（托管形态）
  webui/                 # 可选（iframe 型自带页面）
```

#### 3.1.2 manifest 字段

```jsonc
{
  "schema": "chonkpilot.plugin/v1",
  "id": "websearch",                     // 必填；目录名必须等于 id
  "name": "联网搜索",
  "version": "1.0.0",
  "icon": "search",                      // 图标名（前端图标表）或 data URI
  "description": "基于 DeepSeek Search API 的联网检索",
  "category": "search",
  "capabilities": ["mcp", "ui"],         // mcp | ui | index | inproc

  "runtime": {                           // 缺省 = external-http（不托管进程）
    "mode": "managed-stdio",             // inproc | managed-stdio | managed-http | external-http
    "exe": "chonkpilot-websearch-mcp-server.exe",   // 相对插件目录或 installDir
    "args": ["--stdio"],
    "env": {},                           // 仅静态环境变量；密钥不写这里
    "restart": { "backoff": "1s..30s", "maxFails": 3 },
    "health": { "type": "mcp-initialize", "timeoutMs": 5000 }
  },

  "mcp": {
    "transport": "stdio",                // stdio | http | sse
    "url": "",                           // http/sse 时必填（external 由注册请求提供）
    "namespace": "",                     // 空 = 工具名原样（避免双前缀）
    "hotTools": ["web_search"],
    "timeoutSec": 120
  },

  "config": {
    "mode": "host",                      // host（宿主存储 + schema 表单）| plugin（插件自持 + 自带页面）
    "scope": "prjusr",                   // app | user | project | prjusr
    "schema": "config.schema.json",
    "secrets": ["apiKey"],               // 一律存 usr 级，永不返回明文
    "defaults": { "count": 10, "needContent": false }
  },

  "ui": {
    "menus": [
      { "location": "menu.plugins", "label": "联网搜索", "icon": "search", "page": "settings" }
    ],
    "pages": [
      { "id": "settings", "title": "联网搜索", "type": "schema", "container": "tab",
        "schema": "config.schema.json", "actions": ["test", "reset"] }
    ]
  },

  "permissions": { "network": ["api.deepseek.com"], "fs": ["pluginData"] }
}
```

**校验规则**（`PluginManager` 启动时强制）：

1. `id` 匹配 `^[a-z][a-z0-9-]{1,31}$`，且等于目录名；重复 id 取最具体级整条覆盖。
2. `runtime.mode=managed-*` 时 `exe` 必须存在且在同一插件目录（或 `installDir`）下，拒绝绝对路径越界。
3. `mcp.transport=http|sse` 时 `url` 必须 `http/https` 且 host 在 `permissions.network` 内（external 模式的注册请求同样校验）。
4. `ui.pages[].type=schema` 时 `schema` 必须存在且为合法 JSON Schema 子集（见 3.4.3）。
5. `ui.menus[].location` 必须在宿主白名单内（`menu.plugins` / `statusbar.right` / `toolbar.actions.end` / `settings.tab`）。
6. `capabilities` 为空 → 拒绝（至少一项）。

#### 3.1.3 出厂 manifest 示例（codebase）

```jsonc
{
  "id": "codebase", "name": "代码索引", "version": "2.0.0",
  "capabilities": ["mcp", "index", "ui"],
  "runtime": { "mode": "managed-stdio",
               "exe": "mcps/codebase/chonkpilot-codebase-mcp-server.exe",
               "args": ["--stdio"] },
  "mcp": { "transport": "stdio", "namespace": "", "hotTools": ["codegraph_symbol_search", "vfts_query"] },
  "config": { "mode": "host", "scope": "prj", "schema": "config.schema.json" },
  "ui": { "menus": [{ "location": "menu.plugins", "label": "代码索引", "page": "settings" }],
          "pages": [{ "id": "settings", "title": "代码索引", "type": "builtin",
                      "component": "settings-project", "params": { "tab": "codegraph" } }] }
}
```

> `type=builtin` 只允许**出厂插件**引用前端已注册组件（白名单在 `views.registry.js`），第三方插件只能用 `schema` / `iframe`。这样既统一了菜单/生命周期，又保留 codegraph/vfts 现有富交互页面，不必重写。

### 3.2 插件宿主（PluginManager）

#### 3.2.1 命名消歧（先做）

| 现名 | 新名 | 职责 |
|------|------|------|
| `src/lib/mcp-tools/internal/pluginhost` | `.../internal/sessionpool` | UDS 会话池（browser/desktop 插件内部） |
| `src/lib/llm/pluginhost` | 保留 `pluginhost` | **进程监管器**（成为统一 `PluginManager` 的底座） |

#### 3.2.2 状态机

```text
discovered → starting → running → stopping → stopped
                 │          │
                 │          ├─ health fail ×3 → error（入口保留，status=error）
                 └─ spawn fail → backoff(1s→30s, maxFails=3) → error
external-http: registered → connected → disconnected（进程不在本机，不主动杀）
```

- 状态唯一写入口 = `PluginManager`，经 `ui-plugin-status{id,status,detail}` 广播前端（事件链，前端不 watch）。
- `running` 判据：MCP `initialize` 成功（stdio）或 `GET /health` 200 + `initialize` 成功（http）；不是"进程还在"。

#### 3.2.3 三种托管方式

| mode | 启动 | 就绪判定 | 停止 | 重启恢复 |
|------|------|----------|------|----------|
| `managed-stdio` | host spawn，env 注入 `CHONK_PLUGIN_ID/HOST/TOKEN/CONFIG/DATA` | MCP initialize | 关 stdin → 超时 kill 进程树 | 重新 spawn |
| `managed-http` | host spawn，插件写 `state.json`（复用 markitdown 形态：`{pid,port,token,version}`） | `state.json` + `/health` + initialize | POST `/shutdown` → kill | 清陈旧 `state.json` → 重 spawn |
| `external-http` | 不 spawn（用户/第三方拉起） | 注册请求 + initialize | 仅注销，不杀进程 | host 用持久快照重连；失败标 disconnected |

> `managed-http` 必须复用既有 `state.json` 约定（`<exeDir|dataDir>/mcps/markitdown/state.json` 同款），避免再发明一套。

#### 3.2.4 注册表与持久快照

- 内存注册表：`{id → {manifest, status, endpoint, menus, pages, config, lastSeen}}`，`PluginManager` 独占写。
- 持久快照（prjusr）：`capability/plugins/<id>/state.json` = **UI 需要的静态部分**（manifest 摘要 + menus/pages + last endpoint），用于 host 重启后**菜单立即重建**（避免"菜单闪烁/丢失"）。
- 配置：`config.json`（非密，scope 级）+ `secrets.json`（usr 级，ACL 限当前用户，UI 掩码，日志禁打）。
- **不持久化**：进程 pid、MCP 会话、连接状态（重启后重建/重连）。

### 3.3 MCP 统一注册

#### 3.3.1 注册链路

```text
PluginManager.ensureRunning(id)
  → manifest.runtime 就绪
  → gateway 方法 mcp-servers-register { name: "plugin:<id>", transport, url|runtime,
                                         headers: {Authorization: Bearer <plugin token>},
                                         origin: "plugin", category: manifest.category,
                                         namespace: manifest.mcp.namespace,
                                         timeout: manifest.mcp.timeoutSec, hot_tools }
  → gateway 连接 + tools/list（工具名 = 插件契约，冲突即注册失败并可见报错）
  → PluginManager 广播 ui-plugin-register{manifest+menus+pages+status}
```

- 禁用插件 = `mcp-servers-unregister` + `ui-plugin-set-enabled` 广播（工具面与菜单同时消失）。
- 工具冲突策略：**拒绝注册**（不静默改名），错误写 `ui-plugin-status{status:"error", detail:"tool conflict: xxx"}`。理由：静默改名会让 LLM 侧契约漂移。

#### 3.3.2 第三方 MCP "拉起后注册"

复用既有插件桥（[capabilities.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/pluginbridge/capabilities.go#L20-L64) 的 `plugin/v1/*` 方法面），新增 4 个能力：

| 端点 | 方向 | 载荷 | 说明 |
|------|------|------|------|
| `plugin/v1/plugin.register` | 插件 → host | `{manifest, endpoint?}` → `{id, accepted, status}` | 注册/更新；`manifest` 至少含 id/name/mcp/ui |
| `plugin/v1/plugin.unregister` | 插件 → host | `{id}` | 幂等 |
| `plugin/v1/plugin.status` | 插件 → host | `{id, status, detail}` | 插件自报健康 |
| `plugin/v1/plugin.action` | host → 插件 | `{id, page, action, params}` | 页面自定义动作（如"测试连接"） |

安全：

- 每个 host 生成一个**安装级注册令牌**（usr 配置，UI「插件」菜单里可见+复制+重置）。
- 托管插件经 env `CHONK_PLUGIN_TOKEN` 自动获得；第三方插件由用户在插件页填令牌。
- 可选开关「允许本机免令牌注册」（默认关，开启后首次注册弹确认）。
- 注册请求只接受 loopback 来源；`url` 的 host 必须在 `permissions.network` 内（SSRF 防护，与既有 `web_fetch` 口径一致）。

#### 3.3.3 与 gateway 既有能力的关系

- 传输、headers、env、cwd、超时、隔离、沙箱、spawn、crash-loop、取消阶梯**全部复用**（[provider.go](file:///e:/BizWorks/chonkpilot/src/lib/gateway/gateway/provider.go#L590-L665)、[mcpgateway.go](file:///e:/BizWorks/chonkpilot/src/lib/gateway/gateway/mcpgateway.go#L1370-L1453)），不新造连接层。
- 现有 `capability/mcps/<名>.json`（用户手工加的 MCP server）**自动视为 `external-http`/`managed-stdio` 插件**：由 `PluginManager` 生成一个"最小 manifest"（无 UI，或挂 3.4.4 的通用 MCP 页），从而"任何第三方 MCP 都有菜单入口"。

### 3.4 UI 扩展

#### 3.4.1 插件菜单（动态）

- **一个**新组件 `PluginsMenu.vue` 挂在顶栏右侧（[ChonkTopBar.vue](file:///e:/BizWorks/chonkpilot/src/frontend/src/layout/ChonkTopBar.vue#L12-L31) 的 `right-extra` 左侧），**不改 `layout.free`**（避免用户布局迁移）。
- 数据源 = composable `usePlugins()`：`ui-plugin-list` 拉初始快照 + `ui-plugin-register/unregister/status/set-enabled` 事件链更新（无 watch）。
- 菜单结构：

```text
插件 ▾
 ├─ 联网搜索            → preview: plugin-page(websearch/settings)
 ├─ 代码索引            → preview: plugin-page(codebase/settings)
 ├─ MCP：markitdown     → preview: plugin-page(mcp:markitdown/overview)   ← 自动生成
 ├─ ─────────────
 ├─ 插件管理            → preview: plugin-manager（列表/启停/注册令牌）
 └─ 注册令牌：sk-****   （复制/重置）
```

- 入口显隐：`enabled=false` 隐藏；`status=error|disconnected` 显示灰色圆点 + 悬浮原因（点击进入配置页可重试）。
- 可选扩展点（P2）：`statusbar.right` 图标、`toolbar.actions.end`、`settings.tab`（沿用 2C 的 location 白名单）。

#### 3.4.2 preview 插件页

- `CodeView.vue` 的 `specialComponents` 增加**一个**固定 kind：`plugin-page`（异步组件），载荷由事件 `ui-plugin-open{id,page,params}` 驱动。
- 页面宿主 `PluginPageHost.vue` 按 `page.type` 分派：

| type | 渲染 | 适用 |
|------|------|------|
| `schema` | `PluginForm.vue`（JSON-Schema → 白名单控件） | 绝大多数配置页（websearch） |
| `iframe` | `<iframe sandbox>` + postMessage 桥 | 插件自带完整页面（`config.mode=plugin` / 第三方） |
| `builtin` | 前端已注册组件（白名单） | 出厂插件（codebase 复用现有页签） |

- 页签标题 = `page.title`，实例键 = `<id>:<page>`（同插件同页去重）。

#### 3.4.3 schema 表单（关键：不改主程序就能出配置页）

支持的最小 JSON Schema 子集：

| schema | 控件 | 备注 |
|--------|------|------|
| `string` | 文本输入 | `format: password` / `secret: true` → 密码框 + 掩码回显 |
| `string + enum` | 下拉 | |
| `integer/number` | 数字输入 | `minimum/maximum` 校验 |
| `boolean` | 开关 | |
| `array<string>` | 标签输入 | `exts`/`skip-dirs` 场景 |
| `string + format: textarea` | 多行 | |
| `object`（一层） | 分组 | 超过一层拒绝（防复杂度） |

按钮：`保存`（`ui-plugin-config-set`）、`重置默认`、以及 manifest 声明的 `actions`（如 `test` → `ui-plugin-action` → 插件返回 `{ok,message}`）。表单**本地待保存态 + 保存按钮**，不自动保存（与现有设置页一致）。

#### 3.4.4 第三方 MCP 的通用页（零插件代码）

未声明 `ui` 的 MCP server，自动生成 `mcp:<name>/overview` 页：

- 只读：连接状态、transport/url、工具清单（来自 `tools-list`）、最近错误。
- 可写：`enabled`、`timeout`、headers/env（复用现有 MCP 编辑能力；写入仍走 `data-mcp-save`）。
- 这样即便插件作者什么都不做，"任意第三方 MCP 能注册插件菜单"也成立。

#### 3.4.5 iframe 桥与安全

- `sandbox="allow-scripts"`（第三方**不带** `allow-same-origin`）；出厂插件可用虚拟源 `ck-plugin://<id>/`。
- 页面资源加载：扩展 WebView2 资源拦截（[gui/main.go](file:///e:/BizWorks/chonkpilot/src/lib/gui/main.go#L591-L644)）支持 `ck-plugin://<id>/<path>` → 只读映射 `capability/plugins/<id>/webui/`（路径穿越校验）。
- postMessage 协议：`{ck:{id,type,payload}}`；父窗口校验 `e.source === iframe.contentWindow` + `e.origin` 白名单（出厂虚拟源 / manifest 声明的 origin）。
- 允许的动作仅：`config.get`（脱敏）、`config.set`、`action`（manifest 声明）、`close`、`open-url`（用户确认）。**不暴露**任意 MQ / 任意 data 方法。

### 3.5 配置与密钥

| 项 | 结论 |
|----|------|
| 非密配置 | `capability/plugins/<id>/config.json`（按 `config.scope`，prj 可入库共享） |
| 密钥 | **恒 usr 级** `capability/plugins/<id>/secrets.json`，文件 ACL 限当前用户；不入库、不出现在 `data-*` 应答、不打日志 |
| 注入 | 托管插件：env `CHONK_PLUGIN_CONFIG=<config.json 绝对路径>` + `CHONK_PLUGIN_SECRETS=<secrets.json 绝对路径>`；插件**每次工具调用按 mtime 读**（保存即生效，无 IPC） |
| 第三方/external | `config.mode=plugin`，插件自持；宿主只渲染页面 + 代理显式声明的动作 |
| 回显 | `ui-plugin-config-get` 返回 `values`（密钥字段返回 `"sk-****abcd"` 掩码）+ `schema` + `dirty` |
| 保存 | `ui-plugin-config-set` 先写临时文件 + `atomic rename`，成功后再广播 `ui-plugin-config-changed{id}`；失败整体回滚（不留半截） |
| 日志 | 任何配置值**禁止**整体打印；仅打印 `id`、键名、是否变更 |

### 3.6 websearch 插件（DeepSeek，本期首落地）

#### 3.6.1 形态

```text
capability/plugins/websearch/
  plugin.json
  config.schema.json
  chonkpilot-websearch-mcp-server.exe     # Go，stdio（默认）；支持 --http 供第三方场景
```

- `runtime.mode=managed-stdio`，`mcp.transport=stdio`，`namespace=""`。
- 工具面：**1 个**（本期）`web_search`；预留 `web_fetch_md`（走 DeepSeek Fetch）不实现。
- `idempotent: true`（只读检索，可安全自动重试）。

#### 3.6.2 工具定义（插件自带，即 MCP 契约）

| 字段 | 类型 | 必填 | 映射 DeepSeek |
|------|------|------|---------------|
| `query` | string | ✅ | `query`（1–128 字符；支持 `site:`/`lang:`/`after:`/`before:` 等语法，原样透传） |
| `count` | integer | | `count`（1–50，默认取配置，兜底 10） |
| `need_content` | boolean | | `need_content`（默认 false） |
| `ori_question` | string | | `ori_question`（≤128） |

返回（裁剪为模型可读）：

```json
{ "provider": "deepseek",
  "results": [{ "title": "", "url": "", "snippet": "", "summary": "",
                "content": "", "site_name": "", "date": "", "authority": 3, "score": 2.4 }],
  "count": 5 }
```

#### 3.6.3 配置项（schema 表单）

| 键 | 类型 | 默认 | 说明 |
|----|------|------|------|
| `apiKey` | string（secret） | — | DeepSeek API Key；必填；只存 usr 级 |
| `baseUrl` | string | `https://api.deepseek.com` | 高级项；仅允许 https 且 host 在 `permissions.network` |
| `count` | integer | 10 | 默认返回条数（1–50） |
| `needContent` | boolean | false | 是否返回正文（影响上下文体积） |
| `timeoutSec` | integer | 30 | Search 请求超时 |
| `enabled` | boolean | true | 插件开关（等同 `ui-plugin-set-enabled`） |

页面动作：`test` = 用 `query="DeepSeek" count=1` 发起一次真实检索（**会计费 ¥0.005/次**，UI 明确提示），返回耗时与首条标题。

#### 3.6.4 DeepSeek 接口适配

| 项 | 值 |
|----|----|
| Endpoint | `POST {baseUrl}/v1/tools/search` |
| 鉴权 | `Authorization: Bearer <apiKey>`；`Content-Type: application/json` |
| 计费 | 每 1000 次成功请求 ¥5（仅 200 且 `results` 非空的请求计费） |
| 限速 | Search 100 QPS（账号级） |
| 重试 | 仅 `408/429/5xx` 指数退避重试 2 次（1s/3s）；`400/401/402/403/404` **不重试** |
| 错误映射 | `401` → 「API Key 无效或未配置，请打开插件配置」；`402` → 「DeepSeek 余额不足」；`429` → 「请求过于频繁（Search 100 QPS）」；`5xx/超时` → 「搜索服务暂不可用，已重试 N 次」 |

安全：Key 只在内存与发请求时使用；日志仅打印 `status/elapsed/result_count`；错误消息不得回显 Key。

#### 3.6.5 未配置 Key 时的行为

`tools/list` 仍暴露 `web_search`（否则 LLM 完全不可见、用户不知为何没有能力），但调用返回**可诊断错误**：`"websearch 未配置 DeepSeek API Key：请在 插件 → 联网搜索 中填写"`。菜单入口显示黄点提示。

### 3.7 `codebase` 插件（codegraph + vfts 合并）

#### 3.7.1 目标形态

| 现状 | 数量 | 目标 |
|------|:---:|------|
| `chonkpilot-codegraph-mcp-server.exe` + `chonkpilot-vfts-mcp-server.exe` | 2 | **1** 个 `chonkpilot-codebase-mcp-server.exe`（stdio，暴露 9 个工具） |
| `chonkpilot-codegraph-indexer.exe` + `chonkpilot-vfts-indexer.exe` | 2 | **1** 个 `chonkpilot-codebase-indexer.exe`（双索引编排） |
| 内嵌 hook `plugin-codegraph` + `plugin-vfts`（配置/注册/状态/debounce/文档探测） | 2 | P2 保留配置与索引编排；**工具注册迁到 MCP server**；P4 收敛为 manifest 驱动的通用能力 |
| 9 个 executor exe | 9 | **保留**（一工具一 executor，既定设计） |
| 配置页 `CodegraphConfig.vue` + `VftsConfig.vue` | 2 页签 | 1 个 `codebase` 页（内部两页签，组件复用） |

#### 3.7.2 工具面不变（LLM 无感）

9 个工具名与 schema 全部保留：`codegraph_symbol_search / get_symbol_info / callers / callees / get_dependency_graph / find_circular_deps / analyze_complexity / get_module_summary / vfts_query`。仅 `.tool.md` 的 `runtime` 路径改为 `mcps/codebase/...`（**参数/行为不变，按规范属路径同步，不改 description**）。

#### 3.7.3 关键风险与对策

| 风险 | 对策 |
|------|------|
| workdir 路由差异（codegraph 每 workdir 一进程；vfts 全局共享） | 合并进程内保留**两个会话池**（codegraph per-workdir、vfts shared），对外仍是 1 个 MCP server；路由键依旧是 `context.instance_id → workdir` |
| `zvec` 集合独占 LOCK（vfts 现状，[vfts.go](file:///e:/BizWorks/chonkpilot/src/plugins/plugin-vfts/vfts.go#L755-L757)） | 合并后仍由单进程持锁，天然消解跨进程锁竞争；索引器与查询 server 之间沿用现有"管理结束即释放"约定 |
| CGO 构建（codegraph/vfts 都是 CGO） | 合并产物仍由 `build-codebase.ps1` 统一构建，产出目录 `dist/plugins/codebase/`；`zvec_c_api.dll` 同目录 |
| 合并后单点崩溃影响两类工具 | 保留 executor 兜底（工具执行不依赖 MCP server 常驻）；MCP server 崩溃由 `PluginManager` 退避重启，期间工具注册标记 `unavailable` |
| 迁移期双轨 | P1/P2 阶段两套并存（新插件仅做菜单/页面聚合），P3 切进程，保留 `-legacy` 构建开关一个版本周期 |

#### 3.7.4 迁移阶段

| 阶段 | 内容 | 可回退 |
|------|------|:---:|
| C0 | 现状（两个 MCP server + 两个 indexer + 两个页签） | — |
| C1 | 引入 `codebase` manifest：菜单/页面聚合为一个入口（进程仍两套） | ✅ 删 manifest |
| C2 | 工具注册从 hook 迁到 MCP server（hook 只留配置/索引/状态） | ✅ 恢复 hook 注册 |
| C3 | 合并 MCP server 与 indexer 进程（`chonkpilot-codebase-*.exe`） | ✅ 切 legacy 构建 |
| C4 | hook 收敛为 `PluginManager` 的通用 `index` 能力 | 需 C3 稳定后 |

### 3.8 消息契约变更（`ui-plugin-*` 收缩版 + 配置面）

> 现状：[61](../spec/60-reference/61-消息一览.md) 把 `ui-plugin-*` 列为"预留主题族、不进准则"。本期落地时须**移入冻结清单**并同步 `61-messages.schema.json`；这是"消息契约变更需同步文档 + 测试"的既定要求。

| 主题 | 方向 | 载荷 | 说明 |
|------|------|------|------|
| `ui-plugin-list` | 前端 → 后端（call） | `{}` → `{plugins:[schema]}` | 初始快照（含 menus/pages/status/config 摘要） |
| `ui-plugin-register` | 后端 → 前端（事件） | `{plugin}` | 注册/更新（广播） |
| `ui-plugin-unregister` | 后端 → 前端（事件） | `{id}` | 注销 |
| `ui-plugin-status` | 后端 → 前端（事件，双向语义） | `{id,status,detail?}` | `idle\|starting\|running\|error\|disconnected` |
| `ui-plugin-set-enabled` | 前端 → 后端（call） | `{id,enabled}` | 开关（同时摘/挂工具面） |
| `ui-plugin-open` | 前端 → 后端（call） | `{id,page,params?}` | 后端校验后前端打开 preview 页签 |
| `ui-plugin-config-get` | 前端 → 后端（call） | `{id}` → `{schema,values,dirty}` | 密钥掩码 |
| `ui-plugin-config-set` | 前端 → 后端（call） | `{id,values}` | 原子落盘 + 广播 |
| `ui-plugin-config-changed` | 后端 → 前端（事件） | `{id,keys[]}` | 配置变更通知（插件按 mtime 自读） |
| `ui-plugin-action` | 前端 → 后端（call） | `{id,page,action,params}` → `{ok,message,data?}` | 页面自定义动作（`test`） |
| `ui-plugin-activated` | 后端 → 插件（事件） | `{id,page,context}` | 页面打开时通知插件（可选） |

同步点：

- [61-消息一览](../spec/60-reference/61-消息一览.md)：`ui-plugin-*` 从"预留"移入正式表 + §6 客户端能力面。
- [2C-UI插件](../spec/20-modules/2C-UI插件.md)：收缩为"本期实现子集"（菜单/schema 页/iframe 页），bundle、context 选择集、fetch 代理移到后续。
- [28-plugins](../spec/20-modules/28-plugins.md)：新增"插件平台"章节，说明 Hook 与 manifest 的关系。
- 新增 `data-plugin-*`（仅插件管理页需要，P4）：`list/load/save/delete/refresh`（manifest、配置、启停），沿用 `data-<domain>-<action>` 口径。

---

## 4. 规则与约定

| 项 | 结论 | 理由 |
|----|------|------|
| 插件最小单元 | 一个目录 = manifest + 可选 exe + 可选 webui | 安装/卸载 = 目录增删，不改主程序 |
| 工具契约来源 | 插件 MCP server 的 `tools/list`（不落 `.tool.md`） | 与 codegraph/vfts 引擎现状一致；否则新增插件必须改主程序契约目录 |
| 工具名 | 全局唯一；冲突即注册失败 | 静默改名会让 LLM 契约漂移 |
| 菜单注入 | 只经宿主白名单 location | 防止插件改写任意 UI |
| 配置页 | 优先生成式 schema；复杂页面才 iframe | 降低插件开发成本、便于统一安全审查 |
| 密钥 | 恒 usr 级、永不出现在应答/日志 | 与"禁止真实密钥入库"规则一致 |
| 进程 | 托管插件由 `PluginManager` 独占 spawn/stop | 消除"谁拉起"真空与重复实例 |
| 生命周期 | 状态机唯一写入口，事件链广播 | 前端零轮询、无 watch |
| 启用语义 | 一个开关同时管"菜单 + 工具面" | 用户认知一致；避免"菜单在但工具没了" |
| 不兼容变更 | 插件 manifest 带 `schema` 版本号，主程序按主版本兼容 | 后续演进不破坏第三方插件 |

---

## 5. 与既有机制的关系

| 机制 | 影响 | 处理 |
|------|------|------|
| `layout.free` 布局系统 | 不改结构 | 插件菜单是顶栏宿主组件，不进布局文档；后续如需可配置再评估 |
| 2C UI 插件规划 | 收敛为"本期子集 + 后续" | 更新 2C 状态与范围，bundle/context/fetch 延后 |
| MCP gateway | 复用全部连接/池化/取消能力 | 仅新增 `origin=plugin` 的来源标记与 UI 元数据旁路 |
| MCP 配置（`capability/mcps`） | 自动映射为插件 | 只读映射，不迁移用户配置；写入仍走原 `data-mcp-*` |
| 内嵌 Hook | 增加 `Plugin` 元数据（icon/name/config schema） | P4 接入注册表；实现不变 |
| 数据层 | 新增 `plugin` 域（config/secrets/state） | 沿用四级目录 + `data-*-refresh` |
| 构建 | 新增 `build-plugins.ps1` 或扩展现有脚本 | 插件产物落 `dist/<形态>/capability/plugins/<id>/`；根清理需保留 |
| 测试体系 | 全部 mq 驱动 + stub MCP server | 见 §7 测试项 |

---

## 6. 边界、异常与重启恢复

### 6.1 边界与异常

| 场景 | 行为 |
|------|------|
| manifest 非法 / 目录缺 exe | 跳过该插件 + `status=error` + 插件管理页可见原因；不影响其它插件启动 |
| 插件 MCP initialize 超时 | 判 `starting→error`；按退避重试至 `maxFails` |
| 工具名冲突 | 拒绝注册（保留先注册者），`status=error`，错误含冲突工具名 |
| 第三方注册令牌错误 | 401 + 明确提示；不创建任何菜单 |
| 插件进程崩溃 | 退避重启；期间工具面摘除、菜单置灰；重启成功后自动重注册 |
| 配置写入失败 | 原子回滚，保留旧值；`ui-plugin-config-set` 返回失败原因 |
| 页面 iframe 加载失败 | 宿主错误占位 + 重试；不影响主应用 |
| schema 不合法 | 拒绝保存 + 指出字段；不写盘 |
| 插件访问未授权域名 | 宿主代理层拒绝（`permissions.network` 白名单） |
| 同 id 多级 manifest | 最具体级整条覆盖（与 `capability/mcps` 一致） |
| host 退出 | `PluginManager.Stop` 按启动逆序停止；external 只注销不杀 |

### 6.2 重启恢复（逐条回答 71 §3.3 五问）

1. **状态落在哪**：manifest/config/secrets/注册快照落盘（`capability/plugins/<id>/`）；进程 pid、MCP 会话、连接态在内存（崩溃即丢，可重建）。
2. **输入是否受理即持久化**：配置保存先原子落盘再应用；第三方注册请求先写 `state.json` 快照再返回 200（避免"应答成功但重启丢菜单"）。
3. **非终态由谁收口**：启动时扫描 `state.json` 中 `starting/running` → 统一置 `reconnecting`；托管插件重新 spawn，external 插件重连（退避），超时收敛 `disconnected`；`starting` 超时 → `error`。无永久 `starting` 残留。
4. **重启后重建还是收敛**：托管插件**重建**（重新 spawn + initialize + 注册）；external 插件**重连**，未连上时菜单用快照保留、页面显示"未连接 + 重试"；工具面只注册已连通的插件。
5. **副作用语义**：注册/注销幂等（同名 register 覆盖元数据、unregister 不存在也成功）；配置写入幂等（同值不写）；工具调用默认**不可安全重试**，仅声明 `idempotent: true` 的工具（`web_search`）允许自动重试。

---

## 7. 落地清单

| 阶段 | 内容 | 状态 | 主要落点 |
|------|------|:---:|----------|
| P0-1 | manifest 模型 + 四级发现 + 校验 + `PluginManager`（复用 `pluginhost.Manager`） | 🔵 | `src/lib/llm/pluginhost`、新 `src/lib/pluginmgr` |
| P0-2 | `plugin/v1/plugin.register|unregister|status|action` + 注册令牌 | 🔵 | `src/lib/llm/pluginbridge` |
| P0-3 | MCP 注册映射（`origin=plugin`）+ 冲突策略 + 启停联动 | 🔵 | `src/lib/llm/server`、`gateway` |
| P0-4 | `ui-plugin-*` 消息 + 注册表广播 + 持久快照 | 🔵 | `src/lib/gui/bridge`、msgkeys、61 |
| P0-5 | 前端：`usePlugins` + `PluginsMenu` + `PluginPageHost` + `PluginForm` + `plugin-page` 页签 | 🔵 | `src/frontend/src/{layout,views/codeview,composables}` |
| P1 | **websearch 插件**（DeepSeek Search）：MCP server + schema 页 + test 动作 | 🔵 | `src/plugins/websearch`、`src/initdata/capability/plugins/websearch/` |
| P2 | codebase 页面/菜单聚合（进程仍两套） | 🔵 | manifest + 前端 builtin 页映射 |
| P3 | codebase 进程合并（1 MCP server + 1 indexer）+ 工具注册迁移 | 🔵 | `src/plugins/codebase`、构建脚本 |
| P4 | 内嵌 Hook 接入注册表；`data-plugin-*` 管理面；browser/desktop 纳入托管 | 🔵 | `src/lib/appwire`、`src/lib/mcp-tools/plugins` |

**测试项**（全部 mq 驱动 + stub，不依赖真实外网）：

1. manifest 校验/四级覆盖/非法目录跳过（L1）。
2. 假插件（HTTP stub）注册 → `ui-plugin-list` 含菜单 + `tools-list` 含工具（L2）。
3. `ui-plugin-set-enabled=false` → 菜单消失 + 工具注销（L2）。
4. `ui-plugin-config-set` → 落盘 → 重启后仍生效；密钥在应答中为掩码（L2）。
5. host 重启：菜单快照保留、external 插件标 `disconnected`、重连后恢复（L2，含重启恢复用例）。
6. websearch：stub DeepSeek 返回 200/401/402/429，断言错误文案与重试次数；真实调用仅在显式环境变量开启时跑（L1/L2）。
7. codebase 合并后 9 个工具名与 schema 与合并前逐字一致（L1 快照对比）。

---

## 8. 关联文档与待拍板

### 8.1 关联文档

- [2C-UI插件](../spec/20-modules/2C-UI插件.md) · [28-plugins](../spec/20-modules/28-plugins.md) · [25-mcp-server](../spec/20-modules/25-mcp-server.md) · [26-mcp-gateway](../spec/20-modules/26-mcp-gateway.md) · [27-mcp-tools](../spec/20-modules/27-mcp-tools.md)
- [61-消息一览](../spec/60-reference/61-消息一览.md) · [64-配置项一览](../spec/60-reference/64-配置项一览.md)
- [工具面重构与Gateway二次优化设计方案](../tool-plane/工具面重构与Gateway二次优化设计方案.md)
- [DeepSeek Search & Fetch 文档](https://api-docs.deepseek.com/zh-cn/guides/search/)（外部参考，不进 spec）

### 8.2 待拍板事项（评审通过后登记为 `41` 的 D 项）

| # | 事项 | 建议 |
|---|------|------|
| D1 | 插件菜单放顶栏宿主组件，还是接入 `layout.free` 成为可配置栏项 | 先宿主组件（零布局迁移），后续再评估 |
| D2 | 第三方注册是否允许"本机免令牌 + 首次确认" | 默认令牌；免令牌作为可选开关 |
| D3 | `codebase` 的 9 个 executor 是否也合并为 1 个 | 本期不合并（保持一工具一 executor） |
| D4 | 插件工具是否需要在 `capability/tools` 留"影子契约"供检索 | 不落；以 MCP `tools/list` 为准 |
| D5 | 插件配置 scope 默认值（prj 共享 vs prjusr 本机） | 含密钥/环境相关的默认 `prjusr`；纯策略类可 `prj` |
| D6 | external MCP 是否统一纳入插件菜单（含用户手写 `capability/mcps`） | 纳入，挂通用只读概览页 |

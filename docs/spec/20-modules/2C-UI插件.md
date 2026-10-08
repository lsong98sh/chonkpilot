# 2C · UI 插件扩展设计（🔵 规划中 — 未实现，预留）

> 状态：🔵 **规划中（未实现，预留）**（草案；演进登记见 [40-演进计划](../40-roadmap/40-演进计划.md)；消息族 `ui-plugin-*` 已在 [61](../60-reference/61-消息一览.md) 「预留主题族」与 `61-messages.schema.json` `coverage.planned` 登记，**不进消息准则/对账**）
> 关联：[28-plugins](28-plugins.md)（后端插件体系）· [17-前端状态与MQ](../10-architecture/17-前端状态与MQ.md)（前端状态管理通用模式）· [20-gui](20-gui.md) · 前端 `src/frontend/src/views/*`
> 代码目录：`src/frontend/src`（插件宿主侧待实现）
>
> 设计文档。按标准组织：概述 → 可插拔设计 → 插件设计 → 通信设计 → 示例 → 参考。

---

## 1. 概述

### 1.1 目标

在不改主应用源码的前提下，让**独立进程插件**在 UI 任意合理位置"插入"入口与内容，与主应用双向交互。典型用途：数据管理、会话分析、外部工具管理、自定义面板。

### 1.2 设计原则

| 原则 | 说明 |
|------|------|
| 可插拔 | 一处 `<PluginSlot>` 标记 = 一个插入点，注册表自动聚合 |
| 隔离 | 插件内容与主应用 UI 隔离：JSON 装配（无代码）→ bundle（沙箱）→ iframe（sandbox），由轻到重 |
| 安全 | 能力白名单：context 字段 + actions 动作 + fetch 域名 + call 方法（methods） |
| 数据流 | **MQ 事件链 + state**：状态流转 = 事件链（每步处理器：异步拉数据 → 写 state → 发下一消息）；无 watch、无 computed——所有变更显式可见，消息链即状态机（与"彻底 MQ 化"一致） |
| 协议统一 | 一切交互经 MQ，消息 kebab-case（`-` 分隔），跨进程天然适用 |

### 1.3 核心概念

| 概念 | 说明 |
|------|------|
| 入口（entry） | 插入点上的按钮/图标/菜单项，携带上下文 |
| 内容（content） | 激活后展示的 UI（12 种形态，见 §3.2） |
| 宿主（host） | 内容的承载方式：JSON 装配 / JS bundle / iframe（§3.3） |
| 上下文（context，即 active 选择集） | 主应用暴露的 active 选择集（选中文件/会话/任务等），字段白名单见 §6.2 |
| 三态 | enable（开关）/ visible（显隐）/ status（运行时状态图标） |

### 1.4 文档导航

- 框架侧（主应用开发者）：§2 可插拔设计
- 插件侧（插件开发者）：§3 插件设计 + §5 示例
- 通信（两端）：§4
- 速查：§6

---

## 2. 可插拔设计（Pluggable，框架侧）

### 2.1 模型

```text
入口层（entry）      位置 + 形态（按钮/图标/菜单项/右键项）+ 携带上下文
内容层（content）    json 装配 / bundle / iframe / 下拉 / 上下文菜单 / 卡片 / toast / 状态小组件（12 种形态见 §3.2）
呈现容器（container）dialog（弹框）/ tab（页签）/ panel（面板）/ drawer（抽屉），决定 json/bundle/iframe 的呈现方式（§3.1）
交互                 全部经 MQ（ui-plugin-*）；iframe 内经 postMessage 桥接到 MQ
三态控制             enable（插件开关）· visible（入口显隐）· status（运行时状态，图标可切换）
生命周期             spawn → registered → activated → executing → interacting → deactivated/cancelled
```

### 2.2 插入点（location）

**插入点标记规范**：主应用页面一处标记，注册表自动聚合该位置全部插件入口。

```vue
<PluginSlot location="sessions.header" />
<PluginSlot location="sessions.item" :context="{ activeSession: session }" />
<!-- 等价：自定义指令 v-plugin-slot（插入点不能用裸 attribute 表达，须经组件或指令） -->
<div v-plugin-slot="'sessions.item'" :slot-context="{ activeSession: session }" />
```

`PluginSlot` 职责：直接读 state（registry）渲染本 location 入口列表 → 排序 → 过滤 enable/visible → 点击激活（内部经 `ui-plugin-activate` 命令事件，见 §4.2）→ 挂载内容 → 空列表零渲染。插入点全集见 §6.1（42）。

### 2.3 插件数据（MQ 事件链 + state）

插件注册表 = 响应式 state，由 `ui-plugin-*` 事件链驱动。**通用状态模式（事件链、无 watch/computed）见 [17-前端状态与MQ](../10-architecture/17-前端状态与MQ.md)**，框架侧只需维护注册表（写入只经事件处理器，组件零逻辑）：

```js
const registry = ref({})            // { location: [schema...] }：插件专属 state
const statusMap = ref({})           // { pluginId: status }

// 写入：只经 MQ 事件处理器（事件链）。
// 桥接链路：插件进程 call → 主应用后端处理 → 广播前端事件（见 §4.1 消息枢纽），
// 前端订阅的即这些广播事件，写 state 后驱动渲染。
mq.on("ui-plugin-register",    e => upsertInto(registry.value, e.schema))
mq.on("ui-plugin-unregister",  e => removeFrom(registry.value, e.id))
mq.on("ui-plugin-status",      e => (statusMap.value[e.id] = e.status))
mq.on("ui-plugin-set-enabled", e => setEnabled(registry.value, e.id, e.enabled))
mq.on("ui-plugin-set-visible", e => setVisible(registry.value, e.id, e.location, e.visible))
mq.call("ui-plugin-list", {}).then(list => (registry.value = indexByLocation(list))) // 初始快照

// 读取：模板/函数直接访问 state（无 computed）。
// 返回该 location 的入口条目 { plugin, item }，已过滤插件 enable 与入口 visible
export function pluginsAt(location) {
  return (registry.value[location] ?? [])
    .map(p => ({ plugin: p, item: p.entries.find(e => e.location === location) }))
    .filter(({ plugin, item }) => plugin.enabled && item && item.visible !== false)
    .sort((a, b) => byPosition(a.item, b.item))
}
```

状态流转（激活/三态/内容挂载）遵循 §2.4 生命周期 + 通用事件链模式。

### 2.4 生命周期

```text
[spawn]              入口首次激活前：确保插件进程运行（per workdir 拉起/连接）
[registered]         MQ 就绪后 ui-plugin-register 声明 schema（enable/visible/status）
    │ 入口被点击
    ▼
[activated]          主应用发 ui-plugin-activated（location + context 快照）
    │
    ▼
[executing]          内容加载：json→装配；bundle→沙箱组件；iframe→加载 url
    │                 按需 ui-plugin-context-get/subscribe
    ▼
[interacting]        ui-plugin-call 数据 / ui-plugin-fetch 网络 / ui-plugin-message
    │ 关闭/切换/超时/取消
    ▼
[deactivated]        ui-plugin-deactivated（原因：close|switch|timeout|cancel）
    ▼
[cancelled/清理]     卸载内容、取消订阅、清定时器；插件进程可常驻或按策略退出
```

**取消规范**：插件主动 `ui-plugin-close`；主应用主动 deactivated（带原因）；插件必须实现 `onDeactivated()` 清理，超时强卸。

**三态控制**：

| 态 | 控制什么 | 消息 | 效果 |
|----|---------|------|------|
| enable | 插件开关 | `ui-plugin-set-enabled` | 禁用后全部入口隐藏、不响应 |
| visible | 入口显隐 | `ui-plugin-set-visible` | 单入口隐藏 |
| status | 运行时状态 | `ui-plugin-status`（双向） | 图标切换 idle/running/error |

> 三态操作入口：enable/visible 由用户在「插件管理」面板切换（经 `ui-plugin-set-enabled`/`set-visible`）；status 由插件上报 + 主应用运行时检测双向维护。

### 2.5 边界与兜底

| 场景 | 兜底 |
|------|------|
| 内容加载失败（未知组件/bundle 编译失败/iframe 超时） | 错误态占位 + 重试（重试 = 重新执行 executing 步骤、重新挂载内容宿主，不重启插件进程）；记日志；不影响主应用 |
| 插件进程崩溃 | 入口降级（status=error）；内容宿主卸载；重启自动重注册（spawn 按需拉起） |
| 同 location 多内容同时打开 | 默认互斥（后开替换前开）；container=panel/drawer 可并排 |
| 卸载资源释放 | iframe 移除、bundle 沙箱终止、MQ 订阅句柄统一回收、context 订阅退订、定时器清理 |
| 多实例隔离 | per-workdir 独立命名空间（注册表按 workdir 分区，与 MQ topic 一致）；插件 id 仅在 workdir 命名空间内唯一，同插件多 workdir 各自独立实例互不影响 |
| 能力边界 | context + actions + fetch 域名三重白名单叠加；`ui-plugin-call` 的 method 另须在插件声明 `methods` 白名单内（§4.2） |

---

## 3. 插件设计（Plugin，插件侧）

### 3.1 注册协议（schema）

**顶层字段**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | string | ✅ | 唯一（workdir 命名空间内，多 workdir 独立） |
| `name` | string | ✅ | 显示名 |
| `icon` | string | | 图标 |
| `version` | string | | 版本 |
| `enabled` | bool | | 插件开关（默认 true） |
| `status` | enum | | idle / running / error |
| `entries` | array | ✅ | 入口列表 |
| `content` | object | ✅ | 内容定义 |
| `context` | array | ✅ | 声明的 active 上下文白名单 |
| `actions` | array | | 允许能力：context/call/fetch/subscribe/message（取值见下注） |
| `methods` | array | | `ui-plugin-call` 允许调用的方法名白名单（未声明则 call 一律拒绝） |

> actions 取值语义：`context` = 上下文获取/订阅/退订（context-get/subscribe/unsubscribe）；`call` = 数据调用（method 须在 `methods` 白名单）；`fetch` = 网络代理（域名白名单见 §4.5）；`message` = 自由消息收发；`subscribe` = 订阅主应用事件。

**entries[]**

| 字段 | 类型 | 说明 |
|------|------|------|
| `location` | string | 插入点 id |
| `position` | string | 排布：first / last / group:&lt;id&gt; |
| `visible` | bool | 入口显隐（默认 true） |
| `entry` | object | `{ type: button\|icon, icon, tooltip }` |
| `menu` | array | `[{ label, action, params }]`（action 枚举与 JSON 动作共用，见 §3.3.1） |

**content**

| 字段 | 类型 | 说明 |
|------|------|------|
| `type` | enum | 内容引擎：json / bundle / iframe / dropdown / contextmenu / toast / badge / inline-card |
| `container` | enum | 呈现容器（仅 json/bundle/iframe 需要）：dialog（弹框）/ tab（页签）/ panel（面板）/ drawer（抽屉），默认 dialog |
| `schema` | object | type=json：装配树 |
| `component` | string | type=bundle：已注册组件名 |
| `url` | string | type=iframe：插件进程页面 URL |
| `title`/`width`/`height`/`position` | | 容器尺寸方向（container=dialog/panel/drawer 时生效） |

### 3.2 内容形态（12 种）

| # | 形态 | 说明 | 引擎/容器 |
|---|------|------|-----------|
| 1 | 弹框（dialog） | 模态对话框 | json/bundle/iframe + `container: dialog` |
| 2 | 页签（tab） | 内嵌页签（如 dbviewer.tab） | json/bundle/iframe + `container: tab` |
| 3 | 面板（panel） | 非模态面板 | json/bundle/iframe + `container: panel` |
| 4 | 抽屉（drawer） | 侧滑抽屉 | json/bundle/iframe + `container: drawer` |
| 5 | iframe 宿主页 | 插件进程完整页面（隔离） | type=iframe |
| 6-7 | 下拉 / 上下文菜单 | 入口展开 / 右键 | type=dropdown / contextmenu（主应用渲染） |
| 8 | 内嵌卡片 | 消息流内可交互卡片 | type=inline-card（json/bundle） |
| 9-10 | toast / 状态小组件 | 轻提示 / 常驻状态 | type=toast / badge |
| 11 | JS bundle 渲染 | 自定义组件/复杂逻辑 | type=bundle（沙箱） |
| 12 | JSON 装配 | 声明式 JS 组件装配，无代码、白名单 | type=json |

选择规则：表单/列表/配置页 → JSON；自定义/复杂交互 → bundle；完整独立页面 → iframe；弹框/页签/面板/抽屉用 `container` 指定呈现方式。

### 3.3 内容宿主

**3.3.1 JSON 装配**（组件永远是 JS，JSON 只是声明式装配层）

```json
{ "root": { "type": "el-form", "props": { "labelWidth": 120 },
    "children": [ { "type": "el-form-item", "props": { "label": "Model" },
      "children": [ { "type": "el-select", "props": { "modelValue": "gpt-4o" } } ] },
      { "type": "el-button", "props": { "type": "primary",
        "onClick": { "action": "ui-plugin-call", "method": "SaveConfig" } },
        "children": [ "Save" ] } ] } }
```

- 渲染：`<component :is="registry[node.type]" v-bind="node.props">` 递归装配
- **数据插值**：`{{ activeSession.id }}` 占位符渲染前替换（仅白名单字段）
- **动作枚举**（事件绑定与菜单项 `menu[].action` 共用同一枚举）：

| action | 语义 | 载荷 |
|--------|------|------|
| `open-content` | 激活/打开自身内容（菜单项常用） | { params } |
| `ui-plugin-call` | 数据调用 | { method, args } |
| `ui-plugin-fetch` | 网络代理 | { url, method, headers, body } |
| `ui-plugin-message` | 发自由消息 | { to, payload } |
| `open-url` | 打开外部链接（主应用核准） | { url } |
| `close` | 关闭自身内容 | — |

**3.3.2 JS bundle（自定义组件注册）**

插件经 `ui-plugin-component-register` 注册自定义 JS 组件（bundle as string），主应用沙箱编译注册进组件表，之后可被 JSON 引用：

```jsonc
{ "pluginId": "session-analyzer", "component": "SessionStatsTable",
  "bundle": "window.__chonkPlugin={...};", "deps": ["vue", "element-plus"] }
```

`ui-plugin-component-unregister` 注销。

**3.3.3 iframe（插件进程页面）**

插件进程托管页面，主应用 `<iframe sandbox="allow-scripts allow-same-origin">` 加载。通信见 §4.3。

> 注：`allow-same-origin` 仅用于插件进程托管的**可信**页面（使其保留自身 origin）；若加载第三方内容应去掉该权限，仅保留 `allow-scripts`。

### 3.4 上下文获取

- `ui-plugin-context-get`（call）：拉快照（只返回白名单字段）
- `ui-plugin-context-subscribe`（call→subId）+ `ui-plugin-context-changed`（事件）
- `ui-plugin-context-unsubscribe`（call）：退订（{ subId }；deactivated 时主应用强制清理）
- `ui-plugin-activated` 自带首屏快照

active 选择集：`activeFile / activeDir / activeTab / activeSelection / activeSession / activeMessage / activeTurn / activeTask / activeDataObject`。

---

## 4. 通信设计

### 4.1 三层通信拓扑

```text
┌─────────┐   MQ（主应用后端消息枢纽）   ┌─────────────┐
│ 主应用 UI │ ◀───────────────────────▶ │  插件进程     │
│ (Vue)   │                            │ (Go/任意)    │
└────┬────┘                            └─────────────┘
     │ 宿主内通信（见下）
     ▼
┌──────────────┐   postMessage 桥接    ┌────────────────┐
│ 插件 UI 内容  │ ◀────────────────────▶ │ 主应用(转发MQ)  │
│ (bundle/iframe)│                      └────────────────┘
```

| 链路 | 方式 | 说明 |
|------|------|------|
| 插件进程 ↔ 主应用 | **MQ 直连** | 唯一通道，消息见 §4.2 |
| bundle 内容 ↔ 插件进程 | **MQ（主应用注入 helpers）** | helpers.subscribe/call/context/close 转发 |
| iframe 内容 ↔ 插件进程 | **postMessage 桥接 → MQ** | iframe 内无 MQ 对象，经父窗口转发 |

**消息枢纽（主应用后端）**：插件进程与前端 UI 的一切消息都经主应用后端路由——插件发来的消息经处理后再广播给前端（前端订阅写 state，见 §2.3）。接入端点：

| 端点 | 用途 |
|------|------|
| `POST /plugin/call/<topic>` | 插件 → 后端：请求-响应（call） |
| `POST /plugin/publish` | 插件 → 后端：发送事件（emit） |
| `GET /plugin/events` | 后端 → 插件：SSE 长连接事件订阅（on） |
| 前端侧 | 复用既有 mq（emitRemote / emit / call），不新增通道 |

插件 SDK 封装以上端点，协议契约另行文档化；前端侧事件广播复用现有 `emitRemote` 通道。

### 4.2 消息规范（ui-plugin-*，kebab-case）〔**规划中（未实现，预留）** —— 全表为设计稿，尚未落地；见 [61](../60-reference/61-消息一览.md)「预留主题族」〕

命名空间：`ui-plugin-*`（**草案原文写作 `chonk.{workdir}.ui-plugin-*`，实施前须按 [11-MQ与消息](../10-architecture/11-MQ与消息.md) §3 改为相对主题**——不带 `chonk.`、不带点分段、不带 workdir 段；workdir 经 payload/Context 承载）。

| 类型 | 方向 | 语义 | 载荷 |
|------|------|------|------|
| `ui-plugin-list` | 前端→后端（call） | 查已注册插件（初始快照，可按 location 过滤） | { location? } → [schema] |
| `ui-plugin-register` | 插件→后端（call）→ 广播前端 | 注册/更新 schema（前端事件链写 registry，§2.3） | schema |
| `ui-plugin-unregister` | 插件→后端（call）→ 广播前端 | 注销 | { id } |
| `ui-plugin-activate` | 前端内部（v-mq 指令） | 入口点击命令 → activate 处理（白名单校验）→ 发 activated | { pluginId, entryId, context } |
| `ui-plugin-set-enabled` | 后端→插件（事件） | 开关插件（同时广播前端更新 registry） | { id, enabled } |
| `ui-plugin-set-visible` | 后端→插件（事件） | 入口显隐（同时广播前端更新 registry） | { id, location, visible } |
| `ui-plugin-status` | 双向（事件） | 状态上报/下发（同 id 冲突以后到者为准） | { id, status } |
| `ui-plugin-activated` | 后端→插件（事件） | 入口激活 | { location, context, entryId } |
| `ui-plugin-context-get` | 插件→后端（call） | 取上下文 | { selectors } → 快照 |
| `ui-plugin-context-subscribe` | 插件→后端（call） | 订阅上下文 | { selectors } → subId |
| `ui-plugin-context-unsubscribe` | 插件→后端（call） | 退订上下文（deactivated 时主应用强制清理） | { subId } |
| `ui-plugin-context-changed` | 后端→插件（事件） | 上下文变化推送 | { subId, changes } |
| `ui-plugin-call` | 插件→后端（call） | 数据调用（转发数据层/会话层；method 须在插件 `methods` 白名单内，未声明拒绝） | { method, args } |
| `ui-plugin-fetch` | 插件→后端（call） | 网络代理（域名/头白名单 §4.5） | { url, method, headers, body } |
| `ui-plugin-component-register` | 插件→后端（call） | 注册 JS 组件（bundle as string） | { pluginId, component, bundle, deps } |
| `ui-plugin-component-unregister` | 插件→后端（call） | 注销组件 | { pluginId, component } |
| `ui-plugin-message` | 双向（事件） | 自由消息 | { from, to, payload } |
| `ui-plugin-close` | 插件→后端（call） | 请求关闭自身 | { id, reason } |
| `ui-plugin-deactivated` | 后端→插件（事件） | 关闭/取消/切换 | { id, reason: close\|switch\|timeout\|cancel } |

### 4.3 插件 UI ↔ 插件进程（宿主内通信）

**bundle**：主应用注入 helpers，直接走 MQ（无跨域问题）：

```js
// 主应用激活 bundle
bundle.render(ctx, container, {
  subscribe: (t, cb) => mq.subscribe(t, cb),
  call: (m, a) => mq.call("ui-plugin-call", { method: m, args: a }),
  context: { get: s => mq.call("ui-plugin-context-get", { selectors: s }),
             subscribe: s => mq.call("ui-plugin-context-subscribe", { selectors: s }),
             unsubscribe: subId => mq.call("ui-plugin-context-unsubscribe", { subId }) },
  onDeactivated: cb => deactivatedHooks.push(cb), // 主应用 deactivated 时强制调用（先于卸载内容）
  close: () => mq.call("ui-plugin-close", { id: plugin.id })
})
```

**iframe**：`window.parent.postMessage` 桥接（见 4.4 跨域）。

### 4.4 跨域问题与解决

**是否存在**：是，且仅在两类场景：

| 场景 | 跨域原因 | 解决 |
|------|---------|------|
| iframe 宿主 | 主应用 origin ≠ 插件进程 origin（插件进程独立端口） | **postMessage 桥接**（跨源安全通道，不依赖 CORS） |
| 插件 fetch 外部资源 | 插件 UI 直接 fetch 受 CORS 限制 | **fetch 代理**（§4.5，主应用执行） |

**JSON 装配 / bundle** 运行在主应用沙箱内（同源），无跨域问题。

**postMessage 桥接**（iframe）：

```js
// iframe 内：封装 MQ 调用
function mqCall(type, payload) {
  return new Promise((resolve) => {
    const id = Math.random().toString(36).slice(2)
    window.__bridgeResolvers[id] = resolve
    window.parent.postMessage({ mq: { id, type, payload } }, "*")
  })
}
const stats = await mqCall("ui-plugin-call", { method: "GetSessionStats", args: [sid] })

// 主应用：来源校验 → 转发 MQ → 回投
window.addEventListener("message", e => {
  if (!pluginOrigins.has(e.origin)) return        // 白名单校验
  forwardToMQ(e.data.mq, resp => e.source.postMessage({ mqResp: resp }, e.origin))
})
```

关键点：**不依赖 CORS**；安全靠 origin 白名单校验。

### 4.5 fetch 网络代理

插件任何网络请求一律经 `ui-plugin-fetch` 代理（规避跨域 + 安全）：

| 策略 | 说明 |
|------|------|
| 执行方 | 主应用代理执行（或转 mcp-gateway 的 fetch 工具） |
| 白名单 | 插件声明允许的域名/协议 |
| 请求头 | 仅透传插件声明白名单内的头；不传 Cookie / Authorization |
| 限制 | 超时、响应体大小、重定向次数；二进制响应回传 base64 |
| 回传 | 响应文本/状态码/头，经 call 返回 |

---

## 5. 示例

### 5.1 会话分析插件（完整）

```js
// ① 插件进程启动：注册
await mq.call("ui-plugin-register", {
  id: "session-analyzer", name: "Session Analyzer", icon: "🕵️",
  enabled: true, status: "idle",
  entries: [
    { location: "sessions.item", position: "last",
      entry: { type: "button", icon: "🕵️", tooltip: "Analyze" } },
    { location: "statusbar.right", position: "last",
      entry: { type: "icon", icon: "🕵️" },
      menu: [{ label: "Open Analyzer", action: "open-content", params: {} }] }
  ],
  content: { type: "json", container: "dialog", title: "Session Analysis", width: 480,
    schema: { root: { type: "el-form", children: [ /* 统计表 */ ] } } },
  context: ["activeSession"], actions: ["context", "call", "fetch"],
  methods: ["GetSessionStats"]
})

// ② 激活后：上下文 + 数据 → 渲染
// render(ctx, container, h):
//   h.context.get(["activeSession"]) → h.call("GetSessionStats", [id]) → 渲染统计
```

### 5.2 插入点主应用实现（PluginSlot 聚合）

```vue
<div class="session-actions">
  <!-- pluginsAt 返回 { plugin, item }（已过滤 enable/visible）；激活经 v-mq 命令事件 -->
  <template v-for="p in pluginsAt('sessions.item')">
    <button v-if="p.plugin.enabled && p.item.visible" :title="p.item.entry.tooltip"
            v-mq:[EventNames.uiPluginActivate].click="activationPayload(p, session)">
      <i :class="statusIcon(p.plugin)" />
    </button>
  </template>
</div>
<!-- activationPayload → { pluginId, entryId, context }；v-mq 点击 → ui-plugin-activate →
     activate 白名单校验 → 发 ui-plugin-activated → 按 content.type/container 挂载 -->
```

### 5.3 数据管理插件接入

入口 `statusbar.right` 图标 → 弹出菜单（列数据对象 session/turn/message/task/config…）→ 选中 → `dbviewer.tab` 页签内 iframe/JSON 管理页（per 类型增删改）。

---

## 6. 参考

### 6.1 插入点速查（42）

**A 全局**：`titlebar.actions` / `menu.global` / `shortcut.global` / `layout.dock.left|right`
**B 工具栏**：`toolbar.actions.start|mid|end` / `toolbar.menu`
**C 状态栏**：`statusbar.left` / `statusbar.right` / `statusbar.notify`
**D 工作目录**：`workspace.icon` / `workspace.menu`
**E 文件树**：`filetree.dir.icon` / `filetree.node.hover` / `filetree.context.file|dir|root`
**F 预览**：`preview.tabbar` / `preview.toolbar` / `preview.context` / `preview.selection` / `preview.decorator`
**G 聊天**：`chat.title` / `chat.input.above|below` / `message.hover` / `message.context` / `message.inline-card`
**H 会话**：`sessions.header` / `sessions.item` / `sessions.context`
**I 任务**：`tasktree.node.left|right|context` / `task.detail.title|selection`
**J 对话框**：`settings.tab` / `ask.panel` / `tools.list` / `dbviewer.tab`

> 每个插入点在对应页面组件中放一处 `<PluginSlot location="..."/>` 即开放；可用入口/内容形态与上下文契约按"所在区域"参考 §2.2、§3.2、§6.2。

### 6.2 active 上下文速查

`activeFile`（文件+路径+扩展名）· `activeDir` · `activeTab` · `activeSelection`（文本+range）· `activeSession`（id+标题）· `activeMessage`（key+角色）· `activeTurn` · `activeTask` · `activeDataObject`（类型+记录）

### 6.3 动作枚举速查

`open-content` · `ui-plugin-call` · `ui-plugin-fetch` · `ui-plugin-message` · `open-url` · `close`（JSON 事件与菜单 action 共用，见 §3.3.1）

### 6.4 三态速查

`enable`（`ui-plugin-set-enabled`）· `visible`（`ui-plugin-set-visible`）· `status`（`ui-plugin-status`，idle/running/error）

---

## 7. 实现建议

1. 先建**插件管理域**：MQ 消息枢纽（`/plugin/call`、`/plugin/publish`、`/plugin/events`，§4.1）+ location 白名单 + `ui-plugin-*` 消息路由 + `PluginSlot`（事件链：ui-plugin-* → registry → 渲染；无 watch/computed）
2. 优先开放：`statusbar.right`、`sessions.item`、`chat.input.below`、`filetree.context.*`、`preview.tabbar`
3. JSON 装配只引用白名单组件；bundle 沙箱编译；iframe sandbox + postMessage 来源校验
4. fetch 一律代理：域名/头白名单 + 超时 + 大小限制
5. 取消兜底：超时强卸 + `onDeactivated` 强制调用（经 helpers.onDeactivated 注入）+ 订阅句柄与 context 订阅统一退订
6. 消息全部 kebab-case 相对主题（**不带 `chonk.` 前缀与 workdir 段**；workdir 经 payload/Context 承载），实施前按 11-MQ与消息 §3 对齐

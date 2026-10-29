# 2C · 插件 UI 扩展（iframe view / 预览页 / 管理页 / 状态栏）

> 状态：✅ **已落地**（2026-10-28 重写；PLUGIN-1 · PLUGIN-UI-1 · PLUGIN-UI-2 · PLUGIN-INSTANCE-1）
>
> **本文口径变更**：原稿为「UI 插件扩展设计（🔵 规划中）」——`<PluginSlot location>` 插入点 + `ui-plugin-*` 消息族 + JSON 装配 + JS bundle 沙箱「12 种内容形态」的设计稿。**本期实现口径收敛为 iframe view**（[插件与MCP统一设计方案](../../plugin-platform/插件与MCP统一设计方案.md) §「2C 收敛」）：插件以 `plugin.json` 的 `ui.*` 声明视图 / 栏条目 / 动作按钮，前端统一用 **同源 iframe** 承载插件自持页面（`webui/*.html`），宿主**不装配插件内容、不执行插件 JS、不读插件业务状态键**。bundle / JSON 装配 / `PluginSlot` 插入点**本期不做**（见 §6）。
>
> 关联：[28-plugins](28-plugins.md) §3A（插件 UI 四通道与 instance 归属的**权威明细**）· [20-gui](20-gui.md)（布局与视图注册表）· [31-窗口与布局](../30-function-points/31-窗口与布局.md)（WIN-016 状态栏）· [61-消息一览](../60-reference/61-消息一览.md)（§7.2 `plugin-*`；§8 预留族）· [17-前端状态与MQ](../10-architecture/17-前端状态与MQ.md)
>
> 代码落点：前端包 `src/frontend/packages/plugin-ui/`（`PLUGIN-SPLIT-1 D-70 ②`，宿主零业务依赖）+ 宿主注入 `src/frontend/src/pluginHost.js`；视图注册表 `src/frontend/src/layout/{view-registry,views.registry,view-definitions,commands}.js`；后端 `src/lib/pluginhost/{mgr,bridge}`。

---

## 1. 概述

### 1.1 目标

插件（独立进程 exe）在**不改主应用源码**的前提下，向 UI 提供页面与入口：

| 能力 | 载体 | 宿主是否渲染插件内容 |
|---|---|---|
| 插件页（配置 / 状态 / 任意页面） | iframe `plugin:<id>:<view>` | 否（只给框与信封） |
| 预览区页签 | `preview-tab-open{kind:'plugin-page'}` | 否 |
| 插件维护页（卡片 / 开关 / 动作按钮） | `preview-tab-open{kind:'plugin-manager'}` | **是**（宿主自有页面，读 `plugin-list`） |
| 状态栏 / 工具栏条目 | `ui.statusbar[]`（按钮或**内嵌插件视图**） | 按钮=是；`view` 条目=否 |
| 项目配置页里的插件配置 | `ProjectConfig` 复用 `PluginPageHost` | 否 |

### 1.2 设计原则

| 原则 | 说明 |
|------|------|
| **iframe 唯一** | 插件 UI = 插件自己托管的 HTML/JS/CSS，宿主只提供 iframe 容器 + 通信信封；**没有** JSON 装配 / bundle 沙箱 / 组件注入 |
| **同源可信** | iframe 与宿主**同源**且 `sandbox="allow-scripts allow-forms allow-same-origin"` —— 等价于「插件代码视为可信代码」（插件本就是本机 exe） |
| **资源归插件** | 资源一律 `/plugins/<id>/<entry>` → UDS `plugin/v1/ui.asset.get` → 插件（`go:embed`）；宿主**不读插件目录、没有副本可兜底** |
| **依赖单向** | 宿主**不读**插件业务键、**不为**插件业务写视图；插件状态由插件页/栏条目**自己拉**（见 [28 §3A.8](28-plugins.md)） |
| **消息面零变更** | 插件 UI 复用既有 `plugin-*` 消息族（[61 §7.2](../60-reference/61-消息一览.md)）；`plugin-ui-event` 仅 2026-10-27 增**可选** `instance_id` |
| **事件驱动、无 watch** | 前端插件状态 = `plugin-list` 快照 + `plugin-registered/unregistered/status` 事件链写 `reactive` state（项目规则：composable + 零 watch/轮询） |

### 1.3 术语

| 概念 | 取值 / 说明 |
|---|---|
| 插件视图（view） | manifest `ui.views[]` 的一项 → 注册表 key `plugin:<id>:<view>`（iframe 承载） |
| 容器（container） | `preview`（预览区页签）/ `tab`（布局页签区）/ `drawer`（抽屉）/ `dialog`（弹框）/ `statusbar`（栏条目内嵌） |
| 页面宿主 | `PluginPageHost.vue`（包内）—— iframe + 就绪态 + `ckMq` 注入 + `ckPlugin` 信封 |
| 就绪态 | 插件 `status === 'running'` 且资源 HEAD 探活通过；否则占位 + 自动重试（§3.4） |
| 窗口实例（instance） | **承载该 iframe 的窗口实例**；与插件进程自己的 `plugin.hello` 连接 id 是**两个命名空间**（[28 §3A.7](28-plugins.md)） |

---

## 2. manifest 契约（`plugin.json` 的 `ui` 段）

> 校验实现：`src/lib/pluginhost/mgr/manifest.go`（加载期）；运行时增补：UDS `plugin.ui.register`（`mgr.registerUI` → 覆盖 `p.ui` 并重播 `plugin-registered`）。

| 字段 | 类型 | 说明 |
|------|------|------|
| `ui.selfServe` | bool | 历史分流位（`/plugins/…` vs 旧 `/plugin-ui/…`）。**前端已不再据此分流**（路由统一 `/plugins/`），字段保留用于遥测/兼容 |
| `ui.views[]` | array | `{id, title?, icon?, entry, containers?, default_size?}`。`id=config` 视为**配置视图**（插件维护卡片显示「配置」按钮，`pluginView.config_view`）；`entry` 形如 `webui/config.html`（相对插件资源根）；`default_size` = `{width,height}`（栏内嵌尺寸） |
| `ui.statusbar[]` | array | `{id, zone: left\|center\|right, icon?, tooltip?, view?, command?}`。**`view` 非空 → 栏内嵌该插件视图**（PLUGIN-UI-2，`command` 不再使用）；否则按 `command` 渲染按钮 |
| `ui.menus[]` | array | `{location, label, icon?, view}`。**当前仅透传**（`pluginView.menus`），前端**无消费方**（见 §6 遗留） |
| `ui.actions[]` | array | 插件维护卡片上的动作按钮：`{id, label, icon, tooltip?, style?, view?, container?, command?}`。`style ∈ {icon, icon-text, text}`（缺省 `icon-text`）；`container ∈ {preview, tab, drawer, dialog}`（缺省 = 该 view 的 `containers[0]`）；`view` 必须已在 `ui.views` 注册；**`view` 与 `command` 二选一**，皆缺 → 注册失败 |
| `ui.rpc` | array | 插件 `plugin/v1/ui.rpc` 可调用的动作名声明（约定，宿主不强制）；当前**不进入 `pluginView`**（前端不消费） |
| `permissions.ui.containers` | array | 插件允许申请的容器集合（`preview/tab/drawer/dialog/statusbar`）；评审用，不做技术拦截 |

**缺省**：`ui.views[].containers` 缺省 = `["preview","tab","drawer"]`；`containers` 含 `preview` 时该视图可进预览区页签（`commands.tabsRoute` 分流）。

---

## 3. 前端装配

### 3.1 包与宿主注入

```text
src/frontend/packages/plugin-ui/          # 纯前端包，不 import 宿主模块（vue 除外）
  host.js / components.js / theme.js      # 端口容器（configurePluginHost / setPluginUIComponents）
  usePlugins.js                           # 状态：plugin-list 快照 + 事件链（reactive，零 watch）
  usePluginReadiness.js                   # 页面就绪态 + 自动重试 + iframe URL 构造（含 instance_id）
  PluginPageHost.vue                      # 页面宿主（iframe + ckMq + ckPlugin 信封 + 主题推送）
  PluginManagerPage.vue                   # 插件维护页（卡片 / 开关 / 配置 / 动作按钮）
  pluginBar.js                            # 状态栏 / 工具栏条目（ui.statusbar[]）
  pluginAction.js                         # 动作面 fetch（POST /plugins/<id>/api/<action>）
  pluginUiRoute.js                        # PLUGIN_UI_PREFIX='/plugins/' + pluginUIPageURL()
  pluginNotice.js                         # 失败文案
```

宿主侧 `src/frontend/src/pluginHost.js` 在 `initPluginUIHost()`（**必须早于** `initPlugins()` 与任何插件视图挂载）注入端口：

| 端口 | 宿主实现 |
|---|---|
| `mq` | `utils/mq`（`on` / `emit`） |
| `views` | `layout/views.registry.js`（`registerPluginViews` / `unregisterPluginViews`） |
| `instance` | `currentInstanceId()`（PLUGIN-INSTANCE-1，拼 iframe URL） |
| `theme` | `composables/useTheme.js`（订阅 / 快照） |
| `components` | `Icon` / `Button` / `Switch` |

### 3.2 视图注册表（UIREG-1）

- **唯一注册 API**：`view-registry.js` 的 `registerViews(entries, source)`；内置视图（`views.registry.js`，source=`builtin`）与插件视图（source=`plugin:<id>`）共用。**无静态组件映射表**。
- 插件视图注册：`registerPluginViews(plugin, makeComponent)` 遍历 `plugin.views` → 每个 `view` 产出
  - 注册表条目 `{key:'plugin:<id>:<view>', component}`（component = 宿主注入的 `PluginPageHost` 适配壳）；
  - 动态定义（`DYNAMIC_DEFINITIONS`）`{key, label, icon, mode:'multi', host:'layout', designable:false, containers, plugin:{id, view, entry}}`。
- **幂等**：按结构签名（view 集合 / entry / 标题 / 图标 / 版本）比对；签名未变 → **保留既有组件对象**（不重建，避免视图池里的 iframe 被反复重挂）；变化或插件重连 → 先 `unregisterPluginViews(id)` 再注册。
- 卸载：`plugin-unregistered` → `unregisterPluginViews(id)`（移除动态定义 + 按 source 卸载组件）。

### 3.3 页面宿主 `PluginPageHost.vue`

props：`pluginId` / `pageId`（view id）/ `entry` / `title`。

| 职责 | 实现 |
|---|---|
| URL | `pluginUIPageURL(id, entry)` + `?p=<pageId>[&instance_id=<宿主实例>][&t=<seq>]`（构造唯一入口 = `usePluginReadiness.src`；`t` 用于绕过宿主短缓存） |
| iframe | `sandbox="allow-scripts allow-forms allow-same-origin"`、`referrerpolicy="no-referrer"` |
| iframe → 宿主 | `postMessage({ckPlugin:1, id, type:'call', method, params})` → 宿主 `dispatch` → 回投 `{ckPlugin:1, id, type:'result', ok, result, errors}`；**来源校验** `e.source === iframe.contentWindow` |
| 宿主 → iframe | `{ckPlugin:1, type:'event', topic, payload}`（来源 = `plugin-ui-event`，按 `id` 过滤）；`{ckPlugin:1, type:'theme', theme}`（主题快照，`load` 后推一次 + 主题变化再推，幂等不重建 iframe） |
| `ckMq` | `load` 后注入 `window.ckMq = {on, off, emit}`（**不裸暴露宿主 mq**）；该 frame 的订阅登记在 `frameSubs`，**重新 load / 卸载 / `beforeunload`** 一律 `off`（防回调打到已销毁的 frame） |

`dispatch` 支持的 method（`method` → 实际通道）：

| method | 通道 |
|---|---|
| `config.get` / `config.set` | 既有 `plugin-config-get` / `plugin-config-set`（后端 `pluginmgr`；`config.fields` 已从 manifest 退场 → 主要留给旧插件兼容） |
| `config.reset` / `data.read` / `data.write` / `rpc` / `log` | `plugin-call{id, method, params}`（UDS `plugin/v1/ui.rpc`，方法名须在 `ui.rpc` 声明） |
| `action` | `pluginAction(id, action, payload)` → `POST /plugins/<id>/api/<action>`（带 `X-CK-Plugin-Id`） |
| `open-url` / `close` | `window.open(..., 'noopener')` / `preview-tab-close-this` |

> 插件页脚手架：插件仓提供 `webui/shared/{ck-mq.js, ck-plugin.js, ck-theme.js}`（`ckMq.__ckMqBind` 注入点可补投注入前排队调用）。

### 3.4 就绪态与自动重试（`usePluginReadiness`）

| 量 | 取值 |
|---|---|
| 首帧加载超时 | `LOAD_TIMEOUT_MS = 3000` |
| 自动重试上限（探活失败 + 首帧超时**共用**预算） | `AUTO_RETRY_LIMIT = 3` |
| 自动重试延迟 | `AUTO_RETRY_DELAY_MS = 800` |
| 放行状态 | `status === 'running'`（宿主 `pluginmgr.UIAsset` 的唯一放行值） |

流程：`status` 非 running → 占位（原因 `status`）；否则 HEAD 探活 → 404/异常 → 占位（`asset`）+ 自动重试；200 → 挂 iframe → `load` 前超时 → 占位（`timeout`）+ 自动重试；`error` → 占位。**手动「重试」不限次数、不占自动预算**。插件转 `running`（`subscribePluginChange` 事件）→ 自动重试（不轮询）。`stage === 'ready'` 后**不再**被降级事件拉回占位。

### 3.5 打开路径（视图落点）

| 入口 | 消息 | 落点 |
|---|---|---|
| 插件维护 | `preview-tab-open{kind:'plugin-manager'}`（设置菜单「插件维护」/ 工具栏「插件」） | 预览区页签 `CodeView` → `PluginManagerPage` |
| 插件页（按钮/菜单/`pluginBar`） | `preview-tab-open{kind:'plugin-page', plugin, page, title}` | 预览区页签 `plugin-page:<plugin>:<page>` → `PluginPageHost` |
| 布局命令（`open-tabs` 等，viewKey=`plugin:<id>:<view>`） | 命令对象 `open-dialog` / `open-tabs` / `open-drawer` / `toggle-drawer` / `close-tabs-active` / `publish-mq` | `commands.tabsRoute`：view 定义含 `preview` → 转 `plugin-page`；否则机制层 `lf-layout-open-tabs` |
| 项目配置页（codegraph 配置） | `project-config-open{tab:'codegraph'}` | `ProjectConfig` 内直接复用 `PluginPageHost`（`pluginView('codegraph','config')`），**不新造容器** |

**动作按钮（`ui.actions[]`）**：`PluginManagerPage` 渲染 → 便捷式 `view`+`container` 按容器打开本插件视图；命令式 `command` 原样交布局命令执行（与布局栏按钮同一 `commandToMq`）。

**栏条目（`ui.statusbar[]`）**：`pluginBarItems(zone)` → 带 `view` 的条目产 `{kind:'view', viewKey:'plugin:<id>:<view>', width/height ← default_size}`（复用布局 `LfBarItem` view 分支 → `PluginPageHost` iframe）；否则按 `command`（`target:plugin` → `plugin-command`；`open-view` → 预览/页签）产按钮。**不写入布局文档**（插件禁用即消失）。

### 3.6 资源 / 动作 / 配置（明细见 [28 §3A](28-plugins.md)）

| 通道 | 入口 | 约束 |
|---|---|---|
| 资源 | `GET /plugins/<id>/<entry>` → UDS `plugin/v1/ui.asset.get` | 路径限定插件 `webui` 根内（`..`/`%2e`/反斜杠 → 403）；未命中 404；`Cache-Control: no-cache, no-store, must-revalidate`；缓存键 `(plugin_id, path, plugin_version)` |
| 动作 | `POST /plugins/<id>/api/<action>` | 宿主不解析业务；校验「发起来源 id == 路径 `<id>`」（`X-CK-Plugin-Id` 白名单转发）；超时 + 错误信封 |
| 配置 | 插件 → UDS `plugin/v1/config.project.{get,load,set,delete}` | 落 data 层（`plugins.<id>.*` 命名空间）；**不落插件自有文件、不双写旧宿主键** |

---

## 4. 消息与 iframe 通信

### 4.1 插件管理面（`plugin-*`；[61 §7.2](../60-reference/61-消息一览.md)）

前端只消费其中 5 个 + 动作面 1 个：

| 消息 | 用途 |
|---|---|
| `plugin-list`（publish promise） | 初始快照 `{plugins:[pluginView…]}`；失败短退避重试（后端可能未就绪） |
| `plugin-registered` / `plugin-unregistered`（广播） | 事件链 upsert / 移除 → 注册/卸载插件视图 |
| `plugin-status`（广播） | `{id,status,detail}` → 就绪态驱动 |
| `plugin-command`（前端/宿主 → 插件） | 栏按钮 `target:plugin` 转发 |
| `plugin-ui-event`（宿主 → 前端广播/定向） | `{id, topic, payload[, instance_id?]}` → `PluginPageHost` 转投 iframe 的 `event` 信封 |

> **零新增主题**：本文全部能力经上述既有主题 + UDS 能力面实现。

### 4.2 `ui-plugin-*` 消息族（预留，未实现）

原设计稿的 `ui-plugin-{list,register,unregister,activate,set-enabled,set-visible,status,activated,context-*,call,fetch,message,close,deactivated}` 与「`PluginSlot` 插入点 / 12 种内容形态 / JSON 装配 / bundle 沙箱」**本期均未实现**，属**预留族**：

- 全仓**无生产 / 消费代码**可驱动（已核实）；前端插件入口的开关 = `plugin-set-enabled`（插件级），**没有**入口级 `visible` / 运行时 `status` 三态。
- 已在 [61](../60-reference/61-消息一览.md) §0 与 §8 末条 + `61-messages.schema.json` `coverage.planned` 中登记为**声明未实现**，**不进消息准则/对账**。
- 若将来要做「宿主主页面插入插件入口」，须按 [61 §9](../60-reference/61-消息一览.md) 变更口径（主题增删须用户授权）重新立项，**不得**直接复用本节的规划表当准则。

### 4.3 iframe 内协议（`ckPlugin` 信封）

| 方向 | 形状 |
|---|---|
| iframe → 宿主 | `{ckPlugin:1, id, type:'call', method, params}` |
| 宿主 → iframe | `{ckPlugin:1, id, type:'result', ok, result, errors}` |
| 宿主 → iframe | `{ckPlugin:1, type:'event', topic, payload}` |
| 宿主 → iframe | `{ckPlugin:1, type:'theme', theme:{id, scheme, tokens}}` |
| 宿主注入 | `window.ckMq = {on(topic,cb)→off, off(topic,cb?), emit(topic,payload)→Promise}` |

**自回显语义**：本地 `emit` 同上下文广播（发布者自订阅也收到）；跨进程后端/桥**不回显**（[11-MQ与消息](../10-architecture/11-MQ与消息.md)）。同源 + 可信代码前提下，topic 白名单是**约定**而非强制。

---

## 5. instance 归属与定向投递（摘要）

- **插件 UI 的 instance = 承载该 iframe 的窗口实例**（主窗口 / 子窗口）；插件进程自身 instance 是另一命名空间。
- iframe URL 由 `usePluginReadiness` 拼 `instance_id=<currentInstanceId()>`；**不是凭据**（可出现在 URL）。
- 插件 `plugin.publish{instance_id?}` → 宿主 `mgr.publishUIEvent` 把 `instance_id` **提升到广播顶层** → GUI 桥既有实例过滤；缺省 = 广播（兼容旧插件）。
- 无源消息（定时任务等）→ `work_dir` 的**主窗口实例**（`bridge.MainInstanceForWorkDir` / UDS `plugin/v1/instance.resolve{work_dir}`）；未命中 → **丢弃并记日志，不回落广播**。

> 明细与边界（子窗口/主窗口互不影响、事件不排队不重放）见 [28 §3A.7](28-plugins.md)。

---

## 6. 边界与重启恢复

| 场景 | 行为 |
|------|------|
| 插件未启动 / 未就绪 | 资源一律 404（宿主无副本）→ 插件页**占位**（原因 + 状态 + 手动重试 + 自动重试≤3）；插件转 running 后自动恢复 |
| 插件崩溃 / 重启 | `plugin-status` 事件驱动：已 `ready` 的页面**不被拉回占位**（避免偶然降级清空页面）；重新打开页签走新 URL（`?t=` 递增）拿到新资源 |
| 插件升级 / 重连 | 视图结构签名变化 → 卸载后重注册（iframe 重建）；宿主资源短缓存键含 `plugin_version` → 自动失效 |
| 插件禁用 / 卸载 | `plugin-unregistered` → 动态 view 定义与组件卸载；栏条目消失（不写布局文档）；已开页签显示页面缺失占位 |
| 多窗口 / 多 iframe | 每个 iframe 独立 `ckMq` 订阅集；`instance_id` 定向投递避免串窗；同一 view 可在多窗口各自打开（`mode:'multi'`） |
| iframe 销毁 / 重载 | `frameSubs` 全量 `off` + `beforeunload` 兜底 + `boundWin` 失效判定（`live()`），回调不再打到旧 frame |
| 页面加载慢 | 首帧 3s 超时 → 占位 + 自动重试（与探活失败共用预算），不会永久卡死 |
| 动作失败 | 动作面返回 4xx/5xx + `{ok:false, errors:[…]}`；`ckPlugin` 回投 `ok:false` → 插件页自行提示 |
| 服务未就绪（前端启动早期） | `plugin-list` 失败短退避重试（800ms × 5），就绪后自然收敛；`state.ready` 控制管理页「加载中」 |

---

## 7. 本期不做（原设计稿退场项）

| 项 | 处置 |
|---|---|
| `<PluginSlot location>` 主页面插入点（42 个 location） | **不做**；插件入口 = 预览页签 / 栏条目 / 维护页动作按钮 / 菜单（`ui.menus` 待消费） |
| JSON 装配（`content.type=json`，`el-*` 组件树） | **不做**（项目无 element-plus 依赖，插件页面自持 HTML） |
| JS bundle 沙箱（`ui-plugin-component-register` / deps） | **不做**（同源可信 + iframe 已足够隔离） |
| 12 种内容形态（dropdown / contextmenu / toast / inline-card / badge 等独立形态） | **不做**；栏条目 `view` 覆盖「常驻状态小组件」，其余由插件页内自行实现 |
| `ui-plugin-*` 消息族 + `ui-plugin-fetch` 网络代理 + context 白名单 | **不做**（见 §4.2）；插件取数走自己的 MCP 工具 / 动作面 / data 面 |
| `ui.menus` | 已透传到 `pluginView.menus`，**前端暂无消费方**（候选：设置菜单 / 顶栏菜单聚合，另立项） |

---

## 8. 关联测试

| 层 | 用例 |
|---|---|
| L1（node:test） | `src/frontend/test/pluginConfigSurface.test.js`（manifest `config.fields` 已删 / `ui.views` 含 config / `ui.rpc` 含 load,save,reset）· `pluginBarView.test.js`（栏条目 `view` → `kind:'view'`）· `view-registry` / 插件视图注册单测 |
| L1（Go） | `src/test/chonkpilot-plugin/unittest/plugins_test.go`（manifest 校验：views/actions/config）· `instance/manager_test.go`（主窗口实例解析） |
| L2 / L4 | `src/test/chonkpilot-gui/systest/test_plugin_manager.py`（PM1 入口 / 卡片数 / 启用开关 / 动作按钮）· `test_statusbar.py`（SB2/SB6：栏条目内嵌插件 badge）· `test_plugin_ui*.py`（资源 403/404、动作面 403 id mismatch、配置存取）· 各插件配置页套件 |

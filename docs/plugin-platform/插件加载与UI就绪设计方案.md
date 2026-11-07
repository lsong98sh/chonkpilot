# 插件加载与 UI 就绪设计方案（PLUGIN-LOAD-1）

> 状态：✅ **D1–D18 均有实现**（2026-11-02；**2026-11-04 核对**）：`plugin-refresh` 差量刷新、启动并发闸门 2、先登记后拉起、就绪态文案、刷新遮罩 / 失败关闭；`plugin.json` 极简化（`id/cmd/enabled/name/icon`）+ 完整 manifest 由插件在 `plugin.hello` 上报（Go SDK 读 `<插件目录>/manifest.json`，Python/Node 自读）；只校验 ID；`external` 模式与 `channel.*` 删除；CK Store（`window.ckStore` getter 仓库 + 状态栏抽屉）。
> 2026-11-04 核对偏差：**D5 遮罩可见**（旧审计“不可达”系误判，实为 Teleport 内 `v-if=refreshing` 兄弟节点；仅单行卡片，无双行/并发进度、无 Esc 处理）。
> 2026-11-05 收口：**D11 已统一**（插件页/维护页共用 `layoutFree.plugin.status*` 六词表，维护页不再硬编码中文、不渲染 detail 行）；**D13 已收紧**（`manifest.id` 必须等于插件 id，空值不再接受）；**D14 数据面已清理**（12 份 `manifest.json` 的 `channel`/`runtime.mode` 死字段已删）。
> 关联：[28-plugins](../spec/20-modules/28-plugins.md)（插件体系，权威）· [2C-UI插件](../spec/20-modules/2C-UI插件.md)（插件 UI 四通道）· [61-消息一览](../spec/60-reference/61-消息一览.md)（§7.2 `plugin-*`）· [41-未决项登记](../spec/40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决) · [49-待实现项](../spec/40-roadmap/49-待实现项.md#1-引用迁移与历史索引2026-11-04)
> 代码落点：`chonkpilot-framework/plugin/src/{mgr,bridge}` · `src/lib/gui/host` · `src/frontend/packages/plugin-ui/src/*` · `src/frontend/src/{pluginHost.js,layout/*,views/codeview/CodeView.vue}` · 框架仓 `chonkpilot-framework/host/web/src/layout-free/*`
> 口径来源：2026-11-02 用户逐条裁决；**本文 §2 为唯一决策表**，实现与文档冲突时以本文为准并回改 spec。

---

## 1. 问题与目标

### 1.1 要解决的问题（现状证据）

| # | 问题 | 现状证据 |
|---|------|----------|
| P1 | **插件元数据与用户可写文件混在一起** | `<root>/<id>/plugin.json` 同时是 manifest（`schema/id/runtime/mcp/ui/permissions`），用户改坏后无恢复机会 |
| P2 | **没有热发现** | `plugin-list` 只读 `discover()` 的快照，不重扫目录（`mgr/api.go` 的 `plugin-list` handler）；新放插件必须重启宿主 |
| P3 | **删除插件没有收口** | `plugin-unregistered` 主题有定义、前端已订阅，但**全仓零发布点** |
| P4 | **启动并发无上限** | `mgr.Start()` 对每个插件直接 `go supervise(p)`，十几个插件同时 spawn |
| P5 | **运行态没有“骨架 + 加载中”** | 运行态 viewKey 解析不到组件 → `view-missing` 虚线占位（`LfViewHost.vue`）；插件页 entry 取不到 → `entry=''` → 「插件页缺失」（`CodeView.vue`） |
| P6 | **前端“当前状态”没有通用读取入口** | 插件 iframe 无法读宿主组件内部状态（当前会话 / 当前目录 / 当前选中项等），只能各写一套 |
| P7 | **`external` 模式无用且无规格** | `runtime.mode=external` 仅代码分支（“等待外部进程连接”），仓库零使用、spec 零说明，`channel.addr` 仅服务它 |

### 1.2 目标（可验收）

1. `plugin.json` 极简：只承载**启动所需**信息；插件能力/UI 元数据由插件进程上报。
2. 打开布局编辑框 / 新建布局对话框前触发一次**重扫描 + 差量收敛**，新放插件可被加载、删掉的插件被注销并停进程。
3. 启动并发受控（上限 2），入口先可见、页面用“加载中/启动中”占位，**不出现白屏或假正常**。
4. 卡死的插件**如实显示 `starting`**，用户可感知并去 disable；不做启动预算、不做自动判失败。
5. 插件 iframe 可经统一入口读取宿主组件的当前状态（CK Store）。
6. 删除 `external` 模式与相关残留字段。

### 1.3 非目标

- 不做文件系统 watcher 自动发现（只在“打开布局编辑框/新建布局对话框”时刷新 + 现有开关操作）。
- 不做版本/文件变化检测（Windows 下运行中的 exe 无法被替换，替换必然伴随进程退出）。
- 不做插件元数据校验（除 ID 外一律信任上报内容）。
- 不做 CK Store 的订阅/变更通知（只做 `get`）。
- 不新增 `plugin-ready` 之类“批次完成”消息。

---

## 2. 决策表（逐条可验收）

| # | 决策 | 说明 |
|---|------|------|
| D1 | `plugin-list` 保留 | 语义不变：读缓存快照；**每个前端实例启动时调一次**（首窗口 / 第二个窗口 / 另一个 workdir / F5 重载） |
| D2 | 新增 `plugin-refresh` | 打开布局编辑框、新建布局对话框**之前**调用；重扫插件根 + 差量收敛 + 拉起新插件；返回与 `plugin-list` 同结构的全量快照 |
| D3 | 差量只做新增/消失 | 新增 → 登记 + 拉起；消失 → `plugin-unregistered` + 停进程；未变化 → 不动作、不发事件。**不做版本变化重启** |
| D4 | 刷新失败 → 关闭对话框 | 不提供“继续编辑”，弹错误提示后直接关掉布局编辑/新建布局对话框。**2026-11-04 核对：实际是“刷新失败则不打开对话框 + `message.error`”**（`LayoutProfilesView.vue:134-158`），效果等价 |
| D5 | 刷新期间遮罩 | 对话框已弹出/即将弹出，遮罩「正在刷新插件」并禁止一切操作；最多两行（并发 2），每行一个正在启动的插件；**不可关闭、不可 Esc**。**2026-11-04 核对**：遮罩实为 `<Teleport to="body">` 内与弹层**兄弟**的 `v-if="refreshing"` 节点（`LayoutProfilesView.vue:8,43-49`），`newLayout()/editCurrent()` 先 `close()` 再 refresh 时**仍可见**（Teleport 常驻），旧审计“不可达”作废；实现为**单行卡片**、无每插件两行/并发展示，也没有 Esc 监听（遮罩本身无关闭入口）。 |
| D6 | 刷新完成判定 = 新插件全部 hello 上报 | 拿到 manifest / views / 能力 / MCP 登记即算完成；**不等 `status=running`**（`mcp.mode=none` 插件可能永不 running）。**2026-11-04 核对**：另设 10s 超时兜底（`refreshWaitTimeout`），超时即返回 |
| D7 | 无启动预算 | 卡在 `starting` 就一直是 `starting`；用户可感知，自行 disable |
| D8 | 取消 `plugin-ready` | 不新增“批次结束”哨兵；状态判定只靠快照 + `plugin-registered` / `plugin-status` |
| D9 | 启动并发上限 2 | 闸门只约束 `spawn → hello` 段，进程起来后不占槽 |
| D10 | 先扫描 → 先登记 → 再并发拉起 | 扫描同步全量；登记后入口即可见（仅 `id/name/icon`）；拉起按 D9 分批 |
| D11 | 失败文案统一「插件未安装」 | 只显示状态文字（`运行中` / `启动中` / `启动失败` / `已停止` / `已禁用` / `未安装`），**不加 detail 行**。**2026-11-05 已统一**：插件页 `PluginPageHost.vue` 与维护页 `PluginManagerPage.vue` 共用 `layoutFree.plugin.status*`（discovered/registering/starting→启动中，degraded/error→启动失败，stopping/stopped→已停止）；维护页原硬编码「失败/降级/注册中」与 detail 行已删；回归 `test/pluginStatusCopy.test.js`。 |
| D12 | `plugin.json` 极简 | `{ id, cmd(exe+args), enabled, name?, icon? }`；manifest（`version/description/ui/mcp/permissions`…）由插件进程上报。**2026-11-04 核对：Go SDK 读 `<插件目录>/manifest.json`（文件，非 embed）**；Python/Node 自读同一文件 |
| D13 | 只校验 ID | 上报 `id` 必须等于目录名（`plugin.json` 若写 `id` 也须一致）；其余字段一律信任。**2026-11-05 已收紧**——`manifest.id` 为空或不等即报错（`mgr/api.go` `registerManifest`；回归 `mgr/discover_test.go`）。 |
| D14 | 删除 `external` | 去掉 `runtime.mode`、`channel.{transport,addr}`、「等待外部进程连接」分支；插件一律由宿主 spawn。**2026-11-05 已收口**：框架代码此前已删；12 份 `manifest.json` 的 `channel`/`runtime.mode` 死字段本次清除。 |
| D15 | 监管参数宿主默认 + 上报可选覆盖 | `restart` / `health` / `stop.grace_ms` / `mcp.timeout` 宿主内置默认；插件上报后以上报值为准（hello 之后生效） |
| D16 | CK Store | 前端全局 getter 仓库：组件注册 `key → () => value`；只提供 `get`；未注册返回空；**仅 iframe 可读**（`window.top.ckStore`）；无权限白名单、无 key 登记表 |
| D17 | CK Store 抽屉 | 状态栏加一个图标，点击打开抽屉；列表 = 当前窗口已注册 key + 当前值；打开时求值一次 + 手动「刷新」按钮；不做搜索/附加列/展开交互 |
| D18 | 后端不做按 workspace/对话的独立初始化 | 需要上下文时由前端在消息载荷里携带（`session_id` / `work_dir` 等） |

---

## 3. 详细设计

### 3.1 文件布局与归属

```text
<插件根>/<id>/
  plugin.json                 # 极简：id / cmd / enabled（+ name/icon 可选）
  chonkpilot-plugin-<id>.exe  # canonical（cmd 缺省时按此约定）
  webui/*.html                # 插件页面（iframe 拉取）
```

| 内容 | 载体 | 读取方 | 缺失/损坏 |
|---|---|---|---|
| `id` / `cmd` / `enabled` / `name` / `icon` | `plugin.json` | 宿主（发现阶段） | 该插件不加载（不猜、不兜底） |
| 完整 manifest | Go：SDK 读 `<插件目录>/manifest.json`（随 `plugin.hello` 上报）；Python/Node：插件自读同一文件 | 插件进程 → `plugin.hello` 上报 | `id` 不符 → 该插件 `error`，不登记 |
| 页面资源 | `ui.assets.mode = bytes/dir/resource` | 宿主按请求读 | 按资源语义 404 |

`plugin.json` 字段（唯一）：

```json
{
  "id": "remote",
  "cmd": ["chonkpilot-plugin-remote.exe"],   // 或运行时字符串形式
  "enabled": true,
  "name": "远程接入",
  "icon": "link"
}
```

> `cmd` 缺省 = 目录内 `chonkpilot-plugin-<id>.exe`；`enabled` 缺省 = `true`，且仍以 `<数据根>/plugins/enabled.json`（设置页开关）为准。

### 3.2 发现 / 启动 / 刷新

```text
前端打开布局编辑框 / 新建布局对话框
  → 先 close() 菜单弹层，置 refreshing=true（遮罩「正在刷新插件」仍在 Teleport 内可见、禁操作）
  → plugin-refresh
        宿主：scan 四个插件根（读 plugin.json）
              → 差量：新增登记（id/name/icon）/ 消失 unregistered + stop / 未变不动
              → 对新增：按并发闸门 2 拉起（spawn → 等 hello → 放槽 → 下一个）
              → 等本批新增插件全部 hello 上报（10s 超时兜底）
  → 返回 {plugins:[pluginView…]}
  → refreshing=false，再打开编辑框 / 新建对话框（2026-11-04 核对：实际顺序）
```

| 环节 | 约束 |
|---|---|
| 扫描 | 同步全量；只读 `plugin.json`（不读 exe 内 manifest） |
| 登记 | 扫描后立刻登记并广播 `plugin-registered`（此时 `views` 为空，仅有 `id/name/icon/status=starting`） |
| 拉起 | 并发上限 **2**；槽位 = `spawn` 到 `hello` 之间；进程退出/报错同样释放 |
| 上报 | `plugin.hello` 携带 manifest → 校验 `id == 目录名` → 合并进插件台账 → 再发 `plugin-registered`（含 views/statusbar/actions/version） |
| 单飞 | 已在刷新中 → 复用当前结果返回，不重复扫描/拉起 |
| 失败 | 刷新请求本身失败 → 关闭对话框（D4）；单插件失败 → 该插件 `error`，不影响其它插件与刷新结果 |

### 3.3 消息面

| 消息 | 方向 | 变更 |
|---|---|---|
| `plugin-list` | 前端 → 宿主（req/res） | **不变**（缓存快照）；新增触发约定 = 每个前端实例启动时一次 |
| `plugin-refresh` | 前端 → 宿主（req/res） | **新增**；`{}` → `{plugins:[pluginView…]}`；副作用 = 重扫 + 差量 + 拉起 |
| `plugin-registered` | 宿主 → 前端（广播） | 语义不变；新增发送时机 = 发现登记（最小字段）、hello 上报后（完整字段） |
| `plugin-unregistered` | 宿主 → 前端（广播） | **补发布点**（差量消失时）；payload 仍 `{id}` |
| `plugin-status` | 宿主 → 前端（广播） | 不变 |
| ~~`plugin-ready`~~ | — | **不新增** |

`plugin.hello` 增加可选字段 `manifest`（**桥方法字段，非 MQ 主题**）：插件在握手时上报自己的完整 manifest；宿主只校验 `id`。

### 3.4 前端就绪态与文案

单个插件页/框的判定链（只读快照 + 事件，零轮询）：

```text
框挂载（viewKey = plugin:<id>:<view>）
  ├─ 台账里没有该 id            → 「未安装」
  ├─ status = disabled/stopped  → 「已禁用 / 已停止」（2026-11-04 核对：未统一为「未安装」）
  ├─ status = error             → 「启动失败」（2026-11-04 核对：未统一为「未安装」）
  ├─ status = starting          → 「启动中」（一直如此 = 卡住，用户可感知）
  └─ status = running           → HEAD 探活 → 挂 iframe（失败则占位 + 自动重试，沿用现状）
```

- 只渲染**状态文字**，不加 detail 行（D11）；
- 布局里已有的插件框在 manifest 未到前也先渲染框 + 「启动中」，entry 按 `webui/<view>.html` 约定回退；
- `usePluginReadiness` 的现有自动重试保留（探索活失败/超时），但不再引入“批次完成”概念。

布局编辑框/新建布局对话框的打开流程见 §3.2（遮罩 D5，失败关闭 D4）。

### 3.5 CK Store（前端状态读取）

```js
// 注册（组件 / composable；onMounted 注册、onBeforeUnmount 注销）
ckStore.register('chat.currentSession', () => activeSessionId.value)
ckStore.register('workspace.current', () => workDir.value)

// 读取（唯一入口；同步值与 Promise 统一按 Promise 返回）
await ckStore.get('chat.currentSession')   // 未注册 → 空
```

| 项 | 约定 |
|---|---|
| 作用域 | **每窗口一份**（组件状态本身是窗口内状态） |
| 唯一消费方 | 插件 iframe：`window.top.ckStore.get(key)`（同源）；插件进程**不支持**（需要上下文时由前端随消息携带，见 D18） |
| 未注册 | 返回空（`null`），不抛错；宿主只记一条 debug 日志 |
| 权限 | 无白名单（任何 iframe 可读任何 key） |
| key 命名 | 字符串自由命名，**不维护登记表**；重复注册保留先注册者并告警 |
| 变更通知 | 不做订阅；需要“变化”的场景走既有 MQ 事件 |

**CK Store 抽屉（D17）**：状态栏右侧新增内置图标 → 打开抽屉 → 列表逐行 `key` + 当前值（打开时求值一次）；顶部一个「刷新」按钮；不做搜索、附加列、展开交互；getter 抛错的行显示错误文本，不影响其它行。

### 3.6 后端上下文

后端插件不做“按 workspace / 对话”的初始化。需要 `session_id` / `work_dir` 等上下文时，由**前端在消息载荷里携带**（现有 `instance_id` 定向与 `work_dir` 字段口径不变）。现有 `instance-register` 补发（`replayInstances`）保留，用于 workdir/实例事件，不作为“当前前端状态”的通道。

---

## 4. 与现状的差异 / 迁移

| 现状 | 目标 | 迁移动作 |
|---|---|---|
| `plugin.json` = 完整 manifest | 极简五字段 | 已改造：`src/initdata/capability/plugins/<id>/plugin.json`（极简）+ `manifest.json`（完整，随插件目录投放、插件自读上报）；`plugin.json` 缺 `cmd` 时按 `chonkpilot-plugin-<id>.exe` 约定 |
| `runtime.mode=managed/external` | 删除 mode | 删除校验分支、`channel.addr`、「等待外部进程连接」 |
| `noChrome` / 启动顺序等 | 不变 | — |
| `plugin-list` 是唯一清单入口 | 增加 `plugin-refresh` | 前端只在窗口启动时 `plugin-list`；编辑器打开前 `plugin-refresh` |
| `view-missing` 占位 | 插件框先画 + 「启动中」 | `view-registry` / `LfViewHost` / `CodeView` 三处判定 |
| 无 CK Store | 新增 | 新模块 + 状态栏抽屉 |

**现有插件清单需同步改造**：`src/initdata/capability/plugins/*`（web/task/remote/desktop）、`chonkpilot-plugins/*`（file/history/codegraph/vfts/memory/crontab/web 等）。

---

## 5. 边界与异常

| 场景 | 行为 |
|---|---|
| `plugin.json` 缺失 / 语法错误 / 缺 `id` | 该插件不加载，记错误日志；不影响其它插件 |
| 上报 `id` ≠ 目录名 | 该插件 `status=error`，不登记 UI/MCP；停进程 |
| 上报 manifest 字段非法（views 为空 / entry 异常） | 不校验，按上报内容使用；资源请求侧做路径越界防护（`..`/绝对路径归一化） |
| 插件 spawn 失败 | `status=error`，释放并发槽，继续下一个 |
| 插件卡在 `starting`（永不 hello） | 一直显示「启动中」；并发槽被占用（上限 2，最坏情况会占满两个槽，其它插件排队） |
| 刷新过程中用户关闭窗口 | 刷新继续在宿主侧执行（与窗口解耦）；无窗口时无广播消费者 |
| 刷新时插件目录被删但进程仍在跑 | `plugin-unregistered` → `stopPlugin`（优雅关停 + 宽限 + 兜底杀进程树） |
| 刷新请求超时 / 宿主报错 | 关闭布局对话框 + 错误提示（D4） |
| CK Store getter 抛错 / Promise reject | 该行显示错误，其它行正常 |
| iframe 主动读不存在 key | 得到空值 |

---

## 6. 重启恢复

| 状态 | 落点 | 重启后 |
|---|---|---|
| 插件清单 | 插件目录 + `enabled.json` | `mgr.Start()` 重新扫描并拉起启用插件（与刷新同一套 `discover` 逻辑） |
| 插件运行态（`status/pid/restart 计数`） | 内存 | 全部重建；无持久化“运行中”语义 |
| `plugin-refresh` 的在飞请求 | 内存 | 丢失；前端窗口重载后重新 `plugin-list`，编辑器再次打开会重新刷新 |
| CK Store 注册项 | 内存（每窗口） | 组件重新挂载时重新注册；抽屉重新打开即重新求值 |
| 布局文档 | 布局持久化（`layout.free`） | 不变；其中的 `plugin:<id>:<view>` 继续按 §3.4 判定 |

“异常中断 → 重启 → 恢复”测试口径：杀宿主进程后重启，插件应重新拉起、`plugin-list` 返回同一批 id、卡死插件仍显示 `启动中`。

---

## 7. 测试

| 层 | 内容 |
|---|---|
| L1 Go（framework `mgr`） | 差量刷新（新增/消失/未变）；消失时停进程；并发闸门最多 2；`manifest.id` 为空/不一致 → error（2026-11-05 收紧）；`hello.manifest` 合并 |
| L1 Go（bridge） | `plugin.hello` 携带 manifest 的解析与 `id` 校验 |
| L1 前端（node:test） | `usePlugins`（`plugin-refresh` 应答替换快照、`plugin-unregistered` 摘除视图）；`usePluginReadiness`（未知 id → 未安装；starting → 启动中；running → 探活/iframe）；`ckStore`（注册/get/未注册返回空/重复注册告警）；抽屉渲染（key + 值 + 刷新） |
| L4 systest（mq 驱动） | `plugin-list` 快照 → `plugin-refresh` 新增/消失 → 断言 `plugin-registered` / `plugin-unregistered` / `plugin-status`；卡死插件保持 `starting` |
| L5 人工 | 布局编辑框遮罩（最多两行、不可关闭、失败关闭）；状态栏抽屉 |

---

## 8. 影响面（文件清单）

| 层 | 文件 |
|---|---|
| 框架 `plugin/mgr` | `manager.go`（discover/差量/并发闸门/删 external）、`manifest.go`（校验删除、极简化）、`api.go`（`plugin-list`/新增 `plugin-refresh`/`plugin-unregistered` 发布） |
| 框架 `plugin/bridge` | `control.go`（hello 接 `manifest` 并校验 id）、`plugins.go`（按需） |
| 宿主 | `src/lib/gui/host/main.go`（装配 CK Store 端口 / 抽屉内置 view）、`src/lib/gui/pluginassets.go`（越界防护确认） |
| 前端 | `packages/plugin-ui/src/{usePlugins,usePluginReadiness,PluginPageHost,PluginManagerPage}.{js,vue}`、新增 `ckStore.js` 与抽屉组件、`src/frontend/src/{pluginHost.js,layout/view-registry.js,layout/views.registry.js,views/codeview/CodeView.vue}`、`framework/host/web/src/layout-free/views/LfViewHost.vue` |
| 插件仓 | 各 `plugin.json` 瘦身；Go 插件 manifest embed；SDK `hello` 带 manifest |
| 文档 | 本文 · `28-plugins` · `2C-UI插件` · `61-消息一览 §7.2` · `41`/`49` |

---

## 9. 未决与后续

1. CK Store 是否需要 `has(key)`（区分“未注册”与“值为空”）——当前按 D16 不做。
2. `plugin-refresh` 的宿主侧超时保护（前端遮罩不可关闭，若宿主实现挂起，用户只能关窗口）——按 D5/D7 暂不做。
3. 老布局引用已删插件时，是否需要“清理布局”入口——当前只显示「未安装」，不自动改布局。

# 26 · H5 远程访问（chonkpilot-app）

> **〔PRODSIM-1（2026-10-22，用户拍板）〕本专题保留，但接入前提已变**：H5 **不以浏览器形态运行**，将来做成**外部 WebView 壳 APP**；认证统一走 **registry（注册服务器）**，不依赖已删除的本地 auth / browser 形态。本篇中「挂 `chonkpilot-server.exe`」「`--http-addr` / `--web-root`」等描述为**历史方案**（该 exe 与 browser 入口已删除）；落地时按 registry + 单产物 `chonkpilot.exe` 重新设计接入面。

> 状态：🚧 初稿（待评审）——**尚未实施**
> 关联：[19-多租户服务端形态](19-多租户服务端形态.md) · [22-实例与会话标识](22-实例与会话标识.md) · [20-实例隔离与后端分离](20-实例隔离与后端分离.md) · [61-消息一览](../60-reference/61-消息一览.md) · [33-对话](../30-function-points/33-对话.md) · [34-任务](../30-function-points/34-任务.md) · [03-构建与部署](../00-overview/03-构建与部署.md)

## 1. 问题与目标

- **要解决的问题**：GUI / flash 只能在 PC 前操作；希望在手机 / 平板（后续再套鸿蒙 / 安卓 / Mac 壳）上查看并继续 PC 上**正在进行**的那一轮对话与任务。
- **目标（可验收）**：
  1. H5 登录 → 选择同一用户在 **PC 上已打开的工作区（workdir）** → 进入主会话对话页，实时显示 PC 的对话与任务。
  2. **空闲时可发送消息；运行中可停止；不排队**（运行中不接收新发送，UI 置灰 + 服务端兜底拒绝）。
  3. 顶部两项：**目录浏览**（预览；"打开 / 发送"属原生，本期降级）、**详细**（任务树 → 任务详情）；右上角**会话按钮**（显示主会话）。
  4. **消息面零变更**：[61-消息一览](../60-reference/61-消息一览.md) 不新增 / 不修改 / 不删除任何主题与 payload。
- **非目标**：单聊 / 多聊 / 配置 / 场景 / 知识库 / 多窗口；多租户数据隔离；文件写操作；大文件上传；原生壳（鸿蒙 / 安卓 / Mac）本期不做，仅预留桥接口。

## 2. 总体方案

H5 是 browser 形态的**第二个客户端**，不是一个独立工作区：登录后选择「同一用户在 PC 上已打开的工作区」，HTTP 入口把**该条连接**绑定到 PC 的那个 `instance`（影子连接：同 `instance_id`、同 `data_dir`）。因此会话、任务、事件流与**会话互斥锁（busy）**全部与 PC 同源——H5 看到的就是 PC 正在跑的那一轮。

发送走 `llm-start`（仅在空闲时允许；运行中由服务端 busy 拒绝兜底），停止走 `llm-cancel`。**不使用 `llm-inbox-*`**（不排队）。

前端在 `src/frontend` 增加**独立 H5 入口**（第三个构建入口），只挂载「登录 / 对话 / 目录 / 任务树」四个面；PC 浏览器也可打开该入口（便于测试）。

> **〔PRODSIM-1 注（2026-10-22）〕以下拓扑为**历史方案**（基于已删除的 `flash` / `gui-client` / 独立 `chonkpilot-server.exe` 与 browser HTTP 入口）。落地时按「唯一产物 `chonkpilot.exe` + 外部 WebView 壳 + registry 认证」重新设计接入面；`chonkpilot-server.exe` 不再产出、`--http-addr` / `--web-root` / `httpapi` 已删除、本地认证域已删。**

```text
PC: chonkpilot-flash / chonkpilot-gui-client          ← 已删除（历史）
      │ UDS（本机信任）
      ▼
chonkpilot-server.exe ── MQ 总线 ── llm-server / persist / filesys   ← 不再产出（历史）
      │  ▲
      │  └── 出站长连接（注册 / 心跳 / relay 隧道）──► 注册中心
      │                                              账号 · 登录 · 目录 · relay
      │                                                   ▲
      └────────── HTTP + SSE（经 relay 透明转发）──────────┘
                                                          ▲
                                              手机浏览器 / 壳 WebView
```

H5 通过**注册中心**接入：在注册中心登录 → 选择同账号的在线 server → 由注册中心 relay 转发。注册中心只做身份、目录与字节转发，不碰业务（§3.8）。

## 3. 详细设计

### 3.1 拓扑与作用域

> **〔PRODSIM-1 注（2026-10-22）〕** 下表为**历史方案**：`PC（gui / flash）` 行与「UDS 本地前门」已无载体；将来重设计时 PC 侧 = 唯一产物 `chonkpilot.exe`（`form=desktop`、进程内 `instance`），H5 = 外部 WebView 壳经 registry relay 接入。

| 客户端 | 入口 | 身份 | instance | data_dir |
|---|---|---|---|---|
| ~~PC（gui / flash）~~ | ~~UDS 本地前门~~ | ~~本机信任~~ | ~~PC 的 instance A~~ | ~~`<workdir>/.chonkpilot`~~ |
| H5（待做） | registry relay → `chonkpilot.exe` | registry 令牌 | **绑定到 PC 当前项目主窗口的 instance A** | **同 A** |
| H5（工作区未在 PC 打开，用户确认后） | 同上 | 同上 | 新建 instance B | 仍为 `<workdir>/.chonkpilot` |

**硬约束**：H5 与 PC 必须是**同一个 `instance_id`**（或至少同一运行态桶），否则：

- 运行态（`turns` / `busy`）按 `instKey(instance, id)` 分桶（[instancescope.go](../../../src/lib/llm/server/instancescope.go)）→ H5 会误判"空闲"，在同一 session 上并发出第二轮；
- `session-receive` 等事件载荷带 `instance_id`，下行为按实例过滤（[httpapi.go](../../../src/lib/llm/httpapi/httpapi.go)）→ H5 收不到 PC 的流。

### 3.2 连接级作用域（后端核心改造）

现状：`instanceID` / `workDir` / `dataDir` 是 `httpapi.Server` 的**进程级字段**，所有浏览器客户端共用一个实例（[httpapi.go](../../../src/lib/llm/httpapi/httpapi.go)）。改造点：

| # | 改造 | 说明 |
|---|---|---|
| 1 | 作用域改为**每连接上下文** | `{instance_id, work_dir, data_dir, user}` 由登录会话派生；`bindData` / `bindFilesys` / `injectInstance` 读取该上下文 |
| 2 | SSE 按连接过滤 | `forwardEvent` 现用全局 `s.instanceID` 过滤后广播给所有客户端；改为**按每个连接的绑定实例**过滤 |
| 3 | 绑定解析 | `instance.Manager.List()` 按 `user + work_dir` 匹配 PC 的顶层实例；命中 → 绑定；未命中 → 按 §3.3 处理（`List()` 已存在） |
| 4 | `data_dir` 口径对齐 | browser 认证分支不再用 `<用户数据根>/<uid>`，而用 `<workdir>/.chonkpilot`（与本地前门一致）；否则同库不成立（[claim.go](../../../src/lib/llm/server/claim.go)） |
| 5 | 影子连接的写权限收敛 | H5 连接**禁止**发 `instance-exit` / `instance-heartbeat`，**禁止**改写实例登记（`instance-register` 绑定不得覆盖 PC 的 work_dir） |
| 6 | `/show/` 补到 browser 入口 | 现在只有 GUI 桥有；缺失会把 `/show/...` 落到静态处理（404），图片 / PDF / 附件全不可用 |

### 3.3 工作区选择与绑定

1. H5 调 `gui.recent.list`（读 usr config 的 `recent_dirs`，同一用户跨端共享）列出候选工作区。
2. 服务端在实例表中按 `user + work_dir` 匹配：
   - **命中**（PC 已打开）→ 绑定该实例，直接进对话页。
   - **未命中** → 弹提示「该工作区未在 PC 打开」，让用户二选一：
     - 取消（留在选择页）；
     - **由 server 启动**（新建实例 B，`data_dir` 仍为 `<workdir>/.chonkpilot`）。
3. 归属校验：所选 `work_dir` 必须在该用户的 `auth` 项目/允许根内（复用 `allowWorkDir`）；实例的 `user` 必须与 cookie 身份一致，否则拒绝。

> **禁止**由前端发 `instance-claim` 去复用 PC 的 `instance_id`：`instance-claim` 在入口是豁免注入的，认证分支会把它重算成 `<用户数据根>/<uid>` 并**覆盖** PC 的登记。绑定只能在入口内部完成。

### 3.4 发送与停止（不排队）

| 场景 | 行为 |
|---|---|
| 空闲（该 session 无运行轮） | H5 发 `llm-start`（`session` + 客户端分配 `turn` + `q`） |
| 运行中 | 输入框禁用，只显示「停止」；发送入口不发任何排队消息（**不用 `llm-inbox-*`**） |
| 竞态（PC 恰好在同一瞬间起轮） | 服务端按「同 instance 同 session 互斥」返回 `{accepted:false, error:"session busy: <turn>"}`；H5 收到后置忙并提示 |
| 停止 | H5 发 `llm-cancel{session}`（`turn` 可缺省，服务端按会话定位运行轮并级联取消工具/任务） |
| 运行中出现 `ask-user` | H5 可 `ask-user-reply`（单问单答）——这是运行轮的一部分，不属排队发送 |
| 运行结束 | `session-complete`（前端 type `llm-complete`）→ 恢复输入 |

忙碌态判定（H5 侧）：

- 置忙：本端 `llm-start` 成功 / 收到 `turn-start`（browser 入口映射，见 httpapi 的 `mqTypeMap`）；
- 置闲：`llm-complete`；
- 重连 / 首屏：以 `data-session-latest` + `data-session-history` 校正；仍不确定时以「发送被拒」为最终判据（服务端权威）。

### 3.5 页面与消息映射

| 面 | 消息（全部已在 61 中定义） |
|---|---|
| 登录 | `login-in` / `login-register` / `login-out` |
| 工作区选择 | `gui.recent.list` →（入口内部实例匹配） |
| 对话显示 | `data-session-history`（首屏 / 重连）、`session-receive`、`turn-start`、`session-complete`、`ask-user` |
| 发送 / 停止 | `llm-start`、`llm-cancel`、`ask-user-reply` |
| 右上会话按钮（主会话） | `data-session-list`、`data-session-active-get` |
| 目录浏览（只读） | `filesys.list`、`filesys.content`、`filesys.changed`、`/show/`（HTTP 资源，非消息） |
| 详细（任务树 → 任务详情） | `data-tasktree-list`、`data-tasktree-tasks`、`tasks.started` / `tasks.updated` / `tasks.done` |

### 3.6 原生能力（本期不做，仅预留）

- 壳注入 `window.__ckShell`（能力探测），提供 `toast` / `openFile` / `shareFile`；
- 无壳（浏览器）时：toast 走页内提示，「打开 / 发送」降级为「预览 / 下载」并提示"需原生支持"；
- 原生打开 / 分享的字节源统一走 `/show/`。

### 3.7 前端入口

- 在 `src/frontend` 增加**第三个构建入口**（H5），与 desktop `embed`、browser `index.html` 产物隔离，避免把桌面布局引擎带进手机包。
- UA 仅作**默认跳转**；必须保留显式入口（`?h5=1` 或 `/#/h5`），否则 PC 浏览器 / 微信内置浏览器会被误跳，测试也无法回到桌面版。

### 3.8 注册中心（账号 + 登录 + 目录 + relay）

**定位**：注册中心 = **唯一账号源 + 接入层**。负责账号与登录、server 目录与在线状态，并作为 **relay** 把 H5 的 `/publish` + `/events` + `/show/` 透明转发到用户选中的 server。**不解析 MQ、不碰会话/任务/文件**。

| 组件 | 职责 |
|---|---|
| PC 的 server | 启动时用**绑定令牌**注册并保持长连接（出站，NAT 友好）；接受 relay 的**受信接入** |
| 注册中心 | 账号 / 登录 / 设备绑定 / 目录 / 心跳 TTL / 按账号路由 / 字节转发 / TLS 终结 |
| H5 | 在注册中心登录 → 选择 server → 之后所有请求都指向注册中心，地址不再变化 |

#### 目录项（注册中心持有的最小信息）

| 字段 | 说明 |
|---|---|
| `server_id` | 稳定标识（同机同 OS 用户恒定；重启不产生新条目） |
| `name` / `platform` / `version` | 展示与兼容判断（默认「主机名 / 用户名」，可改） |
| `owner` | 归属的注册中心账号 id（由绑定步骤写入） |
| `online` / `last_seen` | 心跳维护；超 TTL 判离线 |
| `mode` | `relay`（本期目标）/ `direct`（可选：返回 `endpoint` 由 H5 直连） |

#### 流程

1. **设备绑定**：PC 首次用注册中心账号（或一次性绑定码）把该 server 绑到账号，下发持久化 `bind_token`（服务端只存哈希）。
2. **启动注册**：server 启动 → 注册 + 周期心跳；正常退出注销，异常退出靠 TTL 收口。
3. **H5 登录**：注册中心签发会话，返回 `owner = 当前账号` 的在线 server 列表（多设备可选）。
4. **选择 server**：注册中心把该 H5 会话绑定到目标 server 的隧道流；此后 `/publish`、`/events`、`/show/` 全走注册中心。
5. **身份传递**：relay 转发时注入**签名身份断言**（用户 id + 时间戳 + nonce，HMAC 或 mTLS）；server 仅在显式开启「信任 relay」时接受。
6. **绑定实例**：连上后仍按 §3.3 选工作区、§3.2 绑定 PC 的实例——**注册中心不参与**。

#### 身份双源（必须显式处理）

| 模式 | 身份来源 |
|---|---|
| 直连 | 沿用现有 `login-in` + cookie（61 §4.6 不变） |
| relay | **注册中心为唯一账号源**：server 不再要求前端 `login-in`，以签名断言映射到本机 auth 用户（首次见到的账号自动建用户记录与默认工作区） |

- 「信任 relay」必须**显式配置 + 共享密钥**，默认关闭；否则伪造断言即可提权。
- 断言必须带时间戳 + nonce 防重放；server 侧拒绝无签名 / 过期 / 未知密钥的请求。

#### 不变量

- **零消息面变更**：注册中心的接口是自有 HTTP，不是 MQ 主题；`login-in` 等 61 消息不改。
- relay 模式下 server 的 `http-addr` 应只绑 loopback，不直接暴露公网。
- relay 只转发，不做业务判定：工作区授权、实例绑定、busy 互斥全部仍在 server。
- 命名：仓库已有 `gui/bridge`、`pluginbridge`、`llm-bridge`、`mcp-gateway`；本组件统一称**注册中心 / registry**，实现名建议 `chonkpilot-registry`。




## 4. 规则与约定

| 项 | 结论 | 理由 |
|---|---|---|
| 消息面 | **零变更**，全部沿用 61 | 61 为测试唯一准则，动前须用户确认 |
| 排队 | H5 **不使用** `llm-inbox-*` | 需求明确「不排队，只在空闲时发送」 |
| 发送入口 | `llm-start` | 空闲首条的正常路径；busy 由服务端权威拒绝 |
| 停止入口 | `llm-cancel{session}` | `turn` 可缺省；级联取消在飞工具/任务 |
| 实例语义 | H5 = **影子连接** | 不发 `instance-exit` / heartbeat，不改写登记 |
| 工作区来源 | 只能是同一用户、PC 已打开或经用户确认新建的工作区 | 防止越权与"远程凭空开项目" |
| `data_dir` | 恒为 `<workdir>/.chonkpilot` | 与本地前门同口径，才能同库同历史 |
| 目录能力 | 本期只读 | 需求只要求浏览 / 预览；写操作另行设计 |
| 接入方式 | H5 只连注册中心，不直连 PC | PC 不暴露公网端口；server 出站注册 + relay 转发 |
| 身份 | relay 模式 = 注册中心唯一账号源 | 避免两套账号；server 侧需显式信任 relay 才接受断言 |
| 部署 | H5 只能挂 `chonkpilot-server.exe` | desktop 单体无 HTTP 入口（唯一装配点 = `src/server/main.go`） |

## 5. 与既有机制的关系

- **61 消息面**：不触发冻结变更。本方案只改变"消息由谁、以哪个实例身份发出"，不改主题与 payload。
- **19 多租户**：本方案不做多租户 `datadir` 隔离；多用户靠"各自工作区 + 连接级绑定 + 归属校验"隔离。
- **22 实例标识**：新增"影子连接"用法（同一 instance 多连接），实例的注册/心跳/退出语义仍由 PC 侧唯一负责。
- **身份（新增，需评审）**：relay 模式下注册中心为**唯一账号源**，server 的 `auth.db` 退化为「项目/工作区授权表」；直连模式保持 `login-in` + cookie。两者并存，但 server 必须显式开启「信任 relay」才接受断言。
- **部署**：`chonkpilot-server.exe` 是同机单例；relay 模式下 `--http-addr` 只绑 loopback，公网面只暴露注册中心（TLS 由注册中心终结，代码内仍无 TLS）。
- **构建**：H5 入口需要独立 npm 构建入口；desktop / browser 构建产物不受影响。

## 6. 边界、异常与重启恢复

### 6.1 边界与异常

| 场景 | 行为 |
|---|---|
| PC 在跑、H5 尝试发送 | UI 已禁用；竞态由服务端 `accepted:false` 拒绝，H5 置忙 |
| 两端同时发送 | 一胜一拒（同 instance 同 session 互斥） |
| PC 关闭 / 退出 | 服务端取消该实例名下运行轮并释放锁；H5 收到 `instance-exit` 相关事件后提示「PC 已离开」，提供「由 server 接管」（走 §3.3 新建） |
| H5 关闭 / 登出 | **不得**发 `instance-exit`（会杀掉 PC 的轮） |
| 手机切后台 / 断网 | `EventSource` 自动重连；重连后拉 `data-session-history`、`data-tasktree-list` 对齐（SSE 无 `Last-Event-ID` 补偿，慢客户端会丢帧） |
| 同一 workdir 两端都要"打开" | 优先命中既有实例并绑定，不重复新建；已存在顶层实例时明确提示 |
| 多用户并发 | 每连接独立绑定 + `allowWorkDir` 归属校验；不得读写他人实例 |
| 越权尝试 | 非本人工作区 / 非本人实例 → 明确拒绝（不静默降级） |

### 6.2 重启恢复

1. **状态落在哪**：连接绑定（instance / work_dir / data_dir）在服务端**内存**；会话、轮次、消息、任务树在 prjusr 库；H5 的 UI 忙碌态在内存。
2. **输入是否受理即持久化**：`llm-start` 由服务端拆 `session-start` + `session-send` 落库；H5 断线期间不重发（避免重复轮次），由用户确认后再发。
3. **非终态收口**：服务端重启后，运行轮按既有生命周期收口（`data-session-cleanup-stale` / 轮次终态广播）；H5 以 `llm-complete` 或历史快照为准，不自行推断成功。
4. **重启后重建什么**：绑定关系**不持久化**——H5 重新登录 + 重选工作区即重建；会话与任务树从库恢复；不重建运行中的 turn。
5. **副作用语义**：H5 不引入本地队列，因此不存在"重启后补发"；`llm-cancel` 幂等（无运行轮 → 明确记录不动作）。

## 7. 落地清单

| 阶段 | 内容 | 状态 | 关联 |
|---|---|:---:|---|
| P0 | httpapi 连接级作用域（1/2/3/4/5） | 🔵 | `src/lib/llm/httpapi` |
| P0 | browser 入口补 `/show/` | 🔵 | `src/lib/llm/httpapi`、`src/lib/gui/internal/fileserver` |
| P0 | 工作区选择 + 实例绑定 + 未打开提示/新建 | 🔵 | `src/lib/llm/httpapi`、`src/plugins/plugin/instance` |
| P1 | H5 入口（第三构建入口）+ 登录 / 对话 / 目录 / 任务树四面 | 🔵 | `src/frontend` |
| P1 | 忙碌态 + 停止 + ask-user 作答 | 🔵 | `src/frontend` |
| P1 | 部署：HTTPS 反代、关注册、限源 | 🔵 | 部署脚本 / 文档 |
| P2 | 原生壳（鸿蒙 / 安卓 / Mac）：`__ckShell` toast / openFile / shareFile | ⏳ | 新建工程 |
| P2 | 注册中心第一步：账号 / 登录 / 设备绑定 / 目录 / 心跳 TTL | ⏳ | 新组件 `chonkpilot-registry` |
| P2 | 注册中心第二步：relay（server 出站隧道 + 透明转发 + 身份断言） | ⏳ | 新组件 + server 出站客户端 |

## 8. 关联文档

- [19-多租户服务端形态](19-多租户服务端形态.md)：HTTP + SSE 入口、认证、允许根
- [22-实例与会话标识](22-实例与会话标识.md)：instance / session / turn 口径
- [20-实例隔离与后端分离](20-实例隔离与后端分离.md)：运行态按 instance 分桶
- [23-工程与部署拓扑](23-工程与部署拓扑.md)：服务端单例与部署
- [61-消息一览](../60-reference/61-消息一览.md)：消息面唯一准则
- [62-命令行与参数](../60-reference/62-命令行与参数.md)：`--http-addr` / `--web-root` / `--allow-register` / `--project-roots`

## 9. 未决项

| # | 事项 | 待定内容 |
|---|---|---|
| 1 | 接管语义 | PC 退出后 H5「由 server 接管」是复用原实例标识，还是新建实例并继承 `data_dir` |
| 2 | 同工作区多端 | PC 后续打开同一 workdir 时，是提示"已在别处打开"还是允许并存（需明确 busy 归属） |
| 3 | 原生桥契约 | `__ckShell` 的方法名 / 参数 / 返回，需与三端壳一起定稿 |
| 4 | 登录保持 | 当前 cookie 为会话 cookie（无 `Max-Age`），手机端是否需要"记住登录" |
| 5 | 注册中心细节 | `server_id` 生成与去重、心跳/TTL、注册凭据、目录查询接口、中继模式（帧格式 / 多路复用 / 半死连接 / 踢旧）、部署位置（本期不定） |
| 6 | 身份断言 | 签名算法（HMAC / mTLS）、防重放窗口、server 首次见到账号时是否自动建用户、与本地 `auth.db` 密码登录的共存规则 |
| 7 | 多设备选择 | 设备列表展示字段（名称 / 平台 / 版本 / 在线）、默认选哪台、切换入口位置 |

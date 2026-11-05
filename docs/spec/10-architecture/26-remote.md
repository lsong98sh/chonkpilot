# 26 · Remote 远程接入（Remote 插件 + Remote Server + H5）

> 状态：⚠️ **部分实现**（2026-11-04 核对：Server / H5 / 插件三处工程、路由与会话/停止/文件/inbox 均存在，Server 与插件有 `*_test.go`；但 **capability 协商未实现**、**反代下 Cookie 不置 Secure**、**工作区关闭不自动回列表**、**H5 E2E 无仓内可复现资产** —— 2026-11-01 只能视为**手工真机联调记录**，不是自动化 E2E 通过）
> 取代：原 `26-h5-app.md`（H5 初稿）。命名口径统一为 **Remote**（不再使用 Bridge / registry）。
> 关联：[19-单进程形态与实例隔离](19-单进程形态与实例隔离.md) · [22-实例与会话标识](22-实例与会话标识.md) · [28-plugins](../20-modules/28-plugins.md) · [2C-UI插件](../20-modules/2C-UI插件.md) · [61-消息一览](../60-reference/61-消息一览.md) · [33-对话](../30-function-points/33-对话.md) · [34-任务](../30-function-points/34-任务.md)

---

## 1. 问题与目标

- **问题**：桌面端只能在 PC 前操作；希望在手机 / 平板上继续 PC 上的对话，并能新建聊天。
- **目标（可验收）**：
  1. 手机 H5 用账号登录 Remote Server，选中一台在线设备后进入**会话列表**：可**继续某个会话**，也可**新建聊天**，并支持搜索 / 筛选。
  2. 聊天页实时显示桌面对话与任务；**发送一律进 inbox**：空闲立即起新一轮，运行中排队（本轮结束后自动发送）。**两端共享同一 inbox（2026-11-01）**：H5 可见全量 pending 项（不按来源过滤），并可对任意来源的条目做 **插话 / 取消插话 / 删除 / 撤回编辑**（撤回 = 删除 + 内容回填输入框）。
  3. 桌面端由 **Remote 插件**主动出站连接 Remote Server（PC 不暴露公网端口）。
  4. **消息面变化**（[61-消息一览](../60-reference/61-消息一览.md)）：`llm-inbox-add` 增加可选 `source` / `insert`；`llm-inbox-promote` 支持 `ids[]`；新增 `llm-inbox-revert`；新增事件 `session-inbox-changed`（经插件转 `inbox.changed` 推给 H5）。
- **非目标**（本期）：文件写入、文件上传、配置 / 场景 / 知识库编辑、多用户协同、原生壳（鸿蒙 / 安卓 / Mac）。

## 2. 术语（回答「设备 / 工作区是什么」）

| 概念 | 含义 | 是否必须让用户选 |
|---|---|---|
| 设备（device） | 开着 ChonkPilot 的一台电脑（一个 Remote 插件连接） | **默认自动选**（最近在线的一台）；多台时在会话列表顶部提供切换 |
| 工作区（workspace） | 该电脑上**已打开的项目窗口**（一个 `work_dir` 对应一个顶层 instance） | **默认自动选**（上次上下文，否则第一个；无「最近使用」排序）；多项目时同样在顶部切换 |
| 会话（session/dialog） | 工作区里的一条对话 | 用户主动选：列表点选继续，或「＋ 新建聊天」 |

设计原则：**用户第一眼看到的是「会话列表」，不是设备/工作区选择页**。设备与工作区作为「当前上下文」显示在顶部，只有存在多个候选时才出现切换入口。

> **2026-11-04 核对**：设备默认选「最近在线」✅（Server `last_seen DESC`）；工作区**不是「最近使用」** ⚠️ —— H5 取 `me.context.workspace_id`，否则 `workspaces[0]`，无最近使用记录/排序（`SessionListView.vue`）。

## 3. 拓扑与职责

```
手机 H5 ──HTTPS(REST+SSE)──► Remote Server ◄──WSS 出站长连── Remote 插件（桌面 exe 进程）
                               │  账号 / 认证 / 设备目录 / 在线状态 / 中转 / H5 静态托管
                               └── 不解析业务，不存会话
```

**工程落点（不新建仓库）**：全部放在 `chonkpilot-plugins/remote/` 下，分两个工程 + 一份 H5：

```
chonkpilot-plugins/remote/
  client/     # Remote 插件（Go module，产物 chonkpilot-plugin-remote.exe）+ webui/
  server/     # Remote Server（Go module，产物 chonkpilot-remote-server.exe）
  server/web/ # H5（Vue + vite，构建产物 dist 由 server go:embed）
```

> 2026-11-04 核对：仓内只有 `build.ps1 / client / dist / server`，**`doc/` 目录不存在**（原树中的 `doc/` 已删除）。

**两处 Web UI 的归属（不要混）**：

| Web UI | 归属 | 形态 |
|---|---|---|
| 插件配置页 / 状态页（`client/webui/*.html`） | **嵌进插件 exe** | Go `//go:embed all:webui`；manifest `ui.selfServe: true`，宿主经 UDS `plugin/v1/ui.asset.get` 取字节；产物目录只有 `plugin.json + exe`，**不带 webui/** |
| 手机 H5（`server/web/`） | **嵌进 Server exe** | Vue 构建产物 `dist` 由 `//go:embed` 打进 `chonkpilot-remote-server.exe`，由 Server 直接托管；与插件无关 |

> 插件 webui 另有 `ui.assets.mode=dir`（宿主直读插件目录，供 Python/Node 等无法 embed 的插件）与 `resource`（PE 资源段）两种模式；本工程两个 Go 进程统一用 `bytes`（embed）。

| 组件 | 职责 | 明确不做 |
|---|---|---|
| **Remote 插件 client**（id=`remote`） | 配置页填「服务器地址 + 用户名 + 密码」；出站连接 + 断线重连；上报设备与工作区；按白名单把 Server 的调用转发为 `plugin/v1/*`；订阅桌面事件推回 Server；状态栏显示连接状态 | 不存账号、不解析聊天业务、不落库 |
| **Remote Server**（`server/`，产物 `chonkpilot-remote-server.exe`） | 用户 CRUD + 认证；设备绑定与目录；在线状态（心跳 TTL）；WSS 隧道 + H5 下行中转；H5 静态资源托管 | 不存会话 / 消息 / 任务；不调 LLM；不读桌面数据库 |
| **H5**（`server/web/`，随 Server embed） | 登录 → 会话列表（继续 / 新建 / 搜索 / 筛选）→ 聊天（发送 / 排队中 / 停止 / ask-user / 任务树只读 / **浮动 inbox 图标 + 全量列表页：插话·取消插话·删除·撤回编辑**） | 不做桌面布局、多窗口、配置编辑 |

> 插件 **零框架改造**：现有 `plugin/v1/session.start|send|cancel|list|get|history|content|active`、**`plugin/v1/session.inbox.add|list`**、`plugin/v1/ask.reply`、`plugin/v1/task.tree|tasks|task.stop`、`plugin/v1/file.list|read`、`plugin/v1/instance.resolve` 已覆盖全部所需能力（[capabilities.go](../../../../chonkpilot-framework/plugin/src/bridge/capabilities.go)）。

## 4. 协议

### 4.1 传输与端口（TCP，统一 443）

| 项 | 结论 |
|---|---|
| 传输层 | **TCP**（HTTPS + WSS + SSE）。**不用 UDP**：聊天要求有序、可靠、可断线重连，TCP 天然满足；UDP 等于自己重造重传/排序/拥塞控制 |
| 公网端口 | **只开 443**：H5 页面、REST、SSE、设备隧道同端口同源（`wss://host/tunnel`） |
| Server 监听 | 缺省 `127.0.0.1:8080`（由 nginx/Caddy 终结 TLS）；也可 `-addr :8443 -tls-cert … -tls-key …` 自持证书 |
| 桌面入站 | **不开放任何端口**。插件**主动出站**连 `wss://host/tunnel`（NAT / 企业防火墙友好） |
| 端口可配 | 插件配置填完整地址（如 `wss://host:8443/tunnel`），非 443 也能用 |

### 4.2 插件 ↔ Server（WSS `/tunnel`）

插件**发起**连接；Server 不主动连 PC。JSON 帧：

| 帧 | 方向 | 载荷 |
|---|---|---|
| `hello` | 插件 → Server | `{username?, password?, token?, device:{id,name,platform,version}, client_version, protocol, workspaces:[{id,name,path,instance_id}]}`。首连带账号密码；重连带 `token`；`protocol` 用于能力协商（§5.1；**当前仅透传存储，协商未实现** ⚠️） |
| `hello-ok` | Server → 插件 | `{device_id, token, server_time}`（`token` 供重连；服务端只存哈希） |
| `workspaces` | 插件 → Server | `{workspaces:[…]}`（实例增删 / 目录变化时全量覆盖） |
| `call` | Server → 插件 | `{id, workspace_id, method, args}`（`method` = 设备 RPC，见 §4.4） |
| `result` | 插件 → Server | `{id, ok, result?, error?}` |
| `event` | 插件 → Server | `{workspace_id, type, data}`（**产品事件**，插件已把 MQ 主题归一，见 §4.4） |
| `ping` / `pong` | 双向 | 心跳（20s；60s 无响应判离线） |

- **同设备重复连接**：新连接顶替旧连接（旧连接关闭），不产生第二条设备记录。
- `device.id` 由插件生成并持久化在插件数据目录（同机同插件恒定，重启不变）。
- Server 只按 `device_id + workspace_id` 路由与过滤，**不解析 `type` / `data`**。

### 4.3 H5 ↔ Server（普通 REST + SSE，不涉及 MQ）

H5 是普通 Vue 应用，只认下面这组端点；**不出现 topic / payload / bus 概念**。

| 端点 | 说明 |
|---|---|
| `POST /api/login` `{username,password}` | 成功下发 HttpOnly Cookie（`rk_sess`，7 天） |
| `POST /api/logout` | 注销 |
| `GET /api/me` | 当前用户 + 上次上下文 |
| `GET /api/devices` | `[{device_id,name,platform,version,online,last_seen,protocol,workspaces:[{id,name,path}]}]` |
| `POST /api/context` `{device_id, workspace_id}` | 绑定本会话的当前设备 / 工作区 |
| `GET /api/sessions` | 会话列表（搜索 / 筛选由 H5 本地做） |
| `POST /api/sessions` `{text?}` | **新建聊天**（可选首条消息）→ `{session_id, turn_id}` |
| `GET /api/sessions/{id}/messages?before=&limit=` | 历史消息分页（`before` = 上一页最早一条的 **turn_id**；`limit` = 页大小，映射宿主 `target_messages`） |
| `POST /api/sessions/{id}/messages` `{text, insert?}` | 继续会话 / 发送（**进 inbox**；`insert=true` = 插话意向；运行中排队，返回 `{accepted, queued, type}`） |
| `GET /api/sessions/{id}/inbox` | 该会话**全量** pending 项（两端共享，不按来源过滤） |
| `POST /api/sessions/{id}/inbox/{action}` `{id}` | inbox 项操作：`promote`（插话）/ `revert`（取消插话）/ `discard`（删除·撤回） |
| `POST /api/sessions/{id}/stop` | 停止当前轮 |
| `POST /api/sessions/{id}/answer` `{ask_id, answer, custom?}` | ask-user 作答 |
| `GET /api/sessions/{id}/tasks` | 任务树（只读） |
| `POST /api/tasks/{id}/stop` | 停止任务 |
| `GET /api/files?path=` / `GET /api/files/content?path=` | 文件浏览 / 预览（只读） |
| `GET /api/events` | SSE：只推本会话绑定 `device_id + workspace_id` 的产品事件 |
| `GET /api/healthz` | 存活探针 |
| `GET /` | H5 静态资源（`Cache-Control: no-cache, no-store, must-revalidate`） |
| `GET /admin` | **后台管理页**（独立单文件 HTML，随 exe 嵌入，非 H5 应用）：管理员登录后可维护账号、查看设备接入与 workdir |
| `GET /api/admin/users` · `POST /api/admin/users` · `PATCH /api/admin/users/{name}/password` · `POST /api/admin/users/{name}/status` · `DELETE /api/admin/users/{name}` | 账号维护（**需 `role=admin`**）：列表 / 新建（重名 409）/ 改密 / 启用·禁用 / 删除（级联删设备与登录会话）。护栏：**禁止禁用或删除自己**、**禁止删除最后一个未禁用管理员** |
| `GET /api/admin/devices` | 全部用户的设备接入：设备名 / 平台版本 / 在线 / 最后在线 / **workspaces（workdir 路径）** |

管理员：`remote-server user add|list|passwd|delete`（CLI）+ `/api/admin/users`（仅 `role=admin`）。**首启无账号时自动创建默认管理员 `admin` / `admin`（日志告警，须立即改密）**；也可用环境变量 `CK_REMOTE_ADMIN_USER` / `CK_REMOTE_ADMIN_PASSWORD` 指定首个管理员。

### 4.4 设备 RPC 与事件映射（插件侧适配，Server 不解析）

**RPC 白名单**（Server 只按名字转发；插件负责映射到 `plugin/v1/*` 并归一返回形状）：

| 设备 RPC | 插件内部动作 | 稳定返回 |
|---|---|---|
| `sessions.list` | `plugin/v1/session.list` | `[{session_id,dialog_id,title,updated_at,running}]` |
| `sessions.history` | `plugin/v1/session.history` | `{messages:[{id,role,text,turn_id,created_at}]}` |
| `sessions.create` | `session.start` + `session.send(text-user)` | `{session_id,turn_id}` |
| `sessions.send` | **一律 `plugin/v1/session.inbox.add`（`source=mobile`）**：空闲 → inbox 立即消费起新一轮；运行中 → `next_turn` 排队（本轮结束后自动发送）；`insert=true` → 登记插话意向（可插入相位批量插入）。**不拒绝** | `{accepted, queued, session_id, type, inbox_id?, error?}` |
| `sessions.inbox` | `plugin/v1/session.inbox.list{status:pending}`（**全量**，含 `type` / `source`） | `{items:[{id, content, type, source?, created_at}]}` |
| `sessions.inbox.promote` / `.revert` / `.discard` | 对应 `plugin/v1/session.inbox.promote` / `.revert` / `.discard`（插话 / 取消插话 / 删除·撤回） | `{ok, type?, error?}` |
| `sessions.stop` | `plugin/v1/session.cancel` | `{cancelled}` |
| `ask.answer` | `plugin/v1/ask.reply` | `{ok}` |
| `tasks.tree` / `tasks.tasks` / `tasks.stop` | `plugin/v1/task.*` | 任务树 / 任务列表 / `{cancelled}` |
| `files.list` / `files.read` | `plugin/v1/file.list\|read` | `{entries}` / `{content}` |

**事件映射**（插件订阅 61 相对主题 → 归一为产品事件；H5 只认右列）：

| MQ 相对主题 | 产品事件 `type` | `data` |
|---|---|---|
| `session-receive`（type=text） | `message.delta` | `{session_id,turn_id,text}` |
| `session-receive`（type=reason） | `message.reasoning` | `{session_id,turn_id,text}` |
| `session-receive`（type=tool-call） | `message.tool` | `{session_id,turn_id,name,status}` |
| `session-turn-start` | `turn.start` | `{session_id,turn_id}` |
| `session-complete` | `turn.complete` | `{session_id,turn_id,status,finish_reason,message?}` |
| `session-ask` | `ask` | `{ask_id,session_id,turn_id,question,options?,multi?,custom?}` |
| `task-started` / `-updated` / `-done` | `task.update` | `{task_id,session_id,turn_id,state,tool,kind}` |
| `tool-notify` | `notice` | `{message,plugin?,kind?}` |
| `session-inbox-changed` | `inbox.changed` | `{session_id,action,ids?}`（2026-11-04 核对补登） |

> **适配层放在插件**：桌面版本差异（字段增删、能力有无）由各版本的插件吸收；Server 与 H5 保持稳定契约，不随桌面版本改。


### 4.5 认证与绑定

| 场景 | 身份来源 |
|---|---|
| H5 | Remote Server 账号（唯一账号源），Cookie 会话 |
| 插件 | 同一账号的用户名+密码首次登录 → 绑定 `device_id` → 下发 `token`（哈希存储）；重连带 token |
| 插件 token 失效 | Server 回 `{ok:false,error:"unauthorized"}` → 插件用配置里的用户名密码重连；仍失败则状态栏报错，不无限重试 |

- 密码 `bcrypt` 存储；日志与错误信息**不回显密码**。
- H5 只能看到**自己账号**的设备与事件；越权设备返回 403。

## 5. H5 信息架构（按最新需求收敛）

### 5.1 托管位置（决策：Remote Server 托管，Desktop 不托管）

| 方案 | 首屏路径 | 优点 | 缺点 |
|---|---|---|---|
| **A. Remote Server 托管**（选定） | 手机 → Server（静态直出；业务消息 → 隧道 → 桌面，**1 跳**） | 单份前端、一次发布；PC 离线也能打开登录页 / 设备列表；首屏最快；桌面各版本无需打包 H5 | H5 与桌面存在**契约版本差**，需协商 |
| B. Desktop 托管 | 手机 → Server → 隧道 → 桌面（静态也要过隧道，**2 跳**） | H5 与桌面版本天然同源、零版本差 | PC 离线时页面都打不开；每台机器各一份前端、升级不同步；隧道传静态资源慢 |
| C. 嵌入原生 APP | 壳内置资源 + 调 Server | 体验最好 | 本期不做（P2）；仍需 Server 提供数据面 |

**结论：A**。理由按优先级：

1. **可用性**：远程入口必须在 PC 离线时也能打开（登录、看设备在线状态），B 做不到。
2. **路径最短**：静态资源由 Server 直出，只有业务消息走隧道；B 连 HTML/JS 都要经隧道，移动网络下首屏明显更慢。
3. **维护成本**：一份 H5、一次发布；桌面端不需要因 H5 改动重新打包。
4. **版本差异可控**：H5 只耦合**产品 REST/SSE 契约**；桌面的 61 消息契约由插件吸收。

**版本差异处理（A 的必要配套）**：

- 插件 `hello` 上报 `client_version` 与 `protocol`（如 `remote/1`）；Server 存下并随 `/api/devices` 返回。✅（仅存储/透传）
- H5 按 `protocol` 做**能力协商**：不支持的入口隐藏（如旧桌面没有 `tasks.tree` 就不显示任务入口），不报错刷屏。🔵 **未实现（2026-11-04 核对：H5 仅把 `protocol` 存进设备对象，不据其隐藏入口）**
- Server 对未知 RPC 返回 `method_not_allowed`；对 `protocol` 低于最低支持的设备返回 `unsupported_protocol`，H5 提示「请升级桌面端」。🔵 **未实现（2026-11-04 核对：全仓无 `unsupported_protocol`；Server 只原样透传 `protocol`，无最低版本校验）**
- 契约变更只增不改：新增 RPC / 事件对旧插件天然不可用（H5 按能力隐藏）。🔵 待协商实现后生效。

> 原生壳（P2）只包一个 URL（Server 的 H5），不另做一套前端。

### 5.2 页面结构

```
登录
 └─ 会话列表（首页）
      ├─ 顶部：当前设备 · 当前工作区（仅多候选时可点开切换抽屉）
      ├─ 搜索框 + 筛选：全部 / 运行中 / 今天
      ├─ 列表项：标题 · 最后消息摘要 · 时间 · 运行中标记
      ├─ 点会话 → 聊天页（继续该会话）
      └─ ＋新建聊天 → 聊天页（新会话）
聊天页
 ├─ 历史消息（首屏 + 上滚分页）
 ├─ 输入框：**始终可输入**；空闲发送 = 立即起轮，运行中发送 = 排队（`已排队 N 条 · 本轮结束后自动发送`）
 ├─ 运行中：输入框保留 + 「停止」（紧凑按钮，不再整块替换输入区）
 ├─ ask-user 作答（单问单答）
 ├─ 任务：任务树 + 点节点看详情（状态 / 工具 / 类型 / 轮次 / 参数）
 ├─ 文件：只读浏览（目录进出）+ 文本预览（截断提示）
 └─ 返回会话列表
```

- **默认自动选**「最近在线设备 + 上次上下文工作区（否则第一个）」；切换只在多设备 / 多工作区时出现。
- **新建聊天**：`POST /api/sessions`（可带首条 `text`）→ 插件分配 `session` / `turn` 并起轮；H5 拿到 `{session_id, turn_id}` 进入聊天页。
- **继续会话**：`GET /api/sessions` 列表项带 `session_id` + `dialog_id` → `GET /api/sessions/{id}/messages` 拉历史。
- **筛选 / 搜索**：Server 只返回列表，过滤在 H5 本地做（不新增接口）。
- **忙碌判定 / 排队**：`POST /api/sessions/{id}/messages` **不会因忙碌拒绝**——插件恒走 `llm-inbox-add{source:"mobile"}`，返回 `{accepted:true, queued}`（`queued=true` = 已排队，本轮结束后自动发送）。收到 `turn.start` → 置忙；`turn.complete` → 置闲；重连以历史 + `GET /api/sessions/{id}/inbox` 恢复「排队中」条（桌面权威）。
- **运行态同步**：列表页订阅 `turn.start` / `turn.complete`，300ms 去抖刷新会话列表（运行中标记与时间）；从聊天页返回列表也以最新 `sessions.list` 为准。打开运行中会话时聊天页以列表带来的 `running` 初始化（直接显示「停止」），并在 5s 内有界复查一次，避免状态过期锁死输入区。
- **内容一致性**：H5 只消费助手侧事件；其他端（桌面）在同一会话发出的用户消息在**轮末**由历史补齐（按 `turn_id` 拉取并按文本去重），保证 H5 与桌面读到同一份会话内容。

### 5.3 事件订阅（H5 只消费产品事件）

H5 消费 `message.delta` · `message.reasoning` · `message.tool` · `turn.start` · `turn.complete` · `ask` · `task.update` · `notice` · `inbox.changed`。

> 插件按相对主题订阅 61 事件并**归一**为上述产品事件（§4.4）；Server 只按「设备 + 工作区」过滤转发，不改 `type` / `data`。

## 6. Remote Server 数据模型（仅账号侧）

| 表 | 字段 |
|---|---|
| `users` | `id` · `username`（唯一）· `password_hash` · `role`（admin\|user）· `disabled` · `created_at` |
| `devices` | `id`（主键）· `user_id` · `name` · `platform` · `version` · `token_hash` · `last_seen` · `created_at` |
| `h5_sessions` | `token_hash`（Cookie 值哈希，主键；2026-11-04 核对，原 `id` 列名已订正） · `user_id` · `device_id?` · `workspace_id?` · `expires_at` |
| `device_workspaces` | **不落库**，随隧道连接实时上报，断线即清空 |

存储 = **SQLite 单文件**：`<exe 所在目录>\chonkpilot-remote-server.db`（与可执行文件同名；纯 Go 驱动 `modernc.org/sqlite`，无 CGO、无需安装 SQLite）。**无 `-data` 启动参数**，CLI（`user …`）与 `serve` 使用同一路径。

## 7. 边界与异常

| 场景 | 行为 |
|---|---|
| 插件未连接 / 设备离线 | H5 设备列表标离线；`/api/call` 返回 `503 device_offline`；界面提示「电脑未连接」 |
| 工作区已关闭（PC 关窗） | 插件 `workspaces` 更新；H5 提示「该工作区未在电脑上打开」，**应**回会话列表。⚠️ **2026-11-04 核对：当前仅通用错误 toast，不自动回列表**（无 Server→H5 的主动工作区关闭事件，只能等下一次 RPC 失败） |
| PC 在跑、H5 尝试发送 | 入 inbox 排队（`queued:true`，`next_turn`）；H5 显示「已排队 N 条 · 本轮结束后自动发送」，**不插话、不打断**；本轮结束自动起新一轮 |
| H5 断网 / 切后台 | `EventSource` 自动重连；重连后重拉 `session.list` + `session.history` 对齐 |
| 同一账号多台手机 | 各自独立绑定；同一事件广播给所有绑定该设备/工作区的 H5 会话 |
| 插件 token 失效 | 用用户名密码重连一次；仍失败 → 状态栏红色 + 配置页提示 |
| 未授权设备 / 工作区 | 403，不静默降级 |
| H5 会话过期 | 401 → 回登录页 |
| 慢客户端 | SSE 有界队列满则丢帧并记日志；客户端重连后以快照对齐（不重放） |

## 8. 重启恢复

1. **状态在哪**：账号 / 设备 / H5 会话在 Server 库（持久）；隧道与工作区列表在 Server 内存（断线即失效）；插件设备 id 在插件数据目录（持久）；会话 / 消息 / 任务树在桌面 prjusr 库（既有）。
2. **输入受理即持久化**：H5 的 `session.start` 经桌面 `EnsureSession` / `EnsureTurn` 落库；Server 不缓存待发消息。
3. **非终态收口**：桌面运行轮按既有生命周期收口（`data-session-cleanup-stale` / `session-complete`）；H5 不自行推断成功。
4. **重启后重建**：Server 重启 → 设备离线，插件自动重连并重报工作区；H5 Cookie 仍在，重新拉设备/会话即可；插件重启 → 重连并重报工作区。
5. **副作用语义**：`session.start` / `session.send` 以 `turn` 幂等（重复 start 走既有 busy 拒绝）；`session.cancel` 幂等；Server 不做重试补发。

## 9. 安全

- 插件只**出站**连接；PC 不开放任何入站端口（NAT 友好）。
- 生产部署必须 TLS（Server 侧反代或内置证书），H5 Cookie 置 `Secure`。⚠️ **2026-11-04 核对：实现为 `Secure: r.TLS != nil`（`server/httpapi.go`），nginx/Caddy 终结 TLS 时 `r.TLS == nil` → 不会置 Secure；需识别 `X-Forwarded-Proto` 或显式配置（🔵 待修）**。
- Server 侧仅中继白名单方法；`args` 不解析、不落日志正文（只记方法名与耗时）。
- 事件转发按「账号 → 设备 → 工作区」三级归属过滤，禁止跨账号。
- 登录失败限频（同 IP + 用户名，5 次 / 分钟）。

## 10. 落地清单

| 阶段 | 内容 | 落点 | 状态 |
|---|---|---|---|
| S1 | Server：账号 CRUD / 认证 / 设备绑定 / 隧道 / SSE / H5 静态托管 | `chonkpilot-plugins/remote/server` | ✅ 已实现（单测全绿 + 联调） |
| S2 | 插件：配置页 / 出站连接 / 工作区上报 / 方法转发 / 事件回推 / 状态栏 | `chonkpilot-plugins/remote/client` | ✅ 已实现（单测 + **桌面真机联调通过**） |
| S3 | H5：登录 / 会话列表（搜索·筛选·新建）/ 聊天 / 任务树 / 文件 | `chonkpilot-plugins/remote/server/web` | ✅ 已实现（真机**手工**联调；无仓内自动化 E2E 资产） |
| S4 | 构建脚本 + manifest + **`plugin.json`** 登记（2026-11-04 核对，原 `plugin.ini` 不存在） | `build-plugins.ps1`、`src/initdata/capability/plugins/remote/`、`chonkpilot-plugins/remote/build.ps1` | ✅ 已实现 |
| S5 | 测试：Server 单测、插件 L1、H5 端到端 | 各仓 `*_test.go` / `src/test` | ⚠️ **Server / 插件单测存在**（本次未复跑）；**H5 端到端无仓内脚本/记录**（无 Playwright/E2E 资产），仅 §10.1 手工记录 |

### 10.1 联调记录（2026-11-01，单机；**手工记录，非可复现 E2E**）

```powershell
# 构建
cd E:\BizWorks\chonkpilot-plugins\remote
.\build.ps1 -WithFake

# 首启自动创建默认管理员 admin / admin（数据库落 exe 同目录 chonkpilot-remote-server.db）
.\dist\chonkpilot-remote-server.exe serve -addr 127.0.0.1:18080
# 可选：用环境变量指定首个管理员；或用 CLI 追加账号
.\dist\chonkpilot-remote-server.exe user add -u demo -p demo123 -role admin
.\dist\fakedevice.exe -server ws://127.0.0.1:18080/tunnel -user admin -pass admin -device fake-pc
```

- `go test ./...`：Server ✅ / 插件 ✅。
- REST 全链路（login → devices → context → sessions → create → messages → stop）✅。
- 手机视口（390×844，无头 Chrome）E2E：登录 → 会话列表 → **切换工作区** → 继续会话（历史）→ **任务树 + 任务详情** → **文件浏览 + 预览** → **ask 作答** → **运行中发送进入队列（排队条 + 轮末自动发送）** → 运行中停止 → 新建聊天（SSE 流式回复）→ 搜索 → 退出/重登 ✅，仅登录前后 `/api/me` 401 为预期。
- 桌面真机联调（2026-11-01 08:20）：`build-desktop.ps1 -Debug` → 插件配置页保存 `ws://127.0.0.1:18080/tunnel` + 账号 → 真机设备上线（工作区 `E:\TEMP\ck-remote-ws`）→ H5 登录 → 会话列表 4 条 → 历史含 mock LLM 回复 → 新建聊天收到回复 → 文件浏览 + `README.md` 预览 ✅。
- 真机联调中发现并修复：① `hello-ok.server_time` 为 int64（原插件按 string 解析导致握手无限等 10s 超时）；② 插件配置改落**插件数据目录 `config.json`**（宿主 usr 配置在测试实例下未持久化，且未配置时插件被标 degraded 导致配置页 404）；③ 插件桥 `data-*` 读类方法改走 `EmitData`（persist 为「请求 + 同主题应答」异步面，原 `forwardMQ` 恒空）；④ 宿主「一树一库」要求 `dialog_id`：插件从 `session.list` 反查并回填 history/send/cancel；⑤ H5 兼容服务端 `{list}` 信封，并在默认选择时跳过离线设备。

## 11. 未决项

| # | 事项 | 待定 |
|---|---|---|
| 1 | 部署形态 | 公网 VPS + 域名 TLS，还是内网自建 |
| 2 | 账号来源 | Remote Server 独立账号（本期）还是后续对接组织账号 |
| 3 | 管理页 | 本期 CLI + admin API，是否要在 H5 里加用户管理界面 |
| 4 | 多设备默认 | 默认选「最近在线」还是「上次使用」 |

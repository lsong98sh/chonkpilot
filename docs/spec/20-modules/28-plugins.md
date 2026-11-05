# 28 · 插件体系（主仓零业务插件）

> 状态：⚠️ 部分与代码一致（2026-11-04 核对：主体架构真实落地；插件清单/数量、codegraph·vfts 插件侧编排、若干路径与测试引用已修正，见各节标注）
> **〔订正（2026-11-03，SANDBOX-WRITE-1）〕插件写隔离**：gateway 在每次 `tools/call` 的 `_meta.chonkpilot.sandbox = {enabled, writable_dirs}` 下发可写白名单（`origin=builtin` 的插件均可见；sdk `CallContext.Sandbox`）。消费方：`file_*` 7 个写工具（越界 = 整次调用拒绝）、`task_run`（`chonkpilot-util/scriptrun` 的 `Sandbox{Enabled,Writable}` → Low IL + Job）、`web_download`（最终落盘路径）。未启用 = 不校验（旧行为）。**crontab 已接入沙箱**（2026-11-03 P2，见 §3.6）。**（2026-11-04 核对）**
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [21-llm-server](21-llm-server.md) · [29-codegraph](29-codegraph.md)
> 代码目录：**主仓零业务插件（2026-10-24 PLUGIN-SPLIT-1；2026-10-29 TOPO-1 定稿目录）**。全部插件源码在同级仓 **`chonkpilot-plugins/<id>/{src,doc}`**（**11 个**：codegraph / browser / desktop / file / task / crontab / mcp / web / memory / history / remote；嵌套 module = `browser/src/engine`、`desktop/src/engine`、`codegraph/src/engines/{treesitter,vfts}`、`{file,history,memory}/src/lib`、`remote/{client,server}`）；SDK / 宿主运行时 = 同级仓 **`chonkpilot-framework/plugin/src`**（module path 仍 `github.com/lsong98sh/chonkpilot-plugin-framework`，含 `sdk/ protocol/ runtime/ contract/ bridge/ mgr/`）；`file` 域契约 = `chonkpilot-plugins/file/src/tools/file/*.tool.md`（11 个）；引擎源码 = `chonkpilot-plugins/codegraph/src/engines/{treesitter,vfts}/`。平台基座（mq / paths / uds / 窗口层 / 单例）= `chonkpilot-framework/host/src`。主仓 `src/plugins/`、`src/lib/plugin-sdk`、`src/lib/pluginhost/`、`src/lib/core/` 均已不存在。**（2026-11-04 核对）`chonkpilot-plugins/shared/` 不存在**（共享实现已上收 `chonkpilot-util`：`ignore` / `encodingx` / `sandbox` / `scriptrun` 等）。
> **正文内的历史路径**（如 `src/lib/pluginhost/bridge/...`、`src/lib/gui/...`、`src/lib/core/...`）按此口径映射：`pluginhost/* → framework/plugin/src/*`、`gui/{窗口层,models,messages,fileserver,assetserver,folder,themefs} → framework/host/src/*`、`core/* → framework/host/src/core/*`；`gui/{host,bridge,mqpolicy}` 仍在主仓。

---

## 1. 职责与边界

- **一句话**：**唯一形态 = 进程外 exe 插件**（`pluginsdk` + 插件桥 `plugin/v1`；codegraph / browser / desktop / file / task / crontab / mcp / web / memory / history / remote）。压缩**已不是插件**：内建在会话侧（`chonkpilot-agent/session` 压缩内核）+ `llm/server` 编排，详见 §3.1。原进程内 lib Hook 兼容形态（codegraph / vfts）**已于 2026-10-27 删除**，`appwire.DefaultPlugins` 不再装配任何内嵌插件。**（2026-11-04 核对）**
- **做**：事件订阅、按 instance/workdir 自持数据（经数据面消息）、向 gateway 注册/注销工具；进程外插件另含进程生命周期（拉起 / 健康 / 退避 / 停止）。
- **不做**：插件不直接依赖宿主 Go 包（进程外走插件桥 / MCP；进程内 Hook 走 MQ 方法面）。
- **依赖铁律（单向，2026-10-27 定稿）**：**主仓 → 插件 = 零依赖**。主仓只可依赖**插件框架契约**（`plugin-*` 管理面、视图注册、MCP 注册、`plugin/v1/*` 能力面）；**不得**读任何具体插件的业务键、业务文件或业务 UI（`codegraph.status` / `vfts.status` 这类运行态键，以及宿主内置的索引进度徽标 / 详情页均属违规残留，搬移见 [49 PLUGIN-UI-2](../40-roadmap/49-待实现项.md#1-引用迁移与历史索引2026-11-04)）。插件 → 主仓、插件 → 其它插件：允许。

### 1.1 插件调工具 = 宿主准入三条件（2026-10-28 定案，[49 §2.19](../40-roadmap/49-待实现项.md#1-引用迁移与历史索引2026-11-04)）

插件经桥方法 `plugin/v1/tools.call` → 宿主 → `mcp-tools-call` 调工具时，`work_dir` 由**插件自报**（gateway 的沙箱根 / 池键按它判定）。桥入口在 **emit 之前**逐条校验，**任一不满足 → 只回桥错误（错误文本含契约码）、不发 `mcp-tools-call`**：

| # | 判据 | 拒绝码 |
|---|---|---|
| 1 | `work_dir` 归一（`Abs` + `Clean` + `EvalSymlinks`；比较键小写）后 `<work_dir>/.chonkpilot` 存在且是目录 —— **只认它自己**，不沿父目录蒙混；缺 `work_dir` / 不可解析同样拒绝 | `not_a_chonkpilot_project` |
| 2 | `name` 必须在该 workdir 的**已装配工具面**内（= gateway `tools/list` 按 instance 过滤后的结果，取该 workdir 各活动实例的**并集**；装配面即配置面 —— `servers.list` + 插件启停 + 内置工具集，**不新增**项目级工具白名单配置项；宿主侧按 instance 短 TTL 缓存，不每次全量扫） | `tool_not_allowed` |
| 3 | 宿主侧存在该 `work_dir` 的**活动实例**（任一窗口即可，**不要求**顶层主窗口） | `work_dir_not_open` |

- **审计**：拒绝时 `gui.log` 记一行，字段顺序固定 —— `plugin=<id> tool=<name> work_dir=… reason=<code>`。
- **放行路径零变化**：载荷原样转发、`request_id` 语义不变（沿用 `commandMethods`：带 `request_id` 才等结果）、返回结构不变。
- **消息面零变更**：拒绝走桥应答，不产生任何 MQ 消息（[61](../60-reference/61-消息一览.md) 未改）。
- 实现 = `src/lib/pluginhost/bridge/toolgate.go`（L1 = `toolgate_test.go`）。
- **活动实例视图唯一来源 = framework `instance.Manager`（2026-10-29 内存单源化）**：判据 2 / 判据 3、`MainInstanceForWorkDir`（`plugin/v1/instance.resolve`）以及晚连接插件的 `instance-register` 补发，一律查宿主注入的**同一份**视图（`bridge.Options.Instances`，生产 = `llm/server` 的 `s.im`，`InstanceManager()` 注入）；**bridge 不再自持实例登记副本**（原 `instRegs` / `instOrder` / `cacheInstanceEvent` 已删除），故实例退出后**立即**不再命中。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 形态 | **进程外 exe 插件（唯一形态）**：`plugin-framework/sdk` 连插件桥（UDS/TCP），工具经 MCP（streamable HTTP）暴露，实例由宿主 `host/mgr` 按 manifest 拉起；**原进程内 lib Hook 已删除**（`plugin-codegraph` / `plugin-vfts` 不存在） |
| 接口 | `Hook{ Name() string; Start(Deps) error }`；`Deps{ Bus mq.Bus; Logf func(format, ...) }`（另含可选 `Notify func(Notice)`：失败上报，nil = 静默）；**可选** `Stopper{ Stop() }` —— 持可释放资源（后台 goroutine / 引擎子进程 / 订阅 / 实例视图）的插件实现 `Stop()`，宿主退出时按 `Start` **逆序**调用（语义见下） |
| 启动 | **唯一入口 = 框架 `host/mgr`**（2026-10-25 定稿；**2026-11-02 调整**：取消 `plugin.ini`，改为**直接扫描插件根** `<root>/<id>/plugin.json`）：插件根与 `capability` **同级**（优先级从高到低 = `<prjUsr>/plugins` → `<workDir>/.chonkpilot/plugins` → `~/.chonkpilot/plugins` → `<exeDir>/plugins`），同 id 取第一个命中；启用判定 = `<数据根>/plugins/enabled.json`（无该键时回落极简 `plugin.json` 的 `enabled`，缺省 true）。插件一律由宿主 spawn 并守护（`external` 模式已删除）。插件启动后自己连 UDS 发 `plugin.hello` 自报名字并注册 view / 事件——**拉起后不注册的进程视为无效**。**主仓无进程内 Hook**（原 `src/plugins/plugin-codegraph` / `plugin-vfts` 已删除） |
| 数据访问 | 走 `src/lib/data`（`data.Prj(instanceID)` 或 payload 自带 work_dir/data_dir 自登记）；work_dir 解析走 `instance.Manager` |

**实例视图**（`chonkpilot-framework/plugin/src/runtime/instance/manager.go`）：订阅 `instance-register` / `instance-heartbeat` / `instance-exit`，维护 `instance_id ↔ {ID, WorkDir, DataDir, ClientType, User, ParentInstance, LastBeat}`（`LastBeat` 仅记录最近心跳/注册时间；**`User`（🆕 2026-10-09）= 实例归属 OS 用户名**，`instance-register` 必填字段，供 `instance-bring-to-top` 的跨用户判定；**`ParentInstance`（🆕 2026-10-09）= 子窗口实例的宿主顶层实例 id** —— 由 `instance-register` 的 `parent_instance` 字段解析，非空即子实例，供托盘「列举实例」过滤，见 [61 §4.1](../60-reference/61-消息一览.md)）；提供 `Lookup/List/Count` 与 `ListTopLevel`（仅顶层）。
> ⚠️ **心跳超时退出判定 = 编译开关**：心跳发布与超时判定**仅在编入 `-tags split` 时生效**；唯一形态 desktop 为空实现（`heartbeat_inprocess.go`：不发布、不判超时），实例失效以**显式 `instance-exit`** 清理（`instance-heartbeat` 订阅保留）。`-tags split` 编入时：`StartHeartbeat`（注册后起 30s 周期发布 `instance-heartbeat{instance_id}`）与 `Manager.Sweep(timeout)`（**90s = 3× 周期**的实例视同退出）的生产调用方 = 判定侧消费方两处 —— **llm-server** `startInstanceSweep`（`src/lib/llm/server/sweep_split.go`：30s ticker → `Manager.Sweep(90s)` → 逐个走既有 `exitInstance`：取消 running turn + 释放 `busy` 锁 + 注销 capability dir 节点）、**persist** `startStaleSweep`（`src/lib/data/persist/sweep_split.go`：30s 扫描 → `LastBeat` ≥90s → 既有 `instanceGone`：移除 `insts` 绑定 + `data.Unregister`）；阈值单一来源 = `src/lib/heartbeat`：`persist` 的 `staleTimeout = heartbeat.Timeout`（90s）、llm-server 的 `sweepInterval = heartbeat.Interval`（30s）、`plugin/instance` 的 `HeartbeatInterval`/`HeartbeatTimeout` 亦为同名常量别名。**（2026-11-04 核对）插件 codegraph/vfts 不提供 `sweepInstances`、不订阅 `instance-heartbeat`、插件仓无 `sweep_*.go` / `instanceGone`；实例失效只走显式 `instance-exit`**（通用心跳能力仅在框架 `runtime/instance`，插件未使用）。见 [24-lib §2/§6](24-lib.md) · [61 §4.1](../60-reference/61-消息一览.md)。

> **Stop 语义（🆕 2026-10-11；`plugin.Stopper` 可选接口）**：实现 `Stopper` 的插件在宿主退出时按 `Start` **逆序** `Stop()`，**幂等**（`stopOnce`）；进程外插件（memory / history / codegraph 等）的停止由 `host/mgr` 按 manifest `runtime.stop.grace_ms` 负责。**原进程内 Hook（codegraph / vfts）已删除**，其 Stop 顺序仅作历史留存。见 `chonkpilot-framework/plugin/src/runtime/hook` `Stopper`。

**启动清单与「拉起只属于框架」（2026-10-25 定稿）**：

1. **主仓不持有任何单插件拉起代码**。宿主**先起 server**，再由框架 `host/mgr` 扫描四个插件根（`<prjUsr>/plugins`、`<workDir>/.chonkpilot/plugins`、`~/.chonkpilot/plugins`、`<exeDir>/plugins`，与 `capability` 同级）发现 manifest 并统一 spawn。重启/退避/健康检查/优雅关停全部复用 `host/mgr` 的 manifest 语义。
2. **发现与启用**：`<root>/<id>/plugin.json` 是**极简启动信息**（`id/cmd/enabled/name/icon`；完整 manifest 由插件在 `plugin.hello` 上报）（同 id 取优先级最高的根）；启用 = `enabled.json`（设置页开关写入）＞ `plugin.json.enabled`（缺省 true）；启动命令取 `cmd`（字符串或数组，相对路径按插件目录解析；缺省 `chonkpilot-plugin-<id>.exe`）。**`plugin.ini` 已于 2026-11-02 取消**（不再有启动清单 / 行序启动）。
3. **专用监管层已删除（2026-10-25 落地）**：`src/lib/llm/vftsindexer`、`src/lib/llm/codegraphindexer`、`src/lib/llm/pluginhost`（与 `host/mgr` 重复）、`llm/queuehooks`、`src/lib/core/indexkind`，以及 `gui/host`、`server/main` 各自两处 indexer exe 定位 + Ensure 组装 —— 全部退场（≈1100 行）。
4. **事件面以 workdir 为单位**：server 把 `instance-register` / `instance-exit` 翻译为 **`workdir-opened` / `workdir-closed`**（[61 §2.5](../60-reference/61-消息一览.md)），插件只管理 workspace；启动时拉一次当前活动 workdir，之后用事件增量维护。
5. **索引不独立成插件**：索引回路 + 引擎**库内联**在 `codegraph` 插件进程，工具与索引同进程、经 **TCP/IP（MCP）** 暴露；`file`/`codegraph` 这类简单能力**不再起子 exe**。
6. **子进程与安全上下文（以 crontab 类定时任务插件为例）**：插件可以自己 spawn 子 exe / Python 来执行任务。**可写目录等安全信息由宿主经「每次 `tools/call` 的 `_meta.chonkpilot.sandbox`」（有工具调用上下文）或「`plugin/v1/sandbox.resolve{work_dir}`」（定时任务等无上下文场景）下发**（**不是**握手协议头：header 是建连时静态、且跨 workdir 共享，见方案 §6 / C3），插件**必须自己处理**（据此约束自己拉起的子进程）；是否对子任务强制沙箱 = 该插件**配置页里的选项 / 任务级参数**，由插件自己读取并执行（例如定时任务默认沙箱运行，可在配置页或任务参数关闭）。
7. **宿主索引队列已下线（2026-10-25 落地）**：`filesys` 不再写待索引表（`Options.IndexQueue` / `enqueueIndex` 删除）；框架 `host/bridge` 的 `plugin.index.{take,ack,pending,workdirs}` 四帧与 `IndexQueueHooks` / `WorkDirs` 删除；data 面 `index_queue` 域、门面 `IndexQueueAPI`、v10 桶由 **v16 `drop_index_queue`** 清理。索引改由插件**订阅 `filesys.changed` / `session-complete`** 自管（当前两引擎均走**全量重建**：codegraph `codegraph_initialize`、vfts `vfts_index`；引擎虽支持增量，插件暂未编排）。**（2026-11-04 核对）**
8. **快速「停用 → 重新启用」并发收口（2026-11-01）**：`setEnabled` 每次前进 **启停意图序号 `lifeSeq`**；在飞的旧 `supervise` 若发现 `lifeSeq` 已变（或进程代次 `gen` 前进 >1）→ 直接退出，不再写状态 / 再拉起。停用尾段（`stopPlugin`）只在其停用意图仍有效（`stopReq` 未被重新启用清回）时才回写 `enabled=false` / `status=disabled` 并注销 MCP。否则开→关→开会把新进程的运行态覆盖成 stopped（实测 history 卡在非 running、UI 显示已停止）。

---

## 2A. 插件清单与加载模型（PLUGIN-LOAD-1；✅ 2026-11-02 已全部落地）

> 权威设计 = [插件加载与UI就绪设计方案](../../plugin-platform/插件加载与UI就绪设计方案.md)。本节为**已落地口径**（✅ 2026-11-02，D1–D18）。
> 变更摘要：`plugin.json` 极简、manifest 由插件进程上报、新增 `plugin-refresh`、启动并发上限 2、删除 `external`、新增 CK Store。

### 2A.1 清单归属

| 内容 | 载体 | 说明 |
|---|---|---|
| `id` / 启动命令 / `enabled`（+可选 `name`/`icon`） | `<root>/<id>/plugin.json`（极简，用户可写） | 发现阶段唯一读取源 |
| 完整 manifest（`version/description/ui/mcp/permissions`…） | `<root>/<id>/manifest.json`（插件自带；Go SDK 自读，Python/Node 自读） | 插件在 `plugin.hello` 上报（桥方法字段 `manifest`，**非 MQ 主题**） |
| 校验 | **只校验 ID** | 上报 `id` 必须 == 目录名（`plugin.json` 若写 `id` 亦须一致）；其余字段信任 |
| 损坏 | **不兜底** | `plugin.json` 缺失/语法错误 → 该插件不加载；上报 `id` 不符 → `status=error` |

### 2A.2 发现 / 启动 / 刷新

- `plugin-list`：**语义不变**（读缓存快照）；调用约定 = 每个前端实例启动时一次（首窗口 / 第二窗口 / 另一 workdir / F5）。
- `plugin-refresh`（🆕）：打开**布局编辑框 / 新建布局对话框**之前调用；重扫四个插件根 + 差量（新增登记并拉起、消失 → `plugin-unregistered` + 停进程、未变不动；**不做版本变化检测**）；返回与 `plugin-list` 同结构快照；失败 → 关闭对话框。
- 启动：扫描同步全量 → 先登记（入口可见，仅 `id/name/icon`）→ 再按**并发上限 2** 拉起（槽位 = `spawn` → `hello`）。
- 无启动预算：卡在 `starting` 即如实显示，不做超时判失败。
- `runtime.mode` / `channel.addr` / 「等待外部进程连接」**删除**（插件一律由宿主 spawn）。
- 监管参数（`restart`/`health`/`stop.grace_ms`/`mcp.timeout`）宿主内置默认，`plugin.hello` 上报后以上报值为准。
- 刷新期间前端对话框遮罩「正在刷新插件」，禁止操作、不可关闭；最多两行 = 并发中的插件。

### 2A.3 前端就绪态

- 插件框判定：不在台账 → 「未安装」；`disabled/stopped/error` → 「未安装」；`starting` → 「启动中」；`running` → 探活后挂 iframe。
- 只显示状态文字（`运行中/启动中/启动失败/已停止/已禁用/未安装`），**不加 detail 行**。
- 未上报 manifest 前，布局里已有的 `plugin:<id>:<view>` 先渲染框 + 「启动中」，entry 按 `webui/<view>.html` 回退。

### 2A.4 CK Store（前端状态读取）

- 前端全局 getter 仓库：组件 `register(key, getter)`，消费方 `get(key)`；**只提供 get**，未注册返回空；无权限白名单、无 key 登记表；**仅插件 iframe 可读**（`window.top.ckStore`）。
- 插件进程不支持；需要上下文时由前端随消息载荷携带（后端不做按 workspace/对话的初始化）。
- 调试抽屉：状态栏内置图标 → 抽屉列出当前窗口已注册 key + 当前值（打开求值一次 + 手动刷新）。

---

## 3. 各插件

### 3.0 web（联网能力：web_search / web_fetch / web_download）

> **〔2026-10-31 变更：`websearch` → `web`〕** 目录 = `chonkpilot-plugins/web`；插件 id = **`web`**；exe = **`chonkpilot-web.exe`**；配置命名空间 = **`plugins.web.*`**（旧 `plugins.websearch.*` 由 data 层首次读 usr 配置时一次性改名迁移，见 `src/lib/data/internal/config/userconfig.go`）。工具/契约随插件 go:embed，主仓 core 面不再注册这三个工具。

| 工具 | 实现 | 关键点 |
|------|------|--------|
| `web_search` | DeepSeek 网页搜索 `POST {base-url}{search-path}`（默认 `https://api.deepseek.com/v1/tools/search`） | 入参 `query`(1–128 必填) · `count`(1–50，默认 10) · `need_content` · `ori_question` · `localization`；返回 `{provider:"deepseek", results[], count}`（结果含 `rerank_score/title/url/snippet/summary/content/site_name/site_icon/date/authority`） |
| `web_fetch` | DeepSeek 网页抓取 `POST {base-url}{fetch-path}`（默认 `/v1/tools/fetch`） | 入参仅 `url`（http/https，≤8192，不得携带账号密码）；返回 `{provider:"deepseek", url, title, content}`，`content` = Markdown 正文 |
| `web_download` | **本地直连 HTTP**（不走上游 API） | **不传 saveAs/dir/filename → 返回原始内容**（文本原样 `content`；二进制 `content_base64`，MIME 见 `content_type`）；传则落盘下载（`.part` 流式写、超限即删）。支持 `method/headers/body/maxBytes/maxMB/overwrite/timeoutSec`；**重定向**默认跟随 **`max-redirects`（默认 5，0–20；0 = 不跟随）**，参数 `maxRedirects` 可覆盖；返回含 `final_url/redirects/status/content_type/bytes/truncated` |

配置键（全部可在插件配置页写；`/api/load` 密钥恒掩码）：`plugins.web.{api-key,base-url,search-path,fetch-path,count,need-content,timeout-sec,fetch-max-mb,fetch-timeout-sec,max-redirects,download-dir,download-max-mb,download-exts,download-overwrite}`。`web_search` / `web_fetch` 需 API Key；`web_download` 不需要。

### 3.1 compress（历史压缩）

> **〔2026-10-23 变更：压缩已内建，本节仅保留「算法口径」为权威〕** compress 的 **exe 插件形态已删除**（`src/plugins/compress/` · `src/initdata/capability/plugins/compress/` · 构建与注册全部移除）。压缩执行归 **session 组件内建**（[49 SESSION-1](../40-roadmap/49-待实现项.md#1-引用迁移与历史索引2026-11-04)）：**不再订阅 `session-compress`**，触发与载荷构造在 `src/lib/llm/server/compress.go` 内完成（复用刚发出的 payload、去全部 system、末条 user 换压缩要求、单飞/串行覆盖队列、CAS 回填）。下表**三段定位 / 兜底归并 / 摘要提示词 / 阈值来源 / token 估算 / 子会话门控**仍为权威口径；凡出现「订阅 `session-compress`」「插件进程」「插件读快照写回」的表述，**按内建语义理解**（数据面同为 `data-session-snapshot-*`）。进度通知仍走 `tool-notify`（`compress-queued/start/done`），发布方 = 内建压缩宿主。

| 项 | 值 |
|----|----|
| 订阅 | —（**已内建**：触发在 `compress.go` 组装后判定；历史订阅主题 `session-compress` 已于 2026-10-31 退出消息面） |
| 流程 | 读快照 → **三段定位**（`LocateThreeZones`，算法唯一来源 = `data.LocateZones`，**与组装侧同一函数**）：**完整区**（本轮 + 最近 N 轮，N/M 取先到，原文）/ **简化区**（再往前，**brief token 累计 ≤ T**，【简化态原文】= 仅 text）/ **摘要区**（更早）→ **摘要区非空**才压缩：**经 `llm-simple` 摘要摘要区（并入既有摘要）** → 回写快照（`snapshot_turn` 不变）。快照结果 = `[摘要] + 简化区（原文保留）+ 完整区（原文保留）`；简化区/摘要区**在发送时**由组装侧投影为【简化态原文】（`data.BriefMessages`，不落库、不丢字段） |
| **兜底归并**（口径 Y/Z3） | 触发：`摘要 + 简化区 + 完整区 + maxOutputToken > maxContextToken`（**真窗口 `maxContextToken` > 0 才启用**，缺省 0 = 不启用；输出预留 = `maxOutputToken`，**不再用常量 4096**）→ 处置：把【既有摘要 + 简化区全部原内容】（含待摘要的更早轮）合并 → **交同一 `llm-simple` 摘要路径重新生成摘要**（目标长度 = **`maxOutputToken` 的 1/2**，以长度要求追加到摘要输入）→ 结果 = `[新摘要] + 完整区`（**简化区清空**）；下一轮简化区自 0 重新累积。**幂等/可重入**（每次都自入参快照重算，重试不膨胀）· **失败回退**（摘要失败 → 快照原样返回、原文不丢）。**摘要无独立配置项**（目标 = 输出上限 1/2） |
| 失败策略 | **摘要失败直接不压缩**（不降级；含兜底归并失败 → 回退未归并状态） |
| **子会话门控（DSL-4，42 §2 (253)）** | 项目级 `compress.subsession`（**默认关闭**）：关 = **子会话**（会话行 `parent_id != ""`，经 data 门面 `SessionGet` 判定）turn **跳过压缩**（worker 内判定，不压缩、不广播 `compress-*` 进度 → 子会话不入队列状态展示）；**主会话恒不跳过**。开关开启 = 子会话照常压缩。另有 server 侧读点（`src/lib/llm/server/turn.go compressSubsessionOn`）：子会话压缩关闭时 LLM 请求失败（上下文超限，多为不可重试的协议错误）**追加可诊断提示**。**指引块（读路径）与宿主形态无关**。见 [64 §4.1](../60-reference/64-配置项一览.md) |
| 处理模型（OP-03） | **非阻塞、每会话串行**：组装完成后**只置入队并立即返回**（不在请求路径里同步跑压缩）——避免阻塞发终态的 turn 收尾 goroutine（`tc.Close()` 未及时执行 → 同会话 `session busy`），会话锁及时释放。每会话一个 **worker** 串行消费（同会话不并发、不重入），队列空即退出（下次入队再拉起）。同会话在压缩进行中再来的触发**合并**（只跑最后一次；压缩自最新快照幂等重算）。进程退出时未完成任务**允许丢弃**（不阻塞退出）。**已知副作用（接受）**：异步后**下一轮 `msgs()` 可能读到未压缩快照**（上下文暂时偏大），不引入「下一轮等待压缩」 |
| 进度通知（OP-03） | 入队（`compress-queued`，仅当同会话 worker 已在跑）/ 处理**前后**（`compress-start` / `compress-done`）各经**既有通知面** `tool-notify` 投一条（**不新增 MQ 主题**，见 [61 §4.3](../60-reference/61-消息一览.md)）：载荷 = `{instance_id, session_id, turn_id, notice, message, message_id, plugin:"compress"}`。前端 `ChatPanel` 据此显示**会话级**「正在压缩上下文」指示（i18n `chat.compressing`，`composables/useCompressStatus.js`），`compress-done`（或切换会话）后消失；**statusbar** 亦按 `compress-*` 显示压缩队列状态（`composables/useQueueStatus.js`）。压缩为**内部方法面**（2026-10-31 起无独立总线事件；前端「压缩记录」刷新由 `session-complete` 触发） |
| 阈值来源 | 读项目配置 `keep_full_max_turns`/`keep_full_max_tokens`/`compress_token_threshold`（prj 库 config；`compress.go` `resolveOpts`），缺失/非法回落 `DefaultOptions{RetainTurns:10, KeepFullTokens:24000, TokenMax:20000}`（见 [02-配置层级](../00-overview/02-配置层级.md) §5）。**`MaxContextToken` / `MaxOutputToken` 都不是配置项**：运行时由**内建压缩**直接读 provider 配置（`maxContextToken` / `maxOutputToken`；不再经 MQ 载荷透传） |
| 完整区定位 | `LocateThreeZones`（**口径 X**）：完整区 = `data.LocateZones` 的 `FullTurns`（**本轮恒保留**，下限 1，即使其自身完整态 token 已超 M 也不降级）；更早轮从尾部逐轮向前纳入，「累计**完整态** token > M」或「已纳入轮数 >= N」的那一轮起不再纳入完整区（算法唯一来源 = `data.LocateFullTurnCount`，**与组装侧同一函数**）。**取值语义（口径 W）**：`N == 0` = 轮次条件不启用；`M == 0` = token 条件不启用；**两者均 == 0 → 不压缩**（完整区=全量、无摘要区）；**负数 = 非法**（后端不猜、按「不启用」处理并留日志，前端显式提示）。**整轮对齐**（`role=="user" && Kind!="notify"/"continue"/"resume"` 为轮边界） |
| 简化区定位 | **T（`compress_token_threshold`）= 简化区 brief token 预算**：自完整区前一轮向前**逐轮累加 `brief_tokens`**（= 【简化态原文】token，构造 = `data.BriefMessages`/`BriefFacadeMessages`），累计 **≤ T** 的轮进简化区；把累计推过 T 的轮起（含）**全部进摘要区**。`T <= 0` → 简化区预算 0（不进简化区、直接摘要）。简化区 token 取**预存值优先、缺值回退实时估算**（`data.ResolveStoredTokens`，尾部对齐双向） |
| token 估算 | `EstimateTokens`：字符数/2 近似；**预存口径 = 完整态（全消息）/ 简化态（`data.BriefMessages` 仅 text）**，见 [21 §4.2](21-llm-server.md) |
| 摘要 system | 按级读**纯文本** `capability/system/summary.md`（项目级 → 用户级 → 系统级，`summarize.go` `resolveSummaryPrompt`）→ `llm-simple` 请求带 `system`；三级皆缺回落 **embed 内置**（`data.SystemDoc("summary")`，= 出厂文件 `src/initdata/capability/system/summary.md`）。提示词 = `capability/system/summary.md`（**纯文本**，无分区 / 无 `.prompt` 后缀；默认改出厂文件 embed 内置）。**设置页保存区分「继承/覆盖」**——内容与继承值（用户文件/系统文件/embed 内置）相同 → **不写覆盖**（删项目级文件、保持继承）；不同 → 写项目级覆盖；另有「恢复默认」（删项目级文件回落继承），见 [22 §3.1](22-data-persist.md) |

> **兜底归并的窗口与输出预留来源**：按两字段拆分 —— **`maxContextToken` = 上下文窗口大小**（仅用于兜底判定）、**`maxOutputToken` = 最大输出 token**（即请求体 `max_tokens`）。**内建压缩**（`src/lib/llm/server/compress.go`）直接读 provider 配置填入 `Options.MaxContextToken` / `Options.MaxOutputToken`（**`maxContextToken` > 0 才启用兜底**；输出预留 = `maxOutputToken`，**不再用常量 4096**）；摘要目标 = `maxOutputToken`/2。旧键 `maxTokens` / `max_tokens` **读时兼容**（只读不写）；`maxContextToken` **≤ 0 / 缺省 = 兜底不启用**（仅常规三层压缩）。**2026-10-31 起不再经 MQ 载荷透传**（`session-compress` 已退出消息面）。

### 3.2 history（文件历史：独立 ref 的检查点链）

> **语义**：打**独立 ref 的检查点链**，**绝不碰 `.git/index` 与 HEAD、不在用户分支产生任何提交**。消息面见 [61 §5.1.1/§5.4a/§8](../60-reference/61-消息一览.md)，配置键见 [64 §4](../60-reference/64-配置项一览.md)；设置页见 [20-gui §12.12](20-gui.md)。

> **〔2026-10-24 变更：已迁为进程外插件〕** history **不再是内嵌 Hook**：现由 `chonkpilot-plugins/history`（exe；`pluginsdk` + `bridgebus`）承载，业务实现**逐字复用**同仓 `history/lib`（module `chonkpilot-plugin-history`）。宿主侧 MQ 主题经插件桥（`plugin/v1/*`）等价转发，下表订阅/口径/数据落点**不变**。

| 项 | 值 |
|----|----|
| 订阅 | `session-complete`（**主轮次 → 轮末补点**）· `filesys.changed`（该 workdir 文件变更 → 置「脏」位；广播由 filesys 侧 **60ms 去抖**合并）· `instance-register`（建 workState + 异步回读 `history.enabled`）· `data-prj-config-refresh`（开关 / 保留参数实时同步 + `history.clear` 清链）· **前置打点钩子** `history-pre-tool-hook`（gateway 执行工具前同步调用）· 工具回调 `history-tool-call`。**`session-turn-start` 不再消费** |
| 启用/门控 | git 不可执行（`exec.LookPath("git")` 失败）→ `Start` 记日志 + `return nil`（**功能禁用，不算启动失败**）；workdir **非 git 仓库**（无 `.git`；worktree/submodule 的 `.git` 文件亦可）→ 功能禁用；`history.enabled` **仅显式 `"true"` 才开启（默认关闭）**，缺失 / `""` / `false` / 非法 / 未回读 → 关闭。**禁用或非 git 时 4 个工具一律摘除**（`syncTools` 收敛，判据 = 存在「hasGit 且 enabled」的 workdir） |
| 打点触发 | ① **gateway 前置钩子**（执行**任意**工具前，同步；经 `tools/register` 可选字段 `pre_hook_subject` 声明）：启用 && 未熔断 && **脏** → 同步打点；**打点失败 → 写 error → gateway 拒绝该工具调用（工具不执行），LLM 可见失败并可重试**；**单次钩子调用超时与 `CallTimeout` 解耦**（`Params.PreHookTimeout`，默认 **10min**），超时同样按失败处理（拒绝该调用）；**payload 带 `touch_files` = 该工具是否「涉及文件变动」**：显式 `false` → 直接放行、**不打点**（省 8–9 次 git 进程），仍登记会话归属（轮末补点保底）；缺省 / `true` → 打点；② **轮末补点**（`session-complete` 主轮次）：保证「最后一步的产像」入链 —— **不依赖脏位（规避 `filesys.changed` 60ms 去抖竞态）、强制走一次打点流程**，流程内按 tree 比较**内容未变则不建点**。**前置钩子路径不脏 / 标「不涉及」= 零 git 调用**（直接放行） |
| 「涉及文件变动」（打点粒度） | usr `tool_async.<工具暴露名>.touch_files`（布尔；前端「工具配置」页开关；**与异步档位同载体、同页**）。**缺省按工具来源**（**2026-11-04 核对，以 `chonkpilot-agent/gateway/src/tooloverride.go:229-267` 为准**）：`selfTouchFiles` 仅 `filesys_run` / `script_run`（true）；`readOnlyToolDefaults` 仅 `web_search` / `web_fetch`（false，按名判定、与来源无关）；**`file_*` / `task_*` 已迁为插件来源 → 走保守 true**（故 `file_read` / `file_find` / `file_diff` / `file_detect` 实际按 true，`task_run` 也不在 `selfTouchFiles` 白名单）；**dir 节点 / 第三方 MCP / 无法判定 → 保守按涉及（true）**。生效面 = gateway 前置钩子 payload（`resolveTouchFiles`：显式覆盖 > 只读按名 > 来源缺省）。**标错只让检查点粒度变粗**（前后点仍在、`git diff` 一致性校验仍生效），**不丢安全**（见 [64 §3](../60-reference/64-配置项一览.md) `tool_async` / [61 §5.4a](../60-reference/61-消息一览.md)） |
| 流程（打点） | 临时 index `add -A`（尊重 `.gitignore`；进入前 `ensureGitignore` 保证 `.chonkpilot/` 被忽略）→ `write-tree` → **新 tree == 链头 tree → 直接跳过（不建点、不 `update-ref`、不修剪）**；否则 → `commit-tree <tree> [-p <链头>] -m "chonk-ckpt: session=<slug> tool=<tool> ts=<RFC3339>"` → `update-ref refs/chonkpilot/<slug> <commit>` → **修剪** |
| 链与 ref | `<slug>` = **根会话**（优先 `top_session`，缺省 `session`；**父子会话共享一条链**）；链 = parent 指针线性串联（`git rev-list` 可枚举、`git diff A B` 可用）；**ref 只指向链头**（对象靠可达性保活，gc 安全） |
| 存储 | **独立 ref `refs/chonkpilot/<slug>`**（**废弃 `history.db`，纯 git**）+ **持久临时 index** `<workdir>/.chonkpilot/history/index`（经 `GIT_INDEX_FILE` 注入）；**绝不碰 `.git/index`、绝不碰 HEAD** → **不在用户分支产生任何提交** |
| 修剪 | `history.checkpoint_keep`（默认 500）/ `history.checkpoint_ttl_days`（默认 7）**任一超限即修剪**：保留段 = 「最新 keep 个」∩「窗口内」，再并入**保底项（当前轮 / 上一轮起点）永不删**；**天数窗口锚点 = 链上最新点时间**（项目闲置不清历史，不是 `now`）；修剪（2026-10-09 改；**2026-10-11 订正清理口径**）——保留集为**最新连续前缀**（常见）→ 一次 `git replace --graft` 把最老保留点解链（**O(1)、不逐点 `commit-tree` 重放**；链头与保留点 **commit id 不变**，会在 `refs/replace/<oldest>` 建一条替换引用，仅在**本插件链的遍历**范围内生效）；**陈旧替换引用清理 = 每次修剪枚举 `refs/replace/*`，先要求被替换 commit message 以 `chonk-ckpt: ` 前缀开头（**非本插件条目一律保守跳过**，否则用户自己的 `refs/replace/*`（commit message 恰好含 `session=`）会被 `parseCkptMsg` 误判归属 → 误删其替换引用），再按 `session=<slug>` 识别属本插件本会话的条目并清理**（**不依赖内存映射** → **跨进程重启亦能清理**；**绝不触碰其它会话 / 用户的 `refs/replace/*`**）；保留集**非连续**（保底项比 keep 窗口更旧，少见）→ 回退**按保留段重建**（复用 tree/message/时间，该情形 **commit id 会变**）；被淘汰点不可达 → 交 git 自动 gc（不主动跑 gc/prune）。**稳定标识恒为相对编号** |
| 熔断 | 连续打点失败 ≥ `fuseThreshold`(3) → `fused`（**仍尝试打点、失败亦放行、不再拦截工具调用**）；**成功一次即复位**；失败次数与模式经 `history.status.<slug>` 回写可见 |
| 内部工具 | **4 个**（`category=self`；启用且是 git 仓库才注册，否则摘除）：`history_status {}` / `history_diff {to?=-1, path?}` / `history_show {to?=-1, path}` / `history_restore {path, to?=-1}`（**单文件、禁止批量**；一致性校验 = 文件已删除 **或** `git diff --quiet <链头> -- <path>` 为 0，否则拒绝）。`to` 三态 = 负整数相对步（-1 最近一步、-2 再上一步…）/ `"turn-start"` 当前轮起点 / 绝对 commit id（须在本链上） |
| 状态回写（前端只读） | **会话级**内部键：`history.status.<slug>`（`{enabled, mode: active\|fused\|off, repo, dirty, failCount, checkpointCount, bytes, lastCheckpointAt, lastDurationMs, lastError}`）/ `history.timeline.<slug>`（**最新在前**、相对编号 `-1` 起、**≤200 条**）——`<slug>` = 链根会话，**多会话并发各自独立、互不覆盖**；落 **prjusr**（本机可重建派生物）。`history.clear`（前端写 JSON `{ts, session}`）→ **只清目标会话的链**（`update-ref -d refs/chonkpilot/<slug>`）并回写该会话空状态，其它会话链保留 |
| 语义边界（有意为之） | 被 `.gitignore` 忽略的文件**不进检查点、也回滚不了**（含 `.env`/`node_modules`/`.chonkpilot/` —— 不把密钥写进对象库）；**未跟踪文件**回滚时**不动**；回滚以检查点 tree 为准、**不碰 `.git`**；检查点遵守 git 的 ignore，与「索引排除规则」（`lib/ignore`）是**两套独立规则、不联动** |

> ⚠️ **效率注意**：文件历史在**每次工具执行前**打点（脏才打点）。**每次「脏」打点约需 8–9 次 git 进程**（写路径 + 状态/时间线回写的只读 git；逐行走查合计约 10–11 次，以实跑采数为准），**修剪**（仅在 `keep`/`ttl` 超限时发生；常见路径一次 `git replace --graft` 解链，**不逐点重放**）—— **大工程需慎重**（**默认关闭**）。已配「脏标记短路（不脏零 git 调用）+ **「不涉及文件变动」工具直接跳过**（省下每次调用的 8–9 次 git 进程）+ 前置钩子同步打点 + 连续失败熔断 + 固定提交身份（不读用户 git config）」。见 [49-待实现项](../40-roadmap/49-待实现项.md#1-引用迁移与历史索引2026-11-04)。

### 3.3 codegraph（代码语义索引，插件侧）

> **形态（2026-10-27 口径）**：**用户可见插件 = `codegraph`**（进程外 exe，`chonkpilot-plugins/codegraph`；codegraph + vfts 合一的代码知识库，`autostart` 由极简 `plugin.json` 的 `enabled` 承担，工具清单见 `src/initdata/capability/plugins/codegraph/manifest.json`）。本节描述**引擎与工具口径**；主仓原 `src/plugins/plugin-codegraph`（进程内兼容 Hook）**已于 2026-10-27 随 `src/plugins/` 一并删除**，仅保留 `codegraph` 进程外实现。

| 项 | 值 |
|----|----|
| 订阅 | `instance-register`（workdir 就位：旧键一次性迁移 + 按配置下发引擎 + 「启用且未 ready」自动全量索引）· `instance-exit`（清理实例记录）· `data-prj-config-refresh`（配置热生效 → 400ms 去抖重建）· `server-ready`（补跑旧键迁移）· **`filesys.changed` / `session-complete`**（2026-10-31 G-84 恢复：按 workdir **5s 去抖 → 全量重建**启用引擎）。**`vfts.reindex` / `vfts.session.query` / `vfts.session.reindex` 是 UI 动作（`OnRequest`），不是总线订阅**；**不订阅 `instance-heartbeat`**。宿主侧无待索引队列 / `plugin.index.*` / 独立 indexer exe。**（2026-11-04 核对）** |
| 配置键 | prj `plugins.codegraph.enabled` / `.exts` / `.skip-dirs` / `.stack-gitignore`（索引配置，变更即重建）。**运行态不写 prj 键**：`codegraph.status` 已随 PLUGIN-UI-2（2026-10-28）整体退场（宿主零引用），状态经插件动作 `engine-status` / `badge` 现取。旧宿主键（`enable-codegraph` / `codegraph.*`）仅由插件**一次性迁移**读取，写入不再生效 |
| 引擎 | **〔2026-10-27 已落地〕引擎以库形式编入 `codegraph` 插件进程**（工具与索引同进程；treesitter / vfts 均在 `chonkpilot-plugins/codegraph/src/engines/`）——不 spawn stdio 引擎子进程，也无独立 indexer exe。按 workdir 管理索引状态（每 workdir 独立串行） |
| 叠加 gitignore 体系 | 插件把 `plugins.codegraph.skip-dirs`（用户规则，`splitRules` **保序且保留重复项**）与 `.stack-gitignore`（布尔）**原样下发**引擎（`codegraph_configure` / `codegraph_initialize` 的 `skip_dirs` + `stack_gitignore`）；**插件不再读/解析 `.gitignore`**。规则来源/优先级/匹配语义/与 git 差异见 [29 §4.1](29-codegraph.md)（实现 = `chonkpilot-util/ignore`，内核 + I/O 同包，与 vfts 引擎/插件**同一实现**） |
| 注册工具 | **8 个查询工具**（`codegraph_symbol_search` / `_get_symbol_info` / `_callers` / `_callees` / `_get_dependency_graph` / `_find_circular_deps` / `_analyze_complexity` / `_get_module_summary`）+ **1 个 vfts 查询工具** `vfts_query`。8 个 codegraph 查询按引擎 schema **原样热注册**（hot=true；**含必填 `workdir`**，R-11 显式绝对路径）；**`vfts_query` 由插件自注册，schema 已去掉 `workdir`**（由 `_meta.chonkpilot.work_dir` 注入，见 [2B §3.5](2B-vfts.md)）。工具注册为**静态 MCP hot 清单**（`src/initdata/capability/plugins/codegraph/manifest.json` 的 `hot_tools`），无按 workdir 的动态注册/注销。**（2026-11-04 核对）** |
| 周期/超时 | data 5s · **MCP 调用超时 = 300s**（manifest `mcp.timeout_sec`；非 60s）· **全量重建 `rebuildTimeout = 15min`** · 配置去抖 400ms · 工作区事件去抖 5s。**无引擎子进程，故无 `childIdle` / `sweepIdleClient` / `clientFor` 空闲回收**（原描述已退场）；心跳能力仅在框架 `runtime/instance`，插件不订阅、不判超时。**（2026-11-04 核对）** |
| 状态获取 / 自动重建 | 运行态**不回写 prj 键**（`codegraph.status` 已退场）：配置页 / 状态栏 `badge` / L4 门禁均经插件动作 `engine-status`（可选 `engine` 只查一个）现取引擎状态。配置生效后 **400ms 去抖**调度一轮全量重建（`codegraph_initialize`），实例登记「启用且未 ready」亦调度一次（SRCH-004-S01 / SRCH-005-S01，**零新增主题**，无 500ms 进度轮询） |
| 可见性 | **工具静态注册，无 `refs>0` / `syncTools` 门控**（hot 清单固定，插件启用即注册）。多 workdir 时工具面全局一份，执行按 `_meta.chonkpilot.work_dir` / `context.instance_id` 路由到该 workdir 的**进程内 Workspace**（索引状态按 workdir 独立，非子进程）；工具面差异注册留待 v2。**（2026-11-04 核对）** |

详见 [29-codegraph](29-codegraph.md)。

### 3.4 vfts（全文检索索引，插件侧）

> **形态（2026-10-27 口径）**：vfts 与 codegraph 同属 **`codegraph` 进程外插件**的工具面（`vfts_query` 见其 `hot_tools`）；主仓原 `src/plugins/plugin-vfts`（进程内兼容 Hook）**已于 2026-10-27 删除**，唯一路径 = `codegraph` exe。

| 项 | 值 |
|----|----|
| 订阅 | 与 §3.3 同一插件进程（`instance-register` / `instance-exit` / `data-prj-config-refresh` / `server-ready`）。**2026-10-31 起订阅 `filesys.changed` / `session-complete`**（[41 G-84](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决)：按 workdir 去抖 **5s → 全量重建**启用引擎）。**`vfts.reindex` / `vfts.session.query` / `vfts.session.reindex` 为 UI 动作（`OnRequest`），不是总线订阅**；无 `vfts-tool-call` 订阅、无 `instance-heartbeat` 订阅。宿主侧无待索引队列 / `plugin.index.*` / 独立 indexer exe。**（2026-11-04 核对）** |
| 配置键 | prj `plugins.codegraph.vfts.enabled` / `.exts` / `.skip-dirs` / `.stack-gitignore` / `.docs` / `.doc-max-mb` / **`.encoding`**（D-73）/ **`.sessions`**（SESSION-FTS）。**运行态不回写 prj 键**：`vfts.status` 已随 PLUGIN-UI-2（2026-10-28）退场，状态（含插件探测的 `docsPort`）经插件动作 `engine-status` / `badge` 现取。旧宿主键（`enable-vfts` / `vfts.*`）仅由插件**一次性迁移**读取。**（2026-11-04 核对）** |
| 引擎 | **〔2026-10-27 已落地〕vfts 引擎以库形式编入 `codegraph` 插件进程**（与 codegraph 同进程；zvec 集合在该进程内自持）——不 spawn stdio 引擎子进程，也无独立 indexer exe。按 workdir 隔离索引状态（全局 `recMu` 串行索引） |
| 状态获取 / 自动重建 / 清单 | 运行态**不回写 prj 键**（`vfts.status` 已退场）：状态经插件动作 `engine-status` 现取（文档服务端口以 `docsPort` 透出）；配置生效后 400ms 去抖、工作区事件 5s 去抖，调度 **`vfts_index` 全量重建**（插件当前**无增量调用方**，不传 `files`/`remove`）。重建成功即按引擎 `indexed`（**唯一权威**，SRCH-007-S05）重建插件清单 `<workdir>/.chonkpilot/vfts/file_list.json`（字段 key/path/size/mtime/md5/doc_ids/chunks/indexed_at；key = sha1(绝对路径) 前 16 hex；**`md5` 留空**——引擎支持 `vfts_index {files,remove}` 增量，但插件侧暂未编排）。**无 500ms 进度轮询**。**（2026-11-04 核对）** |
| 注册工具 | **1 个查询工具** `vfts_query`（插件自注册；schema **已去掉 `workdir`**，由 `_meta.chonkpilot.work_dir` 注入）；管理工具 `vfts_configure/index/status/dict_* / vfts_session_*` **以 hot=false 注册进插件自带 MCP server**（不进 `hot_tools`，LLM 不可见），插件/配置页直接调引擎。**（2026-11-04 核对）** |
| 索引配置变更 | **去抖合并**：一次保存是**一个多键 `data-prj-config-save` 请求**（exts + skip-dirs + stack-gitignore 同一次 Set），宿主刷新后经 **400ms 去抖窗口**合并为**一轮**强制重建（窗口外单键变更仍触发，不会漏重建）；**保存为全表单直写，不做 dirty 键过滤**（仅 apply 侧做幂等：新旧值相同不重建）。**（2026-11-04 核对）** |
| 叠加 gitignore 体系 | 插件把 `plugins.codegraph.vfts.skip-dirs`（用户规则，`splitRules` **保序且保留重复项**）与 `.stack-gitignore`（布尔）**原样下发**引擎（`vfts_configure` / `vfts_index` 的 `skip_dirs` + `stack_gitignore`）——**插件不再读/解析 `.gitignore`**；插件侧 `manifest.go` **只写清单**（无扫描/diff 逻辑）。规则来源与优先级（低→高）：内置强制（`.git/`·`.svn/`·`.hg/`·`.chonkpilot/`，不可被 `!` 反选）→ 默认排除（`node_modules/`·`dist/`·`build/` 等，可被 `!` 反选）→ 全局 ignore（`$XDG_CONFIG_HOME/git/ignore` 或 `~/.config/git/ignore`）→ `.git/info/exclude` → 各级 `.gitignore`（目录越深优先级越高）→ 用户输入（最高优先级）；不勾选 = 只应用「内置强制 + 默认 + 用户输入」。**目录命中忽略 = 不下降；文件命中即跳过 → 支持文件级排除**。规则语法/匹配/与 git 差异见 [29 §4.1](29-codegraph.md)（实现 = **`chonkpilot-util/ignore`**，非不存在的 `chonkpilot-plugins/shared/ignore`）。**（2026-11-04 核对）** |
| 文档索引 / 文档转换服务接入 | 键 `plugins.codegraph.vfts.docs`（默认关）+ `.doc-max-mb`（默认 50）。**插件只探测、不 spawn** 转换服务（`docsprobe.go`）：读候选状态文件（① 插件 exe 上溯三级 → 产品根 `mcps/markitdown/state.json` ② 插件 exe 同目录 ③ `<dataDir>/mcps/markitdown/state.json`）→ 校验 `pid` 与 `GET /vfts/health` 一致 → 取 `{port, token, version}`；失败一律当「未运行」（不报错、不阻塞）。**配置下发前强制复探**，其余读路径按 30s TTL 缓存（`docsPort` 经 `engine-status` 透出）。索引前把 `docs`/`doc_endpoint`/`doc_token`/`doc_max_bytes`/`doc_text_max_bytes`/`doc_cache_dir` **恒一并下发** `vfts_configure`（**服务不可用 = 空 `doc_endpoint`/`doc_token`**；引擎按「**键存在即覆盖（含空值）**」清掉残留旧值，`docsAvailable` 随之为 false）。**降级**：服务不可用 → 清单与引擎**同口径整批跳过文档类**（未索引的文档类**从插件清单移除**（`<workDir>/.chonkpilot/vfts/file_list.json`，2026-10-25 起清单插件自持、不经消息面））；单文件失败 → 降级为「仅索引文件名」（引擎侧，保留在清单）。**解析器版本**：清单文档类行 `md5` 携带转换器版本（`<md5hex>@<version>`，复用 md5 口径）→ 版本变化即失效重建 + 插件清缓存目录。**token 仅内存、绝不入日志**。 |
| 进度推送 | 无 500ms 轮询：插件动作 `engine-status` / `badge` 现取当前快照（`state` / `progressDone` / `progressTotal` 取自引擎 `vfts_status`）；索引进行中的实时进度刷新随 [41 G-84](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决) 待恢复 |
| 周期/超时 | data 5s · **MCP 调用超时 = 300s**（manifest `mcp.timeout_sec`）· **全量重建 15min** · 配置去抖 400ms · 工作区事件去抖 5s。**无引擎子进程 / `sharedClient` / `sweepIdleClient` / `childIdle`**（D-71 后进程内引擎，原 5min 空闲回收退场）；插件不订阅 `instance-heartbeat`。**（2026-11-04 核对）** |
| 可见性 | **工具静态注册，无 `refs>0` / `syncTools` 门控**；v1 多 workdir 只全局注册一份，执行按 `_meta.chonkpilot.work_dir` / `context.instance_id` 路由到进程内 Workspace。**（2026-11-04 核对）** |

> **配置页与状态页（UIREG-1 / PLUGIN-UI-2）**：原宿主 `VftsConfig.vue` / `CodegraphConfig.vue` 已删除 —— 配置与状态全部由 `codegraph` 插件自己的 iframe 页承担（`webui/config.html` / `webui/status.html` / 栏内 `webui/badge.html`）。状态经插件动作 `engine-status` 现取（`state=""` = 未初始化；`phase` / `progressDone` / `progressTotal` / `err` / `indexedFiles` / `chunkCount` / `tokenizer` / `exts` / `docsService` / `docsPort` 同源），**不写 prj 键**；索引配置（exts/排除规则）**仅 dirty 才写库**（`plugin/v1/config.project.*`），并提供「恢复默认」（删项目级键 → 回落引擎默认集）。**文档索引区块**：开关 `plugins.codegraph.vfts.docs` + 体积上限 `.doc-max-mb` + 状态行（源 = `engine-status` 的 `docsService`/`docsPort`）+ MCP 注册指引；计数 `docsSkipped`/`docsFailed` 有值即显示。

> **两引擎同进程（2026-10-27 起）**：codegraph 与 vfts 的引擎库**都内联在同一个 `codegraph` 插件进程**（不 spawn 子进程、无独立 indexer/查询 executor），按 workdir 隔离索引状态；差异仅在 **LLM 可见工具面（codegraph 8 + vfts 1）**与引擎工具面（codegraph 12 / vfts 9）及索引编排。**（2026-11-04 核对）**

> **发行产物中的引擎 exe（2026-11-04 核对，已变更）**：`build-desktop.ps1` 默认**不向 `dist/desktop/mcps/` 投放引擎 exe**（「mcps 默认不接入；内置 MCP 时才单独投放」），当前 `dist/` 无 `desktop/mcps`。`build-codegraph.ps1` / `build-vfts.ps1` 仍可独立产出引擎 exe（用于自检/调试），但**运行期插件不解析/不 spawn 它**；L4 门禁 `run_index_gate.py` 已声明「发行根不再投放 mcps 引擎 exe；原 `-dump` 自检面退场，改经查询工具面探测」。查询工具「一工具一 exe」已于 PLUGIN-EDGE-1（2026-10-28）退场（见 [03 §5](../00-overview/03-构建与部署.md)）。

---

### 3.5 memory（记忆沉淀）

> **形态（2026-10-24）**：memory 已是**进程外插件** —— `chonkpilot-plugins/memory`（exe；`pluginsdk` + `bridgebus`），业务实现**逐字复用**同仓 `memory/lib`（module `chonkpilot-plugin-memory`）；其 MQ 事件/方法面经插件桥（`plugin/v1/*`）等价转发。下表订阅 / 门控 / 落盘口径**不变**。

| 项 | 值 |
|----|----|
| 订阅 | **`session-complete`**（每轮唯一终态；🆕 2026-10-24 解耦：原 `session-compress` 不再承担记忆触发）· 点分 `memory.flush`（**① 压缩前自动触发**（2026-10-31，server 在压缩阈值/子会话门控通过后、`startCompress` 前投递）· ② 用户手动「立即沉淀」，见 [61 §1.1](../60-reference/61-消息一览.md)）。`session-complete` 载荷为此**只增** `work_dir` / `data_dir`（定位数据根所需）。**触发策略（2026-10-31）**：默认 `memory.trigger=compress` → 轮末仅入队、**不提取**（提取主路径 = 压缩前 `memory.flush`）；安全阀 `memory.safety-tokens` 达阈值时轮末仍提取一次 |
| 配置键 | `memory.enabled`（总开关，**默认 false**）· `memory.trigger`（`compress`（默认）\|`every-turn`；**2026-10-31**）· `memory.safety-tokens`（默认 20000；compress 模式安全阀，0 = 关闭）· `memory.min-turn-tokens`（默认 200；**累计门控**，仅 `every-turn` 生效，见「处理」）· `memory.category.<类别>`（类别开关，缺省启用）· `memory.category-max-tokens`（单类别 token **告警**阈值，默认 2000、缺失不限；**仅告警不截断**）。**类别沉淀提示词（OP-04）已文件化** = `capability/system/memory/<类别名>.md`（**由 memory 域按文件读序 项目→用户→系统→embed 内置解析、随 `data-memory-list` 下发**；**插件不再读 prj/usr 键载体**，旧 `memory.prompt.<类别名>` / `memory_prompts` 与代码常量 `defaultRewriteSystemPrompt` 均已删除） |
| 提取范围（OP-05） | 按 (会话, 类别) 记录「**上次提取 turn**」进度（**专用表 prjusr `memory_extract`**，经 `data-memory-extract-{load,save,delete}`；记录 `{session_id, category, last_turn_id}`、主键 `<会话>\x00<类别>`，见 [61 §3.1](../60-reference/61-消息一览.md) · [12 §6.1](../10-architecture/12-数据层.md)）→ 提取范围 = 该类别**从上次提取轮到最新轮的全部轮**（`data-session-history` 取轮序 + 逐轮 `data-session-load-messages`），**含此前被阈值跳过的轮**；成功后**推进进度**到最后处理的轮（**仅在类别保存成功后**；无新轮 → 跳过） |
| 队列/合并 | 订阅回调（`session-complete` / `memory.flush`）**只置待处理标记**（key = (instance, session)，同 key **覆盖合并**）并立即返回；**每 instance 一个串行 worker** 取标记串行处理（同实例不并发、不重入；裸 goroutine **兜 panic**）。处理 = 该会话各启用类别「进度 → 最新轮」**一次性提取**；**无新轮 → 跳过（不算失败）**。天然幂等：同一 key 多次事件合并成一次、已提取的轮因进度推进不重提 |
| 门控（OP-05） | **累计门控**（非单轮）：自锚点（各启用类别中最旧的已提取轮；无进度 → 从头）以来**跨轮累计 token ≥ 阈值**才提取；未达 → 跳过且**不推进进度**（下次一并提取；跳过轮不丢失）。token 取 `turns.full_tokens` 预存值优先、缺值回退实时估算。**阈值随触发策略（2026-10-31）**：`every-turn` → `memory.min-turn-tokens`；`compress`（默认）→ `memory.safety-tokens`（安全阀；0 = 轮末永不提取）；`force=true`（`memory.flush` / 手动）→ 跳过门控 |
| 遍历范围（OP-07） | **项目级类别含子 session**（子会话自身的轮终态事件照常提取项目级记忆；子步骤也要提）· **用户偏好仅主 session**（顶层；按 `data-session-get.parent_id` 判主/子 → 子会话跳过 `level=user` 类别）。**注（DSL-4，42 §2 (253)）**：子会话是否**进入**该流程先受 `memory.subsession` 门控（默认关；见下「子会话门控」）。 |
| **子会话门控（DSL-4，42 §2 (253)）** | 项目级 `memory.subsession`（**默认关闭**）：关 = **子会话**（`session.parent_id != ""`，与 OP-07 同一 `data-session-get` 读取）turn **跳过自动沉淀**（`process` 在判主/子后即返回，不调 LLM、不写记忆、不广播 `memory-*` 进度）；**主会话恒不跳过**。**只关沉淀（写路径）**——记忆清单/资产/用户偏好**带出指引（读路径）不随该键变化**（只随 `memory.enabled` 门控）。`memory.flush`（手动）作用域 = 活动会话（主会话），子会话无手动入口。见 [64 §4.1](../60-reference/64-配置项一览.md) |
| 写冲突（OP-08） | **per-(workdir/项目, 类别) 进程内互斥 + 读-改-写临界区**：「读旧全文（`data-memory-read`）→ LLM 重写（`llm-simple`）→ 写回（`data-memory-save`）」整段持锁 → 同进程多会话并发写同一类别 md **不并发**；跨进程由**单持有者**保证（OP-14，另项） |
| 处理 | 判主/子会话 → 取类别清单（`data-memory-list`，每项带 `prompt` = 该类沉淀提示词有效值；首次由 persist 预置类别文件）→ 读会话全部轮 + 各类进度（`data-memory-extract-load`）→ **累计门控**（未达阈值 → 跳过）→ 按启用类别**并行**：按该类进度取「上次提取轮 → **最新轮**」全部轮消息 → 读旧全文 + 新信息 → 经 `llm-simple` 重写全文（`system` = 清单 `prompt`；**`prompt` 空 = 无提示词文件 → 该类不提取**，记日志 + `Deps.Notify` 可见提示；`llm.memory` 子系统 LLM，缺省回落 defaultLLM）→ `data-memory-save` → **推进进度**（`data-memory-extract-save`，仅成功后） |
| 进度通知 | 入队（`memory-queued`）/ 处理前（`memory-start`）/ 处理后（`memory-done`）各经**既有通知面** `tool-notify` 广播一次（`plugin=memory`；**消息面零新增主题**，见 [61 §4.3](../60-reference/61-消息一览.md)），供 statusbar 显示队列状态（排队 / 进行中 / 完成） |
| 手动沉淀 / 压缩前触发 | `memory.flush{instance_id, session, dialog_id}`（用户手动 或 server 压缩前自动）→ 置**待处理标记**（force=true；并入同一每 instance 串行 worker）→ **投递即回** `{ok:true, queued:true, session}`（不再同步等各类别 LLM 跑完；前置不满足 → `{ok:false, reason}`）；进度经 `tool-notify`（`memory-*`）推送；复用**跨轮读-改-写沉淀回路**（`distillRanged`，见上「提取范围」，范围 = 各进度 → **最新轮**），**不受 `min-turn-tokens` 门控**（无新轮 → 跳过） |
| 失败策略 | 不阻塞对话：任一环节失败只记日志 + `Deps.Notify` 上报一次（宿主去重/限频，落 `<dataDir>/logs/gui.log`），跳过该类、不降级、不抛出 |

---

### 3.6 crontab（本机全局定时任务；🆕 2026-10-31）

> **形态**：进程外插件 = `chonkpilot-plugins/crontab/src`（exe = `chonkpilot-plugin-crontab.exe`），状态落宿主注入的 `CHONKPILOT_PLUGIN_DATA_DIR/tasks.json`（原子替换）+ `runs.jsonl`（1MB 滚动 ×3）。设计见 [CronTab插件设计方案](../../plugin-platform/CronTab插件设计方案.md)。

| 项 | 值 |
|----|----|
| 工具（6，唯一契约源 = 插件 `src/tools/crontab/*.tool.md`，embed 进 exe） | `crontab_add` · `crontab_update` · `crontab_list` · `crontab_enable` · `crontab_disable` · `crontab_delete`；参数 = `cron` + `action`（update/list/启停删另有 `task_id` / `enabled`）＋ **`sandbox`（🆕 2026-11-03 P2 / C19：`true` / `false` / `"inherit"`；`add` 省略 = 继承插件配置，`update` 省略 = 不变）** |
| 动作模型 | `action = {type, param}`，`type ∈ {run, notify}`：`run.param = {command, notify?}`（完成后可选系统通知）；`notify.param` = HTML 字符串或 `{html, click_url?}`（系统通知；点击最多打开 http(s) 网址）。**没有第三个顶层参数**；「打开网页 / 播放音乐」写成 `run` 的 `start <url>` / `start <file>` |
| 调度 | `robfig/cron/v3`（5 段 + `@daily/@hourly/@weekly/@monthly`；**拒绝 6 段**）· 本地时区 · 单 goroutine 1s ticker 扫描 · 同任务不重入（overlap → `skipped:overlap`）· `run` 超时按配置杀进程树 |
| 持久化与恢复 | 受理即落盘（tmp + fsync + rename）；`running_since` 持久标记 → 重启补 `interrupted` 记录；错过补偿 `missed-grace-sec`（窗口内立即补跑，超出记 `skipped:missed`）；系统时间回拨 >5min → 重算全部 `next_run_at`（不补跑）；**`disabled_reason` 持久化**（当前唯一取值 `work_dir_missing`，用户重新启用或 work_dir 恢复后清空） |
| 配置（`plugins.crontab.*`，usr） | `default-timeout-sec`(600) · `missed-grace-sec`(300) · `max-tasks`(200) · `allowed-work-dirs`(空=不限) · `create-toast`(true) · `notify-fallback`(`log`\|`window`) · `sandbox`(false，**任务级缺省值**；任务可用 `sandbox` 参数覆盖) · **`show-in-session-list`(false，🆕 G-99：true = 定时任务顶层对话进入用户会话列表；消费方 = data 层列表过滤，读取失败/非 `true` 按 false)** |
| UI | `ui.selfServe`；视图 `tasks`（任务页：简述 / cron / 下次执行 / 状态，可启用/停用/删除）· `badge`（状态栏 96×20）· `config`；动作面 `list/toggle/delete/runs/badge/load/save/reset` ＋ **`detail`**（任务详情：沙箱实际状态（开/关/不可用原因）+ 最近 5 条运行记录（含 `skipped` 原因）＋ **P4：会话归属**（会话 id / 标题 / 轮次数 / 最近一轮时间与状态；未执行过 → 「尚无会话」）＋ **G-99：`openable_sessions[]`（`{session_id, dialog_id, title}`）+ 任务页「打开会话内容」按钮**（点击经前端既有 `gui.window.open-chat{session_id}` 开/激活对话窗口）） |
| 变更推送（零轮询） | 工具 handler / UI 动作写库后同进程调用 `notifyChanged()` → `plugin.publish`（topic 固定 `plugin-event`，payload `{kind:"crontab-changed", count, enabled}`）→ 徽标与任务页即时刷新 |
| 通知 | Windows 系统通知（WinRT Toast，内嵌 `toast.ps1`，AUMID `ChonkPilot`）；失败按 `notify-fallback` 记日志或退化为 MessageBox 窗口；非 Windows 返回不支持 |
| 安全（🆕 2026-11-03 P2/P3） | 与宿主同用户、不提权；`allowed-work-dirs` 非空时创建任务的 `work_dir` 必须命中白名单；**执行前 `os.Stat(work_dir)`**：不存在 → 任务停用（`disabled_reason=work_dir_missing`）+ `skipped:workdir-missing`，**只通知一次**、不执行（C14）；**执行前经宿主 `plugin/v1/workdir.ensure{work_dir, hidden:true}` 确保项目已打开**（未打开 → 宿主**隐藏打开**，C13/C17；失败 → `skipped:workdir-open-failed`，**本轮跳过、下轮重试，不 disable**）；随后经宿主 `plugin/v1/sandbox.resolve{work_dir}` 取可写白名单 → 对白名单目录 `sandbox.EnsureLowIL`（幂等/进程内缓存）→ `scriptrun.Options.Sandbox{Enabled, Writable}` 以 Low IL + Job 执行；**resolve 不可用 → fail-open（不隔离执行）+ `RunRecord.Detail` 留痕**。任务级开关 = `sandbox` 参数（缺省继承 `plugins.crontab.sandbox`）。任务文件不存密钥（命令里的密钥会明文落盘，页面已提示） |

> **P2 落地范围（2026-11-03，SANDBOX-WRITE-1 / 方案 §5.5）**：执行前链路 = ① `os.Stat(work_dir)`（缺失 → 停用，C14）；② `plugin/v1/sandbox.resolve` 取白名单（宿主实现 = `llm/server.SandboxPolicyForWorkDir`，命中在册实例用内存态 `security-*`，未命中只用内置默认项）；③ 白名单目录打 Low IL；④ `scriptrun` 以 Low IL + Job 执行。**未做（留 P5）**：PM 合并（C15）。**数据能力端点已由 P6 落地（2026-11-03，§3A.10）**，原「未做」项作废。**（2026-11-04 核对）**
>
> **P3 落地范围（2026-11-03，隐藏打开）**：`run` 在 ① 之后、③ 之前插入 **`plugin/v1/workdir.ensure{work_dir, hidden:true}`**（C13/C17）——已在册 → 直接回主实例 id；未打开 → 宿主隐藏打开项目（`gui.ProjectConfig.Hidden`，窗口建好但不显示）并等 `instance-register`（≤12s）；失败 → `skipped:workdir-open-failed` + 本轮跳过（**不 disable**，与 `work_dir_missing` 区分）。副作用：`sandbox.resolve` 因此**必然命中在册实例** → 用户配置的 prj `security-*` 生效（消除 G-96 的退化口径）。生命周期与普通项目一致（常驻到关闭该项目主窗口或进程退出），**无引用计数自动关窗**（残留项 [41 G-98](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决)）。
>
> **P4 落地范围（2026-11-03，会话归属 C11 / C12）**：`run` 在 ② 之后、④ 之前把本轮挂到**该任务的子会话**下 —— 层级 = 项目根会话（`id = crontab-<8hex(work_dir)>`，`kind=crontab`，`parent_id=""`）→ 任务子会话（`id = crontab-<task_id>`，`kind=crontab_task`，`parent_id` = 根会话）→ 每轮一个 `turn`（`turn-cron-<ns>`）。**插件不直接开库（C8）**：一律经宿主 UDS 能力 `plugin/v1/session.ensure` / `.ensure-turn` / `.complete-turn`（宿主 = `src/lib/llm/server`，建会话走既有 `SessionEnsure` 并 `rememberDialog`），执行主体 `sessions.origin=scheduler`（**不冒充用户**）。归属失败**不阻断执行**（`RunRecord.Detail` 记 `session_bind_failed: …`）；任务详情页经既有只读面 `plugin/v1/session.{get,history}` 展示会话（轮次数 / 最近一轮），`Task.session_id` / `dialog_id` 在执行后写回 `tasks.json`。**顶层会话列表排除 `kind=crontab`**（`data-session-list` / `-latest` / 活动位回落），详见 [61 §0.6/§3.2a/§7.1](../60-reference/61-消息一览.md) 与 [12-数据层 §2](../10-architecture/12-数据层.md)。**G-99（2026-11-03 已实施）**：① 可选配置 `plugins.crontab.show-in-session-list`（缺省 `false`；`true` = 该顶层对话进入用户列表；`data-session-latest` / 活动位回落恒排除），消费方 = data 层 `ListVisibleDialogs(catalog, includeCrontab)`；② 任务详情页新增 `openable_sessions[]` 与「打开会话内容」按钮，点击经**既有** `gui.window.open-chat{session_id}` 打开对话窗口（零新增 MQ 主题/payload）。**残留**：开关关闭（缺省）时，从任务页打开的 crontab 子会话窗口无法解析所属 `dialog_id`（顶层列表不含该对话），前端自愈会为其自举空对话 → 需在窗口面补 `dialog_id` 通道（消息面变更待拍板，[41 G-105](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决)）。

### 3.7 remote（远程接入；🆕 2026-11-01，构建清单 11/11）

> **形态**：进程外插件 = `chonkpilot-plugins/remote/client`（exe = `chonkpilot-plugin-remote.exe`，配置页/徽标 webui 自持并 embed）；配套 `remote/server`（`chonkpilot-remote-server.exe`，独立服务端）；设计见 [26-remote](../10-architecture/26-remote.md) §4。**（2026-11-04 核对）**

| 项 | 值 |
|----|----|
| 工具 | **0**（`mcp.mode=none`、`hot_tools=[]`；本期只做通道 + UI） |
| 通道 | 插件**主动出站 WSS** 到 Remote Server（桌面不开放端口）；上报设备 / 工作区；Server 的设备 RPC 转发为 `plugin/v1/*`，桌面事件归一为产品事件回推 |
| 订阅 | `instance-register` / `instance-exit` + 转发主题 `session-receive` / `session-turn-start` / `session-complete` / `session-ask` / `session-inbox-changed` / `task-started` / `task-updated` / `task-done` / `tool-notify` |
| 配置（`plugins.remote.*`，usr） | `serverUrl`（支持 `wss://` / `ws://`）· `username` · `password` |
| UI | `ui.selfServe`；视图 `config`（远程接入设置）· `badge`（状态栏 96×20）；动作面 `load/save/reset/test/status/badge/ping` |
| 安全 | 凭据只经插件配置读写；不注册 MCP 工具、不参与沙箱写隔离（无本地写工具） |

---

## 3A. 插件 UI 与宿主对接（四通道；🆕 2026-10-24 定稿）

> **前置**：插件 iframe 与宿主**同源**，且必须带 `allow-same-origin`（`sandbox="allow-scripts allow-forms allow-same-origin"`）——**等价于「插件代码视为可信代码」**（插件本就是本机 exe）。旧 `/plugin-ui/<id>/*`（宿主直读磁盘 `webui/`）**下线**，资源改由插件自己提供。
>
> **迁移状态（2026-10-24）**：资源通道 + 动作面骨架 + **全部内置插件自持 webui** 已落地 —— `browser` / `codegraph` / `desktop` / `file` / `mcp` / `task` / `web` 均声明 `ui.selfServe: true`、webui 已搬入插件源码仓 `chonkpilot-plugins/<id>/webui/`（2026-10-24 时在主仓 `src/plugins/<id>/webui/`，后随插件迁出）并 `go:embed`、`src/initdata/capability/plugins/<id>/webui/` 已删除；前端按 `self_serve` 分流到 `/plugins/<id>/…`。**唯一例外 = `demo-python`**（纯 Python 样例、无 exe、不随产物投放 → 仍由宿主直读磁盘，已列入单源测试白名单）。旧 `/plugin-ui/` 路由**保留但已无内置使用者**（`_sdk/plugin.js` 端点仍需保留，下线时只下线磁盘直读分支）。**codegraph 四通道已全部打通**（含配置迁入 `plugins.codegraph.*`、动作面 `save/clear`、未就绪提示页 + 3 次自动重试）。**其余 6 个插件（browser / desktop / file / mcp / task / web）配置链路已收口**：页面 → `/plugins/<id>/api/{load,save,reset}` → 插件校验 → 私有配置存储（键名 `plugins.<id>.*`）；`plugin.json` 的 `config.fields` 已删（**关闭宿主 `plugin-config-set` 第二写入口**），旧键仅作一次性迁移源。✅ **2026-10-31 订正**：`plugin/v1/config.user.{load,set,delete}` 已实现（framework `bridge/configuser.go`，键前缀按插件 id 强制 `plugins.<id>.`），插件配置落 data 层 usr；task / crontab 等插件已直接使用（旧私有 `webui-config.json` 仅作一次性迁移源）。旧 `/plugin-ui/` 磁盘直读分支**已下线**（仅保留 `_sdk/plugin.js`）；`demo-python` 已删 `ui.views/menus/statusbar`（无 exe，页面不随运行时提供）。待做：旧路由/`PluginWebRoot` 残留引用清理、旧 L4 脚本重写。

### 3A.1 四条通道

| 通道 | 入口 | 走向 | 归属 |
|---|---|---|---|
| 静态资源 | `GET /plugins/<id>/<path>` | 宿主按 `ui.assets.mode` 取资源（`bytes` 经 UDS `plugin/v1/ui.asset.get`；`dir` 直读磁盘；`resource` 读 exe PE 资源段） | 见 §3A.2（PLUGIN-ASSET-1） |
| 动作 | `POST /plugins/<id>/api/<action>` | 宿主 → UDS → 插件 | 业务语义全归插件 |
| 事件 | iframe ⇄ 主窗口 ⇄ server | **同一份 mq**（同源直接访问） | mq 现有语义；**instance 归属与定向投递见 §3A.7** |
| 配置 | 插件 → `plugin/v1/config.project.*` | UDS 能力 → data 层 | 统一管理（不落插件自有文件） |

> **契约扫描边界（2026-10-26 定）**：`<capability>/plugins/<id>/**` 是**插件自己的能力面**（其中 `tools/*.tool.md` 由插件进程内的 MCP server 注册/执行），**不属于内核能力面** —— 内核契约扫描（`ScanAll` 的磁盘/内嵌两条路径、`LoadExecTools`、`AppToolNames` 重扫回收清单）**一律跳过 `plugins/` 子树**。否则同名工具会以契约里的 `runtime=builtin` 走内核分派（file 组已无进程内实现 → `category "file" 无进程内实现`），并**抢走**插件 MCP server 的路由。见 [42 §2 (275)](../40-roadmap/42-决策记录.md)。

### 3A.2 资源

**来源三模式**（PLUGIN-ASSET-1，2026-10-31 落地；manifest `ui.assets`）：

| 模式 | 声明 | 宿主如何取 | 适用 |
|---|---|---|---|
| `bytes`（缺省 / 兼容） | 无 `assets` 段 | UDS `plugin/v1/ui.asset.get`，插件回 `{size, data(base64)}`（单帧 ≤ 48 MiB） | 现有 Go 插件（webui embed 进 exe） |
| `dir` | `{mode:"dir", root:"webui"}` | 宿主直读 `<插件目录>/<root>/<path>`，**零桥调用**（插件未就绪也可服务） | Python / Node 等解释器插件、落盘 webui |
| `resource` | `{mode:"resource"}`（或单次响应回 `mode:"resource", resource_id`） | `LoadLibraryExW(exe,0,LOAD_LIBRARY_AS_DATAFILE)` + `FindResource(RT_RCDATA)`（只读取数据、**不执行**） | 原生 exe 以 `.syso` 打进资源段 |

- **不提供 `proxy`**（插件自起 HTTP）：端口占用 / 防火墙提示 / 安全边界外扩。
- `ui.assets.root` 必须是**插件安装目录内的相对子路径**；`..` / 绝对路径 / 盘符 / 反斜杠 → manifest 校验期直接拒绝。
- `dir` 越权：宿主 `Clean` + `EvalSymlinks` 后必须仍在 `root` 内，否则按未命中拒绝。
- 路径限定在该插件资源根内的相对路径；`..` / `%2e` / 反斜杠 → `403`；未命中 → `404`。
- `Content-Type` 由扩展名表决定；`Cache-Control: no-cache, no-store, must-revalidate`。
- 宿主侧短缓存键 = `(plugin_id, path, 版本分量)`；**版本分量** = `manifest.version#运行代次`，`dir` 追加 `root` 的 `mtime+size`、`resource` 追加 exe 的 `mtime+size`（插件升级 / 重启 / 改盘即失效）。**非 `running` 状态版本分量为空**（与任何已缓存条目都不相等 → 缓存必然失效、重取向插件拉取；插件未就绪时拉取放行检查失败 → `404`），保证禁用 / 停止后不再返回旧缓存页面（2026-11-01）。
- **前置条件（仅 `bytes`）**：打开插件页前必须确保插件进程就绪；未就绪时只能展示「插件不可用」提示页（宿主**没有**副本可兜底）。
  前端就绪态（`usePluginReadiness`）：`status≠running` 或资源探活 404 → 占位；**首帧加载超时（3s）与探活失败共用同一自动重试预算（3 次）**〔2026-10-28 订正：原仅探活失败才排自动重试，首帧慢一次就永久卡在占位〕。
- 单文件大小上限 + 超限明确报错（不静默截断）。

### 3A.3 动作

- 请求体/响应体**原样透传**，宿主**不解析业务**；仅做三件事：① 按 id 路由到插件；② 校验「发起 iframe 的 id == 目标 id」（防 A 插件页调 B 插件动作）；③ 超时与错误信封。
- **宿主实现约束（2026-10-26）**：WebView2 资源拦截回调里由宿主 `http.NewRequest` **重建**请求时，浏览器自定义头默认丢失 → 必须按**白名单**转发 `X-CK-Plugin-Id`（否则 ② 恒失败 403 `plugin id mismatch`，插件页保存/清除全部失效）。只转白名单、不整体透传（避免把 `Cookie`/`Origin` 等无关态带进内嵌面）。见 [42 §2 (276)](../40-roadmap/42-决策记录.md)。
- **业务校验、幂等、错误码、并发控制全部由插件自己负责**。
- 该路径**只允许本应用 WebView 访问**；若实现上同时暴露在本机 HTTP 端口，则必须加令牌（否则任意本机网页可触发插件动作）。

### 3A.4 事件

- iframe 直接用宿主的 `mq`（同源可访问），外层薄壳负责**自动退订**（卸载时 + `beforeunload` 兜底），避免事件回调打到已销毁的 frame。
- **自回显语义（必须写清）**：
  - **本地**：`emit` 按 order 分发到同一 JS 上下文的所有订阅者 —— **发布者自己订阅同一主题也会收到**（mq 既有语义）；
  - **跨进程**：后端/桥**不回显**（防环，见 [11-MQ与消息](../10-architecture/11-MQ与消息.md)）。
  - 两者不可混为一谈；插件若不想收到自己的消息，自行在回调里按来源过滤。
- topic 白名单在可信代码前提下是**约定**而非强制（壳可拦误用，拦不住恶意）。

### 3A.5 配置

- **统一由 data 层管理**：插件经插件桥读写 data 层配置；**不落插件自有 `config.json`**（避免导出/备份/切项目三处失配）。三级通道（2026-10-31，G-82①）：
  - `plugin/v1/config.user.{get,load,set,delete}` → **usr**（跨项目、跨进程的用户级配置）；
  - `plugin/v1/config.project.{get,load,set,delete}` → **prj**（项目级、团队共享）；
  - `plugin/v1/config.prjusr.{get,load,set,delete}` → **prjusr**（本机本项目个人运行态配置）。
- **键命名空间（按层强制前缀）**：usr / prj 层键 `plugins.<id>.<field>`；prjusr 层键 `plugins.<id>.prjusr.<field>`。插件桥按插件 id 强制前缀并做读侧收窄（越界 `config_key_forbidden`），data 层再把 `plugins.*.prjusr.*` 路由到 prjusr（`isPluginPrjUsrKey`，[64 §4](../60-reference/64-配置项一览.md)）。
- **校验归插件**：所有请求都经插件，业务校验（取值范围、互斥、依赖）由插件自己做。
- **兜底归插件**：插件不一定收到配置刷新广播，缺值时的默认与降级由插件自己提供。
- **单源**：旧宿主键（`enable-vfts` / `vfts.exts` / `codegraph.exts` …）一次性迁入命名空间后**删除旧键**，不得双写。**插件未启用时的旧键不迁移**（2026-10-31 定：尚未正式上线，不做宿主侧补偿迁移）；残留旧键可用外挂一次性工具清理（`src/tools/cfgclean`，不参与构建）。
- **字段必须被消费（2026-10-31 审查口径）**：配置页（`config.html` 的 `fields`）出现的每个键**必须**在插件运行时有读取点，不得只保存 / 回显。审查处置：`task` 移除 `interpreter-go` / `interpreter-rust`（scriptrun 白名单无 go/rust）；`crontab` 移除 `sandbox`（原“预留”开关零消费）；`file.default-encoding` 改为**逐 target 注入** `targets[i].encoding`（原顶层 `encoding` 无消费方）；`browser` / `desktop` 的 `screenshot-dir` 增加“绝对路径 / `~/`”校验并在使用时展开 `~`。**`plugin.json` 的 `config.fields` 死字段已删除**（2026-10-31，前端配置面由插件自带 `config.html` 承担）。
- **例外**：`history` 无插件配置页，其 `history.*`（如 `history.checkpoint_keep`）属**宿主用户配置**（见 [64-配置项一览](../60-reference/64-配置项一览.md)），不适用 `plugins.<id>.*` 前缀。
- **memory 例外撤销（MEMORY-UI-1，2026-11-02）**：memory 插件新增**自有配置页**（`webui/config.html`，manifest `ui.selfServe=true` + `ui.views[id=config]`），宿主「项目配置 → 上下文」不再承载记忆配置（只留迁移提示）。该页仍直接读写 **既有 `memory.*` prj 键**（`data-prj-config-{list,save}`）与 `data-memory-*` / prompt 域，**不引入 `plugins.memory.*` 前缀、不新增配置键**；动作面 = `config-load` / `config-save` / `category-{content,save,delete}` / `prompt-{save,reset}` / `flush`。为支撑该页，插件桥新增三条通用方法映射：`plugin/v1/config.prompt.delete → data-prompt-delete`、`plugin/v1/memory.delete → data-memory-delete`、`plugin/v1/session.active.get → data-session-active-get`（[61](../60-reference/61-消息一览.md) 数据面主题均为既有，零新增）。

### 3A.6 对接检查清单（评审逐条要有答案）

| # | 关注点 | 必须有答案 |
|---|---|---|
| 1 | 生命周期 | 插件未启动 / 崩溃 / 重启时，资源请求与动作请求分别怎么表现？页面是否自动重载？ |
| 2 | 版本 | 插件升级后资源版本如何变化？旧页面还能不能调新动作？（API 版本协商） |
| 3 | 鉴权 | 动作面是否只被本应用可达？iframe→插件的 id 绑定如何校验？ |
| 4 | 并发 | 同插件多窗口 / 多 iframe / 动作并发时的串行化与幂等 |
| 5 | 错误 | 统一错误信封（code / message / retriable）+ 超时；插件未实现该动作如何回 |
| 6 | 资源上限 | 单文件大小、总响应大小、路径深度；超限行为 |
| 7 | 缓存 | 短缓存键与失效时机；`no-store` 与缓存的边界 |
| 8 | 订阅 | 退订时机（卸载 / 重载 / 插件重启）；事件量上限与背压 |
| 9 | 配置 | 命名空间、迁移、删除旧键、并发写（CAS/冲突） |
| 10 | 迁移 | `/plugin-ui/` 路径下线、旧键迁移、旧 `webui/` 目录清理 |
| 11 | 可观测 | 资源/动作失败是否留日志、是否在 UI 可见（避免"白屏无提示"） |
| 12 | 测试层级 | L1（组件/纯函数）· L2（mq 驱动 + 帧断言，按 [50](../50-testing/50-测试体系.md)）· L4（可见级）各覆盖哪些 |

### 3A.7 instance 归属与定向投递（✅ 2026-10-27 定案 · 2026-10-28 已落地）

> 现状（代码）：`plugin.publish` 已支持**可选** `instance_id`，`mgr.publishUIEvent` 广播时提升到顶层 `{id, instance_id?, topic, payload}` → GUI 桥 `forwardEvent` 的实例过滤直接生效；插件页 iframe URL 由 `usePluginReadiness` 拼 `instance_id`（承载窗口），`PluginPageHost` 按 `id` 过滤 + 桥按顶层实例过滤。缺省仍为广播（兼容旧插件）。落地清单见 [49 PLUGIN-INSTANCE-1](../40-roadmap/49-待实现项.md#1-引用迁移与历史索引2026-11-04)。
>
> 主题名：`plugin-event`（2026-11-04 一次性正名，不做双发兼容期；见 [41 D-72](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决)）。
>
> 专题稿：[插件边界与实例归属设计方案](../../plugin-platform/插件边界与实例归属设计方案.md)（含前端取法 / 后端取法 / 定向投递 / 边界与重启恢复）。

#### 3A.7.1 归属口径（唯一）

| 概念 | 取值 | 说明 |
|---|---|---|
| **插件 UI 的 instance** | **承载该 iframe 的窗口实例** | 插件页开在主窗口 = 主窗口实例；开在单聊/群聊子窗口 = 子窗口实例 |
| 插件进程的 instance | 宿主 `plugin.hello` 后分配的插件连接 id | **另一命名空间**（`plugin/v1/instance.claim` 返回的也是它），**禁止**与窗口实例混用 |
| 无源消息（定时任务等）的目标 | 该 `work_dir` 的**主窗口实例** | 规则见 §3A.7.3 |

#### 3A.7.2 前端取法：iframe URL 携带 `instance_id`

- 唯一构造点 = `usePluginReadiness.src`（`src/frontend/packages/plugin-ui/src/usePluginReadiness.js`），拼接：
  `/plugins/<id>/<entry>?p=<pageId>&instance_id=<currentInstanceId()>[&t=<seq>]`
- 取值 = `currentInstanceId()`（认领优先、未认领回落首屏注入；`instance-claim` 为**入口绑定优先**、不会改写该值，[61 §4.1](../60-reference/61-消息一览.md)）。
- **不影响资源面**：宿主按 `r.URL.Path` 取 `sub`、缓存键 = `(plugin_id, path, plugin_version)` → query 既不进路由也不进缓存（`chonkpilot-framework/host/src/gui/pluginassets.go`）。**（2026-11-04 核对）**
- 插件页**首帧读取并自行持有**（页内跳转 / 刷新不保证 query 仍在）；`instance_id` **不是凭据**（[22-实例与会话标识](../10-architecture/22-实例与会话标识.md)），可出现在 URL。
- 宿主侧**不需要为 instance 新增注入接口**：`ckPlugin` 维持现状；`ckMq` 改为平台基座唯一入口 `window.top.ckMq.new(window)`（[2C §3.3/§4.3](2C-UI插件.md)；原 `public/shared/ck-mq.js` 已删除）。

#### 3A.7.3 后端取法

1. **有来源**（收到事件 / 工具调用 / 订阅回调）：直接用消息里的 `instance_id`。
2. **无来源**（纯后台发起，如定时任务）：`work_dir` → 主窗口实例。
   - 判定 = 实例视图里 **`work_dir` 相同且 `parent_instance` 为空**（顶层）的那一条；
   - 一个项目**只有一个主窗口**（GUI 进程内 `work_dir → 主窗口` 映射表保证，`src/lib/gui/host/main.go`），故不需要额外优先级；
   - 单聊 / 群聊窗口的 `instance-register` 带 `parent_instance`（逐层链）→ 天然排除。
   - 🆕 **项目未打开**（定时任务首次执行）：先经宿主 `plugin/v1/workdir.ensure{work_dir, hidden:true}` **隐藏打开**该项目并拿回主实例 id（C13/C17，见 [61 §7.1](../60-reference/61-消息一览.md)），再按上面的口径投递。
3. **未命中**（项目未打开 / 主窗口已关）→ **丢弃并记日志**，**不得回落广播**（防串窗）。

#### 3A.7.4 投递语义

| `instance_id` | 行为 |
|---|---|
| 缺省 | 广播：所有窗口都收（现状语义，保留） |
| 有值 | 只投该窗口：由 GUI 桥 `acceptEventInstance` 顶层精确匹配完成，前端无需再判 |

#### 3A.7.5 落地（四层；已完成）

| 层 | 改动 |
|---|---|
| 前端 | `usePluginReadiness` 拼 `instance_id`（首次 URL 构造唯一入口）；插件页从 `location.search` 取 |
| 插件桥 | `plugin.publish` 增**可选** `instance_id`（目标窗口）；`mgr.publishUIEvent` 广播时把它提升到**顶层**：`{id, instance_id?, topic, payload}`（L1 `publish_instance_test`） |
| 插件框架 | `runtime/instance.Manager.MainInstanceForWorkDir(workDir)`（框架仓，**待发版**）；`sdk.PublishUI` / `ResolveMainInstance` / `PublishForWorkDir`；主仓自持 `bridge.MainInstanceForWorkDir` + `plugin/v1/instance.resolve{work_dir}`（不依赖新版本） |
| 消息面 | `plugin-event` 载荷**只增**可选 `instance_id`，已在 [61 §7.2](../60-reference/61-消息一览.md) 登记 |

#### 3A.7.6 边界

- 目标窗口未打开 → 事件**丢弃、不排队、不重放**（UI 事件是即时通知，不是可靠队列）。
- 插件页开在子窗口时，投给主窗口的定向事件**收不到**（URL 里它的 instance 是子窗口）—— 需要"插件页在哪就投哪"的场景由插件自己上报其宿主实例，不在本期。
- 广播（无 `instance_id`）仍是合法用法；需要"父 + 子都收"时用它。

### 3A.8 栏条目内嵌插件视图（`ui.statusbar[].view`；✅ 2026-10-28 落地，PLUGIN-UI-2）

**口径**：状态栏 / 工具栏里**只承载宿主自己的可配置 view**；**插件的业务内容（如索引进度）由插件自己在栏内呈现**——宿主不得为插件业务写视图、也不得读插件业务键（依赖单向）。

| 层 | 约定 |
|---|---|
| manifest | `ui.views[]` 增一条视图（如 `{id:"badge", entry:"webui/badge.html", containers:["statusbar"], default_size:{width,height}}`）；`ui.statusbar[]` 条目用 `{id, zone, view:"<view id>", tooltip}` 引用它（原 `command` 形态保留给按钮条目） |
| 宿主（Go） | `mgr.BarItem` 增 `View string`；`plugin.ui.rpc` 仅透传动作名（列表在 `ui.rpc`） |
| 宿主（前端） | `pluginBarItems` 对带 `view` 的条目产出 `{kind:'view', viewKey:'plugin:<id>:<view>', width/height ← default_size}`，复用既有 `LfBarItem` view 分支（`LfViewHost` → `PluginPageHost` iframe）；**不写入布局文档**（插件禁用即消失） |
| 插件 | 视图页自行取数（如 codegraph `badge.html` 经自有动作 `badge` 拉取，`instance_id` 取自 iframe URL）；点击打开完整页用 `const mq = window.top.ckMq.new(window)` 后 `mq.emit('preview-tab-open', {kind:'plugin-page', plugin, page})` |

**codegraph 现状**：`ui.views` = `config` / `status` / `badge`；`ui.statusbar[0].view = "badge"`（200×20：状态点 + 「索引中 N/M / 完成 / 失败 / 未初始化 / 未启用」，2s 轮询）。宿主 `IndexBadgeView` / `IndexStatusDetail` / `utils/indexStatus.js` 及其注册、默认布局条目已删除；`test_statusbar.py` SB2/SB6 按新口径断言。

### 3A.9 订阅与事件隔离：插件 sink 已接入统一分发器（✅ 2026-10-29 落地，49 §2.22.7 S3）

**口径**：插件订阅与窗口**同一套判定**——插件桥 `pluginhost/bridge` **不再各自订阅 `>`**，每个插件会话在 `plugin.hello` 后注册为共享分发器（framework `runtime/dispatch`）的一个 **plugin sink**；分配目标由宿主策略 `mqpolicy.WorkDirPolicy` 决定，**workdir 级事件由宿主强制隔离**（只投同 workdir 的 sink），与窗口 sink 同规则。见 [11-MQ §5.1](../10-architecture/11-MQ与消息.md)。

| 环节 | 约定 |
|---|---|
| 注册时机 | `plugin.hello` 完成（拿到 `plugin_id` / `instance_id` / 可选 `work_dir`）后注册；**不依赖插件先订阅**（先注册后订阅亦可，`matches` 在投递时现查）。未 hello 的会话不注册 |
| sink 字段 | `ID = plugin:<plugin_id>:<connSeq>`（同 plugin_id 重连不覆盖前一条）· `WorkDir = 会话声明的 work_dir`（未声明 = 空 → 策略放行）· `Kind = Plugin` · `Instance` **恒为空**（插件连接的 `instance_id` 属插件命名空间，不参与窗口实例级判定；需按实例收窄用 `Filter.instance_id`） |
| 投递 | 分发器只负责「投给哪个 sink」；**sink 内部仍按本会话 `Subscription.Pattern/Filter` 过滤**，未命中丢弃 |
| 注销 | 会话 `Close` / 插件退出 → `UnregisterSink`（幂等）；慢插件仍走有界队列丢帧，不阻塞总线 |
| 兜底 | 未注入分发器的宿主（L2 单测 / 无总线形态）保留桥自有 `>` + `fanout`，行为与改造前一致 |

**`plugin.hello.work_dir`（可选，本批新增字段）**：非空 = 本连接只服务该项目目录（sink 带 workdir → workdir 级事件只收同 workdir）；空 = 未声明（放行，跨项目，现有插件零改动）。

**`Subscription.Filter.work_dir`（订阅过滤字段，三取值）**：**注册期校验**，**不参与** `matches`（避免与分发器策略形成双重判定口径）：

| 取值 | 语义 |
|---|---|
| 空 / `"*"` | 不限（与其它过滤键同口径） |
| `"self"` | 只收本插件自身 workdir 的事件 —— 该保证由**分发器投递时的 sink 侧判定**承担，故此处等价于不限；显式接受该取值以免插件按文档写 `"self"` 反被拒 |
| 显式绝对路径 | 必须等于本插件自身 workdir（`pathx.Same`：大小写 / 分隔符 / `..` 归一）；否则返回 `subscription_denied`（未声明 work_dir 的连接用显式路径同样拒绝） |

### 3A.10 数据能力端点（SANDBOX-WRITE-1 P6 + G-104；✅ 2026-11-03）

**口径（C8 / C9 / C16 / C18）**：插件与任务脚本**不得直接打开 bbolt 库**（`<work_dir>/.chonkpilot/**/*.db`）；
键值、自定义桶、项目用户枚举与会话导出一律经插件桥 UDS 方法面，由宿主完成读 / 写 / 建桶 / 审计。
`.chonkpilot` 数据根**仍在写白名单内**（沙箱不承担「防写库」职责），该约束由「禁止直接开库」契约 + 本组能力端点承担。

| 方法 | 入参 → 返回 | 说明 |
|------|------------|------|
| `plugin/v1/data.kv.get` | `{work_dir, user?, key}` → `{found, key, value?}` | 键值读；键空间 = **(插件 id, work_dir, user)**；**过期键按不存在返回**（`found=false`，不是错误） |
| `plugin/v1/data.kv.set` | `{work_dir, user?, key, value, ttl_seconds?}` → `{ok, key, size, ttl_seconds?}` | 键值写；`key` = `[A-Za-z0-9._-]{1,64}`；`value` ≤ **64 KiB**（超限 `value_too_large`，**不截断**）；`ttl_seconds` **>0 生效**（缺省 / 0 = 永不过期；负数 `args_invalid`） |
| `plugin/v1/data.kv.del` | `{work_dir, user?, key}` → `{ok, key, deleted}` | 删除（不存在不报错） |
| `plugin/v1/data.kv.list` | `{work_dir, user?, prefix?, limit?}` → `{keys[], count}` | 列举（`limit` 缺省 100 / 上限 1000）；**不返回过期项**（可惰性清理） |
| `plugin/v1/data.kv.del-prefix` 🆕（G-104） | `{work_dir, user?, prefix}` → `{ok, prefix, deleted}` | **前缀批量删除**：`prefix` 必填非空（空前缀 `args_invalid`，防误删全量）；只作用于**当前**命名空间；`deleted` = 删除条数 |
| `plugin/v1/data.bucket.ensure` | `{work_dir, user?, name}` → `{ok, bucket, created}` | **建桶**（幂等；新建必写审计） |
| `plugin/v1/data.bucket.list` | `{work_dir, user?}` → `{buckets[], count}` | 桶元数据（不含路径 / 句柄） |
| `plugin/v1/data.bucket.query` 🆕（G-104） | `{work_dir, user?, name, prefix?, limit?, offset?}` → `{items[], total}` | **桶内查询**（只读、分页 + 可选前缀）；`limit` 缺省 50 / 上限 500；桶不存在 `not_found` |
| `plugin/v1/prj.users.list` | `{work_dir}` → `{work_dir, users:[{user, db_present, dialog_count, last_active}], count}` | C16：只回 user 标识与基础元数据（只 stat / 数文件，**不读会话内容**） |
| `plugin/v1/session.export` | `{work_dir, user?, session_id}` → `{ok, export}` | **会话只读导出（G-103，2026-11-03）**：JSON **内联返回**（不落文件），导出体含 `session_id` / `dialog_id` / `exported_at` / `session` / `turns`（各自嵌套 messages）；`session_id` 必填；跨用户按 G-102 矩阵放行 + 审计；不存在 → `not_found` |

**KV TTL（G-104）落盘口径**：值以信封 `{__ck_kv:1, value, expires_at?}` 存于 `<key>.json`
（`expires_at` = Unix 秒，0/缺省 = 永不过期）；**旧数据 = 裸 JSON 值 = 永不过期**（向后兼容）。
`get` / `list` 读到过期项按「不存在」处理并**惰性删除**；`list` 不返回过期项。

**桶记录形状（G-104 最小化定义）**：桶目录 = `<插件数据根>/<id>/buckets/<hash(work_dir)>/<user>/<name>/`，
记录文件 = `records.jsonl`（**一行一条 JSON，追加写**），每条：

```json
{"id": "r1", "key": "alpha-1", "value": {...}, "created_at": "2026-11-03T00:00:00Z"}
```

- `id` 必填（调用方保证唯一）；`key` 可选（`prefix` 过滤优先作用于它，缺省退回 `id`）；
  `value` 任意 JSON；`created_at` 可选（RFC3339）。
- `data.bucket.query` 按追加顺序返回，`total` = 命中前缀的总条数（分页前），`items` = 当前页。
- 本轮**只提供只读查询**（记录写入由后续能力 / 宿主侧产物按该形状追加）。

**审计与轮转（G-102 技术部分）**：

1. 所有**跨用户读 / 导出**与**建桶**写审计（`data.bucket.query` 只读、不写审计，跨用户除外）。
2. 审计落 `<插件数据根>/_audit/audit.jsonl`（JSONL 追加，**不写会话库**），
   记录 `{at, action, actor_user, plugin, work_dir, target_user, result, reason?}`。
3. **轮转**：写入前检查大小，**超过 8 MiB** → 轮转为 `audit.jsonl.1`，最多保留 **3** 个归档
   （`.1/.2/.3`，超出删除最旧的）；纯本地文件操作、未超限不动作（幂等）；失败只留诊断、不阻断能力。
4. 留存**策略**（是否可关闭 / 项目级授权 / 留存期）仍属 [41 G-102](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决) **待拍板**。

**授权强制**：

1. **键空间** = `(插件 id, work_dir, user)`；插件 id 取自 `plugin.hello` 会话身份（调用方不可伪造）。
2. `user` 缺省 = 当前 OS 用户（`layout.UserDirName()`）；**显式跨用户 = 仅「历史记录读取」类放行**
   （G-102，2026-11-03：`session.get` / `session.history` / `session.export`；KV / 桶的读、写仍 `auth_required`；
   判定落点 = `plugindata.Store.resolveUser`，`Options.Authorize` 为宿主级追加口）；**不模拟登录、不提权**。
3. 落盘位置（插件私有、**不污染会话库**）：
   - `<插件数据根>/<id>/kv/<hash(work_dir)>/<user>/<key>.json`
   - `<插件数据根>/<id>/buckets/<hash(work_dir)>/<user>/<name>/_bucket.json`
   - `<插件数据根>/<id>/buckets/<hash(work_dir)>/<user>/<name>/records.jsonl`
   - `<插件数据根>/_audit/audit.jsonl`（+ `.1/.2/.3` 轮转归档；`<插件数据根>` = `<DataDir>/plugins`）
4. 桥只做参数校验与转发，**不向插件暴露任何库句柄 / 库文件路径**；宿主实现 = `src/lib/llm/plugindata`。
5. 未注入实现 → `method_not_found`（插件按「能力不可用」处理，不得自行开库兜底）。
6. 消息面：**UDS 方法面，零新增 MQ 主题**（登记见 [61 §7.1](../60-reference/61-消息一览.md)）。

## 4. 内部控制流

```text
server.Start
  → persist.Start → gw.Start → im.Start → 方法面/事件面订阅 → 域工具/agent 注册 → 预热 ListTools
  → 插件按序 Start（每个 Start 内：实例视图 + 事件订阅 + 自检）
  → 全部成功 → 广播 server-starting
```

- 插件各自持 `instance.Manager`（不共享 server 的实例表）。
- 宿主侧插件桥**不自持**实例视图：`pluginhost/bridge` 的准入 / 实例解析 / 补发一律查 server 注入的 `instance.Manager`（2026-10-29 内存单源化，见 §1.1 末条）。
- **codegraph 无 `syncTools` 单飞**：查询工具为静态 MCP hot 注册，不按 workdir 动态注册/注销（2026-11-04 核对）。

---

## 5. 数据结构与存储

| 插件 | 数据 | 载体 |
|------|------|------|
| ~~compress~~ | **已内建在会话侧**（非插件）：压缩快照（session `history`/`snapshot_turn`） | prjusr 库（经 `data-snapshot-*`） |
| memory | 记忆类别全文（每类别一份沉淀全文 + 类别清单） | 记忆库（persist memory 域，经 `data-memory-list/read/save`）；**提取进度**（每 (会话, 类别) 一份「最后已提取 turn」） | **prjusr 专用表 `memory_extract`**（记录 `{session_id, category, last_turn_id, updated_at}`、主键 `<会话>\x00<类别>`；经 `data-memory-extract-{load,save,delete}`；OP-05/06） |
| history | 检查点链（`commit-tree` 产物，parent 线性串联，ref 只指向链头）；状态 `history.status.<slug>` / 时间线 `history.timeline.<slug>`（**会话级内部键**，prjusr 库） | workdir 的 `.git`（独立 ref `refs/chonkpilot/<slug>` + 持久临时 index `<workdir>/.chonkpilot/history/index`） |
| codegraph | 索引 | `<workDir>/.chonkpilot/codegraph/index.db`（bbolt 单文件库；元信息 `meta.json`）；**运行态不落 prj 键**（`codegraph.status` 已退场，经插件动作 `engine-status` 现取） |
| vfts | 全文索引 + 文件清单 | 引擎自持索引（`zvec`，按 workdir，进程内）；插件侧清单 `<workDir>/.chonkpilot/vfts/file_list.json`；**运行态不落 prj 键**（`vfts.status` 已退场，经 `engine-status` 现取） |

---

### 5.1 插件日志：全局一份 + 按项目镜像（PLUGIN-LOG-1，2026-10-31 落地）

> 背景：一个插件进程服务**全部项目**，日志原先集中落一个文件，无法按项目区分（G-83 ④ 后续）。

| 项 | 口径 |
|----|------|
| 全局日志 | `<插件平台数据根>/plugins/<id>/logs/plugin.log`（desktop 缺省 = `%LOCALAPPDATA%\chonkpilot`）。**逐字保留插件进程写出的全部字节**，行为与改造前一致 |
| 项目镜像 | 含 `wd=<workdir>` 标签的行**镜像追加**到 `<workDir>/.chonkpilot/users/<OS 用户名>/logs/plugins/<id>.log`（= prjusr 数据根 `logs/plugins/`）。无 wd 的行**只留全局**；**不**为每项目起独立插件进程 |
| 标签写法 | 工作在某个 workdir 下的日志行统一带 `wd=<绝对路径>`（**迁移期兼容历史写法 `workdir=`**）；路径含空格时加双引号（`wd="E:\My Proj"`）。**唯一出口 = SDK**：`pluginsdk.Plugin.Logf(workDir, format, args...)`（内部 `TagWorkDir` 追加标签，已带标签不重复）；插件内的 `plugin.Deps` 走 `LogfWD(workDir, …)`（nil 回落 `Logf`，只进全局）。slog 文本输出的 `\\` 转义与整条 msg 引号由宿主还原 |
| 落盘实现 | 宿主 spawn 时 `cmd.Stdout/Stderr = io.Writer` 分流器（框架 `mgr/logmirror.go`）：先写全局文件，再按行解析 wd 并镜像；**跨 Write 的半个行**、无换行残行（进程退出时）均正确处理；镜像目录懒创建（`MkdirAll`），进程重启**追加不覆盖**；单行超 64KB 放弃解析（只留全局） |
| 安全边界 | 框架只认**绝对路径**，且宿主回调 `mgr.Options.ProjectLogPath` 再判「`<workDir>/.chonkpilot` 存在 = 已打开项目」才返回路径；插件 id 取 basename —— 日志文本无法诱导宿主在任意位置建目录 |
| 日志窗口 | `/api/log/sources`（宿主 + 已有 `plugin.log` 的插件）+ `/api/log/tail?source=host\|plugin:<id>` 不变；窗口新增**「全部项目 / 某项目」**下拉（选项从当前日志文本的 `wd=` 标签收集，纯前端过滤，**零 MQ 变更**） |
| 迁移状态 | codegraph（8 处）/ history（7 处）的 workdir 维度日志已改走 SDK 出口（`logfFor` / `logfWD`）；其余插件**无 workdir 维度日志行**（配置 / 启动类日志只进全局），故无待迁项 |

---

## 6. 依赖与仓库分区（2026-10-24 分离）

**仓库分区**（进程外插件已与主仓分离）：

- **`chonkpilot-framework/plugin`**（独立仓，module path 仍 `github.com/lsong98sh/chonkpilot-plugin-framework`）：
  - `protocol/`：帧编解码（原 `src/lib/core/transport`）+ 子进程 `SysProcAttr`；
  - `contract/`：`*.tool.md` 契约解析（原 `pluginsdk/contract`）；
  - `sdk/`：进程外插件作者侧 SDK（原 `src/lib/plugin-sdk/pluginsdk`，含 `engineclient`）；
  - `runtime/`（2026-10-25 新增，插件侧运行时）：`bus`（原 `core/mq`，**唯一总线实现**）· `topics`（插件引用的
    主题常量）· `hook`（原 `src/plugins/plugin/plugins.go`：`Deps/Notice/Hook/Stopper`）·
    `instance`（实例视图 + 心跳）· `dataclient`（data-* 请求-应答）· `bridgebus`（宿主主题子集
    ↔ 插件桥）· `heartbeat` · `datafmt`（`ChatMsg/ToolCall` + token 估算 + `LLMRefName`）；
  - **`plugin/src/{bridge,mgr}`**（2026-10-25 新增，**宿主侧**，不是 `plugin/src/host/`）：`bridge`（插件前门：帧传输/会话/能力面/过滤链/GUI 反向通道，
    原 `src/lib/llm/pluginbridge`）· `mgr`（插件发现/进程守护/配置/UI 资产，原 `src/lib/llm/pluginmgr`）；
    **端口化** = Bus（`runtime/bus`）· ConfigStore（`mgr.ConfigStore`，宿主注入 `chonkpilot.db` 适配器）·
    Process（`protocol.SysProcAttr` + `os/exec`）· Assets（插件 `plugin/v1/ui.asset.get` RPC）；
  - **已发布 `v0.1.2`**（含 `bridge/` / `mgr/`）；主仓与 plugins 仓均按**版本依赖**引用（无本地 `replace`）。**（2026-11-04 核对：目录名以 `plugin/src/{bridge,mgr}` 为准）**
  - **宿主已改用框架契约（2026-10-25）**：`src/plugins/plugin`（Hook/instance/dataclient）与
    `src/lib/llm/{pluginbridge,pluginmgr}` **均已删除**，
    主仓 `src/lib/{appwire,llm,gui}`、`src/desktop`、`plugin-{compress,codegraph,vfts}` 全部改用
    `framework/runtime/{hook,instance,dataclient}`（后三者在 2026-10-27 已随 `src/plugins/` 删除）；`src/lib/core/mq` 收敛为 `runtime/bus` 的**类型别名**
    （总线实现唯一在框架）；插件桥 / 管理器改用框架 `plugin/src/{bridge,mgr}`（import 别名保持旧名）。
  - 依赖 = 标准库 + MCP go-sdk，**零主仓依赖**。
- **`chonkpilot-plugins`**（独立仓）：`browser` / `codegraph` / `crontab` / `desktop` / `file` / `task` / `mcp` / `web` / `memory` / `history` / `remote`（**11 个**），**每个插件一个 module**；`memory` / `history` 的 Go 业务包在同仓 `<id>/src/lib`；`browser` / `desktop` 的**引擎**在同仓 `<id>/src/engine`；`codegraph` 的**引擎**在同仓 `codegraph/src/engines/{treesitter,vfts}`；`remote` 分 `client/`（插件）与 `server/`（服务端）。**`shared/` 不存在**（共享工具已上收 `chonkpilot-util`）。**（2026-11-04 核对）**
  - **2026-10-25（能力随插件）**：browser / desktop 引擎、codegraph / vfts 引擎、**file 域 11 工具实现与契约**的**源码已全部迁出主仓**（原 `src/lib/mcp-tools/{plugins/*,internal/*,toolset}`、`src/plugins/{codegraph,vfts}`、`src/initdata/capability/tools/file/`）——主仓只保留 gateway / mcp-server / data / config 等平台能力（**插件宿主 bridge/mgr 在框架 `plugin/src/{bridge,mgr}`**，非 `host/`）；`file_*` 由 gateway 调插件 MCP server 执行（主仓 builtin 无 file 分派，`chonkpilot-tools-executor.exe` 已删除）。
  - `browser/engine` 的 WebView2 绑定 = 同级 fork 仓 `chonkpilot-webview2`（**唯一源**；上游无 `EvalWithResult`/`Screenshot`）；**2026-10-25**：去除标题栏 / move / resize 已封装进 fork（`WebViewOptions.Frameless` + `WindowResizer.StartResize` + `HitCode(hit)`，宿主不再持 Win32 命中码表）。**2026-10-30**：插件仓 `third_party/go-webview2` 内嵌副本已删除（与 fork 仓逐字节一致，仅 FORK.md 台账文字不同），`browser/src/engine/go.mod` 改 `replace => ../../../../chonkpilot-webview2`；待独立仓发布后改按版本引用。
- **主仓零插件（2026-10-24 定稿）**：全部业务插件（含 codegraph / vfts 引擎、compress 纯算法、memory / history 业务包）在**同级仓 `chonkpilot-plugins/`**；主仓 `src/plugins/` 整个目录已删除。引擎构建 `build-codegraph.ps1` / `build-vfts.ps1` 的源码取自插件仓。

**依赖方向**：

- 进程外插件（**11 个**，`chonkpilot-plugins/<id>`）→ framework `sdk` / `runtime/*` + `chonkpilot-util/*` + 同仓 `<id>/src/lib` · `<id>/src/engine` · `codegraph/src/engines/*`（**2026-10-25 起零主仓 `replace`**）。**（2026-11-04 核对）**
- `memory` / `history` 业务包（`chonkpilot-plugins/<id>/src/lib`）→ **仅 framework `runtime/*`**（`hook` / `bus` / `topics` / `instance` / `dataclient` / `datafmt` / `heartbeat`）。
- 进程内 Hook 接口 / 实例视图 / dataclient（宿主侧）→ **framework `runtime/{hook,instance,dataclient}`**（主仓以版本依赖引用）。
- `src/plugins/plugin-compress` → **已于 2026-10-27 删除**（压缩自 SESSION-1 起内建在 `src/lib/llm/server/compress.go` + `chonkpilot-agent/session` 压缩内核）。
- ~~`src/plugins/plugin-codegraph` / `plugin-vfts`~~ → **已随主仓 `src/plugins/` 一并删除**（插件仓承载全部实现）。
- 内嵌 Hook 已全部退场（`appwire.DefaultPlugins` 返回 nil，2026-10-27）；进程外插件由框架 `host/mgr` 按 manifest 拉起（gui / server 共用）。
- 构建：`build-plugins.ps1` 的 **11 个**进程外插件源码**全部**取自同级 `chonkpilot-plugins`（第 11 个 = `remote/client`，exe `chonkpilot-plugin-remote.exe`）。**（2026-11-04 核对）**

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 形态：**进程外 exe（唯一形态）** | 走插件桥 `plugin/v1` + MCP 工具面；原进程内 Hook 已于 2026-10-27 删除 | 插件可独立发版 / 崩溃隔离；`appwire.DefaultPlugins` 返回 nil |
| 无宿主函数注入 | 统一走 mq（`llm-simple`） | 解耦、可测试 |
| history 纯 git | 废弃 history.db | 复用版本控制、零额外存储 |
| **history = 独立 ref 的检查点链** | 打点走 `refs/chonkpilot/<slug>` + 持久临时 index（`GIT_INDEX_FILE`）；**绝不碰 `.git/index` / HEAD / 用户分支** | 不再污染用户仓库的提交历史；检查点对用户分支不可见、可随时丢弃；`commit-tree` 线性链天然支持相对步 diff/回滚 |
| codegraph / vfts 引擎**库内联** | 引擎以 Go module 编入 `codegraph` 插件进程（`replace` + 直接 import），不 spawn 子进程 | 同进程共享 workspace/索引状态；独立 exe 仅保留构建自检（发行默认不投放） |
| 插件就绪 = server-starting | 全部 Start 成功才广播 | 明确就绪语义 |
| **插件失败 = 上报 + 可见 + 可查** | 失败路径经可选 **`Deps.Notify(Notice)`** 如实上报一次 → 宿主 **`Server.pluginNotify`**（`src/lib/llm/server/pluginnotice.go`）**去重/限频**后经**既有通知面 `tool-notify`** 投递 **`notice="plugin-failure"`**（`payload` 只增字段，[61 §4.3](../60-reference/61-消息一览.md) 已登记）；**不阻塞**（仅 publish、不等应答、不改终态、不降级、不抛出）。**去重口径** = 键 `实例×会话×轮次×插件×失败类别` → **同轮同类只提示一次**（轮次或类别变化才再提示；判重表有界 FIFO 淘汰、上限 512）；**失败原因**同经 `Deps.Logf` → 统一出口落 `<dataDir>/logs/gui.log`（含原因），提示文案亦带原因（缺因回落"原因未知（详见日志文件）"） | 不再"记忆没沉淀 / 上下文没压缩"却无从诊断；**不新增 MQ 主题**（复用既有通知面）；前端零改动（`ChatPanel` 既有 `tool-notify` 订阅对非 `completion` 即以轻提示展示、`MessageList` 不落气泡） |

---

## 8. 场景与边界

- **无 git** → history 禁用（不失败；`Start` 记日志后 `return nil`）。
- **非 git 仓库** → history 功能禁用（`hasGit=false`）且 **4 个工具摘除**；即使 `history.enabled=true` 也不注册、不打点。
- **history 打点失败** → 前置钩子路径**拒绝该工具调用**（工具不执行，LLM 可见并能重试）；**连续失败 ≥3 → 熔断放行**（不再拦截，成功一次即复位）。
- **history 未启用 / 不脏 / 非 git / 标「不涉及文件变动」** → 前置钩子**零 git 调用**直接放行（不打点、不拦截）。
- **history 按会话** → `history.status.<slug>` / `history.timeline.<slug>` 为**会话级**内部键（**多会话并发各自独立、互不覆盖**）；`history.clear` 写 JSON `{ts, session}` → **只清目标会话的链**（其它会话链保留）。
- **摘要失败** → compress 不压缩（保持原快照）。
- **插件失败** → **用户可见 + 日志可查，且不阻塞**：`memory`（记忆沉淀）/ `compress`（上下文压缩）等失败经 `Deps.Notify` 上报 → **`memory` 外挂后由插件自身按同口径直接 `Emit(tool-notify{notice:"plugin-failure"})`（宿主 `pluginNotify` 仍服务内建压缩等进程内失败面）**，判重后轻提示一次（**同轮同类一次**，见 §7），同时失败原因经统一出口落 `<dataDir>/logs/gui.log`；**本轮对话不受影响**（不改流程终态、不降级、不抛出）。
- **多 workdir** → codegraph **工具面**仍全局注册一份（归属排序后第一个 workdir，按 `context.instance_id` 路由）；**引擎子进程/索引状态已按 workdir 独立**，仅**工具面差异注册留待 v2**。
- **实例崩溃** → **显式 `instance-exit` 清理**；`-tags split` 编入心跳超时判定（90s）→ 既有 `instanceGone` 兜底清理；唯一形态 desktop 只走显式 `instance-exit`，见 §2 实例视图。
- ⏸ **上下文配置**（历史轮次上下文的带出 + 上下文的分类与内容沉淀，与 compress/summary 的关系待定）见 [40-演进计划](../40-roadmap/40-演进计划.md)（演进项，暂不开发）。

---

## 9. 现状与待办

- ✅ **compress 阈值已读项目配置** `keep_full_max_turns`/`keep_full_max_tokens`/`compress_token_threshold`（prj 库 config；`compress.go` `resolveOpts`），缺失/非法回落 `DefaultOptions{10, 24000, 20000}`（[02-配置层级](../00-overview/02-配置层级.md) §5）。`keep_full_max_turns`（保留段轮数上限）/ `keep_full_max_tokens`（保留段 token 上限）/ `compress_token_threshold`（**简化区阈值**）。边界算法上提为 `data.LocateFullTurnCount`（压缩侧/组装侧**共用**）；**「本轮（最新轮）恒保留完整」下限落进共享算法**（压缩侧与组装侧一致，**不单侧钳制**）；取值语义 = `0` 不启用该条件、**两者均 0 → 不压缩**、负数非法（前端提示 + 后端留日志）。轮次结束时预存该轮 `full_tokens`/`brief_tokens`（`data-session-complete-turn` 只增字段），判定/拼接可直接累加（缺值回退实时估算，`data.ResolveStoredTokens`）。
- ✅ **心跳超时退出判定 = 编译开关**：`instance.Manager.Sweep(timeout)`（90s）**仅在编入 `-tags split` 时生效**（`plugin-framework/runtime/instance/heartbeat_split.go`），**唯一形态 desktop 为空实现**（`heartbeat_inprocess.go`）——实例失效只走**显式 `instance-exit`**（`instance-heartbeat` 订阅保留）。插件侧**无引擎子进程**（引擎库内联），故不再有 `sweepIdleClient`。详见 §2 实例视图。
- ✅ **codegraph / vfts 引擎库内联在同一插件进程**（D-71，2026-10-27）：按 workdir 隔离索引状态，不 spawn stdio 引擎子进程、无独立 indexer/查询 executor（PLUGIN-EDGE-1，2026-10-28 移除查询 executor 构建）。见 §3.3/§3.4 与 [29-codegraph §4.5](29-codegraph.md)。
- ✅ 压缩已内建在会话侧（SESSION-1），其全链路由 `src/lib/llm/server/compress_test.go` / `compress_notice_test.go` 覆盖；`TestCompressPluginEndToEnd` 已删除（插件形态退场）。
- ✅ compress/summarize.go 的 `LLMSummarizer`（HTTP 摘要器）已删（运行时走 mq `llm-simple`）。
- ✅ **history 门控 + compress 摘要 system**：history 插件读 prj `history.enabled` 门控（**检查点链打点**，见 §3.2/§7）；compress 按级读 `capability/system/summary.md`（纯文本）作摘要 `system`（OP-01/OP-02）。
- ✅ **history = 独立 ref 的检查点链**：打 `refs/chonkpilot/<slug>`（根会话；父子共享）+ 持久临时 index（`GIT_INDEX_FILE`，**绝不碰 `.git/index` / HEAD**）；触发 = gateway **前置打点钩子**（`tools/register.pre_hook_subject` → `history-pre-tool-hook`，打点失败即拒绝该工具调用）+ `session-complete` **轮末补点**；脏位来自 `filesys.changed`（不脏零 git 调用）；连续失败 ≥3 **熔断**放行、成功复位；`keep`/`ttl` 任一超限**修剪**（锚点 = 链上最新点时间，保底项不删）；`history.enabled` **仅显式 `"true"` 开（默认关闭）**；git 不可用 / 非 git 仓库 → 禁用 + 4 工具摘除。消息面见 [61 §8](../60-reference/61-消息一览.md)，配置键见 [64 §4](../60-reference/64-配置项一览.md)，设置页见 [20-gui §12.12](20-gui.md)。
- ✅ **vfts 共享引擎子进程/空闲回收已退场**：vfts 引擎库内联在 codegraph 插件进程，`sharedClient` / `sweepIdleClient`（原 5min 回收）随 D-71 一并删除。详见 §3.4。
- ✅ **codegraph / vfts 运行态不回写 prj 键**（PLUGIN-UI-2）：原 `codegraph.status` / `vfts.status` 回写与 `phase`/`progress*` 快照已退场；进度/状态经插件动作 `engine-status` / 状态栏 `badge` 现取（与落盘索引同源），零新增 MQ 主题。详见 §3.3/§3.4 与 [29-codegraph §4.5](29-codegraph.md)。
- ✅ **记忆手动沉淀 = 新增点分相对主题 `memory.flush`**：`memory` 插件除既有的 `session-compress`（轮末**异步**沉淀、受 `memory.min-turn-tokens` 门控）外，另订阅点分主题 **`memory.flush`** —— payload `{instance_id, session}`，取该会话**最近一轮**消息 → **复用同一沉淀回路 `distill`**（启用门控 / 类别门控 / 读改写保存**完全同源**），**不受 `memory.min-turn-tokens` 门控**（显式动作即用户意图）；**同步回执** `{ok, session, turn, saved[], failed[], enabled}`（前置不满足 → `{ok:false, reason}`，不静默）；作用域 = payload 指定的 instance + session（不跨实例 / 不跨会话）。前端 = 设置 → 上下文管理「立即沉淀」。明细见 [61 §1.1/§7.1](../60-reference/61-消息一览.md)。
- ✅ **「压缩内容可查」= 复用既有快照面，零新增存储**：上下文压缩的产物**唯一落点** = 会话快照（prjusr `sessions` 表 `history`/`snapshot_turn`；**内建压缩**回写），故设置 → 上下文管理「压缩内容」小节**直接读既有 `data-snapshot-get`**（首条 system `[已压缩早前对话] <摘要>` = 摘要原文），**不新增主题 / 不新增存储**；**压缩时间不可得**（快照不记时间）→ UI 如实说明。口径见 [61 §3.2b](../60-reference/61-消息一览.md)。

---

## 10. 关联测试

`src/test/chonkpilot-plugin/unittest/instance/manager_test.go`（实例视图）· `src/lib/llm/server/plugins_test.go`（`server-starting` 广播 / 装配机制；压缩链路由 `compress_test.go` / `compress_notice_test.go` 覆盖）· 插件仓 `chonkpilot-plugins/history/src/lib/chain_test.go`（检查点链内核：不碰用户分支 / 不脏零 git / 门控 / 修剪 / 熔断 / `history_restore`）· `chonkpilot-plugins/codegraph/src/{config_test.go,webui_asset_test.go,sessionindex_test.go,sessionreconcile_test.go}`（配置迁移/徽标/动作面 / webui 资源 / 会话索引 / 后台对账；**`tools_mcp_test.go` 不在 codegraph 仓**）· `chonkpilot-plugins/file/src/tools_mcp_test.go`（11 个文件工具）。**（2026-11-04 核对）**

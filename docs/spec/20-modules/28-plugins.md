# 28 · 插件体系（src/plugins/plugin*）

> 状态：✅ 与代码一致
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [21-llm-server](21-llm-server.md) · [29-codegraph](29-codegraph.md)
> 代码目录：`src/plugins/plugin/`（接口 + 实例视图）· `src/plugins/plugin-compress/` · `src/plugins/plugin-history/` · `src/plugins/plugin-memory/` · `src/plugins/plugin-codegraph/` · `src/plugins/plugin-vfts/`

---

## 1. 职责与边界

- **一句话**：**内嵌 lib Hook**——编译期打进 server，在 `server.Start` 全部就绪后按序 `Start`，订阅 MQ 事件做扩展（压缩 / 文件历史检查点 / **记忆沉淀** / codegraph·vfts 工具注入）。
- **做**：事件订阅、按 instance/workdir 自持数据（经 `src/lib/data`）、向 gateway 注册/注销工具。
- **不做**：不依赖宿主函数注入（统一走 `llm-simple` 等 mq 方法面）；不独立成进程（🔵 待去中心化 MQ）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 形态 | 内嵌 lib（server 进程内、同一 `mq.Bus`） |
| 接口 | `Hook{ Name() string; Start(Deps) error }`；`Deps{ Bus mq.Bus; Logf func(format, ...) }`（另含可选 `Notify func(Notice)`：失败上报，nil = 静默） |
| 启动 | `server.Start` 中 **compress → memory → history → codegraph → vfts** 按序（装配处 `src/lib/gui/main.go:712-718` / `src/server/main.go:111-117`）；**全部成功后才广播 `server-starting`**（就绪信号） |
| 数据访问 | 走 `src/lib/data`（`data.Prj(instanceID)` 或 payload 自带 work_dir/data_dir 自登记）；work_dir 解析走 `instance.Manager` |

**实例视图**（`src/plugins/plugin/instance/manager.go`）：订阅 `instance-register` / `instance-heartbeat` / `instance-exit`，维护 `instance_id ↔ {ID, WorkDir, DataDir, ClientType, LastBeat}`（`LastBeat` 仅记录最近心跳/注册时间）；提供 `Lookup/List/Count`。
> ⚠️ **心跳超时退出判定 = 编译开关**：心跳发布与超时判定**仅在分离形态（`-tags split`）编入**；合并单进程形态（默认构建）为空实现（`heartbeat_inprocess.go`：不发布、不判超时），实例失效以**显式 `instance-exit`** 清理（`instance-heartbeat` 订阅保留）。分离形态下：`StartHeartbeat`（注册后起 30s 周期发布 `instance-heartbeat{instance_id}`）与 `Manager.Sweep(timeout)`（**90s = 3× 周期**的实例视同退出）的生产调用方 = 判定侧消费方两处 —— **llm-server** `startInstanceSweep`（`src/lib/llm/server/sweep_split.go`：30s ticker → `Manager.Sweep(90s)` → 逐个走既有 `exitInstance`：取消 running turn + 释放 `busy` 锁 + 注销 capability dir 节点）、**persist** `startStaleSweep`（`src/lib/data/persist/sweep_split.go`：30s 扫描 → `LastBeat` ≥90s → 既有 `instanceGone`：移除 `insts` 绑定 + `data.Unregister`）；插件 codegraph/vfts 的 `sweepInstances` 走各自 `instanceGone`（解绑 + 递减 workdir 引用）**均存在**（`src/plugins/plugin/instance/heartbeat_split.go`）。阈值单一来源 = `src/lib/heartbeat`：`persist` 的 `staleTimeout = heartbeat.Timeout`（90s）、llm-server 的 `sweepInterval = heartbeat.Interval`（30s）、`plugin/instance` 的 `HeartbeatInterval`/`HeartbeatTimeout` 亦为同名常量别名；改值只需一处，见 [24-lib §2/§6](24-lib.md) · [61 §4.1](../60-reference/61-消息一览.md)。

---

## 3. 各插件

### 3.1 compress（历史压缩）

| 项 | 值 |
|----|----|
| 订阅 | `session-compress`（server 终态落库 + 写快照后发；载荷另有**可选** `max_context_token` / `max_output_token` 字段，见下） |
| 流程 | 读快照 → **三段定位**（`LocateThreeZones`，算法唯一来源 = `data.LocateZones`，**与组装侧同一函数**）：**完整区**（本轮 + 最近 N 轮，N/M 取先到，原文）/ **简化区**（再往前，**brief token 累计 ≤ T**，【简化态原文】= 仅 text）/ **摘要区**（更早）→ **摘要区非空**才压缩：**经 `llm-simple` 摘要摘要区（并入既有摘要）** → 回写快照（`snapshot_turn` 不变）。快照结果 = `[摘要] + 简化区（原文保留）+ 完整区（原文保留）`；简化区/摘要区**在发送时**由组装侧投影为【简化态原文】（`data.BriefMessages`，不落库、不丢字段） |
| **兜底归并**（口径 Y/Z3） | 触发：`摘要 + 简化区 + 完整区 + maxOutputToken > maxContextToken`（**真窗口 `maxContextToken` > 0 才启用**，缺省 0 = 不启用；输出预留 = `maxOutputToken`，**不再用常量 4096**）→ 处置：把【既有摘要 + 简化区全部原内容】（含待摘要的更早轮）合并 → **交同一 `llm-simple` 摘要路径重新生成摘要**（目标长度 = **`maxOutputToken` 的 1/2**，以长度要求追加到摘要输入）→ 结果 = `[新摘要] + 完整区`（**简化区清空**）；下一轮简化区自 0 重新累积。**幂等/可重入**（每次都自入参快照重算，重试不膨胀）· **失败回退**（摘要失败 → 快照原样返回、原文不丢）。**摘要无独立配置项**（目标 = 输出上限 1/2） |
| 失败策略 | **摘要失败直接不压缩**（不降级；含兜底归并失败 → 回退未归并状态） |
| **子会话门控（DSL-4，42 §2 (253)）** | 项目级 `compress.subsession`（**默认关闭**）：关 = **子会话**（会话行 `parent_id != ""`，经 data 门面 `SessionGet` 判定）turn **跳过压缩**（worker 内判定，不压缩、不广播 `compress-*` 进度 → 子会话不入队列状态展示）；**主会话恒不跳过**。开关开启 = 子会话照常压缩。另有 server 侧读点（`src/lib/llm/server/turn.go compressSubsessionOn`）：子会话压缩关闭时 LLM 请求失败（上下文超限，多为不可重试的协议错误）**追加可诊断提示**。**指引块（读路径）与宿主形态无关**（CLI 亦不关）。见 [64 §4.1](../60-reference/64-配置项一览.md) |
| 处理模型（OP-03） | **非阻塞、每会话串行**：`session-compress` 的**订阅回调只置入队并立即返回**（不在回调里同步跑压缩）——避免阻塞发终态的 turn 收尾 goroutine（`tc.Close()` 未及时执行 → 同会话 `session busy`），会话锁及时释放。每会话一个 **worker** 串行消费（同会话不并发、不重入），队列空即退出（下次入队再拉起）。同会话在压缩进行中再来的事件**合并**（只跑最后一次；压缩自最新快照幂等重算）。进程退出时未完成任务**允许丢弃**（不阻塞退出；Hook 契约无 Stop）。**已知副作用（接受）**：异步后**下一轮 `msgs()` 可能读到未压缩快照**（上下文暂时偏大），不引入「下一轮等待压缩」 |
| 进度通知（OP-03） | 入队（`compress-queued`，仅当同会话 worker 已在跑）/ 处理**前后**（`compress-start` / `compress-done`）各经**既有通知面** `tool-notify` 投一条（**不新增 MQ 主题**，见 [61 §4.3](../60-reference/61-消息一览.md)）：载荷 = `{instance_id, session_id, turn_id, notice, message, message_id, plugin:"compress"}`。前端 `ChatPanel` 据此显示**会话级**「正在压缩上下文」指示（i18n `chat.compressing`，`composables/useCompressStatus.js`），`compress-done`（或切换会话）后消失；**statusbar** 亦按 `compress-*` 显示压缩队列状态（`composables/useQueueStatus.js`）。**与 `session-compress` 区分** —— 后者是触发信号（每轮触发、前端无展示，前端 type `llm-compress` 仅用于刷新「压缩记录」） |
| 阈值来源 | 读项目配置 `keep_full_max_turns`/`keep_full_max_tokens`/`compress_token_threshold`（prj 库 config；`compress.go` `resolveOpts`），缺失/非法回落 `DefaultOptions{RetainTurns:10, KeepFullTokens:24000, TokenMax:20000}`（见 [02-配置层级](../00-overview/02-配置层级.md) §5）。**`MaxContextToken` / `MaxOutputToken` 都不是配置项**：运行时由 server 经 `session-compress` 载荷的 `max_context_token` / `max_output_token` 透传（各自 = provider 对应字段 `maxContextToken` / `maxOutputToken`） |
| 完整区定位 | `LocateThreeZones`（**口径 X**）：完整区 = `data.LocateZones` 的 `FullTurns`（**本轮恒保留**，下限 1，即使其自身完整态 token 已超 M 也不降级）；更早轮从尾部逐轮向前纳入，「累计**完整态** token > M」或「已纳入轮数 >= N」的那一轮起不再纳入完整区（算法唯一来源 = `data.LocateFullTurnCount`，**与组装侧同一函数**）。**取值语义（口径 W）**：`N == 0` = 轮次条件不启用；`M == 0` = token 条件不启用；**两者均 == 0 → 不压缩**（完整区=全量、无摘要区）；**负数 = 非法**（后端不猜、按「不启用」处理并留日志，前端显式提示）。**整轮对齐**（`role=="user" && Kind!="notify"/"continue"/"resume"` 为轮边界） |
| 简化区定位 | **T（`compress_token_threshold`）= 简化区 brief token 预算**：自完整区前一轮向前**逐轮累加 `brief_tokens`**（= 【简化态原文】token，构造 = `data.BriefMessages`/`BriefFacadeMessages`），累计 **≤ T** 的轮进简化区；把累计推过 T 的轮起（含）**全部进摘要区**。`T <= 0` → 简化区预算 0（不进简化区、直接摘要）。简化区 token 取**预存值优先、缺值回退实时估算**（`data.ResolveStoredTokens`，尾部对齐双向） |
| token 估算 | `EstimateTokens`：字符数/2 近似；**预存口径 = 完整态（全消息）/ 简化态（`data.BriefMessages` 仅 text）**，见 [21 §4.2](21-llm-server.md) |
| 摘要 system | 按级读**纯文本** `capability/system/summary.md`（项目级 → 用户级 → 系统级，`summarize.go` `resolveSummaryPrompt`）→ `llm-simple` 请求带 `system`；三级皆缺回落 **embed 内置**（`data.SystemDoc("summary")`，= 出厂文件 `src/initdata/capability/system/summary.md`）。提示词 = `capability/system/summary.md`（**纯文本**，无分区 / 无 `.prompt` 后缀；默认改出厂文件 embed 内置）。**设置页保存区分「继承/覆盖」**——内容与继承值（用户文件/系统文件/embed 内置）相同 → **不写覆盖**（删项目级文件、保持继承）；不同 → 写项目级覆盖；另有「恢复默认」（删项目级文件回落继承），见 [22 §3.1](22-data-persist.md) |

> **兜底归并的窗口与输出预留来源**：按两字段拆分 —— **`maxContextToken` = 上下文窗口大小**（仅用于兜底判定）、**`maxOutputToken` = 最大输出 token**（即请求体 `max_tokens`）。`server.finish` 发 `llm-compress` 时经**只增**可选字段 **`max_context_token`** / **`max_output_token`** 透传（`src/lib/llm/server/server.go:1843-1848`），插件读入 `Options.MaxContextToken` / `Options.MaxOutputToken`（**`max_context_token` > 0 才启用兜底**；输出预留 = `max_output_token`，**不再用常量 4096**）；摘要目标 = `maxOutputToken`/2。旧键 `maxTokens` / `max_tokens` **读时兼容**（只读不写）；`maxContextToken` **≤ 0 / 缺省 = 兜底不启用**（仅常规三层压缩）。载荷字段命名统一 snake_case（口径 Z4）—— 见 [61 §4.3](../60-reference/61-消息一览.md)。

### 3.2 history（文件历史：独立 ref 的检查点链）

> **语义**：打**独立 ref 的检查点链**，**绝不碰 `.git/index` 与 HEAD、不在用户分支产生任何提交**。消息面见 [61 §5.1.1/§5.4a/§8](../60-reference/61-消息一览.md)，配置键见 [64 §4](../60-reference/64-配置项一览.md)；设置页见 [20-gui §12.12](20-gui.md)。

| 项 | 值 |
|----|----|
| 订阅 | `session-complete`（**主轮次 → 轮末补点**）· `filesys.changed`（该 workdir 文件变更 → 置「脏」位；广播由 filesys 侧 **60ms 去抖**合并）· `instance-register`（建 workState + 异步回读 `history.enabled`）· `data-prj-config-refresh`（开关 / 保留参数实时同步 + `history.clear` 清链）· **前置打点钩子** `history-pre-tool-hook`（gateway 执行工具前同步调用）· 工具回调 `history-tool-call`。**`session-turn-start` 不再消费** |
| 启用/门控 | git 不可执行（`exec.LookPath("git")` 失败）→ `Start` 记日志 + `return nil`（**功能禁用，不算启动失败**）；workdir **非 git 仓库**（无 `.git`；worktree/submodule 的 `.git` 文件亦可）→ 功能禁用；`history.enabled` **仅显式 `"true"` 才开启（默认关闭）**，缺失 / `""` / `false` / 非法 / 未回读 → 关闭。**禁用或非 git 时 4 个工具一律摘除**（`syncTools` 收敛，判据 = 存在「hasGit 且 enabled」的 workdir） |
| 打点触发 | ① **gateway 前置钩子**（执行**任意**工具前，同步；经 `tools/register` 可选字段 `pre_hook_subject` 声明）：启用 && 未熔断 && **脏** → 同步打点；**打点失败 → 写 error → gateway 拒绝该工具调用（工具不执行），LLM 可见失败并可重试**；**payload 带 `touch_files` = 该工具是否「涉及文件变动」**：显式 `false` → 直接放行、**不打点**（省 8–9 次 git 进程），仍登记会话归属（轮末补点保底）；缺省 / `true` → 打点；② **轮末补点**（`session-complete` 主轮次）：保证「最后一步的产像」入链 —— **不依赖脏位（规避 `filesys.changed` 60ms 去抖竞态）、强制走一次打点流程**，流程内按 tree 比较**内容未变则不建点**。**前置钩子路径不脏 / 标「不涉及」= 零 git 调用**（直接放行） |
| 「涉及文件变动」（打点粒度） | usr `tool_async.<工具暴露名>.touch_files`（布尔；前端「工具配置」页开关；**与异步档位同载体、同页**）。**缺省按工具来源**：**self 节点内置**仅 `filesys_run` / `script_run` 涉及（true），其余内置（file_read/file_find/file_diff/web_fetch/browser_run/desktop_run…）不涉及（false）；**dir 节点 / 第三方 MCP / 无法判定 → 保守按涉及（true）**。生效面 = gateway 前置钩子 payload（`resolveTouchFiles`：显式覆盖 > 来源缺省）。**标错只让检查点粒度变粗**（前后点仍在、`git diff` 一致性校验仍生效），**不丢安全**（见 [64 §3](../60-reference/64-配置项一览.md) `tool_async` / [61 §5.4a](../60-reference/61-消息一览.md)） |
| 流程（打点） | 临时 index `add -A`（尊重 `.gitignore`；进入前 `ensureGitignore` 保证 `.chonkpilot/` 被忽略）→ `write-tree` → **新 tree == 链头 tree → 直接跳过（不建点、不 `update-ref`、不修剪）**；否则 → `commit-tree <tree> [-p <链头>] -m "chonk-ckpt: session=<slug> tool=<tool> ts=<RFC3339>"` → `update-ref refs/chonkpilot/<slug> <commit>` → **修剪** |
| 链与 ref | `<slug>` = **根会话**（优先 `top_session`，缺省 `session`；**父子会话共享一条链**）；链 = parent 指针线性串联（`git rev-list` 可枚举、`git diff A B` 可用）；**ref 只指向链头**（对象靠可达性保活，gc 安全） |
| 存储 | **独立 ref `refs/chonkpilot/<slug>`**（**废弃 `history.db`，纯 git**）+ **持久临时 index** `<workdir>/.chonkpilot/history/index`（经 `GIT_INDEX_FILE` 注入）；**绝不碰 `.git/index`、绝不碰 HEAD** → **不在用户分支产生任何提交** |
| 修剪 | `history.checkpoint_keep`（默认 500）/ `history.checkpoint_ttl_days`（默认 7）**任一超限即修剪**：保留段 = 「最新 keep 个」∩「窗口内」，再并入**保底项（当前轮 / 上一轮起点）永不删**；**天数窗口锚点 = 链上最新点时间**（项目闲置不清历史，不是 `now`）；修剪 = **按保留段重建**（复用 tree/message/时间）→ **commit id 会变，稳定标识是相对编号**；被淘汰点不可达 → 交 git 自动 gc（不主动跑 gc/prune） |
| 熔断 | 连续打点失败 ≥ `fuseThreshold`(3) → `fused`（**仍尝试打点、失败亦放行、不再拦截工具调用**）；**成功一次即复位**；失败次数与模式经 `history.status.<slug>` 回写可见 |
| 内部工具 | **4 个**（`category=self`；启用且是 git 仓库才注册，否则摘除）：`history_status {}` / `history_diff {to?=-1, path?}` / `history_show {to?=-1, path}` / `history_restore {path, to?=-1}`（**单文件、禁止批量**；一致性校验 = 文件已删除 **或** `git diff --quiet <链头> -- <path>` 为 0，否则拒绝）。`to` 三态 = 负整数相对步（-1 最近一步、-2 再上一步…）/ `"turn-start"` 当前轮起点 / 绝对 commit id（须在本链上） |
| 状态回写（前端只读） | **会话级**内部键：`history.status.<slug>`（`{enabled, mode: active\|fused\|off, repo, dirty, failCount, checkpointCount, bytes, lastCheckpointAt, lastDurationMs, lastError}`）/ `history.timeline.<slug>`（**最新在前**、相对编号 `-1` 起、**≤200 条**）——`<slug>` = 链根会话，**多会话并发各自独立、互不覆盖**；落 **prjusr**（本机可重建派生物）。`history.clear`（前端写 JSON `{ts, session}`）→ **只清目标会话的链**（`update-ref -d refs/chonkpilot/<slug>`）并回写该会话空状态，其它会话链保留 |
| 语义边界（有意为之） | 被 `.gitignore` 忽略的文件**不进检查点、也回滚不了**（含 `.env`/`node_modules`/`.chonkpilot/` —— 不把密钥写进对象库）；**未跟踪文件**回滚时**不动**；回滚以检查点 tree 为准、**不碰 `.git`**；检查点遵守 git 的 ignore，与「索引排除规则」（`lib/ignore`）是**两套独立规则、不联动** |

> ⚠️ **效率注意**：文件历史在**每次工具执行前**打点（脏才打点）。**每次「脏」打点约需 8–9 次 git 进程**（写路径 + 状态/时间线回写的只读 git；逐行走查合计约 10–11 次，以实跑采数为准），**修剪还需按保留段重建**（仅在 `keep`/`ttl` 超限时发生）—— **大工程需慎重**（**默认关闭**）。已配「脏标记短路（不脏零 git 调用）+ **「不涉及文件变动」工具直接跳过**（省下每次调用的 8–9 次 git 进程）+ 前置钩子同步打点 + 连续失败熔断 + 固定提交身份（不读用户 git config）」。见 [49-待实现项](../40-roadmap/49-待实现项.md)。

### 3.3 codegraph（代码语义索引，插件侧）

| 项 | 值 |
|----|----|
| 订阅 | `instance-register`/`heartbeat`/`exit` · `data-prj-config-refresh` · 工具回调 `codegraph-tool-call` |
| 配置键 | `enable-codegraph`（可见性门控）· `codegraph.exts` / `codegraph.skip-dirs` / `codegraph.stack-gitignore`（索引配置，变更即强制重建）· `codegraph.status`（状态回写）· `codegraph.action`（重建/清除/重试信号） |
| 引擎 | 独立 exe `src/plugins/codegraph`（stdio 子进程），按 workdir 管理；**每 workdir 独立 client/子进程**（`p.clients[workdir]`、独立索引内存/独立串行） |
| 叠加 gitignore 体系 | 插件把 `codegraph.skip-dirs`（用户规则，`splitRules` **保序且保留重复项**）与 `codegraph.stack-gitignore`（布尔）**原样下发**引擎（`codegraph_configure` / `codegraph_initialize` 的 `skip_dirs` + `stack_gitignore`）；**插件不再读/解析 `.gitignore`**。规则来源/优先级/匹配语义/与 git 差异见 [29 §4.1](29-codegraph.md)（实现 = 共享包 `github.com/chonkpilot/chonkpilot-ignore`，与 vfts 引擎/插件**同一实现**） |
| 注册工具 | **6 个查询工具**（`codegraph_symbol_search` / `_get_symbol_info` / `_get_dependency_graph` / `_find_circular_deps` / `_analyze_complexity` / `_get_module_summary`），同名同 schema（去掉 workdir），`handler_subject=codegraph-tool-call`，hot=true，category=codegraph |
| 周期/超时 | sweep 15s · heartbeat 90s（**仅 `-tags split` 编入时判超时 → 既有 `instanceGone`；阈值 = `instance.HeartbeatTimeout`（与 llm-server `Manager.Sweep`、persist `staleTimeout` 同口径，见 §2 实例视图）；默认构建不判超时**）· data 5s · call 60s · init 15min；**`childIdle 5min` 已接线**（`sweepIdleClient` 于 15s 扫描中**逐 workdir**回收无引用且空闲超阈值的引擎子进程，下次 `clientFor(workdir)` 懒重建） |
| 进度回写 | 索引经**既有** `codegraph.status` 面写 `{state:"indexing", phase, progressDone, progressTotal}`（`configure`→`index`，索引期 500ms 轮询引擎 `meta.json`；**零新增主题**） |
| 可见性 | 工具仅在存在 `refs>0 且 enabled` 的 workdir 时注册；多 workdir 时**工具面仍全局一份**（归属排序后第一个 workdir），执行按 `context.instance_id` 路由（引擎子进程/索引状态已**按 workdir 独立**，仅**工具面差异注册留待 v2**） |

详见 [29-codegraph](29-codegraph.md)。

### 3.4 vfts（全文检索索引，插件侧）

| 项 | 值 |
|----|----|
| 订阅 | `instance-register`/`heartbeat`/`exit` · `data-prj-config-refresh` · 工具回调 `vfts-tool-call` |
| 配置键 | `enable-vfts`（可见性门控）· `vfts.exts` / `vfts.skip-dirs` / `vfts.stack-gitignore` / `vfts.docs` / `vfts.doc-max-mb`（索引配置，变更即强制重建）· `vfts.status`（状态回写） |
| 引擎 | 独立 exe `src/plugins/vfts`（stdio 子进程，CGO + zvec），**全局共享 client**（一对多，按工具参数 `workdir` 路由） |
| 注册工具 | **1 个查询工具** `vfts_query`（同名同 schema 去 `workdir`，`handler_subject=vfts-tool-call`，hot=true，category=vfts）；管理工具 `vfts_configure/index/status` **不注册**（插件直接调引擎） |
| 索引配置变更 | **去抖合并**：一次保存写 `vfts.exts` + `vfts.skip-dirs`(+`vfts.stack-gitignore`) 多键 = 多次 `data-prj-config-refresh` → 经 **400ms 去抖窗口**合并为**一轮**强制重建（窗口外单键变更仍触发，不会漏重建）；**仅 dirty（值确有变化）才写库** |
| 叠加 gitignore 体系 | 插件把 `vfts.skip-dirs`（用户规则，`splitRules` **保序且保留重复项**）与 `vfts.stack-gitignore`（布尔）**原样下发**引擎（`vfts_configure` / `vfts_index` 的 `skip_dirs` + `stack_gitignore`）与**插件侧清单扫描**（`manifest.go scanFiles`）——**插件不再读/解析 `.gitignore`**。规则来源与优先级（低→高）：内置强制（`.git/`·`.svn/`·`.hg/`·`.chonkpilot/`，不可被 `!` 反选）→ 默认排除（`node_modules/`·`dist/`·`build/` 等，可被 `!` 反选）→ 全局 ignore（`$XDG_CONFIG_HOME/git/ignore` 或 `~/.config/git/ignore`）→ `.git/info/exclude` → 各级 `.gitignore`（目录越深优先级越高）→ 用户输入（最高优先级）；不勾选 = 只应用「内置强制 + 默认 + 用户输入」。**目录命中忽略 = 不下降；文件命中即跳过 → 支持文件级排除**。规则语法/匹配/与 git 差异见 [29 §4.1](29-codegraph.md)（实现 = 共享包 `github.com/chonkpilot/chonkpilot-ignore`） |
| 文档索引 / 文档转换服务接入 | 键 `vfts.docs`（默认关）+ `vfts.doc-max-mb`（默认 50）。**插件只探测、不 spawn** 转换服务（`docsprobe.go`）：读两处 `state.json`（① `<exeDir>/mcps/markitdown/state.json` ② `<dataDir>/mcps/markitdown/state.json`）→ 校验 `pid` 与 `GET /vfts/health` 一致 → 取 `{port, token, version}`；失败一律当「未运行」（不报错、不阻塞）。探测结果**按 30s TTL 缓存**（索引前强制复探；`tick` 内仅在「不可用→可用」跳变时经去抖调度一次重建 → 服务后启动**自动接上、无需重启**）。索引前把 `docs`/`doc_endpoint`/`doc_token`/`doc_max_bytes`/`doc_text_max_bytes`/`doc_cache_dir` **恒一并下发** `vfts_configure`（**服务不可用 = 空 `doc_endpoint`/`doc_token`**；引擎按「**键存在即覆盖（含空值）**」清掉残留旧值，`docsAvailable` 随之为 false）。**降级**：服务不可用 → 清单与引擎**同口径整批跳过文档类**（未索引的文档类**从 `file_list` 移除**）；单文件失败 → 降级为「仅索引文件名」（引擎侧，保留在清单）。**解析器版本**：清单文档类行 `md5` 携带转换器版本（`<md5hex>@<version>`，复用 md5 口径）→ 版本变化即失效重建 + 插件清缓存目录。**token 仅内存、绝不入日志**。 |
| 进度推送 | 索引期间 **500ms 轮询**引擎落盘 `meta.json` 的 `done/total` 变化 → 回写 `vfts.status`（`{state, phase, progressDone, progressTotal}`，复用既有状态面、零新增主题） |
| 周期/超时 | sweep 15s · heartbeat 90s（**仅 `-tags split` 编入时判超时 → 既有 `instanceGone`；阈值 = `instance.HeartbeatTimeout`（与 llm-server `Manager.Sweep`、persist `staleTimeout` 同口径，见 §2 实例视图）；默认构建不判超时**）· data 5s · call 60s · init 15min；**`childIdle 5min` 已接线**：`sweepIdleClient` 于 15s 扫描中回收**无活跃实例且空闲 ≥5min** 的共享引擎子进程（下次 `sharedClient()` 懒重建），`lastActiveAt` 由 register/heartbeat 刷新 |
| 可见性 | 工具仅在存在 `refs>0 且 enabled=true` 的 workdir 时注册；v1 多 workdir 只全局注册一份，执行按 `context.instance_id` 路由 |

> **设置页回显（`VftsConfig.vue`）**：`vfts.status` 由插件回写、UI 只读回显 —— 状态文本（`state=""` = **未初始化**，不再落入"启用中…"误导回显）+ `phase`（索引阶段）+ 进度（`progressDone/progressTotal`，终态不展示）+ `err`；明细补齐 `indexedFiles` / `chunkCount` / `tokenizer` / `exts`；索引配置（exts/排除规则）**仅 dirty 才写库**，并提供**「恢复默认」**（删项目级键 → 回落引擎默认集）。**文档索引区块**：开关 `vfts.docs`（默认关）+ 体积上限 `vfts.doc-max-mb`（默认 50）+ **状态行**「转换服务：未运行 / 已就绪（端口 N）」（源 = `vfts.status.docsService`/`docsPort`）+ **MCP 注册指引**（与 `src/mcps/markitdown/README.md` §3.1 同文案 + 复制按钮）；计数 `docsSkipped`/`docsFailed` 有值即显示。保存仍走**一次批量写**（`setConfigs`），禁 `watch`/`watchEffect`。

> 与 codegraph（§3.3）同构：引擎外置 exe + 按 workdir 引用计数收敛工具注册；差异 = vfts **仍为全局共享 client**（一对多、按工具参数 `workdir` 路由），而 codegraph **已按 workdir 独立 client/子进程**；另差异 = vfts 仅 1 个查询工具、索引由本插件增量/全量编排（`vfts.exts` 变更强制重建）。

> **引擎 exe 路径解析（发行布局）**：两插件的 `resolveExe` 定位引擎 exe 的**发行位置** =
> `codegraph` → **`<exeDir>/mcps/codebase/chonkpilot-codegraph-mcp-server.exe`**、`vfts` → **`<exeDir>/mcps/vfts/chonkpilot-vfts-mcp-server.exe`**
> （`vfts` 的 `zvec_c_api.dll` 须与引擎 exe 同目录）。**不再多路径猜测**；
> 仍保留配置覆盖：`Options.Exe` > 环境变量 `CODEGRAPH_EXE` / `VFTS_EXE` > 上述发行位置。

---

### 3.5 memory（记忆沉淀）

> `plugin-memory`（`src/plugins/plugin-memory/`）为 server 内嵌插件之一，与 compress/history 并列。

| 项 | 值 |
|----|----|
| 订阅 | `session-compress`（每轮终态后触发，与 compress 同源，**消息面零新增**）· 点分 `memory.flush`（手动「立即沉淀」，见 [61 §1.1](../60-reference/61-消息一览.md)） |
| 配置键 | `memory.enabled`（总开关，**默认 false**）· `memory.min-turn-tokens`（默认 200；**累计门控**，见「处理」）· `memory.category.<类别>`（类别开关，缺省启用）· `memory.category-max-tokens`（单类别 token **告警**阈值，默认 2000、缺失不限；**仅告警不截断**）。**类别沉淀提示词（OP-04）已文件化** = `capability/system/memory/<类别名>.md`（**由 memory 域按文件读序 项目→用户→系统→embed 内置解析、随 `data-memory-list` 下发**；**插件不再读 prj/usr 键载体**，旧 `memory.prompt.<类别名>` / `memory_prompts` 与代码常量 `defaultRewriteSystemPrompt` 均已删除） |
| 提取范围（OP-05） | 按 (会话, 类别) 记录「**上次提取 turn**」进度（**专用表 prjusr `memory_extract`**，经 `data-memory-extract-{load,save,delete}`；记录 `{session_id, category, last_turn_id}`、主键 `<会话>\x00<类别>`，见 [61 §3.1](../60-reference/61-消息一览.md) · [12 §6.1](../10-architecture/12-数据层.md)）→ 提取范围 = 该类别**从上次提取轮到最新轮的全部轮**（`data-session-history` 取轮序 + 逐轮 `data-session-load-messages`），**含此前被阈值跳过的轮**；成功后**推进进度**到最后处理的轮（**仅在类别保存成功后**；无新轮 → 跳过） |
| 队列/合并 | 订阅回调（`session-compress` / `memory.flush`）**只置待处理标记**（key = (instance, session)，同 key **覆盖合并**）并立即返回；**每 instance 一个串行 worker** 取标记串行处理（同实例不并发、不重入；裸 goroutine **兜 panic**）。处理 = 该会话各启用类别「进度 → 最新轮」**一次性提取**；**无新轮 → 跳过（不算失败）**。天然幂等：同一 key 多次事件合并成一次、已提取的轮因进度推进不重提 |
| 门控（OP-05） | `memory.min-turn-tokens` = **累计门控**（非单轮）：自锚点（各启用类别中最旧的已提取轮；无进度 → 从头）以来**跨轮累计 token ≥ 阈值**才提取；本轮未达 → 跳过且**不推进进度**（下次一并提取；跳过轮不丢失）。token 取 `turns.full_tokens` 预存值优先、缺值回退实时估算 |
| 遍历范围（OP-07） | **项目级类别含子 session**（子会话自身的轮终态事件照常提取项目级记忆；子步骤也要提）· **用户偏好仅主 session**（顶层；按 `data-session-get.parent_id` 判主/子 → 子会话跳过 `level=user` 类别）。**注（DSL-4，42 §2 (253)）**：子会话是否**进入**该流程先受 `memory.subsession` 门控（默认关；见下「子会话门控」）。 |
| **子会话门控（DSL-4，42 §2 (253)）** | 项目级 `memory.subsession`（**默认关闭**）：关 = **子会话**（`session.parent_id != ""`，与 OP-07 同一 `data-session-get` 读取）turn **跳过自动沉淀**（`process` 在判主/子后即返回，不调 LLM、不写记忆、不广播 `memory-*` 进度）；**主会话恒不跳过**。**只关沉淀（写路径）**——记忆清单/资产/用户偏好**带出指引（读路径）不随该键变化**（只随 `memory.enabled` 门控）。`memory.flush`（手动）作用域 = 活动会话（主会话），子会话无手动入口。见 [64 §4.1](../60-reference/64-配置项一览.md) |
| 写冲突（OP-08） | **per-(workdir/项目, 类别) 进程内互斥 + 读-改-写临界区**：「读旧全文（`data-memory-read`）→ LLM 重写（`llm-simple`）→ 写回（`data-memory-save`）」整段持锁 → 同进程多会话并发写同一类别 md **不并发**；跨进程由**单持有者**保证（OP-14，另项） |
| 处理 | 判主/子会话 → 取类别清单（`data-memory-list`，每项带 `prompt` = 该类沉淀提示词有效值；首次由 persist 预置类别文件）→ 读会话全部轮 + 各类进度（`data-memory-extract-load`）→ **累计门控**（未达阈值 → 跳过）→ 按启用类别**并行**：按该类进度取「上次提取轮 → **最新轮**」全部轮消息 → 读旧全文 + 新信息 → 经 `llm-simple` 重写全文（`system` = 清单 `prompt`；**`prompt` 空 = 无提示词文件 → 该类不提取**，记日志 + `Deps.Notify` 可见提示；`llm.memory` 子系统 LLM，缺省回落 defaultLLM）→ `data-memory-save` → **推进进度**（`data-memory-extract-save`，仅成功后） |
| 进度通知 | 入队（`memory-queued`）/ 处理前（`memory-start`）/ 处理后（`memory-done`）各经**既有通知面** `tool-notify` 广播一次（`plugin=memory`；**消息面零新增主题**，见 [61 §4.3](../60-reference/61-消息一览.md)），供 statusbar 显示队列状态（排队 / 进行中 / 完成） |
| 手动沉淀 | `memory.flush{instance_id, session}` → 置**待处理标记**（force=true；并入同一每 instance 串行 worker）→ **投递即回** `{ok:true, queued:true, session}`（不再同步等各类别 LLM 跑完；前置不满足 → `{ok:false, reason}`）；进度经 `tool-notify`（`memory-*`）推送；复用**跨轮读-改-写沉淀回路**（`distillRanged`，见上「提取范围」，范围 = 各进度 → **最新轮**），**不受 `min-turn-tokens` 门控**（无新轮 → 跳过） |
| 失败策略 | 不阻塞对话：任一环节失败只记日志 + `Deps.Notify` 上报一次（宿主去重/限频，落 `<dataDir>/logs/gui.log`），跳过该类、不降级、不抛出 |

---

## 4. 内部控制流

```text
server.Start
  → persist.Start → gw.Start → im.Start → 方法面/事件面订阅 → 域工具/agent 注册 → 预热 ListTools
  → 插件按序 Start（每个 Start 内：实例视图 + 事件订阅 + 自检）
  → 全部成功 → 广播 server-starting
```

- 插件各自持 `instance.Manager`（不共享 server 的实例表）。
- codegraph 的 `syncTools` 单飞：按"活跃且 enabled 的 workdir"决定注册/注销。

---

## 5. 数据结构与存储

| 插件 | 数据 | 载体 |
|------|------|------|
| compress | 快照（session `history`/`snapshot_turn`） | prjusr 库（经 `data-snapshot-*`） |
| memory | 记忆类别全文（每类别一份沉淀全文 + 类别清单） | 记忆库（persist memory 域，经 `data-memory-list/read/save`）；**提取进度**（每 (会话, 类别) 一份「最后已提取 turn」） | **prjusr 专用表 `memory_extract`**（记录 `{session_id, category, last_turn_id, updated_at}`、主键 `<会话>\x00<类别>`；经 `data-memory-extract-{load,save,delete}`；OP-05/06） |
| history | 检查点链（`commit-tree` 产物，parent 线性串联，ref 只指向链头）；状态 `history.status.<slug>` / 时间线 `history.timeline.<slug>`（**会话级内部键**，prjusr 库） | workdir 的 `.git`（独立 ref `refs/chonkpilot/<slug>` + 持久临时 index `<workdir>/.chonkpilot/history/index`） |
| codegraph | 索引 | `<workDir>/.chonkpilot/codegraph/index.db`（bbolt 单文件库；元信息 `meta.json`）；状态 `codegraph.status`（prj 库） |
| vfts | 全文索引 + 文件清单 | 引擎自持索引（`zvec`，按 workdir）；插件侧回写 `vfts.status` / 清单（prj 库） |

---

## 6. 依赖

- `src/plugins/plugin` → `src/lib/core`。
- `src/plugins/plugin-compress` → `src/lib/core` · `src/lib/data` · `src/plugins/plugin`。
- `src/plugins/plugin-memory` → `src/lib/core` · `src/lib/data` · `src/plugins/plugin`。
- `src/plugins/plugin-history` → `src/lib/core` · `src/plugins/plugin`（纯 git，无 DB）。
- `src/plugins/plugin-codegraph` → `src/lib/core` · `src/plugins/plugin`。
- `src/plugins/plugin-vfts` → `src/lib/core` · `src/plugins/plugin`。
- 被 `src/lib/llm` / `src/lib/gui` / `src/desktop/cli` 内嵌。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 内嵌 Hook（非独立进程） | 同一 Bus、编译期注入 | 单体形态简单；独立进程待去中心化 MQ |
| 无宿主函数注入 | 统一走 mq（`llm-simple`） | 解耦、可测试 |
| history 纯 git | 废弃 history.db | 复用版本控制、零额外存储 |
| **history = 独立 ref 的检查点链** | 打点走 `refs/chonkpilot/<slug>` + 持久临时 index（`GIT_INDEX_FILE`）；**绝不碰 `.git/index` / HEAD / 用户分支** | 不再污染用户仓库的提交历史；检查点对用户分支不可见、可随时丢弃；`commit-tree` 线性链天然支持相对步 diff/回滚 |
| codegraph 引擎外置 exe | 插件管理子进程 | 引擎需 CGO（主模块禁令） |
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
- **插件失败** → **用户可见 + 日志可查，且不阻塞**：`memory`（记忆沉淀）/ `compress`（上下文压缩）等失败经 `Deps.Notify` 上报 → 宿主判重后经既有 `tool-notify{notice:"plugin-failure"}` 轻提示一次（**同轮同类一次**，见 §7），同时失败原因经统一出口落 `<dataDir>/logs/gui.log`；**本轮对话不受影响**（不改流程终态、不降级、不抛出）。
- **多 workdir** → codegraph **工具面**仍全局注册一份（归属排序后第一个 workdir，按 `context.instance_id` 路由）；**引擎子进程/索引状态已按 workdir 独立**，仅**工具面差异注册留待 v2**。
- **实例崩溃** → **显式 `instance-exit` 清理**；分离形态（`-tags split`）由心跳超时判定（90s）→ 既有 `instanceGone` 兜底清理；合并单进程形态只走显式 `instance-exit`，见 §2 实例视图。
- ⏸ **上下文配置**（历史轮次上下文的带出 + 上下文的分类与内容沉淀，与 compress/summary 的关系待定）见 [40-演进计划](../40-roadmap/40-演进计划.md)（演进项，暂不开发）。

---

## 9. 现状与待办

- ✅ **compress 阈值已读项目配置** `keep_full_max_turns`/`keep_full_max_tokens`/`compress_token_threshold`（prj 库 config；`compress.go` `resolveOpts`），缺失/非法回落 `DefaultOptions{10, 24000, 20000}`（[02-配置层级](../00-overview/02-配置层级.md) §5）。`keep_full_max_turns`（保留段轮数上限）/ `keep_full_max_tokens`（保留段 token 上限）/ `compress_token_threshold`（**简化区阈值**）。边界算法上提为 `data.LocateFullTurnCount`（压缩侧/组装侧**共用**）；**「本轮（最新轮）恒保留完整」下限落进共享算法**（压缩侧与组装侧一致，**不单侧钳制**）；取值语义 = `0` 不启用该条件、**两者均 0 → 不压缩**、负数非法（前端提示 + 后端留日志）。轮次结束时预存该轮 `full_tokens`/`brief_tokens`（`data-session-complete-turn` 只增字段），判定/拼接可直接累加（缺值回退实时估算，`data.ResolveStoredTokens`）。
- ✅ **心跳超时退出判定 = 编译开关**：`instance.Manager.Sweep(timeout)`（90s）与 codegraph/vfts 的 `sweepInstances` 心跳超时判定**仅在分离形态（`-tags split`）编入**（`plugin/instance/heartbeat_split.go`、`plugin-codegraph|plugin-vfts/sweep_split.go`），**合并单进程形态（默认构建）为空实现**（`heartbeat_inprocess.go` / `sweep_inprocess.go`）——实例失效只走**显式 `instance-exit`**（`instance-heartbeat` 订阅保留，供分离形态），空闲引擎子进程仍由 `sweepIdleClient` 回收。详见 §2 实例视图。
- ✅ codegraph 每 workdir 独立引擎子进程（`clientFor(workdir)`），见 §3.3 与 [29-codegraph §4.5](29-codegraph.md)。
- ✅ compress 插件用 `prjUsrDB`（`data.PrjUsr`）读快照（快照落 **prjusr**），`TestCompressPluginEndToEnd` 通过。
- ✅ compress/summarize.go 的 `LLMSummarizer`（HTTP 摘要器）已删（运行时走 mq `llm-simple`）。
- ✅ **history 门控 + compress 摘要 system**：history 插件读 prj `history.enabled` 门控（**检查点链打点**，见 §3.2/§7）；compress 按级读 `capability/system/summary.md`（纯文本）作摘要 `system`（OP-01/OP-02）。
- ✅ **history = 独立 ref 的检查点链**：打 `refs/chonkpilot/<slug>`（根会话；父子共享）+ 持久临时 index（`GIT_INDEX_FILE`，**绝不碰 `.git/index` / HEAD**）；触发 = gateway **前置打点钩子**（`tools/register.pre_hook_subject` → `history-pre-tool-hook`，打点失败即拒绝该工具调用）+ `session-complete` **轮末补点**；脏位来自 `filesys.changed`（不脏零 git 调用）；连续失败 ≥3 **熔断**放行、成功复位；`keep`/`ttl` 任一超限**修剪**（锚点 = 链上最新点时间，保底项不删）；`history.enabled` **仅显式 `"true"` 开（默认关闭）**；git 不可用 / 非 git 仓库 → 禁用 + 4 工具摘除。消息面见 [61 §8](../60-reference/61-消息一览.md)，配置键见 [64 §4](../60-reference/64-配置项一览.md)，设置页见 [20-gui §12.12](20-gui.md)。
- ✅ **vfts `childIdleTimeout` 接线**：`sweepIdleClient` 于 15s 扫描中回收**无活跃实例且空闲 ≥5min** 的共享引擎子进程（`sharedClient()` 懒重建；`lastActiveAt` 由 register/heartbeat 刷新），对齐 codegraph；并顺带修 `p.client` 数据竞争（`sharedClient()` 返回指针、`clientMu` 保护）。详见 §3.4。
- ✅ **codegraph 索引进度回写 + 重试**：codegraph 索引进度经**既有** `codegraph.status` 面回写（`phase`/`progressDone`/`progressTotal`，零新增主题）+ `configure`/`initialize` 失败重试（缺省 2 次 / 2s）；`childIdleTimeout`（5min）接线，**逐 workdir**回收无引用的空闲引擎子进程（`clientFor(workdir)` 懒重建）。详见 §3.3 与 [29-codegraph §4.5](29-codegraph.md)。
- ✅ **记忆手动沉淀 = 新增点分相对主题 `memory.flush`**：`memory` 插件除既有的 `session-compress`（轮末**异步**沉淀、受 `memory.min-turn-tokens` 门控）外，另订阅点分主题 **`memory.flush`** —— payload `{instance_id, session}`，取该会话**最近一轮**消息 → **复用同一沉淀回路 `distill`**（启用门控 / 类别门控 / 读改写保存**完全同源**），**不受 `memory.min-turn-tokens` 门控**（显式动作即用户意图）；**同步回执** `{ok, session, turn, saved[], failed[], enabled}`（前置不满足 → `{ok:false, reason}`，不静默）；作用域 = payload 指定的 instance + session（不跨实例 / 不跨会话）。前端 = 设置 → 上下文管理「立即沉淀」。明细见 [61 §1.1/§7.1](../60-reference/61-消息一览.md)。
- ✅ **「压缩内容可查」= 复用既有快照面，零新增存储**：上下文压缩的产物**唯一落点** = 会话快照（prjusr `sessions` 表 `history`/`snapshot_turn`；compress 插件回写），故设置 → 上下文管理「压缩内容」小节**直接读既有 `data-snapshot-get`**（首条 system `[已压缩早前对话] <摘要>` = 摘要原文），**不新增主题 / 不新增存储**；**压缩时间不可得**（快照不记时间）→ UI 如实说明。口径见 [61 §3.2b](../60-reference/61-消息一览.md)。

---

## 10. 关联测试

`src/test/chonkpilot-plugin/unittest/instance/manager_test.go`（实例视图）· `src/lib/llm/server/plugins_test.go`（`server-starting` 广播、compress 端到端）· **`src/plugins/plugin-history/history_test.go`（L1：`gateFromValue` 门控真值表 / 未回读默认关 / `data-prj-config-refresh` 实时同步）** · **`src/plugins/plugin-history/chain_test.go`（L1：检查点链内核 —— 建链且**绝不碰用户分支**（HEAD / `.git/index` 逐字不变）、不脏零 git 调用、门控、工具注册门控（含 `pre_hook_subject`）、修剪（`keep`·`ttl`·锚点=最新点·保底项）、熔断、父子会话共享链、`history_restore` 一致性校验与删除恢复、`diff`·`show`、消息驱动打点）** · `src/plugins/plugin-codegraph/codegraph_test.go`（queryTools 数量=6、名称集合、props 不含 workdir；多 workdir：`TestMultiWorkdirIndexAndQueryIsolation` / `TestClientForPerWorkdirIdentity` / `TestSweepIdleClientPerWorkdir`）· `src/plugins/plugin-vfts/vfts_test.go`（queryTools 数量=1、名称集合）。

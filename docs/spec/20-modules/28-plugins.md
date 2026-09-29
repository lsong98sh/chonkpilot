# 28 · 插件体系（src/plugins/plugin*）

> 日期：2026-09-10 ｜ 状态：✅ 与代码一致
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [21-llm-server](21-llm-server.md) · [29-codegraph](29-codegraph.md)
> 代码目录（D-28：`src/plugin*` → `src/plugins/plugin*`）：`src/plugins/plugin/`（接口 + 实例视图）· `src/plugins/plugin-compress/` · `src/plugins/plugin-history/` · `src/plugins/plugin-codegraph/` · `src/plugins/plugin-vfts/`

---

## 1. 职责与边界

- **一句话**：**内嵌 lib Hook**——编译期打进 server，在 `server.Start` 全部就绪后按序 `Start`，订阅 MQ 事件做扩展（压缩 / 文件历史检查点 / codegraph·vfts 工具注入）。
- **做**：事件订阅、按 instance/workdir 自持数据（经 `src/data`）、向 gateway 注册/注销工具。
- **不做**：不依赖宿主函数注入（**原 `Summarize` 已移除**，统一走 `llm-simple` 等 mq 方法面）；不独立成进程（🔵 待去中心化 MQ）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 形态 | 内嵌 lib（server 进程内、同一 `mq.Bus`） |
| 接口 | `Hook{ Name() string; Start(Deps) error }`；`Deps{ Bus mq.Bus; Logf func(format, ...) }`**〔订正（2026-09-20）：`Deps` 另含可选 `Notify func(Notice)`（失败上报，nil = 静默 = 改前行为）；原文保留。〕** |
| 启动 | `server.Start` 中 compress → history → codegraph 按序；**全部成功后才广播 `server-starting`**（就绪信号） |
| 数据访问 | 走 `src/data`（`data.Prj(instanceID)` 或 payload 自带 work_dir/data_dir 自登记）；work_dir 解析走 `instance.Manager` |

**实例视图**（`src/plugin/instance/manager.go`）：订阅 `instance-register` / `instance-heartbeat` / `instance-exit`，维护 `instance_id ↔ {ID, WorkDir, DataDir, ClientType, LastBeat}`（`LastBeat` 仅记录最近心跳/注册时间）；提供 `Lookup/List/Count`。
> ⚠️ **不做心跳超时退出判定（2026-09-15 后：用户口径「单体不用发布心跳…不需要判定退出」）**：合并单进程形态下实例**不据心跳判退出**；实例失效以**显式 `instance-exit`** 清理，`instance-heartbeat` 订阅保留。**（2026-09-16 后：心跳仍需要 —— 用户口径「心跳看来还是需要。用编译开关。」：心跳发布与超时判定改由**编译开关**编入，**tag 名 = `split`（`-tags split`）** —— 分离形态下 `StartHeartbeat`（注册后起 30s 周期发布 `instance-heartbeat{instance_id}`）与 `Manager.Sweep(timeout)`（**90s = 3× 周期**的实例视同退出）**的生产调用方 = 判定侧消费方两处** —— **llm-server** `startInstanceSweep`（`src/llm/server/sweep_split.go`：30s ticker → `Manager.Sweep(90s)` → 逐个走既有 `exitInstance`：取消 running turn + 释放 `busy` 锁 + 注销 capability dir 节点）、**persist** `startStaleSweep`（`src/data/persist/sweep_split.go`：30s 扫描 → `LastBeat` ≥90s → 既有 `instanceGone`：移除 `insts` 绑定 + `data.Unregister`；**阈值就地取值 `staleTimeout = 90s`、不引 plugin 依赖，改值需与 `instance.HeartbeatTimeout` 同步**〔**（2026-09-16 后：该"就地取值"已作废 —— 阈值上提 `src/lib/heartbeat` 作单一来源：`persist` 的 `staleTimeout = heartbeat.Timeout`（90s）、llm-server 的 `sweepInterval = heartbeat.Interval`（30s）、`plugin/instance` 的 `HeartbeatInterval`/`HeartbeatTimeout` 亦为同名常量别名；改值只需一处，见 [24-lib §2/§6](24-lib.md) · [42 §2 (74)](../40-roadmap/42-决策记录.md)）**〕）；插件 codegraph/vfts 的 `sweepInstances` 走各自 `instanceGone`（解绑 + 递减 workdir 引用）**均存在**（`src/plugin/instance/heartbeat_split.go`）；**默认构建（无 tag）为空实现**（`heartbeat_inprocess.go`：不发布、不判超时）——与上条「单体不据心跳判退出」**一致、不冲突**；见 [42 §2 (72)](../40-roadmap/42-决策记录.md) · [61 §4.1](../60-reference/61-消息一览.md)。）**

---

## 3. 各插件

### 3.1 compress（历史压缩）

| 项 | 值 |
|----|----|
| 订阅 | `session-compress`（server 终态落库 + 写快照后发；载荷另有**可选** `max_context_token` / `max_output_token` 字段，见下） |
| 流程 | 读快照 → **三段定位**（`LocateThreeZones`，算法唯一来源 = `data.LocateZones`，**与组装侧同一函数**）：**完整区**（本轮 + 最近 N 轮，N/M 取先到，原文）/ **简化区**（再往前，**brief token 累计 ≤ T**，【简化态原文】= 仅 text）/ **摘要区**（更早）→ **摘要区非空**才压缩：**经 `llm-simple` 摘要摘要区（并入既有摘要）** → 回写快照（`snapshot_turn` 不变）。快照结果 = `[摘要] + 简化区（原文保留）+ 完整区（原文保留）`；简化区/摘要区**在发送时**由组装侧投影为【简化态原文】（`data.BriefMessages`，不落库、不丢字段） |
| **兜底归并**（口径 Y/Z3，2026-09-25） | 触发：`摘要 + 简化区 + 完整区 + maxOutputToken > maxContextToken`（**真窗口 `maxContextToken` > 0 才启用**，缺省 0 = 不启用；输出预留 = `maxOutputToken`，口径 Z3 起**不再用常量 4096**）→ 处置：把【既有摘要 + 简化区全部原内容】（含待摘要的更早轮）合并 → **交同一 `llm-simple` 摘要路径重新生成摘要**（目标长度 = **`maxOutputToken` 的 1/2**，以长度要求追加到摘要输入）→ 结果 = `[新摘要] + 完整区`（**简化区清空**）；下一轮简化区自 0 重新累积。**幂等/可重入**（每次都自入参快照重算，重试不膨胀）· **失败回退**（摘要失败 → 快照原样返回、原文不丢）。**摘要无独立配置项**（目标 = 输出上限 1/2） |
| 失败策略 | **摘要失败直接不压缩**（不降级；含兜底归并失败 → 回退未归并状态） |
| 阈值来源 | 读项目配置 `keep_full_max_turns`/`keep_full_max_tokens`/`compress_token_threshold`（prj 库 config；`compress.go` `resolveOpts`；**旧键 `keep_full_turns` 读时兼容**），缺失/非法回落 `DefaultOptions{RetainTurns:10, KeepFullTokens:24000, TokenMax:20000}`（见 [02-配置层级](../00-overview/02-配置层级.md) §5）。**`MaxContextToken` / `MaxOutputToken` 都不是配置项**：运行时由 server 经 `session-compress` 载荷的 `max_context_token` / `max_output_token` 透传（各自 = provider 对应字段 `maxContextToken` / `maxOutputToken`，见下方订正注） |
| 完整区定位 | `LocateThreeZones`（**2026-09-25 口径 X**）：完整区 = `data.LocateZones` 的 `FullTurns`（**本轮恒保留**，下限 1，即使其自身完整态 token 已超 M 也不降级）；更早轮从尾部逐轮向前纳入，「累计**完整态** token > M」或「已纳入轮数 >= N」的那一轮起不再纳入完整区（算法唯一来源 = `data.LocateFullTurnCount`，**与组装侧同一函数**）。**取值语义（口径 W）**：`N == 0` = 轮次条件不启用；`M == 0` = token 条件不启用；**两者均 == 0 → 不压缩**（完整区=全量、无摘要区）；**负数 = 非法**（后端不猜、按「不启用」处理并留日志，前端显式提示）。**整轮对齐**（`role=="user" && Kind!="notify"/"continue"/"resume"` 为轮边界） |
| 简化区定位 | **T（`compress_token_threshold`）= 简化区 brief token 预算**（**2026-09-25 推翻上一批"用 full_tokens"**）：自完整区前一轮向前**逐轮累加 `brief_tokens`**（= 【简化态原文】token，构造 = `data.BriefMessages`/`BriefFacadeMessages`），累计 **≤ T** 的轮进简化区；把累计推过 T 的轮起（含）**全部进摘要区**。`T <= 0` → 简化区预算 0（不进简化区、直接摘要）。简化区 token 取**预存值优先、缺值回退实时估算**（`data.ResolveStoredTokens`，尾部对齐双向） |
| token 估算 | `EstimateTokens`：字符数/2 近似；**预存口径 = 完整态（全消息）/ 简化态（`data.BriefMessages` 仅 text）**，见 [21 §4.2](21-llm-server.md) |
| 摘要 system | 按级读 `capability/prompts/summary.prompt.md`（项目级 → 用户级 → 系统级，`summarize.go` `resolveSummaryPrompt`）→ `llm-simple` 请求带 `system`；三级皆缺回落默认常量。原 `LLMSummarizer`（HTTP）**已删（D-09，2026-09-11）**。**2026-09-15：设置页保存区分「继承/覆盖」**——内容与继承值（系统文件/内置默认）相同 → **不写覆盖**（删项目级文件、保持继承）；不同 → 写项目级覆盖；另有「恢复默认」（删项目级文件回落继承），见 [22 §3.1](22-data-persist.md) |

> **〔订正（2026-09-25，口径 Z1/Z3）：兜底归并的窗口与输出预留来源〕** 原以 provider `maxTokens`（实际 = 请求体 `max_tokens` = **最大输出 token**）当**上下文窗口代理** + 常量 `outputReserve = 4096`，**误用输出上限当窗口**（易误触发/误判）。现按用户口径拆分为两字段：**`maxContextToken` = 上下文窗口大小**（**新增**，仅用于兜底判定）、**`maxOutputToken` = 最大输出 token**（**由 `maxTokens` 改名**，即请求体 `max_tokens`）。`server.finish` 发 `llm-compress` 时经**只增**可选字段 **`max_context_token`** / **`max_output_token`** 透传（`src/lib/llm/server/server.go:1843-1848`），插件读入 `Options.MaxContextToken` / `Options.MaxOutputToken`（**`max_context_token` > 0 才启用兜底**；输出预留 = `max_output_token`，**不再用常量 4096**）；摘要目标 = `maxOutputToken`/2。旧键 `maxTokens` / `max_tokens` **读时兼容**（只读不写，不破坏老配置）；`maxContextToken` **≤ 0 / 缺省 = 兜底不启用**（仅常规三层压缩）。原「语义差异（待拍板）」已由本次字段拆分消除。**〔订正（2026-09-25，口径 Z4）：载荷命名统一 snake_case〕** 载荷字段 = `max_context_token` / `max_output_token`（**写入只发 snake_case**）；插件对旧名 camelCase `maxContextToken` / `maxOutputToken` 与更早 `window` **只读兼容**（新名优先，`compress.go pickInt`；只读不写）—— 见 [61 §4.3](../60-reference/61-消息一览.md)。

### 3.2 history（文件历史：独立 ref 的检查点链）

> **语义（2026-09-27 批次③，取代「轮边界 `git add -A` + `git commit -m "chonk: snapshot"` 提交到用户分支」旧口径）**：打**独立 ref 的检查点链**，**绝不碰 `.git/index` 与 HEAD、不在用户分支产生任何提交**。消息面见 [61 §5.1.1/§5.4a/§8 批次③](../60-reference/61-消息一览.md)，配置键见 [64 §4](../60-reference/64-配置项一览.md)；设置页见 [20-gui §12.12](20-gui.md)。

| 项 | 值 |
|----|----|
| 订阅 | `session-complete`（**主轮次 → 轮末补点**）· `filesys.changed`（该 workdir 文件变更 → 置「脏」位；广播由 filesys 侧 **60ms 去抖**合并）· `instance-register`（建 workState + 异步回读 `history.enabled`）· `data-prj-config-refresh`（开关 / 保留参数实时同步 + `history.clear` 清链）· **前置打点钩子** `history-pre-tool-hook`（gateway 执行工具前同步调用）· 工具回调 `history-tool-call`。**`session-turn-start` 不再消费** |
| 启用/门控 | git 不可执行（`exec.LookPath("git")` 失败）→ `Start` 记日志 + `return nil`（**功能禁用，不算启动失败**）；workdir **非 git 仓库**（无 `.git`；worktree/submodule 的 `.git` 文件亦可）→ 功能禁用；`history.enabled` **仅显式 `"true"` 才开启（默认关闭）**，缺失 / `""` / `false` / 非法 / 未回读 → 关闭。**禁用或非 git 时 4 个工具一律摘除**（`syncTools` 收敛，判据 = 存在「hasGit 且 enabled」的 workdir） |
| 打点触发 | ① **gateway 前置钩子**（执行**任意**工具前，同步；经 `tools/register` 可选字段 `pre_hook_subject` 声明）：启用 && 未熔断 && **脏** → 同步打点；**打点失败 → 写 error → gateway 拒绝该工具调用（工具不执行），LLM 可见失败并可重试**；**payload 带 `touch_files`（2026-09-28）= 该工具是否「涉及文件变动」**：显式 `false` → 直接放行、**不打点**（省 8–9 次 git 进程），仍登记会话归属（轮末补点保底）；缺省 / `true` → 与改前一致；② **轮末补点**（`session-complete` 主轮次）：保证「最后一步的产像」入链 —— **不依赖脏位（规避 `filesys.changed` 60ms 去抖竞态）、强制走一次打点流程**，流程内按 tree 比较**内容未变则不建点**。**前置钩子路径不脏 / 标「不涉及」= 零 git 调用**（直接放行） |
| 「涉及文件变动」（打点粒度） | usr `tool_async.<工具暴露名>.touch_files`（布尔；前端「工具配置」页开关；**与异步档位同载体、同页**）。**缺省按工具来源**：**self 节点内置**仅 `filesys_run` / `script_run` 涉及（true），其余内置（file_read/file_find/file_diff/web_fetch/browser_run/desktop_run…）不涉及（false）；**dir 节点 / 第三方 MCP / 无法判定 → 保守按涉及（true）**。生效面 = gateway 前置钩子 payload（`resolveTouchFiles`：显式覆盖 > 来源缺省）。**标错只让检查点粒度变粗**（前后点仍在、`git diff` 一致性校验仍生效），**不丢安全**（见 [64 §3](../60-reference/64-配置项一览.md) `tool_async` / [61 §5.4a](../60-reference/61-消息一览.md)） |
| 流程（打点） | 临时 index `add -A`（尊重 `.gitignore`；进入前 `ensureGitignore` 保证 `.chonkpilot/` 被忽略）→ `write-tree` → **新 tree == 链头 tree → 直接跳过（不建点、不 `update-ref`、不修剪）**；否则 → `commit-tree <tree> [-p <链头>] -m "chonk-ckpt: session=<slug> tool=<tool> ts=<RFC3339>"` → `update-ref refs/chonkpilot/<slug> <commit>` → **修剪** |
| 链与 ref | `<slug>` = **根会话**（优先 `top_session`，缺省 `session`；**父子会话共享一条链**）；链 = parent 指针线性串联（`git rev-list` 可枚举、`git diff A B` 可用）；**ref 只指向链头**（对象靠可达性保活，gc 安全） |
| 存储 | **独立 ref `refs/chonkpilot/<slug>`**（**废弃 `history.db`，纯 git**）+ **持久临时 index** `<workdir>/.chonkpilot/history/index`（经 `GIT_INDEX_FILE` 注入）；**绝不碰 `.git/index`、绝不碰 HEAD** → **不在用户分支产生任何提交** |
| 修剪 | `history.checkpoint_keep`（默认 500）/ `history.checkpoint_ttl_days`（默认 7）**任一超限即修剪**：保留段 = 「最新 keep 个」∩「窗口内」，再并入**保底项（当前轮 / 上一轮起点）永不删**；**天数窗口锚点 = 链上最新点时间**（项目闲置不清历史，不是 `now`）；修剪 = **按保留段重建**（复用 tree/message/时间）→ **commit id 会变，稳定标识是相对编号**；被淘汰点不可达 → 交 git 自动 gc（不主动跑 gc/prune） |
| 熔断 | 连续打点失败 ≥ `fuseThreshold`(3) → `fused`（**仍尝试打点、失败亦放行、不再拦截工具调用**）；**成功一次即复位**；失败次数与模式经 `history.status.<slug>` 回写可见 |
| 内部工具 | **4 个**（`category=self`；启用且是 git 仓库才注册，否则摘除）：`history_status {}` / `history_diff {to?=-1, path?}` / `history_show {to?=-1, path}` / `history_restore {path, to?=-1}`（**单文件、禁止批量**；一致性校验 = 文件已删除 **或** `git diff --quiet <链头> -- <path>` 为 0，否则拒绝）。`to` 三态 = 负整数相对步（-1 最近一步、-2 再上一步…）/ `"turn-start"` 当前轮起点 / 绝对 commit id（须在本链上） |
| 状态回写（前端只读） | **会话级**内部键（2026-09-28，I-135）：`history.status.<slug>`（`{enabled, mode: active\|fused\|off, repo, dirty, failCount, checkpointCount, bytes, lastCheckpointAt, lastDurationMs, lastError}`）/ `history.timeline.<slug>`（**最新在前**、相对编号 `-1` 起、**≤200 条**）——`<slug>` = 链根会话，**多会话并发各自独立、互不覆盖**；落 **prjusr**（本机可重建派生物）。`history.clear`（前端写 JSON `{ts, session}`）→ **只清目标会话的链**（`update-ref -d refs/chonkpilot/<slug>`）并回写该会话空状态，其它会话链保留（I-136） |
| 语义边界（有意为之） | 被 `.gitignore` 忽略的文件**不进检查点、也回滚不了**（含 `.env`/`node_modules`/`.chonkpilot/` —— 不把密钥写进对象库）；**未跟踪文件**回滚时**不动**；回滚以检查点 tree 为准、**不碰 `.git`**；检查点遵守 git 的 ignore，与「索引排除规则」（`lib/ignore`）是**两套独立规则、不联动** |

> ⚠️ **效率注意**：文件历史在**每次工具执行前**打点（脏才打点）。**每次「脏」打点约需 8–9 次 git 进程**（写路径 + 状态/时间线回写的只读 git；逐行走查合计约 10–11 次，以实跑采数为准），**修剪还需按保留段重建**（仅在 `keep`/`ttl` 超限时发生）—— **大工程需慎重**（**默认关闭**）。已配「脏标记短路（不脏零 git 调用）+ **「不涉及文件变动」工具直接跳过**（2026-09-28，省下每次调用的 8–9 次 git 进程）+ 前置钩子同步打点 + 连续失败熔断 + 固定提交身份（不读用户 git config）」。见 [41 I-137](../40-roadmap/41-未决项登记.md)。

### 3.3 codegraph（代码语义索引，插件侧）

| 项 | 值 |
|----|----|
| 订阅 | `instance-register`/`heartbeat`/`exit` · `data-prj-config-refresh` · 工具回调 `codegraph-tool-call` |
| 配置键 | `enable-codegraph`（可见性门控）· `codegraph.exts` / `codegraph.skip-dirs` / `codegraph.stack-gitignore`（索引配置，变更即强制重建）· `codegraph.status`（状态回写）· `codegraph.action`（重建/清除/重试信号） |
| 引擎 | 独立 exe `src/plugins/codegraph`（stdio 子进程），按 workdir 管理；**每 workdir 独立 client/子进程**（2026-09-15 后：T-12/P2-4 已落地 —— `p.clients[workdir]`、独立索引内存/独立串行；原「全局共享 client」作废） |
| 叠加 gitignore 体系（2026-09-27；同日由「目录名折名」升级为完整 gitignore 语义） | 插件把 `codegraph.skip-dirs`（用户规则，`splitRules` **保序且保留重复项**）与 `codegraph.stack-gitignore`（布尔）**原样下发**引擎（`codegraph_configure` / `codegraph_initialize` 的 `skip_dirs` + `stack_gitignore`）；**插件不再读/解析 `.gitignore`**。规则来源/优先级/匹配语义/与 git 差异见 [29 §4.1](29-codegraph.md)（实现 = 共享包 `github.com/chonkpilot/chonkpilot-ignore`，与 vfts 引擎/插件**同一实现**） |
| 注册工具 | **6 个查询工具**（`codegraph_symbol_search` / `_get_symbol_info` / `_get_dependency_graph` / `_find_circular_deps` / `_analyze_complexity` / `_get_module_summary`），同名同 schema（去掉 workdir），`handler_subject=codegraph-tool-call`，hot=true，category=codegraph |
| 周期/超时 | sweep 15s · heartbeat 90s（**2026-09-16 后：仅 `-tags split` 编入时判超时 → 既有 `instanceGone`；阈值 = `instance.HeartbeatTimeout`（与 llm-server `Manager.Sweep`、persist `staleTimeout` 同口径，见 §2 实例视图）；默认构建不判超时**，见 §2 实例视图）· data 5s · call 60s · init 15min；**`childIdle 5min` 已接线**（`sweepIdleClient` 于 15s 扫描中**逐 workdir**回收无引用且空闲超阈值的引擎子进程，下次 `clientFor(workdir)` 懒重建，T-18；2026-09-15 后按 workdir 独立） |
| 进度回写 | 索引经**既有** `codegraph.status` 面写 `{state:"indexing", phase, progressDone, progressTotal}`（`configure`→`index`，索引期 500ms 轮询引擎 `meta.json`；**零新增主题**，T-11） |
| 可见性 | 工具仅在存在 `refs>0 且 enabled` 的 workdir 时注册；多 workdir 时**工具面仍全局一份**（归属排序后第一个 workdir），执行按 `context.instance_id` 路由（2026-09-15 后：引擎子进程/索引状态已**按 workdir 独立**，仅**工具面差异注册留待 v2**） |

详见 [29-codegraph](29-codegraph.md)。

### 3.4 vfts（全文检索索引，插件侧；2026-09-15 补）

| 项 | 值 |
|----|----|
| 订阅 | `instance-register`/`heartbeat`/`exit` · `data-prj-config-refresh` · 工具回调 `vfts-tool-call` |
| 配置键 | `enable-vfts`（可见性门控）· `vfts.exts` / `vfts.skip-dirs` / `vfts.stack-gitignore` / `vfts.docs` / `vfts.doc-max-mb`（索引配置，变更即强制重建）· `vfts.status`（状态回写） |
| 引擎 | 独立 exe `src/plugins/vfts`（stdio 子进程，CGO + zvec），**全局共享 client**（一对多，按工具参数 `workdir` 路由） |
| 注册工具 | **1 个查询工具** `vfts_query`（同名同 schema 去 `workdir`，`handler_subject=vfts-tool-call`，hot=true，category=vfts）；管理工具 `vfts_configure/index/status` **不注册**（插件直接调引擎） |
| 索引配置变更 | **去抖合并（2026-09-15；2026-09-27 扩至 stack-gitignore）**：一次保存写 `vfts.exts` + `vfts.skip-dirs`(+`vfts.stack-gitignore`) 多键 = 多次 `data-prj-config-refresh` → 经 **400ms 去抖窗口**合并为**一轮**强制重建（窗口外单键变更仍触发，不会漏重建）；**仅 dirty（值确有变化）才写库** |
| 叠加 gitignore 体系（2026-09-27；同日由「目录名折名」升级为完整 gitignore 语义） | 插件把 `vfts.skip-dirs`（用户规则，`splitRules` **保序且保留重复项**）与 `vfts.stack-gitignore`（布尔）**原样下发**引擎（`vfts_configure` / `vfts_index` 的 `skip_dirs` + `stack_gitignore`）与**插件侧清单扫描**（`manifest.go scanFiles`）——**插件不再读/解析 `.gitignore`**。规则来源与优先级（低→高）：内置强制（`.git/`·`.svn/`·`.hg/`·`.chonkpilot/`，不可被 `!` 反选）→ 默认排除（`node_modules/`·`dist/`·`build/` 等，可被 `!` 反选）→ 全局 ignore（`$XDG_CONFIG_HOME/git/ignore` 或 `~/.config/git/ignore`）→ `.git/info/exclude` → 各级 `.gitignore`（目录越深优先级越高）→ 用户输入（最高优先级）；不勾选 = 只应用「内置强制 + 默认 + 用户输入」。**目录命中忽略 = 不下降；文件命中即跳过 → 支持文件级排除**。规则语法/匹配/与 git 差异见 [29 §4.1](29-codegraph.md)（实现 = 共享包 `github.com/chonkpilot/chonkpilot-ignore`） |
| 文档索引 / 文档转换服务接入（**2026-09-29 新增**） | 键 `vfts.docs`（默认关）+ `vfts.doc-max-mb`（默认 50）。**插件只探测、不 spawn** 转换服务（`docsprobe.go`）：读两处 `state.json`（① `<exeDir>/mcps/markitdown/state.json` ② `<dataDir>/mcps/markitdown/state.json`）→ 校验 `pid` 与 `GET /vfts/health` 一致 → 取 `{port, token, version}`；失败一律当「未运行」（不报错、不阻塞）。探测结果**按 30s TTL 缓存**（索引前强制复探；`tick` 内仅在「不可用→可用」跳变时经去抖调度一次重建 → 服务后启动**自动接上、无需重启**）。索引前把 `docs`/`doc_endpoint`/`doc_token`/`doc_max_bytes`/`doc_text_max_bytes`/`doc_cache_dir` **恒一并下发** `vfts_configure`（**服务不可用 = 空 `doc_endpoint`/`doc_token`**；引擎按「**键存在即覆盖（含空值）**」清掉残留旧值，`docsAvailable` 随之为 false）。**降级**：服务不可用 → 清单与引擎**同口径整批跳过文档类**（未索引的文档类**从 `file_list` 移除**）；单文件失败 → 降级为「仅索引文件名」（引擎侧，保留在清单）。**解析器版本**：清单文档类行 `md5` 携带转换器版本（`<md5hex>@<version>`，复用 md5 口径）→ 版本变化即失效重建 + 插件清缓存目录。**token 仅内存、绝不入日志**。 |
| 进度推送 | 索引期间 **500ms 轮询**引擎落盘 `meta.json` 的 `done/total` 变化 → 回写 `vfts.status`（`{state, phase, progressDone, progressTotal}`，复用既有状态面、零新增主题，2026-09-15） |
| 周期/超时 | sweep 15s · heartbeat 90s（**2026-09-16 后：仅 `-tags split` 编入时判超时 → 既有 `instanceGone`；阈值 = `instance.HeartbeatTimeout`（与 llm-server `Manager.Sweep`、persist `staleTimeout` 同口径，见 §2 实例视图）；默认构建不判超时**，见 §2 实例视图）· data 5s · call 60s · init 15min；**`childIdle 5min` 已接线**（2026-09-15，T-18b）：`sweepIdleClient` 于 15s 扫描中回收**无活跃实例且空闲 ≥5min** 的共享引擎子进程（下次 `sharedClient()` 懒重建），`lastActiveAt` 由 register/heartbeat 刷新 |
| 可见性 | 工具仅在存在 `refs>0 且 enabled=true` 的 workdir 时注册；v1 多 workdir 只全局注册一份，执行按 `context.instance_id` 路由 |

> **设置页回显（`VftsConfig.vue`，2026-09-15 补齐；2026-09-29 加文档索引区块）**：`vfts.status` 由插件回写、UI 只读回显 —— 状态文本（`state=""` = **未初始化**，不再落入"启用中…"误导回显）+ `phase`（索引阶段）+ 进度（`progressDone/progressTotal`，终态不展示）+ `err`；明细补齐 `indexedFiles` / `chunkCount` / `tokenizer` / `exts`；索引配置（exts/排除规则）**仅 dirty 才写库**，并提供**「恢复默认」**（删项目级键 → 回落引擎默认集）。**文档索引区块（2026-09-29）**：开关 `vfts.docs`（默认关）+ 体积上限 `vfts.doc-max-mb`（默认 50）+ **状态行**「转换服务：未运行 / 已就绪（端口 N）」（源 = `vfts.status.docsService`/`docsPort`）+ **MCP 注册指引**（与 `src/mcps/markitdown/README.md` §3.1 同文案 + 复制按钮）；计数 `docsSkipped`/`docsFailed` 有值即显示。保存仍走**一次批量写**（`setConfigs`），禁 `watch`/`watchEffect`。

> 与 codegraph（§3.3）同构：引擎外置 exe + 按 workdir 引用计数收敛工具注册；差异 = vfts **仍为全局共享 client**（一对多、按工具参数 `workdir` 路由），而 codegraph **已按 workdir 独立 client/子进程**（T-12/P2-4，2026-09-15 后）；另差异 = vfts 仅 1 个查询工具、索引由本插件增量/全量编排（`vfts.exts` 变更强制重建）。

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
| history | 检查点链（`commit-tree` 产物，parent 线性串联，ref 只指向链头）；状态 `history.status.<slug>` / 时间线 `history.timeline.<slug>`（**会话级内部键**，prjusr 库；I-135） | workdir 的 `.git`（独立 ref `refs/chonkpilot/<slug>` + 持久临时 index `<workdir>/.chonkpilot/history/index`） |
| codegraph | 索引 | `<workDir>/.chonkpilot/codegraph/index.db`（bbolt 单文件库；元信息 `meta.json`）；状态 `codegraph.status`（prj 库） |
| vfts | 全文索引 + 文件清单 | 引擎自持索引（`zvec`，按 workdir）；插件侧回写 `vfts.status` / 清单（prj 库） |

---

## 6. 依赖

- `src/plugin` → `src/lib`。
- `src/plugin-compress` → `src/lib` · `src/data` · `src/plugin`。
- `src/plugin-history` → `src/lib` · `src/plugin`（纯 git，无 DB）。
- `src/plugin-codegraph` → `src/lib` · `src/plugin`。
- `src/plugin-vfts` → `src/lib` · `src/plugin`。
- 被 `src/llm` / `src/gui` / `src/cli` 内嵌。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 内嵌 Hook（非独立进程） | 同一 Bus、编译期注入 | 单体形态简单；独立进程待去中心化 MQ |
| 无宿主函数注入 | 统一走 mq（`llm-simple`） | 解耦、可测试（2026-09-08 定稿） |
| history 纯 git | 废弃 history.db | 复用版本控制、零额外存储 |
| **history = 独立 ref 的检查点链（2026-09-27 批次③）** | 打点走 `refs/chonkpilot/<slug>` + 持久临时 index（`GIT_INDEX_FILE`）；**绝不碰 `.git/index` / HEAD / 用户分支** | 不再污染用户仓库的提交历史；检查点对用户分支不可见、可随时丢弃；`commit-tree` 线性链天然支持相对步 diff/回滚 |
| codegraph 引擎外置 exe | 插件管理子进程 | 引擎需 CGO（主模块禁令） |
| 插件就绪 = server-starting | 全部 Start 成功才广播 | 明确就绪语义 |
| **插件失败 = 上报 + 可见 + 可查（2026-09-20）** | 失败路径经可选 **`Deps.Notify(Notice)`** 如实上报一次 → 宿主 **`Server.pluginNotify`**（`src/llm/server/pluginnotice.go`）**去重/限频**后经**既有通知面 `tool-notify`** 投递 **`notice="plugin-failure"`**（`payload` 只增字段，[61 §4.3](../60-reference/61-消息一览.md) 已登记）；**不阻塞**（仅 publish、不等应答、不改终态、不降级、不抛出）。**去重口径** = 键 `实例×会话×轮次×插件×失败类别` → **同轮同类只提示一次**（轮次或类别变化才再提示；判重表有界 FIFO 淘汰、上限 512）；**失败原因**同经 `Deps.Logf` → 统一出口落 `<dataDir>/logs/gui.log`（含原因），提示文案亦带原因（缺因回落"原因未知（详见日志文件）"） | 不再"记忆没沉淀 / 上下文没压缩"却无从诊断（[41 I-115](../40-roadmap/41-未决项登记.md)）；**不新增 MQ 主题**（复用既有通知面）；前端零改动（`ChatPanel` 既有 `tool-notify` 订阅对非 `completion` 即以轻提示展示、`MessageList` 不落气泡） |

---

## 8. 场景与边界

- **无 git** → history 禁用（不失败；`Start` 记日志后 `return nil`）。
- **非 git 仓库** → history 功能禁用（`hasGit=false`）且 **4 个工具摘除**；即使 `history.enabled=true` 也不注册、不打点。
- **history 打点失败** → 前置钩子路径**拒绝该工具调用**（工具不执行，LLM 可见并能重试）；**连续失败 ≥3 → 熔断放行**（不再拦截，成功一次即复位）。
- **history 未启用 / 不脏 / 非 git / 标「不涉及文件变动」** → 前置钩子**零 git 调用**直接放行（不打点、不拦截）。
- **history 按会话（2026-09-28 闭环，[41 I-135/I-136](../40-roadmap/41-未决项登记.md)）** → `history.status.<slug>` / `history.timeline.<slug>` 为**会话级**内部键（**多会话并发各自独立、互不覆盖**）；`history.clear` 写 JSON `{ts, session}` → **只清目标会话的链**（其它会话链保留）。
- **摘要失败** → compress 不压缩（保持原快照）。
- **插件失败（2026-09-20 起）** → **用户可见 + 日志可查，且不阻塞**：`memory`（记忆沉淀）/ `compress`（上下文压缩）等失败经 `Deps.Notify` 上报 → 宿主判重后经既有 `tool-notify{notice:"plugin-failure"}` 轻提示一次（**同轮同类一次**，见 §7），同时失败原因经统一出口落 `<dataDir>/logs/gui.log`；**本轮对话不受影响**（不改流程终态、不降级、不抛出）。
- **多 workdir** → codegraph **工具面**仍全局注册一份（归属排序后第一个 workdir，按 `context.instance_id` 路由）；**引擎子进程/索引状态已按 workdir 独立**（T-12/P2-4，2026-09-15 后），仅**工具面差异注册留待 v2**。（原「v1 只全局注册一份（限制）」措辞订正。）
- **实例崩溃** → **显式 `instance-exit` 清理**（2026-09-15 后：原「需 `Sweep` 清理（宿主未周期调用则滞留）」作废；**2026-09-16 后：分离形态（`-tags split`）由心跳超时判定（90s）→ 既有 `instanceGone` 兜底清理**，合并单进程形态仍只走显式 `instance-exit`，见 §2 实例视图）。
- ⏸ **上下文配置**（历史轮次上下文的带出 + 上下文的分类与内容沉淀，与 compress/summary 的关系待定）见 [40 P3-7](../40-roadmap/40-演进计划.md)（演进项，暂不开发）。

---

## 9. 现状与待办

- ✅ **compress 阈值已读项目配置** `keep_full_max_turns`/`keep_full_max_tokens`/`compress_token_threshold`（prj 库 config；`compress.go` `resolveOpts`；**旧键 `keep_full_turns` 读时兼容**），缺失/非法回落 `DefaultOptions{10, 24000, 20000}`（[02-配置层级](../00-overview/02-配置层级.md) §5）。**2026-09-24（D1）**：`keep_full_max_turns` = 原 `keep_full_turns` 改名；`keep_full_max_tokens` 新增（保留段 token 上限）。**2026-09-24（A）**：判定语义按用户口径落地 —— 边界算法上提为 `data.LocateFullTurnCount`（压缩侧/组装侧**共用**，消两处重复实现）；`compress_token_threshold` 由「全快照门控」改为**简化区阈值**。**2026-09-25（口径 V/W 修正）**：**「本轮（最新轮）恒保留完整」下限落进共享算法**（压缩侧与组装侧一致，**不再单侧钳制**；更正此前「最新轮永不压缩 / x=0 允许保留段为空」的表述）；取值语义 = `0` 不启用该条件、**两者均 0 → 不压缩**、负数非法（前端提示 + 后端留日志）。**2026-09-25（P3）**：轮次结束时预存该轮 `full_tokens`/`brief_tokens`（`data-session-complete-turn` 只增字段），判定/拼接可直接累加（缺值回退实时估算，`data.ResolveStoredTokens`）。
- ✅ **心跳超时退出判定 = 编译开关（2026-09-15 移除 → 2026-09-16 改由 `-tags split` 编入）**：`instance.Manager.Sweep(timeout)`（90s）与 codegraph/vfts 的 `sweepInstances` 心跳超时判定**仅在分离形态（`-tags split`）编入**（`plugin/instance/heartbeat_split.go`、`plugin-codegraph|plugin-vfts/sweep_split.go`），**合并单进程形态（默认构建）为空实现**（`heartbeat_inprocess.go` / `sweep_inprocess.go`）——实例失效只走**显式 `instance-exit`**（`instance-heartbeat` 订阅保留，供分离形态），空闲引擎子进程仍由 `sweepIdleClient` 回收。详见 §2 实例视图。
- 🗄 **已订正（P0-5，2026-09-10）** ~~：codegraph `client.go` 头注改为「全局共享单个 client（`ensureSharedClient` 懒建）」，不再写"每个 workdir 独立 client"~~。**（2026-09-15 后被推翻：T-12/P2-4 落地 —— `clientFor(workdir)` 每 workdir 独立引擎子进程；原「全局共享单个 client」表述作废，见 §3.3 与 [29-codegraph §4.5](29-codegraph.md)）**
- 🗄 **已修（2026-09-11）**：compress 插件原用 `data.Prj`（团队层）读快照，而快照落 **prjusr** → **生产环境压缩永不触发**；已改 `prjUsrDB`（`data.PrjUsr`），`TestCompressPluginEndToEnd` 通过。
- ✅ **已删（D-09，2026-09-11）**：compress/summarize.go 的 `LLMSummarizer`（HTTP 摘要器；运行时已走 mq `llm-simple`）。
- ✅ **已接（T-26/T-28，2026-09-11；2026-09-27 批次③语义升级）**：history 插件读 prj `history.enabled` 门控（**旧「门控提交到用户分支」口径已被批次③取代** —— 现门控**检查点链打点**，见 §3.2/§7）；compress 按级读 `summary.prompt.md` 作摘要 `system`。
- ✅ **history = 独立 ref 的检查点链（2026-09-27 批次③）**：废弃「轮边界提交到用户分支」，改打 `refs/chonkpilot/<slug>`（根会话；父子共享）+ 持久临时 index（`GIT_INDEX_FILE`，**绝不碰 `.git/index` / HEAD**）；触发 = gateway **前置打点钩子**（`tools/register.pre_hook_subject` → `history-pre-tool-hook`，打点失败即拒绝该工具调用）+ `session-complete` **轮末补点**；脏位来自 `filesys.changed`（不脏零 git 调用）；连续失败 ≥3 **熔断**放行、成功复位；`keep`/`ttl` 任一超限**修剪**（锚点 = 链上最新点时间，保底项不删）；`history.enabled` **仅显式 `"true"` 开（默认关闭）**；git 不可用 / 非 git 仓库 → 禁用 + 4 工具摘除。消息面见 [61 §8 批次③](../60-reference/61-消息一览.md)，配置键见 [64 §4](../60-reference/64-配置项一览.md)，设置页见 [20-gui §12.12](20-gui.md)。
- ✅ **已接线（T-18b，2026-09-15）**：vfts `childIdleTimeout`（5min）接线 —— `sweepIdleClient` 于 15s 扫描中回收**无活跃实例且空闲 ≥5min** 的共享引擎子进程（`sharedClient()` 懒重建；`lastActiveAt` 由 register/heartbeat 刷新），对齐 codegraph T-18；并顺带修 `p.client` 数据竞争（`sharedClient()` 返回指针、`clientMu` 保护）。详见 §3.4。
- ✅ **已接线（T-11/T-18，2026-09-13 后）**：codegraph 索引进度经**既有** `codegraph.status` 面回写（`phase`/`progressDone`/`progressTotal`，零新增主题）+ `configure`/`initialize` 失败重试（缺省 2 次 / 2s）；`childIdleTimeout`（5min）接线，**逐 workdir**回收无引用的空闲引擎子进程（`clientFor(workdir)` 懒重建；2026-09-15 后按 workdir 独立）。详见 §3.3 与 [29-codegraph §4.5](29-codegraph.md)。
- ✅ **记忆手动沉淀 = 新增点分相对主题 `memory.flush`（2026-09-20，批 3 · ⑱；[42 §2 (133)](../40-roadmap/42-决策记录.md)）**：`memory` 插件除既有的 `session-compress`（轮末**异步**沉淀、受 `memory.min-turn-tokens` 门控）外，另订阅点分主题 **`memory.flush`** —— payload `{instance_id, session}`，取该会话**最近一轮**消息 → **复用同一沉淀回路 `distill`**（启用门控 / 类别门控 / 读改写保存**完全同源**），**不受 `memory.min-turn-tokens` 门控**（显式动作即用户意图）；**同步回执** `{ok, session, turn, saved[], failed[], enabled}`（前置不满足 → `{ok:false, reason}`，不静默）；作用域 = payload 指定的 instance + session（不跨实例 / 不跨会话）。前端 = 设置 → 上下文管理「立即沉淀」。明细见 [61 §1.1/§7.1](../60-reference/61-消息一览.md)。
- ✅ **「压缩内容可查」= 复用既有快照面，零新增存储（2026-09-20，批 3 · ⑱）**：上下文压缩的产物**唯一落点** = 会话快照（prjusr `sessions` 表 `history`/`snapshot_turn`；compress 插件回写），故设置 → 上下文管理「压缩内容」小节**直接读既有 `data-snapshot-get`**（首条 system `[已压缩早前对话] <摘要>` = 摘要原文），**不新增主题 / 不新增存储**；**压缩时间不可得**（快照不记时间）→ UI 如实说明。口径见 [61 §3.2b](../60-reference/61-消息一览.md) · [42 §2 (133)](../40-roadmap/42-决策记录.md)。

---

## 10. 关联测试

`src/test/chonkpilot-plugin/unittest/instance/manager_test.go`（实例视图）· `src/llm/server/plugins_test.go`（`server-starting` 广播、compress 端到端）· **`src/plugins/plugin-history/history_test.go`（L1：`gateFromValue` 门控真值表 / 未回读默认关 / `data-prj-config-refresh` 实时同步）** · **`src/plugins/plugin-history/chain_test.go`（L1：检查点链内核 —— 建链且**绝不碰用户分支**（HEAD / `.git/index` 逐字不变）、不脏零 git 调用、门控、工具注册门控（含 `pre_hook_subject`）、修剪（`keep`·`ttl`·锚点=最新点·保底项）、熔断、父子会话共享链、`history_restore` 一致性校验与删除恢复、`diff`·`show`、消息驱动打点）** · `src/plugins/plugin-codegraph/codegraph_test.go`（queryTools 数量=6、名称集合、props 不含 workdir；多 workdir：`TestMultiWorkdirIndexAndQueryIsolation` / `TestClientForPerWorkdirIdentity` / `TestSweepIdleClientPerWorkdir`）· `src/plugins/plugin-vfts/vfts_test.go`（queryTools 数量=1、名称集合）。

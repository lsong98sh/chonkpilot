# 25 · MCP / Agent / 场景 分层模型

> 日期：2026-09-25 ｜ 状态：🔵 设计定稿（待实施；#4 为**既有缺陷修正**）
> 关联：[21-llm-server](../20-modules/21-llm-server.md) · [26-mcp-gateway](../20-modules/26-mcp-gateway.md) · [12-数据层](12-数据层.md) · [37-场景](../30-function-points/37-场景.md) · [39-知识库与原语](../30-function-points/39-知识库与原语.md) · [61-消息一览](../60-reference/61-消息一览.md) · [42-决策记录 §2 (170)](../40-roadmap/42-决策记录.md) · [41 G-47](../40-roadmap/41-未决项登记.md)
> 代码目录（D-28）：`src/lib/llm/server/`（提示词 / 工具面 / 资产注册）· `src/lib/gateway/`（meta 工具 + 目录资产）· `src/lib/data/internal/`（capfs 场景根）· `src/frontend/src/views/`（场景编辑器 / chat 输入框）
> 本篇为**自包含**规格：能力面 / 场景面 / chat 面的职责切分、系统提示词拼接、工具面语义、资产面变更、存储命名与落地分解。**以本篇为准**；与之冲突的既有表述按 §8 逐处订正。

---

## 1. 目标与范围

厘清「**能力面（MCP）/ 场景面 / chat 面**」三层职责，消除 agent 与 skill/prompt 在资产面上的语义重叠 —— 即：**agent 只"注入"不"注册"**、**skill 归能力面由 LLM 自行检索**、**prompt 归知识库维护 + chat 面用户选择注入**。

**非目标**：不改消息面主题与 payload 结构（仅说明文字）；不做既有场景数据的迁移。

---

## 2. 分层模型

```text
能力面（MCP）：tool · skill · resource · prompt    ← 知识库维护（agent 已移出，见 §5）
场景面（scenarios/）：agents                        ← 只注入 context，不注册资产
chat 面：prompt                                     ← 用户在输入框选择注入
```

要点：

- **agent 归场景维护**（场景面），**不属能力面资产**（见 §5）。
- **skill 归知识库（能力面）维护**，由 LLM 自行 `mcp_find(type=skill)` 搜索使用。
- **`agent` 侧不配 skill** —— 原计划的 `ScenarioAgent.Skills` 字段**取消**。
- **prompt 不属 LLM 检索面**（不在 `mcp_find`/`mcp_load` 的 type/kind 内），改由 chat 面用户选择注入（见 §7）。

---

## 3. 系统提示词三层拼接

系统提示词按**下列顺序拼接**：

| 层 | 内容 | 载体 |
|---|------|------|
| 全局 | 身份 / 运行环境 —— 模板 **`你是 {agent名}，一个全能智能体。你运行在 {环境} 中。`**（`agent名` = 当前 agent 的人可读裸名；**通用模式（无场景）= `肥猫`**；`环境` = 代码可确定的客观信息：运行形态 + 平台） | **代码写死**（不落文件、不 embed、不可配置） |
| 场景 | `scenario.description`（"我们是一个团队…"）+ **代码按场景 agents 自动拼接的成员段**（名字 + roleTag + 描述） | **组合生成**（非人写死） |
| agent | 当前 agent 的 prompt | `*.agent.md`（主 agent = `main.agent.md`） |

- **无场景时（=「通用模式」）**：**只注入全局层**，无场景层 / agent 层。**UI 文案 = 「通用场景」**（chat 场景下拉**固定首项**，2026-09-26）：选中即关闭场景；可点 ★ 设为默认（`defaultScenario` 保留值 `__general__`，重开/重启后仍为通用）。
- **现状澄清**：现有代码中的 `memoryGuide`（记忆库指引，门控 = 项目配置 `memory.enabled=true`）与 `assetGuide`（知识库资产指引，门控 = 已接入 capability 节点）是**两条功能指引**，与上述"全局层"**不是一回事**。现状**无**"身份 / 运行环境"全局层 → 属**新增**。
- **`Scenario.SystemPrompt` 语义重定义**：由"派生 = 主 agent 的 prompt"改为 **= 场景层（`description` + 团队拼接）**；**主 agent 的 prompt 归 agent 层**。
  - **落地取舍（2026-09-25 核实：字段保留兼容、不被消费）**：数据层 `Scenario.SystemPrompt` **沿用旧派生值（= 主 agent 的 prompt）保留供兼容、不被消费** —— 场景层提示词由 `llm/server` 侧按本节三条**自行拼接**（`loadScenario` 只取 `description` + `agents`，不读该字段）。**不改为场景层语义**的理由 = 该字段的旧语义（主 agent prompt）仍被 `data-scenario-*` 契约与既有前端/测试消费，改语义会牵连下游；而"场景层 = 拼接生成"已在 `llm/server` 落地，本层无新增消费需求（见 §8.1 #5）。

---

## 4. 工具面

### 4.1 直供（进入 LLM `tools` 参数）

直供 = **`hot` 工具 ∪ gateway in-memory MCP 工具**（`mcp_find` / `mcp_load` / `mcp_invoke`）。

> ⚠️ **`hot` 有两个来源，两者都要**：
> ① 下游 `HotTools`（内嵌 self 节点为 `"*"` = 全部）；
> ② **meta 工具显式 `_meta.hot=true`**。
>
> 依据 = self 节点 `entry` **无 `HotTools`**，`isHot` 不会自动补 → `registerMetaTools` 必须**显式**置 `_meta.hot=true`（否则 LLM 连工具发现入口都拿不到）。**本规格须写明"两者都要"**，防后人只改 `HotTools`、漏掉 meta。
>
> **① 的下游 `HotTools` UI（2026-09-27 改造）**：`EditMCPDialog` 独立「**工具**」页签 —— 点【**加载工具**】按别名列出该 server 工具并逐项勾选；写库为**契约原名**列表（`"*"` = 全部 hot），**零新增消息面**（数据源 = 既有 `tools-list`）。原「运行信息」页签的「高频工具」行与独立弹窗 `SetMCPHotToolsDialog.vue` 均已摘除。

### 4.2 「通用模式」（无场景）工具面

「通用模式」工具面 = **全部 hot（含 core/browser/desktop）+ meta**。此前"仅 core 工具"的说法**已否定**。

### 4.3 白名单（`agent.tools`）语义修正

- **非空 → 下发面 = 白名单里的工具（不论是否 hot）**；
- **空 → hot 集**。

**依据（缺陷）**：现状 `llmTools()` = `toolsForLLM()`（**只含 hot**）再按白名单**取交集** → **白名单中的非 hot 工具会被吞掉**（用户在 agent 里配置了却**永远传不到 LLM**）= **缺陷，须修**（见 §8 #4）。

### 4.4 下发面 vs 执行面

| 面 | 语义 |
|----|------|
| **下发面**（发给 LLM 的清单） | 受 §4.1–§4.3 约束（hot ∪ meta；白名单非空 = 白名单全量、空 = hot） |
| **执行面** | **空白名单 = 不限制**（不拦）；**白名单非空 = 仅白名单内**（其中 **`mcp_invoke` 的目标工具也须在白名单内**，见 [42 §2 (168)](../40-roadmap/42-决策记录.md)） |

---

## 5. 资产面变更：agent 退出资产面

agent **只"注入"不"注册"**：

1. 撤 `syncScenarioAgents`（场景子 agent 资产注册）与 `prompts/register asset_kind=agent`；
2. LLM 通过**注入的团队列表**（§3 场景层）知道可委派谁；
3. `mcp_find` 的 `type` 收敛为 **`tool | skill | resource | all`**（**去掉 `prompt` 与 `agent`**）；`all` = 这三类；
4. `mcp_load` 的 `kind` 同步为 **`tool | skill | resource`**（去 `agent` / `prompt`）；
5. `prompts/list` 也**不得再列 agent**（"知识库看不到 agent"的另一处落点，易被漏）；
6. **prompt 移出 LLM 检索面**：`prompt` 不在 `mcp_find` / `mcp_load` 的 type/kind 内（**含第三方 MCP 的 prompt 亦不可被 LLM 检索**）；prompt 改由**知识库维护 + chat 输入框供用户选择注入**（见 §7）；
7. `assetGuide` 指引正文的 type 清单须同步为 `tool|skill|resource|all`。

**附带结论**：原 [41 G-46] ⑤d 的「`mcp_find` 同名去重非确定」问题**随之作废**（无 agent 类目后不再成立）。

---

## 6. 存储与命名

- 场景使用**独立根 `scenarios/`**（与 `capability/` **平级**），三级仍是 **app 级（系统级）/ user / project**。
- 不允许同名场景（场景 id **全局唯一**，跨级亦然）→ 三级"覆盖"语义**整体不存在**：
  - `list` 无需去重；`load` 直接按 id 命中；
  - `ScenarioSave` 需**新增跨级重名校验**（拒绝并报错）；
  - 既有重名数据**不做迁移**（用户明确"既有的不管"）。
- 三级语义 = **存放位置 / 归属**（**三级均可写**，**不是优先级链**）。**〔订正（2026-09-26）**：app 级（系统级）自 2026-09-26 起**可编辑**（原「随发布只读」作废）；`data-scenario-restore` 消息**已删除**。〕**〔订正（2026-09-29）**：出厂场景 = **磁盘目录** `<exeDir>/scenarios/`（**唯一源** `src/initdata/scenarios/`，由构建脚本投放）；**不再 embed、不再自动物化** —— 原「内容由 embed 内嵌、app 初始化缺失即物化」作废；根缺失即缺装（提示重新安装或用 `initial.zip` 恢复）。〕

### 6.1 命名与唯一性

> **〔2026-09-26 用户拍板 · 根本规则〕** **in-memory 注册名必须唯一** —— 一切资产 / agent 装入内存注册表（self 节点 / 域注册表 / capability 目录扫描）时，**注册名不得冲突**；各命名空间的消歧规则见下表。

| 类目 | 唯一性规则 | 消歧方式 |
|------|-----------|---------|
| **in-memory 注册名（总则）** | **必须唯一** | 冲突时按下列各行的类目规则消歧（**任何情况都不得出现同名同名并存**） |
| **agent** | **可重名** | **加场景前缀**：注册键 = **`<场景id>/<agent名>`**（同名 agent 在不同场景下允许存在，以场景前缀消歧） |
| **场景** | **不许重名** | 场景 id **全局唯一**、跨级亦然（见 §6；`ScenarioSave` 跨级重名校验） |
| **skill / resource / tool** | **不许重名** | 同名即拒绝（不得跨来源并存）；键 = `(scope, kind, name)`（资产）/ `(scope, 暴露名)`（tool） |
| **第三方** | **一律加别名** | 第三方（下游 server，`Origin=user`）条目一律带**来源别名前缀** `<server名>_`（`entrySourcePrefix`），与内置 `self_*`、知识库条目区分 |

- **格式约定（已定，2026-09-26）**：
  - **agent 场景前缀** = **`<场景id>/<agent名>`**（分隔符 `/`；场景 id = 场景目录名，本模块 §6）——**注入面**（§3 团队成员段）、**`llm_run` 委派**、**`agentDelegable`**、**子轮次 system 读取**（`registeredAgentDef` / `resolveAgentDef`）**同一口径**；引用可带前缀（精确命中）或裸名（注册表内**唯一**同名才命中；跨场景重名须带前缀；裸名歧义 → 不解析）。
  - **第三方别名（来源前缀）** = **`<server名>_`**（沿用既有 `applyPrefix` 缺省形态，非新分隔符；`namespace "-"` 对第三方**不生效** → 强制回落该前缀）；名字往返一致（`mcp_find` / `mcp_load` 返回名 = 可直接 `tools/call` / `mcp_invoke` 的名）。
  - **唯一性拒绝策略** = 同类同名注册**拒绝并返回含来源的明确错误**（**不静默覆盖**）；gateway 落点见 [26 §4.3.1](../20-modules/26-mcp-gateway.md)。
- **张力闭环**：[41 G-48](../40-roadmap/41-未决项登记.md) 的 ④（`restore` 写出 user 级同名副本）与 ⑥（`app↔user` 同名 UX 后果）**随本节规则统一处置**（另轨实施）。**〔订正（2026-09-26）**：④ 随 `data-scenario-restore` 消息删除而消失（app 级改为**可直接编辑**）；⑥ 由「app 级可编辑 + 场景 id 全局唯一」承载。〕
- **同场景内 agent 不许重名（2026-09-26 用户裁决，[42 §2 (175)](../40-roadmap/42-决策记录.md)）**：上表「agent **可重名**」指**跨场景**（以 `<场景id>/<agent名>` 前缀消歧）；**同一场景内** agent 名**必须唯一** → 场景**保存**时校验（判定键 = agent **落盘文件名**、大小写不敏感），重名（含**大小写等价** / **主 agent 与子 agent 撞名 `main`** / **空名**）**拒绝保存**并返回含「**场景 id + 重复 agent 名**」的错误（落点 `capfs.WriteScenarioDir` 写盘前）；既有重名数据**不迁移**。

---

## 7. prompt 注入（chat 面）

- 用户在 **chat 输入框选择 prompt** → **注入到 `user` 消息**；
- **HTML 上以 `/<prompt-name>` 的 tag 显示**。

---

## 8. 要改清单（9 项）+ 任务分解

### 8.1 要改清单

| # | 要改 | 落点 | 性质 |
|---|------|------|------|
| 1 | `scenarios/` 独立根（含默认场景物化路径） | `data/internal/capfs`（`ScenarioRoot`）· 装配 | 结构 |
| 2 | agent 只注入不注册（撤两处注册；`mcp_find`/`prompts/list` 摘 agent） | `llm/server/domainmcp.go` · `gateway/meta_tools.go` · `gateway/mcpgateway.go` | 替换 |
| 3 | 系统提示词三层拼接 + 无场景降级 + 全局层**新增内容**（代码写死） | `llm/server/turn.go`（注入处）· 提示词常量 | 结构 |
| 4 | **`llmTools()` 白名单语义修正**（非空取白名单全量，不论 hot） | `llm/server/{turn.go,server.go}` | **缺陷修正** |
| 5 | `SystemPrompt` 语义重定义 | `data/facade/scenario.go` · `capfs/scenario.go` · 消费方 | 结构 |
| 6 | `mcp_find`/`mcp_load` 契约（type/kind + schema 文案）· `assetGuide` 文本 | `gateway/meta_tools.go` · `llm/server/memory_guide.go` | **契约变更** |
| 7 | 默认场景改名「**开发场景**」（**仅显示名**，key `default` 不动）**〔✅ 已落地（2026-09-25）〕** | `src/initdata/scenarios/default/scenario.json`（app 级资源 `name`；原 `materializeDefaultScenario` 已随 T6 撤） | 命名 |
| 8 | 两套内嵌（7 域 agent + 默认场景 1+8）统一为 **app 级场景**；⚠️ 连带 `registeredAgentDef` 的回落来源要改〔**订正（2026-09-26）**：内置 agent 集 `builtin-agents/` **已删除** —— 发布 `scenarios/` 仅出厂场景 `default/`（「开发场景」）；「通用」由**无场景（通用模式）**承载，不再有独立 agent 集〕 | `llm/server/{domainmd.go,domainmcp.go}` · `capfs/defaults.go` | 结构 |
| 9 | 前端：场景编辑器加「**组合后系统提示词**」预览页签；chat 输入框加 **prompt 选择**（注入 user 消息 + `/<name>` tag） | `src/frontend/src/views/**` | 前端 |

> **〔订正（2026-09-29）〕**：#1 的出厂场景**来源 = 磁盘目录**（唯一源 `src/initdata/scenarios/`，由构建脚本覆盖式同步到 `<exeDir>/scenarios/`）——**不再 embed、不再物化**：已删 `data/scenarios_embed.go`（`//go:embed scenarios` + `FactoryScenarios()`）与 `capfs.MaterializeFactoryScenarios`；app 初始化不再写盘，根缺失即缺装（`checkFactoryScenarios` 提示重新安装或用 `initial.zip` 恢复）。**app 级可编辑**（门面 `ScenarioSave`/`ScenarioDelete` 允许 `level=app`，前端列表给编辑/删除入口）、`data-scenario-restore` 消息与 `capfs.CopyScenarioDir` 已删除。
> **〔订正（2026-09-26，历史）〕**：原「出场内容由 `//go:embed scenarios` + `MaterializeFactoryScenarios` 在 app 初始化物化」的机制**已于 2026-09-29 整体作废**（见上条）；原文保留仅作沿革。

### 8.2 任务分解与建议顺序

| 任务 | 覆盖项 | 内容 |
|:---:|:---:|------|
| **T1** | #4 | `llmTools()` 白名单语义修正（**缺陷，最小**） |
| **T2** | #2 + #6 | agent 退出资产面（撤两处注册 + `mcp_find`/`prompts/list` 摘 agent + 契约/指引文案） |
| **T3** | #3 + #5 | 系统提示词三层拼接 + `SystemPrompt` 语义重定义 |
| **T4** | #1 + 重名校验（§6） | `scenarios/` 独立根 + `ScenarioSave` 跨级重名校验（无覆盖语义） |
| **T5** | #9 | 前端（组合后提示词预览页签 · chat 选 prompt） |
| **T6** | #8 | 两套内嵌统一为 app 级场景（含 `registeredAgentDef` 回落来源改口）**〔✅ 已落地（2026-09-25）〕**：落地 `src/initdata/scenarios/`（`default/` = 出厂默认 1 主 + 8 子；`builtin-agents/` 后续删除），撤 `//go:embed contracts/agents/*.agent.md` 与 `capfs.DefaultScenarioAgents`（`defaults.go` 删），list 物化（`materializeDefaultScenario`）撤；`registerDomainAgents` 改经数据层门面 `facade.ScenarioAPI.ScenarioList` 读 app 级场景（路径规则单源）；`registeredAgentDef` 回落 = app 级场景内同名 agent；build 脚本（`build-desktop.ps1` / `build-gui.ps1`）投放 `scenarios/`。**〔订正（2026-09-26）：`builtin-agents/` 已删除 —— 发布 `scenarios/` 仅 `default/`（「开发场景」）；「通用」= 无场景的通用模式（无目录）。机制不变：`registerDomainAgents` 仍扫描**全部** app 级场景（当前仅 1 个），`registeredAgentDef` 回落来源仍为「app 级场景内同名 agent」〕**〔订正（2026-09-29）：投放改为「源唯一 = `src/initdata/scenarios/`，build 脚本覆盖式同步到 `<exeDir>/scenarios/`」，不再 embed/物化〕** |

> 顺序：**T1 → T2 → T3 → T4 → T5 → T6**（T1 独立最小、先修缺陷；T2 定义资产面边界；T3 承接提示词；T4 落存储；T5 前端跟随后端契约；T6 最后做内嵌统一）。

---

## 9. 影响面与测试要点

> 按 [50-测试体系 §1](../50-testing/50-测试体系.md)：**必须"发 MQ 驱动 / 监听 MQ 断言"**，禁止直调组件方法或操作 Vue 内部状态。

| # | 断言 | 驱动 / 观测 |
|:--:|------|------------|
| 1 | 白名单含**非 hot** 工具 → 该工具**出现在** LLM `tools` 参数 | 配 agent 白名单含非 hot 工具 → 发 turn → 断言下发清单含该项（缺陷修正回归） |
| 2 | 无场景（通用模式）→ 提示词**只含全局层** | 发 `llm-start{scenario_id:"-"}` → 断言 system 含全局层、**不含**场景层 / agent 层 |
| 3 | 有场景 → 提示词 = 全局层 + 场景层（description + 成员段）+ agent 层，**按序拼接** | 发 turn → 断言三段顺序与内容 |
| 4 | `mcp_find(type=agent)` **报错或空** | 发 `mcp_find{type:"agent"}` → 断言 `error` 或空结果 |
| 5 | `type=prompt` **不含知识库 prompt** | 发 `mcp_find{type:"prompt"}` → 断言报错或空（知识库 prompt 不可被 LLM 检索） |
| 6 | `prompts/list` **不再列 agent** | 发 `prompts/list` → 断言结果无 agent 类目 |
| 7 | 新增**重名场景** → `save` **报错** | 发 `data-scenario-save`（跨级同名）→ 断言返回错误、未落盘 |
| 8 | chat 选 prompt → **user 消息携带**且 HTML 显示 `/<name>` tag | 输入框选 prompt → 发 `llm-send` → 断言 `user` 消息内容 + DOM `/<name>` tag |
| 9 | 白名单非空 → **执行面**硬拒白名单外工具（含 `mcp_invoke` 目标） | 发 turn 触发白名单外调用 → 断言拒绝执行、不落 gateway、不建节点 |
| 10 | 默认场景显示名 = 「**开发场景**」，key 仍 `default` | 打开场景列表 / 读 `data-scenario-list` → 断言显示名与 key |

---

## 10. 待定 / 未决

- **#12「主窗重命名 → 对话窗标题跟随」**：需**新增广播主题**（按 R-2 须用户确认主题名），**主题名未定** → **待确认**。
- 对话窗**打开瞬间不自读主题**（既有行为，见 [42 §2 (169)](../40-roadmap/42-决策记录.md)）。
- 术语 `step`（一次来回）**已在其余篇章推广落地（2026-09-26，用户「相关都改掉」）**：**语义 = 一次来回**的中文「一次」已统一为「一次来回（`step`）」（见 [60-名词约定 §4](../60-reference/60-名词约定.md) 用词约定 · [42 §2 (173)](../40-roadmap/42-决策记录.md)）；**非该语义不改**。

---

## 11. 关联文档

- [21-llm-server](../20-modules/21-llm-server.md)（提示词注入 / 工具面 / 域 agent）
- [26-mcp-gateway](../20-modules/26-mcp-gateway.md)（meta 工具 `mcp_find`/`mcp_load`/`mcp_invoke`）
- [12-数据层](12-数据层.md)（三级根与 `scenarios/` 独立根）
- [37-场景](../30-function-points/37-场景.md) · [39-知识库与原语](../30-function-points/39-知识库与原语.md)
- [61-消息一览](../60-reference/61-消息一览.md)（唯一准则）· [42 §2 (170)](../40-roadmap/42-决策记录.md) · [41 G-47](../40-roadmap/41-未决项登记.md)

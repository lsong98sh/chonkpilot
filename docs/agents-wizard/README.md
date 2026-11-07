# 场景向导（Agent Wizard）设计包

> 状态：🟡 **部分实施**（2026-11-04 核对；原「✅ 已实施（2026-10-21 核验）」结论不成立）
> 代码落点：`src/lib/llm/server/wizard.go`（`onAgentWizardProbe` / `onAgentWizardGenerate` / `onAgentWizardSkip`）· `wizard_compose.go` · 前端 `views/scenario/ScenarioWizardDialog.vue` · i18n `locales/*/wizard.json` · 测试 `wizard_compose_test.go` / `wizard_generate_test.go` · L4 `systest/run_sem_wizard.py`
> 关联：[提示词内容纲要](../提示词内容纲要.md) · [模式选项详细](../模式选项详细.md) · [25-MCP与场景分层模型](../../docs/spec/10-architecture/25-MCP与场景分层模型.md) · [37-场景](../../docs/spec/30-function-points/37-场景.md) · [61-消息一览](../../docs/spec/60-reference/61-消息一览.md) · [28-plugins](../../docs/spec/20-modules/28-plugins.md)

> **实施结论（2026-11-04 核对）**
>
> **已实施**：GUI 桥 `gui.init-data` 检测 + `agent-wizard` 事件 + `wizard_required` 兜底；5 步向导 UI；`agent-wizard-probe/compose/generate/skip` 四方法面；生成写场景（项目级）+ 项目记忆 + prj 配置 + `project_spec.md` + 可选 `git init`；场景编辑页【向导】按钮；i18n；L1/L2/L4 测试。
>
> **未落 / 与本文设计不一致**：
> - **LLM 推荐**（D10 / 01 §2–§3）：向导 **0 次 LLM 调用**；Step2/Step3 默认值硬编码，Step1 输入不参与推荐。2026-11-05 已把 UI 文案改为「项目扫描 + 默认值预填」，消除虚假承诺。
> - **`project_spec.md` 全量快照**（D16 / 04 §5.1）：实际为人类可读散列，无 YAML front-matter；缺 `probe/choices/memory/scenario_id/level/wizard_version`。
> - **D17「其他」逃生舱（部分实现，2026-11-05）**：按钮已关闭向导并打开 `ScenarioEditDialog`（新建态，预填名称/描述，保存后广播 `scenarioReload`）；从编辑器内打开的向导（`origin='editor'`）只关闭回退，不叠新编辑器。**仍未补写 `mode=custom` spec**（spec schema 待定，当前只走向导生成路径）。
> - **D23 生成进度**：成功后**自动关闭**；仅有 spinner，无步骤条 / 索引进度 / 分步重试。
> - **D21 记忆 AI 优化**：agent 提示词可优化；记忆「沉淀提示词」**无 AI 优化、无恢复**，且不落盘。
> - **预置 agent 数量**：实际 **24 个**（非 8 / 21）；文件格式为 **v2**（`# 基本信息` + YAML）。详见 [02](./02-预置智能体目录.md)。

## 1. 一句话目标

应用启动时检测工作目录下的 `.chonkpilot/project_spec.md`；**缺失则自动弹出【场景向导】**，**先采集「项目信息」并让 LLM 给出推荐**（有项目则由扫描预填），再按「模式 → 选项」逐步确认，经**可编辑摘要**后，最终用 **预置智能体 + 用户选择 + 公认提示词标准** 合成一组可运行的 agent（= 一个 ChonkPilot 场景），并把结果落盘为 `project_spec.md`。

## 2. 命名与范围

- 规格文件：**`<workDir>/.chonkpilot/project_spec.md`**（含**项目技术栈等信息**，见 [04](./04-目录探测与project_spec.md)）；存在即「已初始化」标记。
- 功能名：**场景向导（Agent Wizard）**；产物：**场景（scenario）** = `scenario.json` + `main.agent.md` + 被引用的 `*.agent.md`。
- **1.0 范围 = 开发域**；量化炒股 / 财务处理等**行业域**作为 **2.0**（见 [06](./06-行业域扩展(2.0).md)）。

## 3. 与现有资产对齐

| 概念 | 现有实现 | 向导如何用 |
|------|----------|-----------|
| 场景 | `<级别根>/capability/scenarios/<id>/`（`scenario.json` + `main.agent.md`） | 向导产物落**项目级** `<workDir>/.chonkpilot/capability/scenarios/<id>/` |
| 子 agent | `scenario.json.agents` = 引用路径（`${exeDir}…/${usrDir}…/${workDir}…`） | 合成成功后**写项目级 agent 文件再引用**（`ProjectAgentWrite`）；合成失败才回落引用出厂 |
| 预置 agent | `src/initdata/capability/agents/*.agent.md`（**24 个**） | 向导的「智能体积木」；实际 24 = 23 个 02 目录角色 + `新项目需求沟通员` |
| skill / prompt | `src/initdata/capability/skills/**`、`prompts/**` | 由 [prompts-ref](file:///e:/BizWorks/chonkpilot/prompts-ref) 提炼（见 [02 §6](./02-预置智能体目录.md)） |
| agent 文件 | **v2 契约**：`# 基本信息`（YAML：name/title/roletag）+ `# 摘要` + `# 正文` | 合成结果严格沿用该格式（旧 `[meta]/[description]/[content]` 已废） |
| 系统提示词 | 三层拼接：全局(代码写死) → 场景层(description+团队段) → agent 层 | 向导只生成**场景层 + agent 层**（全局层不动） |
| 就绪信号 | **`gui.init-data` 应答检测**（桥带 `wizard_required` 兜底）；无 `server-starting` wizard 逻辑 | 作为「ready」触发点（见 [05](./05-插件与消息设计.md)） |
| 弹窗载体 | 自研 `DialogShell.vue` + `dialog` manager | 向导对话框用它承载 |

## 4. 总流程

```text
启动
 └─ 就绪（前端 `gui.init-data` 应答；实际路径）
     └─ 检测 <workDir>/.chonkpilot/project_spec.md
         ├─ 存在 → 正常进入（场景编辑页提供【向导】按钮可重跑）
         └─ 缺失 → 发出 agent-wizard 信号 → 前端弹【场景向导】
                        │
                        ├─ Step1 项目信息 ★（新建=用户填 目的/概要/预算/背景/要求；有项目=扫描预填）
                        ├─ Step2 模式选择（设计：LLM 依项目信息预选；⚠️ 未实现，按探测二值定默认）
                        ├─ Step3 技术栈与架构（设计：LLM 预填推荐；⚠️ 未实现，默认值硬编码；全部组单选/多选 + 【其他】自定义）
                        ├─ Step4 摘要确认（★可编辑：项目概要/架构/开发语言/团队成员…）
                        └─ Step5 预览与生成（场景 / 组合提示词 / 项目记忆 三 tab）
                               └─ 写入 场景目录 + 项目记忆 + project_spec.md
```

> **Step1 = 关键新增**：不再让用户一上来逐项选技术；设计为新建只填「项目信息」→ **LLM 先推荐**。⚠️ 2026-11-04：**LLM 推荐未实现**；有项目时探测仅预填 summary/deploy。
> **Step4**：预览前置的**可编辑摘要**，用户修改确认后才进入预览。

## 5. 交付物索引

| 文件 | 内容 |
|------|------|
| [01-交互流程与界面设计](./01-交互流程与界面设计.md) | 检测时序、分流、多步向导、摘要步、界面布局 |
| [assets/wizard-mockup.html](./assets/wizard-mockup.html) | 可直接打开的三栏静态界面样张 |
| [02-预置智能体目录](./02-预置智能体目录.md) | 预置 agent 清单、每项内容、prompts-ref 提炼映射 |
| [03-提示词合成设计](./03-提示词合成设计.md) | 合成规则、默认 Leader 提示词、模板 |
| [04-目录探测与project_spec.md](./04-目录探测与project_spec.md) | 探测项、**空目录定义**、规格文件 schema |
| [05-插件与消息设计](./05-插件与消息设计.md) | ready 触发、B+A 方案、消息面与【向导】按钮 |
| [06-行业域扩展(2.0)](./06-行业域扩展(2.0).md) | 量化炒股 / 财务处理等行业域设想 |
| [07-字段与交互规格](./07-字段与交互规格.md) | **字段级规格**：控件 / 初始值 / 必填 / 校验 / 联动 + 状态矩阵 + 可操作性 |

## 6. 关键设计决策

| # | 决策 | 取值 |
|---|------|------|
| D1 | 触发方式 | **A（已定案 2026-10-04）**：**GUI 桥检测**（`gui.init-data` → `agent-wizard` 事件 + `wizard_required` 兜底）；**瘦插件不做**——A 已覆盖全部功能（见 05 · [41 D-44](../../docs/spec/40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决)） |
| D2 | 场景落级别 | 项目级（`<workDir>/.chonkpilot/capability/scenarios/`） |
| D3 | 产物形式 | 🟡 **部分实施**：合成成功后写**项目级** `capability/agents/` 并引用；合成失败才回落引用出厂（非「优先引用出厂」） |
| D4 | 幂等 | `project_spec.md` 即「已初始化」标记 |
| D5 | Leader | **不由用户选**，使用**默认 Leader 提示词**（见 03 §5） |
| D6 | 委派深度 | **不暴露**给用户（不参与向导） |
| D7 | 团队成员 | 摘要步按**成员清单**呈现（非「规模」数字），可增删 |
| D8 | 重入 | 场景编辑页新增【向导】按钮，可再次生成 |
| D9 | 行业域 | 列为 2.0（06），1.0 仅开发域 |
| D10 | 项目信息先行 | **设计：新建=用户描述意图 → LLM 推荐**；⚠️ **LLM 推荐未实现**（Step1 表单/必填在，推荐缺失）；有项目=探测仅预填 summary/deploy |
| D11 | 选项控件 | **只允许单选/多选两种构造**（禁裸下拉），**每组含【其他】+自定义输入**（Zig/MoonBit/JPA…），见 01 §5.1 |
| D12 | 目标平台 | **多选可组合**（前端/后端/Web/移动端/小程序）；选 Web/移动端/小程序**自动勾选「前端」** |
| D13 | 项目类型 | 新增分类：业务型 / 组件型 / 基建型 / 游戏 / 其他（见 01 §5.3） |
| D14 | 项目信息提示 | 提示以 **Label 旁 `?` tooltip（悬停）** 呈现；输入框**空白、不预填**实际文本 |
| D15 | Step3 按模式分叉 | 新建=脚手架 / 迭代=上下文采集 / 重构=约束定义；B/C 技术栈取自**扫描**（只读可改）；**B 不设「迭代任务类型」**（专项角色按需手选），见 01 §5 |
| D16 | project_spec | **设计：**= 向导最终选择的**全量快照**；⚠️ **未实现**：实际为人类可读散列（无 YAML front-matter / 无 probe/choices/memory/scenario_id/level/wizard_version），见 04 §5 |
| D17 | 「其他」模式 | **已实现（2026-11-05）**：关闭向导 → 打开既有 `ScenarioEditDialog`（新建态，预填名称/描述）；保存后广播 `scenarioReload`；从编辑器内打开时只关闭回退；**不补写** `project_spec.md`（`mode=custom` 待 spec schema 决策） |
| D18 | 新 MQ 主题 | ✅ **已授权**（2026-10-04）`agent-wizard*` 4 个；**须配套门面**（`facade.API.ProjectSpec*` / `ProjectProbe`）并同步 61 + 测试，见 05 §4.3/§4.4 |
| D19 | 项目记忆进向导 | 记忆编辑并入**预览页 tab**（场景 / 组合提示词 / 项目记忆）：LLM 依项目内容预填预置 8 类；**内容 / 种类（可增删启停）/ 沉淀提示词 均可编辑**，复用既有 `data-memory-*` 与 `data-prj-config-*`，经门面写入 `<workDir>/.chonkpilot/memory/`，见 01 §7.2 |
| D20 | 预览页（Step5） | = 「场景编辑」**同构 + 记忆 tab**，且为**可编辑的生成前确认台**（场景名/描述/主 agent 提示词/成员增删/记忆可改；子 agent 引用正文只读）；「生成」写 场景 + 记忆 + `project_spec.md`，见 01 §7.1 |
| D21 | 提示词 AI 优化 | 🟡 **部分实施**：**每个 agent（含主 agent）**可【AI 优化】；**记忆沉淀提示词无 AI 优化、无「恢复」**，且不落盘（复用 `prompt-optimise`，`useCase=agent`） |
| D22 | 辅助选项（项目配置预设） | Step4 增设：记忆库 `memory.enabled` / 用户偏好 / codegraph `plugins.codegraph.enabled` / vfts `plugins.codegraph.vfts.enabled`（**仅现有项目**未做门控）/ 无 git 时可初始化 git + `history.enabled`；均为预设、后续可改。~~旧键名 `enable-codegraph` / `enable-vfts` 已过时~~ |
| D23 | 生成过程 | ⚠️ **未实现**：点「生成」成功后 **`dismissDialog()` 自动关闭**；生成态仅 spinner，**无步骤 / 索引进度 / 分步重试**（设计：不自动关闭 + 步骤 + 索引进度 + 可后台继续） |
| D24 | 预置记忆类别 | 不可删已实现；⚠️ **停用未实现**（无开关）；自定义类别可增删 |
| D25 | 字段级规格（补粗） | 新增 [07-字段与交互规格](./07-字段与交互规格.md)：逐字段 控件/初始值/必填/校验/联动 + 状态矩阵 + 可操作性 |
| D26 | Step5 收右栏 | ⚠️ **N/A**：向导自始为两栏（rail + content），无右侧「即将生成的智能体群」，不存在「隐藏」 |
| D27 | 按钮语义 | 🟡 上一步 / 下一步 / 生成 / 默认 / 关闭在；**「存为预设」写 `localStorage['chonkpilot-wizard-preset:<workDir>']`（非 `wizard/preset.json`）**；「生成」成功后**自动关闭**；`project_spec.md` 只在生成成功时写 |
| D28 | 自动弹策略 | **方案1**：不做持久"不再弹"开关；**只要无 `project_spec.md`，启动必弹**（无 spec 即未初始化） |
| D30 | 实施口径（2026-10-04） | 检测落 **GUI 桥**（A 方案；**插件不做**）；**4** 方法面（`probe` / `compose` / `generate` / `skip`）+ `agent-wizard` 事件；生成经 data 门面**直调**（**无新 `data-*` 主题**）；辅助选项经 `ConfigKVSet` 写 prj 并广播 `data-prj-config-refresh` |
| D29 | 记忆种子可扫描性 | 8 类**非都能扫**：✅开发规范/构建发布/测试；🟡接口库/项目概要/共同库；❌典型参照/用户决策。种子**逐类标注来源与置信度**，扫不到**留空+待补充、不编造**，见 01 §7.5 |

- **实施状态（2026-11-04 核对）**：🟡 **部分实施** —— 数据域 `project`（`ProjectAPI`）· llm-server **四**方法面（probe/compose/generate/skip，generate 写 场景+记忆+配置+spec+可选 git init）· GUI 桥检测（A 方案）· 前端 5 步向导（含记忆/预览/【向导】按钮/i18n/刷新）· **24 个出厂预置 agent** · `docs/spec` 同步（[61](../../docs/spec/60-reference/61-消息一览.md)/[37](../../docs/spec/30-function-points/37-场景.md)/20-gui/22-data/64）· L1/L2 测试（含配置写回读 + `data-prj-config-refresh` 广播 + 预置 agent 守卫）。本次**未跑构建**，不验证 `.\build-desktop.ps1` 通过；未落项见文头。

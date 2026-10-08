# 场景向导（Agent Wizard）设计包

> 状态：🔵 设计稿 ｜ 日期：2026-10-04
> 关联：[提示词内容纲要](../提示词内容纲要.md) · [模式选项详细](../模式选项详细.md) · [25-MCP与场景分层模型](../../docs/spec/10-architecture/25-MCP与场景分层模型.md) · [37-场景](../../docs/spec/30-function-points/37-场景.md) · [61-消息一览](../../docs/spec/60-reference/61-消息一览.md) · [28-plugins](../../docs/spec/20-modules/28-plugins.md)

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
| 子 agent | `scenario.json.agents` = 引用路径（`${exeDir}…/${usrDir}…/${workDir}…`） | 优先**引用**出厂预置 agent，不复制 |
| 预置 agent | `src/initdata/capability/agents/*.agent.md`（8 个） | 向导的「智能体积木」；不足处新增（见 [02](./02-预置智能体目录.md)） |
| skill / prompt | `src/initdata/capability/skills/**`、`prompts/**` | 由 [prompts-ref](file:///e:/BizWorks/chonkpilot/prompts-ref) 提炼（见 [02 §6](./02-预置智能体目录.md)） |
| agent 文件 | `# 名` + `[meta]`(ismain/roletag) + `[description]` + `[content]` | 合成结果严格沿用该格式 |
| 系统提示词 | 三层拼接：全局(代码写死) → 场景层(description+团队段) → agent 层 | 向导只生成**场景层 + agent 层**（全局层不动） |
| 就绪信号 | `server-starting`（插件全部 Start 后广播，桥已转发前端） | 作为「ready」触发点（见 [05](./05-插件与消息设计.md)） |
| 弹窗载体 | 自研 `DialogShell.vue` + `dialog` manager | 向导对话框用它承载 |

## 4. 总流程

```text
启动
 └─ 就绪(server-starting / instance-register 拿到 workdir)
     └─ 检测 <workDir>/.chonkpilot/project_spec.md
         ├─ 存在 → 正常进入（场景编辑页提供【向导】按钮可重跑）
         └─ 缺失 → 发出 agent-wizard 信号 → 前端弹【场景向导】
                        │
                        ├─ Step1 项目信息 ★（新建=用户填 目的/概要/预算/背景/要求；有项目=扫描预填）
                        ├─ Step2 模式选择（LLM 依项目信息预选：新构建/迭代/重构/其他）
                        ├─ Step3 技术栈与架构（LLM 预填推荐；全部组单选/多选 + 【其他】自定义）
                        ├─ Step4 摘要确认（★可编辑：项目概要/架构/开发语言/团队成员…）
                        └─ Step5 预览与生成（场景 / 组合提示词 / 项目记忆 三 tab）
                               └─ 写入 场景目录 + 项目记忆 + project_spec.md
```

> **Step1 = 关键新增（本项目回合）**：不再让用户一上来逐项选技术；新建只填「项目信息」→ **LLM 先推荐**。有项目则**扫描预填**、可改。
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
| D1 | 触发方式 | **A（已定案 2026-10-04）**：**GUI 桥检测**（`gui.init-data` → `agent-wizard` 事件 + `wizard_required` 兜底）；**瘦插件不做**——A 已覆盖全部功能；browser 形态不自动弹（见 05 · [41 D-44](../../docs/spec/40-roadmap/41-未决项登记.md)） |
| D2 | 场景落级别 | 项目级（`<workDir>/.chonkpilot/capability/scenarios/`） |
| D3 | 产物形式 | 引用出厂 agent + 生成场景层与主 agent |
| D4 | 幂等 | `project_spec.md` 即「已初始化」标记 |
| D5 | Leader | **不由用户选**，使用**默认 Leader 提示词**（见 03 §5） |
| D6 | 委派深度 | **不暴露**给用户（不参与向导） |
| D7 | 团队成员 | 摘要步按**成员清单**呈现（非「规模」数字），可增删 |
| D8 | 重入 | 场景编辑页新增【向导】按钮，可再次生成 |
| D9 | 行业域 | 列为 2.0（06），1.0 仅开发域 |
| D10 | 项目信息先行 | **新建=用户描述意图 → LLM 推荐**；有项目=扫描预填；新增 Step1（见 01 §3） |
| D11 | 选项控件 | **只允许单选/多选两种构造**（禁裸下拉），**每组含【其他】+自定义输入**（Zig/MoonBit/JPA…），见 01 §5.1 |
| D12 | 目标平台 | **多选可组合**（前端/后端/Web/移动端/小程序）；选 Web/移动端/小程序**自动勾选「前端」** |
| D13 | 项目类型 | 新增分类：业务型 / 组件型 / 基建型 / 游戏 / 其他（见 01 §5.3） |
| D14 | 项目信息提示 | 提示以 **Label 旁 `?` tooltip（悬停）** 呈现；输入框**空白、不预填**实际文本 |
| D15 | Step3 按模式分叉 | 新建=脚手架 / 迭代=上下文采集 / 重构=约束定义；B/C 技术栈取自**扫描**（只读可改）；**B 不设「迭代任务类型」**（专项角色按需手选），见 01 §5 |
| D16 | project_spec | = 向导最终选择的**全量快照**（含各模式 Step3 字段）；与运行实体「场景」**解耦**，见 04 §5 |
| D17 | 「其他」模式 | 逃生舱：跳过模板，**直接复用「场景编辑」**（`ScenarioEditDialog`）；保存后补写 `project_spec.md`（mode=custom），见 01 §4.1 |
| D18 | 新 MQ 主题 | ✅ **已授权**（2026-10-04）`agent-wizard*` 4 个；**须配套门面**（`facade.API.ProjectSpec*` / `ProjectProbe`）并同步 61 + 测试，见 05 §4.3/§4.4 |
| D19 | 项目记忆进向导 | 记忆编辑并入**预览页 tab**（场景 / 组合提示词 / 项目记忆）：LLM 依项目内容预填预置 8 类；**内容 / 种类（可增删启停）/ 沉淀提示词 均可编辑**，复用既有 `data-memory-*` 与 `data-prj-config-*`，经门面写入 `<workDir>/.chonkpilot/memory/`，见 01 §7.2 |
| D20 | 预览页（Step5） | = 「场景编辑」**同构 + 记忆 tab**，且为**可编辑的生成前确认台**（场景名/描述/主 agent 提示词/成员增删/记忆可改；子 agent 引用正文只读）；「生成」写 场景 + 记忆 + `project_spec.md`，见 01 §7.1 |
| D21 | 提示词 AI 优化 | **每个 agent（含主 agent）与每类记忆的沉淀提示词**均可【AI 优化】；复用 `prompt-optimise`（`useCase=agent\|memory`），见 01 §7.3 |
| D22 | 辅助选项（项目配置预设） | Step4 增设：记忆库 `memory.enabled` / 用户偏好 / codegraph `enable-codegraph` / vfts `enable-vfts`（仅现有项目）/ 无 git 时可初始化 git + `history.enabled`；**均为预设、后续可改**，见 01 §6.1 |
| D23 | 生成过程 | 点「生成」**不自动关闭**向导；切「生成中」态显示步骤 + **索引进度条**（复用 `codegraph.status`/`vfts.status`），**可手动关闭**、后台继续，见 01 §7.4 |
| D24 | 预置记忆类别 | 保持**不可删**（仅清空 / 停用）；自定义类别可增删（原 D-43 已撤回） |
| D25 | 字段级规格（补粗） | 新增 [07-字段与交互规格](./07-字段与交互规格.md)：逐字段 控件/初始值/必填/校验/联动 + 状态矩阵 + 可操作性 |
| D26 | Step5 收右栏 | 进入「预览与生成」隐藏右侧「即将生成的智能体群」，宽度让给编辑区 |
| D27 | 按钮语义 | 上一步 / 下一步(**生成**) / **默认**(Step2 内，一键全默认生成并关闭) / **存为预设**(写 `wizard/preset.json`、不写 spec、不关闭) / **关闭**(不写文件)；`project_spec.md` **只在生成成功时写**，见 01 §10 |
| D28 | 自动弹策略 | **方案1**：不做持久"不再弹"开关；**只要无 `project_spec.md`，启动必弹**（无 spec 即未初始化） |
| D30 | 实施口径（2026-10-04） | 检测落 **GUI 桥**（A 方案；**插件不做**——A 已覆盖全部功能；browser 不自动弹）；3 方法面 + `agent-wizard` 事件；生成经 data 门面**直调**（**无新 `data-*` 主题**）；辅助选项经 `ConfigKVSet` 写 prj 并广播 `data-prj-config-refresh` |
| D29 | 记忆种子可扫描性 | 8 类**非都能扫**：✅开发规范/构建发布/测试；🟡接口库/项目概要/共同库；❌典型参照/用户决策。种子**逐类标注来源与置信度**，扫不到**留空+待补充、不编造**，见 01 §7.5 |

- **实施状态**：✅ **已完成（2026-10-04）** —— 数据域 `project`（`ProjectAPI`）· llm-server 三方法面（probe/generate/skip，generate 写 场景+记忆+配置+spec+可选 git init）· GUI 桥检测（A 方案）· 前端 5 步向导（含记忆/预览/【向导】按钮/i18n/刷新）· **21 个出厂预置 agent** · `docs/spec` 同步（[61](../../docs/spec/60-reference/61-消息一览.md)/[37](../../docs/spec/30-function-points/37-场景.md)/20-gui/22-data/64）· L1/L2 测试（含配置写回读 + `data-prj-config-refresh` 广播 + 预置 agent 守卫）· `.\build-desktop.ps1` 通过。**插件不做**（A 已覆盖全部功能）；browser 形态不自动弹（设计口径）。

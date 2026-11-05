# ChonkPilot 规格文档集（docs/spec）

> 状态：✅ **正式规格唯一入口**（2026-11-04 核对/收口：目录规模、索引、状态与源码路径表按现行代码校正）
> 规模（2026-11-04 实点）：`00-overview/`(5) · `10-architecture/`(17) · `20-modules/`(19，含 2G/2H/2I) · `30-function-points/`(11) · `40-roadmap/`(4) · `50-testing/`(4) · `60-reference/`(6 篇 .md + `61-messages.schema.json`) · `70-conventions/`(5) · `_templates/`(3)
> 收敛：`docs/spec/` = 正式规格唯一入口；根目录专题稿仍作为**设计 / 决策记录**维护（不是正式规格，结论以本目录为准）；历史稿归档在 `trash/old/`（不入库）。消息面唯一准则 = [61-消息一览](60-reference/61-消息一览.md)（原「消息面参考」稿已并入）。
> **引用规则（2026-11-04 核对/收口）**：① 本目录正文**允许引用根目录专题稿**（`docs/<专题>/…`），但必须标注「专题稿 / 设计记录」，且仅作背景，结论以 spec 为准；② **禁止引用 IDE 侧规则（`.trae/` 等）与 `trash/` 历史稿**（链接 / 路径 / 文件名均不得出现）；③ **引用源码 / 脚本 / 配置路径不受限**——那是定位，不是文档依赖；④ 外部内容优先迁入本目录对应篇章后再互链。决策与迁移台账见 [42-决策记录](40-roadmap/42-决策记录.md) §3/§4。
>
> **〔源码路径对照（2026-10-29 TOPO-1 + D-72 之后）〕**：本目录正文里出现的**旧源码路径**，按下表**逐字等价**阅读（语义 / 结论 / 行号口径均不变；module 名不再沿用 `chonkpilot-lib`）：
>
> | 旧（正文出现） | 现行落点 |
> |---|---|
> | `src/lib` / `src/lib/core`（mq / paths / winlog / winsvc / heartbeat） | `chonkpilot-framework/host/src/core` |
> | `src/lib/gui`（window / registry / testserver / logfile） | `chonkpilot-framework/host/src/gui` |
> | `src/lib/gateway` / gateway 源码 | `chonkpilot-agent/gateway` |
> | `src/lib/router` | `chonkpilot-agent/router` |
> | `src/lib/ignore` · `src/lib/sandbox` | `chonkpilot-util/ignore` · `chonkpilot-util/sandbox` |
> | `src/lib/mcp-tools`（executor / core tools） | **已删除（D-72，2026-10-31）** |
> | `src/plugin-*` / `src/plugins/*`（业务插件） | `chonkpilot-plugins/<name>`（另 `chonkpilot-plugins/codegraph` 含 vfts 引擎） |
> | `src/lib/go-webview2` | 独立仓 `chonkpilot-webview2`（2026-10-25 剥离；本地克隆与主仓同级，`replace` 指向） |
> | 前端机制层 `src/frontend/src/layout-free` | `chonkpilot-framework/host/web/src/layout-free` |
> | 前端 `src/frontend/src/utils/mq.js` | `chonkpilot-framework/host/web/src/utils/mq.js` |
> | `src/data` · `src/filesys` · `src/llm` · `src/task` · `src/mcp-server` | 主仓对应 `src/lib/{data,filesys,llm,task,mcp-server}` |
>
> **可直接执行**的路径（测试/构建命令、脚本与用例文件）已**就地订正**，见 [50 §5.1–§5.3](50-testing/50-测试体系.md) · [51](50-testing/51-FP与测试映射.md) · [52](50-testing/52-test-port驱动.md) · [29 §10](20-modules/29-codegraph.md) · [28](20-modules/28-plugins.md) · [71 §1.3/§5.2](70-conventions/71-编程规范.md) · [64](60-reference/64-配置项一览.md)；[42](40-roadmap/42-决策记录.md) §1/§2 属**历史行**，按上表阅读（41 已于 2026-11-04 清空）。

---

## 1. 本目录定位

`docs/spec/` 是一套**面向产品全貌**的完整规格，回答五类问题：

| 部分 | 回答的问题 | 对应目录 |
|------|-----------|---------|
| 系统整体架构 | 系统由什么组成、怎么跑、怎么通信 | `00-overview/`、`10-architecture/` |
| 每个模块的设计 | 每个模块负责什么、接口、数据、依赖 | `20-modules/` |
| 功能点分级 | 纯业务视角有哪些能力、每种情况会怎样 | `30-function-points/` |
| 后续演进计划 | 下一步做什么、未决什么 | `40-roadmap/` |
| （支撑）测试与参考 | 怎么验证、名词/消息/配置/工具契约 | `50-testing/`、`60-reference/`、`70-conventions/` |

设计的核心原则：

1. **架构按模块组织，功能点按业务组织** —— 二者正交，通过 ID 互链（见 [30-FP分级规范](30-function-points/30-FP分级规范.md)）。
2. **功能点分级以黑盒测试视角为准** —— 分类 = 窗口 / 文件树 / 对话 / 任务 / 错误处理 / 配置…，与运行时模块无关；但**场景可穿透到代码分支**。
3. **一切以消息面为准** —— 涉及消息主题/payload 的表述，最终以 [61-消息一览](60-reference/61-消息一览.md)（测试唯一准则）为基准。

---

## 2. 目录总览

```text
docs/spec/
├── README.md                          ← 本文件（总索引）
├── 00-overview/                       ← 系统整体架构
│   ├── 00-架构总纲.md                 分层 / 组件 / 形态 / 通信 / 边界（总入口）
│   ├── 01-端到端数据流.md             一次消息从 UI 到落库再回灌的完整时序
│   ├── 02-配置层级.md                 三级库 + 一级资源、fallback、各配置项级别
│   ├── 03-构建与部署.md               构建脚本、产物形态、capability 契约部署链
│   └── 04-交付标准.md                 交付物 / 验收口径 / 产物目录
├── 10-architecture/                   ← 横切架构机制
│   ├── 10-分层与依赖.md
│   ├── 11-MQ与消息.md
│   ├── 12-数据层.md
│   ├── 13-生命周期.md                 进程启动/关闭、会话/turn 生命周期
│   ├── 14-安全域-agentbox.md          信任目录 / 沙箱 / 路径约束（正文历史；现行 Low IL）
│   ├── 15-国际化.md                   i18n 机制与命名空间
│   ├── 16-路径解析规范.md             目录字符串解析的唯一规范
│   ├── 17-前端状态与MQ.md             前端状态管理（composable / 事件链）规范
│   ├── 18-工具异步超时与取消.md        工具异步 / 超时 / 取消的控制面统一方案（🔵 待拍板）
│   ├── 19-单进程形态与实例隔离.md          单进程形态与实例隔离（✅ 现行）
│   ├── 20-实例隔离与后端分离.md        实例隔离：按 instance 管理执行池（`execPool`）与隔离边界（✅ 现行）
│   ├── 21-任务层设计方案.md            Task 层独立化：与 LLM/Data/Filesys 平级 · task_id 由调用层分配 · 全同步落库 · 表结构自由（**⚠️ 部分实施：P1/P2 已实施；P3 部分；P4 未排期**）
│   ├── 22-实例与会话标识.md            实例/会话标识口径
│   ├── 23-工程与部署拓扑.md            工程拓扑 / 装配 / 部署形态
│   ├── 24-多窗口模型设计方案.md         主窗口 + 纯对话窗口（**⚠️ 部分实施：MW-1…MW-11 已落地；MW-12/13 部分**）
│   ├── 25-MCP与场景分层模型.md          MCP / Agent / 场景 三层职责（**⚠️ 部分实施：T1/T3–T6 已落地；T2 契约未收敛**）
│   └── 26-remote.md                    Remote 远程接入：Remote 插件 + Remote Server + H5（**⚠️ 部分实施：三端/REST/SSE/RPC 已在；能力协商缺失**）
├── 20-modules/                        ← 每个模块的设计（统一模板）
│   ├── 20-gui.md                      宿主 + bridge + 前端（含预览区 renderType 映射）
│   ├── 21-llm-server.md               会话 / LLM 服务层
│   ├── 22-data-persist.md             数据面（persist）
│   ├── 23-filesys.md                  文件服务
│   ├── 24-lib.md                      mq / paths / winlog / winsvc（dsl 已退役；多数包已迁 framework/core）
│   ├── 25-mcp-server.md               MCP 契约服务（四原语）
│   ├── 26-mcp-gateway.md              MCP 网关聚合
│   ├── 27-mcp-tools.md                🗄 已退场（D-72）；现行工具面见插件仓
│   ├── 28-plugins.md                  插件体系（browser / codegraph / crontab / desktop / file / history / mcp / memory / remote / task / web）
│   ├── 29-codegraph.md                codegraph 插件（引擎内联；非独立 exe）
│   ├── 2B-vfts.md                     vfts 插件（全文检索索引，引擎内联于 codegraph）
│   ├── 2C-UI插件.md                   插件 UI 扩展（iframe view / 预览页 / 管理页 / 状态栏；✅ 已落地）
│   ├── 2D-router.md                   LLM 出网协议适配（canonical / 多协议 / 降级 / 重试）
│   ├── 2E-assembly.md                 入口装配器（能力源 + 执行配置 + 内嵌 gateway）
│   ├── 2F-ignore.md                   gitignore 语义匹配（排除判定单一实现）
│   ├── 2G-会话组件.md                 会话组件（一树一库 / 双 id / fork / 事件）
│   ├── 2H-轮次运行器与职责边界.md      轮次运行器基座与职责边界（起轮/排队/收口仍在 llm/server）
│   └── 2I-工作区管理.md               工作区管理（单源视图 / ProjectOps / Router）
├── 30-function-points/                ← 功能点分级（黑盒业务视角）
│   ├── 30-FP分级规范.md               分类/功能点/场景三级 + ID 规范 + 状态标记
│   ├── 31-窗口与布局.md
│   ├── 32-文件树与文件操作.md
│   ├── 33-对话.md
│   ├── 34-任务.md
│   ├── 35-错误处理与恢复.md           含附录 A · S1–S26 场景明细
│   ├── 36-配置.md
│   ├── 37-场景.md                     场景定义/切换、agents；plan mode、股票分析等为其用法示例
│   ├── 38-检索与索引.md
│   ├── 39-知识库与原语.md
│   └── 3A-工具与编排.md
├── 40-roadmap/
│   ├── 40-演进计划.md                 未来演进（非单体 / 跨进程 / 多实例）
│   ├── 41-未决项登记.md               待确认事项（仅 D-64 开放；其余已收口/历史）
│   ├── 42-决策记录.md                 **历史决策日志（截至 2026-10-27）**；不作为现行事实源
│   └── 49-待实现项.md                 已清空（2026-11-04）；见 §1 引用迁移说明
├── 50-testing/
│   ├── 50-测试体系.md                 准则、分层、桩与脚本、已知边界
│   ├── 51-FP与测试映射.md             功能点 ↔ 测试资产映射 + 覆盖缺口
│   ├── 52-test-port驱动.md            test-port 指令协议与外部驱动范式
│   └── 53-专题-文档-代码-测试对照.md   专题 × 文档 × 代码 × 现有测试 盘点清单
├── 60-reference/
│   ├── 60-名词约定.md                 领域名词 ↔ 代码标识
│   ├── 61-消息一览.md                 消息面**唯一准则**（主题清单 + payload 明细）
│   ├── 61-messages.schema.json        消息面机读 schema（与 61 同源）
│   ├── 61-命名重构-新旧对照表.md       命名重构规划稿（未采纳；仅对照参考）
│   ├── 62-命令行与参数.md             全 exe 参数 + 环境变量
│   ├── 63-DSL语法.md                  🗄 **已作废（历史存档）**：DSL / `dsl_run` / `filesys_run` 等已全部退役
│   └── 64-配置项一览.md                配置项**唯一 reference**（key/级别/默认/消费方/状态）
├── 70-conventions/                    ← 工程规范
│   ├── 71-编程规范.md                 代码组织 / 命名 / 日志 / 错误处理 / 开发环境等
│   ├── 72-工具开发规范.md              工具契约、meta 字段、Output/RawResult 设计
│   ├── 73-重构方法.md                 MVP→工程级重构流程与检查框架（方法论）
│   ├── 74-代码审查视角清单.md          17 视角代码审查清单（冻结）
│   └── 75-utility.md                  全仓无状态 utility（chonkpilot-util）复用规范与守卫
└── _templates/                        ← 写作模板（新增文档前先读）
    ├── 模块设计模板.md
    ├── 功能点清单模板.md
    └── 专题设计模板.md
```

**状态图例**（全目录统一，头部标注）：

| 标记 | 含义 |
|------|------|
| ✅ | 当前有效、与代码一致 |
| ⚠️ | 待核对（可能已过时） |
| 🔵 | 规划 / 未落地 |
| ⏳ | 远期 |
| 🗄 | 历史归档 |
| 🚧 | 初稿（待评审） |
| ⏸ | 暂缓（已登记，暂不推进） |
| ❌ | 双语义：规范篇（71/72/73）表**禁止**；计划 / 清单篇（00/40/35/3A/21）表**未实现 / 缺失** |

---

## 3. 建议阅读顺序

**新读者（了解全貌）**

1. [00-架构总纲](00-overview/00-架构总纲.md) → [01-端到端数据流](00-overview/01-端到端数据流.md)
2. [02-配置层级](00-overview/02-配置层级.md)（理解系统怎么被配置起来）
3. [30-FP分级规范](30-function-points/30-FP分级规范.md) → 各业务域 FP 篇（了解"能做什么、会怎样"）
4. [10-architecture/*](10-architecture/)（按需深入机制）
5. [20-modules/*](20-modules/)（按需深入模块）

**开发者切入代码**：先读 [20-modules/*](20-modules/)，每篇头部有"代码目录"字段直达对应工程。
**写工具/脚本**：先读 [72-工具开发规范](70-conventions/72-工具开发规范.md)；[63-DSL语法](60-reference/63-DSL语法.md) **已作废（仅历史存档）**，不得作为实现依据。
**跑测试**：先读 [50-测试体系](50-testing/50-测试体系.md) → [52-test-port驱动](50-testing/52-test-port驱动.md)。

---

## 4. 维护约定

1. 新增文档先读 [`_templates/`](_templates/)，按其模板写；文件名 `NN-kebab-or-中文.md` 带两位章节号，`README.md` 同步更新目录。
2. 每篇文档头部固定：**状态 / 关联文档 / 代码目录（模块篇）**。
3. 涉及消息主题或 payload 的改动 = [61-消息一览](60-reference/61-消息一览.md) 冻结变更，动前须用户确认。
4. 功能点 ID 一经分配不再复用；删除的功能点标记 `🗄` 并保留 ID。
5. 本套文档与代码不一致时，**以代码 + 61-消息一览 为准**，并回改文档状态为 ⚠️。
6. **内容优先写进本目录**：`spec` 正文允许引用根目录专题稿（须标注「专题稿 / 设计记录」，仅作背景）；**禁止引用 IDE 侧规则（`.trae/` 等）与 `trash/` 历史稿**；**引用源码 / 脚本 / 配置路径不受限**。若某处仍需外部文档内容，按 [42-决策记录](40-roadmap/42-决策记录.md) R-4/R-6 **先迁入对应篇章**（必要时新建篇）再做目录内互链。（2026-11-04 核对/收口）

# ChonkPilot 规格文档集（docs/spec）

> 状态：✅ **正式规格唯一入口**（全目录收敛、补缺口）
> 规模：`00-overview/`(4) · `10-architecture/`(16) · `20-modules/`(16) · `30-function-points/`(11, 146 FP) · `40-roadmap/`(4) · `50-testing/`(4) · `60-reference/`(5) · `70-conventions/`(3) · `_templates/`(3)
> 收敛：原 `docs/` 下全部草稿与专题**已整体废弃移除**；消息面唯一准则 = [61-消息一览](60-reference/61-消息一览.md)（原「消息面参考」稿已并入）。
> **硬性规则**：**本目录不得引用 `docs/spec/` 以外的任何文档**（含历史稿与 IDE 侧规则；链接/路径/文件名均不得出现）。**引用源码 / 脚本 / 配置路径不受限**——那是定位，不是文档依赖。需要的外部内容必须先迁入本目录对应篇章，再做目录内互链。决策与迁移台账见 [42-决策记录](40-roadmap/42-决策记录.md) §3/§4。
>
> **〔源码路径对照：`src` 目录已按 D-28 重排。本目录正文里出现的**旧源码路径**，按下表**逐字等价**阅读（语义/结论/行号口径均不变；module 名不变）：**
>
> | 旧（正文出现） | 新（现行） |
> |---|---|
> | `src/lib`（dsl / mq / paths / winlog / winsvc；module `chonkpilot-lib`） | `src/lib/core` |
> | `src/data` · `src/filesys` · `src/llm` · `src/gateway` · `src/mcp-server` · `src/mcp-tools` · `src/task` | `src/lib/{data,filesys,llm,gateway,mcp-server,mcp-tools,task}` |
> | `src/gui/lib/go-webview2` | `src/lib/go-webview2` |
> | `src/plugin-<name>`（含 `plugin/instance`） | `src/plugins/plugin-<name>` |
> | `src/codegraph`（引擎 exe） | `src/plugins/codegraph` |
> | `src/vfts`（引擎 exe） | `src/plugins/vfts` |
> | `src/cli` | `src/desktop/cli` |
> | `src/standalone`（桌面单体宿主） | `src/desktop`（壳） + `src/lib/gui`（实现） |
>
> **可直接执行**的路径（测试/构建命令、脚本与用例文件）已**就地订正**，见 [50 §5.1–§5.3](50-testing/50-测试体系.md) · [51](50-testing/51-FP与测试映射.md) · [52](50-testing/52-test-port驱动.md) · [29 §10](20-modules/29-codegraph.md) · [28](20-modules/28-plugins.md) · [71 §1.3/§5.2](70-conventions/71-编程规范.md) · [64](60-reference/64-配置项一览.md)；[42](40-roadmap/42-决策记录.md) §2 / [41](40-roadmap/41-未决项登记.md) §2–§3 属**历史行**，按上表阅读。

---

## 1. 本目录定位

`docs/spec/` 是一套**面向产品全貌**的完整规格，回答五类问题：

| 部分 | 回答的问题 | 对应目录 |
|------|-----------|---------|
| 系统整体架构 | 系统由什么组成、怎么跑、怎么通信 | `00-overview/`、`10-architecture/` |
| 每个模块的设计 | 每个模块负责什么、接口、数据、依赖 | `20-modules/` |
| 功能点分级 | 纯业务视角有哪些能力、每种情况会怎样 | `30-function-points/` |
| 后续演进计划 | 下一步做什么、未决什么 | `40-roadmap/` |
| （支撑）测试与参考 | 怎么验证、名词/消息/配置/DSL 语法 | `50-testing/`、`60-reference/`、`70-conventions/` |

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
│   └── 03-构建与部署.md               构建脚本、产物形态、capability 契约部署链
├── 10-architecture/                   ← 横切架构机制
│   ├── 10-分层与依赖.md
│   ├── 11-MQ与消息.md
│   ├── 12-数据层.md
│   ├── 13-生命周期.md                 进程启动/关闭、会话/turn 生命周期
│   ├── 14-安全域-agentbox.md          信任目录 / 沙箱 / 路径约束（🔵 规划）
│   ├── 15-国际化.md                   i18n 机制与命名空间
│   ├── 16-路径解析规范.md             目录字符串解析的唯一规范
│   ├── 17-前端状态与MQ.md             前端状态管理（composable / 事件链）规范
│   ├── 18-工具异步超时与取消.md        工具异步 / 超时 / 取消的控制面统一方案（🔵 待拍板）
│   ├── 19-多租户服务端形态.md          多租户服务端形态 `-browser`（🔵 待拍板）
│   ├── 20-实例隔离与后端分离.md        按 instance 管理执行池（`execPool`）+ 后端分离方向 / 隔离缺口清单（9 项）（🔵 待拍板 / 未实施）
│   ├── 21-任务层设计方案.md            Task 层独立化：与 LLM/Data/Filesys 平级 · task_id 由调用层分配 · 全同步落库 · 表结构自由（**✅ P1/P2 已实施；P3/P4 未排期**）
│   ├── 22-实例与会话标识.md            实例/会话标识与三形态口径
│   ├── 23-工程与部署拓扑.md            工程拓扑 / 装配 / 部署形态
│   ├── 24-多窗口模型设计方案.md         主窗口 + 纯对话窗口（多窗口模型 / 路由 / 数据 / 任务分解 MW）（🔵 设计定稿，待实施）
│   └── 25-MCP与场景分层模型.md          MCP / Agent / 场景 三层职责（能力面 · 场景面 · chat 面；提示词三层拼接 · 工具面语义 · agent 退出资产面 · 场景独立根）（🔵 设计定稿，待实施）
├── 20-modules/                        ← 每个模块的设计（统一模板）
│   ├── 20-gui.md                      宿主 + bridge + 前端（含预览区 renderType 映射）
│   ├── 21-llm-server.md               会话 / LLM 服务层
│   ├── 22-data-persist.md             数据面（persist）
│   ├── 23-filesys.md                  文件服务
│   ├── 24-lib.md                      mq / dsl / paths / winlog / winsvc
│   ├── 25-mcp-server.md               MCP 契约服务（四原语）
│   ├── 26-mcp-gateway.md              MCP 网关聚合
│   ├── 27-mcp-tools.md                工具层（executor，spawn-on-call with agentbox）
│   ├── 28-plugins.md                  插件体系（compress / memory / history / codegraph / vfts）
│   ├── 29-codegraph.md                codegraph（独立 mcp-server）
│   ├── 2A-cli.md                      CLI 单体
│   ├── 2B-vfts.md                     vfts（全文检索索引，独立 mcp-server）
│   ├── 2C-UI插件.md                   UI 插件扩展设计（🔵 规划中 / 未实现，预留）
│   ├── 2D-router.md                   LLM 出网协议适配（canonical / 多协议 / 降级 / 重试）
│   ├── 2E-assembly.md                 入口装配器（能力源 + 执行配置 + 内嵌 gateway）
│   └── 2F-ignore.md                   gitignore 语义匹配（排除判定单一实现）
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
│   ├── 40-演进计划.md                 未来演进（非单体 / browser / 跨进程 / 多实例）
│   ├── 41-未决项登记.md               待确认（待拍板 / 待设计）事项集中登记
│   ├── 42-决策记录.md                 **决策记录**（长期规则 + 时间线 + 迁移台账）
│   └── 49-待实现项.md                 已确认、待实现事项集中登记
├── 50-testing/
│   ├── 50-测试体系.md                 准则、分层、桩与脚本、已知边界
│   ├── 51-FP与测试映射.md             功能点 ↔ 测试资产映射 + 覆盖缺口
│   ├── 52-test-port驱动.md            test-port 指令协议与外部驱动范式
│   └── 53-专题-文档-代码-测试对照.md   专题 × 文档 × 代码 × 现有测试 盘点清单
├── 60-reference/
│   ├── 60-名词约定.md                 领域名词 ↔ 代码标识
│   ├── 61-消息一览.md                 消息面**唯一准则**（主题清单 + payload 明细）
│   ├── 62-命令行与参数.md             全 exe 参数 + 环境变量
│   ├── 63-DSL语法.md                  DSL 语法唯一规范（llm_run/filesys_run/desktop_run/browser_run）
│   └── 64-配置项一览.md                配置项**唯一 reference**（key/级别/默认/消费方/状态）
├── 70-conventions/                    ← 工程规范
│   ├── 71-编程规范.md                 代码组织 / 命名 / 日志 / 错误处理 / 开发环境等
│   ├── 72-工具开发规范.md              工具契约、meta 字段、Output/RawResult 设计
│   └── 73-重构方法.md                 MVP→工程级重构流程与检查框架（方法论）
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
**写工具/脚本**：先读 [72-工具开发规范](70-conventions/72-工具开发规范.md) 与 [63-DSL语法](60-reference/63-DSL语法.md)。
**跑测试**：先读 [50-测试体系](50-testing/50-测试体系.md) → [52-test-port驱动](50-testing/52-test-port驱动.md)。

---

## 4. 维护约定

1. 新增文档先读 [`_templates/`](_templates/)，按其模板写；文件名 `NN-kebab-or-中文.md` 带两位章节号，`README.md` 同步更新目录。
2. 每篇文档头部固定：**状态 / 关联文档 / 代码目录（模块篇）**。
3. 涉及消息主题或 payload 的改动 = [61-消息一览](60-reference/61-消息一览.md) 冻结变更，动前须用户确认。
4. 功能点 ID 一经分配不再复用；删除的功能点标记 `🗄` 并保留 ID。
5. 本套文档与代码不一致时，**以代码 + 61-消息一览 为准**，并回改文档状态为 ⚠️。
6. **内容只写进本目录**：**本目录正文不得引用 `docs/spec/` 以外的任何文档**（历史稿与 IDE 侧规则一律不引用），以保持 spec 自洽可独立交付；**引用源码/脚本/配置路径不受限**。若某处仍需外部文档内容，按 [42-决策记录](40-roadmap/42-决策记录.md) R-4/R-6 **先迁入对应篇章**（必要时新建篇）再做目录内互链。

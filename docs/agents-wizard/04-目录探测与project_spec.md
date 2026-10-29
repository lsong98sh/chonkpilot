# 04 · 目录探测与 project_spec.md

> 关联：[README](./README.md) · [01-交互流程](./01-交互流程与界面设计.md)

## 1. 探测目标

只读侦察工作目录，产出足以推断「模式」与「预填选项」的画像；**绝不修改文件**。新建（空目录）时无内容可探，改由**用户填写项目概要**（见 §5）。

## 2. 【空目录】如何定义

「空」不是「目录里真空」，而是「**无项目内容**」。采用**分层判定**：

**忽略集（任何判定前先剔除）**：
- 工具自身数据：`.chonkpilot/`
- 版本控制元数据：`.git/`
- 操作系统/编辑器噪音：`.DS_Store`、`Thumbs.db`、`.idea/`、`.vscode/`、`__MACOSX/`

**判据（按优先级）**：

| 结论 | 条件 |
|------|------|
| **空目录** | 剔除忽略集后，**无任何文件**（含子目录） |
| **准空** | 仅含**占位文件**：`README.md` / `LICENSE` / `.gitignore` / `.gitkeep`，且**无源码文件、无依赖/构建清单** |
| **有项目** | 存在**≥1 个源码文件**，或存在依赖/构建清单（`package.json` / `go.mod` / `pom.xml` / `build.gradle` / `requirements.txt` / `Cargo.toml` / `*.csproj` …） |

**源码扩展名集**（可配）：
`.go .js .ts .tsx .jsx .vue .svelte .py .java .kt .rs .c .h .cpp .cs .rb .php .swift .dart .scala .sh`

**分流**：
- 空 / 准空 → 默认「新构建（A）」（准空时提示「疑似已有占位文件，确认按新项目初始化？」）。
- 有项目 → 默认「迭代/重构（B/C）」（据 `git log` 与文档推断倾向）。

## 3. 探测项与判据（有项目时）

| 分组 | 探测项 | 判据（示例） |
|------|--------|-------------|
| 语言 | 主语言 | 扩展名统计 + 清单文件 |
| 框架 | 框架 | 依赖清单关键字 |
| 包管理 | npm/pnpm/yarn/go modules/pip | 锁文件存在性 |
| 构建 | 构建工具 | Vite/Webpack/Makefile/… |
| 测试 | 测试框架与命令 | Vitest/Jest/`go test`/pytest |
| 风格 | lint/format | `.eslintrc*`/`.prettierrc`/gofmt |
| 结构 | 目录/架构模式 | 顶层目录 + README |
| 文档 | README/ADR/API | 文件存在性 |
| 规模 | 文件数/行数 | 计数 |
| 版本史 | 既有演进项目？ | `git log` 条数/最近提交（推断 B vs C） |

## 4. 实现落点（实施参考）

- 优先在**服务端**做（有 `workDir` 与文件系统权限）；探测为只读，结果作为内存对象回前端预览，**用户确认后**才写入 `project_spec.md`。
- 可选复用既有引擎：`codegraph`（结构/符号）、`vfts`（内容），但二者**默认关闭**，探测**不依赖**它们。

## 5. project_spec.md（含项目技术栈等）

- **唯一语义**：该文件存在 = 「本项目已完成场景初始化」→ 启动**不再自动弹**向导。
- **= 向导最终选择的全量快照**：完整记录 Step1 项目信息、探测结果、Step2 模式、**Step3 选项（含各模式的选项与【其他】自定义值）**、Step4 摘要、Step5 生成的 `agents` —— 即「**用户最后确认过的所有内容**」。
- 同时**人类可读**（含**技术栈等信息**），供团队与后续会话参考。
- 位置固定：`<workDir>/.chonkpilot/project_spec.md`。
- **新建时「项目概要」由用户填写**（必填）；有项目时由探测预填、用户可改（见 [01 §6](./01-交互流程与界面设计.md)）。

**与「场景」的关系（别混为一谈）**：

| 文件 | 角色 | 内容 |
|------|------|------|
| `project_spec.md` | **初始化快照 / 标记（源）** | 全部选择 + 探测 + 摘要 |
| `<scenario_id>/scenario.json` + `main.agent.md` | **运行实体** | 驱动 LLM 的场景（名称 / 描述 / agents 引用） |

- 二者**解耦**：后续在「场景编辑页」改场景**不回写** `project_spec.md`（它是一次初始化的存档）；仅当用户点【向导】重跑时**覆盖写**（需确认）。
- 因此：`project_spec.md` 是「最后选择的所有内容」的**完整记录**；但**运行时真正生效**的是它生成的**场景**。

> **Step3 按模式记录不同字段**（见 [01 §5](./01-交互流程与界面设计.md)）：A 记技术栈/架构；B 记接入方式/任务类型/影响范围/节奏；C 记重构目标/行为保持策略/验证方式/特殊角色。共同字段（`mode`/`info`/`probe`/`agents`）恒在。

### 5.1 结构（YAML front-matter + Markdown 正文）

```markdown
---
wizard_version: 1
initialized_at: 2026-10-04T10:00:00+08:00
mode: greenfield            # greenfield | iterate | refactor | custom
scenario_id: demo-team
level: project               # 固定项目级
info:                        # ★ Step1 项目信息（新建=用户填写；有项目=扫描预填+可改）
  purpose: 为团队提供任务看板与工时统计      # 项目目的（必填）
  summary: 主要功能、目标用户、期望形态
  budget: 未知                 # 默认以实际文本预填（非 tooltip）
  deployment: 云部署、私有部署
  period: 未知
  background: 团队现状、已有系统、时间要求
  requirements: ["必须用 Go", "需支持私有化部署"]
summary:                     # Step4 用户确认过的摘要
  name: 示例全栈应用
  goal: 一句话目标
  background: 业务背景
probe:                       # 有项目时才有（空目录时省略）—— 扫描的原始结果
  primary_language: typescript
  frameworks: [vue3, gin]
  architecture: three-tier   # 扫描推断的架构模式
  package_manager: pnpm
  build: "pnpm build:embed"
  test: "pnpm test"
  lint: eslint
  structure: monorepo
  file_count: 312
choices:
  type: biz                    # 业务型 | 组件型 | 基建型 | 游戏 | 其他
  platform: [frontend, web, mobile]   # 多选可组合；web/移动端/小程序 自动含「前端」
  frontend: { lang: [typescript], framework: vue3, ui: tailwind, state: pinia, build: vite }
  backend:  { lang: [go], framework: gin, api: rest }
  data:     { db: postgresql, orm: gorm }
  architecture: three-tier     # 两层 | 三层 | 微服务 | Serverless | 其他
  autonomy: key-milestones     # 每步确认 | key-milestones | 全自动
agents:                       # ★ 团队成员（名词），主 agent 固定含 main
  - main
  - 架构设计师
  - 前端开发
  - 后端开发
  - 代码审查
  - 测试工程师
memory:                       # ★ Step5 项目记忆类别（正文落 <workDir>/.chonkpilot/memory/<类别>.md）
  - 项目概要
  - 开发规范
  - 构建发布规则
  - 接口库
  - 测试规范
---

# 项目规格 · demo-team

（正文：目标、约束、验收标准，由向导依据摘要自动生成。）
```

> 字段统一 `snake_case`；`agents` 记录**逻辑名**（可回溯引用路径）。**不含** `leader` / `delegation_depth`（Leader 用默认、委派深度不暴露）。
> `choices` 中**自定义技术**（来自界面【其他】）以用户原值记录，如 `backend.lang: ["go", "zig"]`、`data.orm: "jpa"`。
> **`probe` vs `choices`（回答「扫描的语言/架构是否进 spec」）**：**是**——`probe` 记**扫描原始结果**（含 `primary_language` / `frameworks` / **`architecture`** 等；空目录时省略）；`choices` 记**用户最终确认值**。二者都进 `project_spec.md`；若用户修正了扫描值，**以 `choices` 为准**、`probe` 保留原始以供对照。
> `memory` = Step5 生成的项目记忆**类别清单**（正文落 `<workDir>/.chonkpilot/memory/<类别>.md`，此处只记类别名）。

## 6. 读写时机

| 时机 | 动作 |
|------|------|
| 启动探测 | 只**读**：判定是否存在 |
| 向导「生成」 | 写场景目录 + 写 `project_spec.md` |
| 场景编辑页【向导】重跑 | 覆盖写（需用户确认） |
| 场景切换/编辑 | 不改 `project_spec.md`（spec = 初始化快照；场景 = 运行实体，二者解耦） |

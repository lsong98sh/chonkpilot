# ChonkPilot 文档

> **正式规格唯一入口 = [docs/spec/](spec/README.md)**（2026-11-04 核对/收口）。
> - **`spec/`** = 正式规格：`00-overview` / `10-architecture` / `20-modules` / `30-function-points` / `40-roadmap` / `50-testing` / `60-reference` / `70-conventions`
> - [spec/60-reference/61-消息一览.md](spec/60-reference/61-消息一览.md) = **消息面唯一准则**（msg/payload 冻结，增改删须同步文档 + 补齐测试，见 [42 R-2](spec/40-roadmap/42-决策记录.md)）
> - **根目录专题稿 = 仍在维护的设计 / 决策记录**（**不是**正式规格；与 spec 不一致时以 spec（及 61）为准）：
>   - 业务与交互：`agent-chat/` · `ask-user/` · `layout/` · `ui-config/`
>   - 插件与工具面：`bridge-plugin/` · `plugin-platform/` · `tool-plane/` · `sandbox/`
>   - LLM 与生成：`llm-bridge/` · `llm-modular/` · `llm-retry/` · `next-run/` · `prompts/`
>   - 执行与并发：`async-task/`（统一异步执行与消息循环，2026-11-06 第一批已实施；验收/白名单见专题稿 §11.2/§11.4）
>   - 数据 / 配置 / 向导：`data-optimize/` · `doc-ai-form/` · `input-validation/` · `agents-wizard/`
>   - 根级方法稿：`业务系统设计-思路与方法.md` · `提示词内容纲要.md` · `模式选项详细.md`
>   - 工具：`tools/design-check/`（文档检查脚本，非规格）
> - **归档**：原 `docs/old/`、原 `.trae/documents/`、`arch-notes/`、`src/test/testplan.md` 等历史稿已移入 `trash/old/`（`trash/` 已加入 `.gitignore` —— **不入库、不物理删除**）；**不要再引用**。
> - **`.trae/`** = 规则入口（`rules/项目规则.md`、`rules/测试准则.md`、`rules/开发者宪章.md`）+ 交付技能（`skills/chonkpilot-change-closure/`）；规则内容以 `spec/` 为权威源。
>
> **规则**（2026-11-04 核对/收口）：
> 1. **新内容优先写进 `spec/`**；专题稿只承载设计过程/决策记录，不另立“第二规格”。
> 2. `spec/` **可引用根目录专题稿**，但必须标注「专题稿 / 设计记录」，且仅作背景；结论以 spec 为准。
> 3. `spec/` **禁止引用 IDE 侧规则（`.trae/` 等）与 `trash/` 历史稿**（链接 / 路径 / 文件名均不得出现）。
> 4. 若某处仍需外部文档内容，应**先迁入对应 spec 篇章**（必要时新建篇）再做目录内互链。
>
> 处理决策与待补台账见 [spec/40-roadmap/42-决策记录.md](spec/40-roadmap/42-决策记录.md)。
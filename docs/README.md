# ChonkPilot 文档

> **正式规格唯一入口 = [docs/spec/](spec/README.md)**（2026-09-11 收敛：全部有效内容已进入 `spec/`，本目录不再保留草稿）。
> - **`spec/`** = 正式规格：`00-overview` / `10-architecture` / `20-modules` / `30-function-points` / `40-roadmap` / `50-testing` / `60-reference` / `70-conventions`
> - [spec/60-reference/61-消息一览.md](spec/60-reference/61-消息一览.md) = **消息面唯一准则**（msg/payload 冻结，增改删须用户确认；原「消息面参考」稿已整体并入）
> - **`old/` 已废弃移除**：原 `docs/` 全部草案与专题、原 `.trae/documents/`、`arch-notes/`、`src/test/testplan.md` 等历史稿已整体清出仓库（归档在 `trash/old/`；`trash/` 已加入 `.gitignore` —— **不入库、不物理删除**）；**不要再引用**。
> - **`.trae/`** = 规则入口（`rules/项目规则.md`、`rules/测试准则.md`、`rules/开发者宪章.md`）+ 交付技能（`skills/chonkpilot-change-closure/`）；规则内容以 `spec/` 为权威源
>
> ⚠️ **规则**：新内容只写进 `spec/`；**`spec/` 不得引用本目录以外的任何文档**（历史稿、IDE 侧规则等一律不引用）。若某处仍需外部内容，应先**迁入对应 spec 篇章**（必要时新建篇）再做目录内互链。
> 处理决策与待补台账见 [spec/40-roadmap/42-决策记录.md](spec/40-roadmap/42-决策记录.md)。

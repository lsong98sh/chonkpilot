# ui-config · UI 可配置化（自由布局容器）专题

> 状态：✅ **已实施（P0–P4 + P6 已落，2026-10-17；P5 五项 = 不做）** ｜ 子决策已拍板（[§4](#4-已拍板口径速查)）；落地与未闭环见 [49 `UILAYOUT-1`](../spec/40-roadmap/49-待实现项.md) · [41 G-52/G-53/G-56/G-57/G-58](../spec/40-roadmap/41-未决项登记.md)
> 关联：[20-gui](../spec/20-modules/20-gui.md) · [31-窗口与布局](../spec/30-function-points/31-窗口与布局.md) · [17-前端状态与MQ](../spec/10-architecture/17-前端状态与MQ.md) · [61-消息一览](../spec/60-reference/61-消息一览.md) · [2C-UI插件](../spec/20-modules/2C-UI插件.md)
> 登记：[41 D-61](../spec/40-roadmap/41-未决项登记.md) ✅ 已拍板 · [42 §2.31](../spec/40-roadmap/42-决策记录.md) · [49 UILAYOUT-1](../spec/40-roadmap/49-待实现项.md) 🔵 待实施
> **落点（已定，并入主工程）**：`src/frontend/src/layout-free/`（机制层子系统）· `src/frontend/src/layout/`（适配层）· `src/frontend/src/stores/selection.js`（跨 view 状态）
> 复制源：`E:\BizWorks\layout-free`（独立 Vite + Vue3 + Pinia 参考工程，73 个源文件 / 80 个纯前端主题）

## 1. 一句话结论

把 `layout-free` 整体**并入主前端工程**（`src/frontend/src/layout-free/`），改造成**一个与宿主解耦的大组件**（容器 + 内容设置编辑器 + 配置协议），由适配层把**本系统全部区块注册为 view**，最终由 `MainLayout.vue` 装配 —— 布局、显隐、尺寸、栏（顶部工具栏/状态栏/自定义栏）、抽屉、标签栏位置全部由一份可编辑 JSON 驱动，替代现在硬编码的五层 `SplitPanel` 嵌套。

## 2. 文档索引

| 文档 | 内容 |
|------|------|
| [01-自由布局容器改造设计方案.md](01-自由布局容器改造设计方案.md) | 现状分析 → 目标架构 → 大组件 API / 配置模型 / 区块注册表 / 命令模型 / 消息面 / 持久化 / 去重 / 落地清单 |

## 3. 关键结论速览（详见 01）

| # | 结论 | 依据 |
|---|------|------|
| 1 | **机制层** = `src/frontend/src/layout-free/`（容器/编辑器/协议，与宿主解耦、不认识任何区块）；**适配层** = `src/frontend/src/layout/`（区块注册表 / 命令落点 / 持久化 / 图标 / 主题 / 文案） | [01 §2](01-自由布局容器改造设计方案.md#2-总体方案) |
| 2 | **并入主工程、不建独立工程**：由 `src/frontend` 单次打包，**零双 Vue / 双 mq / 双产物**；`build-desktop.ps1` / `build-browser.ps1` 零改动 | [01 §3.1](01-自由布局容器改造设计方案.md#31-落地形态与目录已定--并入主工程) |
| 3 | **引入 Pinia**（唯一新增运行时依赖）+ **追加 `stores/selection.js`**：把散落的跨 view 状态（当前文件 / 当前主会话 / 当前任务 / 左侧模式 / workDir）收敛为唯一真相源；写入仍只经 mq 订阅者 | [01 §3.7](01-自由布局容器改造设计方案.md#37-状态管理pinia-分层--跨-view-共享状态) |
| 4 | **零新增后端主题**：80 个布局主题全是纯前端事件（单段 kebab），桌面桥 / browser 入口对未知单段主题均静默忽略（`bridge.go:481-485`、`publish.go:207`）；统一加 **`lf-` 前缀**，落 `event-names.js` 前须用户确认清单 | [01 §3.8](01-自由布局容器改造设计方案.md#38-消息面) |
| 5 | 持久化复用既有面：`gui.ui.save` 的 `layout` 子键新增 `free`（JSON 字符串）→ 落 prj 配置 `layout.free`；读取经 `gui.init-data` 结果 `layout.free` 自动带出（`local.go:101`）——**零主题增删、零既有字段改形** | [01 §3.9](01-自由布局容器改造设计方案.md#39-持久化) |
| 6 | 区块一律**常驻保活**（`display:none` / `KeepAlive` 键控复用，不 `v-if` 卸载）——现在 `ChatPanel` 明确"必须持续挂载"、`ExplorerPane` 用 `v-show`，新容器若重挂会丢会话/流式/页签状态 | [01 §3.11](01-自由布局容器改造设计方案.md#311-设计态与区块保活关键风险) |
| 7 | 系统内重复实现（split / dialog / icon / dropdown）**不保留副本**：复用既有组件，子工程只留树算法；`DialogManager` 必须唯一（否则双蒙板/双层级） | [01 §3.10](01-自由布局容器改造设计方案.md#310-样式主题与组件去重) |
| 8 | 预览区页签**一期不动**（`CodeView` 自管），只把"当前激活文件"镜像进 `selection.activeFile`；栏（工具栏/状态栏）**一期即条目化**，命令**收敛**为 `open-view{target}` | [01 §4.1](01-自由布局容器改造设计方案.md#41-已拍板口径2026-10-16用户) |

## 4. 已拍板口径速查

| # | 事项 | 结论 |
|---|------|------|
| ① | 落地形态 | **直接并入** `src/frontend/src/layout-free/`（不保留独立工程/dev 服务器） |
| ② | 状态库 | **引入 Pinia** + **追加跨 view 共享状态 store**（`stores/selection.js`，整理当前文件/会话/任务/模式/workDir） |
| ③ | 预览区页签 | **一期不动**（`CodeView` 自管；仅镜像 `activeFile`） |
| ④ | 主题命名 | **统一 `lf-` 前缀**（80 个主题；落 `event-names.js` 前须用户确认清单） |
| ⑤ | 栏 / 命令 | **栏一期即条目化**（工具栏·状态栏拆为按钮/菜单/视图条目）+ **命令收敛** `open-view{target}` |

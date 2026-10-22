# ChonkPilot

Windows 桌面 AI 助手 / IDE 工具。**Go + Vue3**（自研 go-webview2 宿主，非 Wails），以 **MQ 消息面**（`/publish` + `chonk.*` 主题）为唯一前后端通道（DevTools 除外），**分层单体**架构。

```
chonkpilot.exe（WebView2 宿主 + 前端 dist 内嵌；壳 = src/desktop，实现 = src/lib/gui）
   └─ server 编排（src/lib/llm/server）→ persist 数据面（src/lib/data）→ filesys（src/lib/filesys）
      ＋ MCP 能力面（src/lib/mcp-server 内嵌 / src/lib/gateway 聚合）
      ＋ executor 执行层（src/lib/mcp-tools，spawn-on-call）＋ 插件（src/plugins/plugin-*）
```

> **测试唯一准则**：消息与 payload 以 [61-消息一览](docs/spec/60-reference/61-消息一览.md) 为基准，**禁止增 / 改 / 删**（见 [.trae/rules/测试准则.md](.trae/rules/测试准则.md)）。
> **权威规格 = [docs/spec/](docs/spec/README.md)**；与 `.trae/rules` 冲突时以 spec 为准并回改规则。

## 运行形态

> **2026-10-22（PRODSIM-1 产物简化）**：**唯一产物 = `chonkpilot.exe`**（WebView2 宿主 + 常驻 server）。原「CLI 单体 / gui / browser」三形态与其 exe、`build-gui.ps1` / `build-browser.ps1` **已删除**；`src/gui`、`src/flash`、`src/desktop/cli`、`src/lib/cli` 源目录一并移除。

| 形态 | 产物 | 说明 |
|---|---|---|
| **desktop（唯一）** | `dist/desktop/chonkpilot.exe` | WebView2 宿主，内嵌 server→persist→filesys、gateway(内嵌 mcp-server) 与插件；前端 dist 经 go:embed。**双击 = 启动常驻 Server**（日志窗口隐藏）→ 打开最近项目主窗口；托盘可显示日志 / 打开项目 / 退出 |
| ~~CLI 单体~~ | ~~`chonkpilot-cli.exe`~~ | 已删除（2026-10-21） |
| ~~gui（GUI 客户端 + 独立 server）~~ | ~~`chonkpilot-gui-client.exe` / `chonkpilot-server.exe`~~ | 已删除（2026-10-22） |
| ~~flash 薄壳 / browser~~ | ~~`chonkpilot-flash.exe` / `dist/server/www/`~~ | 已删除（2026-10-22；H5 APP 将来以外部 WebView 壳接入，见 [26](docs/spec/10-architecture/26-h5-app.md)） |
| **协议外壳** | `dist/other/chonkpilot-mcp-server.exe` · `chonkpilot-mcp-gateway.exe` | 独立 MCP 契约服务 / 网关聚合，两 exe 同目录共用 `capability/` |

运行时形态标识：`window.__ck.form` = **`desktop`**（唯一取值；`gui` / `browser` 已删除，见 [61 §0 订正块](docs/spec/60-reference/61-消息一览.md)）。

**多窗口（2026-10-22 起三层）**：**日志窗口**（进程唯一、创建即隐藏、关闭 = hide 到托盘、不注册 instance）+ **项目主窗口**（每 `work_dir` 一个，进程内映射表保证同项目唯一；含移动端窗口类型）+ **从属小窗口**（单聊 / 子聊 / 群聊，`?session-id=…#chat`，原生标题栏）。关主窗口 = 终止该项目全部小窗口并销毁；**退出只走托盘菜单**（[24](docs/spec/10-architecture/24-多窗口模型设计方案.md)）。

## 场景 / Agent / 能力分层

（详见 [25-MCP与场景分层模型](docs/spec/10-architecture/25-MCP与场景分层模型.md)）

| 层 | 内容 | 维护处 |
|---|---|---|
| **能力面** | `tool` · `skill` · `resource` · `prompt` | 知识库（`capability/`），prompt 亦可由用户在 chat 输入框选择注入 |
| **场景面** | `agent`（主 + 子）；子 agent 组成"团队成员" | **仅由场景维护**，运行时**注入上下文**（不进 `mcp_find`） |
| **系统提示词** | 三层拼接：全局（代码写死）/ 场景（`description` + 团队成员自动拼接）/ agent | [25 §3](docs/spec/10-architecture/25-MCP与场景分层模型.md) |

- `mcp_find` 的 `type` = **`tool | skill | resource | all`**（`agent` / `prompt` 已移出 LLM 检索面）。
- **无场景时 = 「通用模式」**：只注入全局层提示词 + 工具面 = 全量 hot（含 core/browser/desktop）+ gateway meta 工具。
- 子系统默认 LLM：usr 键 `llm.<子系统>`（`compress` / `memory` / `promptOptimise` / `analysis` / `decision`），空值回落 `defaultLLM`；**按轮次热生效**。

## 数据落点

| 层 | 内容 | 路径 |
|---|---|---|
| **usr（用户级）** | LLM / MCP 定义、偏好、凭据、主题 / 语言 | `~/.chonkpilot/chonkpilot.db` |
| **prj（项目级）** | 项目设置 + **project-id** | `<workDir>/.chonkpilot/chonkpilot.db` |
| **prjusr（个人运行态）** | 会话 / 轮次 / 消息、任务树、快照、窗口与布局 | **desktop 缺省** → `~/.chonkpilot/data/<prj-id>/chonkpilot.db`<br>显式 `--data-dir` → 与 prj 同根（兼容 CLI / 脚本 / L2 用例） |
| **prjusr 非库文件** | `logs/`（GUI 滚动日志）、`tmp/uploads/`（附件 / 截图）、`backup/` | 同 prjusr 根（`data.PrjUsrDir`，**随 data_dir 同源**） |
| **app 级资源** | 出厂场景（「开发场景」`default/`，**可编辑**：出厂内容由 embed 提供，app 初始化时缺失即恢复）、知识库契约（只读） | `<exeDir>/scenarios/` · `<exeDir>/capability/` |

> ⚠️ `models.DataDir(workDir)` 是 **data-dir 访问器（= prj 数据根）**，**不是** prjusr 根；prjusr 根一律经 `data.PrjUsrDir` 解析（缺省需先读 prj 库取 `project-id`）。
> ⚠️ **CLI 单体**：`--data-dir` 指向**临时目录**（退出即弃）→ 其会话**不落**用户数据根，GUI 不显示。

## 目录布局

> src / dist 结构重整已落地（[41 D-28](docs/spec/40-roadmap/41-未决项登记.md)）：壳 → `src/{desktop,gui,server,others}`，lib → `src/lib/*`，插件与引擎 → `src/plugins/*`。

| 类别 | 位置 | 说明 |
|---|---|---|
| 外壳 · 宿主 | `src/desktop/` | **桌面单体**壳（`main.go` = 嵌前端 dist + `gui.FormDesktop`）；`cli/` = CLI 单体 |
| | `src/gui/` | **GUI 客户端**壳（`-tags split`）；`frontend/dist/` = 客户端 embed 落点 |
| | `src/server/` | 独立 server 壳（gui 与 browser **共用**同一份 exe） |
| | `src/others/` | 协议外壳：`mcp-server/` + `mcp-gateway/` |
| 库 | `src/lib/gui/` | 宿主实现（**唯一一份** `Main(distFS, Options{Form})`）+ `bridge/` + `models/`（两壳共用） |
| | `src/lib/core/` | 公共底座：`mq` / `dsl` / `paths` / `winlog` / `winsvc` / `exedir` / `agentbox` / `heartbeat` |
| | `src/lib/llm/` | 会话 / LLM server 编排（`server/` + `httpapi/`） |
| | `src/lib/router/` | LLM 路由（provider 选择 / 重试 / 用量） |
| | `src/lib/data/` | persist 数据面（`data-*` 应答）+ bbolt 存储内核 + `facade/` |
| | `src/lib/filesys/` | 文件树 / 变更跟踪 / 内容提供 |
| | `src/lib/mcp-server/` · `src/lib/gateway/` | MCP 契约服务（四原语）/ 网关聚合（servers / tasks / 熔断） |
| | `src/lib/mcp-tools/` | executor 执行层（`core` / `desktop` / `browser`）+ `*.tool.md` 契约 |
| | `src/lib/task/` · `src/lib/assembly/` | 任务层 / 装配层 |
| | `src/lib/go-webview2/` | 自研 WebView2 宿主 fork（vendored） |
| 前端 | `src/frontend/` | 前端源码工程（Vue3，仓库级）：`index.html` = browser 入口（→ `dist/server/www/`）· `embed.html` = GUI/embed 入口（构建后改名 `index.html` 投放 `src/{gui,desktop,server}/frontend/dist`） |
| 插件 / 引擎 | `src/plugins/` | `plugin`（接口）+ `plugin-{compress,history,memory,vfts,codegraph}` + `codegraph` / `vfts` 引擎本体 |
| 测试代码 | `src/test/` | 每模块 `unittest/`（Go 回归）+ `systest/`（Python 端到端，`--test-port` 驱动）；映射见 [51-FP与测试映射](docs/spec/50-testing/51-FP与测试映射.md) |
| 文档 | `docs/spec/` | **正式规格唯一入口** = [docs/spec/README.md](docs/spec/README.md)（spec 自洽，不引用本目录以外文档） |
| 配置 / 规则 | `.trae/rules/` · `.trae/skills/` | 项目规则与变更交付技能 |
| 验证区（不入库） | `TECH-POC/` | 技术可行性验证（如 `webview2-multiwin`），非产品代码 |
| 历史归档（不入库） | `trash/` | 废弃代码 / 文档 / 探针 / 第三方参考项目 |
| 工具脚本 | `sync-contracts.ps1` | 工具契约同步（`*.tool.md` ↔ handler） |

## 构建

**必须**通过构建脚本（**禁止**单独 `go build` / `wails build` / `npm run build`）：

```powershell
.\build-desktop.ps1        # 唯一构建脚本：chonkpilot.exe + 引擎 → dist/desktop\（含 capability/、出厂场景 scenarios/）
.\build-mcp-server.ps1     # mcp-server 部署 → dist/other\
.\build-mcp-gateway.ps1    # mcp-gateway 部署 → dist/other\（与 mcp-server 同目录）
.\build-codegraph.ps1      # 引擎 → dist/plugins/codegraph\
.\build-vfts.ps1           # 引擎 → dist/plugins/vfts\
```

`build-desktop.ps1` 内部流程：kill 旧实例 → 在 `src/frontend` 内 `npm run build`（单入口 `index.html`，产物**直投** `src/desktop/frontend/dist`）→ `go build` 宿主 → 调 `build-mcp-server.ps1` / `build-codegraph.ps1` / `build-vfts.ps1`。

**产物清单**（`dist/desktop/`）：`chonkpilot.exe`（唯一宿主/客户端产物）· 引擎 exe（codegraph / vfts）· `zvec_c_api.dll` · `capability/`（知识库契约 + executor + 出厂场景 `scenarios/`）。（出厂场景「开发场景」`default/` 由 **embed** 内嵌，运行时 app 初始化物化到 `<exeDir>/scenarios/`，app 级**可编辑**。）

> 内置 MCP（codegraph / vfts）**默认不开启、不接入**——设置页开关开启后才拉起 / 注册。

## 运行

```powershell
cd dist/desktop ; .\chonkpilot.exe                                  # 桌面单体（主力）
cd dist/desktop ; .\chonkpilot-cli.exe --work-dir <dir>             # CLI 单体（会话落临时目录，退出即弃）
cd dist/other   ; .\chonkpilot-mcp-server.exe --http                # 默认 127.0.0.1:5700/mcp
cd dist/other   ; .\chonkpilot-mcp-gateway.exe -transport http://127.0.0.1:5556
```

## 测试

```powershell
# L1 Go 回归：各模块 unittest 目录内 go test ./...
# L2 GUI 端到端：启动 chonkpilot.exe --test-port=<port> --work-dir=<ws>，再跑 src/test/chonkpilot-gui/systest 下 run_*.py
```

分级与用例映射见 [50-测试体系](docs/spec/50-testing/50-测试体系.md) · [51-FP与测试映射](docs/spec/50-testing/51-FP与测试映射.md) · [52-test-port驱动](docs/spec/50-testing/52-test-port驱动.md)。

## 文档索引

- **正式规格唯一入口**：[docs/spec/README.md](docs/spec/README.md)
- 架构总纲：[00-架构总纲](docs/spec/00-overview/00-架构总纲.md) · 端到端数据流：[01](docs/spec/00-overview/01-端到端数据流.md) · 配置层级：[02](docs/spec/00-overview/02-配置层级.md) · 构建与部署：[03](docs/spec/00-overview/03-构建与部署.md) · 交付标准：[04](docs/spec/00-overview/04-交付标准.md)
- 关键架构：[11-MQ与消息](docs/spec/10-architecture/11-MQ与消息.md) · [12-数据层](docs/spec/10-architecture/12-数据层.md) · [20-实例隔离与后端分离](docs/spec/10-architecture/20-实例隔离与后端分离.md) · [24-多窗口模型](docs/spec/10-architecture/24-多窗口模型设计方案.md) · [25-MCP与场景分层模型](docs/spec/10-architecture/25-MCP与场景分层模型.md)
- 消息契约（测试唯一准则）：[61-消息一览](docs/spec/60-reference/61-消息一览.md) · 配置项：[64](docs/spec/60-reference/64-配置项一览.md) · 名词：[60](docs/spec/60-reference/60-名词约定.md)
- 模块设计：[20-modules/](docs/spec/20-modules/) · 功能点：[30-function-points/](docs/spec/30-function-points/)
- 路线图 / 决策：[40-演进计划](docs/spec/40-roadmap/40-演进计划.md) · [41-未决项登记](docs/spec/40-roadmap/41-未决项登记.md) · [42-决策记录](docs/spec/40-roadmap/42-决策记录.md)

## 项目规则

- [.trae/rules/项目规则.md](.trae/rules/项目规则.md) — 构建 / 前端 / 后端 / 日志约定（IDE 强制注入的速查；冲突时以 `docs/spec/` 为准）
- [.trae/rules/开发者宪章.md](.trae/rules/开发者宪章.md) — 编码-自查-上报闭环
- [.trae/rules/测试准则.md](.trae/rules/测试准则.md) — 消息面唯一准则与测试约束

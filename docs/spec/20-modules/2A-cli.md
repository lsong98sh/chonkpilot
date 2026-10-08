# 2A · src/desktop/cli（CLI 单体）

> 状态：✅ 与代码一致
> 关联：[21-llm-server](21-llm-server.md) · [10-分层与依赖](../10-architecture/10-分层与依赖.md) · [02-配置层级](../00-overview/02-配置层级.md) §6.3
> 代码目录：`src/desktop/cli/`（`main.go`，只留 flag 解析 + 装配）；启动前置编排（数据根三态 / 占用校验 / 配置复制）在 `src/lib/cli/`（I-158 · D-45）；gui 形态下以 `-tags split` 产出 `chonkpilot-cli-client.exe`

---

## 1. 职责与边界

- **一句话**：console 单体宿主，批处理/脚本模式——内嵌 filesys + persist + server + gateway + mcp-server + 插件，工具调用**强制同步**，无 WebView2。
- **做**：一次性提示词执行、stdout 结果输出（`sse`/`final`/`verbose`）、内嵌全栈能力。
- **不做**：无窗口/前端、无 `--test-port`、无 bridge、无 `gui.*`/`window` 面。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | console exe |
| 构建 | 由 `build-desktop.ps1`（原名 `build-standalone-gui.ps1`）第 3 步产出 → `dist/desktop/chonkpilot-cli.exe`（**无独立 cli 构建脚本**） |
| 内嵌 | `filesys.New` + `server.New`（含 persist/gateway/mcp-server + compress/memory/history/codegraph/vfts） |

**参数**：`--prompt` · `--prompt-file` · `--scenario <string>`（场景目录名，空 = 默认场景） · `--work-dir` · `--data-dir` · `--output sse|final|verbose`（默认 `sse`）· `--llm` · `--think` · `--effort`。
提示词来源：二者皆空 → 读 stdin（字符设备则报错退出）。

---

## 3. 对外接口

- **入**：命令行 + stdin。
- **出**：stdout（`fmt.Print*`）；错误/stderr 用 `fmt.Fprintln(os.Stderr, ...)`，启动失败 `log.Fatalf`。
- **总线**：订阅 `session-receive` / `session-complete` / `task-done`；发布 `instance-register`（`client_type:"cli"`）+ `session-start` + `session-send{type:"text-user"}`。

输出模式：`verbose` 全报文 dump（`[RECV]/[COMPLETE]/[TASK]/[START]/[SEND]`）· `sse` 增量文本 + `[工具]`/`[工具完成]` · `final` 仅终态文本。

---

## 4. 内部控制流

```text
参数/提示词解析 → output 校验 → workDir(Abs)
  → cli.PrepareDataDir（三态数据根 + 占用校验 + prjusr 试开，D-45；失败报错退出 1）
  → instanceID → mq.New(Prefix:"chonk.")
  → filesys.New(bus).Start()
  → server.New(bus, Options{LLMBase:127.0.0.1:8901/v1, LLMModel, AsyncMode:"never", Plugins:compress/memory/history/codegraph/vfts})
  → signal.NotifyContext → srv.Start(ctx)
  → 发布 instance-register
  → session/turn = 随机 id → 订阅 receive/complete/task-done
  → 发布 session-start → session-send{text-user, prompt}
  → 等 doneCh 或 ctx → final 模式打印 finalText → 退出（defer：srv.Stop → filesys.Stop → bus.Close）
```

`AsyncMode: "never"` 链路：`src/desktop/cli/main.go` → `server.Options.AsyncMode` → gateway `Params.AsyncMode` → `doCall` 全局覆盖（强制同步，不转后台）。

---

## 5. 数据结构与存储

- 无本地存储；数据经内嵌 persist（`data-*` 消息面）。
- session/turn id 由 `newUUID`（临时文件名去 `.uuid`）生成（非标准 UUID）。

---

## 6. 依赖

- **直接**：`src/lib` · `src/lib/cli`（数据根编排，I-158）· `src/lib/data` · `src/lib/filesys` · `src/lib/llm` · `src/plugins/plugin` + `-compress`/`-memory`/`-history`/`-codegraph`/`-vfts`。
- **indirect**：`src/lib/gateway` · `src/lib/mcp-server`。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| `AsyncMode: never` | 工具调用强制同步 | CLI 无后台任务消费方，转后台会丢结果 |
| 与 GUI 同 lib 集 | 复用 filesys/server/persist/插件 | 行为一致、避免分叉 |
| 自建 session/turn | CLI 自行分配并驱动 | 无 UI，无需持久会话概念 |

---

## 8. 场景与边界

- **无 `--test-port`**：该参数仅存在于 GUI 宿主。
- **stdin 提示词**：非字符设备（管道为空）→ 报错退出。
- **退出**：正常路径自然返回（无 `os.Exit`）；异常 `os.Exit(1)` 或 `log.Fatalf`；等待期 SIGINT/SIGTERM 由 `ctx.Done()` 结束。

---

## 9. 现状与待办（含目标态差距）

- ✅ **数据根三态语义（方案 B，D-45 · 2026-10-01 反转）**：CLI `--data-dir` 三态 —— **不传** = 临时隔离（**默认**：tmp 空根 + 复制三级配置 `usr`/`prj`/`prjusr` 覆盖 `prj` 与 `llms` 表，不含会话数据；会话数据写临时目录、退出即弃——批处理不污染真实配置、不与 GUI 冲突）；**显式留空** `--data-dir=` = 真实根（与 GUI 一致：prj=`<workDir>/.chonkpilot`、prjusr=`~/.chonkpilot/data/<id>`；**参与 work-dir 占用校验** + prjusr 试开冲突报错，I-74）；**`--data-dir=<路径>`** = 该路径作 prjusr 数据根（`data.SetDataHome` 重定位）、prj 仍按 `<workDir>/.chonkpilot`（无则 tmp 空根，不污染 cwd）。「传空」与「未传」经 **`flag.Visit`** 判定。落点 `src/lib/cli`（`PrepareDataDir`/`ResolveDataMode`），`main.go` 只留 flag + 装配。见 [02-配置层级 §6.3](../00-overview/02-配置层级.md)。
- ✅ **L1 单测**：`src/desktop/cli/main_test.go`（`parseFlags` 参数解析：`--data-dir` 三态 `dataDirSet` / 输出模式校验 / 各参数绑定 / 未知参数报错；`resolveDir` / `newUUID`）。
- ✅ **L2 单测**：`src/test/chonkpilot-cli/unittest`（I-158 落地：`ResolveDataMode` 三态 / `PrepareDataDir` 临时隔离复制与清理 / 真实根锁生命周期 / busy 报错 / prjusr 占用报错 / 自定数据根两分支 / `CopyConfigTables` 源缺失跳过 + 源损坏报错）。

---

## 10. 关联测试

- **L1**：`src/desktop/cli/main_test.go`（参数解析 / `--data-dir` 三态位 / 输出模式 / `resolveDir` / `newUUID`）。
  运行：`cd src/desktop ; go test ./cli/... -count=1`。
- **L2**：`src/test/chonkpilot-cli/unittest`（数据根编排黑盒：`ResolveDataMode` 三态 / `PrepareDataDir` 临时隔离与清理 / 真实根锁与 busy / prjusr 冲突 / 自定数据根 / `CopyConfigTables` 边界）。
  运行：`cd src/test/chonkpilot-cli/unittest ; go test ./... -count=1`。
- 最接近的现成覆盖：`src/test/chonkpilot-llm/unittest/llm_test.go`（同一 server 包，经总线黑盒驱动）；`src/test/chonkpilot-gui/systest/`（走 GUI 前端事件链，**非** CLI stdout 路径）。

> 合规说明：L1 测试均为**纯函数 + 临时文件系统**（参数解析 / 数据根准备 / 配置复制），**不发送/确认任何 MQ 消息**，天然满足 [61-消息一览](../60-reference/61-消息一览.md) 的测试准则。

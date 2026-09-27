# 24 · src/lib/core（公共库）

> 日期：2026-09-10 ｜ 状态：✅ 与代码一致
> 关联：[11-MQ与消息](../10-architecture/11-MQ与消息.md)
> 代码目录（D-28：`src/lib/` 底座目录名 → `src/lib/core/`，module 名 `chonkpilot-lib` 不变）：`src/lib/core/`（`mq/` `dsl/` `paths/` `winlog/` `winsvc/` `exedir/` `winproc/`）

---

## 1. 职责与边界

- **一句话**：**零本仓库依赖**的公共底座——消息总线、DSL 引擎、路径解析、Windows 日志/服务、exe 目录。
- **做**：为所有模块提供通用能力。
- **不做**：不依赖任何其他 `chonkpilot-*` module（所有模块的依赖终点）。
- **同层「非 core」lib module（不在本篇包清单内）**：`src/lib/router`（`chonkpilot-router`，**纯 lib**：**无 facade / 无 MQ / 无 DB / 无 exe**，与 `dsl` 同类；canonical 落 `internal/canon` + 根包以别名再导出，协议适配落 `internal/adaptor/{openai,anthropic,echo}/`；定位见 [40 §LR §1](../40-roadmap/40-演进计划.md)）· `src/lib/ignore`（`chonkpilot-ignore`，**纯 lib**：仅标准库，**gitignore 语义排除匹配单一实现**——codegraph/vfts 两引擎遍历与 vfts 插件清单扫描共用，2026-09-27 新增，见 [29 §4.1](29-codegraph.md)）· `src/lib/assembly`（入口装配器，RB-5 L5）· `src/lib/task` · `src/lib/data` · `src/lib/filesys` · `src/lib/gateway` · `src/lib/llm` · `src/lib/mcp-server` · `src/lib/mcp-tools` 等 —— 见 [10 §3 模块依赖矩阵](../10-architecture/10-分层与依赖.md) / [23 §2](../10-architecture/23-工程与部署拓扑.md)。

---

## 2. 包清单

| 包 | 职责 | 关键导出 |
|----|------|---------|
| `mq` | 进程内消息总线（事件 + 请求-响应） | `Bus`（`Emit/On/Publish/Subscribe/Close/Prefix`）· `New(Options{Prefix})` · `Future`/`Value` · 通配 `*`/`>` |
| `dsl` | DSL 核心（语法固定，动词可注入） | `Parse` · `Run` · `SplitArgs` · `NewEngine` · `Action{Name,Raw,Run}` · `Options{Files,DBs,Actions,StopOnError,Vars}`（`Vars` = 宿主注入只读变量，如 `env`） · `Scope` · `RunResult` · `CollectHandleRefs`（只读句柄引用清单，供 R-11 预校验） |
| `paths` | 路径解析唯一入口 | `ExpandHome` · `ResolvePath(raw, base)` · `ResolveDir(raw, base)` · `SetTempRoot` / `TempRoot` · `InvalidPathMessage` |
| `winlog` | 统一日志落点（console / Windows 事件日志） | `NewWriter(serviceName, useEventLog, out)`（实现 `io.Writer`）· `Error/Warn/Info/Close` |
| `winsvc` | Windows 服务封装 | `Service{Name,RunArgs,Start,Stop}` · `IsWindowsService` · `Run` · `Install` · `Remove` |
| `winproc` | **子进程创建标志**（2026-09-12 新增，[42 §2 (43)](../40-roadmap/42-决策记录.md)） | `SysProcAttr() *syscall.SysProcAttr` —— Windows 返回 `{HideWindow:true, CreationFlags:0x08000000}`（**`CREATE_NO_WINDOW`，spawn 外部 exe 不弹控制台窗口**）；非 Windows 返回 `nil`。**所有 spawn 外部 exe 的落点都应用它**（executor / 引擎 exe / git / 解释器 / npm / `sc` / 外部 stdio MCP server）；有意例外见 42 §2 (43) |
| `exedir` | 取当前可执行文件目录 | `Dir()` |
| `heartbeat` | **实例心跳契约数值的单一来源**（2026-09-16 新增，[42 §2 (74)](../40-roadmap/42-决策记录.md)） | `Interval = 30s`（客户端心跳发布周期）· `Timeout = 90s`（= 3× 周期，服务端/数据层超时阈值） |

---

## 3. 对外接口要点

### 3.1 mq（详见 [11-MQ与消息](../10-architecture/11-MQ与消息.md)）

`Emit(ctx, subject, payload) *Future`（不 Await = 事件；Await = 请求-响应）；`On(subject, order, handler)`（order 升序，同 order 按注册序）；错误**恒收集不 reject**（`v.Errors` / `v.Err()`）；前缀 `chonk.` 由 `Options.Prefix` 注入一次，handler 收**去前缀**的相对主题。

### 3.2 dsl

- 保留字（语法核心）：`SET / IF / LOOP / PARALLEL / BREAK / CONTINUE / EXIT / END`。
- **动词可注入**：`Options.Actions` 注册；`Parse` 校验未注册动词；`Raw` 动作参数**原文透传**（`captureRawRest`）。
- 实际使用方（4 处）：`filesys_run`（7 动词，StopOnError=false）· `desktop_run`（23）· `browser_run`（23）· `llm_run`（`LLM` 动词）。
- **`MaxDepth = 8`**（LOOP/IF/PARALLEL 计入；语法期强制，不暴露为配置）。
- **`Options.MaxLoopIterations`** = **无参 LOOP**（无数据源、无界循环）的迭代上限（缺省 50；`<=0` 取缺省）；到达上限记错并终止本层循环（见 63 §5.2）。
- 错误类型：`ErrBreak/ErrContinue/ErrExit`（流控信号）· `LineError{Line,Msg}`（语法/语义）· `RunError{Line,Msg}`（运行时单步，记错不阻塞）。
- I/O 抽象（`handle.go`）：`FileSystem`/`FileHandle`/`DBSystem`/`DBHandle`/`TableHandle`（测试用内存 Mock）。

### 3.3 paths

`ExpandHome`：`~` / `~/x` / `~\x` → home；`~foo` 不展开。

`ResolvePath(raw, base) (abs, errMsg)`（R-11 二次升级，工具/DSL 参数解析**唯一入口**）：① `raw==""` → `("","")` → ② `!/` / `!\` 前缀 → `Join(TempRoot(), rest)`（临时目录，按 instance 分目录；**缺 instance → 返回 `ErrNoInstance` 消息**）→ ③ `~` 展开 → ④ 绝对原样 `Clean` → ⑤ 否则：`base!=""` → `Clean(Join(base, raw))`（**仅供 CLI 参数解析**）；`base==""` → **返回统一错误消息**（相对路径拒绝）。

`ResolveDir(raw, base) string`（CLI 目录参数：`--work-dir`/`--data-dir`，**语义不变**）：① `~` 展开 → ② 绝对原样 `Clean` → ③ 否则 `Clean(Join(base, raw))`（**允许 `../` 跳出 base**，属有意行为）；`raw==""` → 空。

`SetTempRoot(instanceID) (string, error)` / `TempRoot() (string, error)`：临时目录根（`<系统 temp>/chonkpilot/<sanitize(instanceID)>`）；线程安全、幂等、按需建目录。**instance 为空/未设置 → `("", ErrNoInstance)`**（不再回落 `default`；instance 为空即异常）。`ErrNoInstance` = 「缺少 instance（宿主未注入调用上下文），无法解析 !/ 或注入 env」。

`InvalidPathMessage(raw) string`：路径违规统一消息（单一实现，供各工具/llm_run 复用）。

> `ResolveDir` 语义**不变**：相对路径在 CLI 目录参数下仍合法。mcp-tools 与 llm_run 的**所有输入/输出文件与目录参数**（**含 DSL 内所有 `#"..."` 引用与数据源读取**）另受 R-11 约束（须绝对 / `~/` / `!/` 开头，相对路径整体失败），并注入只读 DSL `env`（`{{env.CHONKPILOT_*}}`），见 [16-路径解析规范](../10-architecture/16-路径解析规范.md) §8。

### 3.4 winlog / winsvc / exedir

- `winlog.Writer`：`useEventLog` 时 `eventlog.Open(serviceName)`，失败静默回退 console；事件日志 >30000 字节截断。
- `winsvc.Service`：`Install` = `sc create`（auto）+ `eventlog.InstallAsEventCreate`；`Remove` = `sc delete` + `eventlog.Remove`；`handler.Execute` 支持 Stop/Shutdown（5s 超时）。
- `exedir.Dir()`：`os.Executable` → 绝对化 → `Dir`。

---

## 4. 内部控制流

- `mq`：`Emit` → 锁内 `match`（精确表 + 通配表，按 `(order,id)` 稳定排序）→ 锁外逐 handler 执行（panic recover 收集）→ resolve。
- `dsl`：`Parse`（lex → AST，深度检查）→ `NewEngine`（动词表大写化）→ `Execute`（`execStmt` 分派 SET/IF/LOOP/PARALLEL/动作）。

---

## 5. 数据结构与存储

- `mq`：`subs map[string][]handlerEntry` + `wild []wildSub`（`sync.RWMutex`）。
- `dsl`：`Script`/`Stmt` AST + `Scope`（变量/句柄表）。
- 其余无状态。

---

## 6. 依赖

**无本仓库依赖**（所有模块的依赖终点）。第三方：`golang.org/x/sys`（winlog/winsvc）。

`heartbeat` 的消费方（2026-09-16）：发布侧 `src/plugin/instance`（`heartbeat_split.go`，**保留既有导出名** `HeartbeatInterval`/`HeartbeatTimeout` 作常量别名，调用方无需改动）· 服务端扫描侧 `src/llm/server`（`sweep_split.go`）· 数据层清理侧 `src/data/persist`（`sweep_split.go`，**const `staleTimeout = heartbeat.Timeout`**）。**上提 lib 的原因**：data 层**不得依赖 `src/plugin`**（分层约束），原先把 30s/90s 就地复制到 data 层；上提后三处引用同一常量，改值只需一处，依赖方向仍合法。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| mq 仅内存实现 | 2026-09-03 去 NATS | 单体形态足够；跨进程待自研 |
| 前缀延迟注入 | 业务写相对主题 | 主题空间由宿主决定，业务零 `chonk.` 字面 |
| DSL 语法固定 + 动词注入 | 消费方注册 Action | 一套引擎服务 4 类工具 |
| MaxDepth 语法期硬编码 | 不做配置 | 死配置清理（[02-配置层级](../00-overview/02-配置层级.md) §8） |
| ResolveDir 允许 `../` | 不强制钳制 | 用户显式指定属有意行为 |

---

## 8. 场景与边界

- mq：总线 `Close` 后 `Emit` 追加 `errClosed`；handler panic → 收集 `mq: handler panic`。
- dsl：嵌套 >8 → `嵌套深度超过 8`；未注册动词/保留字作动词 → `Parse` 报错。
- paths：`base` 为空时相对路径按字面 `Clean`。

---

## 9. 现状与待办

- ✅ **已全迁（2026-09-15，T-17）**：mq v1 `Publish/Subscribe` 兼容层保留、**暂不废弃/删除** —— 生产与测试代码已全部迁至 v2（`Emit`/`On`），仅 `src/lib/mq/{promise_test,prefix_test}.go` 的 v1 自身测试仍使用。
- 🔵 去中心化 MQ（自研）远期；`dsl` 已落地（语法规范见 [63-DSL语法](../60-reference/63-DSL语法.md) §13）。
- `winlog`/`winsvc` 使用方：`src/mcp-server`（service）、`src/gateway`（service）。

---

## 10. 关联测试

`src/test/chonkpilot-lib/unittest/`（`mq` 前缀/promise、`paths`、`exedir`）· 包内 `dsl/dsl_test.go`（lex/未注册动词/SET/IF/断点续跑/BREAK-CONTINUE-EXIT/PARALLEL/嵌套/PTC 重定向等）。

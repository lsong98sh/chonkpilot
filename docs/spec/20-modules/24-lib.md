# 24 · 公共库（chonkpilot-framework/host/src/core）

> 状态：⚠️ 2026-11-04 核对：包功能存在且可用，但代码目录 / module 名 / 依赖结论 / 测试落点已迁仓，本文已订正
> 关联：[11-MQ与消息](../10-architecture/11-MQ与消息.md)
> 代码目录（2026-11-04 核对）：**`chonkpilot-framework/host/src/core/`**（module 名 **`github.com/lsong98sh/chonkpilot-framework/host`**，不是 `chonkpilot-lib`）→ `mq/` `paths/` `winlog/` `winsvc/` `exedir/` `winproc/` `heartbeat/` `lockfile/`；同 module 另有 `msgkeys/` `contractmd/` `idgen/` `indexkind/` `osuser/` `pluginclient/` `sandbox/` `transport/` `tray/` `uds/` `uploadfs/`。`src/lib/core` **已不存在**；进程单例在 `chonkpilot-framework/host/src/singleinstance`（UDS 独占 listen）。

---

## 1. 职责与边界

- **一句话**：宿主侧公共底座——消息总线（framework `runtime/bus` 的类型别名）、路径解析、Windows 日志/服务、exe 目录、心跳常量、文件锁。
- **做**：为所有模块提供通用能力。
- **不做**：不反向依赖主仓业务 module（`src/lib/data`、`llm`、`gui` 等）；但**并非零本仓库依赖**（2026-11-04 核对）：`core/mq` → `chonkpilot-plugin-framework/runtime/bus`，`core/paths` → `chonkpilot-util/pathx`，`core/winlog` → `chonkpilot-util/strx`。
- **同层「非 core」lib module（不在本篇包清单内）**：`chonkpilot-agent/router`（**纯 lib**：**无 facade / 无 MQ / 无 DB / 无 exe**；canonical 落 `internal/canon` + 根包以别名再导出，协议适配落 `internal/adaptor/{openai,anthropic,echo}/`）· `chonkpilot-agent/chat`（纯 lib 端口化内核）· `chonkpilot-agent/session`（纯 lib，零 IO）· `chonkpilot-agent/gateway`（独立仓内核）· `chonkpilot-framework/plugin`（SDK + host runtime）· `chonkpilot-util`（**无状态 utility**：`ignore` 纯内核、`pathx`、`strx`、`textx` 等 **26 个子包**，仅标准库；见 [75-utility](../70-conventions/75-utility.md)）· `src/lib/assembly`（入口装配器）· `src/lib/task` · `src/lib/data` · `src/lib/filesys` · `src/lib/llm` · `src/lib/mcp-server` · `src/lib/mcp-tools` 等 —— 见 [10 §3 模块依赖矩阵](../10-architecture/10-分层与依赖.md) / [23 §2](../10-architecture/23-工程与部署拓扑.md)。

---

## 2. 包清单

| 包 | 职责 | 关键导出 |
|----|------|---------|
| `mq` | 进程内消息总线（事件 + 请求-响应）；**framework `runtime/bus` 的类型别名**（本包不含实现） | `Bus`（`Emit/On/Publish/Subscribe/Close/Prefix`）· `New(Options{Prefix})` · `NewFuture` · `Future`/`Value` · 通配 `*`/`>` |
| `paths` | 路径解析唯一入口 | `ExpandHome` · `ResolvePath(raw, base)` · `ResolveDir(raw, base)` · `SetTempRoot` / `TempRoot` / `TempRootFor` · `ResolvePathFor` · `ToLogical` / `FromLogical` · `InvalidPathMessage` |
| `winlog` | 统一日志落点（console / Windows 事件日志） | `NewWriter(serviceName, useEventLog, out)`（实现 `io.Writer`）· `Error/Warn/Info/Close` |
| `winsvc` | Windows 服务封装 | `Service{Name,RunArgs,Start,Stop}` · `IsWindowsService` · `Run` · `Install` · `Remove` |
| `winproc` | **子进程创建标志 / 进程树治理** | `SysProcAttr() *syscall.SysProcAttr` —— Windows 返回 `{HideWindow:true, CreationFlags:0x08000000}`（**`CREATE_NO_WINDOW`，spawn 外部 exe 不弹控制台窗口**）；非 Windows 返回 `nil`。**所有 spawn 外部 exe 的落点都应用它**（executor / 引擎 exe / git / 解释器 / npm / `sc` / 外部 stdio MCP server）。另有 `KillTree(pid)` / `EnsureKillOnCloseJob()`（2026-11-04 核对补列）。 |
| `exedir` | 取当前可执行文件目录 | `Dir()` |
| `heartbeat` | **实例心跳契约数值的单一来源** | `Interval = 30s`（客户端心跳发布周期）· `Timeout = 90s`（= 3× 周期，服务端/数据层超时阈值） |
| `lockfile` | 跨进程文件锁原语（`LockFileEx`） | `AcquireWorkDir` / `AcquirePrjUsr` · `WorkDirFree` / `PrjUsrFree` · `WorkDirLockPath` / `PrjUsrLockPath` · `ErrBusy` / `ErrUnsupported`。**注**：2026-10-29 起桌面单例改由 `singleinstance`（UDS），`Acquire*` 已无生产调用点（仅测试与常量使用）〔2026-11-04 核对〕 |

---

## 3. 对外接口要点

### 3.1 mq（详见 [11-MQ与消息](../10-architecture/11-MQ与消息.md)）

`Emit(ctx, subject, payload) *Future`（不 Await = 事件；Await = 请求-响应）；`On(subject, order, handler)`（order 升序，同 order 按注册序）；错误**恒收集不 reject**（`v.Errors` / `v.Err()`）；前缀 `chonk.` 由 `Options.Prefix` 注入一次，handler 收**去前缀**的相对主题。

### 3.2 dsl（已删除）

> **🗄 已删除（2026-10-21，GATEWAY-2 L1）**：`src/lib/core/dsl` 整包移除，DSL 语法规范 [63-DSL语法](../60-reference/63-DSL语法.md) 仅作历史存档。路径参数约束仍由 `paths.ResolvePath` + 各工具自行校验（见 §3.3）。

### 3.3 paths

`ExpandHome`：`~` / `~/x` / `~\x` → home；`~foo` 不展开。

`ResolvePath(raw, base) (abs, errMsg)`（R-11 二次升级，工具/DSL 参数解析**唯一入口**）：① `raw==""` → `("","")` → ② `!/` / `!\` 前缀 → `Join(TempRoot(), rest)`（临时目录，按 instance 分目录；**缺 instance → 返回 `ErrNoInstance` 消息**）→ ③ `~` 展开 → ④ 绝对原样 `Clean` → ⑤ 否则：`base!=""` → `Clean(Join(base, raw))`（**仅供宿主参数解析**）；`base==""` → **返回统一错误消息**（相对路径拒绝）。

`ResolveDir(raw, base) string`（宿主目录参数：`--work-dir`，**语义不变**）：① `~` 展开 → ② 绝对原样 `Clean` → ③ 否则 `Clean(Join(base, raw))`（**允许 `../` 跳出 base**，属有意行为）；`raw==""` → 空。

`SetTempRoot(instanceID) (string, error)` / `TempRoot() (string, error)`：临时目录根（`<系统 temp>/chonkpilot/<sanitize(instanceID)>`）；线程安全、幂等、按需建目录。**instance 为空/未设置 → `("", ErrNoInstance)`**（不再回落 `default`；instance 为空即异常）。`ErrNoInstance` = 「缺少 instance（宿主未注入调用上下文），无法解析 !/ 或注入 env」。

`InvalidPathMessage(raw) string`：路径违规统一消息（单一实现，供各工具复用）。

> `ResolveDir` 语义**不变**：相对路径在宿主目录参数下仍合法。工具（`file_*` / `task_run` / `web_download`）的**所有输入/输出文件与目录参数**另受 R-11 约束（须绝对 / `~/` / `!/` 开头，相对路径整体失败），见 [16-路径解析规范](../10-architecture/16-路径解析规范.md) §8。

### 3.4 winlog / winsvc / exedir

- `winlog.Writer`：`useEventLog` 时 `eventlog.Open(serviceName)`，失败静默回退 console；事件日志 >30000 字节截断。
- `winsvc.Service`：`Install` = `sc create`（auto）+ `eventlog.InstallAsEventCreate`；`Remove` = `sc delete` + `eventlog.Remove`；`handler.Execute` 支持 Stop/Shutdown（5s 超时）。
- `exedir.Dir()`：`os.Executable` → 绝对化 → `Dir`。

---

## 4. 内部控制流

- `mq`：`Emit` → 锁内 `match`（精确表 + 通配表，按 `(order,id)` 稳定排序）→ 锁外逐 handler 执行（panic recover 收集）→ resolve。

---

## 5. 数据结构与存储

- `mq`：`subs map[string][]handlerEntry` + `wild []wildSub`（`sync.RWMutex`）。
- 其余无状态。

---

## 6. 依赖

**本仓库依赖（2026-11-04 核对订正；原「无本仓库依赖 / 依赖终点」不成立）**：
- `chonkpilot-plugin-framework`：`core/mq` → `runtime/bus`（唯一总线实现）。
- `chonkpilot-util`：`core/paths` → `pathx`；`core/winlog` → `strx`；`core/uds` → `mqmatch`。
- `chonkpilot-framework/host` 内部：`core/winsvc` → `core/winproc`；`core/idgen` / `core/transport` 等相互引用（同 module 内）。
- 第三方：`golang.org/x/sys`（winlog/winsvc/winproc）。

`heartbeat` 的消费方：发布侧 `chonkpilot-framework/plugin/src/runtime/instance`（`heartbeat_split.go`，**保留既有导出名** `HeartbeatInterval`/`HeartbeatTimeout` 作常量别名，调用方无需改动）· 服务端扫描侧 `src/lib/llm/server`（`sweep_split.go`）· 数据层清理侧 `src/lib/data/persist`（`sweep_split.go`，**const `staleTimeout = heartbeat.Timeout`**）。**上提 lib 的原因**：data 层**不得依赖插件运行时**（分层约束），原先把 30s/90s 就地复制到 data 层；上提后三处引用同一常量，改值只需一处，依赖方向仍合法。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| mq 仅内存实现 | 去 NATS | 单体形态足够；跨进程待自研 |
| 前缀延迟注入 | 业务写相对主题 | 主题空间由宿主决定，业务零 `chonk.` 字面 |
| ResolveDir 允许 `../` | 不强制钳制 | 用户显式指定属有意行为 |

---

## 8. 场景与边界

- mq：总线 `Close` 后 `Emit` 追加 `errClosed`；handler panic → 收集 `mq: handler panic`。
- paths：`base` 为空时相对路径按字面 `Clean`。

---

## 9. 现状与待办

- ✅ **mq v1 `Publish/Subscribe` 兼容层保留、暂不废弃**：生产与测试代码已全部迁至 v2（`Emit`/`On`），仅 `chonkpilot-framework/plugin/src/runtime/bus/{promise_test,prefix_test}.go` 的 v1 自身测试仍使用（2026-11-04 核对：`src/lib/mq` 已不存在）。
- 🔵 去中心化 MQ（自研）远期。
- `winlog`/`winsvc` 使用方（2026-11-04 核对）：`src/others/mcp-server/service.go`（service）、`src/others/mcp-gateway/gateway_service.go`（service；内核在独立仓 `chonkpilot-agent/gateway`）。`src/lib/mcp-server` 未见 service 装配。

---

## 10. 关联测试

`src/test/chonkpilot-lib/unittest/`（2026-11-04 核对）：`paths/` · `exedir/` · `winlog/` · `mq/` · `msgkeys/`（4 个用例文件）。**无 `lockfile`**（该测试在 `chonkpilot-framework/host/src/core/lockfile/lockfile_test.go`）；mq v1 前缀/promise 用例在 framework `plugin/src/runtime/bus`；进程单例用例在 `chonkpilot-framework/host/src/singleinstance`。

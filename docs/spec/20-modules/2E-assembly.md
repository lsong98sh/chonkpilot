# 2E · src/lib/assembly（入口装配器）

> 状态：⚠️ **与代码基本一致**（2026-11-04 核对：装配契约已按 GATEWAY-2 L5 / P6 改写）
> 关联：[21-llm-server](21-llm-server.md) · [25-mcp-server](25-mcp-server.md) · [26-mcp-gateway](26-mcp-gateway.md) · [10-分层与依赖](../10-architecture/10-分层与依赖.md) · [23-工程与部署拓扑](../10-architecture/23-工程与部署拓扑.md)
> 代码目录：`src/lib/assembly/`（`assemble.go` + `assemble_test.go` + `go.mod`）
> 设计出处（决策过程不在本文重复）：见 [42-决策记录](../40-roadmap/42-决策记录.md) 与 [40-演进计划](../40-roadmap/40-演进计划.md)。

---

## 1. 职责与边界

- **一句话职责**：把「**能力源官方 go-sdk server（capability 契约）+ 执行配置 + 内嵌 gateway**」的**静态装配**收敛到一处，供 **gui / server 两个入口共用一份**（二者都经 `chonkpilot-llm/server.New` → 本包），**避免装配漂移**。
- **做**（2026-11-04 核对 `assemble.go`）：
  - 建官方 go-sdk server instance（`mcp.NewServer`，无传输、不 spawn）；
  - `RegisterContracts` 扫 capability 契约根把原语注册进该 instance；
  - 记录 app 根已注册工具名（运行期重扫时回收删除 / 改名者）；
  - `connectSelf`：经**内存传输**把该 instance 连成一个标准 MCP **ClientSession**，作为 `ServerEntry{ID:"self", Origin:Builtin, Session:ss}` 加入下游集合；
  - 以 `Params{Transport, Hooks, Servers, AsyncMode, CallTimeout, MaxTasks, ExecSink, ProgressHandler, Logf}` 构建内嵌 gateway（`Transport` / `Hooks` / `ProgressHandler` 由 `chonkpilot-gwadapter/mqadapter` 从 `Bus` 适配）。
- **不做**（明确边界）：
  - **不越权**：域工具 / 域 agent 的注册（`tools/register` / `prompts/register`）仍由 `llm/server.(*Server).Start` **自注册**（必须在 `gateway.Start` 之后；顺序约束）；
  - **只做静态装配**：不订阅任何主题、不启动任何 goroutine、不读 data / 不接触配置库。
  - **不含管理 REST、不含契约扫描端口**：`Options.ManageAddr`、`mcpgateway.ContractScanner`、`(*Stack).Scan` 全仓均**不存在**（2026-11-04 核对）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | **lib**（module `github.com/chonkpilot/chonkpilot-assembly`），无 `main`、无独立 exe |
| 运行位置 | 进程内（由 llm-server 装配期调用一次） |
| 生命周期 | 装配期一次性调用；产物 `*Stack` 由 llm-server 持有 |
| 装配顺序 | ① `mcp.NewServer` → ② `RegisterContracts` → ③ `AppToolNames` → ④ `connectSelf`（生成 self ClientSession）→ ⑤ `gateway.New` |

---

## 3. 输入输出

### 3.1 输入（`Options`）

| 字段 | 说明 |
|------|------|
| `Bus mq.Bus` | 上游总线（必填；内嵌 gateway 与调用方共用同一进程内总线；nil → gateway 构建失败） |
| `Root string` | 契约根（空 → `<exe 目录>/capability`，经 `chonkpilot-framework/host/core/exedir`） |
| `Servers []mcpgateway.ServerEntry` | 额外下游（外部 proxied / usr mcps，与 self 并存） |
| `AsyncMode string` | 工具异步模式（"" = 默认 auto；"never" = 强制同步，用于内置同步执行体） |
| `SkipTools bool` | **P6**：true = 系统级 app 根只注册资产（prompts/skills/resources），工具改由外部进程注册/执行（`assemble.go:46-48`） |
| `ExecSink mcpgateway.ExecSink` | 任务层执行池接入（nil = 层未注入 → gateway 侧 no-op） |
| `Logf func(...)` | 日志（nil → `fmt.Printf`） |

> 原稿的 `Options.ManageAddr` **不存在**（5 仓 grep 零命中；2026-11-04 核对），管理 REST 不在本装配器契约内。

### 3.2 输出（`Stack`）

| 字段 | 类型 | 说明 |
|------|------|------|
| `Server` | `*mcp.Server` | 能力源（prompts/skills/resources 资产面；域工具 / 域 agent 后续 AddTool 注入其上） |
| `Config` | `*mcpms.Config` | 执行配置（`mcp-server` / 外部 core-mcp-server 消费；I-82 统一施加点） |
| `AppTools` | `map[string]bool` | app 根已注册工具名（重扫回收用） |
| `Gateway` | `*mcpgateway.Gateway` | 内嵌网关（构建失败时可能为 nil，见 §5） |
| `Close` | `func()` | 关闭 self 的内存 ClientSession（`assemble.go:61-63`；nil = 未建立会话） |

### 3.3 接口

| API | 输入 | 输出 / 语义 |
|-----|------|------------|
| `Build(Options)` | 装配参数 | `(*Stack, error)`；`Stack` **永不为 nil** |
| `AppToolNames(root)` | 契约根 | `map[string]bool`；递归收集 `*.tool.md` 原语名（口径同 mcp-server：名 = 文件名去后缀；**额外跳过 `<root>/plugins/` 子树**，`assemble.go:186-191`） |

> **已删除的旧契约**（2026-11-04 核对）：`(*Stack).Scan(root) (*mcp.Server, error)`、`mcpgateway.ContractScanner` 类型/接口、`Params.MCPServer` / `Params.MCPConfig`。能力源自接入方式改为「内存 MCP ClientSession + `ServerEntry{ID:"self"}`」。

---

## 4. 关键流程

```text
Build(opts)
 ├─ logf 缺省 → fmt.Printf
 ├─ root 缺省 → exedir.Dir()/capability（exedir 失败 → "capability"）
 ├─ ms = mcp.NewServer(Implementation{"chonkpilot-server", "1.0.0"})
 ├─ cfg = mcpms.DefaultConfig(); cfg.Root = root; cfg.SkipTools = opts.SkipTools
 ├─ RegisterContracts(ms, root, cfg)
 │     失败 → log 一行「capability 契约注册失败（空能力面继续）」→ 继续（不 return err）
 ├─ st = &Stack{Server: ms, Config: cfg, AppTools: AppToolNames(root)}
 ├─ servers = opts.Servers ++ (connectSelf(ms) 成功 ? [ServerEntry{ID:"self", ...}] : [])
 │     connectSelf 失败 → 记日志、无 self 节点（工具面退化为仅外部下游），st.Close 保持 nil
 ├─ 若 opts.Bus != nil：
 │     adapter = mqadapter.New(opts.Bus)
 │     Transport = adapter；Hooks = adapter.Hooks()；ProgressHandler = adapter.ProgressHandler()
 │     否则三者保持零值（gateway.New 按必填校验失败）
 ├─ gw, err = mcpgateway.New(Params{Transport, Hooks, Servers, AsyncMode,
 │      CallTimeout: 60s, MaxTasks: 8, ExecSink, ProgressHandler, Logf})
 │     失败 → return st, err（能力源 / 执行配置仍可用）
 └─ st.Gateway = gw; return st, nil
```

> `connectSelf` 返回 `(nil, nil)` 表示连接失败（已记日志），此时不追加 self 节点，但 `Build` 仍继续构建 gateway；成功时返回的 `close` 存入 `Stack.Close`，由宿主持有并在退出时调用。

---

## 5. 失败态

| 场景 | 处置 |
|------|------|
| capability 契约注册失败 | **不中断**：记日志、以**空能力面**继续；`Build` 仍返回可用 `Stack` |
| self 内存会话建立失败 | 记日志、**无 self 节点**（自工具面缺失）；`Build` 仍继续 |
| gateway 构建失败（如 `Bus=nil`） | `Build` 返回**非空 `Stack` + error**（`Server`/`Config`/`AppTools` 仍可用） |
| `exedir.Dir()` 失败 | root 回落相对 `"capability"` |
| `AppToolNames` 根缺失 / 不可读 | 返回**空集**（`WalkDir` 错误被吞，不中断） |
| `Logf == nil` | 回落 `fmt.Printf` |

---

## 6. 依赖与边界

- **上游依赖**（`assemble.go:29-33`；`go.mod`）：`github.com/chonkpilot/chonkpilot-gwadapter/mqadapter` · `github.com/lsong98sh/chonkpilot-framework/host/core/{exedir,mq}` · `chonkpilot-mcp-server/server`（`DefaultConfig` / `RegisterContracts` / `Config`）· `github.com/lsong98sh/chonkpilot-agent/gateway`（`New` / `Params` / `ExecSink` / `ServerEntry`；module 路径**不带** `/src`）· `github.com/modelcontextprotocol/go-sdk/mcp`。
- **下游消费者**：`src/lib/llm/server`（`boot.go:106` 装配期调 `assembly.Build`；`capnodes.go:250` 的 `appToolNames` 是 `assembly.AppToolNames` 薄别名；**没有** `(*Server).Scan` 薄别名）。
- **边界**：**不 import data**（配置读取仍归 llm-server）；**不拍板 transport**（spawned/proxied 判据归 mcp-gateway）；不持有能力源/契约扫描（GATEWAY-2 L5 收口后 gateway 只走标准 MCP client）。

---

## 7. 现状与待办

- ✅ 已实施：入口装配器落地并接线 llm-server；能力源经内存 ClientSession 作为 `self` 下游接入 gateway；`SkipTools`（P6）与 `Stack.Close` 为现行公开契约（2026-11-04 核对）。
- ⚠️ 原稿的 `ContractScanner` 依赖倒置、`(*Stack).Scan`、`ManageAddr` 均已不存在，相关待办随 GATEWAY-2 L5 退场。
- 遗留 / 规划：无。

---

## 8. 关联测试（测试落点）

- **L1 包内（`src/lib/assembly/assemble_test.go`，6 例）**（2026-11-04 核对）：
  - `TestBuildWiring`（成功连线：`Server`/`Config`/`Gateway` 齐备 + 契约根注入 + `AppTools` 收集）；
  - `TestBuildGatewayFailure`（缺依赖 `Bus=nil` → 报错但能力源 / 执行配置恒保留）；
  - `TestBuildCapabilityErrorContinues`（非法契约 → 记日志、空能力面继续、不阻断 gateway）；
  - `TestBuildDefaultRoot`（`Root` 缺省 → `<exe 目录>/capability`）；
  - `TestAppToolNames`（递归收集 `*.tool.md` / 忽略非契约文件 / 跳过 `plugins/` 子树 / 缺失根空集 / 幂等）；
  - `TestBuildRepeatable`（同参两次 `Build` 得独立 Stack / Server / Gateway，无共享全局态）。
- **已退场**：`TestScanInjection` 与编译期断言 `var _ mcpgateway.ContractScanner = (*Stack)(nil)` 均不存在。
- **间接（装配期回归）**：`src/lib/llm/server`（`boot.go` 装配路径经 `assembly.Build`）——`llm/server/*_test.go` 起 server 即走本包；L4 `run_llm` / `run_server_deps` / 全量套件经真实装配验证。
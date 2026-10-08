# 2E · src/lib/assembly（入口装配器）

> 状态：✅ 与代码一致
> 关联：[21-llm-server](21-llm-server.md) · [25-mcp-server](25-mcp-server.md) · [26-mcp-gateway](26-mcp-gateway.md) · [10-分层与依赖](../10-architecture/10-分层与依赖.md) · [23-工程与部署拓扑](../10-architecture/23-工程与部署拓扑.md)
> 代码目录：`src/lib/assembly/`（单文件 `assemble.go`）
> 设计出处（决策过程不在本文重复）：见 [42-决策记录](../40-roadmap/42-决策记录.md) 与 [40-演进计划](../40-roadmap/40-演进计划.md)。

---

## 1. 职责与边界

- **一句话职责**：把「**能力源官方 go-sdk server（capability 契约）+ 执行配置 + 内嵌 gateway**」的**静态装配**收敛到一处，供 **gui / server 两个入口共用一份**（二者都经 `chonkpilot-llm/server.New` → 本包），**避免装配漂移**。
- **做**：
  - 建官方 go-sdk server instance（`mcp.NewServer`，无传输、不 spawn）；
  - `RegisterContracts` 扫 capability 契约根把原语注册进该 instance；
  - 记录 app 根已注册工具名（运行期重扫时回收删除 / 改名者）；
  - 以该 instance 作 `Params.MCPServer`（self 节点）构建内嵌 gateway；`ContractScanner` 注入 = `(*Stack).Scan`（依赖倒置：gateway 不读「源」）。
- **不做**（明确边界）：
  - **不越权**：域工具 / 域 agent 的注册（`tools/register` / `prompts/register`）仍由 `llm/server.(*Server).Start` **自注册**（必须在 `gateway.Start` 之后；顺序约束）；
  - **只做静态装配**：不订阅任何主题、不启动任何 goroutine、不读 data / 不接触配置库。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | **lib**（module `github.com/chonkpilot/chonkpilot-assembly`），无 `main`、无独立 exe |
| 运行位置 | 进程内（由 llm-server 装配期调用一次） |
| 生命周期 | 装配期一次性调用；产物 `*Stack` 由 llm-server 持有 |
| 装配顺序 | ① `mcp.NewServer` → ② `RegisterContracts` → ③ `AppToolNames` → ④ `gateway.New`（注入 `ContractScanner`） |

---

## 3. 输入输出

### 3.1 输入（`Options`）

| 字段 | 说明 |
|------|------|
| `Bus mq.Bus` | 上游总线（必填；内嵌 gateway 与调用方共用同一进程内总线） |
| `Root string` | 契约根（空 → `<exe 目录>/capability`，经 `lib/exedir`） |
| `Servers []mcpgateway.ServerEntry` | 额外下游（外部 proxied / usr mcps，与 self 并存） |
| `AsyncMode string` | 工具异步模式（"" = 默认 auto；"never" = 强制同步，用于 CLI） |
| `ManageAddr string` | 管理 REST（空 = 禁用） |
| `ExecSink mcpgateway.ExecSink` | 任务层执行池接入（nil = 层未注入 → gateway 侧 no-op） |
| `Logf func(...)` | 日志（nil → `fmt.Printf`） |

### 3.2 输出（`Stack`）

| 字段 | 类型 | 说明 |
|------|------|------|
| `Server` | `*mcp.Server` | 能力源（self 节点；域工具 / 域 agent 后续 AddTool 注入其上） |
| `Config` | `*mcpms.Config` | 执行配置（dir 节点与 self **共享**同一份，统一施加点） |
| `AppTools` | `map[string]bool` | app 根已注册工具名（重扫回收用） |
| `Gateway` | `*mcpgateway.Gateway` | 内嵌网关（构建失败时可能为 nil，见 §5） |

### 3.3 接口

| API | 输入 | 输出 / 语义 |
|-----|------|------------|
| `Build(Options)` | 装配参数 | `(*Stack, error)`；`Stack` **永不为 nil** |
| `(*Stack).Scan(root)` | 契约根 | `(*mcp.Server, error)`；实现 `mcpgateway.ContractScanner`（dir 节点接入） |
| `AppToolNames(root)` | 契约根 | `map[string]bool`；递归收集 `*.tool.md` 原语名（口径同 mcp-server：名 = 文件名去后缀） |

---

## 4. 关键流程

```text
Build(opts)
 ├─ logf 缺省 → fmt.Printf
 ├─ root 缺省 → exedir.Dir()/capability（exedir 失败 → "capability"）
 ├─ ms = mcp.NewServer(Implementation{"chonkpilot-server", "1.0.0"})
 ├─ cfg = mcpms.DefaultConfig(); cfg.Root = root
 ├─ RegisterContracts(ms, root, cfg)
 │     失败 → log 一行「capability 契约注册失败（空能力面继续）」→ 继续（不 return err）
 ├─ st = &Stack{Server: ms, Config: cfg, AppTools: AppToolNames(root)}
 ├─ gw, err = mcpgateway.New(Params{Bus, MCPServer: ms, MCPConfig: cfg,
 │      ContractScanner: st, Servers, AsyncMode, ManageAddr,
 │      CallTimeout: 60s, MaxTasks: 8, ExecSink})
 │     失败 → return st, err（能力源 / 执行配置仍可用）
 └─ st.Gateway = gw; return st, nil

(*Stack).Scan(root)
 └─ ms = mcp.NewServer(Implementation{"chonkpilot-gateway-dir", "1.0.0"})
    RegisterContracts(ms, root, cfg)  → (ms, err)
    （与 self 能力源共用同一执行配置 Stack.Config —— 工具级覆盖 / 沙箱 / 工具链统一施加点）
```

---

## 5. 失败态

| 场景 | 处置 |
|------|------|
| capability 契约注册失败 | **不中断**：记日志、以**空能力面**继续；`Build` 仍返回可用 `Stack` |
| gateway 构建失败 | `Build` 返回**非空 `Stack` + error**（`Server`/`Config`/`AppTools` 仍可用） |
| `exedir.Dir()` 失败 | root 回落相对 `"capability"` |
| `Scan` 读契约失败 | 返回 `(nil, err)`（由 gateway 的 `servers/register{dir}` 调用方处置） |
| `AppToolNames` 根缺失 / 不可读 | 返回**空集**（`WalkDir` 错误被吞，不中断） |
| `Config == nil`（`Scan` 误用） | 回落 `mcpms.DefaultConfig()` |
| `Logf == nil` | 回落 `fmt.Printf` |

---

## 6. 依赖与边界

- **上游依赖**：`github.com/chonkpilot/chonkpilot-lib`（`exedir`、`mq`）· `chonkpilot-mcp-server/server`（`DefaultConfig` / `RegisterContracts` / `Config`）· `chonkpilot-mcp-gateway/gateway`（`New` / `Params` / `ExecSink` / `ContractScanner` / `ServerEntry`）· `github.com/modelcontextprotocol/go-sdk/mcp`。
- **下游消费者**：`src/lib/llm/server`（`server.go` 装配期调 `assembly.Build`；`appToolNames` 薄别名转发 `assembly.AppToolNames`；`(*Server).Scan` 薄别名转发 `(*Stack).Scan`）。
- **边界**：**不 import data**（配置读取仍归 llm-server）；**不拍板 transport**（spawned/proxied 判据归 mcp-gateway）。

---

## 7. 现状与待办

- ✅ 已实施：入口装配器落地并接线 llm-server；`ContractScanner` 依赖倒置（gateway 不读「源」）。
- ✅ **L1 包内专测**：`assemble_test.go` 覆盖成功连线 / 缺依赖失败态 / 契约失败继续 / 缺省根 / `Scan` 注入 / `AppToolNames` / 重复装配独立（见 §8）。
- 遗留 / 规划：无。

---

## 8. 关联测试（测试落点）

- **L1 包内（`src/lib/assembly/assemble_test.go`，7 例）**：
  - `TestBuildWiring`（成功连线：`Server`/`Config`/`Gateway` 齐备 + 契约根注入 + `AppTools` 收集）；
  - `TestBuildGatewayFailure`（缺依赖 `Bus=nil` → 报错但能力源 / 执行配置恒保留）；
  - `TestBuildCapabilityErrorContinues`（非法契约 → 记日志、空能力面继续、不阻断 gateway）；
  - `TestBuildDefaultRoot`（`Root` 缺省 → `<exe 目录>/capability`）；
  - `TestScanInjection`（`Scan` 建**独立** dir server；非法契约 → 明确报错）；
  - `TestAppToolNames`（递归收集 `*.tool.md` / 忽略非契约文件 / 缺失根空集 / 幂等）；
  - `TestBuildRepeatable`（同参两次 `Build` 得独立 Stack / Server / Gateway，无共享全局态）；
  - 另有编译期断言 `var _ mcpgateway.ContractScanner = (*Stack)(nil)`（依赖倒置契约）。
- **间接（装配期回归）**：`src/lib/llm/server`（`server.go` 装配路径经 `assembly.Build`）——`llm/server/*_test.go` 起 server 即走本包；L4 `run_llm` / `run_server_deps` / 全量套件经真实装配验证。

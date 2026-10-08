# 2D · src/lib/router（LLM 出网协议适配）

> 状态：✅ 与代码一致
> 关联：[21-llm-server](21-llm-server.md) · [10-分层与依赖](../10-architecture/10-分层与依赖.md) · [23-工程与部署拓扑](../10-architecture/23-工程与部署拓扑.md) · [40-演进计划 §LR](../40-roadmap/40-演进计划.md)
> 代码目录：`src/lib/router/`（根包 + `internal/canon/` + `internal/adaptor/{openai,anthropic,echo}/`）
> 设计出处（决策过程不在本文重复）：见 [40-演进计划 §LR](../40-roadmap/40-演进计划.md)。

---

## 1. 职责与边界

- **一句话职责**：多 Provider（LLM 服务商）统一接入的 **canonical 层** —— 把「一次」LLM 请求—响应统一为 canonical 类型，按协议分派适配器，完成编码 → 出网 → 流式解析 → 归一回事件。
- **做**：
  - 统一 canonical 请求 / 响应 / 事件类型与错误分类（`internal/canon` 定义，根包别名再导出）；
  - Provider 注册表（`Register` / `Unregister` / `Reconcile`）与协议适配器表；
  - 一次 `Call` 的**全生命周期**：编码 → 发送（HTTP）→ 流式读取 → 解析 → 出事件；**持有连接生命周期**；
  - **连接层透明重试**（≤1 次；判据见 §4/§5）；
  - 协议实现：`openai`（chat completions）· `responses`（OpenAI Responses API）· `anthropic`（`/v1/messages`）· `echo`（内置兜底）。
- **不做**（边界，防职责蔓延）：
  - **无状态**：不出现轮次（turn）/ 会话（session）概念 —— 无历史管理、上下文拼接、工具循环、子 LLM 调度（全归 [21-llm-server](21-llm-server.md) 编排）；
  - **只做出网（LLM）**：工具执行 / 注册 / 清单归 gateway，本包只接收 `[]ToolDef`；
  - **不访问 data**：`data.ChatMsg` ↔ canonical 的薄转换落在 llm 侧（`src/lib/llm/server/routerconv.go`）；
  - **不做「需要决策或可见的重试」**：超时 / 已输出后失败 / 上报「重试中」/ cancel / 次数用尽处置全归 llm；
  - 形态 = **纯 lib**（无 facade / 无 MQ / 无 DB / 无 exe，与 `src/lib/core/dsl` 同类）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | **纯 lib**（module `github.com/chonkpilot/chonkpilot-router`），无 `main`、无独立 exe |
| 运行位置 | 进程内（llm-server） |
| 依赖方向 | `router → internal/adaptor → internal/canon`（**单向无环**）；本包**只 import 标准库 + 自身 `internal/*`**，**不 import 任何其它 chonkpilot-* module** |
| 装配方 | `src/lib/llm/server`：`router.New(router.Config{})` → `Register` / `Reconcile`（`server.go` / `routercall.go`） |

---

## 3. 输入输出（对外接口）

### 3.1 构造与注册

| API | 输入 | 输出 / 语义 |
|-----|------|------------|
| `New(Config{HTTPClient})` | 出网客户端（nil = 适配器自建） | `*Router`（已装配 4 个内置适配器） |
| `Register(Spec)` | `{Name, Protocol, BaseURL, APIKey, DefaultModel}` | 幂等：同名**覆盖**；Name/Protocol 空 → `*Error{invalid}`；**apiKey 只存内存、不落日志** |
| `Unregister(name)` | provider 名 | 未注册 → `*Error{invalid}` |
| `Reconcile([]Spec)` | 期望注册集 | `Report{Added,Updated,Removed,Unchanged,Failed}`；逐字段相同者**零副作用**；非法 Spec 计 `Failed` 并返回 `errors.Join` |
| `Capabilities(provider)` | provider 名 | `Caps`（未注册 / 无适配器 → 零值） |

### 3.2 一次调用（`Call`）

```
输入：ctx；Request{Provider, Messages []Message, Tools []ToolDef, Options CallOptions}
输出：<-chan Event（含 7 类事件，done/error 后关闭）；前置校验失败 → (nil, *Error{invalid})
```

- `Request.Provider` 为空 **或** = 内置保留名 `echo` → 回落**内置兜底**（见 §5）；其余未注册名仍报 `invalid`（不静默兜底）。
- `Options.Model` 为空 → 回落 `Spec.DefaultModel`。

### 3.3 canonical 事件（7 类；`EventType`）

| 事件 | 载荷要点 | 语义 |
|------|---------|------|
| `text_delta` | `Text` | 正文增量 |
| `reasoning_delta` | `Text` | 思考链增量 |
| `tool_call_delta` | `ToolCall *ToolCallDelta` | 工具调用**增量**（片段） |
| `tool_call` | `ToolCallFull *ToolCall` | 完整的一次工具调用 |
| `usage` | `Usage` | token 统计（0 = 上游未提供） |
| `done` | `FinishReason` / `Model` / `Signature` | 终态 |
| `error` | `Err *Error` | 终态（失败路径唯一一条） |

> 语义收口：带 `_delta` 后缀一律表示增量；`tool_call` 表示完整值。

### 3.4 canonical 请求模型

- `Message{Role, Kind, Content []Part, ToolCalls, ToolCallID, Reasoning, ReasoningSignature}`；`Part{Type: text|image, Text, Image}`。
- `ToolDef{Name, Description, Parameters json.RawMessage}`。
- `CallOptions{Model, Temperature*, MaxTokens*, TopP*, Reasoning*, ResponseTimeout, StreamTimeout}`（指针 nil = 不下发）。
- `ReasoningSignature` 承载 Anthropic 思考块签名，随 `Reasoning` 保真往返。

---

## 4. 关键流程

### 4.1 `Call`（`router.go`）

```text
Call(ctx, req)
 ├─ provider 解析：spec = specs[req.Provider]；ad = adapters[spec.Protocol]
 │    └─ Provider=="" 或 =="echo" → spec=builtinFallback, ad=adapters["echo"]
 ├─ 前置校验：未注册 provider / 协议无适配器 → 返回 *Error{invalid}（不返回通道）
 ├─ Options.Model 空 → 回落 Spec.DefaultModel
 └─ 起 goroutine（defer close(out)）：
      loop attempt=0,1,…：
        (emitted, err) = attemptOnce(ctx, ad, spec, req, out)
        err==nil → return
        若 emitted==0 且 attempt<1 且 err.Kind==network 且 ctx 未取消：
             sleepCtx(300ms)（被取消 → 收手）→ continue（透明重发）
        ctx 已取消 → return（不投错误事件）
        否则投一条 Event{Type: error, Err: err} → return
```

### 4.2 `attemptOnce`（`retry.go`）

```text
把 ad.Stream 写入内部通道 inner（goroutine 返回 error 到 errCh）：
  select ctx.Done() → 返回 errorOf(ctx.Err())
       errCh        → close(inner)；返回 errorOf(err)
       inner 事件    → emitted++；转发到 out（ctx 取消 → 收手）
纪律：适配器**不关闭** inner；仅其 Stream 返回后本函数关闭（ctx 取消时不关闭，避免 send-on-closed）。
```

### 4.3 适配器内部（`internal/adaptor/**`）

```text
Stream(ctx, spec, req, out)
 ├─ Degrade(caps, &req)：硬不支持（Tools / Images）→ *Error{invalid}；软不支持（Reasoning / TopP）→ 就地清零
 ├─ 编码请求体（协议要素见各包文件头：messages / tools / stream / 采样 / 上限字段…）
 ├─ Exchange.PostJSON(ctx, url, headers, body, onPayload)
 │    ├─ 首字节超时 ResponseTimeout（默认 120s）/ chunk 间隔 StreamTimeout（默认 60s）
 │    ├─ 非 2xx → StatusError（Kind/Status/Retry-After；非 JSON 体原样取 message）
 │    ├─ 2xx + text/event-stream → SSE 泵；2xx + 其它 Content-Type 且体首行像 SSE → 仍按 SSE
 │    │        否则按「忽略 stream=true 回单发 JSON」整体回调一次
 │    └─ SSE 解码（sse.go）：data: 前缀 / 多行 / 注释心跳 / 行首空白 / 跨 chunk 半包 / [DONE]；
 │           流中断（事件边界前断）→ ErrTruncated（归 network）
 ├─ 逐载荷映射为 canonical 事件（toolcall.go 按 index 聚合片段；usage.go 归一 usage）
 └─ 终态 done 只投一次；成功收尾可用 ErrStreamDone 提前结束（不等 EOF）
```

---

## 5. 失败态

### 5.1 错误分类（`Error{Kind, Message, Retryable, Status, RetryAfter}`）

| Kind | 触发 | 可重试 | 归属 |
|------|------|:------:|------|
| `network` | 连接失败 / 重置 / 断链 / SSE `ErrTruncated` 半包 | ✅ | router 透明重试 ≤1；余归 llm |
| `timeout` | 首字节 / 流间隔超时 | ✅ | **llm 决策**（router 不透明重发） |
| `rate_limit` | 429 | ✅ | llm 决策（退避取 `RetryWait`） |
| `server` | 5xx | ✅ | llm 决策 |
| `auth` | 401 / 403 | ❌ | 直接透出 |
| `protocol` | 解析失败 / 其余 4xx / 非 4xx5xx 异常 | ❌ | 直接透出 |
| `invalid` | 调用方用法错误（未注册 provider / 空 Name·Protocol / adapter nil / 硬降级） | ❌ | 前置校验，不经 HTTP |

- `KindFromStatus`：401/403 → auth；429 → rate_limit；5xx → server；其余 → protocol。
- `RetryWait(err, attempt)`（根包）：`Retry-After` 优先，否则指数退避 1s·2s·4s… 封顶 30s；不可重试 / nil → 0。

### 5.2 透明重试的唯一判据

**仅当「尚未向上层输出过任何事件」（`emitted == 0`）且失败属连接层（`network`）** 才透明重发 ≤1 次（毫秒级退避 300ms）。一旦已投出任何事件（哪怕一个 `text_delta`）→ 立即透出错误，**绝不重发**。超时 / 429 / 5xx 一律交 llm。

### 5.3 声明式降级（`adaptor.Degrade`）

| 能力 | 触发 | caps=false 时 |
|------|------|--------------|
| Tools | `req.Tools` 非空 | **硬**：`*Error{invalid}`（报错，不降级） |
| Images | 消息含 image 块 | **硬**：`*Error{invalid}` |
| Reasoning | `Options.Reasoning` 非空 | 软：`Reasoning` 清零（省略思考，回落上游默认） |
| ReasoningEffort | `Options.Reasoning` 非空 | 软：**仅档位（Effort）清零、保留 `Reasoning`**（仅 Anthropic 支持档位） |
| TopP | `Options.TopP` 非空 | 软：清零 |
| MaxTokensRequired | `Options.MaxTokens` 空 | caps=true → 适配器补缺省 |

> 〔订正（2026-10-08）：原「Reasoning / ReasoningEffort」合一行「软：清零」**不准确、已拆分** —— `!caps.ReasoningEffort` 时**只清档位、保留 `Reasoning`**（原先误清整个思考请求），`!caps.Reasoning` 才整体清零；与 `src/lib/router/internal/adaptor/degrade.go` 注释表一致。〕

### 5.4 内置兜底 `echo`

无任何可用 provider（`Provider==""`）或命中保留名 `echo` → echo 适配器**不发任何 HTTP**，以固定文案
`收到<最后一条真实用户消息>，目前无法回复，请设置LLM。` 回一条 `text_delta` + `done{finish_reason: "stop"}`（无真实用户消息则只投 `done`）。echo **不进入 `specs`**（`Reconcile` 永不注销、不出现在可配置列表）；它**不调用 `Degrade`**（带工具 / 图片请求**忽略**而非硬报错）。

---

## 6. 依赖与边界

- **上游依赖**：仅标准库 + 自身 `internal/{canon,adaptor}`。**零本仓库其它 module 依赖**（同 `dsl` 类）。
- **下游消费方**：`src/lib/llm/server`（经 `routerconv.go` 折算消息、`routercall.go` 注册/对账/调用、`llm_testconn.go` 探活、`turn.go` 保留可视重试循环）；`gui` / `server` / `desktop` 经 llm 间接依赖（Go `replace` 不跨 module 传递，各 module 自行 `require` + `replace ../router`）。
- **接口不引入 Provider 标识符**：本包用 `Spec` 表示 LLM 服务商；gateway `Provider` = 工具提供方（同名异义）。

---

## 7. 现状与待办

- ✅ 已实施：canonical 类型、注册/分派、四协议实现、SSE/共用件、增量聚合、usage 归一、声明式降级、错误分类与退避值、`llm` 接线。
- ✅ **`router.RetryWait` 已接线**：llm 可视重试经 `RetryWait(err, attempt)` 取退避（`Retry-After` 优先 + 指数退避），`RetryAfter` 由 `routercall.go` `mapRouterError` 透传；llm **不再自持 `retryDelay`**（该配置项已整体移除），`retryCount` 次数语义不变。
- 已知线格式差异（事件语义不变）：恒带 `stream_options.include_usage`；上限字段按模型择一（`max_completion_tokens`）；无参工具补空 schema；空文本图消息省略空文本块；非 2xx 错误串按 JSON 取值。

---

## 8. 关联测试（测试落点）

**L1 白盒（`src/lib/router/**`，仅此层，无 L2/L3/L4 专门套件）**：

- 根包：`router_test.go`（注册/分派/`Call` 契约/能力/内置 echo 回落/透明重试 5 例/并发）· `reconcile_test.go`（Add/增量/Remove/Failed/同名后者胜）· `error_test.go`（Kind↔Retryable / 状态码映射 / `ToError` / `RetryWait`）· `alias_test.go`。
- `internal/adaptor`：`sse_test.go`（多行/注释心跳/空白/CRLF/半包/截断）· `exchange_test.go`（SSE 泵与单发 JSON）· `errors_test.go`（状态码/Retry-After/传输错误/流内 error）· `toolcall_test.go` · `usage_test.go` · `degrade_test.go` · `fixture_test.go`（`testdata/*.sse.json` 金样本）。
- `internal/adaptor/openai`：`openai_test.go`（编码/上限字段/SSE 映射/无 `[DONE]`/按 index 工具/截断/非 SSE 体/错误分类）· `responses_test.go`（Responses 全链）。
- `internal/adaptor/anthropic`：`anthropic_test.go`（编码/签名往返/`max_tokens` 必填/thinking 与采样互斥/端点拼接/校验/流映射/无 `message_stop`/流内错误/HTTP 分类）。
- `internal/adaptor/echo`：`echo_test.go`（固定文案/caps/忽略图片工具/模型覆盖/取消）。
- **间接**：`src/lib/llm/server` 的 `llm_routing_test` / `routerconv_test` / `llm_echo_test` / `llm_images_test` / `llm_testconn_test`（接线折算与探活）。

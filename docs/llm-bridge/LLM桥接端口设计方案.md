# LLM 桥接端口设计方案（独立稿）

> **入口现状（BRIDGE-3，2026-10-24）**：桥入口 = **UDS-only**，默认 `%LOCALAPPDATA%\chonkpilot\llm.sock`（socket `0600` / 父目录 `0700`；`CHONK_LLM_UDS` 覆盖，`off` / `-` 关闭），**不监听任何 TCP 端口**；需要 TCP 的客户端自建 loopback→UDS 字节代理（demo 见 `capability/plugins/demo-python/llm_bridge_proxy.py`）。路由 `/llm/<name>/<rest>`、字节级透传、覆盖鉴权头、只读 `ResolveBridgeTarget`、零新增 MQ 主题。权威现状见 [62 §3](../spec/60-reference/62-命令行与参数.md) · [19 §6](../spec/10-architecture/19-单进程形态与实例隔离.md) · [49 BRIDGE-3](../spec/40-roadmap/49-待实现项.md)。
> 状态：✅ **已实施**（步骤 1–8，2026-10-16 / 步骤 8 于 2026-10-19 / 2026-10-24 入口改 UDS）
> 日期：2026-10-16
> 关联（**spec**）：[49-待实现项 §2](../spec/40-roadmap/49-待实现项.md)（登记项 `BRIDGE-1`）· [21-llm-server](../spec/20-modules/21-llm-server.md) · [19-单进程形态与实例隔离](../spec/10-architecture/19-单进程形态与实例隔离.md) · [62-命令行与参数](../spec/60-reference/62-命令行与参数.md) · [2D-router](../spec/20-modules/2D-router.md) · [11-MQ与消息](../spec/10-architecture/11-MQ与消息.md) · [50-测试体系](../spec/50-testing/50-测试体系.md)
> 代码目录：`src/lib/llm/llmbridge/`（入口）· `src/lib/llm/server/bridge.go`（只读查询）· `src/lib/gui/host/main.go`（唯一装配点）

---

## 1. 问题与目标

- **要解决的问题**：本机脚本（如 Python）想直接调用**本机已配置的 LLM**，但：
  - 不想持有 / 传递 apiKey；
  - 不需要 agent 编排（不注入工具、不代执行 `tool_calls`、不做轮次循环）；
  - 需要「裸调用 + 自行解析结果」。
- **目标（可验收）**：
  1. 仅监听本机 UDS `%LOCALAPPDATA%\chonkpilot\llm.sock`（与业务消息面分离；`CHONK_LLM_UDS` 覆盖），**不开任何 TCP 端口**；
  2. **免 api-key**：使用侧不必带 key；
  3. **路径段选 provider**：`/llm/<providerName>/...`；保留段 `default` = 系统默认 LLM；
  4. 取 `/llm/<name>` **之后**的剩余路径**原样拼接**到 `provider.base_url`；body / 流式 / 非流式**字节级透传**；
  5. 出站**覆盖鉴权头**（用服务端存的 key）；
  6. **不解析 body** → 天然支持 openai / anthropic / responses 及其它任意协议；
  7. 不注入工具、不代执行 `tool_calls`、不做 agent 编排。
- **非目标**：
  - 不做**协议翻译**（OpenAI 体 → Anthropic 体等）；
  - 不做**非流式聚合**（透传即可，上游怎么回就怎么回）；
  - **不新增 / 修改 / 删除任何 MQ 主题与 payload**（本入口是 HTTP 面，不入消息面）；
  - 不接鉴权（免 key 的前提是**仅本机 UDS + 文件 ACL**）。

---

## 2. 总体方案

```
python 脚本 ──（自选 LLM、自用该协议的 SDK）──▶ UDS %LOCALAPPDATA%\chonkpilot\llm.sock
                                                 │ HTTP 路径 /llm/<name>/<rest>
                                                 │ ① 解析 <name> → provider 配置
                                                 │ ② 剩余路径 <rest> 原样拼到 provider.base_url
                                                 │ ③ 覆盖鉴权头（服务端存的 key）
                                                 ▼
                                         provider.base_url/<rest>
```

**一句话**：一个「选 provider + 注入 key」的**透明反向代理**——不解析、不转换、不编排。

---

## 3. 详细设计

### 3.1 监听与开关

| 项 | 值 |
|----|----|
| 开关 | **UDS `llm.sock`**（BRIDGE-3，2026-10-24；原 `127.0.0.1:5669` TCP 已删除） |
| 裸端口归一 | 不适用（UDS 路径，无端口归一） |
| 仅 loopback | UDS 依赖同用户 ACL，无网络暴露 |
| 独立性 | 与静态资产端口（`127.0.0.1:5670`）/ `--test-port` **完全独立** |

### 3.2 选 provider

- 路径段：`/llm/<providerName>/<rest>`；`<providerName>` = usr `llms[].name`。
- 保留段 `default` → **系统默认 LLM**（exe flags 隐含默认，即 router 侧的 `__default__`）。
- 查不到 → **404 + 明确文案**（不静默回落）。
- `echo`（内置兜底，不发 HTTP）**不**作为桥接 provider（`llms[]` 中本就不存在）。

### 3.3 转发与鉴权注入

- 目标 URL = `provider.base_url` + `/<rest>`（`rest` 逐字保留，含查询串）。
- body / 流式（SSE）/ 非流式**直 pipe**，不缓冲、不改写。
- **鉴权头覆盖**（先删后设、不追加；确保用服务端 key，防客户端自报）：

| `provider.protocol` | 注入头 |
|---|---|
| `openai` | `Authorization: Bearer <apiKey>` |
| `responses` | `Authorization: Bearer <apiKey>` |
| `anthropic` | `x-api-key: <apiKey>` |
| 其它 | **400 明确不支持**（扩展点；无法判定鉴权方式，不静默猜） |

- 其余请求头 / 查询串 / body 原样透传；上游响应（状态码 + 头 + body）原样回。

### 3.4 路径与 base_url 约定（使用侧）

`provider.base_url` 已是该 provider 的 API 前缀，剩余路径**原样拼接**。客户端连 UDS（`http://unix` 为占位 host，真正走 socket）：

```python
import httpx
# Linux/macOS：httpx 可直接指定 uds
transport = httpx.HTTPTransport(uds="/home/<user>/.chonkpilot/llm.sock")
r = httpx.post("http://unix/llm/default/v1/chat/completions", json=body, transport=transport)
```

```python
# Windows / 只支持 TCP base_url 的 SDK：先起 loopback→UDS 字节代理
# python capability/plugins/demo-python/llm_bridge_proxy.py --listen 127.0.0.1:5669
from openai import OpenAI
c = OpenAI(base_url="http://127.0.0.1:5669/llm/gpt4", api_key="unused")
c.chat.completions.create(model="gpt-4o", messages=[...])
# → POST /llm/gpt4/chat/completions → 上游 {base_url}/chat/completions

# Anthropic 型
from anthropic import Anthropic
c = Anthropic(base_url="http://127.0.0.1:5669/llm/claude", api_key="unused")
c.messages.create(model="claude-...", max_tokens=1024, messages=[...])
# → POST /llm/claude/v1/messages → 上游 {base_url}/v1/messages
```

> 约定：`provider.base_url`（即配置里的 `baseUrl`）配到该 provider 的 **API 前缀**——OpenAI 型通常含 `/v1`，Anthropic 型不含。

### 3.5 失败语义

| 场景 | 行为 |
|---|---|
| UDS 目录 / socket 权限不满足 | **拒绝启动** + 明确错误（宿主侧降级为 Warn，不致命） |
| 已有活实例占用 socket | 拒启（陈旧 socket 先清理） |
| provider 不存在 / 非 `llms[]` 名 | 404 + 明确文案 |
| `base_url` 为空 | 回落 `--llm-base`（与 `specFor` 同口径） |
| 协议未识别 | 400 + 明确文案 |
| 上游错误 | 原样透传（状态码 + body） |

---

## 4. 规则与约定

| 项 | 结论 | 理由 |
|----|------|------|
| 入口 | 本机 UDS `llm.sock`，默认开（`off` / `-` 关闭） | 独立于业务消息面，脚本零配置可用（TCP 客户端自建代理） |
| 监听 | UDS-only，目录 `0700` / socket `0600` | 免鉴权 → 必须由文件 ACL fail-closed |
| 选 provider | 路径段 `/llm/<name>` | SDK 的 `base_url` 直接可用；转发路径逐字不变，无需猜 `/v1` |
| 鉴权 | 桥注入服务端存的 key，**覆盖**客户端头 | 使用侧免 key；防客户端自报 |
| 协议 | **不解析 body** | 天然支持任意协议；无需翻译器 |
| 工具 | 不注入、不代执行 | 使用侧自行执行 |
| 干扰 | 不碰 router / turn / gateway / MCP | 只读 provider 配置 |
| 安全 | 本机同用户任意进程可白嫖已配置 key | 已知威胁面（免鉴权 + 本机 UDS 的取舍） |

---

## 5. 与既有机制的关系

- **与业务消息面**：两者独立——桥只是 UDS 上的独立 HTTP 面，进程内 MQ 为唯一业务总线（[19 §6](../spec/10-architecture/19-单进程形态与实例隔离.md)）。
- **与 [`chonkpilot-agent/router`](../spec/20-modules/2D-router.md)**：本入口**只读** provider 配置（name → base_url / apiKey / protocol），**不 `Register` / 不 `Call` / 不经 canon** → 对原 LLM 出网路径**零影响**。
- **与 `turn.go` / gateway / MCP**：完全不碰。
- **与消息面（[61-消息一览](../spec/60-reference/61-消息一览.md)）**：**零新增 MQ 主题 / payload**（纯 HTTP-over-UDS 面）。

---

## 6. 边界与异常

- **并发**：无状态（每次请求独立解析 provider），天然并发安全。
- **桌面单体形态**：由 `src/lib/gui/host`（唯一装配源）在 `FormDesktop` 时同开（✅ 已实施；UDS 路径 `CHONK_LLM_UDS` 覆盖，默认 `%LOCALAPPDATA%\chonkpilot\llm.sock`，`off` / `-` 关闭；见 §7 步骤 8）。
- **密钥**：apiKey 只随请求内存流转，**不落日志**（对齐既有掩码口径）。

---

## 7. 落地清单

| 步骤 | 内容 | 状态 | 关联代码 |
|------|------|:---:|---------|
| 1 | 新增 `src/lib/llm/llmbridge/`：UDS 监听 + 路径路由 + 反向代理 + 鉴权覆盖 | ✅ | `src/lib/llm/llmbridge/llmbridge.go` |
| 2 | `package server` 导出只读查询 `ResolveBridgeTarget(name) → {baseURL, apiKey, protocol}` | ✅ | `src/lib/llm/server/bridge.go` |
| 3 | UDS 入口 + `CHONK_LLM_UDS` 覆盖 + 装配 | ✅ | `src/lib/gui/host/main.go` |
| 4 | spec 同步：[62-命令行与参数](../spec/60-reference/62-命令行与参数.md) 登记入口 | ✅ | docs |
| 5 | 订正 [19 §6](../spec/10-architecture/19-单进程形态与实例隔离.md)「进程内通道」表（`llm.sock`） | ✅ | docs |
| 6 | 测试：openai 型转发 / anthropic 型转发（路径拼接 + 鉴权覆盖 + 流式直通） | ✅ | `src/lib/llm/llmbridge/llmbridge_test.go` · `src/lib/llm/server/bridge_test.go` · `src/test/chonkpilot-llm/unittest/llmbridge_test.go` |
| 7 | 测试体系登记：HTTP 面豁免「MQ 驱动」断言（见 [50-测试体系](../spec/50-testing/50-测试体系.md)） | ✅ | docs（50 §7） |
| 8 | 桌面单体形态**同开**（2026-10-17 用户定案「需要」） | ✅ 已实施 | `src/lib/gui/host/main.go`（`FormDesktop`；默认 `llm.sock`、`CHONK_LLM_UDS` 覆盖 / `off` 关闭；UDS-only、免 api-key、零 MQ 变更；冒烟 `run_llm_bridge.py`） |

> **实施说明（2026-10-16）**：
> - **步骤 2 落点** = `src/lib/llm/server/bridge.go`（同 `package server`；设计稿 §7 原记 `server.go`，为避免与既有实现文件混杂而独立成文件）。
> - **`protocol` 返回原值（关键）**：`ResolveBridgeTarget` 读 usr `llms[].protocol` 的**原始值**，**不经** `NormalizeLLMProtocol` —— 后者把 `anthropic` 折叠为 `openai`（`llm.go`：未知/缺省 → openai），而归一化只属 router 出网口径；桥接入口是**不经 router 的透明反代**，必须按原值区分鉴权头（anthropic → `x-api-key`）。**零行为变更**（既有 router 路径与归一表均未改）。
> - **装配范围**：单点 `src/lib/gui/host`（`FormDesktop`；✅ 步骤 8 已实施）。
> - **步骤 8（2026-10-17 定案 / 2026-10-19 实施 / 2026-10-24 改 UDS）**：桌面单体**同开**本入口（默认 UDS `llm.sock`、`CHONK_LLM_UDS` 覆盖、`off` / `-` 关闭、免 api-key、零 MQ 主题/payload 变更）；启动失败不致命、收尾先于 srv 停止；冒烟 = `src/test/chonkpilot-gui/systest/run_llm_bridge.py`（转发 / 鉴权头覆盖 / 未知 provider 404 / 客户端自建 TCP 代理，全绿）；已同步 [62 §3](../spec/60-reference/62-命令行与参数.md) / [19 §6](../spec/10-architecture/19-单进程形态与实例隔离.md) / [49 BRIDGE-1](../spec/40-roadmap/49-待实现项.md)。

---

## 8. 关联文档

- [49-待实现项](../spec/40-roadmap/49-待实现项.md)（登记项 `BRIDGE-1`）
- [21-llm-server](../spec/20-modules/21-llm-server.md) · [19-单进程形态与实例隔离](../spec/10-architecture/19-单进程形态与实例隔离.md) · [62-命令行与参数](../spec/60-reference/62-命令行与参数.md)
- [2D-router](../spec/20-modules/2D-router.md) · [11-MQ与消息](../spec/10-architecture/11-MQ与消息.md) · [50-测试体系](../spec/50-testing/50-测试体系.md)

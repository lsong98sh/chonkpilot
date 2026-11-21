# wanwu vs AOP 对比报告

> 生成时间：2026-11-21（预定 4:00 任务自动执行）
> 对比对象：`E:\BizWorks\wanwu-git`（UnicomAI/wanwu 开源智能体平台，Apache-2.0） vs `E:\BizWorks\AOP`（多智能体运营决策基座 POC）
> 说明：原定检查 `E:\BizWorks\wanwu` 不存在；实际源码落在 `E:\BizWorks\wanwu-git`（完整 git 仓库，2026-11-20 23:05 下载，sparse checkout 72%，另有 `E:\BizWorks\wanwu.zip` 备份），视为下载就绪。
> wanwu 为第三方代码，本次全程只读分析，未执行其任何构建工具。

---

## 0. 一句话总评

| | wanwu | AOP |
|---|---|---|
| 定位 | 通用智能体**平台**（多租户 SaaS 形态，对齐 Dify/Coze） | 企业运营决策**基座 POC**（"一底座两平面三隔离"，边界克制、工程纪律重） |
| 技术栈 | Go 微服务×10 + Python×3（callback/rag/workflow）+ Vue2 前端 | Java Spring Boot 单体 + Python worker×3 + Vue3 前端 |
| 通信 | gRPC（proto 全量契约）+ Kafka + SSE | 同步 REST + SSE，无队列无事件总线 |
| 测试 | 近乎为零（1 个 `_test.go`） | 57 个测试类 ~252 全绿 + worker pytest 108 |
| 工程纪律 | 弱（AutoMigrate、无 CI） | 强（Flyway V1–V26、统一错误码、审计面） |

**核心差异**：wanwu 是"功能广度优先"的平台型产品；AOP 是"正确性与边界优先"的基座型 POC。两者最值得做的交换是：**AOP 借 wanwu 的安全防护、模型/MCP 生态广度与流式工程；wanwu 借 AOP 的测试纪律与迁移治理**（后者对用户无直接收益，仅作对照）。

---

## 1. 架构对比

### 1.1 进程模型与服务拆分

| 维度 | wanwu | AOP |
|---|---|---|
| 服务拆分 | 11 个 Go 服务（bff/iam/model/mcp/knowledge/rag/assistant/agent/app/channel/operate）+ tidb-setup init 容器 + 3 个 Python 服务 + wga-sandbox 沙箱容器 + nginx 前端 | 1 个 Spring Boot 单体（8088，内嵌 SPA）+ 3 个 Python worker（parse/ocr/embed，18081–3）+ 可选本地数据服务（18084）+ PG18+pgvector |
| 网关 | bff-service 唯一对外 HTTP 入口，聚合 21 个 gRPC client（`internal/bff-service/service/service.go:36-63`） | 单体自带 `/api/v1/**`，进程内直调 qkdev service（`config/QkdevIntegrationConfiguration.java` 关闭 HTTP 回环） |
| 服务间通信 | gRPC proto 契约（21 个 proto 目录，`proto/`）；agent-service 走 HTTP；workflow 为外部 Python 项目经 HTTP 回调 | 同步 REST + worker Bearer token HTTP；**无队列无事件总线**（worker 合同 §1.2 明示 v2 再做） |
| 异步 | Kafka（knowledge-service 文档异步处理，`internal/knowledge-service/pkg/mq/kafka.go`：Admin 自动建 topic） | `@Scheduled` 轮询 PENDING job（默认关闭，手工触发） |
| 部署形态 | 4 套 docker-compose（mysql/tidb/oceanbase/ontology 信创矩阵） | 本地 PowerShell 脚本 + mvn/npm，依赖 `../qkdev`、`../bizworks-eai` 两个本地工程 |

**判断**：wanwu 的微服务拆分是产品规模化诉求，**不建议 AOP 照搬**（AOP 单体 + 旁路 worker 的形态与其 POC 阶段匹配，且"进程内直调代替 HTTP 回环"本身就是比 wanwu 21 个 gRPC client 更克制的正确选择）。但 **Kafka 式文档异步处理管线**值得借鉴——AOP 目前知识 job 靠轮询/手工触发，处理大文档时同步面容易超时。

### 1.2 依赖注入与生命周期

| | wanwu | AOP |
|---|---|---|
| 方式 | 无 DI 框架：手写 Init 链 + 包级全局单例（`cmd/bff-service/main.go:59-151` 顺序 Init）；agent/knowledge 另有 LoaderService 插件生命周期 | Spring 装配（7 个 @Configuration），qkdev/EAI 以 jar 内嵌 |

**wanwu 的 `LoaderService` 机制值得看一眼**（`internal/agent-service/pkg/loader_service.go:8-13`）：按 StopPriority（Grpc=-1→Default=1→AsyncTask=2→DB=3）**反序优雅退出**，空导入副作用注册，`InitAllService/StopAllService` 统一驱动。AOP 作为 Spring 应用有现成的 SmartLifecycle/`@PreDestroy` 体系，不必抄，但"退出顺序显式分级"的思路可纳入 worker 关闭流程（AOP worker 目前无统一进程管理，是 todo.md 已知短板）。

### 1.3 持久化

| | wanwu | AOP |
|---|---|---|
| 数据库 | MySQL/TiDB/OceanBase 三选一（`pkg/db/client.go:30-75` 按 DBName 分支） | PostgreSQL 18 + pgvector 0.8.1 |
| ORM/迁移 | GORM **AutoMigrate**（无独立 migration 工具），初始数据由 tidb-setup 容器执行 SQL | Hibernate + **Flyway V1–V26**，表名三段规则（aop_*/t_*/无前缀） |
| 检索 | Elasticsearch 8（dense_vector + knn，`rag/rag_open_source/rag_es_server_unify/`） | pgvector（**未真正启用**，实际跑 512 维 JSON fallback——todo.md P0.2）+ BM25 + RRF |
| 缓存/队列 | Redis×4 业务域实例（含 Sentinel failover）；Kafka | 无 |
| 对象存储 | MinIO | 本地 `./data/storage` |

**判断**：AOP 的 Flyway 治理**严格优于** wanwu 的 AutoMigrate（多服务并发 AutoMigrate 在生产是坑）。AOP 应坚持现状。可借鉴点：
1. **Redis 分域客户端**（OP/Sys/Assistant/Rag 四实例隔离）——AOP 将来加缓存/限流时按域隔离，避免单实例被打爆全局连坐；
2. **pgvector 落地诊断**：wanwu 用 ES 的 `q_{dim}_content_vector` 按向量维度动态命名字段（`es_rag_server.py:201-203`），支撑同一索引服务多 embedding 模型——AOP 修 pgvector 1024 维 HNSW 时可参考"按模型维度分列/分索引"的做法。

### 1.4 可观测性

wanwu：OTel 全家桶（grpc/http/kafka/eino/wga 五种插桩，`pkg/trace-util/`）+ Jaeger + zap 多核滚动日志（`pkg/log/log.go:57-73` stdout+info/err 分文件 Tee）。
AOP：`aop_run_step`/`aop_llm_call` 落库追踪 + worker JSON 结构化日志。

**判断**：各有侧重——wanwu 是分布式 tracing，AOP 是业务审计面。AOP 的 run-step 审计其实更适合决策基座场景，但缺 **trace id 贯穿**（前端→Java→worker→LLM 一条链）。建议补一个轻量 trace-id 中间件 + worker 透传，成本低收益高。

---

## 2. 功能对比

### 2.1 Agent 编排

| 能力 | wanwu | AOP |
|---|---|---|
| 引擎 | 三套并存：① eino adk ReAct + supervisor 多智能体（`internal/agent-service/service/{single,multi}_agent.go`）② WGA 声明式 yaml 智能体工厂（react/sandbox/sequential/loop/parallel/supervisor 六型，`pkg/wga/internal/factory/agent.go:20-36`）③ WGA Sandbox 容器化通用智能体（跑 opencode runner，reuse/oneshot 两模式） | `sys.orchestrator` 通用调度器（目标理解→计划→子代理→汇总，轨迹落库）+ BUILTIN/PROMPT 调度器 |
| 会话持久化 | thread/run 目录 + versioned 模式可回滚（`pkg/wga-persistent/persistent.go`，读写锁并发安全） | run-store memory/db 可切，`engine.resume` 闭环 |
| 容错 | `tools.UnknownToolsHandler`：模型误调不存在工具时返回可恢复文本不中断 ReAct（`pkg/wga/internal/factory/agent.go:53-62`） | 门禁仅 HUMAN_CONFIRM；无计划重跑/取消/中断（todo.md） |

**可借鉴（高价值）**：
1. **UnknownToolsHandler 兜底**——AOP 子代理循环里 LLM 误调工具名时同样会中断；抄这个"返回可恢复文本"策略成本极低。
2. **多智能体 supervisor 模式**——AOP 规划文档（通用调度器-子代理 v0.4）已走类似路线，可对照 wanwu `multi_agent.go` 的 `supervisor.Config{Supervisor, SubAgents}` 结构校验自己的子代理路由设计。
3. **会话 versioned 快照**——AOP 工作台聊天会话目前刷新即清空（前端短板），wanwu thread/run 版本化目录是现成参考。

### 2.2 工具体系与沙箱

| 能力 | wanwu | AOP |
|---|---|---|
| 工具类型 | MCP（双栈 go-mcp + mark3labs）、Skill 元数据广场、OpenAPI/MCP→SKILL.md 转换器 | CSV skills、workspace 文件工具、自定义 Python 工具、data.http、qkdev 组织工具 |
| 沙箱 | **独立容器**（wga-sandbox，Dockerfile.wga-sandbox，bash 超时 600s，reuse 常驻/oneshot 一次性） | **无网络/OS 级沙箱**，Python 以应用进程用户运行（todo.md P0 明示） |
| 审计 | 有限 | `TracingToolInvoker` 全量落 `aop_run_step` |

**这是 AOP 最大的架构级缺口**：wanwu 把"通用智能体"放进一次性容器跑（oneshot 模式正好匹配 AOP 的 script.run 场景），而 AOP 的自定义 Python 工具直接在单体进程内执行。建议：
- **P0**：AOP `script.run`/自定义 Python 工具至少加进程级隔离（子进程 + seccomp/job object 或 Windows AppContainer），中期看齐 wanwu 的容器 oneshot 模式；
- 抄 wanwu 的 **`SafeGo`（`pkg/safe-go-util/safe_go_util.go:11-17`）+ `SageGoWaitGroup`** 思想到 Java 侧即"所有子代理循环包在受控线程池 + panic→error 转译"，防止一个子代理炸掉整个单体。

### 2.3 模型接入

wanwu：model-service 元数据 + `pkg/model-provider` 12 家厂商适配（openai-compatible/元景/火山/ollama/qwen/千帆/deepseek/jina/智谱/腾讯…），模型类型覆盖 llm/embedding/rerank/OCR/GUI/text2image/ASR 等 10 类。
AOP：**仅 DeepSeek** 单网关（`eai-model-deepseek`，无 key 显式失败不伪造——这个"不伪造"纪律是优点）。

**可借鉴（高价值）**：
1. **openai-compatible 适配层一个就够起步**——AOP 总体规划里的"模型目录/多厂商路由"未落地，wanwu 的 `mp-openai-compatible` 包证明大部分国产/开源模型都能走兼容协议，AOP 可先做单适配器 + 元数据表（provider/baseUrl/model 名/用途），不必做 12 个。
2. **模型类型枚举纳入目录设计**（embedding/rerank/OCR 分开管）——AOP 的 OCR SPI（ADR-006）与嵌入已各自为政，统一编目后 worker 的 DeepSeek/GLM OCR、BGE-M3 嵌入可纳入同一路由。

### 2.4 知识库/RAG

wanwu：知识库拆 9 个 proto 子域（doc/keywords/permission/qa/report/splitter/tag/template），Kafka 异步管线，ES dense+sparse 混合检索 + knn，GraphRAG/本体，兼容 Dify `/retrieval` 外部知识库（`internal/bff-service/service/knowledge_dify.go`）。
AOP：worker 解析（bbox 逐句引用）→ OCR SPI → BGE-M3 嵌入 → BM25+向量 RRF → 图谱一跳证据；ACL 三层（分类树/标签/文档）。

**判断**：AOP 的知识质量工程（bbox 逐句引用、OCR SPI、RRF）**细节上不输甚至更精细**；差距在：
1. **异步管线**（前面已提）；
2. **混合检索的 dense+sparse 双路**——wanwu ES 同时存稠密向量和稀疏权重，AOP 目前 BM25(Java 内存) + 向量两路，思路等价，暂无需动；
3. **评测缺位是共同短板**（wanwu 也无固定问题集回归），AOP todo.md P1.6 已列——这个只能自建，无处可抄。

### 2.5 安全（AOP 最痛处，wanwu 最亮点）

AOP 现状：`/api/v1/**` permitAll、JWT 本地默认密钥、工具无沙箱、数据服务无鉴权（todo.md P0 全列）。
wanwu 可直接抄的四个实现（均为小而精的文件）：

| 借鉴点 | wanwu 位置 | 说明 | 优先级 |
|---|---|---|---|
| **服务间身份 HMAC 防伪造** | `pkg/svc-auth/svc_auth.go`（全文 50 行） | HMAC-SHA256(uid+org+ts)，±300s 防重放，`hmac.Equal` 常量时间比较。AOP 的 Java→worker Bearer token 可升级为同款签名+时间窗，防 token 泄露后被无限重放 | **P0** |
| **SSRF 三层防护** | `pkg/url-util/validate_url.go:31-127` + `dial.go` | 字面量黑名单（含 CGNAT/TEST-NET/NAT64 冷门网段）→ DNS 解析 → DialContext 复核防 rebinding；`ValidatePublicURL` 拒私网+.internal+.svc+关键字。AOP 的 `data.http` 工具与未来 MCP/外部 API 接入**必须**有这个 | **P0** |
| **登录限流中间件** | `internal/bff-service/server/http/middleware/rate_limiter.go`（366 行） | 按 body JSON 嵌套路径取 key、只对指定错误码计数、成功清零、responseBufferWriter 命中限流时改写响应。AOP 登录/问答接口可直接移植语义 | P1 |
| **流式敏感词过滤** | `internal/bff-service/service/sensitive_check.go` | SSE 逐帧 Aho-Corasick + 滑动窗 20/缓存 3 帧处理跨帧词；`onForward` 回调保证"落历史=用户所见"。AOP 的 Chat SSE 若要上生产也需同款 | P1（按合规需求定） |

### 2.6 通信/流式

wanwu：`SSEWriter[Res]` 泛型同时适配 Gin HTTP SSE 与 gRPC server stream（`pkg/sse-util/sse_client.go:87`），一套逻辑两用；AG-UI 协议封装。
AOP：`SseEmitter` 推 start/step/citation/done/error，"step 是真实产生的，不伪造 token 流"。

**判断**：AOP 的 SSE 语义（真实步骤而非伪 token 流）与其决策基座定位相符，**不必**抄 wanwu 的逐 token 流。但 AOP 的 180s STREAM_TIMEOUT 对长编排可能不够——wanwu 用 `sse-connector` 会话管理器解耦连接断开与运行继续，值得在"断线重连/任务后台继续"设计里参考。

### 2.7 渠道/IM（wanwu 独有）

channel-service：钉钉/微信适配器、扫码登录、WGA 输出渲染成 IM 消息（`internal/channel-service/chat/wga_im_render`）。AOP 无此需求，暂不借鉴；若将来决策基座要推待办到 IM，这是现成参照。

---

## 3. 工程细节对比

| 维度 | wanwu | AOP | 结论 |
|---|---|---|---|
| 测试 | 1 个 Go test + 2 个 Python test；靠 compose 起全栈手工验 | ~252 Java + 108 pytest + CDP 浏览器 E2E | **AOP 显著领先**，保持 |
| 迁移 | AutoMigrate（无治理） | Flyway V1–V26 + 表名三段规则 | **AOP 显著领先**，保持 |
| 错误处理 | proto 错误码枚举 + i18n key 贯穿 + grpc-util 映射 | 统一错误码 + "不伪造"纪律 + 4 个域 ExceptionHandler | 平手；wanwu 的 **i18n 错误消息**（xlsx→jsonl）是 AOP 多语言时的小参考 |
| 构建 | Makefile×服务×架构 + LDFLAGS 五元组（buildTime/Version/gitCommit/builder）+ swag 文档 | mvn/npm/PowerShell | AOP 可抄 **build 信息注入**（jar manifest 打 commit id/时间，排障对版本） |
| 配置 | yaml + 敏感字段留空部署注入 | 环境变量占位符 + 单 application.yml 高注释密度 | 平手，AOP 形态更佳 |
| CI | 无 | 无（有 E2E 文档化回归） | 双方都缺，AOP 应先补 |
| graceful shutdown | 三种模式（bff 手工链 + 10s flush trace；LoaderService 反序；sandbox Manager RWMutex） | Spring 标准生命周期 | AOP 够用；worker 侧需补统一启停（todo.md 已列） |

---

## 4. 可借鉴清单（按优先级）

### P0 —— 安全生产化（AOP todo.md P0 与 wanwu 亮点直接对齐）

1. **Java→worker 鉴权升级为 HMAC 签名 + 时间窗防重放**：移植 `pkg/svc-auth/svc_auth.go` 语义（HMAC-SHA256(uid+ts)，±300s 窗口，常量时间比较）。当前静态 Bearer token 一旦泄露无任何时效/来源约束。
2. **`data.http` 工具加 SSRF 防护**：移植 `pkg/url-util/validate_url.go` 三层模式（字面量→DNS→Dial 复核；拒私网/环回/内部域后缀）。AOP 的 HTTP 取数工具目前可打内网任意地址。
3. **`script.run`/自定义 Python 工具进程级隔离**：短期子进程 + 资源限额 + 环境变量白名单收紧（白名单已有，执行身份未隔离）；中期对齐 wanwu oneshot 容器模式（Dockerfile.wga-sandbox 可作参照）。
4. **`/api/v1/**` 收口 permitAll**（非借鉴项，双方对比后更凸显 AOP 此项与"决策基座"定位的反差最大；wanwu 有 13 个分层认证中间件 + HMAC 联防可作目标形态）。

### P1 —— 流式与编排健壮性

5. **UnknownToolsHandler 兜底**：子代理循环中模型误调工具返回可恢复文本，不中断 ReAct（`pkg/wga/internal/factory/agent.go:53-62`，一小时内可移植的语义）。
6. **SSE 断线与任务续命**：参考 `pkg/sse-util/sse-connector` 会话管理，把 180s STREAM_TIMEOUT 与任务执行解耦（连接断开任务继续，重连可续读）。
7. **限流中间件**：移植 `rate_limiter.go` 的"JSON 嵌套路径取 key + 指定错误码计数 + 成功清零"语义到登录/问答面。
8. **多 system 消息合并策略**（`agent.go:74-96` SystemMessageStrategyMerge）：AOP 子代理注入技能提示词时同样会踩多 system 冲突。

### P1 —— 基础设施

9. **文档处理改异步管线**：Kafka 对 AOP 是重的，但"job 入队 → worker 消费 → 状态回写"模式比现在的轮询/手工触发更稳，可用 PG 表 + LISTEN/NOTIFY 或简单线程池起步。
10. **pgvector 真正落地**：参考 wanwu 按向量维度动态分列（`es_rag_server.py:201-203`）评估"按模型维度分列/分索引"方案，修掉 512 维 JSON fallback。
11. **Redis 分域客户端**：将来加缓存/限流时按业务域隔离实例（OP/Sys/…模式）。

### P2 —— 锦上添花

12. **trace-id 贯穿前端→Java→worker→LLM 调用链**（wanwu OTel 五插桩的轻量子集）。
13. **build 信息注入 jar manifest**（wanwu Makefile LDFLAGS 五元组模式）。
14. **登录限流 + 敏感词过滤**（若合规需求出现再上；wanwu 的流式 AC 自动机 + LRU 字典热更新是现成设计）。
15. **模型目录起步**：先做 openai-compatible 单适配器 + 元数据表（provider/baseUrl/类型），把 OCR SPI 与嵌入统一编目。
16. **工作台聊天会话落库**（wanwu thread/run versioned 快照模式参考）。

### 不建议照搬

- **微服务拆分**（11 服务 gRPC 网状）：与 AOP 单体 POC 阶段不匹配，运维成本远超收益；
- **GORM AutoMigrate**：低于 AOP 现有 Flyway 水位；
- **逐 token SSE**：与 AOP"真实步骤"语义冲突；
- **Vue2 + qiankun 前端**：AOP Vue3 极简栈更健康；
- **12 家厂商适配包**：AOP 先一个 openai-compatible 即可覆盖 80%。

---

## 5. 双方互鉴总结

- **AOP 学 wanwu**：安全防护四件套（HMAC/SSRF/限流/敏感词）、工具沙箱容器化、模型目录广度、异步管线、流式容错细节。这些恰好覆盖 AOP todo.md 的全部 P0 短板——wanwu 是一份现成的"反面经验正面答案"。
- **wanwu 学 AOP**（仅作对照，不行动）：测试纪律（252 vs 1）、Flyway 迁移治理、统一错误码"不伪造"原则、审计面完整性。
- **共同盲区**：知识质量评测（固定问题集/引用准确率回归）双方都没有——AOP 只能自建，这反而是做出差异化的机会。

---

*报告基于两个只读探索代理对两仓库的全量扫描（wanwu：1150 Go 文件 + Python + Vue 前端；AOP：260 Java 主源文件 + worker + frontend），关键论断均附文件路径可复核。*

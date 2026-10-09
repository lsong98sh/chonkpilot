# 63 · DSL语法（ChonkPilot DSL 核心规范）

> 状态：✅ **已定稿并落地**（`src/lib/dsl` 独立包；四个工具 llm_run / filesys_run / desktop_run / browser_run 均已接入同一引擎）
> 关联：[24-lib](../20-modules/24-lib.md)（`dsl` 包）· [72-工具开发规范](../70-conventions/72-工具开发规范.md) · [27-mcp-tools](../20-modules/27-mcp-tools.md) · [3A-工具与编排](../30-function-points/3A-工具与编排.md)
> 版本：v2026-09-10
> 适用范围：llm_run（批处理编排/委派）、filesys_run（文件操作）、desktop_run（桌面交互）、browser_run（浏览器自动化）
> 共同目标：统一的词法、类型、流控与输出语义，各 DSL 共享同一套核心规范

---

## 1. 词法

### 1.1 行结构

- 每行一个指令，指令名大写（解析器大小写不敏感）
- 前导空白忽略
- 注释：`#` 或 `###` 开头直到行尾
- 空行忽略

### 1.2 标识符

变量名/表名：`[a-zA-Z_$][a-zA-Z0-9_$]*`，可含中文。

**保留字表**：动词保留字 `SET` `IF` `LOOP` `PARALLEL` `BREAK` `CONTINUE` `EXIT` `END` `TYPEOF` `ENTRY` `SPLIT` `JOIN` `PUSH` —— 不得作为**动作动词**；**保留变量名 `env`**（宿主注入的只读上下文）—— 不得作为**变量名 / 绑定目标 / 被遮蔽**（`SET … => env`、`SET … => env.<字段>`、下标写、动作 `=> env`、`TYPEOF/ENTRY/SPLIT/JOIN/PUSH` 目标、`LOOP env=…` 绑定一律报错「env 是保留字（宿主注入的只读上下文），不能作为变量名」）；`env` 唯一受支持用法 = **读取 `{{env.*}}`**（见 §6.4）。**保留变量名 `$RETURN`**（宿主注入的**只写**结果通道）—— 同样不得作为变量名 / 绑定目标 / 被遮蔽；唯一受支持用法 = **`SET 值 => $RETURN` / `动作 … => $RETURN`**（写入即累计追加，见 §4.6）；**读取 `$RETURN`（`{{$RETURN}}` / `SET $RETURN => x`）一律报错**。

### 1.3 字符串

- 双引号包裹：`"文本"`，支持 `\"` 转义
- 插值：`{{变量名[.字段][:N]}}`，`N` 为截断字符数
- 多行文本：行末 `<<<` 开始，行首 `>>>` 结束

```text
LLM "<成员名>" "请分析以下内容：<<<
这是一段多行文本
可以跨多行
>>> 然后继续" "分析多行内容" => #"out.md"
```

### 1.4 句柄表达式

| 符号 | 含义 | 示例 |
|------|------|------|
| `@"路径"` | 数据库句柄（bbolt） | `@"state.db"` |
| `#"路径"` | 文件句柄 | `#"out.md"` |

**句柄表达式的值永远是句柄，不是内容。**

> **路径约束（按消费工具区分）**：`#"path"` 的**文件/目录引用**在 `filesys_run` / `browser_run` / `desktop_run` 中必须为**绝对路径或以 `~/` 开头的用户目录路径**（R-11，相对路径整体失败，见 [16-路径解析规范](../10-architecture/16-路径解析规范.md) §8）——**包括数据源读取**（如 `LOOP row=#"f.csv".lines`、`#"f".content/.array/.object/.range`、`IF exist #"…"`）与**落盘/目录操作**（`SHT`/`DOM`/`DBG`/`UPF`、`WIN … SHT`、行尾 `=> #"file"` 目标）；`web_fetch` 的 `save_as`/`form_files[].path` 同理。字面路径在这三个工具**执行前**预校验（`dsl.CollectHandleRefs`），`{{}}` 插值路径执行时兜底校验。**`llm_run` 亦适用此约束**（〔订正（2026-10-08）〕：原记「`llm_run`（及 `@"…"` 数据库句柄）不适用——引擎层不为 llm_run 注入校验器、也不做预校验，其现有行为不变」**已不成立**，`llm_run` 的**引擎句柄层现已统一校验**：其 `#"path"` 文件/目录引用（数据源读取与落盘/追加）**同样须为绝对 / `~/` / `!/` 开头**，**字面路径在执行前预校验**（`src/lib/llm/server/jobdsl.go` 的 `dsl.CollectHandleRefs` 循环）、`{{}}` 插值路径**执行时由 `jobFile.abs` 严格解析兜底**，且**句柄层统一走 `agentbox.Check`**（读/写沙箱校验，修复原裸写缺口，决策 [42 §2 (250)](../40-roadmap/42-决策记录.md)）；**仅 `@"…"` 数据库句柄仍不适用**此约束〔`llm_run` 引擎未提供 store 面〕）。本文件示例中的短路径（如 `#"out.md"`）仅为语法示意。

---

### 1.5 统一编排方言（`dsl_run`）

`dsl_run` 是**统一编排**工具，可在**同一段脚本**里混合「文件 / 浏览器 / 桌面 / LLM」四类动作。因四域既有动词存在**重名**（如 `CLK`/`DBL`/`SHT`/`SLP`/`MOV`），统一方言对动作动词施加**域前缀**以消歧：

| 域 | 前缀 | 示例 | 说明 |
|---|---|---|---|
| 文件 | `FILE_` | `FILE_RPL` `FILE_APD` `FILE_MOV` `FILE_CPY` … | 对应 `filesys_run` 的 `RPL/APD/PTC/INS/DEL/MOV/CPY` |
| 浏览器 | `WEB_` | `WEB_OPN` `WEB_CLK` `WEB_SHT` `WEB_FILL` … | 对应 `browser_run` 动作集（含 fetch） |
| 桌面 | `PC_` | `PC_WIN` `PC_CLK` `PC_SHT` `PC_KPR` … | 对应 `desktop_run` 动作集 |
| LLM | 无（保持 `LLM`） | `LLM "<agent>" "<prompt>" "<目的>"` | 无冲突，保持原动词 |

- **容器工具仍用裸动词方言**：`filesys_run` / `browser_run` / `desktop_run` 各自的脚本语法**不变**（`RPL`、`CLK`…）；域前缀**仅**用于 `dsl_run` 统一编排。
- 核心语句（`SET/IF/LOOP/PARALLEL/...`）与 `$RETURN`（§4.6）在两种方言下**完全一致**。
- 子会话：`dsl_run` 中每次 LLM 调用 = 一个**子会话**（`parent_id` 非空，不入会话列表，仅在作业步骤表格可见）。
- **`LLM` 返回值口径**：只含该子轮次的**最终回答正文**——**不含 reasoning（思维链）与 tool 相关内容**（工具调用轮的中间正文、工具结果一律剔除）；该正文即以 `=> 目标` / `=> $RETURN` 写入的内容（`llm_run` 与 `dsl_run` 两路一致）。

### 1.6 统一编排的动作执行位置

`dsl_run` 的文件/浏览器/桌面动作**全部在受保护子进程**（`chonkpilot-dsl-executor.exe`）内执行 —— 进程级 agentbox 沙箱（`CHONKPILOT_SANDBOX`）天然生效；LLM 步骤由该进程经 stdio 协议回报 gateway，再由 gateway 经 MQ 执行子轮次（见 [42 §2 (247)](../40-roadmap/42-决策记录.md)）。

执行器与 gateway 之间为**自建 stdio 行协议**（非 MCP；`run`/`llm_result` 下行、`llm_call`/`result`/`tree`/`step`（+`log`）上行，见 [42 §2 (254)](../40-roadmap/42-决策记录.md)）；其中下行 `run` 消息在 `script`（脚本正文）之外含**可选字段 `file`**（脚本文件绝对路径）——**`script` 为空时由受沙箱执行器读该文件**（gateway 不再代为读盘），协议**向后兼容**（无 `file` 即走 `script`，行为等价）。字段明细见 `src/lib/gateway/gateway/dslrun.go` 头注。

## 2. 数据类型

### 2.1 基本类型

| 类型 | 写法 | 说明 |
|------|------|------|
| `string` | `"文本"` | 双引号字符串 |
| `number` | `42`, `3.14` | 浮点数（解析时自动识别） |
| `bool` | `true`, `false` | 布尔值 |
| `null` | `null` | 空值 |
| `list` | JSON 数组 | 来自 LLM 输出、文件或数据库表 |
| `object` | JSON 对象 | 来自 LLM 输出、文件或数据库条目 |

### 2.2 句柄类型

| 类型 | 句柄 | 说明 |
|------|------|------|
| `store` | `@"state.db"` | bbolt 数据库句柄 |
| `file` | `#"out.md"` | 文本文件句柄 |

句柄在插值 `{{}}` 中不展开内容，只产生结构性描述字符串：

```text
"<db: state.db, tables: [logs, tasks, config]>"
"<table: state.db.logs, records: 128>"
"<file: out.md, size: 2.3MB, lines: 50000>"
```

---

## 3. 句柄操作

### 3.1 文件句柄（`#`）

| 访问 | 读写 | 返回 |
|------|------|------|
| `fh.path` | 读 | 文件路径字符串 |
| `fh.size` | 读 | 文件大小（bytes） |
| `fh.count` | 读 | 文件行数 |
| `fh.blocks` | 读 | 文件段落数 |
| `fh.content` | 读 | 全文内容（字符串） |
| `fh.lines` | 读 | 按行解析 → 字符串数组 |
| `fh.array` | 读 | 解析为 JSON 数组（非数组时报错） |
| `fh.object` | 读 | 解析为 JSON 对象（非对象时报错） |
| `fh.range(N,M)` | 读/写 | 读：返回第 N-M 行；写：替换指定范围行 |
| `fh.eof` | 写 | 追加写入（读返回空字符串） |
| `fh` | 写/读 | 写：覆盖写入；读：返回句柄

**说明**：`fh.lines`、`fh.array`、`fh.object`、`fh.content` 是只读访问器，不可作为 SET 写入目标（`SET text => fh.content` 报错）。写入必须用裸句柄 `fh`、`fh.eof` 或 `fh.range`。

### 3.2 数据库句柄（`@`）

| 访问 | 读写 | 返回 |
|------|------|------|
| `db.path` | 读 | 数据库文件路径 |
| `db.size` | 读 | 数据库文件大小 |
| `db.tables` | 读 | 表名列表 `["logs","tasks","config"]`（用于迭代） |
| `db.tables.logs` | 读/写 | 读：表句柄；写：覆盖写表 |
| `db.tables.logs.rows` | 读 | 表行数（只读） |
| `db.tables.logs.content` | 读 | **智能**：小→JSON数组，大→描述+预览 |
| `db.tables.logs.range(N,M)` | 读/写 | 读：返回第 N-M 条；写：替换指定范围记录 |
| `db.tables.logs.eof` | 写 | 追加记录（读返回空） |
| `db.tables.logs.query("expr")` | 读 | 简单查询过滤 |

**系统属性与表名自然分离**：`path`、`size`、`tables` 是保留的系统属性，所有表名通过 `db.tables.<表名>` 访问，杜绝命名冲突。

**注意**：`.content`、`.rows`、`.query()` 是只读访问器，不可作为 SET 写入目标。写入必须用裸表句柄 `db.tables.logs`、`db.tables.logs.eof` 或 `db.tables.logs.range`。

**内容智能规则**（同文件）：

```text
JSON 数组 < 50 条 且 < 100KB → 返回完整数组
否则 → 返回 "[table: db.logs, 10000 rows, preview: 前3条...]"
```

**查询语法**（简单）：

```text
db.tables.logs.query("level=error")              # 字段相等匹配
db.tables.logs.query("time>2026-09-01")          # 字段比较
db.tables.logs.query("level=error&time>2026-09-01")  # 多条件 AND
```

实现：bbolt 遍历 + 简单字段过滤，不引入 SQL 引擎。

### 3.3 值访问器（按类型分派）

访问器 `.成员` 一律**按值的类型分派**：同名成员可跨类型复用同一语义（如 `.array` 既作用于文件句柄，也作用于字符串值）。这是扩展访问器的唯一机制——**新增成员 = 给某个类型增加一个方法/属性**，不新增保留字。

| 类型 | 成员 | 说明 |
|------|------|------|
| `file`（`#"path"`） | 见 §3.1（`path`/`size`/`count`/`blocks`/`content`/`lines`/`array`/`object`/`range(N,M)`/`eof`） | — |
| `store` / `table`（`@"db"` / `db.tables.x`） | 见 §3.2（`path`/`size`/`tables`/`rows`/`content`/`range(N,M)`/`eof`/`query("expr")`） | — |
| `string` | `array` / `object` | **本次新增（只读）**；`lines`/`count` 等后续按需 |
| 其他（`list` / `object` / `number` / `bool` / `null`） | 未定义成员 → 报错 | 后续按需增加 |

**string 成员（把字符串内容按 JSON 解析）**：

```text
SET raw.array  => tasks     ### 解析为 JSON 数组（非数组 → 行错误）
SET raw.object => cfg       ### 解析为 JSON 对象（非对象 → 行错误）
```

- 与文件句柄**同名同义**：差别只是数据来源（文件内容 vs 字符串值）；`#"f".array` ≡ 读文件后解析，`raw.array` ≡ 直接解析字符串
- **解析失败 = 行错误**（报错，不产生变量），**绝不退化为空数组/空对象**——否则后续 `IF tasks` 会静默为 false，被误判为"无事可做"而提前结束
- 错误信息附原文前 N 字符，便于定位提示词问题
- 主要用途：承接 LLM 输出的 JSON 文本 → 结构化变量，供 `IF`（非空判定）与 `LOOP`（动态数组迭代）使用
- 典型（LLM 输出 → 判定 → 迭代，无需落盘）：

```text
LLM "<成员名>" "…只输出 JSON 数组 [{agent,prompt}]；无事可做输出 []" "规划下一步" => raw
SET raw.array => tasks
IF not tasks
   BREAK
END
LOOP t=tasks
   LLM "{{t.agent}}" "{{t.prompt}}" "执行分工步骤"
END
```

**实现与约束**：

- 属性 / 方法沿用现有 `Seg.Call` 区分：无参为属性（`size`/`count`），带参为方法（`range(N,M)`/`query("x")`）
- 新增的只读成员须同步 `isReadOnlyTarget` 白名单（不可作为 `SET` 目标）
- **命名原则**：新增成员仅在"该类型下不存在同名字段"时允许（避免与 JSON 字段撞名；这也是 §13.4 拒绝 `.keys()`/`.entries()` 的原因）
- **不引入类型自动推断**（不加 `.json`）：由访问器显式声明解析目标类型

---

## 4. 赋值与输出（SET）

### 4.1 统一语法

```text
SET 值 => 目标
```

`SET` 是唯一的值传递指令。`=`、`>`、`>>` 不再做赋值。

### 4.2 值（左边）

```text
SET 42 => n                     # 数字
SET "hello" => s                # 字符串
SET true => b                   # 布尔
SET null => x                   # 空
SET @"state.db" => db           # 数据库句柄
SET #"out.md" => fh             # 文件句柄
SET db.tables.logs => tb        # 表句柄（tables.后跟表名）
SET db.tables.logs.content => data     # 读表内容（智能）
SET db.tables.logs.range(0,9) => r     # 读表范围
SET fh.range(1,10) => r         # 读文件范围
SET fh.content => text          # 读文件内容（智能）
SET db.tables.logs.query("level=error") => errors  # 查询结果
```

### 4.3 目标（右边）

| 目标 | 含义 |
|------|------|
| `变量` | 赋值到变量 |
| `fh` | 覆盖写入文件 |
| `fh.eof` | 追加写入文件 |
| `fh.range(N,M)` | 替换文件指定范围行 |
| `db.tables.logs` | 覆盖写入表 |
| `db.tables.logs.eof` | 追加记录到表 |
| `db.tables.logs.range(N,M)` | 替换表指定范围记录 |
| `$RETURN` | **结果通道**：累计追加（非覆盖；超 64K 自动转文件，见 §4.6） |
| `.content` / `.lines` / `.array` / `.object` / `.rows` / `.query()` | **不可作为 SET 目标**（只读） |

### 4.4 类型转换

SET 自动做类型序列化：

```text
SET 42 => fh              # 写 "42" 到文件（数字→字符串）
SET ["a","b"] => fh       # 写 JSON 数组到文件
SET "hello" => fh.eof     # 追加 "hello" 到文件末尾
```

### 4.5 示例

```text
SET @"state.db" => db
SET #"out.md" => fh

# 写
LLM "<成员名>" "写分析报告" "写分析报告" => fh           # 覆盖写文件
LLM "<成员名>" "追加日志" "追加运行日志" => fh.eof        # 追加到文件
LLM "<成员名>" "处理数据" "处理数据入表" => db.tables.logs       # 覆盖写表
LLM "<成员名>" "记录一条" "记录一条日志" => db.tables.logs.eof   # 追加记录到表

# 读
SET db.tables.logs.content => allLogs          # 智能读表
SET db.tables.logs.range(0,9) => recent        # 读前10条
SET fh.range(1,5) => header             # 读文件头5行
```

### 4.6 `$RETURN` 结果通道

`$RETURN` 是宿主注入的**只写保留变量**，用于把脚本的结构化产出回填给宿主（取代「宿主另设约定汇总」）：`SET 值 => $RETURN` 与 `动作 … => $RETURN` 两条写入路径**累计追加**（非覆盖），段间以换行分隔。

```text
SET "分析完成" => $RETURN
LLM "<成员名>" "要点：{{item.title}}" "提炼要点" => $RETURN
```

- **两态**：累计 ≤ **64K**（65536 字节）返回**内容**（inline）；超过即转**文件流式追加**（落 `!/` 临时根、`dsl-return-<作业id>.md`）并返回**文件名 + 大小**（file）。阈值先固定常量。
- **序列化**：与 SET 一致（字符串直拼、数字/布尔→文本、数组/对象→JSON 文本）。
- **只写不可读**：脚本内读取（`{{$RETURN}}`、`SET $RETURN => x`）一律报错，避免「读 + 累计」语义自相缠绕。
- **与宿主汇总的关系**：脚本中出现过 `=> $RETURN` → 宿主以 `$RETURN` 为准；未出现 → 回落宿主既有汇总逻辑（行为不变）。

---

## 5. 流控

### 5.1 IF 条件

```text
IF <条件>
   ...
END
```

无条件块（无 ELSE）。条件支持：

**比较**：`<路径值> <运算符> <值>`，运算符支持 `==`、`!=`、`>`、`>=`、`<`、`<=`。

```text
IF item.done == true
IF item.count != 0
IF item.count > 0
IF item.count >= 5
IF item.count < 10
IF item.count <= 100
IF item.status == "ok"
IF item.name == {{变量}}           # 插值路径
```

**exist**：

```text
IF exist "文件路径"                 # 文件存在
IF exist #"out.md"                 # 文件句柄指向的文件存在
IF exist @"state.db"               # 数据库文件存在
IF exist db.tables.logs                   # 数据库表存在且非空
IF not exist "x.json"              # 文件不存在
IF not exist #"out.md"             # 文件不存在
```

**逻辑组合**（带优先级 + 括号）：

```text
IF not item.done == true           # 取反
IF item.done == false and item.count > 0
IF item.done == true or item.status == "error"
IF (item.done == true or item.status == "ok") and item.count > 0
```

**优先级**：`not` > `and` > `or`，括号可覆盖。

**缺失/null 视为 false**。

### 5.2 LOOP

```text
LOOP <变量名>=<数据源> [concurrency=<N>]     # 有参：按数据源迭代（for-each）
LOOP                                          # 无参：一直循环（while），靠 BREAK / EXIT 退出
   ...
END
```

**变量名**：`item` 是常用名，但可以任意命名（如 `row`、`task`、`user`）。内层用不同名字就不会与外层冲突。

**无参 LOOP（一直循环）**：无数据源即无界循环，用于"干一步 → 判定 → 再干一步，直到无事可做"的编排（如 driver 选下一步 agent）。护栏：

| 护栏 | 规则 |
|------|------|
| 退出方式 | 只能靠 `BREAK`（本层）/ `EXIT`（整脚本）；`CONTINUE` 进入下一次迭代 |
| 迭代上限 | 可配安全上限（**默认 50**，经 `Options.MaxLoopIterations`，`<=0` 取缺省）；到达上限 → **记错并终止本层循环**（`无参 LOOP 超过迭代上限 N，已终止（循环未自然退出）`），不得静默停止 |
| 失败语义 | 单步失败 → **终止本层循环**（步骤错误已记入 `Result.Errors`，另补一条 `无参 LOOP 第 N 次迭代内有步骤失败，终止本层循环`），不停留在失败态空转；**是否终止整个脚本**由 `StopOnError` 决定（`llm_run` = `false` → 循环之后的顶层语句照常执行，见 §8） |
| concurrency | **禁止**：`LOOP concurrency=N` 在**语法期**报错「无参 LOOP 不支持 concurrency」（否则会被误读为「变量名 `concurrency` = 数据源」） |
| 空数据源（有参） | 数据源为空 = **零次迭代（跳过）**，不是退出 |

```text
SET "a" => state
LOOP
   LLM "<成员名>" "{{state}}" "推进状态机" => out
   IF state == "b"
      BREAK
   END
   SET "b" => state
END
```

> 状态：**✅ 已实施**。解析器 `parseLoop` 允许省略 `变量名=数据源`；引擎 `execLoopUnbounded` 实现无界循环 + 上限 + 失败终止；上限经 `Options.MaxLoopIterations` 配置（缺省 50）。用例见 `dsl_test.go` 的 `TestUnboundedLoop*`。

**典型（driver 循环，直到没有可做之事）**：

```text
SET "" => output
SET "" => done
LOOP
   LLM "<成员名>" "已做：{{done}}；产出：{{output}}。选出下一步（只输出 JSON 数组 [{agent,prompt}]）；无事可做输出 []" "选择下一步" => raw
   SET raw.array => tasks
   IF not tasks
      BREAK
   END
   LOOP t=tasks
      LLM "{{t.agent}}" "{{t.prompt}}" "执行分工步骤" => output
      SET "{{done}} | {{t.agent}}" => done
   END
END
```

> 跨轮变量（如 `done`/`output`）**必须在循环外先 `SET` 声明**：循环体每轮是独立作用域，循环内首次赋值只落当轮，下一轮读不到（见 §6.1）。

**数据源**（执行时求值）：

| 写法 | 数据源类型 | 迭代单元 |
|------|-----------|---------|
| `#"file.txt".lines` | 文件按行解析 | 每行一个字符串 |
| `#"data.json".array` | 文件解析为 JSON 数组 | 数组元素 |
| `#"data.json".object` | 文件解析为 JSON 对象 | 不迭代（单对象） |
| `db.tables.logs` | 数据库表句柄 | 表记录（对象） |
| `db.tables` | 数据库表名列表 | 表名（字符串） |
| `myList` | 列表变量（前面 => 捕获的变量） | 列表元素 |

**下标范围**：在数据源后追加 `.range(N,M)` 控制迭代范围，与句柄的 `range` 访问器一致（N 含，M 含，负数 = 倒数）。越界报错。

> **路径（R-11）**：在 `filesys_run` / `browser_run` / `desktop_run` 中，文件数据源（`#"文件"`）须为**绝对路径或 `~/` 开头**（相对路径整体失败，见 [16 §8](../10-architecture/16-路径解析规范.md)）；`llm_run` **同此约束**（〔订正（2026-10-08）〕：原记「`llm_run` 不受约束」已不成立，见 §1.4）。

```text
# CSV 跳过表头行（从第 1 行到末尾）
LOOP row=#"data.csv".lines.range(1,-1)
   LLM "<成员名>" "解析行：{{row}}" "解析数据行"

# 只处理前 10 条
LOOP task=#"tasks.json".array.range(0,9)
   LLM "<成员名>" "处理 {{task.name}}" "处理任务 {{task.name}}"

# 断点续跑：从第 20 项开始
LOOP task=#"tasks.json".array.range(20,-1)
   LLM "<成员名>" "处理 {{task.name}}" "处理任务 {{task.name}}"

# 嵌套（不同变量名，不冲突）
LOOP group=#"groups.json".array
   LOOP task=#"tasks.json".array
      LLM "<成员名>" "组 {{group.name}} 的任务 {{task.name}}" "处理分组任务"
   END
END

# 数据库表 → 记录
LOOP item=db.tables.logs
   LLM "<成员名>" "分析 {{item.message}}" "分析日志记录"     # 记录字段

# 数据库表名列表 → 逐表处理
LOOP item=db.tables
   LLM "<成员名>" "处理表 {{item}}" "处理数据表 {{item}}"           # item 是表名

# 列表变量（前面步骤捕获的列表）
SET db.tables.users.content => users
LOOP user=users
   LLM "<成员名>" "处理用户 {{user.name}}" "处理用户 {{user.name}}"
```

- `concurrency`：并发度，默认 1（串行）；**上限 64**（`MaxLoopConcurrency`，超限在语法期报错 `concurrency 超过上限 64`，防单个输入派生海量 goroutine）
- 可嵌套，深度 ≤ 8

### 5.3 PARALLEL

```text
PARALLEL
   <分支1语句>
   <分支2语句>
   ...
END
```

- 分支间并发执行
- 分支内可嵌套 LOOP/IF/SET/LLM
- 分支捕获互不可见

### 5.4 BREAK / CONTINUE

```text
LOOP item=#"tasks.json"
   IF item.done == true
      CONTINUE                     # 跳过已完成项，继续下一项
   END
   LLM "<成员名>" "处理 {{item.name}}" "处理任务 {{item.name}}"
   IF item.status == "fatal"
      BREAK                        # 遇到致命错误，停止循环
   END
END
```

**作用域**：
- `BREAK`：跳出最近一层 LOOP
- `CONTINUE`：跳到最近一层 LOOP 的下一次迭代
- 在 PARALLEL 分支中使用时，只影响**本分支**内最近的 LOOP，不影响其他分支
- 在 PARALLEL 顶层使用（不在 LOOP 内）→ 错误

### 5.5 EXIT

```text
IF item.status == "fatal"
   EXIT                           # 整个脚本立即终止
END
```

- `EXIT` 立即终止整个脚本执行，无论当前在哪层嵌套
- 在 PARALLEL 中也生效：终止所有分支
- 之后的语句不再执行

---

## 6. 作用域

### 6.1 变量栈

- 顺序块内上一步 `=>` 捕获对本块后续步骤可见
- 内层可读外层
- LOOP 块内捕获仅本轮迭代内有效
- PARALLEL 分支捕获互不可见
- 同名遮蔽（内层可遮蔽外层；**保留字 `env` 例外**：禁止作为绑定名/被遮蔽）

**LOOP 变量名可自定义**：不同层用不同变量名（如外层 `group`、内层 `task`），避免遮蔽问题；**`env` 除外**（保留字，`LOOP env=…` 报错）。

**宿主注入变量（env）**：宿主经 `Options.Vars` 预置到**根作用域**（最外层），脚本只可**读取**；`env` 是保留字（不可作变量名/绑定目标/被遮蔽），见 §1.2、§6.4。

### 6.2 插值来源

`{{expr[:N]}}` 查找顺序（自内向外）：

1. 当前块捕获变量
2. 外层块捕获变量
3. LOOP 的 `item` 字段
4. 根作用域宿主注入变量（如 `env`，见 §6.4）

### 6.3 截断

`{{变量:2000}}` 取前 2000 字符，超长补 `…[截断]`。

### 6.4 宿主注入只读变量 env（R-11 二次升级，方案 A）

**DSL 仍禁止相对路径**（工具/DSL 的文件与目录参数须绝对 / `~/` / `!/` 开头，见 [16-路径解析规范](../10-architecture/16-路径解析规范.md) §8）。为让脚本拼出绝对路径，宿主注入**只读**变量 `env`，LLM 用 **`{{env.*}}` 显式**拼接（**不得再依赖隐式 workDir 兜底**）：

| 变量 | 含义 |
|------|------|
| `CHONKPILOT_WORKDIR` | 项目工作目录（绝对） |
| `CHONKPILOT_DATADIR` | 数据目录（绝对） |
| `CHONKPILOT_TEMPDIR` | 临时目录根（`<系统 temp>/chonkpilot/<instance>/`，`!/` 前缀的落地目录） |
| `CHONKPILOT_EXEDIR` | 执行器/server 可执行文件所在目录（绝对） |
| `CHONKPILOT_PROJECT` | 项目目录（与 `CHONKPILOT_WORKDIR` 同值） |

- **只能 `{{env.<NAME>}}`，无裸名**：根作用域**只**注入 `env` 一个对象；`{{WORKDIR}}`、`{{CHONKPILOT_WORKDIR}}` 等**裸名不存在**（兼容别名 `WORKDIR` 已删除）。
- **只读 + 保留字（R-11 边界，2026-09-12）**：`env` 是**保留标识符**——脚本把它当变量名/绑定目标（`SET … => env`、`SET … => env.<字段>`、动作 `=> env`、下标写、TYPEOF/ENTRY/SPLIT/JOIN/PUSH 目标、`LOOP env=…` 绑定）→ 报错「env 是保留字（宿主注入的只读上下文），不能作为变量名」；**不依赖宿主是否注入 `env`**（未注入时同样报错，杜绝遮蔽）。宿主注入的**其它** `Options.Vars` 键只读（报错「… 是宿主注入的保留变量，只读」）；动作实现经 `Scope.Set` 的内部写不受限。
- **示例**：`RPL #"{{env.CHONKPILOT_WORKDIR}}/src/main.py" "old" "new"`、`INS #"{{env.CHONKPILOT_TEMPDIR}}/tmp.txt" "x"`（等价 `INS #"!/tmp.txt" "x"`）。
- 注入由消费方（mcp-tools 三 executor / llm_run）完成。executor 侧从**子进程环境** `CHONKPILOT_*` 取（宿主 `mcp-server` spawn 时注入；**仅内部 executor 可见，第三方 MCP server 拿不到**）；缺 `CHONKPILOT_INSTANCE` 时**构造 DSL env 即顶层失败**（`ErrNoInstance`），不再回落 `default`。

---

## 7. 指令参考

### 7.1 LLM

```text
LLM "<agent>" "<提示词>" "<目的>" [=> 值 | 文件句柄 | 表句柄.eof...]
```

- **三参必填**：`agent` / `提示词` / `目的` 缺一不可、均须非空。
- 第一参 = **委派 agent 标识**，须**可委派**（`agentDelegable`；**执行到该步时**校验，不可委派 → **该步失败**、不建子任务节点）：
  1. **当前场景内**的 agent 名（即系统提示词「团队成员」段列出的成员；名可带 `<场景id>/` 前缀精确引用）；
  2. 或 **app 级场景**（出厂内容由 **embed** 物化、**可编辑** `scenarios/`）内**唯一**同名的 agent。
  两者皆未命中 → 该步报错「不可委派（app 级场景注册表与当前场景内均无此 agent）」，错误进作业汇总（`【DSL 错误】第 N 行：…`）、**其余步骤照常执行**（`llm_run` 引擎 `StopOnError=false`，见 §8 错误处理）。**无场景（通用模式）**下系统提示词不含团队成员段 → 只能靠第 2 条命中。
  > ⚠️ 本文全部示例以 `"<成员名>"` **占位**（表示"此处填一个可委派的 agent 名"）；**实际可用名以系统提示词「团队成员」段为准**（出厂场景 = 「开发场景」，其成员见 [37-场景](../30-function-points/37-场景.md)）。
- 第二参 = 提示词（委派内容）；与 agent 都用双引号，都可做 `{{}}` 插值
- 第三参 = **目的（purpose，必填且非空）**：本次子 LLM 的**运行目的**，即该步的**展示名**，同时充当 **tasktree 节点 label**（D-15 定义收敛，2026-09-12；OP-11 起改为必填，2026-10-06）；三参均支持 `{{}}` 插值
- 展示名取值：**= 目的**（**软约束 10–20 字**：超长截断为 20 字并加 `…`、不足 10 字仅记录日志，**均不报错**）；**不再回退提示词截断**（旧「省略/空 → 回退提示词截断」行为已废除）
- **参数个数 ≠ 3 → 顶层失败**（严格模式，执行前 `checkLLMArgs` 静态拦截、带行号）：**缺参**（如只写 agent + 提示词）、**任一参数为空串**、参数多于 3 个均报错（错误文案含正确语法示例）
- `=> 目标` 取代 `>` / `>>`：目标可以是变量、句柄、句柄.eof
- 不带 `=>` 时输出文本进入最终汇总

示例：

```text
LLM "<成员名>" "实现 {{item.name}}" "实现 {{item.name}}"    # 三参必填；展示名 = 目的
```

### 7.2 IF

```text
IF <条件>
   ...
END
```

见 §5.1。

### 7.3 SET

```text
SET 值 => 目标
```

见 §4。

### 7.4 LOOP

```text
LOOP <变量名>=<数据源> [concurrency=<N>]     # 有参
LOOP                                          # 无参：一直循环
   ...
END
```

数据源：`#"文件".lines` / `#"文件".array` / `db.tables.表` / `db.tables` / `变量`。可追加 `.range(N,M)` 控制范围。变量名可自定义，不同层用不同名。无参 `LOOP` = 一直循环（靠 `BREAK`/`EXIT` 退出）。见 §5.2。

### 7.5 PARALLEL

```text
PARALLEL
   ...
END
```

见 §5.3。

### 7.6 BREAK

```text
BREAK
```

见 §5.4。

### 7.7 CONTINUE

```text
CONTINUE
```

见 §5.4。

### 7.8 EXIT

```text
EXIT
```

见 §5.5。

---

## 8. 错误处理

- 语法错误：行号定位，格式 `第 N 行: <错误详情>`
- 运行时错误：记录失败步骤，不阻塞其他步骤
- EXIT：立即终止，不报错
- 句柄.eof 读返回空字符串，不报错
- `.range` 越界：报错
- 缺失字段/null 在 IF 中视为 false

---

## 9. 完整示例

### 9.1 断点续跑

```text
LOOP item=#"tasks.json".array concurrency=3
   IF item.done != true
      LLM "<成员名>" "实现 {{item.name}}" "实现 {{item.name}}" => #"out/{{item.name}}.py"
      SET item.done => true
   END
END
```

### 9.2 数据库操作

```text
SET @"state.db" => db
SET db.tables.logs.content => logs
LLM "<成员名>" "分析日志：{{logs}}" "分析日志" => db.tables.logs.eof
```

### 9.3 文件操作

```text
SET #"out.md" => fh
SET fh.content => content
LLM "<成员名>" "分析文件头：{{content}}" "分析文件头" => fh.eof
```

### 9.4 多分支并行

```text
PARALLEL
   LLM "<成员名>" "写周报" "撰写周报" => #"weekly.md"
   LLM "<成员名>" "发周报邮件" "发送周报邮件"
   LOOP item=#"tasks.json".array concurrency=2
      IF item.done != true
         LLM "<成员名>" "处理 {{item.name}}" "处理任务 {{item.name}}" => #"out/{{item.name}}.md"
         SET item.done => true
      END
   END
END
```

### 9.5 条件退出

```text
LOOP item=#"tasks.json".array
   LLM "<成员名>" "检查 {{item.name}} 状态" "检查任务状态"
   IF item.status == "fatal"
      EXIT
   END
   LLM "<成员名>" "处理 {{item.name}}" "处理任务 {{item.name}}"
END
```

---

## 10. 与现有 DSL 的差异

| 特性 | 旧语法（早期 `llm_job` 工具所用，该工具已删除） | 新语法（核心规范） |
|------|-------------------|-------------------|
| 赋值 | `=` | `SET 值 => 目标` |
| 输出重定向 | `> / >>` | `=> 句柄` / `=> 句柄.eof` |
| 文件句柄 | 无 | `#"文件"` |
| 数据库句柄 | 无 | `@"数据库"` |
| 系统属性 | 无 | `path`, `tables`, `size` 等（无 `$` 前缀，通过 `db.tables.xxx` 访问表） |
| BREAK/CONTINUE | 无 | 支持 |
| EXIT | 无 | 支持 |
| 逻辑组合 | 仅 `not` | `not`/`and`/`or` + 括号 |
| 多行文本 | 无 | `<<<` / `>>>` |
| 内容读取 | 无 | `.content`、`.lines`、`.array`、`.object` |
| 表查询 | 无 | `.query("expr")` |
| LOOP 数据源 | 仅 `item="JSON文件"` | `#"文件".lines/array`、`db.tables.表`、变量，变量名可自定义 |
| LOOP 范围控制 | 无 | `.range(N,M)` 追加在数据源后 |

---

## 11. 歧义审查

### 11.1 PARALLEL 中的 BREAK / CONTINUE

**问题**：PARALLEL 分支并发，每个分支有自己的 goroutine。一个分支的 BREAK 是否影响其他分支？

**结论**：**只影响本分支内最近的 LOOP**，不影响其他分支。

```text
PARALLEL
   LOOP item="a.json"
      IF item.fatal
         BREAK          # 只跳出本分支的 LOOP，不中断分支2
      END
   END
   LOOP item="b.json"
      LLM "<成员名>" "处理 {{item.name}}" "处理任务 {{item.name}}"     # 不受影响，继续
   END
END
```

**实现**：BREAK 在词法作用域内找最近的 LOOP，goroutine 之间不共享 BREAK 信号。

### 11.2 PARALLEL 中的 EXIT

**问题**：EXIT 需要终止整个脚本，但 PARALLEL 分支在 goroutine 中执行。

**结论**：**EXIT 必须跨 goroutine 生效**，终止所有分支。

```text
PARALLEL
   LOOP item="a.json"
      IF item.fatal
         EXIT           # 整个脚本终止，所有分支都停止
      END
   END
   LOOP item="b.json"
      LLM "<成员名>" "处理 {{item.name}}" "处理任务 {{item.name}}"     # 也会被终止
   END
END
```

**实现**：共享原子标志 `exitFlag`，每个步骤执行前检查；EXIT 设置标志并终止当前 goroutine，其他 goroutine 在下一次检查时退出。

### 11.3 PARALLEL 顶层使用 BREAK / CONTINUE（不在 LOOP 内）

**问题**：BREAK/CONTINUE 需要最近的 LOOP，但 PARALLEL 顶层没有 LOOP。

**结论**：**报错**，BREAK/CONTINUE 必须在 LOOP 块内使用。

```text
PARALLEL
   BREAK                # 错误：不在 LOOP 内
   LLM "<成员名>" "分析" "分析内容"
END
```

### 11.4 SET 值的类型歧义

**问题**：`SET db.tables.logs => tb` 中，`db.tables.logs` 是表句柄还是表内容？

**结论**：**`tables.` 后跟表名 = 表句柄**，不是内容。要读内容必须显式 `.content` 或 `.range`。

```text
SET db.tables.logs => tb           # tb = 表句柄
SET db.tables.logs.content => data # data = 表内容（智能）
SET db.tables.logs.range(0,9) => r # r = 表前10条记录
```

**同理**：`SET db => d` → d 是数据库句柄，不是内容。数据库没有 `.content`，只有 `.tables`、`.path`、`.size`。

### 11.5 句柄插值歧义

**问题**：`{{db}}` 或 `{{fh}}` 在 LLM 提示词中展开成什么？

**结论**：句柄在 `{{}}` 中**只产生描述字符串**，不读内容。

```text
SET @"state.db" => db
LLM "<成员名>" "分析 {{db}}" "分析数据库"          # {{db}} → "<db: state.db, tables: [logs, tasks, config]>"

SET #"big.log" => fh
LLM "<成员名>" "分析 {{fh}}" "分析日志文件"          # {{fh}} → "<file: big.log, size: 2.3GB, lines: 500000>"
```

**好处**：安全（不会撑爆上下文）、信息足够（LLM 知道数据源是什么，可以决定如何读取）。

### 11.6 SET 值 => 目标 的语义一致性

**问题**：`SET "hello" => fh` 和 `SET fh.content => text` 看起来方向相反。

**结论**：`SET 值 => 目标` 统一为**值流向目标**。

```text
SET "hello" => fh           # 字符串 → 文件（写）
SET fh.content => text      # 文件内容 → 字符串变量（读）
SET db.tables.logs => tb    # 表句柄 → 变量（赋值句柄引用）
SET db.tables.logs.content => data # 表内容 → 变量（赋值数据）
```

**判别规则**：
- `=>` 右边是**句柄/表句柄/句柄.eof/句柄.range** → 写操作
- `=>` 右边是**普通变量** → 赋值操作
- 左边是**句柄.content/句柄.range** → 读操作

### 11.7 句柄的 `.range` 写入语义

**问题**：`SET text => fh.range(10,20)` 写入文件行范围，或 `SET data => db.tables.logs.range(0,9)` 写入表范围。如果行/记录不存在怎么办？

**结论**：`.range` 写入是**替换操作**，与行数/记录数无关。只替换已存在的行/记录，超出范围的部分不生效（静默忽略，不报错）。

```text
# 文件只有 5 行，写 range(10,20) → 不存在 10-20 行，不生效
# 表有 100 条记录，写 range(0,9) → 替换前 10 条，后 90 条不变
```

### 11.8 多行文本 `<<<` / `>>>` 的边界

**问题**：`<<<` 在行末，`>>>` 在行首。如果一行以 `<<<` 结尾，下一行以 `>>>` 开头，不够明确？

**结论**：`<<<` 必须是行末（非空白后紧跟），`>>>` 必须是行首（前导空白忽略）。中间内容全部按原文保留，包括空行。

```text
LLM "<成员名>" "分析：<<<           # 行末 <<< 开始多行
第一行
第二行
>>> 然后继续" "分析多行文本"                # 行首 >>> 结束多行
```

等价于提示词：`"分析：\n第一行\n第二行\n然后继续"`。

### 11.9 无歧义特性确认

以下特性经审查认为**无歧义**：

| 特性 | 理由 |
|------|------|
| `$` 前缀规则已移除 | `$` 前缀不再需要，系统属性用自然名（`path`, `tables`, `size`），表名通过 `db.tables.xxx` 访问 |
| `.eof` 读返回空 | 行为明确，文件末尾就是空 |
| 智能内容阈值 | 只有阈值以内才返回真实数据，行为可预测 |
| `not`/`and`/`or` 优先级 | 标准优先级（not > and > or），括号可覆盖 |
| 作用域遮蔽 | 词法作用域，自内向外查找，标准语义 |
| LOOP 嵌套 | 深度 ≤ 8，超限报错 |
| 只读访问器 | `.content`/`.lines`/`.array`/`.object`/`.rows`/`.query()` 只读，写入报错，避免混淆读写方向 |
| LOOP 变量名 | 变量名可自定义，不同层用不同名字，不存在遮蔽问题 |
| `.range` 越界 | 越界报错，不静默 |

---

## 12. 幂等性审查

### 12.1 原则

脚本应可重放：同一脚本在相同初始状态下执行多次，结果一致。

### 12.2 幂等机制

| 机制 | 写法 | 说明 |
|------|------|------|
| 状态写回 | `SET item.done => true` | 处理完打标，下次跳过 |
| 跳过已完成 | `IF item.done != true` | 断点续跑核心 |
| 输出文件守卫 | `IF not exist #"out/{{item.name}}.md"` | 输出文件已存在则不重复生成 |
| 表内容守卫 | `IF not exist db.tables.logs` | 表已非空则不重复写入 |
| 文件存在守卫 | `IF exist #"config.json"` | 配置文件已存在则跳过初始化 |
| 数据库存在守卫 | `IF exist @"state.db"` | 数据库已存在则跳过建库 |

### 12.3 典型幂等脚本

```text
### 初始化（仅首次执行）
IF not exist @"state.db"
   LLM "<成员名>" "创建数据库结构" "初始化数据库" => @"state.db"
END

### 主循环：断点续跑
LOOP item=#"tasks.json".array concurrency=3
   IF item.done != true
      IF not exist #"out/{{item.name}}.md"
         LLM "<成员名>" "实现 {{item.name}}" "实现 {{item.name}}" => #"out/{{item.name}}.md"
      END
      SET item.done => true
   END
END

### 汇总（仅末次执行）
IF exist #"out" 
   LLM "<成员名>" "汇总所有输出" "汇总输出结果" => #"summary.md"
END
```

### 12.4 注意事项

- **SET 写回是即时整文件写回**：中断后数据不丢失，重跑从断点继续
- **`IF exist` 对文件句柄**：检查文件是否存在（`#"path"`），检查数据库（`@"path"`），检查表非空（`db.tables.logs`）
- **LLM 输出到中间文件**：确保文件名唯一（如 `{{item.name}}` 插值），避免不同条目覆盖同一文件
- **PARALLEL 中的 SET**：并发写回同一源文件，由 `jobSrc.mu` 互斥锁保护，安全但不建议频繁大文件写回

---

## 13. 类型判断 / 遍历扩展 / 数组下标（v2026-09-08，已实施）

> 状态：**✅ 已实施**（保留字与语句分支均已落地——`reservedVerbs` 含 `TYPEOF/ENTRY/SPLIT/JOIN/PUSH`，`execStmt` 含对应语句与数组下标读写）。补三类缺口：
> ① IF 无法按类型分流（无 typeof）；② object 无法逐字段遍历（只有单次迭代）；
> ③ string 无法拆分为可迭代数组；另增数组下标读写（CSV 取列等）。

### 13.1 新指令

均为**行首保留字动词**，不进 `.` 链，不与字段名/表名/句柄访问冲突。

#### TYPEOF —— 获取值的类型名

```text
TYPEOF 值 => 变量
```

`值` 为任意表达式（标量/复合/句柄）。`变量` 收到类型名字符串，与 §2 类型表一致：

| 值 | 返回 |
|----|------|
| `"x"` | `string` |
| `42` / `3.14` | `number` |
| `true` / `false` | `bool` |
| `null` | `null` |
| `[...]` / `.array` / 表记录数组 / SPLIT/PUSH 产物 | `list` |
| `{...}` / `.object` | `object` |
| `#"path"` | `file` |
| `@"path"` | `db` |
| `db.tables.xxx` | `table` |

典型用途：按类型分流（补 IF 缺口），两行组合，不改 cond 语法：

```text
TYPEOF cfg.data => t
IF t == "list"
   LOOP item = cfg.data ...
END
IF t == "object"
   ENTRY cfg.data => entries
   LOOP kv = entries ...
END
```

#### ENTRY —— object → KV 数组（解决 object 逐字段遍历）

```text
ENTRY 对象 => 变量
```

把对象转为 `[{key: ..., value: ...}, ...]` 数组，字段顺序不保证（map 语义）：

```text
SET #"config.json".object => cfg
ENTRY cfg => entries
LOOP kv = entries
   LLM "<成员名>" "{{kv.key}} = {{kv.value}}" "处理配置项"
END
```

约束：
- 值必须为 object（`map`/`Rec`），非 object（list/null/标量/句柄）→ 报错
- 键名固定 `key` / `value`（避免单字母键与真实字段撞名）

#### SPLIT —— 字符串按分隔符拆分（解决 string 迭代）

```text
SPLIT "文本", "分隔符" => 变量
SPLIT 变量, "分隔符" => 变量
```

产物为字符串数组（list）。语义对齐 `strings.Split`：
- 保留空段：`"a,,b"` 按 `,` → `["a","","b"]`（3 段）
- 分隔符必须非空，空串 → 报错
- 首个参数为任意字符串表达式（字面量或变量，可含插值 `{{}}`？**否**——参数解析按 SET 值表达式求值，含插值的用变量先行）

典型：CSV 行取列

```text
LOOP row = #"data.csv".lines
   SPLIT row, "," => cols
   IF cols[2] == "目标值"
      LLM "<成员名>" "处理 {{cols[0]}}" "处理目标行"
   END
END
```

#### JOIN —— list → 字符串（SPLIT 逆操作，CSV 拼行/多行文本落盘）

```text
JOIN 数组, "分隔符" => 变量
```

- 元素须为标量（string/number/bool），按 `valueToText` 文本化后拼接；**复合元素 → 报错**（显式原则）
- 空数组 → 空字符串；目标为裸变量
- 与 SPLIT 互逆：`SPLIT x, "," => p` 后 `JOIN p, "," => y`，`y == x`（含空段对称）
- 分隔符转义已解码：`JOIN rows, "\n" => text` 拼出**多行文本**，`SET text => #"f"` 落盘即为真实分行文件

> 注意：数组**整体** `SET badRows => #"f"` 走 `valueToText` 输出的是 **JSON 数组文本**（含 `[]` 与引号）；要产出 CSV/逐行文本必须先 JOIN 成单个字符串再写。

#### PUSH —— 向 list 尾部追加（运行时收集）

```text
PUSH 值 => 变量
```

目标必须为**已定义的 list 变量**；未定义或非 list → 报错（显式原则，先 `SET [] => 变量` 初始化）。仅内存改值，不回源：

```text
SET [] => rows
LOOP item = #"tasks.json".array
   PUSH item.name => rows
END
SET rows => #"names.json"     # 输出 JSON 数组文本 ["a","b",...]
```

### 13.2 数组下标 `[N]`（读写对称）

```text
值[索引]              # 读
SET 值 => 变量[索引]   # 写
```

规则：
- 索引可为数字字面量或变量；**负数 = 倒数**（`-1` = 末位，与 `.range` 一致）
- 越界：读报错、写报错（不静默）
- **写 = 纯内存改值**，不回源（不触发 Rec 源写回）；落盘用 `SET 数组 => #"file"` 整体输出
- 目标限定为内存 list 变量；句柄 / LOOP 项（带源）内的数组字段**不允许**作为写目标 → 报错

词法规则（消除与 JSON 字面量歧义）：
- `[` 之前是表达式值（标识符 / `"..."` / 数字 / `]` / `)`）→ **下标**
- `[` 位于行首或运算符后（`=`/`=>`/`(`/`,` 等）→ **JSON 数组字面量**

```text
SET parts[0] => first        # 读下标
SET 5 => parts[0]            # 写下标（内存）
LOOP item = [0,3]            # JSON 字面量（逐元素迭代 0、3）
```

### 13.3 LOOP 数据源语义（增强后一览）

| 数据源 | 迭代单元 | 说明 |
|--------|---------|------|
| `myList` | 元素 | 数组变量逐元素 |
| `#"f".lines` / `#"f".array` | 行 / 元素 | 文件访问器 |
| `db.tables.表` | 记录（对象） | 全表，无 50 条上限 |
| `db.tables` | 表名 | 字符串 |
| 对象变量 `cfg` | 对象本身一次 | **不变**（遍历字段用 `ENTRY`） |
| `SPLIT` / `ENTRY` / `PUSH` 产物 | 元素 | 均为 list，可迭代 |
| `JOIN` 产物 | — | 是 string（不可直接 LOOP；需再迭代则先 SPLIT） |
| string / bool / number 直接作数据源 | — | **报错**（JSON 文本先取 `.array`（§3.3）；其他文本先 `SPLIT`；数值序列暂缓，需时上 RANGE） |

### 13.4 明确不做（本轮）

- 容量设置、删除元素（list 不提供；重建/重组替代）
- number LOOP / `[start,end]` 特判范围（与数组字面量语义冲突）
- `.keys()` / `.entries()` 访问器（撞字段名，ENTRY 动词替代）
- `.at(N)` 下标（只读不对称，`[N]` 读写统一）
- 类型自动推断（`.json` 等）——维持显式访问器原则；JSON 文本转结构化改由**显式访问器**承担：`.array` / `.object` 扩展到 `string`（§3.3），不新增 `.json`

### 13.5 与现有语义的边界

| 现有行为 | 处置 |
|---------|------|
| `SET 值 => obj.key` 改/新增字段（`deepSetFields`，中间对象须存在） | 保留不变 |
| LOOP 项字段写 `SET item.done => true`（`Rec` 即时整源写回，幂等断点续跑） | 保留不变 |
| LOOP object 数据源单次迭代 | 保留不变 |
| `.content/.lines/.array/.object` 只读访问器 | 保留不变 |

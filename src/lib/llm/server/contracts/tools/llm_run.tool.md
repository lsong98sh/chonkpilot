# llm_run

[meta]
category=server
async=always
hot=true

[description]
LLM 委派/编排统一 DSL 执行器（script 或 file 二选一传入 DSL 脚本）：把"委派子 agent → 循环/并行批量处理 → 结果落盘 + 状态写回"固化成一份可执行脚本；**单次委派 = 一行 LLM 指令**（不再需要单独的委派/批量工具）。DSL 语法与既有批处理脚本 DSL 一致：行式指令，核心指令 SET/IF/LOOP/PARALLEL/BREAK/CONTINUE/EXIT/END 可任意嵌套（深度 ≤ 8），END 结束 LOOP/PARALLEL/IF 块，### 或 # 开头为注释行；文本一律双引号字符串并支持 {{}} 插值。

**路径要求（R-11）**：`file`（脚本文件路径）与 DSL 内**所有文件/目录引用**（`#"path"` 句柄，含 LOOP/SET 的数据源读取、访问器、`=> #"file"` 目标、`IF exist` 的路径）**必须是绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**；相对路径 → **顶层失败**（带行号/用途与原值，不写任何文件）。**相对路径不再按隐式 workDir 兜底解析**；项目内路径请用 `{{env.CHONKPILOT_WORKDIR}}` 显式拼接。

**DSL 注入变量 env（只读）**：宿主注入 5 个只读变量，DSL 内用 `{{env.名字}}` 引用：

| 变量 | 含义 |
|------|------|
| `CHONKPILOT_WORKDIR` | 项目工作目录（绝对） |
| `CHONKPILOT_DATADIR` | 数据目录（绝对） |
| `CHONKPILOT_TEMPDIR` | 临时目录根（`<系统 temp>/chonkpilot/<instance>/`，`!/` 前缀的落地目录） |
| `CHONKPILOT_EXEDIR` | server 可执行文件所在目录（绝对） |
| `CHONKPILOT_PROJECT` | 项目目录（与 CHONKPILOT_WORKDIR 同值） |

`WORKDIR` 为 `CHONKPILOT_WORKDIR` 的兼容别名（同值）；`env` 为宿主注入的保留变量、**只读**（脚本里 `SET … => env` 会报错）。

- LLM "<agent>" "<提示词>" "<目的>" [=> 目标]：一次 LLM 委派（一个独立子会话步骤，可调用工具）。**agent 必填** = 委派对象名，且必须**可委派** —— 取自系统提示词「**团队成员**」段（= 当前场景内的 agent），或 **app 级场景**（随发布只读）内**唯一**同名的 agent；两者都可写 `<场景id>/<agent名>` 前缀精确引用。不可委派 → **该步失败**（错误进作业汇总、脚本继续后续步骤，见下方「失败语义」），**不建子任务节点、该步不落盘**。**无场景（通用模式）**下系统提示词不含团队成员段 → 只能靠后一条（app 级场景内同名 agent）命中。**提示词必填** = 委派内容；**目的必填且非空** = 本次子 LLM 的运行目的（即该步的 **purpose / 展示名**，呈现为 **tasktree 节点 label**）。参数间以空白或逗号分隔均可（`LLM "架构设计师" "重构这段代码" "重构并解释代码"` 与 `LLM "架构设计师", "重构这段代码", "重构并解释代码"` 等价）；三参都做 `{{}}` 插值。**目的为软约束 10–20 字**（用于节点展示名可读性）：**超长截断为 20 字并记录日志、不足 10 字仅记录日志，均不报错**。`=> 目标` 把该步输出写入：变量（如 `=> 风险`，后续用 `{{风险}}` 或 `{{风险:2000}}` 截断拼入提示词）、文件句柄覆盖（`=> #"{{env.CHONKPILOT_WORKDIR}}/out/{{item.name}}.py"`）、追加（`=> #"{{env.CHONKPILOT_WORKDIR}}/log.txt".eof`）；**不带 `=>` 时输出文本回填父轮次/汇总**。子任务节点展示名 = **目的（按 10–20 字软约束处理后的文本）**。**参数个数 ≠ 3 → 顶层失败**：**缺参**（如只写 agent + 提示词）、**任一参数为空串**、参数多于 3 个，均在执行前拦截（报错含语法示例与行号）。

- SET 值 => 目标：统一赋值/输出。状态写回写法 `SET item.done => true`（兼容）或 `SET true => item.done`：把当前 LOOP 条目字段改为给定值并**即时**把整个源 json 数组写回原文件——与 IF 配合实现断点续跑：处理成功写 `SET item.done => true`，下次跑同脚本用 `IF item.done != true` 跳过已完成项。

- IF <条件>：条件块（无 ELSE）。条件支持全比较符（== != > >= < <=）、`exist`（`IF exist "文件"` / `IF not exist #"out/x.md"` / 表非空）、`not/and/or` 与括号；缺失字段与 null 一律视为 false。

- LOOP <变量名>=<数据源> [concurrency=N]：逐条处理，块内可嵌套 LOOP/PARALLEL/IF/SET/LLM 直到 END。变量名可自定义（外层 group / 内层 task 不冲突）。数据源：`#"{{env.CHONKPILOT_WORKDIR}}/tasks.json".array`（JSON 数组文件）、`#"{{env.CHONKPILOT_WORKDIR}}/x.csv".lines`（按行）、变量列表；可加 `.range(N,M)` 控制范围（跳过表头 `range(1,-1)`）。

- PARALLEL：每个顶层语句一个并发分支，直到 END。BREAK/CONTINUE 只影响本分支最近 LOOP；EXIT 立即终止整个脚本（跨分支）。

失败语义：单个步骤失败只记录错误、不阻塞其它；全部结束后汇总列明失败与落盘文件。脚本可重放：SET 状态写回 + IF 守卫 → 断点续跑幂等。

示例（一次委派 / 循环批量 / 断点续跑）：

```
### 单次委派：一行 LLM 指令（第三参 = 目的/展示名，必填；无输出目标 → 结果文本回填）；项目内路径用 {{env.CHONKPILOT_WORKDIR}} 拼绝对路径
### 返回值口径：只含该子轮次的最终回答正文 —— 不含 reasoning（思维链）与 tool 相关内容（工具调用轮中间正文 / 工具结果一律剔除）
### agent 名须取自系统提示词「团队成员」段（下方示例用的是出厂场景「开发场景」的成员名）
LLM "后端开发" "把这段代码重构并解释" "重构并解释代码" => #"{{env.CHONKPILOT_WORKDIR}}/out/xx.review.md"
### 断点续跑批量
LOOP item=#"{{env.CHONKPILOT_WORKDIR}}/bigtask.json".array concurrency=3
   IF item.done != true
      LLM "后端开发" "实现 {{item.name}}（规模 {{item.count}}）" "实现 {{item.name}}" => #"{{env.CHONKPILOT_WORKDIR}}/out/{{item.name}}.py"
      SET item.done => true
   END
END
### 并行审阅（LOOP 逐项内嵌 PARALLEL；{{item.name}} 须在 LOOP/IF 块内使用，块外插值为空）
LOOP item=#"{{env.CHONKPILOT_WORKDIR}}/files.json".array
   PARALLEL
      LLM "代码审查" "审阅 {{item.name}} 的代码并输出问题清单" "审阅 {{item.name}} 代码" => #"{{env.CHONKPILOT_WORKDIR}}/out/{{item.name}}.review.md"
      LLM "系统测试工程师" "为 {{item.name}} 补充回归用例清单" "补 {{item.name}} 回归用例" => #"{{env.CHONKPILOT_TEMPDIR}}/run.log".eof
   END
END
```

[parameters]
{"type":"object","properties":{"script":{"type":"string","description":"DSL 脚本文本（见 description 语法；与 file 二选一；单行 LLM 指令即一次委派）"},"file":{"type":"string","description":"已保存 DSL 脚本文件路径（与 script 二选一；必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，相对路径会报错）"}}}

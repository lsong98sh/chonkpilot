# chonkpilot-executor

独立 EXE，职责是**执行一个轮次（Turn）**：接收用户提问，调用 LLM 处理，中间可能多次调用工具，最终返回 LLM 的回复结果。可脱离主程序独立运行，不依赖主程序。

## DB 决策规则

```
workdir 下无 .ide/ide.db           → 在 %TEMP%/chonkpilot/<uuid> 创建临时 DB
workdir 下有 .ide/ide.db
  └─ 无 --session-id，无 --turn-id  → 创建临时 DB（独立会话，不污染项目 DB）
  └─ 有 --session-id 或 --turn-id   → 使用真实 .ide/ide.db（查不到就报错）
```

### LLM 配置降级链

```
1. --llm-protocol / --llm-model / --llm-api-key / --llm-api-url 命令行参数
2. --llm-config-file 指向的 JSON 文件
3. ~/.chonkpilot/config.json 中读取第一个 LLM 配置
4. 以上均无 → 报错退出
```

## 运行模式

`--prompt`、`--prompt-file`、`--turn-id` **三者必须且仅能指定一个**。

| 模式 | 传入参数 | 输入来源 | DB 行为 |
|------|---------|---------|---------|
| **IDE 恢复** | `--turn-id=<uuid>`（无 prompt） | 从 messages 表加载该 turn 的 user 消息 | 需真实 `.ide`。lookup turn → 取 session_id → 加载完整历史 → 写 assistant/tool messages |
| **延续会话** | `--session-id=<id>` + `--prompt`/`--prompt-file` | prompt 内容 | 需真实 `.ide`。加载历史 → 自动创建新 turn → 写 user + assistant/tool |
| **子 executor 创建** | `--turn-id=<uuid>` + `--prompt`/`--prompt-file` | prompt 内容 | 真实 `.ide` 或 temp DB。自动创建 turn 记录 → 写 user + assistant/tool |
| **一次性会话** | `--prompt`/`--prompt-file`（无 session/turn） | prompt 内容 | 无真实 `.ide` 时创建 temp DB；有真实 `.ide` 时也创建 temp DB（不污染项目） |

## 命令行参数

| 参数 | 必填 | 说明 | 默认行为 |
|------|------|------|----------|
| `--work-dir=<path>` | 是 | 工作目录。工具执行、文件操作的上下文 | 默认当前目录 |
| `--prompt-file=<path>` | 否* | 用户提问内容（文件路径）。**与 `--prompt`、`--turn-id` 三选一** | — |
| `--prompt=<string>` | 否* | 用户提问内容（直接字符串）。**与 `--prompt-file`、`--turn-id` 三选一** | — |
| `--turn-id=<id>` | 否* | 轮次 ID。取值：`<uuid>`（IDE 恢复/子 executor 创建）。**与 `--prompt`、`--prompt-file` 三选一** | IDE 恢复（无 prompt）；子 executor 创建（有 prompt） |
| `--session-id=<id>` | 否 | 会话 ID。延续会话时使用，**必须搭配 `--prompt`/`--prompt-file`** | — |
| `--system-prompt=<string>` | 否 | 系统提示词（直接字符串） | 不使用系统提示词 |
| `--system-prompt-file=<path>` | 否 | 系统提示词文件路径 | 不使用系统提示词 |
| `--tools=<json>` | 否 | 可用工具列表（JSON 格式，扁平结构数组） | 指定 → 使用该列表 + 内置工具，忽略 DB 中的自定义工具 |
| `--tools-file=<path>` | 否 | 可用工具列表文件路径（JSON 格式），优先级高于 `--tools` | 同 `--tools` |
| `--output=<format>` | 否 | 输出方式：`stdout` / `json` | 默认 `stdout` |
| `--pipe-addr=<path>` | 否 | 命名管道地址（Windows `\\.\pipe\...`），用于向父进程输出结构化事件 | 不使用管道，直接 stdout 输出 |
| `--reasoning=<on\|off>` | 否 | 启用/关闭思考/思维链模式 | 默认 `on` |
| `--verbose` | 否 | 详细输出模式（仅独立运行、无管道时有效） | 不输出 |
| `--log-level=<level>` | 否 | 日志级别：`debug` / `info` / `warn` / `error` | 默认 `error` |
| `--llm-config-file=<path>` | 否 | LLM 配置文件路径（JSON 格式） | 按降级链查找 → 报错 |
| `--llm-protocol=<name>` | 否 | LLM 协议（openai / claude） | 按降级链查找 |
| `--llm-model=<model>` | 否 | 模型名称（如 deepseek-v4-pro） | 按降级链查找 |
| `--llm-api-key=<key>` | 否 | API Key | 按降级链查找 |
| `--llm-api-url=<url>` | 否 | API Endpoint URL | 按降级链查找 |
| `--retry-count=<n>` | 否 | LLM 调用失败重试次数 | 默认 0 = 不重试 |
| `--retry-delay=<s>` | 否 | 重试间隔秒数 | 默认 5 |

> *标 `否*` 的 `--prompt-file`、`--prompt`、`--turn-id` 三者必须且仅能指定一个。

### 输入来源真值表

| 传入参数 | 条件 | 输入来源 | 备注 |
|---------|------|---------|------|
| `--prompt-file=<path>` | 文件存在且非空 | 文件内容 | 空文件等同于未传，报错 |
| `--prompt-file=<path>` | 文件不存在 | — | **报错** |
| `--prompt=<string>` | 字符串非空 | 字符串内容 | 空字符串等同于未传，报错 |
| `--turn-id=<uuid>`（无 prompt） | 真实 `.ide` + DB 中有该 turn | 从 messages 表加载 | lookup session_id → 加载历史 |
| `--turn-id=<uuid>`（无 prompt） | 真实 `.ide` + DB 中无该 turn | — | **报错**（turn 不存在） |
| `--turn-id=<uuid>`（无 prompt） | 无真实 `.ide` | — | **报错**（turn-id 依赖 DB） |
| `--turn-id=<uuid>` + prompt | 真实 `.ide` | prompt 内容 | 子 executor 创建模式 |
| `--turn-id=<uuid>` + prompt | 无真实 `.ide` | prompt 内容 | 创建 temp DB，子 executor 模式 |
| `--session-id=<id>` + prompt | 真实 `.ide` | prompt 内容 | lookup session → 加载历史 |
| `--session-id=<id>` + prompt | 无真实 `.ide` | prompt 内容 | 创建 temp DB |
| 仅 `--prompt`/`--prompt-file` | 有/无 `.ide` | prompt 内容 | 创建 temp DB（独立会话） |
| 三参数均未传 | — | — | **报错退出** |

### --llm-config-file JSON 格式示例

```json
{
  "protocol": "openai",
  "model": "deepseek-v4-pro",
  "apiKey": "sk-xxxxxxxxxxxx",
  "baseUrl": "https://api.deepseek.com",
  "reasoning": true
}
```

### --tools / --tools-file JSON 格式示例

```json
[
  {
    "name": "my_python_script",
    "description": "执行 Python 脚本，传入参数可用",
    "parameters": {
      "type": "object",
      "properties": {
        "script": {
          "type": "string",
          "description": "Python 脚本文件名"
        }
      },
      "required": ["script"]
    }
  }
]
```

每个元素对应一个自定义工具。扁平结构（只需 name/description/parameters），执行时自动包装为 API 格式 `{type:"function", function:{name, description, parameters}}` 发送给 LLM。

## 输出格式

### stdout 模式（无管道 — 独立运行）

使用 DeepSeek/OpenAI 兼容的 SSE 流式格式输出：

```
data: {"choices":[{"delta":{"reasoning_content":"思考过程..."},"finish_reason":null,"index":0}]}

data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":null,"index":0}]}

data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}]}

data: [DONE]
```

启动时输出 banner 到 stderr：
```
肥猫启动中... V1.0 - Session 模式
LLM: deepseek/deepseek-v4-flash
推理=on
```

### 管道模式（有 --pipe-addr）

通过命名管道输出结构化事件到父进程：

```
event: thinking {"content":"Analyzing request..."}
event: message_chunk {"content":"Hello","index":0}
event: tool_call {"tool":"read_file","tool_call_id":"call_xxx","arguments":"..."}
event: tool_result {"tool":"read_file","tool_call_id":"call_xxx","success":true,"result":"..."}
event: complete {"turn_id":"xxx","result":"...","score":0}
```

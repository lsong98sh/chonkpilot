# 基本信息
```yaml
name: ask_user
title: ask_user
args: ask_user --input={RAW-INPUT-FILE}
async: never
category: server
core-only: true
hot: true
output: stdout
runtime: builtin
timeout: 0
```

# 摘要
向用户做**简单抉择**并等待回答（是/否、少量选项）。**不要**用于开放式提问、需要长文本回答、或需要连续多轮澄清的问题——那类问题应直接用普通对话逐轮询问（一次只问一个小问题）。能从上下文推断、或用合理默认值推进的，直接推进，不要调用本工具。答案会作为工具结果返回。

# 使用建议
- **适用**：二选一 / 少量选项的确认，且不阻塞流程推进。
- **不适用**：开放式问题、需要用户写长文本、需要连续追问几轮才能定的事——改用普通对话逐轮问。
- **一次只问一个问题**；多个问题分多轮，不要一次抛出多个问题。
- 有合理默认值时直接采用默认值并在回复中说明假设，不要为了确认而确认。
- **超时**：`timeout` 未设置 = 使用项目全局 `ask_timeout_sec`（全局也未配置则一直等待）；`0` = 用全局；`-1` = 明确不超时；`>0` = 覆盖全局。到点未答 → 系统自动回复：有 `recommended` 时单选取首个、多选按「、」拼接，否则回复「用户未在时限内回复，请按推荐做法继续」，并继续本轮（不中断）。
- **背景说明**：`context` 可选，用一两句话说明为什么问、影响什么、选项差别；会展示在 Ask 中心右侧详情。

# 参数
```json
{
  "properties": {
    "custom": {
      "description": "是否允许用户在选项之外填写「其他」自定义文本（默认 false；与 options 并存，multi=true 时可与多选并存）",
      "type": "boolean"
    },
    "context": {
      "description": "可选：提问背景说明（为什么问、影响什么、选项差别），展示在 Ask 中心详情",
      "type": "string"
    },
    "timeout": {
      "description": "可选：超时秒数。未设置 = 用项目全局 ask_timeout_sec（全局也未配置则一直等待）；0 = 用全局；-1 = 明确不超时；>0 = 覆盖全局。到点自动回复（优先取 recommended）并继续本轮",
      "type": "integer"
    },
    "multi": {
      "description": "是否允许多选（默认 false）",
      "type": "boolean"
    },
    "options": {
      "description": "可选项列表（用户可直接点选回答；multi=true 时可多选）。元素为 {value,label}：value=随回答返回、label=展示文本；**也接受纯字符串**（等价 value=label=该字符串）",
      "items": {
        "properties": {
          "label": {
            "description": "选项展示文本",
            "type": "string"
          },
          "value": {
            "description": "选项值（随回答返回）",
            "type": "string"
          }
        },
        "type": "object"
      },
      "type": "array"
    },
    "question": {
      "description": "要问用户的问题",
      "type": "string"
    },
    "recommended": {
      "description": "推荐选项**值**列表（按 value 匹配高亮；字符串或数组均容错接受，字符串按单元素数组处理）",
      "items": {
        "type": "string"
      },
      "type": "array"
    }
  },
  "required": [
    "question"
  ],
  "type": "object"
}
```

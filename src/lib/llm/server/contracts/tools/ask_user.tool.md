# 基本信息
```yaml
name: ask_user
title: ask_user
async: never
category: server
hot: true
timeout: 0
```

# 摘要
向用户提问并等待回答（需要用户输入/确认时才使用）。适合需要用户决策、确认或提供信息时暂停并征询；答案会作为工具结果返回。

# 参数
```json
{
  "properties": {
    "custom": {
      "description": "是否允许用户在选项之外填写「其他」自定义文本（默认 false；与 options 并存，multi=true 时可与多选并存）",
      "type": "boolean"
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


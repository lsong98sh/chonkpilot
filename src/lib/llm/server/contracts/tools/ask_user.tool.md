# ask_user

[meta]
category=server
async=never
timeout=0
hot=true

[description]
向用户提问并等待回答（需要用户输入/确认时才使用）。适合需要用户决策、确认或提供信息时暂停并征询；答案会作为工具结果返回。

[parameters]
{"type":"object","properties":{"question":{"type":"string","description":"要问用户的问题"},"options":{"type":"array","description":"可选项列表（用户可直接点选回答；multi=true 时可多选）","items":{"type":"object","properties":{"value":{"type":"string","description":"选项值（随回答返回）"},"label":{"type":"string","description":"选项展示文本"}}}},"multi":{"type":"boolean","description":"是否允许多选（默认 false）"},"recommended":{"type":"array","items":{"type":"string"},"description":"推荐选项值列表（前端高亮；字符串或数组均容错接受，字符串按单元素数组处理）"}},"required":["question"]}

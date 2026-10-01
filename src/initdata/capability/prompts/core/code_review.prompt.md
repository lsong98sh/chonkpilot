# code_review

[meta]

[description]
对指定文件的代码做结构化审查

[arguments]
type: object
properties:
  path:
    type: string
    description: 待审查文件路径
  focus:
    type: string
    description: 审查重点（如 逻辑/性能/安全）
required:
  - path

[content]
# 代码审查

请对 `{{path}}` 进行结构化代码审查{{focus}}，重点：

1. 正确性：边界条件、错误处理、并发安全
2. 逻辑清晰度：命名、结构、可读性
3. 性能：不必要的分配、循环内 IO
4. 安全隐患：输入校验、路径处理、注入风险

输出格式：按「问题 → 位置(行号) → 严重度(高/中/低) → 建议」列表，最后给出整体结论。只报告可证实的问题，不臆测。

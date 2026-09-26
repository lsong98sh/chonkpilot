# explore

[meta]

[description]
探索项目：快速了解目录结构、关键文件与代码符号（面向陌生代码库）

[content]
# 探索项目

目标：{{goal}}（如"了解项目模块划分"、"找到某功能实现位置"）。缺省工作目录：{{work_dir}}

按以下顺序执行，控制范围，避免输出过大：

1. **顶层结构**：`directory_list` 列出根目录（`paths:["."]`, `recursive:false`），确认模块/目录划分。
2. **递归清单**（按需）：`directory_list`（`recursive:true`, `type:file`），留意跳过 .git/node_modules 等噪音。
3. **关键文件**：对每个重点目录，`directory_list` 单目录递归到 2-3 层，找到入口文件（main/README/index 等）。
4. **内容抽查**：`file_read` 读入口文件与重点文件的前 80-150 行（`start:1, limit:150, line_numbers:true`）。
5. **符号定位**（代码库）：`grep` 搜关键标识符（`fileext` 限定语言扩展名，如 `["go"]`）。
6. **总结**：输出目录树（2-3 层）+ 关键文件职责 + 依赖关系推断。

约束：
- 递归清单若过大，用 `depth`/`pattern` 收敛，不要一次拉全量。
- 读文件用 limit 分段，避免超限写临时文件后再读（除非必要）。
- 只读不写；不做任何修改操作。

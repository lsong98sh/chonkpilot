# core-tools-overview

[meta]
uri=mcp://chonkpilot/resources/overview
mimetype=text/markdown

[description]
core 分类工具总览（供 LLM 快速决定使用哪个工具）

[content]
# core 分类工具

| 工具 | 用途 | 何时用 |
|---|---|---|
| file_read | 读取文件内容/信息 | 查看文件、取代码片段 |
| file_write | 写文件（文本/base64） | 新建/覆盖文件 |
| grep | 正则搜内容/文件名 | 找符号、查关键字 |
| directory_list | 列目录（递归/过滤） | 了解目录结构 |
| directory_make | 递归建目录 | 准备目录骨架 |
| diff | 两文件 unified diff | 对比版本/改动 |
| patch | 应用 unified diff | 应用补丁到文件 |
| replace | 查找替换（批量/正则） | 批量改文本 |
| remove | 删除文件/目录 | 清理文件 |
| rename | 重命名/移动（批量/规则） | 改名、移动文件 |
| script_run | 执行脚本（多 runtime） | 跑命令/脚本/构建 |
| fetch | HTTP 请求/下载 | 调 API、拉取 URL |

通用参数（递归类工具）：`skip_dirs` 追加跳过目录、`ignore_files` 忽略文件 glob。

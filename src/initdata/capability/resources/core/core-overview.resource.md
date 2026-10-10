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
| file_read | 读取文件内容/元数据（多文件、分段） | 查看文件、取代码片段、修改前确认现状 |
| file_find | 定位文件（目录树/文件名 glob/内容 grep） | 找文件在哪、看目录结构、按关键字命中文件 |
| file_diff | 两文件（或同一文件两版本）统一 diff | 对比版本/改动、code review 看修改点 |
| filesys_run | 文件系统批量编排（新建/修改/删除/移动/复制） | 批量改文件、建/删/移目录 |
| script_run | 执行脚本/命令（多 runtime） | 跑命令/脚本/构建/测试 |
| dsl_run | DSL 统一编排（文件/浏览器/桌面/LLM 委派） | 循环/并行批处理 + 分支判断 + 结果落盘 |
| web_fetch | HTTP 请求/下载 | 调 REST API、抓静态页、下载文件 |
| browser_run | 浏览器自动化（网页 E2E，headless Chrome） | 操作网页、断言、截图、取 JS 渲染内容 |
| desktop_run | 桌面 UI 自动化（窗口/鼠标/键盘/截图） | 操作桌面程序（非浏览器应用） |

通用过滤参数（file_find）：`skip_dirs` 追加跳过目录、`ignore_files` 忽略文件 glob。

# 基本信息
```yaml
name: core-overview
title: core-tools-overview
```

# 摘要
core 分类工具总览（供 LLM 快速决定使用哪个工具）

# 正文
# core 分类工具

| 工具 | 用途 | 何时用 |
|---|---|---|
| filesys_run | 文件系统批量编排（新建/修改/删除/移动/复制 + 只读查询 READ/DIFF/FIND） | 批量改文件、建/删/移目录；读取文件、对比两文件、定位文件（原 file_read/file_find/file_diff 已归入本工具的 READ/DIFF/FIND 动作） |
| script_run | 执行脚本/命令（多 runtime） | 跑命令/脚本/构建/测试 |
| dsl_run | DSL 统一编排（文件/浏览器/桌面/LLM 委派） | 循环/并行批处理 + 分支判断 + 结果落盘；文件查询动作经前缀化为 `FILE_READ`/`FILE_DIFF`/`FILE_FIND` |
| web_fetch | HTTP 请求/下载 | 调 REST API、抓静态页、下载文件 |
| browser_run | 浏览器自动化（网页 E2E，headless Chrome） | 操作网页、断言、截图、取 JS 渲染内容 |
| desktop_run | 桌面 UI 自动化（窗口/鼠标/键盘/截图） | 操作桌面程序（非浏览器应用） |

> 原 `file_read` / `file_find` / `file_diff` 三个独立工具已**收纳**：`filesys_run` 以裸动词 `READ`/`DIFF`/`FIND` 暴露，`dsl_run` 前缀化为 `FILE_READ`/`FILE_DIFF`/`FILE_FIND`。
> 通用过滤参数（FIND）：`skip_dirs` 追加跳过目录、`ignore_files` 忽略文件 glob。

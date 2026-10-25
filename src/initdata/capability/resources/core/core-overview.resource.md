# 基本信息
```yaml
name: core-overview
title: core-tools-overview
```

# 摘要
工具面总览（供 LLM 快速决定使用哪个工具）。file 系列由 `file` 插件提供，`script_run` / `web_fetch` 由 core-mcp-server 提供。

# 正文
# 工具总览

> **归属（2026-10-25，PLUGIN-SPLIT-1）**：`file_*` 11 个工具由 **`file` 插件**（独立 MCP server）提供，
> 经 gateway 调用；`script_run` / `web_fetch` 由 **core-mcp-server** 提供。对 LLM 而言二者用法一致。

| 工具 | 用途 | 何时用 |
|---|---|---|
| file_read / file_write | 读取 / 写入文件 | 单文件或批量；统一 `{purpose, targets[]}`，支持区间读取、BOM 与编码控制 |
| file_replace / file_patch | 精确文本替换 / 应用 unified diff | 改代码、打补丁；可带 `expectMd5` 防并发覆盖 |
| file_diff | 比较两个文件 | 确认改动、生成补丁 |
| file_delete / file_move / file_copy | 删除 / 移动改名 / 复制 | 批量整理文件（删目录须显式 `recursive`） |
| file_find | 四合一查找（列目录 / glob / grep / 树形） | 找文件、找内容；`limit` 上限 2000，`meta=hash` 可算 md5 |
| file_detect / file_iconv | 检测编码 / 转换编码 | 乱码排查、GBK↔UTF-8（低置信度会拒绝自动转，要求显式指定源编码） |
| script_run | 执行脚本/命令（多 runtime） | 跑命令/脚本/构建/测试 |
| web_fetch | HTTP 请求/下载 | 调 REST API、抓静态页、下载文件 |

> **浏览器 / 桌面自动化不再是工具**：在 `script_run`（python 等）里经 UDS 直连常驻插件 ——
> `%LOCALAPPDATA%\chonkpilot\plugins\browser.sock` / `desktop.sock`，端点 `start` / `stop` / `operation`（一行一 JSON）。
> 文件域工具**全部支持批量**：`{purpose, targets[]}`，单文件也写成 1 个元素的数组。

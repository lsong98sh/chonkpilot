# 文件查找

[meta]
runtime=../../executors/chonkpilot-core-executor.exe
hot=true
category=core
async=auto
async-threshold=30
timeout=120
args=file_find --input={RAW-INPUT-FILE}
output=stdout

[description]
项目探索用：在指定目录下**定位文件**——找某文件在哪/看目录结构/按内容命中哪些文件时用它（先定位范围，再 file_read 精读，避免盲目遍历）。path 必填（第一个参数，搜索起始路径），其余过滤可选，组合收窄更高效：
- path（搜索起始路径，必填；**必须为绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**，如 `C:\work\proj\src`、`~/proj/src`；相对路径 → 工具**整体失败**，消息含原值与示例）
- depth（0=递归不限；1=当前目录；2=当前+下一级，tree 模式同样受限）
- glob（文件名 glob 过滤，支持 **）
- grep（文件内容正则，命中行输出，无命中不列出）
- output（tree|file|summary：
  - tree=目录结构
  - file=文件路径
  - summary=文件名+前/后各最多 200 字符摘录，带 grep 时输出命中行，整体不超过 200 行）。

示例（在项目 src 目录下找包含 "TODO" 的文件，summary 预览）：
```json
{
  "path": "~/proj/src",
  "grep": "TODO",
  "output": "summary"
}
```

[parameters]
properties:
    path:
        description: 搜索起始路径（必填；必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径；相对路径会报错）
        type: string
    depth:
        description: 递归深度（0=不限；1=当前目录；2=当前+下一级）
        type: integer
    glob:
        description: 文件名 glob 过滤（filepath.Match，逗号/竖线多模式，支持 **）
        type: string
    grep:
        description: 文件内容正则（命中行输出；无命中不列出）
        type: string
    period:
        description: 文件修改时间范围过滤（仅文件，mtime 落在 [from,to] 含边界；可只给一端）
        type: object
        properties:
            from:
                description: 起始修改时间（含；文件 mtime ≥ from）。YYYY-MM-DD 从当日 00:00:00 起算
                type: string
            to:
                description: 截止修改时间（含；文件 mtime ≤ to）。YYYY-MM-DD 含当日整天
                type: string
    output:
        description: tree | file | summary（默认 file）
        type: string
    skip_dirs:
        description: 追加跳过目录名
        items:
            type: string
        type: array
    ignore_files:
        description: 忽略的文件 glob
        items:
            type: string
        type: array
required:
    - path
type: object

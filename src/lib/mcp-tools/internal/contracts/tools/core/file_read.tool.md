# 读取文件

[meta]
runtime=chonkpilot-core-executor.exe
hot=true
category=core
async=auto
async-threshold=30
timeout=60
args=file_read --input={RAW-INPUT-FILE}
output=stdout

[description]
读取文件内容或元数据，代替"打开文件浏览"。**适用场景**：探索代码/文档时需要看具体内容、修改前确认文件现状、查文件行数/大小/编码。一次请求可读多个文件；**大文件务必分段**（start/limit/tail/ranges），避免一次性读爆输出；只需元数据时用 info=true（不读内容）。UTF-16 自动解码；二进制返回 base64 data URI（不要对大型二进制文件使用）。返回 JSON：每文件含 path、content（或 info）、encoding、行数、size、**md5（文件内容 MD5 hex，用于后续写入/修改时的 md5 校验）**。
**路径要求**：`files[].path` **必须是绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**（如 `C:\work\proj\src\main.py`、`~/data/x.txt`）；相对路径 → 工具**整体失败**（错误状态返回，消息含出错的原值与正确写法示例），请改正后重试。

[parameters]
properties:
    files:
        description: 文件对象数组
        items:
            properties:
                info:
                    description: 仅返回文件信息（行数/大小/编码），不读内容
                    type: boolean
                limit:
                    description: 读取行数
                    type: integer
                line_numbers:
                    description: 输出带行号
                    type: boolean
                path:
                    description: 文件路径（必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径；相对路径会报错）
                    type: string
                ranges:
                    description: 多个行段 [[start,end], ...]
                    items:
                        items:
                            type: integer
                        type: array
                    type: array
                start:
                    description: 起始行号（1 基）
                    type: integer
                tail:
                    description: 只读末尾 N 行
                    type: integer
            required:
                - path
            type: object
        type: array
required:
    - files
type: object
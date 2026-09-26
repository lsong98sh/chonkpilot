# 文件差异

[meta]
runtime=chonkpilot-core-executor.exe
hot=true
category=core
async=auto
async-threshold=30
timeout=60
args=file_diff --input={RAW-INPUT-FILE}
output=stdout

[description]
比较两个文件（或同一文件的两个版本）的内容差异，输出统一 diff（- 删除 / + 新增）。**适用场景**：改动前后对比确认变更、理解两版差异、code review 时看修改点。三种指定方式任选：file1+file2（两文件）、files 数组（可多对）、path 数组（两文件路径）。返回 diff 文本。
**路径要求**：所有文件路径（`file1`/`file2`/`files[].path`/`files[].path2`/`path[]`）**必须是绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**；相对路径 → 工具**整体失败**（错误状态返回，消息指明是哪一个路径并含原值与示例），请改正后重试。

[parameters]
properties:
    file1:
        description: 源文件（必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径；相对路径会报错）
        type: string
    file2:
        description: 目标文件（必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径；相对路径会报错）
        type: string
    files:
        description: 文件对数组（对象含 path/path2；两者均须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径）
        items:
            properties:
                path:
                    description: 源文件（必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径；相对路径会报错）
                    type: string
                path2:
                    description: 目标文件（必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径；相对路径会报错）
                    type: string
            type: object
        type: array
    path:
        description: a、b 两文件路径数组（两项均须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径）
        items:
            type: string
        type: array
type: object

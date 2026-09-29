# 执行脚本

[meta]
runtime=../../executors/chonkpilot-core-executor.exe
hot=true
category=core
async=manual
timeout=300
args=script_run --input={RAW-INPUT-FILE}
output=stdout

[description]
执行脚本/命令，完成内置工具链做不到的事（需要"自己写代码去执行"时用它，如 Python 解析 Excel/转换数据、跑测试/构建、调用外部 CLI）。runtime 必填：python / powershell / shell / cmd / js / bash / vbs / java。**用法建议**：临时脚本用 script（代码直接给），需要复用/复杂依赖用 file；依赖用 requires 声明（python→pip、js→npm，首次安装需联网）；workdir 控制运行目录；输出可用 filter 过滤保留需要的行。失败返回退出码 + stderr，据此修复后重试。
**路径要求（R-11）**：`file`（脚本文件路径）、`workdir`（运行目录）、`interpreter`（解释器/可执行文件路径）**必须是绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**（如 `C:\work\proj\tools\python.exe`、`~/proj`）；相对路径 → 工具**整体失败**（错误状态返回，消息含原值与正确写法示例）。`args`/`env`/`filter` 等**命令参数**不受影响；runtime 内置的 shell/cmd/bash 默认命令走 PATH，不受约束。
**异步语义**：本工具默认 **manual**（同步等待直到执行完成）。根据脚本内容自行决定是否在调用时传 `async` 覆盖：短任务保持默认；长任务 / 启动服务（常驻进程）传 `async=always`（立即返回 pending + task_id，后台运行、完成走 task-done）；不确定时长传 `async=auto`（30s 未完成自动转后台）。

[parameters]
properties:
    args:
        description: 传给脚本的 argv
        items:
            type: string
        type: array
    env:
        additionalProperties:
            type: string
        description: 环境变量覆盖
        type: object
    file:
        description: 脚本文件路径（与 script 二选一；必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，相对路径会报错）
        type: string
    filter:
        description: 输出行过滤正则（等价 | findstr）
        type: string
    interpreter:
        description: 解释器/可执行文件路径（显式覆盖；必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，相对路径会报错）
        type: string
    requires:
        description: 依赖包（python→pip / js→npm；其它 runtime 不支持依赖安装，传了会整体失败）
        items:
            type: string
        type: array
    runtime:
        description: shell | cmd | bash | python | js | powershell | vbs | java
        type: string
    script:
        description: 脚本代码（与 file 二选一）
        type: string
    workdir:
        description: 脚本工作目录（必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，支持 ~ 展开；相对路径会报错）
        type: string
required:
    - runtime
type: object
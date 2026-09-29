# 文件系统编排

[meta]
runtime=../../executors/chonkpilot-core-executor.exe
hot=true
category=core
async=auto
async-threshold=30
timeout=60
args=filesys_run --input={RAW-INPUT-FILE}
output=stdout

[description]
**文件系统批量编排（新建/修改/删除/移动/复制）用它**——替代逐个单文件操作。脚本 DSL 与 llm_job 共用同一引擎（支持 SET/IF/LOOP/PARALLEL 流控与 `{{}}` 插值）。编辑前建议先用 file_read 确认目标现状，并记录每文件 md5；修改时把 md5 映射放到顶层 `md5` 参数，执行器在锁内校验，不一致则不写（记 fails，不整批失败）。

**路径要求（重要）**：脚本内**所有文件/目录引用**——动词的文件句柄 `#"path"`、核心语句的文件句柄（**LOOP/SET 的数据源读取**、访问器 `.content/.lines/.array/.object/.range`、`=> #"file"` 目标、`IF exist` 的路径）、顶层 `file`（脚本文件路径）、顶层 `md5` 的**键**（路径）——**都必须是绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**（如 `C:\work\proj\src\main.py`、`~/proj/src/util.py`）。任一为相对路径 → 工具**整体失败**（返回错误状态，消息含出错的原值、出错位置与正确写法示例），**不记入 fails**；脚本在开始执行前先做一次路径预校验，字面相对路径会被直接拒绝，**不产生任何文件改动**（`{{}}` 插值得到的路径在执行时校验，同样按整体失败处理）。下方示例中的相对写法仅为语法示意，实际调用请一律写绝对路径、`~/` 路径或 `!/` 路径。

**DSL 注入变量 env（只读；相对路径不得依赖隐式 workDir）**：宿主注入 5 个只读变量，DSL 内用 `{{env.名字}}` 显式拼绝对路径（相对路径会被拒绝）：

| 变量 | 含义 |
|------|------|
| `CHONKPILOT_WORKDIR` | 项目工作目录（绝对） |
| `CHONKPILOT_DATADIR` | 数据目录（绝对） |
| `CHONKPILOT_TEMPDIR` | 临时目录根（`<系统 temp>/chonkpilot/<instance>/`，`!/` 前缀的落地目录） |
| `CHONKPILOT_EXEDIR` | 执行器可执行文件所在目录（绝对） |
| `CHONKPILOT_PROJECT` | 项目目录（与 CHONKPILOT_WORKDIR 同值） |

`env` 为宿主注入的保留变量、**只读**（脚本里 `SET … => env` 会报错）；引用**只能**写成 `{{env.<NAME>}}`（上表 5 个名字），**无裸名变量**（`{{WORKDIR}}`、`{{CHONKPILOT_WORKDIR}}` 均不存在）。示例：`RPL #"{{env.CHONKPILOT_WORKDIR}}/src/main.py" …`、`INS #"{{env.CHONKPILOT_TEMPDIR}}/tmp.txt" "x"`（等价于 `INS #"!/tmp.txt" "x"`）。

DSL 语法（每行一条指令；`#` 或 `###` 起注释；文本一律双引号字符串，跨行文本用字面 `\n`）：

| 指令 | 说明 |
|------|------|
| `RPL #"path" "search" "replace"` | 全局替换：把 search 文本出现的每一处替换为 replace（search 为空报错；未找到记 fails） |
| `APD #"path" "content"` | 追加到文件尾（文件非空且末尾无换行时先补换行） |
| `PTC #"path" "<diff>"` | 应用 unified diff 补丁（diff 内换行用字面 `\n` 拼成单行参数） |
| `INS #"path" "content"` | 创建新文件（自动建父目录；目标已存在记 fails，请改用 RPL/PTC/APD） |
| `DEL #"path" "search"` | 删除所有包含 search 的行 |
| `DEL #"path"` | 删除文件或目录 |
| `MOV #"from" #"to"` | 移动/重命名：文件走 rename（跨卷自动回退 复制+删除）；目录 = 先整体复制、成功后逐文件删除源 |
| `CPY #"from" #"to"` | 复制文件或目录（整份递归） |

文件句柄 `#"path"` 与字符串 `"text"` 均支持 `\" \\ \n \t` 转义；含特殊字符的路径/文本直接放引号内即可，无需其它包裹。**所有 `#"path"` 必须是绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**（相对路径 → 整体失败，见开头「路径要求」）。

一致性规则（**两类错误语义必须区分**）：
- 修改/删除已有文件前自动加跨进程锁（30s 内重试，超时才真失败）；操作完成后自动解锁
- 顶层 `md5` 参数（`map[string]string`：路径 → 期望 md5）与 file_read 返回的 `md5` 对应；锁内校验不一致 → 该文件跳过不写，记入 fails
- **参数级违规（路径不合规）= 整体失败**：任一 `#"path"` 句柄（含动词、数据源读取、`=> 目标`、`IF exist`）、顶层 `file`、顶层 `md5` 的键若为相对路径，工具以错误状态返回（exit 非 0，消息含原值、位置与示例），**不记入 fails**
- **运行时冲突 = 记 fails、不整体失败**：md5 不一致、lock 超时、未找到匹配、patch 失败等单条指令失败明细进 `fails`（文件级），其余指令继续执行，整体按成功返回
- 目录操作成功项按目录路径记录（不展开）；目录移动/删除的部分残留文件会以具体文件路径出现在 fails

返回值（顶层 JSON）：

| 字段 | 含义 |
|------|------|
| `output` | 简短摘要（modified/created/deleted 计数）；diff 正文见 `modified[].diff` |
| `modified` | `[{ "path", "type", "size", "mtime", "md5", "diff" }]`：被修改文件 before→after 的统一 diff（同文件多指令合并为一份）；`size`（字节）/ `mtime`（RFC3339）/ `md5`（最终内容） |
| `created` | `[{ "path", "type", "size", "mtime", "md5" }]`：新建/复制/移动目标的条目（含大小/时间/md5）；目录条目 `type="dir"`、`size=0`、无 `md5` |
| `deleted` | 删除/移动源的路径列表（字符串数组；删除后不再取 size/mtime/md5） |
| `fails` | `[{ "file", "op", "error" }]`：单项失败明细（md5 不一致、lock 超时、未找到匹配、patch 失败、删除残留等） |

示例 1（修改已有文件：替换 + 追加 + 删行 + 打补丁）：
```
### 先 file_read 确认现状并取 md5，再修改（路径一律绝对 / ~/ / !/ 开头）
RPL #"~/proj/src/main.py" "def doFoo():" "def doFoo(xs):\n    return sum(xs)"
APD #"~/proj/src/main.py" "print('all done')\n"
DEL #"~/proj/src/main.py" "print('debug')"
PTC #"~/proj/src/old.py" "--- a\n+++ b\n@@ -1,3 +1,3 @@\n def f():\n-    old_call()\n+    new_call()\n "
```

示例 2（创建 / 删除 / 移动 / 复制，目录级）：
```
### 创建新模块
INS #"~/proj/src/util.py" "def helper():\n    pass\n"
### 复制到备份目录
CPY #"~/proj/src/config.py" #"~/proj/backup/config.py.bak"
CPY #"~/proj/src/assets" #"~/proj/backup/assets"
### 移动文件
MOV #"~/proj/src/app.py" #"~/proj/lib/app.py"
### 移动目录（内部先复制后删源）
MOV #"~/proj/src/modules" #"~/proj/pkg/modules"
### 删除文件 / 删除目录
DEL #"~/proj/src/old.py"
DEL #"~/proj/src/legacy"
```

示例 3（携带 md5 校验的编辑 + 失败形态）：
```
filesys_run 请求：
{
  "script": "RPL #\"~/proj/src/main.py\" \"old_text\" \"new_text\"",
  "md5": { "~/proj/src/main.py": "<file_read 返回的 md5>" }
}
返回 fails 示例：
fails: [
  { "file": "~/proj/src/main.py", "op": "RPL", "error": "MD5 不一致，其他进程已修改，请重新 read 后进行 patch" }
]
```
单文件 md5 不一致只影响该文件，同一脚本其它文件的修改照常执行；整体仍按成功返回（fails 供你处置冲突项）。**注意**：上面的 fails 是运行时冲突（整体成功）；若路径写成了相对路径，则是**参数级违规**，工具直接整体失败（无 fails、无文件改动）。

[parameters]
properties:
    file:
        description: 编辑脚本文件路径（与 script 二选一；必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，相对路径会报错）
        type: string
    md5:
        description: 文件 md5 校验映射（路径→期望 md5 hex，取自 file_read 返回值），修改已存在的文件时在锁内校验；不一致跳过该文件并记入 fails。键（路径）必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，否则整体失败
        type: object
        additionalProperties:
            type: string
    script:
        description: 文件操作 DSL 脚本（每行一条指令，语法与样例见上）
        type: string
required:
    - script
type: object

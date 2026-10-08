# 桌面编排

[meta]
runtime=../../executors/chonkpilot-desktop-executor.exe
hot=true
category=desktop
async=auto
async-threshold=60
timeout=300
idempotent=false
args=desktop_run --input={RAW-INPUT-FILE}
output=stdout

[description]
**操作系统桌面 UI 自动化**：需要像真人一样操作桌面程序（记事本/Office/计算器等非浏览器应用：找窗口、点击、输入、拖拽、截图）时用它；**浏览器内网页操作请用 browser_run**。把操作写成一行一条指令的脚本；移动带缓动+随机扰动模拟真人。`#` 或 `//` 开头为注释；任何指令失败立即中止，报错带行号。

**路径要求（R-11，重要）**：`file`（脚本文件路径）、脚本内**落盘路径**（`SHT "f.png"`、`WIN … SHT "f.png"`、行尾 `=> #"file"` 重定向目标）、以及 DSL 核心语句的**所有文件句柄**（**LOOP/SET 的数据源读取**，如 `LOOP row=#"items.csv".lines`；访问器 `.content/.lines/.array/.range`；`IF exist #"…"` / `IF exist "…"` 的路径）**必须是绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**（如 `C:\work\out\notepad.png`、`~/shots/win.png`）。相对路径 → 工具**整体失败**（错误状态返回，消息含出错的原值、位置与正确写法示例）；脚本内**字面**路径在开始执行前预校验（不产生任何文件改动），`{{}}` 插值得到的路径在执行时校验。下方示例中的相对写法仅为语法示意，实际调用请一律写绝对路径、`~/` 路径或 `!/` 路径。

脚本基于 **ChonkPilot DSL 核心**（dsl-core）：除下方桌面指令外，还支持核心流控 `LOOP/IF/SET/PARALLEL/BREAK/CONTINUE/EXIT`（`END` 收尾）、`#"文件".lines/.array.range(N,M)` 数据源与 `{{变量}}` 插值（WIN 后的 `$X/$Y/…` 也可在引号文本里以 `{{$CX}}` 引用），见示例 3。桌面指令（WIN/MOV/CLK/…）的参数保持原文坐标/表达式文法（`@` 锚点、`D/C` 相对、`$` 表达式、`->` 等）。

**动作重定向**：任何指令行尾可带 `=> #"out.txt"`，把该动作的文本输出写入文件（`WIN list => #"wins.txt"` 后接 `#"wins.txt".lines` 可读回）。所有动作统一支持：有文本输出的动作写文本（如 WIN list 的窗口清单），无输出（KPR/CLK/MOV 等）写空文件，不会因输出为空而跳过重定向。

**DSL 注入变量 env（只读；相对路径不得依赖隐式 workDir）**：宿主注入 5 个只读变量，DSL 内用 `{{env.名字}}` 显式拼绝对路径（相对路径会被拒绝）：

| 变量 | 含义 |
|------|------|
| `CHONKPILOT_WORKDIR` | 项目工作目录（绝对） |
| `CHONKPILOT_DATADIR` | 数据目录（绝对） |
| `CHONKPILOT_TEMPDIR` | 临时目录根（`<系统 temp>/chonkpilot/<instance>/`，`!/` 前缀的落地目录） |
| `CHONKPILOT_EXEDIR` | 执行器可执行文件所在目录（绝对） |
| `CHONKPILOT_PROJECT` | 项目目录（与 CHONKPILOT_WORKDIR 同值） |

`env` 为宿主注入的保留变量、**只读**（脚本里 `SET … => env` 会报错）；引用**只能**写成 `{{env.<NAME>}}`（上表 5 个名字），**无裸名变量**（`{{WORKDIR}}`、`{{CHONKPILOT_WORKDIR}}` 均不存在）。示例：`SHT "{{env.CHONKPILOT_WORKDIR}}/shots/win.png"`、`WIN list => #"{{env.CHONKPILOT_TEMPDIR}}/wins.txt"`（等价于 `=> #"!/wins.txt"`）。

指令一览：
- WIN <target> <op...>：定位窗口后可连续操作。target = "标题"(模糊匹配) | class:xxx | hwnd:123；op 依次执行：focus / min / max / restore / move x,y / size w,h / rect x,y,w,h，行内可加 SHT "file.png" 截当前窗口
- WIN list：列出全部可见窗口（标题）
- MOV x,y：鼠标移动到屏幕坐标
- CLK / DBL / CLKR / CLKM / DBLR / DBLM [x,y]：左键单击 / 双击 / 右键 / 中键 / 右键双击 / 中键双击
- LMD / LMU / RMD / RMU / MMD / MMU [x,y]：对应键按下 / 释放
- DRG x1,y1 -> x2,y2 [speed=快|中|慢] [jitter=高|中|低]：拖拽（贝塞尔轨迹 + 随机扰动，模拟真人）
- WHL [x,y] dx,dy：滚轮（120 为一格；dy 向下为正）
- INP "text"：输入文本（可打印字符走 Unicode 直通，不受输入法影响；中文前可先 `IME cn`）
- IME cn | IME en：切换前台窗口输入法（cn=中文启用 / en=英文直通，输路径、冒号反斜杠前用 en）
- KPR key / KDN key / KUP key：按键 按下并释放 / 按住 / 释放；**支持组合键**（修饰+主键，如 `KPR ctrl+s`、`KPR alt+f4`、`KDN win+r`、`KPR shift+enter`）
- SHT "file.png"：整屏截图 | SHT x,y,w,h "file.png"：区域截图 | WIN 链内 SHT：截当前窗口
- SLP ms：等待

坐标写法（鼠标/点击/拖拽目标位置，全脚本统一）：
- 绝对屏幕坐标：300,400
- D x,y：相对当前窗口 rect 左上角（D100,200）
- C x,y：相对当前窗口客户区左上角（C50,80）
- @center / @title / @tl / @tr / @bl / @br / @left / @right / @top / @bottom：窗口语义锚点
- @30%,40%：窗口内百分比位置
- $X+10,$Y：表达式（变量在 WIN 后自动设置：$X $Y $W $H 窗口 rect；$CX $CY $CW $CH 客户区 rect；$WIN 句柄）
- D/C/@/$ 相对参考 = 最后 WIN 定位的窗口；未 WIN 前使用即报错

示例 1（记事本输入并截图）：
```
WIN "记事本" restore
WIN "记事本" focus
MOV @center
CLK
INP "hello 世界"
KPR enter
SLP 500
SHT "notepad.png"
```

示例 2（定位窗口 → 移动 → 拖拽 → 列表）：
```
WIN "计算器" focus move 200,150
WIN "记事本" restore focus
DRG @title -> 600,400 speed=慢 jitter=高
WIN list => #"wins.txt"
CLK @center
```

示例 3（核心流控：按 CSV 逐行录入）：
```
LOOP row=#"items.csv".lines.range(1,-1)
   CLK @center
   INP "{{row}}"
   KPR enter
   SLP 300
END
IF not exist #"done.flag"
   SHT "result.png"
END
```

[parameters]
properties:
    script:
        description: 桌面编排脚本（一行一条指令，示例见上）
        type: string
    file:
        description: 已保存脚本文件路径（与 script 二选一；必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，相对路径会报错）
        type: string
required:
    - script
type: object

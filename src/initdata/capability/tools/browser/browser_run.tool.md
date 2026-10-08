# 浏览器编排

[meta]
runtime=../../executors/chonkpilot-browser-executor.exe
hot=true
category=browser
async=auto
async-threshold=60
timeout=300
idempotent=false
args=browser_run --input={RAW-INPUT-FILE}
output=stdout

[description]
**浏览器自动化（网页 E2E）**：需要像真实用户一样操作网页——打开页面、定位元素、点击/填表/下拉、断言页面状态、截图，或拿**JS 渲染后才存在**的内容时用它（headless Chrome）；**纯静态抓取/API 请求用 web_fetch**。把操作写成一行一条指令的脚本。`###` 开头为注释；指令名大小写不敏感；执行语义 **fail-fast**（除 WAT 外任何指令失败立即中止，报错带行号与原因）。

**路径要求（R-11，重要）**：`file`（脚本文件路径）、脚本内本地文件路径（`SHT` 截图 / `DOM` 导出 html / `DBG` console 日志 / `UPF` 上传文件，均为该指令的**最后一个参数**）、行尾 `=> #"file"` 重定向目标、DSL 核心语句的文件句柄（**LOOP/SET 的数据源读取**，如 `LOOP row=#"queries.csv".lines`，以及访问器 `.content/.lines/.array/.range`、`IF exist` 的路径）、`dom_file`/`console_file`/`fail_shot` 落盘路径、以及 `chrome_path`（Chrome/Edge 可执行文件路径，若提供），**都必须是绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**（如 `C:\work\out\page.png`、`~/shots/page.png`）。相对路径 → 工具**整体失败**（错误状态返回，消息含出错的原值、位置与正确写法示例）；脚本内**字面**路径在启动浏览器前预校验（不产生任何文件改动），`{{}}` 插值得到的路径在执行时校验。下方示例中的相对写法仅为语法示意，实际调用请一律写绝对路径、`~/` 路径或 `!/` 路径。

脚本基于 **ChonkPilot DSL 核心**（dsl-core）：除下方网页指令外，还支持核心流控 `LOOP/IF/SET/PARALLEL/BREAK/CONTINUE/EXIT`（`END` 收尾）、`#"文件".lines/.array.range(N,M)` 数据源与 `{{变量}}` 插值（例：按 CSV 逐行 FILL 搜索，见示例 3）。网页指令（OPN/CLK/FILL/…）参数保持原文（locator 须双引号包裹）；EVL/EXP 多行 JS 用引号内 `"…<<<` … `>>>…"` 表达（见示例 2），不再用 `---ID---` 块。

**DSL 注入变量 env（只读；相对路径不得依赖隐式 workDir）**：宿主注入 5 个只读变量，DSL 内用 `{{env.名字}}` 显式拼绝对路径（相对路径会被拒绝）：

| 变量 | 含义 |
|------|------|
| `CHONKPILOT_WORKDIR` | 项目工作目录（绝对） |
| `CHONKPILOT_DATADIR` | 数据目录（绝对） |
| `CHONKPILOT_TEMPDIR` | 临时目录根（`<系统 temp>/chonkpilot/<instance>/`，`!/` 前缀的落地目录） |
| `CHONKPILOT_EXEDIR` | 执行器可执行文件所在目录（绝对） |
| `CHONKPILOT_PROJECT` | 项目目录（与 CHONKPILOT_WORKDIR 同值） |

`env` 为宿主注入的保留变量、**只读**（脚本里 `SET … => env` 会报错）；引用**只能**写成 `{{env.<NAME>}}`（上表 5 个名字），**无裸名变量**（`{{WORKDIR}}`、`{{CHONKPILOT_WORKDIR}}` 均不存在）。示例：`SHT "{{env.CHONKPILOT_WORKDIR}}/shots/a.png"`、`DOM "{{env.CHONKPILOT_TEMPDIR}}/page.html"`（等价于 `DOM "!/page.html"`）。

指令一览：
- OPN "url"：打开页面
- WAT <loc> visible|enabled|text "x" [timeoutMs] | WAT load | WAT netidle：显式等待（超时默认 10s，是唯一会等待的指令；其余指令不隐式等待）
- CLK/DBL/RCL/HOV <loc>：单击/双击/右键/悬停；CHK/UCHK <loc>：勾选/取消
- FILL <loc> "text"：清空并输入；SELO <loc> "option"：下拉选择（按 value 或文本）
- DRG <from> -> <to>：拖拽；UPF <loc> "file"：文件上传
- KPR/KDN/KUP key：按键（支持组合，如 ctrl+a）
- EXP <loc> visible|enabled|text "x"|count N|attr name "v"：元素断言
- EXP page url ".." | EXP page title ".."：页面断言
- EXP js <JS表达式> ["自定义错误消息"]：JS 断言（表达式为 truthy 即通过）
- EVL <js>：执行任意 JS（单行；多行用 `EVL "<<<` 开行、行首 `>>>"` 收尾的引号内多行，见示例 2）
- DOM [<loc>] "file.html"：把元素 outerHTML（缺省 = 整页 DOM）写入文件
- DBG "file.log"：把当前累计 console 日志写入文件（排查 JS 报错）
- SHT "f.png"：全页截图 | SHT <loc> "f.png"：元素截图
- SCL：滚动（SCL <loc> 滚到元素 / SCL dx,dy 相对滚动）
- TAB list | TAB switch <标题|URL|序号> | TAB wait new | TAB close：标签页管理
- SLP ms：等待

Locator 定位器（动作/断言统一使用，仅两种类型）：
- css="CSS 选择器"：CSS 必须用双引号包裹（CSS 本身可含空格/属性，避免与后续参数冲突）。例：css="#login input[name=user]"、css="button[type=submit]"、css="[data-testid=save]"
- xpath="XPath"：XPATH 也用双引号。例：xpath="//button[text()='登录']"
- 一个 locator 内可链式定位：css=".modal" >> css="button"；取匹配集第 N 个元素追加 >> nth=2（0 基，负数从尾数）

示例 1（登录并断言跳转）：
```
OPN "https://example.com/login"
WAT css="#username" visible
FILL css="#username" "admin"
FILL css="#password" "p@ssw0rd"
CLK css="button[type=submit]"
EXP page url "https://example.com/home"
DOM css="#main" "main.html"
```

示例 2（拦截弹窗 + 新标签页 + JS 断言）：
```
EVL "<<<
window.confirm = () => true;
>>>"
CLK css="a[target=_blank]"
TAB wait new
WAT css="#content" visible
EXP js document.querySelectorAll('.error').length === 0 "页面不应有错误元素"
SHT "page.png"
DBG "console.log"
```

示例 3（核心流控：按 CSV 批量查询）：
```
OPN "https://example.com/search"
LOOP row=#"queries.csv".lines.range(1,-1)
   FILL css="#kw" "{{row}}"
   CLK css="button[type=submit]"
   WAT css="#result" visible
   SLP 300
END
```

[parameters]
properties:
    script:
        description: 浏览器编排脚本（一行一条指令，示例见上）
        type: string
    file:
        description: 已保存脚本文件路径（与 script 二选一；必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，相对路径会报错）
        type: string
    headless:
        description: 无头模式（默认 true；false 弹出可见窗口便于观察/调试）
        type: boolean
    dom_file:
        description: 执行结束（含失败）时把整页 DOM 导出到的文件路径（可选）；必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，相对路径会报错
        type: string
    console_file:
        description: 执行结束（含失败）时把累计 console 日志导出到的文件路径（可选）；路径要求同 dom_file（绝对路径、~/ 或 !/ 开头）
        type: string
    fail_shot:
        description: 任意指令失败时自动截图落盘的文件路径（可选；未提供则失败时不截图）；路径要求同 dom_file
        type: string
    wat_timeout_ms:
        description: WAT 等待指令的默认超时（毫秒，默认 10000）
        type: integer
    chrome_path:
        description: Chrome/Edge 可执行文件路径（可选；须为绝对路径或以 ~/ 开头的用户目录路径，相对路径会报错；缺省自动探测，亦可用环境变量 CHONK_CHROME）
        type: string
required:
    - script
type: object

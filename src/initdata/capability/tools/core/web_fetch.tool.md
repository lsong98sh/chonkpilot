# HTTP 请求

[meta]
runtime=../../executors/chonkpilot-core-executor.exe
hot=true
category=core
async=auto
async-threshold=30
timeout=120
idempotent=false
args=web_fetch --input={RAW-INPUT-FILE}
output=stdout

[description]
发起 HTTP 请求：抓取网页内容、调用 REST API、下载文件。**适用场景**：需要读某个 URL 的响应（页面 HTML/接口 JSON）、把网络资源下载到本地文件（save_as）。静态抓取用本工具；**需要与页面交互（点击/填表/渲染后内容）用 browser_run**。默认跟随重定向；乱码响应用 encoding 转码（gbk/big5/shift_jis 等）；超大响应自动写临时文件（用 file_read 读它）。返回状态码 + 响应体（save_as 时返回保存路径）。
**路径要求（R-11，重要）**：`save_as`（下载落盘路径）与 `form_files[].path`（上传时读取的本地文件）**必须是绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**（如 `C:\work\dl\pkg.zip`、`~/data/upload.txt`）。相对路径 → 工具**整体失败**（错误状态返回，消息含出错的原值与正确写法示例），不会发出请求。

[parameters]
properties:
    body:
        description: 请求体文本
        type: string
    cookies:
        additionalProperties:
            type: string
        description: Cookie 字段
        type: object
    encoding:
        description: 响应编码转码（gbk/big5/shift_jis/euc-jp/euc-kr/iso-8859-1）
        type: string
    follow_redirect:
        description: 跟随重定向（默认 true）
        type: boolean
    form:
        additionalProperties:
            type: string
        description: 表单字段（自动 POST multipart）
        type: object
    form_files:
        description: 表单文件 [{field,path}]；path 必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径（相对路径会报错）
        items:
            properties:
                field:
                    type: string
                path:
                    description: 上传文件路径（必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径；相对路径会报错）
                    type: string
            type: object
        type: array
    headers:
        additionalProperties:
            type: string
        description: 请求头
        type: object
    method:
        description: GET（默认）| POST | PUT | DELETE ...
        type: string
    readTimeout:
        description: 总超时秒数（默认 300）
        type: integer
    save_as:
        description: 下载保存路径（响应体直写文件；必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，相对路径会报错）
        type: string
    url:
        description: 请求 URL
        type: string
required:
    - url
type: object

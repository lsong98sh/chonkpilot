# 文档转换服务（mcps/markitdown）

让 ChonkPilot 的全文索引（vfts）能够读 Office / PDF：把文档转成 Markdown 文本，
同时给出**定位信息**（PDF 页码 / Excel sheet 名 / PPT slide 序号），供索引侧给 chunk 打标。

- **单进程 HTTP 服务**，**只监听 `127.0.0.1`**（跨机访问由回环绑定阻断）。
- **同一端口两个入口**：MCP（Streamable HTTP，`/mcp`）+ vfts 内部快通道（`/vfts/*`）。
- **随产品发，但默认不启动**；由设置页开关或用户按需拉起（本批只交付组件自身）。
- 转换内核 = [markitdown](https://pypi.org/project/markitdown/)（MIT）。

## 1. 启动

源码运行（开发调试）：

```powershell
cd src\mcps\markitdown
.\.venv\Scripts\python.exe server.py                 # 默认端口 7317
.\.venv\Scripts\python.exe server.py --port 7317 --workers 4
```

冻结产物运行（随产品投放，见 §5）：

```powershell
.\dist\desktop\mcps\markitdown\markitdown-mcp.exe
```

| 参数 | 默认 | 说明 |
|------|------|------|
| `--port` | `7317` | 监听端口。**默认端口被占用时自动另选**并写入状态文件；**显式指定**该端口被占用则直接报错退出 |
| `--workers` | `4` | `/vfts/convert` 的转换线程池大小 |

优雅退出（Ctrl+C / 进程结束）时删除状态文件。

## 2. 端口 / 状态文件 / token

启动时写入状态文件 `state.json`，位置（两处，插件侧**两处都探**）：

1. **exe 同目录**：`<installRoot>/mcps/markitdown/state.json`（源码运行 = `src/mcps/markitdown/state.json`）
2. **回落**：`<data-dir>/mcps/markitdown/state.json`，Windows 的 data-dir = `%LOCALAPPDATA%\chonkpilot`

> 安装目录只读（或写入失败）时自动回落到 ②，并在**日志**与 `/vfts/health` 的 `state_path`
> 字段回报**实际写入路径**（探活方以 health 为准即可）。

`state.json` 内容：

```json
{
  "pid": 12345,
  "port": 7317,
  "token": "3f2c…（32 位十六进制，每次启动随机）",
  "version": "0.1.0",
  "started_at": "2026-09-29T10:00:00+08:00",
  "exe_dir": "…\\dist\\desktop\\mcps\\markitdown"
}
```

- **token 只存在于状态文件**（不入日志、不入 health）；`/vfts/convert` 必须携带头 `X-Chonk-Token`。
- 日志：与状态文件同目录的 `markitdown-mcp.log`（UTF-8，轮转 1 MiB × 2 份）。

## 3. 入口一：MCP（Streamable HTTP）

- 端点：`http://127.0.0.1:<port>/mcp`（Streamable HTTP）。
- 工具：`convert_to_markdown(uri_or_path)` → 返回 Markdown 文本。
  `uri_or_path` 支持**本地路径** / `file://` URI / `http(s)://` URL。
  失败时返回以 `[convert_to_markdown failed] error_code=…; message=…` 开头的说明文本。
- **按 MCP 规范，本地使用不加额外鉴权**（安全边界 = 只绑回环 + 校验 Host 头）。

### 3.1 如何在本产品里手动注册为 MCP server（配置页指引文案）

> 把下面的文案直接用于 MCP 配置页 / 文档（后续 vfts 配置页复用同一段）：

```text
【添加 MCP 服务器】
名称：      markitdown（或“文档转换”）
类型：      Streamable HTTP（HTTP）
地址：      http://127.0.0.1:7317/mcp
启动方式：  外部进程（本产品不托管）
启动命令：  <产品安装目录>\mcps\markitdown\markitdown-mcp.exe
说明：      把 PDF / Word / Excel / PPT 转成 Markdown（含页码 / sheet / slide 定位），
            供全文检索与阅读使用。
备注：      若 7317 被占用，本服务会自动另选端口并把实际端口写进
            <安装目录>\mcps\markitdown\state.json（只读安装目录时回落到
            %LOCALAPPDATA%\chonkpilot\mcps\markitdown\state.json），
            此时地址请以 state.json 的 port 为准。
```

### 3.2 MCP 客户端配置片段

```json
{
  "mcpServers": {
    "markitdown": {
      "type": "http",
      "url": "http://127.0.0.1:7317/mcp"
    }
  }
}
```

> 本服务**不预置**进产品；本产品不负责拉起它（拉起/注册属下一批的插件工作）。

## 4. 入口二：vfts 内部快通道（省掉 MCP 往返）

### `GET /vfts/health` —— 探活（**无需 token**）

```powershell
Invoke-RestMethod http://127.0.0.1:7317/vfts/health
# { ok: true, version: "0.1.0", pid: 12345, port: 7317, uptime_s: 12.3,
#   state_path: "…\\mcps\\markitdown\\state.json" }
```

### `POST /vfts/convert` —— 转换（**需 `X-Chonk-Token`**）

请求体：`{ "path": "<本地绝对路径>", "max_bytes": 52428800 }`（`max_bytes` 可省，
= **输入文件**字节上限，默认 50 MiB）。

成功：

```json
{
  "ok": true,
  "text": "…markdown…",
  "loc_kind": "page",
  "locs": [{ "loc": "3", "offset": 1234 }],
  "truncated": false,
  "parser_version": "markitdown-mcp/0.1.0 (markitdown/0.1.8)",
  "elapsed_ms": 42
}
```

失败（`ok:false`，HTTP 200；`path` 缺失等请求格式问题为 400）：

```json
{ "ok": false, "error_code": "unsupported_format", "message": "…" }
```

| `error_code` | 触发条件 |
|--------------|----------|
| `unsupported_format` | 扩展名不在支持集；或旧版二进制 `.doc/.xls/.ppt`；或缺转换依赖 |
| `encrypted` | 加密文档（加密 PDF；加密 OOXML 实为 OLE/CFB 容器） |
| `too_large` | 输入文件超过 `max_bytes`（默认 50 MiB） |
| `scan_only` | PDF 无文本层（扫描件/图片 PDF）——**本服务不做 OCR** |
| `parse_error` | 其余解析失败（含文件头与扩展名不符） |
| `not_found` | 路径不存在、非普通文件，或不是受支持的 URI |
| `timeout` | 单文件转换超过 60 s |

> `401` 的响应体 `error_code` 为 `unauthorized`（传输层，不属上表转换错误枚举）。

字段语义：

- `loc_kind` ∈ `page`（PDF）/ `sheet`（xlsx）/ `slide`（pptx）/ `none`（docx 及文本类）。
- `locs[]`：`loc` = 页码 / sheet 名 / slide 序号；`offset` = 该定位在 `text` 中的**起始偏移**。
  **口径 = UTF-8 字节偏移**（与 Go 索引侧按字节切块一致；中文等多字节字符下与"字符偏移"
  相差数倍，故一律用字节）。仅在非空时返回 `locs`。
- 输出 Markdown 上限 2 MiB（UTF-8 字节）：超出即**截断**并置 `truncated: true`（按字符边界截断）。

调用示例（Python，避免 PowerShell 引号问题）：

```python
import json, urllib.request
state = json.load(open(r"src\mcps\markitdown\state.json", encoding="utf-8"))
req = urllib.request.Request(
    f"http://127.0.0.1:{state['port']}/vfts/convert",
    data=json.dumps({"path": r"E:\docs\report.pdf"}).encode(),
    headers={"Content-Type": "application/json", "X-Chonk-Token": state["token"]},
)
print(json.load(urllib.request.urlopen(req)))
```

## 5. 构建（PyInstaller 冻结）

```powershell
.\src\mcps\markitdown\build-mcps.ps1            # onedir（默认，冷启动快）
.\src\mcps\markitdown\build-mcps.ps1 -Onefile   # onefile（单文件，冷启动慢）
```

- **独立脚本**（PyInstaller 冻结），**不接入** `build-desktop.ps1` 的必经路径（冻结慢）。
- **冻结模式**：**默认 `--onedir`**（目录模式 `exe` + `_internal/`，冷启动快）——脚本会把
  PyInstaller 产出的子目录**平铺**到 `dist\desktop\mcps\markitdown\`（`markitdown-mcp.exe` 落在该目录
  **根下**，状态文件 = 与 exe 同目录，保持投放布局一致）；`-Onefile` 走单文件模式（分发方便，但冷启动慢）。
- 产物：`dist\desktop\mcps\markitdown\` 下的 `markitdown-mcp.exe`（onedir 另含 `_internal\`）
  + `README.md` + `THIRD-PARTY-NOTICES.txt`。**投放时 exe 与 `_internal\` 须一并保留相对布局**。
- 前置：已创建 `.venv` 并 `pip install -r requirements.txt`。
- **实测（2026-09-29，Python 3.14，本机，onedir 默认产物）**：目录总计 **≈168.2 MB**（1112 个文件；
  `markitdown-mcp.exe` 单独 **17.0 MB**，其余 ≈151.2 MB 在 `_internal\`）；**冷启动到 `/vfts/health` 可用 ≈ 4.4 s**
  （4 次实测 3.9–4.6 s，无自解压步骤；首次运行因系统对新建 exe 做首扫会到 ≈14 s，之后稳定在 ~4.4 s）。
  对照 onefile = **单文件 80.0 MB**、**冷启动 ≈ 9.6 s**（需先把载荷解包到临时目录，占其中约 9 s）。
  服务起来后单文件转换为毫秒级，实测 docx 255 ms / PDF 26 ms。
- ⚠️ **杀软误报**：PyInstaller `--onefile` 产物为自解压式可执行文件，部分杀软会启发式误报，
  需要时对 `markitdown-mcp.exe` 加白名单；`--onedir`（默认）不触发该启发式。

## 6. 支持 / 不支持的格式与降级行为

| 类型 | 扩展名 | 结果 |
|------|--------|------|
| PDF | `.pdf` | ✅ 抽取文本；有文本层时给**页码**定位；无文本层 → `scan_only` |
| Word | `.docx` | ✅ 抽取（标题/段落/表格）；`loc_kind=none` |
| Excel | `.xlsx` | ✅ 每个 sheet 一节（`## <sheet>`）；给 **sheet 名**定位 |
| PowerPoint | `.pptx` | ✅ 每页一节（`<!-- Slide number: N -->`）；给 **slide 序号**定位 |
| 文本类 | `.txt/.text/.md/.markdown/.rst/.log/.ini/.toml/.yaml/.yml/.csv/.json/.jsonl/.xml/.html/.htm` | ✅ 原样/转 Markdown；`loc_kind=none` |
| 旧版二进制 | `.doc/.xls/.ppt` | ❌ `unsupported_format`（不支持，不降级尝试） |
| 扫描件 / 图片 PDF | `.pdf` | ❌ `scan_only`（**不做 OCR**） |
| 加密文档 | 同上 | ❌ `encrypted` |
| 其他 | 任意 | ❌ `unsupported_format` |

## 7. 已知限制

- **不做 OCR**：无文本层的 PDF 一律 `scan_only`，不返回内容。
- **不支持旧版二进制 Office**（`.doc/.xls/.ppt`）：需先另存为新格式。
- **PDF 定位是 best-effort**：markitdown 对“表单型”页面会改写文本，锚点可能失配，
  此时 `offset` 退化为顺序游标位置（页码本身仍正确）。
- **docx 表格**：markitdown/markdownify 对无表头表格会补一行**空表头**（`|  |  |`），
  表头分隔行随之而来；正文内容不受影响。
- **`offset` 语义 = UTF-8 字节偏移**（非字符/码点），与索引侧按字节切块口径一致；
  中文等多字节字符下二者相差数倍。
- **并发**：`/vfts/convert` 走 `--workers` 线程池（默认 4）；超时以 `timeout` 返回，
  但底层转换线程无法真正中断（markitdown 无取消接口），仅结果被丢弃。
- **远端抓取**（MCP 工具的 `http(s)://`）仅在回环可达范围内使用，抓取上限与本地一致。

## 8. 自测

```powershell
cd src\mcps\markitdown
.\.venv\Scripts\python.exe selftest.py
```

- 现场生成样本到 `samples/generated/`：**中文** docx / xlsx / pptx + 2 页 ASCII PDF（手写最小 PDF，覆盖 **PDF 页码定位**；
  中文不便于手写 PDF 内嵌字体，中文质量由前三个样本覆盖），逐个转换并**断言**关键中文词句、
  `loc_kind` 与 locs 内容；另含负例（`.doc` → `unsupported_format`、超限 → `too_large`、缺文件 → `not_found`）
  与生命周期用例（状态文件写入 / 优雅退出删除 / 不误删他人状态文件）。
- 把真实 PDF / Office 放进 `samples/`（不递归 `generated/`）后再跑，会逐个转换并打印结果摘要，
  作为抽取质量基线（不做断言）。

## 9. 依赖与许可

- 依赖与版本钉在 `requirements.txt`（含每项许可）。
- 产物内第三方组件许可汇总见 `THIRD-PARTY-NOTICES.txt`（随产物投放，无 GPL/AGPL 传染性组件）。

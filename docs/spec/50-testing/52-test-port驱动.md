# 52 · test-port驱动（测试模式设计，`--test-port`）

> 状态：✅ **已实施**（端点见 §下方清单；现行实现 `chonkpilot-framework/host/src/gui/testserver.go`，宿主接线 `src/lib/gui/host/main.go`；**2026-11-04 核对**）
> 关联：[50-测试体系](50-测试体系.md)（准则/分层/驱动范式）· [51-FP与测试映射](51-FP与测试映射.md)（脚本资产）· [62-命令行与参数](../60-reference/62-命令行与参数.md)（`--test-port`）
> **现行端点**：`/ping` `/eval` `/click` `/input` `/text` `/html` `/exists` `/console` `/screenshot` `/publish` `/wait-event` `/quit`（仅监听 127.0.0.1）；`/call` 已废除。
>
> 目的：让外部脚本（python 等）驱动 GUI 做 E2E 验证 —— 发消息、模拟人的输入/点击、读取 DOM 显示内容。

## 1. 结论先行：可以实现

本地 fork 的 go-webview2（同级仓 `chonkpilot-webview2/webview.go`）已具备两条能力，**无需 Chromium CDP / playwright**：

| 能力 | 接口 | 位置 |
|---|---|---|
| 同步执行 JS 并返回 JSON 结果 | `ScriptEval.EvalWithResult(script, timeout)` | `chonkpilot-webview2/webview.go#L675-L701` |
| 页面截图（PNG） | `Screenshot(timeout)` | `chonkpilot-webview2/webview.go#L711-L741` |

"模拟输入、点击"与"获取 DOM"本质都是**在页面里执行 JS**（`el.click()` / `el.value=...; dispatchEvent(input)` / `el.textContent`），`EvalWithResult` 一条通道全覆盖。只需新增一个真实 TCP listener（`--test-port`）把外部指令转发成 JS eval。

## 2. 总体设计

- **触发**：`chonkpilot.exe --test-port=2345`（仅桌面宿主 debug 构建生效；executor 模式已于 2026-11-02 退役，`--prompt` 参数不存在）。**🆕 2026-10-10**：`--test-port` 由 **build tag `debugport`** 门控 —— **仅 debug 构建存在**（`build-desktop.ps1 -Debug`，见 [03 §1.4](../00-overview/03-构建与部署.md)）；**release 构建不注册该参数**（传入即 unknown flag），也**不开任何监听**。
- **监听**：`127.0.0.1:2345`，**不绑定 `0.0.0.0`**，杜绝局域网访问
- **默认关闭**：不传 `--test-port` 则完全不启动，发布包行为不变
- **生命周期（2026-11-04 核对）**：宿主 `onProjectWired` 时 `AttachTestServer` 起步，仅绑定**进程内第一个项目主窗口**；`ts.Shutdown` 在宿主收尾依赖序中执行（`src/lib/gui/host/main.go`）。对话窗口经 `RegisterChat`/`UnregisterChat` 动态登记，不另起监听。
- **线程模型**：HTTP handler 在 net/http goroutine 中调用 `EvalWithResult`（其内部 `Dispatch` 到 WebView2 UI 线程执行并阻塞等回调，无死锁，见 `chonkpilot-webview2/webview.go#L684-L691`）；**并发请求用互斥锁串行化**（UI 线程单飞）
- **页面就绪门控**：首次导航完成前（复用现有导航完成回调），`/eval` 等端点返回 **HTTP 200 + `ok:false` / `error:"page not ready"`**；`/screenshot` 返回 **503**。
- **日志**：走 `a.logger`（测试请求与结果 Debug 级）

## 3. 指令协议（HTTP + JSON，仅 127.0.0.1）

### 3.1 万能指令 `POST /eval`

```text
POST /eval
{"js": "document.querySelector('.msg-list')?.textContent", "timeout": 5000, "window_id": "<可选>"}
```
```text
200 → {"ok":true, "result":"<ExecuteScript 返回的 JSON 编码字符串>"}
200 → {"ok":false, "error":"ExecuteScript failed: HRESULT=8004... | page not ready"}
400 → {"ok":false, "error":"bad body: ... | js required"}
```

- `result` 为 WebView2 `ExecuteScript` 的原始 JSON 字符串（字符串被 JSON 二次编码，解析时先 `json.loads` 一次）
- **无 `costMs` 字段**（2026-11-04 核对：全仓仅文档曾出现，代码未实现）
- **失败 / 页面未就绪一律 HTTP 200 + `ok:false`**；仅坏请求（body 解析失败、缺 `js`）返回 400。调用方应按 `ok` 判错，不可依赖 5xx。
- `timeout` 可选，默认 5s，上限 30s；**无 1s 下限**（`<=0` → 5s，`>30000` → 30s，1–4999ms 原样接受）。
- JS 抛异常 → `ok:false`，`error` 含堆栈文本

### 3.2 便捷指令（`/eval` 的封装，降低调用方成本）

| 端点 | 请求体 | 行为 |
|---|---|---|
| `POST /click` | `{"selector":"...", "timeout":5000}` | `el.click()`；找不到元素 → `ok:false`，`error` 含 `not found: <sel>` |
| `POST /input` | `{"selector":"...", "value":"...", "timeout":5000}` | **始终**用原生 value setter（`HTMLTextAreaElement.prototype` / `HTMLInputElement.prototype` 二选一，`Object.getOwnPropertyDescriptor(proto,'value').set.call(el,value)`），再派发 `input`/`change`（`bubbles:true`）。**无 `native` 参数**（2026-11-04 核对） |
| `POST /text` | `{"selector":"..."}` | `input`/`textarea` 返回 `.value`，其余返回 `.textContent`；再压缩空白 |
| `POST /html` | `{"selector":"..."}` | `el.outerHTML` |
| `POST /exists` | `{"selector":"..."}` | `{"ok":true, "result":{"count":n,"visible":bool}}`（`visible` = `getClientRects().length>0`） |
| `POST /console` | `{"clear":false}` | `{"ok":true, "result":{"entries":[{"t":ms,"level":"log","text":"..."}],"truncated":bool}}`；`truncated = len(entries)>=1000`；`clear:true` 时清空后返回 `[]` |
| `POST /publish` | `{"type":"...", "payload":"...", "window_id":"<可选>"}` | 本地事件优先，否则桥发布 mq；窗口面消息（`gui.window.*`）由宿主 handler 处理 → `{"ok":bool,"result":...,"errors":[...]}` |
| `POST /wait-event` | `{"type":"...", "timeout":5000, "window_id":"<可选>"}` | 阻塞等待该 type 事件；成功 `{"ok":true,"result":<event>}`，超时/未就绪 `{"ok":false,"error":"..."}` |
| `POST /quit` | —（无体） | 优雅退出（与托盘「退出」同一收尾路径），返回 `{"ok":true}`；未接线时 **503** + `quit not wired` |
| `GET /screenshot` | —（可选查询 `?window_id=`） | PNG bytes（`Content-Type: image/png`）；未就绪 **503**、不支持 **501**、截图失败 **500** |
| `GET /ping` | — | `{"ok":true,"app":"chonkpilot-gui","ready":bool}` |

统一响应包裹：`ok` / `result` / `error`（**无 `costMs`**）；业务失败统一 HTTP 200 + `ok:false`，仅坏请求 400、截图 501/503/500。

**多窗口 `window_id`（2026-11-04 补记）**：`/eval` `/click` `/input` `/text` `/html` `/exists` `/console` `/publish` `/wait-event` 请求体可带 `window_id`（取自 `gui.window.list`）；`/screenshot` 用查询参数 `window_id`。缺省 = 主窗口；未知/已关闭的 `window_id` → 400 `unknown window_id`。

### 3.3 定位策略

- 优先 CSS 选择器；元素带 `data-testid` 时直接用，否则用稳定 class / 属性
- 项目现有 DOM 未预埋 `data-testid`，**本方案不强制改造前端**；测试脚本按实际 DOM 结构写 selector
- 复杂定位（如按文本找按钮）用 `/eval` 直接写 JS，不另造语法

### 3.4 与 MQ 架构的关系（重要说明）

chonkpilot 前端为"彻底 MQ 化"（按钮无 `@click`，交互由 `/publish` 事件驱动）。因此：

- `/click` 的 `el.click()` **忠实模拟真实用户点击**；若某按钮本就不响应原生点击（MQ 驱动型），`/click` 结果与应用真实行为一致 —— 测试模式只提供"模拟人的操作"通道，不绕过业务语义
- 需要驱动 MQ 交互时，仍可组合 `/eval` 直接触发前端监听的事件或调用组件方法（白盒），文档示例以 DOM 级操作为主

### 3.5 console 输出捕获（可行，零 COM 改动）

fork 的 edge 层**未封装**原生 ConsoleMessage 事件（仅有 WebMessageReceived），但不影响需求——用 `w.Init` 注入脚本在页面脚本执行前重写 `console.*`，把消息存入 `window.__chonkConsole`：

```js
(() => {
  if (window.__chonkConsole) return;                 // 防重复注入
  const buf = { entries: [], max: 1000 };
  const push = (level, text) => {
    buf.entries.push({ t: Date.now(), level, text });
    if (buf.entries.length > buf.max)
      buf.entries.splice(0, buf.entries.length - buf.max);   // 环形截断
  };
  const fmt = args => args.map(a => {
    if (typeof a === 'string') return a;
    if (a instanceof Error) return a.name + ': ' + a.message + '\n' + (a.stack || '');
    try { return JSON.stringify(a); } catch { return String(a); }
  }).join(' ');
  ['log', 'info', 'warn', 'error', 'debug'].forEach(lv => {
    const orig = console[lv];
    console[lv] = (...args) => { push(lv, fmt(args)); orig.apply(console, args); }; // 保留原行为
  });
  // Vue 初始化常见失败路径：bundle/动态 chunk 加载失败、未捕获异常（不经 console）
  window.addEventListener('error', e => {
    const t = e.error instanceof Error
      ? e.error.name + ': ' + e.error.message + '\n' + (e.error.stack || '')
      : (e.message || 'script/asset load error: ' + (e.filename || '') + ':' + (e.lineno || ''));
    push('error', '[uncaught] ' + t);
  });
  window.addEventListener('unhandledrejection', e => {
    const r = e.reason;
    const t = r instanceof Error ? r.name + ': ' + r.message + '\n' + (r.stack || '') : String(r);
    push('error', '[unhandledrejection] ' + t);
  });
  window.__chonkConsole = buf;
})();
```

要点：

- **注入时机**：`w.Init`（= `AddScriptToExecuteOnDocumentCreated`）——每个文档创建时执行，页面重载/子 iframe 都生效；控制台捕获脚本是**独立 IIFE**，不与其他注入耦合（fork 已无 `Bind` 注入）
- **仅测试模式注入**：`--test-port` 未指定时不注入，生产行为零影响
- **读取**：`/console` 端点内部走 `EvalWithResult('JSON.stringify(window.__chonkConsole.entries)')`
- **覆盖范围**：① `console.*`（业务日志/前端报错）；② `window` 级未捕获异常与 Promise rejection（`[uncaught]`/`[unhandledrejection]` 前缀）——**Vue 初始化错误三类路径全覆盖**：render/setup/mount 阶段走 `console.error`（Vue 3 未设 errorHandler 时默认行为）、bundle/动态 chunk 加载失败走 `unhandledrejection`、script 级加载错误走 `window.onerror`。WebView2 网络/渲染层原生日志（如证书错误）不在捕获范围（fork 无该事件，需求面不涉及）
- **容量**：环形缓冲 1000 条防膨胀；`clear:true` 清空便于分段断言

## 4. 外部调用示例（python）

```python
import requests, time

BASE = "http://127.0.0.1:2345"

def r(path, **kw):
    return requests.post(BASE + path, json=kw, timeout=10).json()

# 1. 探活等页面就绪
for _ in range(50):
    if requests.get(BASE + "/ping", timeout=2).json().get("ready"):
        break
    time.sleep(0.2)

# 2. 发 LLM 消息（输入 + 点击发送）
r("/input", selector=".chat-input textarea", value="帮我列出当前目录结构")
r("/click", selector=".chat-input .send-btn")

# 3. 轮询消息列表直到出现 assistant 回复（流式）
for _ in range(60):
    txt = r("/text", selector=".msg-list")["result"]
    if "目录" in txt and "```" in txt:
        break
    time.sleep(0.5)

# 4. 断言 DOM
html = r("/html", selector=".msg-list")["result"]
assert "目录结构" in html
```

## 5. 实现要点（代码落点，2026-11-04 核对）

1. **`chonkpilot-framework/host/src/gui/testserver.go`**（`package gui`，**非 main 包**；`//go:build windows && debugport`）：
   - `type testServer struct { se webview2.ScriptEval; shot webview2.Screenshot; br Bridge; hwnd uintptr; destroy func(); quit func(); handler *appHandler; mu sync.Mutex; ready atomic.Bool; srv *http.Server; chats map[string]*testWinTarget }`
   - 构造：`newTestServer(w interface{}, br Bridge) *testServer`（w 需满足 `webview2.ScriptEval`，`Screenshot` 可选）
   - 统一 `doEval(js string, timeout time.Duration, windowID string) (string, error)`（`mu.Lock` + `EvalWithResult`；按 `window_id` 选主窗口/对话窗口目标）
   - handler 路由：`/ping` `/eval` `/click` `/input` `/text` `/html` `/exists` `/console` `/screenshot` `/publish` `/wait-event` `/quit`
   - 便捷指令内部拼接 JS 字符串（selector/value 经 `jsString` JSON 编码防注入）
   - `/console`：`JSON.stringify(window.__chonkConsole.entries)`，`clear:true` 时先清空再返回
   - 多窗口：`RegisterChat(h *windowHost)` / `UnregisterChat(windowID)` / `target(windowID)`
2. **宿主接线 `src/lib/gui/host/main.go`**：解析 `--test-port`（flag，默认 0=关闭）；`onProjectWired` 时创建 testServer → 注入 console 捕获脚本（`InjectConsoleCapture`，常量 `consoleCaptureJS` 在 `testserver.go`）+ `AttachTestServer`（`chonkpilot-framework/host/src/gui/hostapi.go`）+ `SetQuit`；宿主收尾按依赖序 `Shutdown`。release 构建由 `testserver_off.go` 提供空实现。
3. **就绪门控**：导航完成回调里 `ready.Store(true)`；对话窗口各自持 `ready atomic.Bool`（`RegisterChat` 挂 `h.onReady`）。
4. **超时**：请求体 `timeout` 仅做上限钳制——`<=0` → 5s、`>30000` → 30s；**1–4999ms 原样接受（无 1s 下限）**。
5. **并发**：`mu` 全局串行（UI 线程单飞），避免交错执行 JS。
6. **当前无 Go 单测**：framework 内无 `testserver*_test.go`；行为由 L4 脚本间接回归（2026-11-04 核对）。

## 6. 安全边界

- 仅监听 `127.0.0.1`；`--test-port` 未传则不启动任何 listener
- **Host 白名单 + 来源校验（D-03；2026-10-11 补登）**：所有端点统一校验 `Host`，仅接受 `127.0.0.1` / `localhost` / `app.localhost`（端口不限），其余 403（挡 DNS rebinding / 恶意页面直连回环端口）；**并新增 `Origin` / `Sec-Fetch-Site` 校验** —— `Sec-Fetch-Site: cross-site` 拒、`Origin` 存在且主机不在回环白名单 → 拒（跨源网页），无 `Origin` / 同源回环 `Origin` / 非浏览器（外部驱动脚本通常不带这两头）→ 放行。**防恶意网页 JS `fetch` 直连回环端口驱动 `/eval`**（见 `chonkpilot-framework/host/src/gui/testserver.go` `hostGuard`/`originAllowed`）
- 无鉴权（本机测试用途，能力等价于已在本机运行的用户进程）
- `/call/` 已废除；消息驱动通道由 `/publish` / `/wait-event` 提供
- **端点豁免清单**（测试端口固定的白名单，注册于 `testserver.go` 路由）：`/ping`（探活，免页就绪检查）、`/eval`、`/click`、`/input`、`/text`、`/html`、`/exists`、`/console`、`/screenshot`、`/publish`、`/wait-event`、`/quit`
- `--test-port` 与 `--internal` 无关联（**🆕 2026-10-10 订正**：`--test-port` 现由 build tag `debugport` 门控，**release 构建不注册该参数、不开监听**；debug 构建 `-tags debugport` 才编入，见 [03 §1.4](../00-overview/03-构建与部署.md)）

## 7. 验证用例（对接《tool-call 任务与通知设计》§九）

| 用例 | 脚本动作 | 断言 |
|---|---|---|
| 发消息链路 | `/input` + `/click` 发送 | `/text` 消息列表出现 user 气泡 |
| 流式回复 | 轮询 `/text` | assistant 气泡文本持续增长直至完整 |
| 任务行/取消（§9.4） | `/click` 任务行停止按钮 | `/exists` 确认行消失/状态变化；截图留档 |
| 取消弹框三选 | `/click` 触发 cancel → `/click` 弹框按钮 | DOM 中弹框消失、任务状态符合预期 |
| 通知 🔔（§9.3） | 后台任务完成 | `/text` 消息区出现 🔔 样式消息 |
| 前端报错捕获 | 触发一个错误场景 | `/console` 断言 entries 含 `level:"error"` 且 text 含关键字；`clear` 后为空 |

## 8. 实施状态

- [x] `testserver.go`：TestServer 结构 + 路由 + doEval + 便捷指令 + `/publish`/`/wait-event`/`/quit` + 多窗口 `window_id`
- [x] `main.go`：`--test-port` 解析 + `AttachTestServer`/`Shutdown` + 就绪门控接线（宿主 `src/lib/gui/host/main.go`；实现 `chonkpilot-framework/host/src/gui/testserver.go`）
- [x] 构建 `.\build-desktop.ps1` 验证编译通过
- [x] python 冒烟脚本按 §4 示例联调
- [ ] Go 单测：`testserver.go` 目前无 framework 内单测（L4 脚本间接覆盖）

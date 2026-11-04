# CronTab 定时任务插件设计方案（PLUGIN-CRON-1）

> 状态：✅ 已实现（2026-10-31）：代码 = `chonkpilot-plugins/crontab/src`；manifest = `src/initdata/capability/plugins/crontab/plugin.json`；构建 = `build-plugins.ps1`。
> 关联：[28-plugins](../spec/20-modules/28-plugins.md)（插件体系，权威）· [61-消息一览](../spec/60-reference/61-消息一览.md) · [2C-UI插件](../spec/20-modules/2C-UI插件.md) · [49-待实现项](../spec/40-roadmap/49-待实现项.md) · [41-未决项登记](../spec/40-roadmap/41-未决项登记.md)
> 代码落点：`chonkpilot-plugins/crontab/{src,doc}`（新）· `src/initdata/capability/plugins/crontab/plugin.json`（新）· `build-plugins.ps1`（增一步）· 前端/主仓/框架**零改动**

---

## 1. 目标与边界

### 1.1 目标

1. 新增进程外插件 **`crontab`**（exe = `chonkpilot-plugin-crontab.exe`），向 LLM 暴露**六个工具**：`crontab_add` / `crontab_update` / `crontab_list` / `crontab_enable` / `crontab_disable` / `crontab_delete`。
2. 状态栏常驻**小徽标**：显示当前定时任务数；点击 → 预览区打开任务页（同时声明 `drawer` 容器，可被拉到左/右抽屉）；数字变化由插件**主动推事件**刷新，不做轮询。
3. 任务页：列出 `简要描述 / cron 字符串 / 下次执行时间 / 状态`，并支持 **启用 / 停用 / 删除**。
4. 任务是**全局的**（不分项目），`action = {type, param}`：`type` 只有 `run`（执行命令，可选完成通知）与 `notify`（系统通知，param = HTML）两种；**没有第三个顶层参数**。

### 1.2 非目标（本期不做）

- 不做多项目隔离：任务为**本机全局**（每条任务记录创建时的 `work_dir` 仅作为默认 `cwd`）。
- 不做任务编辑（只删/停用后重建）、不做运行中手动终止（除删除时取消）、不做秒级 cron。
- 不新增任何 MQ 主题 / payload（[61](../spec/60-reference/61-消息一览.md) 不变）。
- 不复用 `task_run` / `browser` / `web` 工具：定时任务是**无来源后台**，`plugin/v1/tools.call` 要求「workdir 是 chonkpilot 项目且实例在线」三条件，后台不满足（28 §1.1）。

### 1.3 可行性结论

**整体可行，且全部在插件侧闭环**：`run` 走插件进程内的命令执行（复用 `scriptrun`）；`notify` = **Windows 系统通知（右下角系统气泡 / 通知中心）**，由插件调用系统 WinRT Toast API 弹出，**不是应用主窗口内的浮层**，也不依赖主窗口是否可见。UI 走既有插件 UI 四通道（资源 / 动作 / 事件 / 配置），状态栏徽标走 PLUGIN-UI-2 的 `ui.statusbar[].view`。

系统通知的**点击**行为受 Windows 限制（未注册应用身份时点击只关闭 / 进通知中心，不能回传任意动作），见 §6.2；其余动作与 UI 均无阻塞点。

---

## 2. 插件形态与目录

```text
chonkpilot-plugins/crontab/
├── src/
│   ├── go.mod                     # module github.com/chonkpilot/chonkpilot-crontab-plugin
│   ├── main.go                    # pluginsdk 装配：工具注册 / rpc / 资源 / 调度器启停
│   ├── config.go                  # 插件配置（默认超时、错过补偿、创建提示、通知兜底）
│   ├── store.go                   # tasks.json 原子写 + runs.jsonl 滚动
│   ├── schedule.go                # cron 解析 / 下次执行 / 1s ticker / 单任务不重入
│   ├── action.go                  # action{type,param} 解析与校验（run / notify）、描述派生
│   ├── run.go                     # run 动作（scriptrun）
│   ├── htmlx.go                   # notify 的 HTML → 纯文本
│   ├── toast_windows.go           # 系统通知（WinRT Toast，内嵌 toast.ps1）
│   ├── toast.ps1                  # go:embed 的系统通知脚本
│   ├── tools/crontab/*.tool.md    # 6 个工具契约（go:embed）
│   └── webui/{tasks.html,badge.html,config.html}
└── doc/                           # 设计/使用说明（可选）
```

- `plugin.json`：`runtime.autostart = true`、`mcp.mode = http`、`mcp.category = "crontab"`、`mcp.hot_tools = ["crontab_add","crontab_update","crontab_list","crontab_enable","crontab_disable","crontab_delete"]`、`ui.selfServe = true`。
- 数据根 = `CHONKPILOT_PLUGIN_DATA_DIR`（宿主注入 = `<宿主数据根>/plugins/crontab`，**跨重启保留**，见 `mgr.runOnce`）。
- 拉起：`plugin.ini` 存在时按清单（需加一行）；不存在时回落 `enabled.json + autostart`（零配置即生效）。

---

## 3. 数据模型

```jsonc
// <PLUGIN_DATA_DIR>/tasks.json（原子替换，0600）
{
  "version": 1,
  "tasks": [
    {
      "id": "ct_7f3a91c2",
      "cron": "*/5 * * * *",
      "action": {                                    // 任务是全局的；action 为二选一联合类型
        "type": "run",                               // run | notify
        "param": { "command": "npm run build", "notify": false }
      },
      "description": "执行：npm run build",          // 自动派生（§4.3）
      "enabled": true,
      "created_at": 1761878400,
      "created_by": { "instance_id": "...", "session_id": "...", "work_dir": "E:\\proj" },
      "next_run_at": 1761878700,
      "run_count": 12,
      "last_run": { "at": 1761878400, "ok": true, "exit_code": 0, "ms": 1834, "error": "", "output_tail": "..." }
    }
  ]
}
```

- 运行记录另写 `<PLUGIN_DATA_DIR>/runs.jsonl`（每行一条，1MB 滚动 × 3，页面只读最近 50 条）。
- 上限：任务 ≤ 200 条；`description` ≤ 80 字符；`output_tail` ≤ 4KB；`notify` 正文 ≤ 200 字。
- 配置（§7.4）走插件私有 `configStore`，**不落任务文件**。

---

## 4. 工具契约（六个工具）

### 4.0 工具清单

| 工具 | 作用 | 参数 |
|------|------|------|
| `crontab_add` | 新建任务（默认立即生效，无需确认） | `cron` + (`action` \| `notify`) |
| `crontab_update` | 修改任务 | `task_id` + (`cron`? / `action`? / `notify`?) |
| `crontab_list` | 列出任务 | `enabled`? |
| `crontab_enable` | 启用 | `task_id` |
| `crontab_disable` | 停用 | `task_id` |
| `crontab_delete` | 删除 | `task_id` |

```text
tools/crontab/
├── crontab_add.tool.md
├── crontab_update.tool.md
├── crontab_list.tool.md
├── crontab_enable.tool.md
├── crontab_disable.tool.md
└── crontab_delete.tool.md
```

每个契约的 yaml 头统一：`category: crontab` · `hot: true` · `runtime: plugin` · `output: json` · `timeout: 30`（文件名 = `name`）。

### 4.1 `action`：`{type, param}`（只有两种类型）

一条任务**要么执行命令，要么弹系统通知**，由 `action.type` 区分；**没有第三个顶层参数**。

```json
// run：执行命令（外壳固定 PowerShell；cwd 缺省 = 创建时 work_dir）
{ "type": "run",
  "param": {
    "command": "npm run build",
    "notify": false                                  // 是否完成通知：false | true | "完成提示文本"
  } }

// notify：系统通知，param 就是 HTML
{ "type": "notify", "param": "<b>该喝水了</b><br>顺便站起来走两步" }
```

| type | param | 说明 |
|------|-------|------|
| `run` | `{command, notify?}` | `command` 必填（一条 shell 命令/脚本）；`notify` = 执行完是否弹系统通知（`true` / 自定义文本 / 缺省 false，成功失败都发） |
| `notify` | HTML 字符串，或 `{html, click_url?}` | HTML 落库保留；系统 Toast 只渲染纯文本 → 弹之前**剥标签转文本**（保留换行）；`click_url`（可选，http/https）点击打开网址 |

- `param` 的 schema 写成 `type: ["object","string"]`：`run` 用对象，`notify` 可用字符串简写（= HTML）。
- **打开网页 / 播放音乐不再有独立类型**：分别写成 `run` 的 `start https://…`、`start "D:\music\a.mp3"`（默认程序打开）。
- `run.notify` 与 `notify` 类型是两回事：前者是「命令执行完顺带通知」，后者是「任务本身就是一条通知」。

### 4.2 各工具参数

```jsonc
// crontab_add —— 新建（立即生效，无需确认）
{ "cron": "*/5 * * * *", "action": { "type": "run", "param": { "command": "npm run build", "notify": true } } }
{ "cron": "0 9 * * 1-5", "action": { "type": "notify", "param": "<b>该喝水了</b>", } }

// crontab_update —— 改（省略 = 不变）
{ "task_id": "ct_7f3a91c2", "cron": "0 */2 * * *" }
{ "task_id": "ct_7f3a91c2", "action": { "type": "notify", "param": "<b>换成通知了</b>" } }

// crontab_list
{ "enabled": true }
{ }

// crontab_enable / crontab_disable / crontab_delete
{ "task_id": "ct_7f3a91c2" }
```

`cron` 为 5 段（分 时 日 月 周），本地时区；支持 `@daily` / `@hourly` / `@weekly` / `@monthly`；拒绝 6 段（无秒级）。

### 4.3 描述派生（`description`）

| type | 派生规则 |
|------|----------|
| run | `执行：<command 首行，≤60 字>` |
| notify | `通知：<HTML 去标签后的前 60 字>` |

### 4.4 返回值

```json
// add / update
{ "ok": true, "task": { "id": "ct_7f3a91c2", "description": "执行：npm run build",
  "cron": "*/5 * * * *", "next_run_at": 1761878700, "enabled": true, "type": "run" } }
// list
{ "ok": true, "tasks": [ { "id": "...", "description": "...", "cron": "...", "next_run_at": 1761878700,
  "enabled": true, "type": "notify" } ] }
// enable / disable / delete
{ "ok": true, "task_id": "ct_7f3a91c2", "enabled": false }
```

失败：`{ ok:false, error }`（cron 非法 / action 非法（type 不认识、param 缺 command、notify 的 HTML 为空）/ 任务不存在 / 超上限）。

### 4.5 同进程直连（不走轮询）

工具 handler 与调度器在**同一个插件 exe** 内：写库成功后**直接调用插件内的 `notifyChanged()`**，由插件经 `plugin.publish`（`plugin-event`）推给 UI（§7.2 / §8），UI 立即刷新徽标与列表 —— **不靠轮询**。

---

## 5. 调度内核

| 项 | 口径 |
|----|------|
| 解析 | `github.com/robfig/cron/v3`，`cron.NewParser(Minute\|Hour\|Dom\|Month\|Dow\|Descriptor)` → **5 段 + @宏**；拒绝 6 段（无秒级） |
| 时区 | 宿主本地时区（不提供按任务 tz） |
| 驱动 | 单 goroutine + **1s ticker** 扫描（任务 ≤200，O(n) 足够），不引入 cron 自带调度器 |
| 不重入 | 每任务 `running` 标记；上一轮未结束 → 本轮**跳过**并记 `skipped:overlap` |
| 并发 | 不同任务并发执行；同任务串行 |
| 超时 | run 按 `default-timeout-sec`（缺省 600s）杀**进程树** |
| 执行权限 | 与插件进程同级（不提权、不弹 UAC），与 `task_run` 一致 |
| 任务上限 | 200 条（超出拒绝，明确报错） |
| 触发 | 按 `action.type` 分派：`run` → 执行命令（可选完成通知）；`notify` → 弹系统通知 |
| 触发后 | 立刻算下一次并落盘（防止重复触发） |

---

## 6. 两种任务实现

### 6.1 run（执行命令）

| 项 | 口径 |
|----|------|
| 执行 | 复用 `scriptrun`（见 §6.4）：`command` 一条命令/脚本，外壳固定 `shell`（Windows = PowerShell），`cwd` 缺省 = 创建任务时的 `work_dir` |
| 超时 | 默认 600s（配置 `default-timeout-sec`），超时**杀进程树** |
| 结果 | 退出码 / 耗时 / 输出尾（≤4KB）写 `runs.jsonl` 与 `last_run` |
| 完成通知 | `param.notify`：`true` → 「<描述> 执行成功/失败」；字符串 → 自定义文案 + 结果；只在本任务执行结束后发一次 |
| 打开网页 / 播放音乐 | 就是命令：`start https://…` / `start "D:\music\a.mp3"`（`start` 走系统默认程序） |

> **打开网页不需要 Selenium**：定时「打开某网址」= `start <url>`。若用户要的是**网页自动化**（登录、点击、抓取），那是在 `command` 里跑脚本（或 `browser` 插件，但它的工具经 `tools.call`，后台无实例不满足准入）。

### 6.2 notify（Windows 系统通知）

**口径（2026-10-31 用户确认）**：`action.type = "notify"`，`param` 就是 HTML；触发时弹**系统级通知**（右下角系统气泡 + 通知中心），**不是应用主窗口内的浮层**，也不要求主窗口可见/在最前。

| 项 | 口径 |
|----|------|
| 触发时机 | 到达 cron 时间点直接弹（不依附任何命令） |
| 正文 | HTML（≤200 字）；落库保留原 HTML，弹之前**剥标签转纯文本**（系统 Toast 不支持富文本） |
| 标题 | 固定 `ChonkPilot 定时任务` |
| 点击 | 可选 `click_url`（http/https）：toast 用 `activationType="protocol"` 直接打开该网址（系统 Toast **只能**做这件事，不能回传自定义动作）；无 `click_url` 时点击只关闭 / 进通知中心 |
| 实现 | 插件进程调用系统 WinRT `ToastNotificationManager`（内嵌一段 `toast.ps1`，`powershell -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass` 拉起），AppUserModelID 固定 `ChonkPilot` |
| 免打扰 | 用户开启专注助手 / 关闭通知时，气泡被系统吞掉 —— 这是系统行为，插件只记一条日志，不重试 |
| 失败兜底 | `powershell` 不存在 / WinRT 不可用 / 非 Windows → 记 `gui.log` + 运行记录 `notify_error`，**不静默**；配置 `plugins.crontab.notify-fallback = "log" \| "window"`，`window` = 退回插件自绘右下角置顶窗（可选实现） |
| 不依赖主窗口 | toast 由系统渲染，应用主窗口最小化/关闭小窗口都不影响（应用进程本身仍需在跑，否则定时器不跑） |
| 创建提示 | `crontab_add` 成功后弹一条系统 toast「已创建定时任务：<描述>」，并附下次执行时间（可配 `create-toast`，默认开） |

> 备选实现：纯 Go WinRT 绑定（`github.com/go-toast/toast` 一类），可省掉 PowerShell 进程；本期先用内嵌 ps1（零新依赖、可观测、便于排障），若实测启动延迟明显再换。

### 6.3 非目标说明

- 不做 toast 上的自定义按钮 / 输入框（系统通知能力边界）。
- 不做「点击通知跳回某任务详情」——需要 AUMID 注册与协议回调，列入后续。

### 6.4 与 `task_run` 的执行实现复用

`scriptrun` 目前只存在于 `chonkpilot-plugins/task/src/lib/scriptrun`。按 [75-utility](../spec/70-conventions/75-utility.md) 判据（跨 ≥2 处 / ≥2 仓）应上收：

1. 迁到 `chonkpilot-util/scriptrun`（保留 `Run/Options` 原函数名）；
2. `task/lib/scriptrun` 改为**薄封装**调用 util（行为不变）；
3. `crontab` 直接 import util。

> 若不想在本期动 util 仓，退路 = `crontab` 直接依赖 `github.com/chonkpilot/chonkpilot-task-plugin/lib/scriptrun`（插件→插件允许，但耦合差）。**推荐前者**。

---

## 7. UI

### 7.1 manifest 声明

```jsonc
"ui": {
  "selfServe": true,
  "views": [
    { "id": "tasks", "title": "定时任务", "icon": "clock",
      "entry": "webui/tasks.html", "containers": ["preview","tab","drawer","dialog"],
      "default_size": { "width": 760, "height": 600 } },
    { "id": "badge", "title": "定时任务数", "icon": "clock",
      "entry": "webui/badge.html", "containers": ["statusbar"],
      "default_size": { "width": 96, "height": 20 } },
    { "id": "config", "title": "定时任务设置", "icon": "settings",
      "entry": "webui/config.html", "containers": ["preview","tab","drawer","dialog"],
      "default_size": { "width": 680, "height": 520 } }
  ],
  "statusbar": [ { "id": "crontab", "zone": "right", "view": "badge", "tooltip": "定时任务（点击查看）" } ],
  "actions": [ { "id": "open-tasks", "label": "定时任务", "icon": "...", "view": "tasks", "container": "drawer" } ],
  "rpc": ["ping", "status", "badge", "list", "toggle", "delete", "runs", "load", "save", "reset"]
}
```

### 7.2 徽标（`badge.html`）

- 96×20 透明 iframe，内容 = 时钟图标 + 数字（`启用任务数`）；有任务运行中 → 数字旁呼吸点。
- **事件驱动，不轮询**：页面首次加载取一次 `api/badge`；此后订阅 `plugin-event`（`ckMq`），插件在 add/update/enable/disable/delete、触发开始/结束时推 `{kind:'crontab-changed'}`，页内直接更新数字。
- 兜底：`visibilitychange` 从隐藏转可见时补取一次（防止事件丢失）。
- 点击 → `ckMq.emit('preview-tab-open', {kind:'plugin-page', plugin:'crontab', page:'tasks'})`（默认预览区）。
- 也可经 `ui.actions`（`container: "drawer"`）直接开在右侧抽屉；`tasks` view 声明了 `drawer`，布局引擎也可把它拖入左/右栏。

### 7.3 任务页（`tasks.html`）

| 区块 | 内容 |
|------|------|
| 概览 | 共 N / 启用 M / 运行中 K / 下次最近的执行 |
| 任务表 | 描述 · `cron` 字符串 · 下次执行时间（本地时间 + 相对「约 3 分钟后」）· 类型（执行 / 通知）· 状态（启用/停用/运行中/失败）· 操作（启用 / 停用 / 删除） |
| 运行记录 | 最近 50 条：时间 · 任务描述 · 结果（成功/失败/跳过）· 耗时 · 输出尾（可展开） |
| 空态 | 「还没有定时任务：在对话里让 AI 用 `crontab_add` 创建」 |

- 删除需二次确认（行内确认，不用原生 `confirm`）。
- 停用 = `enabled=false`（保留任务与历史），删除 = 移除并取消正在跑的命令。
- **事件驱动，不轮询**：页面订阅 `plugin-event` 的 `{kind:'crontab-changed'}` → 重取 `api/list`（由插件推事件，不是定时器）；页面自身操作则本地乐观更新 + 以动作返回值校正。

### 7.4 配置页（`config.html`）

`plugins.crontab.*`：`default-timeout-sec`(600) · `missed-grace-sec`(300) · `max-tasks`(200) · `allowed-work-dirs`(空=不限) · `create-toast`(true) · `notify-fallback`(`log`|`window`，缺省 `log`) · `sandbox`(false，见 §9)。

---

## 8. 接口面（零新增 MQ 主题）

| 用途 | 通道 | 说明 |
|------|------|------|
| 工具调用 | 插件自建 MCP（`mcp.AddTool` + `plugin.mcp.register`） | 契约随 exe embed |
| 页面取数/控制 | `POST /plugins/crontab/api/{list,toggle,delete,runs,badge,load,save,reset}` | 动作面原样透传，插件自校验 |
| 页面资源 | `GET /plugins/crontab/webui/*` → `plugin/v1/ui.asset.get` | `ui.selfServe` |
| 打开预览/抽屉 | 前端事件 `preview-tab-open` / `lf-layout-open-drawer` | 既有 |
| 变更广播（核心） | 插件 `plugin.publish` → `plugin-event` `{id:'crontab', payload:{kind:'crontab-changed', count}}` | 既有 topic；工具 handler 写库后**直接调用** `notifyChanged()`，同进程内即时推给 UI；**零轮询** |
| 通知 | 系统 WinRT Toast（内嵌 ps1，不经总线） | — |

结论：**61 不改**（`plugin-event` 载荷为插件自定义 payload）；`gui.log` 记 `plugin=crontab` 审计行（创建/删除/执行失败）。

---

## 9. 安全与沙箱

| 项 | 口径 |
|----|------|
| 权限 | 与宿主同用户、不提权；遇 UAC 需求 → 失败并在运行记录里给可复制命令行（同 `task_run`） |
| 来源 | 只有本应用（LLM 工具 / 插件页）能建任务；插件页动作面已校验 `X-CK-Plugin-Id` |
| 目录约束 | `run` 的 `cwd` 固定 = 创建任务时的 `work_dir`；`allowed-work-dirs` 非空时该目录必须落在白名单内，否则拒绝创建 |
| 定时炸弹风险 | LLM 可创建**周期性任意命令**且**默认立即生效**（用户已定：不加确认）→ 补偿手段：① 创建成功即弹系统 toast + 徽标数字即时 +1；② 任务页一眼可见、可一键停用/删除；③ `gui.log` 审计 |
| 沙箱 | 本期**不做**进程级沙箱（低完整性 + Job Object 属宿主 MCP 沙箱能力，插件不可复用）；`sandbox=true` 预留为「cwd 白名单 + runtime 白名单」的收紧档，完整沙箱登记 [41](../spec/40-roadmap/41-未决项登记.md) |
| 密钥 | 任务文件不存密钥；若命令里写了密钥会明文落盘（页面提示） |

---

## 10. 异常与重启恢复（强制，[71 §3.3](../spec/70-conventions/71-编程规范.md)）

| # | 问题 | 设计回答 |
|---|------|----------|
| 1 | 状态在哪 | 任务定义 / 下次执行 / 最近结果 = `tasks.json`；运行明细 = `runs.jsonl`；`running` 标记 = **内存**（崩溃即丢） |
| 2 | 受理即持久化 | 工具返回前完成 `tmp + fsync + rename`；UI 启停/删除同样先落盘再回执 |
| 3 | 非终态收口 | 启动时把所有任务视为「未运行」；上一轮若中断 → 在 `runs.jsonl` 补一条 `interrupted`，`last_run.ok=false` |
| 4 | 重启后 | 用 `now` 重算每条 `next_run_at`；**错过补偿**：`next_run_at >= now - graceSec` → 立即补跑一次，否则跳过并记 `skipped:missed`；`graceSec` 默认 300 |
| 5 | 副作用语义 | 定时触发 = **至多一次**（进程崩溃那次不重放）；`run` 本身不保证幂等（契约 `idempotent:false` 已声明）；同一任务不并发 |
| 6 | 时钟 | 依赖系统时钟；检测到系统时间回拨 > 5min → 重算全部 `next_run_at` 并记日志（不补跑） |
| 7 | 插件被禁用/卸载 | 调度随进程退出停止；任务文件保留，下次启用继续（不补跑历史） |

---

## 11. 测试计划

| 层 | 内容 |
|----|------|
| L1（Go，插件内） | cron 解析/下次执行（含 @宏、非法表达式）· `action{type,param}` 校验（type 不认识 / run 缺 command / notify 空 HTML → 报错）· **HTML → 纯文本**（换行/链接文字/实体）· 描述派生 · store 原子写/损坏文件容错 · 错过补偿策略 · 不重入（overlap 跳过）· `run.notify` 文案构造（走可注入 `notifier` 接口断言，不真弹）· `notifyChanged()` 触发（fake publisher 断言 `plugin.publish`） |
| L2（systest，`src/test/chonkpilot-gui/systest/run_crontab.py`） | 启动应用 → 经插件动作面建 1 条 `*/1 * * * *` 的 `run` 任务（`echo`）→ 断言 `/api/list` 含该任务、徽标计数 +1（**由 `crontab-changed` 事件驱动，非轮询**）、预览区可打开任务页（`preview-tab-open`）→ 停用 → 断言不再触发 → 删除 → 断言计数归零；**不改消息面**，只断言既有 topic |
| L4（人工） | 系统通知（右下角气泡 + 通知中心 + `click_url` 打开网址）；`run` 的 `start <url>` / `start <mp3>`；任务页/徽标可见性（`test_statusbar.py` 风格截图） |
| 契约 | `contract.Parse` 通过；文件名 = `name`；manifest `hot_tools` 与契约一致 |

---

## 12. 交付清单

1. **（前置）`chonkpilot-util` 上收**：`scriptrun` / `winproc` / `lockfile` / `trayconsole`（§14），`task` 侧改薄封装；跑 `go run ./cmd/utilguard` 与 `go run ./cmd/gencatalog -check`
2. `chonkpilot-plugins/crontab/**`（§2 目录）
3. `src/initdata/capability/plugins/crontab/plugin.json`
4. `build-plugins.ps1`：新增 `[10/10] crontab` + 部署清单追加 `crontab`（**唯一构建入口**：`.\build-desktop.ps1`）
5. `plugin.ini`（若产物中存在）：追加 `capability\plugins\crontab\chonkpilot-plugin-crontab.exe`
6. `docs/spec/20-modules/28-plugins.md` §3 增 `3.6 crontab`
7. `docs/spec/60-reference/64-配置项一览.md` 增 `plugins.crontab.*`
8. `docs/spec/40-roadmap/41-未决项登记.md`：登记「插件级进程沙箱」「通知点击回调（AUMID / 协议注册）」
9. `docs/spec/50-testing/51-FP与测试映射.md`：补 `run_crontab.py` 行
10. 测试：`chonkpilot-plugins/crontab/src/*_test.go` + `src/test/chonkpilot-gui/systest/run_crontab.py`
11. 收尾：`.\build-desktop.ps1`（后台，不等待）

---

## 13. 已定事项

| # | 结论 |
|---|------|
| 1 | 六个工具：`crontab_add` / `crontab_update` / `crontab_list` / `crontab_enable` / `crontab_disable` / `crontab_delete` |
| 2 | 工具参数只有 `cron` + `action`；`action = {type, param}`，`type ∈ {run, notify}`，**没有第三个顶层参数** |
| 3 | `run.param = {command, notify?}`（命令 + 是否完成通知）；`notify.param` = HTML（可选 `click_url`） |
| 4 | 任务**全局**，不分项目；`work_dir` 只作 `run` 的 cwd |
| 5 | 新建**默认生效**，不做确认；创建后弹系统 toast + 徽标计数立即 +1 |
| 6 | 变更刷新**不做轮询**：插件进程内 `notifyChanged()` → `plugin-event` → UI |
| 7 | `notify` = **系统通知**（右下角气泡 / 通知中心），点击最多打开网址（Windows 能力边界） |
| 8 | `scriptrun` 与部分 `plugins/shared` 上收 `chonkpilot-util`（见 §14） |

---

## 14. util 上收评估（`plugins/shared` + `scriptrun`）

判据 = [75-utility](../spec/70-conventions/75-utility.md)（跨 ≥2 处 / ≥2 仓 / 安全相关 / 易错 → 进 util）。
以下为**实际引用核查**结果（2026-10-31，`rg` 全仓）。

| 包 | 现状与消费方 | 处置 |
|----|--------------|------|
| `codegraph/src/engines/{treesitter,vfts}/cmd/indexer` | **死代码**：`build-codegraph.ps1` / `build-vfts.ps1` 只 `go build .`（根包 mcp-server），构建脚本与文档均无引用 | ❌ **删除** |
| `shared/indexkind` | 仅被上面两个死 indexer 引用（`index_queue` 已下线） | ❌ **删除**（用户已定） |
| `shared/pluginclient` | 仅被死 indexer 引用；与框架 `pluginsdk`（插件桥客户端）职责重叠 | ❌ **建议随删**（待确认） |
| `shared/lockfile` | 仅被死 indexer 引用（单实例文件锁） | ❌ **建议随删**；如要留作将来，则上收 util（待确认） |
| `shared/pluginhost` + `shared/pluginproto` | **活消费方**：`browser` / `desktop` 引擎（会话池 + UDS 帧协议） | ✅ **迁 `chonkpilot-framework/plugin/src/sdk/enginehost`**（host + proto 同处，与既有 `sdk/engineclient` 对称），引擎改 import |
| `shared/winproc` | spawn 属性（隐藏控制台 / Job Object kill-on-close / KillTree），`task/scriptrun` 在用 | ✅ **→ `chonkpilot-util/winproc`** |
| `shared/trayconsole` | 引擎 exe 托盘 + 控制台隐藏，browser / desktop 在用 | ✅ **→ `chonkpilot-util/trayconsole`** |
| `shared/ignore` | gitignore **I/O 外壳**（解析内核已在 `util/ignore`）+ 按 instance 规则缓存，codegraph/vfts 引擎在用 | 🔶 **本次不动**（缓存绑 instance，属插件侧；后续再拆 I/O 与缓存） |
| `plugins/task/src/lib/scriptrun` | 命令执行（runtime / env / 超时 / 杀进程树 / 输出上限） | ✅ **→ `chonkpilot-util/scriptrun`**，`task` 保留同名薄封装；`crontab` 直接 import |

约束：上收时**保留原函数名**做薄封装（禁在业务包再写一份实现）；util 侧不得新增 utilguard 黑名单函数名；`chonkpilot-util` 提交前跑 `go run ./cmd/utilguard` 与 `go run ./cmd/gencatalog -check`；框架/插件仓改 import 后各自 `go build ./...` 自检。

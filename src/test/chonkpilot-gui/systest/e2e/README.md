# 场景化 E2E（单实例 / 多阶段）

> 目的：**把「每个功能点起一次 GUI」改为「预置一个空工程 → 一次启动 → 用 LLM 工具把它建成
> 有数据工程 → 在同一实例里跑完所有可测点」**；只有跨重启的持久化用例才二次启动。
>
> `run_all.py` 默认门禁已切换为本套件（`--group all` → `run_e2e.py`）；旧套件保留可跑
> （`run_all.py --group legacy`），但不再进入默认门禁。

## 快速开始

```powershell
cd src\test\chonkpilot-gui\systest

python run_e2e.py                              # 全量：空工程 → 预置 → 有数据工程 → 重启（90 点 / 约 7 分钟 / 2 次启动；破坏性 1 点默认不跑）
python run_e2e.py --coverage                   # 旧套件覆盖台账 → e2e/COVERAGE.md（不启动 GUI）
python run_e2e.py --with-destructive           # 全量 + 破坏性（破坏性永远最后执行）
python run_e2e_destructive.py                  # 只跑破坏性测试点（独立工作区/独立 HOME）
run_e2e_all.bat                                # 常规全量 + 破坏性批（两阶段；任一失败 → 非零退出码）
python run_e2e.py --list                       # 列出全部测试点（ID / 阶段 / 功能点 / 说明 / 引用）
python run_e2e.py --init empty                 # 只测「空工程」初始化模式
python run_e2e.py --init seeded                # 只测「有数据工程」模式（含重启阶段）
python run_e2e.py --only S-FS-01,S-CHAT-01     # 只测指定测试点（可逗号/多次）
python run_e2e.py --area 文件树 --area 任务树    # 按领域过滤
python run_e2e.py -k FS -k CHAT                # 按 ID/功能点子串过滤
python run_e2e.py --max-issues 3               # 累计问题**超过** 3 个后中断（默认 0 = 记录但不中断）
python run_e2e.py --cleanup no                 # 保留工作区与独立 HOME（--keep 同义）
python run_e2e.py --cleanup ask                # 结束后交互确认是否清理
python run_e2e.py --workspace D:\tmp\e2e-ws    # 指定工作区（幂等预置；永不自动删除）
python run_e2e.py --reuse-workspace            # 复用 e2e_runs/ws_shared
python run_e2e.py --no-restart                 # 严格单次启动（跳过 restart 阶段）
python run_e2e.py --port 2345                  # 固定测试端口（默认动态分配）
```

**前置**：被测产物须为 Debug 构建（`build-desktop.ps1 -Debug`，编入 `--test-port`）。

**破坏性用例（stage=`destructive`）**：默认**不纳入全量**，行尾在 `--list` 标 `[破坏性]`；
可在同一次流程末尾执行（`--with-destructive`），或由 `run_e2e_destructive.py`/`run_e2e_all.bat`
独立跑（独立工作区 + 独立 HOME，不与常规点共享状态）。
release 构建会明确报 `SKIPPED: 未编入 --test-port`（不计失败）。

## 阶段模型（一次启动，按需二次）

| 阶段 | 时机 | 工程状态 | 典型测试点 |
|---|---|---|---|
| `empty` | GUI 首启后、预置前 | 空工程（仅 `.chonkpilot/project_spec.md`） | 空树基线 / LLM 工具建首个文件 / filesys 回环 / 会话与配置 |
| *(seed)* | empty 之后 | **经 LLM 工具 `file_write` 批量预置**（幂等：已有文件不再创建） | —（预置步骤，不计测试点） |
| `seeded` | 预置完成 | 演示工程（Markdown/Python/HTML/CSV/Unicode 目录…） | 文件树/预览页签/搜索/上传/对话/工具链/任务树/配置/知识库/记忆/插件/主题/**窗口与拖拽（含真实鼠标）**/**Ask 待办（单选/多选/超时）**/**子代理·单聊**/**群聊·多聊**/**多窗口**/**文件树交互·右键·拖拽·监听**/**排队消息·输入区上方·轮次导航**/**会话导航·状态栏·任务视图**/**插件维护·配置 UI** |
| `restart` | 同工作区第二次启动 | 同一工程 + 持久状态 | 布局占比 / **窗口尺寸** / 打开页签 / **树展开状态** / 会话标题与活动位 / 项目配置 / 主题 |
| `destructive` | 最后（默认跳过） | 同实例（或独立单跑） | **恢复出厂**（清 usr + 备份）；可破坏状态，故永远最后 |

`restart` 测试点的前置状态由 `S-PREP-01` 在第一次启动内写入（布局 `0.7/0.3`、打开
`README.md` + `src/main.py`、prj `e2e.persist`、会话标题/活动位、主题 `dark`）。

## 两种初始化模式（需求 3）

* `--init empty`：只跑 `empty` 阶段 —— 验证「空工程」的启动/空树/首次建文件/文件面/配置/会话。
* `--init seeded`：直接进入「有数据工程」（先幂等预置，再跑 `seeded` + `restart`）。
* `--init auto`（默认）：按选中测试点的阶段全跑。

**预置幂等**：`workspace.pending_plan()` 只把磁盘上缺失的文件交给 LLM 工具；
二次运行（`--workspace` / `--reuse-workspace`）时若全部存在 → 打印「跳过（不重复创建）」。
预置失败时回退直接落盘（并在日志明示），保证后续测试点仍可执行。

## 问题记录与中断（需求 4）

* 单个测试点失败 → 记一条 **issue**（含 traceback + 已产出的 evidence），**继续下一个**。
* `--max-issues N`：累计**超过** N 条后停止后续测试点（未跑的记 `SKIP: 累计问题达到阈值 → 中断`），
  报告、清理、结果确认照常执行。
* 断言首次失败即结束该测试点（后续测试点不受影响）。

## 结果确认 / 报告 / 清理（需求 1）

结束时打印固定「结果确认」块：测试点数、PASS/FAIL/SKIP、问题数（是否中断）、工作区路径
（保留/已清理）、清理策略、报告路径、`RESULT:` 退出码依据。

* 报告**始终保留**：`e2e_reports/<run_id>/report.md`（人读）+ `report.json`（机读，含逐点
  evidence / issue / 耗时 / GUI 启动次数）。
* 工作区默认 `e2e_runs/<run_id>/ws` + 独立 HOME `e2e_runs/<run_id>/home`；
  `--cleanup auto|yes` 删除，`no|--keep|--cleanup ask` 保留；显式 `--workspace` 永不自动删除。

## 目录结构

```
e2e/
  framework.py     注册表 + 阶段执行器（Runner/Env）+ 问题累计 + 报告
  workspace.py     演示工程夹具（DEMO_FILES）+ 幂等预置 + LLM 工具预置
  cases_empty.py   empty 阶段测试点
  cases_files.py   seeded：文件树 / 预览页签 / 搜索 / 上传
  cases_chat.py    seeded：会话消息 / LLM 工具调用链
  cases_task.py    seeded：任务树（真实 task_run）
  cases_data.py    seeded：配置 / 知识库 / 记忆 / 插件 / 主题 / 重启前置
  cases_window.py  seeded：窗口最大化/尺寸（宿主权威）/ 分隔条拖拽 / 真实鼠标拖拽
  cases_agent.py   seeded：ask_user 待办与超时 / 子代理·单聊 / 群聊·多聊
  cases_mw.py      seeded：多窗口（开关 / 定向控制 / 本地事件不跨窗 / 重复打开）
  cases_filetree_ops.py seeded：文件树交互/右键/拖拽/监听/8 类预览
  cases_chat_ops.py seeded：排队消息 / 输入区上方 / 轮次导航
  cases_chat_ui.py seeded：主 chat UI / 富文本输入与附件
  cases_session_ops.py seeded：会话消息面契约（不存在/幂等/错误码）
  cases_preview_ops.py seeded：预览区（Markdown/源码切换/Hex/页签固定）
  cases_ui_ops.py  seeded：会话导航 / 状态栏长任务条目
  cases_task_ops.py seeded：任务视图（tasktree / stop / 终止会话）
  cases_plugin_ops.py seeded：插件维护视图（manifest / 开关 / 配置页）
  cases_cron.py    seeded：crontab 调度语义（重启基点 / 宽限补跑 / missed+人工补跑）
                   + agent 独立子会话、主会话通知/会话未读点、own 独立会话、人工创建/立即运行
  cases_cfg_ui.py  seeded：配置 UI（项目 8 页签 / 用户配置 / 场景 / 导入导出）
  cases_toolbar_ops.py / cases_preview_tabs.py / cases_project_ops.py
                   seeded：工具栏 / 预览页签溢出 / 项目数据与提示词配置
  cases_scenario_ops.py / cases_scenario_more.py
                   seeded：场景编辑/矩阵/重载 / 项目级 system 注入 / agent 重名 / ref 提示词回环
  cases_plugin_deep.py / cases_plugin_ops.py
                   seeded：插件详情（配置页 iframe/主题/禁用面）/ 维护视图
  cases_mcp_ops.py / cases_tool_cfg.py / cases_tool_ops.py / cases_tool_progress.py
                   seeded：MCP 字段/真实拉起 / 工具异步·沙箱配置 / 工具执行 / 进度
  cases_llm_ops.py / cases_chat_scroll.py / cases_paths_ops.py / cases_memory_ui.py
                   seeded：LLM 重试·字段·收件箱 / 长会话滚动 / 工具链路径 / 记忆面板
  cases_memory_ctx.py / cases_error_semantics.py / cases_semantics_more.py
                   seeded：记忆配置 A+B / 错误终态矩阵+工具徽标 / 任务状态与提示词优化
  cases_kb_primitives.py / cases_explore_ui.py / cases_ui_reg.py
                   seeded：skill/prompt 原语回环+tool 热生效 / 资源面板四段 / UI 回归
  cases_codegraph.py / cases_history.py / cases_tool_async.py / cases_mq_scope.py
                   seeded：索引/历史/超时裁决/MQ 分发（点内自起隔离实例）
  cases_wizard.py / cases_bridge.py / cases_docs.py
                   自起隔离实例：向导自动弹出+产物 / UDS LLM 桥 / vfts 文档索引（缺件 SKIP）
  cases_destructive.py destructive：恢复出厂（默认不跑）
  cases_restart.py restart：布局 / 窗口 / 页签 / 会话 / 配置 / 主题持久化
  coverage.py      旧套件覆盖台账（扫描 run_*.py/test_*.py → COVERAGE.md）
run_e2e.py         CLI
run_e2e_destructive.py  只跑破坏性测试点
run_e2e_all.bat   常规全量 + 破坏性批
```

## 新增测试点

```python
from .framework import case
from .workspace import SEED, ensure_demo_file

@case("S-XXX-01", "领域", "功能点一句话（必填）",
      stage="seeded", order=200, fp="61 §x.y / 功能点文档编号",
      desc="方法 + 判据（会出现在 --list 与报告）")
def s_xxx_01(env):
    ensure_demo_file(env, "README.md")          # 需要预置文件时（幂等）
    env.eq("磁盘内容", env.read_text("README.md"), env.read_text("README.md"))
    env.check("条件", True, "证据")
```

约定：

1. **测试点必须写明 `feature`（功能点）**；`desc` 写清「怎么测 + 判据」，`fp` 指向功能点文档。
2. 断言用 `env.check/eq`（自动写 evidence；失败抛 `CaseFailure`）。
3. 能用消息/磁盘判定的**不依赖 DOM**；DOM 只验证渲染落点（与 51-FP与测试映射 一致）。
4. 夹具用唯一哨兵（`env.run_id`），写前断言旧值 ≠ 哨兵；测试点内自清理或使用幂等 helper。
5. 依赖重启的测试点放 `stage="restart"`，状态在 `S-PREP-01`（或本测试点前置）写入。
6. 阶段顺序：`empty` → 预置 → `seeded` → `restart`；`empty` 用例不得依赖预置文件。

## mock LLM 分支（`mock_llm.py`）

| 提示词 | 决策 |
|---|---|
| `e2eplan=<base64 JSON targets>` | 一次 `file_write`（批量 targets；预置工程） |
| `【T-E2E-READ】… e2epath=<绝对路径>` | `file_read`（工具链用例） |
| `【T-E2E-TASK】` | `task_run` → `Write-Output E2E-TASK-OK`（任务树用例） |
| `【T-E2E-SUBCHAT】… e2escenario=<id> e2eagent=<名>` | `subchat_open` 单聊（子代理） |
| `【T-E2E-GROUP】… e2escenario=<id> e2eparticipants=<a,b>` | `subchat_open` + participants（群聊调度） |

判据用**测试编号**而非裸关键词：工具结果回喂后会再次进入 mock，裸关键词（如 `e2e-task`）
可能被工具输出命中 → 无限工具循环（TOOL_LOOP_LIMIT）。

## 未覆盖（本套件边界）

* **子聊天 / 群聊窗口的 DOM**：套件断言事件面 + 会话行 + 历史（sender_agent/mentions）+ 调度状态，
  以及多窗口的开关/定向控制/事件隔离（`S-MW-*`）；独立窗口内 `.subchat-bar` / `.group-board`
  的细粒度渲染仍由旧套件（`run_subchat*.py` / `ask_center` 相关）覆盖。
* **配置「恢复出厂」（C22）**：会清空 usr 全局配置 → 已迁为 `D-CFG-01`（stage=`destructive`）；
  默认全量不跑，经 `run_e2e_destructive.py` / `run_e2e_all.bat` / `--with-destructive` 单独执行。
* **真机窗口移动/拉伸**：**已纳入** `S-WIN-03` —— 移植 `test_window.py` 的模拟鼠标
  （`SetCursorPos` + `mouse_event`）拖右边/下边/顶栏，断言外层尺寸与位置变化；输入事件不可用
  （无前台权限 / 无 hwnd）时记 `SKIP` 而非失败。其余窗口行为仍覆盖「最大化/还原 +
  宿主权威几何落库 + 跨重启恢复」。

## 旧套件覆盖台账（「原来有的不能丢失」）

* 旧套件**一个都没删**；默认门禁已切到本套件，旧套件经 `run_all.py --group legacy` 仍可执行
  （保留排查能力，不丢失的底线）。
* `python run_e2e.py --coverage` 扫描 `systest/` 全部 `run_*.py` / `test_*.py`，抽取
  `run_case("…")` / `.check("…")` 具名用例，逐套件标注「已由新套件部分纳入 / 仅旧套件」，
  写入 `e2e/COVERAGE.md`（自动生成，勿手改）。
* 当前台账：**90 个旧套件 / 446 条具名用例**，**90 个脚本（100%）**均已有新测试点对应；
  仅旧套件覆盖 = **0 个脚本**。新套件共 **113 个测试点**（`empty` 5 / `seeded` 97 /
  `restart` 10 / `destructive` 1）；旧套件保留至全量回归验证通过后即可废弃。

## 已知缺口（记录，不作本套件失败）

* `S-KB-01`：契约型项目工具 `data-knowledge-create` 落盘成功，但当前构建**未进 `tools-list`**
  （`run_sem_kb_loop.py` K3 同样失败）→ 本套件改为断言 create/read/save/delete + 磁盘/列表闭合，
  能力面暴露只记证据。
* **ask_user 超时口径变更**：工具参数 `timeout` 会被 gateway 当作**调用级超时**消费并从 `args`
  剥离 → `ask_user` 收不到；超时改由项目配置 **`ask_timeout_sec`** 生效。本套件 `S-ASK-02`
  按现行口径（配置 8s → 推荐值自动答复）断言；`run_llm.py` C14b 仍按旧口径期待
  `ask-user.expires_at`（本次实测为空），属既有用例漂移。
* **同名移动/复制的覆盖确认框**：桥把后端错误对象压平成字符串（`errors:["target already exists"]`），
  前端 `fileRequest` 取不到 `err.code` → 覆盖确认框不出现（旧 `test_filetree_dnd.py` D3 的
  `.dialog-shell` 断言实为命中前序未清弹窗）。`S-FT-03` 按可保证行为断言（后端拒绝 + 源/目标不变），
  前端确认缺失只记证据。
* **ask 问答是异步的**：提问后本轮立即 `llm-complete`；回答以**后续用户消息**投递并触发续轮。
  套件断言「待办移除 + 后续轮 + 历史含答复」，不把「同一轮阻塞等答复」当判据。
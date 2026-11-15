# 旧套件覆盖台账（自动生成，勿手改）

> 生成：`python src/test/chonkpilot-gui/systest/e2e/coverage.py`
> 口径：旧脚本**全部保留**（不删除）；默认门禁仅跑 `run_e2e.py`，旧套件经 `run_all.py --group legacy` 仍可执行。本表标注新场景套件（`run_e2e.py`）的迁移进度。`run_case`/`.check` 字面量用例被抽取计数（f-string/动态名不计）。

## 汇总

| 指标 | 数量 |
|---|---|
| 旧套件脚本 | 90 |
| 抽到的具名用例 | 446 |
| 已有新套件对应（部分覆盖） | 90 个脚本 / 446 条用例 |
| 仍仅由旧套件覆盖 | 0 个脚本 / 0 条用例 |

## 逐套件

| 旧套件 | 用例数 | 新套件测试点（部分覆盖） | 状态 |
|---|---|---|---|
| `run_ask_ui.py` | 15 | `S-ASK-01`, `S-ASK-02`, `S-ASK-03` | 部分纳入 |
| `run_chat_ui.py` | 6 | `S-CHAT-MSG-01`, `S-CHAT-FLOW-01` | 部分纳入 |
| `run_config.py` | 9 | `S-CFG-DATA-01` | 部分纳入 |
| `run_config_ui.py` | 0 | `S-CFG-UI-01`, `S-CFG-DATA-01` | 部分纳入 |
| `run_crontab.py` | 9 | `S-CRON-01` | 部分纳入 |
| `run_css_themes.py` | 0 | `S-THEME-01`, `S-PLUG-DEEP-01` | 部分纳入 |
| `run_data_shard.py` | 2 | `S-SHARD-01`, `R-SHARD-01` | 部分纳入 |
| `run_docs_gate.py` | 0 | `S-DOCS-01` | 部分纳入 |
| `run_explore_kb.py` | 0 | `S-EXPLORE-01` | 部分纳入 |
| `run_filetree.py` | 15 | `S-FT-04`, `S-FT-05`, `S-FT-06`, `R-FT-01` | 部分纳入 |
| `run_fp_extra_ui.py` | 9 | `S-SCEN-EDIT-01`, `S-SCEN-MATRIX-01`, `S-CFG-PROMPT-01` | 部分纳入 |
| `run_hist_git.py` | 0 | `S-HIST-01` | 部分纳入 |
| `run_inbox.py` | 1 | `S-INBOX-01` | 部分纳入 |
| `run_index_gate.py` | 0 | `S-IDX-01` | 部分纳入 |
| `run_llm.py` | 17 | `S-CHAT-01`, `S-CHAT-02`, `S-TASK-01` | 部分纳入 |
| `run_llm_bridge.py` | 0 | `S-BRIDGE-01` | 部分纳入 |
| `run_llm_config.py` | 2 | `S-LLM-CONFIG-01` | 部分纳入 |
| `run_llm_errors.py` | 5 | `S-LLM-RETRY-01`, `S-CHAT-FLOW-01` | 部分纳入 |
| `run_llm_fields.py` | 0 | `S-LLM-FIELDS-01` | 部分纳入 |
| `run_mcp_fields.py` | 9 | `S-MCP-FIELDS-01`, `S-CFG-DATA-01` | 部分纳入 |
| `run_memory_ctx.py` | 0 | `S-MEM-CTX-01`, `S-MEM-TREE-01` | 部分纳入 |
| `run_memory_tree.py` | 0 | `S-MEM-TREE-01` | 部分纳入 |
| `run_meta_tools.py` | 0 | `S-META-TOOLS-01` | 部分纳入 |
| `run_mq_scope.py` | 0 | `S-MQ-SCOPE-01`, `R-MQ-SCOPE-01` | 部分纳入 |
| `run_mw.py` | 0 | `S-MW-01`, `S-MW-02`, `S-MW-03`, `S-MW-04` | 部分纳入 |
| `run_paths.py` | 8 | `S-PATHS-01`, `R-PATHS-01` | 部分纳入 |
| `run_plugin_gui.py` | 0 | `S-PLUG-UI-01`, `S-PLUG-DEEP-01` | 部分纳入 |
| `run_preview_ui.py` | 9 | `S-PREVIEW-01`, `S-PREVIEW-02` | 部分纳入 |
| `run_primitive_hot.py` | 0 | `S-KB-PRIM-01` | 部分纳入 |
| `run_project.py` | 5 | `S-PRJ-DATA-01` | 部分纳入 |
| `run_project_cfg.py` | 6 | `S-CFG-UI-01`, `S-CFG-PROMPT-01` | 部分纳入 |
| `run_restore_tabs.py` | 0 | `S-FS-04`, `R-TABS-01` | 部分纳入 |
| `run_s21_matrix.py` | 0 | `S-ERR-MATRIX-01` | 部分纳入 |
| `run_scenario_agent_dup.py` | 0 | `S-SCEN-DUP-01` | 部分纳入 |
| `run_scenario_prj.py` | 0 | `S-SCEN-PRJ-01` | 部分纳入 |
| `run_scenario_reload.py` | 1 | `S-SCEN-RELOAD-01` | 部分纳入 |
| `run_sem_config_fallback.py` | 0 | `E-CFG-01`, `S-CFG-03` | 部分纳入 |
| `run_sem_config_loop.py` | 0 | `S-CFG-02`, `R-CFG-01` | 部分纳入 |
| `run_sem_error_recovery.py` | 0 | `S-ERR-MATRIX-01` | 部分纳入 |
| `run_sem_filetree_loop.py` | 0 | `S-FS-01`, `S-FS-03`, `E-FS-02` | 部分纳入 |
| `run_sem_filetree_tree.py` | 0 | `S-FS-01`, `S-FS-02` | 部分纳入 |
| `run_sem_kb_loop.py` | 0 | `S-KB-01` | 部分纳入 |
| `run_sem_kb_skill_loop.py` | 0 | `S-KB-PRIM-01` | 部分纳入 |
| `run_sem_memory_edit.py` | 0 | `S-MEM-01` | 部分纳入 |
| `run_sem_messages.py` | 0 | `S-CHAT-01` | 部分纳入 |
| `run_sem_prompt_optimise.py` | 0 | `S-PROMPT-OPT-01` | 部分纳入 |
| `run_sem_retry_state.py` | 0 | `S-ERR-MATRIX-01`, `S-TOOL-RETRY-01` | 部分纳入 |
| `run_sem_scenario_ref.py` | 0 | `S-SCEN-REF-01` | 部分纳入 |
| `run_sem_search_upload.py` | 0 | `S-SEARCH-01`, `S-UPLOAD-01` | 部分纳入 |
| `run_sem_session_loop.py` | 0 | `E-SESS-01`, `R-SESS-01` | 部分纳入 |
| `run_sem_tabs_loop.py` | 0 | `S-FS-02`, `S-FS-04`, `R-TABS-01` | 部分纳入 |
| `run_sem_task_status_loop.py` | 0 | `S-TASK-STATUS-01` | 部分纳入 |
| `run_sem_task_tree.py` | 0 | `S-TASK-01` | 部分纳入 |
| `run_sem_tool_result.py` | 0 | `S-CHAT-02` | 部分纳入 |
| `run_sem_window_layout_loop.py` | 0 | `S-WIN-01`, `S-WIN-02`, `S-SPLIT-01`, `R-LAYOUT-01`, `R-WIN-01`, `R-THEME-01` | 部分纳入 |
| `run_sem_wizard.py` | 0 | `S-WIZARD-01` | 部分纳入 |
| `run_server_deps.py` | 9 | `S-CHAT-SCROLL-01`, `S-CHAT-FLOW-01`, `S-ASK-01` | 部分纳入 |
| `run_session_active.py` | 0 | `R-SESS-01`, `S-SESS-BRIDGE-01` | 部分纳入 |
| `run_task_ui.py` | 10 | `S-TASK-UI-02` | 部分纳入 |
| `run_tool_async.py` | 0 | `S-TOOL-ASYNC-01` | 部分纳入 |
| `run_tool_async_config.py` | 0 | `S-TOOL-CFG-01` | 部分纳入 |
| `run_tool_progress.py` | 1 | `S-TOOL-PROGRESS-01` | 部分纳入 |
| `run_tool_sandbox.py` | 0 | `S-TOOL-CFG-01`, `S-MCP-FIELDS-01` | 部分纳入 |
| `run_tools.py` | 3 | `S-TOOLS-RUN-01`, `S-ASK-03` | 部分纳入 |
| `run_ui_regressions.py` | 0 | `S-UI-REG-01`, `S-PREVIEW-02`, `S-EXPLORE-01` | 部分纳入 |
| `run_wizard_auto.py` | 0 | `S-WIZARD-01` | 部分纳入 |
| `test_chat.py` | 9 | `S-CHAT-UI-01` | 部分纳入 |
| `test_chat_flow.py` | 11 | `S-CHAT-FLOW-01` | 部分纳入 |
| `test_chat_input_top.py` | 5 | `S-CHAT-TOP-01` | 部分纳入 |
| `test_config.py` | 14 | `S-CFG-UI-01`, `D-CFG-01` | 部分纳入 |
| `test_filetree.py` | 11 | `S-FS-05` | 部分纳入 |
| `test_filetree_ctxmenu.py` | 7 | `S-FT-02` | 部分纳入 |
| `test_filetree_dnd.py` | 10 | `S-FT-03` | 部分纳入 |
| `test_filetree_interact.py` | 8 | `S-FT-01` | 部分纳入 |
| `test_filetree_watcher.py` | 7 | `S-FT-04` | 部分纳入 |
| `test_layout.py` | 29 | `S-SPLIT-01`, `R-LAYOUT-01`, `S-WIN-01` | 部分纳入 |
| `test_plugin_config_e2e.py` | 31 | `S-PLUG-UI-01`, `S-PLUG-DEEP-01`, `S-CRON-01` | 部分纳入 |
| `test_plugin_manager.py` | 9 | `S-PLUG-UI-01` | 部分纳入 |
| `test_preview.py` | 5 | `S-PREVIEW-01` | 部分纳入 |
| `test_queue.py` | 12 | `S-CHAT-QUEUE-01` | 部分纳入 |
| `test_richtext.py` | 10 | `S-CHAT-RICH-01` | 部分纳入 |
| `test_screenshot.py` | 8 | `S-SHOT-01` | 部分纳入 |
| `test_session_bridge.py` | 10 | `S-SESS-BRIDGE-01` | 部分纳入 |
| `test_session_drawer.py` | 15 | `S-SESS-UI-01` | 部分纳入 |
| `test_statusbar.py` | 19 | `S-STATUS-01`, `S-STATUS-02` | 部分纳入 |
| `test_task.py` | 18 | `S-TASK-UI-01`, `S-ASK-01`, `S-ASK-02`, `S-ASK-03` | 部分纳入 |
| `test_tool_retry.py` | 4 | `S-TOOL-RETRY-01` | 部分纳入 |
| `test_toolbar.py` | 21 | `S-TOOLBAR-01` | 部分纳入 |
| `test_turn_nav.py` | 8 | `S-TURN-NAV-01` | 部分纳入 |
| `test_window.py` | 14 | `S-WIN-01`, `S-WIN-02`, `S-WIN-03`, `R-WIN-01` | 部分纳入 |

## 明细（逐套件用例标题）

### `run_ask_ui.py`（15 条）
- A1/A2 自动打开 + 左列待办 + 右侧详情 + 单选回答回链（含 dialog_id）
- A3 自定义输入回答（custom）回链
- A4 多选勾选 + 顿号连接
- A5 多待办聚合 + 逐条作答移除
- A6 子会话提问：左列显示会话标识
- A7 超时（expires_at）→ 已超时标记 + 仍可作答
- A8 跳过：不回答、留在列表
- A9 取消：以「用户选择了其他…」文案作答 + 移出列表
- A10 无 expires_at 时短时间不转 busy
- A11 选项 + 「其他」自定义文本可并存
- A12 选项 {value,label} 对象：渲染 label / 回链 value
- A13 聊一聊：内嵌面板 + 完成 → ask-resolve + 移除
- A13b 面板中切到其他 ask：仅退出，不解除
- A14 聊一聊边界：缺会话 → 提示且待办保留、不误提交
- A15 关窗/重开：待办回填 + 窗口单例

### `run_chat_ui.py`（6 条）
- E1 无会话「选择会话」
- E2 有会话无消息「暂无消息」
- E3 思考过程：实时展开/折叠/复制图标
- E4 工具调用记录 + 状态徽标 + 复制图标
- E5 后台任务完成通知（🔔）
- E6 场景下拉「通用场景」（首项/关闭场景/设为默认）

### `run_config.py`（9 条）
- 三.6 配置页 打开（preview tab）
- 三.1 LLM 列表显示
- 三.6 保存 → data-user-config-refresh 传播
- 三.1 LLM 增删改查
- 三.3 MCP 增删
- 三.5 通用参数（responseTimeout 落库）
- 四.x 项目配置页入口
- C8 配置缺字段兜底（默认值）
- C10 场景并发访问（多进程）

### `run_config_ui.py`（0 条）

（无可抽取的具名字面量用例）

### `run_crontab.py`（9 条）
- C1 crontab 插件已注册且 running
- C2 状态栏内嵌 crontab badge iframe
- C3 crontab_add 建任务
- C4 crontab_list 含新任务
- C5 crontab-changed 事件到达前端
- C6 徽标计数为 1
- C7 点徽标打开任务页
- C8 crontab_disable 后不在启用列表
- C9 crontab_delete 后任务为空

### `run_css_themes.py`（0 条）

（无可抽取的具名字面量用例）

### `run_data_shard.py`（2 条）
- S1 自举生成对话库 + 回带 dialog_id
- S2 重启幂等（不重复建库）

### `run_docs_gate.py`（0 条）

（无可抽取的具名字面量用例）

### `run_explore_kb.py`（0 条）

（无可抽取的具名字面量用例）

### `run_filetree.py`（15 条）
- 一.0 初始化
- 一.1 根目录 添加/改名/删除
- 一.2 展开目录后文件操作
- 一.3 折叠目录下操作不触发 change
- 一.4 重新展开显示最新
- 一.5 子级展开状态保持
- 一.6 收起一级目录后子级操作不触发 change
- 一.7 展开一级目录子级内容状态确认
- 一.8 8 种文件类型预览
- C1 新子目录自动监听
- C2 隐藏/临时文件过滤
- C3 Write 事件刷新（filesys.changed）
- C4 批量添加 60ms 合并去重
- C5 根目录变更整树重建
- C6 展开状态持久化（/quit 退出 + 重启恢复）

### `run_fp_extra_ui.py`（9 条）
- T1 任务不存在提示（L315）
- T3 排队消息写回输入框（L137）
- T4 总结提示词重置（L222）
- T5 索引状态可观测（L232）
- T6a 智能体基本信息字段（L257）
- T6b 智能体工具过滤（L259）
- T6c 子智能体 = agents/ 引用（选择器 + 无添加/复制按钮，L260）
- T7 智能体优化按钮已接线（L258）
- T8 智能体编辑器候选工具按级别矩阵过滤（L4，25 §4）

### `run_hist_git.py`（0 条）

（无可抽取的具名字面量用例）

### `run_inbox.py`（1 条）
- INBOX-S1 工具执行中插话（llm-inbox-add → 插话先出流）

### `run_index_gate.py`（0 条）

（无可抽取的具名字面量用例）

### `run_llm.py`（17 条）
- 五.1 调用参数确权（turn-start；旧扩展字段已废弃）
- 五.2 普通对话（流式 + 气泡落点）
- 五.3 工具调用（tool-call 链路 + 结果回填）
- 五.8 等待时 Cancel
- 五.9 Chat Message 入队/出队
- 五.12 chat 显示内容确认
- 五.13 tool-pair 显示、展开、详情
- C11 会话切换取消（llm-cancel 级联）
- C14 ask_user 双路由（Ask 中心 + 回填）
- C14b ask_user 显式超时自动回复（推荐值 + 本轮继续）
- C15 断链恢复（resumePartial 续写）
- C17 通知按 message_id 去重
- 五.4 单次工具调用（展示名 = tool_call_display_name）
- 五.5 单次工具调用（自定义展示名）
- 五.6 批量工具调用（同轮两条 tool_call → 两节点）
- 五.7 任务节点归属（主会话 / 轮次 / 结果回填）
- C13/C18 工具任务取消（task-stop）

### `run_llm_bridge.py`（0 条）

（无可抽取的具名字面量用例）

### `run_llm_config.py`（2 条）
- 三.2a 按所选 LLM 发起调用（请求配置一致）
- 三.2b 外部修改热重读（改 temperature 无需重启生效）

### `run_llm_errors.py`（5 条）
- S21 空回复实时提示（completed 无输出 → 提示 + 继续）
- RETRY-1 重试可见且最终成功（中间态 n/N + 等待秒数；无错误气泡）
- RETRY-1/ERRUX-1 重试耗尽 → 终态只在下方提示条（详情 + retry 次数）
- §一 网络状态预检（系统在线 + 端点不可达 → 正常重试；断网按 L1）
- S25 发送前校验 LLM 配置（llms 空 → 拦截 + 提示）

### `run_llm_fields.py`（0 条）

（无可抽取的具名字面量用例）

### `run_mcp_fields.py`（9 条）
- M1 全字段哨兵落库（A：env/headers/cwd/timeout/runtime/args/transport/description/enabled）
- M2 transport=stdio 真实拉起 + args 逐个传递（B）
- M3 cwd 效果：子进程真实工作目录 == 配置值（B）
- M4 env 效果：注入 + 值内 ${VAR} 展开（B）
- M5 isolate 三态（A 回读 + B 跨 work_dir 子进程同一性）
- M8 sandbox 三态（A：缺键/true/false）+ 仅 stdio 可开
- M6 hot_tools：「工具」页签加载 + 勾选 → 原名落库 → 工具面 _meta.hot（A+B）
- M9 hot_tools「全部」= [
- M7 enabled=false 保存即生效：工具面移除（B）

### `run_memory_ctx.py`（0 条）

（无可抽取的具名字面量用例）

### `run_memory_tree.py`（0 条）

（无可抽取的具名字面量用例）

### `run_meta_tools.py`（0 条）

（无可抽取的具名字面量用例）

### `run_mq_scope.py`（0 条）

（无可抽取的具名字面量用例）

### `run_mw.py`（0 条）

（无可抽取的具名字面量用例）

### `run_paths.py`（8 条）
- A 7 键哨兵回读 + 无串写
- （重启前基线）占位符已生效
- A 重启后 7 键仍一致（usr 主库持久）
- B 子进程 env 注入（不可覆盖：script_run 已删除，41 D-67）
- B 解释器真实启动（不可覆盖：script_run 已删除，41 D-67）
- B {{toolchain.*}} 占位符替换（送 LLM 的 system 原文）
- B 未配置键 → 空串
- A javaPath 回读（B 不可覆盖：script_run 已删除，41 D-67）

### `run_plugin_gui.py`（0 条）

（无可抽取的具名字面量用例）

### `run_preview_ui.py`（9 条）
- P1 标题显示文件名 + 类型徽标
- P2 文本预览
- P3 markdown 预览渲染
- P4 html 预览 iframe
- P5 图片预览
- P6 页签过多溢出（... 按钮绝对定位覆盖 + 弹框列全部 + 选中置首）
- P7 打开文件持久化（重启恢复标注）
- P8 预览选中文本 → 浮动操作条
- P9 可用宽变化 → 溢出按钮出现/消失 + 页签不被隐藏 + 恢复宽度回初始

### `run_primitive_hot.py`（0 条）

（无可抽取的具名字面量用例）

### `run_project.py`（5 条）
- 四.1 上下文压缩配置（落库 + 传播）
- 四.3 提示词编辑（save/load 往返）
- 四.4 项目安全目录（save/list/delete）
- 四.6 最近目录（gui.recent.list）
- 四.7 会话与工作目录关系

### `run_project_cfg.py`（6 条）
- K1 项目配置 8 页签（安全/上下文管理/代码知识库/文件历史/文件树/群聊/日志/配置导入导出）
- K2 安全页签：添加信任目录
- K3 上下文管理：输入 + 快速阈值 + 保存
- K4 总结提示词自动加载 + 来源标注 + 重置
- K5 代码知识库（codegraph）：插件页文案/控件/状态入口 + workdir 回填
- K6 代码知识库（vfts）：插件页文案/控件/引擎状态入口

### `run_restore_tabs.py`（0 条）

（无可抽取的具名字面量用例）

### `run_s21_matrix.py`（0 条）

（无可抽取的具名字面量用例）

### `run_scenario_agent_dup.py`（0 条）

（无可抽取的具名字面量用例）

### `run_scenario_prj.py`（0 条）

（无可抽取的具名字面量用例）

### `run_scenario_reload.py`（1 条）
- 添加场景后 ChatPanel 场景选择列表即时刷新

### `run_sem_config_fallback.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_config_loop.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_error_recovery.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_filetree_loop.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_filetree_tree.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_kb_loop.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_kb_skill_loop.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_memory_edit.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_messages.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_prompt_optimise.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_retry_state.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_scenario_ref.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_search_upload.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_session_loop.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_tabs_loop.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_task_status_loop.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_task_tree.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_tool_result.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_window_layout_loop.py`（0 条）

（无可抽取的具名字面量用例）

### `run_sem_wizard.py`（0 条）

（无可抽取的具名字面量用例）

### `run_server_deps.py`（9 条）
- B6 L337 ask_user 任务问题内容
- B2 L128 滚顶加载更早消息 + 位置保持
- B3 L127 自动滚动暂停/恢复
- B4 L140/141 切会话持久化与恢复
- B7 L151 点击消息→tasktree 联动
- B8 L148 turn 未完成加载标记
- B9 子会话 viewer（L142/299/311/318/320/321）
- B10 L149 过长内容「+更多」
- B1 L125 顶部指示器（加载中/已到最早）

### `run_session_active.py`（0 条）

（无可抽取的具名字面量用例）

### `run_task_ui.py`（10 条）
- G1 空任务树「暂无会话」
- G2 节点创建 + running 转圈 + 类型图标
- G3 完成/失败状态图标
- G4 llm 会话节点运行耗时
- G5 运行中节点 stop 按钮（默认隐藏 + :hover 规则可达）
- G5b 长标题横向不裁剪 / 无水平溢出
- G5c 多层嵌套缩进 + 面板滚动到底末节点可见
- G6 点击任务节点查看详情（PID/命令/参数）
- G7 详情底部操作按钮
- G8 关闭（逻辑删除）后节点只读可见 + 子节点在 + 无动作

### `run_tool_async.py`（0 条）

（无可抽取的具名字面量用例）

### `run_tool_async_config.py`（0 条）

（无可抽取的具名字面量用例）

### `run_tool_progress.py`（1 条）
- mcp-tools-progress → 工具条目 title 三态（40% / 无事件不显示 / 无 total 不显示百分比）

### `run_tool_sandbox.py`（0 条）

（无可抽取的具名字面量用例）

### `run_tools.py`（3 条）
- task_run 同步（stdout + 任务节点）
- task_run 异步（转后台 + tool_result）
- ask_user 多选 + 推荐（顿号回填）

### `run_ui_regressions.py`（0 条）

（无可抽取的具名字面量用例）

### `run_wizard_auto.py`（0 条）

（无可抽取的具名字面量用例）

### `test_chat.py`（9 条）
- H1 标题栏：主聊天文字 + 新建会话按钮
- H2 场景 tag 显示（非空且非
- H3 场景选择列表项 ≥1
- H4 会话 tag：#前8位 + title 完整 id
- H4b 点击 session tag 不崩
- H6 输入区 富文本 + 发送 + 思考/努力度
- H5 新建会话清除会话 tag
- H7 发送放行 → message-send 载荷 = 输入文本 + 所选 LLM
- 无前端错误

### `test_chat_flow.py`（11 条）
- CF1b 空闲发送先入 inbox → 服务端起轮（turn-start 回填）
- CF1 发送即显 user 气泡 + 忙碌 Cancel
- CF2a 思考过程渲染
- CF2b 思考过程折叠
- CF2c 思考过程展开
- CF3 markdown 渲染 strong/code
- CF4 轮次结束：pending 清除 + 恢复 Send
- CF5 空回复 → 提示 + 继续按钮
- CF6 不可恢复错误 → 只在下方提示条（摘要+详情+retry 次数）+ 继续按钮，聊天流无气泡
- CF7 会话进行中自动滚到底部
- 无前端错误（消息列表交互组）

### `test_chat_input_top.py`（5 条）
- IT1 任务进度条 (1/3 + 1 failed)
- IT2 通知提示条弹出
- IT3 忙碌暂存 → turn 后弹出
- IT4 未完成不静默续写（末条 user 仍为原消息）
- 无前端错误（输入区上方组）

### `test_config.py`（14 条）
- C1 项目配置页打开 + 8 页签
- C1b 日志页签（级别选择 + 日志目录入口）
- C2 security 页签 + 表头 + 空态
- C3 security 添加按钮 → 新增行 + 输入框
- C3b security 目录输入（本地态，无错误）
- C5 用户配置页打开 + 表格 + 添加按钮
- C5b 添加 LLM → 编辑对话框打开（不崩）
- C6 场景配置 tab 打开
- 无前端错误（config 组）
- C20 配置导入/导出页签（导出/导入/恢复厂三区 + 密钥警示 + 排除密钥默认开）
- C21 导入非法 JSON → 人话提示 + 可展开原始详情
- C21b 导入确认弹窗写明影响范围（键覆盖 + 数量/键名）
- C21b 取消导入 → 不写入、无结果提示
- C22 恢复出厂二次确认 + 清空前自动备份（backup/ 快照 + 提示路径）

### `test_filetree.py`（11 条）
- F1 项目目录名显示
- F1b hover 提示=全路径
- F2 VCS 图标按目录显示
- F3 配置图标打开项目配置页签
- F4 文件树加载出节点
- F5 目录展开折叠
- F6 单击节点 preview 临时页签
- F7 右键菜单弹出
- F8 节点重命名
- F9 Delete 删除确认
- F10 拖拽移动/复制

### `test_filetree_ctxmenu.py`（7 条）
- C1 菜单含 Show in Console/Copy
- C1b 无剪贴板时不含 Paste
- C2 复制后右键目录含 Paste
- C3 黏贴复制文件到目标目录
- C4 多选右键隐藏单选项（Open/Rename）
- C5 菜单含 Show in Console
- 无前端错误

### `test_filetree_dnd.py`（10 条）
- M1 Ctrl 点击多选（2 个节点）
- M2 普通单击清多选（退化为单选）
- M3 多选 Delete 弹出确认框
- D1 拖拽 a.txt → a 目录（文件系统移动成功）
- D1b a 目录内出现 a.txt（DOM 刷新）
- D2 拖拽 a/a.txt → b.txt（移入根，恢复）
- D3 同名冲突弹确认框
- D3b 取消后源文件未移动
- D4 拖到 chat 插入全路径
- 无前端错误

### `test_filetree_interact.py`（8 条）
- F6a 单击文件打开临时页签
- F6b 临时页签出现（is-temporary）
- F6c 临时页签覆盖（数量仍1，内容=b.txt）
- F6d 双击转固定页签（a.txt 非临时）
- F8a 再次单击已选中节点触发改名（输入框出现）
- F8b ESC 取消改名（输入框消失）
- F8c F2 触发改名
- F9a Delete 键弹出删除确认框

### `test_filetree_watcher.py`（7 条）
- V0 .git 目录 → git 图标显示
- W1 顶层新建文件自动出现
- W2 顶层删除文件自动消失
- W3 展开目录内新建文件自动出现
- W4 文件修改（write）不崩
- W5 展开目录内删除文件自动消失
- 无前端错误（watcher 组）

### `test_layout.py`（29 条）
- L0 窗口几何基线 = 产品默认 1280x800（物理像素；DPI=%.2f）
- L0b 任务区常显（SessionTree/SessionChat 恒挂载）
- L0c 默认形态 = 自由布局容器（free）
- L1 六区布局存在
- L1b 分隔条（.split-handle）存在
- L1c 分隔条本体 1px 发丝线（真实渲染高 == 1）
- L1c 分隔条常态色 == --lf-border 实解析 rgb
- L1c 命中区宽于本体（±1.5px 命中 / ±4px 落到相邻 pane）
- L1c 激活态（.active）：命中区填 --lf-accent 实解析 rgb + 本体不加宽（恒 1px）
- L2 filetree 宽度（默认占比推导）落在合理区间
- L3 旧尺寸键写入（gui.ui.save layout）仍可用
- L4 chat 宽度（默认占比推导）落在合理区间
- L5 旧 chat 尺寸写入（gui.ui.save layout）仍可用
- L6 任务/对话面板高度可用（>200px）
- L7 tasktree(session-tree) 宽度落在合理区间
- L8 拖拽分隔条改变 chat 宽度
- L8b 拖拽中分隔条本体不加宽（恒 1px，无重排/抖动）
- L11 隐藏文件树（v-show 保活；分割条随之消失）
- L11b 恢复显示文件树（同一实例、占比/分割条恢复）
- L12a 种子档案「简洁」已装载
- L12 切换到「简洁」：可见区域集合 = {chat}（单叶 + 仅 chat）
- L12b 切换布局不重建单例 view（explorer 挂载次数不变）
- L13 重复打开单例 view 被拒绝（返回 false；落点数不变）
- L9a 保存布局（gui.ui.save layout.free）
- L9b 布局落库回读（layout.free 子键）
- L9 重启恢复布局（layout.free 回灌）
- L10a 保存窗口状态（gui.ui.save window；宿主权威校正）
- L10c 最大化时落盘 = NormalPosition（最大化前还原边界）+ maximized=true
- L10 超屏正常化：窗口被收进可见区域（与所在屏可用区真交叠）

### `test_plugin_config_e2e.py`（31 条）
- A0 基线：全部插件进入 running
- A1 打开插件维护视图
- A2-%s 主题切换生效
- A2-%s 插件一览采样
- A2-%s 插件一览：主题生效 + 卡片数 = 插件数
- A3-%s 卡片背景 = --panel-bg（主题 token）
- A4-%s 卡片文字对比度 ≥ 4.5（正文/说明/命令）
- A5-%s 卡片布局：字号(14/12)+无横向溢出+宽度合理
- A6-%s 启用开关轨道色 = 主题 accent/border
- A8-%s 状态栏插件 badge 主题同步 + 文字对比度（%d 个）
- B0 有配置页的插件清单（>=9）
- B1-%s 配置页打开（%s）
- B1-%s 配置页加载：iframe 就绪 + 控件/文案非空
- B2-%s-%s 主题切换
- B2-%s-%s 页面主题同步
- B2-%s-%s 配置页背景随主题（body bg = %s 主题底色）
- B3-%s-%s 配置页无横向溢出 + 尺寸合理
- B4-%s/%s 详情页加载
- B5-%s/%s 详情页背景随三主题正确
- C1-%s 关闭开关 → 卡片 is-off + 插件 disabled/stopped
- C1b-%s 禁用落库（enabled.json=false，重启后仍禁用）
- C2-%s 禁用后工具面移除该插件 %d 个工具
- C2-%s 禁用后状态正确（该插件无 MCP 工具）
- C3-%s 禁用后配置资源不可用（HEAD 404）
- C4-%s 禁用后 RPC 失败（功能面关闭）
- C5-%s 重新启用 → 卡片恢复 + 插件 running
- C5b-%s 启用落库（enabled.json=true）
- C6-%s 启用后工具面恢复 %d 个工具
- C7-%s 启用后配置资源恢复（HEAD 200）
- C8 禁用后打开配置页 → 「插件未就绪」占位（含状态说明）
- C9 无前端错误（全程）

### `test_plugin_manager.py`（9 条）
- PM1 插件维护视图卡片数 == plugin-list
- PM2 每张卡片：名称/启动命令/配置按钮与 manifest 一致
- PM3 动作按钮与 manifest 注册一致（codegraph 含索引进度）
- PM6a 关闭启用开关 → 卡片 is-off
- PM6b 重新启用 → 卡片恢复
- PM4 索引进度 → 抽屉（标题=视图标题 + 插件页 iframe）
- PM4 索引进度 → 抽屉
- PM5 配置按钮 → 预览区打开插件配置页
- PM7 无前端错误（插件维护组）

### `test_preview.py`（5 条）
- P1 markdown 预览渲染（h1/strong/code）
- P2 源码/预览切换
- P3 二进制文件 → hex 视图
- P4 临时 tab 双击转固定
- 无前端错误（preview 组）

### `test_queue.py`（12 条）
- Q0 无排队时摘要条不显示
- Q1 忙碌态显示入队按钮
- Q1b 忙碌 turn 已在跑（session-running-list 命中）
- Q2a 入队 1 条 → 摘要条首行 = 排队消息甲
- Q2b 入队 1 条 → 计数 = 1
- Q2 队列徽章 = 2
- Q3 队列 popover 2 项
- Q3b popover 条目顺序 = 入队顺序
- Q4 撤回 → 文本写回输入区 + 队列剩 1
- Q4b 未撤回项保留（甲）
- Q5 删除 → 队列清空（pending=0 且摘要条持续消失）
- 无前端错误（排队消息组）

### `test_richtext.py`（10 条）
- R1 富文本输入区 + placeholder
- R2 输入后发送按钮可用
- R3 Shift+Enter 换行不发送
- R4 粘贴图片 → 缩略图附件（回执后 pending 清除）
- R5a 拖入文件 → 文件 chip
- R5b 附件可删除
- R6 附件 api 上传落盘（tmp/uploads）
- R7 发送序列化（![名](路径)）→ message-send 载荷含图片标记 + 文本
- R8 写回附件标记 → 缩略图回显 + 文本无标记
- 无前端错误（富文本组）

### `test_screenshot.py`（8 条）
- S0 未声明 vision → 截图禁用 + 点击无 overlay
- S0b 声明 vision → 截图按钮可用
- S1 截图按钮存在
- S2 截图预览 overlay 出现
- S3 拖选裁剪 → 附件 + overlay 关闭
- S4 截图附件可删除
- S5 双击取消（无附件）
- 无前端错误（截图组）

### `test_session_bridge.py`（10 条）
- S1 data-session-latest 空库返回空
- S2 data-session-active-set 成功
- S3 data-session-active-get 回读写入值（不校验存在性）
- S8 data-session-list 空库空列表
- S4 data-session-get 不存在返回 null
- S5 data-session-title 不存在报错
- S6 data-session-delete 不存在幂等成功
- S7 data-session-content 空库返回空 contents
- S9 data-session-history 缺参报错
- 无前端错误（session 相关）

### `test_session_drawer.py`（15 条）
- SD1 左侧导航含记忆页签（项目·会话·记忆·扩展）+ 打开会话为空态
- SD1b 切回项目模式 → 会话面板隐藏
- SD2a session-start 建会话返回 session_id
- SD2b 会话列表 2 卡片 + 标题集 + #id
- SD3 点击卡片切换会话（页签保持打开）+ 落活动会话
- SD4 重命名会话标题更新（就地编辑）
- SD5 删除非当前会话（确认后移除，剩当前 1）
- SD5b fork → 派生成功 + 列表新增 1（不开弹窗）
- SD5c-pre0 取到保留会话 id/turn（供复制取数）
- SD5c-pre 复制源含 system 行（过滤判定有效）
- SD5c-pre1 剪贴板桩可用（否则复制断言不成立）
- SD5c 复制对话 = json-array（不含 system / 含全部对话 / 非压缩原文）
- SD6 新建会话：会话 tag 清除
- SD7 再次切到会话页签可见
- 无前端错误（会话导航组）

### `test_statusbar.py`（19 条）
- SB1 statusbar 存在
- SB2 状态栏条目化：无宿主索引徽标（已归插件，SB6 断言）+ 长任务两条目 + 无配置菜单 + 记忆入口默认隐藏
- SB2b 状态栏记忆总量入口：显示数值 + 点击弹分类列表 + 选中开内容弹框
- SB6a 状态栏内嵌插件 badge 视图（/plugins/codegraph/webui/badge.html + instance_id）
- SB6b 启用 codegraph → badge codegraph 段为插件上报状态（未初始化/索引中/完成/失败）
- SB6c 点击 badge → 预览区打开插件状态页（知识库状态 / status.html）
- SB6g 收尾还原 → badge 两枚均回「未启用」（轮询刷新生效）
- SB7a 长任务条目恒可见（记忆未启用 / 压缩空闲）
- SB7b 记忆提取进行中 → 状态条目文案 + 进行中色
- SB7c 点击记忆条目 → 预览区详情页（状态 + 最近一次任务 + 去设置）
- SB7d 记忆提取失败 → 条目错误态 + 详情错误文本
- SB7e 失败优先于 done（错误态不被「完成」洗掉）
- SB8a 压缩进行中 → 状态条目文案 + 进行中色
- SB8b 点击压缩条目 → 预览区详情页（状态 + 最近一次任务 + 去设置）
- SB8c 压缩完成 → 条目「压缩完成」
- SB3 语言切换 2 按钮 + active
- SB4 语言切换生效（active 移动）
- SB5 调试入口 + gui.devtools.open 可用
- 无前端错误（statusbar 组）

### `test_task.py`（18 条）
- T1 data-tasktree-tasks 空库返回数组
- T2 data-tasktree-tasks 按 session/top 过滤空列表
- T3 data-tasktree-list 缺 top_session 报错
- T4 data-tasktree-list 空 top 返回空 nodes
- T5 data-tasktree-delete 缺 node_id 报错
- T6 data-tasktree-delete 未知节点幂等成功
- T6b publish spy 就绪
- T8 session 切换触发 data-tasktree 查询（无错误）
- T9 tasks.started → 树节点渲染
- T10a task 节点 stop 按钮存在
- T10b 点击 stop → task-stop 发布
- T11 llm 节点 running 显示耗时
- T12 终止会话：busy 态 Cancel → llm-cancel 发布
- T13 ask-user 事件 → Ask 中心待办 + 问题
- T14a 右侧选项按钮数量
- T14b 点击选项 → 选中态
- T15 提交 → ask-user-reply 发布 + 待办移出
- 无前端错误（task 组）

### `test_tool_retry.py`（4 条）
- TR1 工具调用记录渲染（运行中）
- TR2 中断 → 重试按钮出现
- TR3 点击重试 → tool-retry 发布（{session, turn} 契约）
- 无前端错误（工具重试组）

### `test_toolbar.py`（21 条）
- T1 顶栏外壳基本元素（topbar/brand/search/win/条目）
- T1b 顶栏无知识库条目（title 定位；入口在 filetree「扩展」分段）
- T2 打开目录条目存在（publish-mq workdir-open）
- T3 最近项目下拉出现
- T3b 最近项目下拉 = dirs + 默认工作区（空态仅当两者皆空）
- T4a 配置菜单展开（机制层下拉 + 菜单叶子）
- T4 用户配置打开 preview 页签
- T5 场景配置打开 preview 页签
- T6 检索框输入出现下拉备选（file 源命中 a.txt）
- T7a 主题菜单出现
- T7b 主题选择生效并保存
- T7c 主题落库回读（usr theme=dark）
- T8 语言切换生效（localStorage locale）
- T8b 语言落库回读（usr locale=en-US）
- T9 会话入口 → 左侧导航「会话」页签打开
- T10 顶栏无三区显隐条目（默认文档不含 *-toggle 命令）
- T10b chat 区常显（无显隐 → 恒挂载）
- T10c task 区常显（无显隐 → 恒挂载；原「默认收起」口径已废止）
- S1 statusbar 存在（条目化外壳）
- S2 statusbar 条目区存在
- S3 语言保存（gui.ui.save ui）

### `test_turn_nav.py`（8 条）
- TN1 3 轮 → 3 圈
- TN2 悬停圈 → 弹用户消息一览
- TN4 13 轮 → 10 圈 + 3 置灰禁用
- TN5 悬停禁用圈不弹
- TN5b 末圈弹最后一轮消息
- TN6 点击末圈滚动到该轮
- TN7 点击首圈回顶 + 首圈高亮
- 无前端错误（turn 导航组）

### `test_window.py`（14 条）
- W1 无原生标题（frameless：outer-inner 高度差≈0）
- W2 最大化按钮 + hover 提示（title）
- W2b 最大化状态同步（title→Restore/还原）
- W2c 还原状态同步（title→Maximize/最大化）
- W3 最小化按钮 + hover 提示（title）
- W4 关闭按钮 + hover 提示（title）
- W4b 关闭按钮 hover 红色背景（#e81123）
- W5a 前端边缘命中层已渲染（8 条）
- W5 真机拖动右边框 → 宽度 +140px（±3px）
- W5b 真机拖动下边框 → 高度 +110px（±3px）
- W5c 真机拖动顶栏 → 窗口移动 (+120,+80)
- W6 Toolbar 拖拽区（-webkit-app-region: drag；仅移动语义）
- W7 Taskbar 自定义图标
- W8 Taskbar 标题=chonkpilot-<工程目录名>


// 事件通道名唯一事实源（与 Go 侧 internal/messages/messages.go 保持一致）
// 约定：两侧必须使用相同字面量值。新增事件时两端同步添加。
//
// 架构：单通道消息中心
//   - Go 所有消息经 pkg/eventbus 发出，Source != "web" 时统一广播到唯一通道 channelMsg；
//     前端 mq.js 只订阅 channelMsg，按 envelope.type 分发给各消费者（envelope.payload 即原 payload）。
//   - 前端消息经 fetch POST /publish 进入 bus（Source="web"），不回显前端（src 防环）。
export const EventNames = {
  // 唯一通道名
  channelMsg: 'msg',

  // ── Executor 事件（executor → IDE → 前端）──
  llmToken: 'llm-token', // LLM 回复文本 token 增量（原 message-chunk type=text）
  llmText: 'llm-text', // LLM 推理思考增量（原 message-chunk type=reasoning）
  llmToolCall: 'llm-tool-call', // LLM 工具调用（原 tool-call）
  toolResult: 'tool-result', // 工具结果
  toolProgress: 'tool-progress', // 任务进度
  toolNotify: 'tool-notify', // 任务通知（运行中任务数）
  llmError: 'llm-error', // LLM 调用错误
  llmRetry: 'llm-retry', // LLM 重试
  error: 'error', // 轮次错误
  complete: 'complete', // 轮次完成
  toolPair: 'tool-pair', // 工具调用完整信息（后端 → 前端：开始/转异步/终态全量广播；气泡终态已由 tasks.* 驱动，保留兼容）
  toolTimeout: 'mcp-tools-timeout', // 后端 → 前端：调用超时待裁决（{instance_id, tool_call_id, task_id, tool, reason:'timeout', timeout_s, options:[detach,cancel]|[wait,cancel]}）
  toolsWait: 'mcp-tools-wait', // 前端 → 后端：等待完成 / 撤销超时（{instance_id, tool_call_id|task_id}）
  sessionNew: 'session-new', // 会话创建（主/子统一主题；子会话 payload 带 parent_session_id）
  sessionStatisticRefresh: 'session-statistic-refresh', // 会话统计刷新
  // 任务编排事件（server 编排广播，21-llm-server；task 结构字段：task_id/tool/kind/state/…）
  taskStarted: 'tasks.started', // server 任务节点建立（编排广播）
  taskUpdated: 'tasks.updated', // server 任务状态/输出更新（编排广播，节流 ≤250ms）
  taskEnded: 'tasks.done', // server 任务终态（done/error/cancelled，编排广播）
  taskStop: 'task-stop', // 前端 → 后端：用户取消任务（服务端恒按子树级联）。任务树 ▍ 发 {task_id}；工具行「停止」发 {tool_call_id}（裁决态气泡无 server 节点 id，服务端反查归一；2026-09-18 统一走本入口）
  taskClose: 'task-close', // 前端内部：关闭节点（→ data-tasktree-delete 级联**逻辑删除**；节点仍只读展示「已关闭」，42 §2 (126)）
  taskStopChoice: 'task-stop-choice', // 前端内部：llm 型节点 ▍ 停止（§〇.7 直接级联 → task-stop）

  // ── LLM 域事件（域化改造，阶段二/三；与 Go 侧 internal/messages 保持一致）──
  llmStart: 'llm-start', // 前端 → 后端：启动一轮 LLM（每 start 生成新 turn-id；前端 send/队列出队时发，替代 SendChatMessage RPC）
  llmStarted: 'llm-started', // 后端 → 前端：受理 ack（含 turn_id；notify=true = 后端通知轮次）——前端据此维护 currentTurnId + busy
  llmComplete: 'llm-complete', // 后端 → 前端：一轮终结（队列出队的唯一 ack；payload 含 turn_id/status）
  llmCancel: 'llm-cancel', // 前端 → 后端：取消当前轮（{req_id, session, turn}，21-llm-server；替代 CancelChat RPC）
  askUser: 'ask-user', // 后端 → 前端：LLM ask_user 提问（{ask-id, question, options?, custom?, session, turn, expires_at}；替代 tool-pair{user_ask}）
  askReply: 'ask-user-reply', // 用户回答 ask_user（前端 → 后端；{ask-id, answer, custom}；替代 ask-reply，附录 A）

  // ── 应用事件（IDE → 前端）──
  sessionRefresh: 'session-refresh',
  configRefresh: 'config-refresh',
  // usr 配置**下行广播**（服务方在 `data-user-config-save` / `-delete` 成功后发出；
  // **不带 instance_id** → 全局：每个窗口的桥各自转发 → **所有窗口都收到**）。
  // 用途 = 多窗口下 theme/locale 即时同步（61 §3.1 · 24 §6.5 · WIN-021）。
  userConfigChanged: 'data-user-config-changed',
  // 会话标题变更**下行广播**（服务方在 `data-session-title` **写库成功后**发出；
  // **不带 instance_id** → 全局：每个窗口的桥各自转发 → **所有窗口都收到**）。
  // 用途 = 主窗侧改会话标题 → 已开**对话窗口**标题跟随（61 §3.2 · G-48 ⑦ / #12）。
  sessionTitleChanged: 'data-session-title-changed',
  generateToken: 'generate-token',
  generateDone: 'generate-done',
  generateError: 'generate-error',
  optimizeToken: 'optimize-token',
  optimizeDone: 'optimize-done',
  optimizeError: 'optimize-error',
  codebaseStatus: 'codebase-status',
  // 文件域（filesys 直连，61-消息一览 §2）：变更统一 filesys.changed
  // （有 children = 目录批次免二次 list；无 children = 单文件 operation）。
  // 请求发 filesys.list/content/create/remove…；watch/unwatch 声明即展开/收起。
  fileChanged: 'filesys.changed', // 单文件变化/目录批次广播
  fileDirContents: 'filesys.changed', // 别名（批次与单文件同主题，订阅方按 children 有无分流）
  fileWatcherError: 'filesys.watch-error', // 轮询/监视故障广播（{work_dir, path?, error}）
  fileSearch: 'file-search', // 搜索结果选中 → 文件树定位打开（Toolbar emit / FileTree on）
  fileExpand: 'filesys.watch', // 展开目录 = 声明 watch（本地订阅处理 UI 状态 + filesys.list 读子项）
  fileCollapse: 'filesys.unwatch', // 收起目录 = 取消 watch

  // ── 前端内部事件（前端 → 前端，不经过 Go；mq.emit / mq.on / v-mq 统一使用）──
  // 会话 / 消息
  sessionChanged: 'session-changed',
  subsessionChanged: 'subsession-changed',
  sessionCancelLlm: 'session-cancel-llm',
  messageSend: 'message-send',
  messageQueue: 'message-queue', // 入队（LLM 忙碌时）：InputBox → MessageList 队列，仅前端处理，不发送
  messageCancel: 'message-cancel',
  chatQueueRestore: 'chat-queue-restore', // 写回 textarea（MessageList canceled 简化 / 撤回 → InputBox 前置）
  chatQueueAction: 'chat-queue-action', // 队列项操作（InputBox popover → MessageList：{id, action:'recall'|'delete'}）
  // 消息列表项（列表组件多实例，payload 带 id 按实例过滤）
  msgToggleCollapse: 'msg-toggle-collapse',
  msgCopyText: 'msg-copy-text',
  msgCopyTool: 'msg-copy-tool',
  msgLoadMore: 'msg-load-more',
  msgShowFull: 'msg-show-full',
  msgScrollTop: 'msg-scroll-top',
  msgScrollBottom: 'msg-scroll-bottom',
  chatSelectLlm: 'chat-select-llm',
  chatToggleThink: 'chat-toggle-think',
  chatToggleEffort: 'chat-toggle-effort',
  chatSend: 'chat-send',
  chatContinue: 'chat-continue', // 主 chat「继续」按钮（turn 未完成时发送"继续"）
  chatScreenshot: 'chat-screenshot', // 截图按钮：隐藏窗口全屏截图 → 附件
  chatInsertAttachment: 'chat-insert-attachment', // 外部（截图/拖入）注入附件到输入区
  toolRetry: 'tool-retry', // 工具重试（interrupted 的 tool_pair → 直接重跑该工具）
  toolBackground: 'tool-background', // 前端内部：工具卡片「转后台」按钮 → 触发 mq.emit('task-background', {tool_call_id})
  askUserSelectOption: 'ask-user-select-option',
  askUserSubmit: 'ask-user-submit',
  askUserSkip: 'ask-user-skip', // 跳过当前提问（不回答，放到队列尾部稍后重问）
  askUserCancel: 'ask-user-cancel', // 取消当前提问（以"终止任务待讨论"文案作为回答提交）
  // 场景
  scenarioSelect: 'scenario-select',
  scenarioOpen: 'scenario-open',
  scenarioReload: 'scenario-reload',
  scenarioRestoreDefault: 'scenario-restore-default', // 默认场景（固定 key）还原为 embed 值
  scenarioSetDefault: 'scenario-set-default', // 设为"默认选中场景"（defaultScenario，payload: { id }）
  // 配置
  configOpen: 'config-open',
  configMenuToggle: 'config-menu-toggle', // 工具栏「设置」下拉菜单开合
  configMenuSelect: 'config-menu-select', // 下拉菜单选项（payload {kind}）→ preview 开页
  projectConfigOpen: 'project-config-open',
  configSave: 'config-save',
  configDialogClose: 'config-dialog-close',
  configAddLlm: 'config-add-llm',
  configEditLlm: 'config-edit-llm',
  configDeleteLlm: 'config-delete-llm',
  configSetDefaultLlm: 'config-set-default-llm',
  configAddMcp: 'config-add-mcp',
  configAddMcpBuiltin: 'config-add-mcp-builtin', // 从系统内置项添加（MCP 配置页）
  configToggleMcp: 'config-toggle-mcp', // 启用/禁用 MCP（payload {index}）
  configEditMcp: 'config-edit-mcp',
  configDeleteMcp: 'config-delete-mcp',
  configPickFile: 'config-pick-file',
  configPickExecutable: 'config-pick-executable', // 路径配置页选择可执行文件（payload {field}）
  configResetKey: 'config-reset-key', // 继承控件「重置继承」（payload {level, field}）
  configDetectToolchains: 'config-detect-toolchains', // 路径配置页重新探测工具链
  // 工具 / 笔记
  noteAdd: 'note-add',
  noteEdit: 'note-edit',
  noteDelete: 'note-delete',
  // 全局知识库（入口 kbOpen 在状态栏；其余为维护界面内部事件）
  kbOpen: 'kb-open',
  kbView: 'kb-view',
  kbAdd: 'kb-add',
  kbEdit: 'kb-edit',
  kbDelete: 'kb-delete',
  kbRowDelete: 'kb-row-delete',
  kbSave: 'kb-save',
  kbCancel: 'kb-cancel',
  kbRebuild: 'kb-rebuild',
  kbLevelSelect: 'kb-level-select', // 知识库层级切换（payload {kind:'app'|'user'|'project'}）
  // 原语文件面板（*.type.md preview：源码/编辑）
  primSave: 'prim-save',
  primRestore: 'prim-restore',
  // 文件 / 编辑
  fileOpen: 'file-open',
  chatInsertText: 'chat-insert-text',
  previewSelectionToChat: 'preview-selection-to-chat', // 预览区选中文本浮层「添加到对话」（前端内部）
  // 预览区 tab（对话框 → preview tab 列表页的统一打开通道）
  previewTabOpen: 'preview-tab-open', // payload: { kind: 'settings'|'scenario'|'dbviewer'|'knowledge', title? }
  // 预览 tab 栏右键菜单（触发 = TabBar 菜单项 v-mq，CodeView 经 :ctx-topics 注入；
  // 订阅 = CodeView；payload {key} = 右键所在页签，缺省回落当前激活页签）
  previewTabCloseAll: 'preview-tab-close-all', // 关闭全部（关闭预览区所有已开页签：file-open 文件页签 + 功能页页签；payload 忽略）
  previewTabCloseThis: 'preview-tab-close-this', // 关闭当前
  previewTabCloseRight: 'preview-tab-close-right', // 关闭右侧
  previewTabCloseOthers: 'preview-tab-close-others', // 关闭其它
  // 代码查看 / 文件树 / 数据查看器
  codeShowSource: 'code-show-source',
  codeShowPreview: 'code-show-preview',
  codeVersionDiff: 'code-version-diff',
  hexLoadMore: 'hex-load-more',
  versionRestore: 'version-restore',
  fileCtxAction: 'file-ctx-action',
  dbBucketSelect: 'db-bucket-select',
  dbRowSelect: 'db-row-select',
  // 场景 / 会话管理
  agentCopy: 'agent-copy',
  agentOptimize: 'agent-optimize',
  agentToggleCategory: 'agent-toggle-category',
  scenarioAdd: 'scenario-add',
  scenarioEditRow: 'scenario-edit-row',
  scenarioDeleteRow: 'scenario-delete-row',
  scenarioSave: 'scenario-save',
  scenarioAddSubAgent: 'scenario-add-sub-agent',
  scenarioSelectMain: 'scenario-select-main',
  scenarioSelectAgent: 'scenario-select-agent',
  scenarioDeleteAgent: 'scenario-delete-agent',
  sessionCreate: 'session-create',
  sessionSelect: 'session-select',
  sessionRename: 'session-rename',
  sessionDelete: 'session-delete',
  // 工程分析 / LLM/MCP 编辑
  analyzeAddFrontend: 'analyze-add-frontend',
  analyzeAddBackend: 'analyze-add-backend',
  analyzeAddArch: 'analyze-add-arch',
  analyzeAddExtra: 'analyze-add-extra',
  analyzeGenerate: 'analyze-generate',
  analyzeBack: 'analyze-back',
  analyzeShowCode: 'analyze-show-code',
  analyzeShowPreview: 'analyze-show-preview',
  editLlmSave: 'edit-llm-save',
  editLlmCancel: 'edit-llm-cancel',
  editMcpSave: 'edit-mcp-save',
  editMcpCancel: 'edit-mcp-cancel',
  discoveryConfirm: 'discovery-confirm',
  discoveryCancel: 'discovery-cancel',
  sessionTreeNodeToggle: 'session-tree-node-toggle',
  // 任务活动视图（TaskView）
  taskDetailOpen: 'task-detail-open', // 任务行点击 → 右侧 SessionChat 切到任务详情视图（payload: { task_id }）
  // 设置区：安全 / 笔记 / 索引
  securityAdd: 'security-add',
  securitySelectDir: 'security-select-dir',
  securityDelete: 'security-delete',
  noteRowDelete: 'note-row-delete',
  noteOptimize: 'note-optimize',
  noteCancel: 'note-cancel',
  noteSave: 'note-save',
  codeIndexClear: 'code-index-clear',
  codeIndexReindex: 'code-index-reindex',
  codeIndexResetFailed: 'code-index-reset-failed',
  // 上下文压缩页（ContextConfig）：保存/快速阈值/优化/恢复优化
  contextSave: 'context-save',
  contextQuickThreshold: 'context-quick-threshold',
  contextOptimizePrompt: 'context-optimize-prompt',
  contextRestorePrompt: 'context-restore-prompt',
  // 状态栏 / 工具栏
  dbViewerOpen: 'db-viewer-open',
  codebaseClear: 'codebase-clear',
  codebaseReindex: 'codebase-reindex',
  codebaseRetryFailed: 'codebase-retry-failed',
  // 纯 DOM 行为：弹层/菜单容器点击仅停止冒泡（无业务动作，仅使交互可观测）
  containerClick: 'container-click',
  // ── Dialog 对话框操作（DialogShell 组件内部）──
  dialogToggleCollapse: 'dialog-toggle-collapse',
  dialogMinimize: 'dialog-minimize',
  dialogMaximize: 'dialog-maximize',
  dialogRestore: 'dialog-restore',
  dialogClose: 'dialog-close',

  // ── TabBar 页签操作（TabBar 组件内部）──
  tabSelect: 'tab-select',
  tabClose: 'tab-close',
  tabPin: 'tab-pin',
  tabMoreToggle: 'tab-more-toggle',
  tabCtxThis: 'tab-ctx-this',
  tabCtxRight: 'tab-ctx-right',
  tabCtxOthers: 'tab-ctx-others',
  tabCtxAll: 'tab-ctx-all',

  // ── Chat 操作 ──
  chatCopySessionId: 'chat-copy-session-id',

  // ── 知识库树 ──
  kbCtxAction: 'kb-ctx-action',

  // 工具栏 / 布局
  workdirOpen: 'workdir-open',
  recentDirsToggle: 'recent-dirs-toggle',
  recentDirSelect: 'recent-dir-select',
  themeToggle: 'theme-toggle',
  themeSelect: 'theme-select',
  langToggle: 'lang-toggle',
  langSelect: 'lang-select',
  windowToggle: 'window-toggle',
  guiWindowStatus: 'gui.window.status', // 统一窗口控制/查询：payload {command: null|'minimize'|'maximize'|'close'}
  guiWindowOpenChat: 'gui.window.open-chat', // 打开/激活纯对话窗口（多窗口；幂等）：payload {session_id}
  guiWindowList: 'gui.window.list', // 查已开**对话窗口**（仅 chat；主窗口不入注册表）：payload {}
  guiWindowClosed: 'gui.window.closed', // 宿主 → 前端：对话窗口关闭广播 {window_id, session_id}（解除入口占用）
  guiWindowSetTitle: 'gui.window.set-title', // **仅独立对话窗口**：标题随会话摘要变化更新：payload {title}
  guiDevToolsOpen: 'gui.devtools.open', // 状态栏调试图标 → 宿主程序化打开 DevTools（61 §1）
  windowMaximizedChanged: 'window-maximized-changed', // 后端执行最大化后广播，同步按钮图标
  sessionsOpen: 'sessions-open',
  analyzeOpen: 'analyze-open',
  chatToggle: 'chat-toggle',
  filetreeToggle: 'filetree-toggle',
  tasksToggle: 'tasks-toggle',
  // filetree 区「项目 / 知识库」双栈切换（toolbar 知识库按钮 ↔ ExplorerPane 头部联动）
  filetreeModeToggle: 'filetree-mode-toggle', // 无 payload：project ↔ knowledge 切换
  filetreeModeSelect: 'filetree-mode-select', // payload {mode:'project'|'knowledge'}
  filetreeModeRefresh: 'filetree-mode-refresh', // 知识库树手动刷新
  codebaseIndexStatusOpen: 'codebase-index-status-open',
}

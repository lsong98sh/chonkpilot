// Package messages 定义 Go 侧消息中心契约（事件常量镜像）。
//
// 架构：对称 publish/on 消息中心（前后端统一）
//   - Go → 前端：桥把总线事件转发到唯一通道 ChannelMsg（WireEvent 信封，type + JSON 字符串
//     payload）；前端 window.mq.emitRemote 按 type 分发。
//   - 前端 → Go：mq.emit(type, payload) 本地分发 + 经 POST /publish 到桥 PublishEvent。
//
// 常量值唯一准则：frontend/src/events/event-names.js（本文件头部声明为前端事件名唯一
// 权威）。本文件每个常量字符串值必须与 event-names.js 完全一致（常量名可不同，值不可改）；
// 新增/修改/删除事件须经用户确认（msg 冻结，61-消息一览 §8 测试准则）。
package messages

import "github.com/chonkpilot/chonkpilot-lib/msgkeys"

// ChannelMsg 是 Go → 前端唯一的通道名（前端 mq.js 只订阅该通道，按 envelope.type 分发）。
const ChannelMsg = "msg"

// 消息来源（src），用于防循环与职责分层。
const (
	SrcIDE      = "ide"      // IDE 组件发布（广播前端）
	SrcWeb      = "web"      // 前端发布（不广播回前端）
	SrcExecutor = "executor" // executor 事件
)

// ── 事件常量（值 = frontend/src/events/event-names.js，逐条镜像）──
const (
	// Executor 事件（executor → IDE → 前端）
	MsgLLMToken      = "llm-token"             // LLM 回复文本 token 增量（原 message-chunk type=text）
	MsgLLMText       = "llm-text"              // LLM 推理思考增量（原 message-chunk type=reasoning）
	MsgLLMToolCall   = "llm-tool-call"         // LLM 工具调用（原 tool-call）
	MsgToolResult    = "tool-result"           // 工具结果
	MsgToolProgress  = "tool-progress"         // 任务进度
	MsgToolNotify    = msgkeys.TopicToolNotify // 任务通知（运行中任务数）
	MsgLlmError      = "llm-error"             // LLM 调用错误
	MsgLlmRetry      = "llm-retry"             // LLM 重试
	MsgError         = "error"                 // 轮次错误
	MsgComplete      = "complete"              // 轮次完成
	MsgToolPair      = "tool-pair"             // 工具调用完整信息（后端 → 前端：开始/转异步/终态全量广播；兼容保留）
	MsgSubsessionNew = "subsession-new"        // 子会话创建

	// 任务编排事件（server 编排广播；task 结构字段：task_id/tool/kind/state/…）
	MsgTaskStarted    = "tasks.started"       // server 任务节点建立（编排广播）
	MsgTaskUpdated    = "tasks.updated"       // server 任务状态/输出更新（编排广播，节流 ≤250ms）
	MsgTaskEnded      = "tasks.done"          // server 任务终态（done/error/cancelled，编排广播）
	MsgTaskStop       = msgkeys.TopicTaskStop // 前端 → 后端：用户取消异步任务（TaskView 弹框；batch/tasks_run 带 cascade:'all'）
	MsgTaskClose      = "task-close"          // 前端内部：手动关闭节点（✕ → data-tasktree-delete 级联删子树）
	MsgTaskStopChoice = "task-stop-choice"    // 前端内部：call_llm 节点 ▍ 停止（直接级联 → task-stop）

	// LLM 域事件（域化改造；与前端 event-names.js 保持一致）
	MsgLLMStart    = msgkeys.TopicLlmStart     // 前端 → 后端：启动一轮 LLM（每 start 生成新 turn-id；替代 SendChatMessage RPC）
	MsgLLMStarted  = "llm-started"             // 后端 → 前端：受理 ack（含 turn_id；notify=true = 后端通知轮次）
	MsgLLMReceive  = "llm-receive"             // 后端 → 前端：LLM 流增量（桥 mqTypeMap：session-receive → llm-receive；payload.type 二次分子主题）
	MsgLLMComplete = "llm-complete"            // 后端 → 前端：一轮终结（队列出队的唯一 ack；payload 含 turn_id/status）
	MsgLLMCancel   = msgkeys.TopicLlmCancel    // 前端 → 后端：取消当前轮（{req_id, session, turn}；替代 CancelChat RPC）
	MsgAskUser     = "ask-user"                // 后端 → 前端：LLM ask_user 提问（{ask_id, question, options?, custom?, session, turn, expires_at}）
	MsgAskReply    = msgkeys.TopicAskUserReply // 用户回答 ask_user（前端 → 后端；{ask_id, answer, custom}；替代 ask-reply）

	// 应用事件（IDE → 前端）
	MsgSessionRefresh = "session-refresh"
	MsgConfigRefresh  = msgkeys.TopicConfigRefresh
	MsgGenerateToken  = "generate-token"
	MsgGenerateDone   = "generate-done"
	MsgGenerateError  = "generate-error"
	MsgOptimizeToken  = msgkeys.TopicOptimizeToken
	MsgOptimizeDone   = msgkeys.TopicOptimizeDone
	MsgOptimizeError  = msgkeys.TopicOptimizeError
	// 场景向导启动激活（GUI 桥在 gui.init-data 检测到 project_spec.md 缺失时下发前端；
	// payload {reason, work_dir, spec_path}）。
	MsgAgentWizard = msgkeys.TopicAgentWizard
	// 文件域（filesys 直连）：变更统一 filesys.changed（children=目录批次 / 单文件 operation）
	MsgFileChanged      = msgkeys.TopicFilesysChanged    // 单文件变化/目录批次广播
	MsgFileDirContents  = msgkeys.TopicFilesysChanged    // 别名（批次与单文件同主题，订阅方按 children 有无分流）
	MsgFileWatcherError = msgkeys.TopicFilesysWatchError // 轮询/监视故障广播（{work_dir, path?, error}）
	MsgFileSearch       = "file-search"                  // 搜索结果选中 → 文件树定位打开
	MsgFileExpand       = msgkeys.TopicFilesysWatch      // 展开目录 = 声明 watch
	MsgFileCollapse     = msgkeys.TopicFilesysUnwatch    // 收起目录 = 取消 watch

	// 前端内部事件（前端 → 前端，不经过 Go；mq.emit/mq.on 使用，值仍以 event-names.js 为准）
	MsgSessionChanged       = "session-changed"
	MsgSubsessionChanged    = "subsession-changed"
	MsgSessionCancelLlm     = "session-cancel-llm"
	MsgMessageSend          = "message-send"
	MsgMessageQueue         = "message-queue"
	MsgMessageCancel        = "message-cancel"
	MsgChatQueueRestore     = "chat-queue-restore"
	MsgChatQueueAction      = "chat-queue-action"
	MsgMsgToggleCollapse    = "msg-toggle-collapse"
	MsgMsgCopyText          = "msg-copy-text"
	MsgMsgCopyTool          = "msg-copy-tool"
	MsgMsgLoadMore          = "msg-load-more"
	MsgMsgShowFull          = "msg-show-full"
	MsgMsgScrollTop         = "msg-scroll-top"
	MsgMsgScrollBottom      = "msg-scroll-bottom"
	MsgChatSelectLLM        = "chat-select-llm"
	MsgChatToggleThink      = "chat-toggle-think"
	MsgChatToggleEffort     = "chat-toggle-effort"
	MsgChatSend             = "chat-send"
	MsgChatContinue         = "chat-continue"
	MsgChatScreenshot       = "chat-screenshot"
	MsgChatInsertAttachment = "chat-insert-attachment"
	MsgToolRetry            = msgkeys.TopicToolRetry
	MsgAskUserSelectOption  = "ask-user-select-option"
	MsgAskUserSubmit        = "ask-user-submit"
	MsgAskUserSkip          = "ask-user-skip"
	MsgAskUserCancel        = "ask-user-cancel"

	// 场景
	MsgScenarioSelect     = "scenario-select"
	MsgScenarioOpen       = "scenario-open"
	MsgScenarioReload     = "scenario-reload"
	MsgScenarioSetDefault = "scenario-set-default"

	// 配置
	MsgConfigOpen          = "config-open"
	MsgProjectConfigOpen   = msgkeys.TopicProjectConfigOpen
	MsgConfigSave          = "config-save"
	MsgConfigDialogClose   = "config-dialog-close"
	MsgConfigAddLLM        = "config-add-llm"
	MsgConfigEditLLM       = "config-edit-llm"
	MsgConfigDeleteLLM     = "config-delete-llm"
	MsgConfigSetDefaultLLM = "config-set-default-llm"
	MsgConfigAddMCP        = "config-add-mcp"
	MsgConfigEditMCP       = "config-edit-mcp"
	MsgConfigDeleteMCP     = "config-delete-mcp"
	MsgConfigPickFile      = "config-pick-file"

	// 提示词 / 工具 / 笔记
	MsgPromptSubtab = "prompt-subtab"
	MsgPromptSave   = "prompt-save"
	MsgPromptReset  = "prompt-reset"
	MsgNoteAdd      = "note-add"
	MsgNoteEdit     = "note-edit"
	MsgNoteDelete   = "note-delete"

	// 全局知识库
	MsgKbOpen      = "kb-open"
	MsgKbView      = "kb-view"
	MsgKbAdd       = "kb-add"
	MsgKbEdit      = "kb-edit"
	MsgKbDelete    = "kb-delete"
	MsgKbRowDelete = "kb-row-delete"
	MsgKbSave      = "kb-save"
	MsgKbCancel    = "kb-cancel"
	MsgKbRebuild   = "kb-rebuild"

	// 原语文件面板
	MsgPrimSave    = "prim-save"
	MsgPrimRestore = "prim-restore"

	// 文件 / 编辑
	MsgFileOpen               = "file-open"
	MsgChatInsertText         = "chat-insert-text"
	MsgPreviewSelectionToChat = "preview-selection-to-chat"
	MsgPreviewTabOpen         = msgkeys.TopicPreviewTabOpen
	MsgPreviewTabCloseAll     = "preview-tab-close-all"
	MsgPreviewTabCloseThis    = "preview-tab-close-this"
	MsgPreviewTabCloseRight   = "preview-tab-close-right"
	MsgPreviewTabCloseOthers  = "preview-tab-close-others"

	// 代码查看 / 文件树 / 数据查看器
	MsgCodeShowSource  = "code-show-source"
	MsgCodeShowPreview = "code-show-preview"
	MsgCodeVersionDiff = "code-version-diff"
	MsgHexLoadMore     = "hex-load-more"
	MsgVersionRestore  = "version-restore"
	MsgFileCtxAction   = "file-ctx-action"
	MsgDBBucketSelect  = "db-bucket-select"
	MsgDBRowSelect     = "db-row-select"

	// 场景 / 会话管理
	MsgAgentCopy           = "agent-copy"
	MsgAgentOptimize       = "agent-optimize"
	MsgAgentToggleCategory = "agent-toggle-category"
	MsgScenarioAdd         = "scenario-add"
	MsgScenarioEditRow     = "scenario-edit-row"
	MsgScenarioDeleteRow   = "scenario-delete-row"
	MsgScenarioSave        = "scenario-save"
	MsgScenarioAddSubAgent = "scenario-add-sub-agent"
	MsgScenarioSelectMain  = "scenario-select-main"
	MsgScenarioSelectAgent = "scenario-select-agent"
	MsgScenarioDeleteAgent = "scenario-delete-agent"
	MsgSessionDrawerClose  = "session-drawer-close"
	MsgSessionCreate       = "session-create"
	MsgSessionSelect       = "session-select"
	MsgSessionRename       = "session-rename"
	MsgSessionDelete       = "session-delete"

	// 工程分析 / LLM/MCP 编辑
	MsgAnalyzeAddFrontend    = "analyze-add-frontend"
	MsgAnalyzeAddBackend     = "analyze-add-backend"
	MsgAnalyzeAddArch        = "analyze-add-arch"
	MsgAnalyzeAddExtra       = "analyze-add-extra"
	MsgAnalyzeGenerate       = "analyze-generate"
	MsgAnalyzeBack           = "analyze-back"
	MsgAnalyzeShowCode       = "analyze-show-code"
	MsgAnalyzeShowPreview    = "analyze-show-preview"
	MsgEditLLMSave           = "edit-llm-save"
	MsgEditLLMCancel         = "edit-llm-cancel"
	MsgEditMCPSave           = "edit-mcp-save"
	MsgEditMCPCancel         = "edit-mcp-cancel"
	MsgDiscoveryConfirm      = "discovery-confirm"
	MsgDiscoveryCancel       = "discovery-cancel"
	MsgSessionTreeNodeToggle = "session-tree-node-toggle"

	// 任务活动视图（TaskView）
	MsgTaskDetailOpen = "task-detail-open"

	// 设置区：安全 / 笔记 / 索引
	MsgSecurityAdd          = "security-add"
	MsgSecuritySelectDir    = "security-select-dir"
	MsgSecurityDelete       = "security-delete"
	MsgNoteRowDelete        = "note-row-delete"
	MsgNoteOptimize         = "note-optimize"
	MsgNoteCancel           = "note-cancel"
	MsgNoteSave             = "note-save"
	MsgCodeIndexClear       = "code-index-clear"
	MsgCodeIndexReindex     = "code-index-reindex"
	MsgCodeIndexResetFailed = "code-index-reset-failed"

	// 上下文压缩页
	MsgContextSave           = "context-save"
	MsgContextQuickThreshold = "context-quick-threshold"
	MsgContextOptimizePrompt = "context-optimize-prompt"
	MsgContextRestorePrompt  = "context-restore-prompt"

	// 状态栏 / 工具栏
	MsgDBViewerOpen        = "db-viewer-open"
	MsgContainerClick      = "container-click"

	// 工具栏 / 布局
	MsgWorkdirOpen             = "workdir-open"
	MsgRecentDirsToggle        = "recent-dirs-toggle"
	MsgRecentDirSelect         = "recent-dir-select"
	MsgRecentDirRemove         = "recent-dir-remove"
	MsgThemeToggle             = "theme-toggle"
	MsgThemeSelect             = "theme-select"
	MsgLangToggle              = "lang-toggle"
	MsgLangSelect              = "lang-select"
	MsgWindowToggle            = "window-toggle"
	MsgGUIWindowStatus         = msgkeys.TopicGuiWindowStatus        // 统一窗口控制/查询：payload {command: null|'minimize'|'maximize'|'close'}
	MsgWindowMaximizedChanged  = msgkeys.TopicWindowMaximizedChanged // 后端执行最大化后广播，同步按钮图标
	MsgSessionsOpen            = "sessions-open"
	MsgAnalyzeOpen             = "analyze-open"
	MsgChatToggle              = "chat-toggle"
	MsgFiletreeToggle          = "filetree-toggle"
	MsgTasksToggle             = "tasks-toggle"
	MsgFiletreeModeToggle      = "filetree-mode-toggle"
	MsgFiletreeModeSelect      = "filetree-mode-select"
	MsgFiletreeModeRefresh     = "filetree-mode-refresh"
	MsgCodebaseIndexStatusOpen = "codebase-index-status-open"
)

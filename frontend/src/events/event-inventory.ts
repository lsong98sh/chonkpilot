// EVENT INVENTORY — 由架构设计文档生成
// 最后更新: 2025-07-25
//
// 这是一个文档性文件，用于追踪所有前后端事件。
// 不导入任何运行时模块，仅用于文档参考。

/**
 * 事件清单 — 记录所有前后端通信事件
 *
 * 分类：
 * - backendToFrontend: Go EventsEmit → bridge.on → Vue 组件
 * - frontendToBackend: Vue → bridge.emit / window.go.main.App.* → Go binding
 * - customEvents:     window.dispatchEvent(new CustomEvent(...)) (纯前端，建议淘汰)
 * - internalEvents:   后端 Go 组件间事件（EventBus）
 */
export const EventInventory = {
  // ──────────────────────────────────────────────
  // 后端 → 前端（EventsEmit → bridge.on）
  // 源头: app.go push() / app_session.go EventsEmit
  // ──────────────────────────────────────────────
  backendToFrontend: [
    {
      event: 'session:event',
      payload: '{ type: "selected"|"cleared", session_id: string }',
      source: 'app_session.go SetActiveSessionID()',
      listeners: [
        'ChatPanel.vue (handleSessionEvent)',
        'MainLayout.vue (handleSessionEvent → header tag)',
        'SessionTree.vue (handleSessionEvent)',
        'MainLayout.vue (handleSessionEvent → clear selection)',
      ],
    },
    {
      event: 'session:refresh',
      payload: '{ session_id: string }',
      source: 'app.go onExecutorEvent() — complete/error → also push session:refresh',
      listeners: [
        'ChatPanel.vue (reload messages)',
        'SessionChat.vue (onSessionRefresh)',
        'SessionTree.vue (debouncedLoadSessions)',
        'useSession.js (loadSessionList)',
      ],
    },
    {
      event: 'llm:event',
      payload: '{ _event_type, type, session_id, turn_id, content, ... }',
      source: 'app.go onExecutorEvent() — all executor events funneled here',
      listeners: [
        'ChatPanel.vue (live message streaming)',
        'SessionChat.vue (onLLMEvent)',
        'sse.js (llm:event → SSE forwarding)',
        'useSession.js (llm:event → turn/message store)',
        'useTask.js (llm:event → tool_progress tracking)',
      ],
      notes: '统一通道: message_chunk, tool_call, tool_result, tool_progress, complete, error, llm_error, llm_retry, progress 等所有 executor 事件',
    },
    {
      event: 'chat:executor_done',
      payload: '{ session_id, turn_id, ... }',
      source: 'app.go onExecutorEvent() — executor_done branch',
      listeners: [
        'ChatPanel.vue (cleanupAndFinish)',
      ],
    },
    {
      event: 'ask_user',
      payload: '{ question, options?, custom?, pipe_addr, sub_session_id? }',
      source: 'app.go onExecutorEvent() — ask_user branch',
      listeners: [
        'AskUserDialog.vue (handleAskUser → show dialog)',
      ],
    },
    {
      event: 'filetree:capture',
      payload: '{ request_id }',
      source: 'app.go onExecutorEvent() — filetree:capture branch',
      listeners: [
        'FileTree.vue (unsubCapture → snapshot + SaveFileTreeSnapshot)',
      ],
      notes: 'Executor 请求前端返回当前文件树快照',
    },
    {
      event: 'filetree:set',
      payload: '{ request_id, operate, target }',
      source: 'app.go onExecutorEvent() — filetree:set branch',
      listeners: [
        'FileTree.vue (unsubSet → doFileTreeOperate)',
      ],
      notes: 'Executor 请求前端操作文件树（展开/折叠/选中）',
    },
    {
      event: 'file:dir-contents',
      payload: '{ dir, children }',
      source: 'watcher file system events → wailsEventPusher',
      listeners: [
        'useFileTree.js (file:dir-contents → refresh tree)',
      ],
    },
    {
      event: 'file:changed',
      payload: '{ path, operation }',
      source: 'watcher file system events → wailsEventPusher',
      listeners: [
        'CodeView.vue (onFileChanged → reload)',
      ],
    },
    {
      event: 'config:refresh',
      payload: '(none)',
      source: 'app_config.go after config save',
      listeners: [
        'ChatPanel.vue (reload LLM list)',
        'useConfig.js (loadConfigInternal)',
      ],
    },
    {
      event: 'codebase:status',
      payload: '{ pending, indexing, failed, failed_exhausted }',
      source: 'app.go codebase status poller',
      listeners: [
        'useCodebaseStatus.js (update status bar)',
      ],
    },
    {
      event: 'generate:token',
      payload: '{ content: string }',
      source: 'app_config.go OptimizeAgentPrompt (generate)',
      listeners: [
        'config.js (onToken → streaming UI)',
      ],
    },
    {
      event: 'generate:done',
      payload: '{ ... }',
      source: 'app_config.go OptimizeAgentPrompt (generate)',
      listeners: [
        'config.js (cleanup)',
      ],
    },
    {
      event: 'generate:error',
      payload: '{ error: string }',
      source: 'app_config.go OptimizeAgentPrompt (generate)',
      listeners: [
        'config.js (cleanup)',
      ],
    },
    {
      event: 'optimize:token',
      payload: '{ content: string }',
      source: 'app_config.go OptimizeAgentPrompt (optimize)',
      listeners: [
        'config.js (onToken → streaming UI)',
      ],
    },
    {
      event: 'optimize:done',
      payload: '{ ... }',
      source: 'app_config.go OptimizeAgentPrompt (optimize)',
      listeners: [
        'config.js (cleanup)',
      ],
    },
    {
      event: 'optimize:error',
      payload: '{ error: string }',
      source: 'app_config.go OptimizeAgentPrompt (optimize)',
      listeners: [
        'config.js (cleanup)',
      ],
    },
  ] as const,

  // ──────────────────────────────────────────────
  // 前端 → 后端（window.go.main.App.* → Go binding）
  // ──────────────────────────────────────────────
  frontendToBackend: [
    // --- Session operations ---
    { func: 'ListSessions', files: ['SessionDrawer.vue', 'SessionTree.vue'] },
    { func: 'ListAllSessions', files: ['SessionTree.vue'] },
    { func: 'CreateSession', files: ['SessionDrawer.vue'] },
    { func: 'GetSession', files: ['SessionDrawer.vue'] },
    { func: 'DeleteSession', files: ['SessionDrawer.vue'] },
    { func: 'UpdateSessionTitle', files: ['SessionDrawer.vue'] },
    { func: 'GetTurnsBySession', files: ['ChatPanel.vue', 'SessionChat.vue'] },
    { func: 'GetLatestSessionID', files: ['ChatPanel.vue?'] },
    { func: 'GetActiveSessionID', files: ['ChatPanel.vue', 'MainLayout.vue'] },
    { func: 'SetActiveSessionID', files: ['SessionDrawer.vue'] },
    { func: 'GetMessageContent', files: ['ChatPanel.vue'] },
    { func: 'SubscribeSession', files: ['SessionChat.vue'] },
    { func: 'UnsubscribeSession', files: ['SessionChat.vue'] },
    // --- Chat ---
    { func: 'SendChatMessage', files: ['ChatPanel.vue'] },
    { func: 'CancelChat', files: ['ChatPanel.vue'] },
    { func: 'GetChat', files: ['ChatPanel.vue'] },
    // --- File operations ---
    { func: 'LoadInitData', files: ['file.js'] },
    { func: 'SaveFileTreeState', files: ['file.js'] },
    { func: 'SaveWindowState', files: ['file.js'] },
    { func: 'SaveFileTreeWidth', files: ['file.js'] },
    { func: 'WatchDir', files: ['FileTree.vue'] },
    { func: 'UnwatchDir', files: ['FileTree.vue'] },
    { func: 'SaveFileTreeSnapshot', files: ['FileTree.vue'] },
    { func: 'FileTreeOperateDone', files: ['FileTree.vue'] },
    // --- File versions ---
    { func: 'GetFileVersions', files: ['VersionDiffDialog.vue'] },
    { func: 'GetVersionContent', files: ['VersionDiffDialog.vue'] },
    { func: 'RestoreVersion', files: ['VersionDiffDialog.vue'] },
    // --- Config / MCP ---
    { func: 'DiscoverMCPServerTools', files: ['ConfigDialog.vue'] },
    { func: 'PickExecutableFile', files: ['ConfigDialog.vue'] },
    // --- Scenario ---
    { func: 'GetScenarioList', files: ['MainLayout.vue', 'ScenarioDialog.vue'] },
    { func: 'GetActiveScenario', files: ['MainLayout.vue'] },
    { func: 'SetActiveScenario', files: ['MainLayout.vue'] },
    { func: 'SaveScenario', files: ['ScenarioDialog.vue'] },
    { func: 'DeleteScenario', files: ['ScenarioDialog.vue'] },
    // --- Codebase Index ---
    { func: 'ReindexCodebase', files: ['ProjectConfig.vue', 'StatusBar.vue'] },
    { func: 'ClearCodebaseIndex', files: ['ProjectConfig.vue', 'StatusBar.vue'] },
    { func: 'StartCodebaseIndex', files: ['ProjectConfig.vue'] },
    { func: 'ResetFailedCodebaseIndex', files: ['useCodebaseStatus.js'] },
    // --- Toolbar / VCS ---
    { func: 'SearchProjectFiles', files: ['Toolbar.vue'] },
    { func: 'GetVCSInfo', files: ['MainLayout.vue'] },
    { func: 'GetUserConfig', files: ['Toolbar.vue'] },
    { func: 'GetWorkDir', files: ['Toolbar.vue'] },
    { func: 'OpenDevTools', files: ['StatusBar.vue'] },
    { func: 'RespondAskUser', files: ['AskUserDialog.vue'] },
  ] as const,

  // ──────────────────────────────────────────────
  // 纯前端 CustomEvent（通过 window.dispatchEvent）
  // 目标：逐步迁移到 bridge.on 或 composable
  // ──────────────────────────────────────────────
  customEvents: [
    {
      event: 'file:open',
      payload: '{ path: string, isDBConfig?: boolean, dbKey?: string }',
      emitters: [
        'FileTree.vue (click node → open file)',
        'CodeView.vue (tab change → open file)',
        'MainLayout.vue (openIDEConfig, hotkey handler)',
      ],
      listeners: ['CodeView.vue (handleFileOpen → open tab)'],
      status: 'to-migrate',
      notes: '应用内文件打开，可改为 bridge.on 或 provide/inject',
    },
    {
      event: 'config:open-tab',
      payload: '{ tab: string }',
      emitters: ['CodeView.vue (config file click)'],
      listeners: ['CodeView.vue (switch tab)'],
      status: 'to-migrate',
      notes: '同一组件内事件，可改为直接函数调用',
    },
    {
      event: 'chat:insert-text',
      payload: '{ text: string }',
      emitters: ['FileTree.vue (context menu → 请阅读)'],
      listeners: ['ChatPanel.vue? (接收插入文本)'],
      status: 'to-migrate',
      notes: '跨组件文本插入，可改为 bridge.on',
    },
    {
      event: 'filetree:resize',
      payload: '{ width: number }',
      emitters: ['FileTree.vue (drag resize)'],
      listeners: ['MainLayout.vue (sync layout)'],
      status: 'to-migrate',
      notes: '文件树宽度同步，可改为 provide/inject',
    },
    {
      event: 'session:cancel-llm',
      payload: '(none)',
      emitters: ['SessionDrawer.vue (delete active session)'],
      listeners: ['ChatPanel.vue (cancel in-progress LLM)'],
      status: 'to-migrate',
      notes: '跨组件取消信号，可改为 bridge.on',
    },
  ] as const,

  // ──────────────────────────────────────────────
  // 后端内部事件（Go → Go EventBus，新增）
  // ──────────────────────────────────────────────
  internalEvents: [
    {
      event: 'session:event',
      payload: '{ type, session_id }',
      source: 'app_session.go SetActiveSessionID()',
      description: 'Session 选中/清除，Go 组件可监听',
    },
    {
      event: 'llm:event',
      payload: '{ _event_type, type, session_id, turn_id, ... }',
      source: 'app.go push() → EventBus',
      description: '所有 LLM 相关事件的 EventBus 镜像',
    },
    {
      event: 'session:refresh',
      payload: '{ session_id }',
      source: 'app.go push() → EventBus',
      description: 'Session 刷新通知',
    },
    {
      event: 'chat:executor_done',
      payload: '{ session_id, turn_id }',
      source: 'app.go push() → EventBus',
      description: 'Executor 执行完成',
    },
    {
      event: 'ask_user',
      payload: '{ question, options, custom, pipe_addr }',
      source: 'app.go push() → EventBus',
      description: 'Ask user 事件',
    },
    {
      event: 'codebase:status',
      payload: '{ pending, indexing, failed, failed_exhausted }',
      source: 'app.go codebase status → EventBus',
      description: '代码索引状态',
    },
    {
      event: 'config:refresh',
      payload: '(none)',
      source: 'app.go push() → EventBus',
      description: '配置刷新',
    },
    {
      event: 'file:dir-contents',
      payload: '{ dir, children }',
      source: 'watcher → EventBus',
      description: '文件目录内容变更',
    },
    {
      event: 'file:changed',
      payload: '{ path, operation }',
      source: 'watcher → EventBus',
      description: '文件变更通知',
    },
    {
      event: 'filetree:capture',
      payload: '{ request_id }',
      source: 'app.go push() → EventBus',
      description: '文件树快照请求',
    },
    {
      event: 'filetree:set',
      payload: '{ request_id, operate, target }',
      source: 'app.go push() → EventBus',
      description: '文件树操作请求',
    },
    {
      event: 'generate:token',
      payload: '{ content }',
      source: 'app_config.go → push()',
      description: 'Agent prompt 生成 token',
    },
    {
      event: 'generate:done',
      payload: '{ ... }',
      source: 'app_config.go → push()',
      description: 'Agent prompt 生成完成',
    },
    {
      event: 'generate:error',
      payload: '{ error }',
      source: 'app_config.go → push()',
      description: 'Agent prompt 生成错误',
    },
    {
      event: 'optimize:token',
      payload: '{ content }',
      source: 'app_config.go → push()',
      description: 'Agent prompt 优化 token',
    },
    {
      event: 'optimize:done',
      payload: '{ ... }',
      source: 'app_config.go → push()',
      description: 'Agent prompt 优化完成',
    },
    {
      event: 'optimize:error',
      payload: '{ error }',
      source: 'app_config.go → push()',
      description: 'Agent prompt 优化错误',
    },
  ] as const,
} as const

// 类型辅助 (用于编译时检查)
export type BackendEvent =
  (typeof EventInventory.backendToFrontend)[number]['event']

export type CustomEventName =
  (typeof EventInventory.customEvents)[number]['event']

export type InternalEvent =
  (typeof EventInventory.internalEvents)[number]['event']

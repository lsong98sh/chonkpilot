// 由 genmsg 生成，勿手改。DO NOT EDIT.
//
// 契约唯一源：docs/spec/60-reference/61-messages.schema.json
//（= docs/spec/60-reference/61-消息一览.md，消息面唯一准则）。
// 重新生成：go run src/tools/genmsg

/** 相对主题名（61 topic，逐条镜像）。 */
export const MsgTopics = {
  guiInitData: 'gui.init-data',
  agentWizard: 'agent-wizard',
  guiUiSave: 'gui.ui.save',
  guiWindowStatus: 'gui.window.status',
  guiWindowOpenChat: 'gui.window.open-chat',
  guiWindowList: 'gui.window.list',
  guiWindowClosed: 'gui.window.closed',
  guiWindowSetTitle: 'gui.window.set-title',
  guiDirOpenDialog: 'gui.dir.open-dialog',
  guiDirOpen: 'gui.dir.open',
  guiRecentList: 'gui.recent.list',
  guiRecentRemove: 'gui.recent.remove',
  guiConsoleOpen: 'gui.console.open',
  guiVcsInfo: 'gui.vcs.info',
  guiReveal: 'gui.reveal',
  guiOpenWith: 'gui.open-with',
  guiSearch: 'gui.search',
  guiCapture: 'gui.capture',
  guiUpload: 'gui.upload',
  guiDevtoolsOpen: 'gui.devtools.open',
  guiPickExecutable: 'gui.pick-executable',
  guiFileSave: 'gui.file.save',
  guiToolchainDetect: 'gui.toolchain.detect',
  guiSystemBuiltins: 'gui.system.builtins',
  guiPromptOptimise: 'gui.prompt-optimise',
  guiPromptVars: 'gui.prompt-vars',
  optimizeToken: 'optimize-token',
  optimizeDone: 'optimize-done',
  optimizeError: 'optimize-error',
  windowMaximizedChanged: 'window-maximized-changed',
  llmTestConnection: 'llm.test-connection',
  memoryFlush: 'memory.flush',
  filesysList: 'filesys.list',
  filesysContent: 'filesys.content',
  filesysCreate: 'filesys.create',
  filesysMkdir: 'filesys.mkdir',
  filesysRemove: 'filesys.remove',
  filesysRename: 'filesys.rename',
  filesysCopy: 'filesys.copy',
  filesysWatch: 'filesys.watch',
  filesysUnwatch: 'filesys.unwatch',
  filesysChanged: 'filesys.changed',
  filesysWatchError: 'filesys.watch-error',
  dataUserConfigList: 'data-user-config-list',
  dataUserConfigLoad: 'data-user-config-load',
  dataUserConfigSave: 'data-user-config-save',
  dataUserConfigDelete: 'data-user-config-delete',
  dataUserConfigChanged: 'data-user-config-changed',
  dataUserConfigRefresh: 'data-user-config-refresh',
  dataPrjConfigList: 'data-prj-config-list',
  dataPrjConfigLoad: 'data-prj-config-load',
  dataPrjConfigSave: 'data-prj-config-save',
  dataPrjConfigDelete: 'data-prj-config-delete',
  dataPrjConfigRefresh: 'data-prj-config-refresh',
  dataPrjSecurityList: 'data-prj-security-list',
  dataPrjSecurityLoad: 'data-prj-security-load',
  dataPrjSecuritySave: 'data-prj-security-save',
  dataPrjSecurityDelete: 'data-prj-security-delete',
  dataPrjSecurityRefresh: 'data-prj-security-refresh',
  dataPromptList: 'data-prompt-list',
  dataPromptLoad: 'data-prompt-load',
  dataPromptSave: 'data-prompt-save',
  dataPromptDelete: 'data-prompt-delete',
  dataPromptRefresh: 'data-prompt-refresh',
  dataScenarioList: 'data-scenario-list',
  dataScenarioLoad: 'data-scenario-load',
  dataScenarioSave: 'data-scenario-save',
  dataScenarioDelete: 'data-scenario-delete',
  dataScenarioRefresh: 'data-scenario-refresh',
  dataMcpList: 'data-mcp-list',
  dataMcpLoad: 'data-mcp-load',
  dataMcpSave: 'data-mcp-save',
  dataMcpDelete: 'data-mcp-delete',
  dataMcpRefresh: 'data-mcp-refresh',
  dataMemoryList: 'data-memory-list',
  dataMemoryRead: 'data-memory-read',
  dataMemorySave: 'data-memory-save',
  dataMemoryDelete: 'data-memory-delete',
  dataMemoryRefresh: 'data-memory-refresh',
  dataMemoryExtractLoad: 'data-memory-extract-load',
  dataMemoryExtractSave: 'data-memory-extract-save',
  dataMemoryExtractDelete: 'data-memory-extract-delete',
  dataFilelistList: 'data-filelist-list',
  dataFilelistPut: 'data-filelist-put',
  dataFilelistDel: 'data-filelist-del',
  configRefresh: 'config-refresh',
  dataSessionList: 'data-session-list',
  dataSessionGet: 'data-session-get',
  dataSessionHistory: 'data-session-history',
  dataSessionContent: 'data-session-content',
  dataSessionLatest: 'data-session-latest',
  dataSessionTitle: 'data-session-title',
  dataSessionTitleChanged: 'data-session-title-changed',
  dataSessionDelete: 'data-session-delete',
  dataSessionActiveSet: 'data-session-active-set',
  dataSessionActiveGet: 'data-session-active-get',
  dataSessionEnsureSession: 'data-session-ensure-session',
  dataSessionEnsureTurn: 'data-session-ensure-turn',
  dataSessionAppendMessage: 'data-session-append-message',
  dataSessionSetSummary: 'data-session-set-summary',
  dataSessionCompleteTurn: 'data-session-complete-turn',
  dataSessionCleanupStale: 'data-session-cleanup-stale',
  dataSessionLoadMessages: 'data-session-load-messages',
  dataSessionContext: 'data-session-context',
  dataSnapshotGet: 'data-snapshot-get',
  dataSnapshotSet: 'data-snapshot-set',
  dataKnowledgeRoot: 'data-knowledge-root',
  dataKnowledgeList: 'data-knowledge-list',
  dataKnowledgeRead: 'data-knowledge-read',
  dataKnowledgeSave: 'data-knowledge-save',
  dataKnowledgeCreate: 'data-knowledge-create',
  dataKnowledgeDelete: 'data-knowledge-delete',
  dataKnowledgeRename: 'data-knowledge-rename',
  dataKnowledgeMkdir: 'data-knowledge-mkdir',
  dataKnowledgeRmdir: 'data-knowledge-rmdir',
  dataKnowledgeRenameDir: 'data-knowledge-rename-dir',
  dataTasktreeList: 'data-tasktree-list',
  dataTasktreeTasks: 'data-tasktree-tasks',
  dataTasktreeDelete: 'data-tasktree-delete',
  dataTasktreeUpsert: 'data-tasktree-upsert',
  taskDeleted: 'task-deleted',
  dataIndexIgnored: 'data-index-ignored',
  instanceClaim: 'instance-claim',
  instanceRegister: 'instance-register',
  instanceHeartbeat: 'instance-heartbeat',
  instanceExit: 'instance-exit',
  llmStart: 'llm-start',
  llmSend: 'llm-send',
  llmCancel: 'llm-cancel',
  askUserReply: 'ask-user-reply',
  toolRetry: 'tool-retry',
  taskStop: 'task-stop',
  taskBackground: 'task-background',
  promptOptimise: 'prompt-optimise',
  taskVerify: 'task-verify',
  agentWizardProbe: 'agent-wizard-probe',
  agentWizardCompose: 'agent-wizard-compose',
  agentWizardGenerate: 'agent-wizard-generate',
  agentWizardSkip: 'agent-wizard-skip',
  llmSimple: 'llm-simple',
  loginRegister: 'login-register',
  loginIn: 'login-in',
  loginOut: 'login-out',
  sessionReceive: 'session-receive',
  sessionComplete: 'session-complete',
  sessionCompress: 'session-compress',
  sessionAsk: 'session-ask',
  sessionTurnStart: 'session-turn-start',
  sessionNew: 'session-new',
  taskStarted: 'task-started',
  taskUpdated: 'task-updated',
  taskDone: 'task-done',
  serverStarting: 'server-starting',
  promptOptimised: 'prompt-optimised',
  toolNotify: 'tool-notify',
  mcpToolsList: 'mcp-tools-list',
  mcpToolsCall: 'mcp-tools-call',
  mcpToolsWait: 'mcp-tools-wait',
  mcpToolsRegister: 'mcp-tools-register',
  mcpToolsUnregister: 'mcp-tools-unregister',
  mcpPromptsList: 'mcp-prompts-list',
  mcpPromptsGet: 'mcp-prompts-get',
  mcpPromptsRegister: 'mcp-prompts-register',
  mcpPromptsUnregister: 'mcp-prompts-unregister',
  mcpResourcesList: 'mcp-resources-list',
  mcpResourcesRead: 'mcp-resources-read',
  mcpResourcesRegister: 'mcp-resources-register',
  mcpResourcesUnregister: 'mcp-resources-unregister',
  mcpServersList: 'mcp-servers-list',
  mcpServersGet: 'mcp-servers-get',
  mcpServersRegister: 'mcp-servers-register',
  mcpServersUnregister: 'mcp-servers-unregister',
  mcpGatewayCheck: 'mcp-gateway-check',
  mcpGatewayReload: 'mcp-gateway-reload',
  mcpTasksReport: 'mcp-tasks-report',
  mcpToolsTimeout: 'mcp-tools-timeout',
  mcpGatewayChanged: 'mcp-gateway-changed',
  historyPreToolHook: 'history-pre-tool-hook',
  codegraphToolCall: 'codegraph-tool-call',
  vftsToolCall: 'vfts-tool-call',
  vftsDictGet: 'vfts.dict.get',
  vftsDictSet: 'vfts.dict.set',
  vftsReindex: 'vfts.reindex',
  projectConfigOpen: 'project-config-open',
  previewTabOpen: 'preview-tab-open',
}

/** 客户端 topic（schema clientTopic；前端 type ↔ 总线相对主题 的键）。 */
export const MsgClientTopics = {
  toolsList: 'tools-list',
  mcpToolsWait: 'mcp-tools-wait',
  promptsList: 'prompts-list',
  resourcesList: 'resources-list',
}

/** 通用字段键（全部字段名并集，跨主题共用）。 */
export const FieldKeys = {
  accepted: 'accepted',
  activated: 'activated',
  agents: 'agents',
  answers: 'answers',
  apiKey: 'apiKey',
  args: 'args',
  args_digest: 'args_digest',
  arguments: 'arguments',
  ask_id: 'ask_id',
  asset_kind: 'asset_kind',
  async: 'async',
  async_threshold: 'async_threshold',
  b64: 'b64',
  baseUrl: 'baseUrl',
  base_dicts: 'base_dicts',
  before_turn_id: 'before_turn_id',
  brief_tokens: 'brief_tokens',
  cached_rows: 'cached_rows',
  cancelled: 'cancelled',
  cascade: 'cascade',
  category: 'category',
  children: 'children',
  choices: 'choices',
  chunks: 'chunks',
  client_type: 'client_type',
  closed: 'closed',
  code: 'code',
  codegraph: 'codegraph',
  command: 'command',
  config: 'config',
  config_saved: 'config_saved',
  content: 'content',
  contents: 'contents',
  context: 'context',
  continue: 'continue',
  count: 'count',
  created_at: 'created_at',
  cursor: 'cursor',
  data: 'data',
  data_dir: 'data_dir',
  deleted: 'deleted',
  deleted_at: 'deleted_at',
  description: 'description',
  dest_dir: 'dest_dir',
  dict_dir: 'dict_dir',
  diffs: 'diffs',
  dir: 'dir',
  dirs: 'dirs',
  doc: 'doc',
  doc_ids: 'doc_ids',
  done_at: 'done_at',
  effort: 'effort',
  entries: 'entries',
  error: 'error',
  errors: 'errors',
  exclude_turn: 'exclude_turn',
  exec_json: 'exec_json',
  expandedKeys: 'expandedKeys',
  expires_at: 'expires_at',
  file_id: 'file_id',
  files: 'files',
  filetree: 'filetree',
  filetreeWidth: 'filetreeWidth',
  filter: 'filter',
  finish_reason: 'finish_reason',
  finished_at: 'finished_at',
  full_tokens: 'full_tokens',
  git: 'git',
  gitInstalled: 'gitInstalled',
  git_initialized: 'git_initialized',
  groups: 'groups',
  handler_subject: 'handler_subject',
  hot: 'hot',
  id: 'id',
  ids: 'ids',
  include_closed: 'include_closed',
  include_snapshot: 'include_snapshot',
  indexed_at: 'indexed_at',
  init_git: 'init_git',
  instance_id: 'instance_id',
  isError: 'isError',
  is_dir: 'is_dir',
  key: 'key',
  keys: 'keys',
  kind: 'kind',
  last_turn: 'last_turn',
  latency_ms: 'latency_ms',
  layout: 'layout',
  level: 'level',
  limit: 'limit',
  list: 'list',
  llm: 'llm',
  logDir: 'logDir',
  match: 'match',
  max_context_token: 'max_context_token',
  max_output_token: 'max_output_token',
  maximized: 'maximized',
  mcpServers: 'mcpServers',
  mcp_server: 'mcp_server',
  md5: 'md5',
  memory: 'memory',
  memory_saved: 'memory_saved',
  message: 'message',
  message_id: 'message_id',
  messages: 'messages',
  mimetype: 'mimetype',
  minimized: 'minimized',
  mode: 'mode',
  model: 'model',
  model_echo: 'model_echo',
  msg: 'msg',
  mtime: 'mtime',
  name: 'name',
  namespace: 'namespace',
  new_name: 'new_name',
  node_id: 'node_id',
  nodes: 'nodes',
  notice: 'notice',
  offset: 'offset',
  ok: 'ok',
  old_level: 'old_level',
  old_name: 'old_name',
  only_in_authoritative: 'only_in_authoritative',
  only_in_layer: 'only_in_layer',
  op: 'op',
  openedFile: 'openedFile',
  openedFiles: 'openedFiles',
  opened_files: 'opened_files',
  operation: 'operation',
  options: 'options',
  origin: 'origin',
  overwrite: 'overwrite',
  owner: 'owner',
  parent: 'parent',
  parent_node_id: 'parent_node_id',
  parent_session_id: 'parent_session_id',
  parents: 'parents',
  password: 'password',
  path: 'path',
  paths: 'paths',
  payload: 'payload',
  plugin: 'plugin',
  pre_hook_subject: 'pre_hook_subject',
  prefix: 'prefix',
  probe: 'probe',
  project_spec: 'project_spec',
  prompt: 'prompt',
  prompts: 'prompts',
  protocol: 'protocol',
  query: 'query',
  questions: 'questions',
  queued: 'queued',
  reason: 'reason',
  recursive: 'recursive',
  refreshed: 'refreshed',
  registered: 'registered',
  reloaded: 'reloaded',
  removed_tools: 'removed_tools',
  resource: 'resource',
  resources: 'resources',
  resultType: 'resultType',
  result_digest: 'result_digest',
  result_summary: 'result_summary',
  results: 'results',
  retryable: 'retryable',
  root: 'root',
  rows: 'rows',
  scenario_id: 'scenario_id',
  scene_name: 'scene_name',
  schema: 'schema',
  scope: 'scope',
  selectedKey: 'selectedKey',
  server: 'server',
  servers: 'servers',
  session: 'session',
  session_id: 'session_id',
  shadow: 'shadow',
  size: 'size',
  snapshot: 'snapshot',
  snapshot_turn: 'snapshot_turn',
  source: 'source',
  spec_path: 'spec_path',
  start: 'start',
  started: 'started',
  started_at: 'started_at',
  state: 'state',
  status: 'status',
  structuredContent: 'structuredContent',
  summary: 'summary',
  svn: 'svn',
  system: 'system',
  tab: 'tab',
  target_bytes: 'target_bytes',
  target_messages: 'target_messages',
  task_id: 'task_id',
  team: 'team',
  text: 'text',
  think: 'think',
  timeout: 'timeout',
  timeout_s: 'timeout_s',
  title: 'title',
  tool: 'tool',
  tool_call_id: 'tool_call_id',
  tools: 'tools',
  top_session: 'top_session',
  total: 'total',
  touch_files: 'touch_files',
  transport: 'transport',
  treeData: 'treeData',
  truncated: 'truncated',
  ttlMs: 'ttlMs',
  turn: 'turn',
  turn_id: 'turn_id',
  turn_tokens: 'turn_tokens',
  type: 'type',
  ui: 'ui',
  unregistered: 'unregistered',
  uri: 'uri',
  url: 'url',
  useCase: 'useCase',
  user: 'user',
  user_dict: 'user_dict',
  user_dict_path: 'user_dict_path',
  username: 'username',
  vfts: 'vfts',
  waiting: 'waiting',
  window: 'window',
  window_id: 'window_id',
  windows: 'windows',
  wizard_required: 'wizard_required',
  word_count: 'word_count',
  workDir: 'workDir',
  work_dir: 'work_dir',
}

/** 字段语义字典（语义键 → 规范名；同语义唯一名，见 schema fieldDictionary）。 */
export const FieldDict = {
  accepted: 'accepted',
  activated: 'activated',
  agents: 'agents',
  answers: 'answers',
  args: 'args',
  args_digest: 'args_digest',
  arguments: 'arguments',
  ask_id: 'ask_id',
  asset_kind: 'asset_kind',
  async: 'async',
  async_threshold: 'async_threshold',
  b64: 'b64',
  base_dicts: 'base_dicts',
  before_turn_id: 'before_turn_id',
  brief_tokens: 'brief_tokens',
  'builtin-mcp-servers': 'mcpServers',
  cached_rows: 'cached_rows',
  cancelled: 'cancelled',
  cascade: 'cascade',
  category: 'category',
  children: 'children',
  choices: 'choices',
  chunks: 'chunks',
  client_type: 'client_type',
  closed: 'closed',
  code: 'code',
  codegraph: 'codegraph',
  command: 'command',
  config: 'config',
  config_saved: 'config_saved',
  content: 'content',
  contents: 'contents',
  context: 'context',
  continue: 'continue',
  count: 'count',
  created_at: 'created_at',
  cursor: 'cursor',
  data: 'data',
  data_dir: 'data_dir',
  deleted: 'deleted',
  deleted_at: 'deleted_at',
  description: 'description',
  dest_dir: 'dest_dir',
  dict_dir: 'dict_dir',
  diffs: 'diffs',
  dir: 'dir',
  dirs: 'dirs',
  doc: 'doc',
  doc_ids: 'doc_ids',
  done_at: 'done_at',
  effort: 'effort',
  enabled: 'enabled',
  entries: 'entries',
  error: 'error',
  errors: 'errors',
  exclude_turn: 'exclude_turn',
  exec_json: 'exec_json',
  expires_at: 'expires_at',
  explicit: 'explicit',
  failed: 'failed',
  file_id: 'file_id',
  files: 'files',
  filetree: 'filetree',
  filter: 'filter',
  finish_reason: 'finish_reason',
  finished_at: 'finished_at',
  full_tokens: 'full_tokens',
  git: 'git',
  git_initialized: 'git_initialized',
  groups: 'groups',
  handler_subject: 'handler_subject',
  hot: 'hot',
  id: 'id',
  ids: 'ids',
  include_closed: 'include_closed',
  include_snapshot: 'include_snapshot',
  indexed_at: 'indexed_at',
  'init-expanded-keys': 'expandedKeys',
  'init-filetree-width': 'filetreeWidth',
  'init-opened-file': 'openedFile',
  'init-selected-key': 'selectedKey',
  'init-tree-data': 'treeData',
  init_git: 'init_git',
  instance_id: 'instance_id',
  is_dir: 'is_dir',
  key: 'key',
  keys: 'keys',
  kind: 'kind',
  last_turn: 'last_turn',
  latency_ms: 'latency_ms',
  layout: 'layout',
  level: 'level',
  limit: 'limit',
  list: 'list',
  llm: 'llm',
  'llm-api-key': 'apiKey',
  'llm-base-url': 'baseUrl',
  'log-dir': 'logDir',
  match: 'match',
  max_context_token: 'max_context_token',
  max_output_token: 'max_output_token',
  maximized: 'maximized',
  'mcp-is-error': 'isError',
  'mcp-result-type': 'resultType',
  'mcp-structured-content': 'structuredContent',
  'mcp-ttl-ms': 'ttlMs',
  mcp_server: 'mcp_server',
  md5: 'md5',
  memory: 'memory',
  memory_saved: 'memory_saved',
  message: 'message',
  message_id: 'message_id',
  messages: 'messages',
  mimetype: 'mimetype',
  minimized: 'minimized',
  mode: 'mode',
  model: 'model',
  model_echo: 'model_echo',
  msg: 'msg',
  mtime: 'mtime',
  name: 'name',
  namespace: 'namespace',
  new_name: 'new_name',
  node_id: 'node_id',
  nodes: 'nodes',
  notice: 'notice',
  offset: 'offset',
  ok: 'ok',
  old_level: 'old_level',
  old_name: 'old_name',
  only_in_authoritative: 'only_in_authoritative',
  only_in_layer: 'only_in_layer',
  op: 'op',
  'opened-files': 'opened_files',
  operation: 'operation',
  options: 'options',
  origin: 'origin',
  overwrite: 'overwrite',
  owner: 'owner',
  parent: 'parent',
  parent_node_id: 'parent_node_id',
  parent_session_id: 'parent_session_id',
  parents: 'parents',
  password: 'password',
  path: 'path',
  paths: 'paths',
  payload: 'payload',
  plugin: 'plugin',
  pre_hook_subject: 'pre_hook_subject',
  prefix: 'prefix',
  probe: 'probe',
  project_spec: 'project_spec',
  prompt: 'prompt',
  'prompt-use-case': 'useCase',
  prompts: 'prompts',
  protocol: 'protocol',
  query: 'query',
  questions: 'questions',
  queued: 'queued',
  reason: 'reason',
  recursive: 'recursive',
  refreshed: 'refreshed',
  registered: 'registered',
  reloaded: 'reloaded',
  removed_tools: 'removed_tools',
  resource: 'resource',
  resources: 'resources',
  result_digest: 'result_digest',
  result_summary: 'result_summary',
  results: 'results',
  retryable: 'retryable',
  root: 'root',
  rows: 'rows',
  saved: 'saved',
  scenario_id: 'scenario_id',
  scene_name: 'scene_name',
  schema: 'schema',
  scope: 'scope',
  server: 'server',
  servers: 'servers',
  session: 'session',
  session_id: 'session_id',
  shadow: 'shadow',
  size: 'size',
  snapshot: 'snapshot',
  snapshot_turn: 'snapshot_turn',
  source: 'source',
  spec_path: 'spec_path',
  start: 'start',
  started: 'started',
  started_at: 'started_at',
  state: 'state',
  status: 'status',
  summary: 'summary',
  svn: 'svn',
  system: 'system',
  tab: 'tab',
  target_bytes: 'target_bytes',
  target_messages: 'target_messages',
  task_id: 'task_id',
  team: 'team',
  text: 'text',
  think: 'think',
  timeout: 'timeout',
  timeout_s: 'timeout_s',
  title: 'title',
  tool: 'tool',
  'tool-call-id': 'tool_call_id',
  tools: 'tools',
  top_session: 'top_session',
  total: 'total',
  touch_files: 'touch_files',
  transport: 'transport',
  truncated: 'truncated',
  turn: 'turn',
  turn_id: 'turn_id',
  turn_tokens: 'turn_tokens',
  type: 'type',
  ui: 'ui',
  unregistered: 'unregistered',
  uri: 'uri',
  url: 'url',
  user: 'user',
  user_dict: 'user_dict',
  user_dict_path: 'user_dict_path',
  username: 'username',
  'vcs-git-installed': 'gitInstalled',
  vfts: 'vfts',
  waiting: 'waiting',
  window: 'window',
  window_id: 'window_id',
  windows: 'windows',
  wizard_required: 'wizard_required',
  word_count: 'word_count',
  'work-dir': 'work_dir',
}

/** gui.init-data 字段键（payload/result/event 合并去重）。 */
export const GuiInitDataKeys = {
  expandedKeys: 'expandedKeys',
  filetreeWidth: 'filetreeWidth',
  layout: 'layout',
  logDir: 'logDir',
  openedFile: 'openedFile',
  openedFiles: 'openedFiles',
  selectedKey: 'selectedKey',
  treeData: 'treeData',
  ui: 'ui',
  wizard_required: 'wizard_required',
  workDir: 'workDir',
}

/** agent-wizard 字段键（payload/result/event 合并去重）。 */
export const AgentWizardKeys = {
  reason: 'reason',
  spec_path: 'spec_path',
  work_dir: 'work_dir',
}

/** gui.ui.save 字段键（payload/result/event 合并去重）。 */
export const GuiUiSaveKeys = {
  filetree: 'filetree',
  layout: 'layout',
  ok: 'ok',
  opened_files: 'opened_files',
  ui: 'ui',
  window: 'window',
}

/** gui.window.status 字段键（payload/result/event 合并去重）。 */
export const GuiWindowStatusKeys = {
  command: 'command',
  maximized: 'maximized',
  minimized: 'minimized',
}

/** gui.window.open-chat 字段键（payload/result/event 合并去重）。 */
export const GuiWindowOpenChatKeys = {
  activated: 'activated',
  ok: 'ok',
  session_id: 'session_id',
  window_id: 'window_id',
}

/** gui.window.list 字段键（payload/result/event 合并去重）。 */
export const GuiWindowListKeys = {
  windows: 'windows',
}

/** gui.window.closed 字段键（payload/result/event 合并去重）。 */
export const GuiWindowClosedKeys = {
  session_id: 'session_id',
  window_id: 'window_id',
}

/** gui.window.set-title 字段键（payload/result/event 合并去重）。 */
export const GuiWindowSetTitleKeys = {
  ok: 'ok',
  title: 'title',
}

/** gui.dir.open-dialog 字段键（payload/result/event 合并去重）。 */
export const GuiDirOpenDialogKeys = {
  path: 'path',
}

/** gui.dir.open 字段键（payload/result/event 合并去重）。 */
export const GuiDirOpenKeys = {
  ok: 'ok',
  path: 'path',
}

/** gui.recent.list 字段键（payload/result/event 合并去重）。 */
export const GuiRecentListKeys = {
  dirs: 'dirs',
}

/** gui.recent.remove 字段键（payload/result/event 合并去重）。 */
export const GuiRecentRemoveKeys = {
  ok: 'ok',
  path: 'path',
}

/** gui.console.open 字段键（payload/result/event 合并去重）。 */
export const GuiConsoleOpenKeys = {
  ok: 'ok',
  path: 'path',
}

/** gui.vcs.info 字段键（payload/result/event 合并去重）。 */
export const GuiVcsInfoKeys = {
  git: 'git',
  gitInstalled: 'gitInstalled',
  path: 'path',
  svn: 'svn',
}

/** gui.reveal 字段键（payload/result/event 合并去重）。 */
export const GuiRevealKeys = {
  ok: 'ok',
  path: 'path',
}

/** gui.open-with 字段键（payload/result/event 合并去重）。 */
export const GuiOpenWithKeys = {
  ok: 'ok',
  path: 'path',
}

/** gui.search 字段键（payload/result/event 合并去重）。 */
export const GuiSearchKeys = {
  query: 'query',
  results: 'results',
}

/** gui.capture 字段键（payload/result/event 合并去重）。 */
export const GuiCaptureKeys = {
  b64: 'b64',
  file_id: 'file_id',
  name: 'name',
  path: 'path',
  size: 'size',
  url: 'url',
}

/** gui.upload 字段键（payload/result/event 合并去重）。 */
export const GuiUploadKeys = {
  data: 'data',
  file_id: 'file_id',
  kind: 'kind',
  name: 'name',
  path: 'path',
  url: 'url',
}

/** gui.pick-executable 字段键（payload/result/event 合并去重）。 */
export const GuiPickExecutableKeys = {
  path: 'path',
}

/** gui.file.save 字段键（payload/result/event 合并去重）。 */
export const GuiFileSaveKeys = {
  content: 'content',
  mode: 'mode',
  name: 'name',
  path: 'path',
}

/** gui.toolchain.detect 字段键（payload/result/event 合并去重）。 */
export const GuiToolchainDetectKeys = {
  tools: 'tools',
}

/** gui.system.builtins 字段键（payload/result/event 合并去重）。 */
export const GuiSystemBuiltinsKeys = {
  mcpServers: 'mcpServers',
}

/** gui.prompt-optimise 字段键（payload/result/event 合并去重）。 */
export const GuiPromptOptimiseKeys = {
  ok: 'ok',
  prompt: 'prompt',
  started: 'started',
  title: 'title',
  useCase: 'useCase',
}

/** gui.prompt-vars 字段键（payload/result/event 合并去重）。 */
export const GuiPromptVarsKeys = {
  groups: 'groups',
}

/** optimize-token 字段键（payload/result/event 合并去重）。 */
export const OptimizeTokenKeys = {
  content: 'content',
}

/** optimize-done 字段键（payload/result/event 合并去重）。 */
export const OptimizeDoneKeys = {
  prompt: 'prompt',
}

/** optimize-error 字段键（payload/result/event 合并去重）。 */
export const OptimizeErrorKeys = {
  message: 'message',
}

/** window-maximized-changed 字段键（payload/result/event 合并去重）。 */
export const WindowMaximizedChangedKeys = {
  instance_id: 'instance_id',
  maximized: 'maximized',
}

/** llm.test-connection 字段键（payload/result/event 合并去重）。 */
export const LlmTestConnectionKeys = {
  apiKey: 'apiKey',
  baseUrl: 'baseUrl',
  error: 'error',
  instance_id: 'instance_id',
  latency_ms: 'latency_ms',
  model: 'model',
  model_echo: 'model_echo',
  ok: 'ok',
  protocol: 'protocol',
}

/** memory.flush 字段键（payload/result/event 合并去重）。 */
export const MemoryFlushKeys = {
  instance_id: 'instance_id',
  ok: 'ok',
  queued: 'queued',
  reason: 'reason',
  session: 'session',
}

/** filesys.list 字段键（payload/result/event 合并去重）。 */
export const FilesysListKeys = {
  children: 'children',
  is_dir: 'is_dir',
  limit: 'limit',
  path: 'path',
  start: 'start',
  work_dir: 'work_dir',
}

/** filesys.content 字段键（payload/result/event 合并去重）。 */
export const FilesysContentKeys = {
  content: 'content',
  kind: 'kind',
  path: 'path',
  truncated: 'truncated',
  work_dir: 'work_dir',
}

/** filesys.create 字段键（payload/result/event 合并去重）。 */
export const FilesysCreateKeys = {
  content: 'content',
  dir: 'dir',
  name: 'name',
  ok: 'ok',
  path: 'path',
  work_dir: 'work_dir',
}

/** filesys.mkdir 字段键（payload/result/event 合并去重）。 */
export const FilesysMkdirKeys = {
  dir: 'dir',
  name: 'name',
  ok: 'ok',
  path: 'path',
  work_dir: 'work_dir',
}

/** filesys.remove 字段键（payload/result/event 合并去重）。 */
export const FilesysRemoveKeys = {
  ok: 'ok',
  path: 'path',
  work_dir: 'work_dir',
}

/** filesys.rename 字段键（payload/result/event 合并去重）。 */
export const FilesysRenameKeys = {
  new_name: 'new_name',
  ok: 'ok',
  overwrite: 'overwrite',
  path: 'path',
  work_dir: 'work_dir',
}

/** filesys.copy 字段键（payload/result/event 合并去重）。 */
export const FilesysCopyKeys = {
  dest_dir: 'dest_dir',
  new_name: 'new_name',
  ok: 'ok',
  overwrite: 'overwrite',
  path: 'path',
  work_dir: 'work_dir',
}

/** filesys.watch 字段键（payload/result/event 合并去重）。 */
export const FilesysWatchKeys = {
  path: 'path',
  recursive: 'recursive',
  work_dir: 'work_dir',
}

/** filesys.unwatch 字段键（payload/result/event 合并去重）。 */
export const FilesysUnwatchKeys = {
  path: 'path',
  work_dir: 'work_dir',
}

/** filesys.changed 字段键（payload/result/event 合并去重）。 */
export const FilesysChangedKeys = {
  children: 'children',
  instance_id: 'instance_id',
  operation: 'operation',
  path: 'path',
  work_dir: 'work_dir',
}

/** filesys.watch-error 字段键（payload/result/event 合并去重）。 */
export const FilesysWatchErrorKeys = {
  error: 'error',
  instance_id: 'instance_id',
  path: 'path',
  work_dir: 'work_dir',
}

/** data-user-config-list 字段键（payload/result/event 合并去重）。 */
export const DataUserConfigListKeys = {
  list: 'list',
}

/** data-user-config-load 字段键（payload/result/event 合并去重）。 */
export const DataUserConfigLoadKeys = {
  data: 'data',
}

/** data-user-config-save 字段键（payload/result/event 合并去重）。 */
export const DataUserConfigSaveKeys = {
  data: 'data',
  id: 'id',
  ok: 'ok',
}

/** data-user-config-delete 字段键（payload/result/event 合并去重）。 */
export const DataUserConfigDeleteKeys = {
  id: 'id',
  key: 'key',
  ok: 'ok',
}

/** data-user-config-changed 字段键（payload/result/event 合并去重）。 */
export const DataUserConfigChangedKeys = {
  data: 'data',
}

/** data-user-config-refresh 字段键（payload/result/event 合并去重）。 */
export const DataUserConfigRefreshKeys = {
  id: 'id',
  ids: 'ids',
  instance_id: 'instance_id',
  list: 'list',
  op: 'op',
}

/** data-prj-config-list 字段键（payload/result/event 合并去重）。 */
export const DataPrjConfigListKeys = {
  list: 'list',
}

/** data-prj-config-load 字段键（payload/result/event 合并去重）。 */
export const DataPrjConfigLoadKeys = {
  data: 'data',
  id: 'id',
}

/** data-prj-config-save 字段键（payload/result/event 合并去重）。 */
export const DataPrjConfigSaveKeys = {
  data: 'data',
  id: 'id',
  ok: 'ok',
}

/** data-prj-config-delete 字段键（payload/result/event 合并去重）。 */
export const DataPrjConfigDeleteKeys = {
  id: 'id',
  ok: 'ok',
}

/** data-prj-config-refresh 字段键（payload/result/event 合并去重）。 */
export const DataPrjConfigRefreshKeys = {
  id: 'id',
  ids: 'ids',
  instance_id: 'instance_id',
  list: 'list',
  op: 'op',
}

/** data-prj-security-list 字段键（payload/result/event 合并去重）。 */
export const DataPrjSecurityListKeys = {
  list: 'list',
}

/** data-prj-security-load 字段键（payload/result/event 合并去重）。 */
export const DataPrjSecurityLoadKeys = {
  data: 'data',
  id: 'id',
}

/** data-prj-security-save 字段键（payload/result/event 合并去重）。 */
export const DataPrjSecuritySaveKeys = {
  data: 'data',
  id: 'id',
  ok: 'ok',
}

/** data-prj-security-delete 字段键（payload/result/event 合并去重）。 */
export const DataPrjSecurityDeleteKeys = {
  id: 'id',
  ok: 'ok',
}

/** data-prj-security-refresh 字段键（payload/result/event 合并去重）。 */
export const DataPrjSecurityRefreshKeys = {
  id: 'id',
  ids: 'ids',
  instance_id: 'instance_id',
  list: 'list',
  op: 'op',
}

/** data-prompt-list 字段键（payload/result/event 合并去重）。 */
export const DataPromptListKeys = {
  list: 'list',
}

/** data-prompt-load 字段键（payload/result/event 合并去重）。 */
export const DataPromptLoadKeys = {
  data: 'data',
  id: 'id',
}

/** data-prompt-save 字段键（payload/result/event 合并去重）。 */
export const DataPromptSaveKeys = {
  data: 'data',
  id: 'id',
  ok: 'ok',
}

/** data-prompt-delete 字段键（payload/result/event 合并去重）。 */
export const DataPromptDeleteKeys = {
  id: 'id',
  ok: 'ok',
}

/** data-prompt-refresh 字段键（payload/result/event 合并去重）。 */
export const DataPromptRefreshKeys = {
  id: 'id',
  ids: 'ids',
  instance_id: 'instance_id',
  list: 'list',
  op: 'op',
}

/** data-scenario-list 字段键（payload/result/event 合并去重）。 */
export const DataScenarioListKeys = {
  list: 'list',
  ok: 'ok',
}

/** data-scenario-load 字段键（payload/result/event 合并去重）。 */
export const DataScenarioLoadKeys = {
  data: 'data',
  id: 'id',
  level: 'level',
  ok: 'ok',
}

/** data-scenario-save 字段键（payload/result/event 合并去重）。 */
export const DataScenarioSaveKeys = {
  data: 'data',
  id: 'id',
  ok: 'ok',
}

/** data-scenario-delete 字段键（payload/result/event 合并去重）。 */
export const DataScenarioDeleteKeys = {
  id: 'id',
  level: 'level',
  ok: 'ok',
}

/** data-scenario-refresh 字段键（payload/result/event 合并去重）。 */
export const DataScenarioRefreshKeys = {
  id: 'id',
  ids: 'ids',
  instance_id: 'instance_id',
  list: 'list',
  op: 'op',
}

/** data-mcp-list 字段键（payload/result/event 合并去重）。 */
export const DataMcpListKeys = {
  list: 'list',
}

/** data-mcp-load 字段键（payload/result/event 合并去重）。 */
export const DataMcpLoadKeys = {
  data: 'data',
  id: 'id',
  level: 'level',
  name: 'name',
}

/** data-mcp-save 字段键（payload/result/event 合并去重）。 */
export const DataMcpSaveKeys = {
  data: 'data',
  name: 'name',
  ok: 'ok',
  old_level: 'old_level',
  old_name: 'old_name',
}

/** data-mcp-delete 字段键（payload/result/event 合并去重）。 */
export const DataMcpDeleteKeys = {
  id: 'id',
  level: 'level',
  name: 'name',
  ok: 'ok',
}

/** data-mcp-refresh 字段键（payload/result/event 合并去重）。 */
export const DataMcpRefreshKeys = {
  id: 'id',
  ids: 'ids',
  instance_id: 'instance_id',
  list: 'list',
  op: 'op',
}

/** data-memory-list 字段键（payload/result/event 合并去重）。 */
export const DataMemoryListKeys = {
  list: 'list',
}

/** data-memory-read 字段键（payload/result/event 合并去重）。 */
export const DataMemoryReadKeys = {
  category: 'category',
  data: 'data',
}

/** data-memory-save 字段键（payload/result/event 合并去重）。 */
export const DataMemorySaveKeys = {
  data: 'data',
  id: 'id',
  ok: 'ok',
}

/** data-memory-delete 字段键（payload/result/event 合并去重）。 */
export const DataMemoryDeleteKeys = {
  data: 'data',
  id: 'id',
  ok: 'ok',
}

/** data-memory-refresh 字段键（payload/result/event 合并去重）。 */
export const DataMemoryRefreshKeys = {
  id: 'id',
  ids: 'ids',
  instance_id: 'instance_id',
  list: 'list',
  op: 'op',
}

/** data-memory-extract-load 字段键（payload/result/event 合并去重）。 */
export const DataMemoryExtractLoadKeys = {
  category: 'category',
  list: 'list',
  session_id: 'session_id',
}

/** data-memory-extract-save 字段键（payload/result/event 合并去重）。 */
export const DataMemoryExtractSaveKeys = {
  data: 'data',
  id: 'id',
  ok: 'ok',
}

/** data-memory-extract-delete 字段键（payload/result/event 合并去重）。 */
export const DataMemoryExtractDeleteKeys = {
  data: 'data',
  ok: 'ok',
}

/** data-filelist-list 字段键（payload/result/event 合并去重）。 */
export const DataFilelistListKeys = {
  limit: 'limit',
  list: 'list',
  offset: 'offset',
  prefix: 'prefix',
  total: 'total',
}

/** data-filelist-put 字段键（payload/result/event 合并去重）。 */
export const DataFilelistPutKeys = {
  chunks: 'chunks',
  doc_ids: 'doc_ids',
  entries: 'entries',
  id: 'id',
  indexed_at: 'indexed_at',
  key: 'key',
  md5: 'md5',
  mtime: 'mtime',
  ok: 'ok',
  path: 'path',
  size: 'size',
}

/** data-filelist-del 字段键（payload/result/event 合并去重）。 */
export const DataFilelistDelKeys = {
  deleted: 'deleted',
  keys: 'keys',
  ok: 'ok',
}

/** config-refresh 字段键（payload/result/event 合并去重）。 */
export const ConfigRefreshKeys = {
  id: 'id',
  instance_id: 'instance_id',
  list: 'list',
  op: 'op',
}

/** data-session-list 字段键（payload/result/event 合并去重）。 */
export const DataSessionListKeys = {
  filter: 'filter',
  list: 'list',
}

/** data-session-get 字段键（payload/result/event 合并去重）。 */
export const DataSessionGetKeys = {
  data: 'data',
  id: 'id',
}

/** data-session-history 字段键（payload/result/event 合并去重）。 */
export const DataSessionHistoryKeys = {
  before_turn_id: 'before_turn_id',
  messages: 'messages',
  session_id: 'session_id',
  target_bytes: 'target_bytes',
  target_messages: 'target_messages',
}

/** data-session-content 字段键（payload/result/event 合并去重）。 */
export const DataSessionContentKeys = {
  contents: 'contents',
  id: 'id',
  keys: 'keys',
  session_id: 'session_id',
}

/** data-session-latest 字段键（payload/result/event 合并去重）。 */
export const DataSessionLatestKeys = {
  session_id: 'session_id',
}

/** data-session-title 字段键（payload/result/event 合并去重）。 */
export const DataSessionTitleKeys = {
  id: 'id',
  ok: 'ok',
  title: 'title',
}

/** data-session-title-changed 字段键（payload/result/event 合并去重）。 */
export const DataSessionTitleChangedKeys = {
  session_id: 'session_id',
  title: 'title',
}

/** data-session-delete 字段键（payload/result/event 合并去重）。 */
export const DataSessionDeleteKeys = {
  id: 'id',
  ok: 'ok',
}

/** data-session-active-set 字段键（payload/result/event 合并去重）。 */
export const DataSessionActiveSetKeys = {
  ok: 'ok',
  session_id: 'session_id',
}

/** data-session-active-get 字段键（payload/result/event 合并去重）。 */
export const DataSessionActiveGetKeys = {
  session_id: 'session_id',
}

/** data-session-ensure-session 字段键（payload/result/event 合并去重）。 */
export const DataSessionEnsureSessionKeys = {
  ok: 'ok',
  parent_session_id: 'parent_session_id',
  session_id: 'session_id',
}

/** data-session-ensure-turn 字段键（payload/result/event 合并去重）。 */
export const DataSessionEnsureTurnKeys = {
  ok: 'ok',
  session_id: 'session_id',
  turn_id: 'turn_id',
}

/** data-session-append-message 字段键（payload/result/event 合并去重）。 */
export const DataSessionAppendMessageKeys = {
  id: 'id',
  key: 'key',
  msg: 'msg',
  ok: 'ok',
  turn_id: 'turn_id',
}

/** data-session-set-summary 字段键（payload/result/event 合并去重）。 */
export const DataSessionSetSummaryKeys = {
  ok: 'ok',
  summary: 'summary',
  turn_id: 'turn_id',
}

/** data-session-complete-turn 字段键（payload/result/event 合并去重）。 */
export const DataSessionCompleteTurnKeys = {
  brief_tokens: 'brief_tokens',
  finish_reason: 'finish_reason',
  full_tokens: 'full_tokens',
  ok: 'ok',
  status: 'status',
  turn_id: 'turn_id',
}

/** data-session-cleanup-stale 字段键（payload/result/event 合并去重）。 */
export const DataSessionCleanupStaleKeys = {
  count: 'count',
  ok: 'ok',
}

/** data-session-load-messages 字段键（payload/result/event 合并去重）。 */
export const DataSessionLoadMessagesKeys = {
  messages: 'messages',
  turn_id: 'turn_id',
}

/** data-session-context 字段键（payload/result/event 合并去重）。 */
export const DataSessionContextKeys = {
  exclude_turn: 'exclude_turn',
  include_snapshot: 'include_snapshot',
  messages: 'messages',
  session_id: 'session_id',
  turn_tokens: 'turn_tokens',
}

/** data-snapshot-get 字段键（payload/result/event 合并去重）。 */
export const DataSnapshotGetKeys = {
  session_id: 'session_id',
  snapshot: 'snapshot',
}

/** data-snapshot-set 字段键（payload/result/event 合并去重）。 */
export const DataSnapshotSetKeys = {
  ok: 'ok',
  session_id: 'session_id',
  snapshot: 'snapshot',
}

/** data-knowledge-root 字段键（payload/result/event 合并去重）。 */
export const DataKnowledgeRootKeys = {
  kind: 'kind',
  root: 'root',
}

/** data-knowledge-list 字段键（payload/result/event 合并去重）。 */
export const DataKnowledgeListKeys = {
  dir: 'dir',
  dirs: 'dirs',
  files: 'files',
}

/** data-knowledge-read 字段键（payload/result/event 合并去重）。 */
export const DataKnowledgeReadKeys = {
  doc: 'doc',
  path: 'path',
  source: 'source',
}

/** data-knowledge-save 字段键（payload/result/event 合并去重）。 */
export const DataKnowledgeSaveKeys = {
  doc: 'doc',
  ok: 'ok',
  path: 'path',
}

/** data-knowledge-create 字段键（payload/result/event 合并去重）。 */
export const DataKnowledgeCreateKeys = {
  dir: 'dir',
  name: 'name',
  ok: 'ok',
  path: 'path',
  type: 'type',
}

/** data-knowledge-delete 字段键（payload/result/event 合并去重）。 */
export const DataKnowledgeDeleteKeys = {
  ok: 'ok',
  path: 'path',
}

/** data-knowledge-rename 字段键（payload/result/event 合并去重）。 */
export const DataKnowledgeRenameKeys = {
  new_name: 'new_name',
  ok: 'ok',
  path: 'path',
}

/** data-knowledge-mkdir 字段键（payload/result/event 合并去重）。 */
export const DataKnowledgeMkdirKeys = {
  name: 'name',
  ok: 'ok',
  parent: 'parent',
  path: 'path',
}

/** data-knowledge-rmdir 字段键（payload/result/event 合并去重）。 */
export const DataKnowledgeRmdirKeys = {
  ok: 'ok',
  path: 'path',
}

/** data-knowledge-rename-dir 字段键（payload/result/event 合并去重）。 */
export const DataKnowledgeRenameDirKeys = {
  new_name: 'new_name',
  ok: 'ok',
  path: 'path',
}

/** data-tasktree-list 字段键（payload/result/event 合并去重）。 */
export const DataTasktreeListKeys = {
  include_closed: 'include_closed',
  mode: 'mode',
  nodes: 'nodes',
  top_session: 'top_session',
}

/** data-tasktree-tasks 字段键（payload/result/event 合并去重）。 */
export const DataTasktreeTasksKeys = {
  include_closed: 'include_closed',
  list: 'list',
  session_id: 'session_id',
  top_session: 'top_session',
}

/** data-tasktree-delete 字段键（payload/result/event 合并去重）。 */
export const DataTasktreeDeleteKeys = {
  node_id: 'node_id',
  ok: 'ok',
}

/** data-tasktree-upsert 字段键（payload/result/event 合并去重）。 */
export const DataTasktreeUpsertKeys = {
  args_digest: 'args_digest',
  closed: 'closed',
  content: 'content',
  created_at: 'created_at',
  deleted_at: 'deleted_at',
  done_at: 'done_at',
  exec_json: 'exec_json',
  finished_at: 'finished_at',
  instance_id: 'instance_id',
  kind: 'kind',
  ok: 'ok',
  parent_node_id: 'parent_node_id',
  result_digest: 'result_digest',
  session_id: 'session_id',
  shadow: 'shadow',
  started_at: 'started_at',
  state: 'state',
  status: 'status',
  task_id: 'task_id',
  title: 'title',
  tool_call_id: 'tool_call_id',
  top_session: 'top_session',
  work_dir: 'work_dir',
}

/** task-deleted 字段键（payload/result/event 合并去重）。 */
export const TaskDeletedKeys = {
  instance_id: 'instance_id',
  node_id: 'node_id',
}

/** data-index-ignored 字段键（payload/result/event 合并去重）。 */
export const DataIndexIgnoredKeys = {
  codegraph: 'codegraph',
  paths: 'paths',
  truncated: 'truncated',
  vfts: 'vfts',
}

/** instance-claim 字段键（payload/result/event 合并去重）。 */
export const InstanceClaimKeys = {
  data_dir: 'data_dir',
  instance_id: 'instance_id',
  user: 'user',
  work_dir: 'work_dir',
}

/** instance-register 字段键（payload/result/event 合并去重）。 */
export const InstanceRegisterKeys = {
  client_type: 'client_type',
  data_dir: 'data_dir',
  instance_id: 'instance_id',
  work_dir: 'work_dir',
}

/** instance-heartbeat 字段键（payload/result/event 合并去重）。 */
export const InstanceHeartbeatKeys = {
  instance_id: 'instance_id',
}

/** instance-exit 字段键（payload/result/event 合并去重）。 */
export const InstanceExitKeys = {
  instance_id: 'instance_id',
}

/** llm-start 字段键（payload/result/event 合并去重）。 */
export const LlmStartKeys = {
  accepted: 'accepted',
  continue: 'continue',
  effort: 'effort',
  llm: 'llm',
  scenario_id: 'scenario_id',
  session: 'session',
  think: 'think',
  turn: 'turn',
}

/** llm-send 字段键（payload/result/event 合并去重）。 */
export const LlmSendKeys = {
  content: 'content',
  session: 'session',
  tool_call_id: 'tool_call_id',
  turn: 'turn',
  type: 'type',
}

/** llm-cancel 字段键（payload/result/event 合并去重）。 */
export const LlmCancelKeys = {
  cancelled: 'cancelled',
  session: 'session',
  turn: 'turn',
}

/** ask-user-reply 字段键（payload/result/event 合并去重）。 */
export const AskUserReplyKeys = {
  answers: 'answers',
  ask_id: 'ask_id',
}

/** tool-retry 字段键（payload/result/event 合并去重）。 */
export const ToolRetryKeys = {
  instance_id: 'instance_id',
  ok: 'ok',
  session: 'session',
  task_id: 'task_id',
  tool_call_id: 'tool_call_id',
  turn: 'turn',
}

/** task-stop 字段键（payload/result/event 合并去重）。 */
export const TaskStopKeys = {
  cancelled: 'cancelled',
  cascade: 'cascade',
  task_id: 'task_id',
  tool_call_id: 'tool_call_id',
}

/** task-background 字段键（payload/result/event 合并去重）。 */
export const TaskBackgroundKeys = {
  task_id: 'task_id',
  tool_call_id: 'tool_call_id',
}

/** prompt-optimise 字段键（payload/result/event 合并去重）。 */
export const PromptOptimiseKeys = {
  ok: 'ok',
  prompt: 'prompt',
  title: 'title',
  useCase: 'useCase',
}

/** task-verify 字段键（payload/result/event 合并去重）。 */
export const TaskVerifyKeys = {
  cached_rows: 'cached_rows',
  diffs: 'diffs',
  error: 'error',
  include_closed: 'include_closed',
  instance_id: 'instance_id',
  match: 'match',
  only_in_authoritative: 'only_in_authoritative',
  only_in_layer: 'only_in_layer',
  rows: 'rows',
  top_session: 'top_session',
}

/** agent-wizard-probe 字段键（payload/result/event 合并去重）。 */
export const AgentWizardProbeKeys = {
  error: 'error',
  instance_id: 'instance_id',
  ok: 'ok',
  probe: 'probe',
}

/** agent-wizard-compose 字段键（payload/result/event 合并去重）。 */
export const AgentWizardComposeKeys = {
  agents: 'agents',
  choices: 'choices',
  description: 'description',
  error: 'error',
  instance_id: 'instance_id',
  mode: 'mode',
  ok: 'ok',
  probe: 'probe',
  team: 'team',
}

/** agent-wizard-generate 字段键（payload/result/event 合并去重）。 */
export const AgentWizardGenerateKeys = {
  agents: 'agents',
  config: 'config',
  config_saved: 'config_saved',
  description: 'description',
  error: 'error',
  errors: 'errors',
  git_initialized: 'git_initialized',
  init_git: 'init_git',
  instance_id: 'instance_id',
  memory: 'memory',
  memory_saved: 'memory_saved',
  ok: 'ok',
  project_spec: 'project_spec',
  scenario_id: 'scenario_id',
  scene_name: 'scene_name',
  spec_path: 'spec_path',
}

/** agent-wizard-skip 字段键（payload/result/event 合并去重）。 */
export const AgentWizardSkipKeys = {
  instance_id: 'instance_id',
  ok: 'ok',
}

/** llm-simple 字段键（payload/result/event 合并去重）。 */
export const LlmSimpleKeys = {
  instance_id: 'instance_id',
  llm: 'llm',
  prompt: 'prompt',
  system: 'system',
  text: 'text',
}

/** login-register 字段键（payload/result/event 合并去重）。 */
export const LoginRegisterKeys = {
  ok: 'ok',
  password: 'password',
  user: 'user',
  username: 'username',
}

/** login-in 字段键（payload/result/event 合并去重）。 */
export const LoginInKeys = {
  ok: 'ok',
  password: 'password',
  user: 'user',
  username: 'username',
}

/** login-out 字段键（payload/result/event 合并去重）。 */
export const LoginOutKeys = {
  ok: 'ok',
}

/** session-receive 字段键（payload/result/event 合并去重）。 */
export const SessionReceiveKeys = {
  instance_id: 'instance_id',
  payload: 'payload',
  session: 'session',
  turn: 'turn',
  type: 'type',
}

/** session-complete 字段键（payload/result/event 合并去重）。 */
export const SessionCompleteKeys = {
  code: 'code',
  finish_reason: 'finish_reason',
  instance_id: 'instance_id',
  message: 'message',
  parents: 'parents',
  retryable: 'retryable',
  session: 'session',
  status: 'status',
  text: 'text',
  turn: 'turn',
}

/** session-compress 字段键（payload/result/event 合并去重）。 */
export const SessionCompressKeys = {
  instance_id: 'instance_id',
  last_turn: 'last_turn',
  max_context_token: 'max_context_token',
  max_output_token: 'max_output_token',
  session: 'session',
  snapshot_turn: 'snapshot_turn',
}

/** session-ask 字段键（payload/result/event 合并去重）。 */
export const SessionAskKeys = {
  ask_id: 'ask_id',
  expires_at: 'expires_at',
  instance_id: 'instance_id',
  questions: 'questions',
  session: 'session',
  turn: 'turn',
}

/** session-turn-start 字段键（payload/result/event 合并去重）。 */
export const SessionTurnStartKeys = {
  instance_id: 'instance_id',
  parents: 'parents',
  session: 'session',
  turn: 'turn',
}

/** session-new 字段键（payload/result/event 合并去重）。 */
export const SessionNewKeys = {
  instance_id: 'instance_id',
  parent_session_id: 'parent_session_id',
  session_id: 'session_id',
}

/** task-started 字段键（payload/result/event 合并去重）。 */
export const TaskStartedKeys = {
  args: 'args',
  kind: 'kind',
  session: 'session',
  state: 'state',
  task_id: 'task_id',
  tool: 'tool',
  turn: 'turn',
  work_dir: 'work_dir',
}

/** task-updated 字段键（payload/result/event 合并去重）。 */
export const TaskUpdatedKeys = {
  args: 'args',
  kind: 'kind',
  session: 'session',
  state: 'state',
  task_id: 'task_id',
  tool: 'tool',
  turn: 'turn',
  work_dir: 'work_dir',
}

/** task-done 字段键（payload/result/event 合并去重）。 */
export const TaskDoneKeys = {
  args: 'args',
  kind: 'kind',
  session: 'session',
  state: 'state',
  task_id: 'task_id',
  tool: 'tool',
  turn: 'turn',
  work_dir: 'work_dir',
}

/** server-starting 字段键（payload/result/event 合并去重）。 */
export const ServerStartingKeys = {
  started_at: 'started_at',
}

/** prompt-optimised 字段键（payload/result/event 合并去重）。 */
export const PromptOptimisedKeys = {
  content: 'content',
  error: 'error',
  instance_id: 'instance_id',
  type: 'type',
}

/** tool-notify 字段键（payload/result/event 合并去重）。 */
export const ToolNotifyKeys = {
  instance_id: 'instance_id',
  kind: 'kind',
  message: 'message',
  message_id: 'message_id',
  notice: 'notice',
  plugin: 'plugin',
  reason: 'reason',
  session_id: 'session_id',
  task_id: 'task_id',
  turn_id: 'turn_id',
}

/** mcp-tools-list 字段键（payload/result/event 合并去重）。 */
export const McpToolsListKeys = {
  cursor: 'cursor',
  resultType: 'resultType',
  tools: 'tools',
  ttlMs: 'ttlMs',
}

/** mcp-tools-call 字段键（payload/result/event 合并去重）。 */
export const McpToolsCallKeys = {
  arguments: 'arguments',
  content: 'content',
  isError: 'isError',
  name: 'name',
  resultType: 'resultType',
  structuredContent: 'structuredContent',
}

/** mcp-tools-wait 字段键（payload/result/event 合并去重）。 */
export const McpToolsWaitKeys = {
  task_id: 'task_id',
  tool_call_id: 'tool_call_id',
  waiting: 'waiting',
}

/** mcp-tools-register 字段键（payload/result/event 合并去重）。 */
export const McpToolsRegisterKeys = {
  async: 'async',
  async_threshold: 'async_threshold',
  category: 'category',
  description: 'description',
  handler_subject: 'handler_subject',
  hot: 'hot',
  kind: 'kind',
  name: 'name',
  owner: 'owner',
  pre_hook_subject: 'pre_hook_subject',
  registered: 'registered',
  schema: 'schema',
  scope: 'scope',
  timeout: 'timeout',
}

/** mcp-tools-unregister 字段键（payload/result/event 合并去重）。 */
export const McpToolsUnregisterKeys = {
  kind: 'kind',
  name: 'name',
  scope: 'scope',
  unregistered: 'unregistered',
}

/** mcp-prompts-list 字段键（payload/result/event 合并去重）。 */
export const McpPromptsListKeys = {
  prompts: 'prompts',
}

/** mcp-prompts-get 字段键（payload/result/event 合并去重）。 */
export const McpPromptsGetKeys = {
  arguments: 'arguments',
  content: 'content',
  description: 'description',
  name: 'name',
  type: 'type',
}

/** mcp-prompts-register 字段键（payload/result/event 合并去重）。 */
export const McpPromptsRegisterKeys = {
  arguments: 'arguments',
  asset_kind: 'asset_kind',
  content: 'content',
  description: 'description',
  kind: 'kind',
  name: 'name',
  path: 'path',
  registered: 'registered',
  scope: 'scope',
}

/** mcp-prompts-unregister 字段键（payload/result/event 合并去重）。 */
export const McpPromptsUnregisterKeys = {
  asset_kind: 'asset_kind',
  kind: 'kind',
  name: 'name',
  scope: 'scope',
  unregistered: 'unregistered',
}

/** mcp-resources-list 字段键（payload/result/event 合并去重）。 */
export const McpResourcesListKeys = {
  resources: 'resources',
}

/** mcp-resources-read 字段键（payload/result/event 合并去重）。 */
export const McpResourcesReadKeys = {
  resource: 'resource',
  uri: 'uri',
}

/** mcp-resources-register 字段键（payload/result/event 合并去重）。 */
export const McpResourcesRegisterKeys = {
  content: 'content',
  description: 'description',
  kind: 'kind',
  mimetype: 'mimetype',
  name: 'name',
  path: 'path',
  registered: 'registered',
  scope: 'scope',
  uri: 'uri',
}

/** mcp-resources-unregister 字段键（payload/result/event 合并去重）。 */
export const McpResourcesUnregisterKeys = {
  kind: 'kind',
  name: 'name',
  scope: 'scope',
  unregistered: 'unregistered',
}

/** mcp-servers-list 字段键（payload/result/event 合并去重）。 */
export const McpServersListKeys = {
  server: 'server',
  servers: 'servers',
}

/** mcp-servers-get 字段键（payload/result/event 合并去重）。 */
export const McpServersGetKeys = {
  server: 'server',
}

/** mcp-servers-register 字段键（payload/result/event 合并去重）。 */
export const McpServersRegisterKeys = {
  mcp_server: 'mcp_server',
  name: 'name',
  namespace: 'namespace',
  origin: 'origin',
  registered: 'registered',
  scope: 'scope',
  transport: 'transport',
  url: 'url',
}

/** mcp-servers-unregister 字段键（payload/result/event 合并去重）。 */
export const McpServersUnregisterKeys = {
  name: 'name',
  scope: 'scope',
  unregistered: 'unregistered',
}

/** mcp-gateway-check 字段键（payload/result/event 合并去重）。 */
export const McpGatewayCheckKeys = {
  ok: 'ok',
  servers: 'servers',
  tools: 'tools',
}

/** mcp-gateway-reload 字段键（payload/result/event 合并去重）。 */
export const McpGatewayReloadKeys = {
  refreshed: 'refreshed',
  reloaded: 'reloaded',
  removed_tools: 'removed_tools',
}

/** mcp-tasks-report 字段键（payload/result/event 合并去重）。 */
export const McpTasksReportKeys = {
  instance_id: 'instance_id',
  parent: 'parent',
  result_summary: 'result_summary',
  session: 'session',
  state: 'state',
  task_id: 'task_id',
  tool: 'tool',
  tool_call_id: 'tool_call_id',
  top_session: 'top_session',
  turn: 'turn',
}

/** mcp-tools-timeout 字段键（payload/result/event 合并去重）。 */
export const McpToolsTimeoutKeys = {
  instance_id: 'instance_id',
  options: 'options',
  reason: 'reason',
  task_id: 'task_id',
  timeout_s: 'timeout_s',
  tool: 'tool',
  tool_call_id: 'tool_call_id',
}

/** mcp-gateway-changed 字段键（payload/result/event 合并去重）。 */
export const McpGatewayChangedKeys = {
  instance_id: 'instance_id',
  kind: 'kind',
  name: 'name',
  status: 'status',
}

/** history-pre-tool-hook 字段键（payload/result/event 合并去重）。 */
export const HistoryPreToolHookKeys = {
  context: 'context',
  tool: 'tool',
  touch_files: 'touch_files',
}

/** codegraph-tool-call 字段键（payload/result/event 合并去重）。 */
export const CodegraphToolCallKeys = {
  args: 'args',
  content: 'content',
  context: 'context',
  isError: 'isError',
  resultType: 'resultType',
  tool: 'tool',
}

/** vfts-tool-call 字段键（payload/result/event 合并去重）。 */
export const VftsToolCallKeys = {
  args: 'args',
  content: 'content',
  context: 'context',
  isError: 'isError',
  resultType: 'resultType',
  tool: 'tool',
}

/** vfts.dict.get 字段键（payload/result/event 合并去重）。 */
export const VftsDictGetKeys = {
  base_dicts: 'base_dicts',
  dict_dir: 'dict_dir',
  error: 'error',
  instance_id: 'instance_id',
  ok: 'ok',
  user_dict: 'user_dict',
  user_dict_path: 'user_dict_path',
  word_count: 'word_count',
}

/** vfts.dict.set 字段键（payload/result/event 合并去重）。 */
export const VftsDictSetKeys = {
  error: 'error',
  instance_id: 'instance_id',
  ok: 'ok',
  user_dict: 'user_dict',
  word_count: 'word_count',
}

/** vfts.reindex 字段键（payload/result/event 合并去重）。 */
export const VftsReindexKeys = {
  instance_id: 'instance_id',
  ok: 'ok',
  started: 'started',
}

/** project-config-open 字段键（payload/result/event 合并去重）。 */
export const ProjectConfigOpenKeys = {
  tab: 'tab',
}

/** preview-tab-open 字段键（payload/result/event 合并去重）。 */
export const PreviewTabOpenKeys = {
  kind: 'kind',
  tab: 'tab',
  title: 'title',
}

【目录与运行环境】ChonkPilot 采用四级能力目录，每级根下是 capability/ 能力面；同名以最具体级优先（项目私有 > 项目 > 用户 > 系统）。

四级数据根：
- 系统级（app）：<exeDir> = {{path.exeDir}}
- 用户级（user）：~/.chonkpilot = {{path.userDir}}
- 项目级（project）：<workDir>/.chonkpilot（项目工作目录 <workDir> = {{path.workDir}}）
- 项目私有级（prjusr）：~/.chonkpilot/data/<项目id> = {{path.dataDir}}

每级 capability/ 下的子目录：
- tools/ 工具（tool 契约）
- skills/ 技能
- prompts/ 提示词（命令 / 知识库提示词）
- resources/ 资源（知识）
- agents/ 智能体（*.agent.md）
- scenarios/ 场景（每场景一目录：scenario.json + *.agent.md）
- mcps/ MCP server 配置（<名称>.json；非原语）
- system/ 系统内部提示词（纯文本，只读；不进知识库原语树、不进左侧导航）

DSL 用法：脚本默认注入只读环境变量 CHONKPILOT_WORKDIR（项目工作目录）、CHONKPILOT_DATADIR（数据目录）、CHONKPILOT_TEMPDIR（临时目录）、CHONKPILOT_EXEDIR（可执行文件所在目录）、CHONKPILOT_PROJECT（项目目录，与 CHONKPILOT_WORKDIR 同值）。脚本内引用只能写 {{env.CHONKPILOT_WORKDIR}} 这类形式（无裸名）；拼绝对路径须用 {{env.CHONKPILOT_*}}，而 {{path.*}} 占位符用于提示词文本，由系统在注入时替换为上述实际绝对路径。

【第三方工具接入（能力外溢）】进程外工具 / 插件无需嵌入本仓代码，即可复用本机 server 的同一套原子能力：
- 接入：连接插件桥（UDS 优先；TCP 仅 127.0.0.1）→ `plugin.hello{id}` → 按需 `plugin.subscribe` → 调用 `plugin/v1/*`（请求带 `instance_id`）。
- 能力面：会话/LLM（`session.start|send|cancel|list|history`、`llm.step`）· 工具（`tools.list` / `tools.call`，由 core-mcp-server 一工具一 exe 执行）· 文件与配置（`file.*` / `config.*` / `knowledge.*`）· 任务（`task.tree|tasks|stop`）· GUI（`gui.list|apply`）。
- LLM 兼容入口（llm-bridge）：`http://127.0.0.1:5669/llm/<provider>/...` —— 按 OpenAI / Anthropic 协议原样转发到对应 provider，仅 loopback、免 api-key；第三方工具可直接把它当作 LLM `base_url` 使用。
- 外部工具若需与 LLM 工具面互通，用 `tools.register{handler_subject}` 声明回调主题，再由 `tools.call` 触发；不要绕过插件桥直连内部数据库。

【用户交互（ask_user）】仅在必须由用户做**简单抉择**（是/否、少量选项）时使用；开放式提问、需要长文本或需多轮澄清的问题**不要**用本工具——改用普通对话**逐轮**提问（一次只问一个小问题）。能从上下文推断或用合理默认值推进的，直接推进并在回复中说明假设。

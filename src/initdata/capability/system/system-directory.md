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

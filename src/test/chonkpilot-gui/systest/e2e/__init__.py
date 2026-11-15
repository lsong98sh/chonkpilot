# -*- coding: utf-8 -*-
"""场景化 E2E 测试点包（单实例 / 分阶段）。

模块职责：
    framework.py       注册表 + 阶段执行器 + 问题累计 + 报告
    workspace.py       空工程 → 有数据工程（LLM 工具预置，幂等）
    cases_empty.py     empty 阶段：空工程基线 / 首个文件 / 文件面 / 配置 / 会话
    cases_files.py     seeded 阶段：文件树 / 预览页签 / 搜索 / 上传
    cases_chat.py      seeded 阶段：会话消息 / LLM 工具调用链
    cases_task.py      seeded 阶段：任务树（真实 task_run）
    cases_data.py      seeded 阶段：配置 / 知识库 / 记忆库 / 插件 / 主题 / 重启前置
    cases_window.py    seeded 阶段：窗口最大化/尺寸（宿主权威）/ 分隔条拖拽 / 真实鼠标拖拽
    cases_agent.py     seeded 阶段：ask_user 待办与超时 / 子代理·单聊 / 群聊·多聊
    cases_mw.py        seeded 阶段：多窗口（开关 / 定向控制 / 本地事件不跨窗 / 重复打开）
    cases_filetree_ops.py  seeded 阶段：文件树交互/右键/拖拽/监听/预览类型
    cases_preview_ops.py   seeded 阶段：预览区（Markdown/源码切换/Hex/页签固定）
    cases_chat_ops.py  seeded 阶段：排队消息 / 输入区上方 / 轮次导航
    cases_chat_ui.py   seeded 阶段：主 chat UI / 富文本输入与附件
    cases_ui_ops.py    seeded 阶段：会话导航 / 状态栏长任务条目
    cases_session_ops.py seeded 阶段：会话消息面契约（空/不存在语义）
    cases_toolbar_ops.py seeded 阶段：顶栏条目化（结构/菜单/检索/主题/语言）
    cases_task_ops.py  seeded 阶段：任务视图（tasktree / stop / llm 耗时 / 终止会话）
    cases_plugin_ops.py seeded 阶段：插件维护视图（卡片/manifest/开关/配置页）
    cases_cfg_ui.py    seeded 阶段：配置 UI（项目配置/用户配置/场景/导入导出）
    cases_destructive.py destructive 阶段：破坏性用例（恢复出厂；默认不跑）
    cases_restart.py   restart 阶段：布局 / 窗口 / 页签 / 会话 / 配置 / 主题 持久化
    coverage.py        旧套件覆盖台账（抽 run_case/.check 标题 → COVERAGE.md）
"""
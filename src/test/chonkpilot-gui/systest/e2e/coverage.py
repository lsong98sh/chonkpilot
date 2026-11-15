# -*- coding: utf-8 -*-
"""覆盖台账：把 `systest/` 下**全部旧套件**的用例逐条抽出，标注「新套件已纳入 / 仅旧套件覆盖」。

目的（用户要求「原来有的不能丢失」）：

* 旧套件**一个都没删**，仍由 `run_all.py` 全量调度（这是不丢失的底线）；
* 本台账给出可审计的迁移进度：每个旧脚本抽到多少条用例、对应到哪些新测试点、
  尚未迁移的仍是「旧套件覆盖」——新增/迁移不会静默漏项。

生成：`python e2e/coverage.py`（写入 `e2e/COVERAGE.md`）或 `python run_e2e.py --coverage`。
"""

import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SYSTEST = os.path.dirname(HERE)

# 旧套件 → 已纳入的新测试点（**部分覆盖**；未列出的套件 = 仍仅由旧套件覆盖）
MIGRATED = {
    "test_window.py": ["S-WIN-01", "S-WIN-02", "S-WIN-03", "R-WIN-01"],
    "test_layout.py": ["S-SPLIT-01", "R-LAYOUT-01", "S-WIN-01"],
    "run_sem_window_layout_loop.py": ["S-WIN-01", "S-WIN-02", "S-SPLIT-01",
                                      "R-LAYOUT-01", "R-WIN-01", "R-THEME-01"],
    "run_sem_filetree_loop.py": ["S-FS-01", "S-FS-03", "E-FS-02"],
    "run_sem_filetree_tree.py": ["S-FS-01", "S-FS-02"],
    "run_sem_tabs_loop.py": ["S-FS-02", "S-FS-04", "R-TABS-01"],
    "run_restore_tabs.py": ["S-FS-04", "R-TABS-01"],
    "run_sem_messages.py": ["S-CHAT-01"],
    "run_sem_session_loop.py": ["E-SESS-01", "R-SESS-01"],
    "run_sem_task_tree.py": ["S-TASK-01"],
    "run_sem_tool_result.py": ["S-CHAT-02"],
    "run_sem_config_loop.py": ["S-CFG-02", "R-CFG-01"],
    "run_sem_config_fallback.py": ["E-CFG-01", "S-CFG-03"],
    "run_sem_kb_loop.py": ["S-KB-01"],
    "run_sem_memory_edit.py": ["S-MEM-01"],
    "run_sem_search_upload.py": ["S-SEARCH-01", "S-UPLOAD-01"],
    "run_ask_ui.py": ["S-ASK-01", "S-ASK-02", "S-ASK-03"],
    "run_mw.py": ["S-MW-01", "S-MW-02", "S-MW-03", "S-MW-04"],
    "run_llm.py": ["S-CHAT-01", "S-CHAT-02", "S-TASK-01"],
    # 2026-11-11 第二批按领域迁移
    "test_filetree_interact.py": ["S-FT-01"],
    "test_filetree_ctxmenu.py": ["S-FT-02"],
    "test_filetree_dnd.py": ["S-FT-03"],
    "test_filetree_watcher.py": ["S-FT-04"],
    "run_filetree.py": ["S-FT-04", "S-FT-05", "S-FT-06", "R-FT-01"],
    "test_queue.py": ["S-CHAT-QUEUE-01"],
    "test_chat_input_top.py": ["S-CHAT-TOP-01"],
    "test_turn_nav.py": ["S-TURN-NAV-01"],
    "test_session_drawer.py": ["S-SESS-UI-01"],
    "test_statusbar.py": ["S-STATUS-01", "S-STATUS-02"],
    "test_task.py": ["S-TASK-UI-01", "S-ASK-01", "S-ASK-02", "S-ASK-03"],
    "test_plugin_manager.py": ["S-PLUG-UI-01"],
    "test_config.py": ["S-CFG-UI-01", "D-CFG-01"],
    "test_preview.py": ["S-PREVIEW-01"],
    "test_chat.py": ["S-CHAT-UI-01"],
    "test_richtext.py": ["S-CHAT-RICH-01"],
    "test_session_bridge.py": ["S-SESS-BRIDGE-01"],
    "test_filetree.py": ["S-FS-05"],
    "test_chat_flow.py": ["S-CHAT-FLOW-01"],
    "test_toolbar.py": ["S-TOOLBAR-01"],
    "run_config.py": ["S-CFG-DATA-01"],
    "run_task_ui.py": ["S-TASK-UI-02"],
    "run_preview_ui.py": ["S-PREVIEW-01", "S-PREVIEW-02"],
    "run_fp_extra_ui.py": ["S-SCEN-EDIT-01", "S-SCEN-MATRIX-01", "S-CFG-PROMPT-01"],
    "run_chat_ui.py": ["S-CHAT-MSG-01", "S-CHAT-FLOW-01"],
    "run_project.py": ["S-PRJ-DATA-01"],
    "run_project_cfg.py": ["S-CFG-UI-01", "S-CFG-PROMPT-01"],
    "run_crontab.py": ["S-CRON-01"],
    "test_screenshot.py": ["S-SHOT-01"],
    "test_tool_retry.py": ["S-TOOL-RETRY-01"],
    "run_llm_errors.py": ["S-LLM-RETRY-01", "S-CHAT-FLOW-01"],
    "run_server_deps.py": ["S-CHAT-SCROLL-01", "S-CHAT-FLOW-01", "S-ASK-01"],
    "run_paths.py": ["S-PATHS-01", "R-PATHS-01"],
    "run_tools.py": ["S-TOOLS-RUN-01", "S-ASK-03"],
    "run_tool_progress.py": ["S-TOOL-PROGRESS-01"],
    "run_scenario_reload.py": ["S-SCEN-RELOAD-01"],
    "run_data_shard.py": ["S-SHARD-01", "R-SHARD-01"],
    "run_llm_config.py": ["S-LLM-CONFIG-01"],
    "test_plugin_config_e2e.py": ["S-PLUG-UI-01", "S-PLUG-DEEP-01", "S-CRON-01"],
    # 2026-11-11 第三批：原语/错误语义/任务状态/记忆上下文/场景/UI/索引/MQ
    "run_sem_kb_skill_loop.py": ["S-KB-PRIM-01"],
    "run_primitive_hot.py": ["S-KB-PRIM-01"],
    "run_s21_matrix.py": ["S-ERR-MATRIX-01"],
    "run_sem_error_recovery.py": ["S-ERR-MATRIX-01"],
    "run_sem_retry_state.py": ["S-ERR-MATRIX-01", "S-TOOL-RETRY-01"],
    "run_sem_task_status_loop.py": ["S-TASK-STATUS-01"],
    "run_sem_prompt_optimise.py": ["S-PROMPT-OPT-01"],
    "run_memory_ctx.py": ["S-MEM-CTX-01", "S-MEM-TREE-01"],
    "run_scenario_prj.py": ["S-SCEN-PRJ-01"],
    "run_scenario_agent_dup.py": ["S-SCEN-DUP-01"],
    "run_sem_scenario_ref.py": ["S-SCEN-REF-01"],
    "run_ui_regressions.py": ["S-UI-REG-01", "S-PREVIEW-02", "S-EXPLORE-01"],
    "run_index_gate.py": ["S-IDX-01"],
    "run_mq_scope.py": ["S-MQ-SCOPE-01", "R-MQ-SCOPE-01"],
    "run_explore_kb.py": ["S-EXPLORE-01"],
    "run_meta_tools.py": ["S-META-TOOLS-01"],
    "run_llm_bridge.py": ["S-BRIDGE-01"],
    "run_docs_gate.py": ["S-DOCS-01"],
    "run_hist_git.py": ["S-HIST-01"],
    "run_tool_async.py": ["S-TOOL-ASYNC-01"],
    "run_wizard_auto.py": ["S-WIZARD-01"],
    "run_sem_wizard.py": ["S-WIZARD-01"],
    "run_tool_async_config.py": ["S-TOOL-CFG-01"],
    "run_tool_sandbox.py": ["S-TOOL-CFG-01"],
    "test_tool_async_config.py": ["S-TOOL-CFG-01"],
    "run_mcp_fields.py": ["S-MCP-FIELDS-01", "S-CFG-DATA-01"],
    "run_inbox.py": ["S-INBOX-01"],
    "run_llm_fields.py": ["S-LLM-FIELDS-01"],
    "run_memory_tree.py": ["S-MEM-TREE-01"],
    "run_config_ui.py": ["S-CFG-UI-01", "S-CFG-DATA-01"],
    "run_css_themes.py": ["S-THEME-01", "S-PLUG-DEEP-01"],
    "run_plugin_gui.py": ["S-PLUG-UI-01", "S-PLUG-DEEP-01"],
    "run_session_active.py": ["R-SESS-01", "S-SESS-BRIDGE-01"],
    "run_tool_async_config.py": ["S-TOOL-CFG-01"],
    "run_tool_sandbox.py": ["S-TOOL-CFG-01", "S-MCP-FIELDS-01"],
    "mock_llm.py": [],
}

# 只抽「具名用例」：run_case("...") / .check("...") / check("...")
_RE_CASE = re.compile(r"run_case\(\s*[\"']([^\"']{2,120})[\"']")
_RE_CHECK = re.compile(r"\.check\(\s*[\"']([^\"']{2,120})[\"']")


def _suite_files():
    names = []
    for n in sorted(os.listdir(SYSTEST)):
        if not n.endswith(".py"):
            continue
        if n in ("run_all.py", "run_e2e.py", "run_e2e_destructive.py",
                 "harness.py", "chonk_client.py",
                 "drive.py", "mock_llm.py", "mock_sse.py", "mock_slow_mcp.py",
                 "mock_mcp_probe.py", "plugin_client.py"):
            continue
        if n.startswith("test_") or n.startswith("run_"):
            names.append(n)
    return names


def extract(path):
    """抽出脚本内的具名用例（去重、保序）。"""
    with open(path, "r", encoding="utf-8", errors="replace") as f:
        text = f.read()
    out, seen = [], set()
    for m in list(_RE_CASE.finditer(text)) + list(_RE_CHECK.finditer(text)):
        title = m.group(1).strip()
        if title and title not in seen:
            seen.add(title)
            out.append(title)
    return out


def scan():
    rows = []
    for name in _suite_files():
        titles = extract(os.path.join(SYSTEST, name))
        rows.append({"suite": name, "titles": titles,
                     "mapped": MIGRATED.get(name, []),
                     "legacy_only": name not in MIGRATED})
    return rows


def build_text(rows):
    total_cases = sum(len(r["titles"]) for r in rows)
    mapped = [r for r in rows if r["mapped"]]
    unmapped = [r for r in rows if not r["mapped"]]
    mapped_cases = sum(len(r["titles"]) for r in mapped)
    lines = [
        "# 旧套件覆盖台账（自动生成，勿手改）",
        "",
        "> 生成：`python src/test/chonkpilot-gui/systest/e2e/coverage.py`",
        "> 口径：旧脚本**全部保留**（不删除）；默认门禁仅跑 `run_e2e.py`，旧套件经 `run_all.py --group legacy` 仍可执行。本表标注新场景套件（`run_e2e.py`）的迁移进度。`run_case`/`.check` 字面量用例被抽取计数（f-string/动态名不计）。",
        "",
        "## 汇总",
        "",
        "| 指标 | 数量 |",
        "|---|---|",
        "| 旧套件脚本 | %d |" % len(rows),
        "| 抽到的具名用例 | %d |" % total_cases,
        "| 已有新套件对应（部分覆盖） | %d 个脚本 / %d 条用例 |" % (len(mapped), mapped_cases),
        "| 仍仅由旧套件覆盖 | %d 个脚本 / %d 条用例 |"
        % (len(unmapped), total_cases - mapped_cases),
        "",
        "## 逐套件",
        "",
        "| 旧套件 | 用例数 | 新套件测试点（部分覆盖） | 状态 |",
        "|---|---|---|---|",
    ]
    for r in sorted(rows, key=lambda x: (not x["mapped"], x["suite"])):
        mapping = ", ".join("`%s`" % x for x in r["mapped"]) or "—"
        state = "部分纳入" if r["mapped"] else "仅旧套件"
        lines.append("| `%s` | %d | %s | %s |"
                     % (r["suite"], len(r["titles"]), mapping, state))
    lines += ["", "## 明细（逐套件用例标题）", ""]
    for r in rows:
        lines.append("### `%s`（%d 条）" % (r["suite"], len(r["titles"])))
        if not r["titles"]:
            lines.append("")
            lines.append("（无可抽取的具名字面量用例）")
        for t in r["titles"]:
            lines.append("- %s" % t.replace("\n", " ")[:180])
        lines.append("")
    return "\n".join(lines) + "\n"


def main(write=True):
    rows = scan()
    text = build_text(rows)
    if write:
        path = os.path.join(HERE, "COVERAGE.md")
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        print("已写入 %s" % path)
    total = sum(len(r["titles"]) for r in rows)
    mapped = [r for r in rows if r["mapped"]]
    mapped_cases = sum(len(r["titles"]) for r in mapped)
    print("旧套件 %d 个 / 具名用例 %d 条；部分纳入 %d 个脚本（%d 条），"
          "仅旧套件覆盖 %d 个脚本（%d 条）"
          % (len(rows), total, len(mapped), mapped_cases,
             len(rows) - len(mapped), total - mapped_cases))
    return 0


if __name__ == "__main__":
    sys.exit(main())
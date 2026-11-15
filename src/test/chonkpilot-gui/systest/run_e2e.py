# -*- coding: utf-8 -*-
"""场景化 E2E 运行器：**一次启动**跑完整工程场景；跨重启用例单独一个阶段。

与 `run_all.py`（逐套件各起一个 GUI）互补：

    python run_e2e.py                          # 全量（空工程 → 预置 → 有数据工程 → 重启）
    python run_e2e.py --list                   # 列出全部测试点（ID/阶段/功能点/说明）
    python run_e2e.py --init empty             # 只测「空工程」初始化模式
    python run_e2e.py --init seeded            # 只测「有数据工程」模式（含重启阶段）
    python run_e2e.py --only S-FS-01,S-CHAT-01 # 只测指定测试点
    python run_e2e.py --area 文件树 --area 任务树
    python run_e2e.py -k FS -k CHAT            # 按 ID/功能点子串过滤
    python run_e2e.py --max-issues 3           # 累计 3 个问题后中断（默认 0 = 不中断）
    python run_e2e.py --cleanup no             # 保留工作区/用户数据（--keep 同义）
    python run_e2e.py --cleanup ask            # 测试结束后交互确认是否清理
    python run_e2e.py --workspace D:/tmp/ws    # 指定工作区（幂等预置；永不自动删除）
    python run_e2e.py --no-restart             # 严格单次启动（跳过 restart 阶段）

产物：
* 工作区（默认 `<systest>/e2e_runs/<run_id>/ws`）与独立 HOME（同目录 `home/`）
* 报告始终保留：`<systest>/e2e_reports/<run_id>/report.md` + `report.json`
"""

import argparse
import json
import os
import shutil
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

from e2e import framework as fw  # noqa: E402
from e2e import cases_empty  # noqa: E402,F401  （导入即注册）
from e2e import cases_files  # noqa: E402,F401
from e2e import cases_preview_ops  # noqa: E402,F401
from e2e import cases_preview_tabs  # noqa: E402,F401
from e2e import cases_filetree_ops  # noqa: E402,F401
from e2e import cases_chat  # noqa: E402,F401
from e2e import cases_chat_ops  # noqa: E402,F401
from e2e import cases_chat_ui  # noqa: E402,F401
from e2e import cases_task  # noqa: E402,F401
from e2e import cases_task_ops  # noqa: E402,F401
from e2e import cases_data  # noqa: E402,F401
from e2e import cases_cfg_ui  # noqa: E402,F401
from e2e import cases_destructive  # noqa: E402,F401
from e2e import cases_window  # noqa: E402,F401
from e2e import cases_agent  # noqa: E402,F401
from e2e import cases_mw  # noqa: E402,F401
from e2e import cases_ui_ops  # noqa: E402,F401
from e2e import cases_toolbar_ops  # noqa: E402,F401
from e2e import cases_session_ops  # noqa: E402,F401
from e2e import cases_scenario_ops  # noqa: E402,F401
from e2e import cases_project_ops  # noqa: E402,F401
from e2e import cases_plugin_ops  # noqa: E402,F401
from e2e import cases_cron  # noqa: E402,F401
from e2e import cases_screenshot_ops  # noqa: E402,F401
from e2e import cases_llm_ops  # noqa: E402,F401
from e2e import cases_chat_scroll  # noqa: E402,F401
from e2e import cases_paths_ops  # noqa: E402,F401
from e2e import cases_tool_ops  # noqa: E402,F401
from e2e import cases_tool_progress  # noqa: E402,F401
from e2e import cases_mcp_ops  # noqa: E402,F401
from e2e import cases_plugin_deep  # noqa: E402,F401
from e2e import cases_memory_ui  # noqa: E402,F401
from e2e import cases_tool_cfg  # noqa: E402,F401
from e2e import cases_explore_ui  # noqa: E402,F401
from e2e import cases_kb_primitives  # noqa: E402,F401
from e2e import cases_error_semantics  # noqa: E402,F401
from e2e import cases_semantics_more  # noqa: E402,F401
from e2e import cases_memory_ctx  # noqa: E402,F401
from e2e import cases_scenario_more  # noqa: E402,F401
from e2e import cases_ui_reg  # noqa: E402,F401
from e2e import cases_codegraph  # noqa: E402,F401
from e2e import cases_mq_scope  # noqa: E402,F401
from e2e import cases_wizard  # noqa: E402,F401
from e2e import cases_bridge  # noqa: E402,F401
from e2e import cases_tool_async  # noqa: E402,F401
from e2e import cases_history  # noqa: E402,F401
from e2e import cases_docs  # noqa: E402,F401
from e2e import cases_restart  # noqa: E402,F401

E2E_RUNS = os.path.join(HERE, "e2e_runs")
E2E_REPORTS = os.path.join(HERE, "e2e_reports")
SHARED_WS = os.path.join(E2E_RUNS, "ws_shared")


def _run_id():
    return time.strftime("%Y%m%d-%H%M%S")


def _parse_args(argv):
    ap = argparse.ArgumentParser(
        description="场景化 E2E：单实例多阶段（空工程 → LLM 工具预置 → 有数据工程 → 重启）",
        formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--list", action="store_true", help="只列出测试点后退出")
    ap.add_argument("--coverage", action="store_true",
                    help="生成并打印旧套件覆盖台账（e2e/COVERAGE.md）后退出")
    ap.add_argument("--only", action="append", default=[], metavar="ID[,ID]",
                    help="仅跑指定测试点 ID（可多次/逗号分隔）")
    ap.add_argument("--area", action="append", default=[], metavar="AREA",
                    help="仅跑指定领域（可多次）：空工程/LLM 工具/文件面/文件树/预览/搜索/上传/"
                         "会话消息/工具链路/任务树/配置/知识库/记忆库/插件平台/主题/窗口/布局/"
                         "ask_user/子代理/群聊/多窗口/页签持久化/布局持久化/窗口持久化/会话持久化/"
                         "配置持久化/主题持久化/持久化前置")
    ap.add_argument("-k", action="append", default=[], metavar="SUBSTR",
                    help="按 ID/功能点子串过滤（可多次，取并集）")
    ap.add_argument("--init", choices=("auto", "empty", "seeded", "destructive"),
                    default="auto",
                    help="初始化模式：auto=全流程（默认）· empty=只跑空工程测试点 · "
                         "seeded=只跑有数据工程测试点（含重启阶段）· destructive=只跑破坏性测试点"
                         "（需再加 --with-destructive 或直接用 run_e2e_destructive.py）")
    ap.add_argument("--with-destructive", "--destructive", action="store_true",
                    dest="with_destructive",
                    help="纳入 stage=destructive 的破坏性测试点（永远最后执行；默认全量不含）")
    ap.add_argument("--max-issues", type=int, default=0, metavar="N",
                    help="累计问题**超过** N 个后中断后续测试点（默认 0 = 记录但不中断）")
    ap.add_argument("--cleanup", choices=("auto", "yes", "no", "ask"), default="auto",
                    help="结束清理：auto=删自建工作区/HOME（默认）· yes=强制清理 · "
                         "no=全部保留 · ask=交互确认")
    ap.add_argument("--keep", action="store_true", help="等价 --cleanup no")
    ap.add_argument("--workspace", default="", metavar="DIR",
                    help="显式工作区（幂等预置；永不自动删除）")
    ap.add_argument("--reuse-workspace", action="store_true",
                    help="复用 <systest>/e2e_runs/ws_shared（跨轮保留，幂等预置）")
    ap.add_argument("--run-dir", default="", metavar="DIR", help="运行目录（默认 e2e_runs/<run_id>）")
    ap.add_argument("--port", type=int, default=0, help="GUI 测试端口（默认动态分配）")
    ap.add_argument("--no-restart", action="store_true",
                    help="严格单次启动：不跑 restart 阶段")
    return ap.parse_args(argv)


def _split_multi(values):
    out = []
    for v in values or []:
        out.extend(x.strip() for x in str(v).split(",") if x.strip())
    return out


def main(argv=None):
    args = _parse_args(argv)
    if args.coverage:
        from e2e import coverage
        return coverage.main()
    run_id = _run_id()

    only = _split_multi(args.only)
    areas = _split_multi(args.area)
    kws = _split_multi(args.k)

    stages = None
    if args.init == "empty":
        stages = {"empty"}
    elif args.init == "seeded":
        stages = {"seeded", "restart"}
    elif args.init == "destructive":
        stages = {"destructive"}
    if args.list:
        stages = None          # 清单展示全部（含破坏性；行尾标 [破坏性]）
    else:
        if args.no_restart:
            stages = (stages or set(fw.STAGES)) - {"restart"}
        if args.with_destructive:
            stages = (stages or set(fw.STAGES)) | {"destructive"}
        else:
            stages = (stages or set(fw.STAGES)) - {"destructive"}

    points = fw.select(only=only, areas=areas, stages=stages, keywords=kws)
    if args.list:
        fw.print_list(points)
        return 0
    if not points:
        alt = fw.select(only=only, areas=areas, keywords=kws)
        if alt:
            print("（%d 个匹配测试点被 --init %s 的阶段过滤排除：%s）"
                  % (len(alt), args.init, ", ".join(sorted({p.stage for p in alt}))))
            if any(p.stage == "destructive" for p in alt):
                print("  → 破坏性测试点默认不跑；加 --with-destructive，"
                      "或运行 `python run_e2e_destructive.py`。")
        print("（无匹配测试点）可用 --list 查看全部测试点。")
        return 0

    # 运行目录 / 工作区 / 独立 HOME
    run_dir = args.run_dir or os.path.join(E2E_RUNS, run_id)
    explicit_ws = bool(args.workspace)
    if explicit_ws:
        workspace = os.path.abspath(args.workspace)
    elif args.reuse_workspace:
        workspace = SHARED_WS
    else:
        workspace = os.path.join(run_dir, "ws")
    home = os.path.join(run_dir, "home")
    report_dir = os.path.join(E2E_REPORTS, run_id)
    for d in (workspace, home, report_dir):
        os.makedirs(d, exist_ok=True)

    cleanup = "no" if args.keep else args.cleanup
    keep_ws = cleanup == "no" or explicit_ws or args.reuse_workspace

    opts = {
        "run_id": run_id, "run_dir": run_dir, "workspace": workspace, "home": home,
        "report_dir": report_dir, "init": args.init, "max_issues": args.max_issues,
        "cleanup": cleanup, "workspace_kept": keep_ws,
        "port": args.port or 0,
    }
    runner = fw.Runner(points, opts)
    try:
        rc = runner.run()
    except KeyboardInterrupt:
        print("\n[e2e] 收到 Ctrl+C → 停止（报告可能不完整）", flush=True)
        rc = 130
    finally:
        # 用户的最终确认 + 清理决策（在报告与结果打印之后）
        do_clean = False
        if cleanup == "yes":
            do_clean = True
        elif cleanup == "ask":
            if explicit_ws or args.reuse_workspace:
                print("[e2e] 工作区为显式指定/共享目录 → 不自动删除（--workspace/--reuse-workspace）")
            elif sys.stdin and sys.stdin.isatty():
                try:
                    ans = input("\n测试完成：是否清理工作区与用户数据？[y/N] ").strip().lower()
                except EOFError:
                    ans = ""
                do_clean = ans in ("y", "yes")
                print("  → %s" % ("清理" if do_clean else "保留"))
            else:
                print("[e2e] 非交互环境 → 保留工作区（如需清理请用 --cleanup yes）")
        elif cleanup == "auto" and not keep_ws:
            do_clean = True

        ws_deleted = False
        if do_clean:
            removed = fw.cleanup_workspace(workspace, home, explicit_workspace=explicit_ws)
            ws_deleted = any(os.path.abspath(r) == os.path.abspath(workspace) for r in removed)
            if not explicit_ws and os.path.isdir(run_dir):
                shutil.rmtree(run_dir, ignore_errors=True)
            print("[e2e] 清理结果：%s" % (", ".join(removed) if removed else "无可清理内容"),
                  flush=True)
        else:
            print("[e2e] 工作区保留：%s\n       独立 HOME：%s" % (workspace, home), flush=True)
        print("[e2e] 报告已保留：%s" % report_dir, flush=True)
        _annotate_report(report_dir, ws_deleted)

    return rc


def _annotate_report(report_dir, cleaned):
    """把最终清理结果补进报告（report.json / report.md）。"""
    jp = os.path.join(report_dir, "report.json")
    if not os.path.isfile(jp):
        return
    try:
        with open(jp, "r", encoding="utf-8") as f:
            data = json.load(f)
        data["workspace_kept"] = not cleaned
        with open(jp, "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, indent=2)
        mp = os.path.join(report_dir, "report.md")
        with open(mp, "a", encoding="utf-8") as f:
            f.write("\n---\n\n**清理结果**：%s\n" % ("工作区已删除" if cleaned
                                                     else "工作区已保留（独立 HOME 按策略清理）"))
    except Exception as e:
        print("[e2e] 报告补记清理结果失败：%s" % e, flush=True)


if __name__ == "__main__":
    sys.exit(main())
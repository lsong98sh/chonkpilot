"""全量回归运行器：依次执行 systest/gui 下所有 test_*.py + run_*.py，汇总结果。

运行方式 / 隔离（2026-09-28 纳入 `run_*` 套件）：
  * **逐个子进程串行**（`cwd=HERE`）：任一时刻只有一个套件在跑，故各套件**不复用彼此**的
    实例/端口/临时目录 —— 天然无并发争用（这是本运行器唯一的隔离手段：串行 + 各套件自管资源）。
  * `test_*.py`：动态端口（`harness.free_port()`）+ 套件级配置快照-还原。
  * `run_*.py`：
      - 共享底座型（`run_config_ui` / `run_project_cfg` / `run_ui_regressions` / `run_fp_extra_ui` /
        `run_tool_async_config`）
        统一 `_h.acquire_gui(2345, ...)`：**复用优先**，无实例才自起并在退出时回收；串行执行
        → 固定端口 2345 在不同套件间「起-停」交替，不并存（`run_config_ui` 另起独立实例 2347/临时目录，
        并在清理时显式保护 2345，绝不波及底座）。
      - 自起型（`run_hist_git` / `run_index_gate` / `run_filetree_ignored` / `run_docs_gate`）：动态端口 +
        独立 `--data-dir`/临时 work-dir/独立 HOME（`run_hist_git` 另起自管 mock LLM；`run_docs_gate`
        另自拉/自停真转换器，产物缺失时 preflight SKIP）→ 与共享底座完全隔离，退出即回收。

**SKIP ≠ PASS**：运行器按**原始字节**透传每个子进程输出，并据子进程自报的 `(SKIPPED` 标记（`run_*.py`
的 `RESULT: True (SKIPPED: ...)` 约定）把该套件记为 **SKIP**（既不计入 pass，也不判失败）。整轮退出码
= 有 FAIL 才非零；SKIP 不算失败。汇总显式区分 PASS / SKIP / FAIL 三态。
"""

import os
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))

# 第一批：`test_*.py`（动态端口 + 套件级快照-还原；`drive.GUIClient`）。
SCRIPTS = [
    "test_window.py",
    "test_layout.py",
    "test_toolbar.py",
    "test_filetree.py",
    "test_filetree_interact.py",
    "test_filetree_dnd.py",
    "test_filetree_ctxmenu.py",
    "test_chat.py",
    "test_session_bridge.py",
    "test_task.py",
    "test_session_drawer.py",
    "test_statusbar.py",
    "test_config.py",
    "test_turn_nav.py",
    "test_chat_flow.py",
    "test_queue.py",
    "test_filetree_watcher.py",
    "test_richtext.py",
    "test_screenshot.py",
    "test_chat_input_top.py",
    "test_preview.py",
    "test_tool_retry.py",
]

# 第二批：`run_*.py`（端到端/UI 断言套件；隔离与端口分配见模块 docstring）。
RUN_SCRIPTS = [
    "run_config_ui.py",           # 共享底座 2345（复用优先）+ 独立实例 2347
    "run_project_cfg.py",         # 共享底座 2345
    "run_ui_regressions.py",      # 共享底座 2345
    "run_fp_extra_ui.py",         # 共享底座 2345
    "run_tool_async_config.py",   # 共享底座 2345（工具配置页：usr `tool_async` 读/写/效果）
    "run_index_gate.py",          # 自起（动态端口 + 临时 work-dir/data-dir/HOME）
    "run_docs_gate.py",           # 自起（动态端口 + 临时 work-dir/data-dir/HOME；真转换器自拉/自停；产物缺失则 SKIP）
    "run_filetree_ignored.py",    # 自起（动态端口 + 临时 work-dir/data-dir/HOME）
    "run_hist_git.py",            # 自起（动态端口 + 临时目录 + 自管 mock LLM；无 git 则 SKIP）
]


def _run_script(path):
    """运行单个子套件：实时透传输出（原始字节）并探测 SKIP 标记。

    返回 (returncode, skipped)。SKIP 判据 = 子进程自报的 `RESULT: True (SKIPPED: ...)`
    约定（纯 ASCII 标记，按原始字节匹配 → 不受子进程 stdout 编码影响）。透传用原始字节
    保证与「子进程直连终端」（capture_output=False）逐字节一致，不引入父进程编码干扰。
    """
    env = dict(os.environ)
    env["PYTHONUNBUFFERED"] = "1"  # PIPE 下子进程默认块缓冲 → 逐行刷新以保实时性
    r = subprocess.Popen([sys.executable, path], cwd=HERE, env=env,
                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    buf = getattr(sys.stdout, "buffer", None)
    skipped = False
    for raw in r.stdout:
        if buf is not None:
            sys.stdout.flush()
            buf.write(raw)
            buf.flush()
        else:  # 非常规 stdout（无 buffer）→ 退回文本模式
            sys.stdout.write(raw.decode("utf-8", "replace"))
        if b"(SKIPPED" in raw:
            skipped = True
    r.wait()
    return r.returncode, skipped


def main():
    results = []
    for name in SCRIPTS + RUN_SCRIPTS:
        path = os.path.join(HERE, name)
        if not os.path.exists(path):
            print(f"[SKIP] {name} 不存在")
            results.append((name, "skip"))
            continue
        print(f"\n===== {name} =====")
        t0 = time.time()
        rc, skipped = _run_script(path)
        dt = time.time() - t0
        if rc == 0 and skipped:
            state = "skip"
        else:
            state = "pass" if rc == 0 else "fail"
        print(f"----- {name} -> {'PASS' if state == 'pass' else ('SKIP' if state == 'skip' else 'FAIL')} ({dt:.0f}s) -----")
        results.append((name, state))

    print("\n\n===== 汇总 =====")
    label = {"pass": "PASS", "fail": "FAIL", "skip": "SKIP"}
    passed = sum(1 for _, s in results if s == "pass")
    skipped = sum(1 for _, s in results if s == "skip")
    failed = sum(1 for _, s in results if s == "fail")
    for name, s in results:
        print(f"[{label[s]:>4}] {name}")
    print(f"\n== {passed} passed / {skipped} skipped / {failed} failed (共 {len(results)}) ==")
    if skipped:
        print(f"   注：{skipped} 项 SKIP（前置不满足，非失败；SKIP 不计入 passed）")
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())


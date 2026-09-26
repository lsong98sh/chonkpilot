"""全量回归运行器：依次执行 systest/gui 下所有 test_*.py，汇总结果。"""
import os
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))

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


def main():
    results = []
    for name in SCRIPTS:
        path = os.path.join(HERE, name)
        if not os.path.exists(path):
            print(f"[SKIP] {name} 不存在")
            results.append((name, "skip"))
            continue
        print(f"\n===== {name} =====")
        t0 = time.time()
        r = subprocess.run([sys.executable, path], cwd=HERE, capture_output=False)
        dt = time.time() - t0
        ok = r.returncode == 0
        print(f"----- {name} -> {'PASS' if ok else 'FAIL'} ({dt:.0f}s) -----")
        results.append((name, "pass" if ok else "fail"))

    print("\n\n===== 汇总 =====")
    passed = sum(1 for _, s in results if s == "pass")
    for name, s in results:
        print(f"[{'PASS' if s == 'pass' else 'FAIL':>4}] {name}")
    print(f"\n== {passed}/{len(results)} passed ==")
    return 0 if passed == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())

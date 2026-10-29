# -*- coding: utf-8 -*-
"""SESSION-COMP-1 P3-4c / DATA-OPT-1 ②（2026-10-28）· 一树一库**恒生效**（运行期验证）。

覆盖（L4，mq 驱动 / 文件面断言）：
  S1 根会话 ensure → 自举生成 `<prjusr>/dialogs/dialog-<hex>.db` 并**回带 `dialog_id`**；
     `data-session-list` 项带同一 `dialog_id`。
  S2 重启 → 对话文件数不变（自举幂等，不重复建库）。

说明：旧布局（prjusr `sessions` 表 / `sessions/<hex>/chonkpilot.db`）与 `session.db.shard`
开关已整体退场——缺 `dialog_id` 的会话类调用一律 `dialog_required`（根会话自举除外）。

前置：`chonkpilot.exe -Debug` 产物（需 `--test-port`）。
隔离：独立临时 work-dir（prj/prjusr 均在其中）/ HOME。

运行：python run_data_shard.py
"""
import glob
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

WS = _h.tmp_dir("ck-shard-ws-")
HOME = _h.tmp_home()
DD = _h.prjusr_root(WS, HOME)   # prjusr 数据根（固定规则；无 --data-dir 参数）
SID = "shard-legacy-1"

print("[env] ws=%s data=%s home=%s（临时目录，结束即删）" % (WS, DD, HOME), flush=True)


def boot():
    g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
    return g


def dialog_files():
    return sorted(glob.glob(os.path.join(DD, "dialogs", "dialog-*.db")))


def wait_dialogs(timeout=20.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        files = dialog_files()
        if files:
            return files
        time.sleep(0.25)
    return dialog_files()


def case_s1_default_bootstrap():
    """S1：根会话 ensure → 自举生成对话库并回带 dialog_id；list 带 dialog_id。"""
    g = boot()
    try:
        res = g.client.req("data-session-ensure-session", {"session_id": SID})
        dlg = (res or {}).get("dialog_id")
        if not dlg:
            raise TestError(f"根会话自举应回带 dialog_id：{res!r}")
        files = wait_dialogs(10.0)
        if len(files) != 1:
            raise TestError(f"应生成 1 个对话库，实际 {files!r}")
        name = os.path.basename(files[0])
        if not (name.startswith("dialog-") and name.endswith(".db")):
            raise TestError(f"库文件名不符 dialog-<hex>.db：{name}")
        lst = g.client.req("data-session-list", {})
        items = (lst or {}).get("list", [])
        hit = [s for s in items if (s.get("session_id") == SID)]
        if not hit:
            raise TestError(f"list 应列出主会话：{items!r}")
        if hit[0].get("dialog_id") != dlg:
            raise TestError(f"list 项应带同一 dialog_id：{hit[0]!r} vs {dlg}")
    finally:
        g.stop()


def case_s2_restart_idempotent():
    """S2：重启 → 对话文件数不变（自举幂等，不重复建库）。"""
    g = boot()
    try:
        time.sleep(2.0)
        files = dialog_files()
        if len(files) != 1:
            raise TestError(f"重启后仍应 1 个库（自举幂等），实际 {files!r}")
    finally:
        g.stop()


def main():
    import shutil
    total = ok = 0
    try:
        total += 1; ok += run_case("S1 自举生成对话库 + 回带 dialog_id", case_s1_default_bootstrap)
        total += 1; ok += run_case("S2 重启幂等（不重复建库）", case_s2_restart_idempotent)
    finally:
        shutil.rmtree(WS, ignore_errors=True)
        shutil.rmtree(DD, ignore_errors=True)
    print("\n===== 结果：%d/%d 通过 =====" % (ok, total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

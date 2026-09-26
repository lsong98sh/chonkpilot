# -*- coding: utf-8 -*-
"""临时诊断：复现并验证「复用正在退出的实例」导致批内偶发红。

现象（实测）：上一实例 taskkill /F /T 后**端口仍在监听**（进程正在消失）→ `ping_ready()` 仍为 True
→ `acquire_gui` 复用它（owned=False 永不回收）→ 实例在套件刚起步时消失 → P1..P9 全线
`URLError 10061`（批内红、复跑绿）。

验证：
  ① 起实例 → **立即 kill（不等 wait）** → 同一瞬间 `ping_ready`（旧逻辑）vs `stable_ready`（新逻辑）
  ② 同条件立即跑 run_preview_ui.py → 期望：不复用（或复用到稳定实例）+ 9/9 绿
"""
import os
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import harness as _h  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
PORT = 2345


def die_now(g):
    """不发 wait、不登记回收 → 模拟「上一脚本 taskkill 在途」。"""
    _h.kill_tree(g.pid())
    _h.unregister(g)


def main():
    # ── ① 旧逻辑 vs 新逻辑在同一瞬间的判定 ──
    g = _h.start_gui(port=PORT, work_dir=os.path.join(HERE, "ws"), ready_timeout=90)
    print("实例已就绪 pid=%s → 立即 taskkill（不等 wait）" % g.pid(), flush=True)
    die_now(g)
    t0 = time.time()
    old = _h.ping_ready(PORT)
    new = _h.stable_ready(PORT)
    print("① 判定（taskkill 后 %.2fs）：ping_ready(旧)=%s  stable_ready(新)=%s  port_open=%s"
          % (time.time() - t0, old, new, _h.port_open(PORT)), flush=True)

    # ── ② 同条件立即跑 run_preview_ui.py ──
    g2 = _h.start_gui(port=PORT, work_dir=os.path.join(HERE, "ws"), ready_timeout=90)
    print("② 再起实例 pid=%s → 立即 taskkill → 马上跑 run_preview_ui.py" % g2.pid(), flush=True)
    die_now(g2)
    r = subprocess.run([sys.executable, os.path.join(HERE, "run_preview_ui.py")],
                       cwd=HERE, capture_output=True, text=True, encoding="utf-8", errors="replace")
    out = (r.stdout or "") + (r.stderr or "")
    print("② run_preview_ui 退出码 =", r.returncode, flush=True)
    for line in out.splitlines():
        if ("复用" in line or "断言" in line or "[FAIL" in line or "[ERROR" in line
                or "回收" in line or "警告" in line):
            print("   |", line, flush=True)


if __name__ == "__main__":
    main()

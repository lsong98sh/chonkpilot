# -*- coding: utf-8 -*-
"""运行时验证：GUI(inprocess server) + history 插件 git 快照 + 事件链 + **门控默认关**。

门控口径（2026-09-19，[42 §2 (125)]）：`history.enabled` **默认不开启** —— 只有显式 "true"
才在轮次边界提交 git 快照。本套件两段式验证：

  A. **缺省（无该键）**：真实轮次 + 已改动文件 → 断言**不产生任何 git 提交**（`git rev-list` 恒 0）；
  B. **显式开启**（prj `history.enabled="true"` 保存）→ 后续轮次断言提交出现（`chonk: snapshot`）
     且再改动 → 提交数继续增长。

流程：
  1. 准备临时 git 仓库 work-dir（git init + user 配置）
  2. 启动 chonkpilot.exe --work-dir=<repo> --test-port=PORT（mock_llm 8901 前置）
  3. CreateSession → mq 发 llm-start（前端协议：session/turn/llm/q；桥拆 llm-send text-user）
  4. 断言见上；结束前还原 `history.enabled`（避免污染库）

前置：mock_llm.py 监听 127.0.0.1:8901；dist-desktop/chonkpilot.exe 为最新构建。
"""
import json
import os
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）

GUI_EXE = _h.resolve_gui_exe()
PORT = _h.free_port()  # 动态端口：避免与其它套件/机器上的固定端口实例冲突

c = ChonkClient(base=f"http://127.0.0.1:{PORT}")


def git(wd, *args):
    return subprocess.run(["git", "-C", wd] + list(args),
                          capture_output=True, text=True, timeout=20)


def commit_count(wd):
    r = git(wd, "rev-list", "--count", "HEAD")
    if r.returncode != 0:
        return 0
    return int(r.stdout.strip() or 0)


def deep_loads(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def create_session():
    """生成会话 id（mq-only；新架构无 window.go.CreateSession，llm-start 幂等落库）。"""
    sid = "hist-sess-%d" % int(time.time() * 1000)
    c.mq_emit("session-changed", {"session_id": sid})
    time.sleep(0.3)
    return sid


def start_llm_turn(sid, text, turn):
    c.eval("window.mq.emit('llm-start', %s)" % json.dumps({
        "session": sid, "turn": turn, "llm": "mock",
        "think": "", "effort": "", "scenario_id": 0, "q": text,
    }, ensure_ascii=False), 5000)


def wait_turn_start_main(max_wait=15):
    """等主会话（parents 空）turn-start。"""
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in c.events_of("turn-start", clear=True):
            p = e.get("payload") or {}
            if not p.get("parents"):
                return p
        time.sleep(0.3)
    return None


def prj_get(key):
    """读 prj 单键 → 归一为字符串（缺失 → ""；JSON true/false → "true"/"false"）。"""
    r = c.req("data-prj-config-load", {"data": {"id": key}}) or {}
    v = deep_loads(r.get("data"))
    if v is True:
        return "true"
    if v is False:
        return "false"
    if v is None:
        return ""
    return str(v)


def prj_set(key, value):
    return c.req("data-prj-config-save", {"data": {"key": key, "value": value}})


def prj_delete(key):
    return c.req("data-prj-config-delete", {"data": {"id": key}})


def main():
    if shutil.which("git") is None:
        print("[SKIP] git 不可用")
        return 0

    # 临时 work-dir 走 harness 唯一入口（`tmp_dir` 登记 → 退出时带重试回收，见 51 §6）：
    # 子实例 WebView2 profile / bbolt 句柄释放是**异步**的，本地 `rmtree(ignore_errors=True)` 可能静默残留。
    wd = _h.tmp_dir("chonk-hist-")
    ok = total = 0
    saved = None
    saved_known = False
    gui_ready = False
    try:
        r = git(wd, "init", "-q")
        if r.returncode != 0:
            print(f"[FAIL] git init: {r.stderr}")
            return 1
        git(wd, "config", "user.email", "hist@chonkpilot.local")
        git(wd, "config", "user.name", "chonkpilot-hist")

        # 按需加载：本套件必需的 mock LLM(8901) + 自起独立 work-dir 的 GUI（登记回收）
        _M = _h.acquire_mock_llm(_h.DEFAULT_MOCK_LLM_PORT)
        h = _h.start_gui(port=PORT, work_dir=wd, ready_timeout=120)
        try:
            c.wait_ready(120)
            gui_ready = True
            # 记录 history.enabled 原值（结束还原，避免污染 prj 库）
            saved = prj_get("history.enabled")
            saved_known = True
            total = 5
            ok += _verify(wd)
            print(f"\nhistory git 运行时验证：{ok}/{total} 通过")
            return 0 if ok == total else 1
        finally:
            if gui_ready and saved_known:
                try:  # 还原门控键（原本缺省 → 删除；原本有值 → 写回）
                    if saved in (None, ""):
                        prj_delete("history.enabled")
                    else:
                        prj_set("history.enabled", saved)
                except Exception as e:
                    print(f"  [WARN] history.enabled 还原失败: {e}")
            h.stop()  # 结束即关（进程退出/信号路径由 harness 兜底）
    finally:
        shutil.rmtree(wd, ignore_errors=True)


def _verify(wd):
    c.mq_on_capture(["turn-start", "llm-complete", "llm-error"])

    marker = os.path.join(wd, "plan.md")
    with open(marker, "w", encoding="utf-8") as f:
        f.write("v1\n")

    ok = 0
    sid = create_session()
    print(f"    session={sid}")

    # ── A. 缺省（无该键）→ 不产生任何 git 提交 ──
    start_llm_turn(sid, "history git check turn one", "turn-a1")
    comps = c.wait_events("llm-complete", n=1, max_wait=90)
    done = (comps[0].get("payload") or {}) if comps else {}
    if done.get("status") in ("complete", "incomplete", "interrupted", "error"):
        ok += 1
    else:
        print(f"  [FAIL] llm-complete 终态异常: {done}")

    main_start = wait_turn_start_main()
    if main_start and main_start.get("session"):
        ok += 1
        print(f"    turn-start 主会话 session={main_start['session'][:8]}.. turn={main_start.get('turn')}")
    else:
        print("  [FAIL] 未见主会话 turn-start")

    # 反向断言：给足提交窗口（15s）后仍是 0 次提交（默认关闭）
    deadline = time.time() + 15
    n0 = commit_count(wd)
    while n0 == 0 and time.time() < deadline:
        time.sleep(0.5)
        n0 = commit_count(wd)
    if n0 == 0:
        ok += 1
        print("    缺省（无 history.enabled）→ git 提交数=0（默认关闭）")
    else:
        print(f"  [FAIL] 缺省不应提交，实际提交数={n0}；log={git(wd, 'log', '--oneline').stdout!r}")

    # ── B. 显式开启 → 轮次边界提交，且改动后提交增长 ──
    prj_set("history.enabled", "true")
    time.sleep(0.8)
    v = prj_get("history.enabled")
    if v == "true":
        print("    prj history.enabled=true（显式开启）")
    else:
        print(f"  [FAIL] 开启回读异常: history.enabled={v!r}")

    with open(marker, "w", encoding="utf-8") as f:
        f.write("v2\n")
    start_llm_turn(sid, "history git check turn two", "turn-b1")
    c.wait_events("llm-complete", n=1, max_wait=90)
    deadline = time.time() + 15
    while commit_count(wd) < 1 and time.time() < deadline:
        time.sleep(0.5)
    n1 = commit_count(wd)
    msg1 = git(wd, "log", "--oneline", "-1").stdout if n1 >= 1 else ""
    if n1 >= 1 and "chonk: snapshot" in msg1:
        ok += 1
    else:
        print(f"  [FAIL] 显式开启后无 chonk snapshot 提交: count={n1} log={msg1!r}")
    print(f"    git 提交数={n1} 最近提交: {msg1.strip()[:120]}")

    with open(marker, "w", encoding="utf-8") as f:
        f.write("v3\n")
    start_llm_turn(sid, "history git check turn three", "turn-b2")
    c.wait_events("llm-complete", n=1, max_wait=90)
    deadline = time.time() + 15
    while commit_count(wd) <= n1 and time.time() < deadline:
        time.sleep(0.5)
    n2 = commit_count(wd)
    if n2 > n1:
        ok += 1
    else:
        print(f"  [FAIL] 再改动后提交未增长: {n1} -> {n2}")
    print(f"    git 提交数={n2}")
    return ok


if __name__ == "__main__":
    sys.exit(main())

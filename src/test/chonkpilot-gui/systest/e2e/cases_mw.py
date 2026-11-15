# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑦：**多窗口**（同进程多对话窗口，移植自 run_mw.py 的 MW 矩阵核心项）。

窗口级操作一律经 `ChonkClient(window_id=...)` 定向路由；主窗口与对话窗口共享同一进程/同一数据根，
故仍满足「一次启动」。每例开始/结束都清理对话窗口，避免残留互相干扰。
"""

import json
import time

import harness as _h
from chonk_client import ChonkClient

from .framework import case


def _wins(env):
    return ((env.req("gui.window.list", {}) or {}).get("windows")) or []


def _wclient(env, wid):
    return ChonkClient(base=env.client.base, window_id=wid, timeout=30)


def _wait_win(env, wid, timeout=40):
    c = _wclient(env, wid)
    end = time.time() + timeout
    while time.time() < end:
        try:
            if c.eval("!!(document.querySelector('.chat-panel')||document.querySelector('.panel-inner'))"):
                return c
        except Exception:
            pass
        time.sleep(0.4)
    raise AssertionError("对话窗口 %s 未就绪（%ss）" % (wid, timeout))


def _open(env, sid):
    r = env.req("gui.window.open-chat", {"session_id": sid}) or {}
    if not r.get("ok") or not r.get("window_id"):
        raise AssertionError("open-chat(%s) 失败：%r" % (sid, r))
    return r["window_id"], _wait_win(env, r["window_id"])


def _close(env, wid):
    try:
        _wclient(env, wid).publish("gui.window.status", {"command": "close"})
    except Exception:
        pass


def _close_all(env):
    for w in _wins(env):
        _close(env, w.get("window_id"))
    end = time.time() + 20
    while time.time() < end and _wins(env):
        time.sleep(0.3)


@case("S-MW-01", "多窗口", "多窗口开关：open-chat ×2 → list 2 → 关一窗不影响另一窗",
      stage="seeded", order=240, fp="24 多窗口模型 MW-T01/MW-T02 · 61 §1 gui.window.*",
      desc="两个会话各开独立对话窗口：window_id 不同、list 2 项、各自 status 有应答；"
           "关闭 w1 收到 gui.window.closed，list 剩 w2 且 w2 仍可应答。")
def s_mw_01_open_close(env):
    _close_all(env)
    s1, s2 = "e2e-mw-a-%s" % env.run_id, "e2e-mw-b-%s" % env.run_id
    for s in (s1, s2):
        env.req("data-session-ensure-session", {"data": {"session_id": s}})
    w1, c1 = _open(env, s1)
    w2, c2 = _open(env, s2)
    env.check("两个 window_id 不同", w1 != w2, (w1, w2))
    lst = env.wait(lambda: _wins(env) if len(_wins(env)) == 2 else None,
                   "gui.window.list == 2", timeout=20)
    env.eq("窗口会话集合", {w.get("session_id") for w in lst}, {s1, s2})
    env.check("w1 status 有应答", "maximized" in (c1.req("gui.window.status", {}) or {}))
    env.check("w2 status 有应答", "maximized" in (c2.req("gui.window.status", {}) or {}))

    env.capture(["gui.window.closed"])
    _close(env, w1)
    ev = env.wait_events("gui.window.closed", lambda p: p.get("window_id") == w1,
                         "gui.window.closed(w1)", timeout=20)
    env.check("关闭事件携带 window_id", (ev or {}).get("window_id") == w1, ev)
    rest = env.wait(lambda: _wins(env) if len(_wins(env)) == 1 else None,
                    "关闭后 list 剩 1", timeout=20)
    env.eq("剩余窗口 = w2", rest[0].get("window_id"), w2)
    env.check("w2 关闭 w1 后仍应答", "maximized" in (c2.req("gui.window.status", {}) or {}))
    _close_all(env)


@case("S-MW-02", "多窗口", "窗口定向控制隔离：w2 最大化不影响主窗",
      stage="seeded", order=250, fp="24 MW-T04 · 61 §1 gui.window.status（按来源窗口）",
      desc="从对话窗口 w2 发 maximize：仅 w2 maximized=true；主窗口仍 false；还原 w2 后主窗不受影响。")
def s_mw_02_directed_isolation(env):
    _close_all(env)
    sid = "e2e-mw-dir-%s" % env.run_id
    env.req("data-session-ensure-session", {"data": {"session_id": sid}})
    wid, wc = _open(env, sid)
    env.check("主窗初始非最大化", env.req("gui.window.status", {}).get("maximized") is False)

    wc.req("gui.window.status", {"command": "maximize"})
    env.wait(lambda: wc.req("gui.window.status", {}).get("maximized") is True,
             "w2 maximized=true", timeout=15)
    env.check("主窗仍非最大化（定向隔离）",
              env.req("gui.window.status", {}).get("maximized") is False)
    wc.req("gui.window.status", {"command": "maximize"})
    env.wait(lambda: wc.req("gui.window.status", {}).get("maximized") is False,
             "w2 已还原", timeout=15)
    _close_all(env)


@case("S-MW-03", "多窗口", "前端本地事件不跨窗：主窗本地事件不入对话窗",
      stage="seeded", order=260, fp="24 MW-T05 · 61 §6.1 前端本地事件",
      desc="主窗与对话窗各注册 `mq.on('session-changed')` 计数 → 主窗 emit 本地事件 → "
           "主窗计数 +1、对话窗计数保持 0；两窗会话归属不变（w2 仍为 s2）。")
def s_mw_03_frontend_isolation(env):
    _close_all(env)
    s1, s2 = "e2e-mw-fe1-%s" % env.run_id, "e2e-mw-fe2-%s" % env.run_id
    for s in (s1, s2):
        env.req("data-session-ensure-session", {"data": {"session_id": s}})
    wid, wc = _open(env, s2)
    spy = ("(()=>{window.__loc=0;"
           "window.mq.on('session-changed',()=>{window.__loc=(window.__loc||0)+1});"
           "return 'ok'})()")
    env.js(spy)
    wc.eval(spy)

    env.emit("session-changed", {"session_id": s1})
    env.wait(lambda: True if int(_h._plain(env.js("window.__loc||0")) or 0) >= 1 else None,
             "主窗收到本地事件", timeout=15)
    time.sleep(0.6)
    env.eq("主窗本地事件计数", int(_h._plain(env.js("window.__loc||0")) or 0), 1)
    env.eq("对话窗未收到主窗本地事件",
           int(_h._plain(wc.eval("window.__loc||0")) or 0), 0)
    row = next((w for w in _wins(env) if w.get("window_id") == wid), {})
    env.eq("对话窗会话归属不变", row.get("session_id"), s2)
    _close_all(env)


@case("S-MW-04", "多窗口", "重复打开同一会话 = 激活已有窗口（不新建）",
      stage="seeded", order=270, fp="24 MW-T07 I-1 ② · 61 §1 gui.window.open-chat",
      desc="对同一 session 连续 open-chat 两次：第二次 activated=true、window_id 相同、list 仍 1 项。")
def s_mw_04_duplicate_activates(env):
    _close_all(env)
    sid = "e2e-mw-dup-%s" % env.run_id
    env.req("data-session-ensure-session", {"data": {"session_id": sid}})
    w1, _c = _open(env, sid)
    r2 = env.req("gui.window.open-chat", {"session_id": sid}) or {}
    env.check("第二次 open-chat 为激活", r2.get("activated") is True, r2)
    env.eq("window_id 不变", r2.get("window_id"), w1)
    env.wait(lambda: len(_wins(env)) == 1, "list 仍 1 项", timeout=15)
    env.note("重复打开语义", r2)
    _close_all(env)
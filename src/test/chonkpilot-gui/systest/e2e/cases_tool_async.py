# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㊷：**工具异步超时 → 用户裁决（等待完成 / 转后台 → 后台取消）**
（迁移自 run_tool_async B/C/E + 裁决行 DOM）。

该测试点**需要独立实例**：第三方 stdio MCP（`slow3p`）在 GUI 启动期装配（保存后需重启），
故在点内自起隔离实例，预写用户级 `capability/mcps/slow3p.json`（调用级 timeout=1s / 条目 timeout=5s → 快速到裁决点）。

* never：到超时点 → `mcp-tools-timeout{reason:timeout, options:[wait,cancel], tool_call_id/task_id/
  instance_id, timeout_s}`；
* 等待完成 `mcp-tools-wait` → `{waiting:true,task_id}` → 原调用交付 `tool-result`；
* manual：超时 → `options:[detach,cancel]`，裁决行 DOM `.arbitration-hint` + `.background-btn` +
  `.status-badge.async`；`task-background{tool_call_id}` → 层行 `state=detached`；
  `task-stop{task_id}` → `cancelled` + `tasks.done{state:cancelled,success:false}`。
"""

import json
import os
import sys
import time

import harness as _h
from .framework import case

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FIXTURE = os.path.join(HERE, "mock_slow_mcp.py")
EVENTS = ["turn-start", "llm-complete", "mcp-tools-timeout", "tool-result",
          "tasks.done", "tool-pair"]
TIMEOUT_S = 1


def _start_call(c, tag):
    sid = "e2e-async-%s-%s" % (tag, int(time.time() * 1000) % 100000)
    c.req("data-session-ensure-session", {"data": {"session_id": sid}})
    try:
        c.req("data-session-active-set", {"session_id": sid})
    except Exception:
        pass
    c.mq_emit("session-changed", {"session_id": sid})
    # 等聊天区真正切到该会话（批跑负载下异步回填更慢；未切换时工具行不会渲染）
    end = time.time() + 60
    while time.time() < end:
        tag = str(_h._plain(c.eval(
            "(()=>{const t=document.querySelector('.chat-session-tag');"
            "return t?(t.getAttribute('title')||t.textContent||''):''})()")) or "")
        if sid in tag:
            break
        time.sleep(0.3)
    c.mq_on_capture(EVENTS)
    c.mq_emit("llm-start", {"session_id": sid, "turn": "t-" + sid, "q": "",
                            "llm": "", "think": "", "effort": "", "scenario_id": ""})
    return sid


def _ask(c, sid, q):
    c.mq_emit("llm-start", {"session_id": sid, "turn": "t2-" + sid, "q": q,
                            "llm": "", "think": "", "effort": "", "scenario_id": ""})


def _wait_ev(c, topic, pred=None, timeout=60):
    end = time.time() + timeout
    while time.time() < end:
        for e in c.events_of(topic, clear=False):
            p = e.get("payload") or {}
            if pred is None or pred(p):
                return p
        time.sleep(0.4)
    raise AssertionError("等待事件超时：%s" % topic)


def _save_async(c, cfg):
    c.req("data-user-config-save", {"data": {"tool_async": json.dumps(cfg)}})
    c.mq_emit("config-refresh")
    time.sleep(0.8)


def _meta(c, name):
    for t in ((c.req("tools-list", {}) or {}).get("tools") or []):
        if isinstance(t, dict) and t.get("name") == name:
            return t.get("_meta") or {}
    return {}


def _layer(c, sid, tcid, want=None, timeout=25):
    end = time.time() + timeout
    row = None
    while time.time() < end:
        try:
            rows = (c.req("data-tasktree-tasks", {"top_session": sid}) or {}).get("list") or []
        except Exception:
            rows = []
        row = next((r for r in rows if r.get("tool_call_id") == tcid), None)
        if row is not None and (want is None or row.get("state") == want):
            return row
        time.sleep(0.5)
    return row


@case("S-TOOL-ASYNC-01", "工具异步", "超时裁决：never 事件契约 / 等待完成 / manual 转后台+取消 / 裁决行 DOM",
      stage="seeded", order=391, fp="18 工具异步超时与取消 · run_tool_async B/C/E",
      desc="隔离实例预注册 slow3p（调用级 timeout=1s / 条目 timeout=5s）：never 超时 → mcp-tools-timeout 字段与 "
           "wait+cancel；mcp-tools-wait → waiting:true 后收到 tool-result；manual → options="
           "[detach,cancel] 且裁决行 DOM 有 background-btn/async 徽标；task-background → 层行 "
           "detached；task-stop(task_id) → cancelled + tasks.done。")
def s_tool_async_01(env):
    home = _h.tmp_home()
    mcps = os.path.join(home, ".chonkpilot", "capability", "mcps")
    os.makedirs(mcps, exist_ok=True)
    with open(os.path.join(mcps, "slow3p.json"), "w", encoding="utf-8") as f:
        json.dump({"name": "slow3p", "level": "user", "transport": "stdio",
                   "runtime": sys.executable,
                   "args": [FIXTURE, "--sleep", "30"], "enabled": True,
                   "timeout": TIMEOUT_S}, f)
    h = None
    try:
        h = _h.start_gui(port=_h.free_port(), work_dir=_h.tmp_dir("ck-e2e-async-ws-"),
                         home=home, ensure_spec=True,
                         extra_args=("-llm-base=http://127.0.0.1:%d/v1" % env.mock_port,
                                     "-llm-model=mock-model"))
        c = h.client
        c.wait_ready(120)
        c.req("data-user-config-save", {"data": {
            "llms": [{"name": "mock", "protocol": "openai", "model": "mock-model",
                      "baseUrl": "http://127.0.0.1:%d/v1" % env.mock_port,
                      "apiKey": "sk-test", "temperature": 0.7, "maxOutputToken": 1024}],
            "defaultLLM": 0}})
        c.mq_emit("config-refresh")
        end = time.time() + 90
        names = set()
        while time.time() < end:
            names = {t.get("name") or "" for t in ((c.req("tools-list", {}) or {}).get("tools") or [])
                     if isinstance(t, dict)}
            if "slow3p_slow_sleep" in names and "slow3p_fast_echo" in names:
                break
            time.sleep(1.0)
        env.check("slow3p 第三方工具装配就绪", "slow3p_slow_sleep" in names,
                  sorted(n for n in names if "slow3p" in n))
        if "slow3p_slow_sleep" not in names:
            raise AssertionError("slow3p 未装配 → 超时裁决不可测")

        # ── B：never 超时事件契约，然后取消收尾 ──
        _save_async(c, {"slow3p_slow_sleep": {"mode": "never"}})
        env.note("slow3p_slow_sleep _meta（never）", _meta(c, "slow3p_slow_sleep"))
        sid = _start_call(c, "never")
        _ask(c, sid, "please call slow3p-never")
        p = _wait_ev(c, "mcp-tools-timeout", lambda x: x.get("reason") == "timeout", 60)
        env.check("B 超时事件字段（tool/options/ids/timeout_s）",
                  str(p.get("tool") or "").replace("self_", "") == "slow3p_slow_sleep"
                  and list(p.get("options") or []) == ["wait", "cancel"]
                  and all(p.get(k) for k in ("tool_call_id", "task_id", "instance_id"))
                  and abs(float(p.get("timeout_s") or 0) - TIMEOUT_S) <= 1.5, p)
        stop = c.req("task-stop", {"tool_call_id": p["tool_call_id"]}) or {}
        env.check("B task-stop(tool_call_id) → cancelled + task_id",
                  stop.get("cancelled") is True and bool(stop.get("task_id")), stop)

        # ── C：等待完成 → 继续交付 ──
        sid2 = _start_call(c, "wait")
        _ask(c, sid2, "please call slow3p-never")
        p2 = _wait_ev(c, "mcp-tools-timeout", lambda x: x.get("reason") == "timeout", 60)
        w = c.req("mcp-tools-wait", {"tool_call_id": p2["tool_call_id"],
                                     "task_id": p2["task_id"],
                                     "instance_id": p2["instance_id"]}) or {}
        env.check("C mcp-tools-wait 回执 {waiting:true,task_id}",
                  w.get("waiting") is True and bool(w.get("task_id")), w)
        tr = _wait_ev(c, "tool-result",
                      lambda x: str(x.get("tool") or "").replace("self_", "") == "slow3p_slow_sleep",
                      45)
        env.check("C 等待完成 → 原调用交付 tool-result", bool(tr), str(tr)[:120])

        # ── E：manual 超时 → 转后台 → 后台取消 ──
        _save_async(c, {"slow3p_slow_sleep": {"mode": "manual"}})
        sid3 = _start_call(c, "manual")
        _ask(c, sid3, "please call slow3p-manual")
        p3 = _wait_ev(c, "mcp-tools-timeout", lambda x: x.get("reason") == "timeout", 60)
        env.check("E manual 超时 options=[detach,cancel]",
                  list(p3.get("options") or []) == ["detach", "cancel"], p3.get("options"))
        # 等聊天流渲染出工具行（裁决条挂在工具行上）；批跑负载下首屏/会话切换更慢
        c.mq_emit("session-changed", {"session_id": sid3})
        end = time.time() + 60
        while time.time() < end:
            if int(_h._plain(c.eval("document.querySelectorAll('.toolpair-section').length")) or 0) > 0:
                break
            time.sleep(0.3)
        dom = None
        end = time.time() + 45
        while time.time() < end:
            dom = _h._plain(c.eval("""(()=>{const rows=[...document.querySelectorAll(
              '.toolpair-section')].filter(e=>e.getBoundingClientRect().width>0);
              const r=rows.find(e=>e.querySelector('.arbitration-hint'));
              if(!r)return {found:false};const b=r.querySelector('.background-btn');
              const g=r.querySelector('.status-badge.async');
              return {found:true,btn:!!b,badge:g?g.textContent.trim():''};})()""")) or {}
            if dom.get("found"):
                break
            time.sleep(0.3)
        if not dom.get("found"):
            env.note("E 裁决行 DOM 诊断", {
                "rows": int(_h._plain(c.eval(
                    "document.querySelectorAll('.toolpair-section').length")) or 0),
                "msgs": int(_h._plain(c.eval(
                    "document.querySelectorAll('.message-item').length")) or 0),
                "tag": str(_h._plain(c.eval(
                    "(()=>{const t=document.querySelector('.chat-session-tag');"
                    "return t?(t.getAttribute('title')||t.textContent||''):''})()")) or "")})
        env.check("E 裁决行 DOM：arbitration-hint + background-btn + async 徽标",
                  dom.get("found") is True and dom.get("btn") is True
                  and bool(dom.get("badge")), dom)

        bg = c.req("task-background", {"tool_call_id": p3["tool_call_id"]}) or {}
        env.check("E task-background 回执 task_id", bool(bg.get("task_id")), bg)
        row = _layer(c, sid3, p3["tool_call_id"], want="detached", timeout=25)
        env.check("E 转后台后层权威行 state=detached", (row or {}).get("state") == "detached",
                  (row or {}).get("state"))
        # 层权威节点 id（tk-*）才可被统一停止入口反查；task-background 回执给的是 gateway 执行 id
        stop2 = c.req("task-stop", {"task_id": (row or {}).get("task_id")
                                    or bg.get("task_id")}) or {}
        env.check("E 后台运行中 task-stop → cancelled",
                  stop2.get("cancelled") is True and bool(stop2.get("task_id")), stop2)
        row2 = _layer(c, sid3, p3["tool_call_id"], want="cancelled", timeout=25)
        env.check("E 后台取消后层权威行 state=cancelled",
                  (row2 or {}).get("state") == "cancelled", (row2 or {}).get("state"))
        done = _wait_ev(c, "tasks.done",
                        lambda x: x.get("tool_call_id") == p3["tool_call_id"], 30)
        env.check("E tasks.done{state:cancelled, success:false}",
                  done.get("state") == "cancelled" and done.get("success") is False, done)
    finally:
        if h is not None:
            try:
                h.stop()
            except Exception:
                pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("工具异步组（主实例）无前端错误", len(errs) == 0, errs[:3])
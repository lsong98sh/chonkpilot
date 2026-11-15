# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉕：**task_run 同步 / 异步工具链**（迁移自 run_tools 前两条）。

mock LLM 剧本：
* `please call py`       → 同步 task_run（stdout `py-ok`）；
* `please call py-async` → 异步 task_run（`tool-pair{status:async}` → 后台完成 → `tool-result` 含 `py-slow`）。

断言同时覆盖：`llm-tool-call` 暴露名（无 self_ 前缀归一）、`tool-result` 回填、任务节点
（`task-started{task_id:tk-*}`）注册、最终回复纳入工具输出。ask_user 多选由 `S-ASK-03` 覆盖。
"""

import json
import os
import time

from .framework import case
from .cases_chat_ops import _open_session, _select, _send

EVENTS = ["llm-tool-call", "tool-result", "tool-pair", "task-started", "llm-complete"]


def _canon(tool):
    t = tool or ""
    return t[5:] if t.startswith("_self") or t.startswith("self_") else t


def _payloads(env, topic):
    return [(e.get("payload") or {}) for e in env.events(topic)]


def _wait_payload(env, topic, pred, desc, timeout=90):
    return env.wait(lambda: next((p for p in _payloads(env, topic) if pred(p)), None),
                    desc, timeout=timeout)


@case("S-TOOLS-RUN-01", "LLM 工具", "task_run 同步/异步：工具调用 → 结果回填 → 任务节点 → 最终回复",
      stage="seeded", order=440, fp="61 §4 llm-tool-call/tool-result · run_tools",
      desc="mock「please call py」→ llm-tool-call task_run、tool-result 含 py-ok、task-started"
           "（tk-*）注册、最终回复含 py-ok；「please call py-async」→ tool-pair async、"
           "tool-result 含 py-slow、任务节点与最终回复纳入异步输出。")
def s_tools_run_01(env):
    _select(env, "mock")
    sid = "e2e-tools-%s" % env.run_id
    _open_session(env, sid)

    # 同步
    env.capture(EVENTS)
    _send(env, sid, "please call py", llm="mock")
    call = _wait_payload(env, "llm-tool-call",
                         lambda p: str(_canon(p.get("tool"))).endswith("task_run"),
                         "同步 llm-tool-call task_run")
    env.check("同步工具调用 llm-tool-call{task_run}", bool(call),
              _canon(call.get("tool")))
    res = _wait_payload(env, "tool-result",
                        lambda p: str(_canon(p.get("tool"))).endswith("task_run"), "同步 tool-result")
    env.check("同步 tool-result 含 stdout py-ok",
              "py-ok" in json.dumps(res, ensure_ascii=False),
              json.dumps(res, ensure_ascii=False)[:200])
    node = _wait_payload(env, "task-started",
                         lambda p: str(p.get("task_id") or "").startswith("tk-")
                         and str(_canon(p.get("tool"))).endswith("task_run"), "同步任务节点注册")
    env.check("task_run 注册任务节点（tk-*）", bool(node), node.get("task_id"))
    env.wait(lambda: True if "py-ok" in str(env.js(
        "document.body.innerText") or "") else None, "最终回复含 py-ok", timeout=30)
    env.check("最终回复含同步 stdout", "py-ok" in str(env.js(
        "document.body.innerText") or ""))
    env.wait_events("llm-complete", lambda p: (p or {}).get("status") in ("complete", "completed"),
                    "同步回合完成", timeout=60)

    # 异步
    env.capture(EVENTS)
    _send(env, sid, "please call py-async", llm="mock")
    _wait_payload(env, "llm-tool-call", lambda p: str(_canon(p.get("tool"))).endswith("task_run"),
                  "异步 llm-tool-call task_run")
    pair = _wait_payload(env, "tool-pair",
                         lambda p: (p.get("status") == "async"), "tool-pair async", timeout=60)
    env.check("异步工具转后台（tool-pair async）", bool(pair), pair.get("status"))
    res2 = _wait_payload(env, "tool-result",
                         lambda p: str(_canon(p.get("tool"))).endswith("task_run")
                         and "py-slow" in json.dumps(p, ensure_ascii=False),
                         "异步 tool-result 含 py-slow", timeout=120)
    env.check("异步 tool-result 含 stdout py-slow", bool(res2))
    _wait_payload(env, "task-started", lambda p: str(p.get("task_id") or "").startswith("tk-"),
                  "异步任务节点注册", timeout=60)
    env.wait(lambda: True if "py-slow" in str(env.js(
        "document.body.innerText") or "") else None, "最终回复含 py-slow", timeout=40)
    env.check("最终回复纳入异步输出", "py-slow" in str(env.js(
        "document.body.innerText") or ""))
    env.wait_events("llm-complete", lambda p: (p or {}).get("status") in ("complete", "completed"),
                    "异步回合完成", timeout=90)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and all(k not in (e.get("text") or "")
                    for k in ("method not implemented", "SetActiveSessionID"))]
    env.check("task_run 组无前端错误", len(errs) == 0, errs[:3])

def _gw(env, name, args):
    res = env.req("chonk.mcp-tools-call", {
        "name": name, "arguments": dict(args or {}),
        "tool_call_display_name": "e2e-meta", "work_dir": env.workspace}) or {}
    parts = [b.get("text") or "" for b in (res.get("content") or [])
             if isinstance(b, dict) and b.get("type") == "text"]
    return "\n".join(parts), res


@case("S-META-TOOLS-01", "LLM 工具", "gateway 元工具进程外：工具面可见 / find 命中 / load schema / invoke 真实执行",
      stage="seeded", order=444, fp="26 MCP gateway · BRIDGE-2 T7（run_meta_tools）",
      desc="tools-list 含 mcp_find/mcp_load/mcp_invoke 且归属外部 MCP 节点；mcp_find(query=task) 命中"
           "task_run；mcp_load(task_run,kind=tool) 返回 input_schema；mcp_invoke(task_run) 受理并以"
           "落盘哨兵证明真实执行。")
def s_meta_tools_01(env):
    want = ("mcp_find", "mcp_load", "mcp_invoke")
    rows = env.wait(lambda: (lambda ts: ts if all(
        w in {t.get("name") for t in ts} for w in want) else None)(
        ((env.req("tools-list", {}) or {}).get("tools") or [])),
        "三个元工具进入工具面", timeout=90)
    env.check("M1 元工具在工具面", all(w in {t.get("name") for t in rows} for w in want),
              len(rows))
    meta = {t.get("name"): ((t.get("_meta") or {}).get("server") or {}) for t in rows
            if t.get("name") in want}
    env.check("元工具归属外部 MCP 节点",
              all(bool(m.get("alias") or m.get("node")) for m in meta.values()), meta)

    txt, _ = _gw(env, "mcp_find", {"type": "tool", "query": "task"})
    env.check("M2 mcp_find 命中 task_run", "task_run" in txt, txt[:160])
    load, _ = _gw(env, "mcp_load", {"name": "task_run", "kind": "tool"})
    env.check("M3 mcp_load 返回 input_schema",
              "task_run" in load and ("input_schema" in load or "inputSchema" in load),
              load[:200])
    okfile = env.path("meta_invoke_ok.txt")
    try:
        os.remove(okfile)
    except OSError:
        pass
    out, _ = _gw(env, "mcp_invoke", {"name": "task_run", "arguments": {
        "command": "Set-Content -LiteralPath '%s' -Value 'META-INVOKE-OK'"
                   % okfile.replace("\\", "/")}})
    try:
        obj = json.loads(out)
    except Exception:
        obj = {}
    inner = obj.get("result") if isinstance(obj, dict) else None
    env.note("mcp_invoke 应答", json.dumps(out[:800], ensure_ascii=False))
    accepted = (isinstance(inner, dict) and inner.get("accepted") is True)         or obj.get("status") == "complete"
    env.check("M4 mcp_invoke 受理并执行目标工具",
              obj.get("ok") is True and obj.get("tool") == "task_run" and accepted,
              json.dumps(obj, ensure_ascii=False)[:300])
    content = ""
    for _ in range(60):
        if os.path.exists(okfile):
            try:
                content = open(okfile, encoding="utf-8").read()
            except Exception:
                content = ""
            if "META-INVOKE-OK" in content:
                break
        time.sleep(0.5)
    env.check("M4 目标工具真实执行（哨兵落盘）", "META-INVOKE-OK" in content, content[:80])
    try:
        os.remove(okfile)
    except OSError:
        pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("元工具组无前端错误", len(errs) == 0, errs[:3])


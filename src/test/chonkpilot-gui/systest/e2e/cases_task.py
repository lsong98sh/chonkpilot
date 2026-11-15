# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ③：任务树 / 工具任务节点（真实 task_run 执行）。

链路：llm-start → mock LLM 决策 task_run（PowerShell）→ gateway 执行 → 任务层落库 →
前端 tasks.started/updated/done + 数据面 data-tasktree-* 回读。
"""

import json

import harness as _h

from .framework import case

TASK_LABEL = "E2E 任务"
TASK_STDOUT = "E2E-TASK-OK"


def _canon(tool):
    t = tool or ""
    return t[5:] if t.startswith("self_") else t


def _payloads(env, topic):
    return [e.get("payload") for e in env.events(topic) if isinstance(e.get("payload"), dict)]


@case("S-TASK-01", "任务树", "工具任务节点：执行 → 任务层落库 → 终态回读 → 结果回填",
      stage="seeded", order=90, fp="61 §3.4 data-tasktree-* · §4.3 tasks.*",
      desc="task_run 真实执行（stdout 哨兵）→ tasks.started 归属主会话、tasks.done=done、"
           "data-tasktree-list kind=tool/title=展示名/tool_call_id 非空、"
           "data-tasktree-tasks 同 id 终态 done、tool-result 含 stdout。")
def s_task_01_tool_task_node(env):
    _h.wait_plugins_running(env.client, ("task",), max_wait=180)
    _h.wait_tools(env.client, ("task_run",), max_wait=120)

    sid = "e2e-task-%s" % env.run_id
    env.run_turn("【T-E2E-TASK】e2e-task", session_id=sid, timeout=240)

    starts = [p for p in _payloads(env, "tasks.started")
              if _canon(p.get("tool")) == "task_run" and not p.get("parent_id")
              and p.get("top_session") == sid]
    env.check("tasks.started 建立工具任务节点", len(starts) >= 1,
              [p.get("tool") for p in _payloads(env, "tasks.started")])
    root = starts[0]
    rid = root.get("task_id") or ""
    env.check("任务节点带 task_id", bool(rid), root)
    env.eq("任务节点归属主会话", root.get("top_session"), sid)

    done = env.wait(lambda: next(
        (p for p in _payloads(env, "tasks.done")
         if p.get("task_id") == rid and p.get("state") == "done"), None),
        "tasks.done(state=done)", timeout=120)
    env.eq("终态 state", done.get("state"), "done")

    # 层权威表回读（总线异步 → 有界等待，判据不放宽）
    def nodes():
        return (env.req("data-tasktree-list", {"top_session": sid}) or {}).get("nodes") or []

    node = env.wait(lambda: next(
        (n for n in nodes() if n.get("node_id") == rid or n.get("task_id") == rid), None),
        "层权威表出现任务节点", timeout=40)
    env.eq("节点 kind", node.get("kind"), "tool")
    env.eq("节点 node_type", node.get("node_type"), "tool")
    env.eq("节点归属会话", node.get("session_id"), sid)
    env.check("节点无 parent_node_id", not node.get("parent_node_id"), node.get("parent_node_id"))
    env.eq("节点 title == 展示名", node.get("title"), TASK_LABEL)
    env.check("节点带 tool_call_id", bool(str(node.get("tool_call_id") or "")),
              node.get("tool_call_id"))
    env.check("工具节点不带 DSL steps[]", not node.get("steps"), node.get("steps"))

    rows = (env.req("data-tasktree-tasks", {"session_id": sid, "top_session": sid}) or {})
    row = next((r for r in (rows.get("list") or [])
                if r.get("task_id") == rid or r.get("id") == rid), None)
    env.check("data-tasktree-tasks 含同 id 行", row is not None, rows.get("list"))
    env.check("tasks 视图终态 done",
              (row or {}).get("status") == "done" or (row or {}).get("state") == "done", row)

    results = [p for p in _payloads(env, "tool-result")
               if _canon(p.get("tool")) == "task_run"]
    env.check("tool-result(task_run) 到达", len(results) >= 1, results[:1])
    env.eq("tool-result status", (results[-1] or {}).get("status"), "done")
    blob = json.dumps(results[-1], ensure_ascii=False)
    env.check("工具结果含真实 stdout 哨兵", TASK_STDOUT in blob, blob[:200])
    env.note("任务证据", "task_id=%s title=%s" % (rid, node.get("title")))
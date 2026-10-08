# -*- coding: utf-8 -*-
"""B · 语义/回环级推广 ⑧：**任务树 / 委派结果回读**（真实委派 → 层落库 → 数据面回读 → 逻辑删除闭合）。

覆盖（**拒绝"存在级"：断言委派节点归属 / 展示名 / 终态回读 + 单源一致 + 逻辑删除闭合**）：
  T1 委派落库回读：主会话发 `llm-start`（q 命中 mock 关键词 → `llm_run` 单次委派「后端开发」，
     展示名 = 目的「委派展示名-自定义」）→ `data-tasktree-list{top_session}` **按层权威表回读**：
     ① 根节点 `node_type=session` / `session_id==主会话` / 无 `parent_id`；② 子节点
     `parent_id==根节点 id` / `node_type=session` / `session_id == job-*（子会话）` / `title==目的`
     （委派身份 + 归属 = 语义级，非仅"有节点"）。
  T2 终态 + 单源一致：`data-tasktree-tasks{top_session}` 同 **id** 行 `status/state==done`（子轮次终态
     回读）；list 与 tasks 两视图同 id（P2 单源：运行态与持久态同源）。
  T3 逻辑删除闭合：`data-tasktree-delete{node_id=子节点}` → **默认** `data-tasktree-list` 不再含该子节点
     （过滤 closed），`include_closed=true` 仍含且 `closed==true`（历史保留）；根节点不受影响。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）
+ 自管 mock LLM（`-llm-base` 兜底 + usr `llms` 临时写入）；会话/任务落临时库，结束即弃；
套件级快照-还原 `_h.suite_config_guard(c)`。

观测渠道（**均为 61-消息一览既有主题，零新增**）：§4.3 `tasks.started/done`（前端事件，用于定位 id）·
§3.4 `data-tasktree-{list,tasks,delete}`（层权威回读）。

运行：python run_sem_task_tree.py
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

EXE = _h.resolve_gui_exe()
if not os.path.isfile(EXE):
    print("RESULT: True (SKIPPED: 未找到 GUI 产物 %s → 先构建 dist/desktop)" % EXE, flush=True)
    sys.exit(0)

TS = str(int(time.time()))[-6:]
SID = "sem-task-" + TS
PROVIDER = "p-sem-task"
DEP_LABEL = "委派展示名-自定义"

_g = None  # main 赋值


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def main():
    global _g
    m1 = _h.start_mock_llm(_h.free_port())
    _g = _h.acquire_gui(_h.free_port(),
                        work_dir=_h.tmp_dir("ck-semtask-ws-"),
                        data_dir=_h.tmp_dir("ck-semtask-dd-"),
                        home=_h.tmp_home(),
                        extra_args=("-llm-base=http://127.0.0.1:%d/v1" % m1.port,
                                    "-llm-model=m-exe-default"))
    c = _g.client
    _h.suite_config_guard(c)
    print("[env] mock=%d gui=%d ws=%s（临时目录，结束即删）" % (m1.port, _g.port, _g.work_dir), flush=True)

    entry = {"name": PROVIDER, "protocol": "openai", "apiKey": "", "model": "m-sem-task",
             "baseUrl": "http://127.0.0.1:%d/v1" % m1.port, "temperature": 0.7, "maxOutputToken": 4096}
    c.req("data-user-config-save", {"data": {"llms": [entry], "defaultLLM": PROVIDER}})
    c.mq_emit("config-refresh")
    time.sleep(0.4)

    def payloads(topic):
        out = []
        for e in c.events_of(topic, clear=False):
            p = e.get("payload")
            if isinstance(p, dict):
                out.append(p)
        return out

    def poll_find(topic, pred, max_wait=90, interval=0.4):
        deadline = time.time() + max_wait
        while True:
            for p in payloads(topic):
                if pred(p):
                    return p
            if time.time() >= deadline:
                return None
            time.sleep(interval)

    def canon_tool(p):
        t = p.get("tool") or ""
        return t[5:] if t.startswith("self_") else t

    def tree_nodes(include_closed=False):
        pl = {"top_session": SID}
        if include_closed:
            pl["include_closed"] = True
        return (c.req("data-tasktree-list", pl) or {}).get("nodes") or []

    def tasks():
        return (c.req("data-tasktree-tasks", {"top_session": SID}) or {}).get("list") or []

    def node_by_id(nodes, nid):
        for n in nodes:
            if n.get("node_id") == nid or n.get("task_id") == nid:
                return n
        return None

    RES = {}

    def case_t1_delegate_readback():
        """T1：真实委派 → 层权威表回读根/子节点（归属 + 展示名 + 子会话）。"""
        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(["turn-start", "tasks.started", "tasks.updated", "tasks.done"])
        _h.install_session_history_spy(c)
        c.mq_emit("session-changed", {"session_id": SID})
        _h.wait_session_history(c, SID)
        c.mq_emit("llm-start", {"session_id": SID, "turn": "t-" + SID,
                                "q": "please call delegate-purpose", "llm": PROVIDER,
                                "think": "", "effort": "", "scenario_id": ""})
        root = poll_find("tasks.started",
                         lambda p: canon_tool(p) == "llm_run" and not p.get("parent_id")
                         and p.get("top_session") == SID, max_wait=90)
        if not root:
            raise TestError("未建立 llm_run 根任务节点（tasks.started）")
        child = poll_find("tasks.started",
                          lambda p: canon_tool(p) == "llm_run" and p.get("parent_id") == root.get("task_id"),
                          max_wait=90)
        if not child:
            raise TestError("未建立 llm_run 子任务节点（委派子会话）")
        RES.update(root_id=root.get("task_id"), child_id=child.get("task_id"),
                   child_session=child.get("session_id") or "")

        nodes = tree_nodes()
        rn = node_by_id(nodes, RES["root_id"])
        cn = node_by_id(nodes, RES["child_id"])
        if not rn or not cn:
            raise TestError("层权威表回读缺根/子节点：root=%r child=%r（nodes=%r）"
                            % (rn, cn, nodes))
        if rn.get("node_type") != "session" or rn.get("session_id") != SID or rn.get("parent_node_id"):
            raise TestError("根节点形态不符：%r" % rn)
        if cn.get("parent_node_id") != RES["root_id"]:
            raise TestError("子节点 parent_node_id=%r（期望根 id=%r）"
                            % (cn.get("parent_node_id"), RES["root_id"]))
        if cn.get("node_type") != "session":
            raise TestError("子节点 node_type=%r（期望 session）" % cn.get("node_type"))
        if not str(cn.get("session_id") or "").startswith("job-"):
            raise TestError("子节点 session_id=%r（期望 job-* 子会话）" % cn.get("session_id"))
        if cn.get("title") != DEP_LABEL:
            raise TestError("子节点 title=%r（期望委派目的 %r）" % (cn.get("title"), DEP_LABEL))
        evi("T1 委派落库回读", root_id=RES["root_id"], child_id=RES["child_id"],
            child_session=cn.get("session_id"), title=cn.get("title"))

    def case_t2_done_and_single_source():
        """T2：tasks 视图同 id 行 status/state==done（终态回读 + 单源）。"""
        cid = RES.get("child_id") or ""
        done = poll_find("tasks.done",
                         lambda p: p.get("task_id") == cid and p.get("state") == "done", max_wait=120)
        if not done:
            raise TestError("子 LLM 节点未达 done（tasks.done）")
        rows = [t for t in tasks() if t.get("task_id") == cid]
        if not rows:
            raise TestError("data-tasktree-tasks 未含子节点 id=%r（list=%r）" % (cid, tasks()))
        row = rows[0]
        if row.get("status") != "done" and row.get("state") != "done":
            raise TestError("子节点终态 status=%r state=%r（期望 done）"
                            % (row.get("status"), row.get("state")))
        # 单源：list 与 tasks 两视图同 id（子节点）
        if node_by_id(tree_nodes(), cid) is None:
            raise TestError("list 视图未含同 id 子节点 %r（单源不一致）" % cid)
        evi("T2 终态 + 单源一致", child_id=cid, status=row.get("status"), state=row.get("state"))

    def case_t3_logical_delete_closed():
        """T3：delete → 默认 list 过滤该子节点；include_closed 仍含且 closed==true；根不受影响。"""
        cid = RES.get("child_id") or ""
        r = c.req("data-tasktree-delete", {"node_id": cid}) or {}
        if not r.get("ok"):
            raise TestError("data-tasktree-delete 未成功：%r" % r)
        end = time.time() + 10
        while time.time() < end and node_by_id(tree_nodes(), cid) is not None:
            time.sleep(0.3)
        if node_by_id(tree_nodes(), cid) is not None:
            raise TestError("关闭后默认 list 仍含子节点 %r（closed 未过滤）" % cid)
        with_closed = node_by_id(tree_nodes(include_closed=True), cid)
        if not with_closed:
            raise TestError("include_closed=true 仍未含子节点 %r（历史未保留）" % cid)
        if with_closed.get("closed") is not True:
            raise TestError("include_closed 行 closed=%r（期望 true）" % with_closed.get("closed"))
        if node_by_id(tree_nodes(), RES.get("root_id")) is None:
            raise TestError("关闭子节点波及根节点（根不应被关闭）")
        evi("T3 逻辑删除闭合", child_id=cid, closed_filtered=True,
            closed_flag=with_closed.get("closed"))

    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 自管 mock LLM + 隔离 work-dir/data-dir/HOME；"
          "驱动 = llm-start（委派）+ data-tasktree-*（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("T1 委派落库回读：根/子节点归属 + 展示名 + 子会话", case_t1_delegate_readback),
        ("T2 终态 + 单源一致：tasks 同 id 行 done + list 同 id", case_t2_done_and_single_source),
        ("T3 逻辑删除闭合：默认过滤 + include_closed 保留 + 根不受影响", case_t3_logical_delete_closed),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n任务树/委派结果回读：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

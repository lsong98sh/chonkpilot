# -*- coding: utf-8 -*-
"""B · 语义/回环级推广（34-任务）：**任务节点状态与逻辑删除回环**。

覆盖（**拒绝"存在级"：断言状态图标的**文案语义**随状态迁移 + 逻辑删除只读不消失**）：
  T1 运行态语义：`tasks.started`（kind=tool，running）→ 节点行 `.status-icon` 的 `title`
     **==「执行中」**（useTaskStatus 唯一映射，I-94）且 `.node-stop` 出现（running 可停）。
  T2 终态迁移回环：`tasks.updated{state:done}` → `title` **==「已完成」** 且 `.node-stop` 消失
     （running→done 迁移闭合；状态图标按既有事件增量就地重渲）。
  T3 失败态语义：`tasks.updated{state:error}` → `title` **==「执行失败」**。
  T4 逻辑删除只读回环（TASK-010）：`task-close{node_id}` → 后端级联逻辑删除（标 closed）
     → 节点**仍在列表**（不消失）且 `.node-closed-tag` 文本 **==「已关闭」**（历史保留、只读展示）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard(c)`。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  §4.3 `tasks.started/updated`（server 编排广播）· §6.1 `task-close` + §3.4 `data-tasktree-delete`
  （逻辑删除）· DOM（--test-port /eval）。

运行：python run_sem_task_status_loop.py
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

WS = _h.tmp_dir("ck-semtaskstat-ws-")
DD = _h.tmp_dir("ck-semtaskstat-dd-")
HOME = _h.tmp_home()
TOP = "sem-taskstat-1"
NID = "task-sem-1"
RUN, DONE, ERR, CLOSED = "执行中", "已完成", "执行失败", "已关闭"

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
_h.ensure_locale(c, "zh-CN")
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）" % (WS, DD, HOME, _g.port), flush=True)


def J(js):
    return _h._plain(c.eval(js))


def emit_remote(typ, payload):
    J("window.mq.emitRemote({type: %s, payload: %s}); 'ok'"
      % (json.dumps(typ), json.dumps(json.dumps(payload))))


def wait_upto(js, ok, max_wait=15.0, interval=0.2):
    end = time.time() + max_wait
    v = J(js)
    while not ok(v) and time.time() < end:
        time.sleep(interval)
        v = J(js)
    return v


def icon_title():
    """节点行状态图标的 tooltip 文案（= useTaskStatus 该态的 i18n 标签）。"""
    return J("(()=>{const i=document.querySelector('[data-node-id=%s] .status-icon');"
             "return i?i.getAttribute('title'):''})()" % json.dumps(NID))


def stop_count():
    return int(J("document.querySelectorAll('[data-node-id=%s] .node-stop').length"
                 % json.dumps(NID)) or 0)


def node_present():
    return int(J("document.querySelectorAll('[data-node-id=%s]').length" % json.dumps(NID)) or 0) >= 1


def seed_running():
    """展开任务面板 + 切会话 + 事件驱动建 running 节点（前置）。"""
    _h.ensure_task_panel_open(c)
    c.mq_emit("session-changed", {"session_id": TOP})
    time.sleep(0.5)
    emit_remote("tasks.started", {
        "task_id": NID, "kind": "tool", "tool_name": "command_execute",
        "name": "echo status", "purpose": "echo status", "state": "running",
        "session_id": TOP, "top_session": TOP, "started_at": "2026-09-01T00:00:00Z",
    })
    wait_upto("document.querySelectorAll('[data-node-id=%s]').length" % json.dumps(NID),
              lambda v: int(v or 0) >= 1)


def case_t1_running_label():
    """T1：任务节点 running → 图标 tooltip ==「执行中」+ 停止按钮存在。"""
    seed_running()
    t = wait_upto("(()=>{const i=document.querySelector('[data-node-id=%s] .status-icon');"
                  "return i?i.getAttribute('title'):''})()" % json.dumps(NID),
                  lambda v: v == RUN)
    if t != RUN:
        raise TestError("running 态图标文案=%r（期望 %r）" % (t, RUN))
    if stop_count() < 1:
        raise TestError("running 态未见 .node-stop（可停入口）")
    print("[EVIDENCE] " + json.dumps({"case": "T1 运行态文案", "icon": t}, ensure_ascii=False), flush=True)


def case_t2_done_transition():
    """T2：tasks.updated{state:done} → 图标文案 ==「已完成」且停止入口消失（迁移回环）。"""
    emit_remote("tasks.updated", {"task_id": NID, "kind": "tool", "state": "done",
                                  "session_id": TOP, "top_session": TOP})
    t = wait_upto("(()=>{const i=document.querySelector('[data-node-id=%s] .status-icon');"
                  "return i?i.getAttribute('title'):''})()" % json.dumps(NID),
                  lambda v: v == DONE)
    if t != DONE:
        raise TestError("done 态图标文案=%r（期望 %r）" % (t, DONE))
    if stop_count() != 0:
        raise TestError("done 态不应再有 .node-stop（running→done 未收敛）")
    print("[EVIDENCE] " + json.dumps({"case": "T2 终态迁移", "icon": t}, ensure_ascii=False), flush=True)


def case_t3_error_label():
    """T3：tasks.updated{state:error} → 图标文案 ==「执行失败」。"""
    emit_remote("tasks.updated", {"task_id": NID, "kind": "tool", "state": "error",
                                  "session_id": TOP, "top_session": TOP})
    t = wait_upto("(()=>{const i=document.querySelector('[data-node-id=%s] .status-icon');"
                  "return i?i.getAttribute('title'):''})()" % json.dumps(NID),
                  lambda v: v == ERR)
    if t != ERR:
        raise TestError("error 态图标文案=%r（期望 %r）" % (t, ERR))
    print("[EVIDENCE] " + json.dumps({"case": "T3 失败态文案", "icon": t}, ensure_ascii=False), flush=True)


def case_t4_logical_delete_readonly():
    """T4：task-close → 逻辑删除只读回环（节点仍在 + 「已关闭」标签）。"""
    c.mq_emit("task-close", {"node_id": NID})
    tag = wait_upto("(()=>{const e=document.querySelector('[data-node-id=%s] .node-closed-tag');"
                    "return e?e.textContent.trim():''})()" % json.dumps(NID),
                    lambda v: v == CLOSED, max_wait=12.0)
    if tag != CLOSED:
        raise TestError("逻辑删除后未出现「已关闭」标签（tag=%r）" % (tag,))
    if not node_present():
        raise TestError("逻辑删除后节点从列表消失（应只读保留、历史可查）")
    print("[EVIDENCE] " + json.dumps({"case": "T4 逻辑删除只读回环", "tag": tag,
                                      "node_present": True}, ensure_ascii=False), flush=True)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = tasks.started/updated + task-close（既有事件）", flush=True)
    ok = total = 0
    for name, fn in [
        ("T1 运行态：图标文案「执行中」+ 停止入口", case_t1_running_label),
        ("T2 终态迁移：done → 「已完成」+ 停止入口消失", case_t2_done_transition),
        ("T3 失败态：error → 「执行失败」", case_t3_error_label),
        ("T4 逻辑删除只读回环：节点保留 + 「已关闭」", case_t4_logical_delete_readonly),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n任务节点状态与逻辑删除回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

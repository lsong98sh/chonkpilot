# -*- coding: utf-8 -*-
"""工具域补充测试（IDE 模式 · mock LLM）：script_run 的
同步、异步、取结果（tool_result）覆盖（复用 run_llm 基础设施）。

覆盖矩阵：
  - script_run：同步（call py → echo py-ok）、异步（call py-async → 转后台 → tool_result）

注意：事件经 test-port deferral 批量推送，同批事件会被一次 clear 全部清走；
所有 llm-tool-call 断言采用"累积去重集合"，避免批次共享丢失（C16 同款教训）。
工具名按**暴露名**路由（self_*），断言侧剥离 self_ 归一（_canon）。
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import run_llm
from chonk_client import TestError, run_case

c = run_llm.c

EVENTS = run_llm.EVENTS + ["task-started", "task-ended"]


def _canon(tool):
    """工具名归一：剥离网关自节点前缀 self_（暴露名 → 契约名）。"""
    t = tool or ""
    return t[5:] if t.startswith("self_") else t


def _wait_tool(expect_tool, max_wait=60):
    """等待 llm-tool-call 出现指定工具（去重累积，容忍同批 clear）。"""
    seen = set()
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in c.events_of("llm-tool-call", clear=True):
            t = _canon((e["payload"] or {}).get("tool"))
            if t:
                seen.add(t)
        if expect_tool in seen:
            return seen
        time.sleep(0.3)
    raise TestError(f"llm-tool-call 无 {expect_tool}（已见: {seen}）")


def _wait_result(expect_tool, max_wait=60):
    """等待 tool-result 出现指定工具，返回 {canon_tool: payload}。"""
    seen = {}
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in c.events_of("tool-result", clear=True):
            p = e["payload"] or {}
            t = _canon(p.get("tool"))
            if t and t not in seen:
                seen[t] = p
        if expect_tool in seen:
            return seen
        time.sleep(0.3)
    raise TestError(f"tool-result 无 {expect_tool}（已见: {list(seen)}）")


def _wait_pair_async(max_wait=30):
    """等待 tool-pair 出现 async 状态。"""
    seen = set()
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in c.events_of("tool-pair", clear=True):
            s = (e["payload"] or {}).get("status")
            if s:
                seen.add(s)
        if "async" in seen:
            return
        time.sleep(0.3)
    raise TestError(f"tool-pair 无 async 转后台（已见状态: {seen}）")


def case_script_sync():
    """script_run 同步：工具成功执行 + 结果含 stdout + 任务节点注册。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = run_llm.create_session()
    run_llm.activate_session(sid)
    run_llm.start_turn(sid, "please call py")
    run_llm.confirm_turn(sid, max_wait=60)  # mq-only 轮次确权（llm-started 已无源）
    _wait_tool("script_run")
    _wait_result("script_run")
    # 结果文本含 stdout（py-ok）
    deadline = time.time() + 10
    while time.time() < deadline:
        if "py-ok" in c.eval('document.body.innerText'):
            break
        time.sleep(1)
    else:
        raise TestError("script_run 结果缺 stdout（py-ok）")
    # script_run 经任务编排注册节点（task-started，task_id=tk-*）
    ts = run_llm.wait_event("task-started", max_wait=30)
    if not any((e["payload"] or {}).get("task_id", "").startswith("tk-")
               and _canon((e["payload"] or {}).get("tool")) == "script_run" for e in ts):
        raise TestError(f"script_run 未注册任务节点: "
                        f"{[(e['payload'] or {}).get('task_id') for e in ts]}")
    run_llm.wait_event("llm-complete", max_wait=60)


def case_script_async():
    """script_run 异步：转后台（tool-pair async）→ 终态 done + 结果回填。

    新架构异步 = turn 挂起等后台完成回报（不再由 LLM 主动 tool_result 轮询上下文），
    故以 tool-pair async + tool-result 终态（含 stdout）确权。
    """
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = run_llm.create_session()
    run_llm.activate_session(sid)
    run_llm.start_turn(sid, "please call py-async")
    run_llm.confirm_turn(sid, max_wait=60)
    _wait_tool("script_run")
    _wait_pair_async()
    # script_run handler 自注册任务节点
    run_llm.wait_event("task-started", max_wait=30)
    # 后台完成 → tool-result 终态（result 含 stdout py-slow）
    seen = _wait_result("script_run", max_wait=90)
    text = json.dumps(seen["script_run"], ensure_ascii=False)
    if "py-slow" not in text:
        raise TestError(f"异步 script_run 终态缺 stdout: {text[:400]}")
    # 最终回复纳入工具结果
    deadline = time.time() + 15
    while time.time() < deadline:
        if "py-slow" in c.eval('document.body.innerText'):
            break
        time.sleep(1)
    else:
        raise TestError("异步 script_run 最终回复未含 py-slow")
    run_llm.wait_event("llm-complete", max_wait=60)


def _close_ask_dialogs():
    """关闭遗留 ask 弹窗（前次失败残留；点全部 cancel 按钮）。"""
    c.eval("""(() => {
      const btns = [...document.querySelectorAll('.ask-user-content .cancel-btn')];
      btns.forEach(b => b.dispatchEvent(new MouseEvent('click', { bubbles: true })));
      return btns.length;
    })()""", 5000)
    time.sleep(0.5)


def case_ask_user_multi():
    """ask_user 多选 + 推荐：multi=true 弹窗可勾选多项 + 推荐 badge → 提交 → 顿号连接回填。

    新架构 ask 独立成事件（§4.3 session-ask → 前端 ask-user），不再走 tool-pair{user_ask}；
    作答经前端 ask-user-reply（§4.2）上行。
    """
    _close_ask_dialogs()
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS + ["ask-user", "ask-user-reply"])
    sid = run_llm.create_session()
    run_llm.activate_session(sid)
    run_llm.start_turn(sid, "please call ask-multi")
    run_llm.confirm_turn(sid, max_wait=60)
    # ask-user 事件透传 multi / recommended
    deadline = time.time() + 30
    ask = None
    while time.time() < deadline:
        for e in c.events_of("ask-user", clear=False):
            p = e["payload"] or {}
            if p.get("multi") and p.get("question"):
                ask = p
        if ask:
            break
        time.sleep(0.5)
    if not ask:
        raise TestError("ask-user 未透传 multi=true")
    if list(ask.get("options") or []) != ["构建", "测试", "部署"]:
        raise TestError(f"ask-user options 异常: {ask.get('options')}")
    # 弹窗出现：3 个选项 + 1 个推荐 badge
    deadline = time.time() + 10
    while time.time() < deadline:
        if c.exists(".ask-user-content").get("count", 0) > 0:
            break
        time.sleep(0.5)
    r = c.eval("""(() => {
      const btns = [...document.querySelectorAll('.ask-user-content .option-btn')];
      const recs = document.querySelectorAll('.ask-user-content .rec-badge').length;
      return JSON.stringify({ optCount: btns.length, recCount: recs,
                              labels: btns.map(b => b.innerText.trim()) });
    })()""")
    o = r
    for _ in range(3):
        if isinstance(o, str):
            try:
                o = json.loads(o)
            except Exception:
                break
    if not (isinstance(o, dict) and o.get("optCount") == 3):
        raise TestError(f"多选弹窗选项异常: {o}")
    # 勾选两项（构建、测试）
    c.click(".ask-user-content .option-btn:nth-child(1)", 5000)
    time.sleep(0.5)
    c.click(".ask-user-content .option-btn:nth-child(2)", 5000)
    time.sleep(1.0)  # 等 Vue 更新 canSubmit
    c.click(".ask-user-content .submit-btn", 5000)
    # ask-user-reply：answer = 顿号连接的多选
    deadline = time.time() + 10
    while time.time() < deadline:
        ar = c.events_of("ask-user-reply", clear=True)
        if ar:
            ans = (ar[0]["payload"] or {}).get("answer", "")
            if ans == "构建、测试":
                break
            raise TestError(f"ask-user-reply answer 异常: {ans!r}")
        time.sleep(0.5)
    else:
        raise TestError("未收到 ask-user-reply（多选提交失败）")
    # 推荐 badge（契约 §4.3 questions[].recommended；前端要求数组）——
    # 实测 server.ask 只透传 string 型 recommended（apps/契约类型不一致），当前为真缺陷。
    if not (isinstance(o, dict) and o.get("recCount") == 1):
        raise TestError(f"推荐 badge 未渲染（recommended 未透传到 ask-user 事件）: {o}")
    run_llm.wait_event("llm-complete", max_wait=60)


def main():
    ok = 0
    total = 0
    c.console(clear=True)
    total += 1; ok += run_case("script_run 同步（stdout + 任务节点）", case_script_sync)
    total += 1; ok += run_case("script_run 异步（转后台 + tool_result）", case_script_async)
    total += 1; ok += run_case("ask_user 多选 + 推荐（顿号回填）", case_ask_user_multi)
    errs = c.console()
    for e in errs.get("entries", []):
        if e.get("level") in ("error",):
            print(f"  [CONSOLE-ERROR] {e.get('text')}")
    print(f"\n工具域补充测试：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

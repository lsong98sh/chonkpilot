# -*- coding: utf-8 -*-
"""工具进度 title 的 L4 真机两态（GATEWAY-2 L8 · 49 §2.20 欠账）。

契约（唯一准则 = `docs/spec/60-reference/61-消息一览.md` §5.3）：
下行事件 `mcp-tools-progress{instance_id, tool_call_id, progress, total?}` →
前端 `useToolProgress`（`tool_call_id → {progress,total}`）→ 工具调用条目
（`.toolpair-section`）的 **title**：`total > 0` → `进度 N%`；**无事件 / 无 `total` → 不设 title**。

驱动与断言口径（50-测试体系 §1：mq 驱动 + mq 断言）：
  * 驱动 = 真实轮次（`llm-start` → mock LLM 产出工具调用）产生工具条目，再经
    **后端 → 前端同一入口**（`window.mq.emitRemote`，宿主 Eval 直调的同一条通道，
    与 `test_statusbar.py` / `run_sem_retry_state.py` 注入事件的既有做法一致）
    投递 `mcp-tools-progress`；**不新增主题、不改 payload**。
  * 断言 = ① mq：`mq.on('mcp-tools-progress')` 捕获到的载荷逐字段对齐；② DOM：
    `document.querySelectorAll('.toolpair-section')` 上各工具条目的 `title` 属性。

覆盖（3 条）：
  1. 显示态：`progress=2, total=5` → 该条目 title = `进度 40%`；
  2. 不显示态：同一 DOM 内另一工具条目**无**进度事件 → 无 title（不含「进度」）；
  3. 无 total：`progress=3`（缺 `total`）→ 不显示百分比（title 仍为空）。

前置：`.\build-desktop.ps1 -Debug` 产物 + mock LLM（本套件经 harness 自起/复用 8901）。
运行：python src\\test\\chonkpilot-gui\\systest\\run_tool_progress.py
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import run_llm                       # noqa: E402  复用其 GUI/mock LLM 夹具与轮次辅助
from chonk_client import TestError, run_case  # noqa: E402

c = run_llm.c

EVENTS = run_llm.EVENTS + ["mcp-tools-progress"]

_TURN_SEQ = [0]


def _deep(v):
    """循环解包 JSON 字符串（测试通道结果可能多重编码）。"""
    return run_llm._loads_deep(v)


def _start_turn(sid, q):
    """发一轮 llm-start（同会话可连续多轮：turn id 逐轮唯一）。"""
    _TURN_SEQ[0] += 1
    c.mq_emit("llm-start", {
        "session_id": sid,
        "turn": "t-%s-%d" % (sid, _TURN_SEQ[0]),
        "q": q,
        "llm": "mock",
        "think": "",
        "effort": "",
        "scenario_id": "",
    })


def _pairs():
    """当前 DOM 的工具条目列表（消息顺序）：[{label, title}]。

    title = 工具调用条目根节点 `.toolpair-section` 的 title 属性（无 → None）。
    """
    js = """(() => {
      const out = [...document.querySelectorAll('.toolpair-section')].map(el => {
        const lab = el.querySelector('.tool-name-label');
        return { label: (lab ? lab.textContent : '').trim(),
                 title: el.getAttribute('title') };
      });
      return JSON.stringify(out);
    })()"""
    v = _deep(c.eval(js, 15000))
    return v if isinstance(v, list) else []


def _wait_pairs(n, max_wait=90):
    """等 DOM 出现 n 个工具条目（不足则超时）。"""
    deadline = time.time() + max_wait
    while time.time() < deadline:
        ps = _pairs()
        if len(ps) >= n:
            return ps
        time.sleep(0.5)
    raise TestError("DOM 工具条目不足 %d 个（当前 %d）" % (n, len(_pairs())))


def _tool_call_ids():
    """本轮新增的 tool-pair 事件里的 tool_call_id（按到达顺序，不清空累积）。"""
    ids = []
    for e in c.events_of("tool-pair", clear=False):
        p = e.get("payload") or {}
        i = p.get("tool_call_id")
        if i and i not in ids:
            ids.append(i)
    return ids


def _inject_progress(tool_call_id, progress, total=None):
    """经后端 → 前端同一入口投递 `mcp-tools-progress`（payload 为 JSON 字符串，与宿主同形）。"""
    body = {"instance_id": _instance_id(), "tool_call_id": tool_call_id,
            "progress": progress}
    if total is not None:
        body["total"] = total
    js = "window.mq.emitRemote({type:'mcp-tools-progress', payload: JSON.stringify(%s)}); 'ok'" % (
        json.dumps(body, ensure_ascii=False))
    c.eval(js, 10000)


def _instance_id():
    try:
        return str(_deep(c.eval("window.__chonkpilotInstanceId || ''", 8000)) or "")
    except Exception:
        return ""


def case_progress_title_three_states():
    """三态：有 total 显示百分比 / 无事件不显示 / 无 total 不显示百分比。"""
    c.mq_on_capture(EVENTS)
    sid = run_llm.create_session()
    run_llm.activate_session(sid)

    # ① 一轮两条工具调用（mock: "call batch" → 批处理一/批处理二 两条 task_run）
    _start_turn(sid, "please call batch")
    run_llm.wait_event("llm-complete", max_wait=120)
    _wait_pairs(2)

    # ② 第二轮再来一条（mock: "call tool" → file_read）→ 同 DOM 第三个条目
    _start_turn(sid, "please call tool")
    run_llm.wait_event("llm-complete", max_wait=120)
    pairs = _wait_pairs(3)
    if len(pairs) != 3:
        raise TestError("应有 3 个工具条目（2×task_run + 1×file_read），实际 %d: %s"
                        % (len(pairs), pairs))
    ids = _tool_call_ids()
    if len(ids) < 3:
        raise TestError("tool-pair 事件的 tool_call_id 不足 3 个: %s" % ids)
    first, second, third = ids[0], ids[1], ids[2]

    # 初始：三个条目均无 title（进度事件尚未到达）
    if [p["title"] for p in pairs] != [None, None, None]:
        raise TestError("注入前进度 title 应为空: %s" % [p["title"] for p in pairs])

    # ── 用例① 显示态：progress=2 / total=5 → 进度 40% ──
    _inject_progress(first, 2, total=5)
    ev = c.wait_events("mcp-tools-progress", 1, max_wait=10)
    p0 = (ev[0].get("payload") or {})
    if p0.get("tool_call_id") != first or p0.get("progress") != 2 or p0.get("total") != 5:
        raise TestError("mcp-tools-progress 载荷未逐字段对齐（61 §5.3）: %s" % p0)
    deadline = time.time() + 15
    while time.time() < deadline:
        titles = [p["title"] for p in _pairs()]
        if titles and titles[0] == "进度 40%":
            break
        time.sleep(0.4)
    else:
        raise TestError("有 total 的进度未渲染为 title（期望 进度 40%，实际 %s）"
                        % [p["title"] for p in _pairs()])

    # ── 用例② 不显示态：另一条目无进度事件 → title 为空（不含「进度」）──
    titles = [p["title"] for p in _pairs()]
    if titles[1] is not None:
        raise TestError("无进度事件的条目不应有 title: %s" % titles)
    if any(t and "进度" in t for t in titles[1:]):
        raise TestError("无进度事件的条目不得出现「进度」: %s" % titles)

    # ── 用例③ 无 total：progress=3（缺 total）→ 不显示百分比 ──
    _inject_progress(third, 3)
    ev2 = c.wait_events("mcp-tools-progress", 1, max_wait=10)
    p2 = (ev2[0].get("payload") or {})
    if p2.get("tool_call_id") != third or p2.get("progress") != 3 or "total" in p2:
        raise TestError("无 total 的进度载荷异常（不应带 total 键）: %s" % p2)
    time.sleep(1.5)  # 给前端响应式更新留窗口
    titles = [p["title"] for p in _pairs()]
    if titles[2] is not None:
        raise TestError("缺 total 的进度不应渲染百分比 title: %s" % titles)
    if titles != ["进度 40%", None, None]:
        raise TestError("三态 title 结果不符（期望 [进度 40%%, None, None]）: %s" % titles)

    return True


def main():
    ok = 0
    total = 0
    c.console(clear=True)
    total += 1
    ok += run_case("mcp-tools-progress → 工具条目 title 三态（40% / 无事件不显示 / 无 total 不显示百分比）",
                   case_progress_title_three_states)
    for e in c.console().get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"))
    print("\n工具进度 title 两态测试：%d/%d 通过" % (ok, total))
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

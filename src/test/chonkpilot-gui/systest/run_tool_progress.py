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

# 会话消息重载链**末端**主题（run_chat_ui.py 同法）：session-changed → loadMessages
# → assessContinueState → restoreAwaitingArbitration → data-tasktree-tasks（两次：
# ChatPanel 即时一次 + MessageList 重载后一次）。等到两次 = 新会话的消息列表已落定。
RELOAD_TOPIC = "data-tasktree-tasks"
# 一次注册全部所需主题（mq_on_capture 每次调用都会清空旧事件并重置 handler）。
EVENTS = run_llm.EVENTS + ["mcp-tools-progress", RELOAD_TOPIC]

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


def _wait_reloaded(sid, max_wait=25):
    """等本会话 `data-tasktree-tasks` 到达两次（重载链末端，见 run_chat_ui.activate）。"""
    deadline = time.time() + max_wait
    while time.time() < deadline:
        hits = 0
        for e in (c.events_of(RELOAD_TOPIC, clear=False) or []):
            p = e.get("payload") if isinstance(e, dict) else None
            if isinstance(p, str):
                try:
                    p = json.loads(p)
                except Exception:
                    p = None
            if isinstance(p, dict) and p.get("session_id") == sid:
                hits += 1
        if hits >= 2:
            return True
        time.sleep(0.3)
    return False


def _activate_clean_session(sid):
    """新建/激活会话并**确证消息列表已清空**（不依赖“可能为空”，也不继承旧会话条目）。

    旧写法 `activate_session` 只固定 sleep 0.3s 后就开始发轮次：若 GUI 是复用的长驻实例
    （带旧会话/旧构建、消息重载慢），旧会话的 tool_pair 仍在 DOM → 本套件随后统计到的
    是「旧 3 + 新 3 = 6」（用户实测：同 3 个标签各两次、title 全 None）。夹具缺陷，不是
    前端重复渲染：新建会话的 DOM 实测恒为 3 条。
    """
    c.mq_emit("session-changed", {"session_id": sid})
    if not _wait_reloaded(sid):
        raise TestError("等待会话消息重载完成超时（%s, session=%s）" % (RELOAD_TOPIC, sid))
    deadline = time.time() + 20
    last = None
    while time.time() < deadline:
        last = _pairs()
        if not last:
            return
        time.sleep(0.3)
    raise TestError("开跑前会话未清空：DOM 仍有 %d 个工具条目（旧会话残留/未隔离）: %s"
                    % (len(last or []), last))


def _pairs():
    """当前**本会话消息列表**内的工具条目（消息顺序）：[{label, title, message_id}]。

    作用域 = 主 ChatPanel 的 `.message-list`（不再 `document.querySelectorAll` 全局取样：
    预览/旧会话/其它面板残留会把条数污染），且只取 tool_pair 消息卡。
    title = 工具调用条目根节点 `.toolpair-section` 的 title 属性（无 → None）。
    message_id = 所属 `.message-item` 的 `data-id`（`tp_<tool_call_id>`，用于断言顺序/唯一性）。
    """
    js = """(() => {
      const root = document.querySelector('.chat-panel .message-list')
                || document.querySelector('.message-list');
      if (!root) return JSON.stringify([]);
      const out = [...root.querySelectorAll('.message-item.tool_pair .toolpair-section')].map(el => {
        const lab = el.querySelector('.tool-name-label');
        const item = el.closest('.message-item');
        return { label: (lab ? lab.textContent : '').trim(),
                 title: el.getAttribute('title'),
                 message_id: item ? item.getAttribute('data-id') : '' };
      });
      return JSON.stringify(out);
    })()"""
    v = _deep(c.eval(js, 15000))
    return v if isinstance(v, list) else []


def _wait_pairs_exact(n, desc, max_wait=90):
    """等 DOM **恰好** n 个工具条目；超过 n 立即报重复（不做“>=n 即通过”的弱化）。"""
    deadline = time.time() + max_wait
    last = []
    while time.time() < deadline:
        last = _pairs()
        if len(last) == n:
            return last
        if len(last) > n:
            raise TestError("%s：应有 %d 个工具条目，实际 %d（疑似旧会话残留/重复渲染）: %s"
                            % (desc, n, len(last), last))
        time.sleep(0.5)
    raise TestError("%s：DOM 工具条目不足 %d 个（当前 %d）: %s"
                    % (desc, n, len(last), last))


def _tool_calls(sid):
    """本会话本轮新增的 tool-call 事件（按到达/消息顺序）：[{tool, tool_call_id}]。

    以 `llm-tool-call`（= 前端创建 tool_pair 卡片的同一流事件）为序，而不是任务树
    `tool-pair` 的到达序（批量任务的 task-started 顺序不保证与消息顺序一致）。
    """
    out = []
    seen = set()
    for e in c.events_of("llm-tool-call", clear=False):
        p = e.get("payload") or {}
        i = p.get("tool_call_id")
        if not i or i in seen:
            continue
        if p.get("session_id") not in (None, sid):
            continue
        seen.add(i)
        out.append({"tool": p.get("tool") or "", "tool_call_id": i})
    return out


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
    # 夹具隔离（2026-11-07）：等消息重载落定 + 断言本会话起始为空（必须有 0 条，
    # 不接受“列表可能为空”）。否则复用长驻 GUI 时会把旧会话的 tool_pair 一并统计。
    _activate_clean_session(sid)

    # ① 一轮两条工具调用（mock: "call batch" → 批处理一/批处理二 两条 task_run）
    _start_turn(sid, "please call batch")
    run_llm.wait_event("llm-complete", max_wait=120)
    _wait_pairs_exact(2, "第一轮（期望 2×task_run）")

    # ② 第二轮再来一条（mock: "call tool" → file_read）→ 同 DOM 第三个条目
    _start_turn(sid, "please call tool")
    run_llm.wait_event("llm-complete", max_wait=120)
    pairs = _wait_pairs_exact(3, "第二轮（期望 2×task_run + 1×file_read）")

    # 期望值断言：本会话 tool-call 事件序列 + DOM 条目**条数与顺序**（tp_<tool_call_id>）。
    calls = _tool_calls(sid)
    if [x.get("tool") for x in calls] != ["task_run", "task_run", "file_read"]:
        raise TestError("本会话 tool-call 事件序列不符（期望 2×task_run + 1×file_read）: %s" % calls)
    ids = [x["tool_call_id"] for x in calls]
    first, second, third = ids[0], ids[1], ids[2]
    dom_ids = [p.get("message_id") for p in pairs]
    if dom_ids != ["tp_" + first, "tp_" + second, "tp_" + third]:
        raise TestError("工具条目与 tool-call 事件的条数/顺序不符（期望 3 条依序）: DOM=%s 事件=%s"
                        % (dom_ids, ["tp_" + i for i in ids]))
    for call, pair in zip(calls, pairs):
        if call["tool"] not in (pair.get("label") or ""):
            raise TestError("工具条目标签与 tool 不符（期望含 %r）: %s"
                            % (call["tool"], pair.get("label")))

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

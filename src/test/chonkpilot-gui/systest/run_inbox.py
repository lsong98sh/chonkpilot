# -*- coding: utf-8 -*-
"""INBOX（中途插话）L4 端到端：真实 exe + mock LLM + **mq 驱动 / mq 断言**。

覆盖：工具执行中（强制异步 pending）插入一段对话 → 插话在本轮结束前被处理（出流）；
主轮不提前 complete；工具回执后主轮续轮。
设计：docs/agent-chat/中途插话设计方案.md §3.1（S1）/ §7（INS-001）。
驱动：`llm-start`（mock 关键词 "call slow-insert" → `task_run` + 调用级
      `async=always` + `Start-Sleep 6`：派发即 pending，工具执行中留出插话窗口）
      → `llm-inbox-add`；断言：`llm-receive`（插话回显）先于 `llm-complete`。
      口径变更（GATEWAY-2 L1 → 2026-10-23）：原用 `dsl_run`，DSL 删除后曾用 `script_run`；
      `script_run` 亦随 core 契约退役 → 现行 `task_run` 显式 `async=always`（同一 pending 语义）。
前置：usr llms 含 name="mock" 条目（套件级快照-还原；usr 主库不受 work-dir 隔离）。
"""

import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402
import harness as _h  # noqa: E402

_G = _h.acquire_gui(_h.free_port())
_M = _h.acquire_mock_llm(_h.DEFAULT_MOCK_LLM_PORT)
c = _G.client
# 套件级配置快照-还原（usr 主库不受 work-dir 隔离 → 本套件写入的 llms 条目退出即回滚）。
_h.suite_config_guard(c)


def wait_ui_ready(max_wait=60):
    """等首屏可交互（输入框存在 + /eval 可应答）。

    **必要性（2026-10-27 批跑定位）**：`acquire_gui` 只等 server ready，**不等页面首屏**；
    紧接着写 usr 配置 + `location.reload()` 会让两次加载叠加 → 渲染线程长时间忙，
    随后 `events_of` 的 /eval 连续重试超时（INBOX-S1 假红）。
    """
    deadline = time.time() + max_wait
    while time.time() < deadline:
        try:
            raw = c.eval("document.querySelectorAll('[contenteditable=\"true\"]').length || 0", 8000)
            if int(str(raw).strip().strip('"') or 0) >= 1:
                return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


# 2026-10-27（42 (290)）：共享 work-dir 的活动会话可能是他套件留下的大历史 → 首屏恢复会
# 长时间占满渲染线程（/eval 连续 ExecuteScript 超时，INBOX-S1 假红）。先建空会话并把活动位
# 指向它，再做**唯一一次**稳定重载（之后不再 reload，避免两次加载叠加）。
try:
    c.req("data-session-ensure-session", {"data": {"session_id": "inbox-bootstrap"}}, 8000)
    c.req("data-session-active-set", {"session_id": "inbox-bootstrap"}, 8000)
except Exception:
    pass
_h.reload_and_settle(c)
wait_ui_ready()
# 前置：usr llms 需含 name="mock" 的可用条目（llm-start 的 llm 按 name 解析），baseUrl 指向 mock LLM。
# usr 主库不受 work-dir 隔离 → 追加/覆盖同名条目并把默认项指向它。
_LLM_NAME = "mock"
_cfg0 = _h.user_config_load(c)
_llms = [e for e in (_cfg0.get("llms") or []) if isinstance(e, dict) and e.get("name") != _LLM_NAME]
_llms.append({"name": _LLM_NAME, "protocol": "openai", "model": "mock-model",
              "baseUrl": "http://127.0.0.1:%d/v1" % _h.DEFAULT_MOCK_LLM_PORT, "apiKey": ""})
c.req("data-user-config-save", {"data": {"llms": _llms, "defaultLLM": len(_llms) - 1}})
# 2026-10-23：usr `llms` 需经 `data-user-config-refresh` 触发 server 侧 router 对账
#（reconcileLLMProviders）→ 否则 llm-start 的 provider name 解析不到、回落到默认 provider，
# 工具派发即终态（INBOX-S1 假红）。
c.mq_emit("config-refresh")
# **不刷新页面**（2026-10-27）：刷新会按 `active_session_id` 恢复上一个会话——共享工作区里
# 可能是个历史很大的会话（实测 10 条消息 2.4MB HTML），WebView2 渲染线程阻塞数十秒，
# 紧随的 /eval 全部超时（INBOX-S1 假红）。本用例经 `llm-start` 走**服务端**按名解析 provider，
# 不依赖前端 llmList；`config-refresh` 已足以让 server 侧完成 router 对账。

# 事件捕获（mq.on → window.__chonkEvents）；events_of 只读捕获缓冲。
_EVENTS = ["turn-start", "llm-tool-call", "llm-receive", "llm-complete",
           "session-complete", "tool-pair", "tool-result", "error",
           "tasks.started", "tasks.updated", "tasks.done"]
c.mq_on_capture(_EVENTS)


def dump_events(limit=40):
    ev = c.eval("JSON.stringify((window.__chonkEvents||{}).events||[])")
    try:
        import json as _json
        events = _json.loads(_json.loads(ev))
    except Exception:
        print("[dbg] events raw=%r" % (ev,), flush=True)
        return
    for e in events[-limit:]:
        print("[dbg.ev] %s %r" % (e.get("topic"), e.get("payload")), flush=True)

_SEQ = [0]


def new_session():
    _SEQ[0] += 1
    return "inbox-%d-%03d" % (int(time.time() * 1000), _SEQ[0])


def wait_topic(topic, pred=None, max_wait=60):
    """等某主题事件（**容忍瞬态 /eval 超时**：批跑下渲染线程偶发忙，重试到 deadline 为止）。"""
    deadline = time.time() + max_wait
    while time.time() < deadline:
        try:
            evs = c.events_of(topic, clear=False)
        except TestError:
            time.sleep(0.3)
            continue
        for e in evs:
            p = e.get("payload") or {}
            if pred is None or pred(p):
                return p
        time.sleep(0.3)
    topics = {}
    for t in ("turn-start", "session-turn-start", "llm-tool-call", "llm-receive",
              "llm-complete", "session-complete", "error", "task-started", "tasks.started"):
        try:
            topics[t] = len(c.events_of(t, clear=False))
        except TestError:
            topics[t] = "eval-timeout"
    raise TestError("等待 %s 超时（%ss）；事件计数=%s" % (topic, max_wait, topics))


def stream_texts():
    out = []
    try:
        evs = c.events_of("llm-receive", clear=False)
    except TestError:
        return ""
    for e in evs:
        p = e.get("payload") or {}
        if p.get("type") == "text":
            inner = p.get("payload") or {}
            if isinstance(inner, dict) and inner.get("text"):
                out.append(inner["text"])
    return "".join(out)


def completed_turn(turn):
    """该轮是否已终态（容忍瞬态 /eval 超时 → 视为「尚未终态」，下一轮循环再读）。"""
    try:
        evs = c.events_of("llm-complete", clear=False)
    except TestError:
        return False
    return any((e.get("payload") or {}).get("turn") == turn for e in evs)


def case_insert_during_tool():
    """工具执行中插话 → 插话优先出流，主轮不提前终态。"""
    sid = new_session()
    c.mq_emit("session-changed", {"session_id": sid})
    time.sleep(0.3)
    turn = "t-" + sid
    c.mq_emit("llm-start", {
        "session_id": sid, "turn": turn,
        "q": "please call slow-insert",
        "llm": _LLM_NAME, "think": "", "effort": "", "scenario_id": "",
    })
    # 工具已派发（强制异步 → pending）
    wait_topic("llm-tool-call", max_wait=30)
    if completed_turn(turn):
        raise TestError("工具派发后主轮不应立即终态")
    # 工具执行中插入
    c.mq_emit("llm-inbox-add", {"session": sid, "llm": _LLM_NAME, "content": "插话e2e"})
    # 插话回显必须出现在主轮 complete 之前
    deadline = time.time() + 30
    while time.time() < deadline:
        if "插话e2e" in stream_texts():
            break
        if completed_turn(turn):
            dump_events()
            raise TestError("主轮已终态但插话未被处理")
        time.sleep(0.3)
    else:
        dump_events()
        raise TestError("插话未在工具运行期间被处理（llm-receive 无回显）")
    # 工具回执后续轮 → 主轮最终 complete（慢工具 ~7s）
    wait_topic("llm-complete", lambda p: p.get("turn") == turn, 90)


def main():
    ok, total = 0, 0
    total += 1
    ok += run_case("INBOX-S1 工具执行中插话（llm-inbox-add → 插话先出流）", case_insert_during_tool)
    print("\nINBOX e2e：%d/%d 通过" % (ok, total))
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

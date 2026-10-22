# -*- coding: utf-8 -*-
"""INBOX（中途插话）L4 端到端：真实 exe + mock LLM + **mq 驱动 / mq 断言**。

覆盖：工具执行中（强制异步 pending）插入一段对话 → 插话在本轮结束前被处理（出流）；
主轮不提前 complete；工具回执后主轮续轮。
设计：docs/agent-chat/中途插话设计方案.md §3.1（S1）/ §7（INS-001）。
驱动：`llm-start`（mock 关键词 "call slow-insert" → `dsl_run` + `PC_SLP 8000`：契约
      `async=always` 且无 `_meta.timeout` → llm-server 强制异步 pending）
      → `llm-inbox-add`；断言：`llm-receive`（插话回显）先于 `llm-complete`。
前置：usr llms 含 name="mock" 条目（套件级快照-还原；usr 主库不受 --data-dir 隔离）。
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
# 套件级配置快照-还原（usr 主库不受 --data-dir 隔离 → 本套件写入的 llms 条目退出即回滚）。
_h.suite_config_guard(c)
# 前置：usr llms 需含 name="mock" 的可用条目（llm-start 的 llm 按 name 解析），baseUrl 指向 mock LLM。
# usr 主库不受 --data-dir 隔离 → 追加/覆盖同名条目并把默认项指向它。
_LLM_NAME = "mock"
_cfg0 = _h.user_config_load(c)
_llms = [e for e in (_cfg0.get("llms") or []) if isinstance(e, dict) and e.get("name") != _LLM_NAME]
_llms.append({"name": _LLM_NAME, "protocol": "openai", "model": "mock-model",
              "baseUrl": "http://127.0.0.1:%d/v1" % _h.DEFAULT_MOCK_LLM_PORT, "apiKey": ""})
c.req("data-user-config-save", {"data": {"llms": _llms, "defaultLLM": len(_llms) - 1}})
try:
    c.eval("location.reload(); 'ok'")
except Exception:
    pass
time.sleep(3)

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
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in c.events_of(topic, clear=False):
            p = e.get("payload") or {}
            if pred is None or pred(p):
                return p
        time.sleep(0.3)
    topics = {}
    for t in ("turn-start", "session-turn-start", "llm-tool-call", "llm-receive",
              "llm-complete", "session-complete", "error", "task-started", "tasks.started"):
        topics[t] = len(c.events_of(t, clear=False))
    raise TestError("等待 %s 超时（%ss）；事件计数=%s" % (topic, max_wait, topics))


def stream_texts():
    out = []
    for e in c.events_of("llm-receive", clear=False):
        p = e.get("payload") or {}
        if p.get("type") == "text":
            inner = p.get("payload") or {}
            if isinstance(inner, dict) and inner.get("text"):
                out.append(inner["text"])
    return "".join(out)


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
    if any((e.get("payload") or {}).get("turn") == turn for e in c.events_of("llm-complete", clear=False)):
        raise TestError("工具派发后主轮不应立即终态")
    # 工具执行中插入
    c.mq_emit("llm-inbox-add", {"session": sid, "llm": _LLM_NAME, "content": "插话e2e"})
    # 插话回显必须出现在主轮 complete 之前
    deadline = time.time() + 30
    while time.time() < deadline:
        if "插话e2e" in stream_texts():
            break
        if any((e.get("payload") or {}).get("turn") == turn for e in c.events_of("llm-complete", clear=False)):
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

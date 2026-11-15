# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ②：会话消息 + LLM 工具调用链。

驱动 = `llm-start`（消息面）→ mock LLM（127.0.0.1:8901）→ 真实工具（file_read）。
断言 = 事件链（turn-start / llm-tool-call / tool-result / llm-complete）+ 历史回读 + DOM 落点。
"""

import json

import harness as _h

from .framework import case
from .workspace import SEED, ensure_demo_file


def _canon(tool):
    t = tool or ""
    return t[5:] if t.startswith("self_") else t


def _payloads(env, topic):
    out = []
    for e in env.events(topic):
        p = e.get("payload")
        if isinstance(p, dict):
            out.append(p)
    return out


@case("S-CHAT-01", "会话消息", "一轮对话：turn-start → 回复正文 → llm-complete → 历史回读",
      stage="seeded", order=70, fp="61 §4.2/§4.3 · 61 §3.2",
      desc="llm-start 发唯一哨兵 → 主会话 turn-start 确权、终态 ok 且正文含哨兵（mock 回显）；"
           "data-session-history 回读含用户原文；切回该会话后 DOM 气泡含哨兵。"
           "（流式/一次性传输形态只作证据，不作判据）")
def s_chat_01_turn_roundtrip(env):
    sid = "e2e-chat-%s" % env.run_id
    mark = "E2E-CHAT-SENTINEL-%s" % env.run_id

    result = env.run_turn("请回显这句话：%s" % mark, session_id=sid, timeout=180)

    turns = [p for p in _payloads(env, "turn-start")
             if p.get("session") == sid and not p.get("parents")]
    env.check("主会话 turn-start 确权", len(turns) >= 1, turns[:2])
    env.check("llm-complete 终态 ok", (result.get("complete") or {}).get("status")
              in (None, "", "ok", "success", "completed", "complete"), result.get("complete"))

    tokens = []
    for p in _payloads(env, "llm-token") + _payloads(env, "llm-receive"):
        txt = p.get("text") or p.get("delta") or p.get("content") or ""
        if isinstance(txt, str) and txt:
            tokens.append(txt)
    # 传输形态（逐 token 流式 / 一次性 llm-receive）取决于 provider 配置；本测试点判据放在
    # **最终回复正文**与历史回读上，流式事件仅作证据（不把传输模式当功能判据）。
    env.note("流式/接收事件", "n=%d" % len(tokens))
    complete = result.get("complete") or {}
    final_text = complete.get("text") or ""
    env.check("llm-complete 携带回复正文", bool(final_text), repr(final_text)[:80])
    env.check("回复正文含用户哨兵（mock 回显）", mark in final_text, final_text[:160])
    stream_blob = json.dumps({"events": _payloads(env, "llm-token") + _payloads(env, "llm-receive"),
                              "complete": complete}, ensure_ascii=False)
    env.check("事件/终态合并文本含哨兵", mark in stream_blob, stream_blob[:200])

    hist = env.req("data-session-history",
                   {"session_id": sid, "target_messages": 50, "target_bytes": 262144}) or {}
    blob = json.dumps(hist, ensure_ascii=False)
    env.check("历史回读含用户哨兵", mark in blob, blob[:160])
    env.check("历史回读含 assistant 回复", "mock-reply" in blob, blob[:160])

    # DOM 落点：切到该会话 → 等历史应答 → user 气泡含哨兵
    _h.install_session_history_spy(env.client)
    env.emit("session-changed", {"session_id": sid})
    _h.wait_session_history(env.client, sid, timeout=25)
    text = env.wait(lambda: env.js(
        "(document.querySelector('.message-list')||document.body).innerText") or ""
        if mark in (env.js("(document.querySelector('.message-list')||document.body).innerText") or "")
        else None, "DOM 消息列表含哨兵", timeout=25)
    env.check("user 气泡渲染哨兵", mark in text, text[:160])
    n_user = int(env.js("document.querySelectorAll('.message-item.user').length") or 0)
    env.check("存在 user 气泡", n_user >= 1, "n_user=%d" % n_user)


@case("S-CHAT-02", "工具链路", "LLM 工具调用：file_read 执行 → tool-result 回填 → 轮次收尾",
      stage="seeded", order=80, fp="61 §4.3 llm-tool-call/tool-result · §5.1 tools/call",
      desc="提示词携带唯一文件路径 → mock LLM 决策调用 file_read → 断言工具调用事件、"
           "tool-result status=done 且结果含文件哨兵、轮次正常收尾。")
def s_chat_02_tool_chain(env):
    target = "README.md"
    ensure_demo_file(env, target)
    _h.wait_plugins_running(env.client, ("file",), max_wait=180)
    _h.wait_tools(env.client, ("file_read",), max_wait=120)

    sid = "e2e-tool-%s" % env.run_id
    q = "【T-E2E-READ】e2e-read e2epath=%s" % env.posix(env.path(target))
    env.run_turn(q, session_id=sid, timeout=240)

    calls = [p for p in _payloads(env, "llm-tool-call") if _canon(p.get("tool")) == "file_read"]
    env.check("捕获 llm-tool-call(file_read)", len(calls) >= 1,
              [p.get("tool") for p in _payloads(env, "llm-tool-call")])

    results = [p for p in _payloads(env, "tool-result")
               if _canon(p.get("tool")) == "file_read"]
    env.check("捕获 tool-result(file_read)", len(results) >= 1, results[:1])
    env.eq("tool-result status", (results[-1] or {}).get("status"), "done")
    blob = json.dumps(results[-1], ensure_ascii=False)
    env.check("工具结果含文件哨兵", SEED["README"] in blob, blob[:200])

    hist = env.req("data-session-history",
                   {"session_id": sid, "target_messages": 50, "target_bytes": 262144}) or {}
    hblob = json.dumps(hist, ensure_ascii=False)
    env.check("历史中保留工具调用记录", "file_read" in hblob, hblob[:160])
    env.note("工具链路证据", "calls=%d results=%d" % (len(calls), len(results)))
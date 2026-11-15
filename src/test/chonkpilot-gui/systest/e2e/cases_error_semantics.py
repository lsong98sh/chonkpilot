# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉝：**错误终态矩阵 + 工具中断徽标回环**
（迁移自 run_s21_matrix ①–④、run_sem_error_recovery E1–E3、run_sem_retry_state R1–R4）。

* 空回复（retryable=false）→ `.empty-reply-hint` 文案非空 + `.continue-btn`，**不**自动续写
  （本轮 llm-start 恰 1 条）；
* 5xx / 429 终态（retryable=true，retryCount=0）→ 唯一错误面 = 下方提示条，聊天流无
  `.error-bubble`，手动「继续」存在，且不动自动续写；
* complete 但无输出 → 空回复提示 + 继续（终态注入回归护栏）；
* 工具徽标语义：tool-call → `pending`「进行中」+ 卡片含工具名；`interrupted`「已中断」+ 重试按钮；
  点重试 → 中断徽标/按钮消失；`done`「完成」。
"""

import json
import time

from .framework import case
from .cases_chat_ops import (HANG_LLM, _add_hang, _open_session, _restore_llms,
                             _select, _send, _start_hang, _stop_hang)
from .cases_chat_ui import _last_user, _remote, _send_visible


def _badge(env, cls):
    return str(env.js("(()=>{const b=document.querySelector("
                      "'.message-item.tool_pair .status-badge.%s');"
                      "return b?b.textContent.trim():''})()" % cls) or "")


def _hint_text(env):
    return str(env.js("document.querySelector('.empty-reply-hint')"
                      "?.textContent?.trim() || ''") or "")


def _fail_present(env):
    return env.js("!!document.querySelector('.llm-fail-hint.llm-error-hint')") is True


@case("S-ERR-MATRIX-01", "会话消息", "错误终态矩阵：空回复/可重试/不可重试/complete 无输出 + 工具徽标回环",
      stage="seeded", order=312,
      fp="35 错误处理与恢复 · run_s21_matrix / run_sem_error_recovery / run_sem_retry_state",
      desc="空回复 → 提示+继续、不自动续写；retryable=true 终态 → 仅下方提示条（无 error-bubble）"
           "且手动继续、llm-start=1；complete 无输出 → 空回复提示继续；工具徽标 pending「进行中」→"
           "interrupted「已中断」+重试按钮 → 点击后中断态消失 → done「完成」。")
def s_err_matrix_01(env):
    srv, port = _start_hang()
    base = _add_hang(env, port)
    try:
        _select(env, HANG_LLM)

        # ── ① 空回复 ──
        sid1 = "e2e-s21-empty-%s" % env.run_id
        _open_session(env, sid1)
        env.capture(["turn-start", "llm-complete"])
        _send(env, sid1, "这条不会有回复", llm=HANG_LLM)
        env.wait(lambda: True if _last_user(env) == "这条不会有回复" else None,
                 "空回复 user 气泡", timeout=20)
        _remote(env, "llm-complete", {"session": sid1, "status": "error",
                                      "code": "EMPTY_REPLY", "retryable": False})
        hint = env.wait(lambda: _hint_text(env) or None, "空回复提示", timeout=20)
        env.check("① 空回复提示非空 + 继续按钮",
                  bool(hint) and env.js("!!document.querySelector('.continue-btn')") is True,
                  hint[:50])
        time.sleep(1.0)
        st_evs = [e.get("payload") or {} for e in env.events("turn-start")]
        starts = len([p for p in st_evs if (p.get("session") or p.get("session_id")) == sid1])
        env.check("① 不自动续写（本会话 turn-start=1）", starts == 1,
                  {"starts": starts, "payloads": st_evs[-3:]})

        # ── ② 可重试终态（5xx） ──
        sid2 = "e2e-s21-5xx-%s" % env.run_id
        _open_session(env, sid2)
        env.capture(["turn-start", "llm-complete"])
        _send(env, sid2, "触发 500 终态", llm=HANG_LLM)
        env.wait(lambda: True if _last_user(env) == "触发 500 终态" else None,
                 "5xx user 气泡", timeout=20)
        _remote(env, "llm-complete", {"session": sid2, "status": "error",
                                      "code": "PROVIDER_5XX", "retryable": True,
                                      "retry_count": 0, "retry_attempt": 0,
                                      "retry_wait_ms": 0,
                                      "message": "upstream 500 internal error"})
        env.wait(lambda: True if _fail_present(env) else None, "5xx 下方提示条", timeout=20)
        bar = str(env.js("document.querySelector('.llm-fail-hint.llm-error-hint .llm-fail-text')"
                         "?.textContent?.trim() || ''") or "")
        env.check("② 可重试终态 → 唯一错误面提示条 + 无 error-bubble + 继续",
                  bool(bar) and env.js("document.querySelectorAll("
                                       "'.message-item.assistant .error-bubble').length") == 0
                  and env.js("!!document.querySelector('.continue-btn')") is True, bar[:80])
        time.sleep(1.0)
        st_evs2 = [e.get("payload") or {} for e in env.events("turn-start")]
        starts2 = len([p for p in st_evs2 if (p.get("session") or p.get("session_id")) == sid2])
        env.check("② 终态不静默自动续写（turn-start=1）", starts2 == 1,
                  {"starts": starts2, "payloads": st_evs2[-3:]})

        # ── ③ complete 但无输出 ──
        sid3 = "e2e-s21-noout-%s" % env.run_id
        _open_session(env, sid3)
        _send(env, sid3, "complete 无输出", llm=HANG_LLM)
        env.wait(lambda: True if _last_user(env) == "complete 无输出" else None,
                 "no-output user 气泡", timeout=20)
        _remote(env, "llm-complete", {"session": sid3, "status": "complete"})
        h3 = env.wait(lambda: _hint_text(env) or None, "complete 无输出空态提示", timeout=20)
        env.check("③ complete 无输出 → 空回复提示 + 继续",
                  bool(h3) and env.js("!!document.querySelector('.continue-btn')") is True,
                  h3[:50])

        # ── ④ 工具徽标回环（pending → interrupted → 重试 → done） ──
        sid4 = "e2e-toolstate-%s" % env.run_id
        _open_session(env, sid4)
        _send(env, sid4, "工具状态回环", llm=HANG_LLM)
        env.wait(lambda: True if _last_user(env) == "工具状态回环" else None,
                 "toolstate user 气泡", timeout=20)
        _remote(env, "llm-receive",
                {"type": "tool-call", "session": sid4, "tool_call_id": "tc-state-1",
                 "tool": "file_read", "arguments": "{\"targets\":[{\"path\":\"a.txt\"}]}",
                 "purpose": "读取"})
        pend = env.wait(lambda: (lambda t: t if t else None)(_badge(env, "pending")),
                        "徽标 pending", timeout=20)
        card = str(env.js("document.querySelector('.message-item.tool_pair')"
                          "?.textContent || ''") or "")
        env.check("④1 tool-call → pending「进行中」+ 卡片含工具名",
                  pend == "进行中" and "file_read" in card, {"badge": pend, "card": card[:80]})

        _remote(env, "llm-complete", {"session": sid4, "status": "completed"})
        _remote(env, "tool-pair", {"tool_id": "tc-state-1", "status": "interrupted"})
        it = env.wait(lambda: (lambda t: t if t else None)(_badge(env, "interrupted")),
                      "徽标 interrupted", timeout=20)
        env.wait(lambda: True if env.js("!!document.querySelector('.retry-tool-btn')") is True
                 else None, "中断后重试按钮", timeout=15)
        env.check("④2 interrupted「已中断」+ 重试按钮",
                  it == "已中断" and env.js("!!document.querySelector('.retry-tool-btn')") is True,
                  it)

        env.js("document.querySelector('.retry-tool-btn')?.click(); 'ok'")
        env.wait(lambda: True if (env.js(
            "document.querySelectorAll('.message-item.tool_pair .status-badge.interrupted').length") or 0) == 0
            and (env.js("document.querySelectorAll('.retry-tool-btn').length") or 0) == 0
            else None, "点击重试后中断态归零", timeout=15)
        env.check("④3 点击重试 → 中断徽标/按钮消失（本地置回运行态）",
                  (env.js("document.querySelectorAll('.message-item.tool_pair "
                          ".status-badge.interrupted').length") or 0) == 0)

        _remote(env, "tool-pair", {"tool_id": "tc-state-1", "status": "done"})
        dn = env.wait(lambda: (lambda t: t if t else None)(_badge(env, "done")),
                      "徽标 done", timeout=20)
        env.check("④4 done「完成」回环闭合", dn == "完成", dn)
        env.wait(lambda: True if _send_visible(env) else None, "轮次结束 Send 恢复", timeout=15)
    finally:
        _restore_llms(env, base)
        _stop_hang(srv)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("错误矩阵组无前端错误", len(errs) == 0, errs[:3])
# -*- coding: utf-8 -*-
"""B · 语义/回环级推广（35-错误处理与恢复）：**错误终态 → 手动/自动恢复入口语义**。

覆盖（**拒绝"存在级"：断言终态分类驱动的手动入口**出现/抑制**（retryable 语义）**）：
  E1 空回复（S21 / ERR-005）：`llm-complete{status:error, code:EMPTY_REPLY, retryable:false}`
     → 出现**空回复提示**（`.empty-reply-hint` 文案非空）+「继续」按钮（`.continue-btn`）
     —— 空回复虽 retryable=false 仍给手动继续入口（对齐 §5.2 口径）。
  E2 不可重试错误（S16 / ERR-001-S01，如鉴权）：`llm-complete{status:error, code:AUTH_FAILED,
     retryable:false}` → 出现**手动「继续」按钮**（不可自动重试 → 只显示按钮等用户，§5.2）。
  E3 可自动重试错误（S15/S17，如 5xx/429）：`llm-complete{status:error, code:TIMEOUT,
     retryable:true}` → **不**立即出现手动「继续」按钮（后端/前端走自动续写支路，不给重复手动入口）
     —— 以 `retryable` 字段区分手动/自动恢复（§5.2 · `onLlmComplete`）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard(c)`。会话数据落临时库，结束即弃。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  §4.3 `llm-complete`（错误终态，含可选 `retryable`/`code`）· `session-changed`（本地）· DOM。

运行：python run_sem_error_recovery.py
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

WS = _h.tmp_dir("ck-semerr-ws-")
DD = _h.tmp_dir("ck-semerr-dd-")
HOME = _h.tmp_home()
SID_EMPTY, SID_AUTH, SID_TIMEOUT = "sem-err-empty", "sem-err-auth", "sem-err-timeout"

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
_h.ensure_locale(c, "zh-CN")
_h.install_session_history_spy(c)
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


def continue_count():
    return int(J("document.querySelectorAll('.continue-btn').length") or 0)


def open_session(sid):
    """切到独立会话（重置上一轮空回复/继续态），等历史回填 + 会话身份生效。"""
    c.mq_emit("session-changed", {"session_id": sid})
    _h.wait_session_history(c, sid)
    wait_upto("(()=>{const t=document.querySelector('.chat-session-tag');return t?t.getAttribute('title'):null})()",
              lambda v: v == sid)


def case_e1_empty_reply():
    """E1：空回复 → 提示 + 手动「继续」按钮（ERR-005 / S21）。"""
    open_session(SID_EMPTY)
    emit_remote("llm-complete", {"session": SID_EMPTY, "status": "error",
                                 "code": "EMPTY_REPLY", "message": "", "retryable": False})
    hint = wait_upto("(()=>{const e=document.querySelector('.empty-reply-hint');"
                     "return e?e.textContent.trim():''})()", lambda v: bool(v))
    if not hint:
        raise TestError("空回复未出现 .empty-reply-hint 提示")
    n = wait_upto("document.querySelectorAll('.continue-btn').length",
                  lambda v: isinstance(v, int) and v >= 1, max_wait=8.0)
    if not (isinstance(n, int) and n >= 1):
        raise TestError("空回复未出现手动「继续」按钮（count=%r）" % (n,))
    print("[EVIDENCE] " + json.dumps({"case": "E1 空回复提示+继续", "hint": hint,
                                      "continue": n}, ensure_ascii=False), flush=True)


def case_e2_nonretryable_manual():
    """E2：不可重试错误 → 手动「继续」按钮（ERR-001-S01，retryable=false）。"""
    open_session(SID_AUTH)
    emit_remote("llm-complete", {"session": SID_AUTH, "status": "error",
                                 "code": "AUTH_FAILED", "message": "401 unauthorized",
                                 "retryable": False})
    n = wait_upto("document.querySelectorAll('.continue-btn').length",
                  lambda v: isinstance(v, int) and v >= 1, max_wait=8.0)
    if not (isinstance(n, int) and n >= 1):
        raise TestError("不可重试错误未出现手动「继续」按钮（count=%r）" % (n,))
    print("[EVIDENCE] " + json.dumps({"case": "E2 不可重试→手动入口", "continue": n},
                                     ensure_ascii=False), flush=True)


def case_e3_retryable_auto():
    """E3：可自动重试错误 → 不立即给手动「继续」（走自动续写，retryable=true）。"""
    open_session(SID_TIMEOUT)
    emit_remote("llm-complete", {"session": SID_TIMEOUT, "status": "error",
                                 "code": "TIMEOUT", "message": "stream idle timeout",
                                 "retryable": True})
    time.sleep(0.5)  # 自动支路（400ms 起自动续写）已接管 → 手动入口保持抑制
    n = continue_count()
    if n != 0:
        raise TestError("可自动重试错误不应立即出现手动「继续」按钮（count=%r）" % (n,))
    print("[EVIDENCE] " + json.dumps({"case": "E3 可重试→抑制手动入口", "continue": n},
                                     ensure_ascii=False), flush=True)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = llm-complete（错误终态，含 retryable/code，既有事件）", flush=True)
    ok = total = 0
    for name, fn in [
        ("E1 空回复：提示 + 手动「继续」", case_e1_empty_reply),
        ("E2 不可重试错误：手动「继续」入口", case_e2_nonretryable_manual),
        ("E3 可自动重试：抑制手动入口（自动续写支路）", case_e3_retryable_auto),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error" and "SetActiveSessionID" not in (e.get("text") or ""):
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n错误终态恢复入口语义：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

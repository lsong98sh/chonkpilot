# -*- coding: utf-8 -*-
"""B · 语义/回环级推广 ③：**会话工具中断 / 重试态**（状态徽标语义 + 重试事件契约 + 状态回环）。

覆盖（**拒绝"存在级"：断言状态徽标文本、事件载荷契约与状态迁移**）：
  R1 运行态语义：发送 → `llm-receive{tool-call}` 渲染 tool_pair，**徽标文本 ==「进行中」**
     且卡片文本含工具名（语义：身份 + 状态，非仅"元素存在"）。
  R2 中断态语义：`llm-complete` → `tool-pair{status:interrupted}` → 徽标文本 ==「已中断」
     且底部出现「重试」按钮（`.retry-tool-btn`）。
  R3 重试回环：点击「重试」→ ① 事件 `tool-retry` 载荷 **{session, turn} 契约**（spy 捕获）；
     ② 列表本地把该 pair 置回运行态（中断徽标消失 + 重试按钮消失）。
  R4 完成回环：`tool-pair{status:done}` → 徽标文本 ==「完成」（中断→重试→完成 回环闭合）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard(c)`。会话数据落临时库，结束即弃。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  `message-send`/`session-changed`（本地）· `llm-receive`/`llm-complete`/`tool-pair`（§4.3）·
  `tool-retry`（§4.2）· DOM（--test-port /eval）。

运行：python run_sem_retry_state.py
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

WS = _h.tmp_dir("ck-semretry-ws-")
DD = _h.tmp_dir("ck-semretry-dd-")
HOME = _h.tmp_home()
SID = "sem-retry-1"
TOOL = "read_file"
CALL_ID = "tc-sem-1"
PENDING, INTERRUPTED, DONE = "进行中", "已中断", "完成"

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


def wait_session_active(sid):
    return wait_upto("(()=>{const t=document.querySelector('.chat-session-tag');return t?t.getAttribute('title'):null})()",
                     lambda v: v == sid, max_wait=15.0)


def pair_text():
    return J("(()=>{const p=document.querySelector('.message-item.tool_pair');return p?p.textContent.trim():''})()")


def badge_text(cls):
    return J("(()=>{const b=document.querySelector('.message-item.tool_pair .status-badge.%s');"
             "return b?b.textContent.trim():''})()" % cls)


def retry_count():
    return int(J("document.querySelectorAll('.retry-tool-btn').length") or 0)


def setup_session():
    _h.install_session_history_spy(c)
    c.mq_emit("session-changed", {"session_id": SID})
    _h.wait_session_history(c, SID)
    wait_session_active(SID)


def case_r1_running_state():
    """R1：tool-call → tool_pair 徽标「进行中」+ 卡片含工具名。"""
    setup_session()
    c.mq_emit("message-send", {"sessionId": SID, "text": "读取文件并分析"})
    time.sleep(0.4)
    emit_remote("llm-receive", {"type": "tool-call", "session": SID,
                                "tool_call_id": CALL_ID, "tool": TOOL,
                                "arguments": '{"path": "a.txt"}'})
    b = wait_upto("(()=>{const b=document.querySelector('.message-item.tool_pair .status-badge.pending');"
                  "return b?b.textContent.trim():''})()", lambda v: v == PENDING)
    if b != PENDING:
        raise TestError("运行态徽标=%r（期望 %r）" % (b, PENDING))
    txt = pair_text()
    if TOOL not in txt:
        raise TestError("tool_pair 卡片未含工具名 %r：%r" % (TOOL, txt[:120]))
    print("[EVIDENCE] " + json.dumps({"case": "R1 运行态", "badge": b, "has_tool": TOOL in txt},
                                     ensure_ascii=False), flush=True)


def case_r2_interrupted_state():
    """R2：轮次结束 + tool-pair interrupted → 徽标「已中断」+ 重试按钮。"""
    emit_remote("llm-complete", {"session": SID, "status": "completed"})
    time.sleep(0.4)
    emit_remote("tool-pair", {"tool_id": CALL_ID, "status": "interrupted"})
    b = wait_upto("(()=>{const b=document.querySelector('.message-item.tool_pair .status-badge.interrupted');"
                  "return b?b.textContent.trim():''})()", lambda v: v == INTERRUPTED)
    if b != INTERRUPTED:
        raise TestError("中断态徽标=%r（期望 %r）" % (b, INTERRUPTED))
    n = wait_upto("document.querySelectorAll('.retry-tool-btn').length", lambda v: isinstance(v, int) and v >= 1,
                  max_wait=8.0)
    if not (isinstance(n, int) and n >= 1):
        diag = J("(()=>({turn_actions:!!document.querySelector('.turn-actions'),"
                 "continue:!!document.querySelector('.continue-btn'),"
                 "cancel:!!document.querySelector('.input-actions-right button')}))()")
        raise TestError("中断后未出现重试按钮（.retry-tool-btn，count=%r；diag=%r）" % (n, diag))
    print("[EVIDENCE] " + json.dumps({"case": "R2 中断态", "badge": b, "retry_btn": True},
                                     ensure_ascii=False), flush=True)


def case_r3_retry_roundtrip():
    """R3：点击重试 → tool-retry{session,turn} 契约 + 本地置回运行态（中断徽标/按钮消失）。"""
    J("window.__retrySpy=[];window.mq.post('tool-retry', d=>window.__retrySpy.push(d));'ok'")
    J("document.querySelector('.retry-tool-btn')?.click(); 'ok'")
    spy = wait_upto("JSON.stringify(window.__retrySpy)", lambda v: isinstance(v, list) and len(v) >= 1) or []
    if not (isinstance(spy, list) and len(spy) == 1
            and spy[0].get("session") == SID
            and isinstance(spy[0].get("turn"), str)
            and set(spy[0].keys()) <= {"session", "turn", "instance_id"}):
        raise TestError("tool-retry 载荷不符契约：%r" % (spy,))
    # 本地回环：状态置回运行态 → 中断徽标与重试按钮消失
    gone = wait_upto("(()=>{return document.querySelectorAll('.retry-tool-btn').length===0 "
                     "&& document.querySelectorAll('.message-item.tool_pair .status-badge.interrupted').length===0})()",
                     lambda v: v is True)
    if gone is not True:
        raise TestError("重试后未置回运行态（retry 按钮/中断徽标未消失）")
    print("[EVIDENCE] " + json.dumps({"case": "R3 重试回环", "payload": spy[0], "back_to_running": True},
                                     ensure_ascii=False), flush=True)


def case_r4_done_roundtrip():
    """R4：tool-pair done → 徽标「完成」（回环闭合）。"""
    emit_remote("tool-pair", {"tool_id": CALL_ID, "status": "done"})
    b = wait_upto("(()=>{const b=document.querySelector('.message-item.tool_pair .status-badge.done');"
                  "return b?b.textContent.trim():''})()", lambda v: v == DONE)
    if b != DONE:
        raise TestError("完成态徽标=%r（期望 %r）" % (b, DONE))
    if retry_count() != 0:
        raise TestError("完成态不应再有重试按钮")
    print("[EVIDENCE] " + json.dumps({"case": "R4 完成回环", "badge": b}, ensure_ascii=False), flush=True)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = message-send/llm-receive/llm-complete/tool-pair/tool-retry（既有事件）", flush=True)
    ok = total = 0
    for name, fn in [
        ("R1 运行态：tool_pair 徽标「进行中」+ 含工具名", case_r1_running_state),
        ("R2 中断态：徽标「已中断」+ 重试按钮出现", case_r2_interrupted_state),
        ("R3 重试回环：tool-retry{session,turn} 契约 + 置回运行态", case_r3_retry_roundtrip),
        ("R4 完成回环：tool_pair done → 徽标「完成」", case_r4_done_roundtrip),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error" and "SetActiveSessionID" not in (e.get("text") or ""):
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n会话工具中断/重试态语义回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

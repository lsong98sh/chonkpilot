# -*- coding: utf-8 -*-
"""B · 语义/回环级试点 ③：会话/消息列表 —— 消息渲染出**可辨识内容片段**（非仅容器存在）+ 历史回环。

覆盖（**拒绝"存在级"：断言真实文本片段与渲染结构**）：
  M1 user 消息：发送 → `.message-item.user .message-content` **文本 == 唯一标记串**（可辨识内容）。
  M2 assistant 消息：`llm-receive(text)` → `.message-item.assistant .message-content` **含唯一标记串**
     且 markdown 渲染出 `<strong>`+`<code>`（语义级：内容 + 结构）。
  M3 **回环**：轮次结束 → 切走会话 → 切回 → 经 `data-session-history` 回填后，user 气泡文本
     仍 **== 同一标记串**（经受 persist 的历史往返）。

复用（避免与既有套件口径漂移）：`test_chat_flow.py` 的 `J/emit_remote/send_msg/complete_turn/
wait_upto/wait_session_active` + `harness.install_session_history_spy/wait_session_history`。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard(c)`。会话数据落临时库，结束即弃。

运行：python run_sem_messages.py
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402
import test_chat_flow as cf  # noqa: E402  复用其消息驱动/等待助手（J/emit_remote/send_msg/...）

WS = _h.tmp_dir("ck-semmsg-ws-")
DD = _h.tmp_dir("ck-semmsg-dd-")
HOME = _h.tmp_home()
_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
gui = c  # cf.* 助手消费带 .eval 的驱动客户端（ChonkClient）
_h.suite_config_guard(c)
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）" % (WS, DD, HOME, _g.port), flush=True)

SID = "sem-msg-1"
OTHER = "sem-msg-other"
USER_MARK = "SEMMSG-alpha-9c1"
ASST_MARK = "SEMMSG-beta-9c2"
USER_TEXT = USER_MARK + " 请给我一个排序函数"

LAST_USER_JS = ("(()=>{const els=Array.from(document.querySelectorAll('.message-item.user .message-content'));"
                "const last=els[els.length-1];return last?last.textContent.trim():''})()")
LAST_ASST_JS = ("(()=>{const els=Array.from(document.querySelectorAll('.message-item.assistant .message-content'));"
                "const last=els[els.length-1];if(!last)return {text:'',strong:0,code:0};"
                "return {text:last.textContent.trim(),strong:last.querySelectorAll('strong').length,"
                "code:last.querySelectorAll('code').length};})()")


def activate(sid):
    cf.J(gui, "window.mq.emit('session-changed', {session_id: %s}); 'ok'" % json.dumps(sid))


def case_m1_user_renders_identifiable():
    """M1：user 消息渲染出**可辨识文本片段**（精确匹配唯一标记串）。"""
    _h.install_session_history_spy(gui)
    activate(SID)
    cf.wait_session_history(gui, SID)
    cf.wait_session_active(gui, SID)
    cf.send_msg(gui, SID, USER_TEXT)
    v = cf.wait_upto(gui, LAST_USER_JS, lambda x: x == USER_TEXT, max_wait=15.0)
    if v != USER_TEXT:
        raise TestError("user 气泡文本不可辨识：%r（期望 %r）" % (v, USER_TEXT))
    print("[EVIDENCE] " + json.dumps({"case": "M1 user 文本片段", "text": v}, ensure_ascii=False), flush=True)


def case_m2_assistant_semantic():
    """M2：assistant 消息渲染出唯一片段 + markdown 结构（strong/code）。"""
    cf.emit_remote(gui, "llm-receive",
                   {"type": "text", "text": "回复 **%s**，示例 `quickSort(arr)`。" % ASST_MARK,
                    "session": SID})
    v = cf.wait_upto(gui, LAST_ASST_JS,
                     lambda x: isinstance(x, dict) and ASST_MARK in (x.get("text") or "")
                     and x.get("strong", 0) >= 1 and x.get("code", 0) >= 1, max_wait=10.0)
    if not (isinstance(v, dict) and ASST_MARK in (v.get("text") or "")
            and v.get("strong", 0) >= 1 and v.get("code", 0) >= 1):
        raise TestError("assistant 语义渲染不达标：%r" % (v,))
    print("[EVIDENCE] " + json.dumps({"case": "M2 assistant 片段+markdown", **v}, ensure_ascii=False),
          flush=True)


ALL_USER_JS = ("JSON.stringify([...document.querySelectorAll('.message-item.user .message-content')]"
               ".map(e=>e.textContent.trim()))")


def case_m3_history_roundtrip():
    """M3：轮次结束 → 切走 → 切回 → 用户的**可辨识片段**经受历史往返仍在（回环）。

    断言 = 回填后的 user 气泡集合**精确包含** M1 的原文（片段级，非"容器存在"）；
    不以"最后一条"为准——空回复路径可能追加 user-notify 气泡（如「继续」）。
    """
    cf.complete_turn(gui, SID)                 # 结束轮次（否则切换时忙碌态干扰）
    activate(OTHER)                           # 切走
    cf.wait_session_history(gui, OTHER)
    time.sleep(0.3)
    activate(SID)                             # 切回
    cf.wait_session_history(gui, SID)
    texts = cf.wait_upto(gui, ALL_USER_JS,
                         lambda x: isinstance(x, list) and USER_TEXT in x, max_wait=15.0) or []
    if USER_TEXT not in texts:
        raise TestError("回环后 user 片段丢失：%r（期望含 %r）" % (texts, USER_TEXT))
    print("[EVIDENCE] " + json.dumps({"case": "M3 历史回环含片段", "texts": texts}, ensure_ascii=False),
          flush=True)


def main():
    _g.client.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = session-changed/message-send/llm-receive/llm-complete（既有事件）", flush=True)
    ok = total = 0
    for name, fn in [
        ("M1 user 消息渲染可辨识内容片段", case_m1_user_renders_identifiable),
        ("M2 assistant 消息渲染片段 + markdown 结构", case_m2_assistant_semantic),
        ("M3 切走→切回：user 气泡经受历史往返一致（回环）", case_m3_history_roundtrip),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (_g.client.console() or {}).get("entries", []):
        if e.get("level") == "error" and "SetActiveSessionID" not in (e.get("text") or ""):
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n消息列表语义/回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

# -*- coding: utf-8 -*-
"""Ask 中心（独立待办窗口）回归：事件驱动（mq 注入 ask-user，无需 LLM）。

2026-11-04 起 ask_user **不再默认弹 dialog**：归属窗口收到 `ask-user` → `gui.window.open-ask`
→ 自动打开本项目唯一的 `#ask` 待办窗口（左列待办一览 / 右侧问题与选项，可回答或聊一聊）。
本套件按这一新形态重写（原 dialog 形态的 C1-C16 语义映射见每个 case 的 docstring）。

覆盖：
  A1/A2 自动打开 + 左列待办 + 右侧详情（问题/选项/推荐）+ 单选回答回链
        （answer=选项 + dialog_id 原样回带；A2 与 A1 共用同一条注入/提交流程，不另起 case）
  A3 自定义输入回答（options 空 → textarea）
  A4 多选：勾选多个 → 顿号连接
  A5 多待办聚合：左列全部列出，逐条作答后从列表移除
  A6 子会话提问：左列显示会话标识
  A7 超时（expires_at 到期）→ 列表「已超时」标记，仍可作答
  A8 跳过：不回答、留在列表并移到队尾
  A9 取消：以「用户选择了其他…」文案作答 → 移出列表
  A10 无 expires_at 时短时间不转 busy
  A11 选项 + 「其他」自定义文本可并存
  A12 选项 {value,label} 对象：渲染 label / 回链 value
  A13 聊一聊：切/开对话窗口 + defer 回填 + 问题引用注入输入框（不发送）
  A14 聊一聊边界：无会话 → 可见提示且待办保留
  A15 关窗/重开：待办不丢（宿主 gui.ask.list 回填），窗口仍为单例

前置：chonkpilot.exe --test-port=2345（harness 自起/复用）。
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h
import ask_center as ac_mod

_G = _h.acquire_gui(2345, work_dir=_h.tmp_dir("ck-askui-ws-"))
c = _G.client
_h.suite_config_guard(c)

SEQ = [0]


def ask_id():
    SEQ[0] += 1
    return f"ask-{SEQ[0]:03d}"


def wait_until(fn, timeout=15.0, interval=0.25):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            v = fn()
            if v:
                return v
        except Exception:
            pass
        time.sleep(interval)
    return None


def close_chat_windows():
    """关闭本套件为「聊一聊」打开的对话窗口（不影响 Ask 中心）。"""
    try:
        lst = (c.req("gui.window.list", {}) or {}).get("windows") or []
    except Exception:
        return
    for w in lst:
        wid = w.get("window_id") or ""
        if not wid:
            continue
        try:
            ChonkClient(base=c.base, timeout=20, window_id=wid).publish("gui.window.status", {"command": "close"})
        except Exception:
            pass
    time.sleep(0.5)


def cleanup_all():
    """摘除全部宿主待办（测试隔离）+ 关闭 Ask 中心与对话窗口。"""
    try:
        asks = (c.req("gui.ask.list", {}) or {}).get("asks") or []
        for a in asks:
            aid = ((a.get("ask") or a) if isinstance(a, dict) else {}).get("ask_id") or ""
            if aid:
                c.req("gui.ask.remove", {"ask_id": aid})
    except Exception:
        pass
    wid = ac_mod.ask_window_id(c, 2)
    if wid:
        try:
            ac_mod.close_center(ChonkClient(base=c.base, timeout=20, window_id=wid), c, 5)
        except Exception:
            pass
    close_chat_windows()


def inject(payload, timeout=30.0):
    """注入 ask 并等窗口 + 左列待办就绪；返回 (ask_client, ask_id)。"""
    aid = payload.get("ask_id") or ask_id()
    payload = dict(payload, ask_id=aid)
    _wid, ac = ac_mod.inject(c, payload, timeout)
    if not ac_mod.has_item(ac, aid):
        raise TestError(f"Ask 中心左列未出现待办 {aid}")
    ac.mq_on_capture(["ask-user-reply"])
    ac_mod.select_item(ac, aid)
    time.sleep(0.2)
    return ac, aid


def case_auto_open_and_detail():
    """A1：首次 ask 自动打开 Ask 中心；左侧一览 + 右侧问题/选项/推荐。"""
    ac, aid = inject({
        "question": "选择操作：", "options": ["构建", "测试", "部署"], "recommended": ["测试"],
    })
    if ac_mod.item_count(ac) < 1:
        raise TestError("左列待办数为 0")
    if ac_mod.question_text(ac) != "选择操作：":
        raise TestError(f"右侧问题异常: {ac_mod.question_text(ac)!r}")
    labels = ac_mod.option_labels(ac)
    if labels != ["构建", "测试", "部署"]:
        raise TestError(f"右侧选项异常: {labels!r}")
    if ac_mod.recommended_count(ac) != 1:
        raise TestError("推荐 badge 未渲染")
    ac_mod.click_option(ac, "构建")
    time.sleep(0.2)
    ac_mod.click_submit(ac)
    p = ac_mod.wait_reply(ac, aid)
    if not p or p.get("answer") != "构建":
        raise TestError(f"ask-user-reply 异常: {p!r}")
    if p.get("dialog_id") != "dlg-askcenter-fixture":
        raise TestError(f"dialog_id 未原样回带: {p.get('dialog_id')!r}")
    ac_mod.wait_no_item(ac, aid)


def case_custom():
    """A3：options 空 → 自定义 textarea；提交回链自定义文本。"""
    ac, aid = inject({"question": "请描述你的需求："})
    if ac_mod.set_custom(ac, "需要一份发布计划") != "ok":
        raise TestError("自定义 textarea 未出现")
    time.sleep(0.2)
    ac_mod.click_submit(ac)
    p = ac_mod.wait_reply(ac, aid)
    if not p or p.get("answer") != "需要一份发布计划":
        raise TestError(f"自定义回答异常: {p!r}")
    ac_mod.wait_no_item(ac, aid)


def case_multi():
    """A4：multi=true 勾选多个选项 → answer 以「、」拼接。"""
    ac, aid = inject({
        "question": "选择多个：", "options": ["构建", "测试", "部署"], "multi": True,
    })
    ac_mod.click_option(ac, "构建")
    time.sleep(0.2)
    ac_mod.click_option(ac, "测试")
    time.sleep(0.4)
    ac_mod.click_submit(ac)
    p = ac_mod.wait_reply(ac, aid)
    if not p or p.get("answer") != "构建、测试":
        raise TestError(f"多选回答异常: {p!r}")
    ac_mod.wait_no_item(ac, aid)


def case_aggregate():
    """A5：多待办聚合（替代原「同时只弹一个」）：左列全部列出，逐条作答后移除。"""
    ids = [ask_id() for _ in range(3)]
    for i, aid in enumerate(ids):
        ac_mod.emit_ask(c, {"ask_id": aid, "question": f"聚合问题{i + 1}", "options": [f"O{i}a", f"O{i}b"]})
    _wid, ac = ac_mod.ask_client(c, 30)
    for aid in ids:
        if not ac_mod.wait_item(ac, aid):
            raise TestError(f"待办 {aid} 未入左列")
    if ac_mod.item_count(ac) < 3:
        raise TestError(f"左列待办数应 >=3，实际 {ac_mod.item_count(ac)}")
    ac.mq_on_capture(["ask-user-reply"])
    for i, aid in enumerate(ids):
        ac_mod.select_item(ac, aid)
        time.sleep(0.2)
        if ac_mod.question_text(ac) != f"聚合问题{i + 1}":
            raise TestError(f"选中 {aid} 右侧问题异常: {ac_mod.question_text(ac)!r}")
        ac_mod.click_option(ac, f"O{i}a")
        time.sleep(0.2)
        ac_mod.click_submit(ac)
        p = ac_mod.wait_reply(ac, aid)
        if not p or p.get("answer") != f"O{i}a":
            raise TestError(f"聚合作答异常: {p!r}")
        ac_mod.wait_no_item(ac, aid)


def case_subsession():
    """A6：子会话提问 → 左列显示子会话标识（#前 8 位）。"""
    sub = "subsession-abcdef0123456789"
    ac, aid = inject({"question": "任务内提问", "options": ["Y", "N"], "sub_session_id": sub})
    body = ac.eval("document.querySelector('.ask-list')?.innerText || ''")
    if "subsessi" not in str(body):
        raise TestError(f"左列未显示子会话标识: {body!r}")
    ac_mod.click_option(ac, "Y")
    time.sleep(0.2)
    ac_mod.click_submit(ac)
    p = ac_mod.wait_reply(ac, aid)
    if not p or p.get("answer") != "Y":
        raise TestError(f"子会话作答异常: {p!r}")
    ac_mod.wait_no_item(ac, aid)


def case_expired():
    """A7：expires_at 到期 → 列表「已超时」标记；仍可作答（server 会转新轮）。"""
    now_ms = int(time.time() * 1000)
    ac, aid = inject({"question": "超时问题", "options": ["X1", "X2"], "expires_at": now_ms + 800})
    ok = wait_until(lambda: ac.eval(
        "document.querySelector(%s)?.classList.contains('expired') || false"
        % json.dumps(f'[data-ask-id="{aid}"]'), 5000), timeout=8)
    if not ok:
        raise TestError("到期后待办未标记已超时")
    ac_mod.click_option(ac, "X2")
    time.sleep(0.2)
    ac_mod.click_submit(ac)
    p = ac_mod.wait_reply(ac, aid)
    if not p or p.get("answer") != "X2":
        raise TestError(f"超时后作答异常: {p!r}")
    ac_mod.wait_no_item(ac, aid)


def case_skip():
    """A8：跳过 = 不回答、不移除（移到队尾）；后续仍可正常作答。"""
    a1 = ask_id()
    a2 = ask_id()
    ac_mod.emit_ask(c, {"ask_id": a1, "question": "先跳过", "options": ["A1", "A2"]})
    _wid, ac = ac_mod.ask_client(c, 30)
    ac_mod.wait_item(ac, a1)
    ac_mod.select_item(ac, a1)
    time.sleep(0.2)
    ac.mq_on_capture(["ask-user-reply"])
    ac_mod.click_skip(ac)
    time.sleep(0.5)
    if not ac_mod.has_item(ac, a1):
        raise TestError("跳过后待办不应移除")
    evs = ac.events_of("ask-user-reply", clear=True)
    if any((e.get("payload") or {}).get("ask_id") == a1 for e in evs):
        raise TestError("跳过后不应发送 ask-user-reply")
    # 新 ask 到达后，跳过项仍在左列
    ac_mod.emit_ask(c, {"ask_id": a2, "question": "新问题", "options": ["B1", "B2"]})
    ac_mod.wait_item(ac, a2)
    if not ac_mod.has_item(ac, a1):
        raise TestError("新 ask 到达后跳过项丢失")
    # 作答清理
    for aid, label in ((a2, "B1"), (a1, "A2")):
        ac_mod.select_item(ac, aid)
        time.sleep(0.2)
        ac_mod.click_option(ac, label)
        time.sleep(0.2)
        ac_mod.click_submit(ac)
        ac_mod.wait_reply(ac, aid)
        ac_mod.wait_no_item(ac, aid)


def case_cancel():
    """A9：取消 → 以「用户选择了其他，可能需要进一步的信息才能确定」文案作答，并从列表移除。"""
    ac, aid = inject({"question": "取消问题", "options": ["C1", "C2"]})
    ac_mod.click_cancel(ac)
    p = ac_mod.wait_reply(ac, aid)
    if not p:
        raise TestError("取消未发送 ask-user-reply")
    if "用户选择了其他" not in str(p.get("answer") or ""):
        raise TestError(f"取消文案异常: {p.get('answer')!r}")
    ac_mod.wait_no_item(ac, aid)


def case_no_deadline():
    """A10：无 expires_at 时短时间不转 busy（前端默认 5 分钟）。"""
    ac, aid = inject({"question": "无期限问题", "options": ["D1", "D2"]})
    time.sleep(1.6)
    if ac_mod.busy_visible(ac):
        raise TestError("无 expires_at 时短时间不应显示超时提示")
    ac_mod.click_option(ac, "D1")
    time.sleep(0.2)
    ac_mod.click_submit(ac)
    ac_mod.wait_reply(ac, aid)
    ac_mod.wait_no_item(ac, aid)


def case_custom_with_options():
    """A11：选项 + 「其他」自定义文本可并存（答案以「、」拼接）。"""
    ac, aid = inject({
        "question": "选项或自定义：", "options": ["E1", "E2"], "custom": True,
    })
    ac_mod.click_option(ac, "E1")
    time.sleep(0.2)
    if ac_mod.set_custom(ac, "其它想法") != "ok":
        raise TestError("custom textarea 未出现")
    time.sleep(0.2)
    ac_mod.click_submit(ac)
    p = ac_mod.wait_reply(ac, aid)
    if not p or p.get("answer") != "E1、其它想法":
        raise TestError(f"选项+自定义回答异常: {p!r}")
    ac_mod.wait_no_item(ac, aid)


def case_option_objects():
    """A12：选项对象 {value,label} → 渲染 label、回链 value。"""
    ac, aid = inject({
        "question": "对象选项：",
        "options": [{"value": "v-build", "label": "构建"}, {"value": "v-test", "label": "测试"}],
    })
    labels = ac_mod.option_labels(ac)
    if labels != ["构建", "测试"]:
        raise TestError(f"对象选项 label 渲染异常: {labels!r}")
    ac_mod.click_option(ac, "构建")
    time.sleep(0.2)
    ac_mod.click_submit(ac)
    p = ac_mod.wait_reply(ac, aid)
    if not p or p.get("answer") != "v-build":
        raise TestError(f"对象选项应回链 value，实际: {p!r}")
    ac_mod.wait_no_item(ac, aid)


def case_chat():
    """A13：聊一聊 → defer 回填 + 打开源会话对话窗口 + 问题引用注入输入框（不发送）。"""
    s = "s-askchat"
    r = c.req("data-session-ensure-session", {"data": {"session_id": s}}) or {}
    dlg = r.get("dialog_id") or ""
    if not dlg:
        raise TestError("ensure-session 未回带 dialog_id")
    ac, aid = inject({"question": "需要讨论的问题", "options": ["F1", "F2"], "session": s, "dialog_id": dlg})
    ac_mod.click_chat(ac)
    p = ac_mod.wait_reply(ac, aid)
    if not p:
        raise TestError("聊一聊未发送 defer 回填")
    if p.get("dialog_id") != dlg:
        raise TestError(f"defer 回填 dialog_id 不一致: {p.get('dialog_id')!r}")
    if "不直接作答" not in str(p.get("answer") or ""):
        raise TestError(f"defer 文案异常: {p.get('answer')!r}")
    ac_mod.wait_no_item(ac, aid)

    def chat_wid():
        lst = (c.req("gui.window.list", {}) or {}).get("windows") or []
        for w in lst:
            if w.get("session_id") == s:
                return w.get("window_id") or ""
        return ""

    wid = wait_until(chat_wid, 20)
    if not wid:
        raise TestError("聊一聊未打开源会话对话窗口")
    cc = ChonkClient(base=c.base, timeout=30, window_id=wid)
    draft = wait_until(lambda: ac_mod._unwrap(cc.eval(
        "document.querySelector('[contenteditable=\"true\"]')?.innerText || ''", 8000)) or '', 20)
    if not draft or "需要讨论的问题" not in str(draft):
        raise TestError(f"问题引用未注入输入框: {draft!r}")
    close_chat_windows()


def case_chat_missing_session():
    """A14：聊一聊边界：提问缺会话 → 可见提示且待办保留、不误提交。"""
    ac, aid = inject({"question": "无会话提问", "options": ["G1", "G2"]})
    ac_mod.click_chat(ac)
    time.sleep(1.0)
    if not ac_mod.has_item(ac, aid):
        raise TestError("无会话时待办不应被移除")
    evs = ac.events_of("ask-user-reply", clear=True)
    if any((e.get("payload") or {}).get("ask_id") == aid for e in evs):
        raise TestError("无会话时不应提交 defer 回答")
    ac_mod.select_item(ac, aid)
    ac_mod.click_option(ac, "G1")
    time.sleep(0.2)
    ac_mod.click_submit(ac)
    ac_mod.wait_reply(ac, aid)
    ac_mod.wait_no_item(ac, aid)


def case_reopen():
    """A15：关窗/重开：待办不丢（宿主 gui.ask.list 回填）；窗口单例不重复。"""
    aid = ask_id()
    ac_mod.emit_ask(c, {"ask_id": aid, "question": "重开保留", "options": ["H1", "H2"]})
    wid, ac = ac_mod.ask_client(c, 30)
    ac_mod.wait_item(ac, aid)
    ac_mod.close_center(ac, c, 10)
    if ac_mod.ask_window_id(c, 5):
        raise TestError("Ask 中心关窗后 ask_window_id 应清空")
    # 新 ask 到达 → 重新自动打开，旧待办仍在（宿主内存表）
    aid2 = ask_id()
    ac_mod.emit_ask(c, {"ask_id": aid2, "question": "重开后新问题", "options": ["I1", "I2"]})
    wid2, ac2 = ac_mod.ask_client(c, 30)
    if wid2 == wid:
        raise TestError("重开应分配新 window_id（旧窗口已关）")
    if not ac_mod.wait_item(ac2, aid) or not ac_mod.wait_item(ac2, aid2):
        raise TestError("重开后旧待办未回填")
    if ac_mod.item_count(ac2) < 2:
        raise TestError(f"重开后待办数应 >=2，实际 {ac_mod.item_count(ac2)}")
    # 清理
    ac2.mq_on_capture(["ask-user-reply"])
    for x, label in ((aid, "H1"), (aid2, "I1")):
        ac_mod.select_item(ac2, x)
        time.sleep(0.2)
        ac_mod.click_option(ac2, label)
        time.sleep(0.2)
        ac_mod.click_submit(ac2)
        ac_mod.wait_reply(ac2, x)
        ac_mod.wait_no_item(ac2, x)


def main():
    c.wait_ready()
    c.console(clear=True)
    cleanup_all()
    ok = True
    ok &= run_case("A1/A2 自动打开 + 左列待办 + 右侧详情 + 单选回答回链（含 dialog_id）", case_auto_open_and_detail)
    ok &= run_case("A3 自定义输入回答（custom）回链", case_custom)
    ok &= run_case("A4 多选勾选 + 顿号连接", case_multi)
    ok &= run_case("A5 多待办聚合 + 逐条作答移除", case_aggregate)
    ok &= run_case("A6 子会话提问：左列显示会话标识", case_subsession)
    ok &= run_case("A7 超时（expires_at）→ 已超时标记 + 仍可作答", case_expired)
    ok &= run_case("A8 跳过：不回答、留在列表", case_skip)
    ok &= run_case("A9 取消：以「用户选择了其他…」文案作答 + 移出列表", case_cancel)
    ok &= run_case("A10 无 expires_at 时短时间不转 busy", case_no_deadline)
    ok &= run_case("A11 选项 + 「其他」自定义文本可并存", case_custom_with_options)
    ok &= run_case("A12 选项 {value,label} 对象：渲染 label / 回链 value", case_option_objects)
    ok &= run_case("A13 聊一聊：defer 回填 + 开对话窗口 + 问题引用入输入框", case_chat)
    ok &= run_case("A14 聊一聊边界：缺会话 → 提示且待办保留、不误提交", case_chat_missing_session)
    ok &= run_case("A15 关窗/重开：待办回填 + 窗口单例", case_reopen)
    cleanup_all()
    print("RESULT:", ok)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
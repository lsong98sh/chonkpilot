# -*- coding: utf-8 -*-
"""ask_user 对话框回归（FP 350-363）：事件驱动（mq 注入 ask-user，无需 LLM）。

覆盖：
  C1 推荐选项带「推荐」标记（L350）
  C2 单选回答 → ask-user-reply 回链（answer=选项）
  C3 自定义输入回答（options 空 → custom textarea）→ reply custom 值（L351）
  C4 多选：勾选多个选项 → answer 顿号连接（L352）
  C5 提问窗口同时只显示一个，后续排队（L353）
  C6 子会话提问标题显示子会话标识（L360）
  C7 超时（expires_at）→ busy 模式 + 不阻塞后续提问（L354 前半）
  C8 连续多问题依次弹出（L363 多问题）
  C9 跳过：不回答（不返回），放到队列尾部稍后重问（L359）
  C10 取消：以「终止任务待讨论」文案作为回答提交（L363）
  C11 标题栏显示第几个/共几个（L360）
  C12 无 expires_at 时前端默认 5 分钟超时（短时不转 busy）（L362）

前置：chonkpilot.exe --test-port=2345 已启动。
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
# 2026-10-27（42 (290)）：独立临时 work-dir —— 共享 ws 的累积会话/任务会让首屏与重渲染长时间
# 占满渲染线程（/eval 连续 ExecuteScript 超时，套件末尾的 c.console(clear=True) 都会被打断）。
_G = _h.acquire_gui(2345, work_dir=_h.tmp_dir("ck-askui-ws-"))  # 复用优先（owned=False 不回收）；无实例则自起并在结束时回收
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：usr+prj 全量，退出前自动回滚

SEQ = [0]


def deep_loads(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def ask_id():
    SEQ[0] += 1
    return f"ask-{SEQ[0]:03d}"


def wait_el(selector, max_wait=8):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if c.exists(selector).get("count", 0) > 0:
            return True
        time.sleep(0.3)
    return False


def emit_ask(payload):
    """注入前端本地事件 `ask-user`（= 61 §4.3 `session-ask` 的桥映射 type）。

    夹具按契约形状补 `dialog_id`（§0.6 必带；AskUser S1-3）：前端作答会**原样回带**
    （`ask-user-reply.dialog_id`），本文件 C14 断言该回带值一致。
    """
    p = {"dialog_id": "dlg-askui-fixture"}
    p.update(payload)
    c.mq_emit("ask-user", p)


def click_submit():
    """点击当前激活 ask 弹窗的提交按钮（.submit-btn，i18n 语言无关）。"""
    return c.eval("""(() => {
      const conts = [...document.querySelectorAll('.ask-user-content')].filter(e => e.offsetParent !== null);
      const cont = conts[conts.length - 1];
      const btn = cont ? cont.querySelector('.submit-btn') : null;
      if (!btn) return 'no-btn';
      if (btn.disabled) return 'disabled';
      btn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'clicked';
    })()""", 5000)


def click_btn(text):
    """在当前激活（可见且最后）ask 弹窗内点击文本匹配的 .b-btn（选项为用户数据非 i18n）。"""
    return c.eval("""(() => {
      const conts = [...document.querySelectorAll('.ask-user-content')].filter(e => e.offsetParent !== null);
      const cont = conts[conts.length - 1];
      const btn = cont ? [...cont.querySelectorAll('.b-btn')].find(b => b.textContent.includes(%s)) : null;
      if (!btn) return 'no-btn';
      btn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'clicked';
    })()""" % json.dumps(text), 5000)


def set_textarea(container_selector, value):
    c.eval("""(() => {
      const cont = document.querySelector(%s);
      const ta = cont ? cont.querySelector('textarea') : null;
      if (!ta) return 'no-ta';
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value').set;
      setter.call(ta, %s);
      ta.dispatchEvent(new Event('input', { bubbles: true }));
      return 'ok';
    })()""" % (json.dumps(container_selector), json.dumps(value)), 5000)


def ask_content_count():
    return deep_loads(c.eval("document.querySelectorAll('.ask-user-content').length"))


def visible_question():
    """当前可见（激活）弹窗的问题文本。"""
    return deep_loads(c.eval("""(() => {
      const els = [...document.querySelectorAll('.ask-user-content')].filter(e => e.offsetParent !== null);
      const q = els[els.length - 1]?.querySelector('.question-content');
      return q ? q.textContent : '';
    })()"""))


def case_recommended():
    aid = ask_id()
    c.mq_on_capture(["ask-user-reply"])
    emit_ask({"ask_id": aid, "question": "选择操作：", "options": ["构建", "测试", "部署"], "recommended": ["测试"]})
    if not wait_el(".ask-user-content"):
        raise TestError("ask 弹窗未出现")
    time.sleep(0.5)
    # 推荐徽标存在于"测试"按钮内
    n = deep_loads(c.eval("JSON.stringify([...document.querySelectorAll('.option-btn')].filter(b => b.querySelector('.rec-badge')).map(b => b.textContent.trim()))"))
    if not any("测试" in x for x in (n or [])):
        raise TestError(f"推荐徽标位置异常: {n}")
    # 选"构建"并提交
    click_btn("构建")
    time.sleep(0.2)
    click_submit()
    ev = c.wait_events("ask-user-reply", n=1, max_wait=8)
    if ev[0]["payload"].get("ask_id") != aid or ev[0]["payload"].get("answer") != "构建":
        raise TestError(f"单选回答回链异常: {ev}")
    if ask_content_count() != 0:
        raise TestError("回答后弹窗未关闭")


def case_custom():
    aid = ask_id()
    emit_ask({"ask_id": aid, "question": "补充说明：", "options": [], "custom": True})
    if not wait_el(".ask-user-content"):
        raise TestError("custom 弹窗未出现")
    time.sleep(0.5)
    set_textarea(".ask-user-content", "自定义回复内容")
    time.sleep(0.2)
    click_submit()
    ev = c.wait_events("ask-user-reply", n=1, max_wait=8)
    p = ev[0]["payload"]
    if p.get("answer") != "自定义回复内容" or p.get("custom") != "自定义回复内容":
        raise TestError(f"custom 回答回链异常: {ev}")
    if ask_content_count() != 0:
        raise TestError("custom 回答后弹窗未关闭")


def case_multi():
    aid = ask_id()
    emit_ask({"ask_id": aid, "question": "多选：", "options": ["A", "B", "C"], "multi": True, "recommended": ["A"]})
    if not wait_el(".ask-user-content"):
        raise TestError("多选弹窗未出现")
    time.sleep(0.5)
    click_btn("A")
    time.sleep(0.2)
    click_btn("C")
    time.sleep(0.2)
    # 断言两个选项被选中
    sel = deep_loads(c.eval("JSON.stringify([...document.querySelectorAll('.option-btn.selected')].map(b => b.textContent.trim()))"))
    if not any("A" in x for x in (sel or [])) or not any("C" in x for x in (sel or [])):
        raise TestError(f"多选勾选异常: {sel}")
    click_submit()
    ev = c.wait_events("ask-user-reply", n=1, max_wait=8)
    if ev[0]["payload"].get("answer") != "A、C":
        raise TestError(f"多选顿号连接异常: {ev}")
    if ask_content_count() != 0:
        raise TestError("多选回答后弹窗未关闭")


def case_custom_with_options():
    """C12 选项 + 「其他」自定义文本（多选）可并存：勾选 + 填「其他」→ 一并拼接回链。"""
    aid = ask_id()
    c.mq_on_capture(["ask-user-reply"])  # 重置捕获缓冲（否则 wait_events 会命中更早用例的旧事件）
    emit_ask({"ask_id": aid, "question": "选或填：", "options": ["A", "B", "C"], "multi": True, "custom": True})
    if not wait_el(".ask-user-content"):
        raise TestError("选项+custom 弹窗未出现")
    time.sleep(0.5)
    click_btn("A")
    time.sleep(0.2)
    click_btn("C")
    time.sleep(0.2)
    # 选择选项后自定义输入框仍可用（不再互斥/禁用）→ 填「其他」
    set_textarea(".ask-user-content", "其他补充")
    time.sleep(0.2)
    click_submit()
    ev = c.wait_events("ask-user-reply", n=1, max_wait=8)
    if ev[0]["payload"].get("ask_id") != aid or ev[0]["payload"].get("answer") != "A、C、其他补充":
        raise TestError(f"选项+自定义拼接异常: {ev}")
    if ask_content_count() != 0:
        raise TestError("选项+自定义回答后弹窗未关闭")


def case_option_objects():
    """C13 选项为 {value,label} 对象：渲染 label、回链 value、推荐按 value 高亮。"""
    aid = ask_id()
    c.mq_on_capture(["ask-user-reply"])
    emit_ask({"ask_id": aid, "question": "对象选项：",
              "options": [{"value": "v-build", "label": "构建"}, {"value": "v-test", "label": "测试"}],
              "recommended": ["v-test"]})
    if not wait_el(".ask-user-content"):
        raise TestError("对象选项弹窗未出现")
    time.sleep(0.5)
    # 展示 = label
    texts = deep_loads(c.eval("JSON.stringify([...document.querySelectorAll('.option-btn')].map(b => b.querySelector('.option-text')?.textContent.trim()))"))
    if not any("构建" in x for x in (texts or [])) or not any("测试" in x for x in (texts or [])):
        raise TestError(f"对象选项 label 未渲染: {texts}")
    # 推荐徽标按 value 匹配 → 落在 label「测试」按钮内
    n = deep_loads(c.eval("JSON.stringify([...document.querySelectorAll('.option-btn')].filter(b => b.querySelector('.rec-badge')).map(b => b.querySelector('.option-text')?.textContent.trim()))"))
    if not any("测试" in x for x in (n or [])):
        raise TestError(f"对象选项推荐（按 value）高亮异常: {n}")
    # 点 label「构建」→ 回链 value「v-build」
    click_btn("构建")
    time.sleep(0.2)
    click_submit()
    ev = c.wait_events("ask-user-reply", n=1, max_wait=8)
    if ev[0]["payload"].get("answer") != "v-build":
        raise TestError(f"对象选项应回链 value: {ev}")
    if ask_content_count() != 0:
        raise TestError("对象选项回答后弹窗未关闭")


def case_queue2():
    """排队：两个带选项的提问，回答第一个后弹出第二个。"""
    aid1, aid2 = ask_id(), ask_id()
    c.mq_on_capture(["ask-user-reply"])
    emit_ask({"ask_id": aid1, "question": "Q1 排队的第一个", "options": ["P1", "P2"]})
    time.sleep(0.6)
    emit_ask({"ask_id": aid2, "question": "Q2 排队的第二个", "options": ["P3", "P4"]})
    time.sleep(0.8)
    if ask_content_count() != 1:
        raise TestError(f"同时只应显示一个提问弹窗，实际 {ask_content_count()}")
    if visible_question() != "Q1 排队的第一个":
        raise TestError(f"应先显示第一个问题: {visible_question()!r}")
    click_btn("P1")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)
    if visible_question() != "Q2 排队的第二个":
        raise TestError(f"回答后应弹出第二个问题: {visible_question()!r}")
    click_btn("P3")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)
    if ask_content_count() != 0:
        raise TestError("两个提问都回答后应无弹窗")


def case_subsession_title():
    emit_ask({"ask_id": ask_id(), "question": "子会话提问", "options": ["Y", "N"], "sub_session_id": "ss12345678"})
    if not wait_el(".ask-user-content"):
        raise TestError("子会话弹窗未出现")
    time.sleep(0.5)
    tag = deep_loads(c.eval("document.querySelector('.session-tag')?.textContent || ''"))
    if "ss123456" not in tag:
        raise TestError(f"子会话标识未显示: {tag!r}")
    click_btn("Y")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.5)


def case_busy_timeout():
    """expires_at 超时 → busy 模式（banner + 不阻塞下一条）。"""
    now_ms = int(time.time() * 1000)
    emit_ask({"ask_id": ask_id(), "question": "超时问题", "options": ["X1", "X2"], "expires_at": now_ms + 1500})
    if not wait_el(".ask-user-content"):
        raise TestError("超时测试弹窗未出现")
    time.sleep(2.5)
    if c.exists(".busy-banner").get("count", 0) == 0:
        raise TestError("超时后应显示 busy 提示")
    # busy 不阻塞：注入第二条 → 新弹窗出现
    emit_ask({"ask_id": ask_id(), "question": "busy 后的下一个", "options": ["Y1", "Y2"]})
    time.sleep(1.0)
    if visible_question() != "busy 后的下一个":
        raise TestError(f"busy 不应阻塞后续提问: {visible_question()!r}")
    click_btn("Y1")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)
    # 清理 busy 残留弹窗（点 X2 关闭第一个 busy 弹窗）
    click_btn("X2")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)


def case_multi_questions():
    """连续多问题依次弹出（多问题参数语义 = 逐条 ask-user 事件）。"""
    ids = [ask_id() for _ in range(3)]
    for i, (aid, q) in enumerate(zip(ids, ["多问题1", "多问题2", "多问题3"])):
        emit_ask({"ask_id": aid, "question": q, "options": [f"O{i}a", f"O{i}b"]})
        time.sleep(0.5)
    # 依次回答
    for i in range(3):
        time.sleep(0.6)
        q = visible_question()
        if q != f"多问题{i + 1}":
            raise TestError(f"第 {i + 1} 个问题应为多问题{i + 1}，实际 {q!r}")
        click_btn(f"O{i}a")
        time.sleep(0.2)
        click_submit()
        time.sleep(0.6)
    if ask_content_count() != 0:
        raise TestError("多问题全部回答后应无弹窗")


def click_skip():
    """点击当前激活 ask 弹窗的跳过按钮（.skip-btn）。"""
    return c.eval("""(() => {
      const conts = [...document.querySelectorAll('.ask-user-content')].filter(e => e.offsetParent !== null);
      const cont = conts[conts.length - 1];
      const btn = cont ? cont.querySelector('.skip-btn') : null;
      if (!btn) return 'no-btn';
      btn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'clicked';
    })()""", 5000)


def click_cancel():
    """点击当前激活 ask 弹窗的取消按钮（.cancel-btn）。"""
    return c.eval("""(() => {
      const conts = [...document.querySelectorAll('.ask-user-content')].filter(e => e.offsetParent !== null);
      const cont = conts[conts.length - 1];
      const btn = cont ? cont.querySelector('.cancel-btn') : null;
      if (!btn) return 'no-btn';
      btn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'clicked';
    })()""", 5000)


def dialog_title():
    """当前最后（最上层）ask 弹窗的标题文本（.dialog-shell .dialog-title）。

    注意：ask 弹窗为 modal（dialog-shell 是 fixed 定位），offsetParent 恒为 null，
    不能用 offsetParent 过滤，直接取最后一个 dialog-shell。
    """
    return deep_loads(c.eval("""(() => {
      const dialogs = [...document.querySelectorAll('.dialog-shell')];
      const d = dialogs[dialogs.length - 1];
      return d ? (d.querySelector('.dialog-title')?.textContent || '') : '';
    })()"""))


def case_skip():
    """跳过：不回答（不返回），放到队列尾部稍后重问（FP L359）。"""
    aid1, aid2 = ask_id(), ask_id()
    c.mq_on_capture(["ask-user-reply"])
    emit_ask({"ask_id": aid1, "question": "S1 被跳过的", "options": ["S1a", "S1b"]})
    if not wait_el(".ask-user-content"):
        raise TestError("跳过测试：第一个弹窗未出现")
    time.sleep(0.5)
    emit_ask({"ask_id": aid2, "question": "S2 第二个", "options": ["S2a", "S2b"]})
    time.sleep(0.8)
    if visible_question() != "S1 被跳过的":
        raise TestError(f"跳过测试：应先显示第一个: {visible_question()!r}")
    # 点跳过 → 不返回 reply、放入队尾 → 第二个弹出
    click_skip()
    time.sleep(1.0)
    if visible_question() != "S2 第二个":
        raise TestError(f"跳过测试：跳过第一个后应显示第二个: {visible_question()!r}")
    # 跳过期间不应产生 ask-user-reply
    if len(c.events_of("ask-user-reply", clear=False)) != 0:
        raise TestError("跳过测试：跳过不应产生回答事件")
    # 回答第二个
    click_btn("S2a")
    time.sleep(0.2)
    click_submit()
    time.sleep(1.0)
    # 被跳过的第一个重新弹出（队尾）
    if visible_question() != "S1 被跳过的":
        raise TestError(f"跳过测试：被跳过的应重新弹出: {visible_question()!r}")
    click_btn("S1a")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)
    if ask_content_count() != 0:
        raise TestError("跳过测试：全部处理后应无弹窗")


def case_cancel():
    """取消：以「终止任务待讨论」文案作为回答提交（FP L363）。"""
    aid = ask_id()
    c.mq_on_capture(["ask-user-reply"])
    emit_ask({"ask_id": aid, "question": "取消测试问题", "options": ["C1a", "C1b"]})
    if not wait_el(".ask-user-content"):
        raise TestError("取消测试：弹窗未出现")
    time.sleep(0.5)
    click_cancel()
    ev = c.wait_events("ask-user-reply", n=1, max_wait=8)
    p = ev[0]["payload"]
    if p.get("ask_id") != aid:
        raise TestError(f"取消测试：ask_id 不匹配: {p}")
    # 取消文案（zh/en 双语言：locale 可能被之前会话切换）
    cancel_answers = ("此问题较复杂，先终止任务，待讨论清楚后决定",
                      "This question is complex, terminating the task first to discuss it later")
    if p.get("answer") not in cancel_answers:
        raise TestError(f"取消测试：回答文案异常: {p.get('answer')!r}")
    if ask_content_count() != 0:
        raise TestError("取消测试：弹窗未关闭")


def case_seq_title():
    """标题栏显示第几个/共几个（FP L360）。

    同一 eval 内连续 emit 3 条（模拟 server 同一事件循环连续广播多问题）：
    前端 300ms 合并窗口内全部入队 → 第一个弹出时标题即显示 1/3。
    """
    ids = [ask_id() for _ in range(3)]
    payloads = [{"ask_id": aid, "question": q, "options": [f"T{i}x", f"T{i}y"]}
                for i, (aid, q) in enumerate(zip(ids, ["标题1", "标题2", "标题3"]))]
    js = "(() => {" + "".join(
        f"window.mq.emit('ask-user', {json.dumps(p, ensure_ascii=False)});"
        for p in payloads
    ) + " return 'ok'; })()"
    c.eval(js)
    # 等待合并窗口结束 + 弹窗渲染
    time.sleep(1.0)
    t1 = dialog_title()
    if "1/3" not in t1:
        raise TestError(f"标题序号：第一个应为 1/3，实际 {t1!r}")
    click_btn("T0x")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)
    t2 = dialog_title()
    if "2/3" not in t2:
        raise TestError(f"标题序号：第二个应为 2/3，实际 {t2!r}")
    click_btn("T1x")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)
    t3 = dialog_title()
    if "3/3" not in t3:
        raise TestError(f"标题序号：第三个应为 3/3，实际 {t3!r}")
    click_btn("T2x")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)
    if ask_content_count() != 0:
        raise TestError("标题序号：全部回答后应无弹窗")


def case_no_deadline_no_immediate_busy():
    """server 未给 expires_at 时前端默认 5 分钟超时：短时间内不应转 busy（FP L362）。"""
    aid = ask_id()
    emit_ask({"ask_id": aid, "question": "默认超时问题", "options": ["D1", "D2"]})
    if not wait_el(".ask-user-content"):
        raise TestError("默认超时测试：弹窗未出现")
    time.sleep(2.0)
    if c.exists(".busy-banner").get("count", 0) > 0:
        raise TestError("默认超时测试：无 expires_at 时短时间不应转 busy")
    click_btn("D1")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)
    if ask_content_count() != 0:
        raise TestError("默认超时测试：回答后应无弹窗")


# ══════════════════════════════════════════════════════════════════
# AskUser Step 1（S1-1 / S1-2 / S1-4 / S1-6，2026-10-29）
# 口径：docs/ask-user/AskUser专题设计方案.md §4.3 / §4.4 / §4.6 / §9
# ══════════════════════════════════════════════════════════════════

DEFER_ANSWERS = (
    "我先不直接作答：请结束本轮，我先和你讨论这个问题，讨论清楚后我再给出结论",
    "I won't answer directly yet: please end this turn so we can discuss the question first; I'll conclude once it's clear",
)


def make_session(tag):
    """幂等建会话（一树一库）→ 返回 (session_id, dialog_id)。"""
    sid_ = "ask-%s-%d" % (tag, int(time.time() * 1000))
    res = c.req("data-session-ensure-session", {"session_id": sid_}) or {}
    dlg = res.get("dialog_id") or ""
    if not dlg:
        raise TestError("ensure-session 未回带 dialog_id: %s" % (res,))
    return sid_, dlg


def chat_btn_count():
    return deep_loads(c.eval("document.querySelectorAll('.ask-user-content .chat-btn').length"))


def input_text():
    return deep_loads(c.eval("document.querySelector('.richtext-input')?.textContent || ''"))


def case_chat_button_main_window():
    """S1-1/S1-2：主窗口 ask 卡片有「聊一聊」；点击 → 切到发起会话 + defer 回填 + 问题引用入输入框。"""
    sid_, dlg = make_session("chat")
    aid = ask_id()
    c.mq_on_capture(["ask-user-reply", "session-changed"])
    emit_ask({"ask_id": aid, "question": "要不要先讨论一下？", "options": ["直接答", "稍后"],
              "session": sid_, "dialog_id": dlg})
    if not wait_el(".ask-user-content"):
        raise TestError("聊一聊测试：弹窗未出现")
    time.sleep(0.5)
    if chat_btn_count() != 1:
        raise TestError("主窗口 ask 卡片应有且仅有一个「聊一聊」按钮，实际 %s" % chat_btn_count())
    # 点「聊一聊」
    r = c.eval("""(() => {
      const b = [...document.querySelectorAll('.ask-user-content .chat-btn')].pop();
      if (!b) return 'no-btn';
      b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'clicked';
    })()""", 5000)
    if deep_loads(r) != "clicked":
        raise TestError("「聊一聊」按钮不可点: %r" % (r,))
    # ① 应答 = defer 语义文案（既有 answer 字段表达；不新增字段）+ dialog_id 原样回带
    ev = c.wait_events("ask-user-reply", n=1, max_wait=10)
    p = ev[0]["payload"] or {}
    if p.get("ask_id") != aid:
        raise TestError("聊一聊应答 ask_id 不符: %s" % (p,))
    if p.get("answer") not in DEFER_ANSWERS:
        raise TestError("聊一聊应以 defer 语义回填（不作最终作答）: %r" % (p.get("answer"),))
    if p.get("dialog_id") != dlg:
        raise TestError("ask-user-reply.dialog_id 应原样回带 %r，实际 %r" % (dlg, p.get("dialog_id")))
    # ② 切到发起会话（session-changed 广播）
    evs = c.wait_events("session-changed", n=1, max_wait=10)
    got = (evs[0]["payload"] or {}).get("session_id")
    if got != sid_:
        raise TestError("聊一聊应先切到发起会话 %r，实际 %r" % (sid_, got))
    # ③ 卡片关闭 + 问题引用入输入框（可编辑、不自动发送）
    time.sleep(0.6)
    if ask_content_count() != 0:
        raise TestError("聊一聊后 ask 卡片应关闭")
    txt = input_text()
    if "要不要先讨论一下？" not in txt:
        raise TestError("问题引用未进入输入框: %r" % (txt,))
    if "AI" not in txt:
        raise TestError("问题引用应带来源标记（引用块）: %r" % (txt,))
    # 不自动发送：输入框仍有内容（未被清空 = 未发送）
    if not txt.strip():
        raise TestError("「聊一聊」不应自动发送（输入框应保留引用文本）")
    # 清理输入框，避免影响后续用例
    c.eval("(() => { const el = document.querySelector('.richtext-input'); if (el) { el.textContent = ''; el.dispatchEvent(new Event('input', { bubbles: true })); } return 'ok'; })()", 5000)


def case_chat_button_missing_session():
    """S1-2 边界：提问未关联会话 → 不炸、给可见提示、卡片保留、不提交应答。

    注：会话**存在但行已删除**时的读路径会经既有 `api/session.js#sessionReq` 自愈
    （`dialog_required` → `data-session-list` → `ensure-session` 重建）→ 与「复用既有会话链路」
    一致（不会崩、也不会误提交）；本用例取**确定性**分支：载荷无 `session`。
    """
    aid = ask_id()
    c.mq_on_capture(["ask-user-reply"])
    emit_ask({"ask_id": aid, "question": "无会话的提问", "options": ["X", "Y"]})
    if not wait_el(".ask-user-content"):
        raise TestError("无会话测试：弹窗未出现")
    time.sleep(0.4)
    c.eval("""(() => {
      const b = [...document.querySelectorAll('.ask-user-content .chat-btn')].pop();
      if (b) b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""", 5000)
    time.sleep(1.0)
    # 不产生应答（未作答），卡片仍在
    if c.events_of("ask-user-reply", clear=False):
        raise TestError("缺会话时不应提交应答")
    if ask_content_count() != 1:
        raise TestError("缺会话时卡片应保留（可改选其它出口）")
    # 可见提示（toast）
    if c.exists(".b-message--error").get("count", 0) == 0:
        raise TestError("缺会话时应给可见错误提示")
    # 收尾：取消该卡片
    click_cancel()
    time.sleep(0.8)


def ask_metrics():
    """当前 ask 弹窗的几何 + 屏幕工作区快照（视口坐标系；单位 = CSS px）。"""
    raw = c.eval("""(() => {
      const d = [...document.querySelectorAll('.dialog-shell')].pop();
      if (!d) return 'null';
      const r = d.getBoundingClientRect();
      const s = window.screen;
      return JSON.stringify({
        left: r.left, top: r.top, w: r.width, h: r.height,
        innerW: window.innerWidth, innerH: window.innerHeight,
        vpLeft: window.screenX + Math.max(0, (window.outerWidth - window.innerWidth) / 2),
        vpTop: window.screenY + Math.max(0, window.outerHeight - window.innerHeight),
        availLeft: s.availLeft, availTop: s.availTop,
        availWidth: s.availWidth, availHeight: s.availHeight
      });
    })()""", 5000)
    return deep_loads(raw)


def expected_center_pos(m):
    """与 utils/dialogPosition.js 同口径：屏幕工作区中心 → 视口坐标 → clamp。"""
    cx = (m["availLeft"] + m["availWidth"] / 2) - m["vpLeft"]
    cy = (m["availTop"] + m["availHeight"] / 2) - m["vpTop"]
    left = round(cx - m["w"] / 2)
    top = round(cy - m["h"] / 2)
    left = min(max(left, 0), max(0, m["innerW"] - m["w"]))
    top = min(max(top, 0), max(0, m["innerH"] - m["h"]))
    return left, top


def drag_dialog(dx, dy):
    """真前端事件拖拽弹窗标题栏（DialogShell 监听 document mousemove/mouseup）。"""
    c.eval("""(() => {
      const d = [...document.querySelectorAll('.dialog-shell')].pop();
      const h = d ? d.querySelector('.dialog-header') : null;
      if (!h) return 'no-header';
      h.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, clientX: 200, clientY: 120 }));
      document.dispatchEvent(new MouseEvent('mousemove', { bubbles: true, clientX: %d, clientY: %d }));
      document.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }));
      return 'ok';
    })()""" % (200 + dx, 120 + dy), 5000)
    time.sleep(0.3)


def case_screen_centered():
    """S1-6：ask 卡片居中于**屏幕工作区**（±2px）；拖拽后再弹出仍重新居中（不记忆位置）。"""
    aid = ask_id()
    c.mq_on_capture(["ask-user-reply"])
    emit_ask({"ask_id": aid, "question": "位置校验", "options": ["L1", "L2"]})
    if not wait_el(".ask-user-content"):
        raise TestError("居中测试：弹窗未出现")
    time.sleep(0.8)  # 等 ResizeObserver 按实测尺寸再居中
    m = ask_metrics()
    if not isinstance(m, dict):
        raise TestError("居中测试：取不到弹窗几何")
    want_left, want_top = expected_center_pos(m)
    if abs(m["left"] - want_left) > 2 or abs(m["top"] - want_top) > 2:
        raise TestError("弹窗未居中于屏幕工作区：实际 (%.1f, %.1f) 期望 (%d, %d)（±2px）"
                        % (m["left"], m["top"], want_left, want_top))
    # 拖拽 → 位置改变（只影响本次）
    drag_dialog(180, 120)
    m2 = ask_metrics()
    if abs(m2["left"] - m["left"]) < 30 and abs(m2["top"] - m["top"]) < 30:
        raise TestError("拖拽未生效，无法验证「不记忆位置」: %r → %r" % (m["left"], m2["left"]))
    # 作答关闭 → 再弹一张
    click_btn("L1")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)
    emit_ask({"ask_id": ask_id(), "question": "位置校验（第二次）", "options": ["M1", "M2"]})
    if not wait_el(".ask-user-content"):
        raise TestError("居中测试：第二次弹窗未出现")
    time.sleep(0.8)
    m3 = ask_metrics()
    want_left3, want_top3 = expected_center_pos(m3)
    if abs(m3["left"] - want_left3) > 2 or abs(m3["top"] - want_top3) > 2:
        raise TestError("重复弹出应重新居中（不沿用上次拖拽位置）：实际 (%.1f, %.1f) 期望 (%d, %d)"
                        % (m3["left"], m3["top"], want_left3, want_top3))
    click_btn("M1")
    time.sleep(0.2)
    click_submit()
    time.sleep(0.8)



def cleanup_asks(max_rounds=12):
    """关闭所有残留 ask 弹窗（有选项点第一个 + 提交；无选项填 custom 提交）。"""
    for _ in range(max_rounds):
        n = deep_loads(c.eval("document.querySelectorAll('.ask-user-content').length"))
        if n == 0:
            return
        c.eval("""(() => {
          const conts = [...document.querySelectorAll('.ask-user-content')];
          const cont = conts[conts.length - 1];
          const opt = cont.querySelector('.option-btn');
          if (opt) { opt.dispatchEvent(new MouseEvent('click', { bubbles: true })); return 'opt'; }
          const ta = cont.querySelector('textarea');
          if (ta) {
            const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value').set;
            setter.call(ta, 'cleanup');
            ta.dispatchEvent(new Event('input', { bubbles: true }));
            return 'ta';
          }
          return 'none';
        })()""", 5000)
        time.sleep(0.3)
        click_submit()
        time.sleep(0.6)
    if deep_loads(c.eval("document.querySelectorAll('.ask-user-content').length")) > 0:
        raise TestError("残留 ask 弹窗清理失败")


def main():
    c.wait_ready()
    c.console(clear=True)
    c.mq_on_capture(["ask-user-reply"])
    cleanup_asks()
    ok = True
    ok &= run_case("C1 推荐选项带「推荐」标记 + 单选回答回链", case_recommended)
    ok &= run_case("C2 自定义输入回答（custom）回链", case_custom)
    ok &= run_case("C3 多选勾选 + 顿号连接", case_multi)
    ok &= run_case("C4 提问窗口只显示一个 + 排队推进", case_queue2)
    ok &= run_case("C5 子会话提问标题显示子会话标识", case_subsession_title)
    ok &= run_case("C6 超时（expires_at）→ busy + 不阻塞后续", case_busy_timeout)
    ok &= run_case("C7 连续多问题依次弹出", case_multi_questions)
    ok &= run_case("C8 跳过：不返回、放到队尾重新弹出", case_skip)
    ok &= run_case("C9 取消：以「终止任务待讨论」文案提交回答", case_cancel)
    ok &= run_case("C10 标题栏显示第几个/共几个", case_seq_title)
    ok &= run_case("C11 无 expires_at 时前端默认 5 分钟超时（短时不转 busy）", case_no_deadline_no_immediate_busy)
    ok &= run_case("C12 选项 + 「其他」自定义文本（多选）可并存", case_custom_with_options)
    ok &= run_case("C13 选项 {value,label} 对象：渲染 label / 回链 value", case_option_objects)
    # AskUser Step 1（S1-1 / S1-2 / S1-4 / S1-6）
    ok &= run_case("C14 主窗口「聊一聊」：切会话 + defer 回填（带 dialog_id）+ 问题引用入输入框", case_chat_button_main_window)
    ok &= run_case("C15 「聊一聊」边界：提问缺会话 → 可见提示且卡片保留、不误提交", case_chat_button_missing_session)
    ok &= run_case("C16 ask 弹窗居中于屏幕工作区（±2px；重复弹出不沿用拖拽位置）", case_screen_centered)
    print("RESULT:", ok)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

# -*- coding: utf-8 -*-
"""Ask 中心（全局待办窗口）E2E 共用驱动：窗口定位 / 待办断言 / 卡片操作。

2026-11-04 起 ask_user 不再默认弹 dialog：归属窗口收到 `ask-user` 后经
`gui.window.open-ask` 自动打开**本进程唯一**（2026-11-05 起全局跨项目）的 `#ask` 窗口
（左列待办 + 右侧详情）。
本模块把「注入 ask → 等窗口就绪 → 选中待办 → 操作卡片」收敛成可复用函数，供
run_ask_ui / run_mw / run_tools / run_server_deps / test_task 等套件共用。

驱动通道仍是 `--test-port`：Ask 中心窗口 id 由 `gui.window.list.ask_window_id` 取得，
再以 `window_id` 路由测试客户端（与对话窗口同机制）。
"""

import json
import time

from chonk_client import ChonkClient, TestError


def _js_str(v):
    return json.dumps(v, ensure_ascii=False)


def _unwrap(v):
    """EvalWithResult 对字符串会做 JSON 编码（`"ok"`）→ 逐层解包到纯值。"""
    for _ in range(4):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def ask_window_id(c, timeout=30.0):
    """轮询 `gui.window.list.ask_window_id`（未自动打开 → 空串）。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            r = c.req("gui.window.list", {})
            wid = (r or {}).get("ask_window_id") or ""
            if wid:
                return wid
        except Exception:
            pass
        time.sleep(0.3)
    return ""


def ask_client(c, timeout=30.0):
    """等 Ask 中心窗口自动打开并页面就绪，返回 (window_id, ChonkClient)。"""
    wid = ask_window_id(c, timeout)
    if not wid:
        raise TestError("Ask 中心窗口未自动打开（gui.window.list.ask_window_id 为空）")
    ac = ChonkClient(base=c.base, timeout=30, window_id=wid)
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if ac.exists(".ask-center").get("count", 0) > 0:
                return wid, ac
        except Exception:
            pass
        time.sleep(0.3)
    raise TestError("Ask 中心页面未就绪（.ask-center 未出现）")


def close_center(ac, c=None, timeout=10.0):
    """关闭 Ask 中心窗口并等 `gui.window.list.ask_window_id` 变空（幂等；失败不抛）。"""
    try:
        ac.publish("gui.window.status", {"command": "close"})
    except Exception:
        pass
    if c is None:
        return
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if not ((c.req("gui.window.list", {}) or {}).get("ask_window_id") or ""):
                return
        except Exception:
            pass
        time.sleep(0.2)


def emit_ask(c, payload):
    """注入前端本地事件 ask-user（= 61 §4.3 session-ask 的桥映射 type）。

    自动补 `dialog_id`（§0.6 必带；缺省时夹具值），供作答回带断言。
    """
    p = {"dialog_id": "dlg-askcenter-fixture"}
    p.update(payload)
    return c.mq_emit("ask-user", p)


def inject(c, payload, timeout=30.0):
    """注入 ask 并等它出现在 Ask 中心左列；返回 (window_id, ask_client)。"""
    emit_ask(c, payload)
    wid, ac = ask_client(c, timeout)
    wait_item(ac, payload["ask_id"], timeout)
    return wid, ac


def item_count(ac):
    try:
        return int(ac.eval("document.querySelectorAll('.ask-list-item').length") or 0)
    except Exception:
        return 0


def has_item(ac, ask_id):
    try:
        return int(ac.eval("document.querySelectorAll(%s).length" % _js_str(
            '[data-ask-id="%s"]' % ask_id)) or 0) > 0
    except Exception:
        return False


def wait_item(ac, ask_id, timeout=15.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if has_item(ac, ask_id):
            return True
        time.sleep(0.2)
    return False


def wait_no_item(ac, ask_id, timeout=15.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if not has_item(ac, ask_id):
            return True
        time.sleep(0.2)
    return False


def select_item(ac, ask_id):
    """选中左列某条待办（点击；未知 id → 返回 'no-item'）。"""
    return ac.eval("""(() => {
      const el = document.querySelector(%s);
      if (!el) return 'no-item';
      el.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""" % _js_str('[data-ask-id="%s"]' % ask_id), 5000)


def question_text(ac):
    v = ac.eval("document.querySelector('.ask-detail .question-content')?.textContent || ''", 5000)
    v = _unwrap(v)
    return v if isinstance(v, str) else ''


def option_labels(ac):
    raw = ac.eval("JSON.stringify([...document.querySelectorAll('.ask-detail .option-btn .option-text')].map(e => e.textContent.trim()))", 5000)
    for _ in range(3):
        if not isinstance(raw, str):
            break
        try:
            raw = json.loads(raw)
        except Exception:
            break
    return raw if isinstance(raw, list) else []


def recommended_count(ac):
    try:
        return int(ac.eval("document.querySelectorAll('.ask-detail .rec-badge').length") or 0)
    except Exception:
        return 0


def click_option(ac, label):
    """在右侧详情点选文本匹配的选项（用户数据非 i18n）。"""
    return ac.eval("""(() => {
      const btns = [...document.querySelectorAll('.ask-detail .option-btn')];
      const b = btns.find(x => x.textContent.includes(%s));
      if (!b) return 'no-btn';
      b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""" % _js_str(label), 5000)


def click_submit(ac):
    return ac.eval("""(() => {
      const b = document.querySelector('.ask-detail .submit-btn');
      if (!b) return 'no-btn';
      if (b.disabled) return 'disabled';
      b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""", 5000)


def click_chat(ac):
    return ac.eval("""(() => {
      const b = document.querySelector('.ask-detail .chat-btn');
      if (!b) return 'no-btn';
      b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""", 5000)


def click_skip(ac):
    return ac.eval("""(() => {
      const b = document.querySelector('.ask-detail .skip-btn');
      if (!b) return 'no-btn';
      b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""", 5000)


def click_cancel(ac):
    return ac.eval("""(() => {
      const b = document.querySelector('.ask-detail .cancel-btn');
      if (!b) return 'no-btn';
      b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""", 5000)


def set_custom(ac, value):
    return _unwrap(ac.eval("""(() => {
      const ta = document.querySelector('.ask-detail .custom-input textarea');
      if (!ta) return 'no-ta';
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value').set;
      setter.call(ta, %s);
      ta.dispatchEvent(new Event('input', { bubbles: true }));
      return 'ok';
    })()""" % _js_str(value), 5000))


def busy_visible(ac):
    try:
        return int(ac.eval("document.querySelectorAll('.ask-detail .busy-banner').length") or 0) > 0
    except Exception:
        return False


def wait_reply(ac, ask_id, max_wait=10.0):
    """在 Ask 中心窗口内等 ask-user-reply（先调用方 `ac.mq_on_capture(["ask-user-reply"])`）。"""
    deadline = time.time() + max_wait
    while time.time() < deadline:
        evs = ac.events_of("ask-user-reply", clear=False)
        for e in evs:
            p = e.get("payload") or {}
            if p.get("ask_id") == ask_id:
                ac.events_of("ask-user-reply", clear=True)
                return p
        time.sleep(0.2)
    return None
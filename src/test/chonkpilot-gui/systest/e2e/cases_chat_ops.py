# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑨：**对话输入区 / 排队 / 轮次导航**（迁移自旧套件三套）。

旧套件（仍保留在 run_all.py）：
* `test_queue.py`          → S-CHAT-QUEUE-01（忙碌入队 / 摘要条 / 撤回 / 删除）
* `test_chat_input_top.py` → S-CHAT-TOP-01（任务进度条 / 后台通知 / 忙碌暂存 / 不静默续写）
* `test_turn_nav.py`       → S-TURN-NAV-01（3/13 轮圈数、悬停一览、禁用圈、点击滚动）

忙碌夹具 = 本地「只收不回」LLM 端点（usr `llms` 临时条目，用例结束还原）；
轮次驱动 = `message-send` + `llm-receive`/`llm-complete` 远端事件，不依赖真实外网 LLM。
"""

import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import harness as _h

from .framework import case

HANG_LLM = "e2e-hang"


class _HangHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        if n:
            self.rfile.read(n)
        time.sleep(120)

    def log_message(self, *a):
        pass


def _start_hang():
    port = _h.free_port()
    srv = ThreadingHTTPServer(("127.0.0.1", port), _HangHandler)
    srv.daemon_threads = True
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, port


def _stop_hang(srv):
    try:
        srv.shutdown()
        srv.server_close()
    except Exception:
        pass


def _llms(env):
    raw = env.usr().get("llms")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw)
        except Exception:
            raw = []
    return [e for e in (raw or []) if isinstance(e, dict)]


def _add_hang(env, port):
    base = [e for e in _llms(env) if e.get("name") != HANG_LLM]
    env.req("data-user-config-save", {"data": {"llms": base + [{
        "name": HANG_LLM, "protocol": "openai", "apiKey": "hang-key",
        "model": "hang-model", "baseUrl": "http://127.0.0.1:%d/v1" % port,
        "temperature": 0.7, "maxOutputToken": 1024,
    }]}})
    return base


def _restore_llms(env, base):
    env.req("data-user-config-save", {"data": {"llms": base}})


def _select(env, name):
    env.js("window.mq.emit('chat-select-llm', {name: %s}); 'ok'" % json.dumps(name))
    time.sleep(0.3)


def _remote(env, typ, payload):
    env.js("window.mq.emitRemote({type: %s, payload: %s}); 'ok'"
           % (json.dumps(typ), json.dumps(json.dumps(payload))))


def _send(env, sid, text, llm=None):
    body = {"sessionId": sid, "text": text}
    if llm:
        body["llm"] = llm
    env.js("window.mq.emit('message-send', %s); 'ok'" % json.dumps(body))


def _queue_send(env, sid, text, llm):
    env.js("window.mq.emit('message-queue', %s); 'ok'"
           % json.dumps({"sessionId": sid, "text": text, "llm": llm}))


def _session_tag(env):
    return env.js("(()=>{const t=document.querySelector('.chat-session-tag');"
                  "return t?t.getAttribute('title'):null})()")


def _open_session(env, sid):
    env.req("data-session-ensure-session", {"data": {"session_id": sid}})
    env.js("window.mq.emit('session-changed', {session_id: %s}); 'ok'" % json.dumps(sid))
    env.wait(lambda: True if _session_tag(env) == sid else None,
             "会话身份生效 %s" % sid, timeout=20)
    time.sleep(0.4)


def _running(env, sid):
    r = env.req("session-running-list", {}) or {}
    lst = r.get("list") or (r.get("result") or {}).get("list") or []
    return [x for x in lst if (x or {}).get("session_id") == sid]


def _badge(env):
    return str(env.js("document.querySelector('.inbox-bar .inbox-bar-count')"
                      "?.textContent?.trim() || ''") or "")


def _wait_badge(env, want, timeout=15):
    return env.wait(lambda: True if _badge(env) == str(want) else None,
                    "队列计数 = %s" % want, timeout=timeout)


def _popover_texts(env):
    return env.jsj("JSON.stringify([...document.querySelectorAll("
                   "'.inbox-popover .inbox-item .inbox-item-content')]"
                   ".map(e=>e.textContent.trim()))") or []


def _user_count(env):
    return int(env.js("document.querySelectorAll('.message-item.user').length") or 0)


def _wait_users(env, n, timeout=25):
    return env.wait(lambda: True if _user_count(env) >= n else None,
                    "用户消息渲染数 ≥ %d" % n, timeout=timeout)


def _toasts(env):
    return env.jsj("JSON.stringify([...document.querySelectorAll("
                   "'#b-message-container .b-message')].map(e=>e.textContent.trim()))") or []


def _run_turn(env, sid, n, hang):
    before = _user_count(env)
    _send(env, sid, "Q%d what is the plan?" % n, llm=HANG_LLM if hang else None)
    if not _wait_users(env, before + 1):
        _send(env, sid, "Q%d what is the plan?" % n, llm=HANG_LLM if hang else None)
        _wait_users(env, before + 1)
    _remote(env, "llm-receive", {"type": "text",
                                 "text": "A%d this is a reply body for turn %d." % (n, n),
                                 "session": sid})
    time.sleep(0.3)
    _remote(env, "llm-complete", {"session": sid, "status": "completed"})
    time.sleep(0.4)


@case("S-CHAT-QUEUE-01", "会话消息", "排队消息：忙碌入队计数 / 摘要条展开 / 撤回写回 / 删除清空",
      stage="seeded", order=300, fp="33 CHAT 输入区操作行 · INBOX 摘要条（test_queue）",
      desc="挂起 LLM 使 turn 持续 busy（session-running-list 命中）→ 入队 2 条 → "
           "`.inbox-bar-count`=1/2、popover 顺序=入队顺序 → 撤回第二条写回输入区且剩 1 → "
           "删除剩余 → `llm-inbox-list` 空且摘要条消失。")
def s_chat_queue_01(env):
    srv, port = _start_hang()
    base = _add_hang(env, port)
    try:
        _select(env, HANG_LLM)
        sid = "e2e-queue-%s" % env.run_id
        _open_session(env, sid)
        env.check("空队列不渲染摘要条", env.js("!document.querySelector('.inbox-bar')") is True)

        _send(env, sid, "hang turn", llm=HANG_LLM)
        env.wait(lambda: True if _running(env, sid) else None,
                 "忙碌 turn 已在跑", timeout=25)
        env.check("忙碌态出现入队按钮", env.js(
            "(()=>{const bs=[...document.querySelectorAll('.input-actions-right button')];"
            "return bs.some(b=>['Queue','入队'].includes((b.textContent||'').trim()))})()") is True)

        _queue_send(env, sid, "排队消息甲", HANG_LLM)
        env.wait(lambda: True if str(env.js(
            "document.querySelector('.inbox-bar .inbox-bar-text')?.textContent?.trim() || ''")
        ) == "排队消息甲" else None, "摘要条首行=甲", timeout=15)
        env.eq("入队 1 条计数", _badge(env), "1")
        _queue_send(env, sid, "排队消息乙", HANG_LLM)
        _wait_badge(env, 2)
        env.eq("入队 2 条计数", _badge(env), "2")

        env.js("document.querySelector('.inbox-bar .b-popover__reference')"
               "?.dispatchEvent(new MouseEvent('click',{bubbles:true})); 'ok'")
        time.sleep(0.6)
        env.eq("popover 条目数", int(env.js(
            "document.querySelectorAll('.inbox-popover .inbox-item').length") or 0), 2)
        env.eq("popover 顺序 = 入队顺序", _popover_texts(env), ["排队消息甲", "排队消息乙"])

        env.js("document.querySelectorAll('.inbox-popover .inbox-item')[1]"
               "?.querySelector('.inbox-item-actions button[title^=\"撤回\"]')?.click(); 'ok'")
        _wait_badge(env, 1)
        ta = str(env.js("document.querySelector('[contenteditable][data-drop-target="
                        "\"chat-input\"]')?.innerText?.trim() || ''") or "")
        env.eq("撤回文本写回输入区", ta, "排队消息乙")
        env.eq("未撤回项保留", _popover_texts(env), ["排队消息甲"])

        if env.js("!!document.querySelector('.inbox-popover .inbox-item')") is not True:
            env.js("document.querySelector('.inbox-bar .b-popover__reference')"
                   "?.dispatchEvent(new MouseEvent('click',{bubbles:true})); 'ok'")
            time.sleep(0.5)
        env.js("document.querySelector('.inbox-popover .inbox-item "
               ".inbox-item-actions button[title=\"删除\"]')?.click(); 'ok'")
        stable = 0
        end = time.time() + 15
        while time.time() < end:
            ib = env.req("llm-inbox-list", {"session": sid}) or {}
            items = ib.get("items") or (ib.get("result") or {}).get("items") or []
            if not items and env.js("!document.querySelector('.inbox-bar')") is True:
                stable += 1
                if stable >= 3:
                    break
            else:
                stable = 0
            time.sleep(0.3)
        env.check("删除后队列清空且摘要条持续消失", stable >= 3,
                  "stable=%d badge=%r" % (stable, _badge(env)))

        _remote(env, "llm-complete", {"session": sid, "status": "completed"})
        time.sleep(0.4)
        errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
                if e.get("level") == "error"]
        env.check("排队组无前端错误", len(errs) == 0, errs[:3])
    finally:
        _restore_llms(env, base)
        _stop_hang(srv)


@case("S-CHAT-TOP-01", "会话消息", "输入区上方：任务进度条 / 后台通知 toast / 忙碌暂存 / 不静默续写",
      stage="seeded", order=310, fp="33 CHAT 输入区上方（test_chat_input_top）",
      desc="tool-progress → `.task-progress-bar` 显示 1/3 + 失败数；tool-notify 空闲即弹 toast；"
           "忙碌时通知暂存、`llm-complete` 后统一弹出；`llm-complete(incomplete)` 后末条 user "
           "仍是原消息（前端不自行发「继续」）。")
def s_chat_top_01(env):
    srv, port = _start_hang()
    base = _add_hang(env, port)
    try:
        _select(env, HANG_LLM)
        sid = "e2e-input-top-%s" % env.run_id
        _open_session(env, sid)

        _remote(env, "tool-progress",
                {"task_id": "t1", "completed": 1, "total": 3, "failed": 1})

        def _bar():
            return str(env.js("document.querySelector('.task-progress-bar')"
                              "?.textContent?.trim() || ''") or "")

        bar = env.wait(lambda: _bar() if "1/3" in _bar() else None,
                       "任务进度条 1/3", timeout=15)
        env.check("进度条含 1/3", "1/3" in bar, bar)
        env.check("进度条含失败数", ("1 个失败" in bar) or ("1 failed" in bar), bar)
        _remote(env, "tool-progress",
                {"task_id": "t1", "completed": 3, "total": 3, "failed": 0})
        time.sleep(0.4)

        _remote(env, "tool-notify", {"message": "后台任务已完成"})
        ts = env.wait(lambda: _toasts(env) if any("后台任务已完成" in t for t in _toasts(env))
                      else None, "空闲通知 toast 弹出", timeout=10)
        env.check("空闲通知已弹出", any("后台任务已完成" in t for t in ts), ts[:3])

        _send(env, sid, "busy turn", llm=HANG_LLM)
        env.wait(lambda: True if _running(env, sid) else None, "忙碌 turn 已在跑", timeout=25)
        _remote(env, "tool-notify", {"message": "忙碌期间的通知"})
        time.sleep(0.5)
        env.check("忙碌期间通知暂存（未弹）",
                  not any("忙碌期间的通知" in t for t in _toasts(env)), _toasts(env)[:3])
        _remote(env, "llm-complete", {"session": sid, "status": "completed"})
        ts2 = env.wait(lambda: _toasts(env) if any("忙碌期间的通知" in t for t in _toasts(env))
                       else None, "turn 结束后暂存通知弹出", timeout=15)
        env.check("暂存通知在 turn 后弹出", any("忙碌期间的通知" in t for t in ts2), ts2[:3])

        _send(env, sid, "写一篇长文", llm=HANG_LLM)
        _wait_users(env, 1)
        _remote(env, "llm-receive",
                {"type": "text", "text": "这是被截断的回复内容。", "session": sid})
        time.sleep(0.3)
        _remote(env, "llm-complete", {"session": sid, "status": "incomplete"})
        time.sleep(1.4)
        last = str(env.js("(()=>{const ms=[...document.querySelectorAll("
                          "'.message-item.user .message-content')];"
                          "const l=ms[ms.length-1];return l?l.textContent.trim():''})()") or "")
        env.eq("未完成不静默续写（末条 user 仍为原消息）", last, "写一篇长文")

        _remote(env, "llm-complete", {"session": sid, "status": "completed"})
        time.sleep(0.3)
        errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
                if e.get("level") == "error"]
        env.check("输入区上方组无前端错误", len(errs) == 0, errs[:3])
    finally:
        _restore_llms(env, base)
        _stop_hang(srv)


@case("S-TURN-NAV-01", "会话消息", "轮次导航圈：3 轮→3 圈、13 轮→10 圈+禁用、悬停一览、点击滚动",
      stage="seeded", order=320, fp="33 CHAT turn 导航（test_turn_nav）",
      desc="真实发送 3 轮 → `.turn-dot`=3；悬停首圈弹 Q0；补到 13 轮 → 10 圈且 3 个 disabled；"
           "悬停 disabled 不弹；悬停末活动圈弹 Q12；点末圈 scrollTop>0，点首圈回顶且首圈 active。")
def s_turn_nav_01(env):
    srv, port = _start_hang()
    base = _add_hang(env, port)
    try:
        _select(env, HANG_LLM)
        sid = "e2e-turnnav-%s" % env.run_id
        _open_session(env, sid)

        for n in range(3):
            _run_turn(env, sid, n, hang=True)
        env.eq("3 轮 → 3 圈", int(env.js("document.querySelectorAll('.turn-dot').length") or 0), 3)

        env.js("document.querySelectorAll('.turn-dot')[0]"
               ".dispatchEvent(new MouseEvent('mouseenter',{bubbles:true})); 'ok'")
        time.sleep(0.45)
        pop = env.js("!!document.querySelector('.turn-nav-pop')") is True
        ptxt = str(env.js("document.querySelector('.turn-nav-item')"
                          "?.textContent?.trim() || ''") or "")
        env.check("悬停首圈弹出一览且含 Q0", pop and "Q0" in ptxt, (pop, ptxt))
        env.js("document.querySelector('.turn-nav')"
               ".dispatchEvent(new MouseEvent('mouseleave',{bubbles:true})); 'ok'")
        time.sleep(0.3)

        for n in range(3, 13):
            _run_turn(env, sid, n, hang=True)
        dots = int(env.js("document.querySelectorAll('.turn-dot').length") or 0)
        dis = int(env.js("document.querySelectorAll('.turn-dot.disabled').length") or 0)
        env.check("13 轮 → 10 圈 + 3 置灰", dots == 10 and dis == 3,
                  "dots=%d disabled=%d" % (dots, dis))

        env.js("document.querySelectorAll('.turn-dot.disabled')[0]"
               ".dispatchEvent(new MouseEvent('mouseenter',{bubbles:true})); 'ok'")
        time.sleep(0.45)
        env.check("悬停禁用圈不弹", env.js("!document.querySelector('.turn-nav-pop')") is True)

        env.js("document.querySelectorAll('.turn-dot')[6]"
               ".dispatchEvent(new MouseEvent('mouseenter',{bubbles:true})); 'ok'")
        time.sleep(0.45)
        poptxt = str(env.js("(()=>[...document.querySelectorAll('.turn-nav-item')]"
                            ".map(e=>e.textContent.trim()).join('|'))()") or "")
        env.check("末活动圈一览含 Q12", "Q12" in poptxt, poptxt[:120])
        env.js("document.querySelector('.turn-nav')"
               ".dispatchEvent(new MouseEvent('mouseleave',{bubbles:true})); 'ok'")
        time.sleep(0.3)

        env.js("document.querySelectorAll('.turn-dot')[6].click(); 'ok'")
        time.sleep(0.7)
        st = int(env.js("document.querySelector('.message-list')?.scrollTop || 0") or 0)
        env.check("点末圈滚动到该轮（scrollTop>0）", st > 0, "scrollTop=%d" % st)

        env.js("document.querySelectorAll('.turn-dot')[0].click(); 'ok'")
        time.sleep(0.7)
        st0 = int(env.js("document.querySelector('.message-list')?.scrollTop || 0") or 0)
        act = int(env.js("(()=>{const ds=[...document.querySelectorAll('.turn-dot')];"
                         "return ds.findIndex(d=>d.classList.contains('active'))})()") or 0)
        env.check("点首圈回顶且首圈高亮", st0 <= 30 and act == 0,
                  "scrollTop=%d activeIdx=%d" % (st0, act))

        errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
                if e.get("level") == "error"]
        env.check("turn 导航组无前端错误", len(errs) == 0, errs[:3])
    finally:
        _restore_llms(env, base)
        _stop_hang(srv)
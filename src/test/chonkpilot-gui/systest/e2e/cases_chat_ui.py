# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑮：**主 chat UI / 富文本输入区**（迁移自 test_chat.py H1–H8 + test_richtext.py R1–R8）。

* 标题栏 / 场景 tag / 会话 tag / 新建会话清除 tag / 发送载荷 / 思考与努力度
* 富文本 placeholder / Shift+Enter / 粘贴图片 / 拖入文件 / 附件上传落盘 / 序列化发送 / 回显
"""

import json
import os
import time

from .framework import case
from .cases_chat_ops import (HANG_LLM, _add_hang, _open_session, _restore_llms,
                             _select, _send, _start_hang, _stop_hang)

INPUT = "document.querySelector('[contenteditable][data-drop-target=\"chat-input\"]')"


def _select_mock(env):
    env.js("window.mq.emit('chat-select-llm', {name: 'mock'}); 'ok'")
    time.sleep(0.2)


def _set_text(env, text):
    env.js("(()=>{const el=%s;el.innerHTML='';el.appendChild(document.createTextNode(%s));"
           "el.dispatchEvent(new Event('input',{bubbles:true}));return 'ok'})()"
           % (INPUT, json.dumps(text)))


def _text(env):
    return str(env.js("(%s).innerText || ''" % INPUT) or "")


def _send_button():
    return ("[...document.querySelectorAll('button')].find(b=>"
            "['Send','发送'].includes((b.textContent||'').trim()))")


def _click_send(env):
    return env.js("(()=>{const b=%s;if(!b)return 'no-btn';if(b.disabled)return 'disabled';"
                  "b.click();return 'ok';})()" % _send_button())


@case("S-CHAT-UI-01", "会话消息", "主 chat UI：标题栏/场景 tag/会话 tag/新建清空/发送载荷/思考努力度",
      stage="seeded", order=305, fp="33 主 chat 窗口（test_chat H1–H8）",
      desc="标题栏「主聊天/新建会话」；场景 tag 非空且点开列表 ≥1；session-changed → "
           "tag 显示 #前8 + title=完整 id；新建会话清 tag；输入文本点发送 → message-send 载荷"
           "含文本与 llm；think/effort 切换不报错。")
def s_chat_ui_01(env):
    _select_mock(env)
    body = str(env.js("document.body.textContent") or "")
    env.check("标题栏含主聊天/新建会话",
              (("主聊天" in body) or ("Main Chat" in body))
              and env.js("!!document.querySelector('.new-session-btn')") is True)

    scen = str(env.js("(()=>{const r=document.querySelector('.panel-header .b-popover__reference')"
                      "||document.querySelector('.b-popover__reference');"
                      "return r?r.textContent.trim():''})()") or "")
    env.check("场景 tag 非空且非「选择场景」",
              bool(scen) and scen not in ("选择场景", "Select scenario"), scen)

    env.js("document.querySelector('.panel-header .b-popover__reference')?.click(); 'ok'")
    time.sleep(0.6)
    items = int(env.js("document.querySelectorAll('.popover-item').length") or 0)
    env.check("场景选择列表 ≥1 项", items >= 1, items)
    env.js("document.body.click(); 'ok'")
    time.sleep(0.2)

    sid = "e2e-chatui-%s" % env.run_id
    env.js("window.mq.emit('session-changed', {session_id: %s}); 'ok'" % json.dumps(sid))
    tag = env.wait(lambda: env.jsj(
        "JSON.stringify((()=>{const t=document.querySelector('.chat-session-tag');"
        "return t?{text:t.textContent.trim(),title:t.getAttribute('title')||''}:null})())")
        if (env.jsj("JSON.stringify((()=>{const t=document.querySelector('.chat-session-tag');"
                    "return t?{text:t.textContent.trim(),title:t.getAttribute('title')||''}:null})())")
            or {}).get("title") == sid else None, "会话 tag 显示该会话", timeout=15)
    env.eq("会话 tag #前8", (tag or {}).get("text"), "#" + sid[:8])
    env.eq("会话 tag title = 完整 id", (tag or {}).get("title"), sid)

    h6 = env.jsj("""JSON.stringify((()=>{
      const ta=document.querySelector('[contenteditable][data-drop-target="chat-input"]');
      const btns=[...document.querySelectorAll('.input-actions-right button')];
      return {ta:!!ta,
              send:btns.some(b=>['Send','发送'].includes((b.textContent||'').trim())),
              think:document.querySelectorAll('.input-actions-left .icon-btn').length>=2}})())""")
    env.check("输入区富文本 + 发送 + 思考/努力度",
              bool(h6.get("ta")) and bool(h6.get("send")) and bool(h6.get("think")), h6)

    env.js("document.querySelector('.new-session-btn')?.click(); 'ok'")
    env.wait(lambda: True if env.js("!document.querySelector('.chat-session-tag')") is True
             else None, "新建会话清除 tag", timeout=15)
    env.check("新建会话清空会话 tag", env.js("!document.querySelector('.chat-session-tag')") is True)

    env.client.console(clear=True)
    env.js("window.__sendSpy=[];window.mq.post('message-send',"
           "d=>window.__sendSpy.push(d));'ok'")
    _set_text(env, "hello chat ui")
    time.sleep(0.3)
    r = _click_send(env)
    env.check("点击发送", r == "ok", r)
    spy = env.wait(lambda: env.jsj("JSON.stringify(window.__sendSpy)")
                   if (env.jsj("JSON.stringify(window.__sendSpy)") or []) else None,
                   "message-send 载荷", timeout=10)
    env.check("发送载荷文本", (spy[0] or {}).get("text") == "hello chat ui", spy[:1])
    env.check("发送载荷带 llm", bool((spy[0] or {}).get("llm")), spy[:1])

    # 收尾：等本轮结束（mock 回执），避免把「忙碌」留给下一个测试点
    env.wait(lambda: True if _send_visible(env) else None, "本轮结束 Send 恢复", timeout=60)
    env.js("window.mq.emit('chat-toggle-think'); window.mq.emit('chat-toggle-effort'); 'ok'")
    time.sleep(0.3)
    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error" and "SetActiveSessionID" not in (e.get("text") or "")]
    env.check("主 chat UI 组无前端错误", len(errs) == 0, errs[:3])


@case("S-CHAT-RICH-01", "会话消息", "富文本输入：placeholder/Shift+Enter/图片与文件附件/上传落盘/序列化发送",
      stage="seeded", order=306, fp="33 富文本输入区（test_richtext R1–R8）",
      desc="placeholder 非空；输入后 Send 可用；Shift+Enter 不发送；粘贴图片 → dataURL 缩略图且"
           "上传回执清 pending；拖入文件 → chip 可删除；`<data_root>/tmp/uploads` 新增文件；"
           "发送载荷以 `![名](路径)` 开头且含正文；`chat-queue-restore` 回显附件且正文无标记。")
def s_chat_rich_01(env):
    _select_mock(env)
    # 前置：上一测试点可能留下未结束轮次 → 等发送按钮恢复可用
    env.wait(lambda: True if not _cancel_visible(env) else None,
             "上一轮已结束（无 Cancel）", timeout=60)
    r1 = env.jsj("""JSON.stringify((()=>{const el=%s;
      const cs=getComputedStyle(el,'::before');
      return {el:!!el,ph:cs.content||''}})())""" % INPUT)
    env.check("富文本输入区存在", bool(r1.get("el")))
    env.check("空态 placeholder 非空", "none" not in str(r1.get("ph"))
              and str(r1.get("ph")) != "", r1.get("ph"))

    _set_text(env, "hello richtext")
    time.sleep(0.3)
    disabled = env.js("(()=>{const b=%s;return b?!!b.disabled:null})()" % _send_button())
    env.check("输入后发送按钮可用", disabled is False, disabled)

    env.js("(%s).dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',shiftKey:true,"
           "bubbles:true,cancelable:true})); 'ok'" % INPUT)
    time.sleep(0.3)
    env.check("Shift+Enter 不发送（文本保持）", "hello richtext" in _text(env), _text(env))

    upload_root = os.path.join(env.data_root, "tmp", "uploads")
    before = set(os.listdir(upload_root)) if os.path.isdir(upload_root) else set()

    # 粘贴图片 → dataURL 缩略图 + pending 清除
    env.js("""(()=>{const dt=new DataTransfer();
      const bin=new Uint8Array([137,80,78,71,13,10,26,10,0,0,0,13,73,72,68,82]);
      dt.items.add(new File([bin], 'e2e-shot.png', {type:'image/png'}));
      const ev=new ClipboardEvent('paste',{clipboardData:dt,bubbles:true,cancelable:true});
      (%s).dispatchEvent(ev);return 'ok'})()""" % INPUT)
    stamp = env.wait(lambda: env.jsj("""JSON.stringify((()=>{
        const c=document.querySelector('.attach-chip.attach-image .attach-thumb');
        const chip=c?c.closest('.attach-chip'):null;
        return {n:document.querySelectorAll('.attach-chip.attach-image').length,
                src:c?(c.getAttribute('src')||''):'',
                pending:chip?chip.classList.contains('pending'):null}})())""")
        if (lambda v: int(v.get("n") or 0) >= 1
            and str(v.get("src") or "").startswith("data:image/")
            and v.get("pending") is False)(env.jsj("""JSON.stringify((()=>{
                const c=document.querySelector('.attach-chip.attach-image .attach-thumb');
                const chip=c?c.closest('.attach-chip'):null;
                return {n:document.querySelectorAll('.attach-chip.attach-image').length,
                        src:c?(c.getAttribute('src')||''):'',
                        pending:chip?chip.classList.contains('pending'):null}})())"""))
        else None, "图片附件缩略图（回执已到）", timeout=15)
    env.check("图片附件缩略图为 dataURL 且 pending 清除",
              str((stamp or {}).get("src") or "").startswith("data:image/")
              and (stamp or {}).get("pending") is False, stamp)

    # 拖入文件 → chip → 删除
    env.js("""(()=>{const dt=new DataTransfer();
      dt.items.add(new File(['hello'], 'e2e-notes.txt', {type:'text/plain'}));
      const ev=new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:dt});
      (%s).dispatchEvent(ev);return 'ok'})()""" % INPUT)
    env.wait(lambda: True if int(env.js(
        "document.querySelectorAll('.attach-chip.attach-file').length") or 0) >= 1 else None,
        "文件附件 chip", timeout=15)
    env.check("拖入文件 → 文件 chip", int(env.js(
        "document.querySelectorAll('.attach-chip.attach-file').length") or 0) >= 1)
    env.wait(lambda: True if int(env.js(
        "document.querySelectorAll('.attach-chip.attach-file .attach-remove').length")
        or 0) >= 1 else None, "附件删除入口就绪", timeout=15)
    env.js("document.querySelector('.attach-chip.attach-file .attach-remove')?.click(); 'ok'")
    env.wait(lambda: True if int(env.js(
        "document.querySelectorAll('.attach-chip.attach-file').length") or 0) == 0 else None,
        "文件附件已删除", timeout=10)
    env.check("附件可删除", int(env.js(
        "document.querySelectorAll('.attach-chip.attach-file').length") or 0) == 0)

    new_uploads = sorted(f for f in (set(os.listdir(upload_root)) - before)
                         if not f.startswith(".")) if os.path.isdir(upload_root) else []
    env.check("附件上传落盘（tmp/uploads 新增）", len(new_uploads) >= 1, new_uploads[:3])

    # 序列化发送：图片在前 + 文本在后
    _set_text(env, "看图解释一下")
    time.sleep(0.3)
    env.js("window.__sendSpy=[];window.mq.post('message-send',"
           "d=>window.__sendSpy.push(d));'ok'")
    env.check("点击发送", _click_send(env) == "ok")
    spy = env.wait(lambda: env.jsj("JSON.stringify(window.__sendSpy)")
                   if (env.jsj("JSON.stringify(window.__sendSpy)") or []) else None,
                   "message-send 载荷", timeout=10)
    tx = str(((spy or [{}])[0] or {}).get("text") or "")
    env.check("发送序列化以图片标记开头", tx.startswith("!["), tx[:80])
    env.check("序列化含 .png) 与正文", ".png)" in tx and "看图解释一下" in tx, tx[:120])

    # 回显：markdown 图片标记 → 缩略图 + 正文无标记
    uploads = [f for f in os.listdir(upload_root) if f.endswith(".png")] \
        if os.path.isdir(upload_root) else []
    env.check("存在已上传 png", bool(uploads), uploads[:3])
    if uploads:
        png = os.path.join(upload_root, uploads[-1]).replace("\\", "/")
        env.js("window.mq.emit('chat-queue-restore', {text: %s}); 'ok'"
               % json.dumps("![截图](%s)\n附件回显测试" % png))
        env.wait(lambda: True if int(env.js(
            "document.querySelectorAll('.attach-chip.attach-image').length") or 0) >= 1
            else None, "回显图片附件", timeout=10)
        ed = _text(env)
        env.check("回显缩略图 + 正文无标记",
                  int(env.js("document.querySelectorAll('.attach-chip.attach-image').length")
                      or 0) >= 1 and "![截图]" not in ed and "附件回显测试" in ed, ed[:80])

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("富文本组无前端错误", len(errs) == 0, errs[:3])


def _remote(env, typ, payload):
    env.js("window.mq.emitRemote({type: %s, payload: %s}); 'ok'"
           % (json.dumps(typ), json.dumps(json.dumps(payload))))


def _last_user(env):
    return str(env.js("(()=>{const es=[...document.querySelectorAll("
                      "'.message-item.user .message-content')];"
                      "const e=es[es.length-1];return e?e.textContent.trim():''})()") or "")


def _cancel_visible(env):
    return env.js("(()=>[...document.querySelectorAll('.input-actions-right button')]"
                  ".some(b=>['Cancel','取消'].includes((b.textContent||'').trim())))()") is True


def _send_visible(env):
    return env.js("(()=>[...document.querySelectorAll('.input-actions-right button')]"
                  ".some(b=>['Send','发送'].includes((b.textContent||'').trim())))()") is True


@case("S-CHAT-FLOW-01", "会话消息", "消息流：user 气泡/忙碌/思考折叠/markdown/空回复/错误提示/自动滚底",
      stage="seeded", order=307, fp="33 主 chat 内容区（test_chat_flow CF1–CF7）",
      desc="挂起 LLM 保持忙碌：发送 → user 气泡 + Cancel + turn-start；reason → 思考段可折叠/展开；"
           "assistant markdown bold/code 渲染；complete → pending 清 + Send 恢复；空回复 → 提示+继续；"
           "401 不可恢复错误 → 下方提示条（摘要/详情/无气泡）+继续；长消息自动滚到底部。")
def s_chat_flow_01(env):
    srv, port = _start_hang()
    base = _add_hang(env, port)
    try:
        _select(env, HANG_LLM)
        sid = "e2e-flow-%s" % env.run_id
        _open_session(env, sid)
        env.js("window.__turnStarts=[];window.mq.on('turn-start',"
               "d=>window.__turnStarts.push(d));'ok'")

        # CF1 发送 → user 气泡 + 忙碌 + turn-start
        _send(env, sid, "帮我写一个排序函数", llm=HANG_LLM)
        env.wait(lambda: True if _last_user(env) == "帮我写一个排序函数" else None,
                 "user 气泡渲染", timeout=20)
        starts = env.wait(lambda: env.jsj("JSON.stringify(window.__turnStarts)")
                          if sid in str(env.jsj("JSON.stringify(window.__turnStarts)") or [])
                          else None, "turn-start 回填", timeout=15)
        env.check("发送后 user 气泡 + turn-start",
                  _last_user(env) == "帮我写一个排序函数" and sid in str(starts), starts)
        env.wait(lambda: True if _cancel_visible(env) else None, "忙碌 Cancel 按钮", timeout=15)
        env.check("忙碌态显示 Cancel", _cancel_visible(env))

        # CF2 思考过程折叠/展开
        _remote(env, "llm-receive",
                {"type": "reason", "text": "用户想要一个排序函数，先考虑算法选择。",
                 "session": sid})
        body = env.wait(lambda: str(env.js("document.querySelector("
                                           "'.reasoning-section .section-body')"
                                           "?.textContent?.trim() || ''") or "")
                        if "排序" in str(env.js("document.querySelector("
                                                "'.reasoning-section .section-body')"
                                                "?.textContent?.trim() || ''")) else None,
                        "思考过程渲染", timeout=15)
        env.check("思考过程渲染", "排序" in body, body[:40])
        env.js("document.querySelector('.reasoning-section .section-header')?.click(); 'ok'")
        env.wait(lambda: True if env.js("!document.querySelector("
                                        "'.reasoning-section .section-body')") is True else None,
                 "思考折叠", timeout=10)
        env.check("思考过程折叠", env.js("!document.querySelector("
                                        "'.reasoning-section .section-body')") is True)
        env.js("document.querySelector('.reasoning-section .section-header')?.click(); 'ok'")
        env.wait(lambda: True if env.js("!!document.querySelector("
                                        "'.reasoning-section .section-body')") is True else None,
                 "思考展开", timeout=10)
        env.check("思考过程展开", env.js("!!document.querySelector("
                                        "'.reasoning-section .section-body')") is True)

        # CF3 markdown
        _remote(env, "llm-receive",
                {"type": "text",
                 "text": "推荐使用**快速排序**，示例：`quickSort(arr)`。", "session": sid})
        md = env.wait(lambda: env.jsj("""JSON.stringify((()=>{
            const es=[...document.querySelectorAll('.message-item.assistant .message-content')];
            const e=es[es.length-1];if(!e)return {strong:0,code:0};
            return {strong:e.querySelectorAll('strong').length,
                    code:e.querySelectorAll('code').length}})())""")
                 if (lambda v: int(v.get("strong") or 0) >= 1
                     and int(v.get("code") or 0) >= 1)(env.jsj("""JSON.stringify((()=>{
            const es=[...document.querySelectorAll('.message-item.assistant .message-content')];
            const e=es[es.length-1];if(!e)return {strong:0,code:0};
            return {strong:e.querySelectorAll('strong').length,
                    code:e.querySelectorAll('code').length}})())""")) else None,
                 "assistant markdown 渲染", timeout=15)
        env.check("markdown strong/code", int(md.get("strong") or 0) >= 1
                  and int(md.get("code") or 0) >= 1, md)

        # CF4 轮次结束
        _remote(env, "llm-complete", {"session": sid, "status": "completed"})
        env.wait(lambda: True if int(env.js(
            "document.querySelectorAll('.message-item.pending').length") or 0) == 0
            else None, "pending 清除", timeout=15)
        env.wait(lambda: True if _send_visible(env) else None, "Send 恢复", timeout=15)
        env.check("轮次结束 pending=0 + Send 恢复",
                  int(env.js("document.querySelectorAll('.message-item.pending').length") or 0) == 0
                  and _send_visible(env))

        # CF5 空回复
        _send(env, sid, "这条不会有回复", llm=HANG_LLM)
        env.wait(lambda: True if _last_user(env) == "这条不会有回复" else None,
                 "空回复 user 气泡", timeout=15)
        _remote(env, "llm-complete", {"session": sid, "status": "complete"})
        hint = env.wait(lambda: str(env.js("document.querySelector('.empty-reply-hint')"
                                           "?.textContent?.trim() || ''") or None),
                        "空回复提示", timeout=15)
        env.check("空回复提示 + 继续按钮",
                  bool(hint) and env.js("!!document.querySelector('.continue-btn')") is True,
                  hint[:40])

        # CF6 不可恢复错误（401）：提示条 + 详情 + 无气泡 + 继续
        _send(env, sid, "触发 401", llm=HANG_LLM)
        env.wait(lambda: True if _last_user(env) == "触发 401" else None,
                 "错误轮 user 气泡", timeout=15)
        for _ in range(5):
            if env.js("!!document.querySelector("
                      "'.llm-fail-hint.llm-error-hint .llm-fail-text')") is True:
                break
            _remote(env, "llm-error", {"retryable": False, "message": "invalid api key"})
            _remote(env, "llm-complete", {"session": sid, "status": "error",
                                          "message": "invalid api key",
                                          "retry_attempt": 0, "retry_count": 0,
                                          "retry_wait_ms": 0})
            time.sleep(0.8)
        human = env.wait(lambda: str(env.js("document.querySelector("
                                            "'.llm-fail-hint.llm-error-hint .llm-fail-text')"
                                            "?.textContent?.trim() || ''") or None),
                         "错误提示条", timeout=15)
        detail = env.wait(lambda: True if env.js(
            "!!document.querySelector('.llm-fail-detail-toggle')") is True else None,
            "错误详情入口", timeout=10)
        env.js("document.querySelector('.llm-fail-detail-toggle')?.click(); 'ok'")
        raw = env.wait(lambda: True if env.js(
            "document.querySelector('.llm-fail-raw')?.textContent?.includes('invalid api key')")
            is True else None, "原始错误详情", timeout=10)
        no_bubble = env.js("document.querySelectorAll("
                           "'.message-item.assistant .error-bubble').length === 0") is True
        env.check("401 → 提示条 + 详情 + 无错误气泡 + 继续",
                  bool(human) and detail is True and raw is True and no_bubble
                  and env.js("!!document.querySelector('.continue-btn')") is True,
                  human[:60])

        # CF7 自动滚底
        _send(env, sid, "最后一条很长的消息" + "内容" * 20, llm=HANG_LLM)
        env.wait(lambda: True if "最后一条很长的消息" in _last_user(env) else None,
                 "长消息 user 气泡", timeout=15)
        _remote(env, "llm-receive",
                {"type": "text", "text": "这是最后一条回复 " + "内容" * 30, "session": sid})
        near = env.wait(lambda: True if env.js(
            "(()=>{const el=document.querySelector('.message-list');"
            "if(!el)return false;return el.scrollHeight-el.scrollTop-el.clientHeight<40})()")
            is True else None, "消息区接近底部", timeout=15)
        env.check("会话进行中自动滚到底部", near is True)

        _remote(env, "llm-complete", {"session": sid, "status": "completed"})
    finally:
        _restore_llms(env, base)
        # 复位聊天选择：本点曾把 HANG_LLM 设为当前选择；hang provider 已随 restore 移除，
        # 不复位会让后续测试点（fields/retry…）的轮次仍打到已关闭的 hang 端口 → 永不 complete。
        _select_mock(env)
        _stop_hang(srv)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "invalid api key" not in (e.get("text") or "")]
    env.check("消息流组无前端错误（忽略注入的 401）", len(errs) == 0, errs[:3])


@case("S-CHAT-MSG-01", "会话消息", "空态提示 / 思考与工具调用记录：复制图标、pending→done 状态徽标",
      stage="seeded", order=308, fp="33 主 chat 内容区（run_chat_ui E1–E4）",
      desc="session-changed(null) → 主对话区「选择会话」空态；新会话无消息 → 「暂无消息」；"
           "reason 段实时展开且有复制图标；tool-call → `.toolpair-section` 含工具名、pending 徽标、"
           "复制图标，`tool-pair{status:done}` → done 徽标。")
def s_chat_msg_01(env):
    # E1 无会话空态（主对话区作用域；重发 null 直到空态可见）
    empty = None
    for _ in range(8):
        env.js("window.mq.emit('session-changed', {session_id: null}); 'ok'")
        empty = env.wait(lambda: str(env.js(
            "document.querySelector('.chat-panel .empty-prompt')"
            "?.textContent?.trim() || ''") or None), "无会话空态", timeout=3)
        if empty:
            break
    env.check("无会话 → 「选择会话」提示",
              bool(empty) and any(k in empty for k in ("选择会话", "Select a session")), empty)

    srv, port = _start_hang()
    base = _add_hang(env, port)
    try:
        _select(env, HANG_LLM)
        sid = "e2e-msgui-%s" % env.run_id
        _open_session(env, sid)

        # E2 有会话无消息
        txt = env.wait(lambda: str(env.js(
            "document.querySelector('.empty-inside')?.textContent?.trim() || ''") or None),
            "无消息提示", timeout=15)
        env.check("有会话无消息 → 「暂无消息」提示",
                  bool(txt) and any(k in txt for k in ("暂无消息", "No messages yet")), txt)

        # E3 思考段复制图标（实时展开）
        _send(env, sid, "思考复制图标测试", llm=HANG_LLM)
        env.wait(lambda: True if _last_user(env) == "思考复制图标测试" else None,
                 "思考测试 user 气泡", timeout=20)
        _remote(env, "llm-receive", {"type": "reason", "text": "先分析问题，再给出结论。",
                                     "session": sid})
        env.wait(lambda: True if env.js(
            "!!document.querySelector('.reasoning-section .section-body')") is True else None,
            "思考段实时展开", timeout=15)
        icon = env.wait(lambda: True if int(env.js(
            "document.querySelectorAll('.reasoning-section .copy-icon').length") or 0) >= 1
            else None, "思考段复制图标", timeout=10)
        env.check("思考段默认展开 + 复制图标",
                  env.js("!!document.querySelector('.reasoning-section .section-body')") is True
                  and icon is True)

        # 结束思考轮 → 发送恢复，才能进入下一轮
        _remote(env, "llm-complete", {"session": sid, "status": "completed"})
        env.wait(lambda: True if int(env.js(
            "document.querySelectorAll('.message-item.pending').length") or 0) == 0
            else None, "思考轮结束", timeout=15)
        env.wait(lambda: True if _send_visible(env) else None, "Send 恢复", timeout=15)

        # E4 工具调用记录：渲染 / pending / 复制图标 / done
        _send(env, sid, "工具调用记录测试", llm=HANG_LLM)
        env.wait(lambda: True if _last_user(env) == "工具调用记录测试" else None,
                 "工具测试 user 气泡", timeout=20)
        _remote(env, "llm-receive",
                {"type": "tool-call", "tool_call_id": "tc-e2e-msg-1",
                 "tool": "file_read", "simplified": "file_read", "purpose": "读取",
                 "arguments": "{\"targets\":[{\"path\":\"C:/work/proj/a.txt\"}],"
                              "\"tool_call_display_name\":\"读取\"}", "session": sid})
        env.wait(lambda: True if env.js("!!document.querySelector('.toolpair-section')")
                 is True else None, "工具调用记录渲染", timeout=15)
        tool_txt = str(env.js("document.querySelector('.toolpair-section')"
                              "?.textContent || ''") or "")
        env.check("工具调用记录含工具名", "file_read" in tool_txt, tool_txt[:80])
        env.check("工具调用初始 pending 徽标", env.js(
            "!!document.querySelector('.toolpair-section .status-badge.pending')") is True)
        env.check("工具调用记录有复制图标", int(env.js(
            "document.querySelectorAll('.toolpair-section .copy-icon').length") or 0) >= 1)

        _remote(env, "tool-pair", {"session_id": sid, "tool_id": "tc-e2e-msg-1",
                                   "status": "done"})
        done = env.wait(lambda: True if env.js(
            "!!document.querySelector('.toolpair-section .status-badge.done')") is True
            else None, "工具调用 done 徽标", timeout=15)
        env.check("tool-pair done → done 徽标", done is True)

        _remote(env, "llm-complete", {"session": sid, "status": "completed"})
    finally:
        _restore_llms(env, base)
        # 复位聊天选择：本点曾把 HANG_LLM 设为当前选择；hang provider 已随 restore 移除，
        # 不复位会让后续测试点（fields/retry…）的轮次仍打到已关闭的 hang 端口 → 永不 complete。
        _select_mock(env)
        _stop_hang(srv)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("消息渲染组无前端错误", len(errs) == 0, errs[:3])


@case("S-TOOL-RETRY-01", "会话消息", "工具中断 → 重试按钮 → tool-retry{session,turn} 契约",
      stage="seeded", order=309, fp="33 主 chat 工具调用记录（test_tool_retry TR1–TR3）",
      desc="挂起 LLM 发送建轮 → tool-call 渲染 `.message-item.tool_pair`；轮次结束后 "
           "`tool-pair{status:interrupted}` → `.retry-tool-btn` 出现；点击重试 → 前端 post "
           "`tool-retry` 载荷含 session 与 turn（键集合 ⊆ {session,turn,instance_id}）。")
def s_tool_retry_01(env):
    srv, port = _start_hang()
    base = _add_hang(env, port)
    try:
        _select(env, HANG_LLM)
        sid = "e2e-retry-%s" % env.run_id
        _open_session(env, sid)
        _send(env, sid, "读取文件并分析", llm=HANG_LLM)
        env.wait(lambda: True if _last_user(env) == "读取文件并分析" else None,
                 "重试测试 user 气泡", timeout=20)

        _remote(env, "llm-receive",
                {"type": "tool-call", "session": sid, "tool_call_id": "tc-retry-1",
                 "tool": "file_read", "arguments": "{\"targets\":[{\"path\":\"a.txt\"}]}",
                 "purpose": "读取"})
        pair = env.wait(lambda: True if env.js(
            "!!document.querySelector('.message-item.tool_pair')") is True else None,
            "工具调用记录渲染", timeout=15)
        env.check("工具调用记录渲染（运行中）", pair is True)

        _remote(env, "llm-complete", {"session": sid, "status": "completed"})
        _remote(env, "tool-pair", {"tool_id": "tc-retry-1", "status": "interrupted"})
        retry = env.wait(lambda: True if env.js(
            "!!document.querySelector('.retry-tool-btn')") is True else None,
            "中断后重试按钮", timeout=15)
        env.check("中断 → 重试按钮出现", retry is True)

        env.js("window.__retrySpy=[];window.mq.post('tool-retry',"
               "d=>window.__retrySpy.push(d));'ok'")
        env.js("document.querySelector('.retry-tool-btn')?.click(); 'ok'")
        spy = env.wait(lambda: env.jsj("JSON.stringify(window.__retrySpy)")
                       if (env.jsj("JSON.stringify(window.__retrySpy)") or []) else None,
                       "tool-retry 载荷", timeout=10)
        # 61 §0：业务载荷「只增」+ 前端 mq 自动补 work_dir/data_dir 上下文键（多 workdir
        # 批次起），故判据 = 必需键在场、且不含业务外自定义键（而非精确键集合）。
        keys = set((spy[0] or {}).keys())
        ok = (isinstance(spy, list) and len(spy) == 1
              and (spy[0] or {}).get("session") == sid
              and isinstance((spy[0] or {}).get("turn"), str)
              and {"session", "turn"} <= keys
              and keys <= {"session", "turn", "task_id", "tool_call_id",
                           "instance_id", "work_dir", "data_dir"})
        env.check("tool-retry 载荷 = {session,turn} 契约", ok, spy)
        _remote(env, "llm-complete", {"session": sid, "status": "completed"})
    finally:
        _restore_llms(env, base)
        # 复位聊天选择：本点曾把 HANG_LLM 设为当前选择；hang provider 已随 restore 移除，
        # 不复位会让后续测试点（fields/retry…）的轮次仍打到已关闭的 hang 端口 → 永不 complete。
        _select_mock(env)
        _stop_hang(srv)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error" and "method not implemented" not in (e.get("text") or "")]
    env.check("工具重试组无前端错误", len(errs) == 0, errs[:3])

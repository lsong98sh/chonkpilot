"""回归测试：消息列表交互（51-FP与测试映射「主chat 对话内容区」mock_llm 事件驱动）。

覆盖：
- 发送 → **落库回执后**渲染 user 气泡（右侧）+ 忙碌态（Cancel 按钮）
- 思考过程渲染 + 折叠/展开
- assistant markdown 渲染（**bold** → <strong>，`code` → <code>）
- 轮次结束：pending 清除 + 恢复 Send
- 空回复 →「(空回复)」+「继续」按钮
- 不可恢复错误（401）→ Error 气泡 +「继续」按钮
- 会话进行中自动滚到底部
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, snapshot_config, restore_config, ensure_llm_configured  # noqa: E402  动态端口；套件级配置快照-还原；LLM 前置
from harness import reload_and_settle  # noqa: E402  共享 work-dir 大会话恢复会长时间占满渲染线程
from harness import install_session_history_spy, wait_session_history  # noqa: E402  切会话→发送竞态（历史回填覆盖）

PORT = free_port()
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_flow_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def emit_remote(gui, typ, payload):
    J(gui, "window.mq.emitRemote({type: %s, payload: %s}); 'ok'" % (
        json.dumps(typ), json.dumps(json.dumps(payload))))


def send_msg(gui, sid, text):
    J(gui, "window.mq.emit('message-send', {sessionId: %s, text: %s}); 'ok'" % (
        json.dumps(sid), json.dumps(text)))
    time.sleep(0.4)


def complete_turn(gui, sid, status='completed', message='', **extra):
    p = {'session': sid, 'status': status}
    if message:
        p['message'] = message
    p.update(extra)  # RETRY-1：retry_attempt / retry_count / retry_wait_ms（真实计数）
    emit_remote(gui, 'llm-complete', p)
    time.sleep(0.5)


def wait_upto(gui, js, ok, max_wait=10.0, interval=0.2):
    """有界轮询：轮询 js 求值直到 ok(v) 为真或超时；返回末次值。

    I-92 低风险加固（2026-09-19）：把「事件注入后渲染断言」前的**固定 sleep**改为**有界轮询**
    —— 只改**何时**读，不改**读什么**（断言条件仍在调用处，强度不变）。
    L4 时序加固（2026-09-23，[42 §2 (148)]）：max_wait 由 6.0s 上调至 10.0s 并覆盖全部
    事件→渲染断言点。实测（负载下）：llm-error+complete → `.error-bubble` max **0.632s**
    （原固定 sleep 0.50s，**超预算**）→ CF6 偶发红的根因即「读早于渲染」。
    """
    deadline = time.time() + max_wait
    v = J(gui, js)
    while not ok(v) and time.time() < deadline:
        time.sleep(interval)
        v = J(gui, js)
    return v


def switch_session(gui, sid, max_wait=25.0, stable=1.5):
    """把主会话切到 sid 并确认**稳定生效**（返回是否稳定）。

    首屏/配置重载后 `App.init` 会**异步** emit 一次 `session-changed`（读活动会话）——
    可能与测试注入的切换交错并把它覆盖回去（表现为 CF1 气泡/Cancel 丢失、`message-send`
    因 props 不匹配被静默丢弃）。故重发切换直到 `.chat-session-tag` 连续 `stable` 秒保持 sid。
    """
    end = time.time() + max_wait
    since, last_emit = None, 0.0
    while time.time() < end:
        tag = J(gui, "(()=>{const t=document.querySelector('.chat-session-tag');return t?t.getAttribute('title'):null})()")
        now = time.time()
        if tag == sid:
            if since is None:
                since = now
            elif now - since >= stable:
                return True
        else:
            since = None
            if now - last_emit >= 1.0:
                J(gui, "window.mq.emit('session-changed', {session_id: %s}); 'ok'" % json.dumps(sid))
                last_emit = now
        time.sleep(0.3)
    return False


def wait_session_active(gui, sid, max_wait=15.0):
    """有界等待「会话身份已生效」（`.chat-session-tag` title == sid）。

    前置必要性：`MessageList.vue:553` `if (data.sessionId !== props.sessionId) return` ——
    `message-send` 在 props 尚未跟上时被**静默丢弃**（无用户气泡、无忙碌态、无重试）。
    该值由 `ChatPanel.currentSessionId` 同一次渲染刷新供应（`.chat-session-tag` 即其 DOM 投影），
    故等到它等于 sid ⇒ props.sessionId 必已等于 sid。原实现靠固定 `sleep(0.8)` 赌竞态。
    """
    return wait_upto(gui, "(()=>{const t=document.querySelector('.chat-session-tag');return t?t.getAttribute('title'):null})()",
                     lambda v: v == sid, max_wait=max_wait)


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    # 独立 work-dir（与 test_queue / test_task / test_session_drawer 同口径）：共享 ws 会继承
    # 他套件遗留的「运行中轮次 / 大历史会话」→ `llm-inbox-add` 恒 pending（无 turn-start，CF1 假红）。
    gui = GUIClient(port=PORT, data_dir=DATA_DIR, work_dir=os.path.join(DATA_DIR, "ws"))
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：prj 随独立临时 work-dir，usr 主库不受其隔离 → 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        # 共享 work-dir 的活动会话可能已被其他套件写成大历史（MB 级 HTML）：首屏与
        # `ensure_llm_configured` 内部 reload 都会按 `active_session_id` 恢复它 →
        # WebView2 渲染线程长时间占满、`/eval` 超时、`message-send` 被静默丢弃
        # （CF1/CF1b 假红的根因，见 42 (290)）。先建一个空会话并把活动位指向它，
        # 再稳定重载一次（`active-set` 指向不存在的会话会被 active-get 判失效并回落「最近会话」，
        # 故必须先 ensure-session 建行）。
        try:
            gui.req("data-session-ensure-session", {"data": {"session_id": "flow-bootstrap"}}, timeout=8000)
            gui.req("data-session-active-set", {"session_id": "flow-bootstrap"}, timeout=8000)
        except Exception:
            pass
        reload_and_settle(gui)
        snap = snapshot_config(gui)
        ensure_llm_configured(gui)  # S25 前置：无 LLM 选项 → 发送被拦截（CF1 断言发送）
        for _ in range(30):
            if J(gui, "document.querySelector('.panel-inner')") and J(gui, "document.querySelector('[contenteditable=\"true\"]')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # 会话 id 每次运行唯一：共享 work-dir 下固定 id 会继承上一轮遗留的「运行中轮次」
        # → `llm-inbox-add` 被判 insert/排队（无 turn-start、无忙碌态，CF1 假红）。
        sid = 'flow-sess-%d' % int(time.time() * 1000)
        install_session_history_spy(gui)  # 必须先装：记录本次切会话触发的 data-session-history 应答
        switch_session(gui, sid)          # 切到测试会话并等其**稳定**生效（防 App.init 异步回填覆盖）
        wait_session_history(gui, sid)  # 等历史回填落地再发（发送与历史回填交错会让气泡落点不可预期）

        # CF1 发送 → user 气泡（右侧）+ 忙碌（Cancel 按钮出现）
        #（T5 方案 B：user 气泡不再乐观插入 —— 以「落库回执」（llm-start /publish 应答）为渲染时点，
        #  故断言必须**有界等待**（实测回执延迟中位 ~30ms / 最大 <100ms，预算 15s 富余）；
        #  断言条件与强度不变，只是把「读」的下界从"同步渲染"改为"回执后渲染"。）
        J(gui, "window.__turnStarts=[];window.mq.on('turn-start', d=>window.__turnStarts.push(d));'ok'")
        send_msg(gui, sid, '帮我写一个排序函数')
        uBubble = wait_upto(gui, """(()=>{const els=Array.from(document.querySelectorAll('.message-item.user .message-content'));const last=els[els.length-1];return last?last.textContent.trim():''})()""",
                            lambda v: v == '帮我写一个排序函数', max_wait=15.0)
        # Q6：空闲首条也先入 inbox → 服务端 consumeNextTurn 起轮 → turn-start 回填 turn id。
        starts = wait_upto(gui, "JSON.stringify((window.__turnStarts||[]).map(d=>d.session||d.session_id))",
                           lambda v: sid in str(v), max_wait=10.0)
        c.check("CF1b 空闲发送先入 inbox → 服务端起轮（turn-start 回填）", sid in str(starts),
                "starts=%r" % (starts,))
        cancelBtn = wait_upto(gui, """(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-right button'));return bs.some(b=>(b.textContent||'').trim()==='Cancel'||(b.textContent||'').trim()==='取消')})()""",
                              lambda v: v is True)
        _btns = J(gui, "JSON.stringify(Array.from(document.querySelectorAll('.input-actions-right button')).map(b=>(b.textContent||'').trim()))")
        c.check("CF1 发送即显 user 气泡 + 忙碌 Cancel",
                uBubble == '帮我写一个排序函数' and bool(cancelBtn),
                "bubble=%r cancel=%r buttons=%s" % (uBubble, cancelBtn, _btns))

        # CF2 思考过程：reason → .reasoning-section 渲染 + 折叠/展开
        emit_remote(gui, 'llm-receive', {'type': 'reason', 'text': '用户想要一个排序函数，先考虑算法选择。', 'session': sid})
        reasonBody = wait_upto(gui, "document.querySelector('.reasoning-section .section-body')?.textContent?.trim() || ''",
                               lambda v: '排序' in (v or ''))
        c.check("CF2a 思考过程渲染", '排序' in reasonBody, f"r={repr(reasonBody)[:40]}")
        J(gui, "document.querySelector('.reasoning-section .section-header')?.click(); 'ok'")
        folded = wait_upto(gui, "!document.querySelector('.reasoning-section .section-body')",
                           lambda v: v is True)
        c.check("CF2b 思考过程折叠", bool(folded))
        J(gui, "document.querySelector('.reasoning-section .section-header')?.click(); 'ok'")
        unfolded = wait_upto(gui, "!!document.querySelector('.reasoning-section .section-body')",
                             lambda v: v is True)
        c.check("CF2c 思考过程展开", bool(unfolded))

        # CF3 assistant markdown 渲染：**bold** → <strong>；`code` → <code>
        #（I-92 低风险加固：注入后**有界轮询**等末条 assistant 消息渲染出 strong+code，替代固定 sleep）
        emit_remote(gui, 'llm-receive', {'type': 'text', 'text': '推荐使用**快速排序**，示例：`quickSort(arr)`。', 'session': sid})
        mdjs = """(()=>{const els=Array.from(document.querySelectorAll('.message-item.assistant .message-content'));const last=els[els.length-1];if(!last)return {strong:0,code:0};return {strong:last.querySelectorAll('strong').length,code:last.querySelectorAll('code').length,html:last.innerHTML.slice(0,200)}})()"""
        md = wait_upto(gui, mdjs,
                       lambda v: isinstance(v, dict) and v.get('strong', 0) >= 1 and v.get('code', 0) >= 1,
                       max_wait=6.0)
        c.check("CF3 markdown 渲染 strong/code", bool(md) and md['strong'] >= 1 and md['code'] >= 1, repr(md)[:120])

        # CF4 轮次结束：pending 清除 + 恢复 Send（忙碌结束）
        complete_turn(gui, sid)
        pending = wait_upto(gui, "document.querySelectorAll('.message-item.pending').length",
                            lambda v: int(v or 0) == 0)
        sendBtn = wait_upto(gui, """(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-right button'));return bs.some(b=>(b.textContent||'').trim()==='Send'||(b.textContent||'').trim()==='发送')})()""",
                            lambda v: v is True)
        c.check("CF4 轮次结束：pending 清除 + 恢复 Send", int(pending) == 0 and bool(sendBtn), f"pending={pending} send={sendBtn}")

        # CF5 空回复：complete 无输出 → (空回复) + 继续按钮
        send_msg(gui, sid, '这条不会有回复')
        complete_turn(gui, sid, status='complete')
        hint = wait_upto(gui, "document.querySelector('.empty-reply-hint')?.textContent?.trim() || ''",
                         lambda v: bool(v))
        contBtn = wait_upto(gui, "!!document.querySelector('.continue-btn')", lambda v: v is True)
        c.check("CF5 空回复 → 提示 + 继续按钮", bool(hint) and bool(contBtn), f"hint={repr(hint)[:30]} cont={contBtn}")

        # CF6 不可恢复错误（401）：llm-error retryable=false + llm-complete error
        # ERRUX-1 口径（2026-10-17）：错误**只在下方提示条**显示（聊天流内不再渲染错误气泡）；
        # 提示条 = 人话摘要 + retry 次数 +「查看错误信息」（展开原始错误）。
        send_msg(gui, sid, '触发 401')
        # 批跑时序加固（2026-10-26）：本用例注入的 llm-error/llm-complete(error) 与
        # **真实 turn 的终态**可能交错——真实 turn 后到的 completed/EMPTY_REPLY 会按产品
        # 口径清掉错误提示条（`onLlmCompleteHint` 成功/空回复分支）→ 读到 human=''。
        # 故**同一场景重放**（同 payload、既有事件面）直到提示条稳定出现，断言不变。
        for _ in range(5):
            if J(gui, "!!document.querySelector('.llm-fail-hint.llm-error-hint .llm-fail-text')"):
                break
            emit_remote(gui, 'llm-error', {'retryable': False, 'message': 'invalid api key'})
            complete_turn(gui, sid, status='error', message='invalid api key',
                          retry_attempt=0, retry_count=0, retry_wait_ms=0)
            time.sleep(0.8)
        human = wait_upto(gui, "document.querySelector('.llm-fail-hint.llm-error-hint .llm-fail-text')?.textContent?.trim() || ''",
                          lambda v: bool(v))
        detailBtn = wait_upto(gui, "!!document.querySelector('.llm-fail-detail-toggle')",
                              lambda v: v is True)
        J(gui, "document.querySelector('.llm-fail-detail-toggle')?.click(); 'ok'")
        rawDetail = wait_upto(gui, "document.querySelector('.llm-fail-raw')?.textContent?.includes('invalid api key') || false",
                              lambda v: v is True)
        noBubble = wait_upto(gui, "document.querySelectorAll('.message-item.assistant .error-bubble').length === 0",
                             lambda v: v is True)
        contBtn = wait_upto(gui, "!!document.querySelector('.continue-btn')", lambda v: v is True)
        c.check("CF6 不可恢复错误 → 只在下方提示条（摘要+详情+retry 次数）+ 继续按钮，聊天流无气泡",
                bool(human) and bool(detailBtn) and bool(rawDetail) and bool(noBubble) and bool(contBtn),
                f"human={repr(human)[:40]} detailBtn={detailBtn} raw={rawDetail} noBubble={noBubble} cont={contBtn}")

        # CF7 会话进行中自动滚到底部（发送新 turn 后接近底部）
        send_msg(gui, sid, '最后一条很长的消息' + '内容' * 20)
        emit_remote(gui, 'llm-receive', {'type': 'text', 'text': '这是最后一条回复 ' + '内容' * 30, 'session': sid})
        nearBottom = wait_upto(gui, """(()=>{const el=document.querySelector('.message-list');if(!el)return false;return el.scrollHeight-el.scrollTop-el.clientHeight<40})()""",
                               lambda v: v is True)
        c.check("CF7 会话进行中自动滚到底部", bool(nearBottom))

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')
                and 'invalid api key' not in (e.get('text') or '')]
        c.check("无前端错误（消息列表交互组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

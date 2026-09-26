"""回归测试：task/ask_user 组（51-FP与测试映射「任务视图 / 终止会话 / ask_user 提问」）。

覆盖：
- 消息面契约：data-tasktree-tasks / data-tasktree-list / data-tasktree-delete（空库语义）
- 任务树渲染：tasks.* 事件驱动节点出现 / llm 节点耗时 / task 节点 stop
- 终止会话：busy 态 Cancel 按钮 → llm-cancel 发布
- ask_user：ask-user 事件 → 弹窗 → 选项+提交 → ask-user-reply 发布

发布观测：monkey-patch window.fetch 捕获 /publish 请求（window.__pubSpy）。
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import ensure_locale, free_port, snapshot_config, restore_config  # noqa: E402  动态端口 + 语言确定性（T12 Cancel 按英文文案断言）+ 套件级配置快照-还原
from harness import install_session_history_spy, wait_session_history  # noqa: E402  切会话→发送竞态（历史回填覆盖）

PORT = free_port()
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_task_data")

SPY_JS = """(()=>{
  if (window.__pubSpy) return 'exists';
  window.__pubSpy = [];
  const orig = window.fetch.bind(window);
  window.fetch = (...a) => {
    try {
      if (String(a[0]) === '/publish') {
        window.__pubSpy.push(JSON.parse(a[1].body));
      }
    } catch (e) {}
    return orig(...a);
  };
  return 'ok';
})()"""

GET_SPY = "window.__pubSpy || []"
CLEAR_SPY = "window.__pubSpy = []; 'ok'"


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def emit_remote(gui, typ, payload):
    J(gui, "window.mq.emitRemote({type: %s, payload: %s}); 'ok'" % (
        json.dumps(typ), json.dumps(json.dumps(payload))))


def payloads(spied, typ):
    """从 publish spy 中取指定 type 的已解析 payload 列表。"""
    out = []
    for p in spied:
        if p.get('type') != typ:
            continue
        raw = p.get('payload')
        try:
            out.append(json.loads(raw) if isinstance(raw, str) else raw)
        except Exception:
            out.append(raw)
    return out


def wait_upto(gui, js, ok, max_wait=10.0, interval=0.2):
    """有界轮询：轮询 js 求值直到 ok(v) 为真或超时；返回末次值。

    L4 时序加固（2026-09-23，[42 §2 (148)]）：把「事件注入后渲染断言」前的**固定 sleep**
    改为**有界轮询** —— 只改**何时**读，不改**读什么**（断言条件仍在调用处，强度不变）。
    实测（负载下，见 [42 §2 (148)] 证据）：ask-user → `.ask-user-content` max **0.981s**
    （原固定 sleep 1.00s）、llm-error+complete → `.error-bubble` max **0.632s**（原固定
    sleep 0.50s，**超预算**）→ 固定 sleep 余量不足即批内偶发红的根因。
    """
    deadline = time.time() + max_wait
    v = J(gui, js)
    while not ok(v) and time.time() < deadline:
        time.sleep(interval)
        v = J(gui, js)
    return v


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

    gui = GUIClient(port=PORT, data_dir=DATA_DIR)
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：prj 走独立 --data-dir，usr 主库不受其隔离 → 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        # 语言确定性（51 §6-8）：本套件按英文文案断言，不依赖他套件遗留在 usr 库的 ui.locale
        ensure_locale(gui, "en-US")
        for _ in range(30):
            if J(gui, "document.querySelector('.panel-inner')") and J(gui, "document.querySelector('[contenteditable=\"true\"]')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # ── 消息面契约（空库） ──
        r = gui.req('data-tasktree-tasks')
        c.check("T1 data-tasktree-tasks 空库返回数组", r.get('list') == [], repr(r)[:80])

        r = gui.req('data-tasktree-tasks', {'data': {'session_id': 'no-sess', 'top_session': 'no-top'}})
        c.check("T2 data-tasktree-tasks 按 session/top 过滤空列表", r.get('list') == [], repr(r)[:80])

        try:
            gui.req('data-tasktree-list')
            c.check("T3 data-tasktree-list 缺 top_session 报错", False, "未抛错")
        except RuntimeError as e:
            c.check("T3 data-tasktree-list 缺 top_session 报错", 'top_session' in str(e), str(e)[:80])

        r = gui.req('data-tasktree-list', {'data': {'top_session': 'top-empty'}})
        c.check("T4 data-tasktree-list 空 top 返回空 nodes", r.get('nodes') == [], repr(r)[:80])

        try:
            gui.req('data-tasktree-delete')
            c.check("T5 data-tasktree-delete 缺 node_id 报错", False, "未抛错")
        except RuntimeError as e:
            c.check("T5 data-tasktree-delete 缺 node_id 报错", 'node_id' in str(e), str(e)[:80])

        try:
            gui.req('data-tasktree-delete', {'data': {'node_id': 'no-such-node'}})
            c.check("T6 data-tasktree-delete 未知节点幂等成功", True)
        except RuntimeError as e:
            c.check("T6 data-tasktree-delete 未知节点幂等成功", False, str(e)[:80])

        # ── 发布观测 spy ──
        J(gui, SPY_JS)
        c.check("T6b publish spy 就绪", J(gui, "Array.isArray(window.__pubSpy)") is True)

        # ── 任务树渲染（事件驱动） ──
        J(gui, "window.mq.emit('session-changed', {session_id:'top-sess-1'}); 'ok'")
        time.sleep(1.0)
        noErr = J(gui, "(()=>{const b=window.__chonkConsole||{entries:[]};return b.entries.some(e=>e.level==='error'&&(e.text||'').includes('data-tasktree'))})()")
        c.check("T8 session 切换触发 data-tasktree 查询（无错误）", noErr is False)

        # tool 任务节点启动 → 树渲染
        emit_remote(gui, 'tasks.started', {
            'task_id': 'cmd-test-1', 'kind': 'tool', 'tool_name': 'command_execute',
            'name': 'echo test', 'purpose': 'echo test', 'status': 'running',
            'session_id': 'top-sess-1', 'top_session': 'top-sess-1',
            'started_at': '2026-09-01T00:00:00Z',
        })
        nodes = wait_upto(gui, "document.querySelectorAll('.session-tree-node').length",
                          lambda v: int(v or 0) >= 1)
        title = wait_upto(gui, "(()=>{const t=document.querySelector('.session-tree-node .node-title');return t?t.textContent.trim():''})()",
                          lambda v: v == 'echo test')
        c.check("T9 tasks.started → 树节点渲染", int(nodes) >= 1 and title == 'echo test', f"nodes={nodes} title={repr(title)}")

        # task 节点 running：stop 按钮存在（hover 才可见，直接 click 验证发布）
        stopBtn = wait_upto(gui, "document.querySelectorAll('.node-stop').length",
                            lambda v: int(v or 0) >= 1)
        c.check("T10a task 节点 stop 按钮存在", int(stopBtn) >= 1, f"count={stopBtn}")
        J(gui, CLEAR_SPY)
        J(gui, "(()=>{const b=document.querySelector('.node-stop');if(b)b.click();return 'ok'})()")
        stops = payloads(wait_upto(gui, GET_SPY,
                                   lambda v: len(payloads(v or [], 'task-stop')) >= 1) or [], 'task-stop')
        c.check("T10b 点击 stop → task-stop 发布", len(stops) == 1
                and stops[0].get('task_id') == 'cmd-test-1', repr(stops)[:120])

        # llm 节点 running：think meta（耗时）显示
        emit_remote(gui, 'tasks.started', {
            'task_id': 'op-1', 'session_id': 'sub-sess-1', 'kind': 'llm',
            'status': 'running', 'top_session': 'top-sess-1',
            'name': 'GPT-4o', 'purpose': 'GPT-4o', 'started_at': '2026-09-01T00:00:00Z',
        })
        llmMeta = wait_upto(gui, "document.querySelectorAll('.llm-task-meta').length",
                            lambda v: int(v or 0) >= 1)
        c.check("T11 llm 节点 running 显示耗时", int(llmMeta) >= 1, f"count={llmMeta}")

        # ── 终止会话：busy → Cancel → llm-cancel ──
        install_session_history_spy(gui)  # 必须先装（记录本次切会话触发的 data-session-history 应答）
        J(gui, "window.mq.emit('session-changed', {session_id:'sess-term'}); 'ok'")
        wait_session_history(gui, 'sess-term')  # 等历史回填落地再发（发送与历史回填交错会让气泡落点不可预期）
        wait_session_active(gui, 'sess-term')  # 有界等待会话身份生效（否则 message-send 被静默丢弃）
        J(gui, CLEAR_SPY)
        J(gui, "window.mq.emit('message-send', {sessionId:'sess-term', text:'stop me'}); 'ok'")
        cancelBtn = wait_upto(gui, """(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-right button'));return bs.some(x=>(x.textContent||'').trim()==='Cancel')})()""",
                              lambda v: v is True)
        J(gui, """(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-right button'));const b=bs.find(x=>(x.textContent||'').trim()==='Cancel');if(b)b.click();return 'ok'})()""")
        cancels = payloads(wait_upto(gui, GET_SPY,
                                     lambda v: len(payloads(v or [], 'llm-cancel')) >= 1) or [], 'llm-cancel')
        c.check("T12 终止会话：busy 态 Cancel → llm-cancel 发布",
                bool(cancelBtn) and len(cancels) == 1
                and cancels[0].get('session') == 'sess-term',
                f"btn={cancelBtn} cancels={repr(cancels)[:140]}")

        # ── ask_user 分支 ──
        J(gui, CLEAR_SPY)
        emit_remote(gui, 'ask-user', {
            'ask-id': 'ask-1', 'question': '是否继续执行?', 'options': ['继续', '取消'],
            'session': 'sess-term', 'turn': 'turn-1',
        })
        askVisible = wait_upto(gui, "document.querySelectorAll('.ask-user-content').length",
                               lambda v: int(v or 0) >= 1)
        qText = wait_upto(gui, "(()=>{const q=document.querySelector('.ask-user-content .question-content');return q?q.textContent.trim():''})()",
                          lambda v: v == '是否继续执行?')
        c.check("T13 ask-user 事件 → 弹窗+问题", int(askVisible) >= 1 and qText == '是否继续执行?', f"n={askVisible} q={repr(qText)}")

        opts = wait_upto(gui, "document.querySelectorAll('.ask-user-content .option-btn').length",
                         lambda v: int(v or 0) >= 2)
        c.check("T14a 选项按钮数量", int(opts) >= 2, f"n={opts}")
        J(gui, "(()=>{const b=document.querySelector('.ask-user-content .option-btn');if(b)b.click();return 'ok'})()")
        sel = wait_upto(gui, "document.querySelectorAll('.ask-user-content .option-btn.selected').length",
                        lambda v: int(v or 0) >= 1)
        c.check("T14b 点击选项 → 选中态", int(sel) >= 1, f"n={sel}")
        J(gui, "(()=>{const b=document.querySelector('.ask-user-content .submit-btn');if(b)b.click();return 'ok'})()")
        replies = payloads(wait_upto(gui, GET_SPY,
                                     lambda v: len(payloads(v or [], 'ask-user-reply')) >= 1) or [], 'ask-user-reply')
        dialogGone = wait_upto(gui, "document.querySelectorAll('.ask-user-content').length",
                               lambda v: int(v or 0) == 0) == 0
        c.check("T15 提交 → ask-user-reply 发布 + 弹窗关闭",
                len(replies) == 1
                and replies[0].get('ask-id') == 'ask-1'
                and bool(replies[0].get('answer'))
                and dialogGone, repr(replies)[:140])

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')]
        c.check("无前端错误（task 组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

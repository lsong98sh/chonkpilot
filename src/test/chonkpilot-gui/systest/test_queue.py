"""回归测试：排队消息交互（51-FP与测试映射「主chat 输入区操作行」排队消息）。

覆盖（INBOX 摘要条，2026-11-01 起替代旧 `.queue-indicator` 徽章）：
- 0 条不显示摘要条（边界）
- 忙碌态入队 → `.inbox-bar-count` 计数（1 条 / 2 条）
- 点击摘要条展开 `.inbox-popover` 全量列表（条目数与顺序）
- 撤回（↩ title「撤回」）→ 文本写回输入区 + 剩余计数 / 顺序
- 删除（✕ title「删除」）→ 队列清空、摘要条整体消失

忙碌夹具：usr `llms` 追加一条挂起 provider（本地 HTTP 只收不回），选中它并发一轮 →
该轮无 llm-complete → turn 持续 busy（否则默认 LLM 轮次失败即 llm-complete
→ drainQueue 清空待发队列，撤回后徽章数不稳定）。
**usr 配置是用户级全局库（~/.chonkpilot，不受 work-dir 隔离）** → 按 51 §6-8
「配置快照-还原」：跑前 `harness.snapshot_user_config` 快照，finally `restore_user_config` 还原
（原本为空/缺省 → 清空集合）。
"""
import json
import os
import shutil
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, snapshot_user_config, restore_user_config  # noqa: E402  动态端口；usr 配置快照-还原
from harness import install_session_history_spy, wait_session_history  # noqa: E402  切会话→发送竞态（历史回填覆盖）

PORT = free_port()
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_queue_data")

HANG_PORT = 8917       # 挂起 LLM 监听端口（仅本用例）
HANG_LLM = 'hangmock'  # usr llms 条目名（= llm-start.llm / 队列分桶键）


class _HangHandler(BaseHTTPRequestHandler):
    """只收不回的 chat/completions 端点：连接保持到用例结束 → 该轮不终态。"""

    protocol_version = 'HTTP/1.1'

    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0) or 0)
        if n:
            self.rfile.read(n)
        time.sleep(120)

    def log_message(self, *a):
        pass


def start_hang_llm():
    srv = ThreadingHTTPServer(('127.0.0.1', HANG_PORT), _HangHandler)
    srv.daemon_threads = True
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def emit_remote(gui, typ, payload):
    J(gui, "window.mq.emitRemote({type: %s, payload: %s}); 'ok'" % (
        json.dumps(typ), json.dumps(json.dumps(payload))))


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    hang_srv = start_hang_llm()
    # 2026-10-26：独立 work-dir（inbox 落 prjusr；共享 ws → 队列徽章基数被其它套件污染）。
    gui = GUIClient(port=PORT, data_dir=DATA_DIR, work_dir=os.path.join(DATA_DIR, "ws"))
    c = Checker()
    snap = None
    try:
        gui.start()
        for _ in range(30):
            if J(gui, "document.querySelector('.panel-inner')") and J(gui, "document.querySelector('[contenteditable=\"true\"]')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # 忙碌夹具：usr llms 追加挂起 provider 并选中（队列按 LLM 分桶 → 徽章归属所选 LLM）
        # 快照-还原（51 §6-8）：usr 主库 ~/.chonkpilot 不受 work-dir 隔离 → 跑前快照、finally 还原。
        # 夹具幂等（2026-09-23，[42 §2 (148)]）：同名条目**先去重再加** —— 服务端按名解析取**首个命中**
        # （`src/lib/llm/server/server.go` `userLLMProvider`），同名残留会把请求打到旧端口死端点。
        snap = snapshot_user_config(gui, ['llms'])
        base_llms = [e for e in (snap['llms'] or []) if (e or {}).get('name') != HANG_LLM]
        gui.req('data-user-config-save', {'data': {'llms': base_llms + [{
            'name': HANG_LLM, 'protocol': 'openai', 'apiKey': 'hang-key', 'model': 'hang-model',
            'baseUrl': 'http://127.0.0.1:%d/v1' % HANG_PORT, 'temperature': 0.7, 'maxOutputToken': 1024,
        }]}})
        J(gui, "window.mq.emit('chat-select-llm', {name: %s}); 'ok'" % json.dumps(HANG_LLM))

        sid = 'queue-sess-1'
        install_session_history_spy(gui)  # 必须先装：记录本次切会话触发的 data-session-history 应答
        J(gui, "window.mq.emit('session-changed', {session_id: %s}); 'ok'" % json.dumps(sid))
        wait_session_history(gui, sid)  # 等历史回填落地再发（发送与历史回填交错会让气泡落点不可预期）

        # Q0（边界）：会话就绪、尚无待发项 → 摘要条整体不渲染（0 条不显示）
        c.check("Q0 无排队时摘要条不显示", bool(J(gui, "!document.querySelector('.inbox-bar')")))

        # 忙碌态：发一条不结束的 turn（挂起 LLM → 无 llm-complete）
        J(gui, "window.mq.emit('message-send', {sessionId: %s, text: 'hang turn', llm: %s}); 'ok'" % (
            json.dumps(sid), json.dumps(HANG_LLM)))
        time.sleep(0.5)
        busy = J(gui, """(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-right button'));return bs.some(b=>(b.textContent||'').trim()==='Queue'||(b.textContent||'').trim()==='入队')})()""")
        c.check("Q1 忙碌态显示入队按钮", bool(busy))

        def count_badge():
            """INBOX 摘要条计数（`.inbox-bar-count`）；摘要条未渲染 → ''（= 0 条）。"""
            return J(gui, "document.querySelector('.inbox-bar .inbox-bar-count')?.textContent?.trim() || ''")

        def wait_count(expect, timeout=15):
            """轮询计数至期望值：入队落 prjusr inbox 后由 `llm-inbox-list` 异步回填。"""
            deadline = time.time() + timeout
            got = ''
            while time.time() < deadline:
                got = count_badge()
                if str(got) == str(expect):
                    return got
                time.sleep(0.3)
            return got

        def popover_texts():
            """popover 全量条目文本（按展示顺序）；返回 JS 数组 → 驱动层按 JSON 数组回传。"""
            return J(gui, "[...document.querySelectorAll('.inbox-popover .inbox-item .inbox-item-content')].map(e => e.textContent.trim())")

        # 夹具同步点：首条 `message-send` 自己也是经 llm-inbox-add 起轮（无运行轮 → 立即消费）。
        # 必须等服务端 `session-running-list` 里出现该会话（turn 真在跑）再入队；否则入队项会与
        # 起轮项被同批消费（首轮 start 前的竞态）→ 徽章数少 1 且用例误红。
        running = []
        deadline = time.time() + 20
        while time.time() < deadline:
            rl = gui.req('session-running-list', {}, 8000) or {}
            lst = rl.get('list') or (rl.get('result') or {}).get('list') or []
            running = [x for x in lst if (x or {}).get('session_id') == sid]
            if running:
                break
            time.sleep(0.3)
        c.check("Q1b 忙碌 turn 已在跑（session-running-list 命中）", bool(running), f"running={running}")

        # 入队 2 条（逐条等摘要条首行文本：服务端 created_at 升序 = 入队顺序 → 撤回顺序断言可预期）。
        # 注意等「首行文本」而非仅计数：起轮项被服务端消费后无 `session-inbox-changed` 广播时，
        # 前端镜像会残留上一条（幽灵徽标）→ 只等计数会被残留项提前满足。
        J(gui, "window.mq.emit('message-queue', {sessionId: %s, text: '排队消息甲', llm: %s}); 'ok'" % (
            json.dumps(sid), json.dumps(HANG_LLM)))
        first_line = ''
        deadline = time.time() + 15
        while time.time() < deadline:
            first_line = J(gui, "document.querySelector('.inbox-bar .inbox-bar-text')?.textContent?.trim() || ''")
            if first_line == '排队消息甲':
                break
            time.sleep(0.3)
        c.check("Q2a 入队 1 条 → 摘要条首行 = 排队消息甲", first_line == '排队消息甲', f"line={repr(first_line)}")
        badge = count_badge()
        c.check("Q2b 入队 1 条 → 计数 = 1", badge == '1', f"badge={repr(badge)}")

        J(gui, "window.mq.emit('message-queue', {sessionId: %s, text: '排队消息乙', llm: %s}); 'ok'" % (
            json.dumps(sid), json.dumps(HANG_LLM)))
        badge = wait_count(2)
        c.check("Q2 队列徽章 = 2", badge == '2', f"badge={repr(badge)}")

        # 点击摘要条 → popover 展开全量 2 条（顺序 = 入队顺序）
        J(gui, "document.querySelector('.inbox-bar .b-popover__reference')?.dispatchEvent(new MouseEvent('click', {bubbles: true})); 'ok'")
        time.sleep(0.6)
        items = J(gui, "document.querySelectorAll('.inbox-popover .inbox-item').length")
        c.check("Q3 队列 popover 2 项", int(items) == 2, f"items={items}")
        c.check("Q3b popover 条目顺序 = 入队顺序", popover_texts() == ['排队消息甲', '排队消息乙'],
                f"texts={popover_texts()}")

        # 撤回第二条（↩ = title「撤回」按钮）→ 文本写回输入区 + 队列剩 1（甲保留）
        # 按钮按 title 语义选（行内 3 个动作：插话/取消插话、删除、撤回），不按下标猜。
        J(gui, "document.querySelectorAll('.inbox-popover .inbox-item')[1]?.querySelector('.inbox-item-actions button[title^=\"撤回\"]')?.click(); 'ok'")
        badge = wait_count(1)
        ta = J(gui, "document.querySelector('[contenteditable][data-drop-target=\"chat-input\"]')?.innerText?.trim() || ''")
        c.check("Q4 撤回 → 文本写回输入区 + 队列剩 1", ta == '排队消息乙' and badge == '1',
                f"ta={repr(ta)} badge={repr(badge)}")
        c.check("Q4b 未撤回项保留（甲）", popover_texts() == ['排队消息甲'], f"texts={popover_texts()}")

        # 删除剩余一条（✕ = title「删除」）→ 队列清空、摘要条整体消失
        # （服务端 pending=0 **且** DOM 连续 3 次无摘要条 → 排除瞬时重渲染 / 镜像刷新竞态）
        # popover 可能随清单重渲染关闭 → 未展开时先点开。
        if not J(gui, "!!document.querySelector('.inbox-popover .inbox-item')"):
            J(gui, "document.querySelector('.inbox-bar .b-popover__reference')?.dispatchEvent(new MouseEvent('click', {bubbles: true})); 'ok'")
            time.sleep(0.5)
        J(gui, "document.querySelector('.inbox-popover .inbox-item .inbox-item-actions button[title=\"删除\"]')?.click(); 'ok'")
        stable = 0
        server_items = None
        deadline = time.time() + 15
        while time.time() < deadline:
            ib = gui.req('llm-inbox-list', {'session': sid}, 8000) or {}
            server_items = ib.get('items') or (ib.get('result') or {}).get('items') or []
            if not server_items and J(gui, "!document.querySelector('.inbox-bar')"):
                stable += 1
                if stable >= 3:
                    break
            else:
                stable = 0
            time.sleep(0.3)
        c.check("Q5 删除 → 队列清空（pending=0 且摘要条持续消失）", stable >= 3,
                f"stable={stable} server_items={server_items} badge={repr(count_badge())}")

        # 结束 turn 清理
        emit_remote(gui, 'llm-complete', {'session': sid, 'status': 'completed'})
        time.sleep(0.5)

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')]
        c.check("无前端错误（排队消息组）", len(errs) == 0, repr(errs[:3]))
    finally:
        # 还原用户级（全局）llms：挂起 provider 不得留在 ~/.chonkpilot（原为缺省/空 → 清空集合）
        if snap is not None:
            restore_user_config(gui, snap)
        gui.stop()
        hang_srv.shutdown()
        hang_srv.server_close()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

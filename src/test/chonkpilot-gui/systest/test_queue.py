"""回归测试：排队消息交互（51-FP与测试映射「主chat 输入区操作行」排队消息）。

覆盖：
- 忙碌态入队 → 队列数字徽章
- 点击徽章展开队列 popover
- 撤回（↩）→ 文本写回输入区
- 删除（✕）→ 队列项移除

忙碌夹具：usr `llms` 追加一条挂起 provider（本地 HTTP 只收不回），选中它并发一轮 →
该轮无 llm-complete → turn 持续 busy（否则默认 LLM 轮次失败即 llm-complete
→ drainQueue 清空待发队列，撤回后徽章数不稳定）。
**usr 配置是用户级全局库（~/.chonkpilot，不受 --data-dir 隔离）** → 按 51 §6-8
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
    gui = GUIClient(port=PORT, data_dir=DATA_DIR)
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
        # 快照-还原（51 §6-8）：usr 主库 ~/.chonkpilot 不受 --data-dir 隔离 → 跑前快照、finally 还原。
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

        # 忙碌态：发一条不结束的 turn（挂起 LLM → 无 llm-complete）
        J(gui, "window.mq.emit('message-send', {sessionId: %s, text: 'hang turn', llm: %s}); 'ok'" % (
            json.dumps(sid), json.dumps(HANG_LLM)))
        time.sleep(0.5)
        busy = J(gui, """(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-right button'));return bs.some(b=>(b.textContent||'').trim()==='Queue'||(b.textContent||'').trim()==='入队')})()""")
        c.check("Q1 忙碌态显示入队按钮", bool(busy))

        # 入队 2 条
        J(gui, "window.mq.emit('message-queue', {sessionId: %s, text: '排队消息甲', llm: %s}); 'ok'" % (
            json.dumps(sid), json.dumps(HANG_LLM)))
        J(gui, "window.mq.emit('message-queue', {sessionId: %s, text: '排队消息乙', llm: %s}); 'ok'" % (
            json.dumps(sid), json.dumps(HANG_LLM)))
        time.sleep(0.6)
        badge = J(gui, "document.querySelector('.queue-indicator .queue-badge')?.textContent?.trim() || ''")
        c.check("Q2 队列徽章 = 2", badge == '2', f"badge={repr(badge)}")

        # 点击徽章 → popover 展开 2 条
        J(gui, "document.querySelector('.queue-indicator')?.click(); 'ok'")
        time.sleep(0.6)
        items = J(gui, "document.querySelectorAll('.queue-popover .queue-item').length")
        c.check("Q3 队列 popover 2 项", int(items) == 2, f"items={items}")

        # 撤回第一条（↩）→ 文本写回输入区 + 队列剩 1
        J(gui, "document.querySelector('.queue-popover .queue-item .queue-item-actions button')?.click(); 'ok'")  # 第一个 = 撤回
        time.sleep(0.6)
        ta = J(gui, "document.querySelector('[contenteditable][data-drop-target=\"chat-input\"]')?.innerText || ''")
        badge = J(gui, "document.querySelector('.queue-indicator .queue-badge')?.textContent?.trim() || ''")
        c.check("Q4 撤回 → 文本写回输入区 + 队列剩 1", '排队消息甲' in ta and badge == '1', f"ta={repr(ta[:20])} badge={repr(badge)}")

        # 删除第二条（✕）→ 队列清空徽章消失（撤回后 popover 关闭，先重新打开）
        J(gui, "document.querySelector('.queue-indicator')?.click(); 'ok'")
        time.sleep(0.5)
        J(gui, "document.querySelector('.queue-popover .queue-item .queue-item-actions button:nth-child(2)')?.click(); 'ok'")  # 第二个 = 删除
        time.sleep(0.6)
        badgeGone = J(gui, "!document.querySelector('.queue-indicator .queue-badge')")
        c.check("Q5 删除 → 队列清空", bool(badgeGone))

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

"""回归测试：turn 导航圆圈（51-FP与测试映射「主chat窗口 turn导航」）。

覆盖：
- 圈数与轮次映射：turn ≤10 → 1 圈/turn；>10 → 固定 10 圈每圈 2 轮，覆盖不存在轮次的圈置灰禁用
- 悬停圈 → 弹出该圈覆盖轮次的用户消息一览（≤20 字符截断）
- 点击圈 → 滚动到该轮开始；当前圈高亮（滚动位置联动）

消息驱动：message-send（user 气泡）→ llm-receive（assistant 回复）→ llm-complete（轮次结束）。

忙碌夹具（同 test_queue）：把所选 LLM 指向本地「只收不回」端点，使 message-send 触发的
真实 llm-start 永不终态 —— 否则真实 LLM 失败即发 llm-error → 前端自动续写（"Continue"）
再发一轮 → 多出第 4 个 turn 圈，TN1 断言失真（依赖外网/负载时序）。
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
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_turn_data")

HANG_PORT = 8918       # 挂起 LLM 监听端口（仅本用例）
HANG_LLM = 'hangmock'  # usr llms 条目名（= llm-start.llm）


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


def run_turn(gui, sid, n):
    """驱动一轮对话：user 消息 + assistant 回复 + 轮次结束。"""
    J(gui, "window.mq.emit('message-send', {sessionId: %s, text: 'Q%d what is the plan?'}); 'ok'" % (
        json.dumps(sid), n))
    time.sleep(0.4)
    emit_remote(gui, 'llm-receive', {'type': 'text', 'text': 'A%d this is a reply body for turn %d.' % (n, n), 'session': sid})
    time.sleep(0.3)
    emit_remote(gui, 'llm-complete', {'session': sid, 'status': 'completed'})
    time.sleep(0.4)


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

        # 忙碌夹具：usr（用户级全局库）llms 追加挂起 provider 并选中，finally 还原
        # 快照-还原（51 §6-8）：usr 主库 ~/.chonkpilot 不受 --data-dir 隔离。
        # 夹具幂等（2026-09-23，[42 §2 (148)]）：同名条目**先去重再加** —— 服务端按名解析取**首个命中**
        # （`src/lib/llm/server/server.go` `userLLMProvider`），若本机 usr 残留同名条目（既往被中断的
        # 批跑未还原），请求会打到**旧端口死端点** → llm-error → 前端自动续写 → 多出 turn/圈 → TN1/TN4/TN5b 失真。
        snap = snapshot_user_config(gui, ['llms'])
        base_llms = [e for e in (snap['llms'] or []) if (e or {}).get('name') != HANG_LLM]
        gui.req('data-user-config-save', {'data': {'llms': base_llms + [{
            'name': HANG_LLM, 'protocol': 'openai', 'apiKey': 'hang-key', 'model': 'hang-model',
            'baseUrl': 'http://127.0.0.1:%d/v1' % HANG_PORT, 'temperature': 0.7, 'maxOutputToken': 1024,
        }]}})
        J(gui, "window.mq.emit('chat-select-llm', {name: %s}); 'ok'" % json.dumps(HANG_LLM))

        sid = 'turn-sess-1'
        install_session_history_spy(gui)  # 必须先装：记录本次切会话触发的 data-session-history 应答
        J(gui, "window.mq.emit('session-changed', {session_id: %s}); 'ok'" % json.dumps(sid))
        wait_session_history(gui, sid)  # 等历史回填落地再发（发送与历史回填交错会让气泡落点不可预期）

        # 3 轮 → 3 圈（turn ≤10 每圈 1 轮）
        for n in range(3):
            run_turn(gui, sid, n)
        dots = J(gui, "document.querySelectorAll('.turn-dot').length")
        c.check("TN1 3 轮 → 3 圈", int(dots) == 3, f"dots={dots}")

        # 悬停第 1 圈 → 弹出该圈用户消息一览（Q0）
        J(gui, "document.querySelectorAll('.turn-dot')[0].dispatchEvent(new MouseEvent('mouseenter',{bubbles:true})); 'ok'")
        time.sleep(0.4)
        pop = J(gui, "!!document.querySelector('.turn-nav-pop')")
        popText = J(gui, "document.querySelector('.turn-nav-item')?.textContent?.trim() || ''")
        c.check("TN2 悬停圈 → 弹用户消息一览", bool(pop) and 'Q0' in popText, f"pop={pop} t={repr(popText)}")
        J(gui, "document.querySelector('.turn-nav')?.dispatchEvent(new MouseEvent('mouseleave',{bubbles:true})); 'ok'")
        time.sleep(0.3)

        # 补齐到 13 轮 → 固定 10 圈，末 3 圈置灰禁用（覆盖不存在轮次）
        for n in range(3, 13):
            run_turn(gui, sid, n)
        dots = J(gui, "document.querySelectorAll('.turn-dot').length")
        disabled = J(gui, "document.querySelectorAll('.turn-dot.disabled').length")
        c.check("TN4 13 轮 → 10 圈 + 3 置灰禁用", int(dots) == 10 and int(disabled) == 3, f"dots={dots} disabled={disabled}")

        # 悬停禁用圈 → 不弹 popover
        J(gui, "document.querySelectorAll('.turn-dot.disabled')[0].dispatchEvent(new MouseEvent('mouseenter',{bubbles:true})); 'ok'")
        time.sleep(0.4)
        pop = J(gui, "!!document.querySelector('.turn-nav-pop')")
        c.check("TN5 悬停禁用圈不弹", pop is False, f"pop={pop}")

        # 悬停末活动圈（index 6，覆盖 turn 12）→ 弹 Q12
        J(gui, "document.querySelectorAll('.turn-dot')[6].dispatchEvent(new MouseEvent('mouseenter',{bubbles:true})); 'ok'")
        time.sleep(0.4)
        popText = J(gui, "Array.from(document.querySelectorAll('.turn-nav-item')).map(e=>e.textContent.trim()).join('|')")
        c.check("TN5b 末圈弹最后一轮消息", 'Q12' in str(popText), f"t={repr(popText)[:80]}")
        J(gui, "document.querySelector('.turn-nav')?.dispatchEvent(new MouseEvent('mouseleave',{bubbles:true})); 'ok'")
        time.sleep(0.3)

        # 点击末活动圈 → 滚动到底部附近（scrollTop > 0）
        J(gui, "document.querySelectorAll('.turn-dot')[6].click(); 'ok'")
        time.sleep(0.6)
        st = J(gui, "document.querySelector('.message-list')?.scrollTop || 0")
        c.check("TN6 点击末圈滚动到该轮", int(st) > 0, f"scrollTop={st}")

        # 点击第 1 圈 → 回到顶部；第 1 圈高亮（active）
        J(gui, "document.querySelectorAll('.turn-dot')[0].click(); 'ok'")
        time.sleep(0.6)
        st = J(gui, "document.querySelector('.message-list')?.scrollTop || 0")
        activeIdx = J(gui, "(()=>{const ds=Array.from(document.querySelectorAll('.turn-dot'));const i=ds.findIndex(d=>d.classList.contains('active'));return i})()")
        c.check("TN7 点击首圈回顶 + 首圈高亮", int(st) <= 30 and int(activeIdx) == 0, f"scrollTop={st} activeIdx={activeIdx}")

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')]
        c.check("无前端错误（turn 导航组）", len(errs) == 0, repr(errs[:3]))
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

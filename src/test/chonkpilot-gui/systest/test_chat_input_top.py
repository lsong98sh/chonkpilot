"""回归测试：输入区上方（51-FP与测试映射「主chat 对话内容输入区上方」）。

覆盖：
- 任务进度条：toolProgress → "Running task x/y (n failed)"
- 后台任务完成通知提示条（toolNotify → toast）
- 会话忙碌期间通知暂存，turn 结束后统一弹出
- 回复未完成（llm-complete incomplete）→ 自动续写（发送"继续"）
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, snapshot_config, restore_config  # noqa: E402  动态端口；套件级配置快照-还原
from harness import install_session_history_spy, wait_session_history  # noqa: E402  切会话→发送竞态（历史回填覆盖）

PORT = free_port()
HERE = os.path.dirname(os.path.abspath(__file__))
DATA_DIR = os.path.join(HERE, "_input_top_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def emit_remote(gui, typ, payload):
    J(gui, "window.mq.emitRemote({type: %s, payload: %s}); 'ok'" % (
        json.dumps(typ), json.dumps(json.dumps(payload))))


def toasts(gui):
    return J(gui, "Array.from(document.querySelectorAll('#b-message-container .b-message')).map(e=>e.textContent.trim())")


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
        for _ in range(30):
            if J(gui, "document.querySelector('.panel-inner')") and J(gui, "document.querySelector('[contenteditable=\"true\"]')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        sid = 'input-top-1'
        install_session_history_spy(gui)  # 必须先装：记录本次切会话触发的 data-session-history 应答
        J(gui, "window.mq.emit('session-changed', {session_id: %s}); 'ok'" % json.dumps(sid))
        wait_session_history(gui, sid)  # 等历史回填落地再发（发送与历史回填交错会让气泡落点不可预期）

        # IT1 任务进度条：toolProgress 驱动
        emit_remote(gui, 'tool-progress', {'task_id': 't1', 'completed': 1, 'total': 3, 'failed': 1})
        time.sleep(0.5)
        bar = J(gui, "document.querySelector('.task-progress-bar')?.textContent?.trim() || ''")
        # 批 2 口径更新：该行文案原为硬编码英文（Running task N/M (F failed)），已改 i18n
        # （chat.task_running / chat.task_failed_suffix）→ 断言按当前语言取失败后缀（zh 或 en），强度不降。
        c.check("IT1 任务进度条 (1/3 + 1 failed)",
                '1/3' in bar and ('1 个失败' in bar or '1 failed' in bar), repr(bar))
        emit_remote(gui, 'tool-progress', {'task_id': 't1', 'completed': 3, 'total': 3, 'failed': 0})
        time.sleep(0.5)

        # IT2 后台任务完成通知提示条（空闲直接弹出）
        emit_remote(gui, 'tool-notify', {'message': '后台任务已完成'})
        time.sleep(0.5)
        ts = toasts(gui)
        c.check("IT2 通知提示条弹出", any('后台任务已完成' in t for t in ts), repr(ts[:3]))

        # IT3 忙碌期间通知暂存 → turn 结束后统一弹出
        J(gui, "window.mq.emit('message-send', {sessionId: %s, text: 'busy turn'}); 'ok'" % json.dumps(sid))
        time.sleep(0.5)
        emit_remote(gui, 'tool-notify', {'message': '忙碌期间的通知'})
        time.sleep(0.4)
        ts = toasts(gui)
        busyQueued = not any('忙碌期间的通知' in t for t in ts)
        emit_remote(gui, 'llm-complete', {'session': sid, 'status': 'completed'})
        time.sleep(0.6)
        ts = toasts(gui)
        c.check("IT3 忙碌暂存 → turn 后弹出", bool(busyQueued) and any('忙碌期间的通知' in t for t in ts), repr(ts[:3]))

        # IT4 回复未完成（incomplete）→ 自动续写（发"继续"）
        J(gui, "window.mq.emit('message-send', {sessionId: %s, text: '写一篇长文'}); 'ok'" % json.dumps(sid))
        time.sleep(0.5)
        emit_remote(gui, 'llm-receive', {'type': 'text', 'text': '这是被截断的回复内容。', 'session': sid})
        time.sleep(0.3)
        emit_remote(gui, 'llm-complete', {'session': sid, 'status': 'incomplete'})
        time.sleep(1.2)  # autoContinue 400ms + 渲染
        lastUser = J(gui, """(()=>{const ms=Array.from(document.querySelectorAll('.message-item.user .message-content'));const last=ms[ms.length-1];return last?last.textContent.trim():''})()""")
        c.check("IT4 未完成自动续写（发送继续）", '继续' in lastUser or 'continue' in lastUser.lower(), repr(lastUser[:30]))

        # 清理：结束自动续写产生的 turn
        emit_remote(gui, 'llm-complete', {'session': sid, 'status': 'completed'})
        time.sleep(0.5)

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')]
        c.check("无前端错误（输入区上方组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

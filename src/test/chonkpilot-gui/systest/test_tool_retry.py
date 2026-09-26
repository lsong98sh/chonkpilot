"""回归测试：工具重试按钮（51-FP与测试映射「主chat 对话内容区」最后一条工具调用记录）。

覆盖：
- llm-tool-call → tool_pair 消息渲染（调用意图 + 运行中状态）
- tool-result status=interrupted → pair 标记中断 → 底部操作区出现「重试」按钮
- 点击重试 → tool-retry 事件发布（重跑最后一条中断工具，不走 LLM）
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
DATA_DIR = os.path.join(HERE, "_retry_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def _loads_deep(v):
    """gui.eval 结果可能多重 JSON 编码（EvalWithResult）：循环解包到非 JSON 字符串。"""
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def emit_remote(gui, typ, payload):
    J(gui, "window.mq.emitRemote({type: %s, payload: %s}); 'ok'" % (
        json.dumps(typ), json.dumps(json.dumps(payload))))


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    gui = GUIClient(port=PORT, data_dir=DATA_DIR)
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：prj 走独立 --data-dir，usr 主库不受其隔离（重试参数等会写
    # usr 库）→ 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        for _ in range(30):
            if J(gui, "document.querySelector('.panel-inner')") and J(gui, "document.querySelector('[contenteditable=\"true\"]')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        sid = 'retry-1'
        install_session_history_spy(gui)  # 必须先装：记录本次切会话触发的 data-session-history 应答
        J(gui, "window.mq.emit('session-changed', {session_id: %s}); 'ok'" % json.dumps(sid))
        wait_session_history(gui, sid)  # 等历史回填落地再发（发送与历史回填交错会让气泡落点不可预期）

        # TR1 工具调用记录渲染（tool_pair + 运行中）：llm-receive{type:'tool-call'}
        J(gui, "window.mq.emit('message-send', {sessionId: %s, text: '读取文件并分析'}); 'ok'" % json.dumps(sid))
        time.sleep(0.4)
        emit_remote(gui, 'llm-receive', {'type': 'tool-call', 'session': sid, 'tool_call_id': 'tc-1', 'tool': 'read_file', 'arguments': '{"path": "a.txt"}'})
        time.sleep(0.6)
        pair = J(gui, """(()=>{const p=document.querySelector('.message-item.tool_pair');return {exists:!!p, label:(p?.textContent||'').slice(0,80)}})()""")
        c.check("TR1 工具调用记录渲染（运行中）", bool(pair.get('exists')), repr(pair.get('label'))[:80])

        # TR2 中断 → 底部「重试」按钮出现：turn 结束后 tool-pair 事件标 interrupted（启动清理/abort 路径）
        emit_remote(gui, 'llm-complete', {'session': sid, 'status': 'completed'})
        time.sleep(0.4)
        emit_remote(gui, 'tool-pair', {'tool_id': 'tc-1', 'status': 'interrupted'})
        time.sleep(0.5)
        retryBtn = J(gui, "!!document.querySelector('.retry-tool-btn')")
        c.check("TR2 中断 → 重试按钮出现", bool(retryBtn), f"retry={retryBtn}")

        # TR3 点击重试 → tool-retry 发布（61-消息一览 §4.2：tool-retry 载荷 = {session, turn}，
        # 不带工具 id——后端按 session+turn 定位该轮最后一条中断工具重跑；turn = 该工具所属轮次，
        # 实时气泡无 turn_id 时回落 currentTurnId（此处为空串）。原「task_id=中断工具」口径失效）
        J(gui, "window.__retrySpy=[];window.mq.post('tool-retry', d=>window.__retrySpy.push(d));'ok'")
        J(gui, "document.querySelector('.retry-tool-btn')?.click(); 'ok'")
        time.sleep(0.5)
        spy = _loads_deep(J(gui, "JSON.stringify(window.__retrySpy)"))
        c.check("TR3 点击重试 → tool-retry 发布（{session, turn} 契约）",
                isinstance(spy, list) and len(spy) == 1
                and spy[0].get('session') == sid
                and isinstance(spy[0].get('turn'), str)
                and set(spy[0].keys()) <= {'session', 'turn', 'instance_id'},
                repr(spy)[:120])

        # 清理 turn
        emit_remote(gui, 'llm-complete', {'session': sid, 'status': 'completed'})
        time.sleep(0.4)

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')]
        c.check("无前端错误（工具重试组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

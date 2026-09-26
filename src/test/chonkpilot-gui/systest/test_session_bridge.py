"""回归测试：session 消息面契约（51-FP与测试映射「主chat 标题栏」session 相关）。

空库语义验证：无会话时各 data-session-* 消息不崩、回落逻辑正确、错误路径报错。
会话/消息写入需 llm-start（mock_llm）后续验证。
驱动：drive.req → POST /publish（data-session-* 请求-响应取 result）。
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, snapshot_config, restore_config  # noqa: E402  动态端口；套件级配置快照-还原

PORT = free_port()
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_sess_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


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
            if J(gui, "document.querySelector('.panel-inner')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # S1 空库 data-session-latest → 空
        r = gui.req('data-session-latest')
        c.check("S1 data-session-latest 空库返回空", r.get('session_id', 'x') == '', repr(r)[:80])

        # S2 data-session-active-set → 成功（result 无 error 即成功）
        try:
            gui.req('data-session-active-set', {'data': {'session_id': 'sess-bridge-1'}})
            c.check("S2 data-session-active-set 成功", True)
        except RuntimeError as e:
            c.check("S2 data-session-active-set 成功", False, str(e))

        # S3 data-session-active-get → 无真实会话记录 → 回落最新（空）
        r = gui.req('data-session-active-get')
        c.check("S3 data-session-active-get 回落（无记录→空）", r.get('session_id', 'x') == '', repr(r)[:80])

        # S4 data-session-get 不存在 → {data: null}
        r = gui.req('data-session-get', {'id': 'no-such-session'})
        c.check("S4 data-session-get 不存在返回 null", r.get('data') is None, repr(r)[:80])

        # S5 data-session-title 不存在 → error
        try:
            gui.req('data-session-title', {'data': {'id': 'no-such-session', 'title': '新标题'}})
            c.check("S5 data-session-title 不存在报错", False, "未抛错")
        except RuntimeError as e:
            c.check("S5 data-session-title 不存在报错", 'not found' in str(e), str(e)[:80])

        # S6 data-session-delete 不存在 → 成功（幂等）
        try:
            gui.req('data-session-delete', {'id': 'no-such-session'})
            c.check("S6 data-session-delete 不存在幂等成功", True)
        except RuntimeError as e:
            c.check("S6 data-session-delete 不存在幂等成功", False, str(e))

        # S7 data-session-content → contents 空字典
        r = gui.req('data-session-content', {'data': {'session_id': 'sess-bridge-1', 'keys': ['message:abc']}})
        c.check("S7 data-session-content 空库返回空 contents", r.get('contents') == {}, repr(r)[:80])

        # S8 data-session-list 空 → {list: []}
        r = gui.req('data-session-list')
        c.check("S8 data-session-list 空库空列表", r.get('list', ['x']) == [], repr(r)[:80])

        # S9 data-session-history 无 session_id → error
        try:
            gui.req('data-session-history', {'data': {}})
            c.check("S9 data-session-history 缺参报错", False, "未抛错")
        except RuntimeError as e:
            c.check("S9 data-session-history 缺参报错", 'session_id' in str(e), str(e)[:80])

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error' and 'method not implemented' not in (e.get('text') or '')]
        c.check("无前端错误（session 相关）", len(errs) == 0, repr(errs[:2]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

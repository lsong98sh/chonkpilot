"""回归测试：preview 组（51-FP与测试映射「preview 预览区」）。

覆盖：
- 打开 md 文件 → 预览渲染 markdown
- 源码/预览切换（code-show-source / code-show-preview）
- 不支持文件 → 退化为 Hex View
- 临时页签双击 → 转固定（is-temporary 消失）
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
HERE = os.path.dirname(os.path.abspath(__file__))
WORK = os.path.join(HERE, "_prev_ws")
DATA_DIR = os.path.join(HERE, "_prev_data")

W = WORK.replace("\\", "/")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def open_file(gui, rel, temporary=True):
    J(gui, "window.mq.emit('file-open', {path: %s, temporary: %s}); 'ok'" % (
        json.dumps(W + "/" + rel), json.dumps(temporary)))
    time.sleep(1.0)


def active_tab_info(gui):
    return J(gui, """(()=>{const t=document.querySelector('.code-view .tb-tab.active');return {exists:!!t, type:t?.getAttribute('data-render')||'', temp:t?.classList.contains('is-temporary')||false, name:t?.querySelector('.tb-name')?.textContent?.trim()||''}})()""")


def main():
    shutil.rmtree(WORK, ignore_errors=True)
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(WORK, exist_ok=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    with open(os.path.join(WORK, "readme.md"), "w") as f:
        f.write("# 预览标题\n\n这是**加粗**正文与 `代码`。")
    with open(os.path.join(WORK, "data.bin"), "wb") as f:
        f.write(bytes([0x48, 0x65, 0x00, 0x01, 0xFF, 0x0A]))
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    gui = GUIClient(port=PORT, work_dir=WORK, data_dir=DATA_DIR)
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

        # P1 打开 md → markdown 预览渲染
        open_file(gui, "readme.md")
        md = J(gui, """(()=>{const el=document.querySelector('.code-view .tb-tab.active');const body=document.querySelector('.code-preview-content')||document.body;return {h1:body.querySelector('h1')?.textContent||'', strong:body.querySelectorAll('strong').length, code:body.querySelectorAll('code').length}})()""")
        c.check("P1 markdown 预览渲染（h1/strong/code）",
                bool(md) and '预览标题' in str(md.get('h1')) and md.get('strong', 0) >= 1 and md.get('code', 0) >= 1, repr(md)[:120])

        # P2 源码/预览切换
        J(gui, "window.mq.emit('code-show-source'); 'ok'")
        time.sleep(0.6)
        src = J(gui, "document.querySelector('.code-view .tb-tab.active') ? (document.querySelector('.source-code')?.textContent || '') : ''")
        J(gui, "window.mq.emit('code-show-preview'); 'ok'")
        time.sleep(0.6)
        backPreview = J(gui, "document.querySelector('.code-view .tb-tab.active') ? !!document.querySelector('.source-code') : null")
        c.check("P2 源码/预览切换", '预览标题' in src and backPreview is False, f"src={repr(src[:30])} back={backPreview}")

        # P3 不支持文件 → hex 退化
        open_file(gui, "data.bin")
        hexView = J(gui, """(()=>{const t=document.querySelector('.code-view .tb-tab.active');const txt=document.querySelector('.hex-view')||document.querySelector('.code-preview-content');return {exists:!!t, body:txt?txt.textContent.slice(0,60):''}})()""")
        c.check("P3 二进制文件 → hex 视图", bool(hexView.get('exists')) and '48' in hexView.get('body', ''), repr(hexView)[:100])

        # P4 临时页签双击 → 转固定
        open_file(gui, "readme.md", temporary=True)
        temp1 = J(gui, "document.querySelector('.code-view .tb-tab.active')?.classList.contains('is-temporary')")
        J(gui, "document.querySelector('.code-view .tb-tab.active')?.dispatchEvent(new MouseEvent('dblclick',{bubbles:true})); 'ok'")
        time.sleep(0.5)
        temp2 = J(gui, "document.querySelector('.code-view .tb-tab.active')?.classList.contains('is-temporary')")
        c.check("P4 临时 tab 双击转固定", bool(temp1) and not bool(temp2), f"before={temp1} after={temp2}")

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')]
        c.check("无前端错误（preview 组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

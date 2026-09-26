"""回归测试：filetree 右键菜单补全（FP：在控制台显示 / 复制 / 黏贴 + 单选多选过滤）。

- 复制/黏贴用应用内剪贴板 + filemon copy；文件系统断言。
- 「在控制台显示」仅断言菜单项存在（点击会弹系统 cmd 窗口，自动化不触发）。
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
# GUI 工作目录 = drive.DEFAULT_WORK（systest/ws）。原口径 `..\..\chonkpilot-test\ws` 解析为
# chonkpilot-test\chonkpilot-test\ws（不存在）→ 黏贴产物断言恒假。
WS = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "ws"))
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_ctx_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def ctx_open(gui, label):
    """右键节点弹出菜单。返回菜单文本。"""
    return J(gui, f"""(()=>{{const rows=Array.from(document.querySelectorAll('.tree-row'));const r=rows.find(x=>(x.querySelector('.node-label')||{{}}).textContent==='{label}');if(!r)return 'no-node';const rect=r.getBoundingClientRect();r.dispatchEvent(new MouseEvent('contextmenu',{{bubbles:true,cancelable:true,clientX:rect.left+10,clientY:rect.top+5}}));return 'ok'}})()""")


def menu_items(gui):
    return J(gui, "Array.from(document.querySelectorAll('.context-menu-item')).map(i=>i.textContent.trim())")


def menu_click(gui, label):
    return J(gui, f"""(()=>{{const its=Array.from(document.querySelectorAll('.context-menu-item'));const it=its.find(i=>i.textContent.trim()==='{label}');if(!it)return false;it.click();return true}})()""")


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()
    # 初始状态加固
    if os.path.exists(os.path.join(WS, "a", "b.txt")) and not os.path.exists(os.path.join(WS, "b.txt")):
        os.rename(os.path.join(WS, "a", "b.txt"), os.path.join(WS, "b.txt"))

    gui = GUIClient(port=PORT, data_dir=DATA_DIR)
    c = Checker()
    pasted = os.path.join(WS, "a", "b.txt")  # 黏贴产物，测试后清理
    # 套件级配置快照-还原（51 §6-8）：prj 走独立 --data-dir，usr 主库不受其隔离 → 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        # 固定 en-US locale（WebView2 localStorage 共享，避免受先前测试切换影响）
        J(gui, "localStorage.setItem('chonkpilot-locale','en-US'); location.reload(); 'ok'")
        time.sleep(3)
        for _ in range(25):
            if J(gui, "document.querySelectorAll('.tree-row').length") > 0:
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # C1 右键文件 → 菜单含 Show in Console/Copy（无剪贴板时不含 Paste）
        ctx_open(gui, "b.txt")
        time.sleep(0.5)
        items = menu_items(gui)
        c.check("C1 菜单含 Show in Console/Copy", "Show in Console" in items and "Copy" in items, repr(items))
        c.check("C1b 无剪贴板时不含 Paste", "Paste" not in items, repr(items))
        # 关闭菜单
        J(gui, "document.body.click(); 'ok'")
        time.sleep(0.3)

        # C2 复制 b.txt → 剪贴板有值 → 右键 a 目录 → 菜单含 Paste
        ctx_open(gui, "b.txt")
        time.sleep(0.4)
        menu_click(gui, "Copy")
        time.sleep(0.4)
        ctx_open(gui, "a")
        time.sleep(0.5)
        items2 = menu_items(gui)
        c.check("C2 复制后右键目录含 Paste", "Paste" in items2, repr(items2))

        # C3 黏贴到 a 目录 → a/b.txt 复制成功（文件系统）
        menu_click(gui, "Paste")
        time.sleep(1.5)
        c.check("C3 黏贴复制文件到目标目录", os.path.exists(pasted) and os.path.exists(os.path.join(WS, "b.txt")),
                f"pasted={os.path.exists(pasted)} srcStill={os.path.exists(os.path.join(WS, 'b.txt'))}")

        # C4 多选右键 → 单选项（Open/Rename）隐藏
        J(gui, """(()=>{const rows=Array.from(document.querySelectorAll('.tree-row'));const click=(l,opts)=>{for(const r of rows){if((r.querySelector('.node-label')||{}).textContent===l){r.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,...opts}));return true}}return false};click('a.txt',{ctrlKey:true});click('b.txt',{ctrlKey:true});return 'ok'})()""")
        time.sleep(0.4)
        ctx_open(gui, "b.txt")
        time.sleep(0.5)
        items3 = menu_items(gui)
        c.check("C4 多选右键隐藏单选项（Open/Rename）",
                "Open" not in items3 and "Rename" not in items3 and "Copy" in items3 and "Delete" in items3,
                repr(items3))
        # 关闭菜单 + 清多选
        J(gui, "document.body.click(); 'ok'")
        time.sleep(0.3)
        J(gui, """(()=>{const rows=Array.from(document.querySelectorAll('.tree-row'));for(const r of rows){if((r.querySelector('.node-label')||{}).textContent==='b.txt'){r.dispatchEvent(new MouseEvent('click',{bubbles:true}));break}}return 'ok'})()""")
        time.sleep(0.3)

        # C5 Show in Console：仅断言菜单项存在（点击弹系统 cmd，不触发）
        ctx_open(gui, "a.txt")
        time.sleep(0.5)
        items4 = menu_items(gui)
        c.check("C5 菜单含 Show in Console", "Show in Console" in items4, repr(items4))

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error' and 'doDelete error' not in (e.get('text') or '')]
        c.check("无前端错误", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
        # 清理黏贴产物
        for p in (pasted,):
            try:
                os.remove(p)
            except OSError:
                pass
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

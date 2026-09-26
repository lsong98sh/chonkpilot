"""回归测试：filetree 多选 + 拖拽（FP：Ctrl 多选 / 拖拽移动 / 同名冲突确认 / 拖到 chat 全路径）。

文件系统断言（os.path）为权威；测试后恢复移动的文件；数据目录隔离。
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
# chonkpilot-test\chonkpilot-test\ws（不存在）→ 文件系统断言恒假 + 写夹具抛 FileNotFoundError。
WS = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "ws"))
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_dnd_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def drag(gui, srcLabel, targetLabel):
    return J(gui, f"""(()=>{{
  const rows=Array.from(document.querySelectorAll('.tree-row'));
  const find=l=>rows.find(r=>(r.querySelector('.node-label')||{{}}).textContent===l);
  const s=find('{srcLabel}'), tg=find('{targetLabel}');
  if(!s||!tg)return 'missing:'+(s?'':'src')+(tg?'':'tgt');
  const dt=new DataTransfer();
  s.dispatchEvent(new DragEvent('dragstart',{{bubbles:true,cancelable:true,dataTransfer:dt}}));
  tg.dispatchEvent(new DragEvent('dragover',{{bubbles:true,cancelable:true,dataTransfer:dt}}));
  tg.dispatchEvent(new DragEvent('drop',{{bubbles:true,cancelable:true,dataTransfer:dt}}));
  s.dispatchEvent(new DragEvent('dragend',{{bubbles:true,cancelable:true,dataTransfer:dt}}));
  return 'ok';
}})()""")


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    # 预置同名冲突文件（测试后清理）
    root_dnd = os.path.join(WS, "_dnd.txt")
    a_dnd = os.path.join(WS, "a", "_dnd.txt")
    with open(root_dnd, "w") as f:
        f.write("dnd-src")
    with open(a_dnd, "w") as f:
        f.write("dnd-dst")
    # 初始状态加固：确保 a.txt 在根（前次运行残留时移回）
    if os.path.exists(os.path.join(WS, "a", "a.txt")) and not os.path.exists(os.path.join(WS, "a.txt")):
        os.rename(os.path.join(WS, "a", "a.txt"), os.path.join(WS, "a.txt"))

    gui = GUIClient(port=PORT, data_dir=DATA_DIR)
    c = Checker()
    moved_away = False  # D1 移动了 a.txt → a/；finally 恢复
    # 套件级配置快照-还原（51 §6-8）：prj 走独立 --data-dir，usr 主库不受其隔离 → 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        for _ in range(25):
            if J(gui, "document.querySelectorAll('.tree-row').length") > 0:
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # M1 Ctrl 点击多选
        J(gui, """(()=>{const rows=Array.from(document.querySelectorAll('.tree-row'));const click=(l,opts)=>{for(const r of rows){if((r.querySelector('.node-label')||{}).textContent===l){r.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,...opts}));return true}}return false};click('a.txt',{ctrlKey:true});click('b.txt',{ctrlKey:true});return 'ok'})()""")
        time.sleep(0.4)
        multi = J(gui, "document.querySelectorAll('.tree-row.multi-selected').length")
        c.check("M1 Ctrl 点击多选（2 个节点）", multi == 2, f"multi={multi}")

        # M2 非 Ctrl 单击 → 清多选退化为单选
        J(gui, """(()=>{const rows=Array.from(document.querySelectorAll('.tree-row'));for(const r of rows){if((r.querySelector('.node-label')||{}).textContent==='b.txt'){r.dispatchEvent(new MouseEvent('click',{bubbles:true}));break}}return 'ok'})()""")
        time.sleep(0.4)
        multi2 = J(gui, "document.querySelectorAll('.tree-row.multi-selected').length")
        c.check("M2 普通单击清多选（退化为单选）", multi2 == 0, f"multi={multi2}")

        # M3 Ctrl 多选后 Delete → 确认框
        J(gui, """(()=>{const rows=Array.from(document.querySelectorAll('.tree-row'));const click=(l,opts)=>{for(const r of rows){if((r.querySelector('.node-label')||{}).textContent===l){r.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,...opts}));return true}}return false};click('a.txt',{ctrlKey:true});click('b.txt',{ctrlKey:true});return 'ok'})()""")
        time.sleep(0.3)
        J(gui, "document.dispatchEvent(new KeyboardEvent('keydown',{key:'Delete',bubbles:true})); 'ok'")
        time.sleep(0.8)
        c.check("M3 多选 Delete 弹出确认框", J(gui, "!!document.querySelector('.dialog-shell')"))
        J(gui, """(()=>{const b=Array.from(document.querySelectorAll('.dialog-shell button')).find(x=>/取消|cancel/i.test(x.textContent||''));if(b)b.click();return 'ok'})()""")
        time.sleep(0.4)

        # D1 拖拽 a.txt → a 目录 → 移动成功（文件系统权威）
        gui.console(clear=True)
        r = drag(gui, "a.txt", "a")
        time.sleep(1.5)
        moved_away = os.path.exists(os.path.join(WS, "a", "a.txt")) and not os.path.exists(os.path.join(WS, "a.txt"))
        c.check("D1 拖拽 a.txt → a 目录（文件系统移动成功）", r == "ok" and moved_away, f"r={r} moved={moved_away}")
        # D1b DOM 刷新：doMove 已自动展开目标目录，直接检查 a 内 a.txt 节点
        time.sleep(0.5)
        dom_in_a = J(gui, """Array.from(document.querySelectorAll('.tree-row')).some(r=>(r.querySelector('.node-label')||{}).textContent==='a.txt'&&r.dataset.path.includes('/a/a.txt'))""")
        c.check("D1b a 目录内出现 a.txt（DOM 刷新）", bool(dom_in_a))

        # D2 拖拽 a/a.txt → b.txt（移入其所在目录=根）→ 恢复
        # 确保 a 目录展开（arrow 处于展开态才跳过）
        J(gui, """(()=>{const rows=Array.from(document.querySelectorAll('.tree-row'));const a=rows.find(r=>(r.querySelector('.node-label')||{}).textContent==='a'&&r.dataset.path.split('/').filter(Boolean).length===2);if(a&&!a.querySelector('.arrow-icon.expanded')&&a.querySelector('.arrow')){a.querySelector('.arrow').dispatchEvent(new MouseEvent('click',{bubbles:true}));return true}return false})()""")
        time.sleep(1.2)
        drag(gui, "a.txt", "b.txt")
        time.sleep(1.5)
        restored = os.path.exists(os.path.join(WS, "a.txt")) and not os.path.exists(os.path.join(WS, "a", "a.txt"))
        c.check("D2 拖拽 a/a.txt → b.txt（移入根，恢复）", bool(restored), f"restored={restored}")
        moved_away = False

        # D3 同名冲突：根 _dnd.txt → a 目录（a/_dnd.txt 已存在）→ 确认框 → 取消
        gui.console(clear=True)
        drag(gui, "_dnd.txt", "a")
        time.sleep(1.0)
        conflict = J(gui, "!!document.querySelector('.dialog-shell')")
        c.check("D3 同名冲突弹确认框", bool(conflict))
        if conflict:
            J(gui, """(()=>{const b=Array.from(document.querySelectorAll('.dialog-shell button')).find(x=>/取消|cancel/i.test(x.textContent||''));if(b)b.click();return 'ok'})()""")
            time.sleep(0.6)
        # 取消后源文件未移动（根 _dnd.txt 仍存在）
        c.check("D3b 取消后源文件未移动", os.path.exists(root_dnd), "取消后不移动（文件系统）")

        # D4 拖到 chat 输入区 → 插入全路径
        J(gui, """(()=>{const rows=Array.from(document.querySelectorAll('.tree-row'));const s=rows.find(r=>(r.querySelector('.node-label')||{}).textContent==='a.txt');if(!s)return 'no-src';const ta=document.querySelector('[contenteditable][data-drop-target="chat-input"]');if(!ta)return 'no-chat';const dt=new DataTransfer();s.dispatchEvent(new DragEvent('dragstart',{bubbles:true,cancelable:true,dataTransfer:dt}));const rect=ta.getBoundingClientRect();ta.dispatchEvent(new DragEvent('dragover',{bubbles:true,cancelable:true,dataTransfer:dt,clientX:rect.left+10,clientY:rect.top+10}));ta.dispatchEvent(new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:dt,clientX:rect.left+10,clientY:rect.top+10}));s.dispatchEvent(new DragEvent('dragend',{bubbles:true,cancelable:true,dataTransfer:dt}));return 'ok'})()""")
        time.sleep(0.8)
        taVal = J(gui, "document.querySelector('[contenteditable][data-drop-target=\"chat-input\"]')?.innerText || ''")
        c.check("D4 拖到 chat 插入全路径", "a.txt" in taVal, repr(taVal[:60]))

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error' and 'doDelete error: cancel' not in (e.get('text') or '')]
        c.check("无前端错误", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
        # 恢复移动的 a.txt（D2 失败时）
        if moved_away and os.path.exists(os.path.join(WS, "a", "a.txt")) and not os.path.exists(os.path.join(WS, "a.txt")):
            os.rename(os.path.join(WS, "a", "a.txt"), os.path.join(WS, "a.txt"))
        # 清理预置文件
        for p in (root_dnd, a_dnd):
            try:
                os.remove(p)
            except OSError:
                pass
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

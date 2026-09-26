"""回归测试：filetree 交互组（FP：单击临时页签 / 改名判定 / 双击固定页签 / Delete 删除确认）。

只读断言为主：改名/删除仅验证触发（输入框/确认框出现），不实际改动文件系统。
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
# 隔离数据目录（opened-files/layout 等 prj 状态会跨运行污染页签恢复）
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_filetree_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def click_label(gui, label, dbl=False):
    """按节点名点击文件树行。返回是否命中。

    注意：改名态下该行 `.node-label` 被 `.inline-edit-input` 替换 → 本函数必然返回 False
    （无声未命中）。故调用方须保证前置非改名态，或对返回值断言。
    """
    evt = "dblclick" if dbl else "click"
    return J(gui, f"""(()=>{{const rows=Array.from(document.querySelectorAll('.tree-row'));for(const r of rows){{const l=r.querySelector('.node-label');if(l&&l.textContent==='{label}'){{r.dispatchEvent(new MouseEvent('{evt}',{{bubbles:true,cancelable:true}}));return true}}}}return false}})()""")


def wait_editing(gui, want=True, timeout=3.0, step=0.15):
    """有界等待改名输入框出现/消失（避免固定 sleep 在负载下的时序抖动）。"""
    deadline = time.time() + timeout
    while True:
        if J(gui, "!!document.querySelector('.inline-edit-input')") is want:
            return True
        if time.time() >= deadline:
            return False
        time.sleep(step)


def exit_editing(gui):
    """强制退出改名态（ESC），返回是否已退出。"""
    J(gui, "document.querySelector('.inline-edit-input')?.dispatchEvent(new KeyboardEvent('keyup',{key:'Escape',bubbles:true})); 'ok'")
    return wait_editing(gui, want=False, timeout=2.0)


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()  # 防 ProjectPath 回落旧库
    gui = GUIClient(port=PORT, data_dir=DATA_DIR)
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：prj 走独立 --data-dir，usr 主库不受其隔离 → 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        # 等树加载
        for _ in range(20):
            if J(gui, "document.querySelectorAll('.tree-row').length") > 0:
                break
            time.sleep(0.5)

        # F6a 单击文件 → 临时页签（斜体）
        c.check("F6a 单击文件打开临时页签", click_label(gui, "a.txt"), "a.txt 命中")
        time.sleep(1.0)
        # 预览页签栏 = 公共 TabBar（components/tabs/TabBar.vue：.tb-bar.tb-bottom .tb-tab/.tb-name），
        # 原 .preview-tab/.preview-tab-name 口径已失效
        tmp = J(gui, "document.querySelectorAll('.code-view .tb-bar.tb-bottom .tb-tab.is-temporary').length")
        total = J(gui, "document.querySelectorAll('.code-view .tb-bar.tb-bottom .tb-tab').length")
        c.check("F6b 临时页签出现（is-temporary）", tmp == 1 and total >= 1, f"tmp={tmp} total={total}")

        # F6c 单击另一文件 → 临时页签覆盖（临时数保持 1）
        click_label(gui, "b.txt")
        time.sleep(1.0)
        tmp2 = J(gui, "document.querySelectorAll('.code-view .tb-bar.tb-bottom .tb-tab.is-temporary').length")
        name = J(gui, "document.querySelector('.code-view .tb-bar.tb-bottom .tb-tab.is-temporary .tb-name')?.textContent || ''")
        c.check("F6c 临时页签覆盖（数量仍1，内容=b.txt）", tmp2 == 1 and name == "b.txt", f"tmp={tmp2} name={name}")

        # F6d 双击文件 → 转固定页签（该文件 tab 不再是临时；此前单击的临时页签 b.txt 保留）
        click_label(gui, "a.txt", dbl=True)
        time.sleep(1.0)
        pin = J(gui, """(()=>{for(const t of document.querySelectorAll('.code-view .tb-bar.tb-bottom .tb-tab')){const n=t.querySelector('.tb-name')?.textContent||'';if(n==='a.txt'&&!t.classList.contains('is-temporary'))return true}return false})()""")
        c.check("F6d 双击转固定页签（a.txt 非临时）", bool(pin))

        # F8 改名判定：单击选中 → 间隔 >500ms → 再次单击 → 输入框出现
        # 前置确定性：先退出可能的改名态，再点 a.txt 复位 lastRowClick（原实现依赖 F6c 对 b.txt
        # 的残留 → 首次单击即进改名，且改名态下 `.node-label` 被输入框替换 → 第二次单击无声未命中）
        assert exit_editing(gui), "F8 前置：未能退出改名态"
        assert click_label(gui, "a.txt"), "F8 前置：a.txt 未命中"
        time.sleep(0.4)
        assert click_label(gui, "b.txt"), "F8 前置：b.txt 未命中"
        assert not J(gui, "!!document.querySelector('.inline-edit-input')"), "F8 前置：单击非选中节点即进入改名（不应发生）"
        time.sleep(0.8)  # 等 500ms 阈值
        assert click_label(gui, "b.txt"), "F8 再次单击未命中 b.txt（可能仍在改名态）"
        c.check("F8a 再次单击已选中节点触发改名（输入框出现）", wait_editing(gui, want=True, timeout=3.0))
        # ESC 取消改名（TreeNode @keyup.escape=cancelEdit）
        c.check("F8b ESC 取消改名（输入框消失）", exit_editing(gui))

        # F2 改名（键盘路径；同样复位 lastRowClick，避免上一击残留直接进改名而弱化断言）
        assert click_label(gui, "a.txt"), "F8c 前置：a.txt 未命中"
        time.sleep(0.3)
        assert click_label(gui, "b.txt"), "F8c 前置：b.txt 未命中"
        time.sleep(0.3)
        J(gui, "document.dispatchEvent(new KeyboardEvent('keydown',{key:'F2',bubbles:true})); 'ok'")
        c.check("F8c F2 触发改名", wait_editing(gui, want=True, timeout=3.0))
        exit_editing(gui)

        # F9 Delete 键 → 确认框出现（不实际删除）
        gui.console(clear=True)
        click_label(gui, "a.txt")
        time.sleep(0.3)
        J(gui, "document.dispatchEvent(new KeyboardEvent('keydown',{key:'Delete',bubbles:true})); 'ok'")
        time.sleep(1.0)
        dlg = J(gui, "!!document.querySelector('.dialog-shell')")
        containers = J(gui, "document.querySelectorAll('body > div[id^=dialog]').length")
        bodyHas = J(gui, "document.body.textContent.includes('删除')")
        errs = [e.get('text') for e in (gui.console() or {}).get('entries', []) if e.get('level') in ('error', 'warn')]
        c.check("F9a Delete 键弹出删除确认框", dlg, f"containers={containers} errs={errs[:2]}")
        # 取消确认框
        J(gui, """(()=>{const btns=Array.from(document.querySelectorAll('.dialog-shell button'));const b=btns.find(x=>/取消|cancel/i.test(x.textContent||''));if(b){b.click();return true}return false})()""")
        time.sleep(0.4)

    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

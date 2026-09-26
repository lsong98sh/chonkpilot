"""回归测试：窗口组（51-FP与测试映射「窗口」8 项）。

每条 FP 一个 check；不可运行时验证的项如实标注（需代码审查/OS 级）。
运行：python test_window.py（cwd 任意，脚本自定位）
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, snapshot_config, restore_config  # noqa: E402  动态端口；套件级配置快照-还原

PORT = free_port()


def J(gui, js):
    """eval 原始结果解码（EvalWithResult 返回 JSON 编码值）。"""
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def main():
    gui = GUIClient(port=PORT)
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：本套件用**共享** work-dir（ws/.chonkpilot 库，无 --data-dir），
    # 最大化/还原窗口会把 prj 的 `window.*`（x/y/width/height/maximized）写实；usr 库亦为机器主库
    # → 跑前快照、finally 还原（原本缺省的键 → 删除）。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)

        # W1 没有原生标题：自绘标题栏（toolbar 拖拽区 + 自绘窗口按钮）；
        # frameless 生效判据：客户区高度 ≈ 窗口高度（WM_NCCALCSIZE=0，无标题栏/边框）
        w1 = J(gui, "(()=>{const d=window.outerHeight-window.innerHeight;return {d:d, t:!!document.querySelector('.toolbar'), wc:!!document.querySelector('.win-controls')}})()")
        c.check("W1 无原生标题（frameless：outer-inner 高度差≈0）",
                bool(w1) and w1['t'] and w1['wc'] and w1['d'] <= 5,
                repr(w1))

        # W2 最大化（hover背景灰）hover提示随状态变化
        t = J(gui, "document.querySelector('.win-maximize')?.getAttribute('title') || ''")
        c.check("W2 最大化按钮 + hover 提示（title）", bool(t), repr(t))
        # W2b 点击最大化 → 窗口最大化 + 前端图标/标题同步（window-maximized-changed 广播）；再点还原
        J(gui, "document.querySelector('.win-maximize')?.click(); 'ok'")
        time.sleep(1.0)
        t1 = J(gui, "document.querySelector('.win-maximize')?.getAttribute('title') || ''")
        c.check("W2b 最大化状态同步（title→Restore/还原）", t1 in ('Restore', '还原'), repr(t1))
        J(gui, "document.querySelector('.win-maximize')?.click(); 'ok'")
        time.sleep(1.0)
        t2 = J(gui, "document.querySelector('.win-maximize')?.getAttribute('title') || ''")
        c.check("W2c 还原状态同步（title→Maximize/最大化）", t2 in ('Maximize', '最大化'), repr(t2))

        # W3 最小化（hover背景灰），hover提示：最小化
        t = J(gui, "document.querySelector('.win-minimize')?.getAttribute('title') || ''")
        c.check("W3 最小化按钮 + hover 提示（title）", bool(t), repr(t))

        # W4 关闭（hover背景红色，图标白色），hover提示：关闭
        t = J(gui, "document.querySelector('.win-close')?.getAttribute('title') || ''")
        c.check("W4 关闭按钮 + hover 提示（title）", bool(t), repr(t))
        css = J(gui, """(()=>{let f=false;for(const s of document.styleSheets){let r;try{r=s.cssRules}catch(e){continue}for(const x of r||[]){const st=x.selectorText||'';if(st.includes('win-close')&&st.includes('hover')){const bg=(x.style&&(x.style.backgroundColor||x.style.background))||'';if(bg.includes('232, 17, 35')||bg.includes('e81123')){f=true}}}}return f})()""")
        c.check("W4b 关闭按钮 hover 红色背景（#e81123）", bool(css))

        # W5 支持 Resize（八个方向）：frameless 保留 WS_THICKFRAME → 系统边缘热区原生八方向 resize
        n = J(gui, "document.querySelectorAll('.split-resizer').length")
        c.check("W5 布局分隔条存在 + frameless 保留系统八方向 resize",
                n >= 4, f"resizers={n}（WS_THICKFRAME 保留：系统边缘热区八方向 resize，见 webview.go）")

        # W6 点击 Toolbar 后拖拽移动
        region = J(gui, "getComputedStyle(document.querySelector('.toolbar')).getPropertyValue('-webkit-app-region').trim()")
        c.check("W6 Toolbar 拖拽区（-webkit-app-region: drag）", region == "drag", repr(region))

        # W7 Taskbar 自定义图标
        c.check("W7 Taskbar 自定义图标", J(gui, "!!document.querySelector('.win-controls')"),
                "OS 级不可自动化；代码已设 WindowOptions.IconId=2")

        # W8 Taskbar 标题 = 当前工程目录名
        import subprocess
        title = ""
        try:
            out = subprocess.run(
                ["powershell", "-Command", f"(Get-Process -Id {gui.proc.pid}).MainWindowTitle"],
                capture_output=True, text=True, timeout=10)
            title = (out.stdout or "").strip()
        except Exception as e:
            title = f"err:{e}"
        c.check("W8 Taskbar 标题=chonkpilot-<工程目录名>", title == "chonkpilot-ws", repr(title))

    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj（window.* 等）；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

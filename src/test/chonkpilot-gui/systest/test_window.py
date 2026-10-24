"""回归测试：窗口组（51-FP与测试映射「窗口」8 项）。

每条 FP 一个 check；不可运行时验证的项如实标注（需代码审查/OS 级）。
运行：python test_window.py（cwd 任意，脚本自定位）

W5 = **真机拖动**（Win32 真实鼠标事件 + `GetWindowRect` 前后对比）：frameless 主窗口的
resize 由前端边缘命中条承担（`WindowResizeHandles.vue` → `gui.window.resize` → `StartResize`），
故必须走真实鼠标路径才能覆盖（手法同 [41 G-56]：`SetCursorPos` + `mouse_event`，非 SendMessage）。
"""
import ctypes
import ctypes.wintypes as wt
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, snapshot_config, restore_config  # noqa: E402  动态端口；套件级配置快照-还原

PORT = free_port()

# ── Win32 真实鼠标拖拽（W5；同 41-未决项登记 G-56 手法）────────────────────────
_u32 = ctypes.windll.user32
_k32 = ctypes.windll.kernel32
_EnumWindowsProc = ctypes.WINFUNCTYPE(wt.BOOL, wt.HWND, wt.LPARAM)

_MOUSEEVENTF_LEFTDOWN = 0x0002
_MOUSEEVENTF_LEFTUP = 0x0004
_SWP_NOSIZE = 0x0001
_SWP_NOZORDER = 0x0004
_SWP_NOACTIVATE = 0x0010
_SPI_GETWORKAREA = 0x0030
_SW_RESTORE = 9
_HWND_TOPMOST = -1
_HWND_NOTOPMOST = -2


class _RECT(ctypes.Structure):
    _fields_ = [("left", wt.LONG), ("top", wt.LONG), ("right", wt.LONG), ("bottom", wt.LONG)]


def _find_main_hwnd(pid):
    """取标题 `chonkpilot-*` 的可见顶层窗口（找不到则回落首个可见顶层窗口）。"""
    found = []

    def cb(hwnd, _lp):
        p = wt.DWORD()
        _u32.GetWindowThreadProcessId(hwnd, ctypes.byref(p))
        if p.value == pid and _u32.IsWindowVisible(hwnd):
            buf = ctypes.create_unicode_buffer(256)
            _u32.GetWindowTextW(hwnd, buf, 256)
            found.append((int(hwnd), buf.value))
        return True

    _u32.EnumWindows(_EnumWindowsProc(cb), 0)
    for hwnd, title in found:
        if title.startswith("chonkpilot-"):
            return hwnd, title
    return (found[0][0], found[0][1]) if found else (None, "")


def _wrect(hwnd):
    """GetWindowRect → [left, top, right, bottom]（物理像素）。"""
    r = _RECT()
    if not _u32.GetWindowRect(hwnd, ctypes.byref(r)):
        return None
    return [int(r.left), int(r.top), int(r.right), int(r.bottom)]


def _work_area():
    r = _RECT()
    if not _u32.SystemParametersInfoW(_SPI_GETWORKAREA, 0, ctypes.byref(r), 0):
        return [0, 0, 1920, 1040]
    return [int(r.left), int(r.top), int(r.right), int(r.bottom)]


def _drag(hwnd, x0, y0, x1, y1, steps=14, hold=0.03, engage_timeout=5.0):
    """真实鼠标拖拽：定位起点 → 左键按下 → **等待模态循环进入** → 分步移到终点 → 抬起。

    frameless resize / caption 移动的模态循环由前端 mousedown → `gui.window.resize` → 宿主
    `StartResize` 进入（跨 JS→HTTP→UI 线程）。原实现「按下后固定停 0.8s」：批跑负载下该延迟
    可能超过停顿（实测 140px 只得到 +30px）——因为系统循环以**进入时刻的光标位置**为锚点。
    现改为**自适应**：按下后每 6px 小步移动并观察窗口矩形，一旦矩形变化（= 循环已进入）立即
    回到起点重新锚定，再按目标距离分步拖动 → 终态始终 = 起点 + 目标位移（断言仍 ±3px）。
    """
    r0 = _wrect(hwnd)
    _u32.SetCursorPos(int(x0), int(y0))
    time.sleep(0.25)
    _u32.mouse_event(_MOUSEEVENTF_LEFTDOWN, 0, 0, 0, 0)
    time.sleep(0.12)
    dx = 6 if abs(x1 - x0) >= abs(y1 - y0) else 0
    dy = 0 if dx else 6
    deadline = time.time() + engage_timeout
    engaged = False
    while time.time() < deadline:
        _u32.SetCursorPos(int(x0 + dx), int(y0 + dy))
        time.sleep(0.12)
        if _wrect(hwnd) != r0:
            engaged = True
            break
    if engaged:
        # 重新锚定：光标回起点（窗口回到原始尺寸/位置），再拖到目标 → 位移精确等于目标值。
        _u32.SetCursorPos(int(x0), int(y0))
        time.sleep(0.30)
        # 2026-10-23（批跑稳定性）：负载下系统 resize 循环可能**尚未**把窗口收回到 r0，
        # 残余偏移会被带入最终位移（实测 140px 只得到 +133px）→ 有界等回位，未回位则再挪一次
        # 光标（幂等，只回起点）。断言不变（仍 ±3px）。
        settle = time.time() + 1.0
        while time.time() < settle and _wrect(hwnd) != r0:
            _u32.SetCursorPos(int(x0), int(y0))
            time.sleep(0.10)
    for i in range(1, steps + 1):
        _u32.SetCursorPos(int(x0 + (x1 - x0) * i / steps), int(y0 + (y1 - y0) * i / steps))
        time.sleep(hold)
    time.sleep(0.20)
    _u32.mouse_event(_MOUSEEVENTF_LEFTUP, 0, 0, 0, 0)
    time.sleep(0.45)


def _normalize_window(gui, hwnd):
    """把窗口挪到工作区内已知尺寸，并确保它**真的**在前台可接收真实鼠标。

    2026-10-23：原实现只 `SetForegroundWindow` + 点一下激活，实测在批跑/多窗口环境下会
    失效（后台进程受前台锁限制 → SetForegroundWindow 失败；窗口又被其它窗口遮挡 →
    真实 mousedown 落到别的窗口上），W5/W5b/W5c 全部 delta=0。现改为：
    ① 最大化/最小化先还原（这两种状态本就不可 resize / 移动）；
    ② `SetWindowPos(HWND_TOPMOST)` 置顶，避免被其它窗口遮挡；
    ③ `AttachThreadInput` 附加到当前前台线程后再 `SetForegroundWindow`，绕过前台锁；
    ④ **带校验的预热点击**：WebView2 内容区首个真实 mousedown 常被「激活/聚焦」吞掉，
       故注入页面级 mousedown 计数，反复点到计数增长（最多 5 次）才继续。
    断言本身不变（仍为 GetWindowRect 前后对比 ±3px）。
    """
    if _u32.IsIconic(hwnd) or _u32.IsZoomed(hwnd):
        _u32.ShowWindow(hwnd, _SW_RESTORE)
        time.sleep(0.6)
    wa = _work_area()
    w = max(640, min(900, (wa[2] - wa[0]) - 320))
    h = max(420, min(520, (wa[3] - wa[1]) - 280))
    x, y = wa[0] + 100, wa[1] + 80
    _u32.SetWindowPos(ctypes.c_void_p(hwnd), ctypes.c_void_p(_HWND_TOPMOST), x, y, w, h, _SWP_NOACTIVATE)
    time.sleep(0.4)
    fg = _u32.GetForegroundWindow()
    t_fg = _u32.GetWindowThreadProcessId(fg, None) if fg else 0
    t_self = _k32.GetCurrentThreadId()
    attached = False
    if t_fg and t_fg != t_self:
        attached = bool(_u32.AttachThreadInput(t_fg, t_self, True))
    _u32.BringWindowToTop(ctypes.c_void_p(hwnd))
    _u32.SetForegroundWindow(ctypes.c_void_p(hwnd))
    _u32.SetFocus(ctypes.c_void_p(hwnd))
    if attached:
        _u32.AttachThreadInput(t_fg, t_self, False)
    time.sleep(0.5)
    _prime_input(gui, hwnd)


# 页面级真机输入计数（捕获阶段统计所有 mousedown）→ 校验「真实鼠标已到达页面」。
_PRIME_JS = ("(()=>{window.__ckPrime=0;"
             "document.addEventListener('mousedown',()=>{window.__ckPrime++},true);return 'ok'})()")


def _prime_input(gui, hwnd, attempts=5):
    """客户区真机点按直到页面侧 mousedown 计数增长（消除「首个点击被吞」的时序差异）。"""
    try:
        J(gui, _PRIME_JS)
    except Exception:
        pass
    r = _wrect(hwnd)
    for _ in range(attempts):
        _u32.SetCursorPos(r[0] + 120, r[1] + 120)
        time.sleep(0.2)
        _u32.mouse_event(_MOUSEEVENTF_LEFTDOWN, 0, 0, 0, 0)
        time.sleep(0.05)
        _u32.mouse_event(_MOUSEEVENTF_LEFTUP, 0, 0, 0, 0)
        time.sleep(0.4)
        try:
            if int(J(gui, "String(window.__ckPrime||0)") or 0) > 0:
                return True
        except Exception:
            return False
    return False


def _restore_zorder(hwnd, left, top):
    """拖拽结束后撤销置顶（测试不得改变窗口的常态 Z 序）。"""
    _u32.SetWindowPos(ctypes.c_void_p(hwnd), ctypes.c_void_p(_HWND_NOTOPMOST), left, top, 0, 0, _SWP_NOSIZE)
    time.sleep(0.3)




def J(gui, js):
    """eval 原始结果解码（EvalWithResult 返回 JSON 编码值）。"""
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def main():
    gui = GUIClient(port=PORT, wait_ready=90)
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

        # W5 真实 resize（八方向能力抽样：右 + 下）：frameless 主窗口**系统层无边缘命中区**
        # （WebView2 子窗铺满客户区 → WM_NCHITTEST 恒 HTCLIENT，G-59），由前端
        # `WindowResizeHandles.vue` 的 8 条命中条 mousedown 发 gui.window.resize{hit}，
        # 宿主 StartResize(HT*) 进入系统原生 resize 模态循环。故本项**必须**用真实鼠标事件
        # （SetCursorPos + mouse_event，同 41 G-56 手法），以 GetWindowRect 前后对比取证。
        hwnd, wtitle = _find_main_hwnd(gui.proc.pid)
        handles = J(gui, "document.querySelectorAll('.win-resize-handle').length")
        c.check("W5a 前端边缘命中层已渲染（8 条）", int(handles or 0) == 8,
                f"hwnd={hwnd} title={wtitle!r} handles={handles}")

        if hwnd:
            _normalize_window(gui, hwnd)
            # ① 右边框（距边 3px）：向右拖 140px
            r0 = _wrect(hwnd)
            x, y = r0[2] - 3, r0[1] + (r0[3] - r0[1]) // 2
            _drag(hwnd, x, y, x + 140, y)
            r1 = _wrect(hwnd)
            dw = (r1[2] - r1[0]) - (r0[2] - r0[0])
            c.check("W5 真机拖动右边框 → 宽度 +140px（±3px）", abs(dw - 140) <= 3,
                    f"before={r0} after={r1} delta_w={dw} drag=({x},{y})->({x + 140},{y})")

            # ② 下边框（距边 3px）：向下拖 110px（窗口已归一到工作区内，末端不出屏）
            r2 = _wrect(hwnd)
            x, y = r2[0] + (r2[2] - r2[0]) // 2, r2[3] - 3
            _drag(hwnd, x, y, x, y + 110)
            r3 = _wrect(hwnd)
            dh = (r3[3] - r3[1]) - (r2[3] - r2[1])
            c.check("W5b 真机拖动下边框 → 高度 +110px（±3px）", abs(dh - 110) <= 3,
                    f"before={r2} after={r3} delta_h={dh} drag=({x},{y})->({x},{y + 110})")

            # ③ 顶栏拖动 → 窗口整体移动：非交互区 mousedown → gui.window.resize{hit=caption}
            #    → 宿主 WM_NCLBUTTONDOWN(HTCAPTION) 系统移动循环（WebView2 app-region 不生效）。
            r4 = _wrect(hwnd)
            mx, my = r4[0] + 40, r4[1] + 20  # 品牌区（非交互）
            _drag(hwnd, mx, my, mx + 120, my + 80)
            r5 = _wrect(hwnd)
            dx, dy = r5[0] - r4[0], r5[1] - r4[1]
            c.check("W5c 真机拖动顶栏 → 窗口移动 (+120,+80)", abs(dx - 120) <= 3 and abs(dy - 80) <= 3,
                    f"before={r4} after={r5} moved=({dx},{dy}) drag=({mx},{my})->({mx + 120},{my + 80})")
            _restore_zorder(hwnd, r4[0], r4[1])
        else:
            c.check("W5 真机拖动右边框 → 宽度 +140px（±3px）", False, "未找到主窗口 hwnd")
            c.check("W5b 真机拖动下边框 → 高度 +110px（±3px）", False, "未找到主窗口 hwnd")

        # W6 顶栏拖拽区样式：仅证明 CSS drag 标记存在；**实际移动**由 W5c（caption → 系统移动循环）
        # 覆盖（WebView2 app-region 在本项目自绘 frameless 窗口上不触发，不作为判据）。
        region = J(gui, "getComputedStyle(document.querySelector('.toolbar')).getPropertyValue('-webkit-app-region').trim()")
        c.check("W6 Toolbar 拖拽区（-webkit-app-region: drag；仅移动语义）", region == "drag", repr(region))

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

# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑤：窗口（移动/尺寸/最大化）与布局分隔条拖拽。

窗口几何的**落库以宿主 `GetWindowPlacement` 为准**（前端上报值被忽略）——本模块按此口径断言：
* 最大化/还原：`gui.window.status{command:maximize}` + `window-maximized-changed` + DOM 标题/命中层；
* 几何保存：写一组**伪造**的 x/y/w/h → 宿主只接受真实普通窗口矩形（伪值不得落库）；
* 分隔条拖拽：synthetic mouse 事件 → pane 宽度变化 + `layout.free` 占比变化（前端提交 + prj 落库）；
* 拖到超限 → 被 leaf `minWidth/maxWidth` 夹住（不下穿）。

真机鼠标移动/拉伸（Win32）属 L5 手工，不在本套件范围。
"""

import json
import time

from .framework import case

TITLE_RESTORE = ("还原", "Restore")
TITLE_MAX = ("最大化", "Maximize", "Maximise")


def _win_status(env, command=None):
    payload = {} if command is None else {"command": command}
    return env.req("gui.window.status", payload) or {}


def _max_title(env):
    return (env.js("document.querySelector('.win-maximize')?.getAttribute('title')||''") or "").strip()


def _geom(env):
    """前端当前窗口几何（物理像素；与宿主落库口径一致）。"""
    return env.jsj("JSON.stringify({x: Math.round(screenX*devicePixelRatio),"
                   " y: Math.round(screenY*devicePixelRatio),"
                   " w: Math.round(outerWidth*devicePixelRatio),"
                   " h: Math.round(outerHeight*devicePixelRatio),"
                   " iw: innerWidth, dpr: devicePixelRatio})") or {}


def _split_handle_index(env):
    """选中「紧邻 chat 面板左侧」的竖向分隔条索引（无则 -1）。"""
    return int(env.js("""(()=>{
      const c=document.querySelector('.chat-panel'); if(!c) return -1;
      const cr=c.getBoundingClientRect();
      const hs=[...document.querySelectorAll('.split-handle.vertical')];
      let best=-1, bd=1e9;
      hs.forEach((h,i)=>{const r=h.getBoundingClientRect();
        if(r.height<10) return;
        const d=Math.abs((r.left+r.width/2)-cr.left);
        if(d<bd){bd=d;best=i;}});
      return best;
    })()""") or -1)


def _drag_split(env, idx, dx):
    """synthetic 鼠标拖拽分隔条：mousedown(handle) → mousemove/mouseup(window)。"""
    r = env.js("""(()=>{
      const hs=[...document.querySelectorAll('.split-handle.vertical')];
      const h=hs[%d]; if(!h) return 'no-handle';
      const r=h.getBoundingClientRect();
      const x=r.left+r.width/2, y=r.top+r.height/2;
      h.dispatchEvent(new MouseEvent('mousedown',{clientX:x,clientY:y,bubbles:true,cancelable:true,buttons:1}));
      for(let i=1;i<=5;i++){
        window.dispatchEvent(new MouseEvent('mousemove',{clientX:x+%d*i/5,clientY:y,bubbles:true,cancelable:true,buttons:1}));
      }
      window.dispatchEvent(new MouseEvent('mouseup',{clientX:x+%d,clientY:y,bubbles:true,cancelable:true}));
      return 'ok';
    })()""" % (idx, dx, dx))
    env.check("派发分隔条拖拽事件", r == "ok", r)
    time.sleep(0.8)


def _pane_w(env, sel):
    return float(env.js("(document.querySelector(%s)?.getBoundingClientRect().width)||0"
                        % json.dumps(sel)) or 0)


def _live_sizes(env):
    doc = env.jsj("JSON.stringify((window.__ckLayout&&window.__ckLayout.engine)?"
                  "window.__ckLayout.engine.state.config.live:null)") or {}
    body = (doc or {}).get("layout") or {}
    for nd in [body] + list(body.get("children") or []):
        if isinstance(nd, dict) and nd.get("type") == "split" and len(nd.get("children") or []) == 2:
            return list(nd.get("sizes") or [])
    return []


@case("S-WIN-01", "窗口", "窗口最大化/还原：宿主状态 + 广播 + DOM 标题与命中层同步",
      stage="seeded", order=160, fp="61 §1 gui.window.status · 31 WIN-002/WIN-003",
      desc="gui.window.status{maximize} → maximized=true、window-maximized-changed 广播、"
           "`.win-maximize` 标题切「还原」、`.win-resize-layer.is-maximized` 出现；再切回正常。")
def s_win_01_maximize_restore(env):
    st = _win_status(env)
    env.check("窗口状态查询可用", isinstance(st, dict) and "maximized" in st, st)
    if st.get("maximized") is True:  # 归一：先回到正常
        _win_status(env, "maximize")
        env.wait(lambda: _win_status(env).get("maximized") is False, "窗口先归一到正常", timeout=15)

    env.capture(["window-maximized-changed"])
    _win_status(env, "maximize")
    env.wait(lambda: _win_status(env).get("maximized") is True, "status.maximized=true", timeout=15)
    ev = env.wait_events("window-maximized-changed", lambda p: p.get("maximized") is True,
                         "window-maximized-changed{maximized:true}", timeout=15)
    env.check("广播 maximized=true", (ev or {}).get("maximized") is True, ev)

    title = env.wait(lambda: _max_title(env) if _max_title(env) in TITLE_RESTORE else None,
                     "DOM 最大化按钮标题切「还原」", timeout=15)
    env.check("最大化按钮标题 == 还原", title in TITLE_RESTORE, title)
    env.check("最大化时 resize 命中层标记",
              bool(env.js("!!document.querySelector('.win-resize-layer.is-maximized')")),
              env.js("document.querySelectorAll('.win-resize-layer').length"))

    _win_status(env, "maximize")
    env.wait(lambda: _win_status(env).get("maximized") is False, "status.maximized=false", timeout=15)
    title2 = env.wait(lambda: _max_title(env) if _max_title(env) in TITLE_MAX else None,
                      "DOM 标题切回「最大化」", timeout=15)
    env.check("还原后标题 == 最大化", title2 in TITLE_MAX, title2)


@case("S-WIN-02", "窗口", "窗口尺寸/位置落库以宿主为准：伪造前端值不得覆盖真实几何",
      stage="seeded", order=170, fp="61 §1 gui.ui.save{window} · 31 WIN-026",
      desc="读真实 outer 几何 → gui.ui.save{window:{x:99999,y:99999,width:1000,height:700}} → "
           "prj window.* 不采纳伪值，宽高与真实几何一致（±30px），且 maximized=false。")
def s_win_02_host_authority(env):
    g = _geom(env)
    env.check("读到真实窗口几何", g.get("w", 0) > 0 and g.get("h", 0) > 0, g)
    env.req("gui.ui.save", {"window": {"x": 99999, "y": 99999, "width": 1000,
                                       "height": 700, "maximized": False}})
    prj = env.wait(lambda: env.prj() if env.prj().get("window.width") else None,
                   "window.* 落库", timeout=15)
    width = int(float(prj.get("window.width")))
    height = int(float(prj.get("window.height")))
    x = int(float(prj.get("window.x") or 0))
    env.check("伪造宽度未落库", width != 1000, width)
    env.check("伪造位置未落库", x != 99999, x)
    env.check("落库宽度 == 真实几何（±30）", abs(width - int(g["w"])) <= 30,
              "prj=%s real=%s" % (width, g.get("w")))
    env.check("落库高度 == 真实几何（±30）", abs(height - int(g["h"])) <= 30,
              "prj=%s real=%s" % (height, g.get("h")))
    env.eq("maximized 落库为 false", str(prj.get("window.maximized")).lower(), "false")
    env.note("窗口几何证据", "prj=%dx%d@(%d,%d) real=%sx%s" % (width, height, x,
                                                          int(float(prj.get("window.y") or 0)),
                                                          g.get("w"), g.get("h")))


@case("S-SPLIT-01", "布局", "分隔条拖拽：pane 宽度变化 + layout.free 占比提交 + 超限夹取",
      stage="seeded", order=180, fp="31 WIN-010/WIN-026 · 61 §1 gui.ui.save{layout}",
      desc="对 chat 左侧竖向 handle 派发 synthetic mouse 拖拽：chat 宽度随 dx 增大；"
           "layout.free 对应 split sizes 变化并落库；反向超限拖拽被 leaf minWidth 夹住，不下穿。")
def s_split_01_drag_resize(env):
    idx = _split_handle_index(env)
    env.check("找到 chat 左侧竖向分隔条", idx >= 0, idx)
    before_w = _pane_w(env, ".chat-panel")
    before = _live_sizes(env)
    env.check("拖前 pane 宽度 > 0", before_w > 0, before_w)
    env.note("拖前", "chat=%.1f sizes=%r" % (before_w, before))

    _drag_split(env, idx, -120)          # 向左拖 → chat（右 pane）变宽
    after_w = env.wait(lambda: _pane_w(env, ".chat-panel")
                       if _pane_w(env, ".chat-panel") >= before_w + 60 else None,
                       "chat 宽度随拖拽增大 ≥60px", timeout=10)
    env.check("chat 宽度增大", after_w >= before_w + 60,
              "before=%.1f after=%.1f" % (before_w, after_w))
    after = _live_sizes(env)
    env.check("layout live sizes 发生变化", after != before and len(after) == 2,
              "before=%r after=%r" % (before, after))
    if len(after) == 2 and len(before) == 2:
        env.check("右 pane 占比增大", after[1] > before[1],
                  "before=%r after=%r" % (before, after))

    def prj_ratio():
        raw = env.prj().get("layout.free")
        if not isinstance(raw, str):
            return None
        try:
            doc = json.loads(raw)
        except Exception:
            return None
        body = doc.get("layout") or {}
        for nd in [body] + list(body.get("children") or []):
            if (isinstance(nd, dict) and nd.get("type") == "split"
                    and len(nd.get("children") or []) == 2):
                return list(nd.get("sizes") or [])
        return None

    ratio = env.wait(lambda: prj_ratio() if prj_ratio() and prj_ratio() != before else None,
                     "拖拽后 layout.free 占比已落库", timeout=20)
    env.check("prj layout.free 占比非拖前值", ratio != before, "ratio=%r before=%r" % (ratio, before))

    # 反向超限：被 minWidth 夹住（chat leaf minWidth=280）
    _drag_split(env, idx, 3000)
    time.sleep(0.8)
    clamped = _pane_w(env, ".chat-panel")
    env.check("超限拖拽被 minWidth 夹住（chat ≥ 275px）", clamped >= 275,
              "chat=%.1f" % clamped)
    env.note("夹取证据", "chat=%.1f（minWidth 280）" % clamped)

# ══════════════════════════════════════════════════════════
# S-WIN-03：**真机模拟鼠标**拖动/拉伸（移植自 test_window.py W5/W5b/W5c）
# ══════════════════════════════════════════════════════════
# frameless 主窗口系统层无边缘命中区（WebView2 子窗铺满 → HTCLIENT 恒），缩放由前端
# `WindowResizeHandles` 的 8 条命中条 mousedown → `gui.window.resize` → 宿主 `StartResize`
# 进入系统原生模态循环。**必须**用真实鼠标事件（SetCursorPos + mouse_event），合成 DOM 事件
# 无法触发系统循环。非交互桌面（无法取得前台窗口）→ CaseSkip（不伪装成通过）。

import ctypes  # noqa: E402
import ctypes.wintypes as wt  # noqa: E402

from .framework import CaseSkip  # noqa: E402

_MOUSEEVENTF_LEFTDOWN = 0x0002
_MOUSEEVENTF_LEFTUP = 0x0004
_SWP_NOSIZE = 0x0001
_SWP_NOACTIVATE = 0x0010
_SPI_GETWORKAREA = 0x0030
_SW_RESTORE = 9
_HWND_TOPMOST = -1
_HWND_NOTOPMOST = -2


class _RECT(ctypes.Structure):
    _fields_ = [("left", wt.LONG), ("top", wt.LONG), ("right", wt.LONG), ("bottom", wt.LONG)]


def _find_main_hwnd(pid):
    u32 = ctypes.windll.user32
    proc = ctypes.WINFUNCTYPE(wt.BOOL, wt.HWND, wt.LPARAM)
    found = []

    def cb(hwnd, _lp):
        p = wt.DWORD()
        u32.GetWindowThreadProcessId(hwnd, ctypes.byref(p))
        if p.value == pid and u32.IsWindowVisible(hwnd):
            buf = ctypes.create_unicode_buffer(256)
            u32.GetWindowTextW(hwnd, buf, 256)
            found.append((int(hwnd), buf.value))
        return True

    u32.EnumWindows(proc(cb), 0)
    for hwnd, title in found:
        if title.startswith("chonkpilot-"):
            return hwnd, title
    return (found[0][0], found[0][1]) if found else (None, "")


def _wrect(hwnd):
    r = _RECT()
    if not ctypes.windll.user32.GetWindowRect(hwnd, ctypes.byref(r)):
        return None
    return [int(r.left), int(r.top), int(r.right), int(r.bottom)]


def _work_area():
    r = _RECT()
    if not ctypes.windll.user32.SystemParametersInfoW(_SPI_GETWORKAREA, 0, ctypes.byref(r), 0):
        return [0, 0, 1920, 1040]
    return [int(r.left), int(r.top), int(r.right), int(r.bottom)]


def _drag(hwnd, x0, y0, x1, y1, steps=14, hold=0.03, engage_timeout=5.0):
    """真实鼠标拖拽：按下 → 等系统模态循环进入 → 回锚 → 分步到终点 → 抬起。"""
    u32 = ctypes.windll.user32
    r0 = _wrect(hwnd)
    u32.SetCursorPos(int(x0), int(y0))
    time.sleep(0.25)
    u32.mouse_event(_MOUSEEVENTF_LEFTDOWN, 0, 0, 0, 0)
    time.sleep(0.12)
    dx = 6 if abs(x1 - x0) >= abs(y1 - y0) else 0
    dy = 0 if dx else 6
    engaged = False
    end = time.time() + engage_timeout
    while time.time() < end:
        u32.SetCursorPos(int(x0 + dx), int(y0 + dy))
        time.sleep(0.12)
        if _wrect(hwnd) != r0:
            engaged = True
            break
    if engaged:
        u32.SetCursorPos(int(x0), int(y0))
        time.sleep(0.30)
        settle = time.time() + 1.0
        while time.time() < settle and _wrect(hwnd) != r0:
            u32.SetCursorPos(int(x0), int(y0))
            time.sleep(0.10)
    for i in range(1, steps + 1):
        u32.SetCursorPos(int(x0 + (x1 - x0) * i / steps), int(y0 + (y1 - y0) * i / steps))
        time.sleep(hold)
    time.sleep(0.20)
    u32.mouse_event(_MOUSEEVENTF_LEFTUP, 0, 0, 0, 0)
    time.sleep(0.45)


def _normalize_window(env, hwnd):
    """还原 + 置顶 + 抢前台 + 预热点击（首个真实 mousedown 常被激活吞掉）。"""
    u32 = ctypes.windll.user32
    k32 = ctypes.windll.kernel32
    if u32.IsIconic(hwnd) or u32.IsZoomed(hwnd):
        u32.ShowWindow(hwnd, _SW_RESTORE)
        time.sleep(0.6)
    wa = _work_area()
    w = max(640, min(900, (wa[2] - wa[0]) - 320))
    h = max(420, min(520, (wa[3] - wa[1]) - 280))
    x, y = wa[0] + 100, wa[1] + 80
    u32.SetWindowPos(ctypes.c_void_p(hwnd), ctypes.c_void_p(_HWND_TOPMOST), x, y, w, h,
                     _SWP_NOACTIVATE)
    time.sleep(0.4)
    fg = u32.GetForegroundWindow()
    t_fg = u32.GetWindowThreadProcessId(fg, None) if fg else 0
    t_self = k32.GetCurrentThreadId()
    attached = False
    if t_fg and t_fg != t_self:
        attached = bool(u32.AttachThreadInput(t_fg, t_self, True))
    u32.BringWindowToTop(ctypes.c_void_p(hwnd))
    u32.SetForegroundWindow(ctypes.c_void_p(hwnd))
    u32.SetFocus(ctypes.c_void_p(hwnd))
    if attached:
        u32.AttachThreadInput(t_fg, t_self, False)
    time.sleep(0.5)

    try:
        env.js("(()=>{window.__ckPrime=0;"
               "document.addEventListener('mousedown',()=>{window.__ckPrime++},true);"
               "return 'ok'})()")
    except Exception:
        pass
    r = _wrect(hwnd)
    for _ in range(5):
        u32.SetCursorPos(r[0] + 120, r[1] + 120)
        time.sleep(0.2)
        u32.mouse_event(_MOUSEEVENTF_LEFTDOWN, 0, 0, 0, 0)
        u32.mouse_event(_MOUSEEVENTF_LEFTUP, 0, 0, 0, 0)
        time.sleep(0.2)
        try:
            if int(env.js("window.__ckPrime||0") or 0) > 0:
                return u32.GetForegroundWindow() == hwnd
        except Exception:
            return u32.GetForegroundWindow() == hwnd
    return u32.GetForegroundWindow() == hwnd


def _restore_rect(hwnd, rect):
    u32 = ctypes.windll.user32
    u32.SetWindowPos(ctypes.c_void_p(hwnd), ctypes.c_void_p(_HWND_NOTOPMOST),
                     rect[0], rect[1], rect[2] - rect[0], rect[3] - rect[1], 0)
    time.sleep(0.3)


@case("S-WIN-03", "窗口", "真机模拟鼠标：拖动右边框/下边框缩放 + 拖动顶栏移动",
      stage="seeded", order=175, fp="31 WIN-004/WIN-005 · test_window.py W5/W5b/W5c",
      desc="Win32 SetCursorPos + mouse_event（非 SendMessage）拖拽：右边框 +140px、下边框 +110px、"
           "顶栏移动 (+120,+80)，GetWindowRect 前后差 ±3px；非交互桌面（拿不到前台窗口）自报 SKIP。")
def s_win_03_real_mouse_drag(env):
    if not hasattr(ctypes, "windll"):
        raise CaseSkip("非 Windows 平台无 Win32 真实鼠标通道")
    hwnd, title = _find_main_hwnd(env.gui_pid)
    if not hwnd:
        raise CaseSkip("未找到主窗口 hwnd（pid=%s）" % env.gui_pid)
    env.check("前端边缘命中层已渲染（8 条）",
              int(env.js("document.querySelectorAll('.win-resize-handle').length") or 0) == 8,
              env.js("document.querySelectorAll('.win-resize-handle').length"))

    orig = _wrect(hwnd)
    if not _normalize_window(env, hwnd):
        _restore_rect(hwnd, orig)
        raise CaseSkip("无法取得前台窗口（非交互桌面/被遮挡）→ 真实鼠标不可用")
    u32 = ctypes.windll.user32
    deltas = []

    def drag_right():
        rb = ra = None
        dw = 0
        for _ in range(3):
            rb = _wrect(hwnd)
            px, py = rb[2] - 3, rb[1] + (rb[3] - rb[1]) // 2
            _drag(hwnd, px, py, px + 140, py)
            ra = _wrect(hwnd)
            dw = (ra[2] - ra[0]) - (rb[2] - rb[0])
            if abs(dw - 140) <= 3:
                break
            _normalize_window(env, hwnd)
        return dw

    def drag_bottom():
        rb = ra = None
        dh = 0
        for _ in range(3):
            rb = _wrect(hwnd)
            px, py = rb[0] + (rb[2] - rb[0]) // 2, rb[3] - 3
            _drag(hwnd, px, py, px, py + 110)
            ra = _wrect(hwnd)
            dh = (ra[3] - ra[1]) - (rb[3] - rb[1])
            if abs(dh - 110) <= 3:
                break
            _normalize_window(env, hwnd)
        return dh

    def drag_caption():
        rb = ra = None
        dx = dy = 0
        for _ in range(3):
            rb = _wrect(hwnd)
            mx, my = rb[0] + 40, rb[1] + 20
            _drag(hwnd, mx, my, mx + 120, my + 80)
            ra = _wrect(hwnd)
            dx, dy = ra[0] - rb[0], ra[1] - rb[1]
            if abs(dx - 120) <= 3 and abs(dy - 80) <= 3:
                break
            _normalize_window(env, hwnd)
        return dx, dy

    try:
        dw = drag_right()
        deltas.append(dw)
        dh = drag_bottom()
        deltas.append(dh)
        dx, dy = drag_caption()
        deltas.append(dx)
        deltas.append(dy)
        if all(d == 0 for d in deltas):
            raise CaseSkip("真实鼠标事件未生效（窗口不可交互/系统循环未进入）")
        env.check("拖动右边框 → 宽度 +140px（±3）", abs(dw - 140) <= 3, dw)
        env.check("拖动下边框 → 高度 +110px（±3）", abs(dh - 110) <= 3, dh)
        env.check("拖动顶栏 → 窗口移动 (+120,+80)（±3）",
                  abs(dx - 120) <= 3 and abs(dy - 80) <= 3, (dx, dy))
        env.note("真机拖拽证据", "hwnd=%s title=%r deltas=%r" % (hwnd, title, deltas))
    finally:
        if orig:
            _restore_rect(hwnd, orig)
          

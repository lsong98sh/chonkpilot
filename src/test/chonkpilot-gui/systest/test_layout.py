"""回归测试：窗口布局组（51-FP与测试映射「窗口布局」10 项）。

〔2026-10-16 UILAYOUT-1 P4⑤：**默认形态 = 自由布局容器（free）**〕
本套件原按 legacy（硬编码 `MainLayout` 五层 `SplitPanel`）断言「产品默认像素」；切默认后
渲染路径 = `<ChonkLayout>`（区域树 + 占比 `sizes` + **像素定尺运行时**），故口径同步为：

  · 分隔条 = `.split-handle`（机制层布局容器；**不再是** legacy 的 `.split-resizer`），
    视觉/命中解耦同规则：本体 1px 发丝线（`--split-hairline`）+ 命中区 `::after`（`--split-hit=5px`），
    激活态类名 = `.active`（legacy 为 `.is-active`）；本体恒 1px（无重排）。
  · 尺寸 = **占比 × 可用像素**（`layout.free` 文档 `sizes`）→ 断言取「按默认占比推导的容差区间」，
    不再断言 legacy 的固定像素 320/520/400（那是 legacy 分支的 `ref()` 默认值）。
  · 持久化 = `layout.free`（**不再** `layout.*` 尺寸键；旧键仍可写但 free 形态不读、不回灌）。

任务区**常显**（2026-10-16 用户裁决：三区 chat / task / filetree 去掉显隐能力与 UI 入口）→
SessionTree / SessionChat 恒定挂载；`ensure_task_panel_open` 已退化为 no-op（保留调用以维持链路）。
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, ensure_task_panel_open  # noqa: E402

PORT = free_port()
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_layout_data")
WORK_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_layout_ws")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def JD(gui, js):
    v = J(gui, js)
    if isinstance(v, str):
        try:
            v = json.loads(v)
        except Exception:
            pass
    return v


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    shutil.rmtree(WORK_DIR, ignore_errors=True)
    os.makedirs(WORK_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()
    gui = GUIClient(port=PORT, work_dir=WORK_DIR, data_dir=DATA_DIR)
    c = Checker()
    try:
        gui.start()

        # L0 窗口几何基线（产品默认 1280x800；占比推导的前提）
        VW = J(gui, "window.innerWidth || 0")
        VH = J(gui, "window.innerHeight || 0")
        c.check("L0 窗口几何基线 = 产品默认 1280x800",
                VW == 1280 and 700 <= VH <= 820, f"innerWidth={VW} innerHeight={VH}")

        # L0b 任务区常显（裁决 2）
        shown = J(gui, "!!document.querySelector('.session-chat')")
        c.check("L0b 任务区常显（SessionTree/SessionChat 恒挂载）", shown is True, f"shown={shown}")
        ensure_task_panel_open(gui)

        # L0c 默认形态 = free（容器外壳 + 区域树；legacy `.split-resizer` 不应出现）
        f = J(gui, """(()=>{return JSON.stringify({
  free:!!document.querySelector('.ck-topbar') && !!document.querySelector('.layout-leaf'),
  legacyResizer:document.querySelectorAll('.split-resizer').length,
  handles:document.querySelectorAll('.split-handle').length})})()""")
        fd = json.loads(f)
        c.check("L0c 默认形态 = 自由布局容器（free）", fd["free"] and fd["handles"] >= 4,
                repr(fd))

        # L1 布局六区：toolbar / filetree / preview / chat / task(tasktree+taskview) / statusbar
        zones = J(gui, """(()=>{const q=s=>document.querySelectorAll(s).length;return JSON.stringify({
  toolbar:q('.toolbar'),filetree:q('.filetree-panel'),codeview:q('.code-view'),
  chat:q('.chat-panel'),statusbar:q('.statusbar'),panels:q('.panel-inner')})})()""")
        z = json.loads(zones)
        c.check("L1 六区布局存在", z["toolbar"] > 0 and z["filetree"] > 0 and z["codeview"] > 0
                and z["chat"] > 0 and z["statusbar"] > 0 and z["panels"] >= 2,
                repr(z))

        n = J(gui, "document.querySelectorAll('.split-handle').length")
        c.check("L1b 分隔条（.split-handle）存在", n >= 4, f"handles={n}")

        # L1c 分隔条视觉/命中（与 legacy 同口径，类名改机制层）：本体 1px + 命中区 ::after + .active 填强调色
        vis_js = """(()=>{
  const r=document.querySelector('.split-handle.horizontal');
  if(!r||r.getBoundingClientRect().height<=0) return JSON.stringify({err:'no-handle'});
  const rgb=v=>{const p=document.createElement('div');p.style.background=v;
    document.body.appendChild(p);const c=getComputedStyle(p).backgroundColor;p.remove();return c;};
  const rect=r.getBoundingClientRect();
  const cx=rect.left+rect.width/2, cy=rect.top+rect.height/2;
  const hit=d=>{const e=document.elementFromPoint(cx,cy+d);
    return !!(e&&(e===r||(e.closest&&e.closest('.split-handle')===r)));};
  const color=getComputedStyle(r).backgroundColor;
  const hitNear=hit(1.5)&&hit(-1.5), hitFar=hit(4)||hit(-4);
  const st=document.createElement('style');
  st.textContent='.split-handle::after{transition:none !important}';
  document.head.appendChild(st);
  r.classList.add('active');
  const aAfter=getComputedStyle(r,'::after').backgroundColor;
  const aW=parseFloat(getComputedStyle(r,'::after').height)||0;
  const aBodyH=r.getBoundingClientRect().height;
  r.classList.remove('active');
  st.remove();
  return JSON.stringify({h:rect.height,color:color,refBorder:rgb('var(--lf-border)'),
    accent:aAfter,refAccent:rgb('var(--lf-accent)'),afterH:aW,activeBodyH:aBodyH,
    hitNear:hitNear,hitFar:hitFar});})()"""
        rv = JD(gui, vis_js) or {}
        c.check("L1c 分隔条本体 1px 发丝线（真实渲染高 == 1）",
                isinstance(rv.get("h"), (int, float)) and abs(rv.get("h") - 1) < 0.6,
                f"height={rv.get('h')} err={rv.get('err')}")
        c.check("L1c 分隔条常态色 == --lf-border 实解析 rgb",
                rv.get("color") is not None and rv.get("color") == rv.get("refBorder"),
                f"color={rv.get('color')} --lf-border={rv.get('refBorder')}")
        c.check("L1c 命中区宽于本体（±1.5px 命中 / ±4px 落到相邻 pane）",
                rv.get("hitNear") is True and rv.get("hitFar") is False,
                f"hit(±1.5px)={rv.get('hitNear')} hit(±4px)={rv.get('hitFar')} ::afterH={rv.get('afterH')}")
        c.check("L1c 激活态（.active）：命中区填 --lf-accent 实解析 rgb + 本体不加宽（恒 1px）",
                rv.get("accent") == rv.get("refAccent")
                and isinstance(rv.get("activeBodyH"), (int, float))
                and abs(rv.get("activeBodyH") - 1) < 0.6,
                f"::after色={rv.get('accent')} --lf-accent={rv.get('refAccent')} 本体高={rv.get('activeBodyH')}")

        def pane_w(sel):
            return J(gui, f"(document.querySelector('{sel}')?.getBoundingClientRect().width||0)")

        def pane_h(sel):
            return J(gui, f"(document.querySelector('{sel}')?.getBoundingClientRect().height||0)")

        # L2 filetree 宽度：默认文档占比（左列内 320 : previewMin 280）→ 容差区间（非 legacy 固定 320）
        fw = pane_w(".filetree-panel")
        c.check("L2 filetree 宽度（默认占比推导）落在合理区间", 300 <= fw <= 560, f"actual={fw}px vw={VW}")

        # L3 旧尺寸键仍可写（free 形态不读；只验证既有消息面可用，不回灌）
        try:
            gui.req("gui.ui.save", {"layout": {"filetreeWidth": 300}})
            c.check("L3 旧尺寸键写入（gui.ui.save layout）仍可用", True, "free 形态不读旧键")
        except Exception as e:
            c.check("L3 旧尺寸键写入（gui.ui.save layout）仍可用", False, str(e))

        # L4 chat 宽度：默认占比（body 640 : 520）→ 容差区间
        cw = pane_w(".chat-panel")
        c.check("L4 chat 宽度（默认占比推导）落在合理区间", 480 <= cw <= 660,
                f"actual={cw}px（vw={VW}）")

        # L5 同上（消息面可用性）
        try:
            gui.req("gui.ui.save", {"layout": {"chatWidth": 500}})
            c.check("L5 旧 chat 尺寸写入（gui.ui.save layout）仍可用", True, "ok")
        except Exception as e:
            c.check("L5 旧 chat 尺寸写入（gui.ui.save layout）仍可用", False, str(e))

        # L6 任务区高度（左上/左下 上下分割）：`.panel-inner` 代理（SessionTree/SessionChat/ChatPanel 三处）
        th = pane_h(".panel-inner")
        c.check("L6 任务/对话面板高度可用（>200px）", th > 200, f"actual={th}px")

        # L7 session-tree 宽度（左下 400 : sessionChatMin 240）
        sw = pane_w(".panel-inner")
        c.check("L7 tasktree(session-tree) 宽度落在合理区间", 280 <= sw <= 620, f"actual={sw}px vw={VW}")

        # L8 拖拽分隔条实际改变 chat 宽度；拖拽中本体恒 1px
        js_drag = """(()=>{let best=null;for(const r of document.querySelectorAll('.split-handle.vertical')){const next=r.nextElementSibling;if(next&&next.querySelector('.chat-panel')){const rect=r.getBoundingClientRect();best={el:r,x:rect.left+rect.width/2,y:rect.top+rect.height/2};break}}if(!best)return JSON.stringify({w0:0,rw:0});const w0=document.querySelector('.chat-panel').getBoundingClientRect().width;best.el.dispatchEvent(new MouseEvent('mousedown',{clientX:best.x,clientY:best.y,bubbles:true,cancelable:true}));window.dispatchEvent(new MouseEvent('mousemove',{clientX:best.x-60,clientY:best.y,bubbles:true,cancelable:true}));const rw=best.el.getBoundingClientRect().width;window.dispatchEvent(new MouseEvent('mouseup',{clientX:best.x-60,clientY:best.y,bubbles:true,cancelable:true}));return JSON.stringify({w0:w0,rw:rw});})()"""
        d = JD(gui, js_drag) or {}
        w0 = d.get("w0", 0)
        time.sleep(0.8)
        w1 = pane_w(".chat-panel")
        c.check("L8 拖拽分隔条改变 chat 宽度", w1 != w0 and w1 > 0, f"{w0}->{w1}")
        c.check("L8b 拖拽中分隔条本体不加宽（恒 1px，无重排/抖动）",
                isinstance(d.get("rw"), (int, float)) and abs(d.get("rw") - 1) < 0.6,
                f"拖拽中 handle 宽={d.get('rw')}")

        # L9a 保存布局（**free** 口径）：改 `split_body` 占比 → 落 `layout.free` → 重启回灌
        doc = JD(gui, "JSON.stringify((window.__ckLayout&&window.__ckLayout.engine)?window.__ckLayout.engine.state.config.live:null)")
        if not isinstance(doc, dict) or not doc.get("layout"):
            raise AssertionError("未取到 layout.free 文档：%r" % (doc,))
        # 把 body 水平分割的 chat 占比调小（0.30/0.70），便于区分「读到保存值」与「回落默认」
        body = doc["layout"]
        kids = body.get("children") or []
        if len(kids) == 2:
            body["sizes"] = [0.70, 0.30]
        else:
            for nd in kids:
                if isinstance(nd, dict) and nd.get("type") == "split" and nd.get("direction") == "row" and len(nd.get("children") or []) == 2:
                    nd["sizes"] = [0.70, 0.30]
                    break
        free_json = json.dumps(doc, ensure_ascii=False)
        try:
            gui.req("gui.ui.save", {"layout": {"free": free_json}})
            c.check("L9a 保存布局（gui.ui.save layout.free）", True, "ok")
        except Exception as e:
            c.check("L9a 保存布局（gui.ui.save layout.free）", False, str(e))

        # L9b 落库回读（layout.free 子键）
        pl = (gui.req("data-prj-config-list", {}) or {}).get("list") or {}
        stored = pl.get("layout.free")
        c.check("L9b 布局落库回读（layout.free 子键）",
                isinstance(stored, str) and '"sizes": [0.7, 0.3]' in stored.replace("0.70", "0.7"),
                repr((stored or "")[:200]))
    finally:
        gui.stop()

    time.sleep(1)
    # 第二实例：**只验证布局恢复**（L9，free 口径）
    g2 = GUIClient(work_dir=WORK_DIR, data_dir=DATA_DIR)
    g2.start()
    try:
        deadline = time.time() + 20
        cw2 = 0
        while time.time() < deadline:
            cw2 = J(g2, "(document.querySelector('.chat-panel')?.getBoundingClientRect().width||0)")
            # 保存占比 chat=0.30 → 期望 chat ≈ 0.30 * 内容宽（1280 上下）；默认是 0.4483
            if cw2 and cw2 <= 460:
                break
            time.sleep(0.3)
        c.check("L9 重启恢复布局（layout.free 回灌）", 200 <= cw2 <= 460, f"chatWidth={cw2}px")

        # L10a 保存屏幕外窗口状态（与 L9 解耦）
        try:
            g2.req("gui.ui.save", {"window": {"width": 1000, "height": 700, "x": 99999, "y": 99999, "maximized": False}})
            c.check("L10a 保存屏幕外窗口状态（gui.ui.save window）", True, "ok")
        except Exception as e:
            c.check("L10a 保存屏幕外窗口状态（gui.ui.save window）", False, str(e))

        pl2 = (g2.req("data-prj-config-list", {}) or {}).get("list") or {}
        c.check("L10c 窗口状态落库回读（window.width/x/y/maximized）",
                pl2.get("window.width") == "1000" and pl2.get("window.x") == "99999"
                and pl2.get("window.y") == "99999" and pl2.get("window.maximized") == "false",
                repr({k: v for k, v in pl2.items() if k.startswith("window.")}))
    finally:
        g2.stop()

    time.sleep(1)
    g3 = GUIClient(work_dir=WORK_DIR, data_dir=DATA_DIR)
    g3.start()
    try:
        L10_PROBE = ("(()=>{const S=window.screen||{};const n=v=>typeof v==='number'?v:0;"
                     "const ol=n(S.availLeft),ot=n(S.availTop),"
                     "aw=n(S.availWidth)||n(S.width),ah=n(S.availHeight)||n(S.height);"
                     "const x=n(window.screenX),y=n(window.screenY),"
                     "w=n(window.outerWidth),h=n(window.outerHeight);"
                     "const ix=Math.max(0,Math.min(x+w,ol+aw)-Math.max(x,ol));"
                     "const iy=Math.max(0,Math.min(y+h,ot+ah)-Math.max(y,ot));"
                     "return JSON.stringify({x:x,y:y,w:w,h:h,ol:ol,ot:ot,aw:aw,ah:ah,vis:ix*iy});})()")
        SAVED_OFF = 99999
        deadline = time.time() + 15
        probe = {}
        while time.time() < deadline:
            probe = JD(g3, L10_PROBE) or {}
            if isinstance(probe, dict) and probe.get("vis", 0) > 0:
                break
            time.sleep(0.3)
        if not isinstance(probe, dict):
            probe = {}
        vis = probe.get("vis", 0)
        moved = (abs(probe.get("x", SAVED_OFF) - SAVED_OFF) > 1000
                 and abs(probe.get("y", SAVED_OFF) - SAVED_OFF) > 1000)
        c.check("L10 超屏正常化：窗口被收进可见区域（与所在屏可用区真交叠）",
                vis > 0 and moved, f"probe={probe} moved={moved}")
    finally:
        g3.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

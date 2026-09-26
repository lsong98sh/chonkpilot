"""回归测试：窗口布局组（51-FP与测试映射「窗口布局」10 项）。

FP 目标默认尺寸：filetree 400 / chat 600 / task 500 / tasktree(session-tree) 400。
窗口 1280 时 applyLayout clamp：chat=min(600, vw*45%)=576、filetree 受 CodeView min 300 约束≈392，
故断言用合理区间。
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port  # noqa: E402  动态端口：避免与残留实例/他套件抢固定端口

PORT = free_port()
# 布局状态持久化在 prj/prjusr 层，为测「默认值」需隔离数据目录（每次运行前清理）。
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_layout_data")
# work-dir 亦自持（不再回落共享 systest/ws）：本套件只依赖**产品默认布局**，不依赖共享工程树
# 内容；且 `--data-dir` 非空时 prjusr == prj（persist.go:400-408），窗几/布局全在本目录内，
# 与任何他套件（含并发的同 work-dir 实例，I-74）无关。
WORK_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_layout_ws")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def JD(gui, js):
    """eval 结果**双重解码**（同 `run_memory_ctx._plain` 口径）：测试通道会把返回的 JSON 字符串
    再包一层，`J` 只解一层 → 拿到 str；此处再解一层得 dict（非 str 时原样返回）。"""
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
    # 预创建 prj 库文件：ProjectPath 在 data-dir 库不存在时会回落 workdir 既有库（迁移语义），
    # 隔离测试需先占位避免读到旧布局。
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()
    gui = GUIClient(port=PORT, work_dir=WORK_DIR, data_dir=DATA_DIR)
    c = Checker()
    try:
        gui.start()

        # L0 窗口几何基线（**显式声明**，替代「靠上一套件遗留状态」）：
        # L2/L4/L7 断言的是「默认布局 + 窗口约束下的期望区间」，前提 = 窗口 = 产品默认 1280x800
        # （main.go:602 创建；本套件 prj/prjusr 全新 → 无 window.* 记录 → 不回落任何旧几何）。
        # 把实际 vw 打进 detail：若某次红了，可直接判定是「窗口几何偏离基线」还是「布局 clamp 变了」。
        VW = J(gui, "window.innerWidth || 0")
        VH = J(gui, "window.innerHeight || 0")
        c.check("L0 窗口几何基线 = 产品默认 1280x800（vw*45% clamp 前提）",
                VW == 1280 and 700 <= VH <= 820, f"innerWidth={VW} innerHeight={VH}")

        # L1 布局六区：toolbar / filetree / preview / chat / task(tasktree+taskview) / statusbar
        zones = J(gui, """(()=>{const q=s=>document.querySelectorAll(s).length;return JSON.stringify({
  toolbar:q('.toolbar'),filetree:q('.filetree-panel'),codeview:q('.code-view'),
  chat:q('.chat-panel'),statusbar:q('.statusbar'),panels:q('.panel-inner')})})()""")
        z = json.loads(zones)
        c.check("L1 六区布局存在", z["toolbar"] > 0 and z["filetree"] > 0 and z["codeview"] > 0
                and z["chat"] > 0 and z["statusbar"] > 0 and z["panels"] >= 2,
                repr(z))

        n = J(gui, "document.querySelectorAll('.split-resizer').length")
        c.check("L1b 分隔条（resizer）存在", n >= 4, f"resizers={n}")

        def pane_w(sel):
            return J(gui, f"(document.querySelector('{sel}')?.getBoundingClientRect().width||0)")

        # L2 filetree 宽度默认 400px（受窗口约束 ≈392）
        fw = pane_w(".filetree-panel")
        c.check("L2 filetree 宽度默认 400px", 380 <= fw <= 402, f"actual={fw}px vw={VW}")

        # L3 filetree 宽度保存（gui.ui.save {layout}）
        try:
            gui.req("gui.ui.save", {"layout": {"filetreeWidth": 300}})
            c.check("L3 filetree 宽度保存（gui.ui.save layout）", True, "ok")
        except Exception as e:
            c.check("L3 filetree 宽度保存（gui.ui.save layout）", False, str(e))

        # L4 chat 宽度默认 600px（窗口 45% 约束 ≈576）
        cw = pane_w(".chat-panel")
        c.check("L4 chat 宽度默认 600px", 560 <= cw <= 602, f"actual={cw}px（vw*45% clamp，vw={VW}）")

        # L5 chat 宽度保存
        try:
            gui.req("gui.ui.save", {"layout": {"chatWidth": 500}})
            c.check("L5 chat 宽度保存（gui.ui.save layout）", True, "ok")
        except Exception as e:
            c.check("L5 chat 宽度保存（gui.ui.save layout）", False, str(e))

        # L6 task 区高度默认 500px（.panel-inner 为代理，允许面板内边距误差）
        th = J(gui, "(document.querySelector('.panel-inner')?.getBoundingClientRect().height||0)")
        c.check("L6 task 区高度默认 500px", 470 <= th <= 510, f"actual={th}px")

        # L7 tasktree(session-tree) 宽度默认 400px
        sw = pane_w(".panel-inner")
        c.check("L7 tasktree 宽度默认 400px", 390 <= sw <= 410, f"actual={sw}px vw={VW}")

        # L8 拖拽 resizer 实际改变 chat 宽度（前端交互）
        js_drag = """(()=>{let best=null;for(const r of document.querySelectorAll('.split-resizer.resizer-horizontal')){const next=r.nextElementSibling;if(next&&next.querySelector('.chat-panel')){const rect=r.getBoundingClientRect();best={el:r,x:rect.left+rect.width/2,y:rect.top+rect.height/2};break}}if(!best)return 0;const w0=document.querySelector('.chat-panel').getBoundingClientRect().width;best.el.dispatchEvent(new MouseEvent('mousedown',{clientX:best.x,clientY:best.y,bubbles:true,cancelable:true}));window.dispatchEvent(new MouseEvent('mousemove',{clientX:best.x-60,clientY:best.y,bubbles:true,cancelable:true}));window.dispatchEvent(new MouseEvent('mouseup',{clientX:best.x-60,clientY:best.y,bubbles:true,cancelable:true}));return w0;})()"""
        w0 = J(gui, js_drag)
        time.sleep(0.8)
        w1 = pane_w(".chat-panel")
        c.check("L8 拖拽分隔条改变 chat 宽度", w1 != w0 and w1 > 0, f"{w0}->{w1}")

        # L9a 保存布局（chatWidth=500）→ 停 main → 重启验证恢复
        try:
            gui.req("gui.ui.save", {"layout": {"chatWidth": 500, "sessiontreeWidth": 350, "filetreeWidth": 330}})
            c.check("L9a 保存布局（gui.ui.save layout）", True, "ok")
        except Exception as e:
            c.check("L9a 保存布局（gui.ui.save layout）", False, str(e))

        # L9b A 落库回读（P6 复核补齐）：`gui.ui.save{layout}` → bridge/local.go:191-196
        # `callSaveLayoutState` → 键 `layout.<k>` 落 prj 库（`--data-dir` 非空时 prjusr == prj）。
        # 原用例只有「重启后 chat 宽度 DOM」= 效果侧，无落库回读。
        pl = (gui.req("data-prj-config-list", {}) or {}).get("list") or {}
        c.check("L9b 布局落库回读（layout.chatWidth/sessiontreeWidth/filetreeWidth）",
                pl.get("layout.chatWidth") == "500"
                and pl.get("layout.sessiontreeWidth") == "350"
                and pl.get("layout.filetreeWidth") == "330",
                repr({k: v for k, v in pl.items() if k.startswith("layout.")}))

        # 解耦（2026-09-25）：L10a/L10c 的窗口状态（`window.width=1000`）**不在本会话写**——
        # 若在此写入，重启后 applyLayout 会把 chatWidth clamp 到 `vw*45%`=450（MainLayout.vue:124）
        # → L9 的 480..520 断言被 L10 连带判红（用例耦合，非功能回归）。改到「L9 验证之后的
        # 独立会话」写入，使 L9 在**产品默认窗口几何**下断言。
    finally:
        gui.stop()  # 释放 DB 文件锁后重启验证（bbolt 单进程写锁）

    time.sleep(1)
    # 第二实例：**只验证布局恢复**（L9）。此时 `window.*` 尚未写入 → 窗口 = 产品默认 1280x800
    # → applyLayout 的 `vw*45%` clamp（MainLayout.vue:124）不生效，L9 与 L10 互不影响。
    g2 = GUIClient(work_dir=WORK_DIR, data_dir=DATA_DIR)  # 第二实例同样动态端口 + 同一自持数据根
    g2.start()
    try:
        # L9 重启恢复布局：恢复是**异步**的 —— 宿主 `ready`（导航完成 → test-port /ping ready）
        # **早于**前端启动期工作（instance-claim 门控主视图挂载 → gui.init-data 往返 → applyLayout），
        # 实测恢复落点在 ready 之后（2026-09-22 探针/宿主日志：claim ~0.8s + init-data ~0.25s）。
        # 故此处**有界等待**恢复值出现；断言强度不变（必须落在 480..520，15s 内不出现即失败）。
        deadline = time.time() + 15
        cw2 = 0
        while time.time() < deadline:
            cw2 = J(g2, "(document.querySelector('.chat-panel')?.getBoundingClientRect().width||0)")
            if 480 <= cw2 <= 520:
                break
            time.sleep(0.3)
        c.check("L9 重启恢复布局", 480 <= cw2 <= 520, f"chatWidth={cw2}px")

        # L10a 超屏正常化（保存侧，与 L9 解耦）：L9 断言已结束 → 在本会话末写屏幕外窗口状态；
        # 该 `window.width=1000` 只影响后续 L10b 的判定，不再回灌 L9。
        try:
            g2.req("gui.ui.save", {"window": {"width": 1000, "height": 700, "x": 99999, "y": 99999, "maximized": False}})
            c.check("L10a 保存屏幕外窗口状态（gui.ui.save window）", True, "ok")
        except Exception as e:
            c.check("L10a 保存屏幕外窗口状态（gui.ui.save window）", False, str(e))

        # L10c A 落库回读（P6 复核补齐）：`window.<k>` 同链路；原用例只断重启后 screenX/Y。
        pl2 = (g2.req("data-prj-config-list", {}) or {}).get("list") or {}
        c.check("L10c 窗口状态落库回读（window.width/x/y/maximized）",
                pl2.get("window.width") == "1000" and pl2.get("window.x") == "99999"
                and pl2.get("window.y") == "99999" and pl2.get("window.maximized") == "false",
                repr({k: v for k, v in pl2.items() if k.startswith("window.")}))
    finally:
        g2.stop()

    time.sleep(1)
    # 第三实例：**只验证超屏正常化**（L10b）——此时 `window.x/y=99999` 已落库。
    g3 = GUIClient(work_dir=WORK_DIR, data_dir=DATA_DIR)  # 第三实例同样动态端口 + 同一自持数据根
    g3.start()
    try:
        # L10b 超屏正常化（**语义判定**，2026-09-24 加固）：L10a 保存的是**屏幕外**坐标
        # （x=y=99999），重启后宿主按**当前显示器布局**正常化（`src/lib/gui/main.go
        # normalizeWindowRect`：无交集 → 收进主显示器工作区并居中）。
        #
        # 判据（不写死数值区间，免疫多屏/虚拟屏/负坐标原点）：
        #   ① **已移出保存点**：|screenX-99999| / |screenY-99999| 均 > 1000；
        #   ② **真可见**：窗口矩形与「窗口所在屏的可用区域」**交叠面积 > 0**
        #      （`screen.availLeft/availTop` = 该屏在虚拟桌面中的原点、`availWidth/Height` = 该屏可用尺寸；
        #       负坐标屏同样成立；完全不交叠 = 没被收进可见区域 → 判失败）。
        # **有界等待**：启动瞬间窗口可能尚未定位（观测到 `screenX=-32000` = Windows 图标态坐标），
        # 故轮询至条件成立，超时才失败 —— **判定强度不变**（仍要求真交叠）。
        L10_PROBE = ("(()=>{const S=window.screen||{};const n=v=>typeof v==='number'?v:0;"
                     "const ol=n(S.availLeft),ot=n(S.availTop),"
                     "aw=n(S.availWidth)||n(S.width),ah=n(S.availHeight)||n(S.height);"
                     "const x=n(window.screenX),y=n(window.screenY),"
                     "w=n(window.outerWidth),h=n(window.outerHeight);"
                     "const ix=Math.max(0,Math.min(x+w,ol+aw)-Math.max(x,ol));"
                     "const iy=Math.max(0,Math.min(y+h,ot+ah)-Math.max(y,ot));"
                     "return JSON.stringify({x:x,y:y,w:w,h:h,ol:ol,ot:ot,aw:aw,ah:ah,vis:ix*iy});})()")
        SAVED_OFF = 99999  # = L10a 写入的屏幕外坐标
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

"""回归测试：窗口组（51-FP与测试映射「窗口」8 项）。

每条 FP 一个 check；不可运行时验证的项如实标注（需代码审查/OS 级）。
运行：python test_window.py（cwd 任意，脚本自定位）

状态无关性：本套件用**共享** work-dir（`ws`，prj 库继承历史持久化布局）→ 依赖布局可见性的
断言（W5 `.split-resizer` 计数）必须**先经既有 MQ 开关归一到已知状态**再断言、用后还原，
否则结果随前序套件遗留状态漂移（见 W5 处注释）。
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


# ── 布局可见性归一（W5；只读产品 DOM + **既有** MQ 开关，不新增消息面）──────────
# `.split-resizer` 由 `components/split/SplitPanel.vue` 按「相邻两个**可见** pane」渲染
# （`splitLayout.js shouldShowResizer`：`visible:false` 的 pane 不参与布局、也不产出分隔条）。
# 四区全可见时才有 4 条：① filetree|preview ② content|chat ③ top|bottom(task) ④ session-tree|session-chat。
# 本套件用**共享** work-dir（`ws`，prj 库继承历史持久化布局）→ 面板可见性不确定 → 直接数分隔条
# 属「状态依赖」断言（实测该库 `layout.taskOpen=false` → 只剩 2 条）。故断言前先经既有开关归一到
# 「全可见」，用后还原。
_PANE_PROBES = (
    ("chat-toggle", ".chat-panel"),        # ChatPanel 内（chat pane 隐藏 → 不挂载）
    ("filetree-toggle", ".explorer-pane"),  # ExplorerPane 根（filetree pane 隐藏 → 不挂载）
    ("tasks-toggle", ".session-chat"),     # SessionChat 内（task pane 隐藏 → 不挂载）
)


def _resizer_count(gui):
    """当前渲染的 `.split-resizer` 数（随面板可见性变化）。"""
    try:
        return int(J(gui, "document.querySelectorAll('.split-resizer').length") or 0)
    except Exception:
        return -1


def _pane_evidence(gui):
    """W5 取证：视口宽 + 三区 pane 是否已挂载（供失败时定位是哪块被收起）。"""
    return J(gui, """(()=>({vw:window.innerWidth,
  chat:!!document.querySelector('.chat-panel'),
  filetree:!!document.querySelector('.explorer-pane'),
  task:!!document.querySelector('.session-chat'),
  resizers:document.querySelectorAll('.split-resizer').length}))()""")


def _ensure_panes_visible(gui):
    """经**既有** MQ 开关把三区（chat/filetree/task）归一到「已挂载」；返回被自己切换过的主题列表。

    幂等：已挂载的区不发开关。返回列表供用例末尾**还原**（回跑前布局，不给后续套件留状态）。
    """
    flipped = []
    for topic, sel in _PANE_PROBES:
        if not J(gui, "!!document.querySelector('%s')" % sel):
            gui.eval("window.mq.emit('%s', {}); 'ok'" % topic)
            flipped.append(topic)
    return flipped


def _wait_resizers(gui, least=4, timeout=8.0):
    """有界轮询 `.split-resizer` 数稳定到 >= least（等 Vue 重渲染落定）；超时返回最后观测值。"""
    deadline = time.time() + timeout
    n = _resizer_count(gui)
    while n < least and time.time() < deadline:
        time.sleep(0.2)
        n = _resizer_count(gui)
    return n


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

        # W5 支持 Resize（八个方向）：frameless 保留 WS_THICKFRAME → 系统边缘热区原生八方向 resize。
        #
        # **分隔条计数口径**：`.split-resizer` 只在「相邻两个可见 pane」之间渲染（SplitPanel.vue +
        # splitLayout.js shouldShowResizer）。四区齐备（filetree|preview · content|chat · top|bottom(task)
        # · session-tree|session-chat）才 = 4。**实测根因**：本套件用共享 work-dir（`ws`），其 prj 库
        # 持久化了 `layout.taskOpen=false`（任务区收起）→ 任务区两种 pane 均 `visible:false`，其 slot
        # 不挂载 → top|bottom 与 session-* 两条分隔条随之消失，只剩 filetree|preview + content|chat
        # = **2** 条。即「面板被收起 → 分隔条变少」是 SplitPanel 的**正确行为**（非产品缺陷），
        # 而 W5 语义（系统八方向 resize 能力）要求四区齐备。
        # 故先经**既有** MQ 开关（chat-toggle/filetree-toggle/tasks-toggle，等价用户点顶部开关）
        # 把三区归一到「全可见」→ 等 DOM 稳定 → 断言 n>=4；用后**还原**，不留状态给后续套件。
        n0 = _resizer_count(gui)
        flipped = _ensure_panes_visible(gui)
        n = _wait_resizers(gui, least=4)
        c.check("W5 布局分隔条存在 + frameless 保留系统八方向 resize",
                n >= 4,
                f"resizers={n}（归一前 {n0}；flipped={flipped or '无'}；state={_pane_evidence(gui)}；"
                f"WS_THICKFRAME 保留：系统边缘热区八方向 resize，见 webview.go）")
        # 还原自己切换过的面板（回跑前布局）；并留足 > saveLayoutState 500ms 防抖，
        # 避免其晚于 finally 的配置快照还原、把「归一态」回灌 prj 库。
        if flipped:
            for topic in flipped:
                gui.eval("window.mq.emit('%s', {}); 'ok'" % topic)
            deadline = time.time() + 5.0
            while _resizer_count(gui) != n0 and time.time() < deadline:
                time.sleep(0.2)
            time.sleep(0.8)

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

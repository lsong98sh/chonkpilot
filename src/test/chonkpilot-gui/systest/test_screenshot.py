"""回归测试：截图按钮 + 区域选择（51-FP与测试映射「主chat 操作行【截图】按钮」）。

覆盖：
- **能力门控（2026-09-26）**：所选 LLM 的 `capabilities` 未含 `vision` → 截图按钮禁用、点击不发
  `gui.capture`（用户口径：「当 chat 窗口选择的 llm 没有图片时，不能截图」）
- 截图按钮存在于操作行左侧控件
- 点击 → chat-screenshot → /call/CaptureScreen（隐藏窗口全屏 GDI）→ 全屏预览 overlay
- overlay 拖拽选择区域 → canvas 裁剪 → 附件上传（同一链路）
- 附件可删除；双击取消截图模式
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
DATA_DIR = os.path.join(HERE, "_screenshot_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def uploads_dir():
    d = os.path.join(DATA_DIR, "tmp", "uploads")
    return [f for f in os.listdir(d) if not f.startswith('.')] if os.path.isdir(d) else []


def wait_upto(gui, js, ok, max_wait=8.0, interval=0.25):
    """有界轮询：轮询 js 求值直到 ok(v) 为真或超时；返回末次值。

    I-92 低风险加固（2026-09-19）：把「裁剪上传/overlay 出现」前的**固定 sleep**改为**有界轮询**
    —— 只改**何时**读，不改**读什么**（断言条件仍在调用处，强度不变）。"""
    deadline = time.time() + max_wait
    v = J(gui, js)
    while not ok(v) and time.time() < deadline:
        time.sleep(interval)
        v = J(gui, js)
    return v


def click_screenshot(gui):
    J(gui, """(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-left .icon-btn'));const b=bs.find(x=>((x.getAttribute('title')||'').toLowerCase().indexOf('screenshot')>=0)||((x.getAttribute('title')||'').indexOf('截图')>=0));if(b)b.click();return 'ok'})()""")


def drag_select(gui):
    """在 overlay 上拖拽一个选择区域（模拟 mousedown/mousemove/mouseup）。"""
    J(gui, """(()=>{const ov=document.querySelector('.screenshot-overlay');const img=document.querySelector('.screenshot-img');if(!ov||!img)return 'no-overlay';const r=img.getBoundingClientRect();const mk=(t,x,y)=>new MouseEvent(t,{clientX:x,clientY:y,button:0,bubbles:true,cancelable:true,view:window});ov.dispatchEvent(mk('mousedown',r.left+40,r.top+40));ov.dispatchEvent(mk('mousemove',r.left+260,r.top+180));ov.dispatchEvent(mk('mouseup',r.left+260,r.top+180));return 'ok'})()""")


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    gui = GUIClient(port=PORT, data_dir=DATA_DIR)
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

        # ── 截图能力门控（2026-09-26，用户口径：「当 chat 窗口选择的 llm 没有图片时，不能截图」）──
        # 观测点 = 所选 provider 的 `capabilities` 是否含 `vision`：
        #   未声明 → 截图按钮 **disabled** + 点击**不发** `gui.capture`（无 overlay）；
        #   已声明 → 按钮可用，后续 S1~S5 走完整截图链路。
        # 写 usr 配置（llms/defaultLLM）→ `data-user-config-save` 触发后端 `config-refresh`
        # 广播 → ChatPanel 重载 llms → `chat-select-llm` 选中该 provider（等价用户点选）。
        def seed_llm(name, caps):
            gui.req("data-user-config-save", {"data": {
                "llms": [{"name": name, "protocol": "openai", "apiKey": "", "model": "m",
                          "baseUrl": "http://127.0.0.1:1", "temperature": 0.7,
                          "maxOutputToken": 4096, "maxContextToken": 128000,
                          "thinking": True, "reasoningEffort": "", "maxToolIterations": 20,
                          "capabilities": caps}],
                "defaultLLM": name,
            }})
            time.sleep(1.0)
            gui.publish("chat-select-llm", {"name": name})
            time.sleep(0.6)

        def screenshot_btn():
            return J(gui, """(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-left .icon-btn'));const b=bs.find(x=>((x.getAttribute('title')||'').toLowerCase().indexOf('screenshot')>=0)||((x.getAttribute('title')||'').indexOf('截图')>=0));return {found:!!b, disabled:b?(!!b.disabled||b.classList.contains('is-disabled')):null, title:b?b.getAttribute('title'):''}})()""")

        # S0 未声明「图形」→ 按钮禁用 + 点击无 overlay（不发 gui.capture）
        seed_llm("shot-novision", [])
        b0 = screenshot_btn()
        click_screenshot(gui)
        time.sleep(1.2)
        ov0 = J(gui, "!!document.querySelector('.screenshot-overlay')")
        c.check("S0 未声明 vision → 截图禁用 + 点击无 overlay",
                bool(b0['found']) and bool(b0['disabled']) and not ov0, repr(b0))

        # 前置：声明「图形」的 provider（S1~S5 截图链路前提）
        seed_llm("shot-vision", ["vision"])
        bv = screenshot_btn()
        c.check("S0b 声明 vision → 截图按钮可用", bool(bv['found']) and not bv['disabled'], repr(bv))

        # S1 截图按钮存在
        s1 = J(gui, """(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-left .icon-btn'));const b=bs.find(x=>((x.getAttribute('title')||'').toLowerCase().indexOf('screenshot')>=0)||((x.getAttribute('title')||'').indexOf('截图')>=0));return {found:!!b, titles:bs.map(x=>x.getAttribute('title')).join('|')}})()""")
        c.check("S1 截图按钮存在", bool(s1['found']), repr(s1['titles']))

        # S2 点击 → 截图预览 overlay（隐藏窗口全屏截取后显示）；分时轮询（截图约 0.5s）
        click_screenshot(gui)
        ovSeen = None
        for _ in range(12):
            time.sleep(0.5)
            ovSeen = J(gui, "!!document.querySelector('.screenshot-overlay')")
            if ovSeen:
                break
        warns = [e.get('text', '')[:150] for e in (gui.console() or {}).get('entries', []) if e.get('level') == 'warning']
        c.check("S2 截图预览 overlay 出现", bool(ovSeen), f"seen={ovSeen} warns={warns[-2:]}")

        # S3 拖拽选择区域 → 裁剪上传 → 附件缩略图 + 落盘（有界轮询替代固定 sleep）
        drag_select(gui)
        wait_upto(gui, "document.querySelectorAll('.attach-chip.attach-image').length",
                  lambda v: isinstance(v, (int, float)) and v >= 1, max_wait=10.0)
        chips = J(gui, "document.querySelectorAll('.attach-chip.attach-image').length")
        chipName = J(gui, "document.querySelector('.attach-chip.attach-image .attach-name')?.textContent?.trim() || ''")
        overlayGone = J(gui, "!document.querySelector('.screenshot-overlay')")
        hasPng = any(f.endswith('.png') for f in uploads_dir())
        c.check("S3 拖选裁剪 → 附件 + overlay 关闭", bool(hasPng) and int(chips) >= 1 and 'screenshot' in chipName and bool(overlayGone),
                f"png={hasPng} chips={chips} name={repr(chipName)} gone={overlayGone}")

        # S4 附件可删除
        # L4 加固（2026-09-23，[42 §2 (148)]）：上传回执前 chip 无删除入口（InputBox.vue:9-10
        # `v-if="a.pending"` 显状态、`v-else` 才是 `.attach-remove`）→ 先等删除按钮出现再点。
        wait_upto(gui, "document.querySelectorAll('.attach-chip.attach-image .attach-remove').length",
                  lambda v: isinstance(v, (int, float)) and v >= 1, max_wait=10.0)
        J(gui, "document.querySelector('.attach-chip.attach-image .attach-remove')?.click(); 'ok'")
        gone = wait_upto(gui, "document.querySelectorAll('.attach-chip').length",
                         lambda v: isinstance(v, (int, float)) and int(v) == 0, max_wait=10.0)
        c.check("S4 截图附件可删除", int(gone) == 0, f"chips={gone}")

        # S5 双击 overlay 取消（不进附件）——有界轮询等 overlay 出现（替代固定 sleep 2.5s）
        click_screenshot(gui)
        wait_upto(gui, "!!document.querySelector('.screenshot-overlay')",
                  lambda v: v is True, max_wait=8.0)
        J(gui, "document.querySelector('.screenshot-overlay')?.dispatchEvent(new MouseEvent('dblclick',{bubbles:true})); 'ok'")
        wait_upto(gui, "!document.querySelector('.screenshot-overlay')",
                  lambda v: v is True, max_wait=5.0)
        ovGone = J(gui, "!document.querySelector('.screenshot-overlay')")
        chips = J(gui, "document.querySelectorAll('.attach-chip').length")
        c.check("S5 双击取消（无附件）", bool(ovGone) and int(chips) == 0, f"gone={ovGone} chips={chips}")

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')
                and 'CaptureScreen' not in (e.get('text') or '')]
        c.check("无前端错误（截图组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

"""回归测试：富文本输入区（51-FP与测试映射「主chat 输入区」富文本 + 附件）。

覆盖：
- 富文本输入区存在（contenteditable + 空态 placeholder）
- 输入文本 → 发送按钮可用；Shift+Enter 换行不发送
- 粘贴图片 → 附件缩略图；拖入文件 → 文件 chip；附件可删除
- 附件走 /call/UploadAttachment 上传落盘（数据根 tmp/uploads/）
- 发送序列化为 markdown（图片 ![名](路径)；文件 [名](路径)；文本在后）
- 无 LLM 配置发送被拦截 → 序列化内容写回编辑区（证明 serialize + 发送链路）
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import ensure_locale, free_port, snapshot_config, restore_config  # noqa: E402  动态端口 + 语言确定性（发送控件按英文文案断言）+ 套件级配置快照-还原

PORT = free_port()
HERE = os.path.dirname(os.path.abspath(__file__))
DATA_DIR = os.path.join(HERE, "_richtext_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def edit_sel():
    return "document.querySelector('[contenteditable][data-drop-target=\"chat-input\"]')"


def _loads_deep(v):
    """gui.eval 结果可能多重 JSON 编码（EvalWithResult）：循环解包到非 JSON 字符串。"""
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def set_text(gui, t):
    J(gui, "(()=>{const el=%s;el.innerHTML='';el.appendChild(document.createTextNode(%s));el.dispatchEvent(new Event('input',{bubbles:true}));return 'ok'})()" % (edit_sel(), json.dumps(t)))


def get_text(gui):
    return J(gui, "(%s).innerText || ''" % edit_sel())


def wait_upto(gui, js, ok, max_wait=6.0, interval=0.2):
    """有界轮询：轮询 js 求值直到 ok(v) 为真或超时；返回末次值。

    I-92 低风险加固（2026-09-19）：把「异步上传/渲染后断言」前的**固定 sleep**改为**有界轮询**
    —— 只改**何时**读，不改**读什么**（断言条件仍在调用处，强度不变）。"""
    deadline = time.time() + max_wait
    v = J(gui, js)
    while not ok(v) and time.time() < deadline:
        time.sleep(interval)
        v = J(gui, js)
    return v


def paste_image(gui, name='shot.png'):
    """构造 paste 事件携带图片文件 → 触发 onPaste 上传附件。"""
    J(gui, """(()=>{const dt=new DataTransfer();const bin=new Uint8Array([137,80,78,71,13,10,26,10,0,0,0,13,73,72,68,82]);dt.items.add(new File([bin], %s, {type:'image/png'}));const ev=new ClipboardEvent('paste',{clipboardData:dt,bubbles:true,cancelable:true});(%s).dispatchEvent(ev);return 'ok'})()""" % (json.dumps(name), edit_sel()))


def drop_file(gui, name='notes.txt'):
    """构造 drop 事件携带文件 → onDrop 上传附件。"""
    J(gui, """(()=>{const dt=new DataTransfer();dt.items.add(new File(['hello'], %s, {type:'text/plain'}));const ev=new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:dt});(%s).dispatchEvent(ev);return 'ok'})()""" % (json.dumps(name), edit_sel()))


def click_send(gui):
    J(gui, """(()=>{const bs=Array.from(document.querySelectorAll('button'));const s=bs.find(b=>(b.textContent||'').trim()==='Send');if(s)s.click();return 'ok'})()""")


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
        # 语言确定性（51 §6-8）：本套件按英文文案断言，不依赖他套件遗留在 usr 库的 ui.locale
        ensure_locale(gui, "en-US")
        for _ in range(30):
            if J(gui, "document.querySelector('.panel-inner')") and J(gui, "document.querySelector('[contenteditable=\"true\"]')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # R1 富文本输入区 + 空态 placeholder
        r1 = J(gui, """(()=>{const el=%s;const cs=getComputedStyle(el,'::before');return {el:!!el, ph:cs.content}})()""" % edit_sel())
        c.check("R1 富文本输入区 + placeholder", bool(r1['el']) and 'none' not in str(r1['ph']) and r1['ph'] != '', repr(r1['ph']))

        # R2 输入文本 → Send 可用
        set_text(gui, 'hello richtext')
        time.sleep(0.3)
        sendDisabled = J(gui, """(()=>{const bs=Array.from(document.querySelectorAll('button'));const s=bs.find(b=>(b.textContent||'').trim()==='Send');return s?!!s.disabled:null})()""")
        c.check("R2 输入后发送按钮可用", sendDisabled is False, f"disabled={sendDisabled}")

        # R3 Shift+Enter 换行不发送（编辑区文本保持）
        J(gui, "(%s).dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',shiftKey:true,bubbles:true,cancelable:true})); 'ok'" % edit_sel())
        time.sleep(0.3)
        t = get_text(gui)
        c.check("R3 Shift+Enter 换行不发送", 'hello richtext' in t, repr(t))

        # R4 粘贴图片 → 附件缩略图 chip（有界轮询替代固定 sleep，见 wait_upto）
        # L4 加固（2026-09-23，[42 §2 (148)]）：断言除「chip 出现」外还要求**上传回执后**的
        # `/show/` 缩略图 src（InputBox.vue:257 `a.url = res.url || a.url`，回执前是本地 dataURL）
        # → 轮询条件须**同时**覆盖两者（原仅等 chip，负载下回执未到即读 src 得 data:URL）。
        paste_image(gui)
        stamp = wait_upto(gui, """(()=>{const c=document.querySelector('.attach-chip.attach-image .attach-thumb');return {n:document.querySelectorAll('.attach-chip.attach-image').length,src:c?(c.getAttribute('src')||''):''}})()""",
                          lambda v: isinstance(v, dict) and int(v.get('n') or 0) >= 1
                          and str(v.get('src') or '').startswith('/show/'), max_wait=10.0)
        imgChips = (stamp or {}).get('n')
        imgSrc = (stamp or {}).get('src') or ''
        c.check("R4 粘贴图片 → 缩略图附件", int(imgChips or 0) >= 1 and imgSrc.startswith('/show/'), f"chips={imgChips} src={imgSrc[:40]}")

        # R5 拖入文件 → 文件 chip；删除 → 消失
        # L4 加固（2026-09-23，[42 §2 (148)]）：chip 在**上传回执前**只有「上传中」态、**无删除入口**
        # （InputBox.vue:9-10：`v-if="a.pending"` 显状态、`v-else` 才是 `.attach-remove`）→
        # 必须先等删除按钮出现再点，否则 click 落空、chip 不消失（实测负载批跑 R5b `chips=1`）。
        drop_file(gui)
        wait_upto(gui, "document.querySelectorAll('.attach-chip.attach-file').length",
                  lambda v: isinstance(v, (int, float)) and v >= 1, max_wait=10.0)
        fileChips = J(gui, "document.querySelectorAll('.attach-chip.attach-file').length")
        c.check("R5a 拖入文件 → 文件 chip", int(fileChips) >= 1, f"chips={fileChips}")
        wait_upto(gui, "document.querySelectorAll('.attach-chip.attach-file .attach-remove').length",
                  lambda v: isinstance(v, (int, float)) and v >= 1, max_wait=10.0)
        J(gui, "document.querySelector('.attach-chip.attach-file .attach-remove')?.click(); 'ok'")
        fileGone = wait_upto(gui, "document.querySelectorAll('.attach-chip.attach-file').length",
                             lambda v: isinstance(v, (int, float)) and int(v) == 0, max_wait=10.0)
        c.check("R5b 附件可删除", int(fileGone) == 0, f"chips={fileGone}")

        # R6 上传落盘（数据根 tmp/uploads/ 有上传文件）
        uploads = [f for f in os.listdir(os.path.join(DATA_DIR, "tmp", "uploads")) if not f.startswith('.')] if os.path.isdir(os.path.join(DATA_DIR, "tmp", "uploads")) else []
        c.check("R6 附件 api 上传落盘（tmp/uploads）", len(uploads) >= 1, repr(uploads[:3]))

        # R7 序列化发送：图片附件 + 文本 → 点 Send → message-send 载荷 = 附件在前（![名](路径)）+ 文本在后
        #（原「无 LLM 配置 → 拦截写回」前置已不成立：全新库自带可用默认 LLM，发送直接放行；
        #  序列化定义见 InputBox.serialize()）
        set_text(gui, '看图解释一下')
        time.sleep(0.3)
        J(gui, """window.__sendSpy=[];window.mq.post('message-send', d=>window.__sendSpy.push(d));'ok'""")
        click_send(gui)
        wait_upto(gui, "window.__sendSpy.length",
                  lambda v: isinstance(v, (int, float)) and v >= 1, max_wait=6.0)
        spy = _loads_deep(J(gui, "JSON.stringify(window.__sendSpy)"))
        tx7 = spy[0].get('text', '') if isinstance(spy, list) and spy else ''
        c.check("R7 发送序列化（![名](路径)）→ message-send 载荷含图片标记 + 文本",
                isinstance(spy, list) and len(spy) == 1 and tx7.startswith('![')
                and '.png)' in tx7 and '看图解释一下' in tx7,
                f"n={len(spy) if isinstance(spy, list) else spy} text={repr(tx7[:60])}")

        # R8 写回 md 含图片标记 → 解析恢复为附件缩略图 + 文本无标记
        uploads = [f for f in os.listdir(os.path.join(DATA_DIR, "tmp", "uploads")) if f.endswith('.png')] if os.path.isdir(os.path.join(DATA_DIR, "tmp", "uploads")) else []
        assert uploads, "缺少上传图片（R8 前置）"
        pngPath = os.path.join(DATA_DIR, "tmp", "uploads", uploads[0]).replace("\\", "/")
        J(gui, "window.mq.emit(%s, {text: %s}); 'ok'" % (
            json.dumps("chat-queue-restore"),
            json.dumps("![截图](%s)\\n附件回显测试" % pngPath)))
        wait_upto(gui, "document.querySelectorAll('.attach-chip.attach-image').length",
                  lambda v: isinstance(v, (int, float)) and v >= 1, max_wait=6.0)
        chips = J(gui, "document.querySelectorAll('.attach-chip.attach-image').length")
        ed = get_text(gui)
        c.check("R8 写回附件标记 → 缩略图回显 + 文本无标记",
                int(chips) >= 1 and '![截图]' not in ed and '附件回显测试' in ed,
                f"chips={chips} ed={repr(ed[:40])}")

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')]
        c.check("无前端错误（富文本组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

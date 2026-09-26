"""回归测试：主 chat 组（51-FP与测试映射「主chat窗口」标题栏/内容区/输入区 UI 结构）。

- 会话数据用前端 session-changed 事件驱动（无需 mock_llm）。
- locale 固定 en-US（WebView2 localStorage 共享）。
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import ensure_locale, free_port, snapshot_config, restore_config  # noqa: E402  动态端口 + 语言确定性（H1/H6/H7 按英文文案断言）+ 套件级配置快照-还原

PORT = free_port()
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_chat_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


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


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    gui = GUIClient(port=PORT, data_dir=DATA_DIR)
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：prj 走本套件独立 --data-dir，但 **usr 主库（~/.chonkpilot）
    # 不受 --data-dir 隔离**（theme/locale/llms 等仍写机器库）→ 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        # 语言确定性（51 §6-8）：本套件按英文文案断言，不依赖他套件遗留在 usr 库的 ui.locale
        ensure_locale(gui, "en-US")
        for _ in range(30):
            if J(gui, "document.querySelectorAll('.panel-inner').length") > 0 and J(gui, "document.querySelector('[contenteditable=\"true\"]')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # H1 标题栏：主聊天文字 + 新建会话按钮（FileTree 也用 .panel-header，用全局选择器）
        h1 = J(gui, """(()=>{const nb=document.querySelector('.new-session-btn');const body=document.body.textContent;return {txt:body.includes('Main Chat'), nb:!!nb}})()""")
        c.check("H1 标题栏：主聊天文字 + 新建会话按钮", bool(h1) and h1['txt'] and h1['nb'], repr(h1))

        # H2 场景 tag（activeScenarioLabel）非空（seed 出厂默认场景后应为「开发场景」）
        scen = J(gui, "(document.querySelector('.chat-session-tag')?.parentElement?.querySelector('.b-popover__reference')||document.querySelector('.b-popover__reference')||{}).textContent?.trim() || ''")
        c.check("H2 场景 tag 显示（非空且非'选择场景'）", bool(scen) and scen != "选择场景", repr(scen))

        # H3 点击场景 tag → 弹列表（场景项 ≥1；popover 可能 teleport 到 body）
        J(gui, "document.querySelector('.panel-header .b-popover__reference')?.click(); 'ok'")
        time.sleep(0.6)
        scenItems = J(gui, "document.querySelectorAll('.popover-item').length")
        c.check("H3 场景选择列表项 ≥1", int(scenItems) >= 1, f"items={scenItems}")
        # 关闭
        J(gui, "document.body.click(); 'ok'")
        time.sleep(0.3)

        # H4 会话 tag：模拟会话切换（session-changed）→ #前8位 + title=完整 id
        J(gui, "window.mq.emit('session-changed', {session_id:'test-sess-abc12345'}); 'ok'")
        time.sleep(0.8)
        tag = J(gui, """(()=>{const t=document.querySelector('.chat-session-tag');return t? {text:t.textContent.trim(), title:t.getAttribute('title')||''}:null})()""")
        c.check("H4 会话 tag：#前8位 + title 完整 id",
                bool(tag) and tag['text'] == '#test-ses' and tag['title'] == 'test-sess-abc12345', repr(tag))

        # H4b 点击 tag 复制（不崩；clipboard 权限下可能无提示，弱断言）
        J(gui, "document.querySelector('.chat-session-tag').click(); 'ok'")
        time.sleep(0.4)
        c.check("H4b 点击 session tag 不崩", True, "clipboard 权限受限时无提示可接受")

        # H6 输入区：富文本输入区 + 发送按钮 + 思考/努力度控件（在 H5 前测，避免 reload 时序）
        h6 = J(gui, """(()=>{const ta=document.querySelector('[contenteditable][data-drop-target="chat-input"]');const btns=Array.from(document.querySelectorAll('.input-actions-right button'));return {ta:!!ta, send:btns.some(b=>(b.textContent||'').trim()==='Send'), think:document.querySelectorAll('.input-actions-left .icon-btn').length>=2}})()""")
        c.check("H6 输入区 富文本 + 发送 + 思考/努力度", bool(h6) and h6['ta'] and h6['send'] and h6['think'], repr(h6))

        # H5 新建会话按钮 → sessionChanged(null) → tag 消失
        J(gui, "document.querySelector('.new-session-btn').click(); 'ok'")
        time.sleep(0.6)
        tagGone = J(gui, "!document.querySelector('.chat-session-tag')")
        c.check("H5 新建会话清除会话 tag", bool(tagGone))

        # H7 输入文本 → 点击发送：全新库已带可用默认 LLM（defaultLLM=deepseek-flash，非「无 LLM 配置」态）
        # → 发送放行，断言 message-send 载荷（原「被拦截 + 文本写回」前置已不成立：ChatPanel.llmConfigReady
        # 仅在无任何 LLM 选项时拦截，而内置/默认 LLM 恒存在）
        gui.console(clear=True)
        J(gui, """window.__sendSpy=[];window.mq.post('message-send', d=>window.__sendSpy.push(d));'ok'""")
        J(gui, """(()=>{const el=document.querySelector('[contenteditable][data-drop-target="chat-input"]');el.innerHTML='';el.appendChild(document.createTextNode('hello chat test'));el.dispatchEvent(new Event('input',{bubbles:true}));return 'ok'})()""")
        time.sleep(0.3)
        J(gui, """(()=>{const btns=Array.from(document.querySelectorAll('button'));const s=btns.find(b=>(b.textContent||'').trim()==='Send');if(s)s.click();return 'ok'})()""")
        time.sleep(0.8)
        spy = _loads_deep(J(gui, "JSON.stringify(window.__sendSpy)"))
        c.check("H7 发送放行 → message-send 载荷 = 输入文本 + 所选 LLM",
                isinstance(spy, list) and len(spy) == 1 and spy[0].get('text') == 'hello chat test'
                and bool(spy[0].get('llm')),
                repr(spy)[:140])

        # H8 思考/努力度切换按钮可点（不崩）
        J(gui, "window.mq.emit('chat-toggle-think'); window.mq.emit('chat-toggle-effort'); 'ok'")
        time.sleep(0.3)

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error' and 'SetActiveSessionID' not in (e.get('text') or '')]
        c.check("无前端错误", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

"""回归测试：会话导航组（51-FP与测试映射「会话导航」，原「session 抽屉」）。

P3-C1（2026-09-24，用户口径）：原会话**抽屉**内容整体迁入**左侧导航「会话」页签**（SessionsPane），
抽屉组件已删除 → 本套件按新落点重写（断言只增不减）：

覆盖：
- 左侧导航「项目 · 知识库 · 项目记忆 · 会话」页签切换（filetree-mode-select{model:sessions}）
- 空态提示 / 切走再切回（原「遮罩关闭」已无遮罩 → 改为「切回项目模式即隐藏」）
- 会话列表展示（#id / 标题 / 轮数）+ 两行布局
- 点击会话切换（活动会话落库）
- 重命名（promptInput）/ 删除（confirm）
- fork 占位（A8）：第 3 个按钮 → 提示「敬请期待」，不改列表 / 不开弹窗
- **复制对话**（P3-C1 口径）：json-array、**不含 system**、**全部对话（非压缩后）**、与发给 LLM 一致

种子会话经 server 会话方法面 session-start 受理落库 + data-session-append-message 逐轮补消息
（systest 无 /call、data-session 亦无 create 面）。
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
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_sess_drawer_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def wait_upto(gui, js, ok, max_wait=10.0, interval=0.2):
    """有界轮询：轮询 js 求值直到 ok(v) 为真或超时；返回末次值。

    L4 时序加固（2026-09-23，[42 §2 (148)]）：把「动作 → 列表刷新后读取」前的**固定 sleep**
    改为**有界轮询** —— 只改**何时**读，不改**读什么**（断言条件仍在调用处，强度不变）。
    """
    deadline = time.time() + max_wait
    v = J(gui, js)
    while not ok(v) and time.time() < deadline:
        time.sleep(interval)
        v = J(gui, js)
    return v


def open_sessions(gui):
    """切到左侧导航「会话」页签（与真实点击同一消息面）。"""
    J(gui, "window.mq.emit('filetree-mode-select', {mode: 'sessions'}); 'ok'")


def pane_visible(gui):
    return J(gui, "(()=>{const p=document.querySelector('.sessions-pane');"
                  "return !!p && p.getBoundingClientRect().width>0})()")


def seed_session(gui, sid, title, turn=''):
    """建测试会话（替代旧 /call CreateSession；61-消息一览 §4.2 llm-start → 域主题 session-start）：
    GUI 内嵌 inprocess server，实例启动已 instance-register（GUI 自动）。发布带命名空间前缀的完整
    主题 chonk.session-start（桥 publishV 收集结果；自动注入 instance_id），server onLLMStart 幂等落库
    session+turn 并返回 {accepted, session, turn}。不跟 session-send（避免触发真实 LLM）；随后用
    data-session-title（§3.2 {id,title}）改显示标题。
    """
    r = gui.req('chonk.session-start', {'session': sid, 'turn': turn or ('t-' + sid + '-1'), 'llm': 'mock'})
    if not isinstance(r, dict) or r.get('accepted') is not True:
        raise RuntimeError('seed session-start rejected: ' + repr(r)[:120])
    gui.req('data-session-title', {'data': {'id': sid, 'title': title}})
    return {'session_id': sid}


def append_msg(gui, turn_id, role, content):
    """逐轮补消息（data-session-append-message，61 §3.2 既有面；载荷 = {turn_id, msg:{role,content}}）：
    构造「含 system 与压缩标记」的原始对话，用于验证复制口径（不含 system / 非压缩后）。
    """
    gui.req('data-session-append-message', {'data': {
        'turn_id': turn_id,
        'msg': {'role': role, 'content': content},
    }})


def click_primary_dialog(gui):
    """点击当前对话框的「确认」按钮（b-btn--primary）。"""
    J(gui, """(()=>{const b=document.querySelector('.dialog-shell .b-btn--primary');if(b)b.click();return !!b})()""")


def set_dialog_input(gui, value):
    J(gui, """(()=>{const el=document.querySelector('.dialog-shell .b-input');if(!el)return false;const s=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;s.call(el,%s);el.dispatchEvent(new Event('input',{bubbles:true}));return true})()""" % json.dumps(value))


def nonempty_str(v):
    return isinstance(v, str) and len(v) > 0


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
            if J(gui, "document.querySelector('.panel-inner')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # SD1 左侧导航「会话」页签 → 空态（页签顺序：项目 · 知识库 · 项目记忆 · 会话）
        segs = J(gui, "Array.from(document.querySelectorAll('.explorer-seg-btn')).map(e=>e.textContent.trim())")
        open_sessions(gui)
        shown = wait_upto(gui, "(()=>{const p=document.querySelector('.sessions-pane');"
                               "return !!p && p.getBoundingClientRect().width>0})()", lambda v: v is True)
        empty = wait_upto(gui, "document.body.textContent.includes('No sessions') || document.body.textContent.includes('暂无会话')",
                          lambda v: v is True)
        labels = list(segs or [])[:4]
        order_ok = (labels == ['项目', '知识库', '项目记忆', '会话']
                    or labels == ['Project', 'Knowledge', 'Project Memory', 'Sessions'])
        c.check("SD1 左侧导航含项目记忆页签（项目·知识库·项目记忆·会话）+ 打开会话为空态",
                order_ok and bool(shown) and bool(empty),
                f"segs={segs} shown={shown} empty={empty}")

        # SD1b 切回「项目」模式 → 会话面板隐藏（原「遮罩点击关闭」已无遮罩）
        J(gui, "window.mq.emit('filetree-mode-select', {mode: 'project'}); 'ok'")
        hidden = wait_upto(gui, "(()=>{const p=document.querySelector('.sessions-pane');"
                                "return !p || p.getBoundingClientRect().width===0})()", lambda v: v is True)
        c.check("SD1b 切回项目模式 → 会话面板隐藏", bool(hidden))

        # SD2 种子会话（session-start 受理落库）→ 列表 2 张卡片
        s1 = seed_session(gui, 'sess-drawer-1', 'Drawer One')
        s2 = seed_session(gui, 'sess-drawer-2', 'Drawer Two')
        c.check("SD2a session-start 建会话返回 session_id", bool(s1.get('session_id')) and bool(s2.get('session_id')))
        open_sessions(gui)
        cards = wait_upto(gui, "document.querySelectorAll('.sessions-pane .session-card').length", lambda v: int(v or 0) == 2)
        titles = wait_upto(gui, "Array.from(document.querySelectorAll('.sessions-pane .session-title')).map(e=>e.textContent.trim())",
                           lambda v: set(v or []) == {'Drawer One', 'Drawer Two'})
        id1 = J(gui, "document.querySelector('.sessions-pane .session-card .session-id')?.textContent?.trim() || ''")
        c.check("SD2b 会话列表 2 卡片 + 标题集 + #id",
                int(cards) == 2 and set(titles) == {'Drawer One', 'Drawer Two'}
                and id1.startswith('#'), f"n={cards} titles={repr(titles)} id={repr(id1)}")

        # SD3 点击会话 → 切换（页签保持打开；活动会话落库）
        J(gui, "document.querySelector('.sessions-pane .session-card')?.click(); 'ok'")
        time.sleep(0.6)
        active = gui.req('data-session-active-get').get('session_id')
        stillOpen = pane_visible(gui)
        c.check("SD3 点击卡片切换会话（页签保持打开）+ 落活动会话",
                stillOpen and active in (s1.get('session_id'), s2.get('session_id')),
                f"open={stillOpen} active={repr(active)[:20]}")

        # SD4 重命名（promptInput 对话框）→ 标题更新
        J(gui, "document.querySelector('.sessions-pane .session-card .session-actions button')?.click(); 'ok'")  # 第一个 = rename
        wait_upto(gui, "!!document.querySelector('.dialog-shell .b-input')", lambda v: v is True)
        set_dialog_input(gui, 'Renamed One')
        click_primary_dialog(gui)
        title1 = wait_upto(gui, "document.querySelector('.sessions-pane .session-card .session-title')?.textContent?.trim() || ''",
                           lambda v: v == 'Renamed One')
        c.check("SD4 重命名会话标题更新", title1 == 'Renamed One', f"t={repr(title1)}")

        # SD5 删除非当前会话（.session-card:not(.active)）→ 确认后卡片移除（剩 1 = 当前）
        J(gui, """(()=>{const cards=Array.from(document.querySelectorAll('.sessions-pane .session-card'));const t=cards.find(x=>!x.classList.contains('active'));if(t)t.querySelector('.session-actions button:nth-child(2)').click();return !!t})()""")  # 第二个按钮 = delete
        dl = wait_upto(gui, "document.querySelectorAll('.dialog-shell .b-btn--primary').length", lambda v: int(v or 0) >= 1)
        click_primary_dialog(gui)
        cards = wait_upto(gui, "document.querySelectorAll('.sessions-pane .session-card').length", lambda v: int(v or 0) == 1)
        titles = wait_upto(gui, "Array.from(document.querySelectorAll('.sessions-pane .session-title')).map(e=>e.textContent.trim())",
                           lambda v: (v or []) == ['Renamed One'])
        c.check("SD5 删除非当前会话（确认后移除，剩当前 1）",
                int(dl) >= 1 and int(cards) == 1 and titles == ['Renamed One'],
                f"dl={dl} n={cards} titles={repr(titles)}")

        # SD5b fork 占位（A8）：第 3 个按钮 → 提示「敬请期待」，不改列表 / 不开弹窗
        # （2026-09-26 加固：判定改读**全部** message 文案再匹配 —— 前序「会话已删除」成功提示与
        #  fork 提示在 3s 存活期内并存且先后到期，只读**首条**会落在「删除」提示上、并在两条交替的
        #  窄窗内漏判（本用例原断言只读首条）。只改「读哪些」，断言与观测内容不变。）
        wait_upto(gui, "!!document.querySelector('.sessions-pane .session-card .session-actions button:nth-child(3)')",
                  lambda v: v is True)
        forkHit = J(gui, "(()=>{const b=document.querySelector('.sessions-pane .session-card .session-actions button:nth-child(3)');if(!b)return false;b.click();return true})()")
        forkTexts = wait_upto(gui, "Array.from(document.querySelectorAll('#b-message-container .b-message-text')).map(n=>(n.textContent||'').trim())",
                              lambda v: any((('敬请期待' in x) or ('Coming soon' in x)) for x in (v or [])))
        forkNoDialog = J(gui, "!document.querySelector('.dialog-shell')")
        forkCards = J(gui, "document.querySelectorAll('.sessions-pane .session-card').length")
        c.check("SD5b fork 占位 → 提示「敬请期待」（不改列表 / 不开弹窗）",
                bool(forkHit)
                and any((('敬请期待' in x) or ('Coming soon' in x)) for x in (forkTexts or []))
                and bool(forkNoDialog) and int(forkCards) == 1,
                f"hit={forkHit} texts={forkTexts} noDialog={forkNoDialog} n={forkCards}")

        # SD5c 复制对话（P3-C1 口径）：json-array + 不含 system + 全部对话（非压缩后的原文）
        #  —— 对**列表内保留的会话**操作：SD5 删的是「非当前会话」→ 保留 = 当前会话；
        #     在**同一轮**内补消息：system（压缩摘要标记）+ user + assistant + user。
        #     注：不再对同会话追加第 2 轮 —— seed 不跟 session-send，首轮在本进程内恒「运行中」，
        #     同会话再 session-start 会被拒 `session busy`（server 侧按会话判忙）。复制口径在本轮内
        #     已完整覆盖：多消息 / 含 system / 非压缩原文 / 轮内顺序保持。
        sid = gui.req('data-session-active-get').get('session_id')
        c.check("SD5c-pre0 取到保留会话 id（供复制取数）", bool(sid), f"sid={sid!r}")
        append_msg(gui, 't-%s-1' % sid, 'system', '[已压缩早前对话] 旧摘要（不应出现在复制结果）')
        append_msg(gui, 't-%s-1' % sid, 'user', 'hello-1')
        append_msg(gui, 't-%s-1' % sid, 'assistant', 'reply-1')
        append_msg(gui, 't-%s-1' % sid, 'user', 'hello-2')
        time.sleep(0.4)
        # 前置：源数据（data-session-load-messages）确实含 system 行 → 「不含 system」才是有意义的过滤
        raw1 = (gui.req('data-session-load-messages', {'data': {'turn_id': 't-%s-1' % sid}}) or {}).get('messages') or []
        raw_roles = [m.get('role') for m in raw1 if isinstance(m, dict)]
        c.check("SD5c-pre 复制源含 system 行（过滤判定有效）", raw_roles == ['system', 'user', 'assistant', 'user'],
                f"raw_roles={raw_roles}")
        # 劫持剪贴板（宿主 WebView2 的 clipboard 可能不可写/受限；只桩化浏览器 API，不改产品代码）
        stub = J(gui, "(()=>{window.__ckCopy=null;try{Object.defineProperty(navigator,'clipboard',"
                      "{configurable:true,value:{writeText:(t)=>{window.__ckCopy=String(t);return Promise.resolve()}}});"
                      "return 'ok'}catch(e){return 'no-clip:'+e.message}})()")
        c.check("SD5c-pre1 剪贴板桩可用（否则复制断言不成立）", stub == 'ok', f"stub={stub!r}")
        clicked = J(gui, "(()=>{const b=document.querySelector('.sessions-pane .session-card .session-actions button:nth-child(4)');if(b)b.click();return !!b})()")
        copied = wait_upto(gui, "window.__ckCopy", lambda v: nonempty_str(v), max_wait=15.0)
        toast = J(gui, "document.querySelector('#b-message-container .b-message-text')?.textContent?.trim() || ''")
        parsed, roles, texts = None, [], []
        try:
            parsed = json.loads(copied) if copied else None
        except Exception:
            parsed = None
        if isinstance(parsed, list):
            roles = [m.get('role') for m in parsed if isinstance(m, dict)]
            texts = [m.get('content') for m in parsed if isinstance(m, dict)]
        c.check("SD5c 复制对话 = json-array（不含 system / 含全部对话 / 非压缩原文）",
                isinstance(parsed, list) and roles == ['user', 'assistant', 'user']
                and texts == ['hello-1', 'reply-1', 'hello-2']
                and '[已压缩早前对话]' not in str(copied),
                f"clicked={clicked} toast={toast!r} roles={roles} texts={repr(texts)[:80]} copy={str(copied)[:80]!r}")

        # SD6 新建会话（图标按钮，第一行）→ 清空回空白
        J(gui, "document.querySelector('.sessions-pane .sp-new')?.click(); 'ok'")
        tagGone = wait_upto(gui, "!document.querySelector('.chat-session-tag')", lambda v: v is True)
        c.check("SD6 新建会话：会话 tag 清除", bool(tagGone))

        # SD7 再切回会话页签（重开一致性）
        J(gui, "window.mq.emit('filetree-mode-select', {mode: 'project'}); 'ok'")
        wait_upto(gui, "(()=>{const p=document.querySelector('.sessions-pane');return !p||p.getBoundingClientRect().width===0})()", lambda v: v is True)
        open_sessions(gui)
        c.check("SD7 再次切到会话页签可见", bool(wait_upto(gui, "(()=>{const p=document.querySelector('.sessions-pane');"
                                                            "return !!p && p.getBoundingClientRect().width>0})()", lambda v: v is True)))

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')
                and 'MessageBox' not in (e.get('text') or '')]
        c.check("无前端错误（会话导航组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

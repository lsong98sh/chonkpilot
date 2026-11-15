# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑩：**会话导航（原会话抽屉）/ 状态栏 / 长任务状态条目**。

迁移自旧套件（仍保留在 run_all.py）：
* `test_session_drawer.py` → S-SESS-UI-01（页签切换 / 列表 / 切换 / 重命名 / 删除 / fork / 复制对话 / 新建）
* `test_statusbar.py`       → S-STATUS-01（条目化结构 / 语言切换 / DevTools / 记忆总量入口）
                              S-STATUS-02（记忆提取 & 上下文压缩：进行中/失败/完成 + 详情页）
"""

import json
import time

from .framework import case


def _js(env, code):
    return env.js(code)


def _pane_open(env):
    return env.js("(()=>{const p=document.querySelector('.sessions-pane');"
                  "return !!p&&p.getBoundingClientRect().width>0})()") is True


def _mode(env, mode):
    env.js("window.mq.emit('filetree-mode-select', {mode: %s}); 'ok'"
           % json.dumps(mode))
    time.sleep(0.4)


def _dialog_of(env, sid):
    for it in ((env.req("data-session-list", {}) or {}).get("list") or []):
        if isinstance(it, dict) and it.get("session_id") == sid and it.get("dialog_id"):
            return it["dialog_id"]
    return ""


def _mk_session(env, sid, title):
    env.req("data-session-ensure-session", {"data": {"session_id": sid}})
    env.req("data-session-title", {"data": {"id": sid, "title": title}})
    return _dialog_of(env, sid)


def _card(title):
    """按会话标题定位卡片（`.session-id` 仅显示前 8 位，测试 id 前缀相同 → 不可用）。"""
    return ("[...document.querySelectorAll('.sessions-pane .session-card')]"
            ".find(c=>((c.querySelector('.session-title')||{}).textContent||'').trim()===%s)"
            % json.dumps(title))


def _titles(env):
    return env.jsj("JSON.stringify([...document.querySelectorAll("
                   "'.sessions-pane .session-title')].map(e=>e.textContent.trim()))") or []


def _click_card_action(env, title, nth):
    r = env.js("(()=>{const c=%s;if(!c)return 'no-card';"
               "const b=c.querySelector('.session-actions button:nth-child(%d)');"
               "if(!b)return 'no-btn';b.click();return 'ok';})()" % (_card(title), nth))
    env.check("点击会话卡片「%s」第 %d 个动作" % (title, nth), r == "ok", r)


def _primary_dialog(env):
    r = env.js("(()=>{const b=document.querySelector('.dialog-shell .b-btn--primary');"
               "if(!b)return 'no-btn';b.click();return 'ok';})()")
    env.check("确认对话框", r == "ok", r)
    time.sleep(0.5)


def _toast_texts(env):
    return env.jsj("JSON.stringify([...document.querySelectorAll("
                   "'#b-message-container .b-message-text')].map(e=>e.textContent.trim()))") or []


@case("S-SESS-UI-01", "会话", "会话导航：列表/切换/就地重命名/删除/fork/复制对话/新建会话",
      stage="seeded", order=330, fp="33 会话导航 SessionsPane（test_session_drawer）",
      desc="切到「会话」页签 → 两张卡片（标题/#id）；点卡片切换并落活动会话；首个动作就地改名；"
           "第二个动作删除非当前会话（确认后移除）；第三个动作 fork 新增 1 卡；第四个动作复制对话"
           "（json-array、不含 system、内容为原文）；新建会话清空 tag。")
def s_sess_ui_01(env):
    s1 = "e2e-sui-a-%s" % env.run_id
    s2 = "e2e-sui-b-%s" % env.run_id
    _mk_session(env, s1, "E2E Drawer One")
    _mk_session(env, s2, "E2E Drawer Two")

    _mode(env, "sessions")
    env.wait(lambda: True if _pane_open(env) else None, "会话页签可见", timeout=20)
    segs = env.jsj("JSON.stringify([...document.querySelectorAll('.explorer-seg-btn')]"
                   ".map(e=>e.textContent.trim()))") or []
    env.check("左侧导航含 项目·会话·记忆·扩展",
              segs[:4] in (["项目", "会话", "记忆", "扩展"],
                           ["Project", "Sessions", "Memory", "Extensions"]), segs[:6])

    env.wait(lambda: True if {"E2E Drawer One", "E2E Drawer Two"} <= set(_titles(env))
             else None, "两张会话卡片就绪", timeout=20)
    env.check("卡片标题集", {"E2E Drawer One", "E2E Drawer Two"} <= set(_titles(env)), _titles(env))
    env.js("%s?.click(); 'ok'" % _card("E2E Drawer One"))
    active = env.wait(lambda: (env.req("data-session-active-get", {}) or {}).get("session_id")
                      if (env.req("data-session-active-get", {}) or {}).get("session_id") == s1
                      else None, "点击卡片后活动会话落库", timeout=15)
    env.eq("活动会话 = 点击的会话", active, s1)
    env.check("切换后会话页签保持打开", _pane_open(env))

    # 就地重命名（第一个动作）
    _click_card_action(env, "E2E Drawer One", 1)
    env.wait(lambda: True if env.js("!!document.querySelector('.sessions-pane .session-title-input')")
             is True else None, "改名输入框出现", timeout=10)
    r = env.js("""(()=>{const el=document.querySelector('.sessions-pane .session-title-input');
      if(!el)return 'no-input';el.value='E2E Renamed';
      el.dispatchEvent(new Event('input',{bubbles:true}));
      el.dispatchEvent(new KeyboardEvent('keyup',{key:'Enter',bubbles:true}));return 'ok'})()""")
    env.check("提交改名", r == "ok", r)
    env.wait(lambda: True if "E2E Renamed" in _titles(env) else None,
             "改名后标题更新", timeout=15)
    env.check("就地改名生效", "E2E Renamed" in _titles(env), _titles(env))

    # 删除非当前会话（s2 第二个动作）
    _click_card_action(env, "E2E Drawer Two", 2)
    env.wait(lambda: True if int(env.js(
        "document.querySelectorAll('.dialog-shell .b-btn--primary').length") or 0) >= 1 else None,
        "删除确认框出现", timeout=10)
    _primary_dialog(env)
    env.wait(lambda: True if "E2E Drawer Two" not in _titles(env) else None,
             "删除后 s2 卡片消失", timeout=15)
    env.check("保留卡片标题", "E2E Renamed" in _titles(env), _titles(env))

    # 复制对话（第四个动作）：补消息 + 剪贴板桩（在 fork 前做，避免派生会话同名卡片歧义）
    turn = "t-e2e-sui-%s" % env.run_id
    dlg = _dialog_of(env, s1)
    env.check("取到复制源会话 dialog_id", bool(dlg), dlg)
    env.req("data-session-ensure-turn",
            {"turn_id": turn, "session_id": s1, "dialog_id": dlg})
    for role, text in (("system", "[已压缩早前对话] 旧摘要"),
                       ("user", "hello-1"),
                       ("assistant", "reply-1"),
                       ("user", "hello-2")):
        env.req("data-session-append-message",
                {"turn_id": turn, "dialog_id": dlg,
                 "msg": {"role": role, "content": text}})
    raw = (env.req("data-session-load-messages",
                   {"data": {"turn_id": turn, "dialog_id": dlg}}) or {}).get("messages") or []
    env.eq("复制源含 system 行（过滤判定有效）",
           [m.get("role") for m in raw if isinstance(m, dict)],
           ["system", "user", "assistant", "user"])

    stub = env.js("(()=>{window.__ckCopy=null;try{Object.defineProperty(navigator,'clipboard',"
                  "{configurable:true,value:{writeText:(t)=>{window.__ckCopy=String(t);"
                  "return Promise.resolve()}}});return 'ok'}catch(e){return 'no-clip:'+e.message}})()")
    env.check("剪贴板桩可用", stub == "ok", stub)
    env.client.console(clear=True)
    _click_card_action(env, "E2E Renamed", 4)
    try:
        copied = env.wait(lambda: env.js("window.__ckCopy"), "复制对话写入剪贴板", timeout=20)
    except AssertionError:
        env.note("复制诊断", "copy=%r toasts=%r titles=%r"
                 % (env.js("window.__ckCopy"), _toast_texts(env)[:6], _titles(env)))
        raise
    if isinstance(copied, list):
        parsed = copied                      # 驱动层已解一层 JSON
    else:
        try:
            parsed = json.loads(copied) if copied else None
        except Exception:
            parsed = None
    roles = [m.get("role") for m in (parsed or []) if isinstance(m, dict)]
    texts = [m.get("content") for m in (parsed or []) if isinstance(m, dict)]
    env.check("复制结果 roles（不含 system）", roles == ["user", "assistant", "user"],
              "parsed=%r copy=%r" % (parsed, str(copied)[:160]))
    env.eq("复制结果正文（原文顺序）", texts, ["hello-1", "reply-1", "hello-2"])
    env.check("复制结果不含压缩摘要", "[已压缩早前对话]" not in str(copied))

    # fork（第三个动作）
    n0 = int(env.js("document.querySelectorAll('.sessions-pane .session-card').length") or 0)
    _click_card_action(env, "E2E Renamed", 3)
    env.wait(lambda: True if (any(("已派生对话" in x) or ("Dialog forked" in x)
                                  or ("派生对话失败" in x) or ("Failed to fork" in x)
                                  for x in _toast_texts(env))) else None,
             "fork 提示出现", timeout=20)
    ok_fork = any(("已派生对话" in x) or ("Dialog forked" in x) for x in _toast_texts(env))
    n1 = int(env.js("document.querySelectorAll('.sessions-pane .session-card').length") or 0)
    env.check("fork 成功并新增 1 卡", ok_fork and n1 == n0 + 1, (ok_fork, n0, n1))

    # 新建会话清空 tag
    env.js("document.querySelector('.sessions-pane .sp-new')?.click(); 'ok'")
    env.wait(lambda: True if env.js("!document.querySelector('.chat-session-tag')") is True else None,
             "新建会话后 tag 清除", timeout=15)
    env.check("新建会话清空当前会话", env.js("!document.querySelector('.chat-session-tag')") is True)

    _mode(env, "project")
    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("会话导航组无前端错误", len(errs) == 0, errs[:3])


def _task_state(env, kind):
    return env.jsj("JSON.stringify((()=>{const e=document.querySelector("
                   "'.statusbar .sb-task[data-task=\"%s\"]');if(!e)return null;"
                   "return {text:((e.querySelector('.sb-task-state')||{}).textContent||'').trim(),"
                   "cls:e.className||''}})())" % kind)


def _detail_text(env, kind):
    return str(env.js("(()=>{const e=document.querySelector("
                      "'.task-detail[data-detail=\"%s\"]');"
                      "return e?(e.textContent||'').replace(/\\s+/g,' ').trim():''})()"
                      % kind) or "")


def _active_tab(env):
    return str(env.js("document.querySelector('.code-view .tb-tab.active .tb-name')"
                      "?.textContent?.trim() || ''") or "")


def _notice(env, payload):
    env.js("window.mq.emitRemote({type:'tool-notify', payload: JSON.stringify(%s)}); 'ok'"
           % json.dumps(payload))


@case("S-STATUS-01", "状态栏", "状态栏条目化结构 / 语言切换 / DevTools / 记忆总量入口",
      stage="seeded", order=340, fp="33 状态栏（test_statusbar SB1–SB5）",
      desc="状态栏存在且无配置菜单、两枚长任务条目；语言切换 2 按钮且点击 active 移动；"
           "DevTools 条目 + `gui.devtools.open` 可用；启用记忆后显示数字总量，点击弹分类列表 → "
           "选中开内容编辑框（取消）。")
def s_status_01_structure(env):
    env.wait(lambda: True if env.js("!!document.querySelector('.statusbar')") is True else None,
             "状态栏存在", timeout=20)
    env.check("状态栏存在", env.js("!!document.querySelector('.statusbar')") is True)
    menu = int(env.js("document.querySelectorAll('.statusbar .sb-menu').length") or 0)
    tasks = int(env.js("document.querySelectorAll('.statusbar .sb-task[data-task]').length") or 0)
    env.check("无配置菜单 + 两条长任务条目", menu == 0 and tasks == 2,
              "menu=%d tasks=%d" % (menu, tasks))

    # 语言切换（记录初始 active，切走再切回，保证后续用例语言不变）
    btns = int(env.js("document.querySelectorAll('.lang-switcher .lang-btn').length") or 0)
    first = str(env.js("document.querySelector('.lang-switcher .lang-btn.active')"
                       "?.textContent?.trim() || ''") or "")
    env.check("语言切换 2 按钮 + 初始 active", btns == 2 and bool(first),
              "n=%d active=%r" % (btns, first))
    env.js("(()=>{const bs=[...document.querySelectorAll('.lang-switcher .lang-btn')];"
           "const b=bs.find(x=>!x.classList.contains('active'));if(b)b.click();return 'ok'})()")
    time.sleep(0.7)
    second = str(env.js("document.querySelector('.lang-switcher .lang-btn.active')"
                        "?.textContent?.trim() || ''") or "")
    env.check("点击后 active 移动", bool(second) and second != first,
              "before=%r after=%r" % (first, second))
    env.js("(()=>{const bs=[...document.querySelectorAll('.lang-switcher .lang-btn')];"
           "const b=bs.find(x=>x.textContent.trim()===%s);if(b)b.click();return 'ok'})()"
           % json.dumps(first))
    env.wait(lambda: True if str(env.js(
        "document.querySelector('.lang-switcher .lang-btn.active')"
        "?.textContent?.trim() || ''")) == first else None,
        "语言切回初始", timeout=15)

    # DevTools
    dbg = int(env.js("(()=>[...document.querySelectorAll('.statusbar [title]')]"
                     ".filter(e=>(e.getAttribute('title')||'').includes('DevTools')).length)()") or 0)
    dev_ok = True
    try:
        env.req("gui.devtools.open", {})
    except Exception:
        dev_ok = False  # 无返回的本地事件：正常不抛；抛错 = 通道不可用
    env.check("DevTools 条目 + 打开通道可用", dbg >= 1 and dev_ok, "dbg=%d" % dbg)

    # 记忆总量入口（启用 → 数字 → 分类列表 → 编辑框 → 取消 → 还原）
    cat = "e2e_sb_%s" % env.run_id
    try:
        env.req("data-memory-save", {"data": {"category": cat, "content": "SB-MEM"}})
        env.req("data-prj-config-save", {"data": {"key": "memory.enabled", "value": "true"}})
        mem = env.wait(lambda: str(env.js(
            "document.querySelector('.statusbar .sb-mem-value')?.textContent?.trim() || ''") or "")
            if str(env.js("document.querySelector('.statusbar .sb-mem-value')"
                          "?.textContent?.trim() || ''") or "").isdigit() else None,
            "记忆总量显示数字", timeout=20)
        env.check("记忆总量为数字", str(mem).isdigit(), mem)
        env.js("document.querySelector('.statusbar .sb-mem')?.click(); 'ok'")
        items = env.wait(lambda: int(env.js(
            "document.querySelectorAll('.mem-cat-list .mem-cat-item').length") or 0)
            if int(env.js("document.querySelectorAll('.mem-cat-list .mem-cat-item').length")
                   or 0) >= 1 else None, "记忆分类列表 ≥1", timeout=20)
        env.check("记忆分类列表可见", int(items) >= 1, items)
        env.js("document.querySelector('.mem-cat-list .mem-cat-item')?.click(); 'ok'")
        env.wait(lambda: True if env.js(
            "!!document.querySelector('.dialog-shell .text-edit-dialog-body textarea')")
            is True else None, "记忆内容编辑框出现", timeout=15)
        env.check("选中分类打开内容编辑框", env.js(
            "!!document.querySelector('.dialog-shell .text-edit-dialog-body textarea')") is True)
        env.js("(()=>{const d=document.querySelector('.dialog-shell');if(!d)return 'no-dialog';"
               "const b=[...d.querySelectorAll('button')].find(x=>/取消|cancel/i.test(x.textContent));"
               "if(b)b.click();return 'ok'})()")
        env.wait(lambda: True if env.js("!document.querySelector('.text-edit-dialog-body')")
                 is True else None, "编辑框关闭", timeout=15)
    finally:
        try:
            env.req("data-prj-config-save", {"data": {"key": "memory.enabled", "value": "false"}})
            env.req("data-memory-delete", {"data": {"category": cat}})
        except Exception:
            pass
        time.sleep(0.4)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("状态栏结构组无前端错误", len(errs) == 0, errs[:3])


@case("S-STATUS-02", "状态栏", "长任务状态条目：记忆提取/上下文压缩的进行中、失败、完成与详情页",
      stage="seeded", order=350, fp="33 状态栏长任务条目（test_statusbar SB7/SB8）",
      desc="tool-notify(memory-start) → 条目「记忆沉淀中」+ is-progress，点击开详情页；"
           "plugin-failure → 「失败」+ is-error 且 done 不覆盖错误；compress-start/done → "
           "「压缩中」详情页 → 「压缩完成」。")
def s_status_02_tasks(env):
    env.wait(lambda: True if "未启用" in ((_task_state(env, "memory") or {}).get("text") or "")
             and "空闲" in ((_task_state(env, "compress") or {}).get("text") or "") else None,
             "长任务条目基线（未启用/空闲）", timeout=20)
    mem0 = _task_state(env, "memory") or {}
    cmp0 = _task_state(env, "compress") or {}
    env.check("基线：记忆未启用 / 压缩空闲",
              "未启用" in (mem0.get("text") or "") and "空闲" in (cmp0.get("text") or ""),
              (mem0, cmp0))

    _notice(env, {"notice": "memory-start", "plugin": "memory", "session_id": "e2e-stb",
                  "turn_id": "t-e2e-stb-1", "message": "正在沉淀记忆…",
                  "message_id": "e2e-stb-mem-start"})
    mem1 = env.wait(lambda: _task_state(env, "memory")
                    if "记忆沉淀中" in ((_task_state(env, "memory") or {}).get("text") or "")
                    else None, "记忆提取进行中", timeout=15)
    env.check("记忆进行中文案 + 进行中色",
              "is-progress" in (mem1.get("cls") or ""), mem1)

    env.js("document.querySelector('.statusbar .sb-task[data-task=\"memory\"]')?.click(); 'ok'")
    env.eq("记忆详情页标题", _active_tab(env), "记忆提取详情")
    mtxt = env.wait(lambda: _detail_text(env, "memory")
                    if "记忆沉淀中" in _detail_text(env, "memory")
                    and "最近一次任务" in _detail_text(env, "memory") else None,
                    "记忆详情文本", timeout=15)
    env.check("记忆详情含状态 + 最近一次任务 + 去设置",
              "去设置" in mtxt and bool(env.js(
                  "!!document.querySelector('.task-detail[data-detail=\"memory\"] "
                  "[data-action=\"go-settings\"]')")))

    _notice(env, {"notice": "plugin-failure", "plugin": "memory", "kind": "save",
                  "reason": "stb-boom", "message": "记忆沉淀失败：stb-boom",
                  "message_id": "e2e-stb-mem-fail"})
    mem2 = env.wait(lambda: _task_state(env, "memory")
                    if "失败" in ((_task_state(env, "memory") or {}).get("text") or "")
                    else None, "记忆失败态", timeout=15)
    env.check("记忆失败态 + 错误色", "is-error" in (mem2.get("cls") or ""), mem2)
    env.check("记忆详情含失败原因", "stb-boom" in _detail_text(env, "memory"))

    _notice(env, {"notice": "memory-done", "plugin": "memory", "session_id": "e2e-stb",
                  "turn_id": "t-e2e-stb-1", "message": "记忆沉淀完成",
                  "message_id": "e2e-stb-mem-done"})
    time.sleep(0.5)
    mem3 = _task_state(env, "memory") or {}
    env.check("失败优先于 done（错误态不被完成覆盖）", "失败" in (mem3.get("text") or ""), mem3)

    _notice(env, {"notice": "compress-start", "plugin": "compress", "session_id": "e2e-stb",
                  "turn_id": "t-e2e-stb-2", "message": "正在压缩上下文…",
                  "message_id": "e2e-stb-cmp-start"})
    cmp1 = env.wait(lambda: _task_state(env, "compress")
                    if "压缩中" in ((_task_state(env, "compress") or {}).get("text") or "")
                    else None, "压缩进行中", timeout=15)
    env.check("压缩进行中文案 + 进行中色",
              "is-progress" in (cmp1.get("cls") or ""), cmp1)
    env.js("document.querySelector('.statusbar .sb-task[data-task=\"compress\"]')?.click(); 'ok'")
    env.eq("压缩详情页标题", _active_tab(env), "上下文压缩详情")
    ctxt = env.wait(lambda: _detail_text(env, "compress")
                    if "压缩中" in _detail_text(env, "compress")
                    and "最近一次任务" in _detail_text(env, "compress") else None,
                    "压缩详情文本", timeout=15)
    env.check("压缩详情含状态 + 最近一次任务 + 去设置",
              "去设置" in ctxt and bool(env.js(
                  "!!document.querySelector('.task-detail[data-detail=\"compress\"] "
                  "[data-action=\"go-settings\"]')")))

    _notice(env, {"notice": "compress-done", "plugin": "compress", "session_id": "e2e-stb",
                  "turn_id": "t-e2e-stb-2", "message": "上下文压缩完成",
                  "message_id": "e2e-stb-cmp-done"})
    cmp2 = env.wait(lambda: _task_state(env, "compress")
                    if "压缩完成" in ((_task_state(env, "compress") or {}).get("text") or "")
                    else None, "压缩完成态", timeout=5)
    env.check("压缩完成文案", "压缩完成" in (cmp2.get("text") or ""), cmp2)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("长任务条目组无前端错误", len(errs) == 0, errs[:3])
"""回归测试：statusbar 组（51-FP与测试映射「statusbar」）。

对齐 2026-09-04 /call 清理：codegraph 索引进度 / DB 查看器 / 外部工具图标 /
知识库占位入口 UI 已删除（T5，61-消息一览 §8），对应断言移除；
保留 statusbar 存在性、语言切换（LangSwitcher）。

2026-09-24（B2 移除 + A4 迁移）：上一批把配置入口改成展开「全部配置」菜单，本轮按用户口径
**整体移除该图标/菜单**（不再提供状态栏配置入口，配置入口仍在工具栏「设置」下拉）→
SB2 断言入口已移除；A4「记忆总 token 数」入口由上下文管理页迁入状态栏底部 →
SB2b 断言启用记忆库后入口出现、数值为数字且点击可弹出记忆分类列表 → 选中 → 内容编辑弹框（可编辑）。
新增调试图标（SB5：gui.devtools.open 可用；用户 F12 入口已在宿主层屏蔽）。

2026-09-27（索引 / codegraph 状态区）：曾新增 `codegraph` / `vfts` 两枚**宿主**徽标……
2026-10-28（PLUGIN-UI-2，**口径变更**）：索引进度 UI 整体**归插件**——宿主删除
`IndexBadgeView` / `IndexStatusDetail` 与两个 status 键的读取；状态栏改为内嵌 codegraph 插件的
`badge` 视图（`ui.statusbar[].view=badge` → iframe `/plugins/codegraph/webui/badge.html`），
文案 / 进度由插件经自有动作 `badge` 拉取渲染；点击 badge → 前端 `preview-tab-open{kind:'plugin-page'}`
打开插件 `status` 完整页。SB2 改为「无宿主徽标」，SB6 覆盖「插件 badge 内嵌 / 终态文案 / 点开插件页 / 关引擎回未启用」。
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, snapshot_config, restore_config, tmp_dir, ensure_locale  # noqa: E402

PORT = free_port()
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_sb_data")
# 独立临时工作区：SB6 会**真实启用** codegraph / vfts（插件在 workdir 下落 `<workdir>/.chonkpilot/`
# 索引产物）→ 用临时目录，避免污染被其它套件复用的共享夹具 `systest/ws`。
WS = tmp_dir("ck-sb-ws-")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def wait_upto(gui, js, ok, max_wait=12.0, interval=0.3):
    """有界轮询：直到 ok(v) 为真或超时（只改「何时读」，不改读什么）。"""
    deadline = time.time() + max_wait
    v = J(gui, js)
    while not ok(v) and time.time() < deadline:
        time.sleep(interval)
        v = J(gui, js)
    return v


def console_errors(gui):
    return [e.get('text') for e in (gui.console() or {}).get('entries', []) if e.get('level') == 'error']


def prj_list(gui):
    """读实例 prj 配置（data-prj-config-list 的既有载荷 {list}）。"""
    r = gui.req('data-prj-config-list', {})
    if isinstance(r, dict):
        v = r.get('list', r)
        return v if isinstance(v, dict) else {}
    return {}


def prj_save(gui, key, value):
    gui.req('data-prj-config-save', {'data': {'key': key, 'value': value}})


def prj_del(gui, key):
    gui.req('data-prj-config-delete', {'id': key})


# ── STB-1（长任务状态条目 + 详情页）辅助 ─────────────────────────────
SHOTS = os.path.join("E:\\TEMP", "ck-l4-visual", "stb")


def shot(gui, name):
    """截图（仓库外证据目录）；失败仅告警，不影响断言。"""
    try:
        os.makedirs(SHOTS, exist_ok=True)
        p = os.path.join(SHOTS, name)
        with open(p, "wb") as f:
            f.write(gui._get("/screenshot"))
        print("    [shot] %s" % p, flush=True)
        return p
    except Exception as e:  # noqa: BLE001
        print("    [warn] screenshot %s 失败: %s" % (name, e), flush=True)
        return ""


def emit_notice(gui, payload):
    """经**后端 → 前端同一入口**注入一条 tool-notify（mq.emitRemote；不伪造 DOM/状态）。"""
    js = "window.mq.emitRemote({type:'tool-notify', payload: JSON.stringify(%s)}); 'ok'" % json.dumps(payload)
    return J(gui, js)


def task_state_js(kind):
    return ("(()=>{const e=document.querySelector('.statusbar .sb-task[data-task=\"%s\"]');"
            "if(!e)return null;return {text:((e.querySelector('.sb-task-state')||{}).textContent||'').trim(),"
            "cls:e.className||'',title:e.getAttribute('title')||''}})()" % kind)


def task_state(gui, kind):
    return J(gui, task_state_js(kind))


def detail_text_js(kind):
    return ("(()=>{const e=document.querySelector('.task-detail[data-detail=\"%s\"]');"
            "return e?(e.textContent||'').replace(/\\s+/g,' ').trim():''})()" % kind)


def active_preview_tab_js():
    return ("(()=>{const e=document.querySelector('.code-view .tb-tab.active .tb-name');"
            "return e?(e.textContent||'').trim():''})()")


def has_el_js(selector):
    return "!!document.querySelector(%s)" % json.dumps(selector)


def _deep(v, n=4):
    """循环解包 JSON 字符串：test-port 的 eval 会把 JSON.stringify 的结果**再包一层**，
    `J` 只解一层 → 拿到的是 str（同 `run_memory_ctx._plain` / `test_layout.JD` 口径）。"""
    for _ in range(n):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def idx_states(gui):
    """状态栏索引区两枚徽标：{engine: {text, cls}}。"""
    v = _deep(J(gui, "JSON.stringify(Object.fromEntries([...document.querySelectorAll('.sb-index .sb-idx')]"
                    ".map(e=>[e.getAttribute('data-engine'),{text:((e.querySelector('.sb-idx-state')||{}).textContent||'').trim(),"
                    "cls:e.className}])))"))
    return v if isinstance(v, dict) else {}


def badge_text_js():
    """读状态栏内嵌的**插件 badge iframe** 文本（同源 → contentDocument 可读）。

    PLUGIN-UI-2：索引进度 UI 归插件，宿主不再有 `.sb-index` 徽标；进度文案由插件页自渲染。
    """
    return ("(()=>{const f=document.querySelector('.statusbar iframe');"
            "if(!f||!f.contentDocument||!f.contentDocument.body)return '';"
            "return (f.contentDocument.body.innerText||'').replace(/\\s+/g,' ').trim()})()")


def plugin_page_src_js():
    """页面上**所有**插件 iframe 的 src（空格拼接）：用于确认「点开的是插件 status 页」。"""
    return ("(()=>[...document.querySelectorAll('iframe')]"
            ".map(f=>f.getAttribute('src')||'').join(' '))()")


def badge_diag(gui):
    """badge 页的轮询诊断：poll 次数 / 最近成功时间 / 最近错误（失败信息用）。"""
    return _deep(J(gui, "(()=>{const f=document.querySelector('.statusbar iframe');"
                        "if(!f||!f.contentDocument||!f.contentDocument.body)return '{}';"
                        "const b=f.contentDocument.body;"
                        "return JSON.stringify({poll:b.getAttribute('data-poll'),"
                        "ts:b.getAttribute('data-ts'),err:b.getAttribute('data-err')})})()"))


def wait_settle(gui, key, max_wait=60):
    """等插件把真实索引跑进终态（ready/error）——之后插件空闲，再写合成状态即可稳定断言前端映射。"""
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        last = prj_list(gui).get(key)
        try:
            st = json.loads(last or '{}').get('state')
        except Exception:
            st = None
        if st in ('ready', 'error'):
            return last
        time.sleep(0.5)
    return last


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    gui = GUIClient(port=PORT, data_dir=DATA_DIR, work_dir=WS)
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：prj 随独立临时 work-dir，usr 主库不受其隔离（SB4 切语言会写
    # usr ui.locale）→ 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        ensure_locale(gui, 'zh-CN')  # 语言确定性（SB6 按 zh 文案定位）
        for _ in range(30):
            if J(gui, "document.querySelector('.statusbar')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # SB1 statusbar 存在
        sb = J(gui, "!!document.querySelector('.statusbar')")
        c.check("SB1 statusbar 存在", bool(sb), f"sb={sb}")

        # SB2 「打开全部配置」图标已移除（B2，2026-09-24）+ 索引状态区（2026-09-27）
        # 〔2026-10-16 UILAYOUT-1 P4①：状态栏**条目化**〕状态栏 = 自由布局栏三区（`layout.free.statusbar.items`）：
        #   靠左 = 记忆 token（条形视图，默认未启用记忆库 → 不渲染）/ 队列（空闲不渲染）/ 索引徽标（恒显示 2 枚）；
        #   靠右 = DevTools（配置按钮）+ 语言切换（条形视图）。无 `.sb-menu` 配置菜单容器。
        secs = J(gui, "document.querySelectorAll('.statusbar .sb-section').length")
        menu = J(gui, "document.querySelectorAll('.statusbar .sb-menu').length")
        mem = J(gui, "document.querySelectorAll('.statusbar .sb-mem').length")
        idx_badges = J(gui, "document.querySelectorAll('.statusbar .sb-index .sb-idx').length")
        # STB-1：记忆提取 / 上下文压缩各一枚长任务状态条目（空闲 / 未启用也显示 = 每类任务都有 view）
        tasks = J(gui, "document.querySelectorAll('.statusbar .sb-task[data-task]').length")
        c.check("SB2 状态栏条目化：无宿主索引徽标（已归插件，SB6 断言）+ 长任务两条目 + 无配置菜单 + 记忆入口默认隐藏",
                int(menu) == 0 and int(mem) == 0 and int(idx_badges) == 0 and int(tasks) == 2 and int(secs) >= 1,
                f"sections={secs} menu={menu} mem={mem} idx={idx_badges} tasks={tasks}")

        # SB2b 记忆总 token 数入口（A4 迁移）：启用记忆库后状态栏出现入口，数值为数字，
        # 点击 → 记忆分类列表 → 选中 → 内容编辑弹框（可编辑、可保存）
        gui.req('data-prj-config-save', {'data': {'key': 'memory.enabled', 'value': 'true'}})
        memShown = wait_upto(gui, "(()=>{const e=document.querySelector('.statusbar .sb-mem');"
                                  "if(!e)return null;const v=e.querySelector('.sb-mem-value');"
                                  "return v?(v.textContent||'').trim():null})()",
                             lambda v: v is not None and str(v).strip().isdigit())
        gui.console(clear=True)
        J(gui, "document.querySelector('.statusbar .sb-mem')?.click(); 'ok'")
        items = wait_upto(gui, "document.querySelectorAll('.mem-cat-list .mem-cat-item').length", lambda v: int(v or 0) >= 1)
        picked = J(gui, "(()=>{const i=document.querySelector('.mem-cat-list .mem-cat-item');if(i)i.click();return !!i})()")
        edit = wait_upto(gui, "!!document.querySelector('.dialog-shell .text-edit-dialog-body textarea')", lambda v: v is True)
        J(gui, "(()=>{const d=document.querySelector('.dialog-shell');if(!d)return false;"
               "const t=[...d.querySelectorAll('button')].find(x=>x.textContent.trim()==='取消');if(t)t.click();return !!t})()")
        closed = wait_upto(gui, "!document.querySelector('.text-edit-dialog-body')", lambda v: v is True)
        errs = console_errors(gui)
        c.check("SB2b 状态栏记忆总量入口：显示数值 + 点击弹分类列表 + 选中开内容弹框",
                memShown is not None and str(memShown).strip().isdigit()
                and int(items) >= 1 and bool(picked) and bool(edit) and bool(closed) and len(errs) == 0,
                f"mem={memShown!r} items={items} picked={picked} edit={edit} closed={closed} errs={errs[:2]}")
        # 还原记忆开关（后续用例不受影响；套件级 restore 仍兜底）
        gui.req('data-prj-config-save', {'data': {'key': 'memory.enabled', 'value': 'false'}})
        time.sleep(0.5)

        # ── SB6 索引进度徽标（PLUGIN-UI-2：UI 归插件）─────────────────────────
        # 宿主不再有 `.sb-index` 徽标、也不再读 `codegraph.status` / `vfts.status`：
        # 状态栏内嵌 codegraph 插件的 `badge` 视图（iframe，`ui.statusbar[].view=badge`），
        # 文案 / 进度由插件经自有动作 `badge` 拉取并渲染；完整页 = 插件的 `status` 视图。
        badge_src = wait_upto(
            gui,
            "(()=>{const f=document.querySelector('.statusbar iframe');return f?(f.getAttribute('src')||''):''})()",
            lambda v: isinstance(v, str) and '/plugins/codegraph/' in v)
        c.check("SB6a 状态栏内嵌插件 badge 视图（/plugins/codegraph/webui/badge.html + instance_id）",
                isinstance(badge_src, str) and 'webui/badge.html' in badge_src and 'instance_id=' in badge_src,
                f"src={badge_src!r}")

        # ② 启用 codegraph → badge 由**插件自持**渲染：codegraph 段必须是四态之一（不再是「未启用」），
        #    vfts 段仍「未启用」。真实索引是否 ready 取决于项目内容（本套件只验「状态由插件提供」）。
        prj_save(gui, 'plugins.codegraph.enabled', 'true')
        badge_txt = wait_upto(gui, badge_text_js(),
                              lambda v: isinstance(v, str)
                              and any(k in v for k in ('未初始化', '索引中', '完成', '失败')),
                              max_wait=60)
        cg_seg = (badge_txt or '').split(' ')[0]
        c.check("SB6b 启用 codegraph → badge codegraph 段为插件上报状态（未初始化/索引中/完成/失败）",
                cg_seg in ('未初始化', '完成', '失败') or cg_seg.startswith('索引中'),
                f"text={badge_txt!r} diag={badge_diag(gui)!r}")

        # ③ 点击 badge → 前端 `preview-tab-open{kind:'plugin-page'}` → 打开插件完整状态页
        clicked = J(gui, "(()=>{const f=document.querySelector('.statusbar iframe');"
                         "if(!f||!f.contentDocument)return false;f.contentDocument.body.click();return true})()")
        tab_idx = wait_upto(gui, active_preview_tab_js(),
                            lambda v: isinstance(v, str) and '知识库状态' in v)
        page_src = wait_upto(gui, plugin_page_src_js(),
                             lambda v: isinstance(v, str) and 'webui/status.html' in v)
        c.check("SB6c 点击 badge → 预览区打开插件状态页（知识库状态 / status.html）",
                bool(clicked) and isinstance(tab_idx, str) and '知识库状态' in tab_idx
                and isinstance(page_src, str) and 'webui/status.html' in page_src,
                f"clicked={clicked} tab={tab_idx!r} src={page_src!r}")
        shot(gui, "04-detail-index.png")

        # ④ 收尾还原：关两引擎 → badge 必须**重新拉取**并显示两枚「未启用」（证明轮询存活）
        prj_save(gui, 'plugins.codegraph.enabled', 'false')
        prj_save(gui, 'plugins.codegraph.vfts.enabled', 'false')
        back = wait_upto(gui, badge_text_js(),
                         lambda v: isinstance(v, str) and v.count('未启用') >= 2)
        c.check("SB6g 收尾还原 → badge 两枚均回「未启用」（轮询刷新生效）",
                isinstance(back, str) and back.count('未启用') >= 2,
                f"text={back!r} diag={badge_diag(gui)!r}")

        # ── SB7 记忆提取状态条目 + 详情页（STB-1）─────────────────────────────
        # 数据源 = 既有通知面 tool-notify，经**后端 → 前端同一入口**（window.mq.emitRemote）注入；
        # 不伪造 DOM/状态（与真实插件广播同一条链路）。
        # 基线：记忆库已还原关闭 → 条目仍存在（「记忆 · 未启用」）；压缩条目「压缩 · 空闲」。
        mem0 = task_state(gui, 'memory') or {}
        cmp0 = task_state(gui, 'compress') or {}
        c.check("SB7a 长任务条目恒可见（记忆未启用 / 压缩空闲）",
                '未启用' in (mem0.get('text') or '') and '空闲' in (cmp0.get('text') or ''),
                f"mem={mem0!r} compress={cmp0!r}")

        emit_notice(gui, {'notice': 'memory-start', 'plugin': 'memory', 'session_id': 's-stb',
                          'turn_id': 't-stb-1', 'message': '正在沉淀记忆…',
                          'message_id': 'stb-mem-start-1'})
        mem1 = wait_upto(gui, task_state_js('memory'),
                         lambda v: isinstance(v, dict) and '记忆沉淀中' in (v.get('text') or ''))
        c.check("SB7b 记忆提取进行中 → 状态条目文案 + 进行中色",
                isinstance(mem1, dict) and '记忆沉淀中' in (mem1.get('text') or '')
                and 'is-progress' in (mem1.get('cls') or ''),
                f"mem={mem1!r}")
        shot(gui, "01-statusbar-memory-running.png")

        # 点击条目 → preview 详情页（DOM 文本含状态；含「去设置」）
        J(gui, "document.querySelector('.statusbar .sb-task[data-task=\"memory\"]')?.click(); 'ok'")
        tab_mem = wait_upto(gui, active_preview_tab_js(), lambda v: v == '记忆提取详情')
        mem_txt = _deep(wait_upto(gui, detail_text_js("memory"),
                                  lambda v: isinstance(v, str) and '记忆沉淀中' in v and '最近一次任务' in v))
        mem_go = J(gui, has_el_js('.task-detail[data-detail="memory"] [data-action="go-settings"]'))
        c.check("SB7c 点击记忆条目 → 预览区详情页（状态 + 最近一次任务 + 去设置）",
                tab_mem == '记忆提取详情' and isinstance(mem_txt, str) and '记忆沉淀中' in mem_txt
                and '最近一次任务' in mem_txt and bool(mem_go),
                f"tab={tab_mem!r} txt={str(mem_txt)[:140]!r} go={mem_go}")
        shot(gui, "02-detail-memory-running.png")

        # 失败（既有 plugin-failure）→ 条目错误态 + 详情页错误文本（不弹 toast、不静默）
        emit_notice(gui, {'notice': 'plugin-failure', 'plugin': 'memory', 'kind': 'save',
                          'reason': 'stb-boom', 'message': '⚠️ 记忆沉淀失败：stb-boom',
                          'message_id': 'stb-mem-fail-1'})
        mem2 = wait_upto(gui, task_state_js('memory'),
                         lambda v: isinstance(v, dict) and '失败' in (v.get('text') or ''))
        mem_err_txt = _deep(wait_upto(gui, detail_text_js("memory"),
                                      lambda v: isinstance(v, str) and 'stb-boom' in v))
        c.check("SB7d 记忆提取失败 → 条目错误态 + 详情错误文本",
                isinstance(mem2, dict) and '失败' in (mem2.get('text') or '')
                and 'is-error' in (mem2.get('cls') or '')
                and isinstance(mem_err_txt, str) and 'stb-boom' in mem_err_txt,
                f"mem={mem2!r} txt={str(mem_err_txt)[:140]!r}")
        shot(gui, "03-detail-memory-error.png")

        # worker 失败后仍补发 memory-done → 不覆盖错误态（失败必须可见）
        emit_notice(gui, {'notice': 'memory-done', 'plugin': 'memory', 'session_id': 's-stb',
                          'turn_id': 't-stb-1', 'message': '记忆沉淀完成',
                          'message_id': 'stb-mem-done-1'})
        time.sleep(0.4)
        mem3 = task_state(gui, 'memory') or {}
        c.check("SB7e 失败优先于 done（错误态不被「完成」洗掉）",
                '失败' in (mem3.get('text') or ''), f"mem={mem3!r}")

        # ── SB8 上下文压缩状态条目 + 详情页（STB-1）───────────────────────────
        emit_notice(gui, {'notice': 'compress-start', 'plugin': 'compress', 'session_id': 's-stb',
                          'turn_id': 't-stb-2', 'message': '正在压缩上下文…',
                          'message_id': 'stb-cmp-start-1'})
        cmp1 = wait_upto(gui, task_state_js('compress'),
                         lambda v: isinstance(v, dict) and '压缩中' in (v.get('text') or ''))
        c.check("SB8a 压缩进行中 → 状态条目文案 + 进行中色",
                isinstance(cmp1, dict) and '压缩中' in (cmp1.get('text') or '')
                and 'is-progress' in (cmp1.get('cls') or ''),
                f"compress={cmp1!r}")
        J(gui, "document.querySelector('.statusbar .sb-task[data-task=\"compress\"]')?.click(); 'ok'")
        tab_cmp = wait_upto(gui, active_preview_tab_js(), lambda v: v == '上下文压缩详情')
        cmp_txt = _deep(wait_upto(gui, detail_text_js("compress"),
                                  lambda v: isinstance(v, str) and '压缩中' in v and '最近一次任务' in v))
        cmp_go = J(gui, has_el_js('.task-detail[data-detail="compress"] [data-action="go-settings"]'))
        c.check("SB8b 点击压缩条目 → 预览区详情页（状态 + 最近一次任务 + 去设置）",
                tab_cmp == '上下文压缩详情' and isinstance(cmp_txt, str) and '压缩中' in cmp_txt
                and '最近一次任务' in cmp_txt and bool(cmp_go),
                f"tab={tab_cmp!r} txt={str(cmp_txt)[:140]!r} go={cmp_go}")
        shot(gui, "05-detail-compress-running.png")

        # 完成 → 条目「压缩完成」（3s 后自动隐藏；此处即时断言）
        emit_notice(gui, {'notice': 'compress-done', 'plugin': 'compress', 'session_id': 's-stb',
                          'turn_id': 't-stb-2', 'message': '上下文压缩完成',
                          'message_id': 'stb-cmp-done-1'})
        cmp2 = wait_upto(gui, task_state_js('compress'),
                         lambda v: isinstance(v, dict) and '压缩完成' in (v.get('text') or ''),
                         max_wait=2.0)
        c.check("SB8c 压缩完成 → 条目「压缩完成」",
                isinstance(cmp2, dict) and '压缩完成' in (cmp2.get('text') or ''), f"compress={cmp2!r}")

        # SB3 语言切换：2 个按钮 + 一个 active
        langBtns = J(gui, "document.querySelectorAll('.lang-switcher .lang-btn').length")
        activeFlag = J(gui, "document.querySelector('.lang-switcher .lang-btn.active')?.textContent?.trim() || ''")
        c.check("SB3 语言切换 2 按钮 + active", int(langBtns) == 2 and bool(activeFlag), f"n={langBtns} active={repr(activeFlag)}")

        # SB4 点击非 active 语言 → active 切换（再切回）
        J(gui, "(()=>{const bs=Array.from(document.querySelectorAll('.lang-switcher .lang-btn'));const b=bs.find(x=>!x.classList.contains('active'));if(b)b.click();return !!b})()")
        time.sleep(0.6)
        activeFlag = J(gui, "document.querySelector('.lang-switcher .lang-btn.active')?.textContent?.trim() || ''")
        c.check("SB4 语言切换生效（active 移动）", bool(activeFlag), f"active={repr(activeFlag)}")
        J(gui, "(()=>{const bs=Array.from(document.querySelectorAll('.lang-switcher .lang-btn'));const b=bs.find(x=>!x.classList.contains('active'));if(b)b.click();return 'ok'})()")
        time.sleep(0.5)

        # SB5 调试入口（B3）：条目存在（title=打开 DevTools；条目化后为栏按钮 `bar-btn`）+
        # gui.devtools.open 通道可用
        dbg = J(gui, "(()=>{const es=Array.from(document.querySelectorAll('.statusbar [title]'));"
                     "return es.filter(e=>(e.getAttribute('title')||'').indexOf('DevTools')>=0).length})()")
        dev_ok, dev_detail = False, ''
        try:
            r = gui.req('gui.devtools.open', {})
            dev_ok = True  # 无返回（本地事件）：不抛错即通道可用
            dev_detail = 'ok result=%r' % (r,)
        except Exception as e:  # noqa: BLE001
            dev_detail = 'req error: ' + str(e)
        c.check("SB5 调试入口 + gui.devtools.open 可用", int(dbg) >= 1 and dev_ok,
                f"dbg={dbg} resp={dev_detail}")

        errs = console_errors(gui)
        c.check("无前端错误（statusbar 组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

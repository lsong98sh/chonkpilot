"""回归测试：statusbar 组（51-FP与测试映射「statusbar」）。

对齐 2026-09-04 /call 清理：codebase 索引进度 / DB 查看器 / 外部工具图标 /
知识库占位入口 UI 已删除（T5，61-消息一览 §8），对应断言移除；
保留 statusbar 存在性、语言切换（LangSwitcher）。

2026-09-24（B2 移除 + A4 迁移）：上一批把配置入口改成展开「全部配置」菜单，本轮按用户口径
**整体移除该图标/菜单**（不再提供状态栏配置入口，配置入口仍在工具栏「设置」下拉）→
SB2 断言入口已移除；A4「记忆总 token 数」入口由上下文管理页迁入状态栏底部 →
SB2b 断言启用记忆库后入口出现、数值为数字且点击可弹出记忆分类列表 → 选中 → 内容编辑弹框（可编辑）。
新增调试图标（SB5：gui.devtools.open 可用；用户 F12 入口已在宿主层屏蔽）。

2026-09-27（索引 / codebase 状态区）：状态栏新增 `codegraph` / `vfts` 两枚徽标（恒显示），
数据源 = 既有 prj 键（`enable-codegraph` / `codegraph.status` / `enable-vfts` / `vfts.status`），
刷新 = 既有 `data-prj-config-refresh` 广播，点击走既有 `project-config-open`（**零新增消息面**）
→ SB2 计入该区；SB6 覆盖「未启用 / 索引中 N/total / 完成 / 失败 + 原生 title + 点击开项目配置并定位页签」。
徽标 = [图标] + 状态文本（无 Tooltip；悬停原生 `title`），点击带可选 `tab` 定位到项目配置对应页签。
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
    # 套件级配置快照-还原（51 §6-8）：prj 走独立 --data-dir，usr 主库不受其隔离（SB4 切语言会写
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
        # → 状态栏 = 索引状态区 + DevTools 两项，不存在上一批的配置菜单容器（.sb-menu）
        secs = J(gui, "document.querySelectorAll('.statusbar .sb-section').length")
        menu = J(gui, "document.querySelectorAll('.statusbar .sb-menu').length")
        mem = J(gui, "document.querySelectorAll('.statusbar .sb-mem').length")
        idx_badges = J(gui, "document.querySelectorAll('.statusbar .sb-index .sb-idx').length")
        c.check("SB2 配置入口图标已移除（索引状态区 + DevTools 两项 + 无菜单）",
                int(secs) == 2 and int(menu) == 0 and int(mem) == 0 and int(idx_badges) == 2,
                f"sections={secs} menu={menu} mem={mem} idx={idx_badges}")

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

        # ── SB6 索引 / codebase 状态区（2026-09-27）─────────────────────────────
        # 数据源 = 既有 prj 键 + 既有 data-prj-config-refresh 广播（零新增消息面）。
        # ① 基线（独立 data-dir，无键）→ 两枚徽标均「未启用」
        base = idx_states(gui)
        c.check("SB6a 基线（无键）→ codegraph / vfts 均「未启用」",
                set(base.keys()) == {'codegraph', 'vfts'}
                and all(v.get('text') == '未启用' and 'is-disabled' in (v.get('cls') or '')
                        for v in base.values()),
                f"base={base!r}")

        # ② enable-codegraph=true → 等插件真实索引进终态（ready/error）→ 再写合成状态
        #    （插件只对 enable-* / 索引配置键作出反应，对 codegraph.status 的写入不响应 →
        #     插件空闲后写合成状态可稳定断言前端映射：state→文案/N-total）
        prj_save(gui, 'enable-codegraph', 'true')
        settle_cg = wait_settle(gui, 'codegraph.status')
        prj_save(gui, 'codegraph.status', json.dumps(
            {'state': 'indexing', 'phase': 'index', 'progressDone': 3, 'progressTotal': 8}))
        cg_idx = wait_upto(gui, "(()=>{const e=document.querySelector('.sb-index .sb-idx[data-engine=\"codegraph\"]');"
                                "return e?((e.querySelector('.sb-idx-state')||{}).textContent||'').trim():''})()",
                           lambda v: isinstance(v, str) and '3/8' in v)
        c.check("SB6b codegraph 索引中：文案「索引中」+ 进度 3/8",
                isinstance(cg_idx, str) and '索引中' in cg_idx and '3/8' in cg_idx,
                f"text={cg_idx!r} settle={settle_cg!r}")

        # ③ state=ready → 「完成」；悬停为原生 title（「点击设置：codegraph」）
        prj_save(gui, 'codegraph.status', json.dumps(
            {'state': 'ready', 'progressDone': 8, 'progressTotal': 8, 'indexedFiles': 2, 'indexedSymbols': 5}))
        cg_ready = wait_upto(gui, "(()=>{const e=document.querySelector('.sb-index .sb-idx[data-engine=\"codegraph\"]');"
                                  "return e?((e.querySelector('.sb-idx-state')||{}).textContent||'').trim():''})()",
                             lambda v: v == '完成')
        cg_title = J(gui, "(()=>{const e=document.querySelector('.sb-index .sb-idx[data-engine=\"codegraph\"]');"
                          "return e?(e.getAttribute('title')||''):''})()")
        c.check("SB6c codegraph 完成：「完成」+ 原生 title（点击设置：codegraph）",
                cg_ready == '完成' and isinstance(cg_title, str)
                and '点击设置' in cg_title and 'codegraph' in cg_title,
                f"text={cg_ready!r} title={cg_title!r}")

        # ④ state=error → 「失败」
        prj_save(gui, 'codegraph.status', json.dumps({'state': 'error', 'message': 'kaboom'}))
        cg_err = wait_upto(gui, "(()=>{const e=document.querySelector('.sb-index .sb-idx[data-engine=\"codegraph\"]');"
                                "return e?((e.querySelector('.sb-idx-state')||{}).textContent||'').trim():''})()",
                           lambda v: v == '失败')
        c.check("SB6d codegraph 失败：「失败」", cg_err == '失败', f"text={cg_err!r}")

        # ⑤ enable-vfts 同理覆盖一条（索引中 N/total）
        prj_save(gui, 'enable-vfts', 'true')
        settle_vf = wait_settle(gui, 'vfts.status')
        prj_save(gui, 'vfts.status', json.dumps(
            {'state': 'indexing', 'progressDone': 2, 'progressTotal': 4}))
        vf_idx = wait_upto(gui, "(()=>{const e=document.querySelector('.sb-index .sb-idx[data-engine=\"vfts\"]');"
                                "return e?((e.querySelector('.sb-idx-state')||{}).textContent||'').trim():''})()",
                           lambda v: isinstance(v, str) and '2/4' in v)
        c.check("SB6e vfts 索引中：「索引中 2/4」",
                isinstance(vf_idx, str) and '索引中' in vf_idx and '2/4' in vf_idx,
                f"text={vf_idx!r} settle={settle_vf!r}")

        # ⑥ 点击徽标 → 既有 project-config-open（打开项目配置页并定位到对应页签）
        J(gui, "document.querySelector('.sb-index .sb-idx[data-engine=\"codegraph\"]')?.click(); 'ok'")
        opened = wait_upto(gui, "document.querySelectorAll('.project-config-panel').length", lambda v: int(v or 0) >= 1)
        active_cg = wait_upto(gui, "(()=>{const e=document.querySelector('.project-config-panel .b-tabs-item.active');"
                                   "return e?(e.textContent||'').trim():''})()",
                              lambda v: v == 'CodeGraph 索引')
        c.check("SB6f 点击 codegraph 徽标 → 打开项目配置且 CodeGraph 页签 active",
                int(opened or 0) >= 1 and active_cg == 'CodeGraph 索引',
                f"panels={opened} active={active_cg!r}")
        # 已开页签再点 vfts 徽标 → 页内切到 Vfts 页签（nonce 递增路径）
        J(gui, "document.querySelector('.sb-index .sb-idx[data-engine=\"vfts\"]')?.click(); 'ok'")
        active_vf = wait_upto(gui, "(()=>{const e=document.querySelector('.project-config-panel .b-tabs-item.active');"
                                   "return e?(e.textContent||'').trim():''})()",
                              lambda v: v == 'Vfts 全文索引')
        c.check("SB6f2 点击 vfts 徽标 → 项目配置页切到 Vfts 页签",
                active_vf == 'Vfts 全文索引', f"active={active_vf!r}")

        # ⑥ 收尾还原键值（还原后两徽标回「未启用」）
        prj_del(gui, 'codegraph.status')
        prj_del(gui, 'vfts.status')
        prj_save(gui, 'enable-codegraph', 'false')
        prj_save(gui, 'enable-vfts', 'false')
        back = _deep(wait_upto(gui, "JSON.stringify([...document.querySelectorAll('.sb-index .sb-idx .sb-idx-state')]"
                                     ".map(e=>(e.textContent||'').trim()))",
                                lambda v: _deep(v) == ['未启用', '未启用']))
        c.check("SB6g 收尾还原 → 两徽标回「未启用」", back == ['未启用', '未启用'], f"back={back!r}")

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

        # SB5 调试入口（B3）：图标存在（title=打开 DevTools）+ gui.devtools.open 通道可用
        # （用户 F12/右键入口已在宿主层屏蔽 → 该消息面是唯一入口；61 §1 该主题**无返回**：
        #  信封成功即通道可用，宿主不支持时会返回 errors → req 抛错）
        dbg = J(gui, "(()=>{const es=Array.from(document.querySelectorAll('.statusbar .sb-section'));"
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

"""回归测试：statusbar 组（51-FP与测试映射「statusbar」）。

对齐 2026-09-04 /call 清理：codebase 索引进度 / DB 查看器 / 外部工具图标 /
知识库占位入口 UI 已删除（T5，61-消息一览 §8），对应断言移除；
保留 statusbar 存在性、语言切换（LangSwitcher）。

2026-09-24（B2 移除 + A4 迁移）：上一批把配置入口改成展开「全部配置」菜单，本轮按用户口径
**整体移除该图标/菜单**（不再提供状态栏配置入口，配置入口仍在工具栏「设置」下拉）→
SB2 断言入口已移除（statusbar 仅剩 DevTools 一项 + 无菜单容器）；A4「记忆总 token 数」入口
由上下文管理页迁入状态栏底部 → SB2b 断言启用记忆库后入口出现、数值为数字且点击可弹出
记忆分类列表 → 选中 → 内容编辑弹框（可编辑）。
新增调试图标（SB5：gui.devtools.open 可用；用户 F12 入口已在宿主层屏蔽）。
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
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_sb_data")


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


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    gui = GUIClient(port=PORT, data_dir=DATA_DIR)
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：prj 走独立 --data-dir，usr 主库不受其隔离（SB4 切语言会写
    # usr ui.locale）→ 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        for _ in range(30):
            if J(gui, "document.querySelector('.statusbar')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # SB1 statusbar 存在
        sb = J(gui, "!!document.querySelector('.statusbar')")
        c.check("SB1 statusbar 存在", bool(sb), f"sb={sb}")

        # SB2 「打开全部配置」图标已移除（B2，2026-09-24）：状态栏只剩 DevTools 一项，
        # 且不存在上一批的配置菜单容器（.sb-menu）
        secs = J(gui, "document.querySelectorAll('.statusbar .sb-section').length")
        menu = J(gui, "document.querySelectorAll('.statusbar .sb-menu').length")
        mem = J(gui, "document.querySelectorAll('.statusbar .sb-mem').length")
        c.check("SB2 配置入口图标已移除（仅 DevTools 一项 + 无菜单）",
                int(secs) == 1 and int(menu) == 0 and int(mem) == 0,
                f"sections={secs} menu={menu} mem={mem}")

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

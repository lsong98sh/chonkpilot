# -*- coding: utf-8 -*-
"""L4 UI 缺陷回归（用户报障项）+ 对话框/页签几何断言。

覆盖（用户 2026-09-13 报障）：
  R1 知识库选 `.md`（*.tool.md 原语）→ preview PrimitivePanel 解析并渲染出字段（描述/参数，不再 loading）
  R2 文件树 rename 编辑态中点击其它节点 → 退出编辑态（内联输入框消失，未误改名）
  R3 preview 底部页签溢出：`...` 弹框**不越界**且**贴合触发按钮**（位置）
  R4 项目设置「自动提交」页签**文案**（不再叫"文件历史"）
  R5 MCP / LLM 添加对话框**不越界**且表单**一行两列**（位置）

复用 systest/chonk_client.py 通道与 run_explore_kb.KB / run_config_ui 辅助。
前置：dist-desktop\\chonkpilot.exe --test-port=2345 --work-dir=<ws>。
运行：python run_ui_regressions.py
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import run_explore_kb as kbmod
import run_config_ui as rc

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345, work_dir=os.path.join(os.path.dirname(os.path.abspath(__file__)), "ws"))
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：R2/R3/R4 隐式落 prj → 退出前自动回滚
_h.ensure_locale(c)  # 语言确定性：R4 页签文案按 zh-CN 断言（DB ui.locale 可能被他套件写成 en-US）
kb = kbmod.KB(c)

HERE = os.path.dirname(os.path.abspath(__file__))
WS = os.path.join(HERE, "ws")
TMPDIR = os.path.join(WS, "_ui_reg")


def _loads(v):
    for _ in range(4):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def _js(expr):
    return _loads(c.eval(expr))


# ── R1 知识库 .md 原语预览 ──────────────────────────────────

def _kb_dir_visible(name):
    return any(kb.row_label(r) == name and r["d"] for r in kb.kb_rows())


def _kb_ensure(child, click_name):
    """幂等展开：child 已可见则不点击（避免把已展开目录收起）。"""
    if _kb_dir_visible(child):
        return
    kb.click_kb_dir(click_name)
    assert kb.poll(lambda: _kb_dir_visible(child)), "展开 %s 失败（child=%s）" % (click_name, child)


def case_r1_knowledge_md_preview():
    assert kb.ensure_explorer_visible(), "filetree 区未能挂载"
    assert kb.switch_mode("knowledge"), "切换知识库失败 active=%s" % kb.mode_active()
    time.sleep(0.8)
    # 系统（app）级 capability 根（幂等展开，避免把已展开状态收起）
    _kb_ensure("tools", "知识库")
    _kb_ensure("core", "tools")
    # core 目录需展开才加载其子文件
    if not any(kb.row_label(r).endswith(".tool.md") for r in kb.kb_rows()):
        kb.click_kb_dir("core")
    assert kb.poll(lambda: any(kb.row_label(r).endswith(".tool.md") for r in kb.kb_rows())), \
        "core 下无 *.tool.md 行"
    # 选一个 .md 原语 → preview
    assert kb.click_kb_file("file_find.tool.md"), "单击 file_find.tool.md 失败"
    assert kb.poll(lambda: kb.prim_panel_exists()), "PrimitivePanel 未打开"
    path = kb.prim_active_path()
    if not path.replace("\\", "/").endswith("file_find.tool.md"):
        raise TestError("活动原语面板路径异常：%r" % path)
    # 内容渲染（P3-C2 新口径，2026-09-24）：原「源码」页签已移除（子页签改为 meta·描述·参数·正文
    # 四页签，见 PrimitivePanel.vue / run_explore_kb.py:295-296）→ 原「源码渲染出内容（不再 loading）」
    # 校准为「**.md 原语被解析并渲染出字段**」：描述页签正文非空（读到 [description]）+
    # 参数页签渲染出 JSON Schema 树（读到 [parameters]）。两者皆空 = 未读取/加载失败。
    assert kb.prim_click_tab(["描述", "Description"]), "切换描述页签失败"
    assert kb.poll(lambda: (kb.prim_edit_info() or {}).get("ok")), "描述页签未就绪"
    desc = kb.prim_get_ta(0)
    if not desc or len(desc.strip()) < 20:
        raise TestError("描述页签未渲染出内容（疑似未读取/加载失败）：%r" % (desc or "")[:60])
    assert kb.prim_click_tab(["参数", "Parameters"]), "切换参数页签失败"
    if not kb.poll(lambda: kb.prim_schema_rows() >= 1):
        raise TestError("参数页签未渲染 JSON Schema 树（[parameters] 未解析）")
    # 读取失败会显式报错（.pf-error）——不应出现
    err = kb.prim_scope("const e=P.querySelector('.pf-error');return e?e.textContent.trim():''")
    if err:
        raise TestError("原语面板加载报错：%r" % err)


# ── R2 文件树 rename 退出编辑态 ────────────────────────────

def _ft_inline():
    """项目树内联改名输入框的值（None = 不在编辑态）。"""
    return _js("(function(){const e=document.querySelector('.filetree-panel .inline-edit-input');"
               "return e?e.value:null;})()")


def _ft_edit_active(label):
    """指定文件行是否正处内联改名态（该行渲染出 value==label 的输入框）。"""
    return bool(_js("(function(){return [...document.querySelectorAll('.filetree-panel .tree-row')]"
                    ".some(r=>{const i=r.querySelector('.inline-edit-input');return i&&i.value===%s;});})()"
                    % json.dumps(label)))


def _ft_label_present(label):
    """指定文件行是否已恢复正常 label（非编辑态）。"""
    return bool(_js("(function(){return [...document.querySelectorAll('.filetree-panel .tree-row')]"
                    ".some(r=>{const l=r.querySelector('.node-label');return l&&l.textContent.trim()===%s;});})()"
                    % json.dumps(label)))


def _ft_cancel_edit():
    _js("(function(){const e=document.querySelector('.filetree-panel .inline-edit-input');"
        "if(e)e.dispatchEvent(new KeyboardEvent('keyup',{key:'Escape',code:'Escape',bubbles:true}));})()")


def case_r2_rename_exit_on_click():
    assert kb.ensure_explorer_visible(), "filetree 区未能挂载"
    assert kb.switch_mode("project"), "切换项目树失败 active=%s" % kb.mode_active()
    time.sleep(0.8)
    _ft_cancel_edit()   # 清理上一轮可能残留的内联编辑态（避免行 label 被输入框顶替）
    time.sleep(0.5)
    assert kb.wait_ft_row("a.txt", False), "项目树未出现 a.txt"
    a_path = os.path.join(WS, "a.txt")
    assert os.path.exists(a_path), "a.txt 磁盘不存在（前置）"
    # 经右键菜单「重命名」进入编辑态（限定项目树，避免与知识库树的 F2 监听串扰）
    assert kb.rclick_ft_row("a.txt", False), "右键 a.txt 失败"
    assert kb.poll(lambda: len(kb.ft_menu_texts()) > 0), "项目树右键菜单未弹出"
    hit = next((x for x in kb.ft_menu_texts() if x in ("重命名", "Rename")), None)
    assert hit, "项目树菜单无 重命名：%r" % kb.ft_menu_texts()
    assert kb.ft_click_menu(hit), "点击 重命名 失败"
    assert kb.poll(lambda: _ft_edit_active("a.txt")), "未进入 a.txt 内联改名态"
    # 编辑态中点击其它节点（目录 `a`：目录点击不会触发「再次单击→改名」重入）→ a.txt 退出编辑态
    assert kb.click_ft_dir("a"), "单击目录 a 失败"
    assert kb.poll(lambda: _ft_label_present("a.txt")), "点击其它节点后 a.txt 仍在编辑态（未退出改名）"
    _ft_cancel_edit()
    time.sleep(0.4)
    assert kb.poll(lambda: not _ft_edit_active("a.txt")), "a.txt 仍处编辑态"
    # 未误改名
    assert os.path.exists(a_path), "a.txt 被误改名（应保持原名）"


# ── R3 页签溢出弹框几何 ────────────────────────────────────
# 溢出机制（2026-09-16 新口径）：多余页签**不隐藏**（无 `.tb-hidden`）；溢出只体现为
# 「最后一个页签越过外层可用宽」+ `.tb-more` 按钮**绝对定位覆盖**在右侧；弹框列出**全部**页签。

def case_r3_tabs_overflow_popover():
    os.makedirs(TMPDIR, exist_ok=True)
    files = []
    for i in range(1, 15):
        p = os.path.join(TMPDIR, "ui_regression_tab_%02d.txt" % i)
        with open(p, "w", encoding="utf-8") as f:
            f.write("tab %d content\n" % i)
        files.append(p)
    try:
        for p in files:
            c.mq_emit("file-open", {"path": p, "temporary": False})
            time.sleep(0.12)
        time.sleep(1.2)
        n = _js("document.querySelectorAll('.tb-bar.tb-bottom .tb-tab').length") or 0
        if n < 14:
            raise TestError("应打开 14 个 preview 页签，实际 %d" % n)
        if not rc.wait_vis(".tb-bar.tb-bottom .tb-more", 6):
            raise TestError("页签溢出未出现 ... 触发按钮")
        # 按钮为绝对定位覆盖（不占 flex 位）；全部页签均**未隐藏**（无 .tb-hidden / 不可见项）
        st = rc.ev("""(() => {
          const bar = document.querySelector('.tb-bar.tb-bottom');
          const tabs = [...bar.querySelectorAll('.tb-tab')];
          const btn = bar.querySelector('.tb-more');
          const r = btn ? btn.getBoundingClientRect() : null;
          const br = bar.getBoundingClientRect();
          const lr = tabs.length ? tabs[tabs.length - 1].getBoundingClientRect().right : 0;
          return {
            btnPos: btn ? getComputedStyle(btn).position : '',
            total: tabs.length,
            hidden: bar.querySelectorAll('.tb-tab.tb-hidden').length,
            hiddenElse: tabs.filter(t => {
              const cs = getComputedStyle(t);
              return cs.display === 'none' || cs.visibility === 'hidden' || t.getBoundingClientRect().width <= 0;
            }).length,
            lastRight: lr,
            barRight: br.right,
            btnLeft: r ? r.left : 0,
            btnRight: r ? r.right : 0,
          };
        })()""") or {}
        if st.get("btnPos") != "absolute":
            raise TestError("溢出按钮应为绝对定位覆盖：%r" % (st,))
        if st.get("total") != n:
            raise TestError("页签数不符（不应有页签被移除）：%r vs %d" % (st, n))
        if st.get("hidden") or st.get("hiddenElse"):
            raise TestError("多余页签不应被隐藏/不可见：%r" % (st,))
        if st.get("lastRight", 0) <= st.get("barRight", 0) - 8:
            raise TestError("溢出场景最后页签应越界（被按钮遮盖）：%r" % (st,))
        if st.get("btnLeft", 0) >= st.get("lastRight", 0):
            raise TestError("按钮应覆盖住最后页签的一部分：%r" % (st,))
        # 点击 → 弹框（列出**全部**页签）
        rc.ev("(function(){const b=document.querySelector('.tb-bar.tb-bottom .tb-more');if(b)b.click();return 'ok';})()")
        if not rc.wait_vis(".tb-more-pop", 5):
            raise TestError("点击 ... 未弹出页签列表")
        pop = rc.rect(".tb-more-pop")
        btn = rc.rect(".tb-bar.tb-bottom .tb-more")
        v = rc.vp()
        if not pop or not btn:
            raise TestError("弹框/按钮 rect 不可得 pop=%r btn=%r" % (pop, btn))
        # 不越界
        if pop["l"] < -1 or pop["t"] < -1 or pop["r"] > v["w"] + 1 or pop["b"] > v["h"] + 1:
            raise TestError("溢出弹框越界：pop=%r vp=%r" % (pop, v))
        # 贴合触发按钮（右对齐，误差 ≤10px；极端贴边允许夹到视口内）
        if abs(pop["r"] - btn["r"]) > 10 and pop["r"] < v["w"] - 12:
            raise TestError("溢出弹框未贴合触发按钮：pop.r=%.1f btn.r=%.1f" % (pop["r"], btn["r"]))
        items = rc.ev("document.querySelectorAll('.tb-more-pop .tb-more-item').length") or 0
        if items != n:
            raise TestError("溢出弹框应列出全部页签（%d），实际 %d" % (n, items))
        # 弹框选中 → 该项成为**第一个 .tb-tab**，其余保持相对顺序（只改内部显示顺序）
        names = rc.ev("[...document.querySelectorAll('.tb-bar.tb-bottom .tb-tab .tb-name')].map(n=>n.textContent.trim())") or []
        pick = 5  # 第 6 项
        rc.ev("(function(){const it=document.querySelectorAll('.tb-more-pop .tb-more-item')[%d];if(it)it.click();return 'ok';})()" % pick)
        time.sleep(0.7)
        after = rc.ev("[...document.querySelectorAll('.tb-bar.tb-bottom .tb-tab .tb-name')].map(n=>n.textContent.trim())") or []
        want = [names[pick]] + names[:pick] + names[pick + 1:]
        if after != want:
            raise TestError("弹框选中应置首、其余相对顺序顺移：期望=%r 实际=%r" % (want, after))
        if int(rc.ev("document.querySelectorAll('.tb-more-pop').length") or 0):
            raise TestError("弹框选中后应关闭")
    finally:
        c.mq_emit("preview-tab-close-all")
        time.sleep(0.6)
        shutil.rmtree(TMPDIR, ignore_errors=True)


# ── R4 「自动提交」页签文案 ────────────────────────────────

def case_r4_auto_commit_label():
    rc.open_page("settings-project", ".project-config-panel")
    labels = rc.tab_labels(".project-config-panel")
    hist = [x for x in labels if "历史" in x or "提交" in x]
    if not hist:
        raise TestError("项目设置未找到历史/自动提交页签：%r" % labels)
    got = hist[0]
    if got != "自动提交":
        raise TestError("页签文案=%r，期望「自动提交」（不再叫「文件历史」）" % got)


# ── R5 MCP/LLM 对话框几何 ──────────────────────────────────

def _assert_dialog_grid(open_kind, emit_add, cancel_evt, title):
    rc.open_page(open_kind, ".settings-page")
    c.mq_emit(emit_add)
    if not rc.wait_vis(".dialog-shell", 8):
        raise TestError("%s 弹窗未打开" % title)
    time.sleep(0.6)
    rc.assert_in_viewport(".dialog-shell", "%s 弹窗" % title)
    rows = rc.ev("(function(){const R=[...document.querySelectorAll('.dialog-shell')].find(e=>e.getBoundingClientRect().width>0);"
                 "return [...R.querySelectorAll('.form-item-12')].filter(e=>e.getBoundingClientRect().width>0)"
                 ".map(e=>{const r=e.getBoundingClientRect();return {t:Math.round(r.top),l:Math.round(r.left),w:Math.round(r.width)};});})()")
    if not rows or len(rows) < 2:
        raise TestError("%s 弹窗无 form-item-12 字段" % title)
    # 一行两列：按 top 分组，每组 ≤2 且同组两列左右并排、宽度近似相等
    groups = {}
    for r in rows:
        groups.setdefault(r["t"], []).append(r)
    for top, g in groups.items():
        if len(g) > 2:
            raise TestError("%s 弹窗一行超过两列（top=%s 有 %d 项）：%r" % (title, top, len(g), g))
        if len(g) == 2:
            g.sort(key=lambda x: x["l"])
            if g[1]["l"] <= g[0]["l"]:
                raise TestError("%s 弹窗两列未左右并排：%r" % (title, g))
            if abs(g[0]["w"] - g[1]["w"]) > 4:
                raise TestError("%s 弹窗两列宽度不一致：%r" % (title, g))
    c.mq_emit(cancel_evt)
    time.sleep(0.4)


def case_r5_dialog_geometry():
    _assert_dialog_grid("settings-llm", "config-add-llm", "edit-llm-cancel", "LLM")
    _assert_dialog_grid("settings-mcp", "config-add-mcp", "edit-mcp-cancel", "MCP")


def main():
    ok = 0
    total = 0
    c.wait_ready(60)
    c.console(clear=True)
    # 套件级 prj 快照-还原（51 §6-8）：R2/R3 会经前端隐式落 prj 状态
    # （file-open → `opened-files`，preview-tab-close-all → `opened-files`=[]，文件树选中/展开）
    # → 整体快照、结束（含异常）整体回滚到跑前状态。
    with _h.prj_config_guard(c, None):
        for name, fn in [
            ("R1 知识库选 .md（原语）→ preview 解析渲染出内容（不再 loading）", case_r1_knowledge_md_preview),
            ("R2 文件树 rename 中点击其它节点退出编辑态", case_r2_rename_exit_on_click),
            ("R3 页签溢出弹框不越界且贴合触发按钮", case_r3_tabs_overflow_popover),
            ("R4「自动提交」页签文案（不再叫「文件历史」）", case_r4_auto_commit_label),
            ("R5 MCP/LLM 对话框不越界且一行两列", case_r5_dialog_geometry),
        ]:
            total += 1
            ok += run_case(name, fn)
    print("\nUI 缺陷回归：%d/%d 通过" % (ok, total))
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

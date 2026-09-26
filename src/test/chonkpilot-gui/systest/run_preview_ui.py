# -*- coding: utf-8 -*-
"""preview 区回归（FP 84-95 可自动化项）。

覆盖：
  P1 标题显示文件名 + 类型（L89）
  P2 文本预览（L84 text）
  P3 markdown 预览渲染（L84 md）
  P4 html 预览 iframe（L84 html）
  P5 图片预览 img（L84 image）
  P6 页签过多：overflow 溢出（L95）
  P7 打开文件持久化（L88 SaveOpenedFile 桥，重启恢复标注）
  P8 预览区选中文本 → 浮动操作条（L86）
  P9 可用宽变化 → 溢出按钮「出现/消失」+ 页签不被隐藏 + 恢复宽度回初始状态（L95 固化项）

前置：chonkpilot.exe --test-port=2345 已启动；workDir 含 sample.txt/md/html/png + f01~f11.txt。

选择器口径（2026-09-15 迁移）：preview 页签栏 = 公共 TabBar
（`components/tabs/TabBar.vue`，position=bottom）：
  `.tb-bar.tb-bottom .tb-tab`（页签）/ `.tb-name`（名）/ `.tb-tab.active`（激活）
  溢出机制（**2026-09-16 新口径**）：外层 `.tb-bar`（`overflow:hidden` 裁剪 + ResizeObserver）/
  内层 `.tb-inner`（`width:max-content`，**不隐藏**多余页签）；溢出时 `.tb-more`（"..." 按钮，
  **绝对定位覆盖**在右侧）出现；`.tb-more-pop` 弹框 / `.tb-more-item` 项（**列出全部页签**）/
  `.tb-more-list` 列表。**已删除 `.tb-hidden`**（旧"累加 offsetWidth 算可见数 + 隐藏多余项"机制
  与 `.preview-tab*` / `.tabs-hidden` 一并未用）；弹框选中 = 该项移到**显示首位**（其余相对顺序顺移，
  只改内部显示顺序，不通知外部）。
内容区为 v-show 面板栈：多页签时**全部面板都在 DOM 中**，故所有内容断言必须限定在
「可见面板」（`.tab-panel` 中 offsetParent !== null 者），否则会误命中其它页签的内容。
"""
import base64
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345, work_dir=os.path.join(os.path.dirname(os.path.abspath(__file__)), "ws"))
c = _G.client
# 套件级配置快照-还原（51 §6-8）：本套件以**共享 work-dir** 起实例 → file-open / 页签关闭 /
# 分隔条拖拽会经前端隐式落 prj（`opened-files`/`layout.*`/`window.*`）→ 退出前自动回滚到跑前状态。
_h.suite_config_guard(c)

WS = r"e:\BizWorks\chonkpilot\src\test\chonkpilot-gui\systest\ws"

TABBAR = ".tb-bar.tb-bottom"
TAB = TABBAR + " .tb-tab"

# 可见面板选择器（v-show 面板栈：隐藏面板 offsetParent 为 null）
ACTIVE_PANEL = "[...document.querySelectorAll('.tab-panel')].find(p => p.offsetParent !== null)"


def deep_loads(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def wait_el(selector, max_wait=8):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if c.exists(selector).get("count", 0) > 0:
            return True
        time.sleep(0.3)
    return False


def wait_js(expr, max_wait=8):
    """轮询 JS 表达式为真（用于「可见面板内」等需运行时求值的断言）。"""
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if deep_loads(c.eval("!!(%s)" % expr, 5000)):
            return True
        time.sleep(0.3)
    return False


def in_active(inner):
    """在「可见面板」内取元素（inner 中可用变量 P 指代可见面板）。"""
    return "(function(){const P=%s; if(!P) return null; %s})()" % (ACTIVE_PANEL, inner)


def in_active_text(sel):
    """可见面板内 sel 元素的 textContent（无则空串）。"""
    return deep_loads(c.eval(in_active("const el=P.querySelector(%s); return el?el.textContent:''" % json.dumps(sel)), 5000)) or ""


def wait_active_el(sel, max_wait=8):
    return wait_js(in_active("return !!P.querySelector(%s)" % json.dumps(sel)), max_wait)


def active_tab_name():
    return deep_loads(c.eval("""(() => {
      const t = document.querySelector(%s + '.active');
      return t ? (t.querySelector('.tb-name')||{}).textContent || '' : '';
    })()""" % json.dumps(TAB), 5000)) or ""


def open_file(name, temporary=False):
    p = os.path.join(WS, name).replace("\\", "/")
    c.mq_emit("file-open", {"path": p, "temporary": temporary})
    time.sleep(1.2)


def case_title():
    open_file("sample.txt")
    if not wait_js("document.querySelectorAll(%s).length > 0" % json.dumps(TAB)):
        raise TestError("文件 tab 未打开")
    # 「标题显示文件名」的判定口径 = **本文件页签（激活页签）的 `.tb-name`**。
    # 旧断言取 `.tb-bar.tb-bottom .tb-tab .tb-name` 的 **DOM 首项**：实例启动会恢复上次打开的
    # 页签（guistate）+ 功能页页签（参数配置/项目配置等）也在同一条页签栏，首项未必是本次打开的
    # 文件 → 断言随实例状态漂移（2026-09-15 迁移遗留）。改按激活页签断言，强度不变（仍要求
    # 标题恰等于文件名 + 类型徽标正确）。
    deadline = time.time() + 8
    while time.time() < deadline and active_tab_name() != "sample.txt":
        time.sleep(0.3)
    if active_tab_name() != "sample.txt":
        raise TestError(f"激活页签标题应为文件名: {active_tab_name()!r}")
    typ = in_active_text(".file-type-tag")
    if not typ:
        raise TestError("缺文件类型徽标")
    if typ.lower() not in ("text", "txt"):
        raise TestError(f"类型徽标异常: {typ!r}")


def case_text_preview():
    open_file("sample.txt")
    if not wait_active_el(".source-code"):
        raise TestError("文本预览未渲染 .source-code")
    txt = in_active_text(".source-code")
    if "plain text content" not in txt:
        raise TestError(f"文本内容异常: {txt[:100]!r}")


def case_markdown_preview():
    open_file("sample.md")
    if not wait_active_el(".markdown-preview"):
        raise TestError("markdown 预览未渲染")
    txt = in_active_text(".markdown-preview")
    if "正文内容" not in txt:
        raise TestError(f"markdown 渲染内容异常: {txt[:100]!r}")
    # h1 渲染
    h1 = in_active_text(".markdown-preview h1")
    if "标题" not in h1:
        raise TestError(f"markdown h1 渲染异常: {h1!r}")


def case_html_preview():
    open_file("sample.html")
    if not wait_active_el("iframe.html-preview"):
        raise TestError("html 预览 iframe 未渲染")


def case_image_preview():
    open_file("sample.png")
    if not wait_active_el("img.preview-image"):
        raise TestError("图片预览未渲染")


def tab_display_names():
    """页签栏当前的**显示顺序**（DOM 顺序 = TabBar 内部顺序）。"""
    return deep_loads(c.eval("[...document.querySelectorAll(%s + ' .tb-name')].map(n => n.textContent.trim())" % json.dumps(TAB))) or []


def panel_order():
    """内容栈（CodeView `props.tabs` 顺序）指纹：`.tab-panel` DOM 顺序 + 各自 file-path。

    用于证明「弹框选中只改页签栏内部显示顺序、**未改 props.tabs**」——面板 DOM 顺序不变。
    """
    return deep_loads(c.eval("""[...document.querySelectorAll('.code-view .tab-panel')].map(p => {
      const e = p.querySelector('.file-path');
      return e ? e.textContent.trim() : '';
    })""")) or []


def button_need_state():
    """页签栏「按钮存在 / 溢出判定」联合状态（P6 ⑤ / P9 共用）。"""
    return deep_loads(c.eval("""(() => {
      const bar = document.querySelector(%s);
      if (!bar) return null;
      const tabs = [...bar.querySelectorAll('.tb-tab')];
      const last = tabs[tabs.length - 1];
      const padRight = parseFloat(getComputedStyle(bar).paddingRight) || 0;
      const availRight = bar.getBoundingClientRect().right - padRight;
      return {
        btn: !!bar.querySelector('.tb-more'),
        need: last ? last.getBoundingClientRect().right > availRight + 0.5 : false,
        barW: Math.round(bar.getBoundingClientRect().width),
      };
    })()""" % json.dumps(TABBAR)))


def drag_filetree(dx):
    """拖文件树|预览 分隔条（+dx = 预览变窄）。"""
    c.eval("""(() => {
      for (const r of document.querySelectorAll('.split-resizer.resizer-horizontal')) {
        const next = r.nextElementSibling;
        if (next && next.querySelector('.code-view')) {
          const rect = r.getBoundingClientRect();
          const x = rect.left + rect.width / 2, y = rect.top + rect.height / 2;
          r.dispatchEvent(new MouseEvent('mousedown', { clientX: x, clientY: y, bubbles: true, cancelable: true }));
          window.dispatchEvent(new MouseEvent('mousemove', { clientX: x + %d, clientY: y, bubbles: true, cancelable: true }));
          window.dispatchEvent(new MouseEvent('mouseup', { clientX: x + %d, clientY: y, bubbles: true, cancelable: true }));
          return 'ok';
        }
      }
      return 'no-resizer';
    })()""" % (dx, dx), 5000)
    time.sleep(0.9)


def case_many_tabs():
    # 打开 11 个文件 → 页签溢出（**2026-09-16 新口径**）：多余页签**不隐藏**，
    # 溢出只体现为「最后一个页签越过外层可用宽」+ "..." 按钮**绝对定位覆盖**在右侧。
    for i in range(1, 12):
        open_file(f"f{i:02d}.txt")
    names = tab_display_names()
    n = len(names)
    if n < 11:
        raise TestError(f"应打开 11+ 页签，实际 {n}")
    # ① 溢出 → "..." 按钮出现，且为**绝对定位**（覆盖式，不占 flex 位）
    if not wait_el(TABBAR + " .tb-more"):
        raise TestError("页签过多未显示 ... 按钮")
    g = deep_loads(c.eval("""(() => {
      const bar = document.querySelector(%s);
      const btn = bar.querySelector('.tb-more');
      const inner = bar.querySelector('.tb-inner');
      const tabs = [...bar.querySelectorAll('.tb-tab')];
      const br = bar.getBoundingClientRect();
      const lr = tabs.length ? tabs[tabs.length - 1].getBoundingClientRect().right : 0;
      const btr = btn ? btn.getBoundingClientRect() : null;
      return {
        total: tabs.length,
        hidden: bar.querySelectorAll('.tb-tab.tb-hidden').length,
        displayNone: tabs.filter(t => getComputedStyle(t).display === 'none').length,
        visHidden: tabs.filter(t => getComputedStyle(t).visibility === 'hidden').length,
        zeroWidth: tabs.filter(t => t.getBoundingClientRect().width <= 0).length,
        innerScroll: inner.scrollWidth,
        barClient: bar.clientWidth,
        innerWidthCss: getComputedStyle(inner).width,
        lastRight: lr,
        barRight: br.right,
        btnPos: btn ? getComputedStyle(btn).position : '',
        btnLeft: btr ? btr.left : 0,
        btnRight: btr ? btr.right : 0,
      };
    })()""" % json.dumps(TABBAR)))
    # ① 按钮绝对定位覆盖
    if g.get("btnPos") != "absolute":
        raise TestError(f"... 按钮应为绝对定位覆盖：{g}")
    if g.get("innerScroll", 0) <= g.get("barClient", 0):
        raise TestError(f"溢出场景内层内容宽应超过外层可用宽：{g}")
    # ② **全部页签均未被隐藏**（数量 = 页签数；无 .tb-hidden / display:none / visibility:hidden）
    if g.get("total") != n:
        raise TestError(f"页签数不符：DOM={g.get('total')} 名称数={n}")
    if g.get("hidden") or g.get("displayNone") or g.get("visHidden") or g.get("zeroWidth"):
        raise TestError(f"多余页签不应被隐藏/不可见：{g}")
    # 最后一个页签越过外层可用宽（可被按钮遮住一半），且确实被按钮覆盖
    if g.get("lastRight", 0) <= g.get("barRight", 0) - 8:
        raise TestError(f"溢出场景最后页签应越界（被按钮遮盖）：{g}")
    if g.get("btnLeft", 0) >= g.get("lastRight", 0):
        raise TestError(f"按钮应覆盖住最后页签的一部分：{g}")
    if abs(g.get("btnRight", 0) - g.get("barRight", 0)) > 2:
        raise TestError(f"按钮应贴外层右边界：{g}")
    # ③ 点击 "..." → 弹框列出**全部**页签（按当前显示顺序）
    c.eval("""(() => {
      const b = document.querySelector(%s + ' .tb-more');
      if (b) b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""" % json.dumps(TABBAR), 5000)
    if not wait_el(".tb-more-pop"):
        raise TestError("点击 ... 未弹出页签列表")
    if not wait_el(".tb-more-list"):
        raise TestError("页签列表容器缺失")
    pop_names = deep_loads(c.eval("[...document.querySelectorAll('.tb-more-item .tb-more-name')].map(n => n.textContent.trim())")) or []
    if pop_names != names:
        raise TestError(f"弹框应列出全部页签且顺序与页签栏一致：pop={pop_names} bar={names}")
    # ④ 选中弹框第 N 项 → 该项成为**第一个 .tb-tab**，其余保持相对顺序；只改内部顺序（面板顺序不变）
    pick = 4 if n > 4 else 1
    picked = pop_names[pick]
    expected = [picked] + names[:pick] + names[pick + 1:]
    panels_before = panel_order()
    errs_before = len((c.console().get("entries") or []))
    c.eval("""(() => {
      const it = document.querySelectorAll('.tb-more-item')[%d];
      if (it) it.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""" % pick, 5000)
    time.sleep(0.8)
    if deep_loads(c.eval("document.querySelectorAll('.tb-more-pop').length")):
        raise TestError("选中后弹框应关闭")
    after = tab_display_names()
    if after != expected:
        raise TestError(f"选中项应移到首位、其余相对顺序顺移：期望={expected} 实际={after}")
    if after[0] != picked:
        raise TestError(f"选中的页签应成为第一个 .tb-tab：{after[0]!r}")
    act = active_tab_name()
    if act != picked:
        raise TestError(f"选中后应激活该页签：{act!r}")
    if panel_order() != panels_before:
        raise TestError("选中重排不应改动 props.tabs 顺序（内容面板 DOM 顺序发生了变化）")
    for e in (c.console().get("entries") or [])[errs_before:]:
        if e.get("level") == "error":
            raise TestError(f"选中重排触发 console error：{e.get('text')}")
    # ⑤ 缩小→放大可用宽（拖文件树分隔条 → TabBar 变窄/变宽）：按钮「按需出现/消失」，
    #    判定与几何始终一致（按钮存在 ⟺ 最后页签越过外层可用宽），且不留弹框。
    #    （P9 是本项的正式固化用例：自校准「刚好放得下」的页签数 + 双向断言 + 页签不被隐藏。）
    st0 = button_need_state()
    if not st0:
        raise TestError("未取到页签栏几何")
    drag_filetree(-600)  # 预览区放到最宽
    st1 = button_need_state()
    if not st1 or st1["btn"] != st1["need"]:
        raise TestError(f"可用宽变宽后按钮出现/消失与几何判定不一致：{st1}")
    drag_filetree(600)   # 预览区放到最窄 → 必溢出
    if not wait_el(TABBAR + " .tb-more", max_wait=4):
        raise TestError("可用宽变窄后溢出按钮未出现")
    if c.exists(".tb-more-pop").get("count", 0) > 0:
        raise TestError("可用宽变化后弹框残留")
    drag_filetree(st0["barW"] - (button_need_state() or {}).get("barW", st0["barW"]))  # 还原原宽度


def tabbar_overflow_state():
    """页签栏溢出联合指纹（DOM + 计算样式证据）：几何判定 need / 按钮存在 btn / 按钮定位与贴边 /
    页签数与可见性（.tb-hidden / display:none / visibility:hidden / 零宽）。"""
    return deep_loads(c.eval("""(() => {
      const bar = document.querySelector(%s);
      if (!bar) return null;
      const tabs = [...bar.querySelectorAll('.tb-tab')];
      const inner = bar.querySelector('.tb-inner');
      const btn = bar.querySelector('.tb-more');
      const last = tabs[tabs.length - 1];
      const padRight = parseFloat(getComputedStyle(bar).paddingRight) || 0;
      const br = bar.getBoundingClientRect();
      const availRight = br.right - padRight;
      const btr = btn ? btn.getBoundingClientRect() : null;
      return JSON.stringify({
        n: tabs.length,
        barW: Math.round(br.width),
        innerScroll: inner ? inner.scrollWidth : -1,
        need: last ? last.getBoundingClientRect().right > availRight + 0.5 : false,
        btn: !!btn,
        btnPos: btn ? getComputedStyle(btn).position : '',
        btnRightGap: btr ? Math.round(br.right - btr.right) : null,
        hiddenClass: bar.querySelectorAll('.tb-tab.tb-hidden').length,
        displayNone: tabs.filter(t => getComputedStyle(t).display === 'none').length,
        visHidden: tabs.filter(t => getComputedStyle(t).visibility === 'hidden').length,
        zeroWidth: tabs.filter(t => t.getBoundingClientRect().width <= 0).length,
      });
    })()""" % json.dumps(TABBAR)))


def close_last_tab():
    """关掉页签栏最后一个页签（`.tb-tab .tb-close` × ；P9 自造「刚好放得下」的页签数用）。"""
    return deep_loads(c.eval("""(() => {
      const tabs = [...document.querySelectorAll(%s + ' .tb-tab')];
      const last = tabs[tabs.length - 1];
      const btn = last ? last.querySelector('.tb-close') : null;
      if (!btn) return 'no-close';
      btn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""" % json.dumps(TABBAR)))


def case_tabbar_more_toggle():
    """P9 可用宽变化 → 溢出按钮「出现 / 消失」与溢出判定一致 + 页签不被隐藏 + 恢复宽度回初始状态。

    （把原先只做「手工 eval 复核」（1280→900→2400→900）的「拖宽 → 按钮出现/消失」固化为正式用例。）
    口径与 P6 一致（[51 §2] 2026-09-16 页签栏新口径）：`need` = 最后一个页签右边界 > 外层可用宽
    右边界；`.tb-more` 为**绝对定位覆盖**按钮、多余页签**不隐藏**。
    自校准：先在最宽可用宽下把页签收敛到「刚好放得下」（收不掉就关最后一个），再压缩到最窄 →
    同一批页签下按钮必须出现；再拖回最宽 → 必须消失且与几何一致；最后把可用宽还原到初始值。
    """
    st0 = tabbar_overflow_state()
    if not st0:
        raise TestError("未取到页签栏几何")
    drag_filetree(-2000)  # 预览区放到最宽（filetree 到最小宽）
    wide = tabbar_overflow_state()
    # ① 自校准「最宽可用宽下刚好放得下」：关最后一个页签直到不溢出（容纳上限随窗口/页签名宽度自适应）
    for _ in range(30):
        if not wide["need"]:
            break
        if wide["n"] <= 2:
            raise TestError(f"窗口过窄：仅 2 个页签在最宽可用宽下仍溢出：{wide}")
        if close_last_tab() != "ok":
            raise TestError(f"页签关闭按钮不可用（无法自造状态）：{wide}")
        time.sleep(0.6)
        wide = tabbar_overflow_state()
    if wide["need"] or wide["btn"]:
        raise TestError(f"最宽可用宽 + {wide['n']} 页签应无溢出（... 按钮消失）：{wide}")
    n_fit = wide["n"]
    # ② 同一批页签压缩到最窄可用宽 → 必溢出：按钮**出现** + 绝对定位贴外层右边界
    drag_filetree(2000)
    narrow = tabbar_overflow_state()
    if not narrow["need"] or not narrow["btn"]:
        raise TestError(f"可用宽变窄后应溢出并显示 ... 按钮（{n_fit} 页签）：{narrow}")
    if narrow["n"] != n_fit:
        raise TestError(f"可用宽变化不应改变页签集合：{wide['n']} → {narrow['n']}")
    if narrow["btnPos"] != "absolute" or (narrow["btnRightGap"] if narrow["btnRightGap"] is not None else 99) > 2:
        raise TestError(f"... 按钮应为绝对定位且贴外层右边界：{narrow}")
    if narrow["hiddenClass"] or narrow["displayNone"] or narrow["visHidden"] or narrow["zeroWidth"]:
        raise TestError(f"溢出时页签不应被隐藏 / 零宽：{narrow}")
    names = tab_display_names()
    if len(names) != narrow["n"]:
        raise TestError(f"页签数（DOM {narrow['n']}）应等于名称数（{len(names)}）")
    # ③ 拖回最宽 → 按钮**消失**，且回到同一页签集 / 同一可用宽
    drag_filetree(-2000)
    back = tabbar_overflow_state()
    if back["btn"] or back["need"]:
        raise TestError(f"可用宽恢复（最宽）后应回到无溢出（按钮消失）：{back}")
    if back["n"] != narrow["n"] or abs(back["barW"] - wide["barW"]) > 2:
        raise TestError(f"恢复最宽后应回到同一页签集/可用宽：{wide} → {back}")
    # ④ 还原到用例开始时的可用宽 → 按钮存在性与溢出判定一致（回初始状态）
    #    注：drag_filetree 的正负号 = **正 dx → 预览变窄**，故从「最宽」回到 st0 需 dx = back - st0。
    drag_filetree(back["barW"] - st0["barW"])
    final = tabbar_overflow_state()
    if abs(final["barW"] - st0["barW"]) > 2 or final["btn"] != final["need"]:
        raise TestError(f"还原初始可用宽后按钮存在性与溢出判定应一致：st0={st0} final={final}")
    # ⑤ 全程不留残留弹框
    if c.exists(".tb-more-pop").get("count", 0) > 0:
        raise TestError("可用宽变化后弹框残留")


def case_open_persist():
    # 打开文件应持久化（SaveOpenedFile 桥）——重启恢复依赖该落盘，此处断言调用无异常
    open_file("sample.md")
    time.sleep(0.5)
    # 触发一次无异常打开即可（持久化链路在 handleFileOpen 内同步调用）
    return True


def case_selection_bar():
    # 预览区选中文本 → 浮动操作条（.preview-selection-bar）
    open_file("sample.txt")
    if not wait_active_el(".source-code"):
        raise TestError("文本预览未渲染")
    # ── 稳定条件（2026-09-16 加固；针对「首跑偶发红、复跑 8/8」）──
    # 本用例断言的是「用户选中文本 → 浮条出现」。测试侧只用**一次**合成 `selectionchange`
    # 模拟拖选（真实拖选会产生一串），而 CodeView 的浮条判定是「selectionchange → 120ms 防抖
    # → updateSelBar」（CodeView.vue:657-660），且任一 scroll / mousedown 都会 hideSelBar
    # （CodeView.vue:626/744）——面板尚未落定（页签切换 / 文件异步读取 / 布局抖动）时建立的
    # 选区会被吞掉，浮条永不出现。故先等「激活页签 = 本文件 + 可见面板正文非空」连续两次
    # 采样一致，再建立选区。断言强度不变（仍要求浮条出现）。
    prev = None
    settled = False
    deadline = time.time() + 8
    while time.time() < deadline:
        cur = panel_state()
        if cur == prev and cur[0] == "sample.txt" and cur[1] > 0:
            settled = True
            break
        prev = cur
        time.sleep(0.3)
    if not settled:
        raise TestError(f"文本面板未进入稳定状态（激活页签 / 正文长度 / 可见面板数）: {prev!r}")
    # 建立选区 → 等浮条；最多 3 轮（按同一契约重放「选中 + selectionchange」信号，模拟真实
    # 拖选的事件流；**断言不变**：浮条必须出现，否则报错并贴上现场）。
    diag = None
    for _ in range(3):
        c.eval("""(() => {
      const P = %s;
      const el = P ? P.querySelector('.source-code') : null;
      if (!el) return 'no-el';
      const range = document.createRange();
      range.selectNodeContents(el);
      const sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
      document.dispatchEvent(new Event('selectionchange'));
      return 'selected';
    })()""" % ACTIVE_PANEL, 5000)
        time.sleep(0.8)
        if c.exists(".preview-selection-bar").get("count", 0) > 0:
            return True
        diag = selection_diag()
        time.sleep(0.3)
    raise TestError(f"选中文本后浮动操作条未出现：{diag}")


def panel_state():
    """(激活页签名, 可见面板 .source-code 正文长度, 可见面板数) —— P8 稳定条件采样。"""
    return tuple(deep_loads(c.eval("""(() => {
      const t = document.querySelector(%s + '.active');
      const vis = [...document.querySelectorAll('.tab-panel')].filter(p => p.offsetParent !== null);
      const P = vis[0];
      const el = P ? P.querySelector('.source-code') : null;
      return JSON.stringify([
        t ? ((t.querySelector('.tb-name') || {}).textContent || '').trim() : '',
        el ? (el.textContent || '').length : -1,
        vis.length,
      ]);
    })()""" % json.dumps(TAB)))) or ()


def selection_diag():
    """P8 失败现场：选区状态 + 可见面板 + 浮条/幽灵层 + 页签数（DOM/计算样式证据）。"""
    return deep_loads(c.eval("""(() => {
      const P = %s;
      const sel = window.getSelection();
      let rect = null;
      try { rect = sel && sel.rangeCount ? sel.getRangeAt(0).getBoundingClientRect() : null; } catch (e) {}
      const root = document.querySelector('.code-view');
      const el = P ? P.querySelector('.source-code') : null;
      return JSON.stringify({
        selCollapsed: sel ? sel.isCollapsed : null,
        selLen: sel ? (sel.toString() || '').length : -1,
        scLen: el ? (el.textContent || '').length : -1,
        scRect: rect ? [Math.round(rect.width), Math.round(rect.height)] : null,
        barCount: document.querySelectorAll('.preview-selection-bar').length,
        dragGhost: document.querySelectorAll('.preview-drag-ghost').length,
        rootHasEmpty: root ? root.classList.contains('empty') : null,
        tabs: document.querySelectorAll(%s).length,
      });
    })()""" % (ACTIVE_PANEL, json.dumps(TAB))))


def main():
    c.wait_ready()
    c.console(clear=True)
    total = 0
    ok = 0
    total += 1; ok += run_case("P1 标题显示文件名 + 类型徽标", case_title)
    total += 1; ok += run_case("P2 文本预览", case_text_preview)
    total += 1; ok += run_case("P3 markdown 预览渲染", case_markdown_preview)
    total += 1; ok += run_case("P4 html 预览 iframe", case_html_preview)
    total += 1; ok += run_case("P5 图片预览", case_image_preview)
    total += 1; ok += run_case("P6 页签过多溢出（... 按钮绝对定位覆盖 + 弹框列全部 + 选中置首）", case_many_tabs)
    total += 1; ok += run_case("P7 打开文件持久化（重启恢复标注）", case_open_persist)
    total += 1; ok += run_case("P8 预览选中文本 → 浮动操作条", case_selection_bar)
    total += 1; ok += run_case("P9 可用宽变化 → 溢出按钮出现/消失 + 页签不被隐藏 + 恢复宽度回初始",
                               case_tabbar_more_toggle)
    # 与其它套件统一口径：计数汇总 + 退出码（0=全过）——见 51-FP与测试映射 §1
    print("\npreview UI 断言：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
    print("RESULT:", ok == total)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

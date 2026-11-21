# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑰：**顶栏（条目化 toolbar）**（迁移自 test_toolbar.py T1–T10 + S1–S3）。

按 title / 视图类定位（不按下标）：顶栏外壳/品牌/搜索/窗口控件/条目、最近项目下拉、
配置菜单叶子、场景入口、检索、会话入口、三区常显；主题/语言菜单在末尾验证并**还原**。
"""

import json
import time

from .framework import case


def _bar_click(env, pat):
    return env.js("(()=>{const b=[...document.querySelectorAll('.ck-topbar .bar-btn')]"
                  ".find(x=>/%s/.test(x.getAttribute('title')||''));"
                  "if(!b)return 'no-btn';b.click();return 'ok';})()" % pat)


def _menu_click(env, textpat):
    return env.js("(()=>{const rows=[...document.querySelectorAll("
                  "'.b-dropdown-popper .menu-row')];"
                  "const r=rows.find(x=>/%s/.test(x.textContent||''));"
                  "if(!r)return 'no-row';r.click();return 'ok';})()" % textpat)


@case("S-TOOLBAR-01", "工具栏", "顶栏条目化：结构/最近项目/配置菜单/场景/检索/会话入口/主题/语言",
      stage="seeded", order=850, fp="33 toolbar 顶栏（test_toolbar T1–T10）",
      desc="`.ck-topbar` 外壳 + 品牌/搜索/窗口控件/条目≥6；无知识库按钮且 Explorer 有「扩展」；"
           "最近项目下拉与 gui.recent.list 一致；配置菜单叶子打开 LLM 配置页；场景按钮打开场景页；"
           "检索 README 命中；会话入口打开左侧工作区树；无三区 toggle 且 chat/task 常显；"
           "主题→dark、语言→en-US 生效后**还原 light/zh-CN**。")
def s_toolbar_01(env):
    it = env.jsj("""JSON.stringify((()=>{const q=s=>!!document.querySelector(s);
      return {topbar:q('.ck-topbar'),brand:q('.ck-topbar .ck-brand'),
              search:q('.search-input-wrap'),win:q('.win-controls'),
              entries:document.querySelectorAll('.ck-topbar .bar-btn').length}})())""")
    env.check("顶栏外壳/品牌/搜索/窗口控件/条目≥6",
              all(it.get(k) for k in ("topbar", "brand", "search", "win"))
              and int(it.get("entries") or 0) >= 6, it)

    kb = env.jsj("""JSON.stringify((()=>{
      const ts=[...document.querySelectorAll('.ck-topbar .bar-btn')]
        .map(b=>b.getAttribute('title')||'');
      const seg=[...document.querySelectorAll('.explorer-seg-btn')]
        .some(s=>/扩展|Extensions/.test(s.getAttribute('title')||''));
      return {kb:ts.some(t=>/知识库|Knowledge/.test(t)),seg}})())""")
    env.check("顶栏无知识库 + Explorer 有扩展分段",
              kb.get("kb") is False and kb.get("seg") is True, kb)
    env.check("存在「打开目录」条目", env.js(
        "(()=>[...document.querySelectorAll('.ck-topbar .bar-btn')]"
        ".some(x=>/打开|Open/.test(x.getAttribute('title')||'')))()") is True)

    # 最近项目
    env.js("document.querySelector('.rd-arrow')?.click(); 'ok'")
    env.wait(lambda: True if env.js("!!document.querySelector('.b-dropdown-popper')")
             is True else None, "最近项目下拉", timeout=10)
    rl = env.req("gui.recent.list", {}) or {}
    rows = env.jsj("""JSON.stringify([...document.querySelectorAll(
      '.b-dropdown-popper .b-dropdown-item')].map(el=>({
      title:(el.querySelector('.recent-item')||{}).getAttribute
            ?((el.querySelector('.recent-item')||{}).getAttribute('title')||''):'',
      text:(el.textContent||'').trim(),
      disabled:el.classList.contains('is-disabled'),
      dflt:el.classList.contains('recent-row-default')})))""") or []
    dirs = [str(d) for d in (rl.get("dirs") or [])]
    dflt = rl.get("default_dir") or ""
    got_dirs = [r.get("title") for r in rows if not r.get("dflt") and not r.get("disabled")]
    def_row = next((r for r in rows if r.get("dflt")), None)
    if dirs:
        ok = got_dirs == dirs and (not dflt or dflt in dirs or def_row is not None)
    elif dflt:
        ok = def_row is not None and def_row.get("title") == dflt
    else:
        ok = len(rows) == 1 and rows[0].get("disabled") is True
    env.check("最近项目下拉 = dirs + 默认工作区", ok,
              {"dirs": dirs, "default": bool(def_row), "rows": rows})
    env.js("document.querySelector('.rd-arrow')?.click(); 'ok'")
    time.sleep(0.2)

    # 配置菜单 → LLM 配置页
    env.check("配置菜单展开", _bar_click(env, "配置|Settings|Config") == "ok")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.b-dropdown-popper .menu-row')") is True else None,
        "配置菜单叶子", timeout=10)
    env.check("点击 LLM 菜单叶子", _menu_click(env, "LLM") == "ok")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.code-view .special-tab.settings-page')")
        is True else None, "LLM 配置页打开", timeout=20)
    env.check("配置菜单 → 用户配置页", env.js(
        "!!document.querySelector('.code-view .special-tab.settings-page')") is True)

    # 场景入口
    env.check("点击场景入口", _bar_click(env, "场景|Scenarios") == "ok")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.code-view .special-tab.dialog-content')")
        is True else None, "场景页打开", timeout=20)
    env.check("场景入口 → 场景页", env.js(
        "!!document.querySelector('.code-view .special-tab.dialog-content')") is True)

    # 检索
    env.client.input(".search-input-wrap input", "README")
    res = env.wait(lambda: int(env.js(
        "document.querySelectorAll('.search-result-item').length") or 0)
        if int(env.js("document.querySelectorAll('.search-result-item').length") or 0) > 0
        else None, "检索下拉出现结果", timeout=10)
    names = env.jsj("JSON.stringify([...document.querySelectorAll("
                    "'.search-result-item .search-filename')]"
                    ".map(e=>e.textContent.trim()))") or []
    env.check("检索命中 README", any("README" in n for n in names), names[:6])
    env.client.input(".search-input-wrap input", "")
    time.sleep(0.3)

    # 三区常显 + 无 toggle 条目
    toggles = env.js("(()=>{const s=JSON.stringify("
                     "window.__ckLayout?.engine?.state?.config?.live||{});"
                     "return /chat-toggle|filetree-toggle|tasks-toggle/.test(s)})()")
    env.check("顶栏无三区显隐命令", toggles is False)
    env.check("chat 区常显", env.js("!!document.querySelector('.chat-panel')") is True)
    env.check("task 区常显", env.js("!!document.querySelector('.session-chat')") is True)
    env.check("statusbar 外壳存在", env.js(
        "!!document.querySelector('.statusbar.ck-statusbar')") is True)
    env.check("statusbar 条目 ≥2", int(env.js(
        "document.querySelectorAll('.statusbar .bar-item-shell').length") or 0) >= 2)

    # 会话入口（多 workdir 后 = 左侧「工作区」段：会话挂在项目工作区树下）
    env.check("点击会话入口", _bar_click(env, "会话|Sessions") == "ok")
    env.wait(lambda: True if env.js(
        "(()=>{const p=document.querySelector('.ws-tree');"
        "return !!p&&p.getBoundingClientRect().width>0})()") is True else None,
        "左侧工作区树打开", timeout=15)
    env.check("会话入口 → 左侧工作区树", env.js(
        "(()=>{const p=document.querySelector('.ws-tree');"
        "return !!p&&p.getBoundingClientRect().width>0})()") is True)
    env.js("window.mq.emit('filetree-mode-select', {mode:'project'}); 'ok'")
    time.sleep(0.4)

    # 主题菜单 → dark，并还原 light
    env.check("主题菜单展开", _bar_click(env, "主题|Theme") == "ok")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.b-dropdown-popper .menu-row')") is True else None,
        "主题菜单叶子", timeout=10)
    env.check("选择深色主题", _menu_click(env, "深色|Dark") == "ok")
    env.wait(lambda: True if env.js(
        "document.documentElement.getAttribute('data-theme')") == "dark" else None,
        "主题 dark 生效", timeout=15)
    env.eq("usr theme 落库 dark", env.usr().get("theme"), "dark")
    env.req("gui.ui.save", {"ui": {"theme": "light"}})
    env.wait(lambda: True if env.js(
        "document.documentElement.getAttribute('data-theme')") == "light" else None,
        "主题还原 light", timeout=15)

    # 语言菜单 → en-US，并还原 zh-CN
    env.check("语言菜单展开", _bar_click(env, "语言|Language") == "ok")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.b-dropdown-popper .menu-row')") is True else None,
        "语言菜单叶子", timeout=10)
    env.check("选择 English", _menu_click(env, "English") == "ok")
    env.wait(lambda: True if env.js(
        "localStorage.getItem('chonkpilot-locale')") == "en-US" else None,
        "locale en-US 生效", timeout=15)
    env.eq("usr locale 落库 en-US", env.usr().get("locale"), "en-US")
    # 语言还原走真实菜单动作（lang-select 才会应用 DOM/i18n；gui.ui.save 仅落库）
    env.check("语言菜单再开", _bar_click(env, "语言|Language") == "ok")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.b-dropdown-popper .menu-row')") is True else None,
        "语言菜单叶子（还原）", timeout=10)
    env.check("选择简体中文", _menu_click(env, "简体中文|Chinese") == "ok")
    env.wait(lambda: True if env.js(
        "localStorage.getItem('chonkpilot-locale')") == "zh-CN" else None,
        "locale 还原 zh-CN", timeout=15)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("顶栏组无前端错误", len(errs) == 0, errs[:3])
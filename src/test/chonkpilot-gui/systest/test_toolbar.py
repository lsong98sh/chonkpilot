"""回归测试：toolbar 组（51-FP与测试映射「toolbar」）+ statusbar（3 项）。

〔2026-10-16 UILAYOUT-1 P4①：**栏条目化**（默认 free 形态）〕
toolbar 不再是 `views/toolbar/Toolbar.vue` 的硬编码 DOM，而是**自由布局容器的顶栏外壳**：
  - 外壳 = `.toolbar.ck-topbar`（品牌静态块 + 机制层栏三区 `LfBarZones` + **窗口控件固定右端**）；
  - 条目 = `layout.free` 文档 `topToolbar.items`：
      · 配置**按钮** → 发**既有宿主主题**（`publish-mq`：workdir-open / scenario-open / sessions-open …）；
      · 配置**菜单** → 机制层菜单树（`.bar-btn` 触发 → `.b-dropdown-popper` + `.menu-row` 叶子）；
      · **视图条目** → 内嵌宿主组件（搜索 `.search-input-wrap` / 最近项目 `.rd-arrow` / Chrome 告警 /
        登出 / 记忆 token / 队列 / 索引徽标 / 语言切换）——实时 / 条件 / 动态项一律做成栏视图（裁决 2）。
故本套件按**新结构**定位：按钮/菜单触发用 `.ck-topbar .bar-btn[title=…]`，菜单叶子用 `.menu-row`。
`test_toolbar` 的旧 `.toolbar-left/.toolbar-right .tb-btn` **固定下标**基线已废弃（条目化后条目可配置、
顺序可变），改按 title / 视图类定位。

驱动：真实 DOM click（触发 v-mq）→ 断言前端状态变化。
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, snapshot_config, restore_config  # noqa: E402  动态端口；套件级配置快照-还原

PORT = free_port()


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def bar_btn_js(pat):
    """按 title 正则定位顶栏条目（按钮 / 菜单触发）并点击（不按下标）。"""
    return ("(()=>{const b=[...document.querySelectorAll('.ck-topbar .bar-btn')]"
            ".find(x=>/%s/.test(x.getAttribute('title')||''));"
            "if(!b)return false;b.click();return true})()") % pat


def menu_leaf_click(textpat):
    """在已展开的机制层下拉里点第一个命中的菜单叶子（`.menu-row`）。"""
    return ("(()=>{const rows=[...document.querySelectorAll('.b-dropdown-popper .menu-row')];"
            "const r=rows.find(x=>/%s/.test(x.textContent||''));if(!r)return false;r.click();return true})()") % textpat


def main():
    gui = GUIClient(port=PORT)
    c = Checker()
    # T7（主题）/ T8（语言）/ S3（gui.ui.save）都会把 **usr 主库**（~/.chonkpilot）的 theme/locale 写实，
    # 故按 51 §6-8「配置快照-还原」：跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)

        # T1 结构齐全：顶栏外壳 + 品牌 + 搜索视图条目 + 窗口控件 + 条目（按钮/菜单）
        items = J(gui, """(()=>{const q=s=>!!document.querySelector(s);return JSON.stringify({
  topbar:q('.ck-topbar'), brand:q('.ck-topbar .ck-brand'), search:q('.search-input-wrap'),
  win:q('.win-controls'), entries:document.querySelectorAll('.ck-topbar .bar-btn').length})})()""")
        it = json.loads(items)
        c.check("T1 顶栏外壳基本元素（topbar/brand/search/win/条目）",
                it["topbar"] and it["brand"] and it["search"] and it["win"] and it["entries"] >= 6,
                repr(it))

        # T1b 顶栏已无「知识库」按钮（2026-09-16 移除；入口在 filetree「扩展」分段）
        kbo = json.loads(J(gui, """(()=>{const bs=[...document.querySelectorAll('.ck-topbar .bar-btn')];
  const ts=bs.map(b=>b.getAttribute('title')||'');
  const seg=[...document.querySelectorAll('.explorer-seg-btn')]
    .some(s=>/扩展|Extensions/.test(s.getAttribute('title')||''));
  return JSON.stringify({n:bs.length, kb:ts.some(t=>/知识库|Knowledge/.test(t)), seg})})()"""))
        c.check("T1b 顶栏无知识库条目（title 定位；入口在 filetree「扩展」分段）",
                (not kbo["kb"]) and kbo["seg"], repr(kbo))

        # T2 【打开目录】条目存在（`publish-mq{workdir-open}`）—— 点击会弹系统目录对话框，自动化不触发
        c.check("T2 打开目录条目存在（publish-mq workdir-open）",
                bool(J(gui, """(()=>{return [...document.querySelectorAll('.ck-topbar .bar-btn')]
  .some(x=>/打开|Open/.test(x.getAttribute('title')||''))})()""")),
                "点击弹系统对话框，自动化不触发")

        # T3 最近项目（栏视图条目）：点箭头 → 下拉出现（空态提示）
        J(gui, "document.querySelector('.rd-arrow')?.click(); 'ok'")
        time.sleep(0.5)
        c.check("T3 最近项目下拉出现", J(gui, "!!document.querySelector('.b-dropdown-popper')"),
                "栏视图条目 recent-dirs → 动态清单（gui.recent.list）")
        if J(gui, "!!document.querySelector('.b-dropdown-popper')"):
            c.check("T3b 最近项目空态提示", J(gui, "!!document.querySelector('.b-dropdown-item.is-disabled')"))
        J(gui, "document.querySelector('.rd-arrow')?.click(); 'ok'")
        time.sleep(0.3)

        # T4 设置菜单（配置菜单条目）→ 叶子发 preview-tab-open{settings-llm} → 预览区 LLM 配置页签
        gui.console(clear=True)
        J(gui, bar_btn_js("设置|Settings"))
        time.sleep(0.6)
        c.check("T4a 设置菜单展开（机制层下拉 + 菜单叶子）",
                J(gui, "!!document.querySelector('.b-dropdown-popper .menu-row')"))
        J(gui, menu_leaf_click("LLM"))
        time.sleep(1.0)
        c.check("T4 用户配置打开 preview 页签",
                J(gui, "!!document.querySelector('.code-view .special-tab.settings-page')"),
                "菜单叶子 publish-mq(preview-tab-open,settings-llm) → CodeView 渲染 LLM 配置页")

        # T5 场景（配置按钮）→ publish-mq(scenario-open) → preview 场景页签
        gui.console(clear=True)
        J(gui, bar_btn_js("场景|Scenarios"))
        time.sleep(1.0)
        c.check("T5 场景配置打开 preview 页签",
                J(gui, "!!document.querySelector('.code-view .special-tab.dialog-content')"),
                "bar button publish-mq(scenario-open) → previewTabOpen(scenario)")

        # T6 检索框（栏视图条目 `search`）
        gui.input(".search-input-wrap input", "a")
        time.sleep(0.9)
        n = J(gui, "document.querySelectorAll('.search-result-item').length")
        c.check("T6 检索框输入出现下拉备选", n > 0, f"results={n}（SearchProjectFiles 桥未实现 → 无结果）— 待实施")
        gui.input(".search-input-wrap input", "")
        time.sleep(0.3)

        # T7 主题（菜单条目）→ 叶子 publish-mq(theme-select) → 生效并保存
        J(gui, bar_btn_js("主题|Theme"))
        time.sleep(0.5)
        c.check("T7a 主题菜单出现", J(gui, "!!document.querySelector('.b-dropdown-popper')"))
        J(gui, menu_leaf_click("深色|Dark"))
        time.sleep(0.6)
        theme = J(gui, "document.documentElement.getAttribute('data-theme')")
        c.check("T7b 主题选择生效并保存", theme == "dark", repr(theme))
        # T7c 落库回读（usr 主库 theme）
        usr = (gui.req("data-user-config-load", {}) or {}).get("data") or {}
        c.check("T7c 主题落库回读（usr theme=dark）", usr.get("theme") == "dark",
                f"theme={usr.get('theme')!r}")

        # T8 语言（菜单条目）→ 叶子 publish-mq(lang-select) → 生效
        J(gui, bar_btn_js("语言|Language"))
        time.sleep(0.5)
        J(gui, menu_leaf_click("English"))
        time.sleep(0.6)
        lang = J(gui, "localStorage.getItem('chonkpilot-locale') || ''")
        c.check("T8 语言切换生效（localStorage locale）", lang == "en-US", repr(lang))
        usr2 = (gui.req("data-user-config-load", {}) or {}).get("data") or {}
        c.check("T8b 语言落库回读（usr locale=en-US）", usr2.get("locale") == "en-US",
                f"locale={usr2.get('locale')!r}")

        # T9 会话入口（配置按钮 publish-mq sessions-open）→ 左侧导航切到「会话」页签
        J(gui, bar_btn_js("会话|Sessions"))
        shown = False
        for _ in range(20):
            shown = bool(J(gui, "(()=>{const p=document.querySelector('.sessions-pane');"
                               "return !!p && p.getBoundingClientRect().width>0})()"))
            if shown:
                break
            time.sleep(0.2)
        segActive = J(gui, "(()=>{const b=Array.from(document.querySelectorAll('.explorer-seg-btn'))"
                           ".find(x=>x.classList.contains('active'));return b?b.textContent.trim():''})()")
        c.check("T9 会话入口 → 左侧导航「会话」页签打开",
                shown and segActive in ('会话', 'Sessions'), f"shown={shown} seg={segActive!r}")
        gui.eval("window.mq.emit('filetree-mode-select', {mode:'project'}); 'ok'")
        time.sleep(0.4)

        # T10 三区显隐已移除 + 三区常显（2026-10-16 用户裁决）
        # 顶栏条目里不得再有指向 chat-toggle / filetree-toggle / tasks-toggle 的命令
        toggles = J(gui, """(()=>{const s=JSON.stringify(window.__ckLayout?.engine?.state?.config?.live || {});
  return /chat-toggle|filetree-toggle|tasks-toggle/.test(s)})()""")
        c.check("T10 顶栏无三区显隐条目（默认文档不含 *-toggle 命令）", toggles is False, f"found={toggles}")
        c.check("T10b chat 区常显（无显隐 → 恒挂载）",
                J(gui, "!!document.querySelector('.chat-panel')"), "裁决：chat 常显")
        c.check("T10c task 区常显（无显隐 → 恒挂载；原「默认收起」口径已废止）",
                J(gui, "!!document.querySelector('.session-chat')"), "裁决：task 常显")

        # ── statusbar（并入本组）──
        c.check("S1 statusbar 存在（条目化外壳）",
                J(gui, "!!document.querySelector('.statusbar.ck-statusbar')"),
                "自由布局形态状态栏外壳")
        c.check("S2 statusbar 条目区存在",
                J(gui, "document.querySelectorAll('.statusbar .bar-item-shell').length >= 2"),
                "状态栏条目（记忆 token / 队列 / 索引徽标 / DevTools / 语言切换）")
        try:
            gui.req("gui.ui.save", {"ui": {"locale": "en-US"}})
            c.check("S3 语言保存（gui.ui.save ui）", True, "ok")
        except Exception as e:
            c.check("S3 语言保存（gui.ui.save ui）", False, str(e))

    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr theme/locale + prj opened-files/layout.*（缺省 → 删键）
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

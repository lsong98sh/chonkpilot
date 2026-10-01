"""回归测试：toolbar 组（51-FP与测试映射「toolbar」12 项）+ statusbar（3 项）。

toolbar-left 按钮（2026-09-16 起 4 个，按 title 定位，不按下标）：
open 打开 / recent-dirs 最近目录 / settings 设置 / scenarios 场景
（原第 5 个「知识库」按钮已移除；知识库视图入口 = filetree 区「项目 / 知识库」分段）。
toolbar-right 按钮顺序（DOM）：0=theme 1=lang 2=sessions 3=task切换 4=filetree切换 5=chat切换
（元素顺序/类名不变，即使 3/4/5 已补「图标+文字」——下标定位仍成立）。
任务区默认**收起**（2026-09-27 首屏减负：`MainLayout.vue taskOpen = ref(false)`）→ T12 先断默认收起。
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


def left_btn_js(pat):
    """按 title 正则定位 toolbar-left 按钮并点击（不按下标，避免按钮增减导致误点）。"""
    return ("(()=>{const b=[...document.querySelectorAll('.toolbar-left .tb-btn')]"
            ".find(x=>/%s/.test(x.getAttribute('title')||''));"
            "if(!b)return false;b.click();return true})()") % pat


def main():
    gui = GUIClient(port=PORT)
    c = Checker()
    # T7（主题）/ T8（语言）/ S3（gui.ui.save）都会把 **usr 主库**（~/.chonkpilot）的 theme/locale
    # 写实（前端 Toolbar `saveUIState` / i18n `setLocale` → `gui.ui.save` → `callSaveUIState`
    # → `data-user-config-save`），且 `gui.init-data` 让 MainLayout 在 localStorage 缺失时按 DB
    # `ui.locale` 兜底 → 写后不还原会让同批 zh-CN 文案断言的套件漂移。故按 51 §6-8
    # 「配置快照-还原」：跑前快照、finally 还原（缺省 → 删键回落系统默认 zh-CN / system）。
    # 本套件无 `--data-dir`（用**共享** ws 库）→ 打开 settings/scenario 页签与面板开关切换还会隐式
    # 落 prj 的 `opened-files` / `layout.*` → 取**套件级全量快照**（usr + prj 两层）。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)

        # T1 内容齐全
        items = J(gui, """(()=>{const q=s=>!!document.querySelector(s);return JSON.stringify({
  open:q('.open-btn'),recent:q('.dropdown-arrow'),search:q('.search-input-wrap'),win:q('.win-controls'),
  rightBtns:document.querySelectorAll('.toolbar-right .tb-btn').length})})()""")
        it = json.loads(items)
        c.check("T1 toolbar 基本元素（open/recent/search/win/右侧按钮）",
                it["open"] and it["recent"] and it["search"] and it["win"] and it["rightBtns"] >= 6,
                repr(it))

        # T1b toolbar 已无「知识库」按钮（2026-09-16 移除）：左区恰 4 个按钮，且无一标题命中
        # 知识库/Knowledge；同时能力面视图入口仍在（filetree 区「扩展」分段，2026-10-01 P3 由
        # 原「知识库/工具」两段合并为「扩展」页）。
        kbo = json.loads(J(gui, """(()=>{const bs=[...document.querySelectorAll('.toolbar-left .tb-btn')];
  const ts=bs.map(b=>b.getAttribute('title')||'');
  const seg=[...document.querySelectorAll('.explorer-seg-btn')]
    .some(s=>/扩展|Extensions/.test(s.getAttribute('title')||''));
  return JSON.stringify({n:bs.length, ts, kb:ts.some(t=>/知识库|Knowledge/.test(t)), seg})})()"""))
        c.check("T1b toolbar 无知识库按钮（title 定位；入口在 filetree「扩展」分段）",
                (not kbo["kb"]) and kbo["n"] == 4 and kbo["seg"], repr(kbo))

        # T2 打开菜单：open-btn 存在；点击会弹系统目录对话框（模态），测试不触发
        c.check("T2 打开菜单按钮存在",
                J(gui, "!!document.querySelector('.open-btn')"),
                "OpenDirDialog 桥已实现（弹系统对话框），自动化不触发点击")

        # T3 最近项目下拉
        gui.click(".dropdown-arrow")
        time.sleep(0.5)
        c.check("T3 最近项目下拉出现", J(gui, "!!document.querySelector('.b-dropdown-popper')"),
                "getRecentDirs 桥未实现 → 空列表展示空态")
        if J(gui, "!!document.querySelector('.b-dropdown-popper')"):
            c.check("T3b 最近项目空态提示", J(gui, "!!document.querySelector('.b-dropdown-item.is-disabled')"))
        gui.click(".dropdown-arrow")

        # T4 用户配置：toolbar-left Settings 按钮（按 title「设置/Settings」定位）→ 下拉菜单首项
        # （settings-llm）→ preview 打开 LLM 配置页签（Toolbar settingsItems[0]=settings-llm）
        gui.console(clear=True)
        J(gui, left_btn_js("设置|Settings"))
        time.sleep(0.6)
        J(gui, "document.querySelector('.b-dropdown-popper .b-dropdown-item')?.click(); 'ok'")
        time.sleep(1.0)
        c.check("T4 用户配置打开 preview 页签",
                J(gui, "!!document.querySelector('.code-view .special-tab.settings-page')"),
                "settingsMenu(settings-llm) → previewTabOpen(settings-llm) → CodeView 渲染 LLM 配置页")

        # T5 场景配置：toolbar-left Scenarios 按钮（按 title「场景/Scenarios」定位）→ preview 场景页签
        gui.console(clear=True)
        J(gui, left_btn_js("场景|Scenarios"))
        time.sleep(1.0)
        c.check("T5 场景配置打开 preview 页签",
                J(gui, "!!document.querySelector('.code-view .special-tab.dialog-content')"),
                "scenarioOpen → previewTabOpen(scenario) → CodeView 渲染场景 tab")

        # T6 检索框
        gui.input(".search-input-wrap input", "a")
        time.sleep(0.9)
        n = J(gui, "document.querySelectorAll('.search-result-item').length")
        c.check("T6 检索框输入出现下拉备选", n > 0, f"results={n}（SearchProjectFiles 桥未实现 → 无结果）— 待实施")
        gui.input(".search-input-wrap input", "")
        time.sleep(0.3)

        # T7 主题：点击 theme → 选 dark → 生效并保存
        gui.eval("document.querySelectorAll('.toolbar-right .tb-btn')[0]?.click(); 'ok'")
        time.sleep(0.5)
        c.check("T7a 主题下拉出现", J(gui, "!!document.querySelector('.b-dropdown-popper')"))
        J(gui, """(()=>{for(const it of document.querySelectorAll('.b-dropdown-item')){if(/Dark|暗|深/.test(it.textContent)){it.click();return true}}return false})()""")
        time.sleep(0.5)
        theme = J(gui, "document.documentElement.getAttribute('data-theme')")
        c.check("T7b 主题选择生效并保存", theme == "dark", repr(theme))
        # T7c A 落库回读（P6 复核补齐）：`theme` 属 usr 主库（bridge/local.go:207 callSaveUIState
        # → data-user-config-save）——原用例只断 DOM `data-theme`，无落库回读。
        usr = (gui.req("data-user-config-load", {}) or {}).get("data") or {}
        c.check("T7c 主题落库回读（usr theme=dark）", usr.get("theme") == "dark",
                f"theme={usr.get('theme')!r}")

        # T8 语言：点击 lang → 选 English → 生效
        gui.eval("document.querySelectorAll('.toolbar-right .tb-btn')[1]?.click(); 'ok'")
        time.sleep(0.5)
        J(gui, """(()=>{for(const it of document.querySelectorAll('.b-dropdown-item')){if(/English/.test(it.textContent)){it.click();return true}}return false})()""")
        time.sleep(0.5)
        lang = J(gui, "localStorage.getItem('chonkpilot-locale') || ''")
        c.check("T8 语言切换生效（localStorage locale）", lang == "en-US", repr(lang))
        # T8b A 落库回读（P6 复核补齐）：`locale` 属 usr 主库（同 T7c 链路）——原用例只断
        # localStorage（本实例专属 WebView2 profile）与 DOM 文案，无落库回读。
        usr2 = (gui.req("data-user-config-load", {}) or {}).get("data") or {}
        c.check("T8b 语言落库回读（usr locale=en-US）", usr2.get("locale") == "en-US",
                f"locale={usr2.get('locale')!r}")

        # T9 会话入口（第 3 个按钮）→ 左侧导航切到「会话」页签（P3-C1：原会话抽屉已迁入左侧导航）
        gui.eval("document.querySelectorAll('.toolbar-right .tb-btn')[2]?.click(); 'ok'")
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

        # T10-12 面板显示切换（chat=5 filetree=4 task=3）
        def toggle(idx):
            gui.eval(f"document.querySelectorAll('.toolbar-right .tb-btn')[{idx}]?.click(); 'ok'")
            time.sleep(0.6)

        toggle(5)
        c.check("T10 chat 切换（隐藏）", J(gui, "!document.querySelector('.chat-panel')"), "chat 面板隐藏")
        toggle(5)
        c.check("T10b chat 切换（恢复）", J(gui, "!!document.querySelector('.chat-panel')"), "chat 面板恢复")

        toggle(4)
        c.check("T11 filetree 切换（隐藏）", J(gui, "!document.querySelector('.filetree-panel')"))
        toggle(4)
        c.check("T11b filetree 切换（恢复）", J(gui, "!!document.querySelector('.filetree-panel')"))

        # T12 task 面板（下标 3）：默认**收起**（2026-09-27 首屏减负：taskOpen 默认 false）
        # → 点一次展开 → 再点收起（顶部「任务」开关的切换语义不变）。
        c.check("T12a task 区默认收起（SessionChat 未挂载）",
                J(gui, "!document.querySelector('.session-chat')"), "默认收起")
        toggle(3)
        c.check("T12 task 切换（展开）", J(gui, "!!document.querySelector('.session-chat')"),
                "点顶部「任务」开关 → task 区（SessionChat）展开")
        toggle(3)
        c.check("T12b task 切换（收起）", J(gui, "!document.querySelector('.session-chat')"))

        # ── statusbar（并入本组）──
        c.check("S1 statusbar 存在（config/lang 区）",
                J(gui, "!!document.querySelector('.statusbar')"),
                "statusbar 存在")
        c.check("S2 statusbar 语言/配置区存在",
                J(gui, "document.querySelectorAll('.statusbar .sb-section').length >= 1"))
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

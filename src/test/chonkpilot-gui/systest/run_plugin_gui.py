# -*- coding: utf-8 -*-
"""L4 套件：插件桥 GUI 动作面逐项验收（BRIDGE-2 ⑤）。

链路：测试插件（UDS 帧客户端）→ `plugin/v1/gui.list` / `plugin/v1/gui.apply`（宿主
`pluginGUIController`）→ 目标窗口（backend action = PublishEvent / 窗口面直调 /
纯前端事件 = EmitFrontend）。

覆盖（真机 DOM / 落库断言，拒绝"调用成功即算过"）：
  P0 `gui.list` 返回当前窗口实例
  P1 窗口：`gui.window.status{maximize}` → 标题「还原」；再执行 → 「最大化」
  P2 页面：`preview-tab-open{kind:settings-llm}` → 预览区 LLM 配置页签
  P3 偏好：`theme-select{themeId:dark}` → data-theme=dark + usr theme 落库
  P4 偏好：`lang-select{lang:en-US}` → localStorage locale
  P5 抽屉：布局域动作 `runtime.toggleDrawer('drawer_task')`（G-89 口径）→ `.drawer` 开（标题
     「任务详情」+ `task-detail` 视图槽）；再执行 → 关；布局面板与视图池保活不重建
  P6 布局后端动作：`gui.ui.save{ui:{locale:en-US}}` → usr locale 落库
  P7 未知动作：明确报错（不得静默）
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402
from plugin_client import PluginClient, wait_socket  # noqa: E402

_g = None
_cli = None
_inst = ""


def deep_loads(v):
    """`/eval` 返回的是 JSON 文本（字符串多层包裹）→ 逐层解到原生值再比对。"""
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def main():
    global _g, _cli, _inst
    _g = _h.start_gui(port=_h.free_port(),
                      work_dir=_h.tmp_dir("ck-plugui-ws-"),
                      data_dir=_h.tmp_dir("ck-plugui-dd-"),
                      home=_h.tmp_home(),
                      plugin_addr="tcp://127.0.0.1:%d" % _h.free_port())
    c = _g.client
    _h.suite_config_guard(c)
    _h.ensure_locale(c, "zh-CN")
    print("[env] gui=%d plugin=%s" % (_g.port, _g.plugin_uds), flush=True)

    wait_socket(_g.plugin_uds, timeout=30)
    _cli = PluginClient(_g.plugin_uds, timeout=30)
    res = _cli.hello("acme.test.plugin")
    if not res.get("ok"):
        raise TestError("plugin.hello 失败: %r" % (res,))

    def apply(action, payload=None):
        return _cli.call("plugin/v1/gui.apply", {
            "instance_id": _inst, "action": action,
            "payload": json.dumps(payload or {}, ensure_ascii=False),
        })

    def apply_ok(action, payload=None):
        f = apply(action, payload)
        if not f.get("ok"):
            raise TestError("%s 调用失败: %r" % (action, f.get("errors")))
        body = f.get("result") or {}
        if isinstance(body, dict) and body.get("ok") is False:
            raise TestError("%s 返回失败: %r" % (action, body))
        return body

    def wait_js(js, want, max_wait=15, desc=""):
        deadline = time.time() + max_wait
        last = None
        while time.time() < deadline:
            last = deep_loads(c.eval(js, 6000))
            if last == want:
                return last
            time.sleep(0.3)
        raise TestError("等待 %s 超时：last=%r want=%r" % (desc or js, last, want))

    def case_p0_list():
        global _inst
        f = _cli.call("plugin/v1/gui.list", {})
        if not f.get("ok"):
            raise TestError("gui.list 失败: %r" % (f,))
        body = f.get("result") or {}
        insts = body.get("instances") or []
        if not insts:
            raise TestError("gui.list 无实例：%r" % (body,))
        _inst = insts[0]
        return "instances=%r" % insts

    def case_p1_window():
        apply_ok("gui.window.status", {"command": "maximize"})
        t1 = wait_js("document.querySelector('.win-maximize')?.getAttribute('title') || ''",
                     "还原", desc="最大化标题")
        apply_ok("gui.window.status", {"command": "maximize"})  # 切换 = 还原
        t2 = wait_js("document.querySelector('.win-maximize')?.getAttribute('title') || ''",
                     "最大化", desc="还原标题")
        return "%s/%s" % (t1, t2)

    def case_p2_page():
        apply_ok("preview-tab-open", {"kind": "settings-llm"})
        wait_js("!!document.querySelector('.code-view .special-tab.settings-page')", True,
                desc="LLM 配置页签")
        return "settings-page"

    def case_p3_theme():
        apply_ok("theme-select", {"themeId": "dark"})
        wait_js("document.documentElement.getAttribute('data-theme')", "dark", desc="data-theme")
        deadline = time.time() + 10
        theme = None
        while time.time() < deadline:
            usr = (c.req("data-user-config-load", {}) or {}).get("data") or {}
            theme = usr.get("theme")
            if theme == "dark":
                break
            time.sleep(0.3)
        if theme != "dark":
            raise TestError("usr theme 未落库：%r" % (theme,))
        return "data-theme/usr=dark"

    def case_p4_lang():
        apply_ok("lang-select", {"lang": "en-US"})
        wait_js("localStorage.getItem('chonkpilot-locale') || ''", "en-US", desc="locale")
        return "en-US"

    def case_p5_drawer():
        # G-89（2026-10-31）：`lf-*` MQ 主题已删除（宿主零订阅者）→ 抽屉开/关 = 布局域动作。
        # 驱动与 `PluginManagerPage.vue#layoutActions().toggleDrawer` / `window.__ckLayout.actions`
        # 同一执行体（`layout-free/stores/runtime.js#toggleDrawer`；actions.js 只是薄转发）。
        # 期望值（31-窗口与布局 WIN-027 + 默认布局文档 drawers[0]）：
        #   drawer_task → 标题「任务详情」、内容视图 `task-detail`；关闭后抽屉子树移除，
        #   但布局区块仍挂载、视图池实例不重建（mounts 不变 = 保活）。
        mounts0 = deep_loads(c.eval("JSON.stringify(window.__ckViewPool.mounts)", 6000)) or {}
        c.eval("window.__ckLayout.engine.state.runtime.toggleDrawer('drawer_task'); 'ok'", 6000)
        wait_js("document.querySelectorAll('.drawer').length", 1, desc="抽屉打开")
        snap = deep_loads(c.eval("""(()=>{const d=document.querySelector('.drawer');
          return JSON.stringify({ttl:(d&&(d.querySelector('.drawer-head .ttl')||{}).textContent)||'',
            slots:d?[...d.querySelectorAll('.lf-view-slot')].map(x=>x.getAttribute('data-view-key')):[],
            empty:d?!!d.querySelector('.drawer-empty'):null})})()""", 6000)) or {}
        if snap.get("ttl") != "任务详情":
            raise TestError("抽屉标题不符：%r（want='任务详情'）" % (snap.get("ttl"),))
        if "task-detail" not in (snap.get("slots") or []):
            raise TestError("抽屉内容视图不符：%r（want=task-detail）" % (snap.get("slots"),))
        if snap.get("empty"):
            raise TestError("抽屉为空态（应承载 task-detail 视图）")
        mounts_open = deep_loads(c.eval("JSON.stringify(window.__ckViewPool.mounts)", 6000)) or {}

        c.eval("window.__ckLayout.engine.state.runtime.toggleDrawer('drawer_task'); 'ok'", 6000)
        wait_js("document.querySelectorAll('.drawer').length", 0, desc="抽屉关闭")
        wait_js("document.querySelectorAll('.drawer-overlay').length", 0, desc="抽屉蒙板关闭")
        mounts1 = deep_loads(c.eval("JSON.stringify(window.__ckViewPool.mounts)", 6000)) or {}
        if mounts1.get("task-detail") != mounts_open.get("task-detail"):
            raise TestError("关闭抽屉后 view 实例被重建/卸载：%r -> %r"
                            % (mounts_open.get("task-detail"), mounts1.get("task-detail")))
        panels = deep_loads(c.eval("""(()=>{return JSON.stringify({
          chat:!!document.querySelector('.chat-panel'), tree:!!document.querySelector('.filetree-panel'),
          leaves:document.querySelectorAll('.layout-leaf').length})})()""", 6000)) or {}
        if not (panels.get("chat") and panels.get("tree") and panels.get("leaves") == 5):
            raise TestError("关闭抽屉后布局面板未保持挂载：%r" % (panels,))
        return "open/close（title=任务详情 viewKey=task-detail；mounts 保活）"

    def case_p6_layout_save():
        apply_ok("gui.ui.save", {"ui": {"locale": "en-US"}})
        usr = (c.req("data-user-config-load", {}) or {}).get("data") or {}
        if usr.get("locale") != "en-US":
            raise TestError("gui.ui.save 未落库 locale：%r" % (usr.get("locale"),))
        return "usr locale=en-US"

    def case_p7_unknown():
        f = apply("no-such-action", {})
        if f.get("ok"):
            raise TestError("未知动作应报错（不得静默）：%r" % (f,))
        errs = " ".join(f.get("errors") or [])
        if "未知 GUI action" not in errs:
            raise TestError("错误文案不符：%r" % (errs,))
        return errs[:80]

    total = 0
    ok = 0
    cases = [
        ("P0 gui.list 返回当前窗口实例", case_p0_list),
        ("P1 窗口控制：maximize/还原（插件直驱）", case_p1_window),
        ("P2 页面：preview-tab-open → LLM 配置页签", case_p2_page),
        ("P3 主题：theme-select → DOM + 落库", case_p3_theme),
        ("P4 语言：lang-select → localStorage", case_p4_lang),
        ("P5 抽屉：布局域 toggleDrawer 开/关（标题/视图 + 保活）", case_p5_drawer),
        ("P6 布局后端动作：gui.ui.save 落库", case_p6_layout_save),
        ("P7 未知动作明确报错", case_p7_unknown),
    ]
    for name, fn in cases:
        total += 1
        ok += run_case(name, fn)
    print("\n插件桥 GUI 动作面：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
    print("RESULT:", ok == total)
    return 0 if ok == total else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    finally:
        if _cli is not None:
            _cli.close()

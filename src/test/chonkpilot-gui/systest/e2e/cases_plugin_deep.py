# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉘：**插件配置页 / 主题同步 / 禁用-启用功能面**（迁移自
test_plugin_config_e2e A/B/C 的核心机制）。

* A0：plugin-list 全部 running；
* B0/B1：所有带 config_view 的插件（≥9）配置页 iframe 可加载且控件/文案非空；
* A2/B2：dark 主题下发到插件 iframe（data-theme=dark，body 背景 = 宿主背景），light 还原；
* A3/A4：插件卡片背景随主题变化、正文对比度 ≥ 4.5；
* C1–C8（代表插件）：web 关闭 → 卡片 is-off + plugin disabled/stopped + HEAD 404 + RPC 失败 +
  禁用态配置页「插件未就绪」占位；重新启用 → running + HEAD 200；file 关闭 → 工具面移除，
  启用 → 工具恢复；启用态以宿主 DB plugin_state（plugin-list.enabled）为准。
"""

import json
import os
import time

from .framework import case
from .cases_plugin_ops import _find, _open_manager, _plugins

CARD_EXPR = lambda pid: ("[...document.querySelectorAll('.pm-card')].find(c=>"
                         "((c.querySelector('.pm-name')||{}).textContent||'').indexOf(%s)>=0)"
                         % json.dumps(pid))


def _plugin(env, pid):
    return next((p for p in _plugins(env) if p.get("id") == pid), None)


def _wait_plugin(env, pid, statuses, timeout=90):
    return env.wait(lambda: (lambda p: p if p and p.get("status") in statuses else None)(
        _plugin(env, pid)), "插件 %s 状态 %s" % (pid, statuses), timeout=timeout)


def _click_switch(env, pid):
    name = (_plugin(env, pid) or {}).get("name") or pid
    ex = _find(name)
    def _try():
        r = env.js("(()=>{const c=%s;if(!c)return 'no-card';"
                   "const s=c.querySelector('.pm-switch .b-switch');if(!s)return 'no-switch';"
                   "if(s.classList.contains('is-disabled'))return 'busy';s.click();return 'ok';})()" % ex)
        return True if r == "ok" else None
    return env.wait(_try, "点击 %s 启用开关" % pid, timeout=30)


def _card_off(env, pid):
    name = (_plugin(env, pid) or {}).get("name") or pid
    return env.js("%s?.classList.contains('is-off')" % _find(name)) is True


def _head(env, pid, entry="webui/config.html"):
    env.js("window.__head=null;fetch('/plugins/%s/%s',{method:'HEAD',cache:'no-store'})"
           ".then(r=>window.__head=r.status).catch(()=>window.__head=-1);'ok'" % (pid, entry))
    v = env.wait(lambda: env.js("String(window.__head)") if str(
        env.js("String(window.__head)") or "") not in ("", "None", "null", "undefined") else None,
        "HEAD /plugins/%s/%s" % (pid, entry), timeout=15)
    try:
        return int(v)
    except (TypeError, ValueError):
        return None


def _wait_head(env, pid, want, timeout=60):
    end = time.time() + timeout
    st = None
    while time.time() < end:
        st = _head(env, pid)
        if st == want:
            return st
        time.sleep(1)
    return st


def _rpc_ok(env, pid):
    try:
        r = env.req("plugin-call", {"id": pid, "method": "rpc",
                                    "params": {"method": "ping", "params": {}}}) or {}
        return not (r.get("ok") is False)
    except Exception:
        return False


def _tools_names(env):
    raw = env.req("tools-list", {}) or {}
    return {t.get("name") or "" for t in (raw.get("tools") or []) if isinstance(t, dict)}


def _iframe(env, pid, entry="webui/config.html"):
    return env.jsj("""JSON.stringify((()=>{const fs=[...document.querySelectorAll('iframe.pph-frame')]
      .filter(f=>(f.getAttribute('src')||'').indexOf('/plugins/%s/')>=0
                  && (f.getAttribute('src')||'').indexOf(%s)>=0);
      const f=fs[fs.length-1];if(!f)return {found:false};
      const d=f.contentDocument;if(!d||!d.body)return {found:false,inaccessible:true};
      const hostBg=getComputedStyle(document.documentElement).backgroundColor;
      return {found:true,themeAttr:d.documentElement.getAttribute('data-theme')||'',
              ckTheme:(d.documentElement.getAttribute('data-ck-theme')||''),
              bodyBg:getComputedStyle(d.body).backgroundColor,hostBg,
              controls:d.querySelectorAll('input,button,select,textarea').length,
              text:(d.body.textContent||'').trim().slice(0,200),
              overflowX:Math.max(0,d.documentElement.scrollWidth-d.documentElement.clientWidth)};})())"""
      % (pid, json.dumps(entry)))


def _set_theme(env, theme):
    env.req("gui.ui.save", {"ui": {"theme": theme}})
    return env.wait(lambda: True if env.js(
        "document.documentElement.getAttribute('data-theme')") == theme else None,
        "主题 %s 生效" % theme, timeout=20)


def _enabled_flag(env, pid):
    """插件启用态**权威 = 宿主 DB `plugin_state`**（`plugin-list` 视图 `enabled` 字段）。

    2026-11-02 起启用态不再写 `DataDir/plugins/enabled.json`（见 mgr.setEnabled：
    "权威 = plugin_state（宿主 DB）；不再写 DataDir/plugins/enabled.json"）。
    """
    p = _plugin(env, pid)
    return None if p is None else bool(p.get("enabled"))


def _wait_flag(env, pid, want, timeout=20):
    end = time.time() + timeout
    v = None
    while time.time() < end:
        v = _enabled_flag(env, pid)
        if v is want:
            return v
        time.sleep(0.5)
    return v


@case("S-PLUG-DEEP-01", "插件平台", "插件配置页加载/主题同步/卡片对比度/禁用-启用功能面与占位",
      stage="seeded", order=396, fp="42 插件平台 · test_plugin_config_e2e A/B/C",
      desc="全部插件 running；≥9 个配置页 iframe 加载（控件/文案非空）；dark 下发 iframe 且背景=宿主、"
           "卡片背景随主题变化且正文对比度≥4.5；web 禁用 → is-off/disabled/HEAD404/RPC失败/占位，"
           "启用 → running/HEAD200；file 禁用 → 工具面移除，启用 → 恢复；启用态随 plugin-list.enabled 变化。")
def s_plug_deep_01(env):
    def _all_running():
        rows = _plugins(env)
        return rows if rows and all(p.get("status") == "running" for p in rows) else None
    try:
        ps = env.wait(_all_running, "全部插件 running", timeout=30)
    except Exception:
        # 前置自愈：前序测试点可能留下 disabled/stopped 的插件（如开关往返未收敛）→
        # 显式恢复启用并等待 running；仍失败才判 A0。
        for p in _plugins(env):
            if p.get("status") != "running":
                try:
                    env.req("plugin-set-enabled", {"id": p.get("id"), "enabled": True})
                except Exception:
                    pass
        ps = env.wait(_all_running, "全部插件 running（自愈重启用后）", timeout=120)
    env.check("插件台账非空", bool(ps), len(ps))
    not_running = [(p.get("id"), p.get("status")) for p in ps if p.get("status") != "running"]
    env.check("A0 全部插件 running", not not_running, not_running[:6])
    by_id = {p.get("id"): p for p in ps}
    cfg_pids = [p.get("id") for p in ps if p.get("config_view")]
    env.check("B0 有配置页的插件 ≥9", len(cfg_pids) >= 9, cfg_pids)

    initial = {p.get("id"): bool(p.get("enabled")) for p in ps}
    initial_theme = env.usr().get("theme")
    try:
        # B1 所有配置页 iframe 加载
        _open_manager(env)
        bad = []
        for pid in cfg_pids:
            env.js("window.mq.emit('preview-tab-open', {kind:'plugin-page', plugin:%s, "
                   "page:'config', title:%s}); 'ok'"
                   % (json.dumps(pid), json.dumps(pid + " 配置")))
            def _loaded():
                v = _iframe(env, pid)
                return v if (v.get("found") and (int(v.get("controls") or 0) >= 1
                                                 or len(v.get("text") or "") >= 10)) else None
            try:
                rep = env.wait(_loaded, "插件 %s 配置页 iframe" % pid, timeout=35)
            except Exception:
                rep = _iframe(env, pid)
            if int(rep.get("controls") or 0) < 1 and len(rep.get("text") or "") < 10:
                bad.append((pid, rep.get("controls"), (rep.get("text") or "")[:30]))
            env.js("window.mq.emit('preview-tab-close-all', {}); 'ok'")
            time.sleep(0.3)
        env.check("B1 全部配置页 iframe 加载（控件/文案非空）", not bad, bad[:5])

        # A2/B2 主题下发 iframe（代表插件 codegraph）+ 卡片主题/对比度
        _open_manager(env)
        card_theme = {}
        for theme in ("light", "dark"):
            _set_theme(env, theme)
            time.sleep(1.0)
            card_theme[theme] = env.jsj("""JSON.stringify((()=>{const c=document.querySelector('.pm-card');
              if(!c)return null;const n=c.querySelector('.pm-name')||c;
              const bg=getComputedStyle(c).backgroundColor;
              const fg=getComputedStyle(n).color;
              const rgb=s=>(s.match(/\\d+/g)||[0,0,0]).slice(0,3).map(Number);
              const lum=([r,g,b])=>{const f=v=>{v/=255;return v<=0.03928?v/12.92:Math.pow((v+0.055)/1.055,2.4)};
                return 0.2126*f(r)+0.7152*f(g)+0.0722*f(b);};
              const L1=lum(rgb(fg)),L2=lum(rgb(bg));
              return {bg,fg,contrast:(Math.max(L1,L2)+0.05)/(Math.min(L1,L2)+0.05)};})())""") or {}
        env.check("A3 卡片背景随主题变化",
                  card_theme.get("dark", {}).get("bg") != card_theme.get("light", {}).get("bg"),
                  card_theme)
        env.check("A4 卡片正文对比度 ≥4.5（两主题）",
                  all(float(v.get("contrast") or 0) >= 4.5 for v in card_theme.values()),
                  {k: v.get("contrast") for k, v in card_theme.items()})

        if "codegraph" in by_id:
            env.js("window.mq.emit('preview-tab-open', {kind:'plugin-page', plugin:'codegraph', "
                   "page:'config', title:'codegraph 配置'}); 'ok'")
            env.wait(lambda: True if _iframe(env, "codegraph").get("found") else None,
                     "codegraph 配置页就绪", timeout=35)
            dark = env.wait(lambda: (lambda v: v if v.get("themeAttr") == "dark" else None)(
                _iframe(env, "codegraph")), "iframe 收到 dark 主题", timeout=20)
            env.check("B2 插件页主题同步 dark + 背景=宿主",
                      dark.get("themeAttr") == "dark" and dark.get("bodyBg") == dark.get("hostBg"),
                      {k: dark.get(k) for k in ("themeAttr", "bodyBg", "hostBg")})
            env.js("window.mq.emit('preview-tab-close-all', {}); 'ok'")
        else:
            env.note("codegraph 缺失", "B2 iframe 主题同步未验")

        _set_theme(env, "light")

        # C1–C8：web 禁用/启用
        if "web" in by_id:
            _open_manager(env)
            env.check("点击 web 开关（关）", _click_switch(env, "web"))
            env.wait(lambda: True if _card_off(env, "web") else None, "web 卡片 is-off", timeout=60)
            off = _wait_plugin(env, "web", {"disabled", "stopped"}, timeout=90)
            env.check("C1 web 禁用 → 卡片 is-off + 插件 disabled/stopped",
                      _card_off(env, "web") and bool(off),
                      (off or {}).get("status"))
            env.check("C1b plugin-list.enabled web=false", _wait_flag(env, "web", False) is False,
                      _enabled_flag(env, "web"))
            env.check("C3 web 配置资源 HEAD 404", _wait_head(env, "web", 404, timeout=30) == 404)
            rpc = False
            for _ in range(20):
                rpc = _rpc_ok(env, "web")
                if not rpc:
                    break
                time.sleep(0.5)
            env.check("C4 web 禁用后 RPC 失败", rpc is False, rpc)

            # 禁用态配置页占位（全新挂载）
            env.js("window.mq.emit('preview-tab-close-all', {}); 'ok'")
            time.sleep(0.5)
            _open_manager(env)
            env.js("(()=>{const c=%s;const b=[...c.querySelectorAll('.pm-btn')]"
                   ".find(x=>(x.textContent||'').indexOf('配置')>=0);if(b)b.click();return 'ok';})()"
                   % _find(by_id["web"].get("name") or "web"))
            ph = env.wait(lambda: env.jsj("""JSON.stringify({ph:document.querySelectorAll(
              '.pph-placeholder').length,txt:(document.querySelector('.pph-placeholder')||{}).innerText||''})""")
              if int(env.js("document.querySelectorAll('.pph-placeholder').length") or 0) >= 1
              else None, "禁用态配置页占位", timeout=30)
            env.check("C8 禁用后配置页 →「插件未就绪」占位",
                      "未就绪" in str(ph.get("txt") or ""), (ph.get("txt") or "")[:60])

            env.check("点击 web 开关（开）", _click_switch(env, "web"))
            on = _wait_plugin(env, "web", {"running"}, timeout=120)
            env.check("C5 web 启用 → running", bool(on), (on or {}).get("status"))
            env.check("C5b plugin-list.enabled web=true", _wait_flag(env, "web", True) is True,
                      _enabled_flag(env, "web"))
            env.check("C7 web 配置资源 HEAD 200", _wait_head(env, "web", 200, timeout=60) == 200)
            env.js("window.mq.emit('preview-tab-close-all', {}); 'ok'")

        # C2/C6：file 禁用 → 工具面移除；启用 → 恢复
        file_p = by_id.get("file") or {}
        want_tools = [t for t in (file_p.get("tools") or []) if isinstance(t, str)]
        if want_tools:
            _open_manager(env)
            env.check("点击 file 开关（关）", _click_switch(env, "file"))
            env.wait(lambda: True if not any(t in _tools_names(env) for t in want_tools) else None,
                     "file 禁用后工具面移除", timeout=90)
            env.check("C2 file 禁用后工具面移除", not any(t in _tools_names(env) for t in want_tools),
                      want_tools[:4])
            env.check("点击 file 开关（开）", _click_switch(env, "file"))
            env.wait(lambda: True if all(t in _tools_names(env) for t in want_tools) else None,
                     "file 启用后工具面恢复", timeout=120)
            env.check("C6 file 启用后工具面恢复", all(t in _tools_names(env) for t in want_tools))
        else:
            env.note("file 插件无 tools 清单", want_tools)
    finally:
        # 恢复启用表与主题
        for pid, en in initial.items():
            cur = _plugin(env, pid) or {}
            if bool(cur.get("enabled")) != en:
                try:
                    env.req("plugin-set-enabled", {"id": pid, "enabled": en})
                except Exception:
                    pass
        if initial_theme:
            _set_theme(env, initial_theme)
        env.js("window.mq.emit('preview-tab-close-all', {}); 'ok'")

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("C9 插件深度组无前端错误", len(errs) == 0, errs[:3])
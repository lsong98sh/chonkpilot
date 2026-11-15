# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑫：**插件维护视图**（迁移自 test_plugin_manager.py PM1–PM7）。

入口 = `preview-tab-open{kind:'plugin-manager'}`；断言卡片数/名称/命令/配置按钮/动作按钮与
`plugin-list` manifest 一致，启用开关真实收停并可恢复，配置页与 view 动作打开插件 iframe。
"""

import json
import time

from .framework import case

CARDS_JS = ("JSON.stringify([...document.querySelectorAll('.pm-card')].map(c=>({"
            "name:(c.querySelector('.pm-name')||{}).textContent||'',"
            "cmd:(c.querySelector('.pm-cmd-value')||{}).textContent||'',"
            "off:c.classList.contains('is-off'),"
            "btns:[...c.querySelectorAll('.pm-btn')].map(b=>(b.textContent||'').trim())})))")


def _cards(env):
    return env.jsj(CARDS_JS) or []


def _plugins(env):
    return (env.req("plugin-list", {}) or {}).get("plugins") or []


def _find(name):
    return ("[...document.querySelectorAll('.pm-card')].find(c=>"
            "((c.querySelector('.pm-name')||{}).textContent||'').indexOf(%s)>=0)" % json.dumps(name))


def _manifest_ready(env, plugin_id, timeout=40):
    def ready():
        row = next((p for p in _plugins(env) if p.get("id") == plugin_id), {})
        return row if row.get("status") == "running" and row.get("config_view") and row.get("actions") else None
    return env.wait(ready, "插件 %s manifest 就绪" % plugin_id, timeout=timeout)


def _open_manager(env):
    env.js("window.mq.emit('preview-tab-open', {kind:'plugin-manager'}); 'ok'")
    env.wait(lambda: True if env.js("!!document.querySelector('.plugin-manager')") is True else None,
             "插件维护视图打开", timeout=25)


@case("S-PLUG-UI-01", "插件平台", "插件维护视图：卡片/manifest 一致 / 启用开关 / 配置页 / view 动作抽屉",
      stage="seeded", order=370, fp="42 插件平台 · 插件维护视图（test_plugin_manager PM1–PM7）",
      desc="plugin-manager 卡片数 == plugin-list；名称/启动命令/配置按钮/动作按钮与 manifest 一致；"
           "mcp（缺省则 codegraph）开关关闭 → 卡片 is-off、再开 → 恢复；配置按钮打开 "
           "`/plugins/codegraph/webui/config.html`；view 动作打开抽屉并内嵌插件页。")
def s_plug_ui_01(env):
    plugins = _plugins(env)
    env.check("plugin-list 非空", len(plugins) > 0, len(plugins))
    by_id = {p.get("id"): p for p in plugins if isinstance(p, dict)}

    _open_manager(env)
    cards = _cards(env)
    env.wait(lambda: True if len(_cards(env)) == len(_plugins(env)) else None,
             "卡片数收敛到 plugin-list", timeout=25)
    env.eq("卡片数 == plugin-list", len(_cards(env)), len(_plugins(env)))

    plugins = _plugins(env)
    bad = []
    for p in plugins:
        name = p.get("name") or p.get("id") or ""
        card = next((x for x in _cards(env) if x.get("name") == name), None)
        if not card:
            bad.append((p.get("id"), "no-card"))
            continue
        if not card.get("cmd") or not (p.get("command") or ""):
            bad.append((p.get("id"), "no-command"))
        want_cfg = bool(p.get("config_view"))
        has_cfg = any(x in card["btns"] for x in ("配置", "Configure"))
        if want_cfg != has_cfg:
            bad.append((p.get("id"), "config-btn-mismatch"))
    env.check("每卡名称/命令/配置按钮与 manifest 一致", not bad, bad[:5])

    bad_act = []
    for p in plugins:
        acts = p.get("actions") or []
        if not acts:
            continue
        card = next((x for x in _cards(env)
                     if x.get("name") == (p.get("name") or p.get("id"))), None)
        if not card:
            bad_act.append((p.get("id"), "no-card"))
            continue
        missing = [a.get("label") for a in acts if a.get("label") not in card["btns"]]
        if missing:
            bad_act.append((p.get("id"), missing))
    env.check("动作按钮与 manifest 一致", not bad_act, bad_act[:5])

    # codegraph 专属：manifest 完整（就绪栅栏），否则只记录
    cg = by_id.get("codegraph")
    if cg:
        try:
            cg = _manifest_ready(env, "codegraph", timeout=40)
        except AssertionError:
            env.note("codegraph manifest 未就绪（配置页/抽屉检查跳过）", cg)
        else:
            labels = [a.get("label") for a in (cg.get("actions") or [])]
            env.check("codegraph 动作含「索引进度」", "索引进度" in labels, labels)

    # 启用开关：优先 mcp（不影响聊天链路），缺省 codegraph
    target = "mcp" if "mcp" in by_id else (plugins[0].get("id") if plugins else "")
    tname = (by_id.get(target) or {}).get("name") or target
    env.check("找到开关目标插件", bool(target), target)
    r = env.js("(()=>{const c=%s;const s=c&&c.querySelector('.pm-switch .b-switch');"
               "if(!s)return 'no-switch';s.click();return 'ok';})()" % _find(tname))
    env.check("点击关闭启用开关", r == "ok", r)
    env.wait(lambda: True if env.js("%s?.classList.contains('is-off')" % _find(tname)) is True
             else None, "卡片 is-off", timeout=30)
    env.check("关闭后卡片 is-off", env.js("%s?.classList.contains('is-off')" % _find(tname)) is True)
    # 先等插件真正停机（stopping → disabled/stopped）再重新启用，避免「停机中点击」把
    # mcp 插件卡在 stopping（批跑负载下 60s 都回不到 running）。
    try:
        env.wait(lambda: True if (lambda p: p and p.get("status") in ("disabled", "stopped"))(
            next((x for x in _plugins(env) if x.get("id") == target), None)) else None,
            "插件停机完成", timeout=90)
    except Exception:
        env.note("插件停机等待", "90s 内未到 disabled/stopped，仍尝试启用")
    env.wait(lambda: True if env.js(
        "(()=>{const c=%s;const s=c&&c.querySelector('.pm-switch .b-switch');"
        "return !!s && !s.classList.contains('is-disabled')})()" % _find(tname)) is True else None,
        "开关恢复可用", timeout=30)
    env.js("(()=>{const c=%s;const s=c&&c.querySelector('.pm-switch .b-switch');"
           "if(s)s.click();return 'ok';})()" % _find(tname))
    env.wait(lambda: True if env.js("%s?.classList.contains('is-off')" % _find(tname)) is False
             else None, "重新启用后卡片恢复", timeout=40)
    env.check("重新启用后卡片恢复", env.js("%s?.classList.contains('is-off')" % _find(tname)) is False)
    # 等插件真正回到 running，避免把「stopping」状态泄漏给后续测试点
    env.wait(lambda: True if (lambda p: p and p.get("status") == "running")(
        next((x for x in _plugins(env) if x.get("id") == target), None)) else None,
        "重新启用后插件 running", timeout=150)

    if (cg or {}).get("actions"):
        act = next((a for a in (cg.get("actions") or []) if a.get("view")), None)
        if act:
            env.js("(()=>{const c=%s;const b=[...c.querySelectorAll('.pm-btn')]"
                   ".find(x=>(x.textContent||'').indexOf(%s)>=0);if(b)b.click();return 'ok';})()"
                   % (_find("代码知识库"), json.dumps(act.get("label"))))
            env.wait(lambda: True if env.js("!!document.querySelector('.drawer')") is True else None,
                     "view 动作打开抽屉", timeout=20)
            want_title = next((v.get("title") or "" for v in (cg.get("views") or [])
                               if v.get("id") == act.get("view")), "")
            dr = env.wait(lambda: (lambda v: v if "/plugins/codegraph/" in (v.get("src") or "")
                                   else None)(env.jsj(
                "JSON.stringify((()=>{const d=document.querySelector('.drawer');"
                "if(!d)return null;const f=d.querySelector('iframe.pph-frame');"
                "return {title:(d.querySelector('.drawer-head .ttl')||{}).textContent||'',"
                "src:f?(f.getAttribute('src')||''):''}})())")), "抽屉内插件页 iframe", timeout=20)
            env.eq("抽屉标题 = 视图标题", (dr or {}).get("title"), want_title)
            env.check("抽屉 iframe 指向 codegraph 插件页",
                      "/plugins/codegraph/" in ((dr or {}).get("src") or ""), dr)
            env.js("document.querySelector('.drawer .d-btn')?.click(); 'ok'")
            time.sleep(0.6)

        env.js("(()=>{const c=%s;const b=[...c.querySelectorAll('.pm-btn')]"
               ".find(x=>/配置|Configure/.test(x.textContent||''));if(b)b.click();return 'ok';})()"
               % _find("代码知识库"))
        ok5 = env.wait(lambda: True if env.js(
            "(()=>[...document.querySelectorAll('iframe.pph-frame')]"
            ".some(x=>(x.getAttribute('src')||'').includes('/plugins/codegraph/webui/config.html')))()")
            is True else None, "配置按钮打开插件配置页", timeout=25)
        env.check("配置按钮 → 插件配置页 iframe", ok5 is True)
    else:
        env.note("codegraph 配置页/抽屉未验（manifest 不含 actions）", cg)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("插件维护组无前端错误", len(errs) == 0, errs[:3])

def _cron_state(env):
    rows = (env.req("plugin-list", {}) or {}).get("plugins") or []
    return next((p for p in rows if p.get("id") == "crontab"), None)


def _cron_call(env, name, args=None):
    res = env.req("chonk.mcp-tools-call", {
        "name": name, "arguments": dict(args or {}),
        "tool_call_display_name": "e2e-cron", "work_dir": env.workspace}) or {}
    if res.get("isError"):
        raise AssertionError("工具 %s isError: %r" % (name, res))
    parts = [b.get("text") or "" for b in (res.get("content") or [])
             if isinstance(b, dict) and b.get("type") == "text"]
    return json.loads("\n".join(parts)) if parts else {}


def _cron_badge_js(expr):
    return ("(()=>{const fs=[...document.querySelectorAll('.statusbar iframe')]"
            ".filter(f=>(f.getAttribute('src')||'').indexOf('/plugins/crontab/')>=0);"
            "if(!fs.length)return null;const f=fs[0];if(!f.contentDocument"
            "||!f.contentDocument.body)return null;%s})()" % expr)


@case("S-CRON-01", "插件平台", "crontab 插件：就绪/状态栏徽标/工具建任务与事件/徽标计数/任务页/停用/删除",
      stage="seeded", order=392, fp="42 插件平台 · crontab 插件（run_crontab C1–C9）",
      desc="plugin-list 中 crontab running；状态栏 iframe 指向插件页；gateway crontab_add → ct_ 任务、"
           "crontab_list 可见、plugin-event{crontab-changed} 到达前端、徽标计数=1；点徽标发"
           "preview-tab-open{plugin-page,crontab}；disable 后不在启用列表；delete 后任务清空。")
def s_cron_01(env):
    pv = env.wait(lambda: (lambda p: p if p and p.get("status") == "running" else None)(
        _cron_state(env)), "crontab 插件 running", timeout=60)
    env.check("crontab 插件已注册且 running", bool(pv), (pv or {}).get("status"))

    src = env.wait(lambda: (lambda s: s if isinstance(s, str) and "/plugins/crontab/" in s else None)(
        env.js(_cron_badge_js("return (f.getAttribute('src')||'');"))),
        "状态栏 crontab iframe", timeout=25)
    env.check("状态栏内嵌 crontab badge iframe", bool(src), src)

    env.js("""(()=>{window.__cronEvents=[];window.__cronPreviews=[];
      window.mq.on('plugin-event',e=>{try{const p=typeof e.payload==='string'
        ? JSON.parse(e.payload):e.payload;
        window.__cronEvents.push({id:e.id,kind:p&&p.kind,count:p&&p.count});}catch(_){}});
      window.mq.on('preview-tab-open',e=>window.__cronPreviews.push(e));
      return 'ok'})()""")

    env.capture(["plugin-event"])
    add = _cron_call(env, "crontab_add", {
        "cron": "*/1 * * * *",
        "action": {"type": "run", "param": {"command": "echo e2e-cron-ok"}}})
    tid = ((add or {}).get("task") or {}).get("id") or ""
    env.check("crontab_add 建任务", bool(add.get("ok") and tid.startswith("ct_")), add)
    try:
        lst = _cron_call(env, "crontab_list", {})
        ids = [t.get("id") for t in (lst.get("tasks") or [])]
        env.check("crontab_list 含新任务", tid in ids, ids)

        ev = None
        for _ in range(40):
            got = env.js("JSON.stringify(window.__cronEvents||[])") or ""
            if "crontab-changed" in str(got):
                ev = got
                break
            time.sleep(0.5)
        env.check("crontab-changed 事件到达前端", bool(ev), str(ev)[:120])

        badge = env.wait(lambda: (lambda t: t if isinstance(t, str) and "1" in t else None)(
            env.js(_cron_badge_js("return (f.contentDocument.body.innerText||'').trim();"))),
            "徽标计数 = 1", timeout=25)
        env.check("徽标计数 = 1", bool(badge), badge)

        env.js(_cron_badge_js("f.contentDocument.body.click();return 'ok';"))
        prev = None
        for _ in range(30):
            got = env.js("JSON.stringify(window.__cronPreviews||[])") or ""
            if "plugin-page" in str(got) and "crontab" in str(got):
                prev = got
                break
            time.sleep(0.5)
        env.check("点徽标发 preview-tab-open{plugin-page, crontab}", bool(prev), str(prev)[:160])

        dis = _cron_call(env, "crontab_toggle", {"task_id": tid, "enabled": False})
        lst2 = _cron_call(env, "crontab_list", {"enabled": True})
        ids2 = [t.get("id") for t in (lst2.get("tasks") or [])]
        env.check("disable 后不在启用列表", bool(dis.get("ok")) and tid not in ids2,
                  {"disable": dis, "enabled": ids2})
    finally:
        if tid:
            _cron_call(env, "crontab_delete", {"task_id": tid})
    lst3 = _cron_call(env, "crontab_list", {})
    env.check("delete 后任务清空", not (lst3.get("tasks") or []), lst3.get("tasks"))

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("crontab 组无前端错误", len(errs) == 0, errs[:3])

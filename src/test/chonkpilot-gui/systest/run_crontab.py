# -*- coding: utf-8 -*-
"""L4：crontab 插件端到端（51-FP与测试映射 `run_crontab.py`）。

口径（2026-10-31）：
- 驱动 = 既有 `chonk.mcp-tools-call`（6 个 `crontab_*` 工具）+ 插件动作面 `POST /plugins/crontab/api/*`；
- 断言 = 既有 `plugin-list` / `plugin-event` / `preview-tab-open`，**零新增消息主题**；
- 独立临时 work-dir + 独立数据目录（任务落 `<PLUGIN_DATA_DIR>/tasks.json`，零残留）。

C1 插件就绪 · C2 状态栏徽标 iframe · C3 工具建任务 · C4 list 可见 ·
C5 `crontab-changed` 事件（非轮询） · C6 徽标计数 · C7 点徽标开任务页 · C8 停用 · C9 删除归零。
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
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_crontab_data")
WS = tmp_dir("ck-crontab-ws-")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def wait_js(gui, js, ok, max_wait=15.0, interval=0.3):
    deadline = time.time() + max_wait
    v = J(gui, js)
    while not ok(v) and time.time() < deadline:
        time.sleep(interval)
        v = J(gui, js)
    return v


def has(v, needle):
    """JSON.stringify 的返回值可能被 /eval 多解一层（list/dict）→ 统一转文本再判。"""
    s = v if isinstance(v, str) else json.dumps(v, ensure_ascii=False)
    return needle in s


def gw_call(gui, tool, args):
    """经 gateway 方法面 `chonk.mcp-tools-call` 调工具（与 run_mcp_fields 同口径）。"""
    res = gui.req("chonk.mcp-tools-call", {
        "name": tool, "arguments": dict(args), "tool_call_display_name": "crontab-l4",
        "work_dir": WS,
    }, timeout=60000)
    if not isinstance(res, dict):
        raise RuntimeError("工具返回非对象: %r" % (res,))
    if res.get("isError"):
        raise RuntimeError("工具返回 isError: %r" % (res,))
    parts = [b.get("text") or "" for b in (res.get("content") or [])
             if isinstance(b, dict) and b.get("type") == "text"]
    return json.loads("\n".join(parts)) if parts else {}


def plugin_state(gui):
    r = gui.req("plugin-list", {})
    for p in (r or {}).get("plugins", []):
        if p.get("id") == "crontab":
            return p
    return None


def badge_js(expr):
    return ("(()=>{const fs=[...document.querySelectorAll('.statusbar iframe')]"
            ".filter(f=>(f.getAttribute('src')||'').indexOf('/plugins/crontab/')>=0);"
            "if(!fs.length)return null;const f=fs[0];if(!f.contentDocument||!f.contentDocument.body)return null;%s})()" % expr)


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    gui = GUIClient(port=PORT, data_dir=DATA_DIR, work_dir=WS)
    c = Checker()
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        ensure_locale(gui, "zh-CN")
        for _ in range(30):
            if J(gui, "!!document.querySelector('.statusbar')"):
                break
            time.sleep(0.5)

        # 事件采集（前端既有 mq；不伪造 DOM）。
        J(gui, """(()=>{window.__ckCronEvents=[];window.__ckCronPreviews=[];
          window.mq.on('plugin-event',e=>{try{const p=typeof e.payload==='string'?JSON.parse(e.payload):e.payload;
            window.__ckCronEvents.push({id:e.id,kind:p&&p.kind,count:p&&p.count,enabled:p&&p.enabled});}catch(_){}});
          window.mq.on('preview-tab-open',e=>window.__ckCronPreviews.push(e));return 'ok'})()""")

        # C1 插件就绪。
        deadline = time.time() + 40
        pv = None
        while time.time() < deadline:
            pv = plugin_state(gui)
            if pv and pv.get("status") == "running":
                break
            time.sleep(0.5)
        c.check("C1 crontab 插件已注册且 running", bool(pv and pv.get("status") == "running"),
                "plugin=%r" % (pv and {k: pv.get(k) for k in ("id", "status", "detail")},))

        # C2 状态栏徽标 iframe。
        src = wait_js(gui, badge_js("return (f.getAttribute('src')||'');"),
                      lambda v: isinstance(v, str) and "/plugins/crontab/" in v, max_wait=20)
        c.check("C2 状态栏内嵌 crontab badge iframe", isinstance(src, str) and "/plugins/crontab/" in src,
                "src=%r" % (src,))

        # C3 工具建任务（run + echo）。
        add = gw_call(gui, "crontab_add", {
            "cron": "*/1 * * * *",
            "action": {"type": "run", "param": {"command": "echo ck-crontab-ok"}},
        })
        task = (add or {}).get("task") or {}
        tid = task.get("id") or ""
        c.check("C3 crontab_add 建任务", bool(add.get("ok") and tid.startswith("ct_")),
                "add=%r" % (add,))

        # C4 crontab_list 可见。
        lst = gw_call(gui, "crontab_list", {})
        ids = [t.get("id") for t in (lst.get("tasks") or [])]
        c.check("C4 crontab_list 含新任务", tid in ids, "tasks=%r" % (ids,))

        # C5 变更事件（plugin-event，非轮询）。
        ev = wait_js(gui, "JSON.stringify(window.__ckCronEvents||[])", lambda v: has(v, "crontab-changed"))
        c.check("C5 crontab-changed 事件到达前端", has(ev, "crontab-changed"), "events=%r" % (ev,))

        # C6 徽标计数 = 1（事件驱动后）。
        txt = wait_js(gui, badge_js("return (f.contentDocument.body.innerText||'').replace(/\\s+/g,' ').trim();"),
                      lambda v: isinstance(v, str) and "1" in v, max_wait=20)
        c.check("C6 徽标计数为 1", isinstance(txt, str) and "1" in txt, "badge=%r" % (txt,))

        # C7 点徽标 → preview-tab-open{kind:'plugin-page',plugin:'crontab',page:'tasks'}。
        J(gui, badge_js("f.contentDocument.body.click();return 'ok';"))
        pv2 = wait_js(gui, "JSON.stringify(window.__ckCronPreviews||[])", lambda v: has(v, "crontab"))
        c.check("C7 点徽标打开任务页", has(pv2, "plugin-page") and has(pv2, "crontab"),
                "previews=%r" % (pv2,))

        # C8 停用 → list.enabled=false。
        dis = gw_call(gui, "crontab_disable", {"task_id": tid})
        lst2 = gw_call(gui, "crontab_list", {"enabled": True})
        ids2 = [t.get("id") for t in (lst2.get("tasks") or [])]
        c.check("C8 crontab_disable 后不在启用列表",
                bool(dis.get("ok")) and tid not in ids2, "disable=%r enabled=%r" % (dis, ids2))

        # C9 删除 → 计数归零。
        dele = gw_call(gui, "crontab_delete", {"task_id": tid})
        lst3 = gw_call(gui, "crontab_list", {})
        c.check("C9 crontab_delete 后任务为空", bool(dele.get("ok")) and not (lst3.get("tasks") or []),
                "delete=%r tasks=%r" % (dele, lst3.get("tasks")))
    finally:
        try:
            if snap is not None:
                restore_config(gui, snap)
        finally:
            gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()

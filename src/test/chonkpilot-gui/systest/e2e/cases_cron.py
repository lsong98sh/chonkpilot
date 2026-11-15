# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㊾：**crontab 调度语义 / 启动恢复 / 智能体任务 / 完成通知**。

S-CRON-01 覆盖插件就绪、徽标、工具建任务与任务页；本模块补齐「时间」与「跨进程」语义：

* **重启基点不变**：一次性任务 delay=40s → 5s 内关闭 GUI → 10s 后重启。`next_run_at` 必须仍是
  创建时的绝对时间，剩余时间 ≈ 原剩余（而非「重启后再等 40s」），最终在原点触发；
* **宽限补跑**：任务在 GUI 关闭期间到点，重启时仍在 `plugins.crontab.missed-grace-sec` 内 →
  启动恢复立即补跑（`trigger` 空 = 计划触发）；
* **超宽限 missed**：grace=0 且错过后重启 → `kind=skipped/detail=missed` + `disabled_reason=
  once_missed`，不自动执行；任务页「补跑」/ `POST api/run` → `trigger=manual` 且**不改**
  `next_run_at`、不重新启用一次性终态；
* **agent 动作 + 创建者通知**：`action.type=agent` 在独立任务子会话（`crontab-<task_id>`）执行，
  结果写入 `output_tail`；完成后向创建者会话追加 `kind=notify` 消息并广播
  `tool-notify{notice=cron-complete}`；
* **人工创建 / 配置保存 / 任务页 UI**：任务页 `POST api/create`（run/agent/notify）与 `api/save`
  配置键生效；页面包含新建任务、立即运行、漏跑补跑入口。

隔离方式：点内自起实例，`LOCALAPPDATA` 指向临时目录 → 插件 tasks.json / runs.jsonl 与真实用户
数据完全隔离（重启复用同一目录以验证恢复）。需要重启是功能本身要求，非框架每点起一次。
"""

import json
import os
import time
import urllib.request

import harness as _h
from .framework import case

# 测试点内部使用的插件 HTTP 动作面（与任务页 fetch 同路径）。
API = "/plugins/crontab/api/"
PAGE = "/plugins/crontab/webui/tasks.html"


def _plugins(c):
    return (c.req("plugin-list", {}) or {}).get("plugins") or []


def _tool_names(c):
    return {t.get("name") or "" for t in ((c.req("tools-list", {}) or {}).get("tools") or [])
            if isinstance(t, dict)}


def _wait_plugin(c, timeout=120):
    """等插件 running + 工具装配完成（插件是异步登记）。"""
    end = time.time() + timeout
    last = {}
    while time.time() < end:
        row = next((p for p in _plugins(c) if p.get("id") == "crontab"), {})
        names = _tool_names(c)
        last = {"status": row.get("status"), "has_add": "crontab_add" in names}
        if row.get("status") == "running" and "crontab_add" in names:
            return row
        time.sleep(0.8)
    raise AssertionError("crontab 插件/工具未就绪：%r" % last)


def _call(c, name, args, ws, session=None, display="e2e-cron", top_session=None):
    payload = {"name": name, "arguments": dict(args or {}),
               "tool_call_display_name": display, "work_dir": ws}
    if session:
        payload["session"] = session
        # 顶层对话里创建（e2e 无子代理上下文）→ top_session 同创建会话。
        payload["top_session"] = top_session or session
    res = c.req("chonk.mcp-tools-call", payload) or {}
    if res.get("isError"):
        raise AssertionError("工具 %s isError: %r" % (name, res))
    parts = [b.get("text") or "" for b in (res.get("content") or [])
             if isinstance(b, dict) and b.get("type") == "text"]
    return json.loads("\n".join(parts)) if parts else {}


def _tasks(c, ws):
    return (_call(c, "crontab_list", {}, ws) or {}).get("tasks") or []


def _task(c, ws, tid):
    return next((t for t in _tasks(c, ws) if t.get("id") == tid), {})


def _loads(x):
    """eval 结果可能是 JSON 二次编码字符串 → 尽量解到对象。"""
    for _ in range(3):
        if not isinstance(x, str):
            break
        try:
            x = json.loads(x)
        except Exception:
            break
    return x


def _http(c, action, payload=None, timeout=30):
    """插件 UI 动作面必须在窗口同源页面内 fetch（test-port 只暴露测试通道，无该路由）。

    页面源 = `https://app.localhost`（WebView2 自定义映射），Python 侧不可直连，故经 /eval
    发 fetch 并把结果暂存 `window.__ckApiRes`（与既有 codegraph/code 系列同口径）。
    """
    body = json.dumps(json.dumps(payload or {}))
    c.eval("window.__ckApiRes=null;fetch('%s%s',{method:'POST',"
           "headers:{'Content-Type':'application/json','X-CK-Plugin-Id':'crontab'},body:%s})"
           ".then(async r=>{window.__ckApiRes={status:r.status,body:await r.text()}})"
           ".catch(e=>{window.__ckApiRes={status:0,body:String(e)}});'ok'"
           % (API, action, body))
    end = time.time() + timeout
    last = None
    while time.time() < end:
        r = _loads(c.eval("JSON.stringify(window.__ckApiRes)"))
        if isinstance(r, dict) and r.get("status") is not None:
            code, raw = r.get("status"), r.get("body") or ""
            val = _loads(raw)
            if code != 200:
                raise AssertionError("插件动作 %s 失败（HTTP %s）：%s"
                                     % (action, code, str(val)[:300]))
            last = val
            break
        time.sleep(0.3)
    else:
        raise AssertionError("插件动作 %s 超时（%ss）" % (action, timeout))
    return last if isinstance(last, dict) else {}


def _page(c):
    c.eval("window.__ckPage=null;fetch('%s').then(r=>r.text())"
           ".then(t=>{window.__ckPage=t}).catch(e=>{window.__ckPage='ERR:'+e});'ok'" % PAGE)
    end = time.time() + 20
    while time.time() < end:
        v = _loads(c.eval("window.__ckPage"))
        if isinstance(v, str) and v:
            if v.startswith("ERR:"):
                raise AssertionError("读取任务页失败：%s" % v)
            return v
        time.sleep(0.3)
    raise AssertionError("读取任务页超时")


def _runs(c, tid=None, limit=50):
    payload = {"limit": limit}
    if tid:
        payload["task_id"] = tid
    return (_http(c, "runs", payload) or {}).get("runs") or []


def _start(port, ws, home, local, mock_port):
    h = _h.start_gui(port=port, work_dir=ws, home=home, ensure_spec=True,
                     extra_args=("-llm-base=http://127.0.0.1:%d/v1" % mock_port,
                                 "-llm-model=mock-model"),
                     env_overrides={"LOCALAPPDATA": local})
    c = h.client
    c.wait_ready(120)
    _wait_plugin(c)
    return h


def _wait(fn, desc, timeout=60, interval=0.5):
    end = time.time() + timeout
    last = None
    while time.time() < end:
        try:
            v = fn()
            if v:
                return v
            last = v
        except Exception as e:  # 轮询期瞬态错误不作为最终判据
            last = e
        time.sleep(interval)
    raise AssertionError("等待超时（%ss）：%s（末次 %r）" % (timeout, desc, last))


def _notify_msgs(obj, task_id):
    """递归收集 history/DB 快照里的 kind=notify 且含任务 ID 的消息。"""
    found = []

    def walk(x):
        if isinstance(x, dict):
            if x.get("kind") == "notify" and str(task_id) in json.dumps(x, ensure_ascii=False):
                found.append(x)
            for v in x.values():
                walk(v)
        elif isinstance(x, list):
            for v in x:
                walk(v)

    walk(obj)
    return found


def _fmt_ts(ts):
    return time.strftime("%H:%M:%S", time.localtime(ts))


def _add_once(c, ws, delay, action, session=None, session_mode=None):
    args = {"delay": delay, "action": action}
    if session_mode:
        args["session_mode"] = session_mode
    return _call(c, "crontab_add", args, ws, session=session)


NOTIFY_ACTION = {"type": "notify", "param": {"html": "e2e-cron-ping"}}


@case("S-CRON-02", "插件平台",
      "crontab 调度语义：重启基点不变 / 宽限内补跑 / 超宽限 missed + 人工补跑不改基点",
      stage="seeded", order=393,
      fp="42 插件平台 · crontab（run_crontab C10–C13）+ 61 消息面 plugin-event",
      desc="隔离实例：一次性 delay=40s → 关闭 10s 后重启，next_run_at 与剩余时间不变且在原点触发；"
           "关闭期间到点且 grace 内 → 恢复即补跑；grace=0 超窗 → skipped:missed + once_missed 终态；"
           "api/run 人工补跑 → trigger=manual、next_run_at 不变、一次性终态不复活；周期任务补跑同样不改基点。")
def s_cron_02(env):
    local = os.path.join("E:/ckcron", env.run_id, "s2")
    os.makedirs(local, exist_ok=True)
    ws = _h.tmp_dir("ck-e2e-cron-ws-")
    home = _h.tmp_home()
    port = _h.free_port()
    gui = None
    try:
        gui = _start(port, ws, home, local, env.mock_port)
        c = gui.client

        # 配置面：保存 grace=300 并回读（插件配置 API 与配置页同源）。
        cfg = _http(c, "load", {})
        env.check("配置页字段就绪", bool(cfg.get("fields")),
                  [f.get("key") for f in (cfg.get("fields") or [])][:5])
        _http(c, "save", {"entries": {"plugins.crontab.missed-grace-sec": "300"}})
        cfg2 = _http(c, "load", {})
        env.check("配置保存后回读 grace=300",
                  str((cfg2.get("values") or {}).get("plugins.crontab.missed-grace-sec")) == "300",
                  (cfg2.get("values") or {}).get("plugins.crontab.missed-grace-sec"))

        # ── A：重启基点不变 ──────────────────────────────────────
        add = _add_once(c, ws, "40s", NOTIFY_ACTION)
        t1 = (add.get("task") or {})
        tid1 = t1.get("id") or ""
        run_at1 = int(t1.get("run_at") or 0)
        next1 = int(t1.get("next_run_at") or 0)
        env.check("一次性任务 next_run_at = run_at（绝对基点）",
                  bool(tid1) and run_at1 > 0 and next1 == run_at1,
                  {"id": tid1, "run_at": run_at1, "next_run_at": next1})

        time.sleep(4)
        gui.stop()
        gui = None
        stopped = int(time.time())
        # 停机 10s（仍在 run_at 之前），再重启。
        time.sleep(10)
        gui = _start(port, ws, home, local, env.mock_port)
        c = gui.client
        row = _task(c, ws, tid1)
        env.check("重启后一次性任务仍启用且 next_run_at 未重算",
                  row.get("enabled") is True and int(row.get("next_run_at") or 0) == run_at1,
                  {"now": int(time.time()), "stopped": stopped, "run_at": run_at1,
                   "next_run_at": row.get("next_run_at")})
        remain = run_at1 - int(time.time())
        env.check("剩余时间仍按原点计算（非重启后 +40s）", 15 <= remain <= 36, remain)

        # 等到原点触发（不额外 sleep 40s：剩余时间即原点余量）。
        done1 = _wait(lambda: (lambda r: r if r and r.get("kind") == "notify"
                                and int(r.get("at") or 0) >= run_at1 - 2 else None)(
            (_task(c, ws, tid1).get("last_run") or {})),
            "任务在原点触发", timeout=max(20, remain + 20), interval=1.0)
        row1 = _task(c, ws, tid1)
        env.check("触发时间贴原点（±6s）", abs(int(done1.get("at") or 0) - run_at1) <= 6,
                  {"at": _fmt_ts(int(done1.get("at") or 0)), "run_at": _fmt_ts(run_at1)})
        env.check("一次性任务完成后终态停用", row1.get("enabled") is False
                  and row1.get("disabled_reason") == "completed_once"
                  and int(row1.get("next_run_at") or 0) == 0, row1.get("disabled_reason"))

        # ── B：关闭期间到点，宽限内重启 → 立即补跑 ────────────────
        add2 = _add_once(c, ws, "10s", NOTIFY_ACTION)
        tid2 = ((add2.get("task") or {}).get("id") or "")
        time.sleep(2)
        gui.stop()
        gui = None
        time.sleep(11)  # 已过 run_at，但 grace=300 内
        gui = _start(port, ws, home, local, env.mock_port)
        c = gui.client
        done2 = _wait(lambda: (lambda r: r if r and r.get("kind") == "notify" and r.get("ok")
                                else None)((_task(c, ws, tid2).get("last_run") or {})),
                      "宽限窗口内重启补跑", timeout=35, interval=0.8)
        env.check("宽限内错过 → 启动恢复补跑（非 missed）",
                  done2.get("kind") == "notify" and not done2.get("detail"),
                  {"kind": done2.get("kind"), "detail": done2.get("detail")})
        recs2 = _runs(c, tid2)
        env.check("补跑记录 trigger 为空（计划触发）",
                  bool(recs2) and recs2[0].get("trigger") in (None, ""), recs2[:1])

        # ── C：超宽限（grace=0）→ missed 终态，人工补跑 ───────────
        _http(c, "save", {"entries": {"plugins.crontab.missed-grace-sec": "0"}})
        add3 = _add_once(c, ws, "8s", NOTIFY_ACTION)
        tid3 = ((add3.get("task") or {}).get("id") or "")
        time.sleep(2)
        gui.stop()
        gui = None
        time.sleep(10)  # > 8s，且 grace=0
        gui = _start(port, ws, home, local, env.mock_port)
        c = gui.client
        missed = _wait(lambda: (lambda r: r if r and r.get("kind") == "skipped"
                                and r.get("detail") == "missed" else None)(
            (_task(c, ws, tid3).get("last_run") or {})),
            "超宽限 missed 记录", timeout=25, interval=0.6)
        row3 = _task(c, ws, tid3)
        env.check("超宽限 → skipped:missed + once_missed 终态",
                  missed.get("detail") == "missed" and row3.get("enabled") is False
                  and row3.get("disabled_reason") == "once_missed",
                  {"detail": missed.get("detail"), "reason": row3.get("disabled_reason")})
        missed_list = [r for r in _runs(c, limit=200)
                       if r.get("task_id") == tid3 and r.get("kind") == "skipped"]
        env.check("漏跑清单可读（runs.jsonl 保留 skipped:missed）",
                  any(r.get("detail") == "missed" for r in missed_list), missed_list[:2])

        # 人工补跑：trigger=manual、不改 next_run_at、不复活一次性终态
        next_before = int(_task(c, ws, tid3).get("next_run_at") or 0)
        _http(c, "run", {"task_id": tid3})
        manual = _wait(lambda: next((r for r in _runs(c, tid3)
                                     if r.get("trigger") == "manual"), None),
                       "人工补跑记录 trigger=manual", timeout=30, interval=0.6)
        env.check("人工补跑执行成功且结果落 runs", bool(manual.get("trigger") == "manual"),
                  {"trigger": manual.get("trigger"), "ok": manual.get("ok")})
        row3b = _task(c, ws, tid3)
        env.check("人工补跑不改 next_run_at / 不重新启用",
                  int(row3b.get("next_run_at") or 0) == next_before
                  and row3b.get("enabled") is False, {"next": row3b.get("next_run_at"),
                                                      "enabled": row3b.get("enabled")})

        # 周期任务人工补跑：next_run_at 不变（基点不被人工运行扰动）
        add4 = _call(c, "crontab_add",
                     {"cron": "*/5 * * * *", "action": NOTIFY_ACTION}, ws)
        tid4 = ((add4.get("task") or {}).get("id") or "")
        next4 = int(_task(c, ws, tid4).get("next_run_at") or 0)
        _http(c, "run", {"task_id": tid4})
        _wait(lambda: next((r for r in _runs(c, tid4)
                            if r.get("trigger") == "manual"), None),
              "周期任务人工补跑", timeout=30)
        env.check("周期任务人工补跑后 next_run_at 不变",
                  int(_task(c, ws, tid4).get("next_run_at") or 0) == next4,
                  {"before": next4, "after": _task(c, ws, tid4).get("next_run_at")})
    finally:
        if gui is not None:
            gui.stop()


@case("S-CRON-03", "插件平台",
      "crontab agent 动作：独立子会话 / output_tail / 通知主会话 + 未读点 / own 独立会话",
      stage="seeded", order=394,
      fp="42 插件平台 · crontab（run_crontab C14–C17）+ 18 会话归属",
      desc="隔离实例配置 mock LLM：gateway 以 top_session=主会话建 agent 一次性任务（notify=false 只关"
           "系统 Toast）→ 在 crontab-<task_id> 子会话跑完整轮次、output_tail 有结果、task.session_id "
           "归属正确；完成后**主会话**追加 kind=notify 且 tool-notify{notice=cron-complete} 到达，"
           "会话列表 notify_unread=true、打开会话后持久化清除；session_mode=own 的任务自己是顶层会话、"
           "进会话列表且不通知；人工 api/create 建 run 任务并 api/run；任务页包含新建/运行/补跑入口。")
def s_cron_03(env):
    local = os.path.join("E:/ckcron", env.run_id, "s3")
    os.makedirs(local, exist_ok=True)
    ws = _h.tmp_dir("ck-e2e-cron-ws-")
    home = _h.tmp_home()
    port = _h.free_port()
    gui = None
    try:
        gui = _start(port, ws, home, local, env.mock_port)
        c = gui.client
        c.req("data-user-config-save", {"data": {
            "llms": [{"name": "mock", "protocol": "openai", "model": "mock-model",
                      "baseUrl": "http://127.0.0.1:%d/v1" % env.mock_port,
                      "apiKey": "sk-test", "temperature": 0.7, "maxOutputToken": 1024}],
            "defaultLLM": 0}})
        c.mq_emit("config-refresh")
        time.sleep(0.6)

        sid = "e2e-cron-main"
        c.req("data-session-ensure-session", {"data": {"session_id": sid}})
        c.mq_on_capture(["tool-notify"])

        prompt = "E2E-CRON-AGENT: reply with the marker only"
        # notify=False 只关系统 Toast；创建者会话通知必须仍然发送（LLM 任务的硬要求）。
        add = _add_once(c, ws, "4s", {"type": "agent", "param": {
            "prompt": prompt, "notify": False}}, session=sid)
        t = add.get("task") or {}
        tid = t.get("id") or ""
        env.check("gateway 建 agent 任务（创建者会话已注入）", bool(tid), add)

        last = _wait(lambda: (lambda r: r if r and r.get("kind") == "agent" else None)(
            (_http(c, "detail", {"task_id": tid}).get("task") or {}).get("last_run")),
            "agent 任务执行完成", timeout=90, interval=1.0)
        body = _http(c, "detail", {"task_id": tid})
        task = body.get("task") or {}
        env.check("agent 任务成功", last.get("ok") is True,
                  {"ok": last.get("ok"), "error": last.get("error")})
        env.check("结果写入 output_tail", bool((last.get("output_tail") or "").strip()),
                  (last.get("output_tail") or "")[:120])
        env.check("任务归属独立子会话 crontab-<task_id>",
                  task.get("session_id") == "crontab-" + tid
                  and last.get("session_id") == "crontab-" + tid,
                  {"task": task.get("session_id"), "run": last.get("session_id")})
        env.check("任务子会话与创建者会话不同",
                  task.get("session_id") != sid, {"child": task.get("session_id"), "main": sid})

        hist = c.req("data-session-history",
                     {"session_id": sid, "target_messages": 50,
                      "target_bytes": 262144}) or {}
        msgs = _notify_msgs(hist, tid)
        env.check("创建者会话收到 kind=notify 完成消息（含任务 ID）", bool(msgs),
                  json.dumps(msgs[:1], ensure_ascii=False)[:240])
        ev = None
        for _ in range(40):
            for e in (c.events_of("tool-notify", clear=False) or []):
                p = e.get("payload") or {}
                if p.get("notice") == "cron-complete" and p.get("task_id") == tid:
                    ev = p
                    break
            if ev:
                break
            time.sleep(0.5)
        env.check("tool-notify{notice=cron-complete} 到达前端", bool(ev),
                  json.dumps(ev or {}, ensure_ascii=False)[:200])

        # 未读标识：主会话列表行 notify_unread=true；data-session-active-set（打开会话）→ 持久化已读。
        def _sess_row(session_id):
            return next((s for s in ((c.req("data-session-list", {}) or {}).get("list") or [])
                         if (s.get("session_id") or s.get("id")) == session_id), {})

        row = _wait(lambda: (lambda s: s if s.get("notify_unread") else None)(_sess_row(sid)),
                    "主会话通知未读点", timeout=15, interval=0.5)
        env.check("主会话列表行带 notify_unread", row.get("notify_unread") is True, row)
        c.req("data-session-active-set", {"session_id": sid})
        cleared = _wait(lambda: (lambda s: s if not s.get("notify_unread") else None)(_sess_row(sid)),
                        "打开主会话后未读点清除", timeout=15, interval=0.5)
        env.check("打开会话后 notify_unread 持久化清除",
                  cleared.get("notify_unread") in (False, None), cleared)

        # 独立会话模式（own）：任务自己是顶层会话、进会话列表、不通知主会话。
        own_prompt = "E2E-CRON-OWN: reply with the marker only"
        add_own = _add_once(c, ws, "3s", {"type": "agent", "param": {
            "prompt": own_prompt, "notify": False}}, session=sid, session_mode="own")
        own_tid = ((add_own.get("task") or {}).get("id") or "")
        own_sid = "crontab-" + own_tid
        env.check("own 模式持久化到任务视图",
                  (add_own.get("task") or {}).get("session_mode") == "own",
                  (add_own.get("task") or {}).get("session_mode"))
        _wait(lambda: (lambda r: r if r and r.get("kind") == "agent" else None)(
            (_http(c, "detail", {"task_id": own_tid}).get("task") or {}).get("last_run")),
            "own 模式 agent 执行完成", timeout=60, interval=0.8)
        own_row = _wait(lambda: (lambda s: s if s.get("session_id") == own_sid else None)(_sess_row(own_sid)),
                        "own 任务顶层会话进入会话列表", timeout=15, interval=0.5)
        env.check("own 模式任务自己是顶层会话", bool(own_row.get("session_id")), own_row)
        env.check("own 模式不产生通知未读点",
                  own_row.get("notify_unread") in (False, None), own_row)
        own_hist = c.req("data-session-history",
                         {"session_id": sid, "target_messages": 50,
                          "target_bytes": 262144}) or {}
        env.check("own 模式不通知主会话", not _notify_msgs(own_hist, own_tid),
                  json.dumps(_notify_msgs(own_hist, own_tid), ensure_ascii=False)[:160])

        # 人工创建（任务页表单同 API）：run 动作 + 立即运行。
        created = _http(c, "create", {
            "work_dir": ws, "notify_session_id": sid, "cron": "*/5 * * * *",
            "action": {"type": "run", "param": {"command": "echo ui-cron-ok", "notify": False}},
        })
        utid = ((created.get("task") or {}).get("id") or "")
        env.check("api/create 人工建任务", bool(created.get("ok") and utid.startswith("ct_")),
                  created.get("task"))
        next_before = int(_task(c, ws, utid).get("next_run_at") or 0)
        _http(c, "run", {"task_id": utid})
        mrec = _wait(lambda: next((r for r in _runs(c, utid)
                                   if r.get("trigger") == "manual"), None),
                     "任务页立即运行 → trigger=manual", timeout=40, interval=0.6)
        env.check("立即运行产出命令结果", bool((mrec.get("output_tail") or "").strip()),
                  (mrec.get("output_tail") or "")[:120])
        env.check("立即运行不改周期基点",
                  int(_task(c, ws, utid).get("next_run_at") or 0) == next_before,
                  {"before": next_before, "after": _task(c, ws, utid).get("next_run_at")})

        page = _page(c)
        env.check("任务页含新建任务/立即运行/漏跑补跑入口",
                  all(k in page for k in ("新建任务", "补跑", "create-card", "missed-card")),
                  [k for k in ("新建任务", "补跑", "create-card", "missed-card") if k not in page])
    finally:
        if gui is not None:
            gui.stop()
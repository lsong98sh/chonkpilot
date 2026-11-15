# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑪：**任务视图**（迁移自 test_task.py T1–T12；ask_user 分支已由 S-ASK-* 覆盖）。

* 消息面契约：`data-tasktree-tasks` / `-list` / `-delete`（缺参报错 / 未知节点幂等）
* 任务树渲染：`tasks.started(task)` → 节点标题；stop 按钮 → `task-stop` 发布（fetch spy 观测）
* llm 节点：`tasks.started(kind=llm)` → `.llm-task-meta` 耗时
* 终止会话：忙碌 turn → Cancel → `llm-cancel` 发布（挂起 LLM 夹具，复用 cases_chat_ops）
"""

import json
import time

import harness as _h

from .framework import case
from .cases_chat_ops import (HANG_LLM, _add_hang, _open_session, _restore_llms,
                             _select, _send, _start_hang, _stop_hang)

SPY_JS = """(()=>{
  if (window.__pubSpy) return 'exists';
  window.__pubSpy = [];
  const orig = window.fetch.bind(window);
  window.fetch = (...a) => {
    try {
      if (String(a[0]) === '/publish') {
        window.__pubSpy.push(JSON.parse(a[1].body));
      }
    } catch (e) {}
    return orig(...a);
  };
  return 'ok';
})()"""


def _spied(env):
    raw = env.js("window.__pubSpy || []") or []
    return raw if isinstance(raw, list) else []


def _payloads(env, typ):
    out = []
    for p in _spied(env):
        if not isinstance(p, dict) or p.get("type") != typ:
            continue
        raw = p.get("payload")
        try:
            out.append(json.loads(raw) if isinstance(raw, str) else raw)
        except Exception:
            out.append(raw)
    return out


def _clear_spy(env):
    env.js("window.__pubSpy = []; 'ok'")


def _remote(env, typ, payload):
    env.js("window.mq.emitRemote({type: %s, payload: %s}); 'ok'"
           % (json.dumps(typ), json.dumps(json.dumps(payload))))


@case("S-TASK-UI-01", "任务树", "任务视图：tasktree 契约 / 节点渲染与停止 / llm 耗时 / 会话终止",
      stage="seeded", order=360, fp="33 任务视图 · tasktree（test_task T1–T12）",
      desc="data-tasktree-tasks 返回数组、按不存在会话过滤为空；-list 缺 top_session 报错、"
           "空 top 返回空 nodes；-delete 缺 node_id 报错、未知节点幂等；tasks.started 渲染节点"
           "标题与 stop 按钮 → task-stop{task_id}；llm 节点显示耗时；忙碌 turn 点 Cancel → "
           "llm-cancel{session}。")
def s_task_ui_01(env):
    env.wait(lambda: True if _h.ensure_task_panel_open(env.client, timeout=30) else None,
             "任务面板展开", timeout=35)
    env.check("任务面板已展开", env.js("!!document.querySelector('.session-chat')") is True)

    r = env.req("data-tasktree-tasks", {}) or {}
    env.check("data-tasktree-tasks 返回数组", isinstance(r.get("list"), list), r)
    r2 = env.req("data-tasktree-tasks",
                 {"data": {"session_id": "e2e-no-sess-%s" % env.run_id,
                           "top_session": "e2e-no-top-%s" % env.run_id}}) or {}
    env.eq("按不存在会话过滤为空", r2.get("list"), [])

    raised = ""
    try:
        env.req("data-tasktree-list", {})
    except Exception as e:
        raised = str(e)
    env.check("-list 缺 top_session 报错", "top_session" in raised, raised[:120])

    anchor = "e2e-task-anchor-%s" % env.run_id
    dlg = (env.req("data-session-ensure-session", {"data": {"session_id": anchor}}) or {}).get("dialog_id") or ""
    env.check("锚点会话 dialog_id", bool(dlg), dlg)

    r4 = env.req("data-tasktree-list",
                 {"data": {"top_session": "e2e-empty-top-%s" % env.run_id,
                           "dialog_id": dlg}}) or {}
    env.eq("空 top 返回空 nodes", r4.get("nodes"), [])

    raised = ""
    try:
        env.req("data-tasktree-delete", {})
    except Exception as e:
        raised = str(e)
    env.check("-delete 缺 node_id 报错", "node_id" in raised, raised[:120])

    try:
        env.req("data-tasktree-delete",
                {"data": {"node_id": "e2e-no-node-%s" % env.run_id, "dialog_id": dlg}})
        env.check("未知节点删除幂等成功", True)
    except Exception as e:
        env.check("未知节点删除幂等成功", False, str(e)[:120])

    env.js(SPY_JS)
    env.check("publish spy 就绪", env.js("Array.isArray(window.__pubSpy)") is True)

    top = "e2e-task-top-%s" % env.run_id
    env.client.console(clear=True)
    env.js("window.mq.emit('session-changed', {session_id: %s}); 'ok'" % json.dumps(top))
    time.sleep(1.0)
    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error" and "data-tasktree" in (e.get("text") or "")]
    env.check("会话切换触发 tasktree 查询且无错误", len(errs) == 0, errs[:2])

    tid = "e2e-task-node-%s" % env.run_id
    _remote(env, "tasks.started", {
        "task_id": tid, "kind": "tool", "tool_name": "command_execute",
        "name": "e2e echo", "purpose": "e2e echo", "status": "running",
        "session_id": top, "top_session": top,
        "started_at": "2026-09-01T00:00:00Z",
    })
    env.wait(lambda: True if int(env.js(
        "document.querySelectorAll('.session-tree-node').length") or 0) >= 1 else None,
        "任务树节点出现", timeout=20)
    title = env.wait(lambda: str(env.js(
        "document.querySelector('.session-tree-node .node-title')"
        "?.textContent?.trim() || ''") or "")
        if str(env.js("document.querySelector('.session-tree-node .node-title')"
                      "?.textContent?.trim() || ''") or "") == "e2e echo" else None,
        "节点标题 = e2e echo", timeout=20)
    env.eq("任务树节点标题", title, "e2e echo")

    env.wait(lambda: True if int(env.js(
        "document.querySelectorAll('.node-stop').length") or 0) >= 1 else None,
        "task 节点 stop 按钮", timeout=15)
    _clear_spy(env)
    env.js("document.querySelector('.node-stop')?.click(); 'ok'")
    stops = env.wait(lambda: _payloads(env, "task-stop")
                     if len(_payloads(env, "task-stop")) >= 1 else None,
                     "task-stop 已发布", timeout=15)
    env.eq("task-stop 携带 task_id", (stops[-1] or {}).get("task_id"), tid)

    _remote(env, "tasks.started", {
        "task_id": "e2e-op-%s" % env.run_id, "session_id": "e2e-sub-%s" % env.run_id,
        "kind": "llm", "status": "running", "top_session": top,
        "name": "mock", "purpose": "mock", "started_at": "2026-09-01T00:00:00Z",
    })
    meta = env.wait(lambda: int(env.js(
        "document.querySelectorAll('.llm-task-meta').length") or 0)
        if int(env.js("document.querySelectorAll('.llm-task-meta').length") or 0) >= 1
        else None, "llm 节点耗时显示", timeout=20)
    env.check("llm 节点显示耗时 meta", int(meta) >= 1, meta)

    # 终止会话：挂起 LLM → 忙碌 → Cancel → llm-cancel 发布
    srv, port = _start_hang()
    base = _add_hang(env, port)
    try:
        _select(env, HANG_LLM)
        sid = "e2e-term-%s" % env.run_id
        _open_session(env, sid)
        _clear_spy(env)
        _send(env, sid, "stop me", llm=HANG_LLM)

        def _actions():
            return env.jsj("JSON.stringify([...document.querySelectorAll("
                           "'.input-actions-right button')]"
                           ".map(b=>b.textContent.trim()))") or []

        env.wait(lambda: True if any(x in ("Cancel", "取消", "终止", "停止")
                                     for x in _actions()) else None,
                 "忙碌态 Cancel 按钮", timeout=20)
        clicked = env.js(
            "(()=>{const bs=[...document.querySelectorAll('.input-actions-right button')];"
            "const b=bs.find(x=>['Cancel','取消','终止','停止'].includes(x.textContent.trim()));"
            "if(!b)return 'no-btn';b.click();return 'ok';})()")
        env.check("点击 Cancel", clicked == "ok", clicked)
        cancels = env.wait(lambda: _payloads(env, "llm-cancel")
                           if len(_payloads(env, "llm-cancel")) >= 1 else None,
                           "llm-cancel 已发布", timeout=15)
        env.eq("llm-cancel 携带 session", (cancels[-1] or {}).get("session"), sid)
        _remote(env, "llm-complete", {"session": sid, "status": "cancelled"})
    finally:
        _restore_llms(env, base)
        _stop_hang(srv)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("任务视图组无前端错误", len(errs) == 0, errs[:3])


def _activate_task_session(env, sid):
    env.req("data-session-ensure-session", {"data": {"session_id": sid}})
    env.emit("session-changed", {"session_id": sid})
    env.wait(lambda: True if str(env.js("document.querySelector('.chat-session-tag')"
                                        "?.getAttribute('title') || ''")) == sid else None,
             "任务会话激活 %s" % sid, timeout=20)
    time.sleep(0.5)


def _started(env, sid, tid, **kw):
    d = {"task_id": tid, "session_id": sid, "top_session": sid, "state": "running",
         "started_at": int(time.time() * 1000)}
    d.update(kw)
    env.emit("tasks.started", d)
    time.sleep(0.3)


def _done(env, sid, tid, state="done", **kw):
    d = {"task_id": tid, "session_id": sid, "top_session": sid, "state": state}
    d.update(kw)
    env.emit("tasks.done", d)
    time.sleep(0.3)


@case("S-TASK-UI-02", "任务树", "任务树渲染：空态/状态图标/耗时/stop/长标题裁剪/嵌套滚动/详情输出",
      stage="seeded", order=365, fp="33 任务视图渲染（run_task_ui G1–G7）",
      desc="空会话显示空态；tasks.started running → 节点+转圈+类型图标；done/error 不再转圈；"
           "llm 节点显示耗时；running 节点 stop 默认隐藏且有 :hover 规则；超长标题 ellipsis 且"
           "面板无水平溢出；三层嵌套缩进递增、超一屏可滚到底；点节点开详情（PID/命令/参数 + "
           "partial_output 实时追加）；running/done 详情均有 footer 操作。")
def s_task_ui_02(env):
    env.wait(lambda: True if _h.ensure_task_panel_open(env.client, timeout=30) else None,
             "任务面板展开", timeout=35)

    sid0 = "e2e-task-empty-%s" % env.run_id
    _activate_task_session(env, sid0)
    env.wait(lambda: True if env.js("!!document.querySelector('.empty-state')") is True else None,
             "空任务树空态", timeout=15)
    etxt = str(env.js("(()=>{const es=[...document.querySelectorAll('.empty-state')]"
                      ".filter(e=>e.offsetParent!==null);"
                      "const e=es[es.length-1];return e?e.textContent:''})()") or "")
    env.check("空任务树显示暂无会话", any(k in etxt for k in
                                         ("暂无会话", "No sessions", "暂无子会话")), etxt[:80])

    sid = "e2e-task-render-%s" % env.run_id
    _activate_task_session(env, sid)
    _started(env, sid, "e2e-t-run", kind="command", tool="command_execute",
             name="e2e echo 渲染", pid=1234)
    env.wait(lambda: True if int(env.js(
        "document.querySelectorAll('.session-tree-node').length") or 0) >= 1 else None,
        "节点出现", timeout=15)
    ntxt = str(env.js("(()=>{const e=document.querySelector("
                      "'.session-tree-node .node-title');"
                      "return e?e.textContent.trim():''})()") or "")
    env.check("节点标题 = e2e echo 渲染", "e2e echo 渲染" in ntxt, ntxt)
    env.check("running 节点转圈", int(env.js(
        "document.querySelectorAll('.session-tree-node .status-icon.spinning').length")
        or 0) > 0)
    env.check("节点有类型图标", int(env.js(
        "document.querySelectorAll('.session-tree-node svg,"
        ".session-tree-node .b-icon').length") or 0) > 0)

    _done(env, sid, "e2e-t-run", state="done")
    env.wait(lambda: True if int(env.js(
        "document.querySelectorAll('.session-tree-node .status-icon.spinning').length")
        or 0) == 0 else None, "done 后停止转圈", timeout=10)
    env.check("done 后不再转圈", int(env.js(
        "document.querySelectorAll('.session-tree-node .status-icon.spinning').length")
        or 0) == 0)
    _started(env, sid, "e2e-t-err", kind="tool", tool="file_read", name="失败任务")
    _done(env, sid, "e2e-t-err", state="error")
    env.check("多节点全部保留", int(env.js(
        "document.querySelectorAll('.session-tree-node').length") or 0) >= 2)

    _started(env, sid, "e2e-t-llm", kind="llm", name="子会话思考",
             session_id="e2e-sub-%s" % env.run_id)
    meta = env.wait(lambda: str(env.js("(()=>{const es=[...document.querySelectorAll("
                                       "'.llm-task-meta')].filter(e=>e.offsetParent!==null);"
                                       "const e=es[es.length-1];return e?e.textContent:''})()")
                                or "") or None, "llm 运行耗时", timeout=15)
    env.check("llm 节点耗时非空", bool(meta.strip()), meta[:40])

    _started(env, sid, "e2e-t-stop", kind="command", tool="command_execute", name="可停止任务")
    stop = env.jsj("""JSON.stringify((()=>{const btn=document.querySelector(
      '.session-tree-node .node-stop');if(!btn)return {ok:false,why:'no button'};
      const def=getComputedStyle(btn).display;let hover=false;
      for(const ss of document.styleSheets){let rs=[];try{rs=[...ss.cssRules]}catch(e){continue}
        for(const r of rs){const sel=r.selectorText||'';
          if(sel.includes(':hover')&&sel.includes('.hover-act')&&sel.includes('has-action')){
            const d=r.style.display||'';hover=d.includes('inline-flex')||d.includes('flex')}}}
      return {ok:true,def,hover}})())""")
    env.check("stop 按钮存在且默认隐藏", stop.get("ok") is True and stop.get("def") == "none", stop)
    env.check("存在 :hover 显示规则", bool(stop.get("hover")), stop)

    _started(env, sid, "e2e-t-wide", kind="tool", tool="file_read",
             name="超长任务标题-" + "X" * 120)
    time.sleep(0.5)
    geo = env.jsj("""JSON.stringify((()=>{const p=document.querySelector('.tree-scroll');
      const node=document.querySelector('.session-tree-node .node-header');
      if(!p||!node)return {ok:false};const r=node.getBoundingClientRect(),
      pr=p.getBoundingClientRect();
      return {ok:true,left:r.left,right:r.right,panelLeft:pr.left,panelRight:pr.right,
              sw:p.scrollWidth,cw:p.clientWidth}})())""")
    env.check("节点不横向越界", geo.get("left", 0) >= geo.get("panelLeft", 0) - 1
              and geo.get("right", 0) <= geo.get("panelRight", 0) + 1, geo)
    env.check("任务面板无水平溢出", int(geo.get("sw") or 0) <= int(geo.get("cw") or 0) + 1, geo)
    ell = env.jsj("""JSON.stringify((()=>{const el=document.querySelector(
      '.session-tree-node .node-title');return {ow:getComputedStyle(el).overflow,
      ws:getComputedStyle(el).whiteSpace};})())""")
    env.check("超长标题 ellipsis", ell.get("ow") == "hidden" and ell.get("ws") == "nowrap", ell)

    sid2 = "e2e-task-nest-%s" % env.run_id
    _activate_task_session(env, sid2)
    _started(env, sid2, "e2e-n0", kind="command", tool="command_execute", name="父任务")
    _started(env, sid2, "e2e-n1", kind="tool", tool="file_read", name="子任务",
             parent_id="e2e-n0")
    _started(env, sid2, "e2e-n2", kind="tool", tool="file_read", name="孙任务",
             parent_id="e2e-n1")
    for i in range(24):
        _started(env, sid2, "e2e-bulk-%02d" % i, kind="tool", tool="file_read",
                 name="批量任务 %02d" % i)
    time.sleep(0.8)
    ng = env.jsj("""JSON.stringify((()=>{const byId=id=>document.querySelector(
      '.node-header[data-node-id="'+id+'"]');
      const a=byId('e2e-n0'),b=byId('e2e-n1'),d=byId('e2e-n2');
      if(!a||!b||!d)return {ok:false};
      const r=e=>e.getBoundingClientRect();
      const p=document.querySelector('.tree-scroll').getBoundingClientRect();
      return {ok:true,indent0:parseFloat(getComputedStyle(a).paddingLeft),
        indent1:parseFloat(getComputedStyle(b).paddingLeft),
        indent2:parseFloat(getComputedStyle(d).paddingLeft),
        left0:r(a).left,left1:r(b).left,left2:r(d).left,
        panelRight:p.right,
        nested:document.querySelectorAll('.node-children .session-tree-node').length}})())""")
    env.check("三层节点渲染且缩进递增",
              ng.get("ok") is True and ng["indent0"] < ng["indent1"] < ng["indent2"]
              and ng["left0"] < ng["left1"] < ng["left2"], ng)
    env.check("子/孙嵌在父节点内", int(ng.get("nested") or 0) >= 2, ng.get("nested"))
    for k in (0, 1, 2):
        env.check("第 %d 层不横向越界" % k, ng["left%d" % k] <= ng["panelRight"] + 1, ng)
    sc = env.jsj("""JSON.stringify((()=>{const p=document.querySelector('.tree-scroll');
      p.scrollTop=p.scrollHeight;
      const last=[...document.querySelectorAll('.node-header')].pop();
      const pr=p.getBoundingClientRect(),lr=last.getBoundingClientRect();
      return {top:p.scrollTop,sh:p.scrollHeight,ch:p.clientHeight,
              lastBottom:lr.bottom,panelBottom:pr.bottom}})())""")
    env.check("任务面板可滚动", int(sc.get("sh") or 0) > int(sc.get("ch") or 0), sc)
    env.check("滚到底后末节点可见", int(sc.get("top") or 0) > 0
              and sc.get("lastBottom", 0) <= sc.get("panelBottom", 0) + 1, sc)

    sid3 = "e2e-task-detail-%s" % env.run_id
    _activate_task_session(env, sid3)
    _started(env, sid3, "e2e-dtl", kind="command", tool="command_execute",
             name="详情任务", pid=5678, cmd="ping -n 3 127.0.0.1",
             args=["ping", "-n", "3", "127.0.0.1"])
    env.js("""(()=>{const el=[...document.querySelectorAll('.node-title')]
      .find(e=>e.offsetParent!==null&&e.textContent.includes('详情任务'));
      if(el)el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return 'ok'})()""")
    env.wait(lambda: True if env.js("!!document.querySelector('.task-detail')") is True else None,
             "任务详情打开", timeout=15)
    txt = str(env.js("(()=>{const es=[...document.querySelectorAll('.task-detail')]"
                     ".filter(e=>e.offsetParent!==null);const e=es[es.length-1];"
                     "return e?e.textContent:''})()") or "")
    env.check("详情含标题/PID/命令",
              "详情任务" in txt and "PID 5678" in txt and "ping" in txt.lower(), txt[:160])
    env.emit("tasks.updated", {"task_id": "e2e-dtl", "session_id": sid3,
                               "top_session": sid3, "partial_output": "PING 开始\n"})
    env.wait(lambda: True if "PING 开始" in str(env.js(
        "(()=>{const es=[...document.querySelectorAll('.task-detail')]"
        ".filter(e=>e.offsetParent!==null);const e=es[es.length-1];"
        "return e?e.textContent:''})()") or "") else None,
        "详情输出实时追加", timeout=15)
    env.check("详情 partial_output 实时追加", "PING 开始" in str(env.js(
        "(()=>{const es=[...document.querySelectorAll('.task-detail')]"
        ".filter(e=>e.offsetParent!==null);const e=es[es.length-1];"
        "return e?e.textContent:''})()") or ""))
    env.check("running 详情有 footer 按钮", int(env.js(
        "document.querySelectorAll('.task-detail .td-footer .b-btn').length") or 0) > 0)
    _done(env, sid3, "e2e-dtl", state="done")
    time.sleep(0.4)
    env.check("done 详情仍有 footer", int(env.js(
        "document.querySelectorAll('.task-detail .td-footer').length") or 0) > 0)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("任务树渲染组无前端错误", len(errs) == 0, errs[:3])

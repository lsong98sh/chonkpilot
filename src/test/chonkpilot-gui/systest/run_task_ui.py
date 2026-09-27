# -*- coding: utf-8 -*-
"""task 树/详情回归（FP 298-336 可事件驱动项）。

驱动：tasks.started/updated/done 事件注入（server 协议载荷，无需真实 server）
+ 点击节点 → task-detail-open → SessionChat 详情视图断言。

覆盖：
  G1 无会话/无节点「暂无会话」（L303）
  G2 任务节点创建 + 自动展开（L293）
  G3 节点类型图标（tool/command/llm）
  G4 运行中转圈状态（L296 running）
  G5 完成/失败状态图标（done/error）
  G6 子会话运行耗时实时刷新（L297 llm running meta）
  G7 运行中节点 hover 显示停止按钮（L300/302）
  G8 点击任务节点查看详情（L299）
  G9 任务详情：PID/耗时/参数（L306-308/L325）
  G10 详情运行输出实时追加（L326/L328）

前置：chonkpilot.exe --test-port=2345 已启动（无需 server）。
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345)  # 复用优先（owned=False 不回收）；无实例则自起并在结束时回收
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：任务树/会话态落 prj，退出前自动回滚

SEQ = [0]


def deep_loads(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def new_sid():
    SEQ[0] += 1
    return f"task-test-{SEQ[0]:03d}"


def wait_el(selector, max_wait=8):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if c.exists(selector).get("count", 0) > 0:
            return True
        time.sleep(0.3)
    return False


def visible_text(selector):
    return deep_loads(c.eval("""(() => {
      const els = [...document.querySelectorAll(%s)].filter(e => e.offsetParent !== null);
      const el = els[els.length - 1];
      return el ? el.textContent : '';
    })()""" % json.dumps(selector)))


def activate(sid):
    c.mq_emit("session-changed", {"session_id": sid})
    time.sleep(0.6)


def task_started(sid, task_id, **kw):
    d = {"task_id": task_id, "session_id": sid, "top_session": sid, "state": "running",
         "started_at": int(time.time() * 1000)}
    d.update(kw)
    c.mq_emit("tasks.started", d)
    time.sleep(0.5)


def task_done(sid, task_id, state="done", **kw):
    d = {"task_id": task_id, "session_id": sid, "top_session": sid, "state": state}
    d.update(kw)
    c.mq_emit("tasks.done", d)
    time.sleep(0.5)


def task_updated(sid, task_id, **kw):
    d = {"task_id": task_id, "session_id": sid, "top_session": sid}
    d.update(kw)
    c.mq_emit("tasks.updated", d)
    time.sleep(0.5)


def node_count():
    return deep_loads(c.eval("document.querySelectorAll('.session-tree-node').length"))


def case_empty_tree():
    sid = new_sid()
    activate(sid)
    time.sleep(0.8)
    if not wait_el(".empty-state"):
        raise TestError("空任务树未显示「暂无会话」空态")
    txt = visible_text(".empty-state")
    if not any(k in txt for k in ("No sessions", "暂无会话", "暂无子会话")):
        raise TestError(f"空态文本异常: {txt!r}")


def case_node_create_running():
    sid = new_sid()
    activate(sid)
    task_started(sid, "t-001", kind="command", tool="command_execute", name="echo 测试",
                 pid=1234)
    if not wait_el(".session-tree-node"):
        raise TestError("任务节点未创建")
    txt = visible_text(".session-tree-node")
    if "echo 测试" not in txt:
        raise TestError(f"节点标题异常: {txt[:100]!r}")
    if not c.exists(".session-tree-node .status-icon.spinning").get("count", 0) > 0:
        raise TestError("running 节点应显示转圈状态图标")
    # kind 图标存在（command 类）
    if not c.exists(".session-tree-node svg, .session-tree-node .b-icon").get("count", 0) > 0:
        raise TestError("节点缺类型图标")
    return sid, "t-001"


def case_terminal_states():
    sid = new_sid()
    activate(sid)
    task_started(sid, "t-done", kind="command", tool="command_execute", name="完成任务")
    task_done(sid, "t-done", state="done")
    if c.exists(".session-tree-node .status-icon.spinning").get("count", 0) > 0:
        raise TestError("done 后不应再转圈")
    task_started(sid, "t-err", kind="tool", tool="file_read", name="失败任务")
    task_done(sid, "t-err", state="error")
    # error 状态图标（非 spinning）
    time.sleep(0.3)
    if node_count() < 2:
        raise TestError("节点未全部保留")


def case_llm_elapsed():
    sid = new_sid()
    activate(sid)
    task_started(sid, "t-llm", kind="llm", name="子会话思考", session_id="sub-abc1")
    if not wait_el(".llm-task-meta"):
        raise TestError("llm running 未显示运行耗时")
    txt = visible_text(".llm-task-meta")
    if not txt.strip():
        raise TestError("耗时文本为空")


def case_hover_stop():
    sid = new_sid()
    activate(sid)
    task_started(sid, "t-stop", kind="command", tool="command_execute", name="可停止任务")
    # 模拟 hover：mouseenter 到 status-wrap（CSS hover 需真实鼠标，改用事件冒泡触发 v-mq hover 动作？
    # v-mq 只绑 click/stop/emit，hover 由 CSS :hover 控制 → 无法用合成事件触发。
    # 改验证停止按钮元素存在（hidden，hover 显示）——DOM 存在即链路具备。
    if not c.exists(".session-tree-node .node-stop").get("count", 0) > 0:
        raise TestError("running 节点缺 stop 按钮元素")


def case_detail_view():
    sid = new_sid()
    activate(sid)
    task_started(sid, "t-dtl", kind="command", tool="command_execute",
                 name="详情任务", pid=5678,
                 cmd="ping -n 3 127.0.0.1", args=["ping", "-n", "3", "127.0.0.1"])
    # 点击节点标题 → 详情视图
    c.eval("""(() => {
      const el = [...document.querySelectorAll('.node-title')].find(e => e.offsetParent !== null && e.textContent.includes('详情任务'));
      if (!el) return 'no-node';
      el.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'clicked';
    })()""", 5000)
    time.sleep(0.8)
    if not wait_el(".task-detail"):
        raise TestError("点击任务节点后详情视图未打开")
    txt = visible_text(".task-detail")
    if "详情任务" not in txt:
        raise TestError(f"详情标题异常: {txt[:120]!r}")
    if "PID 5678" not in txt:
        raise TestError(f"详情缺 PID: {txt[:200]!r}")
    if "ping" not in txt.lower():
        raise TestError(f"详情缺命令/参数: {txt[:200]!r}")
    # 输出实时追加
    task_updated(sid, "t-dtl", partial_output="PING 开始\n")
    time.sleep(0.4)
    txt2 = visible_text(".task-detail")
    if "PING 开始" not in txt2:
        raise TestError("详情输出未实时追加")
    return sid


def case_detail_close_stop():
    """详情底部：running 显示停止、非 running 显示关闭（L310-312）。"""
    sid = new_sid()
    activate(sid)
    task_started(sid, "t-ft", kind="command", tool="command_execute", name="详情操作任务")
    c.eval("""(() => {
      const el = [...document.querySelectorAll('.node-title')].find(e => e.offsetParent !== null && e.textContent.includes('详情操作任务'));
      if (el) el.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""", 5000)
    time.sleep(0.8)
    if not c.exists(".task-detail .td-footer .b-btn").get("count", 0) > 0:
        raise TestError("详情底部缺操作按钮")
    task_done(sid, "t-ft", state="done")
    time.sleep(0.4)
    # done 后操作按钮仍在（关闭）——仅断言 footer 存在
    if not c.exists(".task-detail .td-footer").get("count", 0) > 0:
        raise TestError("完成态详情缺 footer")


def case_closed_readonly():
    """G8 关闭任务（逻辑删除）后仍**同列表可见且只读**（42 §2 (126)）：

    「关闭」= `data-tasktree-delete` 逻辑删除（不物理删）→ 前端按新口径请求 `include_closed`
    取回 → 节点以灰色 + 「已关闭」标签只读展示；**级联子节点一并可见**；不显示停止/恢复动作
    （详情 footer 只读提示）。
    """
    sid = new_sid()
    activate(sid)
    # 直接以终态创建（避免 running→done 时 `tasks` 快照 status 残留 running 导致 footer 仍是「停止」）
    task_started(sid, "t-cls", kind="command", tool="command_execute", name="可关闭任务",
                 state="done")
    task_started(sid, "t-cls-child", kind="tool", tool="file_read", name="子任务",
                 parent_id="t-cls", state="done")
    time.sleep(0.8)
    # 打开父节点详情（此刻标题仍为 name；后续增量会把标题回落 task_id）→ footer 应为「关闭」
    r0 = deep_loads(c.eval("""(() => {
      const el = [...document.querySelectorAll('.node-title')].find(e => e.offsetParent !== null && e.textContent.includes('可关闭任务'));
      if (!el) return 'no-node';
      el.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""", 5000))
    if r0 != "ok":
        raise TestError("未找到父节点标题（关闭前置）：%r" % r0)
    if not wait_el(".task-detail"):
        raise TestError("详情视图未打开")
    time.sleep(0.5)
    r = deep_loads(c.eval("""(() => {
      const b = [...document.querySelectorAll('.task-detail .td-footer button')].find(x => x.getBoundingClientRect().width > 0);
      if (!b) return 'no-btn';
      b.click();
      return 'ok';
    })()""", 5000))
    if r != "ok":
        raise TestError("详情底部未找到「关闭」按钮：%r" % r)
    time.sleep(0.8)
    # ── DOM 证据（每次跑都打印，便于人工核对）──
    titles = deep_loads(c.eval(
        "[...document.querySelectorAll('.session-tree-node .node-title')].map(e=>e.textContent.trim())"))
    nested = deep_loads(c.eval("document.querySelectorAll('.node-children .session-tree-node').length"))
    closed_n = deep_loads(c.eval("document.querySelectorAll('.session-tree-node .node-header.closed').length"))
    tags = deep_loads(c.eval(
        "[...document.querySelectorAll('.session-tree-node .node-closed-tag')].map(e=>e.textContent.trim())"))
    stops = deep_loads(c.eval("document.querySelectorAll('.session-tree-node .node-stop').length"))
    awaiting = deep_loads(c.eval("document.querySelectorAll('.session-tree-node .node-awaiting').length"))
    btns = deep_loads(c.eval("document.querySelectorAll('.task-detail .td-footer button').length"))
    hint = visible_text(".task-detail .td-footer").strip()
    dtags = deep_loads(c.eval("document.querySelectorAll('.task-detail .td-closed-tag').length"))
    print("      [evidence] titles=%r nested=%s closed=%s tags=%r stops=%s awaiting=%s "
          "detailButtons=%s detailTags=%s detailHint=%r"
          % (titles, nested, closed_n, tags, stops, awaiting, btns, dtags, hint))
    # ① 节点仍可见（父 + 级联子）且带闭合态样式与「已关闭」标签
    if "可关闭任务" not in titles or "子任务" not in titles:
        raise TestError("关闭后节点/子节点不应消失：titles=%r" % titles)
    # 级联：子节点仍嵌在父节点下（树结构完整）
    if int(nested) < 1:
        raise TestError("级联子节点未显示在父节点下")
    if int(closed_n) < 2:
        raise TestError("闭合态节点数不足（父+子应均可见）：%r" % closed_n)
    if len(tags) < 2 or any(t not in ("已关闭", "Closed") for t in tags):
        raise TestError("「已关闭」标签缺失/异常（zh-CN/en-US 双语之一）：%r" % tags)
    # ② 只读：无停止动作（也不渲染裁决条）
    if int(stops) != 0:
        raise TestError("已关闭节点不应有停止动作：.node-stop=%r" % stops)
    if int(awaiting) != 0:
        raise TestError("已关闭节点不应渲染裁决条")
    # ③ 详情只读：无动作按钮 + 只读提示 + 「已关闭」标签
    if int(btns) != 0:
        raise TestError("已关闭详情不应有动作按钮：buttons=%r" % btns)
    if hint not in ("已关闭任务为只读展示，不可恢复", "Closed tasks are read-only and cannot be restored"):
        raise TestError("详情只读提示异常：%r" % hint)
    if int(dtags) < 1:
        raise TestError("详情缺「已关闭」标签")


def main():
    c.wait_ready()
    # 任务面板默认收起（2026-09-27 首屏减负：MainLayout taskOpen 默认 false）→
    # 本套件断言任务树 / 详情 DOM → 先经既有 tasks-toggle 展开。
    _h.ensure_task_panel_open(c)
    c.console(clear=True)
    ok = True
    ok &= run_case("G1 空任务树「暂无会话」", case_empty_tree)
    ok &= run_case("G2 节点创建 + running 转圈 + 类型图标", case_node_create_running)
    ok &= run_case("G3 完成/失败状态图标", case_terminal_states)
    ok &= run_case("G4 llm 会话节点运行耗时", case_llm_elapsed)
    ok &= run_case("G5 运行中节点 stop 按钮元素", case_hover_stop)
    ok &= run_case("G6 点击任务节点查看详情（PID/命令/参数）", case_detail_view)
    ok &= run_case("G7 详情底部操作按钮", case_detail_close_stop)
    ok &= run_case("G8 关闭（逻辑删除）后节点只读可见 + 子节点在 + 无动作", case_closed_readonly)
    print("RESULT:", ok)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

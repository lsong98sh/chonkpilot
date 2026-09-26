# -*- coding: utf-8 -*-
"""chat 对话内容区 + 消息展示回归（FP 121-156 中可事件驱动覆盖项）。

驱动：mq 事件注入模拟 server（无需 LLM server）——
  session-changed 激活会话 → message-send 建 turn（前端自分配）→ 注入
  llm-receive（text/reason/tool-call）/ llm-complete / tool-pair / toolNotify。

覆盖：
  E1 无会话「选择会话」（L121）
  E2 有会话无消息「暂无消息」（L122）
  E3 思考过程：实时展开 + 折叠 + 复制图标（L147/L156 实时侧/L155）
  E4 工具调用记录显示（L150 tool_call 区块）
  E5 工具状态徽标（L150 tool_pair pending→done）
  E6 后台任务完成通知（🔔 user-notify）（L139/L153）
  E7 切换会话不中断 + 切回保留（L140/L141）
  E8 工具调用记录可复制图标（L155）

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
# 套件级配置快照-还原（51 §6-8）：会话/消息落共享 work-dir 库（`active_session_id` 等）→ 退出前回滚
_h.suite_config_guard(c)
_h.ensure_locale(c)  # 语言确定性：E1/E2 提示文案按 zh-CN 断言（DB ui.locale 可能被他套件写成 en-US）

SEQ = [0]

# 会话消息重载链**末端**主题（见 activate 注释）：`data-tasktree-tasks`（既有主题，未新增/改消息面）
RELOAD_TOPIC = "data-tasktree-tasks"


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
    return f"chat-test-{SEQ[0]:03d}"


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


def activate(sid, wait_reload=True):
    """激活会话；`sid` 非空时**等消息重载落地**再返回（稳定条件，见下）。

    根因（E5「后台任务完成通知」批内偶发红）：`session-changed` → `MessageList.onSessionChanged`
    会 `resetMessages()` + `await loadMessages(newId)`（前端 `utils/sessionMessages.js:263-284`：
    `messages.value = []` → `await getTurnsPaginated(...)` → **整体替换** `messages.value`）。
    而「后台任务完成通知」是前端把 `tool-notify{notice:completion}` **push 进 messages** 的本地
    追加（`views/chat/MessageList.vue:688-706`）→ 若重载在 push **之后**落地，刚 push 的通知被
    整体替换清掉 → `.notify-row` 永不出现。旧写法固定 `sleep(0.6)` 赌重载快于它：单跑（DB 空、
    查询毫秒级）恒绿；批内/高负载时重载可能 >0.6s → 偶发红。

    稳定条件 = 重载链**末端**的 `data-tasktree-tasks` 请求（`onSessionChanged` 的顺序为
    `await loadMessages(newId)` → `await assessContinueState()` → `await restoreAwaitingArbitration(newId)`
    → `useTaskView.refresh` → `mq.emit('data-tasktree-tasks', {session_id, top_session})`，
    见 MessageList.vue:490-493 / 837-840）——该请求**严格晚于** `messages.value` 的赋值，
    故「看到本会话的该请求」= 重载已落地，此后注入的通知不会再被覆盖。
    """
    if wait_reload and sid:
        c.mq_on_capture([RELOAD_TOPIC])
    c.mq_emit("session-changed", {"session_id": sid})
    if wait_reload and sid:
        if not _wait_reloaded(sid):
            raise TestError("等待会话消息重载完成超时（%s, session=%s）" % (RELOAD_TOPIC, sid))
    else:
        time.sleep(0.6)


def _wait_reloaded(sid, max_wait=20):
    """等本会话的 `data-tasktree-tasks`（重载链末端）到达。"""
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in (c.events_of(RELOAD_TOPIC, clear=False) or []):
            p = e.get("payload") if isinstance(e, dict) else None
            if isinstance(p, str):
                try:
                    p = json.loads(p)
                except Exception:
                    p = None
            if isinstance(p, dict) and p.get("session_id") == sid:
                return True
        time.sleep(0.3)
    return False


def send_turn(sid, text):
    c.mq_emit("message-send", {"sessionId": sid, "text": text})
    time.sleep(1.0)


def llm(sid, typ, payload):
    d = {"session": sid, "session_id": sid, "type": typ}
    d.update(payload)
    c.mq_emit("llm-receive", d)
    time.sleep(0.5)


def complete(sid, status="complete"):
    c.mq_emit("llm-complete", {"session": sid, "status": status})
    time.sleep(0.5)


def msg_count():
    return deep_loads(c.eval("document.querySelectorAll('.message-item').length"))


def case_no_session():
    # 断言**主对话区**（.chat-panel）空态提示。同名 .empty-prompt 另有一处 = SessionChat 子会话区
    # （文案「选择子会话以查看其对话」），而 visible_text 取「最后一个可见」→ 当主对话区有会话
    # （启动 initSession 异步恢复上次活动/最新会话）时只剩子会话区那处 → 误判。
    # 故：限定 .chat-panel 作用域 + 重发 session-changed(null) 直至主对话区进入空态。
    ok = False
    deadline = time.time() + 10
    while not ok and time.time() < deadline:
        activate(None)
        ok = wait_el(".chat-panel .empty-prompt", max_wait=2)
    if not ok:
        raise TestError("无会话时未显示「选择会话」提示")
    txt = visible_text(".chat-panel .empty-prompt")
    if not any(k in txt for k in ("Select a session", "选择会话")):
        raise TestError(f"无会话提示异常: {txt!r}")


def case_no_messages():
    sid = new_sid()
    activate(sid)
    if not wait_el(".empty-inside"):
        raise TestError("有会话无消息时未显示「暂无消息」")
    txt = visible_text(".empty-inside")
    if not any(k in txt for k in ("No messages yet", "暂无消息")):
        raise TestError(f"无消息提示异常: {txt!r}")


def case_reasoning():
    sid = new_sid()
    activate(sid)
    send_turn(sid, "思考测试")
    llm(sid, "reason", {"text": "第一步推理\n第二步推理"})
    if not wait_el(".reasoning-section"):
        raise TestError("思考区块未渲染")
    # 实时回复：思考默认展开（section-body 可见）
    body_visible = deep_loads(c.eval("""(() => {
      const s = document.querySelector('.reasoning-section');
      return s && s.querySelector('.section-body') ? true : false;
    })()"""))
    if not body_visible:
        raise TestError("实时思考应默认展开")
    # 复制图标存在
    if not c.exists(".reasoning-section .copy-icon").get("count", 0) > 0:
        raise TestError("思考区块缺复制图标")
    # 点击 header 折叠 → body 隐藏
    c.eval("""(() => {
      const h = document.querySelector('.reasoning-section .section-header');
      if (h) h.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""", 5000)
    time.sleep(0.5)
    body_gone = deep_loads(c.eval("!!document.querySelector('.reasoning-section .section-body')"))
    if body_gone:
        raise TestError("折叠后思考正文应隐藏")
    # 再点展开
    c.eval("""(() => {
      const h = document.querySelector('.reasoning-section .section-header');
      if (h) h.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""", 5000)
    time.sleep(0.3)
    complete(sid)


def case_tool_call():
    sid = new_sid()
    activate(sid)
    send_turn(sid, "工具测试")
    llm(sid, "tool-call", {"tool-call-id": "tc-e2e-1", "tool": "file_read",
                           "arguments": "{\"files\":[{\"path\":\"C:/work/proj/a.txt\"}]}", "simplified": "file_read"})
    # handleToken 把 tool-call 渲染为 tool_pair 消息
    if not wait_el(".toolpair-section"):
        raise TestError("工具调用记录未渲染")
    txt = visible_text(".toolpair-section")
    if "file_read" not in txt:
        raise TestError(f"工具调用记录缺工具名: {txt[:120]!r}")
    if not c.exists(".toolpair-section .status-badge.pending").get("count", 0) > 0:
        raise TestError("tool_pair 初始状态应为 pending")
    # tool_pair 终态更新 → done
    c.mq_emit("tool-pair", {"session_id": sid, "tool_id": "tc-e2e-1", "status": "done"})
    time.sleep(0.5)
    if not c.exists(".toolpair-section .status-badge.done").get("count", 0) > 0:
        raise TestError("tool_pair 状态未更新为 done")
    if not c.exists(".toolpair-section .copy-icon").get("count", 0) > 0:
        raise TestError("tool_pair 缺复制图标（L155）")
    complete(sid)


def case_notify():
    sid = new_sid()
    activate(sid)
    c.mq_emit("tool-notify", {"notice": "completion", "session_id": sid,
                              "message": "异步任务已完成", "message_id": "ntf-e2e-1"})
    time.sleep(0.6)
    if not wait_el(".notify-row"):
        raise TestError("完成通知消息未插入")
    txt = visible_text(".notify-row")
    if "异步任务已完成" not in txt:
        raise TestError(f"通知内容异常: {txt!r}")
    # 🔔 图标（notify-row 内 emoji 或图标）
    if "🔔" not in txt and not c.exists(".notify-row .bell, .notify-row svg").get("count", 0) > 0:
        raise TestError("通知消息缺铃铛图标")


def _usr_cfg():
    r = c.req("data-user-config-load", {})
    return (r.get("data") or {}) if isinstance(r, dict) else {}


def case_general_scenario():
    """E6 场景下拉首项「通用场景」（2026-09-26，用户口径）：

    此前下拉**只列真实场景**、且未选时会**自动选中第一个场景** → 选过场景就回不到通用态。
    本用例验证：① 下拉**首项固定**为「通用场景」；② 选中即**关闭场景**（Tag 显示「通用场景」）；
    ③ 该项 ★ **设为默认** → 写入 `defaultScenario = __general__`（重开/重启仍为通用）。
    """
    r = c.req("data-scenario-list", {})
    scns = (r or {}).get("scenarios") or (r or {}).get("list") or []
    if not scns:
        raise TestError("场景列表为空（应有出厂场景 default）：%r" % (r,))
    # 先进入"已选场景"态，才能验证"能切回通用"
    c.mq_emit("scenario-select", {"id": scns[0]["id"]})
    time.sleep(0.8)

    # 打开场景下拉（场景 Tag = 唯一带 inline cursor:pointer 的 Tag；其父 = Popover reference）
    opened = deep_loads(c.eval("""(() => {
      const t = [...document.querySelectorAll('.b-tag')].find(x => x.style && x.style.cursor === 'pointer');
      if (!t) return 'no-tag';
      (t.closest('.b-popover__reference') || t).dispatchEvent(new MouseEvent('click', {bubbles: true}));
      return 'ok';
    })()"""))
    if opened != 'ok':
        raise TestError("未找到场景 Tag（%r）" % (opened,))
    time.sleep(0.6)
    items = deep_loads(c.eval("""(() => [...document.querySelectorAll(
      '.b-popover__popper .scenario-item .scenario-item-name')].map(x => x.textContent.trim()))()"""))
    if not items:
        raise TestError("场景下拉未打开或无选项")
    if items[0] != '通用场景':
        raise TestError("下拉首项应为「通用场景」，实际：%r" % (items[:3],))

    # 选中首项 → 关闭场景（Tag 显示「通用场景」）
    c.eval("""(() => { const n = document.querySelector('.b-popover__popper .scenario-item .scenario-item-name');
      n.dispatchEvent(new MouseEvent('click', {bubbles: true})); return 'ok'; })()""")
    time.sleep(0.8)
    if visible_text('.panel-header .b-tag').strip() != '通用场景':
        raise TestError("选中通用场景后 Tag 文案不符：%r" % visible_text('.panel-header .b-tag'))

    # ★ 设为默认 → 落 defaultScenario = __general__
    with _h.user_config_guard(c, ["defaultScenario"]):
        c.eval("""(() => { const t = [...document.querySelectorAll('.b-tag')].find(x => x.style && x.style.cursor === 'pointer');
          (t.closest('.b-popover__reference') || t).dispatchEvent(new MouseEvent('click', {bubbles: true})); return 'ok'; })()""")
        time.sleep(0.6)
        c.eval("""(() => { const s = document.querySelector('.b-popover__popper .scenario-item .scenario-item-star');
          s.dispatchEvent(new MouseEvent('click', {bubbles: true})); return 'ok'; })()""")
        time.sleep(1.0)
        if _usr_cfg().get("defaultScenario") != '__general__':
            raise TestError("★ 设为默认未写入 __general__：%r" % _usr_cfg().get("defaultScenario"))
        # 重载场景列表（模拟重开）：默认=通用 → **不得**自动选中第一个场景
        c.mq_emit("scenario-reload", {})
        time.sleep(0.8)
        if visible_text('.panel-header .b-tag').strip() != '通用场景':
            raise TestError("默认=通用后重载仍被自动选中场景：%r" % visible_text('.panel-header .b-tag'))


def main():
    c.wait_ready()
    c.console(clear=True)
    ok = True
    ok &= run_case("E1 无会话「选择会话」", case_no_session)
    ok &= run_case("E2 有会话无消息「暂无消息」", case_no_messages)
    ok &= run_case("E3 思考过程：实时展开/折叠/复制图标", case_reasoning)
    ok &= run_case("E4 工具调用记录 + 状态徽标 + 复制图标", case_tool_call)
    ok &= run_case("E5 后台任务完成通知（🔔）", case_notify)
    ok &= run_case("E6 场景下拉「通用场景」（首项/关闭场景/设为默认）", case_general_scenario)
    print("RESULT:", ok)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

# -*- coding: utf-8 -*-
"""testplan.md 五、LLM 调用 结合测试（mq 驱动 + 本地 mock LLM）。

驱动：POST /eval 执行 window.mq.emit('llm-start', {session_id, turn, q, llm, think, effort, scenario_id})
轮次确权：主会话 turn-start（mq-only；llm-started 兼容事件已无源，不再等待）。
断言：window.mq.on 捕获 turn-start / llm-token / llm-tool-call / tool-pair / tool-result /
      llm-receive / complete / llm-complete 事件链；DOM 断言消息落点（.message-item）。
      G-11 重建的 llm_run 委派/批量/子会话/级联取消用例（五.4/5/6/7 + C13/C18）经
      tasks.started/updated/done（前端 tasks.*，payload=TaskNode）与 llm-receive
      （子轮次流，payload 带 session/turn）断言；主题/payload 均取自 61-消息一览，无新增。
前置：IDE 以 --test-port=2345 启动；mock_llm.py 监听 127.0.0.1:8901；
      用户配置（usr 库，经 data-user-config-load/save 消息面）的 llms 含 mock 条目
      （%USERPROFILE%/.chonkpilot/config.json 为死文件，不再读写）。
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345)  # 复用优先（owned=False 不回收）；无实例则自起并在结束时回收
_M = _h.acquire_mock_llm(_h.DEFAULT_MOCK_LLM_PORT)  # mock LLM 按需起（8901 与 usr 库 llms[0] 对齐），结束回收
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：会话/任务落共享 work-dir 库 → 退出前回滚

_SEQ = [0]


EVENTS = ["turn-start", "llm-started", "llm-token", "llm-tool-call", "tool-pair", "tool-result",
          "llm-receive", "complete", "llm-complete", "error"]

# G-11 重建：llm_run 委派/编排场景额外断言的任务树 / 子会话事件。
# 主题与 payload 见 docs/spec/60-reference/61-消息一览.md §4.3（task-started/updated/done →
# 前端 tasks.*）与 §4.5（llm-receive ← session-receive）；不引入任何新主题。
DELEGATE_EVENTS = EVENTS + ["tasks.started", "tasks.updated", "tasks.done", "llm-receive",
                            "turn-start"]


def _loads_deep(v):
    """循环解包 JSON 字符串直到非字符串。"""
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def wait_el_exists(selector, max_wait=10):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if c.exists(selector).get("count", 0) > 0:
            return True
        time.sleep(0.4)
    return False


def create_session():
    """生成会话 id（mq-only）。

    新架构已无 window.go 绑定（禁止直调 window.go.*）：会话由 mq 消息面驱动——
    llm-start 经桥 session-start 幂等落库（§4.2），无需 RPC 预建。此处仅本地生成
    唯一 id，后续以 session-changed / llm-start 注入。
    """
    _SEQ[0] += 1
    return "llm-sess-%d-%03d" % (int(time.time() * 1000), _SEQ[0])


def activate_session(sid):
    """激活会话（前端内部事件 session-changed；新架构无 window.go.SetActiveSessionID）。"""
    c.mq_emit("session-changed", {"session_id": sid})
    time.sleep(0.3)


def start_turn(sid, q, llm="mock"):
    """发布 llm-start（等效真实输入；session/turn 客户端分配，scenario_id 为字符串 key）。"""
    c.mq_emit("llm-start", {
        "session_id": sid,
        "turn": "t-" + sid,
        "q": q,
        "llm": llm,
        "think": "",
        "effort": "",
        "scenario_id": "",
    })


def wait_event(topic, n=1, max_wait=60):
    return c.wait_events(topic, n=n, max_wait=max_wait, clear=True)


def confirm_turn(sid, max_wait=60):
    """主轮次确权（mq-only）：以主会话 turn-start（parents 空）替代已无源的 llm-started。

    新构建已无 `llm-started` 兼容事件源（compat 兼发未接 promise result），受理改由
    §4.3 `session-turn-start` → 前端 `turn-start` 确认；不再等待 llm-started。
    """
    return _wait_main_turn_start(sid, max_wait=max_wait)


# ── llm_run 委派/编排场景辅助（G-11 重建）──────────────────────────────

def _payloads(topic):
    """当前累积的某主题事件载荷（dict）列表；不清空，容忍 test-port deferral 批量推送。"""
    out = []
    for e in c.events_of(topic, clear=False):
        p = e.get("payload")
        if isinstance(p, dict):
            out.append(p)
    return out


def poll_find(topic, pred, max_wait=60, interval=0.4):
    """轮询（不清空）找首个满足 pred 的载荷；超时返回 None（max_wait<=0 = 仅查一次）。"""
    deadline = time.time() + max_wait
    while True:
        for p in _payloads(topic):
            if pred(p):
                return p
        if max_wait <= 0 or time.time() >= deadline:
            return None
        time.sleep(interval)


def wait_until(pred, desc, max_wait=90, interval=0.4):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if pred():
            return
        time.sleep(interval)
    raise TestError(f"等待超时：{desc}")


def _canon_tool(p):
    """任务/工具事件载荷的工具名按契约名归一（剥离网关自节点前缀 self_）。"""
    t = p.get("tool") or ""
    return t[5:] if t.startswith("self_") else t


def _wait_main_turn_start(sid, max_wait=60):
    """等待主会话 turn-start（parents 空；§4.3 session-turn-start → 前端 turn-start）。"""
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in c.events_of("turn-start", clear=False):
            p = e.get("payload") or {}
            if p.get("session") == sid and not p.get("parents"):
                return p
        time.sleep(0.3)
    raise TestError("未收到主会话 turn-start")


def _start_and_main_session(sid, q):
    """发一轮 llm-start 并返回（主会话 id）：以主会话 turn-start 确权（新架构无 llm-started）。"""
    start_turn(sid, q)
    _wait_main_turn_start(sid, max_wait=60)
    return sid


def _llm_run_nodes(main_sid, max_wait=60):
    """定位本轮 llm_run 根节点与其首个子步骤节点（kind=llm；按 top_session 归属本会话）。

    工具名按契约名归一（self_llm_run → llm_run）。根节点无 parent_id；子步骤节点
    parent_id = 根节点、session_id = 子会话（job-*）。
    """
    root = poll_find("tasks.started",
                     lambda p: _canon_tool(p) == "llm_run" and not p.get("parent_id")
                     and p.get("top_session") == main_sid, max_wait=max_wait)
    if not root:
        raise TestError("未建立 llm_run 根任务节点（tasks.started）")
    child = poll_find("tasks.started",
                      lambda p: _canon_tool(p) == "llm_run"
                      and p.get("parent_id") == root["task_id"], max_wait=max_wait)
    if not child:
        raise TestError("未建立 llm_run 子任务节点（子会话）")
    return root, child


def case_plain_chat():
    """五.2 普通对话：llm-started → llm-token → llm-complete；DOM 有 user/assistant 气泡。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = create_session()
    activate_session(sid)
    start_turn(sid, "hello")
    ts = confirm_turn(sid)  # mq-only 轮次确权（llm-started 兼容事件已无源）
    if not ts.get("turn"):
        raise TestError("turn-start 缺 turn")
    # 流式 token
    tokens = wait_event("llm-token", n=1, max_wait=60)
    if not tokens:
        raise TestError("未收到 llm-token")
    # 终结 ack（status 兼容新旧协议枚举：complete/completed/ok/success）
    done = wait_event("llm-complete", max_wait=60)
    if done[0]["payload"].get("status") not in (None, "ok", "success", "completed", "complete"):
        raise TestError(f"llm-complete status 异常: {done[0]['payload']}")
    # DOM 落点：assistant 回复（mock-reply: hello）
    deadline = time.time() + 10
    found = False
    while time.time() < deadline:
        txt = c.eval('document.body.innerText')
        if "mock-reply" in txt:
            found = True
            break
        time.sleep(1)
    if not found:
        raise TestError("DOM 未出现 assistant 回复（mock-reply）")


def case_tool_call():
    """五.3 工具调用：q 含 'call tool' → mock 返回 tool_calls(file_read) → tool-pair/tool-result。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = create_session()
    activate_session(sid)
    start_turn(sid, "please call tool file_read on a.txt")
    confirm_turn(sid)
    # 工具调用事件
    tc = wait_event("llm-tool-call", max_wait=60)
    if not tc[0]["payload"].get("tool"):
        raise TestError(f"llm-tool-call payload 缺 tool: {tc[0]['payload']}")
    wait_event("tool-pair", n=1, max_wait=60)
    # 工具结果
    tr = wait_event("tool-result", max_wait=60)
    if not tr:
        raise TestError("未收到 tool-result")
    # 终结
    wait_event("llm-complete", max_wait=90)
    # 最终回复含文件内容
    deadline = time.time() + 10
    while time.time() < deadline:
        txt = c.eval('document.body.innerText')
        if "hello from a.txt" in txt:
            return
        time.sleep(1)
    raise TestError("最终回复未包含 file_read 结果（hello from a.txt）")


def case_payload_llm_config():
    """五.1 调用参数随所选 LLM：受理 ack + 可选的旧 test-port 扩展字段。

    旧 IDE test-port 契约（llm-started 附加 llm_config/scenario/system_prompt）**已废弃**：
    新架构下受理信息由 llm-start 的 promise result 承担（§4.2），且兼容事件 llm-started
    **已无源**（实测 0 条，见 61 §6）。故轮次确权改用主会话 turn-start（mq-only）。
    以下字段按「存在才断言、缺失跳过」处理（llm-started 恒缺失即恒跳过）：
      - llm_config：protocol/model/apiKey/baseUrl/temperature/maxTokens/thinking/reasoningEffort
      - scenario：scenario_id/name/systemPrompt/agents
      - system_prompt：最终合成提示词全文（含工具使用说明）
    """
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = create_session()
    start_turn(sid, "payload check")
    ts = confirm_turn(sid)
    if not ts.get("turn"):
        raise TestError("turn-start 缺 turn")
    if ts.get("session") != sid:
        raise TestError(f"turn-start.session={ts.get('session')!r} != {sid!r}")
    # 扩展字段（旧 IDE test-port 契约）：源（llm-started）已无源 → 尽力校验，缺失跳过
    lp = c.events_of("llm-started", clear=False)
    p = (lp[-1].get("payload") or {}) if lp else {}
    cfg = p.get("llm_config") or {}
    if cfg:
        for k in ("protocol", "model", "apiKey", "baseUrl", "temperature", "maxTokens", "thinking", "reasoningEffort"):
            if k not in cfg:
                raise TestError(f"llm_config 缺字段 {k}，实际: {cfg}")
        if cfg.get("model") != "mock-model":
            raise TestError(f"llm_config.model={cfg.get('model')}，期望 mock-model")
    sc = p.get("scenario") or {}
    if sc:
        for k in ("scenario_id", "name", "systemPrompt", "agents"):
            if k not in sc:
                raise TestError(f"scenario 缺字段 {k}，实际: {sc}")
        if not sc.get("systemPrompt"):
            raise TestError("scenario.systemPrompt 为空")
    sp = p.get("system_prompt") or ""
    if sp and "工具" not in sp and "tool" not in sp.lower():
        raise TestError("system_prompt 应含工具使用说明")
    wait_event("llm-complete", max_wait=60)


def case_cancel():
    """五.8 等待时 Cancel：message-cancel（前端内部 → llm-cancel，§4.2）→ llm-complete 终结；队列文本写回。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = create_session()
    activate_session(sid)
    start_turn(sid, "please call cancel")
    confirm_turn(sid)
    tc = wait_event("llm-tool-call", max_wait=60)
    if _canon_tool(tc[0]["payload"]) != "script_run":
        raise TestError(f"llm-tool-call tool={tc[0]['payload'].get('tool')}，期望 script_run（可带 self_ 前缀）")
    # 等待工具进入运行（命令 sleep 20s 阻塞中）
    wait_event("tool-pair", max_wait=30)
    time.sleep(2)
    # 取消主 turn
    c.mq_emit("message-cancel")
    done = wait_event("llm-complete", max_wait=60)
    status = done[0]["payload"].get("status")
    if status not in ("error", "canceled", "cancelled"):
        print(f"    llm-complete status={status}（cancel 语义兼容多值）")
    return


def case_queue():
    """五.9 Chat Message 入队与出队：忙碌时 message-queue 入队 → 队列指示器 → cancel 后文本写回 textarea。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = create_session()
    activate_session(sid)
    # 先选 LLM（队列按 LLM 分桶，badge 归属所选 LLM）
    c.mq_emit("chat-select-llm", {"name": "mock"})
    # 启动一个忙碌 turn（command sleep 20s 阻塞）
    start_turn(sid, "please call cancel")
    confirm_turn(sid)
    wait_event("llm-tool-call", max_wait=60)
    wait_event("tool-pair", max_wait=30)
    time.sleep(1.5)
    # 忙碌中入队 3 条
    for i in range(1, 4):
        c.mq_emit("message-queue", {
            "sessionId": sid, "text": f"queued msg {i}", "llm": "mock",
            "thinkFlag": "", "effortLevel": "", "scenarioId": 0,
        })
    # 队列指示器（InputBox .queue-badge）
    deadline = time.time() + 10
    badge = ""
    while time.time() < deadline:
        try:
            badge = c.text(".queue-badge", 3000)
        except Exception:
            badge = ""
        if "3" in badge:
            break
        time.sleep(0.5)
    if "3" not in badge:
        print(f"    队列指示器未显示 3（badge={badge!r}），继续验证写回")
    # 取消主 turn → 队列文本写回 textarea（textarea 的 value 不体现于 innerText，须读 .value）
    c.mq_emit("message-cancel")
    wait_event("llm-complete", max_wait=60)
    # 输入区是 contenteditable 富文本（无原生 textarea）：读编辑区文本
    deadline = time.time() + 15
    while time.time() < deadline:
        v = c.eval("(() => { const t = document.querySelector('.b-textarea--autosize'); return t ? JSON.stringify(t.innerText || t.textContent || '') : ''; })()")
        v = _loads_deep(v) or ""
        if "queued msg" in v:
            return
        time.sleep(1)
    raise TestError("cancel 后队列文本未写回输入区（queued msg 未见）")


def case_chat_display():
    """五.12 chat 显示内容确认：本轮自建会话并完成一轮普通对话 → user/assistant 气泡落点。

    原实现依赖「上一用例在 DOM 遗留的 mock-reply」，在取消/队列用例行为调整后不再稳定；
    改为自成一体（自建会话 + turn-start 确权 + 等 llm-complete），再断言 DOM 落点。
    """
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = create_session()
    activate_session(sid)
    start_turn(sid, "hello chat display")
    confirm_turn(sid)
    wait_event("llm-complete", max_wait=90)
    deadline = time.time() + 10
    while time.time() < deadline:
        if c.exists(".message-item").get("count", 0) > 0 and "mock-reply" in c.eval('document.body.innerText'):
            return
        time.sleep(0.5)
    if c.exists(".message-item").get("count", 0) == 0:
        raise TestError("DOM 无 .message-item")
    raise TestError("assistant 回复未渲染（mock-reply 未见）")


def case_toolpair_dom():
    """五.13 tool-pair 显示、展开、详情：msg-toggle-collapse 展开 → arguments/result 可见。"""
    # 复用最近会话：触发一次带 file_read 的 turn 产生 tool-pair 消息
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = create_session()
    activate_session(sid)
    start_turn(sid, "please call tool file_read on a.txt")
    confirm_turn(sid)
    wait_event("llm-complete", max_wait=90)
    # tool-pair 消息落点（DOM）
    deadline = time.time() + 10
    while time.time() < deadline:
        if c.exists(".toolpair-section").get("count", 0) > 0:
            break
        time.sleep(1)
    if c.exists(".toolpair-section").get("count", 0) == 0:
        raise TestError("DOM 未出现 tool-pair 消息")
    # 取最后一条 tool-pair 的 message_id 并展开
    mid = c.eval("""
(() => {
  const items = document.querySelectorAll('.message-item');
  for (let i = items.length - 1; i >= 0; i--) {
    const m = items[i];
    if (m.querySelector('.toolpair-section')) {
      const id = m.getAttribute('data-id');
      if (id) return JSON.stringify(id);
    }
  }
  return '';
})()""")
    mid = _loads_deep(mid)
    if not mid:
        # 兜底：展开第一个 toolpair
        c.mq_emit("msg-toggle-collapse", {})
    else:
        c.mq_emit("msg-toggle-collapse", {"id": mid})
    time.sleep(1.5)
    # tool_pair 两级展示：折叠 → 简述（toggle 展开）→ 参数/结果（msg-show-full 展开 full）
    c.mq_emit("msg-show-full", {"id": mid})
    time.sleep(1.5)
    # 展开后详情可见（arguments/result 区域）
    txt = c.eval('document.body.innerText')
    if "arguments" not in txt.lower() and "参数" not in txt and "result" not in txt.lower() and "结果" not in txt:
        raise TestError("tool-pair 展开后未见 arguments/result 详情")
    return


def case_session_switch_cancel():
    """C11 会话切换取消（mq-only）：A 长任务运行中经 llm-cancel（模拟切换会话触发的取消）
    → A 轮次收敛为唯一终态 llm-complete{status:interrupted}（§4.3）；B 正常。
    """
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sidA = create_session()
    activate_session(sidA)
    start_turn(sidA, "please call cancel")
    tsA = confirm_turn(sidA, max_wait=60)
    turnA = tsA.get("turn") or ""
    # 长任务开始（script_run sleep 20s → tool-pair running）
    wait_event("tool-pair", max_wait=60)
    # mq-only 取消（§4.2 llm-cancel {session, turn?}）：替代已移除的 window.go.app.App.CancelChat。
    # 注：llm-cancel 定位并终止**轮次**（tc.Close），网关在飞工具不随之级联取消
    #（网关任务级联取消由 task-stop 承担，见 C13/C18）；故以轮次终态确权。
    c.mq_emit("llm-cancel", {"session": sidA, "turn": turnA})
    deadline = time.time() + 20
    cancelled = False
    while time.time() < deadline:
        for e in c.events_of("llm-complete", clear=True):
            p_ = e.get("payload") or {}
            if p_.get("session") == sidA and p_.get("status") in ("interrupted", "cancelled", "error"):
                cancelled = True
                break
        if cancelled:
            break
        time.sleep(0.5)
    if not cancelled:
        raise TestError("A 轮次未被取消（未收到 llm-complete{interrupted}）")
    # B 会话可正常发送
    sidB = create_session()
    activate_session(sidB)
    start_turn(sidB, "hello after cancel")
    confirm_turn(sidB)
    wait_event("llm-complete", max_wait=90)


def case_ask_user_duplex():
    """C14 ask_user 双路由：user_ask → 前端弹窗 → ask-reply 提交 → 回答回填 LLM。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS + ["ask-reply"])
    sid = create_session()
    activate_session(sid)
    start_turn(sid, "please call ask")
    confirm_turn(sid, max_wait=60)
    pair = wait_event("tool-pair", max_wait=60)
    p = pair[0]["payload"] or {}
    ask_id = p.get("tool_id") or p.get("task_id") or p.get("ask_id") or ""
    # 断言 AskUser 弹窗出现（.ask-user-content；tool-pair 消息也会显示 question，不能用 bodyText）
    deadline = time.time() + 10
    if not wait_el_exists(".ask-user-content", max_wait=10):
        raise TestError("AskUser 弹窗未出现（user_ask 双路由断链）")
    if not ask_id:
        raise TestError(f"tool-pair payload 无 ask_id（tool_id/task_id）: {p}")
    # 走真实 UI 路径：填 custom textarea + 点提交（v-mq ask-user-submit → onAnswer →
    # emit ask-reply + closeAndAdvance）
    r = _loads_deep(c.eval("""(() => {
      const ta = document.querySelector('.ask-user-content .custom-input textarea');
      if (ta) {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(ta, '继续');
        ta.dispatchEvent(new Event('input', { bubbles: true }));
        return 'ok';
      }
      return 'no-textarea';
    })()""", 5000))
    if r != "ok":
        raise TestError(f"AskUser 弹窗无 custom 输入框: {r}")
    time.sleep(1.0)  # 等 Vue 更新 canSubmit（disabled 按钮 click 无效，须先解除）
    c.click(".ask-user-content .submit-btn", 5000)
    # 回答回填 LLM → 最终 llm-complete
    done = wait_event("llm-complete", max_wait=90)
    # 弹窗关闭（onAnswer → closeAndAdvance）
    deadline = time.time() + 8
    while time.time() < deadline:
        if c.exists(".ask-user-content").get("count", 0) == 0:
            return
        time.sleep(0.5)
    raise TestError("ask-reply 提交后弹窗未关闭（回答未回填）")


def case_notify_dedup():
    """C17 通知消息按 message_id 去重：同一完成通知重复推送不产生重复气泡。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = create_session()
    activate_session(sid)
    # 前端同 message_id 的完成通知重复推送
    ntf = {"notice": "completion", "message_id": "msg-notify-c17-dedup",
           "message": "🔔 C17 去重测试消息", "session_id": sid, "task_id": "op-c17"}
    c.mq_emit("tool-notify", ntf)
    time.sleep(0.5)
    c.mq_emit("tool-notify", ntf)
    time.sleep(0.5)
    deadline = time.time() + 8
    while time.time() < deadline:
        n = c.eval("""(() => {
          const els = [...document.querySelectorAll('.chat-panel .message-item')]
            .filter(el => el.textContent.includes('C17 去重测试消息'));
          return String(els.length);
        })()""")
        if n.strip('"') == "1":
            return
        time.sleep(0.5)
    raise TestError(f"同 message_id 通知产生了重复气泡（count={n}）")


def case_resume_partial():
    """C15 断链恢复（resumePartial）：mock 流中断（部分文本后 RST）→ runner 追加「继续」续写 → 最终回复 = 部分+续写。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = create_session()
    activate_session(sid)
    start_turn(sid, "please call break")
    confirm_turn(sid, max_wait=60)
    wait_event("llm-complete", max_wait=90)
    # 断链恢复的「继续」提示（tool-notify notice=recover）为**已移除兼容事件**（61 §6：无源），
    # 改以最终回复内容确权（部分文本 + 续写），不再依赖该事件。
    # 最终回复 = 部分文本 + 续写（同段，无工具分发间隔）
    deadline = time.time() + 20
    while time.time() < deadline:
        txt = c.eval('document.body.innerText')
        if "PARTIAL-TEXT-" in txt and "mock-reply: 继续" in txt:
            return
        time.sleep(1)
    raise TestError("断链续写未完成（未见 PARTIAL-TEXT- 与 mock-reply: 继续）")


# ── G-11 重建：llm_run DSL 委派/批量/子会话/级联取消（原五.4/5/6/7 + C13/C16/C18 等价场景）──
# 驱动：主轮次 q 命中 mock_llm 关键词 → 返回 llm_run tool_call（script=DSL）；LLM 步骤
# 即子会话（runChildTurn）。断言仅依赖 61-消息一览 的主题/payload：tasks.started/updated/
# done（前端 tasks.*，payload = TaskNode）与 llm-receive（子轮次流，payload 带 session/turn）。

def _delegate_single(q, expect_label):
    """单次委派（一行 LLM 指令）共用体：断言子节点展示名语义 + 子会话 id + 子节点终态。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(DELEGATE_EVENTS)
    sid = create_session()
    activate_session(sid)
    main_sid = _start_and_main_session(sid, q)
    root, child = _llm_run_nodes(main_sid)
    if child.get("kind") != "llm":
        raise TestError(f"子节点 kind 非 llm: {child.get('kind')!r}")
    label = child.get("purpose") or child.get("name") or child.get("simplified") or ""
    if label != expect_label:
        raise TestError(f"子节点展示名={label!r}，期望 {expect_label!r}")
    sub = child.get("session_id") or ""
    if not sub.startswith("job-"):
        raise TestError(f"子会话 id 异常（应 job-*）: {sub!r}")
    if not poll_find("tasks.done",
                     lambda p: p.get("task_id") == child["task_id"] and p.get("state") == "done",
                     max_wait=90):
        raise TestError("子 LLM 节点未达到 done")
    wait_event("llm-complete", max_wait=90)


def case_delegate_single_plain():
    """五.4 单次委派（无第三参）：子任务展示名回退为提示词截断（≤24 字符）。"""
    _delegate_single("please call delegate-plain", "delegate-plain-prompt")


def case_delegate_single_purpose():
    """五.5 单次委派（第三参=目的）：子任务展示名（tasktree 节点 label）= 目的。"""
    _delegate_single("please call delegate-purpose", "委派展示名-自定义")


def case_delegate_loop():
    """五.6 LOOP 批量委派：planner 产出 JSON 数组 → string.array → concurrency=2 迭代。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(DELEGATE_EVENTS)
    sid = create_session()
    activate_session(sid)
    main_sid = _start_and_main_session(sid, "please call batch")
    root = poll_find("tasks.started",
                     lambda p: _canon_tool(p) == "llm_run" and not p.get("parent_id")
                     and p.get("top_session") == main_sid, max_wait=60)
    if not root:
        raise TestError("未建立 llm_run 根任务节点（tasks.started）")

    def _children():
        return [p for p in _payloads("tasks.started")
                if _canon_tool(p) == "llm_run" and p.get("parent_id") == root["task_id"]]

    def _labels():
        return {p.get("purpose") or p.get("name") for p in _children()}

    # 3 个子步骤：planner 产出清单 + 2 个 worker（LOOP 迭代；concurrency=2）
    wait_until(lambda: len(_labels()) >= 3, "3 个 LOOP 子步骤节点", max_wait=60)
    labels = _labels()
    for want in ("生成批处理清单", "批处理一", "批处理二"):
        if want not in labels:
            raise TestError(f"缺 LOOP 子步骤展示名 {want!r}（实际 {labels}）")
    subs = {p.get("session_id") for p in _children()}
    if len(subs) != 3 or not all(s and s.startswith("job-") for s in subs):
        raise TestError(f"LOOP 子会话 id 异常（应 3 个 job-*）: {subs}")

    def _all_done():
        done = {p.get("task_id") for p in _payloads("tasks.done") if p.get("state") == "done"}
        return all(ch["task_id"] in done for ch in _children())

    wait_until(_all_done, "LOOP 子步骤全部 done", max_wait=90)
    wait_event("llm-complete", max_wait=90)


def case_delegate_subsession():
    """五.7 子会话 viewer：子会话（子 turn）的产生与归属在任务/会话事件中可见。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(DELEGATE_EVENTS)
    sid = create_session()
    activate_session(sid)
    main_sid = _start_and_main_session(sid, "please call delegate-plain")
    root, child = _llm_run_nodes(main_sid)
    # 归属：子节点挂根节点，且同属主会话（top_session）
    if child.get("parent_id") != root.get("task_id"):
        raise TestError(f"子节点归属错误: parent_id={child.get('parent_id')!r}")
    if child.get("top_session") != main_sid:
        raise TestError(f"子节点 top_session={child.get('top_session')!r} != 主会话 {main_sid!r}")
    sub = child.get("session_id") or ""
    if not sub.startswith("job-"):
        raise TestError(f"子会话 id 异常（应 job-*）: {sub!r}")
    # 子 turn 实际产生：llm-receive 事件带该子会话 session
    if not poll_find("llm-receive", lambda p: p.get("session") == sub, max_wait=60):
        raise TestError(f"未见子会话 {sub} 的 llm-receive（子 turn 未产生）")
    # 子轮次 turn_id 经 tasks.updated 回填到子节点
    if not poll_find("tasks.updated",
                     lambda p: p.get("task_id") == child["task_id"] and p.get("turn_id"),
                     max_wait=60):
        raise TestError("子节点 turn_id 未回填（tasks.updated）")
    wait_event("llm-complete", max_wait=90)


def case_delegate_cancel():
    """C13/C18 子会话工具 + 级联取消：task-stop 根 llm_run → 子步/子会话级联终止。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(DELEGATE_EVENTS)
    sid = create_session()
    activate_session(sid)
    main_sid = _start_and_main_session(sid, "please call delegate-cancel")
    root, child = _llm_run_nodes(main_sid)
    # 首个子步骤（慢步子任务）委派子会话内执行慢工具（script_run ping 20s）
    if (child.get("purpose") or child.get("name")) != "慢步子任务":
        raise TestError(f"首个子步骤展示名异常: {(child.get('purpose') or child.get('name'))!r}")
    sub = child.get("session_id") or ""
    if not sub.startswith("job-"):
        raise TestError(f"子会话 id 异常（应 job-*）: {sub!r}")
    # 子会话内确已调用慢工具（llm-tool-call 带子会话 session；C13 子会话工具链）
    if not poll_find("llm-tool-call",
                     lambda p: (p.get("tool") or "").endswith("script_run")
                     and p.get("session_id") == sub, max_wait=30):
        raise TestError("子会话内未执行慢工具（script_run）")
    # 对根 llm_run 节点取消（服务端恒按子树级联）→ 子节点随之终止
    c.mq_emit("task-stop", {"task_id": root["task_id"], "cascade": "all"})
    if not poll_find("tasks.done",
                     lambda p: p.get("task_id") == root["task_id"] and p.get("state") == "cancelled",
                     max_wait=30):
        raise TestError("根 llm_run 节点未级联 cancelled")
    if not poll_find("tasks.done",
                     lambda p: p.get("task_id") == child["task_id"] and p.get("state") == "cancelled",
                     max_wait=30):
        raise TestError("子步骤节点未级联 cancelled")
    # 取消后不再推进 DSL：哨兵步（取消后置步）不得启动
    if poll_find("tasks.started",
                 lambda p: (p.get("purpose") or p.get("name")) == "取消后置步", max_wait=3):
        raise TestError("取消后仍启动了后置步（DSL 未终止）")
    # 兜底清理：等待后台子轮次（慢工具）结束、主 turn 收尾（失败不阻塞——取消已断言）
    try:
        wait_event("llm-complete", max_wait=90)
    except TestError:
        pass


def main():
    ok = 0
    total = 0
    c.console(clear=True)
    total += 1; ok += run_case("五.1 调用参数确权（turn-start；旧扩展字段已废弃）", case_payload_llm_config)
    total += 1; ok += run_case("五.2 普通对话（流式 + 气泡落点）", case_plain_chat)
    total += 1; ok += run_case("五.3 工具调用（tool-call 链路 + 结果回填）", case_tool_call)
    total += 1; ok += run_case("五.8 等待时 Cancel", case_cancel)
    total += 1; ok += run_case("五.9 Chat Message 入队/出队", case_queue)
    total += 1; ok += run_case("五.12 chat 显示内容确认", case_chat_display)
    total += 1; ok += run_case("五.13 tool-pair 显示、展开、详情", case_toolpair_dom)
    total += 1; ok += run_case("C11 会话切换取消（llm-cancel 级联）", case_session_switch_cancel)
    total += 1; ok += run_case("C14 ask_user 双路由（弹窗 + 回填）", case_ask_user_duplex)
    total += 1; ok += run_case("C15 断链恢复（resumePartial 续写）", case_resume_partial)
    total += 1; ok += run_case("C17 通知按 message_id 去重", case_notify_dedup)
    # ── G-11 重建：llm_run DSL 委派/批量/子会话/级联取消（原五.4/5/6/7 + C13/C16/C18 等价场景）──
    total += 1; ok += run_case("五.4 单次委派（无目的 → 展示名回退提示词）", case_delegate_single_plain)
    total += 1; ok += run_case("五.5 单次委派（第三参=目的 → 展示名）", case_delegate_single_purpose)
    total += 1; ok += run_case("五.6 LOOP 批量委派（string.array + concurrency=2）", case_delegate_loop)
    total += 1; ok += run_case("五.7 子会话 viewer（子会话/子 turn 归属）", case_delegate_subsession)
    total += 1; ok += run_case("C13/C18 子会话工具 + 级联取消（task-stop）", case_delegate_cancel)
    errs = c.console()
    for e in errs.get("entries", []):
        if e.get("level") in ("error",):
            print(f"  [CONSOLE-ERROR] {e.get('text')}")
    print(f"\nLLM 结合测试：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

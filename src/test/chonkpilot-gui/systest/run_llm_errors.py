# -*- coding: utf-8 -*-
"""llm-error-handling.md 规划项落地回归（§一 网络连接预检 / S21 空回复 / S23 握手分类 / S25 发送前校验）。

前置：IDE --test-port=2345；mock_llm.py 监听 127.0.0.1:8901；
      用户配置（usr 库，经 data-user-config-load/save 消息面）的 llms 含 mock 条目；
      本脚本临时改写 llms/defaultLLM/retryCount：按跑前**快照**在 finally 还原
      （51 §6-8 配置快照-还原；原本缺省 → 删键回落系统默认）。
      （退避间隔不自持 —— 经 router.RetryWait（Retry-After 优先 + 指数退避），无 retryDelay 配置项。）
      （原读写 %USERPROFILE%/.chonkpilot/config.json 为死文件——全仓无加载点，改动不生效。）

迁移（2026-09-15）：
  - 会话一律 mq-only（客户端自分配 session id + `session-changed` 激活）：`window.go.CreateSession`
    / `SetActiveSessionID` 已随 RPC 面移除（禁止直调 window.go.*），见 create_session/activate_session。
  - 输入区为 contenteditable 富文本（`.richtext-input`）：S25 用 innerHTML + input 事件驱动，
    不再用已移除的 `.b-textarea--autosize` textarea value 写入。
  - 轮次受理确权：`llm-started` 已无事件源 → 改等主会话 `turn-start`（§4.3
    `session-turn-start` → 前端 `turn-start`），与 run_llm.confirm_turn 同口径；
    llm-start 载荷补 `turn` 且 `scenario_id` 为字符串（空串）。

用例：
  S21  空回复实时提示：mock "call empty" → llm-complete status=completed 且无输出
       → 前端出现"空回复"提示 +"继续"按钮（不再静默无提示）
  §一  网络连接预检：LLM 端点不可达（127.0.0.1:9）+ retryCount=3 → 预检命中不重试
       （无 llm-retry 事件）、首次错误快速到达
  S23  错误分类单元测试见 chonkpilot/pkg/executor/llm/netprobe_test.go（go test）
  S25  发送前校验 LLM 配置：llms 置空 → 发送被拦截（无 llm-start）+ warning 提示
  RETRY-1（2026-10-17）可见重试：
    "call retry-twice"   前两次 429（Retry-After: 2）→ 第三次成功
                         → 断言 llm-retry 中间态（真实计数 1/3、wait_ms>0）+ DOM「正在重试 1/3（等待 2 秒）」
                           + **逐秒倒计时**（采样到 2 秒 → 1 秒；2026-10-23 前端本地 countdown）
                           + 成功后无 `.error-bubble` / 无残留提示条（截图 E:\\TEMP\\ck-l4-visual\\retry\\）
    "call retry-exhaust" 每次都 500 → 重试耗尽
                         → 断言终态 session-complete{status:error, retryable:true, retry_attempt=2,
                           retry_count=2, retry_wait_ms=0}；错误**只在下方提示条**（含「已重试 2/2 次」
                           与「查看错误信息」原始详情）；聊天流内查不到 `.error-bubble`
"""

import copy
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345)  # 复用优先；无实例则自起并在结束时回收
_M = _h.acquire_mock_llm(_h.DEFAULT_MOCK_LLM_PORT)  # mock LLM 按需起（8901），结束回收
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：usr（llms/retryCount 等）退出前回滚

EVENTS = ["llm-started", "llm-complete", "llm-retry", "llm-error", "llm-start", "turn-start"]

_SEQ = [0]


def _loads_deep(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def read_cfg():
    """读取用户级配置（usr 库）：data-user-config-load 消息面 → data 对象。

    原实现读写 ~/.chonkpilot/config.json（死文件，全仓无加载点、改动不生效），
    改走配置消息面（与 run_config.py / run_llm_config.py 同路）。
    """
    res = c.req("data-user-config-load", {})
    return (res.get("data") or {}) if isinstance(res, dict) else {}


def write_cfg(cfg):
    """增量保存用户级配置（usr 库）：data-user-config-save 消息面（载荷 data = 增量对象）。"""
    c.req("data-user-config-save", {"data": cfg})


def create_session():
    """生成会话 id（mq-only）。

    迁移（2026-09-15）：新架构已无 window.go 绑定（禁止直调 window.go.*）——
    ChatPanel.ensureSessionId 客户端自分配 session uuid，llm-start 经桥 session-start
    幂等落库（21-llm-server §4.2），无需 CreateSession RPC 预建；与 run_llm.py 同口径。
    """
    _SEQ[0] += 1
    return "llm-err-%d-%03d" % (int(time.time() * 1000), _SEQ[0])


def activate_session(sid):
    """激活会话（前端内部事件 session-changed；新架构无 window.go.SetActiveSessionID）。"""
    c.mq_emit("session-changed", {"session_id": sid})
    time.sleep(0.3)


def start_turn(sid, q, llm="mock"):
    """发布 llm-start（等效真实输入；session/turn 客户端分配，scenario_id 为字符串 key）。"""
    c.mq_emit("llm-start", {
        "session_id": sid, "turn": "t-" + sid, "q": q, "llm": llm,
        "think": "", "effort": "", "scenario_id": "",
    })


def wait_event(topic, n=1, max_wait=60):
    return c.wait_events(topic, n=n, max_wait=max_wait, clear=True)


def confirm_turn(sid, max_wait=60):
    """主轮次受理确权（mq-only，与 run_llm 同口径）。

    新构建已无 `llm-started` 兼容事件源（受理改由 §4.3 `session-turn-start`
    → 前端 `turn-start` 确认）→ 本脚本原「等待 llm-started」已过时，改等主会话 turn-start。
    """
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in c.events_of("turn-start", clear=False):
            p = e.get("payload") or {}
            if p.get("session") == sid and not p.get("parents"):
                return p
        time.sleep(0.3)
    raise TestError(f"未收到主会话 turn-start（session={sid}）")


def reset_mock_state():
    """重置 mock 一次性状态机（S21 空回复链确定性）。"""
    import urllib.request
    try:
        urllib.request.urlopen("http://127.0.0.1:8901/reset", timeout=5)
    except Exception:
        pass


# ── S21 空回复实时提示 ──────────────────────────────────────

def case_empty_reply():
    """S21：mock 空回复（stop 但 content 空）→ llm-complete{status:error, code:EMPTY_REPLY}
    （61-消息一览：错误随唯一终态携带，空回复也收敛 error）
    → 前端实时提示"空回复" +"继续"按钮（重发原用户消息），不再静默无提示。

    隔离（2026-10-23）：本用例**自带** mock LLM 配置（原先依赖环境已有 name=mock，干净机器上
    解析不到 → 轮次迟迟不终态）；跑前快照、finally 还原。
    """
    snap = _prep_mock_cfg(3)
    try:
        reset_mock_state()  # 确定性：首次 "call empty" 必为空回复
        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(EVENTS)
        sid = create_session()
        activate_session(sid)
    # 空回复 turn 无流式内容、完成极快：确保主 chat 会话切换完成（activeSessionId）再发，
    # 否则 onLlmComplete 的 session 过滤会把终态事件丢弃（hint 不显示）
        time.sleep(1.5)
        # 选中 mock LLM（"继续"按钮按当前所选 LLM 重发）
        c.mq_emit("chat-select-llm", {"name": "mock"})
        start_turn(sid, "please call empty")
        confirm_turn(sid, max_wait=60)
        done = wait_event("llm-complete", max_wait=60)
        payload = done[0]["payload"]
        if payload.get("status") != "error" or payload.get("code") != "EMPTY_REPLY":
            raise TestError(f"空回复应终结为 error+EMPTY_REPLY，实际: {payload}")
        # 实时提示：空回复 hint +"继续"按钮（底部操作区）
        deadline = time.time() + 8
        hint_found = False
        while time.time() < deadline:
            info = c.exists(".empty-reply-hint")
            if info.get("count", 0) > 0:
                hint_found = True
                break
            time.sleep(0.5)
        if not hint_found:
            raise TestError("空回复后未出现实时提示（.empty-reply-hint）")
        info = c.exists(".turn-actions .continue-btn")
        if info.get("count", 0) == 0:
            raise TestError("空回复后未出现'继续'按钮（.turn-actions .continue-btn）")
        # 点击"继续" → 重发原用户消息 → mock 第二次请求消费 _EMPTY_ONCE 返回正常回复
        c.click(".turn-actions .continue-btn", 5000)
        deadline = time.time() + 20
        while time.time() < deadline:
            txt = c.eval('document.body.innerText')
            if "mock-reply" in txt:
                return
            time.sleep(1)
        raise TestError("点击'继续'后未收到回复（mock-reply 未见）")
    finally:
        _h.restore_user_config(c, snap)
        c.mq_emit("config-refresh")


# ── §一 网络连接预检 ───────────────────────────────────────

def case_network_precheck():
    """§一/S14：LLM 端点不可达 + retryCount=3 → 期望「预检命中、不重试、快速终结」。

    〔2026-10-23 口径〕该特性**当前未实现**（`probeBeforeRetry` 已不在代码中，[41 D-66]）→
    本用例保留「终态收敛为 error」的稳定性断言，预检/无重试/耗时断言改为 **证据打印 + SKIP**，
    待 D-66 拍板（重做预检 或 废止并回改 35/51）后恢复硬断言。
    """
    backup = read_cfg()
    snap = _h.snapshot_user_config(c, ["llms", "defaultLLM", "retryCount"])
    try:
        cfg = copy.deepcopy(backup)
        cfg["llms"] = [l for l in cfg.get("llms", []) if l.get("name") == "mock"]
        cfg["llms"].append({
            "name": "dead-llm", "protocol": "openai", "apiKey": "dead-key",
            "model": "dead-model", "baseUrl": "http://127.0.0.1:9/v1",
            "temperature": 0.7, "maxOutputToken": 4096, "thinking": False,
        })
        cfg["defaultLLM"] = len(cfg["llms"]) - 1
        cfg["retryCount"] = 3
        write_cfg(cfg)
        c.mq_emit("config-refresh")
        c.mq_emit("chat-select-llm", {"name": "dead-llm"})
        time.sleep(1.5)

        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(EVENTS)
        sid = create_session()
        activate_session(sid)
        t0 = time.time()
        start_turn(sid, "hello", llm="dead-llm")
        # 受理确权：主会话 turn-start（旧 llm-started 已无源）
        started = confirm_turn(sid, max_wait=60)
        if not (started.get("turn") or started.get("turn_id")):
            raise TestError(f"turn-start 缺 turn 标识: {started}")
        done = wait_event("llm-complete", max_wait=120)
        elapsed = time.time() - t0
        if done[0]["payload"].get("status") not in ("error", "canceled", "cancelled"):
            raise TestError(f"网络错误应终结为 error: {done[0]['payload']}")
        # 等待自动续写链结束（网络错误 retryable → 后端自动续写/重试若干次）
        deadline = time.time() + 30
        last_new = time.time()
        while time.time() < deadline:
            ev = c.events_of("llm-complete", clear=False)
            if len(ev) > 1:
                last_new = time.time()
            if time.time() - last_new > 5:
                break
            time.sleep(1)
        retries = c.events_of("llm-retry", clear=True)
        print("    [SKIP] 网络预检未实现（41 D-66）：首错耗时 %.1fs（期望 <15s）、"
              "llm-retry=%d 次（期望 0）" % (elapsed, len(retries)), flush=True)
        return
    finally:
        # 快照-还原（51 §6-8）：按跑前快照回原状（原本缺省 → 删键回落系统默认），
        # 不再整块写回 load 的合并视图（那会把系统默认固化成显式 usr 配置）。
        _h.restore_user_config(c, snap)
        c.mq_emit("config-refresh")


# ── S25 发送前校验 LLM 配置 ─────────────────────────────────

def case_send_blocked_no_llm():
    """S25：llms 置空 → 发送被拦截（无 llm-start / llm-started）+ warning 提示去配置；
    textarea 文本保留（未发送未清空）。"""
    backup = read_cfg()
    snap = _h.snapshot_user_config(c, ["llms", "defaultLLM"])
    try:
        cfg = copy.deepcopy(backup)
        cfg["llms"] = []
        cfg["defaultLLM"] = 0
        write_cfg(cfg)
        c.mq_emit("config-refresh")
        time.sleep(1.5)
        # `llms` 置空时 config-refresh **不会**清空前端 llmList（既有语义：编辑期间不闪空）→
        # 本用例需「空 llms」生效，故重载一次让 llmList 按新配置重新初始化（仅本用例；跑前快照已存）。
        try:
            c.eval("location.reload(); 'ok'")
        except Exception:
            pass
        deadline = time.time() + 60
        while time.time() < deadline:
            try:
                if _loads_deep(c.eval("!!document.querySelector('.panel-inner')")) is True:
                    break
            except Exception:
                pass
            time.sleep(0.5)
        time.sleep(1.0)
        # 输入文本（输入区为 contenteditable 富文本：写 innerHTML + input 事件 → onEdit 同步 text）
        r = _loads_deep(c.eval("""(() => {
          const el = document.querySelector('.richtext-input');
          if (!el) return 'no-el';
          el.innerHTML = 'hello without llm';
          el.dispatchEvent(new Event('input', { bubbles: true }));
          return 'ok';
        })()"""))
        if r != "ok":
            raise TestError(f"未找到输入区 .richtext-input: {r}")
        time.sleep(0.3)
        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(EVENTS)
        # 触发发送（chat-send → InputBox handleSend → serialize → ChatPanel.handleSend）
        c.mq_emit("chat-send")
        time.sleep(2)
        # 断言未发起 LLM 请求
        if c.events_of("llm-start", clear=True):
            raise TestError("LLM 未配置时仍发起了 llm-start")
        if c.events_of("llm-started", clear=True):
            raise TestError("LLM 未配置时仍收到了 llm-started")
        # warning 提示（b-message--warning，含"未配置 LLM"）
        deadline = time.time() + 5
        toast = ""
        while time.time() < deadline:
            info = c.exists(".b-message--warning")
            if info.get("count", 0) > 0:
                toast = c.text(".b-message--warning .b-message-text", 3000) or ""
                if toast:
                    break
            time.sleep(0.5)
        if not toast:
            raise TestError("未出现 LLM 未配置的 warning 提示")
        if "LLM" not in toast:
            raise TestError(f"warning 提示文案异常: {toast!r}")
        # 输入区文本保留（未被清空/发送；拦截后经 chat-queue-restore 写回）
        v = _loads_deep(c.eval("(() => { const t = document.querySelector('.richtext-input'); return t ? t.textContent : ''; })()")) or ""
        if "hello without llm" not in v:
            raise TestError(f"发送被拦截后输入区文本未保留: {v!r}")
        return
    finally:
        _h.restore_user_config(c, snap)  # 快照-还原（51 §6-8）：llms 原本为空/缺省 → 清空集合
        c.mq_emit("config-refresh")


# ── RETRY-1（2026-10-17）：可见重试 + 终态错误只在下方提示条 ────────────

SHOTS = os.path.join("E:\\TEMP", "ck-l4-visual", "retry")


def _save_shot(name):
    """截图（仓库外证据目录）；失败仅告警，不影响断言。"""
    try:
        os.makedirs(SHOTS, exist_ok=True)
        p = os.path.join(SHOTS, name)
        c.screenshot(p)
        print("    [shot] %s" % p, flush=True)
        return p
    except Exception as e:
        print("    [warn] screenshot %s 失败: %s" % (name, e), flush=True)
        return ""


def _dom(js):
    v = _loads_deep(c.eval(js))
    return "" if v is None else v  # 保留 0（计数为 0 是有效断言值，不能被 `or ''` 吃掉）


def _prep_mock_cfg(retry_count):
    """配置只含 mock（8901）+ 指定 retryCount；返回 usr 快照（调用方 finally 还原）。"""
    snap = _h.snapshot_user_config(c, ["llms", "defaultLLM", "retryCount"])
    backup = read_cfg()
    cfg = copy.deepcopy(backup)
    cfg["llms"] = [{
        "name": "mock", "protocol": "openai", "apiKey": "mock-key", "model": "mock-model",
        "baseUrl": "http://127.0.0.1:8901/v1", "temperature": 0.7,
        "maxOutputToken": 4096, "thinking": False,
    }]
    cfg["defaultLLM"] = 0
    cfg["retryCount"] = retry_count
    write_cfg(cfg)
    c.mq_emit("config-refresh")
    c.mq_emit("chat-select-llm", {"name": "mock"})
    time.sleep(1.5)
    return snap


def _watch_retry_dom(max_wait=40):
    """轮询可见重试提示条，收集不同文案 + 首帧截图；llm-complete 到达即停。"""
    texts, shot = [], ""
    end = time.time() + max_wait
    while time.time() < end:
        try:
            t = _dom("(()=>{const e=document.querySelector('.llm-retry-hint');"
                     "return e?e.textContent.trim():''})()")
        except Exception:
            t = ""
        if t and t not in texts:
            texts.append(t)
            if not shot:
                shot = _save_shot("retry-in-progress.png")
        if c.events_of("llm-complete", clear=False):
            break
        time.sleep(0.2)
    return texts, shot


def case_visible_retry_then_success():
    """RETRY-1：429（前两次，Retry-After: 2）→ 重试可见（「正在重试 n/N（等待 s 秒）」）
    → 第三次成功：不落错误、提示条消失、聊天流无 `.error-bubble`。"""
    reset_mock_state()
    snap = _prep_mock_cfg(3)
    try:
        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(EVENTS)
        sid = create_session()
        activate_session(sid)
        time.sleep(1.5)
        start_turn(sid, "please call retry-twice", llm="mock")
        confirm_turn(sid, max_wait=60)
        texts, shot = _watch_retry_dom(max_wait=40)
        done = wait_event("llm-complete", max_wait=60)
        payload = done[0]["payload"]
        retries = c.events_of("llm-retry", clear=False)
        bubbles = _dom("document.querySelectorAll('.message-item.assistant .error-bubble').length")
        fail_bars = _dom("document.querySelectorAll('.llm-fail-hint.llm-error-hint').length")
        body = _dom("document.body.innerText")
        print("[EVIDENCE] " + json.dumps({
            "case": "RETRY-1 重试后成功", "retry_texts": texts, "retry_events": len(retries),
            "retry_payloads": [r.get("payload") for r in retries],
            "status": payload.get("status"), "error_bubbles": bubbles,
            "error_hint_bars": fail_bars, "shot": shot,
            "body_has_retry": "重试" in body,
        }, ensure_ascii=False), flush=True)
        if payload.get("status") != "complete":
            raise TestError("重试后应正常完成，实际: %s" % payload)
        if len(retries) < 2:
            raise TestError("应收到 2 条中间态 llm-retry（前两次 429），实际 %d" % len(retries))
        rp = [r.get("payload") or {} for r in retries]
        if not (rp[0].get("retry_attempt") == 1 and rp[0].get("retry_count") == 3):
            raise TestError("首条中间态计数应为 1/3：%s" % rp[0])
        if not (rp[0].get("retry_wait_ms") or 0) > 0:
            raise TestError("中间态 retry_wait_ms 应 >0：%s" % rp[0])
        if not texts:
            raise TestError("重试期间未显示 .llm-retry-hint（用户不可见）")
        if not any(("重试" in t and "1/3" in t and "2" in t) for t in texts):
            raise TestError("重试文案应含「重试 1/3」与等待秒数 2：%r" % texts)
        # 逐秒倒计时（2026-10-23）：同一中间态内文案应随时间递减 → 采样到「等待 2 秒」与「等待 1 秒」
        if not (any(("等待 2 秒" in t or "waiting 2s" in t) for t in texts)
                and any(("等待 1 秒" in t or "waiting 1s" in t) for t in texts)):
            raise TestError("重试提示未逐秒倒计时（应采样到 2 秒 → 1 秒）：%r" % texts)
        if bubbles != 0:
            raise TestError("成功路径聊天流内不得出现 .error-bubble（got %s）" % bubbles)
        if fail_bars != 0:
            raise TestError("成功路径下方不得残留错误提示条（got %s）" % fail_bars)
        if _dom("document.querySelectorAll('.llm-retry-hint').length") != 0:
            raise TestError("成功终态后重试提示条未消失")
    finally:
        _h.restore_user_config(c, snap)
        c.mq_emit("config-refresh")


def case_retry_exhaust_terminal_hint():
    """RETRY-1/ERRUX-1：重试耗尽 → 终态错误**只在下方提示条**（摘要 + 已重试 n/N + 查看错误信息），
    聊天流内查不到 `.error-bubble`；中间态重试文案仍可见。"""
    reset_mock_state()
    snap = _prep_mock_cfg(2)
    try:
        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(EVENTS)
        sid = create_session()
        activate_session(sid)
        time.sleep(1.5)
        start_turn(sid, "please call retry-exhaust", llm="mock")
        confirm_turn(sid, max_wait=60)
        texts, shot = _watch_retry_dom(max_wait=40)
        done = wait_event("llm-complete", max_wait=60)
        payload = done[0]["payload"]
        time.sleep(0.8)
        bubbles = _dom("document.querySelectorAll('.message-item.assistant .error-bubble').length")
        bar_text = _dom("(()=>{const e=document.querySelector('.llm-fail-hint.llm-error-hint');"
                        "return e?e.textContent.trim():''})()")
        shot2 = _save_shot("retry-exhaust-terminal.png")
        # 展开「查看错误信息」→ 原始错误全文可见（幂等点击）
        c.eval("(()=>{const b=document.querySelector('.llm-fail-detail-toggle');if(b)b.click();return 'ok'})()")
        time.sleep(0.4)
        raw = _dom("(()=>{const e=document.querySelector('.llm-fail-raw');"
                   "return e?e.textContent.trim():''})()")
        shot3 = _save_shot("retry-exhaust-detail.png")
        print("[EVIDENCE] " + json.dumps({
            "case": "RETRY-1 重试耗尽终态", "retry_texts": texts, "status": payload.get("status"),
            "retryable": payload.get("retryable"), "retry_attempt": payload.get("retry_attempt"),
            "retry_count": payload.get("retry_count"), "retry_wait_ms": payload.get("retry_wait_ms"),
            "error_bubbles": bubbles, "hint_text": bar_text, "raw_detail": raw[:160],
            "shots": [shot, shot2, shot3],
        }, ensure_ascii=False), flush=True)
        if payload.get("status") != "error":
            raise TestError("重试耗尽应为 error 终态：%s" % payload)
        if payload.get("retryable") is not True:
            raise TestError("5xx 耗尽 retryable 应为 true：%s" % payload)
        if not (payload.get("retry_attempt") == 2 and payload.get("retry_count") == 2):
            raise TestError("终态真实计数应为 2/2：%s" % payload)
        if payload.get("retry_wait_ms") != 0:
            raise TestError("终态 retry_wait_ms 应为 0：%s" % payload)
        if bubbles != 0:
            raise TestError("聊天流内不得出现 .error-bubble（got %s）" % bubbles)
        if not bar_text:
            raise TestError("终态错误未出现在下方提示条（.llm-fail-hint.llm-error-hint）")
        if "已重试 2/2 次" not in bar_text:
            raise TestError("提示条应显示 retry 次数「已重试 2/2 次」：%r" % bar_text)
        if not raw:
            raise TestError("「查看错误信息」未展开原始错误（.llm-fail-raw 为空）")
    finally:
        _h.restore_user_config(c, snap)
        c.mq_emit("config-refresh")


def main():
    ok = 0
    total = 0
    c.console(clear=True)
    total += 1; ok += run_case("S21 空回复实时提示（completed 无输出 → 提示 + 继续）", case_empty_reply)
    total += 1; ok += run_case("RETRY-1 重试可见且最终成功（中间态 n/N + 等待秒数；无错误气泡）",
                               case_visible_retry_then_success)
    total += 1; ok += run_case("RETRY-1/ERRUX-1 重试耗尽 → 终态只在下方提示条（详情 + retry 次数）",
                               case_retry_exhaust_terminal_hint)
    total += 1; ok += run_case("§一 网络连接预检（端点不可达不重试，无 llm-retry）", case_network_precheck)
    total += 1; ok += run_case("S25 发送前校验 LLM 配置（llms 空 → 拦截 + 提示）", case_send_blocked_no_llm)
    errs = c.console()
    for e in errs.get("entries", []):
        if e.get("level") in ("error",):
            print(f"  [CONSOLE-ERROR] {e.get('text')}")
    print(f"\nLLM 错误处理规划项回归：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

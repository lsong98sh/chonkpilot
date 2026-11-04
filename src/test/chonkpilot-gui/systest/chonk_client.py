# -*- coding: utf-8 -*-
"""ChonkPilot 测试通道客户端（--test-port 模式）。

协议（见 chonkpilot/testserver.go / arch-notes/测试模式.md）：
  GET  /ping       → {ok, app, ready}
  POST /eval       → {js, timeout}   → {ok, result}
  POST /click      → {selector, timeout}
  POST /input      → {selector, value, timeout}
  POST /text       → {selector, timeout}   → result=纯文本
  POST /html       → {selector, timeout}   → result=outerHTML
  POST /exists     → {selector, timeout}   → result={count, visible}
  POST /console    → {clear}               → result={entries, truncated}
  GET  /screenshot → PNG
"""

import functools
import json
import time
import urllib.request
import urllib.error


class TestError(AssertionError):
    """断言失败。"""


# ── 瞬态错误有界重试（**仅**驱动/取数层；不包裹任何断言）────────────────────
#
# 背景（已知抖动，用户核准加重试）：满负载批量跑时 WebView2 UI 线程偶发 starvation →
# 驱动指令（`/eval` 及其之上的 `/click` `/input` `/text` `/html` `/exists` `/console`；服务端
# 统一走 `doEval`→`EvalWithResult`，见 `src/lib/gui/testserver.go:226`）派发的 ExecuteScript
# 在预算内拿不到调度 → 抛 `ExecuteScript timed out`（`chonkpilot-webview2/webview.go:675`：
# 派发到 UI 线程 + 等回调，超时即报错）→ 断言脚崩溃（批内偶发红、单跑全绿）。
# 就绪探测早有 `harness.wait_probe` 容忍瞬时错误（[42 §2 (148)]），**断言期**的驱动调用此前没有。
#
# 口径（守住「不掩盖产品缺陷」的底线）：
#   * **只**重试「瞬态/超时类」错误（见 TRANSIENT_ERRORS）：ExecuteScript/网络超时、连接类瞬态；
#     业务失败——元素不存在（`not found: …`）、脚本自身抛错、`/publish` 信封 `ok=false` 等——
#     **立即上抛，绝不重试**；
#   * **有界**：每次调用额外重试 ≤ `RETRY_MAX` 次（总尝试 = RETRY_MAX+1），退避 `RETRY_BACKOFF`；
#     2026-10-27（42 (290)）：满负载批跑实测 UI 线程饥饿可持续数十秒（多套件连跑后累积），
#     原 2 次重试（~6s 预算）不足以覆盖 → 上调为 4 次 + 单次 10s 预算（总预算 ~57s）。
#   * **可观测**：每次重试打印一行 `[retry] <调用>: <错误>, attempt n/m, backoff Xs`
#     （便于区分「真绿」与「重试后绿」）；
#   * **作用域**：仅驱动/取数指令（`.eval/.click/.input/.text/.html/.exists/.console`）；
#     `/publish`（`req`/`publish`，消息面请求-响应，可能触发落库/LLM/工具等**副作用**）**不重试**，
#     断言（`Checker`/`run_case`/`assert_*`）**不重试**。
#
# 关于「重试是否可能重复施加副作用」（如 `eval` 里 `el.click()`）：`EvalWithResult` 的超时
# 覆盖「派发 + 执行 + 回调」全程；UI 线程饥饿时派发闭包**尚未执行**即超时（回调若已触发，
# 结果会先入 `ch` 缓冲区）——故超时通常意味着该 JS 未运行，重试等价于「再派发一次」。
RETRY_MAX = 4
RETRY_BACKOFF = (0.5, 1.0, 2.0, 4.0)
TRANSIENT_ERRORS = ("ExecuteScript timed out", "timed out", "10061", "10054", "refused",
                    "reset", "RemoteDisconnected", "connection aborted")


def is_transient_error(exc):
    """错误是否属「瞬态/超时类」（可由重试消除，非产品缺陷）。"""
    msg = str(exc)
    return any(t in msg for t in TRANSIENT_ERRORS)


def retryable(name):
    """装饰驱动/取数指令方法：**仅**对其抛出的瞬态错误做有界重试，其余立即上抛。

    只包裹「取数 / 派发」这一层；不得用于断言（断言失败必须原样暴露）。
    """
    def deco(fn):
        @functools.wraps(fn)
        def wrapper(*args, **kwargs):
            failures = 0
            while True:
                try:
                    return fn(*args, **kwargs)
                except Exception as e:
                    if failures >= RETRY_MAX or not is_transient_error(e):
                        raise
                    backoff = RETRY_BACKOFF[min(failures, len(RETRY_BACKOFF) - 1)]
                    failures += 1
                    print("[retry] %s: %s, attempt %d/%d, backoff %.1fs"
                          % (name, e, failures + 1, RETRY_MAX + 1, backoff), flush=True)
                    time.sleep(backoff)
        return wrapper
    return deco


def _find_session_id(payload, id_is_session=False):
    """在 payload（可嵌套）里找首个会话 id。

    识别键：`session_id` / `session` / `top_session`（任务树按 `top_session` 定位）；
    `id_is_session=True` 时额外认 `id`（`data-session-get` / `-title` / `-delete` 用 `id` 指会话）。
    """
    def walk(o):
        keys = ("session_id", "session", "top_session") + (("id",) if id_is_session else ())
        if isinstance(o, dict):
            for k in keys:
                v = o.get(k)
                if isinstance(v, str) and v:
                    return v
            for v in o.values():
                r = walk(v)
                if r:
                    return r
        elif isinstance(o, list):
            for v in o:
                r = walk(v)
                if r:
                    return r
        return ""

    return walk(payload)


def _find_parent_session_id(payload):
    """在 payload（可嵌套）里找首个 `parent_session_id`（子会话建库需带父会话的 dialog_id）。"""
    def walk(o):
        if isinstance(o, dict):
            v = o.get("parent_session_id")
            if isinstance(v, str) and v:
                return v
            for x in o.values():
                r = walk(x)
                if r:
                    return r
        elif isinstance(o, list):
            for x in o:
                r = walk(x)
                if r:
                    return r
        return ""

    return walk(payload)


def _find_turn_id(payload):
    """在 payload（可嵌套）里找首个 `turn_id`。

    会话运行时写原语（`data-session-append-message` / `-complete-turn`）只带 `turn_id`，
    不带会话 id → 驱动层用「turn → dialog」映射补 `dialog_id`（见 `ChonkClient.req`）。
    """
    def walk(o):
        if isinstance(o, dict):
            v = o.get("turn_id")
            if isinstance(v, str) and v:
                return v
            for x in o.values():
                r = walk(x)
                if r:
                    return r
        elif isinstance(o, list):
            for x in o:
                r = walk(x)
                if r:
                    return r
        return ""

    return walk(payload)


def _inject_dialog(payload, sid, dlg, id_is_session=False):
    """返回 payload 副本：给「带该会话 id 的对象」补 `dialog_id`（顶层与 data 子对象同源）。

    `id_is_session=True` 时把 `id == sid` 的对象也视为目标（`data-session-title` 等用 `id` 指会话）。
    """
    def walk(o):
        if isinstance(o, dict):
            new = dict(o)
            changed = False
            if sid == "":
                # 全实例快照类主题（如 `data-tasktree-delete{node_id}`）载荷不带会话 id →
                # 在**顶层**补 `dialog_id`。
                if not new.get("dialog_id"):
                    new["dialog_id"] = dlg
                    changed = True
                return new, changed
            hit = new.get("session_id") == sid or new.get("session") == sid or new.get("top_session") == sid
            if not hit and id_is_session and new.get("id") == sid:
                hit = True
            if hit and not new.get("dialog_id"):
                new["dialog_id"] = dlg
                changed = True
            for k, v in list(new.items()):
                nv, ch = walk(v)
                if ch:
                    new[k] = nv
                    changed = True
            return new, changed
        if isinstance(o, list):
            out = []
            changed = False
            for v in o:
                nv, ch = walk(v)
                out.append(nv)
                changed = changed or ch
            return (out if changed else o), changed
        return o, False

    return walk(payload)[0]


def dialog_id_for(session_id):
    """根会话的**确定性** dialog_id（与 Go `sessionstore.MigratedDialogID` 逐字同算法）。

    `sha256("chonkpilot-dialog:" + 根 session_id)` 前 16 字节 → 版本 5 / RFC4122 variant 的 UUID 文本。
    根会话首次 `data-session-ensure-session`（缺 `dialog_id`）即按此算法自举建对话库（旧布局已退场）。
    """
    import hashlib

    b = bytearray(hashlib.sha256(("chonkpilot-dialog:" + session_id).encode("utf-8")).digest()[:16])
    b[6] = (b[6] & 0x0F) | 0x50
    b[8] = (b[8] & 0x3F) | 0x80
    h = bytes(b).hex()
    return "%s-%s-%s-%s-%s" % (h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])


# 「创建类」主题：会话可能尚不存在 → 允许经 `data-session-ensure-session` 自举建对话（拿 dialog_id）。
# 读/删类主题**不得**自举（否则「读已删除会话应返回 null」会被 ensure 重新建出来）。
CREATING_TOPICS = ("chonk.session-start", "llm-start", "message-send", "data-session-ensure-session")


def resolve_dialog_id(client, typ, sid):
    """解析会话的 dialog_id（缓存 → `data-session-list` 现查 → 创建类主题自举）；失败 → ""。

    `sid == ""`（载荷不带会话 id 的全实例快照类主题）→ 仅当实例内**恰有一个**对话时返回它。
    """
    cache = client._dlg_cache
    if sid and sid in cache:
        return cache[sid]
    if sid == "" and len(cache) == 1:
        return next(iter(cache.values()))
    if client._dlg_busy:
        return ""
    client._dlg_busy = True
    try:
        try:
            r = client._req_once("data-session-list", {})
        except Exception:
            r = None
        for it in (((r or {}).get("list")) or []):
            if isinstance(it, dict) and it.get("session_id") and it.get("dialog_id"):
                cache[it["session_id"]] = it["dialog_id"]
        if sid:
            if sid in cache:
                return cache[sid]
            if typ in CREATING_TOPICS and typ != "data-session-ensure-session":
                try:
                    r2 = client._req_once("data-session-ensure-session", {"data": {"session_id": sid}})
                except Exception:
                    r2 = None
                dlg = ((r2 or {}).get("dialog_id") or "")
                if dlg:
                    cache[sid] = dlg
                    return dlg
        else:
            dialogs = {v for v in cache.values() if v}
            if len(dialogs) == 1:
                return next(iter(dialogs))
    finally:
        client._dlg_busy = False
    return ""


class ChonkClient:
    def __init__(self, base="http://127.0.0.1:2345", timeout=30, window_id=""):
        # HTTP 读超时必须**大于**单次 `/eval` 的 JS 预算（2026-10-27，42 (290)）：预算已上调到 10s，
        # 若 HTTP 超时仍是 10s，客户端会先断开 → 表现为 `timed out`（而非 `ExecuteScript timed out`），
        # 既浪费整轮预算又让服务端后台脚本空跑。30s = 10s 预算 + 排队/传输余量。
        self.base = base
        self.timeout = timeout
        # 目标窗口（多窗口测试通道路由，见 24 §4.5 / main.go testServer）：
        # 空 = 主窗口（缺省，与单窗口行为逐字一致）；非空 = `gui.window.list` 的 window_id
        # → /eval、/publish、/wait-event、/screenshot 作用于该对话窗口。
        self.window_id = window_id
        # 一树一库自愈：session_id → dialog_id 缓存（+ 解析互斥，防重入）
        self._dlg_cache = {}
        self._dlg_busy = False
        # turn_id → dialog_id（`append-message` / `complete-turn` 只带 turn_id）
        self._turn_dlg = {}

    def _post(self, path, payload):
        if self.window_id and isinstance(payload, dict) and "window_id" not in payload:
            payload = dict(payload, window_id=self.window_id)
        req = urllib.request.Request(
            self.base + path,
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=self.timeout) as resp:
            return json.loads(resp.read().decode("utf-8"))

    def _get(self, path):
        if self.window_id:
            path = path + ("&" if "?" in path else "?") + "window_id=" + self.window_id
        with urllib.request.urlopen(self.base + path, timeout=self.timeout) as resp:
            return resp.read()

    def ping(self):
        data = self._get("/ping")
        return json.loads(data)

    def wait_ready(self, max_wait=60):
        deadline = time.time() + max_wait
        while time.time() < deadline:
            try:
                p = self.ping()
                if p.get("ok") and p.get("ready"):
                    return p
            except Exception:
                pass
            time.sleep(1)
        raise TestError(f"IDE 测试通道 {max_wait}s 内未就绪")

    @retryable("eval")
    def eval(self, js, timeout=10000):
        """执行 JS，返回 EvalWithResult 解包后的原始结果（瞬态超时由装饰器有界重试）。"""
        r = self._post("/eval", {"js": js, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/eval 失败: {r.get('error')}")
        return r.get("result")

    @retryable("click")
    def click(self, selector, timeout=10000):
        r = self._post("/click", {"selector": selector, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/click 失败: {r.get('error')}")
        return r.get("result")

    @retryable("input")
    def input(self, selector, value, timeout=10000):
        r = self._post("/input", {"selector": selector, "value": value, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/input 失败: {r.get('error')}")
        return r.get("result")

    @retryable("text")
    def text(self, selector, timeout=10000):
        r = self._post("/text", {"selector": selector, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/text 失败: {r.get('error')}")
        return r.get("result")

    @retryable("html")
    def html(self, selector, timeout=10000):
        r = self._post("/html", {"selector": selector, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/html 失败: {r.get('error')}")
        return r.get("result")

    @retryable("exists")
    def exists(self, selector, timeout=10000):
        r = self._post("/exists", {"selector": selector, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/exists 失败: {r.get('error')}")
        return r.get("result")

    @retryable("console")
    def console(self, clear=False, timeout=10000):
        r = self._post("/console", {"clear": clear, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/console 失败: {r.get('error')}")
        return r.get("result")

    def screenshot(self, path=None):
        data = self._get("/screenshot")
        if path:
            with open(path, "wb") as f:
                f.write(data)
        return data

    # ── 消息面请求-响应（data-* / gui.*；publish + 收集 result）──

    def publish(self, typ, payload=None, timeout=30000):
        """POST /publish 原始信封 {ok, result, errors}（不校验成败；事件类/需读 ok=false 时用）。

        业务消息面 payload 传对象（本方法负责 JSON 编码，与前端 mq.js publishToBackend 同形）；
        跨窗口时由 `window_id` 指定目标窗口（见 `__init__`）。
        **不重试**：消息面请求-响应可能触发落库/LLM/工具等副作用（见模块头「瞬态重试」口径）。
        """
        body = {"type": typ,
                "payload": json.dumps(payload, ensure_ascii=False) if payload is not None else "",
                "timeout": timeout}
        if self.window_id and "window_id" not in body:
            body["window_id"] = self.window_id
        return self._post("/publish", body)

    def req(self, typ, payload=None, timeout=20000):
        """经 /publish 走消息面请求-响应（替代已移除的 window.go RPC）。

        桥 PublishEvent → persist/gateway 写回 result；返回 result 载荷；
        信封 ok=false / errors 非空 / result.ok=false → 抛 TestError。
        **不重试**（同 publish：请求-响应可能有副作用）——**唯一例外**见下「一树一库自愈」。

        一树一库自愈（2026-10-26）：会话类 data 主题必须带
        `dialog_id`，缺失即 `dialog_required: …`（旧布局已退场，无开关）。产品前端已在收到该错误时补 `dialog_id`
        重试一次（42 §2 (273) ④ `api/session.js#sessionReq`）→ 驱动层与之同口径：命中该错误
        时按 `data-session-list` 解析一次该会话的 `dialog_id`，注入 payload 副本后
        **原样重试一次**（仅此一类错误、仅此一次；其它错误照旧立即上抛）。
        """
        try:
            res = self._req_once(typ, payload, timeout)
        except TestError as e:
            if "dialog_required" not in str(e) or not isinstance(payload, (dict, list)):
                raise
            id_is_sess = typ.startswith("data-session-")
            sid = _find_session_id(payload, id_is_sess)
            tid = _find_turn_id(payload)
            dlg = self._turn_dlg.get(tid, "") if (tid and not sid) else ""
            if not dlg:
                dlg = resolve_dialog_id(self, typ, sid)
            if not dlg:
                # 子会话建库：会话本身尚不存在 → 用**父会话**的 dialog_id（P3-4d 必需）
                parent = _find_parent_session_id(payload)
                if parent:
                    dlg = resolve_dialog_id(self, "llm-start", parent)
            if not dlg:
                raise
            print("[dialog] %s 缺 dialog_id → 自愈注入 %s 并重试一次" % (typ, dlg), flush=True)
            res = self._req_once(typ, _inject_dialog(payload, sid, dlg, id_is_sess), timeout)
            self._remember_dialog(typ, payload, dlg)
            return res
        if typ == "data-session-delete" and isinstance(payload, dict):
            # 删除会级联删对话库 → 缓存的 dialog_id 失效（后续写路径不得再注入已删除的库）
            self._dlg_cache.pop(str(payload.get("id") or ""), None)
        self._remember_dialog(typ, payload, (res or {}).get("dialog_id") if isinstance(res, dict) else "")
        return res

    def _remember_dialog(self, typ, payload, dlg=""):
        """记忆 `session_id → dialog_id` / `turn_id → dialog_id`（供后续缺字段的会话写原语复用）。"""
        if not isinstance(payload, (dict, list)):
            return
        id_is_sess = typ.startswith("data-session-")
        sid = _find_session_id(payload, id_is_sess)
        if not dlg and sid:
            dlg = self._dlg_cache.get(sid, "")
        if not dlg:
            return
        if sid:
            self._dlg_cache.setdefault(sid, dlg)
        tid = _find_turn_id(payload)
        if tid:
            self._turn_dlg[tid] = dlg

    def _req_once(self, typ, payload=None, timeout=20000):
        body = {"type": typ,
                "payload": json.dumps(payload, ensure_ascii=False) if payload is not None else "",
                "timeout": timeout}
        r = self._post("/publish", body)
        if not isinstance(r, dict) or r.get("ok") is False:
            msg = ""
            if isinstance(r, dict):
                errs = r.get("errors") or []
                if errs:
                    msg = errs[0]
                else:
                    res = r.get("result")
                    if isinstance(res, dict):
                        msg = res.get("error") or ""
            raise TestError(msg or ("publish failed: " + typ))
        res = r.get("result")
        if isinstance(res, dict) and res.get("ok") is False:
            raise TestError(res.get("error") or (typ + " failed"))
        return res

    # ── dialog_id 解析见模块级 `resolve_dialog_id`（两个驱动类共用）──

    # ── mq 便捷方法（testplan 总纲：mq 驱动）──

    def mq_emit(self, topic, payload=None, timeout=10000):
        js = f"window.mq.emit({json.dumps(topic)}, {json.dumps(payload, ensure_ascii=False)})"
        return self.eval(js, timeout)

    def mq_on_capture(self, topics, timeout=10000):
        """注册监听，把事件 push 到 window.__chonkEvents，返回已注册的 topics 集合。

        每次调用前先注销上一轮注册的 handler：mq.on 按「回调引用」去重（mq.js:132），
        而本方法每次都是新闭包，若不注销会持续累积 → 同一事件被 push N 次（N = 调用次数），
        表现为测试侧「同一事件重复到达」。注销后每次捕获都是干净的一轮。
        """
        topics_js = json.dumps(topics, ensure_ascii=False)
        js = f"""
(() => {{
  if (!window.__chonkEvents) window.__chonkEvents = {{ events: [], map: {{}}, unsubs: [] }};
  const ev = window.__chonkEvents;
  const norm = (d) => {{
    if (typeof d === 'string' && d !== '') {{ try {{ return JSON.parse(d); }} catch (e) {{ return d; }} }}
    return d;
  }};
  (ev.unsubs || []).forEach(u => {{ try {{ u(); }} catch (e) {{}} }});  // 注销上一轮，避免 handler 累积
  ev.unsubs = [];
  ev.map = {{}};  // 强制重注册，确保使用最新回调
  ev.events = []; // 清空旧事件
  {topics_js}.forEach(t => {{
    if (ev.map[t]) return;
    ev.map[t] = true;
    ev.unsubs.push(window.mq.on(t, (d) => ev.events.push({{ topic: t, payload: norm(d), ts: Date.now() }})));
  }});
  return Object.keys(ev.map).length;
}})()
"""
        return self.eval(js, timeout)

    def events_of(self, topic, clear=True):
        """读取并（可选）清空 window.__chonkEvents 中某 topic 的事件。"""
        js = """
(() => {
  const ev = window.__chonkEvents || { events: [] };
  const out = ev.events.filter(e => e.topic === %s);
  if (%s) ev.events = ev.events.filter(e => e.topic !== %s);
  return JSON.stringify(out);
})()
""" % (json.dumps(topic), "true" if clear else "false", json.dumps(topic))
        res = self.eval(js, 5000)
        # EvalWithResult 双重 JSON 编码：循环解包直到非 JSON 字符串
        for _ in range(3):
            if not isinstance(res, str) or not res:
                break
            try:
                parsed = json.loads(res)
            except Exception:
                break
            if isinstance(parsed, str):
                res = parsed
                continue
            return parsed
        return []

    def wait_events(self, topic, n=1, max_wait=30, clear=True):
        """轮询等待某 topic 至少 n 条事件，超时抛错。"""
        deadline = time.time() + max_wait
        while time.time() < deadline:
            ev = self.events_of(topic, clear=False)
            if len(ev) >= n:
                if clear:
                    self.events_of(topic, clear=True)
                return ev
            time.sleep(0.5)
        raise TestError(f"等待事件 {topic} 超时（{max_wait}s），当前 {len(self.events_of(topic, clear=False))} 条")

    # ── 断言工具 ──

    def assert_exists(self, selector, visible=True, timeout=5000):
        info = self.exists(selector, timeout)
        if info.get("count", 0) == 0:
            raise TestError(f"断言失败：元素不存在 {selector}")
        if visible and not info.get("visible"):
            raise TestError(f"断言失败：元素不可见 {selector}")
        return info

    def assert_not_exists(self, selector, timeout=5000):
        info = self.exists(selector, timeout)
        if info.get("count", 0) > 0:
            raise TestError(f"断言失败：元素不应存在 {selector}")
        return info

    def assert_text_contains(self, selector, needle, timeout=5000):
        txt = self.text(selector, timeout)
        if needle not in txt:
            raise TestError(f"断言失败：{selector} 文本不含 {needle!r}，实际 {txt[:200]!r}")
        return txt


def run_case(name, fn):
    """执行单个用例并输出 PASS/FAIL。"""
    try:
        fn()
        print(f"  [PASS] {name}")
        return True
    except TestError as e:
        print(f"  [FAIL] {name}: {e}")
        return False
    except Exception as e:
        print(f"  [ERROR] {name}: {type(e).__name__}: {e}")
        return False

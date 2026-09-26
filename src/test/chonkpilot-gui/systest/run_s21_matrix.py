# -*- coding: utf-8 -*-
"""S21「可重试分类」四终态行为矩阵 —— 原始证据采集（DOM + mq 事件）。

分类来源（S21 定稿）：`llm-complete` 可选字段 `retryable`（后端 = `*LLMError.Retryable`，
桥 `compat.go` 原样透传；前端 `MessageList.onLlmComplete` 优先读该字段，缺省回落旧
`llm-error.retryable`）。四情形：

  ① EMPTY_REPLY（retryable=false）→ 显示「空回复」提示 + 「继续」按钮；**不**自动续写
  ② 5xx（retryable=true）→ **自动续写**（静默清理占位气泡，不显示错误气泡）
  ③ 401（auth，retryable=false）→ 显示错误气泡 + 「继续」按钮；**不**自动续写
  ④ complete 无输出 → 提示 + 继续（回归护栏，前端注入终态）

隔离与回收：GUI 由 harness 自起（动态端口 + 独立 work-dir/data-dir/HOME），mock LLM 由
harness 自起（8901 复用优先），本脚本内嵌状态 mock（500/401）为进程内线程；结束全部回收。
用 `retryCount=0` 隔离后端「方式 A」重发，聚焦**前端分类 → 续写/提示**行为。

运行：python run_s21_matrix.py
"""
import copy
import json
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError  # noqa: E402
import harness as _h  # noqa: E402

EVENTS = ["llm-start", "llm-complete", "llm-error", "turn-start"]


# ══════════════════════════════════════════════════════════
# 进程内状态 mock：按路径前缀返回 500 / 401（openai 客户端 URL = baseUrl + /chat/completions）
# ══════════════════════════════════════════════════════════

class _StatusHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        if n:
            self.rfile.read(n)
        if "/500/" in self.path:
            self._send(500, {"error": {"message": "mock internal server error"}})
        elif "/401/" in self.path:
            self._send(401, {"error": {"message": "invalid api key"}})
        else:
            self._send(404, {"error": {"message": "no route " + self.path}})

    def _send(self, code, obj):
        data = json.dumps(obj).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *a):
        pass


class _QuietThreadingHTTPServer(ThreadingHTTPServer):
    """静默「对端主动断开」噪音的 socketserver。

    定位（2026-09-16）：本 mock 的 500/401 响应由 **Go LLM 客户端**（GUI 进程内 server）经
    keep-alive 连接读取；对端拿到响应/进程被回收（`harness` taskkill）后立即关闭连接 →
    `http.server.BaseHTTPRequestHandler.handle_one_request` 的下一轮 `self.rfile.readline()`
    抛 `ConnectionResetError [WinError 10054]`；`socketserver.BaseServer.process_request_thread`
    → `handle_error` **默认把整份 traceback 打到 stderr**（套件退出码仍是 0、11/11 全过，但
    stderr 不干净）。此处按「连接类异常 = 正常收尾」静默；**其它异常照旧打 traceback**（真失败
    仍需证据）。
    """

    daemon_threads = True

    def handle_error(self, request, client_address):
        et = sys.exc_info()[0]
        if et is not None and issubclass(et, (ConnectionError, TimeoutError)):
            return
        ThreadingHTTPServer.handle_error(self, request, client_address)


def start_status_mock():
    srv = _QuietThreadingHTTPServer(("127.0.0.1", 0), _StatusHandler)
    srv.daemon_threads = True
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, srv.server_address[1]


# ══════════════════════════════════════════════════════════
# 会话 / 事件助手（与 run_llm_errors / test_chat_flow 同口径）
# ══════════════════════════════════════════════════════════

_SEQ = [0]


def new_session():
    _SEQ[0] += 1
    return "s21-%d-%03d" % (int(time.time() * 1000), _SEQ[0])


def start_turn(c, sid, q, llm):
    c.mq_emit("llm-start", {
        "session_id": sid, "turn": "t-" + sid, "q": q, "llm": llm,
        "think": "", "effort": "", "scenario_id": "",
    })


def counts(c):
    return {t: len(c.events_of(t, clear=False)) for t in EVENTS}


def jp(v):
    """解包 eval 结果的 JSON 字符串（通道可能双重编码）。"""
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            p = json.loads(v)
        except Exception:
            return v
        if p == v:
            return v
        v = p
    return v


def wait_llm_complete(c, n=1, max_wait=60):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        ev = c.events_of("llm-complete", clear=False)
        if len(ev) >= n:
            return ev
        time.sleep(0.4)
    raise TestError("等待 llm-complete >= %d 超时，实得 %d" % (n, len(c.events_of("llm-complete", clear=False))))


def settle(c, idle=6.0, min_complete=1, max_wait=90):
    """等到 llm-complete 链静默（idle 秒无新增）或超时；返回终态列表。"""
    deadline = time.time() + max_wait
    last_n = len(c.events_of("llm-complete", clear=False))
    last_ts = time.time()
    while time.time() < deadline and last_n >= min_complete:
        time.sleep(0.5)
        n = len(c.events_of("llm-complete", clear=False))
        if n != last_n:
            last_n, last_ts = n, time.time()
        elif time.time() - last_ts >= idle:
            break
    return c.events_of("llm-complete", clear=False)


def dom(c, sid):
    """当前会话消息区关键 DOM。

    **错误呈现面口径（批 2，2026-09-20，见 MessageItem.vue:175-188）**：轮次错误不再直出
    `Error: <原始串>`，而是 `.message-bubble.error-bubble` 内含 `.error-text`（人话分类文案，
    由 `utils/errorMessage.js` 的 `classifyError` 映射）+ `.error-detail-bar .more-link`（展开
    折叠的原始串 `<pre class="error-detail-raw">`）。故「错误气泡」= 新选择器 `.error-bubble`。
    """
    return {
        "empty_hint_text": jp(c.eval("(document.querySelector('.empty-reply-hint')||{}).textContent||''")),
        "empty_hint_count": c.exists(".empty-reply-hint").get("count", 0),
        "continue_btn": c.exists(".turn-actions .continue-btn").get("count", 0),
        "error_bubbles": jp(c.eval(
            "(()=>[...document.querySelectorAll('.message-item.assistant .error-bubble')].length)()")),
        "error_text": jp(c.eval(
            "(()=>{const e=document.querySelector('.message-item.assistant .error-bubble .error-text');"
            "return e?e.textContent.trim().slice(0,120):''})()")),
    }


def error_detail_raw(c):
    """展开（幂等）错误气泡的「原始错误」详情，返回原始串 `<pre class="error-detail-raw">` 文本。

    批 2 口径：已识别类别的原始串**默认折叠**，点 `.error-detail-bar .more-link` 展开 →
    原始串仍可核验（不丢弃）。返回 '' = 无错误气泡 / 详情未渲染。"""
    c.eval("(()=>{const B=document.querySelector('.message-item.assistant .error-bubble');if(!B)return 'no-bubble';"
           "if(!B.querySelector('.error-detail-raw')){const m=B.querySelector('.error-detail-bar .more-link');"
           "if(m)m.click();}return 'ok';})()")
    time.sleep(0.4)
    return jp(c.eval("(()=>{const e=document.querySelector('.message-item.assistant .error-bubble .error-detail-raw');"
                     "return e?e.textContent.trim():''})()")) or ""


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


# ══════════════════════════════════════════════════════════
# 主流程
# ══════════════════════════════════════════════════════════

def main():
    srv, sport = start_status_mock()
    mock = _h.acquire_mock_llm(_h.DEFAULT_MOCK_LLM_PORT)
    g = _h.acquire_gui(_h.free_port(),
                       work_dir=_h.tmp_dir("s21-ws-"),
                       data_dir=_h.tmp_dir("s21-dd-"),
                       home=_h.tmp_home())
    c = g.client
    stat = {"n": 0, "fail": []}

    def chk(name, cond, detail):
        stat["n"] += 1
        print(("  [PASS] " if cond else "  [FAIL] ") + name + ("" if cond else ": " + str(detail)), flush=True)
        if not cond:
            stat["fail"].append(name)

    backup = None
    try:
        # ── usr 配置：lib 三名（mock / err500 / err401）+ retryCount=0（隔离后端方式 A）──
        backup = (c.req("data-user-config-load", {}).get("data") or {})
        cfg = copy.deepcopy(backup)
        cfg["llms"] = [
            {"name": "mock", "protocol": "openai", "apiKey": "mock-key", "model": "mock-model",
             "baseUrl": "http://127.0.0.1:%d/v1" % mock.port, "temperature": 0.7,
             "maxOutputToken": 4096, "thinking": False},
            {"name": "err500", "protocol": "openai", "apiKey": "k500", "model": "m500",
             "baseUrl": "http://127.0.0.1:%d/500/v1" % sport, "temperature": 0.7,
             "maxOutputToken": 4096, "thinking": False},
            {"name": "err401", "protocol": "openai", "apiKey": "k401", "model": "m401",
             "baseUrl": "http://127.0.0.1:%d/401/v1" % sport, "temperature": 0.7,
             "maxOutputToken": 4096, "thinking": False},
        ]
        cfg["defaultLLM"] = 0
        cfg["retryCount"] = 0
        cfg["retryDelay"] = 1
        c.req("data-user-config-save", {"data": cfg})
        c.mq_emit("config-refresh")
        time.sleep(1.5)
        print("[setup] llms=%s retryCount=0 status-mock=%d mock-llm=%s gui=%d"
              % ([l["name"] for l in cfg["llms"]], sport, mock.port, g.port), flush=True)

        # ══ ① EMPTY_REPLY → 提示 + 继续；不自动续写 ══
        try:
            c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
            c.mq_on_capture(EVENTS)
            c.mq_emit("chat-select-llm", {"name": "mock"})
            sid = new_session()
            c.mq_emit("session-changed", {"session_id": sid})
            time.sleep(1.5)
            start_turn(c, sid, "please call empty", "mock")
            done = wait_llm_complete(c, 1, 60)
            p = done[0]["payload"]
            time.sleep(8)  # 自动续写（若发生）窗口
            d = dom(c, sid)
            cnt = counts(c)
            evi("1-EMPTY_REPLY", status=p.get("status"), code=p.get("code"),
                retryable=p.get("retryable"), llm_complete=len(done), llm_start=cnt["llm-start"],
                hint=d["empty_hint_count"], hint_text=d["empty_hint_text"],
                continue_btn=d["continue_btn"], error_bubbles=d["error_bubbles"])
            chk("① EMPTY_REPLY: status=error + code=EMPTY_REPLY + retryable=false",
                p.get("status") == "error" and p.get("code") == "EMPTY_REPLY" and p.get("retryable") is False, p)
            chk("① 空回复提示 + 继续按钮", d["empty_hint_count"] > 0 and d["continue_btn"] > 0, d)
            chk("① 不自动续写（llm-complete=1 / llm-start=1）",
                len(done) == 1 and cnt["llm-start"] == 1, {"complete": len(done), "start": cnt["llm-start"]})
        except Exception as e:
            chk("① EMPTY_REPLY 用例执行", False, "%s: %s" % (type(e).__name__, e))

        # ══ ② 5xx → 自动续写（静默，无错误气泡）══
        try:
            c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
            c.mq_on_capture(EVENTS)
            c.mq_emit("chat-select-llm", {"name": "err500"})
            sid = new_session()
            c.mq_emit("session-changed", {"session_id": sid})
            time.sleep(1.5)
            start_turn(c, sid, "hello 500", "err500")
            wait_llm_complete(c, 1, 60)
            first = c.events_of("llm-complete", clear=False)[0]["payload"]
            d1 = dom(c, sid)
            done = settle(c, idle=6.0, min_complete=1, max_wait=60)
            d = dom(c, sid)
            cnt = counts(c)
            evi("2-5xx", first_status=first.get("status"), first_code=first.get("code"),
                first_retryable=first.get("retryable"), llm_complete=len(done),
                llm_start=cnt["llm-start"], hint_after_first=d1["empty_hint_count"],
                hint=d["empty_hint_count"], continue_btn=d["continue_btn"],
                error_bubbles=d["error_bubbles"], error_text=d["error_text"])
            chk("② 5xx: 首次终态 status=error + retryable=true",
                first.get("status") == "error" and first.get("retryable") is True, first)
            chk("② 自动续写（llm-complete≥2 / llm-start≥2）",
                len(done) >= 2 and cnt["llm-start"] >= 2, {"complete": len(done), "start": cnt["llm-start"]})
            chk("② 静默：无错误气泡 + 无空回复提示",
                d["error_bubbles"] == 0 and d["empty_hint_count"] == 0, d)
        except Exception as e:
            chk("② 5xx 用例执行", False, "%s: %s" % (type(e).__name__, e))

        # ══ ③ 401（auth，不可重试）→ 错误气泡 + 继续；不自动续写 ══
        try:
            c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
            c.mq_on_capture(EVENTS)
            c.mq_emit("chat-select-llm", {"name": "err401"})
            sid = new_session()
            c.mq_emit("session-changed", {"session_id": sid})
            time.sleep(1.5)
            start_turn(c, sid, "hello 401", "err401")
            done = wait_llm_complete(c, 1, 60)
            p = done[0]["payload"]
            time.sleep(8)
            d = dom(c, sid)
            cnt = counts(c)
            raw_msg = (p.get("message") or "").strip()
            raw_detail = error_detail_raw(c)  # 展开折叠的原始串（幂等）
            evi("3-401", status=p.get("status"), code=p.get("code"), retryable=p.get("retryable"),
                message=raw_msg[:80], llm_complete=len(done), llm_start=cnt["llm-start"],
                hint=d["empty_hint_count"], continue_btn=d["continue_btn"],
                error_bubbles=d["error_bubbles"], error_text=d["error_text"],
                raw_detail=raw_detail[:80], raw_has_msg=bool(raw_msg) and raw_msg in raw_detail)
            chk("③ 401: status=error + retryable=false",
                p.get("status") == "error" and p.get("retryable") is False, p)
            # 新 UI 口径（批 2「错误呈现面」）：错误以 `.error-bubble`/`.error-text` 呈现**人话
            # 分类文案**（复用 errorMessage 分类口径：auth → 文案含状态参数「401」，不写死整句中文），
            # 原始串折叠入可展开详情 → 展开后仍可核验。核心校验「错误确实被呈现」与「原始串不丢失」
            # 一并覆盖（强度不降）。
            chk("③ 人话错误气泡 + 原始串可展开核验 + 继续按钮",
                d["error_bubbles"] >= 1 and bool(d["error_text"])
                and not d["error_text"].startswith("Error:")
                and "401" in d["error_text"]
                and bool(raw_msg) and raw_msg in raw_detail
                and d["continue_btn"] > 0,
                {"bubbles": d["error_bubbles"], "human": d["error_text"],
                 "raw_detail": raw_detail[:80], "continue": d["continue_btn"]})
            chk("③ 不自动续写（llm-complete=1 / llm-start=1）",
                len(done) == 1 and cnt["llm-start"] == 1, {"complete": len(done), "start": cnt["llm-start"]})
        except Exception as e:
            chk("③ 401 用例执行", False, "%s: %s" % (type(e).__name__, e))

        # ══ ④ complete 无输出（前端注入终态）→ 提示 + 继续（回归护栏）══
        # 不发起真实请求（否则真实 LLM 会作答 → 非"无输出"），只注入终态观测前端分支。
        try:
            c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
            c.mq_on_capture(EVENTS)
            sid = new_session()
            c.mq_emit("session-changed", {"session_id": sid})
            time.sleep(1.5)
            c.eval("window.mq.emitRemote({type:%s,payload:%s}); 'ok'"
                   % (json.dumps("llm-complete"), json.dumps(json.dumps({"session": sid, "status": "complete"}))))
            time.sleep(3)
            d = dom(c, sid)
            cnt = counts(c)
            evi("4-complete-no-output", injected="llm-complete{status:complete}",
                hint=d["empty_hint_count"], hint_text=d["empty_hint_text"],
                continue_btn=d["continue_btn"], llm_complete=cnt["llm-complete"], llm_start=cnt["llm-start"])
            chk("④ complete 无输出 → 提示 + 继续按钮",
                d["empty_hint_count"] > 0 and d["continue_btn"] > 0, d)
            chk("④ 不自动续写（llm-start=0）", cnt["llm-start"] == 0, cnt)
        except Exception as e:
            chk("④ complete 无输出用例执行", False, "%s: %s" % (type(e).__name__, e))
    finally:
        if backup is not None:
            try:
                c.req("data-user-config-save", {"data": backup})
                c.mq_emit("config-refresh")
            except Exception:
                pass
        g.stop()
        mock.stop()
        srv.shutdown()
        srv.server_close()  # 关监听套接字并回收 handler 线程（shutdown 只停 serve_forever）
        print("[teardown] GUI/mock LLM/状态 mock 已回收", flush=True)

    print("\nS21 行为矩阵：%d/%d 断言通过" % (stat["n"] - len(stat["fail"]), stat["n"]), flush=True)
    return 0 if not stat["fail"] else 1


if __name__ == "__main__":
    sys.exit(main())

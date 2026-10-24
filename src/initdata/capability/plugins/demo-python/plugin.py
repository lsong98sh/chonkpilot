#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""ChonkPilot 插件规范演示（Python 语言，仅标准库）。

演示覆盖面（对照《插件与MCP-Server开发指南》）：
  1. 通道        AF_UNIX + 行分隔 JSON 帧（k/id/name/payload）→ plugin.hello / plugin.register
  2. MCP         Streamable HTTP（POST /mcp，JSON-RPC，Bearer）→ plugin.mcp.register
  3. 工具        demo_echo / demo_time / demo_counter（tools/list + tools/call）
  4. 配置        CHONKPILOT_PLUGIN_CONFIG / SECRETS 读取；plugin.config.changed 热重载
  5. UI RPC      plugin.ui.rpc（echo / stats / llm / counter.reset）供 iframe 调用
  6. 事件订阅    plugin.register.subscriptions（session-complete）
  7. 私有数据    plugin/v1/data.read|write + storage.self（计数持久化）
  8. LLM 桥      plugin/v1/llm.step（**UDS 或 TCP 均可**：ADDR=`tcp://host:port` 时走 TCP）
  9. 反向推送    plugin.publish → plugin-ui-event（iframe 实时日志）
 10. 状态上报    plugin.status.report（running / degraded）
 11. 生命周期    plugin.shutdown（ctl）优雅退出；plugin.sys.ping 健康应答

  传输：宿主注入的 CHONKPILOT_PLUGIN_ADDR 决定通道 —— 裸路径 = AF_UNIX（默认）；
  `tcp://127.0.0.1:<port>` = TCP（与 UDS 完全相同的帧协议）。LLM 走的也是这条通道。

运行：由宿主按 manifest 拉起（runtime.exe=python, args=["-u","plugin.py"]），
也可手工验证：设置 CHONKPILOT_PLUGIN_ADDR 后 `python plugin.py`。
"""

import json
import os
import socket
import sys
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PLUGIN_ID = "demo-python"
PLUGIN_VERSION = "1.0.0"

_lock = threading.Lock()
_cfg = {}
_counters = {"events": 0, "tools": 0, "rpc": 0}


# ── 配置 ────────────────────────────────────────────────────────────

def _load_json(path, default):
    try:
        with open(path, "r", encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return default


def load_config():
    global _cfg
    base = _load_json(os.environ.get("CHONKPILOT_PLUGIN_CONFIG", ""), {})
    secret = _load_json(os.environ.get("CHONKPILOT_PLUGIN_SECRETS", ""), {})
    merged = {"greeting": "hi from python", "echoLimit": 200, "flaky": False}
    merged.update(base or {})
    merged.update(secret or {})
    _cfg = merged
    log("config loaded: %s" % sorted(merged.keys()))


def cfg(key, default=None):
    return _cfg.get(key, default)


# ── 日志（stdout 由宿主重定向到 logs/plugin.log）─────────────────────

def log(msg):
    sys.stdout.write("[%s] %s\n" % (PLUGIN_ID, msg))
    sys.stdout.flush()


# ── MCP（Streamable HTTP，JSON-RPC over POST /mcp）───────────────────

TOOLS = [
    {
        "name": "demo_echo",
        "description": "回显文本（演示 MCP 工具与配置读取）。",
        "inputSchema": {
            "type": "object",
            "properties": {"text": {"type": "string", "description": "要回显的文本"}},
            "required": ["text"],
        },
    },
    {
        "name": "demo_time",
        "description": "返回插件进程时间与时区偏移（演示无参数工具）。",
        "inputSchema": {"type": "object", "properties": {}},
    },
    {
        "name": "demo_counter",
        "description": "读取/递增插件私有计数（演示 storage.self 数据面）。action=get|inc。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "action": {"type": "string", "description": "get（默认）/ inc"},
                "key": {"type": "string", "description": "计数键，默认 demo"},
            },
        },
    },
    {
        "name": "demo_llm",
        "description": "经插件通道调用宿主 LLM（plugin/v1/llm.step），演示「插件内用 LLM」而不持有 apiKey。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "prompt": {"type": "string", "description": "用户提示词"},
                "system": {"type": "string", "description": "系统提示词（可选）"},
            },
            "required": ["prompt"],
        },
    },
]


class MCPHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "chonk-demo/" + PLUGIN_VERSION

    def log_message(self, fmt, *args):  # 静音默认访问日志
        return

    def do_POST(self):
        if self.path.rstrip("/") != "/mcp":
            return self._send(404, {"error": "not found"})
        token = os.environ.get("CHONK_DEMO_TOKEN", "")
        if token and self.headers.get("Authorization", "") != "Bearer " + token:
            return self._send(401, {"error": "unauthorized"})
        try:
            n = int(self.headers.get("Content-Length", "0"))
            req = json.loads(self.rfile.read(n) or b"{}")
        except Exception as e:
            return self._send(400, {"jsonrpc": "2.0", "id": None,
                                    "error": {"code": -32700, "message": str(e)}})
        resp = self._rpc(req)
        if resp is None:  # notification
            self.send_response(202)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        self._send(200, resp)

    def do_GET(self):
        # 本演示不提供 SSE 流；给出明确 405（Streamable HTTP 允许）
        self._send(405, {"error": "GET not supported (stateless JSON mode)"})

    def _send(self, code, body):
        raw = json.dumps(body, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _rpc(self, req):
        method = req.get("method", "")
        rid = req.get("id")
        if rid is None and method.startswith("notifications/"):
            return None
        if method == "initialize":
            return self._ok(rid, {
                "protocolVersion": req.get("params", {}).get("protocolVersion", "2024-11-05"),
                "capabilities": {"tools": {"listChanged": False}},
                "serverInfo": {"name": PLUGIN_ID, "version": PLUGIN_VERSION},
            })
        if method == "ping":
            return self._ok(rid, {})
        if method == "tools/list":
            return self._ok(rid, {"tools": TOOLS})
        if method == "tools/call":
            params = req.get("params") or {}
            name = params.get("name", "")
            args = params.get("arguments") or {}
            try:
                text = call_tool(name, args)
                with _lock:
                    _counters["tools"] += 1
                return self._ok(rid, {"content": [{"type": "text", "text": text}], "isError": False})
            except Exception as e:
                return self._ok(rid, {"content": [{"type": "text", "text": "错误: %s" % e}], "isError": True})
        return {"jsonrpc": "2.0", "id": rid, "error": {"code": -32601, "message": "unknown method: " + method}}

    @staticmethod
    def _ok(rid, result):
        return {"jsonrpc": "2.0", "id": rid, "result": result}


def call_tool(name, args):
    if name == "demo_echo":
        text = str(args.get("text", ""))
        limit = int(cfg("echoLimit", 200) or 200)
        if len(text) > limit:
            text = text[:limit] + "…(truncated)"
        return "%s: %s" % (cfg("greeting", "hi"), text)
    if name == "demo_time":
        return "now=%s tz_offset_min=%d" % (time.strftime("%Y-%m-%d %H:%M:%S"), -time.timezone // 60)
    if name == "demo_counter":
        key = str(args.get("key", "demo"))
        if str(args.get("action", "get")) == "inc":
            return "counter %s = %s" % (key, counter_add(key, 1))
        return "counter %s = %s" % (key, counter_get(key))
    if name == "demo_llm":
        return llm_call(str(args.get("prompt", "")), str(args.get("system", "")))
    raise ValueError("unknown tool: " + name)


# ── 通道客户端 ──────────────────────────────────────────────────────

class Bridge:
    """插件通道客户端：AF_UNIX（默认）或 TCP（ADDR = tcp://host:port），帧协议完全一致。"""

    def __init__(self, addr):
        if addr.startswith("tcp://"):
            host, port = addr[6:].rsplit(":", 1)
            self.transport = "tcp://%s:%s" % (host, port)
            self.sock = socket.create_connection((host, int(port)), timeout=5)
        else:
            self.transport = "unix:" + addr
            self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            self.sock.settimeout(5)
            self.sock.connect(addr)
        self.sock.settimeout(None)
        log("bridge connected: %s" % self.transport)
        self.rf = self.sock.makefile("rb")
        self.wf = self.sock.makefile("wb")
        self.next_id = 0
        self.pending = {}
        self.lock = threading.Lock()
        self.closed = threading.Event()
        self.on_event = None
        self.reader = threading.Thread(target=self._read_loop, daemon=True)
        self.reader.start()

    def _read_loop(self):
        while not self.closed.is_set():
            line = self.rf.readline()
            if not line:
                self.closed.set()
                return
            try:
                f = json.loads(line)
            except Exception:
                continue
            k = f.get("k")
            if k == "res":
                with self.lock:
                    ev = self.pending.pop(f.get("id"), None)
                if ev is not None:
                    ev[1] = f
                    ev[0].set()
            elif k in ("req", "ctl", "evt"):
                threading.Thread(target=self._dispatch, args=(f,), daemon=True).start()

    def _dispatch(self, f):
        k, name = f.get("k"), f.get("name", "")
        payload = f.get("payload") or {}
        if k == "evt":
            with _lock:
                _counters["events"] += 1
            if name == "session-complete":
                publish("demo-event", {"kind": "session-complete", "at": int(time.time())})
            if callable(self.on_event):
                self.on_event(name, payload)
            return
        # req / ctl：同步处理并回 res
        try:
            result = self._handle(name, payload)
            self.send({"k": "res", "id": f.get("id"), "ok": True, "result": result})
        except Exception as e:
            self.send({"k": "res", "id": f.get("id"), "ok": False, "errors": [str(e)]})

    def _handle(self, name, payload):
        if name in ("plugin.sys.ping", "plugin/v1/sys.ping"):
            return {"pong": True, "ts": int(time.time() * 1000)}
        if name == "plugin.shutdown":
            threading.Thread(target=self.shutdown, daemon=True).start()
            return {"ok": True}
        if name == "plugin.config.changed":
            load_config()
            publish("demo-event", {"kind": "config-changed", "keys": sorted(_cfg.keys())})
            return {"ok": True}
        if name in ("plugin.ui.rpc", "plugin.command"):
            method = payload.get("method") or payload.get("name") or ""
            params = payload.get("params") or {}
            with _lock:
                _counters["rpc"] += 1
            return rpc(method, params)
        raise ValueError("method not found: " + name)

    def call(self, name, payload=None, timeout=30):
        with self.lock:
            self.next_id += 1
            rid = self.next_id
            ev = [threading.Event(), None]
            self.pending[rid] = ev
        self.send({"k": "req", "id": rid, "name": name, "payload": payload or {}})
        if not ev[0].wait(timeout):
            raise TimeoutError("bridge call timeout: " + name)
        f = ev[1] or {}
        if not f.get("ok"):
            raise RuntimeError("bridge %s failed: %s" % (name, f.get("errors")))
        return f.get("result")

    def send(self, frame):
        data = (json.dumps(frame, ensure_ascii=False) + "\n").encode("utf-8")
        with self.lock:
            self.wf.write(data)
            self.wf.flush()

    def shutdown(self):
        self.closed.set()
        try:
            self.sock.close()
        except Exception:
            pass
        os._exit(0)


_bridge = None


def publish(topic, payload):
    if _bridge is None:
        return
    try:
        _bridge.call("plugin.publish", {"topic": "plugin-ui-event", "payload": {"topic": topic, "payload": payload}})
    except Exception as e:
        log("publish failed: %s" % e)


def unwrap(v):
    """剥掉 {ok,result} 信封（最多 3 层）。"""
    for _ in range(3):
        if isinstance(v, dict) and "result" in v:
            v = v["result"]
        else:
            break
    return v


def llm_call(prompt, system=""):
    """经插件通道调宿主 LLM —— 与工具/数据面同一条通道（UDS 或 TCP），插件不持有 apiKey。"""
    if not str(prompt).strip():
        raise ValueError("prompt 不能为空")
    if _bridge is None:
        raise RuntimeError("bridge 未连接")
    payload = {"prompt": str(prompt)}
    if str(system).strip():
        payload["system"] = str(system)
    env = _bridge.call("plugin/v1/llm.step", payload, timeout=180)
    inner = unwrap(env)
    if isinstance(inner, dict):
        text = inner.get("text")
        if not text and isinstance(inner.get("result"), dict):
            text = inner["result"].get("text")
        if text:
            return str(text)
    if isinstance(inner, str) and inner.strip():
        return inner
    raise RuntimeError("llm.step 返回空文本")


# ── 私有数据（storage.self）─────────────────────────────────────────

def counter_get(key):
    if _bridge is None:
        return 0
    try:
        env = _bridge.call("plugin/v1/data.read", {
            "resource": "storage.self", "op": "get", "params": {"key": "counter." + key},
        })
        inner = unwrap(env)
        if isinstance(inner, dict) and inner.get("value") is not None:
            return int(inner["value"])
    except Exception as e:
        log("counter_get failed: %s" % e)
    return 0


def counter_add(key, delta):
    val = counter_get(key) + delta
    try:
        _bridge.call("plugin/v1/data.write", {
            "resource": "storage.self", "op": "set", "params": {"key": "counter." + key, "value": val},
        })
    except Exception as e:
        log("counter_set failed: %s" % e)
    return val


# ── UI RPC ─────────────────────────────────────────────────────────

def rpc(method, params):
    if method == "echo":
        return {"ok": True, "text": "%s: %s" % (cfg("greeting", "hi"), params.get("text", ""))}
    if method == "stats":
        with _lock:
            snap = dict(_counters)
        snap["counters"] = {"demo": counter_get("demo")}
        snap["config"] = {"greeting": cfg("greeting"), "echoLimit": cfg("echoLimit")}
        snap["transport"] = _bridge.transport if _bridge else ""
        return {"ok": True, "stats": snap}
    if method == "bridge":
        return {"ok": True, "transport": _bridge.transport if _bridge else ""}
    if method == "llm":
        return {"ok": True, "text": llm_call(params.get("prompt", ""), params.get("system", ""))}
    if method == "counter.reset":
        key = str(params.get("key", "demo"))
        _bridge.call("plugin/v1/data.write", {
            "resource": "storage.self", "op": "set", "params": {"key": "counter." + key, "value": 0},
        })
        publish("demo-event", {"kind": "counter-reset", "key": key})
        return {"ok": True}
    raise ValueError("unknown rpc: " + method)


# ── 主流程 ─────────────────────────────────────────────────────────

def start_mcp():
    """起 MCP HTTP（127.0.0.1:0 + Bearer），返回 (url, token)。"""
    token = os.environ.get("CHONK_DEMO_TOKEN") or os.urandom(12).hex()
    os.environ["CHONK_DEMO_TOKEN"] = token
    srv = ThreadingHTTPServer(("127.0.0.1", 0), MCPHandler)
    port = srv.server_address[1]
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return "http://127.0.0.1:%d/mcp" % port, token


def main():
    global _bridge
    addr = os.environ.get("CHONKPILOT_PLUGIN_ADDR", "")
    if not addr:
        log("CHONKPILOT_PLUGIN_ADDR 未设置（应由宿主注入）")
        return 2
    load_config()
    _bridge = Bridge(addr)

    _bridge.call("plugin.hello", {
        "id": PLUGIN_ID, "name": "Python 演示插件", "version": PLUGIN_VERSION,
        "features": {"mcp": "http", "ui": True, "lang": "python"},
    })
    _bridge.call("plugin.register", {
        "features": {"mcp": "http", "ui": True},
        "subscriptions": [{"pattern": "session-complete"}],
    })

    url, token = start_mcp()
    _bridge.call("plugin.mcp.register", {"url": url, "token": token, "hot_tools": ["demo_echo"]})
    _bridge.call("plugin.status.report", {"state": "running", "detail": "python demo ready"})
    log("ready: mcp=%s" % url)
    publish("demo-event", {"kind": "started", "at": int(time.time())})

    # 事件循环：reader 线程负责；主线程等关闭
    while not _bridge.closed.wait(1.0):
        if cfg("flaky"):
            _bridge.call("plugin.status.report", {"state": "degraded", "detail": "flaky=true"})
            time.sleep(5)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(0)

# -*- coding: utf-8 -*-
"""插件桥最小客户端（L4 验收用）：行分隔 JSON 帧（req/res/evt/ctl）。

仅实现测试所需：hello / 能力方法调用 / 工具主题绑定 / 接收下行帧。
权威协议：`src/lib/core/transport/transport.go` + `docs/bridge-plugin/插件桥接端口设计方案.md`。
"""

import json
import os
import queue
import socket
import threading
import time


class PluginClient:
    def __init__(self, addr, timeout=10):
        self.addr = addr
        self.timeout = timeout
        self._next = 0
        self._mu = threading.Lock()
        self._pending = {}
        self._events = queue.Queue()
        self._closed = False
        if addr.startswith("tcp://"):
            host, _, port = addr[len("tcp://"):].partition(":")
            self.sock = socket.create_connection((host, int(port)), timeout=timeout)
        else:
            self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            self.sock.settimeout(timeout)
            self.sock.connect(addr)
        self._buf = b""
        self._reader = threading.Thread(target=self._read_loop, daemon=True)
        self._reader.start()

    # ── 公共 API ────────────────────────────────────────────
    def hello(self, plugin_id):
        return self.call("plugin.hello", {"id": plugin_id})

    def bind(self, subject):
        return self.call("plugin.tool.bind", {"subject": subject})

    def call(self, name, payload=None, timeout=None):
        """发 req → 等同 id 的 res；返回 res 帧（dict）。"""
        with self._mu:
            self._next += 1
            fid = self._next
            self._pending[fid] = queue.Queue(maxsize=1)
        self._send({"k": "req", "id": fid, "name": name, "payload": payload})
        try:
            return self._pending[fid].get(timeout=timeout or self.timeout)
        finally:
            with self._mu:
                self._pending.pop(fid, None)

    def recv_event(self, timeout=5):
        """取一条非 res 帧（evt/ctl/req）；超时返回 None。"""
        try:
            return self._events.get(timeout=timeout)
        except queue.Empty:
            return None

    def close(self):
        self._closed = True
        try:
            self.sock.close()
        except Exception:
            pass

    # ── 内部 ────────────────────────────────────────────────
    def _send(self, frame):
        data = (json.dumps(frame, ensure_ascii=False) + "\n").encode("utf-8")
        self.sock.sendall(data)

    def _read_loop(self):
        try:
            while not self._closed:
                chunk = self.sock.recv(65536)
                if not chunk:
                    break
                self._buf += chunk
                while b"\n" in self._buf:
                    line, self._buf = self._buf.split(b"\n", 1)
                    if not line.strip():
                        continue
                    try:
                        f = json.loads(line)
                    except Exception:
                        continue
                    if f.get("k") == "res":
                        with self._mu:
                            q = self._pending.get(f.get("id"))
                        if q is not None:
                            q.put(f)
                    else:
                        self._events.put(f)
        except Exception:
            pass
        finally:
            with self._mu:
                pending = list(self._pending.items())
            for _, q in pending:
                try:
                    q.put({"k": "res", "ok": False, "errors": ["connection closed"]})
                except Exception:
                    pass


def default_plugin_uds():
    """与 Go `pluginbridge.DefaultAddr()` 同口径。"""
    p = os.environ.get("CHONK_PLUGIN_UDS", "")
    if p:
        return p
    base = os.environ.get("LOCALAPPDATA") or os.environ.get("TEMP") or "."
    return os.path.join(base, "chonkpilot", "plugin.sock")


def wait_socket(addr, timeout=30):
    """等插件桥 socket 可连接（GUI 就绪后桥仍可能有短暂窗口）。"""
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        try:
            c = PluginClient(addr, timeout=2)
            c.close()
            return True
        except Exception as e:  # noqa: BLE001
            last = e
            time.sleep(0.3)
    raise RuntimeError("插件桥 %s 在 %ss 内不可连接: %s" % (addr, timeout, last))

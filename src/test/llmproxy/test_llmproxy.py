#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""llmproxy 自测（标准库 unittest，无第三方依赖）。

运行：python test_llmproxy.py
覆盖：
  1. 普通 JSON 往返：代理响应体与直连一致；日志含 >>> REQ 请求体与 <<< RES 响应体；
  2. SSE 流式：响应体与直连逐块一致；日志含响应块内容。
"""

from __future__ import annotations

import http.client
import http.server
import json
import socketserver
import tempfile
import threading
import time
import unittest
from pathlib import Path
from urllib.parse import urlsplit

import llmproxy

# ---------------------------------------------------------------------------
# 假 provider
# ---------------------------------------------------------------------------

SSE_LINES = [f'data: {{"i": {i}}}\n\n' for i in range(3)] + ["data: [DONE]\n\n"]
SSE_BODY = "".join(SSE_LINES).encode("utf-8")


class _ProviderHandler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):  # 静默
        return

    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b""

        if self.path == "/v1/chat/completions":
            payload = json.dumps({"ok": True, "echo": body.decode("utf-8")}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        elif self.path == "/v1/stream":
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("Connection", "close")
            self.end_headers()
            for line in SSE_LINES:
                self.wfile.write(line.encode("utf-8"))
                self.wfile.flush()
                time.sleep(0.03)
            self.close_connection = True

        else:
            self.send_response(404)
            self.send_header("Content-Length", "0")
            self.end_headers()


class _ProviderServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True


# ---------------------------------------------------------------------------
# 工具函数
# ---------------------------------------------------------------------------


def post_json(url: str, body: str, headers: dict | None = None):
    """向 url 发 POST，返回 (status, body_bytes)。"""
    u = urlsplit(url)
    conn = http.client.HTTPConnection(u.hostname, u.port, timeout=15)
    try:
        hdrs = {"Content-Type": "application/json"}
        if headers:
            hdrs.update(headers)
        conn.request("POST", u.path, body=body.encode("utf-8"), headers=hdrs)
        resp = conn.getresponse()
        return resp.status, resp.read()
    finally:
        conn.close()


class LlmProxyTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.provider = _ProviderServer(("127.0.0.1", 0), _ProviderHandler)
        threading.Thread(target=cls.provider.serve_forever, daemon=True).start()
        cls.provider_port = cls.provider.server_address[1]
        cls.target = f"http://127.0.0.1:{cls.provider_port}/v1"

    @classmethod
    def tearDownClass(cls):
        cls.provider.shutdown()
        cls.provider.server_close()

    def _start_proxy(self, out_file):
        """起一个 llmproxy 实例，返回 (base_url, stop)；stop 可重复调用。"""
        args = llmproxy.parse_args(["--target", self.target, "--port", "0", "--out", str(out_file)])
        cfg = llmproxy.build_config(args)
        srv = llmproxy.create_server(cfg)
        threading.Thread(target=srv.serve_forever, daemon=True).start()

        done = {"closed": False}

        def stop():
            if done["closed"]:
                return
            done["closed"] = True
            srv.shutdown()
            srv.server_close()
            srv.log.close()

        self.addCleanup(stop)
        return f"http://127.0.0.1:{srv.server_address[1]}", stop

    # -- 1：JSON 往返一致 + 日志含请求/响应内容 ---------------------------

    def test_json_roundtrip_and_log(self):
        with tempfile.TemporaryDirectory() as tmp:
            log_file = Path(tmp) / "llmproxy.log"
            proxy, stop = self._start_proxy(log_file)
            try:
                body = '{"model":"m","messages":[{"role":"user","content":"hi"}]}'

                direct_status, direct_body = post_json(
                    f"{self.target}/chat/completions", body, {"Authorization": "Bearer secret-abc"}
                )
                proxy_status, proxy_body = post_json(
                    f"{proxy}/chat/completions", body, {"Authorization": "Bearer secret-abc"}
                )

                # ① 与直连一致
                self.assertEqual(proxy_status, 200)
                self.assertEqual(direct_status, 200)
                self.assertEqual(proxy_body, direct_body)

                # ② 日志含 REQ 请求体 与 RES 响应体
                text = log_file.read_text(encoding="utf-8")
                self.assertIn(">>> REQ ", text)
                self.assertIn("<<< RES ", text)
                self.assertIn(body, text)
                self.assertIn(proxy_body.decode("utf-8"), text)
                # 请求头不落盘：Authorization 不应出现在日志中
                self.assertNotIn("secret-abc", text)
            finally:
                stop()

    # -- 2：SSE 流式逐块一致 + 日志含响应块内容 ---------------------------

    def test_sse_stream_matches_direct(self):
        with tempfile.TemporaryDirectory() as tmp:
            log_file = Path(tmp) / "llmproxy.log"
            proxy, stop = self._start_proxy(log_file)
            try:
                body = '{"model":"m","stream":true}'

                _, direct_body = post_json(f"{self.target}/stream", body)
                proxy_status, proxy_body = post_json(f"{proxy}/stream", body)

                self.assertEqual(proxy_status, 200)
                self.assertEqual(direct_body, SSE_BODY)
                self.assertEqual(proxy_body, SSE_BODY)  # 逐块内容一致

                text = log_file.read_text(encoding="utf-8")
                self.assertIn(">>> REQ ", text)
                self.assertIn("<<< RES ", text)
                # 每个 SSE 块内容都进了日志
                for line in SSE_LINES:
                    self.assertIn(line.strip(), text)
            finally:
                stop()


if __name__ == "__main__":
    unittest.main(verbosity=2)

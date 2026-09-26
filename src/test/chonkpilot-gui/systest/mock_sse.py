# -*- coding: utf-8 -*-
"""临时 OpenAI 兼容 SSE mock（验证 OptimizeAgentPrompt 桥）。"""
import json
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

PORT = 8801


class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n).decode("utf-8", "replace")
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        text = "优化后的提示词内容: " + body[:60]
        for ch in text:
            chunk = {"choices": [{"delta": {"content": ch}, "finish_reason": None}]}
            self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
            self.wfile.flush()
            time.sleep(0.01)
        done = {"choices": [{"delta": {}, "finish_reason": "stop"}]}
        self.wfile.write(("data: " + json.dumps(done) + "\n\n").encode())
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

    def log_message(self, *a):
        pass


HTTPServer(("127.0.0.1", PORT), H).serve_forever()

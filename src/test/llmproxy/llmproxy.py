#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""llmproxy —— 本地 LLM 转发代理（仅用 Python 标准库）。

职责（最小可用版本）：
  1. 在 127.0.0.1 上监听，把收到的 HTTP 请求原样转发到真实 LLM provider（--target）；
  2. 把收发到的「内容」写进同一个日志文件（追加写、每写即 flush），便于实时 tail。

设计要点：
  - 只监听回环地址 127.0.0.1；
  - 转发时保留客户端请求路径（客户端 base_url 指到本代理，/chat/completions 原样拼接）；
  - 转发时强制请求头 Accept-Encoding: identity，换取上游返回明文，日志可读；
    其余请求头原样透传（含 Authorization）；不记录请求头，故 Authorization 天然不落盘；
  - text/event-stream 边读边转（read1 单块读 → 立即写回客户端并 flush），逐块落盘；
  - 上游错误 / 客户端断开：状态码原样透传，代理继续运行。
"""

from __future__ import annotations

import argparse
import http.client
import http.server
import json
import socketserver
import ssl
import sys
import threading
from datetime import datetime
from pathlib import Path
from urllib.parse import urlsplit

# ---------------------------------------------------------------------------
# 常量
# ---------------------------------------------------------------------------

DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 5710
DEFAULT_OUT = "./llmproxy.log"

# 逐跳头：不应在代理间原样透传
HOP_BY_HOP_HEADERS = {
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "te",
    "trailer",
    "trailers",
    "transfer-encoding",
    "upgrade",
    "host",
}

# 上游读超时（秒）：LLM 流式可能较长
READ_TIMEOUT = 600.0

STREAM_READ_SIZE = 65536


# ---------------------------------------------------------------------------
# 配置
# ---------------------------------------------------------------------------


class ProxyConfig:
    """代理运行配置。"""

    def __init__(self, target: str, port: int, out: Path) -> None:
        self.target = target
        self.port = int(port)
        self.out = Path(out)


# ---------------------------------------------------------------------------
# 内容日志：单个文件，追加写，行缓冲 + 每写即 flush
# ---------------------------------------------------------------------------


def now_iso() -> str:
    """当前本地时间的 ISO8601（毫秒）。"""
    return datetime.now().isoformat(timespec="milliseconds")


class ContentLog:
    """把收发内容写进同一个日志文件（追加、每写即 flush，可实时 tail）。"""

    def __init__(self, path: Path) -> None:
        self.path = Path(path)
        if self.path.parent and str(self.path.parent):
            self.path.parent.mkdir(parents=True, exist_ok=True)
        # buffering=1 → 行缓冲；open("a") → 追加写
        self._fh = open(self.path, "a", encoding="utf-8", buffering=1)
        self._lock = threading.Lock()

    def write(self, text: str) -> None:
        """写一段文本并立即 flush（多线程下加锁，避免不同请求内容交错）。"""
        with self._lock:
            self._fh.write(text)
            self._fh.flush()

    def close(self) -> None:
        try:
            self._fh.close()
        finally:
            self._fh = None


# ---------------------------------------------------------------------------
# 转发工具
# ---------------------------------------------------------------------------


def decode_body(data: bytes) -> str:
    """原始字节按 utf-8 宽松解码，保证日志可读。"""
    return data.decode("utf-8", errors="replace")


def connect_upstream(parsed):
    """按 scheme 建立到上游的连接。"""
    host = parsed.hostname
    if not host:
        raise ValueError("target 缺少主机名")
    if parsed.scheme == "https":
        return http.client.HTTPSConnection(
            host, parsed.port or 443, timeout=READ_TIMEOUT, context=ssl.create_default_context()
        )
    if parsed.scheme == "http":
        return http.client.HTTPConnection(host, parsed.port or 80, timeout=READ_TIMEOUT)
    raise ValueError(f"不支持的 target scheme: {parsed.scheme!r}")


# ---------------------------------------------------------------------------
# HTTP 处理器
# ---------------------------------------------------------------------------


class ProxyHandler(http.server.BaseHTTPRequestHandler):
    """把任意方法/路径透传到上游，并把收发内容写入日志文件。"""

    protocol_version = "HTTP/1.1"
    server_version = "llmproxy/1.0"

    # 关闭 BaseHTTPRequestHandler 的 stderr 访问日志
    def log_message(self, fmt, *args):  # noqa: A003
        return

    def do_GET(self):  # noqa: N802
        self._proxy()

    def do_POST(self):  # noqa: N802
        self._proxy()

    def do_PUT(self):  # noqa: N802
        self._proxy()

    def do_PATCH(self):  # noqa: N802
        self._proxy()

    def do_DELETE(self):  # noqa: N802
        self._proxy()

    def do_OPTIONS(self):  # noqa: N802
        self._proxy()

    def do_HEAD(self):  # noqa: N802
        self._proxy()

    # -- 内部实现 ---------------------------------------------------------

    def _read_incoming_body(self) -> bytes:
        """读取客户端请求体（支持 Content-Length 与 chunked）。"""
        te = (self.headers.get("Transfer-Encoding") or "").lower()
        if "chunked" in te:
            chunks = []
            while True:
                line = self.rfile.readline(65536).strip()
                if not line:
                    break
                size = int(line.split(b";")[0], 16)
                if size == 0:
                    self.rfile.readline(65536)  # 结束后的 CRLF
                    break
                chunks.append(self.rfile.read(size))
                self.rfile.read(2)  # 块尾 CRLF
            return b"".join(chunks)
        length = self.headers.get("Content-Length")
        if length:
            return self.rfile.read(int(length))
        return b""

    def _build_forward_headers(self, body: bytes) -> dict:
        """透传请求头，但强制 Accept-Encoding: identity 并修正 Content-Length。"""
        headers = {}
        for name, value in self.headers.items():
            low = name.lower()
            if low in HOP_BY_HOP_HEADERS or low == "accept-encoding":
                continue
            headers[name] = value
        headers["Accept-Encoding"] = "identity"
        if body:
            headers["Content-Length"] = str(len(body))
        else:
            headers.pop("Content-Length", None)
        return headers

    def _proxy(self) -> None:
        cfg: ProxyConfig = self.server.cfg
        log: ContentLog = self.server.log

        parsed = urlsplit(cfg.target)
        base_path = parsed.path.rstrip("/")
        upstream_path = base_path + self.path if base_path else self.path

        conn = None
        self._response_started = False
        try:
            body = self._read_incoming_body()
            # 请求：头部一行 + 原始请求体 + 空行
            log.write(f">>> REQ {now_iso()}\n")
            log.write(decode_body(body))
            log.write("\n\n")

            conn = connect_upstream(parsed)
            conn.request(
                self.command,
                upstream_path,
                body=body if body else None,
                headers=self._build_forward_headers(body),
            )
            resp = conn.getresponse()
            status = resp.status
            resp_headers = resp.getheaders()

            content_type = ""
            for k, v in resp_headers:
                if k.lower() == "content-type":
                    content_type = v
                    break

            # 响应：头部一行 + 原始响应内容（流式逐块）+ 空行
            log.write(f"<<< RES {now_iso()}\n")
            if "text/event-stream" in content_type.lower():
                self._relay_stream(log, resp, resp_headers, status)
            else:
                self._relay_buffered(log, resp, resp_headers, status)
        except Exception as exc:  # noqa: BLE001 —— 兜底：记录一行并透传失败
            try:
                log.write(f"!!! ERR {type(exc).__name__}: {exc}\n\n")
            except Exception:  # noqa: BLE001
                pass
            self._send_simple_error(502, f"{type(exc).__name__}: {exc}")
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:  # noqa: BLE001
                    pass

    def _write_headers(self, status: int, reason: str, resp_headers, extra=None, skip=()) -> None:
        """发送状态行 + 上游响应头（过滤逐跳头与 skip 名单）。"""
        skip = {s.lower() for s in skip}
        self.send_response_only(status, reason)
        for name, value in resp_headers:
            low = name.lower()
            if low in HOP_BY_HOP_HEADERS or low in skip:
                continue
            self.send_header(name, value)
        if extra:
            for name, value in extra.items():
                self.send_header(name, value)
        self.end_headers()
        self._response_started = True

    def _relay_stream(self, log: ContentLog, resp, resp_headers, status: int) -> None:
        """SSE：边读上游边写客户端并 flush，同时逐块落盘。"""
        self._write_headers(
            status,
            resp.reason,
            resp_headers,
            extra={"Transfer-Encoding": "chunked", "Connection": "close"},
            skip=("content-length",),
        )
        self.close_connection = True
        while True:
            chunk = resp.read1(STREAM_READ_SIZE)
            if not chunk:
                break
            log.write(decode_body(chunk))  # 每块立即落盘
            try:
                self.wfile.write(("%X\r\n" % len(chunk)).encode("ascii"))
                self.wfile.write(chunk)
                self.wfile.write(b"\r\n")
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
                break
        log.write("\n\n")
        try:
            self.wfile.write(b"0\r\n\r\n")  # chunked 结束标记
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
            pass

    def _relay_buffered(self, log: ContentLog, resp, resp_headers, status: int) -> None:
        """非流式：完整读取后写回，保持上游 Content-Length。"""
        data = b"" if self.command == "HEAD" else resp.read()
        log.write(decode_body(data))
        log.write("\n\n")
        if self.command != "HEAD":
            # 断言 Content-Length 与实体一致，避免客户端等待
            has_len = any(k.lower() == "content-length" for k, _ in resp_headers)
            extra = None if has_len else {"Content-Length": str(len(data))}
            self._write_headers(status, resp.reason, resp_headers, extra=extra)
            self.wfile.write(data)
            self.wfile.flush()
        else:
            self._write_headers(status, resp.reason, resp_headers)

    def _send_simple_error(self, status: int, message: str) -> None:
        """上游异常时给客户端一个最小错误响应（仅在尚未开始响应时）。"""
        if getattr(self, "_response_started", False):
            return
        try:
            payload = json.dumps({"error": {"message": message}}).encode("utf-8")
            self.send_response_only(status, "Bad Gateway")
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(payload)
                self.wfile.flush()
        except Exception:  # noqa: BLE001
            pass


class ProxyServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    """多线程 HTTP 服务器。"""

    daemon_threads = True
    allow_reuse_address = True

    def __init__(self, server_address, handler_class, cfg: ProxyConfig, log: ContentLog) -> None:
        super().__init__(server_address, handler_class)
        self.cfg = cfg
        self.log = log


# ---------------------------------------------------------------------------
# CLI / 启动
# ---------------------------------------------------------------------------


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="llmproxy",
        description="本地 LLM 转发代理：转发到真实 provider，并把收发内容写入一个日志文件。",
    )
    parser.add_argument("--target", required=True, help="真实 LLM base URL，如 https://api.deepseek.com/v1")
    parser.add_argument("--port", type=int, default=DEFAULT_PORT, help=f"监听端口（固定 127.0.0.1），默认 {DEFAULT_PORT}")
    parser.add_argument("--out", default=DEFAULT_OUT, help=f"日志文件路径（追加写），默认 {DEFAULT_OUT}")
    return parser


def parse_args(argv=None) -> argparse.Namespace:
    args = build_parser().parse_args(argv)
    if not (0 <= args.port <= 65535):
        raise SystemExit(f"--port 越界: {args.port}")
    return args


def build_config(args: argparse.Namespace) -> ProxyConfig:
    return ProxyConfig(target=args.target, port=args.port, out=args.out)


def create_server(cfg: ProxyConfig) -> ProxyServer:
    log = ContentLog(cfg.out)
    return ProxyServer((DEFAULT_HOST, cfg.port), ProxyHandler, cfg, log)


def main(argv=None) -> int:
    args = parse_args(argv)
    cfg = build_config(args)
    server = create_server(cfg)
    host, port = server.server_address[0], server.server_address[1]
    print(f"[llmproxy] http://{host}:{port} -> {args.target} (log={cfg.out})", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("[llmproxy] interrupted, shutting down", flush=True)
    finally:
        server.shutdown()
        server.server_close()
        server.log.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())

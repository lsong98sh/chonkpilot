#!/usr/bin/env python3
"""LLM 桥接入口的 loopback → UDS 字节级代理（demo）。

ChonkPilot 的 LLM 兼容入口只监听 UDS（默认 %LOCALAPPDATA%\\chonkpilot\\llm.sock），
不再开放任何 TCP 端口。只支持 TCP base_url 的 SDK / 语言，可在本地起这份代理：

    python llm_bridge_proxy.py --listen 127.0.0.1:5669
    # 之后把 SDK 的 base_url 指到 http://127.0.0.1:5669/llm/<provider>/v1

本代理**不解析 HTTP**：accept 后即与 UDS 建立连接，双向原样搬运字节，
因此流式（SSE）/ chunked / keep-alive / 大 body 全部照原样工作。

安全边界：
- 默认只绑 loopback（非 loopback 直接拒绝启动）——桥入口免 api-key，靠文件系统 ACL 收口；
- 身份不随代理传递（UDS peer credential 不可用），按调用方限流 / 审计只能在应用层做。

平台说明（重要）：CPython 在 **Windows 上没有 `socket.AF_UNIX`**（Linux/macOS 有）。
本脚本因此内置 WinSock（`Ws2_32.dll`）AF_UNIX 兜底，Windows 亦可直接运行；
Windows 下 UDS 路径上限 **107 字节**（`sockaddr_un`）。仅用 Python 标准库。
"""

from __future__ import annotations

import argparse
import os
import socket
import sys
import threading

BUF_SIZE = 64 * 1024
LOOPBACK_HOSTS = {"127.0.0.1", "localhost", "::1"}
WIN_AF_UNIX = 1  # Windows 的 AF_UNIX 值（Python 未导出）


def default_socket_path() -> str:
    env = os.environ.get("CHONK_LLM_UDS", "").strip()
    if env:
        return env
    base = os.environ.get("LOCALAPPDATA", "").strip()
    if base:
        return os.path.join(base, "chonkpilot", "llm.sock")
    return os.path.join(os.path.expanduser("~"), ".chonkpilot", "llm.sock")


def connect_unix(path: str) -> socket.socket:
    """连接 UDS 并返回可 send/recv 的 socket（Windows 走 WinSock 兜底）。"""
    if hasattr(socket, "AF_UNIX"):
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.connect(path)
        return s
    return _connect_unix_winsock(path)


def _connect_unix_winsock(path: str) -> socket.socket:
    if os.name != "nt":
        raise OSError("当前平台无 AF_UNIX（且非 Windows），无法连接 UDS")
    import ctypes
    from ctypes import wintypes

    raw = os.fspath(path).encode("utf-8")
    if len(raw) > 107:
        raise OSError("UDS 路径超过 107 字节（Windows sockaddr_un 上限）：%s" % path)

    ws2 = ctypes.WinDLL("Ws2_32.dll", use_last_error=True)
    wsadata = ctypes.create_string_buffer(512)
    if ws2.WSAStartup(0x0202, wsadata) != 0:
        raise OSError("WSAStartup 失败")

    class SockaddrUn(ctypes.Structure):
        _fields_ = [("sun_family", wintypes.USHORT), ("sun_path", ctypes.c_char * 108)]

    ws2.WSASocketW.restype = ctypes.c_size_t
    ws2.WSASocketW.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_int,
                               ctypes.c_void_p, ctypes.c_uint, ctypes.c_ulong]
    handle = ws2.WSASocketW(WIN_AF_UNIX, socket.SOCK_STREAM, 0, None, 0, 0)
    invalid = ctypes.c_size_t(-1).value
    if handle == invalid:
        raise OSError("WSASocketW(AF_UNIX) 失败: %d" % ws2.WSAGetLastError())

    addr = SockaddrUn()
    addr.sun_family = WIN_AF_UNIX
    addr.sun_path = raw
    ws2.connect.argtypes = [ctypes.c_size_t, ctypes.c_void_p, ctypes.c_int]
    if ws2.connect(handle, ctypes.byref(addr), ctypes.sizeof(addr)) != 0:
        err = ws2.WSAGetLastError()
        ws2.closesocket(handle)
        raise OSError("connect(%s) 失败: WinSock error %d" % (path, err))

    # 包装为 Python socket（send/recv/shutdown/close 直接复用；family 不影响字节收发）
    return socket.socket(fileno=handle)


def parse_listen(value: str) -> tuple[str, int]:
    value = value.strip()
    if ":" not in value:
        raise SystemExit(f"--listen 需要 host:port（收到 {value!r}）")
    host, port_text = value.rsplit(":", 1)
    host = host.strip("[]") or "127.0.0.1"
    try:
        port = int(port_text)
    except ValueError:
        raise SystemExit(f"--listen 端口非法：{port_text!r}")
    if host not in LOOPBACK_HOSTS:
        raise SystemExit(
            f"拒绝启动：{host} 不是 loopback。桥入口免 api-key，代理只允许绑本机回环地址。"
        )
    return host, port


def pump(src: socket.socket, dst: socket.socket) -> None:
    try:
        while True:
            chunk = src.recv(BUF_SIZE)
            if not chunk:
                break
            dst.sendall(chunk)
    except OSError:
        pass
    finally:
        # 单向结束 → 关写方向，让对端读到 EOF；另一方向仍可继续。
        try:
            dst.shutdown(socket.SHUT_WR)
        except OSError:
            pass


def handle(client: socket.socket, sock_path: str) -> None:
    try:
        upstream = connect_unix(sock_path)
    except OSError as exc:
        print(f"[proxy] 连接 UDS 失败 {sock_path}: {exc}", file=sys.stderr)
        client.close()
        return
    t1 = threading.Thread(target=pump, args=(client, upstream), daemon=True)
    t2 = threading.Thread(target=pump, args=(upstream, client), daemon=True)
    t1.start()
    t2.start()
    t1.join()
    t2.join()
    client.close()
    upstream.close()


def main() -> int:
    ap = argparse.ArgumentParser(description="ChonkPilot LLM 桥接入口 loopback → UDS 字节代理")
    ap.add_argument("--listen", default="127.0.0.1:5669", help="监听地址（仅 loopback，默认 127.0.0.1:5669）")
    ap.add_argument("--socket", default="", help="UDS 路径（默认取 CHONK_LLM_UDS，否则 %%LOCALAPPDATA%%\\chonkpilot\\llm.sock）")
    args = ap.parse_args()

    host, port = parse_listen(args.listen)
    sock_path = args.socket.strip() or default_socket_path()
    if not os.path.exists(sock_path):
        print(f"[proxy] 警告：{sock_path} 当前不存在（宿主未启动 / 桥已关闭？）", file=sys.stderr)

    family = socket.AF_INET6 if ":" in host else socket.AF_INET
    server = socket.socket(family, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind((host, port))
    server.listen(64)
    print(f"[proxy] listening on http://{host}:{port}  ->  unix:{sock_path}", flush=True)
    try:
        while True:
            client, _ = server.accept()
            threading.Thread(target=handle, args=(client, sock_path), daemon=True).start()
    except KeyboardInterrupt:
        pass
    finally:
        server.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

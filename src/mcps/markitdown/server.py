#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""ChonkPilot 文档转换服务（源码工程 = src/mcps/markitdown）。

单进程 HTTP 服务，**只监听 127.0.0.1**；同一端口同时提供两种入口：

1. **MCP（Streamable HTTP）端点 `/mcp`** —— 暴露工具 `convert_to_markdown(uri_or_path)`，
   返回 Markdown 文本。本服务**不预置**进产品，由用户在 MCP 配置页手动注册
   （注册指引见同目录 README.md）。本地使用，不加额外鉴权；跨机访问由回环绑定阻断。
2. **vfts 内部快通道**（供 vfts 引擎直连，省掉 MCP 往返）：
   - `GET  /vfts/health`  —— 探活，**无需 token**，返回 `{ok, version, pid, port, uptime_s, state_path}`
   - `POST /vfts/convert` —— **需 `X-Chonk-Token` 头**，body `{path, max_bytes?, root?}`；
     `path` **必须**落在允许根内（请求 `root` 或环境变量 `MARKITDOWN_ALLOWED_ROOTS`）

启动时把运行态写入状态文件 `state.json`（与 exe 同目录，即 `<installRoot>/mcps/markitdown/`；
目录不可写时回落到 `<data-dir>/mcps/markitdown/`，Windows 的 data-dir = `%LOCALAPPDATA%\\chonkpilot`），
优雅退出时删除。实际写入路径记入日志并在 `/vfts/health` 回报（插件侧两处都会探）。

元数据/契约见 README.md；构建见 build-mcps.ps1。
"""

from __future__ import annotations

import argparse
import asyncio
import ipaddress
import json
import logging
import os
import re
import secrets
import shutil
import socket
import sys
import tempfile
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from datetime import datetime, timezone
from logging.handlers import RotatingFileHandler
from typing import Any, Optional
from urllib.error import HTTPError, URLError
from urllib.parse import urlparse
from urllib.request import Request as UrlRequest
from urllib.request import url2pathname, urlopen

import markitdown as _markitdown_pkg
from markitdown import (
    FileConversionException,
    MarkItDown,
    MissingDependencyException,
    UnsupportedFormatException,
)

from mcp.server.mcpserver import MCPServer
from starlette.requests import Request
from starlette.responses import JSONResponse

# ─────────────────────────────── 常量 ───────────────────────────────

CONVERTER_NAME = "markitdown"
CONVERTER_VERSION = "0.1.0"
MARKITDOWN_VERSION = getattr(_markitdown_pkg, "__version__", "unknown")
PARSER_VERSION = f"{CONVERTER_NAME}-mcp/{CONVERTER_VERSION} (markitdown/{MARKITDOWN_VERSION})"

SERVICE_NAME = "chonkpilot-docconv"
DEFAULT_PORT = 7317
PORT_SCAN_SPAN = 100  # 默认端口被占时向后试探的端口数，仍无则取系统临时端口
DEFAULT_WORKERS = 4
DEFAULT_MAX_INPUT_BYTES = 50 * 1024 * 1024  # 输入文件上限 50 MiB
DEFAULT_MAX_OUTPUT_BYTES = 2 * 1024 * 1024  # 输出 markdown 上限 2 MiB（超出截断）
DEFAULT_TIMEOUT_SEC = 60.0  # 单文件转换超时
REMOTE_FETCH_TIMEOUT_SEC = 30.0  # MCP 工具远端抓取超时

# ── 安全约束（2026-10-08）────────────────────────────────────────────────
# 远端抓取（convert_to_markdown 的 http(s) 源）SSRF 防护：默认拒绝解析到本机 / 私网 /
# 链路本地 / 保留地址（含云元数据 169.254.169.254）。内网联调可设
# MARKITDOWN_ALLOW_PRIVATE_HOSTS=1 显式放行（关闭校验）。
ALLOW_PRIVATE_HOSTS = os.environ.get("MARKITDOWN_ALLOW_PRIVATE_HOSTS", "").strip().lower() in (
    "1",
    "true",
    "yes",
    "on",
)
# `/vfts/convert` 允许访问的根目录白名单（os.pathsep 分隔的绝对路径）；缺省空 =
# 仅接受请求体显式携带的 `root`。见 allowed_convert_roots / path_within_roots。
ALLOWED_ROOTS_ENV = "MARKITDOWN_ALLOWED_ROOTS"

LOG_FILENAME = "markitdown-mcp.log"
LOG_MAX_BYTES = 1024 * 1024
LOG_BACKUPS = 2
STATE_FILENAME = "state.json"
STATE_REL_DIR = os.path.join("mcps", "markitdown")  # 仅用于 data-dir 回落路径

TOKEN_HEADER = "X-Chonk-Token"

# 错误码（`/vfts/convert` 失败响应体；`unauthorized` 为传输层 401，不在转换错误枚举内）
ERROR_UNSUPPORTED_FORMAT = "unsupported_format"
ERROR_ENCRYPTED = "encrypted"
ERROR_TOO_LARGE = "too_large"
ERROR_SCAN_ONLY = "scan_only"
ERROR_PARSE_ERROR = "parse_error"
ERROR_NOT_FOUND = "not_found"
ERROR_TIMEOUT = "timeout"

# 支持格式（由 markitdown 内置转换器覆盖）
PRIMARY_EXTENSIONS = (".pdf", ".docx", ".xlsx", ".pptx")
TEXT_EXTENSIONS = (
    ".txt",
    ".text",
    ".md",
    ".markdown",
    ".rst",
    ".log",
    ".ini",
    ".toml",
    ".yaml",
    ".yml",
    ".csv",
    ".json",
    ".jsonl",
    ".xml",
    ".html",
    ".htm",
)
OOXML_EXTENSIONS = (".docx", ".xlsx", ".pptx")
LEGACY_BINARY_EXTENSIONS = (".doc", ".xls", ".ppt")  # 旧版二进制 → unsupported_format
SUPPORTED_EXTENSIONS = PRIMARY_EXTENSIONS + TEXT_EXTENSIONS

OLE_MAGIC = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"  # OLE/CFB：加密的 OOXML 或旧版二进制
PDF_MAGIC = b"%PDF-"

# loc 抽取用正则（markitdown 输出里的稳定标记）
_XLSX_SHEET_RE = re.compile(r"^##[ \t]+(.+?)[ \t]*$", re.MULTILINE)
_PPTX_SLIDE_RE = re.compile(r"<!--\s*Slide number:\s*(\d+)\s*-->")

LOC_KIND_PAGE = "page"
LOC_KIND_SHEET = "sheet"
LOC_KIND_SLIDE = "slide"
LOC_KIND_NONE = "none"

# ─────────────────────────── 运行态（进程内） ───────────────────────────

RUNTIME: dict[str, Any] = {
    "port": None,
    "workers": DEFAULT_WORKERS,
    "token": "",
    "state_path": None,
    "started_monotonic": time.monotonic(),
}

_LOG = logging.getLogger("markitdown_mcp")
_LOG.setLevel(logging.DEBUG)
_LOG.propagate = False
if not _LOG.handlers:
    _stderr_handler = logging.StreamHandler(sys.stderr)
    _stderr_handler.setFormatter(
        logging.Formatter("%(asctime)s %(levelname)s %(name)s %(message)s")
    )
    _LOG.addHandler(_stderr_handler)

_executor: Optional[ThreadPoolExecutor] = None
_executor_lock = threading.Lock()

_markitdown: Optional[MarkItDown] = None
_markitdown_lock = threading.Lock()

_remote_fetch_lock = threading.Lock()  # markitdown 内部 requests.Session 非严格线程安全


# ─────────────────────────────── 结果模型 ───────────────────────────────


@dataclass
class Outcome:
    """一次转换的结果（成功或失败）。"""

    ok: bool
    text: str = ""
    loc_kind: str = LOC_KIND_NONE
    locs: list[dict] = field(default_factory=list)
    truncated: bool = False
    error_code: str = ""
    message: str = ""
    elapsed_ms: int = 0

    def to_response(self) -> dict:
        """转成 `/vfts/convert` 的响应体。"""
        if not self.ok:
            return {"ok": False, "error_code": self.error_code, "message": self.message}
        body: dict[str, Any] = {
            "ok": True,
            "text": self.text,
            "loc_kind": self.loc_kind,
            "truncated": self.truncated,
            "parser_version": PARSER_VERSION,
            "elapsed_ms": self.elapsed_ms,
        }
        if self.locs:
            body["locs"] = self.locs
        return body


def _fail(code: str, message: str, t0: float) -> Outcome:
    return Outcome(
        ok=False,
        error_code=code,
        message=message,
        elapsed_ms=int((time.perf_counter() - t0) * 1000),
    )


# ───────────────────────────── 路径与状态文件 ─────────────────────────────


def exe_dir() -> str:
    """转换器 exe 所在目录（源码运行时 = 本文件所在目录）。"""
    if getattr(sys, "frozen", False):
        return os.path.dirname(os.path.abspath(sys.executable))
    return os.path.dirname(os.path.abspath(__file__))


def data_dir() -> str:
    """用户级数据根目录（Windows = %LOCALAPPDATA%\\chonkpilot）。"""
    if os.name == "nt":
        base = os.environ.get("LOCALAPPDATA") or os.path.join(
            os.path.expanduser("~"), "AppData", "Local"
        )
    else:
        base = os.path.expanduser("~")
    return os.path.join(base, "chonkpilot")


def _dir_writable(path: str) -> bool:
    """探测目录是否可写（安装目录可能只读）。"""
    try:
        os.makedirs(path, exist_ok=True)
        probe = os.path.join(path, ".write-probe")
        with open(probe, "w", encoding="utf-8") as fh:
            fh.write("ok")
        os.remove(probe)
        return True
    except OSError:
        return False


def resolve_state_path(logger: logging.Logger) -> str:
    """定位可写的状态文件路径：优先 exe 同目录，只读则回落到 data-dir。"""
    candidates = [
        exe_dir(),  # 安装布局：<installRoot>/mcps/markitdown/（与 exe 同目录）
        os.path.join(data_dir(), STATE_REL_DIR),
    ]
    for directory in candidates:
        if _dir_writable(directory):
            return os.path.join(directory, STATE_FILENAME)
        logger.warning("state dir not writable, falling back: %s", directory)
    raise RuntimeError("no writable directory for state file")


def write_state(path: str, payload: dict) -> None:
    """原子写入状态文件（UTF-8，不转义非 ASCII）。"""
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, ensure_ascii=False, indent=2)
    os.replace(tmp, path)


def remove_state(path: Optional[str]) -> None:
    """优雅退出时删除状态文件（仅当它确实属于本进程）。"""
    if not path:
        return
    try:
        with open(path, encoding="utf-8") as fh:
            current = json.load(fh)
        if current.get("pid") == os.getpid():
            os.remove(path)
    except FileNotFoundError:
        pass
    except (OSError, ValueError):
        _LOG.warning("failed to remove state file: %s", path)


def add_file_handler(logger: logging.Logger, log_path: str) -> None:
    handler = RotatingFileHandler(
        log_path, maxBytes=LOG_MAX_BYTES, backupCount=LOG_BACKUPS, encoding="utf-8"
    )
    handler.setFormatter(
        logging.Formatter("%(asctime)s %(levelname)s %(name)s %(message)s")
    )
    logger.addHandler(handler)


# ─────────────────────────────── 转换核心 ───────────────────────────────


def get_markitdown() -> MarkItDown:
    """惰性创建（并复用）MarkItDown 实例。"""
    global _markitdown
    if _markitdown is None:
        with _markitdown_lock:
            if _markitdown is None:
                _markitdown = MarkItDown()
    return _markitdown


def get_executor() -> ThreadPoolExecutor:
    """转换线程池（进程内单例，大小 = --workers）。"""
    global _executor
    if _executor is None:
        with _executor_lock:
            if _executor is None:
                _executor = ThreadPoolExecutor(
                    max_workers=max(1, int(RUNTIME.get("workers") or DEFAULT_WORKERS)),
                    thread_name_prefix="convert",
                )
    return _executor


async def run_in_convert_pool(func, *args, timeout: float = DEFAULT_TIMEOUT_SEC):
    """在转换线程池里执行 func(*args) 并施加超时；超时 → 尽力取消 future 后抛 TimeoutError。

    取消语义（Python 限制）：线程不可被强制中断——若 func **已开始执行**，future.cancel()
    只能回收**尚未出队**的任务（释放线程池槽位）；运行中的转换仍会占用一个工作线程直至其
    自然返回。超时判定本身不阻塞：wait_for 取消的是 asyncio 包装 future，应答即刻返回。
    """
    loop = asyncio.get_running_loop()
    future = loop.run_in_executor(get_executor(), func, *args)
    try:
        return await asyncio.wait_for(future, timeout=timeout)
    except asyncio.TimeoutError:
        future.cancel()  # 尽力取消：未出队 → 释放槽位；运行中 → 幂等无副作用
        raise


def truncate_utf8(text: str, max_bytes: int) -> tuple[str, bool]:
    """按 UTF-8 字节数截断（不切开多字节字符）。"""
    encoded = text.encode("utf-8")
    if len(encoded) <= max_bytes:
        return text, False
    return encoded[:max_bytes].decode("utf-8", errors="ignore"), True


def _read_head(path: str, size: int = 8) -> bytes:
    try:
        with open(path, "rb") as fh:
            return fh.read(size)
    except OSError:
        return b""


def _is_encrypted_ooxml(path: str) -> bool:
    """加密的 OOXML（docx/xlsx/pptx）实际是 OLE/CFB 容器。"""
    return _read_head(path, 8) == OLE_MAGIC


def _classify_failure(exc: BaseException, t0: float) -> Outcome:
    """把转换异常映射到错误码。"""
    text = f"{type(exc).__name__}: {exc}".lower()
    if "password" in text or "encrypt" in text:
        return _fail(ERROR_ENCRYPTED, "document is encrypted or password protected", t0)
    if isinstance(exc, MissingDependencyException):
        return _fail(ERROR_UNSUPPORTED_FORMAT, str(exc), t0)
    return _fail(ERROR_PARSE_ERROR, f"{type(exc).__name__}: {exc}", t0)


def _first_line(text: str) -> str:
    for line in text.splitlines():
        stripped = line.strip()
        if stripped:
            return stripped
    return ""


def byte_offset(text: str, char_index: int) -> int:
    """字符索引（Python str 码点下标）→ **UTF-8 字节偏移**（与 Go 消费侧一致）。

    中文等多字节字符下二者相差数倍；索引侧按字节切块，故 locs[].offset 一律用字节口径。
    """
    if char_index <= 0:
        return 0
    if char_index >= len(text):
        char_index = len(text)
    return len(text[:char_index].encode("utf-8"))


def _pdf_page_locs(path: str, text: str) -> list[dict]:
    """PDF 页码定位：逐页取首个非空行做锚点，在全文中顺序查找其偏移。

    锚点在**字符域**查找（`str.find`），产出的 `offset` 转成 **UTF-8 字节偏移**。
    锚点找不到时退化为当前游标位置（best-effort；markitdown 对表单型页面会改写文本，
    此时锚点可能失配）。
    """
    locs: list[dict] = []
    try:
        import pdfplumber
    except ImportError:  # pragma: no cover - pdf extra 缺失
        return locs
    try:
        with pdfplumber.open(path) as pdf:
            cursor = 0
            for index, page in enumerate(pdf.pages, 1):
                try:
                    raw = page.extract_text() or ""
                finally:
                    page.close()
                anchor = _first_line(raw)
                if not anchor:
                    continue
                found = text.find(anchor, cursor)
                offset = found if found >= 0 else cursor
                locs.append({"loc": str(index), "offset": byte_offset(text, offset)})
                cursor = max(cursor, offset + len(anchor))
    except Exception as exc:  # 定位失败不影响转换结果
        _LOG.warning("pdf page loc extraction failed: %s", exc)
        return []
    return locs


def build_locs(path: str, extension: str, text: str) -> tuple[str, list[dict]]:
    """按格式产出定位信息：pdf→页码 / xlsx→sheet 名 / pptx→slide 序号 / 其余→无。

    `locs[].offset` 一律为 `text` 中的 **UTF-8 字节偏移**（与索引侧消费口径一致）。
    """
    if extension == ".pdf":
        return LOC_KIND_PAGE, _pdf_page_locs(path, text)
    if extension == ".xlsx":
        locs = [
            {"loc": m.group(1).strip(), "offset": byte_offset(text, m.start())}
            for m in _XLSX_SHEET_RE.finditer(text)
        ]
        return LOC_KIND_SHEET, locs
    if extension == ".pptx":
        locs = [
            {"loc": m.group(1), "offset": byte_offset(text, m.start())}
            for m in _PPTX_SLIDE_RE.finditer(text)
        ]
        return LOC_KIND_SLIDE, locs
    return LOC_KIND_NONE, []


def convert_path(path: str, max_input_bytes: int = DEFAULT_MAX_INPUT_BYTES) -> Outcome:
    """转换本地文件，返回 Outcome（不抛异常）。"""
    t0 = time.perf_counter()

    if not os.path.exists(path):
        return _fail(ERROR_NOT_FOUND, f"path does not exist: {path}", t0)
    if not os.path.isfile(path):
        return _fail(ERROR_NOT_FOUND, f"not a regular file: {path}", t0)

    try:
        size = os.path.getsize(path)
    except OSError as exc:
        return _fail(ERROR_NOT_FOUND, f"cannot stat file: {exc}", t0)
    if size > max_input_bytes:
        return _fail(
            ERROR_TOO_LARGE,
            f"file size {size} bytes exceeds limit {max_input_bytes} bytes",
            t0,
        )

    extension = os.path.splitext(path)[1].lower()
    if extension not in SUPPORTED_EXTENSIONS:
        hint = (
            "legacy binary Office formats are not supported"
            if extension in LEGACY_BINARY_EXTENSIONS
            else "unsupported extension"
        )
        return _fail(
            ERROR_UNSUPPORTED_FORMAT,
            f"{hint}: '{extension or '(no extension)'}'; supported: "
            + ", ".join(SUPPORTED_EXTENSIONS),
            t0,
        )
    if extension in OOXML_EXTENSIONS and _is_encrypted_ooxml(path):
        return _fail(ERROR_ENCRYPTED, "OOXML file is encrypted (password protected)", t0)
    if extension == ".pdf" and _read_head(path, len(PDF_MAGIC)) != PDF_MAGIC:
        return _fail(ERROR_PARSE_ERROR, "file does not look like a PDF", t0)

    try:
        result = get_markitdown().convert_local(path)
        text = result.markdown or ""
    except UnsupportedFormatException as exc:
        return _fail(ERROR_UNSUPPORTED_FORMAT, str(exc), t0)
    except MissingDependencyException as exc:
        return _fail(ERROR_UNSUPPORTED_FORMAT, str(exc), t0)
    except FileConversionException as exc:
        return _classify_failure(exc, t0)
    except Exception as exc:  # noqa: BLE001 - 全部归一为结构化错误码
        return _classify_failure(exc, t0)

    if extension == ".pdf" and not text.strip():
        return _fail(
            ERROR_SCAN_ONLY,
            "no text layer extracted (scanned/image-only PDF is not supported; OCR not implemented)",
            t0,
        )

    loc_kind, locs = build_locs(path, extension, text)
    text, truncated = truncate_utf8(text, DEFAULT_MAX_OUTPUT_BYTES)
    if truncated:
        # offset 为 UTF-8 字节偏移 → 与截断后文本的字节长度比较
        max_offset = len(text.encode("utf-8"))
        locs = [entry for entry in locs if entry["offset"] <= max_offset]

    return Outcome(
        ok=True,
        text=text,
        loc_kind=loc_kind,
        locs=locs,
        truncated=truncated,
        elapsed_ms=int((time.perf_counter() - t0) * 1000),
    )


def _file_uri_to_path(uri: str) -> str:
    """file:// URI → 本地路径（仅接受本机路径，拒绝 UNC/设备路径）。"""
    parsed = urlparse(uri)
    if parsed.netloc and parsed.netloc.lower() not in ("", "localhost"):
        raise ValueError(f"unsupported file URI host: {parsed.netloc}")
    path = url2pathname(parsed.path)
    if os.name == "nt" and path[:1] in ("/", "\\") and path[2:3] == ":":
        path = path[1:]
    return path


def _is_disallowed_ip(ip: "ipaddress.IPv4Address | ipaddress.IPv6Address") -> bool:
    """判断解析出的 IP 是否属**禁止抓取**的目标（本机 / 私网 / 链路本地 / 保留 / 组播 / 未指定）。

    169.254.169.254 等云元数据地址落在链路本地段（169.254.0.0/16），由 is_link_local 覆盖。
    """
    if ip.is_loopback or ip.is_private or ip.is_link_local:
        return True
    if ip.is_multicast or ip.is_reserved or ip.is_unspecified:
        return True
    mapped = getattr(ip, "ipv4_mapped", None)  # IPv4-mapped IPv6（::ffff:127.0.0.1）→ 还原再判
    if mapped is not None:
        return _is_disallowed_ip(mapped)
    return False


def check_remote_host(host: str) -> str:
    """解析 host（含 DNS）并校验目标地址；返回拒绝原因（空串 = 通过）。

    在 urlopen **之前**调用，阻断 SSRF（本机 / 私网 / 链路本地 / 云元数据地址）。
    MARKITDOWN_ALLOW_PRIVATE_HOSTS 显式放行（内网联调）时不做校验。
    注：DNS 重绑定（校验与连接之间地址变化）不在本策略覆盖范围内。
    """
    if ALLOW_PRIVATE_HOSTS:
        return ""
    try:
        infos = socket.getaddrinfo(host, None, proto=socket.IPPROTO_TCP)
    except socket.gaierror as exc:
        return f"cannot resolve host {host!r}: {exc}"
    if not infos:
        return f"cannot resolve host {host!r}"
    for info in infos:
        addr = info[4][0]
        try:
            ip = ipaddress.ip_address(addr.split("%", 1)[0])  # 去 IPv6 zone id
        except ValueError:
            continue
        if _is_disallowed_ip(ip):
            return f"host {host!r} resolves to disallowed address {addr}"
    return ""


def allowed_convert_roots(extra: Optional[str] = None) -> list[str]:
    """生效的 `/vfts/convert` 允许根：请求体显式 `root`（若有）+ 环境变量白名单。"""
    roots: list[str] = []
    if extra and extra.strip():
        roots.append(extra.strip())
    raw = os.environ.get(ALLOWED_ROOTS_ENV, "") or ""
    for candidate in raw.split(os.pathsep):
        candidate = candidate.strip()
        if candidate:
            roots.append(candidate)
    return roots


def path_within_roots(path: str, roots: list[str]) -> bool:
    """`path` 的 realpath 是否落在任一 root 的 realpath 之内（含符号链接解析，防越界读）。"""
    if not roots:
        return False
    try:
        real = os.path.realpath(path)
    except OSError:
        return False
    for root in roots:
        try:
            real_root = os.path.realpath(root)
            # commonpath 逐段比较，避免 "/a/bc" 被误判落在 "/a/b" 之内
            if os.path.commonpath([real, real_root]) == real_root:
                return True
        except (OSError, ValueError):  # 不同盘符 / 路径不可比较 → 该根不匹配
            continue
    return False


def _convert_remote(url: str, max_input_bytes: int) -> Outcome:
    """下载远端文件到临时文件后复用 convert_path（含大小上限与超时）。

    抓取前校验 scheme 与目标 host/IP（SSRF 防护，见 check_remote_host）。
    """
    t0 = time.perf_counter()
    parsed = urlparse(url)
    if parsed.scheme.lower() not in ("http", "https"):
        return _fail(
            ERROR_NOT_FOUND,
            f"unsupported URL scheme: '{parsed.scheme or '(none)'}' (only http/https)",
            t0,
        )
    host = parsed.hostname or ""
    if not host:
        return _fail(ERROR_NOT_FOUND, f"URL has no host: {url}", t0)
    if reason := check_remote_host(host):
        return _fail(ERROR_PARSE_ERROR, f"refusing to fetch remote URL: {reason}", t0)
    extension = os.path.splitext(parsed.path)[1].lower()
    if extension not in SUPPORTED_EXTENSIONS:
        return _fail(
            ERROR_UNSUPPORTED_FORMAT,
            f"cannot infer a supported format from URL: '{extension or '(no extension)'}'",
            t0,
        )

    tmpdir = tempfile.mkdtemp(prefix="chonkpilot-docconv-")
    tmp_path = os.path.join(tmpdir, "download" + extension)
    try:
        request = UrlRequest(
            url,
            headers={
                "User-Agent": f"chonkpilot-docconv/{CONVERTER_VERSION}",
                "Accept": "text/markdown, text/html;q=0.9, */*;q=0.1",
            },
        )
        try:
            with _remote_fetch_lock:
                with urlopen(request, timeout=REMOTE_FETCH_TIMEOUT_SEC) as response:
                    total = 0
                    with open(tmp_path, "wb") as out:
                        while True:
                            chunk = response.read(65536)
                            if not chunk:
                                break
                            total += len(chunk)
                            if total > max_input_bytes:
                                return _fail(
                                    ERROR_TOO_LARGE,
                                    f"remote file exceeds limit {max_input_bytes} bytes",
                                    t0,
                                )
                            out.write(chunk)
        except HTTPError as exc:
            code = ERROR_NOT_FOUND if exc.code == 404 else ERROR_PARSE_ERROR
            return _fail(code, f"http {exc.code} while fetching: {url}", t0)
        except URLError as exc:
            return _fail(ERROR_NOT_FOUND, f"cannot fetch URL: {exc.reason}", t0)
        except OSError as exc:
            return _fail(ERROR_PARSE_ERROR, f"download failed: {exc}", t0)

        return convert_path(tmp_path, max_input_bytes)
    finally:
        shutil.rmtree(tmpdir, ignore_errors=True)


def convert_source(
    uri_or_path: str, max_input_bytes: int = DEFAULT_MAX_INPUT_BYTES
) -> Outcome:
    """统一入口：本地路径 / `file://` URI / `http(s)://` URL。"""
    t0 = time.perf_counter()
    raw = (uri_or_path or "").strip()
    if not raw:
        return _fail(ERROR_NOT_FOUND, "empty uri_or_path", t0)

    if os.path.isfile(raw):
        return convert_path(raw, max_input_bytes)

    scheme = urlparse(raw).scheme.lower()
    if scheme in ("http", "https"):
        return _convert_remote(raw, max_input_bytes)
    if scheme == "file":
        try:
            return convert_path(_file_uri_to_path(raw), max_input_bytes)
        except ValueError as exc:
            return _fail(ERROR_NOT_FOUND, str(exc), t0)

    return _fail(
        ERROR_NOT_FOUND,
        f"not an existing file and not a supported URI (http/https/file): {raw}",
        t0,
    )


# ─────────────────────────── MCP server 与 HTTP 路由 ───────────────────────────

server = MCPServer(
    name=SERVICE_NAME,
    title="ChonkPilot Document Converter",
    version=CONVERTER_VERSION,
    instructions=(
        "把本地或远端文档（PDF / Word / Excel / PowerPoint / 文本类）转换为 Markdown 文本，"
        "供全文检索或阅读。PDF 会尽量附带页码定位，Excel 附带 sheet 名，PPT 附带 slide 序号。"
    ),
)


@server.tool(
    name="convert_to_markdown",
    title="Convert document to Markdown",
    description=(
        "将文档转换为 Markdown 文本。支持 PDF、Word(.docx)、Excel(.xlsx)、PowerPoint(.pptx) "
        "以及常见文本类格式（.txt/.md/.csv/.json/.html 等）。"
        "参数 uri_or_path 可为本地文件路径、file:// URI 或可访问的 http(s) URL。"
        "不支持旧版二进制 Office（.doc/.xls/.ppt）、加密文档与扫描件（无文本层）——"
        "此时返回以 `[convert_to_markdown failed] error_code=...` 开头的说明文本。"
    ),
)
def convert_to_markdown(uri_or_path: str) -> str:
    """把文档转换为 Markdown 文本并返回；失败时返回带 error_code 的说明文本。"""
    outcome = convert_source(uri_or_path)
    if not outcome.ok:
        return (
            f"[convert_to_markdown failed] error_code={outcome.error_code}; "
            f"message={outcome.message}"
        )
    if outcome.truncated:
        return outcome.text + "\n\n<!-- note: output truncated at 2 MiB -->\n"
    return outcome.text


@server.custom_route("/vfts/health", methods=["GET"])
async def vfts_health(_: Request) -> JSONResponse:
    """探活（无需 token）。"""
    return JSONResponse(
        {
            "ok": True,
            "version": CONVERTER_VERSION,
            "pid": os.getpid(),
            "port": RUNTIME["port"],
            "uptime_s": round(time.monotonic() - RUNTIME["started_monotonic"], 3),
            "state_path": RUNTIME["state_path"],
        }
    )


@server.custom_route("/vfts/convert", methods=["POST"])
async def vfts_convert(request: Request) -> JSONResponse:
    """vfts 内部转换入口（需 X-Chonk-Token）。"""
    token = request.headers.get(TOKEN_HEADER, "")
    expected = RUNTIME.get("token") or ""
    if not token or not expected or not secrets.compare_digest(token, expected):
        return JSONResponse(
            {
                "ok": False,
                "error_code": "unauthorized",
                "message": f"missing or invalid {TOKEN_HEADER} header",
            },
            status_code=401,
        )

    try:
        body = await request.json()
    except Exception:
        return JSONResponse(
            {"ok": False, "error_code": ERROR_PARSE_ERROR, "message": "invalid JSON body"},
            status_code=400,
        )
    if not isinstance(body, dict):
        return JSONResponse(
            {"ok": False, "error_code": ERROR_PARSE_ERROR, "message": "body must be a JSON object"},
            status_code=400,
        )

    path = body.get("path")
    if not isinstance(path, str) or not path.strip():
        return JSONResponse(
            {"ok": False, "error_code": ERROR_PARSE_ERROR, "message": "'path' is required (string)"},
            status_code=400,
        )

    # 路径根约束（防任意文件读）：`path` 的 realpath 必须落在允许根内。
    # 允许根 = 请求体 `root`（调用方指定，vfts 引擎传其 workdir）+ 环境白名单 MARKITDOWN_ALLOWED_ROOTS。
    root = body.get("root")
    if root is not None and not isinstance(root, str):
        return JSONResponse(
            {"ok": False, "error_code": ERROR_PARSE_ERROR, "message": "'root' must be a string"},
            status_code=400,
        )
    roots = allowed_convert_roots(root if isinstance(root, str) else None)
    if not roots:
        return JSONResponse(
            {
                "ok": False,
                "error_code": ERROR_PARSE_ERROR,
                "message": f"no allowed root: pass 'root' or set {ALLOWED_ROOTS_ENV}",
            },
            status_code=400,
        )
    if not path_within_roots(path, roots):
        return JSONResponse(
            {
                "ok": False,
                "error_code": ERROR_PARSE_ERROR,
                "message": f"path escapes allowed root(s): {path}",
            },
            status_code=400,
        )

    max_bytes = body.get("max_bytes", DEFAULT_MAX_INPUT_BYTES)
    if isinstance(max_bytes, bool) or not isinstance(max_bytes, int) or max_bytes <= 0:
        return JSONResponse(
            {
                "ok": False,
                "error_code": ERROR_PARSE_ERROR,
                "message": "'max_bytes' must be a positive integer",
            },
            status_code=400,
        )

    try:
        outcome = await run_in_convert_pool(convert_path, path, max_bytes)
    except asyncio.TimeoutError:
        return JSONResponse(
            {
                "ok": False,
                "error_code": ERROR_TIMEOUT,
                "message": f"conversion exceeded {DEFAULT_TIMEOUT_SEC:.0f}s",
            }
        )
    return JSONResponse(outcome.to_response())


# 注意：custom_route 在 streamable_http_app() 调用时被快照，故 app 必须在路由注册之后构建。
app = server.streamable_http_app(streamable_http_path="/mcp", host="127.0.0.1")


# ─────────────────────────────── 启动/退出 ───────────────────────────────


def _port_available(port: int) -> bool:
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.bind(("127.0.0.1", port))
        return True
    except OSError:
        return False


def choose_port(preferred: int, explicit: bool, logger: logging.Logger) -> int:
    """选定监听端口：优先 preferred；被占则向后试探（显式指定时直接报错）。"""
    if _port_available(preferred):
        return preferred
    if explicit:
        raise SystemExit(f"--port {preferred} is already in use")

    for candidate in range(preferred + 1, preferred + PORT_SCAN_SPAN + 1):
        if _port_available(candidate):
            logger.warning("port %d in use; using %d instead", preferred, candidate)
            return candidate

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    logger.warning("no free port near %d; using ephemeral port %d", preferred, port)
    return port


def _warmup() -> None:
    """后台预热 MarkItDown（首帧解析模型加载较慢），不阻塞健康探活。"""
    try:
        get_markitdown()
        _LOG.info("markitdown warmup done")
    except Exception as exc:  # pragma: no cover
        _LOG.warning("markitdown warmup failed: %s", exc)


def parse_args(argv: Optional[list[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="markitdown-mcp",
        description="ChonkPilot 文档转换服务（MCP /vfts 双入口，仅监听 127.0.0.1）",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=None,
        help=f"监听端口（默认 {DEFAULT_PORT}；默认端口被占用时自动另选，显式指定则报错）",
    )
    parser.add_argument(
        "--workers",
        type=int,
        default=DEFAULT_WORKERS,
        help=f"/vfts/convert 线程池大小（默认 {DEFAULT_WORKERS}）",
    )
    return parser.parse_args(argv)


def main(argv: Optional[list[str]] = None) -> int:
    import uvicorn  # 延迟导入，缩短 --help 响应

    args = parse_args(argv)
    logger = _LOG
    logger.info(
        "%s %s starting (pid=%d, python=%s)", SERVICE_NAME, CONVERTER_VERSION,
        os.getpid(), sys.version.split()[0],
    )

    try:
        state_path = resolve_state_path(logger)
    except Exception as exc:
        logger.error("cannot resolve state file location: %s", exc)
        return 2
    add_file_handler(logger, os.path.join(os.path.dirname(state_path), LOG_FILENAME))

    try:
        port = choose_port(args.port if args.port is not None else DEFAULT_PORT,
                           args.port is not None, logger)
    except SystemExit as exc:
        logger.error("%s", exc)
        return 2

    workers = max(1, int(args.workers))
    token = secrets.token_hex(16)  # 32 hex chars，仅落状态文件
    RUNTIME.update(
        {
            "port": port,
            "workers": workers,
            "token": token,
            "state_path": state_path,
            "started_monotonic": time.monotonic(),
        }
    )

    state = {
        "pid": os.getpid(),
        "port": port,
        "token": token,
        "version": CONVERTER_VERSION,
        "started_at": datetime.now(timezone.utc).astimezone().isoformat(timespec="seconds"),
        "exe_dir": exe_dir(),
    }
    try:
        write_state(state_path, state)
    except OSError as exc:
        logger.error("failed to write state file %s: %s", state_path, exc)
        return 2
    logger.info("state file written: %s", state_path)
    logger.info(
        "listening on http://127.0.0.1:%d (mcp=/mcp, health=/vfts/health, convert=/vfts/convert)",
        port,
    )

    threading.Thread(target=_warmup, name="warmup", daemon=True).start()

    config = uvicorn.Config(
        app,
        host="127.0.0.1",  # 只允许本机访问
        port=port,
        log_config=None,
        access_log=False,
        lifespan="on",
    )
    http_server = uvicorn.Server(config)
    try:
        http_server.run()
    finally:
        remove_state(state_path)
        logger.info("%s stopped", SERVICE_NAME)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

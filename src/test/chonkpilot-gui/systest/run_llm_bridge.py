# -*- coding: utf-8 -*-
"""L4 套件：LLM 桥接入口（BRIDGE-1 步骤 8 / BRIDGE-3 UDS-only）冒烟。

验证（真实 HTTP over UDS，不经 MQ）：
  B1 `/llm/<name>/v1/chat/completions` 经桥转发到本机 mock LLM，返回 200 + choices；
  B2 客户端自带的 `Authorization` 被桥**覆盖**（provider apiKey 空 → 出站不带鉴权头）；
  B3 未知 provider → 404（不静默回落）；
  B4 客户端自建 proxy（demo `llm_bridge_proxy.py`）loopback → UDS 字节转发端到端可用。

隔离：自起 GUI（临时 work-dir/data-dir/独立 HOME）+ 自管 mock LLM；桥 socket 经
`CHONK_LLM_UDS` 指向独立临时路径（避免与常驻 `llm.sock` 冲突）；结束即回收。
"""

import http.client
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import run_case  # noqa: E402

import harness as _h  # noqa: E402

TS = str(int(time.time()))[-6:]
PROVIDER = "mock-llm-" + TS

_REPO_ROOT = os.path.abspath(os.path.join(_h.HERE, "..", "..", "..", ".."))
PROXY_DEMO = os.path.join(_REPO_ROOT, "src", "initdata", "capability", "plugins",
                          "demo-python", "llm_bridge_proxy.py")


def _load_demo():
    """加载 demo proxy 模块（复用其跨平台 UDS 连接；CPython 在 Windows 无 socket.AF_UNIX）。"""
    spec = importlib.util.spec_from_file_location("ck_llm_bridge_proxy", PROXY_DEMO)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


_demo = _load_demo()

_ctx = {}


class _UnixHTTPConnection(http.client.HTTPConnection):
    """把 http.client 的 connect() 换成 UDS（协议仍是 HTTP/1.1）。"""

    def __init__(self, sock_path, timeout=20):
        super().__init__("localhost", timeout=timeout)
        self._sock_path = sock_path

    def connect(self):
        self.sock = _demo.connect_unix(self._sock_path)
        self.sock.settimeout(self.timeout)


def _post(sock_path, path, body):
    conn = _UnixHTTPConnection(sock_path)
    conn.request("POST", path, body, {
        "Content-Type": "application/json",
        # 客户端自报的鉴权头必须被桥覆盖/删除（B2）
        "Authorization": "Bearer CLIENT-SHOULD-NOT-PASS",
    })
    resp = conn.getresponse()
    data = resp.read()
    conn.close()
    return resp.status, data


def _post_tcp(port, path, body):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=20)
    conn.request("POST", path, body, {
        "Content-Type": "application/json",
        "Authorization": "Bearer CLIENT-SHOULD-NOT-PASS",
    })
    resp = conn.getresponse()
    data = resp.read()
    conn.close()
    return resp.status, data


def _wait_socket(path, timeout=20):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if os.path.exists(path):
            try:
                c = _demo.connect_unix(path)
                c.close()
                return True
            except OSError:
                pass
        time.sleep(0.2)
    return False


def case_b1_forward():
    status, data = _post(_ctx["uds"], "/llm/%s/v1/chat/completions" % PROVIDER,
                         json.dumps({"model": "mock-model", "stream": False,
                                     "messages": [{"role": "user", "content": "bridge-smoke"}]}))
    if status != 200:
        raise AssertionError("桥转发状态=%d body=%r" % (status, data[:400]))
    if b"choices" not in data:
        raise AssertionError("桥转发响应不含 choices: %r" % data[:400])


def case_b2_auth_overwritten():
    # mock 记录最近一次请求头；provider apiKey 为空 → 桥删除客户端 Auth 头
    url = "http://127.0.0.1:%d/last" % _ctx["mock"]
    with urllib.request.urlopen(url, timeout=10) as r:
        last = json.loads(r.read().decode("utf-8", "replace"))
    if last.get("authorization"):
        raise AssertionError("客户端 Authorization 未被覆盖：%r" % last.get("authorization"))


def case_b3_unknown_provider():
    status, _ = _post(_ctx["uds"], "/llm/no-such-provider-%s/v1/models" % TS, "{}")
    if status != 404:
        raise AssertionError("未知 provider 状态=%d（期望 404）" % status)


def case_b4_client_proxy():
    """客户端自建 loopback → UDS 字节代理（demo 脚本）端到端。"""
    if not os.path.isfile(PROXY_DEMO):
        raise AssertionError("demo proxy 缺失：%s" % PROXY_DEMO)
    port = _h.free_port()
    proc = subprocess.Popen(
        [sys.executable, PROXY_DEMO, "--listen", "127.0.0.1:%d" % port,
         "--socket", _ctx["uds"]],
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    try:
        if not _h.wait_port(port, 20):
            raise AssertionError("demo proxy 未在 127.0.0.1:%d 监听" % port)
        status, data = _post_tcp(port, "/llm/%s/v1/chat/completions" % PROVIDER,
                                 json.dumps({"model": "mock-model", "stream": False,
                                             "messages": [{"role": "user", "content": "proxy-smoke"}]}))
        if status != 200:
            raise AssertionError("经 proxy 转发状态=%d body=%r" % (status, data[:400]))
        if b"choices" not in data:
            raise AssertionError("经 proxy 响应不含 choices: %r" % data[:400])
    finally:
        try:
            proc.terminate()
            proc.wait(timeout=5)
        except Exception:
            try:
                proc.kill()
            except Exception:
                pass


def main():
    _m = _h.start_mock_llm(_h.free_port())
    _ctx["mock"] = _m.port
    sock_dir = tempfile.mkdtemp(prefix="cklb-")
    _ctx["uds"] = os.path.join(sock_dir, "llm.sock")
    old = os.environ.get("CHONK_LLM_UDS")
    os.environ["CHONK_LLM_UDS"] = _ctx["uds"]
    try:
        _ctx["gui"] = _h.acquire_gui(_h.free_port(),
                                     work_dir=_h.tmp_dir("ck-bridge-ws-"),
                                     data_dir=_h.tmp_dir("ck-bridge-dd-"),
                                     home=_h.tmp_home(), reuse=False)
        c = _ctx["gui"].client
        _h.suite_config_guard(c)
        entry = {"name": PROVIDER, "protocol": "openai", "apiKey": "", "model": "mock-model",
                 "baseUrl": "http://127.0.0.1:%d/v1" % _ctx["mock"]}
        c.req("data-user-config-save", {"data": {"llms": [entry], "defaultLLM": PROVIDER}})
        c.mq_emit("config-refresh")
        time.sleep(0.5)
        if not _wait_socket(_ctx["uds"], 20):
            raise AssertionError("llm-bridge 未在 %s 监听（CHONK_LLM_UDS 未生效？）" % _ctx["uds"])
        print("[env] gui=%d mock=%d uds=%s" % (_ctx["gui"].port, _ctx["mock"], _ctx["uds"]), flush=True)

        ok = total = 0
        for name, fn in [("B1 /llm/<name>/... 真实转发（UDS）", case_b1_forward),
                         ("B2 客户端鉴权头被覆盖", case_b2_auth_overwritten),
                         ("B3 未知 provider → 404", case_b3_unknown_provider),
                         ("B4 客户端 proxy loopback→UDS", case_b4_client_proxy)]:
            total += 1
            ok += run_case(name, fn)
        print("\nllm-bridge 桌面单体冒烟：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
        print("RESULT:", ok == total)
        return 0 if ok == total else 1
    finally:
        if old is None:
            os.environ.pop("CHONK_LLM_UDS", None)
        else:
            os.environ["CHONK_LLM_UDS"] = old


if __name__ == "__main__":
    sys.exit(main())

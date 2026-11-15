# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㊶：**LLM 桥接入口冒烟（UDS-only）**
（迁移自 run_llm_bridge B1–B4）。

该测试点需要独立实例：`CHONK_LLM_UDS` 必须指向独立 socket（不与常驻 `llm.sock` 冲突）。

* B1 `/llm/<provider>/v1/chat/completions` 经桥转发到本机 mock → 200 + choices；
* B2 客户端自带 `Authorization` 被桥覆盖（provider apiKey 空 → 出站无鉴权头）；
* B3 未知 provider → 404（不静默回落）；
* B4 demo proxy loopback→UDS 字节转发端到端可用。
"""

import http.client
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import time

import harness as _h
from .framework import CaseSkip, case

_TS = str(int(time.time()))[-6:]
PROVIDER = "e2e-bridge-" + _TS
_REPO_ROOT = os.path.abspath(os.path.join(_h.HERE, "..", "..", "..", ".."))
PROXY_DEMO = os.path.join(_REPO_ROOT, "src", "initdata", "capability", "plugins",
                          "demo-python", "llm_bridge_proxy.py")


def _demo_module():
    if not os.path.isfile(PROXY_DEMO):
        return None
    spec = importlib.util.spec_from_file_location("ck_llm_bridge_proxy_e2e", PROXY_DEMO)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


class _UnixHTTPConnection(http.client.HTTPConnection):
    """把 HTTPConnection.connect 换成 UDS（协议仍 HTTP/1.1）。"""

    def __init__(self, mod, sock_path, timeout=20):
        super().__init__("localhost", timeout=timeout)
        self._mod, self._sock_path = mod, sock_path

    def connect(self):
        self.sock = self._mod.connect_unix(self._sock_path)
        self.sock.settimeout(self.timeout)


def _unix_post(mod, sock_path, path, body):
    conn = _UnixHTTPConnection(mod, sock_path)
    conn.request("POST", path, body, {
        "Content-Type": "application/json",
        "Authorization": "Bearer CLIENT-SHOULD-NOT-PASS",
    })
    resp = conn.getresponse()
    data = resp.read()
    conn.close()
    return resp.status, data


def _tcp_post(port, path, body):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=20)
    conn.request("POST", path, body, {
        "Content-Type": "application/json",
        "Authorization": "Bearer CLIENT-SHOULD-NOT-PASS",
    })
    resp = conn.getresponse()
    data = resp.read()
    conn.close()
    return resp.status, data


@case("S-BRIDGE-01", "LLM", "UDS LLM 桥：转发/鉴权覆盖/未知 provider 404/客户端 proxy 端到端",
      stage="seeded", order=393, fp="LLM 桥接（run_llm_bridge B1–B4）",
      desc="自起实例并令 CHONK_LLM_UDS 指向独立 socket、usr provider 指向本机 mock："
           "POST /llm/<name>/v1/chat/completions → 200+choices；mock 收到请求无客户端 Authorization；"
           "未知 provider → 404；demo proxy 监听 TCP 后转发到 UDS → 200+choices。")
def s_bridge_01(env):
    mod = _demo_module()
    if mod is None:
        raise CaseSkip("demo llm_bridge_proxy.py 缺失 → 无法覆盖 B4")
    sock_dir = tempfile.mkdtemp(prefix="ck-e2e-bridge-")
    uds = os.path.join(sock_dir, "llm.sock")
    h = None
    proc = None
    try:
        h = _h.start_gui(port=_h.free_port(), work_dir=_h.tmp_dir("ck-e2e-bridge-ws-"),
                         home=_h.tmp_home(), ensure_spec=True,
                         extra_args=("-llm-base=http://127.0.0.1:%d/v1" % env.mock_port,
                                     "-llm-model=mock-model"),
                         env_overrides={"CHONK_LLM_UDS": uds})
        c = h.client
        c.wait_ready(120)
        entry = {"name": PROVIDER, "protocol": "openai", "apiKey": "", "model": "mock-model",
                 "baseUrl": "http://127.0.0.1:%d/v1" % env.mock_port}
        c.req("data-user-config-save", {"data": {"llms": [entry], "defaultLLM": PROVIDER}})
        c.mq_emit("config-refresh")
        conn_ok = False
        end = time.time() + 25
        while time.time() < end:
            if os.path.exists(uds):
                try:
                    s = mod.connect_unix(uds)
                    s.close()
                    conn_ok = True
                    break
                except OSError:
                    pass
            time.sleep(0.2)
        env.check("CHONK_LLM_UDS 生效（socket 可连接）", conn_ok, uds)
        if not conn_ok:
            return

        # B1 转发
        status, data = _unix_post(mod, uds, "/llm/%s/v1/chat/completions" % PROVIDER,
                                  json.dumps({"model": "mock-model", "stream": False,
                                              "messages": [{"role": "user", "content": "bridge-smoke"}]}))
        env.check("B1 桥转发 200 + choices",
                  status == 200 and b"choices" in data, {"status": status, "body": data[:120]})

        # B2 鉴权头覆盖
        try:
            import urllib.request
            with urllib.request.urlopen("http://127.0.0.1:%d/last" % env.mock_port,
                                        timeout=10) as r:
                last = json.loads(r.read().decode("utf-8", "replace"))
        except Exception as e:
            last = {"error": str(e)}
        env.check("B2 客户端 Authorization 被覆盖（出站无该头）",
                  not last.get("authorization"), last.get("authorization"))

        # B3 未知 provider → 404
        status3, _ = _unix_post(mod, uds, "/llm/no-such-provider-%s/v1/models" % _TS, "{}")
        env.check("B3 未知 provider → 404", status3 == 404, status3)

        # B4 demo proxy loopback → UDS
        port = _h.free_port()
        proc = subprocess.Popen([sys.executable, PROXY_DEMO,
                                 "--listen", "127.0.0.1:%d" % port,
                                 "--socket", uds],
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        env.check("B4 demo proxy 监听 TCP", bool(_h.wait_port(port, 20)), port)
        last_exc = None
        for _ in range(3):
            try:
                st4, d4 = _tcp_post(port, "/llm/%s/v1/chat/completions" % PROVIDER,
                                    json.dumps({"model": "mock-model", "stream": False,
                                                "messages": [{"role": "user", "content": "proxy-smoke"}]}))
                break
            except (http.client.RemoteDisconnected, ConnectionError, OSError) as e:
                last_exc = e
                st4, d4 = None, b""
                time.sleep(1.0)
        env.check("B4 经客户端 proxy 转发 200 + choices",
                  st4 == 200 and b"choices" in d4,
                  {"status": st4, "body": d4[:120], "err": str(last_exc or "")})
    finally:
        if proc is not None:
            try:
                proc.terminate()
                proc.wait(timeout=5)
            except Exception:
                try:
                    proc.kill()
                except Exception:
                    pass
        if h is not None:
            try:
                h.stop()
            except Exception:
                pass
        try:
            os.rmdir(sock_dir)
        except OSError:
            pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("桥接组（主实例）无前端错误", len(errs) == 0, errs[:3])
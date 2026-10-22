# -*- coding: utf-8 -*-
"""L4 套件：BRIDGE-1 步骤 8 —— 桌面单体（chonkpilot.exe）同开 llm-bridge 冒烟。

验证（真实 HTTP，不经 MQ）：
  B1 `/llm/<name>/v1/chat/completions` 经桥转发到本机 mock LLM，返回 200 + choices；
  B2 客户端自带的 `Authorization` 被桥**覆盖**（provider apiKey 空 → 出站不带鉴权头）；
  B3 未知 provider → 404（不静默回落）。

隔离：自起 GUI（临时 work-dir/data-dir/独立 HOME）+ 自管 mock LLM；桥端口经
`CHONK_LLM_BRIDGE_ADDR` 覆盖为动态端口（避免与常驻 5669 冲突）；结束即回收。
"""

import http.client
import json
import os
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import run_case  # noqa: E402

import harness as _h  # noqa: E402

TS = str(int(time.time()))[-6:]
PROVIDER = "mock-llm-" + TS

_ctx = {}


def _post(port, path, body):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=20)
    conn.request("POST", path, body, {
        "Content-Type": "application/json",
        # 客户端自报的鉴权头必须被桥覆盖/删除（B2）
        "Authorization": "Bearer CLIENT-SHOULD-NOT-PASS",
    })
    resp = conn.getresponse()
    data = resp.read()
    conn.close()
    return resp.status, data


def case_b1_forward():
    status, data = _post(_ctx["bridge"], "/llm/%s/v1/chat/completions" % PROVIDER,
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
    status, _ = _post(_ctx["bridge"], "/llm/no-such-provider-%s/v1/models" % TS, "{}")
    if status != 404:
        raise AssertionError("未知 provider 状态=%d（期望 404）" % status)


def main():
    _m = _h.start_mock_llm(_h.free_port())
    _ctx["mock"] = _m.port
    _ctx["bridge"] = _h.free_port()
    old = os.environ.get("CHONK_LLM_BRIDGE_ADDR")
    os.environ["CHONK_LLM_BRIDGE_ADDR"] = "127.0.0.1:%d" % _ctx["bridge"]
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
        if not _h.wait_port(_ctx["bridge"], 20):
            raise AssertionError("llm-bridge 未在 127.0.0.1:%d 监听（CHONK_LLM_BRIDGE_ADDR 未生效？）"
                                 % _ctx["bridge"])
        print("[env] gui=%d mock=%d bridge=%d" % (_ctx["gui"].port, _ctx["mock"], _ctx["bridge"]), flush=True)

        ok = total = 0
        for name, fn in [("B1 /llm/<name>/... 真实转发", case_b1_forward),
                         ("B2 客户端鉴权头被覆盖", case_b2_auth_overwritten),
                         ("B3 未知 provider → 404", case_b3_unknown_provider)]:
            total += 1
            ok += run_case(name, fn)
        print("\nllm-bridge 桌面单体冒烟：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
        print("RESULT:", ok == total)
        return 0 if ok == total else 1
    finally:
        if old is None:
            os.environ.pop("CHONK_LLM_BRIDGE_ADDR", None)
        else:
            os.environ["CHONK_LLM_BRIDGE_ADDR"] = old


if __name__ == "__main__":
    sys.exit(main())

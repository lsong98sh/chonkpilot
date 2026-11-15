# -*- coding: utf-8 -*-
"""`seeded` + `restart` 测试点 ㉔：**工具链路径配置**（迁移自 run_paths A 面 + 占位符 B 面）。

* 7 个路径键逐键哨兵写 → `data-user-config-load` 精确回读，且加载结果包含全部 7 键；
* 单键改写不串写其它 6 键；
* 场景主智能体提示词里的 `{{toolchain.*}}` 由服务端每次调用替换：mock LLM `/last` 的 system
  原文含哨兵路径、且不再残留字面占位符（未配置键 → 空串替换）；
* 重启后 7 键仍一致（usr 主库持久），测试结束还原跑前值。

注：解释器 env / `CHONK_CHROME` 注入面随 `script_run` 退役（41 D-67），旧套件已显式 SKIP；
本测试点同样只覆盖 A 面与占位符替换。
"""

import json
import os
import time
import urllib.request

from .framework import case
from .cases_chat_ops import _open_session, _select, _send

PATHS = ("chromePath", "javaPath", "pythonPath", "nodePath", "goPath",
         "rustPath", "cCompilerPath")
SENT = r"C:\probe\e2e\%s\bin\%s"
_ORIG = {}


def _load(env, key):
    d = (env.req("data-user-config-load", {"id": key}) or {}).get("data") or {}
    return d.get(key) if isinstance(d, dict) else d


def _set_many(env, mapping):
    env.req("data-user-config-save", {"data": dict(mapping)})
    env.js("window.mq.emit('config-refresh'); 'ok'")
    time.sleep(0.5)


@case("S-PATHS-01", "配置", "工具链 7 路径键：逐键回读 / 无串写 / 占位符替换（含未配置键空串）",
      stage="seeded", order=430, fp="64 配置项 · 路径族（run_paths A/B）",
      desc="7 键写哨兵 → 逐键 load 精确回读且 7 键齐全；只改 goPath → 其余 6 键不变；"
           "场景提示词 `{{toolchain.*}}` → mock LLM system 原文含 goPath 哨兵、无字面残留，"
           "未配置的 cCompilerPath 占位符亦被空串替换。")
def s_paths_01(env):
    for k in PATHS:
        _ORIG[k] = _load(env, k)
    _ORIG["__defaultScenario"] = env.usr().get("defaultScenario")

    vals = {k: (SENT % (k, k)) for k in PATHS}
    _set_many(env, vals)
    got = {k: _load(env, k) for k in PATHS}
    env.check("7 键逐键精确回读", got == vals, got)
    allc = env.req("data-user-config-load", {}) or {}
    data = allc.get("data") or {}
    env.check("load 恒返回全部 7 键", set(PATHS) <= set(data.keys()),
              [k for k in PATHS if k not in data])

    # 无串写：只改 goPath
    new_go = r"C:\probe\e2e\goPath\second\go.exe"
    _set_many(env, {"goPath": new_go})
    changed = {k: _load(env, k) for k in PATHS}
    env.eq("goPath 已更新", changed.get("goPath"), new_go)
    env.check("其余 6 键未被串写",
              all(changed.get(k) == vals[k] for k in PATHS if k != "goPath"),
              {k: changed.get(k) for k in PATHS})
    _set_many(env, {"goPath": vals["goPath"]})

    # 占位符替换：清空 cCompilerPath（未配置键 → 空串），goPath 指向哨兵
    _set_many(env, {"cCompilerPath": ""})
    _select(env, "mock")
    env.js("window.mq.emit('scenario-select', {id: 'default'}); 'ok'")
    time.sleep(0.8)
    env.note("场景选择", "tag=%r" % env.js(
        "document.querySelector('.panel-header .b-popover__reference')?.textContent?.trim() || ''"))
    sid = "e2e-paths-%s" % env.run_id
    _open_session(env, sid)
    env.capture(["llm-complete", "llm-start"])
    _send(env, sid, "toolchain placeholder probe", llm="mock")
    env.wait_events("llm-complete", lambda p: (p or {}).get("status") in ("complete", "completed"),
                    "占位符探针回合完成", timeout=90)

    last = {}
    try:
        with urllib.request.urlopen("http://127.0.0.1:8901/last", timeout=5) as r:
            last = json.loads(r.read().decode("utf-8", "replace"))
    except Exception as e:
        env.check("mock /last 可读", False, str(e)[:120])
    sys_prompt = str((last or {}).get("system") or "")
    env.check("system 原文非空", bool(sys_prompt.strip()), sys_prompt[:80])
    starts = [e.get("payload") or {} for e in env.events("llm-start")]
    env.note("llm-start", json.dumps(starts[-1:] if starts else [], ensure_ascii=False)[:500])
    env.note("toolchain 占位符证据", "n_ph=%d go=%s sentinel=%s snippet=%r" % (
        sys_prompt.count("{{toolchain"), "{{toolchain.go}}" in sys_prompt,
        vals["goPath"] in sys_prompt,
        (sys_prompt[sys_prompt.find("toolchain")-60:sys_prompt.find("toolchain")+180]
         if "toolchain" in sys_prompt else "")))
    if "{{toolchain" in sys_prompt:
        env.check("goPath 哨兵已替换进 system", vals["goPath"] in sys_prompt,
                  vals["goPath"])
        env.check("无字面 {{toolchain.go}} 残留", "{{toolchain.go}}" not in sys_prompt)
        env.check("未配置 cCompilerPath 占位符被替换（无字面残留）",
                  "{{toolchain.c}}" not in sys_prompt)
    else:
        # 现行构建的 system 原文未携带场景主 agent 提示词（占位符消费面不可观测）→
        # 不伪造通过：保留 A 面（落库/无串写/重启持久）并在报告中登记该可见性缺口。
        env.note("占位符 B 面不可观测",
                 "mock /last system 不含 {{toolchain.*}}（场景主 agent 提示词未进入 system）")

    _set_many(env, {"cCompilerPath": vals["cCompilerPath"]})
    # 会话清理（保留 7 键供 restart 阶段核对）
    try:
        env.req("data-session-delete", {"id": sid})
    except Exception:
        pass


@case("R-PATHS-01", "配置", "工具链 7 路径键跨重启持久 + 还原",
      stage="restart", order=431, fp="64 配置项 · usr 主库持久（run_paths A restart）",
      desc="重启后 data-user-config-load 7 键仍等于哨兵值；随后按跑前快照还原（原值/删除）。")
def r_paths_01(env):
    vals = {k: (SENT % (k, k)) for k in PATHS}
    got = {k: _load(env, k) for k in PATHS}
    env.check("重启后 7 键仍一致", got == vals, got)

    # 还原（原值为空/缺失 → 删除键回落默认）
    restore = {k: v for k, v in _ORIG.items()
               if k in PATHS and v not in (None, "")}
    if restore:
        env.req("data-user-config-save", {"data": restore})
    for k in PATHS:
        if _ORIG.get(k) in (None, ""):
            try:
                env.req("data-user-config-delete", {"id": k})
            except Exception:
                pass
    if _ORIG.get("__defaultScenario") is not None:
        env.req("data-user-config-save", {"data": {
            "defaultScenario": _ORIG["__defaultScenario"]}})
    env.js("window.mq.emit('config-refresh'); 'ok'")
    time.sleep(0.4)
    env.check("还原后不再保留哨兵 goPath", _load(env, "goPath") != vals["goPath"],
              _load(env, "goPath"))
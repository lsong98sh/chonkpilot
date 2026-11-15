# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉟：**记忆/上下文配置 A+B**（迁移自 run_memory_ctx A/B-1、A/B-2、A/B-3）。

* A/B-1 `memory.enabled`：false → system 无「以下是本机沉淀的项目/用户记忆文件」指引段且记忆目录
  不落盘；true → 指引段注入 + 8 个预置类别文件落盘；
* A/B-2 `memory.min-turn-tokens` + `memory.trigger`：A 回读；默认 `compress` + 小阈值 → 轮末
  **不**独立沉淀（无 data-memory-refresh、文件不变）；`every-turn` + 小阈值 → 轮末沉淀发生
  （文件变化 + data-memory-refresh(op=save)），且各类别文件只含本类哨兵/类别标记；
* A/B-3 `memory.category.<类别>`：关闭「项目概要」→ system 指引不含该类、仍含其它类；恢复。
"""

import json
import os
import time
import urllib.request

from .framework import case

GUIDE_MARK = "以下是本机沉淀的项目/用户记忆文件"
PROJ_CATS = ["项目概要", "共同库", "开发规范", "构建发布规则",
             "接口库", "测试规范", "典型参照", "用户决策"]
GATED = "项目概要"
CTRL = "共同库"


def _last(env):
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/last" % env.mock_port,
                                    timeout=5) as f:
            return json.loads(f.read().decode("utf-8")) or {}
    except Exception:
        return {}


def _reset(env):
    try:
        urllib.request.urlopen("http://127.0.0.1:%d/reset" % env.mock_port,
                               timeout=5).read()
    except Exception:
        pass


def _prj_save(env, k, v):
    env.req("data-prj-config-save", {"data": {"key": k, "value": v}})


def _prj_del(env, k):
    try:
        env.req("data-prj-config-delete", {"id": k})
    except Exception:
        pass


def _mem_dir(env):
    return env.path(".chonkpilot", "memory")


def _files(env):
    out = {}
    d = _mem_dir(env)
    if os.path.isdir(d):
        for n in sorted(os.listdir(d)):
            if n.endswith(".md"):
                try:
                    with open(os.path.join(d, n), encoding="utf-8", errors="replace") as f:
                        out[n] = f.read()
                except Exception:
                    pass
    return out


def _turn_system(env, q):
    _reset(env)
    env.run_turn(q)
    last = _last(env)
    return str(last.get("system") or ""), int(last.get("n_requests") or 0)


def _guide_cats(system):
    """system 指引段里被点名的类别（段内 `- <类>` 行）。"""
    return {c for c in PROJ_CATS if c in system}


@case("S-MEM-CTX-01", "记忆库", "记忆开关/阈值/触发模式/类别门控：system 指引 + 落盘 + 轮末沉淀",
      stage="seeded", order=337, fp="34 记忆与上下文 · run_memory_ctx A/B-1/2/3",
      desc="enabled=false → system 无记忆指引段 + 记忆目录不落盘；true → 指引段 + 8 预置文件；"
           "min-turn-tokens/trigger 回读；compress+小阈值轮末不沉淀；every-turn+小阈值轮末沉淀"
           "（文件变化 + data-memory-refresh + 本类哨兵/类别标记）；关闭「项目概要」→ 指引不含该类、"
           "「共同库」仍在。")
def s_mem_ctx_01(env):
    base_enabled = env.prj().get("memory.enabled")
    base_thr = env.prj().get("memory.min-turn-tokens")
    base_trig = env.prj().get("memory.trigger")
    base_gate = env.prj().get("memory.category." + GATED)
    try:
        # ── A/B-1 ──
        _prj_save(env, "memory.enabled", "false")
        _prj_save(env, "memory.min-turn-tokens", "999999")
        before_names = set(_files(env))
        env.eq("B-1 enabled=false 回读", env.prj().get("memory.enabled"), "false")
        sys_off, n_off = _turn_system(env, "记忆开关验证-关闭态")
        env.check("B-1 关闭态本轮真打到 mock", n_off >= 1, n_off)
        env.check("B-1 关闭态 system 无记忆指引段", GUIDE_MARK not in sys_off, sys_off[:160])
        if os.path.isdir(_mem_dir(env)):
            env.note("关闭态记忆目录", "目录可能由前序测试点创建；判据 = 关闭轮不新增文件")
        env.check("B-1 关闭态不新增预置文件（集合不变）",
                  set(_files(env)) == before_names,
                  {"before": sorted(before_names)[:6], "after": sorted(_files(env))[:6]})

        _prj_save(env, "memory.enabled", "true")
        env.eq("B-1 enabled=true 回读", env.prj().get("memory.enabled"), "true")
        sys_on, n_on = _turn_system(env, "记忆开关验证-开启态")
        env.check("B-1 开启态 system 注入记忆指引段",
                  GUIDE_MARK in sys_on, sys_on[:200])
        preset = _files(env)
        env.check("B-1 开启后 8 个预置类别文件落盘",
                  len(preset) >= 8, sorted(preset)[:10])

        # ── A/B-3 类别门控（system 指引） ──
        _prj_save(env, "memory.category." + GATED, "false")
        sys_gate, n_gate = _turn_system(env, "记忆类别门控-关闭项目概要")
        cats = _guide_cats(sys_gate)
        env.check("B-3 关闭类别不出现在 system 指引",
                  GUIDE_MARK in sys_gate and GATED not in cats and CTRL in cats,
                  sorted(cats))
        _prj_save(env, "memory.category." + GATED, "true")

        # ── A/B-2 A 面回读 ──
        _prj_save(env, "memory.min-turn-tokens", "999999")
        env.eq("B-2 min-turn-tokens 极大回读", env.prj().get("memory.min-turn-tokens"),
               "999999")
        _prj_save(env, "memory.trigger", "every-turn")
        env.eq("B-2 trigger=every-turn 回读", env.prj().get("memory.trigger"), "every-turn")
        _prj_save(env, "memory.trigger", "compress")
        env.eq("B-2 trigger=compress 回读", env.prj().get("memory.trigger"), "compress")

        # ── B-2 默认 compress + 小阈值：轮末不独立沉淀 ──
        _prj_save(env, "memory.min-turn-tokens", "1")
        before = dict(_files(env))
        env.js("window.__memRefresh=[];window.mq.on('data-memory-refresh',"
               "d=>{window.__memRefresh.push(d);});'ok'")
        env.run_turn("记忆阈值-极小-compress-本轮不得沉淀")
        time.sleep(3.0)
        refs = env.jsj("JSON.stringify(window.__memRefresh)") or []
        after = dict(_files(env))
        env.check("B-2 compress 轮末无 data-memory-refresh", not refs, len(refs))
        env.check("B-2 compress 轮末记忆文件不变", after == before,
                  [k for k in after if after.get(k) != before.get(k)])

        # ── B-2 every-turn + 小阈值：轮末沉淀（文件变化 + refresh + 本类标记） ──
        sents = {c: "E2E-MEMCTX-%02d-%s" % (i, env.run_id)
                 for i, c in enumerate(PROJ_CATS)}
        for c, s in sents.items():
            env.req("data-memory-save", {"data": {"category": c, "content": s}})
        time.sleep(0.5)
        before2 = dict(_files(env))
        _prj_save(env, "memory.trigger", "every-turn")
        env.js("window.__memRefresh=[];window.mq.on('data-memory-refresh',"
               "d=>{window.__memRefresh.push(d);});'ok'")
        env.run_turn("记忆阈值-极小-every-turn-本轮应当沉淀")
        try:
            got = env.wait(lambda: (lambda v: v if v else None)(
                env.jsj("JSON.stringify(window.__memRefresh)")), "轮末沉淀 data-memory-refresh",
                timeout=60)
            refresh = True
        except Exception:
            got = env.jsj("JSON.stringify(window.__memRefresh)") or []
            refresh = False
        env.check("B-2 every-turn 轮末收到 data-memory-refresh", refresh, len(got or []))

        def _changed():
            now = dict(_files(env))
            return [c for c in PROJ_CATS
                    if now.get(c + ".md") not in (None, before2.get(c + ".md"))]
        changed = env.wait(lambda: _changed() or None, "沉淀后类别文件变化", timeout=60)
        files = dict(_files(env))
        own = [c for c in changed
               if ("【类别】" + c) in files.get(c + ".md", "")
               and sents[c] in files.get(c + ".md", "")]
        cross = [c for c in PROJ_CATS
                 if any(c2 != c and sents[c2] in files.get(c + ".md", "")
                        for c2 in PROJ_CATS)]
        env.check("B-2 沉淀发生且类别→内容一致（本类标记+哨兵、无串味）",
                  bool(changed) and len(own) == len(changed) and not cross,
                  {"changed": changed, "own": own, "cross": cross})
    finally:
        for key, base in (("memory.enabled", base_enabled),
                          ("memory.min-turn-tokens", base_thr),
                          ("memory.trigger", base_trig),
                          ("memory.category." + GATED, base_gate)):
            try:
                if base is None:
                    _prj_del(env, key)
                else:
                    _prj_save(env, key, str(base))
            except Exception:
                pass
        for c in PROJ_CATS:
            try:
                env.req("data-memory-delete", {"data": {"category": c}})
            except Exception:
                pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("记忆上下文组无前端错误", len(errs) == 0, errs[:3])
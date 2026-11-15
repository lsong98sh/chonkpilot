# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㊳：**codegraph 索引范围/重建/清除 + 插件门控**
（迁移自 run_index_gate G1/G3/G4 的核心终效断言）。

* G1 插件 enabled=false → codegraph 8 工具 + vfts_query 从工具面移除；true → 恢复；
* G3 插件动作面 `save` 写 `plugins.codegraph.exts/.skip-dirs` → prj 回读 + `engine-status`
  的 exts/skipDirs + **落盘索引命中集合**（`codegraph_get_symbol_info` 逐文件哨兵探测）
  与夹具推导期望字面全等：exts=.go 收 2；追加 skip-dirs 后只剩 1；
* G4 `clear` → 状态回未初始化（indexedFiles=0）、`index.db` 删除；重新索引 → ready 且命中恢复。
"""

import json
import os
import time

from .framework import CaseSkip, case

CG_TOOLS = ["codegraph_symbol_search", "codegraph_get_symbol_info", "codegraph_callers",
            "codegraph_callees", "codegraph_get_dependency_graph",
            "codegraph_find_circular_deps", "codegraph_analyze_complexity",
            "codegraph_get_module_summary"]


def _api(env, action, payload=None, timeout=180):
    body = json.dumps(json.dumps(payload or {}))
    env.js("window.__ckApiRes=null;fetch('/plugins/codegraph/api/%s',{method:'POST',"
           "headers:{'Content-Type':'application/json','X-CK-Plugin-Id':'codegraph'},body:%s})"
           ".then(async r=>{window.__ckApiRes={status:r.status,body:await r.text()}})"
           ".catch(e=>{window.__ckApiRes={status:0,body:String(e)}});'ok'"
           % (action, body))
    def _res():
        r = env.jsj("JSON.stringify(window.__ckApiRes)")
        if not isinstance(r, dict):
            return None
        code, raw = r.get("status"), r.get("body") or ""
        try:
            val = json.loads(raw) if raw else None
        except Exception:
            val = raw
        if code != 200:
            raise AssertionError("插件动作 %s 失败（HTTP %s）：%s" % (action, code, str(val)[:300]))
        return {"val": val}
    return (env.wait(_res, "插件动作 %s" % action, timeout=timeout) or {}).get("val")


def _status(env):
    try:
        r = _api(env, "engine-status", {"workdir": env.workspace, "engine": "codegraph"},
                 timeout=60) or {}
    except Exception:
        return {}
    return (r.get("codegraph") or {}) if isinstance(r, dict) else {}


def _save(env, entries):
    full = {(k if k.startswith("plugins.") else "plugins.codegraph." + k): v
            for k, v in entries.items()}
    return _api(env, "save", {"workdir": env.workspace, "entries": full})


def _index(env):
    return _api(env, "tool.call", {"name": "codegraph_initialize",
                                   "args": {"workdir": env.workspace}})


def _tools(env):
    return {t.get("name") or "" for t in
            ((env.req("tools-list", {}) or {}).get("tools") or []) if isinstance(t, dict)}


def _has_symbol(env, rel, name):
    r = _api(env, "tool.call", {"name": "codegraph_get_symbol_info",
                                "args": {"workdir": env.workspace, "file": rel, "name": name}},
             timeout=90)
    return isinstance(r, dict) and r.get("status") is None and bool(r.get("matches"))


def _hit_set(env, files):
    return {rel for rel, name in files.items() if _has_symbol(env, rel, name)}


def _converge(env, want, desc, timeout=90):
    got = set()
    st = {}
    end = time.time() + timeout
    while time.time() < end:
        st = _status(env)
        try:
            got = _hit_set(env, want)
        except Exception:
            got = set()
        if st.get("state") == "ready" and got == set(want) \
                and int(st.get("indexedFiles") or -1) == len(want):
            return st, got
        time.sleep(1.0)
    env.check("%s：索引命中集合字面全等" % desc, got == set(want),
              {"got": sorted(got), "want": sorted(want), "status": st})
    return st, got


@case("S-IDX-01", "索引", "codegraph 插件门控/索引范围/重建/清除：工具面 + 状态 + 落盘命中集合",
      stage="seeded", order=397, fp="codegraph 插件（run_index_gate G1/G3/G4）",
      desc="插件关闭 → codegraph 8 工具 + vfts_query 移除、开启恢复；save exts=.go → prj 回读"
           "+ engine-status exts=[.go] + 落盘命中 2 个夹具 .go 字面全等；追加 skip-dirs → "
           "命中只剩 1（被排除目录消失）；clear → state 空/indexedFiles=0/index.db 删除；重建 → "
           "ready 且命中恢复。")
def s_idx_01(env):
    if not any(t.startswith("codegraph_") for t in _tools(env)):
        raise CaseSkip("codegraph 插件未运行（工具面无 codegraph_*）")
    rid = env.run_id.replace("-", "_")
    src = "e2e_idx_%s/src" % rid
    skipd = "e2e_idx_%s/skip" % rid
    files = {src + "/alpha.go": "E2EAlpha" + rid[-4:], skipd + "/delta.go": "E2EDelta" + rid[-4:]}
    for rel, fn in files.items():
        p = env.path(*rel.split("/"))
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            f.write("package e2e\n\nfunc %s() int { return 1 }\n" % fn)
    with open(env.path(src, "note.md"), "w", encoding="utf-8") as f:
        f.write("# not indexed\n")

    base = {k: env.prj().get("plugins.codegraph." + k)
            for k in ("enabled", "exts", "skip-dirs", "stack-gitignore")}
    try:
        # ── G1 门控（唯一门控 = 宿主插件启用开关 plugin-set-enabled）──
        env.req("plugin-set-enabled", {"id": "codegraph", "enabled": False})
        env.wait(lambda: True if not any(t.startswith("codegraph_") for t in _tools(env))
                 and "vfts_query" not in _tools(env) else None, "停用后工具面移除", timeout=120)
        env.check("G1 插件停用 → codegraph_* + vfts_query 移除",
                  not any(t.startswith("codegraph_") for t in _tools(env))
                  and "vfts_query" not in _tools(env))
        env.req("plugin-set-enabled", {"id": "codegraph", "enabled": True})
        env.wait(lambda: True if all(t in _tools(env) for t in CG_TOOLS) else None,
                 "启用后 codegraph 工具恢复", timeout=150)
        env.check("G1 插件开启 → 8 工具 + vfts_query 恢复",
                  all(t in _tools(env) for t in CG_TOOLS) and "vfts_query" in _tools(env),
                  sorted(t for t in _tools(env) if t.startswith("codegraph_")))

        # ── G3 A：exts=.go，skip-dirs 空 ──
        env.req("data-prj-config-save", {"data": {"key": "plugins.codegraph.enabled",
                                                  "value": "true"}})
        _save(env, {"exts": ".go", "skip-dirs": "", "stack-gitignore": "false"})
        env.eq("G3 A prj exts 回读", env.prj().get("plugins.codegraph.exts"), ".go")
        _index(env)
        st, got = _converge(env, files, "G3 A（exts=.go）")
        env.check("G3 A status.exts = ['.go']", st.get("exts") == [".go"], st.get("exts"))
        env.check("G3 A 两个夹具 .go 均命中索引", got == set(files), sorted(got))

        # ── G3 B：追加 skip-dirs ──
        _save(env, {"exts": ".go", "skip-dirs": skipd.split("/")[-1] if False else skipd})
        env.eq("G3 B prj skip-dirs 回读", env.prj().get("plugins.codegraph.skip-dirs"), skipd)
        _index(env)
        st2, got2 = _converge(env, {src + "/alpha.go": files[src + "/alpha.go"]},
                              "G3 B（skip-dirs 排除 skip/）")
        env.check("G3 B status.skipDirs 含 e2e 目录",
                  any(skipd in (x or "") or "skip" in (x or "")
                      for x in (st2.get("skipDirs") or [])), st2.get("skipDirs"))
        env.check("G3 B 被排除目录文件不在索引",
                  skipd + "/delta.go" not in got2 and src + "/alpha.go" in got2, sorted(got2))

        # ── G4 clear → 未初始化；重建 → 恢复 ──
        _api(env, "clear", {"workdir": env.workspace})
        db = env.path(".chonkpilot", "codegraph", "index.db")
        env.wait(lambda: True if _status(env).get("state") in ("", None)
                 and not os.path.exists(db) else None, "clear 后状态未初始化 + index.db 删除",
                 timeout=60)
        env.check("G4 clear → state 空 + indexedFiles=0 + index.db 删除",
                  _status(env).get("state") in ("", None)
                  and int(_status(env).get("indexedFiles") or 0) == 0
                  and not os.path.exists(db),
                  {"state": _status(env).get("state"),
                   "indexedFiles": _status(env).get("indexedFiles"), "db": os.path.exists(db)})
        _index(env)
        st3, got3 = _converge(env, {src + "/alpha.go": files[src + "/alpha.go"]},
                              "G4 重建（仍 skip-dirs）")
        env.check("G4 重建 → ready 且命中恢复",
                  st3.get("state") == "ready" and src + "/alpha.go" in got3, sorted(got3))
    finally:
        # 还原插件配置（保留 enabled=true）；触发一次后台重建（不阻塞收尾）
        try:
            rest = {k: v for k, v in base.items() if v is not None}
            rest["enabled"] = base.get("enabled") or "true"
            _save(env, rest)
            _index(env)
        except Exception:
            pass
        for rel, _ in list(files.items()) + [(src + "/note.md", None)]:
            p = env.path(*rel.split("/"))
            try:
                if os.path.isfile(p):
                    os.remove(p)
            except OSError:
                pass
        for d in (env.path(*src.split("/")), env.path(*skipd.split("/")),
                  env.path("e2e_idx_%s" % rid)):
            try:
                os.rmdir(d)
            except OSError:
                pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("codegraph 组无前端错误", len(errs) == 0, errs[:3])
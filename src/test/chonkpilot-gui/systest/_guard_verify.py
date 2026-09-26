# -*- coding: utf-8 -*-
"""临时驱动：逐套单跑 run_*.py，校验「跑前 / 跑后 usr 与 prj 库键集与关键值一致」（51 §6 配置快照-还原）。

用法：python _guard_verify.py [suite.py [suite.py ...]]   # 不给参数 = 默认清单
每套：_cfg_probe（非破坏性快照）→ 跑套件 → _cfg_probe → 逐键比对（`updated_at` 时间戳差异忽略）。
"""
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
WS = os.path.join(HERE, "ws")
PY = sys.executable

DEFAULT = [
    "run_ask_ui.py", "run_chat_ui.py", "run_config.py", "run_config_ui.py", "run_explore_kb.py",
    "run_filetree.py", "run_fp_extra_ui.py", "run_llm.py", "run_llm_config.py", "run_llm_errors.py",
    "run_preview_ui.py", "run_project.py", "run_project_cfg.py", "run_scenario_reload.py",
    "run_server_deps.py", "run_task_ui.py", "run_tool_async.py", "run_tools.py", "run_ui_regressions.py",
]


def norm(v):
    """归一：忽略 persist 落库时间戳（值语义未变）。"""
    if isinstance(v, dict):
        return {k: norm(x) for k, x in v.items() if k != "updated_at"}
    if isinstance(v, list):
        return [norm(x) for x in v]
    return v


def probe(tag):
    out = os.path.join(os.environ.get("TEMP", "."), "guard_%s.json" % tag)
    subprocess.run([PY, os.path.join(HERE, "_cfg_probe.py"), WS, out], cwd=HERE,
                   capture_output=True, text=True, encoding="utf-8", errors="replace")
    with open(out, encoding="utf-8") as f:
        return json.load(f)


def diff(a, b):
    out = []
    for layer in ("usr", "prj"):
        ka, kb = set(a[layer]), set(b[layer])
        for k in sorted(ka - kb):
            out.append("%s: 跑后有→跑后无 %s (原值 %s)" % (layer, k, json.dumps(a[layer][k], ensure_ascii=False)[:80]))
        for k in sorted(kb - ka):
            out.append("%s: 跑前无→跑后有 %s (=%s)" % (layer, k, json.dumps(b[layer][k], ensure_ascii=False)[:80]))
        for k in sorted(ka & kb):
            va, vb = norm(a[layer][k]), norm(b[layer][k])
            if va != vb:
                out.append("%s: 值变化 %s\n      跑前=%s\n      跑后=%s"
                           % (layer, k, json.dumps(va, ensure_ascii=False)[:200], json.dumps(vb, ensure_ascii=False)[:200]))
    return out


def main():
    suites = sys.argv[1:] or DEFAULT
    summary = []
    for name in suites:
        path = os.path.join(HERE, name)
        if not os.path.exists(path):
            print("[SKIP] %s 不存在" % name, flush=True)
            continue
        before = probe("before")
        r = subprocess.run([PY, path], cwd=HERE, capture_output=True, text=True,
                           encoding="utf-8", errors="replace")
        tail = [l for l in ((r.stdout or "") + (r.stderr or "")).splitlines()
                if "断言" in l or "通过" in l or "RESULT" in l or "[FAIL" in l or "[ERROR" in l]
        after = probe("after")
        d = diff(before, after)
        ok = (r.returncode == 0) and not d
        summary.append((name, r.returncode, len(d)))
        print("=== %s | 退出码=%d | 配置一致=%s" % (name, r.returncode, "是" if not d else "否(%d 处)" % len(d)), flush=True)
        for l in tail[-6:]:
            print("    " + l, flush=True)
        for l in d[:12]:
            print("    [DIFF] " + l, flush=True)

    print("\n===== 汇总（套件 | 退出码 | 配置差异数）=====", flush=True)
    for name, rc, nd in summary:
        print("  %-26s rc=%d  diff=%d  %s" % (name, rc, nd, "OK" if rc == 0 and nd == 0 else "!!"), flush=True)


if __name__ == "__main__":
    main()

# -*- coding: utf-8 -*-
"""配置读取探针（非破坏性）：导出指定 work-dir 的 prj 配置 + 真实 usr 主库配置快照。

用途：核对各套件「跑前 / 跑后 usr 与 prj 库键集与关键值一致」（51-FP与测试映射 §6-8）。

非破坏性做法：**不直接打开共享库**（GUI 启动/退出会隐式落盘 `window.*` 等），而是
把库文件复制到临时目录后离线读取：
  - prj：`<work-dir>/.chonkpilot/chonkpilot.db` → 复制为 `--data-dir/<tmp>/chonkpilot.db`
  - usr：`%USERPROFILE%/.chonkpilot/chonkpilot.db` → 复制到临时 HOME 下
  - work-dir：只复制普通文件（忽略 `.chonkpilot`），避免回落干扰

用法：
    python _cfg_probe.py [work_dir] [out.json]
默认 work_dir = systest/ws；不给 out.json 则打印 JSON。
"""
import json
import os
import shutil
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import harness as _h  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_WS = os.path.join(HERE, "ws")


def _copy_ws(src, dst):
    """复制 work-dir 的普通文件（跳过 .chonkpilot 库目录/索引）。"""
    for name in os.listdir(src):
        if name == ".chonkpilot":
            continue
        s, d = os.path.join(src, name), os.path.join(dst, name)
        if os.path.isdir(s):
            shutil.copytree(s, d, dirs_exist_ok=True)
        else:
            shutil.copy2(s, d)


def probe(work_dir=DEFAULT_WS):
    tmp_root = tempfile.mkdtemp(prefix="ck-probe-")
    tmp_ws = os.path.join(tmp_root, "ws")
    tmp_data = os.path.join(tmp_root, "data")
    tmp_home = os.path.join(tmp_root, "home")
    for d in (tmp_ws, tmp_data, os.path.join(tmp_home, ".chonkpilot")):
        os.makedirs(d, exist_ok=True)
    _copy_ws(work_dir, tmp_ws)
    prj_src = os.path.join(work_dir, ".chonkpilot", "chonkpilot.db")
    shutil.copy2(prj_src, os.path.join(tmp_data, "chonkpilot.db"))
    usr_src = os.path.join(os.path.expanduser("~"), ".chonkpilot", "chonkpilot.db")
    if os.path.isfile(usr_src):
        shutil.copy2(usr_src, os.path.join(tmp_home, ".chonkpilot", "chonkpilot.db"))

    h = _h.start_gui(port=_h.free_port(), work_dir=tmp_ws, data_dir=tmp_data,
                     home=tmp_home, ready_timeout=90)
    try:
        usr = (h.client.req("data-user-config-load", {}) or {}).get("data") or {}
        prj = (h.client.req("data-prj-config-list", {}) or {}).get("list") or {}
    finally:
        h.stop()
        shutil.rmtree(tmp_root, ignore_errors=True)
    return {"usr": usr, "prj": prj}


def main():
    work_dir = sys.argv[1] if len(sys.argv) > 1 else DEFAULT_WS
    out = sys.argv[2] if len(sys.argv) > 2 else None
    snap = probe(work_dir)
    usr, prj = snap["usr"], snap["prj"]
    print("PRJ keys(%d): %s" % (len(prj), ", ".join(sorted(prj))), flush=True)
    print("USR keys(%d): %s" % (len(usr), ", ".join(sorted(usr))), flush=True)
    for k in ("locale", "theme", "responseTimeout", "llms", "defaultLLM"):
        if k in usr:
            v = usr[k]
            print("USR[%s] = %s" % (k, json.dumps(v, ensure_ascii=False) if not isinstance(v, list)
                                    else "[%d 项] %s" % (len(v), [i.get("name") for i in v])), flush=True)
    for k in ("opened-files", "window.width", "window.height", "window.x", "window.y",
              "layout.filetreeWidth", "layout.chatWidth", "layout.sessiontreeWidth", "layout.taskHeight"):
        if k in prj:
            print("PRJ[%s] = %s" % (k, json.dumps(prj[k], ensure_ascii=False)), flush=True)
    if out:
        with open(out, "w", encoding="utf-8") as f:
            f.write(json.dumps(snap, ensure_ascii=False, sort_keys=True, indent=2))
        print("snapshot -> %s" % out, flush=True)


if __name__ == "__main__":
    main()

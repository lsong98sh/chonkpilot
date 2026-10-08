# -*- coding: utf-8 -*-
"""B · 语义/回环级推广 ⑦：**上传 / 搜索 结果语义回环**（落盘字节 ↔ 回执 ↔ 检索命中）。

覆盖（**拒绝"存在级"：断言落盘字节一致 + 回执语义 + 检索结果语义（源/类型/相对路径）**）：
  Q1 上传语义：`gui.upload{name,data,kind}`（data = base64 哨兵）→ 回执 `{file_id,name,path,url}`
     语义 = ① `file_id` 保留扩展名；② `name` == 传入基名；③ `path` 真实文件且**磁盘字节 == 上传字节**；
     ④ `url` == `/show/<path>`（可带 `?instance_id=`）。
  Q2 搜索命中语义：workdir 落一个唯一名文件 → `gui.search{query}` → 结果含该项且
     `source=="file"` / `matchType=="filename"` / `path` == **workdir 相对斜杠路径**（非绝对盘符）。
  Q3 搜索无命中语义：换唯一哨兵查询 → 结果**不含** 上面该项（查询确实过滤，非"恒返回全量"）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/HOME）；
夹具文件落在**临时 work-dir**、上传落盘在**独立数据根** → 随 tmp_dir 回收，零残留；
套件级快照-还原 `_h.suite_config_guard(c)`。**无需 mock LLM**（gui.upload / gui.search 均为本地桥能力）。

观测渠道（**均为 61-消息一览既有主题，零新增**）：§1 `gui.upload` · §1 `gui.search`。

运行：python run_sem_search_upload.py
"""
import base64
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

EXE = _h.resolve_gui_exe()
if not os.path.isfile(EXE):
    print("RESULT: True (SKIPPED: 未找到 GUI 产物 %s → 先构建 dist/desktop)" % EXE, flush=True)
    sys.exit(0)

TS = str(int(time.time()))[-6:]
WS = _h.tmp_dir("ck-semsearch-ws-")
DD = _h.tmp_dir("ck-semsearch-dd-")
HOME = _h.tmp_home()

UP_NAME = "sem-upload-%s.png" % TS
UP_BYTES = ("SEM-UPLOAD-BYTES-%s" % TS).encode("utf-8")
SEARCH_NAME = "sem-search-%s.txt" % TS      # workdir 内唯一名（供 file 源命中）
SEARCH_BODY = "SEM-SEARCH-CONTENT-%s\n" % TS
NO_HIT = "SEM-NOHIT-%s" % TS

# workdir 夹具：唯一名文件（gui.search 的 file 源扫 workdir）
with open(os.path.join(WS, SEARCH_NAME), "w", encoding="utf-8") as f:
    f.write(SEARCH_BODY)

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）" % (WS, DD, HOME, _g.port), flush=True)


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def sl(p):
    return p.replace("\\", "/")


def search(query):
    return (c.req("gui.search", {"query": query}) or {}).get("results") or []


def find_item(results, name):
    for it in results:
        if it.get("name") == name:
            return it
    return None


def case_q1_upload_semantics():
    """Q1：upload → 回执语义 + 落盘字节 == 上传字节。"""
    r = c.req("gui.upload", {"name": UP_NAME, "data": base64.b64encode(UP_BYTES).decode("ascii"),
                             "kind": "image"}) or {}
    fid, name, path, url = r.get("file_id") or "", r.get("name") or "", r.get("path") or "", r.get("url") or ""
    if not fid.lower().endswith(".png"):
        raise TestError("file_id 未保留扩展名 .png：%r" % fid)
    if name != UP_NAME:
        raise TestError("回执 name=%r（期望传入基名 %r）" % (name, UP_NAME))
    if not path or not os.path.isfile(path):
        raise TestError("回执 path 非真实文件：%r" % path)
    with open(path, "rb") as f:
        disk = f.read()
    if disk != UP_BYTES:
        raise TestError("落盘字节 != 上传字节：%r" % disk[:80])
    if url.split("?")[0] != "/show/" + sl(path):
        raise TestError("url 形态 != /show/<path>：%r（path=%r）" % (url, path))
    evi("Q1 上传语义", file_id=fid, name=name, bytes=len(disk), url_head=url.split("?")[0])


def case_q2_search_hit():
    """Q2：gui.search 命中 workdir 唯一名 → source=file / matchType=filename / 相对路径。"""
    results = search(SEARCH_NAME[:-4])  # 用不含扩展名的唯一名查询
    it = find_item(results, SEARCH_NAME)
    if it is None:
        raise TestError("搜索结果未含 %r：%r" % (SEARCH_NAME, results))
    if it.get("source") != "file":
        raise TestError("命中 source=%r（期望 file）" % it.get("source"))
    if it.get("matchType") != "filename":
        raise TestError("命中 matchType=%r（期望 filename）" % it.get("matchType"))
    if it.get("path") != SEARCH_NAME:
        raise TestError("命中 path=%r（期望 workdir 相对斜杠名 %r）" % (it.get("path"), SEARCH_NAME))
    if ":" in it.get("path", ""):
        raise TestError("命中 path 含盘符（应为相对路径）：%r" % it.get("path"))
    evi("Q2 搜索命中语义", query=SEARCH_NAME[:-4], path=it.get("path"),
        source=it.get("source"), matchType=it.get("matchType"))


def case_q3_search_no_hit():
    """Q3：换哨兵查询 → 结果不含该项（查询确实过滤）。"""
    results = search(NO_HIT)
    if find_item(results, SEARCH_NAME) is not None:
        raise TestError("无命中查询却返回了 %r：%r" % (SEARCH_NAME, results))
    evi("Q3 搜索无命中语义", query=NO_HIT, results=len(results))


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = gui.upload / gui.search（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("Q1 上传语义：回执语义 + 落盘字节一致", case_q1_upload_semantics),
        ("Q2 搜索命中语义：source=file/matchType=filename/相对路径", case_q2_search_hit),
        ("Q3 搜索无命中语义：查询确实过滤", case_q3_search_no_hit),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n上传/搜索结果语义回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

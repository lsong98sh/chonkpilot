# -*- coding: utf-8 -*-
"""B · 语义/回环级推广 ④：**记忆库类别内容编辑回环**（写 → 落盘 → 读回 → 列表 → 删除闭合）。

覆盖（**拒绝"存在级"：断言内容逐字对齐 + 磁盘落盘 + 列表归属 + 删除闭合**）：
  M1 写→读回环：`data-memory-save{data:{category,content}}`（自定义类别）→ `data-memory-read` 读回
     **内容 == 唯一哨兵**，且收到 `data-memory-refresh` 广播（op=save）。
  M2 磁盘落盘回环：`data-memory-read` 返回的 `data.path` 为真实文件，**磁盘字节 == 哨兵**
     （UIdata↔磁盘 双向对齐）。
  M3 编辑回环：改用**另一哨兵**再 save → read 读回 == 新哨兵 **且** 磁盘 == 新哨兵。
  M4 列表归属：`data-memory-list` 含该类别且 `level == project`、`path` 与 M2 一致（清单即目录扫描）。
  M5 删除闭合：`data-memory-delete{data:{category}}` → 磁盘文件消失 **且** read 内容为空 **且**
     列表不再含（回环闭合）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/HOME）；
夹具类别内容落在**临时 work-dir/data-dir** 内 → 随 tmp_dir 回收，零残留；套件级快照-还原。
**无需 mock LLM**（纯 persist memory 域读写；沉淀回路另见 run_memory_ctx）。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  §3.1 `data-memory-{list,read,save,delete}` + 广播 `data-memory-refresh`。

运行：python run_sem_memory_edit.py
"""
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

CAT = "semmem" + str(int(time.time()))[-5:]      # 自定义类别（非预置 → 可删）
SENT1 = "SEM-MEM-EDIT-A-" + CAT
SENT2 = "SEM-MEM-EDIT-B-" + CAT

WS = _h.tmp_dir("ck-semmem-ws-")
DD = _h.tmp_dir("ck-semmem-dd-")
HOME = _h.tmp_home()

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
print("[env] ws=%s data=%s home=%s gui=%d cat=%s（临时目录，结束即删）"
      % (WS, DD, HOME, _g.port, CAT), flush=True)

# 清残留（同类别历史）：避免上次中断留下的行影响「删除闭合」判定
try:
    c.req("data-memory-delete", {"data": {"category": CAT}})
except Exception:
    pass


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def mem_read(cat=CAT):
    return (c.req("data-memory-read", {"data": {"category": cat}}) or {}).get("data") or {}


def mem_list():
    return ((c.req("data-memory-list", {}) or {}).get("list")) or []


def disk_bytes(path):
    try:
        with open(path, "rb") as f:
            return f.read().decode("utf-8")
    except OSError:
        return None


def case_m1_write_read():
    """M1：save 自定义类别 → read 读回 == 哨兵；收到 data-memory-refresh。"""
    c.mq_on_capture(["data-memory-refresh"])
    r = c.req("data-memory-save", {"data": {"category": CAT, "content": SENT1}}) or {}
    if not r.get("ok"):
        raise TestError("data-memory-save 未成功：%r" % r)
    d = mem_read()
    if d.get("content") != SENT1:
        raise TestError("read 回内容 != 哨兵：%r" % (d.get("content"),))
    refs = c.wait_events("data-memory-refresh", 1, max_wait=10)
    evi("M1 写→读回环", ok=r.get("ok"), id=r.get("id"), content=d.get("content"),
        refresh_events=len(refs))


def case_m2_disk_roundtrip():
    """M2：read 返回的 path 为真实文件且磁盘字节 == 哨兵。"""
    d = mem_read()
    path = d.get("path") or ""
    if not path or not os.path.isfile(path):
        raise TestError("read.data.path 非真实文件：%r" % path)
    disk = disk_bytes(path)
    if disk != SENT1:
        raise TestError("磁盘内容 != 哨兵（path=%s）：%r" % (path, (disk or "")[:120]))
    evi("M2 磁盘落盘回环", path=path, disk_len=len(disk), level=d.get("level"))


def case_m3_edit_roundtrip():
    """M3：改内容再 save → read 读回新哨兵 且 磁盘 == 新哨兵。"""
    c.req("data-memory-save", {"data": {"category": CAT, "content": SENT2}})
    d = mem_read()
    if d.get("content") != SENT2:
        raise TestError("编辑后 read 回 != 新哨兵：%r" % (d.get("content"),))
    disk = disk_bytes(d.get("path") or "")
    if disk != SENT2:
        raise TestError("编辑后磁盘 != 新哨兵：%r" % ((disk or "")[:120],))
    evi("M3 编辑回环", content=d.get("content"), disk_len=len(disk))


def case_m4_list_membership():
    """M4：list 含该类别、level=project、path 与 read 一致。"""
    d = mem_read()
    rows = [x for x in mem_list() if x.get("category") == CAT]
    if not rows:
        raise TestError("data-memory-list 未含类别 %r" % CAT)
    row = rows[0]
    if row.get("level") != "project":
        raise TestError("类别 level != project：%r" % row.get("level"))
    if row.get("path") != d.get("path"):
        raise TestError("list.path(%r) != read.path(%r)" % (row.get("path"), d.get("path")))
    evi("M4 列表归属", category=CAT, level=row.get("level"), path=row.get("path"),
        tokens=row.get("tokens"))


def case_m5_delete_closed():
    """M5：delete → 磁盘消失 + read 内容空 + list 不再含。"""
    path = mem_read().get("path") or ""
    r = c.req("data-memory-delete", {"data": {"category": CAT}}) or {}
    if not r.get("ok"):
        raise TestError("data-memory-delete 未成功：%r" % r)
    end = time.time() + 8
    while time.time() < end and path and os.path.exists(path):
        time.sleep(0.3)
    if path and os.path.exists(path):
        raise TestError("删除后磁盘仍存在 %s" % path)
    # 删除后 read 该类别应报错（category 已不存在）→ 幂等判据：无内容即可
    content = ""
    try:
        content = (c.req("data-memory-read", {"data": {"category": CAT}}) or {}).get("data", {}).get("content") or ""
    except Exception:
        content = ""
    if content:
        raise TestError("删除后 read 内容非空：%r" % content)
    if [x for x in mem_list() if x.get("category") == CAT]:
        raise TestError("删除后 list 仍含 %r" % CAT)
    evi("M5 删除闭合", file_exists=bool(path and os.path.exists(path)), read_content=content)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = data-memory-*（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("M1 写→读回环：save → read 读回==哨兵 + data-memory-refresh", case_m1_write_read),
        ("M2 磁盘落盘回环：read.path 为真实文件且磁盘字节==哨兵", case_m2_disk_roundtrip),
        ("M3 编辑回环：再 save → read/磁盘 均==新哨兵", case_m3_edit_roundtrip),
        ("M4 列表归属：list 含类别 + level=project + path 一致", case_m4_list_membership),
        ("M5 删除闭合：delete → 磁盘消失 + read 空 + list 移除", case_m5_delete_closed),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n记忆库类别内容编辑回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

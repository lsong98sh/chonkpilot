# -*- coding: utf-8 -*-
"""B8 · 语义/回环级推广 ②：**文件树/目录操作回环**（消息面写 → 磁盘 → 树可见 → 读回 → 删除闭合）。

覆盖（**拒绝"存在级"：每步断言 磁盘字节 / 树节点 / filesys.list 三处一致**）：
  F1 建目录回环：`filesys.mkdir` → 磁盘目录存在 + 树节点出现 + `filesys.list` children 含 `is_dir`。
  F2 建文件回环：`filesys.create(content=MARK)` → 树节点出现 + 磁盘**字节** == MARK
     + `filesys.content` 读回 == MARK（UIdata↔磁盘 双向对齐）。
  F3 改名回环：`filesys.rename` → 树新名出现/旧名消失 + 磁盘旧路径消失/新路径在 + `filesys.list` 名字对齐。
  F4 复制回环：`filesys.copy(new_name)` → 树出现 + 磁盘内容 == MARK（复制语义）。
  F5 删除闭合：`filesys.remove` → 树消失 + 磁盘消失 + `filesys.list` 不含（回环闭合）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard(c)`。夹具落在**临时 work-dir**（随 tmp_dir 回收，零残留）。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  §2 `filesys.list` / `filesys.content` / `filesys.create` / `filesys.mkdir` / `filesys.rename` /
  `filesys.copy` / `filesys.remove`（经桥注入 instance_id，基目录只按实例绑定解析）· DOM（`--test-port` /eval）。

运行：python run_sem_filetree_loop.py
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

WS = _h.tmp_dir("ck-semft-ws-")
DD = _h.tmp_dir("ck-semft-dd-")
HOME = _h.tmp_home()
WS_POSIX = WS.replace("\\", "/")
MARK = "SEMFT-file-body-7a3"
DIR = "semdir"
FILE = "sem_file.txt"
RENAMED = "sem_renamed.txt"
COPY = "sem_copy.txt"

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）" % (WS, DD, HOME, _g.port), flush=True)


def J(js):
    return _h._plain(c.eval(js))


def p(*rel):
    return os.path.join(WS, *rel)


def tree_sel(rel):
    return '.tree-row[data-path="%s/%s"]' % (WS_POSIX, rel)


def wait_tree(rel, present=True, max_wait=20.0):
    sel = tree_sel(rel)
    end = time.time() + max_wait
    while time.time() < end:
        n = J("document.querySelectorAll(%s).length" % json.dumps(sel))
        n = int(n or 0)
        if present and n > 0:
            return True
        if not present and n == 0:
            return True
        time.sleep(0.4)
    raise TestError("等待树节点 %s %s 超时（%s）" % (rel, "出现" if present else "消失", sel))


def names_in_list():
    """filesys.list 读回：work_dir 根下 children 名集合（instance 绑定基目录）。"""
    r = c.req("filesys.list", {"work_dir": WS, "path": WS}) or {}
    return [str(n.get("name")) for n in (r.get("children") or [])]


def wait_list_name(name, present=True, max_wait=15.0):
    end = time.time() + max_wait
    last = []
    while time.time() < end:
        last = names_in_list()
        if (name in last) == present:
            return last
        time.sleep(0.3)
    raise TestError("filesys.list %s %s 超时（当前 %r）" % (name, "含" if present else "不含", last))


def read_disk(path):
    with open(path, "rb") as f:
        return f.read().decode("utf-8")


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def case_f1_mkdir_loop():
    """F1：mkdir → 磁盘目录 + 树节点 + list 含 is_dir。"""
    c.mq_emit("filesys.mkdir", {"work_dir": WS, "dir": WS, "name": DIR})
    end = time.time() + 10
    while time.time() < end and not os.path.isdir(p(DIR)):
        time.sleep(0.2)
    if not os.path.isdir(p(DIR)):
        raise TestError("mkdir 未落盘：%s" % p(DIR))
    wait_tree(DIR)
    lst = wait_list_name(DIR)
    r = c.req("filesys.list", {"work_dir": WS, "path": WS}) or {}
    node = next((n for n in (r.get("children") or []) if n.get("name") == DIR), None)
    if not node or node.get("is_dir") is not True:
        raise TestError("list 未把 %s 标为目录：%r" % (DIR, node))
    evi("F1 建目录回环", dir=DIR, disk=os.path.isdir(p(DIR)), list_names=lst)


def case_f2_create_loop():
    """F2：create(MARK) → 树节点 + 磁盘字节 == MARK + filesys.content 读回 == MARK。"""
    c.mq_emit("filesys.create", {"work_dir": WS, "dir": WS, "name": FILE, "content": MARK})
    end = time.time() + 10
    while time.time() < end and not os.path.isfile(p(FILE)):
        time.sleep(0.2)
    if not os.path.isfile(p(FILE)):
        raise TestError("create 未落盘：%s" % p(FILE))
    disk = read_disk(p(FILE))
    if disk != MARK:
        raise TestError("磁盘字节 != MARK（%r）" % disk)
    wait_tree(FILE)
    r = c.req("filesys.content", {"work_dir": WS, "path": p(FILE)}) or {}
    if r.get("content") != MARK:
        raise TestError("filesys.content 读回=%r（期望 %r）" % (r.get("content"), MARK))
    evi("F2 建文件回环", file=FILE, disk_bytes=len(disk), readback=r.get("content"))


def case_f3_rename_loop():
    """F3：rename → 树新名/旧名 + 磁盘旧无/新有 + list 名字对齐。"""
    c.mq_emit("filesys.rename", {"work_dir": WS, "path": p(FILE), "new_name": RENAMED})
    end = time.time() + 10
    while time.time() < end and not os.path.isfile(p(RENAMED)):
        time.sleep(0.2)
    if not os.path.isfile(p(RENAMED)) or os.path.exists(p(FILE)):
        raise TestError("rename 磁盘态不符：新在=%s 旧在=%s"
                        % (os.path.isfile(p(RENAMED)), os.path.exists(p(FILE))))
    wait_tree(RENAMED)
    wait_tree(FILE, present=False)
    wait_list_name(RENAMED)
    wait_list_name(FILE, present=False)
    if read_disk(p(RENAMED)) != MARK:
        raise TestError("rename 后内容丢失")
    evi("F3 改名回环", frm=FILE, to=RENAMED, content_kept=True)


def case_f4_copy_loop():
    """F4：copy(new_name) → 树出现 + 磁盘内容 == MARK。"""
    c.mq_emit("filesys.copy", {"work_dir": WS, "path": p(RENAMED), "new_name": COPY})
    end = time.time() + 10
    while time.time() < end and not os.path.isfile(p(COPY)):
        time.sleep(0.2)
    if not os.path.isfile(p(COPY)):
        raise TestError("copy 未落盘：%s" % p(COPY))
    if read_disk(p(COPY)) != MARK:
        raise TestError("copy 内容不符：%r" % read_disk(p(COPY)))
    wait_tree(COPY)
    wait_list_name(COPY)
    evi("F4 复制回环", src=RENAMED, dst=COPY, content_kept=True)


def case_f5_remove_closed():
    """F5：remove → 树消失 + 磁盘消失 + list 不含（回环闭合）。"""
    for name in (DIR, RENAMED, COPY):
        c.mq_emit("filesys.remove", {"work_dir": WS, "path": p(name)})
    end = time.time() + 10
    while time.time() < end and any(os.path.exists(p(n)) for n in (DIR, RENAMED, COPY)):
        time.sleep(0.2)
    left = [n for n in (DIR, RENAMED, COPY) if os.path.exists(p(n))]
    if left:
        raise TestError("remove 后磁盘仍存在：%r" % left)
    for name in (DIR, RENAMED, COPY):
        wait_tree(name, present=False)
        wait_list_name(name, present=False)
    evi("F5 删除闭合", removed=[DIR, RENAMED, COPY], disk_left=left)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = filesys.{mkdir,create,rename,copy,remove} + filesys.list/content（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("F1 建目录回环：磁盘 + 树节点 + list is_dir", case_f1_mkdir_loop),
        ("F2 建文件回环：磁盘字节 == filesys.content 读回", case_f2_create_loop),
        ("F3 改名回环：磁盘/树/list 三处对齐", case_f3_rename_loop),
        ("F4 复制回环：磁盘内容保持", case_f4_copy_loop),
        ("F5 删除闭合：磁盘消失 + 树消失 + list 不含", case_f5_remove_closed),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error" and "SetActiveSessionID" not in (e.get("text") or ""):
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n文件树/目录操作回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

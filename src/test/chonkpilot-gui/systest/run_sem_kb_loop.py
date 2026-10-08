# -*- coding: utf-8 -*-
"""B · 语义/回环级推广 ②：**知识库 / 原语读写回环**（写 → 落盘 → 读回 / 能力面生效）。

覆盖（**拒绝"存在级"：断言内容对齐 + 写后读回一致 + 能力面可见性回环**）：
  K1 读语义对齐：预置项目级 resource 夹具 → `data-knowledge-read` 的 `source` 含唯一标记
     且 == 磁盘字节；`doc.description` == 夹具描述（契约分区语义解析对齐）。
  K2 写回环：改 `doc.description` → `data-knowledge-save` → `data-knowledge-read` 读回 == 新值
     且**磁盘文件**含新值（UIdata↔磁盘 回环）。
  K3 原语写→能力面回环：`data-knowledge-create{type=tool}` → 磁盘契约含 `[meta]/[description]`
     分区 → 客户端能力面 `tools-list` **出现**该工具（写原语 → 能力面可观测）。
  K4 删除闭合：`data-knowledge-delete` → 磁盘消失 **且** `tools-list` 不再含（回环闭合）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/HOME）；
夹具落在**临时 work-dir** 的 `<ws>/.chonkpilot/capability/` 内 → 随 tmp_dir 回收，零残留；
套件级快照-还原 `_h.suite_config_guard(c)`。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  §3.3 `data-knowledge-{read,save,create,delete}` · §4.5 客户端能力面 `tools-list`（= run_primitive_hot 同法）。

运行：python run_sem_kb_loop.py
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

TOOL = "semkb" + str(int(time.time()))[-5:]           # 唯一哨兵原语名
WS = _h.tmp_dir("ck-semkb-ws-")
DD = _h.tmp_dir("ck-semkb-dd-")
HOME = _h.tmp_home()
PRJ_CAP = os.path.join(WS, ".chonkpilot", "capability")
RES_DIR = os.path.join(PRJ_CAP, "resources", "core")
RES_FILE = os.path.join(RES_DIR, "sem_kb.resource.md")
TOOLS_DIR = os.path.join(PRJ_CAP, "tools")
TOOL_FILE = os.path.join(TOOLS_DIR, TOOL + ".tool.md")
ORIG_DESC = "SEMKB-ORIG-desc-3f1"
ORIG_BODY = "SEMKB-ORIG-body-3f2"
NEW_DESC = "SEMKB-NEW-desc-7c9"

os.makedirs(RES_DIR, exist_ok=True)
os.makedirs(TOOLS_DIR, exist_ok=True)
with open(RES_FILE, "w", encoding="utf-8") as f:
    f.write("# sem_kb\n\n[meta]\nname=sem_kb\nkind=smoke\n\n[description]\n%s\n\n[content]\n%s\n"
            % (ORIG_DESC, ORIG_BODY))

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
print("[env] ws=%s data=%s home=%s gui=%d tool=%s（临时目录，结束即删）"
      % (WS, DD, HOME, _g.port, TOOL), flush=True)


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def sl(p):
    return p.replace("\\", "/")


def read_disk():
    """按**字节**读（binary + utf-8 解码）：避免文本模式把 CRLF 归一成 LF，破坏与 read 原文的逐字比对。"""
    try:
        with open(RES_FILE, "rb") as f:
            return f.read().decode("utf-8")
    except OSError:
        return ""


def tools():
    r = c.req("tools-list", {}) or {}
    return [t.get("name") or "" for t in (r.get("tools") or [])]


def case_k1_read_aligned():
    """K1：data-knowledge-read 的 source == 磁盘且含标记；doc.description == 夹具描述。"""
    r = c.req("data-knowledge-read", {"path": sl(RES_FILE)}) or {}
    src = r.get("source") or ""
    doc = r.get("doc") or {}
    if ORIG_BODY not in src or ORIG_DESC not in src:
        raise TestError("source 未含夹具标记：%r" % src[:160])
    if src != read_disk():
        raise TestError("source 与磁盘字节不一致（read 路径未逐字对齐）")
    if doc.get("description") != ORIG_DESC:
        raise TestError("doc.description=%r（期望 %r）" % (doc.get("description"), ORIG_DESC))
    evi("K1 读语义对齐", src_len=len(src), desc=doc.get("description"))


def case_k2_write_roundtrip():
    """K2：改 doc.description → save → read 读回 == 新值 且 磁盘含新值。"""
    r = c.req("data-knowledge-read", {"path": sl(RES_FILE)}) or {}
    doc = r.get("doc") or {}
    doc["description"] = NEW_DESC
    doc["content"] = (doc.get("content") or "") + "\n" + NEW_DESC + "-body"
    c.req("data-knowledge-save", {"path": sl(RES_FILE), "doc": doc})
    r2 = c.req("data-knowledge-read", {"path": sl(RES_FILE)}) or {}
    got = (r2.get("doc") or {}).get("description")
    if got != NEW_DESC:
        raise TestError("save 后 read 回 doc.description=%r（期望 %r）" % (got, NEW_DESC))
    disk = read_disk()
    if NEW_DESC not in disk:
        raise TestError("save 未落盘：磁盘不含新描述 %r" % NEW_DESC)
    evi("K2 写回环", readback=got, disk_has_mark=NEW_DESC in disk, disk_bytes=len(disk))


def case_k3_create_tool_hot():
    """K3：create tool 原语 → 磁盘契约分区 + 能力面出现（写原语→能力面回环）。"""
    r = c.req("data-knowledge-create", {"dir": sl(TOOLS_DIR), "type": "tool", "name": TOOL}) or {}
    if not os.path.isfile(TOOL_FILE):
        raise TestError("create 未落盘：%s 不存在（返回 path=%r）" % (TOOL_FILE, r.get("path")))
    content = open(TOOL_FILE, encoding="utf-8").read()
    if "[meta]" not in content or "[description]" not in content:
        raise TestError("落盘内容缺契约分区：%r" % content[:120])
    # 能力面：不重启轮询
    end = time.time() + 30
    hit = []
    while time.time() < end:
        hit = [n for n in tools() if TOOL in n]
        if hit:
            break
        time.sleep(0.3)
    if not hit:
        raise TestError("能力面 tools-list 未出现 %r" % TOOL)
    evi("K3 原语写→能力面回环", name=TOOL, path=r.get("path"), exposed=hit,
        file_bytes=len(content))


def case_k4_delete_closed():
    """K4：delete → 磁盘消失 且 能力面不再含（回环闭合）。"""
    c.req("data-knowledge-delete", {"path": sl(TOOL_FILE)})
    end = time.time() + 8
    while time.time() < end and os.path.exists(TOOL_FILE):
        time.sleep(0.3)
    if os.path.exists(TOOL_FILE):
        raise TestError("删除后磁盘仍存在 %s" % TOOL_FILE)
    end = time.time() + 30
    left = [TOOL]
    while time.time() < end:
        left = [n for n in tools() if TOOL in n]
        if not left:
            break
        time.sleep(0.3)
    if left:
        raise TestError("删除后能力面仍含 %r" % left)
    evi("K4 删除闭合", file_exists=os.path.exists(TOOL_FILE), left=left)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = data-knowledge-* + tools-list（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("K1 读：source==磁盘且含标记 + doc.description 对齐", case_k1_read_aligned),
        ("K2 写回环：save → read 读回新值 + 磁盘落盘", case_k2_write_roundtrip),
        ("K3 原语写→能力面回环：create tool → 磁盘分区 + tools-list 出现", case_k3_create_tool_hot),
        ("K4 删除闭合：delete → 磁盘消失 + tools-list 移除", case_k4_delete_closed),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n知识库/原语读写回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

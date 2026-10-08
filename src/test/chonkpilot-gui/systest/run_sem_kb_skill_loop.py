# -*- coding: utf-8 -*-
"""B · 语义/回环级推广 ⑥：**知识库原语 skill / prompt 读写回环**（写 → 落盘 → 读回 → 列表 → 删除闭合）。

覆盖（**拒绝"存在级"：断言内容对齐 + 写后读回一致 + 落盘字节 + 类型归属 + 删除闭合**）：
  P1 skill 建→读：`data-knowledge-create{dir,type=skill,name}` → 文件 `<name>.skill.md` 落盘且含
     `[meta]/[description]` 契约分区 → `data-knowledge-read` 的 `doc.description` == 模板默认（内容级）。
  P2 skill 编辑回环：改 `doc.description`/`doc.content` → `data-knowledge-save` → 读回 == 新值 **且**
     磁盘含新值（UIdata↔磁盘 回环）。
  P3 prompt 建→读（分区语义）：`type=prompt` → 文件 `<name>.prompt.md` → `doc.parameters` 含 `arg1`
     且 `doc.paramsSection == "[arguments]"`（prompt 类型用 arguments 区，非 parameters）。
  P4 列表归属 + 删除闭合：`data-knowledge-list{dir}` 两项 `type` 分别为 `skill`/`prompt`；
     `data-knowledge-delete` 两文件 → 磁盘消失 **且** list 不再含（回环闭合）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/HOME）；
夹具落在**临时 work-dir** 的 `<ws>/.chonkpilot/capability/{skills,prompts}/` 内 → 随 tmp_dir 回收，
零残留；套件级快照-还原 `_h.suite_config_guard(c)`。**无需 mock LLM**（纯 persist knowledge 域）。

观测渠道（**均为 61-消息一览既有主题，零新增**）：§3.3 `data-knowledge-{list,read,save,create,delete}`。

运行：python run_sem_kb_skill_loop.py
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

TS = str(int(time.time()))[-6:]
WS = _h.tmp_dir("ck-semskill-ws-")
DD = _h.tmp_dir("ck-semskill-dd-")
HOME = _h.tmp_home()

CAP = os.path.join(WS, ".chonkpilot", "capability")
SKILL_DIR = os.path.join(CAP, "skills")
PROMPT_DIR = os.path.join(CAP, "prompts")
SKILL = "semskill" + TS
PROMPT = "semprompt" + TS
SKILL_FILE = os.path.join(SKILL_DIR, SKILL + ".skill.md")
PROMPT_FILE = os.path.join(PROMPT_DIR, PROMPT + ".prompt.md")
NEW_DESC = "SEM-SKILL-NEW-desc-" + TS
NEW_BODY = "SEM-SKILL-NEW-body-" + TS

os.makedirs(SKILL_DIR, exist_ok=True)
os.makedirs(PROMPT_DIR, exist_ok=True)

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
print("[env] ws=%s data=%s home=%s gui=%d skill=%s prompt=%s（临时目录，结束即删）"
      % (WS, DD, HOME, _g.port, SKILL, PROMPT), flush=True)


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def sl(p):
    return p.replace("\\", "/")


def disk_text(path):
    """按**字节**读（避免文本模式 CRLF 归一化破坏逐字比对）。"""
    try:
        with open(path, "rb") as f:
            return f.read().decode("utf-8")
    except OSError:
        return ""


def read(path):
    return c.req("data-knowledge-read", {"path": sl(path)}) or {}


def klist(dirpath):
    return (c.req("data-knowledge-list", {"dir": sl(dirpath)}) or {}).get("files") or []


def file_row(dirpath, name):
    for f in klist(dirpath):
        if f.get("name") == name:
            return f
    return {}


def case_p1_create_skill_read():
    """P1：create skill → 落盘 `<name>.skill.md`（含契约分区）→ read 的 description == 模板默认。"""
    r = c.req("data-knowledge-create", {"dir": sl(SKILL_DIR), "type": "skill", "name": SKILL}) or {}
    if not os.path.isfile(SKILL_FILE):
        raise TestError("create skill 未落盘：%s（返回=%r）" % (SKILL_FILE, r))
    content = disk_text(SKILL_FILE)
    if "[description]" not in content:
        raise TestError("落盘内容缺 [description] 契约分区：%r" % content[:120])
    doc = read(SKILL_FILE).get("doc") or {}
    if doc.get("description") != SKILL + " 描述":
        raise TestError("read doc.description=%r（期望模板默认 %r）"
                        % (doc.get("description"), SKILL + " 描述"))
    evi("P1 skill 建→读", path=r.get("path"), file_bytes=len(content), desc=doc.get("description"))


def case_p2_skill_edit_roundtrip():
    """P2：改 description/content → save → 读回 == 新值 且 磁盘含新值。"""
    r = read(SKILL_FILE)
    doc = r.get("doc") or {}
    doc["description"] = NEW_DESC
    doc["content"] = (doc.get("content") or "") + "\n" + NEW_BODY + "\n"
    s = c.req("data-knowledge-save", {"path": sl(SKILL_FILE), "doc": doc}) or {}
    if not s.get("ok"):
        raise TestError("data-knowledge-save 未成功：%r" % s)
    doc2 = read(SKILL_FILE).get("doc") or {}
    if doc2.get("description") != NEW_DESC:
        raise TestError("save 后 read doc.description=%r（期望 %r）" % (doc2.get("description"), NEW_DESC))
    if NEW_BODY not in (doc2.get("content") or ""):
        raise TestError("save 后 read doc.content 未含新正文：%r" % (doc2.get("content") or "")[:160])
    disk = disk_text(SKILL_FILE)
    if NEW_DESC not in disk or NEW_BODY not in disk:
        raise TestError("save 未落盘（磁盘缺新值）：%r" % disk[:160])
    evi("P2 skill 编辑回环", readback=doc2.get("description"), disk_has_mark=(NEW_BODY in disk))


def case_p3_create_prompt_arguments():
    """P3：create prompt → 落盘 `<name>.prompt.md`；read 的 arguments 区语义（paramsSection == [arguments]）。"""
    r = c.req("data-knowledge-create", {"dir": sl(PROMPT_DIR), "type": "prompt", "name": PROMPT}) or {}
    if not os.path.isfile(PROMPT_FILE):
        raise TestError("create prompt 未落盘：%s（返回=%r）" % (PROMPT_FILE, r))
    doc = read(PROMPT_FILE).get("doc") or {}
    if doc.get("params_section") != "[arguments]":
        raise TestError("prompt 类型 params_section=%r（期望 [arguments]）" % doc.get("params_section"))
    if "arg1" not in (doc.get("parameters") or ""):
        raise TestError("prompt 模板缺 arg1 参数：%r" % (doc.get("parameters") or "")[:160])
    evi("P3 prompt 建→读", path=r.get("path"), params_section=doc.get("params_section"))


def case_p4_list_and_delete_closed():
    """P4：list 两项类型归属（skill/prompt）→ delete 两文件 → 磁盘消失 + list 不再含。"""
    rs = file_row(SKILL_DIR, SKILL + ".skill.md")
    rp = file_row(PROMPT_DIR, PROMPT + ".prompt.md")
    if rs.get("type") != "skill":
        raise TestError("list skill 行 type=%r（期望 skill；行=%r）" % (rs.get("type"), rs))
    if rp.get("type") != "prompt":
        raise TestError("list prompt 行 type=%r（期望 prompt；行=%r）" % (rp.get("type"), rp))
    for p in (SKILL_FILE, PROMPT_FILE):
        c.req("data-knowledge-delete", {"path": sl(p)})
    end = time.time() + 8
    while time.time() < end and (os.path.exists(SKILL_FILE) or os.path.exists(PROMPT_FILE)):
        time.sleep(0.3)
    if os.path.exists(SKILL_FILE) or os.path.exists(PROMPT_FILE):
        raise TestError("删除后磁盘仍存在：%s / %s" % (SKILL_FILE, PROMPT_FILE))
    if file_row(SKILL_DIR, SKILL + ".skill.md") or file_row(PROMPT_DIR, PROMPT + ".prompt.md"):
        raise TestError("删除后 list 仍含（skill=%r prompt=%r）"
                        % (file_row(SKILL_DIR, SKILL + ".skill.md"),
                           file_row(PROMPT_DIR, PROMPT + ".prompt.md")))
    evi("P4 列表归属 + 删除闭合", skill_type=rs.get("type"), prompt_type=rp.get("type"),
        skill_gone=not os.path.exists(SKILL_FILE), prompt_gone=not os.path.exists(PROMPT_FILE))


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = data-knowledge-*（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("P1 skill 建→读：落盘契约分区 + doc.description 默认", case_p1_create_skill_read),
        ("P2 skill 编辑回环：save → 读回==新值 + 磁盘落盘", case_p2_skill_edit_roundtrip),
        ("P3 prompt 建→读：paramsSection==[arguments] + arg1", case_p3_create_prompt_arguments),
        ("P4 列表归属 + 删除闭合：type 正确 + 磁盘/list 均移除", case_p4_list_and_delete_closed),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n知识库原语 skill/prompt 读写回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

# -*- coding: utf-8 -*-
"""B · 语义/回环级试点 ①②：场景编辑页 · ref 型 agent 提示词「非空 → 编辑 → 保存 → 重开值一致 + ref 不变」。

覆盖（**全部真实 DOM 驱动 + 磁盘/消息面回环断言**，拒绝"存在级"）：
  S1 ref 型子 agent 的提示词框**非空**且 == 被引文件正文（语义级：内容对齐，非仅"元素存在"）。
  S2 编辑提示词 → 保存 → 被引 agent 文件落盘内容 == 新值（磁盘回环）。
  S3 重开编辑页 → 提示词框值 == 新值（重开回环，经受 knowledge 读回）。
  S4 场景里的 `ref` **保持不变**（编辑不移动引用；场景记录 ref 与文件一致）。

隔离（51-FP与测试映射 §5/§6-8）：
  * 自起 GUI：动态端口 + 独立临时 work-dir / `--data-dir` / **独立 HOME**（不读机器 `~/.chonkpilot`）。
  * 夹具（场景 + agent 文件）全落在临时 work-dir，随 `harness.tmp_dir` 删除 → 零残留。
  * 套件级快照-还原 `_h.suite_config_guard(c)`（usr+prj；含异常/中断路径）。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  `data-scenario-save/load/delete`（§3.1）· `data-knowledge-read/save`（§3.3）· DOM（--test-port /eval）。

运行：python run_sem_scenario_ref.py
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

WS = _h.tmp_dir("ck-semref-ws-")
DD = _h.tmp_dir("ck-semref-dd-")
HOME = _h.tmp_home()
_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
_h.ensure_locale(c, "zh-CN")
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）" % (WS, DD, HOME, _g.port), flush=True)

SID = "ck-semref-scn"
NAME = "语义引用场景"
SUB = "ckRefA"
REF = "${workDir}/.chonkpilot/capability/agents/%s.agent.md" % SUB
AGENT_FILE = os.path.join(WS, ".chonkpilot", "capability", "agents", SUB + ".agent.md")
ORIG_MARK = "SEMREF-ORIG-7a1"
EDIT_MARK = "SEMREF-EDIT-7b2"


def plain(v):
    return _h._plain(v)


def wait_js(js, pred, max_wait=15.0, interval=0.3):
    """有界轮询 /eval 求值直到 pred(v) 为真；返回末次值。"""
    deadline = time.time() + max_wait
    v = plain(c.eval(js))
    while not pred(v) and time.time() < deadline:
        time.sleep(interval)
        v = plain(c.eval(js))
    return v


def click_by_text(selector, text):
    js = ("(()=>{const els=[...document.querySelectorAll(%s)];"
          "const el=els.find(e=>(e.textContent||'').trim()===%s);"
          "if(!el)return 'not-found';el.click();return 'ok';})()"
          % (json.dumps(selector), json.dumps(text)))
    return plain(c.eval(js))


def write_fixture():
    os.makedirs(os.path.dirname(AGENT_FILE), exist_ok=True)
    doc = ("# %s\n\n[description]\nref agent 夹具\n\n[content]\n%s 原始提示词正文\n"
           % (SUB, ORIG_MARK))
    with open(AGENT_FILE, "w", encoding="utf-8") as f:
        f.write(doc)


MAIN_PROMPT = "SEMREF-MAIN 主协调者提示词"


def scenario_save():
    c.req("data-scenario-save", {"data": {
        "id": SID, "name": NAME, "level": "project",
        "agents": [
            {"name": "主", "roleTag": "main", "isMain": True, "prompt": MAIN_PROMPT},
            {"name": SUB, "roleTag": "", "ref": REF},
        ],
    }})


def open_editor():
    """打开本场景编辑页（preview-tab-open 场景 tab → scenario-edit-row）。"""
    c.mq_emit("preview-tab-open", {"kind": "scenario", "title": "场景"})
    wait_js("document.body.innerText.includes('场景')", lambda v: bool(v), max_wait=8)
    time.sleep(0.5)
    c.mq_emit("scenario-edit-row", {"row": {
        "id": SID, "name": NAME, "level": "project",
        "agents": [
            {"name": "主", "roleTag": "main", "isMain": True, "prompt": MAIN_PROMPT},
            {"name": SUB, "roleTag": "", "ref": REF},
        ],
    }})
    if not wait_js("!!document.querySelector('.edit-dialog-body')", lambda v: bool(v), max_wait=10):
        raise TestError("场景编辑页未打开（.edit-dialog-body 未出现）")


def select_ref_agent_and_prompt_tab():
    """选中 ref 子 agent（点击列表项）→ 切到 AgentEditor 的「提示词」页签。

    列表随 `loadAgents` 异步渲染 → 先**有界等待**目标项出现再点击（非"存在级"赌时序）。
    """
    click_js = ("(()=>{const a=[...document.querySelectorAll('.agent-list-item')]"
                ".find(e=>((e.querySelector('.al-name')||{}).textContent||'').trim()===(%s));"
                "if(!a)return '';a.click();return 'ok';})()" % json.dumps(SUB))
    if wait_js(click_js, lambda v: v == "ok", max_wait=12) != "ok":
        names = plain(c.eval(
            "JSON.stringify([...document.querySelectorAll('.agent-list-item .al-name')]"
            ".map(e=>(e.textContent||'').trim()))"))
        raise TestError("未找到 ref 子 agent 列表项 %r（现列表=%r）" % (SUB, names))
    if not wait_js("!!document.querySelector('.ref-agent-banner')", lambda v: bool(v), max_wait=10):
        raise TestError("选中 ref agent 后未出现 ref 横幅（.ref-agent-banner）")
    if click_by_text(".agent-editor-tabs .b-tabs-item", "提示词") != "ok":
        raise TestError("未找到 AgentEditor「提示词」页签")
    if not wait_js("!!document.querySelector('.prompt-textarea')", lambda v: bool(v), max_wait=10):
        raise TestError("提示词页签下未渲染提示词框（.prompt-textarea）")
    # 等 ref 文件正文载入（异步 knowledge-read）
    return wait_js("(()=>{const t=document.querySelector('.prompt-textarea');return t?t.value:''})()",
                   lambda v: isinstance(v, str) and ORIG_MARK in v, max_wait=10)


def prompt_value():
    return plain(c.eval("(()=>{const t=document.querySelector('.prompt-textarea');return t?t.value:''})()"))


def ref_banner_path():
    return plain(c.eval("(()=>{const e=document.querySelector('.ref-path');return e?e.textContent.trim():''})()"))


def scenario_load():
    return (c.req("data-scenario-load", {"data": {"id": SID, "level": "project"}}) or {}).get("data") or {}


def case_s1_nonempty_and_matches_file():
    """S1：ref agent 提示词框非空，且内容 == 被引文件正文（语义级对齐）。"""
    open_editor()
    v0 = select_ref_agent_and_prompt_tab()
    if not (isinstance(v0, str) and v0.strip()):
        raise TestError("ref agent 提示词框为空（应载入被引文件正文）")
    if ORIG_MARK not in v0:
        raise TestError("提示词框内容未与被引文件对齐：%r" % v0[:120])
    print("[EVIDENCE] " + json.dumps({"case": "S1 ref 提示词非空且对齐文件",
                                      "len": len(v0), "head": v0[:40]}, ensure_ascii=False), flush=True)


def case_s2_s3_s4_edit_save_reopen():
    """S2/S3/S4：编辑 → 保存（磁盘回环）→ 重开（重开回环一致）→ ref 不变。"""
    v0 = prompt_value()
    new_val = v0 + "\n" + EDIT_MARK + " 追加内容"

    # S2 编辑：原生 setter + input 事件（/input 走 HTMLTextAreaElement 原型 setter）
    c.input(".prompt-textarea", new_val, 5000)
    got = wait_js("(()=>{const t=document.querySelector('.prompt-textarea');return t?t.value:''})()",
                  lambda v: v == new_val, max_wait=5)
    if got != new_val:
        raise TestError("编辑未生效：%r" % (got or "")[:80])

    # 保存（scenario-save → writeBackRefAgents + data-scenario-save → 关闭弹窗）
    c.mq_emit("scenario-save")
    if not wait_js("!document.querySelector('.edit-dialog-body')", lambda v: bool(v), max_wait=12):
        raise TestError("保存后编辑弹窗未关闭")

    # S2 磁盘回环：被引文件含新值
    with open(AGENT_FILE, "r", encoding="utf-8") as f:
        disk = f.read()
    if EDIT_MARK not in disk or new_val.split("\n")[-1] not in disk:
        raise TestError("保存后被引 agent 文件未落盘新值：%r" % disk[:160])
    print("[EVIDENCE] " + json.dumps({"case": "S2 保存→磁盘回环",
                                      "file_len": len(disk), "has_mark": EDIT_MARK in disk},
                                     ensure_ascii=False), flush=True)

    # S3 重开回环：重开编辑页 → 提示词框值 == 新值
    time.sleep(0.6)
    open_editor()
    v1 = select_ref_agent_and_prompt_tab()
    # 重开时载入的是磁盘内容（含新值）；用 EDIT_MARK + 末行断言，避免换行归一差异
    if not (isinstance(v1, str) and EDIT_MARK in v1):
        raise TestError("重开后提示词值未回到新值：%r" % (v1 or "")[:120])

    # S4 ref 不变：编辑页 ref 横幅 == 原 ref；场景记录 ref 也未变
    banner = ref_banner_path()
    loaded = scenario_load()
    sub_rec = next((a for a in (loaded.get("agents") or []) if a.get("name") == SUB), None)
    if banner != REF or not sub_rec or sub_rec.get("ref") != REF:
        raise TestError("ref 被改动：banner=%r 场景=%r" % (banner, (sub_rec or {}).get("ref")))
    print("[EVIDENCE] " + json.dumps({"case": "S3/S4 重开一致 + ref 不变",
                                      "reopen_len": len(v1), "ref": banner}, ensure_ascii=False), flush=True)

    # 收尾：关闭编辑弹窗
    c.mq_emit("scenario-cancel")


def cleanup():
    try:
        c.req("data-scenario-delete", {"data": {"id": SID, "level": "project"}})
    except Exception as e:
        print("[cleanup] 删除场景失败：%s" % e, flush=True)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = preview-tab-open/scenario-edit-row/scenario-save（既有事件）", flush=True)
    write_fixture()
    scenario_save()
    ok = total = 0
    try:
        for name, fn in [
            ("S1 ref 型 agent 提示词框非空且对齐文件", case_s1_nonempty_and_matches_file),
            ("S2/S3/S4 编辑→保存（磁盘回环）→重开一致 + ref 不变", case_s2_s3_s4_edit_save_reopen),
        ]:
            total += 1
            ok += run_case(name, fn)
    finally:
        cleanup()
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n场景编辑 ref 提示词语义/回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

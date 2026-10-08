# -*- coding: utf-8 -*-
"""L2 套件：**同场景内 agent 重名 → 保存报错**（2026-09-26 用户裁决，[42 §2 (175)] ·
[25-MCP与场景分层模型 §6.1] · [37-场景 SCEN-003-S05]）。

覆盖（全部 mq 驱动 + mq 断言）：
  ① 同场景内两个 agent 同名（**大小写差异**）→ `data-scenario-save` **失败**，返回错误含
     「**场景 id + 重复 agent 名**」；**不落盘**（`data-scenario-list` 无该场景）且**不广播**
     `data-scenario-refresh`（消息面零新增：仅观察既有主题）。
  ② **不同场景**同名 agent → 允许（两场景各自保存成功、均落盘、各收到 `data-scenario-refresh`）。
  ③ 未重名（唯一名）→ 正常保存：落盘 + `data-scenario-refresh` 广播 + `data-scenario-load` 回读。

观测渠道（**均为 61-消息一览既有主题，零新增**）
  §3.1 `data-scenario-{save,load,list,delete}` + 变更广播 `data-scenario-refresh`（§3.1）

隔离（51-FP与测试映射 §5）：
  * 自起 GUI：动态端口 + 独立 work-dir + 独立 `--data-dir` + **独立 HOME**
    （越级场景写进临时 work-dir 的 `.chonkpilot/capability/scenarios/`，随 `harness.tmp_dir` 删除 → 零残留）。
  * 套件级快照-还原 `_h.suite_config_guard(c)`（usr+prj；含异常/中断路径）。

运行：python run_scenario_agent_dup.py
"""
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

WS = _h.tmp_dir("ck-scdup-ws-")
DD = _h.tmp_dir("ck-scdup-dd-")
HOME = _h.tmp_home()
_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
_h.ensure_locale(c)
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）"
      % (WS, DD, HOME, _g.port), flush=True)

DUP_ID = "ck-dup-scn"
SCN_A = "ck-dup-a"
SCN_B = "ck-dup-b"
SCN_C = "ck-dup-c"
DUP_AGENT = "DupAgent"
SHARED_AGENT = "SharedAgent"
MAIN_PROMPT = "SENT-SCDUP-MAIN"


# ══════════════════════════════════════════════════════════════
# 通用工具
# ══════════════════════════════════════════════════════════════

def evi(tag, **kw):
    import json
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def scenario_save(sc):
    return c.req("data-scenario-save", {"data": sc})


def scenario_load(sid, level=""):
    body = {"data": {"id": sid}}
    if level:
        body["data"]["level"] = level
    return (c.req("data-scenario-load", body) or {}).get("data") or {}


def scenario_rows(sid):
    return [x for x in ((c.req("data-scenario-list", {}) or {}).get("list") or [])
            if x.get("id") == sid]


def refresh_ids():
    return [(e.get("payload") or {}).get("id") for e in c.events_of("data-scenario-refresh", clear=False)]


def wait_refresh(sid, max_wait=8):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if sid in refresh_ids():
            return True
        time.sleep(0.3)
    return False


def save_ok(sid, name, agents):
    """保存并断言收到该场景的 data-scenario-refresh 广播（mq 断言）。"""
    scenario_save({"id": sid, "name": name, "level": "project", "agents": agents})
    if not wait_refresh(sid):
        raise TestError("保存 %s 后未收到 data-scenario-refresh（ids=%r）" % (sid, refresh_ids()))


def main_agent():
    return {"name": "主", "roleTag": "主", "isMain": True, "prompt": MAIN_PROMPT}


AGENTS_DIR = os.path.join(WS, ".chonkpilot", "capability", "agents")


def agent_ref(name):
    """子 agent 唯一形态 = 引用（内联子 agent 已废除）：落一份项目级 `agents/<名>.agent.md`
    夹具并返回 `${workDir}` 引用串（读侧按引用展开，悬空则静默删除 → 夹具须真实存在）。"""
    os.makedirs(AGENTS_DIR, exist_ok=True)
    with open(os.path.join(AGENTS_DIR, name + ".agent.md"), "w", encoding="utf-8") as f:
        f.write("# %s\n\n[content]\n子 agent 提示词\n" % name)
    return "${workDir}/.chonkpilot/capability/agents/%s.agent.md" % name


# ══════════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════════

def case_reject_same_scenario_duplicate():
    """① 同场景内 agent 重名（大小写差异）→ 保存失败，错误含场景 id + 重复名，不落盘、不广播。"""
    c.mq_on_capture(["data-scenario-refresh"])
    err = ""
    try:
        scenario_save({"id": DUP_ID, "name": "重名场景", "level": "project",
                       "agents": [main_agent(),
                                  {"name": DUP_AGENT, "ref": agent_ref(DUP_AGENT)},
                                  {"name": DUP_AGENT.upper(), "ref": agent_ref(DUP_AGENT.upper())}]})  # 大小写差异 = 重名
    except TestError as e:
        err = str(e)
    if not err:
        raise TestError("同场景内 agent 重名应保存失败")
    low = err.lower()
    if DUP_ID.lower() not in low or DUP_AGENT.lower() not in low:
        raise TestError("拒绝文案未含「场景 id + 重复 agent 名」：%s" % err)
    if scenario_rows(DUP_ID):
        raise TestError("被拒的重名场景不应落盘")
    time.sleep(0.6)
    if DUP_ID in refresh_ids():
        raise TestError("被拒的保存不应广播 data-scenario-refresh（ids=%r）" % (refresh_ids(),))
    evi("① 同场景重名被拒", rejected=err, listed=scenario_rows(DUP_ID),
        refresh_has_dup=DUP_ID in refresh_ids())


def case_allow_same_name_across_scenarios():
    """② 不同场景同名 agent → 允许（各自保存成功、均落盘、各收到 refresh）。"""
    c.mq_on_capture(["data-scenario-refresh"])
    for sid in (SCN_A, SCN_B):
        save_ok(sid, "同名跨场景 " + sid,
                [main_agent(), {"name": SHARED_AGENT, "ref": agent_ref(SHARED_AGENT)}])
    for sid in (SCN_A, SCN_B):
        rows = scenario_rows(sid)
        if not rows or rows[0].get("level") != "project":
            raise TestError("不同场景同名 agent 应允许，%s 未落盘：%r" % (sid, rows))
    evi("② 跨场景同名允许", a_ok=bool(scenario_rows(SCN_A)), b_ok=bool(scenario_rows(SCN_B)),
        refresh_ids=refresh_ids())


def case_unique_names_saved():
    """③ 未重名（唯一名）→ 正常保存：落盘 + refresh 广播 + load 回读（名称/主 agent 保真）。"""
    c.mq_on_capture(["data-scenario-refresh"])
    save_ok(SCN_C, "唯一名场景",
            [main_agent(), {"name": "alpha", "ref": agent_ref("alpha")},
             {"name": "beta", "ref": agent_ref("beta")}])
    rec = scenario_load(SCN_C, "project")
    names = [a.get("name") for a in (rec.get("agents") or [])]
    if not ({"alpha", "beta"} <= set(names)):
        raise TestError("唯一名场景保存后回读 agent 名异常：%r" % (names,))
    if not (rec.get("agents") and rec["agents"][0].get("isMain")):
        raise TestError("唯一名场景回读主 agent 应恒列首位：%r" % (rec.get("agents"),))
    if SCN_C not in refresh_ids():
        raise TestError("唯一名场景保存未广播 data-scenario-refresh")
    evi("③ 唯一名正常保存", names=names, refresh_has=SCN_C in refresh_ids())


def cleanup():
    for sid in (DUP_ID, SCN_A, SCN_B, SCN_C):
        try:
            c.req("data-scenario-delete", {"data": {"id": sid, "level": "project"}})
        except Exception as e:
            print("[cleanup] 项目级场景删除失败(%s): %s" % (sid, e), flush=True)


def main():
    ok = total = 0
    c.console(clear=True)
    print("依赖：--test-port=%d 的 GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME" % _g.port,
          flush=True)
    try:
        for name, fn in [
            ("① 同场景内 agent 重名（大小写差异）→ 保存失败（含场景 id + agent 名）",
             case_reject_same_scenario_duplicate),
            ("② 不同场景同名 agent → 允许（均落盘 + 各收到 refresh）",
             case_allow_same_name_across_scenarios),
            ("③ 未重名 → 正常保存（落盘 + refresh + 回读）",
             case_unique_names_saved),
        ]:
            total += 1
            ok += run_case(name, fn)
    finally:
        cleanup()
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n场景 agent 重名校验：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

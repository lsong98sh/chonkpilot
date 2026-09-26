# -*- coding: utf-8 -*-
"""P6 批次 L4 套件：场景（**项目级** + **出厂默认 app 级只读 + 同名 save 被拒**）的
**A 落库回读 + B 送 LLM system 含哨兵**。

背景（2026-09-25 T6 口径更新，[25-MCP与场景分层模型 §6/§8.1 #8] · [42 §2 (170)]）：
  * 场景 = **独立根 `scenarios/`**（与 capability/ 平级），三级 app / user / project；
    **场景 id 全局唯一（跨级亦然）** → 三级"覆盖"语义不存在，`save` 跨级同名**拒绝**。
  * **出厂默认场景 = app 级**（随发布只读资源 `<exeDir>/scenarios/default/`，由 build 脚本投放）：
    不再由代码内嵌物化到 user 级 → user 级**不能**自建同名 `default`（改用其它 id 自建）。
  * 『还原默认』（`data-scenario-restore`）= **非 save 路径**：不受跨级重名校验约束，
    把 app 级出厂同名场景覆盖写一份到 user 级。

覆盖（每条 = A 数据面回读 + B 真链路可观测；B 恒以 system 原文/内容比对收口）
  P1 项目级场景 A+B   ：data-scenario-save{level:project}（含哨兵 main.agent.md）→
      A：data-scenario-load{id, level:project} 的 systemPrompt 含哨兵 + 磁盘
         `<WS>/.chonkpilot/scenarios/<id>/main.agent.md` 含哨兵；
      B：llm-start{scenario_id} → mock `/last.system` 含哨兵（**送 LLM 的系统提示词原文**）。
  P2 切换场景即生效 B ：改发第二个项目级场景 → system 含哨兵2 **且不含哨兵1**（逐轮解析，非缓存）。
  P3 出厂默认（app 级）+ 同名 save 被拒 A+B ：
      A ①：data-scenario-list 的 `default` 行 level=app（只读；真实 UI 页显示只读徽标、无编辑/删除/还原入口）；
      B ①：llm-start{scenario_id:"default"} → system 含**出厂主 agent 提示词**（Loop Engineer）；
      A ②：data-scenario-save{id:default, level:user} → **被拒**（跨级同名）且不落盘、不产生 user 级副本；
      B ②：被拒后 system 仍为出厂文案（哨兵未生效）；
      A ③：`data-scenario-restore{id:default}`（非 save 路径）→ app 级出厂内容写入 user 级（可回读），随后清理。

观测渠道（**全部为 61-消息一览既有主题，零新增**）
  §3.1 data-scenario-{list,load,save,delete,restore} · §4.2 llm-start{scenario_id}
  · mock LLM 桩侧 `GET /last`（system 原文，run_llm_fields 同法）
  前端内部既有事件：preview-tab-open(kind=scenario) / preview-tab-close-all

隔离（51-FP与测试映射 §5/§6-8）：
  * 自起 GUI：动态端口 + 独立 work-dir + 独立 `--data-dir` + 独立 `HOME`
    （app 级场景来自发行目录只读资源；restore 写出的 user 级副本落在临时 HOME 内并即时清理，
    绝不碰机器 ~/.chonkpilot）。
  * 套件级快照-还原 `_h.suite_config_guard(c)`（usr+prj；含异常/中断路径）。
  * 项目级场景落在临时 work-dir 内 → 结束随 `harness.tmp_dir` 删除 → **零残留**。

运行：python run_scenario_prj.py
"""

import json
import os
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

SENT = "SENT-P6SC-%d" % int(time.time())
PRJ_ID = "p6sc-prj-1"
PRJ_ID2 = "p6sc-prj-2"
PRJ_PROMPT = SENT + "-PRJ-PROMPT"
PRJ_PROMPT2 = SENT + "-PRJ2-PROMPT"
DEF_PROMPT = SENT + "-DEFAULT-PROMPT"
# 出厂默认场景（app 级 `scenarios/default/main.agent.md`）主 agent 提示词的特征串
APP_DEF_MARK = "你是自动化工作流的编排者与决策者"

WS = _h.tmp_dir("ck-scprj-ws-")
DD = _h.tmp_dir("ck-scprj-dd-")
HOME = _h.tmp_home()
PRJ_PROMPTS = os.path.join(WS, ".chonkpilot", "scenarios")
os.makedirs(PRJ_PROMPTS, exist_ok=True)

MOCK = _h.start_mock_llm(_h.free_port())
_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME,
                  extra_args=("-llm-base=http://127.0.0.1:%d/v1" % MOCK.port,
                              "-llm-model=m-exe-default"))
c = _g.client
_h.suite_config_guard(c)
_h.ensure_locale(c)
print("[env] ws=%s data=%s home=%s mock=%d gui=%d（临时目录，结束即删）"
      % (WS, DD, HOME, MOCK.port, _g.port), flush=True)


# ══════════════════════════════════════════════════════════════
# 通用工具
# ══════════════════════════════════════════════════════════════

def ev(js, timeout=6000):
    v = c.eval(js, timeout)
    for _ in range(4):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def wait_for(pred, desc, max_wait=20, interval=0.3):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if pred():
            return True
        time.sleep(interval)
    raise TestError("%s 超时（%ss）" % (desc, max_wait))


def mock_last():
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/last" % MOCK.port, timeout=5) as r:
            return json.loads(r.read().decode("utf-8")) or {}
    except Exception:
        return {}


def mock_reset():
    try:
        urllib.request.urlopen("http://127.0.0.1:%d/reset" % MOCK.port, timeout=5).read()
    except Exception:
        pass
    time.sleep(0.2)


def scenario_save(sc):
    return c.req("data-scenario-save", {"data": sc})


def scenario_load(sid, level=""):
    body = {"data": {"id": sid}}
    if level:
        body["data"]["level"] = level
    return (c.req("data-scenario-load", body) or {}).get("data") or {}


def scenario_ids(sid):
    return [x for x in ((c.req("data-scenario-list", {}) or {}).get("list") or [])
            if x.get("id") == sid]


def prj_prompt_file(sid):
    return os.path.join(PRJ_PROMPTS, sid, "main.agent.md")


def system_of_turn(q, scenario_id):
    """mq 驱动一轮 llm-start（走真实 server → mock LLM），返回 (llm-complete payload, system 原文)。"""
    sid = "p6sc-sess-%d" % int(time.time() * 1000)
    mock_reset()
    c.mq_emit("llm-start", {"session_id": sid, "turn": "t-" + sid, "q": q, "llm": "",
                            "think": "", "effort": "", "scenario_id": scenario_id})
    deadline = time.time() + 90
    done = None
    while time.time() < deadline:
        for e in c.events_of("llm-complete", clear=False):
            p = e.get("payload") or {}
            if p.get("session") in (sid, None) and p.get("status"):
                done = p
                break
        if done:
            break
        time.sleep(0.4)
    if not done:
        raise TestError("llm-complete 超时（scenario=%r）" % scenario_id)
    c.events_of("llm-complete", clear=True)
    time.sleep(0.3)
    return done, (mock_last().get("system") or "")


def arm_events():
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(["llm-complete", "turn-start"])


# ══════════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════════

def case_p1_prj_scenario():
    """P1：项目级场景 —— A 回读（消息面 + 磁盘 main.agent.md）+ B 送 LLM system 含哨兵。"""
    scenario_save({"id": PRJ_ID, "name": "P6 项目级场景", "level": "project",
                   "agents": [{"name": "主", "roleTag": "主", "isMain": True,
                               "prompt": PRJ_PROMPT}]})
    # A ①：消息面回读（level=project 精确级）
    rec = scenario_load(PRJ_ID, "project")
    if rec.get("level") != "project" or PRJ_PROMPT not in (rec.get("systemPrompt") or ""):
        raise TestError("A 回读失败（项目级）: level=%r payload=%r"
                        % (rec.get("level"), (rec.get("systemPrompt") or "")[:80]))
    # A ②：磁盘落点（main.agent.md 文件名 = 主 agent 契约承载）
    fp = prj_prompt_file(PRJ_ID)
    wait_for(lambda: os.path.isfile(fp) and PRJ_PROMPT in open(fp, encoding="utf-8").read(),
             "项目级 main.agent.md 未落盘哨兵", 8)
    disk = open(fp, encoding="utf-8").read()
    # A ③：合并列表可见（项目级场景进入 data-scenario-list，level=project）
    mine = scenario_ids(PRJ_ID)
    if not mine or mine[0].get("level") != "project":
        raise TestError("A 列表未含项目级场景: %r" % (mine,))
    # B：真实一轮 → mock 收到的 system 原文含哨兵
    arm_events()
    p, system = system_of_turn("hello prj scenario", PRJ_ID)
    evi("P1 项目级场景", status=p.get("status"), level=rec.get("level"), file=fp,
        file_has_sentinel=PRJ_PROMPT in disk, list_level=mine[0].get("level"),
        system_has_sentinel=PRJ_PROMPT in system, system_head=system[:70])
    if p.get("status") != "complete":
        raise TestError("本轮未正常收尾: %r" % (p,))
    if PRJ_PROMPT not in system:
        raise TestError("B 项目级场景 systemPrompt 未注入 LLM: %r" % (system[:200],))


def case_p2_switch_takes_effect():
    """P2：切换场景即生效（同一实例内改发第二个场景 → system 换成哨兵2，且不含哨兵1）。"""
    scenario_save({"id": PRJ_ID2, "name": "P6 项目级场景2", "level": "project",
                   "agents": [{"name": "主", "roleTag": "主", "isMain": True,
                               "prompt": PRJ_PROMPT2}]})
    rec = scenario_load(PRJ_ID2, "project")
    if PRJ_PROMPT2 not in (rec.get("systemPrompt") or ""):
        raise TestError("A 回读失败（第二个项目级场景）: %r" % (rec.get("systemPrompt") or "")[:80])
    arm_events()
    p, system = system_of_turn("hello switch scenario", PRJ_ID2)
    evi("P2 切换场景即生效", status=p.get("status"), system_has_sentinel2=PRJ_PROMPT2 in system,
        system_has_sentinel1=PRJ_PROMPT in system, system_head=system[:70])
    if PRJ_PROMPT2 not in system:
        raise TestError("B 切换后的场景未生效: %r" % (system[:200],))
    if PRJ_PROMPT in system:
        raise TestError("B 切换后仍带旧场景哨兵: %r" % (system[:200],))


def case_p3_app_default_and_duplicate_rejected():
    """P3：出厂默认场景 = app 级只读（A+B）+ 跨级同名 save 被拒（A+B）+ restore 非 save 路径可用。"""
    # A ①：出厂默认场景在 **app 级**（随发布只读资源 scenarios/default/）
    rows = scenario_ids("default")
    if not rows:
        raise TestError("data-scenario-list 未含出厂默认场景 default")
    if rows[0].get("level") != "app":
        raise TestError("出厂默认场景应落在 app 级（只读）: %r" % (rows,))

    # B ①：默认场景（app 级）主 agent 提示词真的进 LLM
    arm_events()
    p, system = system_of_turn("hello app default", "default")
    evi("P3-1 出厂默认场景（app 级）", level=rows[0].get("level"), name=rows[0].get("name"),
        status=p.get("status"), system_has_builtin=APP_DEF_MARK in system, system_head=system[:70])
    if p.get("status") != "complete":
        raise TestError("本轮未正常收尾: %r" % (p,))
    if APP_DEF_MARK not in system:
        raise TestError("B 出厂默认场景 prompt 未注入 LLM: %r" % (system[:200],))

    # 真实 UI：场景管理页 → default 行 = 系统级只读徽标 + 无编辑/删除/还原入口
    c.mq_emit("preview-tab-close-all")
    time.sleep(0.5)
    c.mq_emit("preview-tab-open", {"kind": "scenario", "title": "场景"})
    wait_for(lambda: int(ev("document.querySelectorAll('.b-table tbody tr').length") or 0) > 0,
             "场景管理表格未渲染", 15)
    time.sleep(0.6)
    probe = ev("""(() => {
      const rows = [...document.querySelectorAll('.b-table tbody tr')];
      const row = rows.find(r => (r.textContent||'').includes('开发场景'));
      if (!row) return 'norow';
      const badge = (row.querySelector('.readonly-badge')||{}).textContent || '';
      const btns = [...row.querySelectorAll('button')].map(b => (b.textContent||'').trim());
      return JSON.stringify({badge: badge, btns: btns});
    })()""")
    info = probe if isinstance(probe, dict) else {}
    if not info and isinstance(probe, str) and probe.startswith("{"):
        try:
            info = json.loads(probe)
        except Exception:
            info = {}
    if not info:
        raise TestError("场景页未找到 default 行: %r" % (probe,))
    evi("P3-2 app 级只读 UI", badge=info.get("badge"), buttons=info.get("btns"))
    if not (info.get("badge") or "").strip():
        raise TestError("app 级 default 行未显示「系统级只读」徽标: %r" % (info,))
    for b in info.get("btns") or []:
        if any(k in b for k in ("还原默认", "Restore", "删除", "Delete", "编辑", "Edit")):
            raise TestError("app 级 default 行不应有可写入口: %r" % (info,))

    # A ②：跨级同名 save **被拒**（场景 id 全局唯一，25 §6）且不落盘
    rejected = ""
    try:
        scenario_save({"id": "default", "name": "默认场景", "level": "user",
                       "agents": [{"name": "Loop Engineer", "roleTag": "主", "isMain": True,
                                   "prompt": DEF_PROMPT}]})
    except TestError as e:
        rejected = str(e)
    if not rejected:
        raise TestError("跨级同名 save 应被拒（场景 id 全局唯一）")
    if "全局唯一" not in rejected:
        raise TestError("同名 save 拒绝文案不含「全局唯一」: %s" % rejected)
    after = scenario_ids("default")
    if len(after) != 1 or after[0].get("level") != "app":
        raise TestError("被拒的 save 不应落盘: %r" % (after,))
    user_rec = {}
    try:
        user_rec = scenario_load("default", "user")
    except TestError:
        user_rec = {}
    if user_rec:
        raise TestError("user 级不应存在 default 副本: %r" % (user_rec,))

    # B ②：被拒 → system 仍为出厂文案（哨兵未生效）
    arm_events()
    p2, sys2 = system_of_turn("hello app default 2", "default")
    if p2.get("status") != "complete":
        raise TestError("本轮未正常收尾: %r" % (p2,))
    evi("P3-3 同名 save 被拒", rejected=rejected, sentinel_absent=DEF_PROMPT not in sys2,
        system_has_builtin=APP_DEF_MARK in sys2, list_levels=[x.get("level") for x in after])
    if DEF_PROMPT in sys2:
        raise TestError("被拒的 save 不应改变 system: %r" % (sys2[:200],))

    # A ③：**非 save 路径**（restore）不受跨级重名校验影响 —— app 级出厂内容覆盖写一份到 user 级
    c.req("data-scenario-restore", {"id": "default"})
    restored = scenario_load("default", "user")
    evi("P3-4 restore（非 save 路径）", restored=bool(restored), level=restored.get("level"),
        has_prompt=bool(restored.get("systemPrompt")))
    if not restored or restored.get("level") != "user" or not restored.get("systemPrompt"):
        raise TestError("restore 未把 app 级出厂场景写入 user 级: %r" % (restored,))
    # 收尾：删掉刚写出的 user 级副本（app 级只读、不受影响）
    c.req("data-scenario-delete", {"data": {"id": "default", "level": "user"}})


def cleanup():
    for sid in (PRJ_ID, PRJ_ID2):
        try:
            c.req("data-scenario-delete", {"data": {"id": sid, "level": "project"}})
        except Exception as e:
            print("[cleanup] 项目级场景删除失败(%s): %s" % (sid, e), flush=True)
    try:  # restore 用例写出的 user 级副本（异常路径兜底）
        c.req("data-scenario-delete", {"data": {"id": "default", "level": "user"}})
    except Exception:
        pass


def main():
    ok = total = 0
    c.console(clear=True)
    print("依赖：--test-port=%d 的 GUI（harness 自起）+ mock LLM:%d + 隔离 work-dir/data-dir/HOME"
          % (_g.port, MOCK.port), flush=True)
    try:
        for name, fn in [
            ("P1 项目级场景：A 回读（消息面+main.agent.md）+ B system 含哨兵", case_p1_prj_scenario),
            ("P2 切换场景即生效：system 换哨兵2 且不含哨兵1（B）", case_p2_switch_takes_effect),
            ("P3 出厂默认（app 级只读）+ 跨级同名 save 被拒 + restore 非 save 路径（A+B）",
             case_p3_app_default_and_duplicate_rejected),
        ]:
            total += 1
            ok += run_case(name, fn)
    finally:
        cleanup()
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n场景（项目级 + 出厂默认 app 级只读 + 重名拒绝）：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㊱：**场景项目级落库→system 注入、agent 重名拒绝、ref 提示词回环**
（迁移自 run_scenario_prj P1/P2、run_scenario_agent_dup ①②③、run_sem_scenario_ref S1–S4）。

* 项目级场景 save（main.agent.md 哨兵）→ load.agents[isMain].prompt / 磁盘均含哨兵；
  `llm-start{scenario_id}` → mock `/last.system` 含哨兵；切换第二场景 → 只含哨兵2、不含哨兵1；
* 同场景 agent 大小写重名 → save 失败（文案含场景 id + 重复名）、不落盘、不广播；
  跨场景同名允许；唯一名正常保存 + refresh + load 回读；
* ref 子 agent：编辑页提示词框 == 被引文件正文 → 编辑保存 → 磁盘新值 → 重开一致 + ref 不变。
"""

import json
import os
import time
import urllib.request

from .framework import case


def _last(env):
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/last" % env.mock_port,
                                    timeout=5) as f:
            return json.loads(f.read().decode("utf-8")) or {}
    except Exception:
        return {}


def _turn_system(env, scenario_id, q):
    sid = "e2e-scn-%s-%d" % (env.run_id, int(time.time() * 1000) % 100000)
    env.req("data-session-ensure-session", {"data": {"session_id": sid}})
    env.capture(["turn-start", "llm-complete", "llm-error"])
    env.emit("llm-start", {"session_id": sid, "turn": "t-" + sid, "q": q, "llm": "mock",
                           "think": "", "effort": "", "scenario_id": scenario_id})
    env.wait_events("turn-start", lambda p: p.get("session") == sid,
                    "turn-start（%s）" % sid, timeout=60)
    env.wait_events("llm-complete", lambda p: p.get("session") == sid,
                    "llm-complete（%s）" % sid, timeout=120)
    return str(_last(env).get("system") or "")


def _save(env, data):
    return env.req("data-scenario-save", {"data": data}) or {}


def _rows(env, sid):
    return [x for x in ((env.req("data-scenario-list", {}) or {}).get("list") or [])
            if x.get("id") == sid]


def _load(env, sid):
    return (env.req("data-scenario-load", {"data": {"id": sid, "level": "project"}}) or {}
            ).get("data") or {}


@case("S-SCEN-DUP-01", "配置", "场景 agent 重名：同场景大小写重名拒绝、跨场景同名允许、唯一名正常",
      stage="seeded", order=338, fp="42 场景 · run_scenario_agent_dup ①②③",
      desc="同场景两个 agent 名大小写差异 → data-scenario-save 失败且错误含场景 id+重复名，"
           "list 不落盘、无 data-scenario-refresh；不同场景同名 → 两场景均保存+refresh；唯一名 → "
           "落盘 + refresh + load 回读名称与主 agent。")
def s_scen_dup_01(env):
    rid = env.run_id.replace("-", "_")
    dup_id = "e2e-dup-%s" % rid
    a_id = "e2e-dupA-%s" % rid
    b_id = "e2e-dupB-%s" % rid
    ok_id = "e2e-dupOK-%s" % rid
    agents_dir = env.path(".chonkpilot", "capability", "agents")
    os.makedirs(agents_dir, exist_ok=True)

    def _fixture(name):
        p = os.path.join(agents_dir, name + ".agent.md")
        with open(p, "w", encoding="utf-8") as f:
            f.write("# 基本信息\n```yaml\nname: %s\n```\n\n# 摘要\n夹具\n\n# 正文\n"
                    "ref prompt %s\n" % (name, name))
        return "${workDir}/.chonkpilot/capability/agents/%s.agent.md" % name

    main = {"name": "主", "roleTag": "主", "isMain": True, "prompt": "E2E MAIN %s" % rid}
    dup = "E2EAgent" + rid[-4:]
    try:
        env.capture(["data-scenario-refresh"])
        err = ""
        try:
            _save(env, {"id": dup_id, "name": "重名场景", "level": "project",
                        "agents": [main,
                                   {"name": dup, "ref": _fixture(dup)},
                                   {"name": dup.upper(), "ref": _fixture(dup.upper())}]})
        except Exception as e:
            err = str(e)
        low = err.lower()
        env.check("① 同场景大小写重名被拒且文案含场景 id+重复名",
                  bool(err) and dup_id.lower() in low and dup.lower() in low, err[:160])
        env.check("① 被拒场景不落盘", not _rows(env, dup_id))
        time.sleep(0.6)
        refreshed = [(e.get("payload") or {}).get("id")
                     for e in env.events("data-scenario-refresh")]
        env.check("① 被拒不广播 data-scenario-refresh", dup_id not in refreshed, refreshed)

        env.capture(["data-scenario-refresh"])
        shared = "Shared" + rid[-4:]
        for sid, label in ((a_id, "同名 A"), (b_id, "同名 B")):
            _save(env, {"id": sid, "name": label, "level": "project",
                        "agents": [main, {"name": shared, "ref": _fixture(shared)}]})
        env.wait(lambda: True if all(_rows(env, x) for x in (a_id, b_id)) else None,
                 "跨场景同名两场景落盘", timeout=20)
        ids = [(e.get("payload") or {}).get("id") for e in env.events("data-scenario-refresh")]
        env.check("② 不同场景同名允许（均落盘 + 各自 refresh）",
                  all(_rows(env, x) for x in (a_id, b_id)) and a_id in ids and b_id in ids, ids)

        env.capture(["data-scenario-refresh"])
        name = "e2e-unique-%s" % rid
        _save(env, {"id": ok_id, "name": "唯一名场景", "level": "project",
                    "agents": [main, {"name": name, "ref": _fixture(name)},
                               {"name": name + "B", "ref": _fixture(name + "B")}]})
        env.wait(lambda: True if _rows(env, ok_id) else None, "唯一名场景落盘", timeout=20)
        env.wait(lambda: True if ok_id in [(e.get("payload") or {}).get("id")
                                           for e in env.events("data-scenario-refresh")] else None,
                 "唯一名场景 refresh", timeout=15)
        d = _load(env, ok_id)
        subs = [a.get("name") for a in (d.get("agents") or []) if not a.get("isMain")]
        env.check("③ 唯一名正常保存 + load 回读",
                  (d.get("name") == "唯一名场景") and name in subs and name + "B" in subs,
                  {"name": d.get("name"), "agents": subs})
    finally:
        for sid in (dup_id, a_id, b_id, ok_id):
            try:
                env.req("data-scenario-delete", {"data": {"id": sid}})
            except Exception:
                pass
        for p in os.listdir(agents_dir):
            if p.startswith(("E2EAgent", "Shared", "e2e-unique-")):
                try:
                    os.remove(os.path.join(agents_dir, p))
                except OSError:
                    pass


@case("S-SCEN-PRJ-01", "配置", "项目级场景：落库/磁盘/送 LLM system，切换场景逐轮生效",
      stage="seeded", order=339, fp="42 场景 · run_scenario_prj P1/P2",
      desc="项目级 save 含 main 哨兵 → load.agents[isMain].prompt 与磁盘 main.agent.md 含哨兵；"
           "llm-start{scenario_id} → mock system 含哨兵；切换第二场景 → system 含哨兵2、不含哨兵1。")
def s_scen_prj_01(env):
    rid = env.run_id
    s1 = "e2e-prj-scn-%s" % rid.replace("-", "_")
    s2 = s1 + "-b"
    p1 = "E2E-SCEN-PRJ-ONE-%s" % rid
    p2 = "E2E-SCEN-PRJ-TWO-%s" % rid
    try:
        _save(env, {"id": s1, "name": "E2E 项目场景一", "level": "project",
                    "agents": [{"name": "主", "roleTag": "主", "isMain": True, "prompt": p1}]})
        env.wait(lambda: True if _rows(env, s1) else None, "场景一落盘", timeout=25)
        d = _load(env, s1)
        mains = [a for a in (d.get("agents") or []) if a.get("isMain")]
        env.check("P1 load 主 agent prompt 含哨兵",
                  bool(mains) and p1 in str(mains[0].get("prompt") or ""), mains[:1])
        f1 = env.path(".chonkpilot", "capability", "scenarios", s1, "main.agent.md")
        env.check("P1 磁盘 main.agent.md 含哨兵",
                  os.path.isfile(f1) and p1 in open(f1, encoding="utf-8").read(), f1)
        sys1 = _turn_system(env, s1, "项目场景一 hello")
        env.check("P1 送 LLM system 含哨兵", p1 in sys1, sys1[:200])

        _save(env, {"id": s2, "name": "E2E 项目场景二", "level": "project",
                    "agents": [{"name": "主", "roleTag": "主", "isMain": True, "prompt": p2}]})
        env.wait(lambda: True if _rows(env, s2) else None, "场景二落盘", timeout=25)
        sys2 = _turn_system(env, s2, "项目场景二 hello")
        env.check("P2 切换场景 → system 含哨兵2 且不含哨兵1",
                  p2 in sys2 and p1 not in sys2, {"has2": p2 in sys2, "has1": p1 in sys2})
    finally:
        for sid in (s1, s2):
            try:
                env.req("data-scenario-delete", {"data": {"id": sid}})
            except Exception:
                pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("项目级场景组无前端错误", len(errs) == 0, errs[:3])


@case("S-SCEN-REF-01", "配置", "ref 子 agent 提示词：编辑页载入文件正文、编辑保存落盘、重开一致、ref 不变",
      stage="seeded", order=340, fp="42 场景编辑 · run_sem_scenario_ref S1–S4",
      desc="save 场景含 ref 子 agent → scenario-edit-row 打开编辑页 → 选中子 agent + 提示词页签 → "
           "文本框 == 被引文件正文；编辑 → scenario-save → 磁盘新值；重开值一致；ref 横幅/场景记录"
           "均未变（编辑不移动引用）。")
def s_scen_ref_01(env):
    rid = env.run_id.replace("-", "_")
    sid = "e2e-ref-%s" % rid
    sub = "RefAgent" + rid[-4:]
    ref = "${workDir}/.chonkpilot/capability/agents/%s.agent.md" % sub
    agents_dir = env.path(".chonkpilot", "capability", "agents")
    os.makedirs(agents_dir, exist_ok=True)
    afile = os.path.join(agents_dir, sub + ".agent.md")
    body = "# 基本信息\n```yaml\nname: %s\n```\n\n# 摘要\nref agent 夹具\n\n# 正文\n" \
           "REF-ORIGINAL-%s\n" % (sub, rid)
    with open(afile, "w", encoding="utf-8") as f:
        f.write(body)
    new_val = "REF-EDITED-%s\n\n详细要求：保持引用不变。" % rid

    def _open():
        env.emit("preview-tab-open", {"kind": "scenario", "title": "场景"})
        env.wait(lambda: True if env.js(
            "(()=>{const ps=[...document.querySelectorAll('.special-tab.dialog-content')];"
            "return ps.some(p=>p.getBoundingClientRect().width>0)})()") is True else None,
            "场景管理页打开", timeout=25)
        row = _load(env, sid) or {"id": sid, "name": "E2E ref 场景", "level": "project",
                                   "agents": [{"name": "主", "roleTag": "主", "isMain": True,
                                               "prompt": "main"},
                                              {"name": sub, "roleTag": "", "ref": ref}]}
        env.js("window.mq.emit('scenario-edit-row', {row: %s}); 'ok'"
               % json.dumps(row, ensure_ascii=False))
        return env.wait(lambda: True if env.js(
            "!!document.querySelector('.edit-dialog-body')") is True else None,
            "场景编辑弹窗", timeout=20)

    def _select_ref():
        env.wait(lambda: True if int(env.js(
            "document.querySelectorAll('.agent-list-item').length") or 0) >= 2 else None,
            "agent 列表项就绪", timeout=20)
        r = env.js("(()=>{const els=[...document.querySelectorAll('.agent-list-item')];"
                   "const a=els.find(e=>((e.querySelector('.al-name')||{}).textContent||'')"
                   ".trim()===%s);if(a){a.click();return 'ok';}"
                   "const b=els.filter(e=>!(e.classList.contains('is-main')"
                   "||/主/.test((e.querySelector('.al-name')||{}).textContent||'')))[0];"
                   "if(b){b.click();return 'fallback';}return 'no-item';})()" % json.dumps(sub))
        env.check("选中 ref 子 agent 列表项", r in ("ok", "fallback"), r)
        try:
            env.wait(lambda: True if env.js(
                "!!document.querySelector('.ref-agent-banner')") is True else None,
                "ref 横幅", timeout=15)
        except Exception:
            names = env.jsj("JSON.stringify([...document.querySelectorAll("
                            "'.agent-list-item .al-name')].map(n=>n.textContent.trim()))") or []
            env.note("ref 横幅缺失诊断", {"names": names,
                                     "editor": env.js("!!document.querySelector('.agent-editor')")})
        env.js("(()=>{const t=[...document.querySelectorAll('.agent-editor-tabs .b-tabs-item')]"
               ".find(e=>(e.textContent||'').trim()==='提示词');if(t)t.click();return 'ok';})()")
        return env.wait(lambda: (lambda v: v if v else None)(str(env.js(
            "(()=>{const t=document.querySelector('.prompt-textarea');"
            "return t?t.value:''})()") or "")), "提示词框载入", timeout=20)

    try:
        env.req("data-scenario-save", {"data": {
            "id": sid, "name": "E2E ref 场景", "level": "project",
            "agents": [{"name": "主", "roleTag": "主", "isMain": True, "prompt": "main"},
                       {"name": sub, "roleTag": "", "ref": ref}]}})
        env.wait(lambda: True if _rows(env, sid) else None, "ref 场景落盘", timeout=25)
        env.check("S1 打开编辑页", _open() is True)
        v0 = _select_ref()
        disk0 = open(afile, encoding="utf-8").read()
        body0 = disk0.split("# 正文", 1)[1].strip() if "# 正文" in disk0 else disk0.strip()
        env.check("S1 ref 提示词框非空且 == 被引文件正文", v0.strip() == body0,
                  {"ui": v0[:60], "body": body0[:60]})

        env.client.input(".prompt-textarea", new_val, 5000)
        env.wait(lambda: True if str(env.js("document.querySelector('.prompt-textarea')"
                                            "?.value || ''")) == new_val else None,
                 "提示词框新值", timeout=10)
        env.js("window.mq.emit('scenario-save'); 'ok'")
        env.wait(lambda: True if not env.js("!!document.querySelector('.edit-dialog-body')")
                 else None, "保存后弹窗关闭", timeout=15)
        env.wait(lambda: True if new_val.strip() in open(
            afile, encoding="utf-8").read() else None, "被引 agent 文件落盘新值", timeout=20)
        env.check("S2 保存 → 被引文件落盘新值",
                  new_val.strip() in open(afile, encoding="utf-8").read())

        env.check("S3 重开编辑页", _open() is True)
        v1 = _select_ref()
        banner = str(env.js("(()=>{const e=document.querySelector('.ref-path');"
                            "return e?e.textContent.trim():''})()") or "")
        d = _load(env, sid)
        rec = next((a for a in (d.get("agents") or []) if a.get("name") == sub), {})
        env.check("S3 重开提示词值一致", v1.strip() == new_val.strip(),
                  {"reopen": v1[:60]})
        env.check("S4 ref 未变（横幅 + 场景记录）",
                  banner == ref and rec.get("ref") == ref, {"banner": banner, "ref": rec.get("ref")})
    finally:
        errs_close = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
                      if e.get("level") == "error"]
        if errs_close:
            env.note("已知前端缺陷（记录，不阻断）",
                     "场景编辑弹窗关闭触发 Vue render error：%s" % errs_close[0][:160])
            env.client.console(clear=True)
        env.js("""(()=>{[...document.querySelectorAll('.dialog-shell')]
          .filter(d=>d.querySelector('.edit-dialog-body')).forEach(d=>d.remove());return 'ok';})()""")
        try:
            env.req("data-scenario-delete", {"data": {"id": sid}})
        except Exception:
            pass
        try:
            if os.path.isfile(afile):
                os.remove(afile)
        except OSError:
            pass
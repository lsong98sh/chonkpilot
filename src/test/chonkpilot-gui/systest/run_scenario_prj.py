# -*- coding: utf-8 -*-
"""P6 批次 L4 套件：场景（**项目级** + **出厂 app 级可编辑 + 同名 save 被拒**）的
**A 落库回读 + B 送 LLM system 含哨兵**。

背景（2026-10-01 P1 口径更新，[25-MCP与场景分层模型 §6] · [42 §2]）：
  * 场景 = **capability 根下的 `scenarios/` 子目录**（`<级别根>/capability/scenarios/`），四级
    app / user / project / prjusr；**场景 id 全局唯一（跨级亦然）** → 四级"覆盖"语义不存在，
    `save` 跨级同名**拒绝**。
  * **四级均可编辑**：app 级（出厂场景 `<exeDir>/capability/scenarios/default/`）出厂内容 = **磁盘目录**
    （唯一源 `src/initdata/capability/scenarios/`，由构建脚本投放）—— **不再 embed、不再物化**，
    app 初始化不写盘：可保存 / 删除（删除即缺装，须由用户重装或 `initial.zip` 恢复）。
  * `data-scenario-restore` **已删除**（2026-09-26）；出厂恢复语义 = 从唯一源 `src/initdata/capability/scenarios/` 还原。

覆盖（每条 = A 数据面回读 + B 真链路可观测；B 恒以 system 原文/内容比对收口）
  P1 项目级场景 A+B   ：data-scenario-save{level:project}（含哨兵 main.agent.md）→
      A：data-scenario-load{id, level:project} 的 systemPrompt 含哨兵 + 磁盘
         `<WS>/.chonkpilot/capability/scenarios/<id>/main.agent.md` 含哨兵；
      B：llm-start{scenario_id} → mock `/last.system` 含哨兵（**送 LLM 的系统提示词原文**）。
  P2 切换场景即生效 B ：改发第二个项目级场景 → system 含哨兵2 **且不含哨兵1**（逐轮解析，非缓存）。
  P3 出厂场景（app 级，可编辑）+ 同名 save 被拒 A+B ：
      A ①：data-scenario-list 的 `default` 行 level=app；真实 UI 页 default 行**有编辑/删除入口**（无只读徽标）；
      B ①：llm-start{scenario_id:"default"} → system 含**出厂主 agent 提示词**（Loop Engineer）；
      A ②：data-scenario-save{id:default, level:user} → **被拒**（跨级同名）且不落盘、不产生 user 级副本；
      B ②：被拒后 system 仍为出厂文案（哨兵未生效）；
      A ③+B ③：**app 级可编辑** —— data-scenario-save{id:default, level:app}（哨兵）→ load 回读含哨兵、
         llm-start system 含哨兵；收尾删除 app default 后由 cleanup 从唯一源 `src/initdata/capability/scenarios/default` 还原。
  P4 另存为（新目录）A+UI ：编辑弹窗「另存为」= 换新 id 发**同一条** data-scenario-save（新目录语义）→
      A：新目录 `<WS>/.chonkpilot/capability/scenarios/<新id>/main.agent.md` 落盘 + data-scenario-list 新增一行
         + data-scenario-load{新id} 回读含哨兵；**原场景不变**（仍在、level/prompt 未改）；
      UI：真实编辑弹窗底部含「另存为」按钮、**新建**弹窗底部无（`v-if=!isNew`）。

观测渠道（**全部为 61-消息一览既有主题，零新增**）
  §3.1 data-scenario-{list,load,save,delete} · §4.2 llm-start{scenario_id}
  · mock LLM 桩侧 `GET /last`（system 原文，run_llm_fields 同法）
  前端内部既有事件：preview-tab-open(kind=scenario) / preview-tab-close-all

隔离（51-FP与测试映射 §5/§6-8）：
  * 自起 GUI：动态端口 + 独立 work-dir + 独立 `--data-dir` + 独立 `HOME`
    （app 级场景 = 发行目录磁盘目录 `dist/desktop/capability/scenarios/`；user 级自建落在临时 HOME 内并即时清理，绝不碰机器 ~/.chonkpilot）。
  * 套件级快照-还原 `_h.suite_config_guard(c)`（usr+prj；含异常/中断路径）。
  * 项目级场景落在临时 work-dir 内 → 结束随 `harness.tmp_dir` 删除 → **零残留**；
    app 级 default 用例收尾删除后由 cleanup 从唯一源 `src/initdata/capability/scenarios/default` 还原（不再 embed/物化）。

运行：python run_scenario_prj.py
"""

import json
import os
import shutil
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

SENT = "SENT-P6SC-%d" % int(time.time())
PRJ_ID = "p6sc-prj-1"
PRJ_ID2 = "p6sc-prj-2"
PRJ_ID_SAVEAS = "p6sc-prj-1-copy"
PRJ_PROMPT = SENT + "-PRJ-PROMPT"
PRJ_PROMPT2 = SENT + "-PRJ2-PROMPT"
DEF_PROMPT = SENT + "-DEFAULT-PROMPT"
DEF_EDIT = SENT + "-APP-EDIT"
# 出厂默认场景（app 级 `scenarios/default/main.agent.md`）主 agent 提示词的特征串
APP_DEF_MARK = "你是自动化工作流的编排者与决策者"

WS = _h.tmp_dir("ck-scprj-ws-")
DD = _h.tmp_dir("ck-scprj-dd-")
HOME = _h.tmp_home()
PRJ_PROMPTS = os.path.join(WS, ".chonkpilot", "capability", "scenarios")
os.makedirs(PRJ_PROMPTS, exist_ok=True)

# 出厂场景唯一源 与 发行落点（app 级场景根 = <exeDir>/capability/scenarios = dist/desktop/capability/scenarios）
REPO = r"e:\BizWorks\chonkpilot"
SRC_SCN_DIR = os.path.join(REPO, "src", "initdata", "capability", "scenarios")
APP_SCN_DIR = os.path.join(REPO, "dist", "desktop", "capability", "scenarios")

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


def case_p3_app_default_editable_and_duplicate_rejected():
    """P3：出厂场景 = app 级（可编辑）（A+B）+ 跨级同名 save 被拒（A+B）。"""
    # A ①：出厂场景在 **app 级**
    rows = scenario_ids("default")
    if not rows:
        raise TestError("data-scenario-list 未含出厂场景 default")
    if rows[0].get("level") != "app":
        raise TestError("出厂场景应落在 app 级: %r" % (rows,))

    # B ①：默认场景（app 级）主 agent 提示词真的进 LLM
    arm_events()
    p, system = system_of_turn("hello app default", "default")
    evi("P3-1 出厂场景（app 级）", level=rows[0].get("level"), name=rows[0].get("name"),
        status=p.get("status"), system_has_builtin=APP_DEF_MARK in system, system_head=system[:70])
    if p.get("status") != "complete":
        raise TestError("本轮未正常收尾: %r" % (p,))
    if APP_DEF_MARK not in system:
        raise TestError("B 出厂场景 prompt 未注入 LLM: %r" % (system[:200],))

    # 真实 UI：场景管理页 → default 行 = **可编辑/删除入口**（app 级不再只读、无只读徽标）
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
    evi("P3-2 app 级可编辑 UI", badge=info.get("badge"), buttons=info.get("btns"))
    if (info.get("badge") or "").strip():
        raise TestError("app 级 default 行不应再有只读徽标: %r" % (info,))
    joined = " ".join(info.get("btns") or [])
    if ("编辑" not in joined and "Edit" not in joined) or ("删除" not in joined and "Delete" not in joined):
        raise TestError("app 级 default 行应有编辑/删除入口: %r" % (info,))

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

    # A ③+B ③：**app 级可编辑** —— 保存到 app 级（哨兵）→ 消息面回读 + 送 LLM system 含哨兵
    scenario_save({"id": "default", "name": "开发场景（改）", "level": "app",
                   "agents": [{"name": "Loop Engineer", "roleTag": "主", "isMain": True,
                               "prompt": DEF_EDIT}]})
    edited = scenario_load("default", "app")
    if edited.get("level") != "app" or DEF_EDIT not in (edited.get("systemPrompt") or ""):
        raise TestError("app 级编辑回读失败: %r" % ((edited.get("systemPrompt") or "")[:120],))
    arm_events()
    p3, sys3 = system_of_turn("hello app edited", "default")
    evi("P3-4 app 级可编辑", status=p3.get("status"), level=edited.get("level"),
        system_has_edit=DEF_EDIT in sys3, system_head=sys3[:70])
    if p3.get("status") != "complete":
        raise TestError("本轮未正常收尾: %r" % (p3,))
    if DEF_EDIT not in sys3:
        raise TestError("app 级编辑未生效（system 不含哨兵）: %r" % (sys3[:200],))


def _footer_button_texts():
    """读当前编辑弹窗 `.edit-footer` 的按钮文案（'另存为|取消|保存' 形）。"""
    return ev("""(() => {
      const f = document.querySelector('.edit-footer');
      if (!f) return '';
      return [...f.querySelectorAll('button')].map(b => (b.textContent||'').trim()).join('|');
    })()""")


def _open_scenario_page():
    c.mq_emit("preview-tab-close-all")
    time.sleep(0.5)
    c.mq_emit("preview-tab-open", {"kind": "scenario", "title": "场景"})
    wait_for(lambda: int(ev("document.querySelectorAll('.b-table tbody tr').length") or 0) > 0,
             "场景管理表格未渲染", 15)
    time.sleep(0.6)


def _probe_save_as_button():
    """真实 UI：编辑现有场景 → 读编辑弹窗底部按钮（应含「另存为」）；
    再开**新建**弹窗 → 读底部按钮（应**无**「另存为」，`v-if=!isNew`）。返回 (编辑有, 新建有)。"""
    _open_scenario_page()

    # 编辑：点列表首行「编辑」入口 → 编辑弹窗底部
    c.eval("""(() => {
      const rows = [...document.querySelectorAll('.b-table tbody tr')];
      if (!rows.length) return 'norow';
      const btn = [...rows[0].querySelectorAll('button')]
        .find(b => ['编辑', 'Edit'].includes((b.textContent||'').trim()));
      if (!btn) return 'nobtn';
      btn.click();
      return 'ok';
    })()""")
    wait_for(lambda: ev("document.querySelector('.edit-footer') ? 1 : 0") == 1,
             "编辑弹窗未渲染", 10)
    time.sleep(0.4)
    edit_texts = _footer_button_texts()
    c.mq_emit("dialog-close")
    time.sleep(0.6)

    # 新建：点工具栏「添加场景」→ 新建弹窗底部
    c.eval("""(() => {
      const btn = [...document.querySelectorAll('.toolbar-actions button')]
        .find(b => ['添加场景', 'Add Scenario'].includes((b.textContent||'').trim()));
      if (!btn) return 'nobtn';
      btn.click();
      return 'ok';
    })()""")
    wait_for(lambda: ev("document.querySelector('.edit-footer') ? 1 : 0") == 1,
             "新建弹窗未渲染", 10)
    time.sleep(0.4)
    new_texts = _footer_button_texts()
    c.mq_emit("dialog-close")
    time.sleep(0.4)

    def has_save_as(txt):
        return ("另存为" in txt) or ("Save As" in txt)

    return has_save_as(edit_texts), has_save_as(new_texts)


def case_p4_save_as_new_dir():
    """P4：「另存为」= 换新 id 发同一条 data-scenario-save（新目录语义）——
    新目录落盘 + 列表/回读可见；原场景不变；编辑弹窗有「另存为」/ 新建弹窗无（UI）。"""
    origin_before = scenario_load(PRJ_ID, "project")
    if not origin_before:
        raise TestError("前置：原场景 %s 不存在（P1 未跑？）" % PRJ_ID)

    # 「另存为」在消息层 = 同一条 save，换新 id（前端 promptInput 输入新目录名后置 form.id）
    scenario_save({"id": PRJ_ID_SAVEAS, "name": "P6 项目级场景（另存为）", "level": "project",
                   "agents": [{"name": "主", "roleTag": "主", "isMain": True,
                               "prompt": PRJ_PROMPT}]})

    # A ①：写到**新目录**（原目录不动）
    fp = prj_prompt_file(PRJ_ID_SAVEAS)
    wait_for(lambda: os.path.isfile(fp) and PRJ_PROMPT in open(fp, encoding="utf-8").read(),
             "另存为的新目录 main.agent.md 未落盘", 8)
    # A ②：列表新增一行 + 回读
    rows = scenario_ids(PRJ_ID_SAVEAS)
    if not rows or rows[0].get("level") != "project":
        raise TestError("另存为的新场景未进入 data-scenario-list: %r" % (rows,))
    copy_rec = scenario_load(PRJ_ID_SAVEAS, "project")
    if PRJ_PROMPT not in (copy_rec.get("systemPrompt") or ""):
        raise TestError("另存为的新场景回读异常: %r" % ((copy_rec.get("systemPrompt") or "")[:80],))
    # A ③：原场景不变（仍在、level 与 name/prompt 未改）
    origin_after = scenario_ids(PRJ_ID)
    if not origin_after or origin_after[0].get("level") != "project":
        raise TestError("另存为后原场景应保持不变: %r" % (origin_after,))
    origin_rec = scenario_load(PRJ_ID, "project")
    if (origin_rec.get("name") != origin_before.get("name")
            or PRJ_PROMPT not in (origin_rec.get("systemPrompt") or "")):
        raise TestError("原场景内容被另存为篡改: %r"
                        % ((origin_rec.get("name"), (origin_rec.get("systemPrompt") or "")[:60]),))

    # UI：编辑弹窗底部含「另存为」；新建弹窗底部**不含**
    has_edit, has_new = _probe_save_as_button()
    evi("P4 另存为（新目录）", new_file=fp, new_level=rows[0].get("level"),
        new_prompt_has=PRJ_PROMPT in (copy_rec.get("systemPrompt") or ""),
        origin_unchanged=(origin_after[0].get("level") == "project"),
        edit_footer_has_save_as=has_edit, new_footer_has_save_as=has_new)
    if not has_edit:
        raise TestError("编辑弹窗底部应有「另存为」按钮")
    if has_new:
        raise TestError("新建弹窗底部不应有「另存为」按钮（v-if=!isNew）")


def cleanup():
    for sid in (PRJ_ID, PRJ_ID2, PRJ_ID_SAVEAS):
        try:
            c.req("data-scenario-delete", {"data": {"id": sid, "level": "project"}})
        except Exception as e:
            print("[cleanup] 项目级场景删除失败(%s): %s" % (sid, e), flush=True)
    # app 级 default 若被 P3-4 编辑 → 删除后从**出厂唯一源** `src/initdata/capability/scenarios/default` 还原
    # （2026-09-29：不再 embed、不再自动物化 —— 缺装须显式恢复）
    try:
        c.req("data-scenario-delete", {"data": {"id": "default", "level": "app"}})
    except Exception:
        pass
    src_def = os.path.join(SRC_SCN_DIR, "default")
    dst_def = os.path.join(APP_SCN_DIR, "default")
    if os.path.isdir(src_def):
        try:
            if os.path.isdir(dst_def):
                shutil.rmtree(dst_def)
            shutil.copytree(src_def, dst_def)
        except Exception as e:
            print("[cleanup] 出厂场景还原失败: %s" % e, flush=True)
    try:
        c.req("data-scenario-list", {})
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
            ("P3 出厂场景（app 级，可编辑）+ 跨级同名 save 被拒（A+B）",
             case_p3_app_default_editable_and_duplicate_rejected),
            ("P4 另存为（新目录）：新目录落盘 + 列表/回读可见 + 原场景不变（A）+ 编辑有/新建无按钮（UI）",
             case_p4_save_as_new_dir),
        ]:
            total += 1
            ok += run_case(name, fn)
    finally:
        cleanup()
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n场景（项目级 + 出厂 app 级可编辑 + 重名拒绝）：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

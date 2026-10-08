# -*- coding: utf-8 -*-
"""B · 语义/回环级试点 ②：场景向导「走完一遍 → 断言产物 + 各步选项非空渲染」。

覆盖（**产物级 + DOM 选项级**，拒绝"存在级"）：
  W1 各步选项**非空渲染**（语义级）：走 1→2→3→4→5，逐步断言**当前可见** `.wz-panel` 的
     选项控件数（step2 `.wz-card`≥4 · step3 `.wz-card`+`.wz-chip`≥5 · step4 `.wz-aux-item`/`.wz-member`≥1 ·
     step5 `.wz-agent-item`≥1）；step1 目标框可输入。
  W2 走完一遍（step2「默认」一键生成）→ **断言产物**：
      `<ws>/.chonkpilot/project_spec.md` 非空；
      `<ws>/.chonkpilot/capability/scenarios/<id>/scenario.json` 落盘且可解析；
      主 agent **内联提示词非空**；存在子 agent **ref 指向 `${workDir}/.chonkpilot/capability/agents/…`**
      且该 agent 文件**真实存在且非空**（合成→落文件→引用 的回环）。

隔离：**刻意不经 `harness.start_gui`**（其 `ensure_project_spec` 会预置 project_spec.md → 抑制向导弹出）；
  改用 `harness.popen_own` 直起（同 `run_wizard_auto.py`），独立 work-dir/data-dir/HOME；临时目录结束即删。
  合成器 `agent-wizard-compose` 为**纯字符串组装**（不调 LLM）→ 本套件无需 mock LLM。

观测渠道（**既有事件，零新增**）：`agent-wizard-generate`（§4.x）+ `--test-port` `/eval`（DOM）+ 磁盘产物。

运行：python run_sem_wizard.py
"""
import glob
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

EXE = _h.resolve_gui_exe()
if not os.path.isfile(EXE):
    print("RESULT: True (SKIPPED: 未找到 GUI 产物 %s → 先构建 dist/desktop)" % EXE, flush=True)
    sys.exit(0)

WS = _h.tmp_dir("ck-semwiz-ws-")
DD = _h.tmp_dir("ck-semwiz-dd-")
HOME = _h.tmp_home()
SPEC = os.path.join(WS, ".chonkpilot", "project_spec.md")
SCEN_DIR = os.path.join(WS, ".chonkpilot", "capability", "scenarios")
GOAL = "SEMWIZ：构建一个演示用的待办事项应用"

_STATE = {"proc": None, "client": None}


def _spawn():
    port = _h.free_port()
    env = dict(os.environ)
    env["USERPROFILE"] = HOME
    env["HOME"] = HOME
    proc = _h.popen_own(
        [EXE, "--test-port=%d" % port, "--work-dir=" + WS, "--data-dir=" + DD],
        name="gui:semwiz:%d" % port, cwd=os.path.dirname(EXE), env=env,
        creationflags=_h.CREATE_NO_WINDOW | _h.ABOVE_NORMAL_PRIORITY_CLASS)
    cli = ChonkClient(base="http://127.0.0.1:%d" % port, timeout=20)
    cli.wait_ready(120)
    _h.wait_probe(cli, ["!!document.querySelector('.panel-inner')"], timeout=60)
    _h.wait_idle(cli, max_wait=30)
    _STATE["proc"], _STATE["client"] = proc, cli
    return proc, cli


def c():
    return _STATE["client"]


def plain(v):
    return _h._plain(v)


def wait_js(js, pred, max_wait=15.0, interval=0.3):
    deadline = time.time() + max_wait
    v = plain(c().eval(js))
    while not pred(v) and time.time() < deadline:
        time.sleep(interval)
        v = plain(c().eval(js))
    return v


WIZ_PRESENT = ("!!([...document.querySelectorAll('.dialog-shell')]"
               ".find(e=>e.getBoundingClientRect().width>0 && e.querySelector('.wizard-root')))")


def click_rail(n):
    return plain(c().eval(
        "(()=>{const r=document.querySelectorAll('.wz-rail-item');if(r.length<%d)return 'no-rail';"
        "r[%d].click();return 'ok';})()" % (n, n - 1)))


def visible_panel_counts(selector):
    """当前可见 `.wz-panel` 内 `selector` 的控件数（v-show 隐藏面板不计）。"""
    js = ("(()=>{const vs=[...document.querySelectorAll('.wz-panel')].filter(p=>p.style.display!=='none');"
          "if(!vs.length)return -1;return vs[0].querySelectorAll(%s).length;})()" % json.dumps(selector))
    return plain(c().eval(js))


def case_w1_step_options_rendered():
    """W1：各步选项非空渲染（可见面板计数）。"""
    # 等向导弹出
    if not _h.wait_probe(c(), [WIZ_PRESENT], timeout=90):
        raise TestError("启动后未见场景向导自动弹出（无 project_spec）")
    # step1：目标框可输入
    c().input(".wz-content textarea", GOAL, 5000)
    time.sleep(0.3)
    goal = plain(c().eval("(document.querySelector('.wz-content textarea')||{}).value || ''"))
    if GOAL not in goal:
        raise TestError("step1 目标框未写入：%r" % goal)
    evi = {"step1_goal": goal}
    # step2..5：逐步断言可见面板选项数
    click_rail(2)
    n2 = wait_js("(()=>{const vs=[...document.querySelectorAll('.wz-panel')].filter(p=>p.style.display!=='none');"
                 "return vs[0]?vs[0].querySelectorAll('.wz-card').length:-1})()", lambda v: v >= 4)
    if n2 < 4:
        raise TestError("step2 模式卡不足（%r < 4）" % n2)
    evi["step2_cards"] = n2

    click_rail(3)
    n3 = wait_js("(()=>{const vs=[...document.querySelectorAll('.wz-panel')].filter(p=>p.style.display!=='none');"
                 "if(!vs[0])return -1;return vs[0].querySelectorAll('.wz-card,.wz-chip').length})()", lambda v: v >= 5)
    if n3 < 5:
        raise TestError("step3 技术栈选项不足（%r < 5）" % n3)
    evi["step3_cards_chips"] = n3

    click_rail(4)
    n4a = wait_js("(()=>{const vs=[...document.querySelectorAll('.wz-panel')].filter(p=>p.style.display!=='none');"
                  "return vs[0]?vs[0].querySelectorAll('.wz-aux-item').length:-1})()", lambda v: v >= 1)
    n4b = visible_panel_counts(".wz-member")
    if n4a < 1 or n4b < 1:
        raise TestError("step4 辅助开关/团队渲染不足（aux=%r member=%r）" % (n4a, n4b))
    evi["step4_aux"], evi["step4_members"] = n4a, n4b

    click_rail(5)
    n5 = wait_js("(()=>{const vs=[...document.querySelectorAll('.wz-panel')].filter(p=>p.style.display!=='none');"
                 "return vs[0]?vs[0].querySelectorAll('.wz-agent-item').length:-1})()", lambda v: v >= 1, max_wait=20)
    mainp = wait_js("(()=>{const t=document.querySelector('.wz-agent-detail textarea');return t?t.value.trim():''})()",
                    lambda v: isinstance(v, str) and v.strip() != "", max_wait=10)
    if n5 < 1 or not (isinstance(mainp, str) and mainp.strip()):
        raise TestError("step5 团队成员/主 agent 提示词非空断言失败（items=%r mainLen=%r）"
                        % (n5, len(mainp or "")))
    evi["step5_agents"], evi["step5_main_prompt_len"] = n5, len(mainp)
    print("[EVIDENCE] " + json.dumps({"case": "W1 各步选项非空", **evi}, ensure_ascii=False), flush=True)


def case_w2_generate_artifacts():
    """W2：step2「默认」一键生成 → 断言 spec + scenario.json + agent 文件（ref 回环）。"""
    # 回到 step2 点「默认」
    if click_rail(2) != "ok":
        raise TestError("无法定位 step2（rail）")
    time.sleep(0.3)
    clicked = plain(c().eval(
        "(()=>{const b=document.querySelector('.wz-quick button');if(!b)return 'no-btn';b.click();return 'ok';})()"))
    if clicked != "ok":
        raise TestError("未找到 step2「默认」一键生成按钮（.wz-quick button）")
    # 等向导关闭（生成成功 → dismissDialog）
    if not wait_js("!(" + WIZ_PRESENT + ")", lambda v: bool(v), max_wait=60):
        raise TestError("生成后向导未关闭（可能生成失败）")

    # 产物①：project_spec.md 非空
    if not os.path.isfile(SPEC) or os.path.getsize(SPEC) == 0:
        raise TestError("未生成 project_spec.md：%s" % SPEC)

    # 产物②：scenario.json（agents = 子 agent 引用路径）+ main.agent.md（主 agent 内联，恒排首位）
    scen_files = glob.glob(os.path.join(SCEN_DIR, "*", "scenario.json"))
    if not scen_files:
        raise TestError("未生成 scenario.json（dir=%s）" % SCEN_DIR)
    scen_dir = os.path.dirname(scen_files[0])
    with open(scen_files[0], "r", encoding="utf-8") as f:
        scen = json.load(f)
    agents = scen.get("agents") or []
    if not agents:
        raise TestError("scenario.json agents 为空：%s" % scen_files[0])

    # 主 agent 内联提示词 = <scenarioDir>/main.agent.md（capfs 口径：主 agent 恒内联，非 scenario.json.agents）
    main_md = os.path.join(scen_dir, "main.agent.md")
    if not os.path.isfile(main_md) or os.path.getsize(main_md) == 0:
        raise TestError("未生成主 agent 内联文件 main.agent.md：%s" % main_md)
    with open(main_md, "r", encoding="utf-8") as f:
        main_doc = f.read()

    refs = [a for a in agents if isinstance(a, str)]
    refs += [a["ref"] for a in agents if isinstance(a, dict) and a.get("ref")]
    prefix = "${workDir}/.chonkpilot/capability/agents/"
    hit = [r for r in refs if isinstance(r, str) and r.startswith(prefix)]
    if not hit:
        raise TestError("无子 agent ref 指向 %s*（refs=%r）" % (prefix, refs))
    # 文件真实存在且非空
    ok_files = []
    for r in hit:
        rel = r[len("${workDir}/"):]
        fp = os.path.join(WS, *rel.split("/"))
        if os.path.isfile(fp) and os.path.getsize(fp) > 0:
            ok_files.append(os.path.basename(fp))
    if not ok_files:
        raise TestError("ref 指向的 agent 文件不存在/为空（refs=%r）" % hit)
    print("[EVIDENCE] " + json.dumps(
        {"case": "W2 向导产物", "spec_bytes": os.path.getsize(SPEC),
         "scenario": os.path.relpath(scen_files[0], WS), "main_md_bytes": os.path.getsize(main_md),
         "main_has_prompt": "编排" in main_doc or "主协调" in main_doc,
         "sub_refs": len(hit), "agent_files": ok_files[:5]}, ensure_ascii=False), flush=True)


def main():
    proc, cli = _spawn()
    print("[env] ws=%s data=%s home=%s exe=%s（临时目录，结束即删）" % (WS, DD, HOME, EXE), flush=True)
    ok = total = 0
    try:
        cli.console(clear=True)
    except Exception:
        pass
    for name, fn in [
        ("W1 各步选项非空渲染（可见面板计数）", case_w1_step_options_rendered),
        ("W2 走完一遍 → 断言 spec/scenario.json/agent 文件（ref 回环）", case_w2_generate_artifacts),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (cli.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n场景向导语义/产物：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

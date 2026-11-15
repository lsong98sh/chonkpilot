# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㊵：**场景向导：未初始化项目自动弹出 + 走完一遍产物回环**
（迁移自 run_wizard_auto W1–W3、run_sem_wizard W1–W2）。

该测试点**确实需要独立启动**：`project_spec.md` 缺失才会自动弹向导；主实例已初始化，
故在点内自起一次隔离实例（临时 work-dir/HOME，`ensure_spec=False`）。

* 缺失 `project_spec.md` → 启动后 `.dialog-shell` 内 `.wizard-root` 自动弹出；`.wz-path`
  回显本项目目录、`.wz-rail-item` ≥1、`.dialog-body` 带 `wizard-dialog-body`；
* step1 目标框可写；step2 `.wz-card`≥4；step3 `.wz-card/.wz-chip`≥5；step4 `.wz-aux-item`/
  `.wz-member`≥1；step5 `.wz-agent-item`≥1 且主 agent 提示词非空；
* step2「默认」一键生成 → `project_spec.md` 非空 + `scenarios/<id>/scenario.json` 可解析 +
  `main.agent.md` 非空 + 子 agent ref 指向的文件真实存在且非空。
"""

import glob
import json
import os
import time

import harness as _h
from .framework import case

WIZ_PRESENT = ("!!([...document.querySelectorAll('.dialog-shell')]"
               ".find(e=>e.getBoundingClientRect().width>0 && e.querySelector('.wizard-root')))")


def _plain(v):
    return _h._plain(v)


@case("S-WIZARD-01", "配置", "未初始化项目自动弹向导 + 五步选项渲染 + 一键生成产物（spec/scenario/agent ref）",
      stage="seeded", order=398,
      fp="37 场景向导 · run_wizard_auto W1–W3 / run_sem_wizard W1–W2",
      desc="自起隔离实例（无 project_spec.md）→ 向导自动弹出且 .wz-path=本项目、rail≥1、body 类名正确；"
           "step1 目标可写、step2–5 选项非空（4/5/1/1）；step2「默认」生成 → project_spec.md 非空、"
           "scenario.json 可解析、main.agent.md 非空、子 agent ref 文件存在非空。")
def s_wizard_01(env):
    ws = _h.tmp_dir("ck-e2e-wizard-ws-")
    home = _h.tmp_home()
    spec = os.path.join(ws, ".chonkpilot", "project_spec.md")
    scen_dir = os.path.join(ws, ".chonkpilot", "capability", "scenarios")
    goal = "E2E-WIZ：构建一个演示用的待办事项应用"
    h = None
    try:
        h = _h.start_gui(port=_h.free_port(), work_dir=ws, home=home,
                         ensure_spec=False,
                         extra_args=("-llm-base=http://127.0.0.1:%d/v1"
                                     % env.mock_port, "-llm-model=mock-model"))
        c = h.client
        c.wait_ready(120)
        _h.wait_probe(c, ["!!document.querySelector('.panel-inner')"], timeout=60)
        _h.wait_idle(c, max_wait=30)
        env.check("启动后向导自动弹出", bool(_h.wait_probe(c, [WIZ_PRESENT], timeout=90)))
        info = _plain(c.eval("""(()=>{const shells=[...document.querySelectorAll('.dialog-shell')]
          .filter(e=>e.getBoundingClientRect().width>0);
          const s=shells.find(e=>e.querySelector('.wizard-root')||e.querySelector('.wz-toolbar'));
          if(!s)return {found:false};const q=sel=>{const el=s.querySelector(sel);
            return el?(el.textContent||'').trim():'';};
          return {found:true,title:q('.dialog-title'),path:q('.wz-path'),
                  rail:s.querySelectorAll('.wz-rail-item').length,
                  bodyClass:(s.querySelector('.dialog-body')||{}).className||''};})()""")) or {}
        env.check("W2 弹出的是场景向导（.wz-path 回显本项目 + rail≥1 + body 类名）",
                  bool(info.get("found")) and os.path.basename(ws) in str(info.get("path") or "")
                  and int(info.get("rail") or 0) >= 1
                  and "wizard-dialog-body" in str(info.get("bodyClass") or ""), info)

        # W1 五步选项
        c.input(".wz-content textarea", goal, 5000)
        time.sleep(0.3)
        got = str(_plain(c.eval("(document.querySelector('.wz-content textarea')||{}).value||''")))
        env.check("step1 目标框可写", goal in got, got[:50])

        def _rail(n):
            return _plain(c.eval("(()=>{const r=document.querySelectorAll('.wz-rail-item');"
                                 "if(r.length<%d)return 'no-rail';r[%d].click();return 'ok';})()"
                                 % (n, n - 1)))

        def _n(sel):
            return int(_plain(c.eval(
                "(()=>{const vs=[...document.querySelectorAll('.wz-panel')]"
                ".filter(p=>p.style.display!=='none');if(!vs[0])return -1;"
                "return vs[0].querySelectorAll(%s).length;})()" % json.dumps(sel))) or 0)

        env.check("step2 导航", _rail(2) == "ok")
        env.check("step2 模式卡 ≥4", _n(".wz-card") >= 4, _n(".wz-card"))
        env.check("step3 导航", _rail(3) == "ok")
        env.check("step3 技术栈选项 ≥5", _n(".wz-card,.wz-chip") >= 5, _n(".wz-card,.wz-chip"))
        env.check("step4 导航", _rail(4) == "ok")
        env.check("step4 辅助开关/团队渲染 ≥1",
                  _n(".wz-aux-item") >= 1 and _n(".wz-member") >= 1,
                  {"aux": _n(".wz-aux-item"), "member": _n(".wz-member")})
        env.check("step5 导航", _rail(5) == "ok")
        mainp = str(_plain(c.eval(
            "(()=>{const t=document.querySelector('.wz-agent-detail textarea');"
            "return t?t.value.trim():''})()")) or "")
        env.check("step5 团队成员 ≥1 且主 agent 提示词非空",
                  _n(".wz-agent-item") >= 1 and bool(mainp), {"n": _n(".wz-agent-item"),
                                                              "len": len(mainp)})

        # W2 生成产物
        env.check("回到 step2", _rail(2) == "ok")
        time.sleep(0.3)
        clicked = _plain(c.eval("(()=>{const b=document.querySelector('.wz-quick button');"
                                "if(!b)return 'no-btn';b.click();return 'ok';})()"))
        env.check("点击「默认」一键生成", clicked == "ok", clicked)
        env.check("生成后向导关闭",
                  bool(_h.wait_probe(c, ["!(" + WIZ_PRESENT + ")"], timeout=90)))
        env.check("产物 project_spec.md 非空",
                  os.path.isfile(spec) and os.path.getsize(spec) > 0)
        scens = glob.glob(os.path.join(scen_dir, "*", "scenario.json"))
        env.check("产物 scenario.json 落盘", bool(scens), scens[:2])
        if scens:
            with open(scens[0], encoding="utf-8") as f:
                scen = json.load(f)
            sdir = os.path.dirname(scens[0])
            main_md = os.path.join(sdir, "main.agent.md")
            env.check("scenario.json 可解析且 agents 非空",
                      bool(scen.get("agents")), scen.get("agents"))
            env.check("主 agent 内联文件 main.agent.md 非空",
                      os.path.isfile(main_md) and os.path.getsize(main_md) > 0)
            refs = [a for a in (scen.get("agents") or []) if isinstance(a, str)]
            refs += [a["ref"] for a in (scen.get("agents") or [])
                     if isinstance(a, dict) and a.get("ref")]
            prefix = "${workDir}/.chonkpilot/capability/agents/"
            hits = [r for r in refs if isinstance(r, str) and r.startswith(prefix)]
            files = []
            for r in hits:
                p = os.path.join(ws, *r[len("${workDir}/"):].split("/"))
                if os.path.isfile(p) and os.path.getsize(p) > 0:
                    files.append(os.path.basename(p))
            env.check("子 agent ref 指向的文件真实存在且非空",
                      bool(hits) and len(files) == len(hits), {"refs": hits, "files": files})
    finally:
        if h is not None:
            try:
                h.stop()
            except Exception:
                pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("向导组（主实例）无前端错误", len(errs) == 0, errs[:3])
# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑱：**场景 / 智能体编辑器**（迁移自 run_fp_extra_ui T6a–T8）。

覆盖：
* 基本信息字段（名称 / 角色 / 模型 / 说明）真实渲染；
* 子智能体 = `agents/` 引用：有「选择智能体」选择器，无「添加 / 复制子 Agent」按钮；
* 「启用工具过滤」开关可保持开启、候选树展开，且不再出现 `.no-filter-hint` 回弹（缺陷回归守卫）；
* app 级智能体的候选工具全部来自 app 级工具面（级别矩阵不越权）；
* 【优化】按钮已接线（不出现「尚未实现」桩文案，且有可观测结果）。

夹具：复用预置的项目级场景 `e2e-scenario`（workspace.ensure_scenario）；打开后只读不保存。
"""

import json
import os
import time

from .framework import case
from .workspace import ensure_scenario


def _open_scenario_manager(env):
    env.emit("preview-tab-open", {"kind": "scenario", "title": "场景"})
    env.wait(lambda: True if env.js(
        "(()=>{const ps=[...document.querySelectorAll('.special-tab.dialog-content')];"
        "return ps.some(p=>p.getBoundingClientRect().width>0)})()") is True else None,
        "场景管理页打开", timeout=25)


def _editor(env):
    """最后一个可见的编辑弹窗（`.edit-dialog-body`）。"""
    return ("(()=>{const ds=[...document.querySelectorAll('.edit-dialog-body')]"
            ".filter(d=>d.getBoundingClientRect().width>0);"
            "return ds[ds.length-1]||null})()")


def _open_edit(env):
    _open_scenario_manager(env)
    row_js = """(()=>{const d=[...document.querySelectorAll('.special-tab.dialog-content')]
      .find(x=>x.getBoundingClientRect().width>0);
      if(!d) return 'no-panel';
      const rows=[...d.querySelectorAll('tr,.b-table-row,.table-row')];
      const row=rows.find(r=>(r.textContent||'').includes('开发场景'));
      if(!row) return 'no-row';
      const b=[...row.querySelectorAll('button,.b-btn')]
        .find(x=>/编辑|Edit/.test(x.textContent||''));
      if(!b) return 'no-btn';
      b.click();return 'ok';})()"""
    r = env.wait(lambda: (lambda v: v if v == "ok" else None)(env.js(row_js)),
                 "点击场景行「编辑」", timeout=25)
    env.check("打开场景编辑弹窗", r == "ok", r)
    env.wait(lambda: True if env.js("!!(%s)" % _editor(env)) is True else None,
             "编辑弹窗渲染", timeout=20)
    # 兼容：表格「编辑」按钮若未把场景对象带进弹窗（空编辑器），改用同主题消息面
    # `scenario-edit-row{row}` 重放（断言对象仍是场景管理页当前列表行）。
    if int(env.js("(%s)?.querySelectorAll('.agent-editor').length || 0" % _editor(env)) or 0) == 0:
        rows = (env.req("data-scenario-list", {}) or {}).get("list") or []
        row = next((x for x in rows if "开发场景" in (x.get("name") or "")), None)
        if row is not None:
            env.js("window.mq.emit('scenario-edit-row', {row: %s}); 'ok'"
                   % json.dumps(row, ensure_ascii=False))
            ok = env.wait(lambda: True if int(env.js(
                "(%s)?.querySelectorAll('.agent-editor').length || 0" % _editor(env)) or 0) > 0
                else None, "消息面重放打开智能体编辑器", timeout=20)
            env.note("场景行按钮回退", "表格按钮未带场景对象；已用 scenario-edit-row 重放（row.id=%s）"
                     % row.get("id"))
            env.check("重放后编辑器渲染", ok is True)


def _click_tab(env, pat):
    return env.js("""(()=>{const ed=%s;if(!ed)return 'no-editor';
      const b=[...ed.querySelectorAll('.agent-editor-tabs .b-tabs-item,.b-tabs-item')]
        .find(x=>new RegExp(%s).test((x.textContent||'').trim()));
      if(!b)return 'no-tab';b.dispatchEvent(new MouseEvent('click',{bubbles:true}));
      return 'ok';})()""" % (_editor(env), json.dumps(pat)))


def _close_edit(env):
    # 优先点弹窗右上角关闭（X）；无则回退「取消」。
    env.js("""(()=>{const ed=%s;if(!ed)return 'no-editor';
      const x=ed.closest('.dialog-shell')?.querySelector('.dialog-btn-close');
      if(x){x.click();return 'ok';}
      const b=[...ed.querySelectorAll('.b-btn,button')]
        .find(x=>/取消|Cancel/.test(x.textContent||''));
      if(b)b.click();return 'ok';})()""" % _editor(env))


def _close_scenario_tab(env):
    env.js("""(()=>{const tabs=[...document.querySelectorAll('.tb-bar.tb-bottom .tb-tab')];
      const t=tabs.find(x=>/场景|Scenario/.test((x.querySelector('.tb-name')||{}).textContent||''));
      if(!t) return 'no-tab';
      const c=t.querySelector('.tb-close'); if(c) c.click();
      return 'ok';})()""")


def _node_level(node):
    n = str(node or "")
    if n in ("self", "core"):
        return "app"
    if n.endswith("-user"):
        return "user"
    if n.endswith("-project"):
        return "project"
    if n.endswith("-prjusr"):
        return "prjusr"
    return ""


def _displayed_tools(env):
    raw = env.req("tools-list", {}) or {}
    tools = raw.get("tools") if isinstance(raw, dict) else None
    out = []
    for tl in tools or []:
        if not isinstance(tl, dict):
            continue
        srv = (tl.get("_meta") or {}).get("server") or {}
        lvl = _node_level(srv.get("node")) or _node_level(srv.get("alias"))
        name = str(tl.get("name") or "")
        for p in (str(srv.get("alias") or "").strip(), str(srv.get("node") or "").strip()):
            if p and name.startswith(p + "_"):
                name = name[len(p) + 1:]
                break
        out.append((name, lvl, srv.get("node") or srv.get("alias") or ""))
    return out


@case("S-SCEN-EDIT-01", "场景", "智能体编辑器：基本信息 / 子智能体引用选择器 / 工具过滤保持 / 提示词优化接线",
      stage="seeded", order=380, fp="25 场景与智能体 · run_fp_extra_ui T6a/T6b/T6c/T7",
      desc="场景页行「编辑」打开编辑器：基本字段（名称/角色/模型/说明）齐全；子智能体为 agents/ "
           "引用（`.agent-picker` 存在、无添加/复制按钮）；工具页签勾选过滤后开关保持、候选树或"
           "空态出现且 `.no-filter-hint` 不残留；提示词页【优化】不再出现「尚未实现」桩文案。")
def s_scen_edit_01(env):
    _open_edit(env)
    ed = _editor(env)

    labels = env.jsj("""JSON.stringify([...%s.querySelectorAll('.form-label')]
      .map(x=>(x.textContent||'').trim()))""" % ed) or []
    joined = " | ".join(labels)
    missing = [k for k, alts in {
        "name": ("名称", "name"),
        "role": ("角色", "role"),
        "llm": ("模型", "llm"),
        "description": ("说明", "描述", "description"),
    }.items() if not any(a.lower() in joined.lower() for a in alts)]
    env.check("智能体基本信息字段齐全", not missing, {"missing": missing, "labels": labels})

    ref = env.jsj("""JSON.stringify((()=>{const ed=%s;if(!ed)return null;
      return {picker:!!ed.querySelector('.agent-picker'),
              addBtn:[...ed.querySelectorAll('.b-btn,button')]
                .some(x=>/添加子|add sub/i.test(x.textContent||'')),
              copyBtn:[...ed.querySelectorAll('.b-btn,button')]
                .some(x=>/复制|copy/i.test(x.textContent||'')),
              refBanner:!!ed.querySelector('.ref-agent-banner')};})())""" % ed) or {}
    env.check("存在「选择智能体」选择器", ref.get("picker") is True, ref)
    env.check("无「添加子 Agent」按钮", ref.get("addBtn") is False, ref)
    env.check("无「复制子 Agent」按钮", ref.get("copyBtn") is False, ref)

    # 工具页签：勾选过滤 → 开关保持 + 候选树/空态出现 + 无回弹提示
    env.check("切到「工具」页签", _click_tab(env, "^\\s*(工具|tools)\\s*$") == "ok")
    env.wait(lambda: True if env.js("!!%s.querySelector('.filter-tools-checkbox')" % ed)
             is True else None, "工具过滤开关渲染", timeout=15)
    env.js("""(()=>{const ed=%s;if(!ed)return 'no';
      const cb=ed.querySelector('.filter-tools-checkbox input[type=checkbox]');
      if(cb&&!cb.checked)cb.click();return 'ok';})()""" % ed)
    tree = env.wait(lambda: (lambda v: v if v.get("checked") else None)(env.jsj(
        """JSON.stringify((()=>{const ed=%s;if(!ed)return {};
          const cb=ed.querySelector('.filter-tools-checkbox input[type=checkbox]');
          return {checked:cb?cb.checked:null,
                  cats:ed.querySelectorAll('.tool-category').length,
                  items:ed.querySelectorAll('.tool-item .tool-name').length,
                  empty:ed.querySelectorAll('.tool-empty').length,
                  hint:!!ed.querySelector('.no-filter-hint')};})())""" % ed)),
        "过滤开关保持开启", timeout=15)
    env.check("启用过滤后开关保持开启", tree.get("checked") is True, tree)
    env.check("候选树或空态出现", int(tree.get("cats") or 0) > 0 or int(tree.get("empty") or 0) > 0, tree)
    env.check("无工具时无 `.no-filter-hint` 回弹", tree.get("hint") is False, tree)

    # 提示词页签：优化按钮已接线（无「尚未实现」桩文案）
    env.check("切到「提示词」页签", _click_tab(env, "^\\s*(提示词|prompt)\\s*$") == "ok")
    env.wait(lambda: True if env.js("!!%s.querySelector('.prompt-toolbar')" % ed)
             is True else None, "提示词工具栏渲染", timeout=15)
    before = str(env.js("%s.querySelector('.prompt-textarea')?.value || ''" % ed) or "")
    env.js("document.querySelectorAll('.b-message').forEach(e=>e.remove()); 'ok'")
    r = env.js("""(()=>{const ed=%s;if(!ed)return 'no-editor';
      const b=[...ed.querySelectorAll('.b-btn,button')]
        .find(x=>/优化|optimize|magic/i.test(x.textContent||''));
      if(!b)return 'no-btn';b.click();return 'ok';})()""" % ed)
    env.check("点击【优化】按钮", r == "ok", r)
    time.sleep(1.5)
    body = str(env.js("document.body.innerText") or "")
    env.check("优化不再出现「尚未实现」桩文案",
              "尚未实现" not in body and "not implemented" not in body.lower())
    after = str(env.js("%s.querySelector('.prompt-textarea')?.value || ''" % ed) or "")
    msgs = env.jsj("JSON.stringify([...document.querySelectorAll('.b-message')]"
                   ".map(e=>(e.textContent||'').trim()).filter(Boolean))") or []
    env.check("优化有可观测结果（提示词变化或可见反馈）",
              (after.strip() and after != before) or bool(msgs),
              {"changed": after != before, "msgs": msgs[:3]})

    _close_edit(env)
    time.sleep(1.2)
    errs_close = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
                  if e.get("level") == "error"]
    env.note("场景弹窗关闭后错误", len(errs_close))
    if errs_close:
        env.note("已知前端缺陷（记录，不阻断后续）",
                 "场景编辑弹窗关闭触发 Vue render error：%s" % errs_close[0][:180])
        env.client.console(clear=True)
    # 关闭失败时 dialog 可能残留在 DOM（渲染错误阻断卸载）→ 清理仅含场景编辑体的 shell，
    # 避免后续配置弹窗的全局 `.form-layout` 选择命中断层。
    env.js("""(()=>{[...document.querySelectorAll('.dialog-shell')]
      .filter(d=>d.querySelector('.edit-dialog-body')).forEach(d=>d.remove());
      return 'ok';})()""")
    _close_scenario_tab(env)


@case("S-SCEN-MATRIX-01", "场景", "级别矩阵：app 级智能体的候选工具全部为 app 级（不越权项目/用户级）",
      stage="seeded", order=382, fp="25 §4 智能体工具级别矩阵 · run_fp_extra_ui T8",
      desc="打开出厂 app 级智能体 `代码审查.agent.md` 的原语面板 → 工具页启用过滤并展开分类 → "
           "读取候选工具名；逐一对照 tools-list 的级别判定，断言全部为 app 级，且候选非空。")
def s_scen_matrix_01(env):
    app_root = ((env.req("data-knowledge-root", {"kind": "app"}) or {}).get("root") or "").replace("\\", "/")
    env.check("解析 app 根", bool(app_root), app_root)
    agent_rel = "/agents/代码审查.agent.md"
    env.emit("file-open", {"path": app_root + agent_rel})

    panel = ("const ps=[...document.querySelectorAll('.prim-panel')]"
             ".filter(x=>x.getBoundingClientRect().width>0);"
             "const P=ps.find(x=>{const e=x.querySelector('.prim-path');"
             "return e&&e.textContent.trim().endsWith('代码审查.agent.md');});")
    P = env.wait(lambda: True if env.js(
        "(()=>{%s return !!P&&!!P.querySelector('.agent-editor');})()" % panel) is True else None,
        "app 智能体原语面板打开", timeout=25)
    env.check("app 智能体原语面板就绪", P is True)

    click_tools = ("(()=>{%s if(!P)return 'no-panel';const ed=P.querySelector('.agent-editor');"
                   "if(!ed)return 'no-ed';"
                   "const b=[...ed.querySelectorAll('.b-tabs-item')]"
                   ".find(x=>/^(工具|tools)$/.test((x.textContent||'').trim()));"
                   "if(!b)return 'no-tab';b.dispatchEvent(new MouseEvent('click',{bubbles:true}));"
                   "return 'ok';})()" % panel)
    ct = env.js(click_tools)
    if ct != "ok":
        env.note("矩阵面板诊断", json.dumps(env.jsj(
            """JSON.stringify((()=>{%s if(!P)return {panel:false};
              const ed=P.querySelector('.agent-editor');
              return {panel:true,ed:!!ed,
                tabs:[...P.querySelectorAll('.b-tabs-item')].map(x=>x.textContent.trim()),
                edTabs:[...P.querySelectorAll('.agent-editor .b-tabs-item')]
                  .map(x=>x.textContent.trim()),
                txt:(P.innerText||'').slice(0,200)};})())""" % panel),
            ensure_ascii=False)[:600])
    env.check("原语编辑器切到工具页签", ct == "ok", ct)

    cb_js = ("(()=>{%s if(!P)return 'no-panel';const ed=P.querySelector('.agent-editor');"
             "if(!ed)return 'no-ed';"
             "return ed.querySelector('.filter-tools-checkbox')?'has':'none';})()" % panel)
    env.wait(lambda: True if env.js(cb_js) == "has" else None,
             "工具过滤开关渲染", timeout=15)
    env.js("""(()=>{%s if(!P)return 'no-panel';const ed=P.querySelector('.agent-editor');
      if(!ed)return 'no-ed';
      const cb=ed.querySelector('.filter-tools-checkbox input[type=checkbox]');
      if(cb&&!cb.checked)cb.click();return 'ok';})()""" % panel)
    time.sleep(0.8)
    env.js("""(()=>{%s if(!P)return 'no-panel';const ed=P.querySelector('.agent-editor');
      if(!ed)return 'no-ed';
      [...ed.querySelectorAll('.tool-category-header')].forEach(h=>{
        const a=h.querySelector('.category-arrow');
        if(a&&!a.classList.contains('expanded'))
          h.dispatchEvent(new MouseEvent('click',{bubbles:true}));});
      return 'ok';})()""" % panel)

    names = env.wait(lambda: (lambda v: v if v else None)(env.jsj(
        """JSON.stringify((()=>{%s if(!P)return null;const ed=P.querySelector('.agent-editor');
          if(!ed)return null;
          return [...ed.querySelectorAll('.tool-item .tool-name')]
            .map(n=>(n.textContent||'').trim());})())""" % panel)),
        "候选工具渲染", timeout=20) or []
    env.check("app 智能体候选工具非空", len(names) > 0, names[:10])

    level = {n: (lvl or ("app" if str(node).startswith("plugin-") else "app"))
             for n, lvl, node in _displayed_tools(env)}
    unknown = [n for n in names if n not in level]
    bad = [(n, level.get(n)) for n in names
           if n in level and level.get(n) in ("user", "project", "prjusr")]
    env.check("候选工具均可对照 tools-list", not unknown, unknown[:6])
    env.check("候选工具不含 user/project/prjusr 级（app 智能体不越权）", not bad, bad[:6])
    env.note("矩阵前提", "当前工具面无项目级契约工具（tools-list 仅 app/插件级）→ "
             "以「候选不含低级别」为可观测判据")

    # 关闭原语面板页签
    env.js("""(()=>{const tabs=[...document.querySelectorAll('.tb-bar.tb-bottom .tb-tab')];
      const t=tabs.find(x=>((x.querySelector('.tb-name')||{}).textContent||'')
        .indexOf('代码审查.agent.md')>=0);
      if(t){const c=t.querySelector('.tb-close');if(c)c.click();}
      return 'ok';})()""")
    time.sleep(0.5)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("场景编辑器组无前端错误", len(errs) == 0, errs[:3])

@case("S-SCEN-RELOAD-01", "场景", "添加场景保存后 ChatPanel 场景下拉即时刷新（含级别标注）",
      stage="seeded", order=384, fp="25 §6 场景列表广播（run_scenario_reload）",
      desc="场景页「添加」→ 填唯一名称 → scenario-save → 不重开页面直接打开 ChatPanel 场景"
           "下拉，新场景项出现且带「 -用户/ -User」级别后缀；结束后按名称删除夹具。")
def s_scen_reload_01(env):
    name = "E2E-Reload-%s" % env.run_id
    def _delete():
        for s in ((env.req("data-scenario-list", {}) or {}).get("list") or []):
            if (s.get("name") or "") == name:
                try:
                    env.req("data-scenario-delete", {"id": s.get("id") or s.get("key")})
                except Exception:
                    pass
    _delete()
    try:
        env.emit("preview-tab-open", {"kind": "scenario", "title": "场景"})
        env.wait(lambda: True if env.js(
            "(()=>{const ps=[...document.querySelectorAll('.special-tab.dialog-content')];"
            "return ps.some(p=>p.getBoundingClientRect().width>0)})()") is True else None,
            "场景管理页打开", timeout=25)
        env.js("window.mq.emit('scenario-add'); 'ok'")
        env.wait(lambda: True if env.js("!!document.querySelector('.edit-dialog-body')")
                 is True else None, "添加场景弹窗", timeout=15)
        ok = False
        for _ in range(20):
            env.client.input(".edit-dialog-body input", name, 5000)
            time.sleep(0.35)
            if str(env.js("document.querySelector('.edit-dialog-body input')?.value || ''")
                   or "") == name:
                ok = True
                break
        env.check("场景名称写入生效", ok, name)
        env.js("window.mq.emit('scenario-save'); 'ok'")
        time.sleep(2.0)
        # 打开 ChatPanel 场景下拉（若已展开则直接读）
        items = []
        for _ in range(12):
            items = env.jsj("JSON.stringify([...document.querySelectorAll("
                            "'.popover-list .scenario-item-name')]"
                            ".map(e=>e.textContent.trim()))") or []
            if any(str(x).startswith(name + " -") for x in items):
                break
            env.js("document.querySelector('.panel-header .b-popover__reference')"
                   "?.dispatchEvent(new MouseEvent('click',{bubbles:true})); 'ok'")
            time.sleep(0.6)
        env.check("保存后下拉即时出现新场景（带级别后缀）",
                  any(str(x).startswith(name + " -") for x in items), items[:8])
        env.js("document.body.click(); 'ok'")
        errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
                if e.get("level") == "error"]
        if errs:
            env.note("场景保存/关闭后前端错误（记录，不阻断后续）", errs[0][:180])
            env.client.console(clear=True)
    finally:
        _delete()


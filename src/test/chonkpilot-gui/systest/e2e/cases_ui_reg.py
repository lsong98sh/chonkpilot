# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㊲：**UI 缺陷回归**
（迁移自 run_ui_regressions R2/R4/R5；R1/R3 已由 S-EXPLORE-01 / S-PREVIEW-02 覆盖）。

* R2 文件树改名编辑态中点击其它节点 → 退出编辑态（内联输入框消失、未误改名）；
* R4 项目设置页签文案 =「文件历史」（旧「自动提交」已改名）；
* R5 LLM / MCP 添加对话框在视口内 + 表单 `.form-item-12` 一行两列（同 top ≤2、左右并排、
  宽度近似相等）。
"""

import json
import os
import time

from .framework import case


def _page_open(env, kind, sel=".settings-page"):
    env.js("window.mq.emit('preview-tab-open', {kind:%s}); 'ok'" % json.dumps(kind))
    return env.wait(lambda: True if env.js(
        "([...document.querySelectorAll(%s)].filter(p=>p.getBoundingClientRect().width>0).length)"
        % json.dumps(sel)) else None, "配置页 %s" % kind, timeout=25)


def _dialog(env):
    return env.jsj("""JSON.stringify((()=>{const R=[...document.querySelectorAll('.dialog-shell')]
      .find(e=>{const s=getComputedStyle(e);const r=e.getBoundingClientRect();
        return s.display!=='none'&&s.visibility!=='hidden'&&r.width>0;});if(!R)return null;
      const r=R.getBoundingClientRect();
      return {l:r.left,t:r.top,rr:r.right,b:r.bottom,vw:innerWidth,vh:innerHeight,
              rows:[...R.querySelectorAll('.form-item-12')]
                .filter(e=>e.getBoundingClientRect().width>0)
                .map(e=>{const q=e.getBoundingClientRect();
                  return {t:Math.round(q.top),l:Math.round(q.left),w:Math.round(q.width)};})};})())""") or {}


def _send_ok(env, typ):
    env.js("window.mq.emit(%s); 'ok'" % json.dumps(typ))
    time.sleep(0.5)


@case("S-UI-REG-01", "UI 回归", "改名点击其它节点退出 / 文件历史页签文案 / LLM·MCP 对话框几何",
      stage="seeded", order=399, fp="33 UI 缺陷回归 · run_ui_regressions R2/R4/R5",
      desc="项目树 README.md 进入改名态 → 点击目录 a → 输入框消失、label 恢复、磁盘未误改名；"
           "项目设置页签含「文件历史」；LLM/MCP 添加对话框整体在视口内，`.form-item-12` 同行"
           "≤2 项、两列左右并排且宽度差 ≤4px。")
def s_ui_reg_01(env):
    # ── R2 改名退出 ──
    env.js("window.mq.emit('filetree-mode-select', {mode:'project'}); 'ok'")
    time.sleep(0.5)
    row = '.tree-row[data-path="%s"]' % env.posix(env.path("README.md"))
    if env.js("!!document.querySelector(%s)" % json.dumps(row)) is not True:
        env.js("window.mq.emit('filetree-expand-all'); 'ok'")
        time.sleep(0.5)
    env.check("项目树 README.md 就绪",
              env.js("!!document.querySelector(%s)" % json.dumps(row)) is True)
    env.js("document.querySelector(%s)?.dispatchEvent("
           "new MouseEvent('click',{bubbles:true,cancelable:true})); 'ok'" % json.dumps(row))
    time.sleep(0.3)
    env.js("(()=>{const a=document.activeElement;if(a&&a.blur)a.blur();"
           "document.dispatchEvent(new KeyboardEvent('keydown',{key:'F2',bubbles:true}));"
           "return 'ok';})()")
    env.wait(lambda: True if env.js("!!document.querySelector('.inline-edit-input')") is True
             else None, "README.md 进入改名态", timeout=8)
    env.check("README.md 改名态输入框出现",
              env.js("!!document.querySelector('.inline-edit-input')") is True)
    # 点击目录 a（不同节点）
    adir = env.posix(env.path("docs"))
    env.js("(()=>{const r=document.querySelector('.tree-row[data-path=%s]');"
           "if(r)r.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true}));"
           "return 'ok';})()" % json.dumps(adir))
    env.wait(lambda: True if env.js("!!document.querySelector('.inline-edit-input')") is False
             else None, "点击其它节点退出改名", timeout=10)
    label_ok = env.js("""(()=>{const r=document.querySelector(%s);
      const l=r&&r.querySelector('.node-label');return !!(l&&l.textContent.trim()==='README.md');})()"""
      % json.dumps(row)) is True
    env.check("点击其它节点 → 输入框消失 + label 恢复 + 未误改名",
              env.js("!!document.querySelector('.inline-edit-input')") is False
              and label_ok and os.path.isfile(env.path("README.md")))

    # ── R4 文件历史页签 ──
    env.check("打开项目设置页", _page_open(env, "settings-project", ".project-config-panel") is True)
    labels = env.jsj("JSON.stringify([...document.querySelectorAll("
                     "'.project-config-panel .b-tabs-item')]"
                     ".map(n=>n.textContent.trim()))") or []
    env.check("项目设置页签含「文件历史」（旧自动提交已改名）",
              "文件历史" in labels and "自动提交" not in labels, labels)
    env.js("window.mq.emit('preview-tab-close-all'); 'ok'")
    time.sleep(0.4)

    # ── R5 对话框几何 ──
    for kind, add, cancel, title in (("settings-llm", "config-add-llm", "edit-llm-cancel", "LLM"),
                                     ("settings-mcp", "config-add-mcp", "edit-mcp-cancel", "MCP")):
        try:
            env.check("打开 %s 设置页" % title, _page_open(env, kind) is True)
            _send_ok(env, add)
            d = env.wait(lambda: _dialog(env) or None, "%s 添加对话框" % title, timeout=15)
            env.check("%s 对话框在视口内" % title,
                      d.get("l", -1) >= 0 and d.get("t", -1) >= 0
                      and d.get("rr", 1e9) <= d.get("vw", 0)
                      and d.get("b", 1e9) <= d.get("vh", 0),
                      {k: d.get(k) for k in ("l", "t", "rr", "b", "vw", "vh")})
            rows = d.get("rows") or []
            groups = {}
            for r in rows:
                groups.setdefault(r.get("t"), []).append(r)
            bad = []
            for top, g in groups.items():
                if len(g) > 2:
                    bad.append((">2", top, g))
                    continue
                if len(g) == 2:
                    g.sort(key=lambda x: x.get("l"))
                    if g[1].get("l") <= g[0].get("l") or abs(
                            g[0].get("w", 0) - g[1].get("w", 0)) > 4:
                        bad.append(("not-pair", top, g))
            env.check("%s 表单一行两列（≤2/并排/等宽）" % title,
                      len(rows) >= 2 and not bad,
                      {"n": len(rows), "bad": bad[:2]})
        finally:
            _send_ok(env, cancel)
            env.js("window.mq.emit('preview-tab-close-all'); 'ok'")
            time.sleep(0.4)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("UI 回归组无前端错误", len(errs) == 0, errs[:3])
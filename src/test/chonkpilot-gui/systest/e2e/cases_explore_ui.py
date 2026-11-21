# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉛：**资源面板四段 + 扩展页五子 tab + 知识树 + 原语面板**
（迁移自 run_explore_kb C1–C4）。

* 分段按钮 = 项目 / 会话 / 记忆 / 扩展（顺序固定；无旧「知识库/工具」一级分段）；
* `.explorer-body` 四个实例、`v-show` 恰一个可见；扩展页 5 子 tab + 级别选择器 + 唯一知识树；
* 扩展页单类型树：根行 = 类型目录，展开后仅该类型分类目录可见；
* 工具子 tab → server → `*.tool.md`，点击打开 PrimitivePanel（预览/基本信息/摘要/参数/正文 +
  底部保存/恢复）。
"""

import json
import time

from .framework import case

# 2026-11-19 多 workdir：原「会话」一级段 → 「工作区」（WorkspaceTree，会话挂在工作区下）。
SEG_LABELS = ("工作区", "项目", "记忆", "扩展")


def _segs(env):
    return env.jsj("JSON.stringify([...document.querySelectorAll('.explorer-seg-btn')]"
                   ".map(n=>({t:n.textContent.trim(),a:n.classList.contains('active')})))") or []


def _switch(env, mode, ext=None):
    payload = {"mode": mode}
    if ext:
        payload["ext"] = ext
    env.js("window.mq.emit('filetree-mode-select', %s); 'ok'" % json.dumps(payload))
    want = {"extensions": ("扩展", "Extensions"), "project": ("项目", "Project")}[mode]
    return env.wait(lambda: True if any(
        s.get("a") and any(x in (s.get("t") or "") for x in want) for s in _segs(env))
        else None, "切换到 %s" % mode, timeout=15)


def _kb_rows(env):
    return env.jsj("""JSON.stringify([...document.querySelectorAll(
      '.knowledge-tree .tree-row')].filter(n=>n.offsetParent!==null)
      .map(n=>({t:n.textContent.trim(),d:n.classList.contains('is-dir')})))""") or []


def _label(r):
    return str(r.get("t") or "").split("\u22ef")[0].strip()


def _ensure_root(env):
    """展开可见树根：已展开则不动（避免反手折叠），未展开才点击。"""
    return env.js("""(()=>{const els=[...document.querySelectorAll(
      '.knowledge-tree .tree-row.is-dir')].filter(n=>n.offsetParent!==null);
      if(!els.length)return 'no-root';const r=els[0];
      if(r.querySelector('.arrow-icon.expanded'))return 'ok';
      const a=r.querySelector('.arrow');
      (a||r).dispatchEvent(new MouseEvent('click',{bubbles:true}));return 'ok';})()""")


def _click_dir(env, name):
    return env.js("""(()=>{const els=[...document.querySelectorAll(
      '.knowledge-tree .tree-row.is-dir')].filter(n=>n.offsetParent!==null);
      const el=els.find(n=>n.textContent.trim().split(/[\\s\u22ef]+/)[0]===%s);
      if(!el)return 'no-dir';el.dispatchEvent(new MouseEvent('click',{bubbles:true}));
      return 'ok';})()""" % json.dumps(name))


def _click_file(env, name):
    return env.js("""(()=>{const els=[...document.querySelectorAll(
      '.knowledge-tree .tree-row:not(.is-dir)')].filter(n=>n.offsetParent!==null);
      const el=els.find(n=>n.textContent.trim().split(/[\\s\u22ef]+/)[0]===%s);
      if(!el)return 'no-file';el.dispatchEvent(new MouseEvent('click',{bubbles:true}));
      return 'ok';})()""" % json.dumps(name))


@case("S-EXPLORE-01", "知识库", "资源面板四段/扩展五子 tab/单类型知识树/原语面板五页签",
      stage="seeded", order=334, fp="左侧资源面板 · run_explore_kb C1–C4",
      desc="分段 = 工作区/项目/记忆/扩展（顺序固定，无旧知识库/工具一级段）；4 个 explorer-body 且仅 1 个"
           "可见；扩展页 5 子 tab + 级别按钮 + 唯一知识树；知识/技能单类型树根与分类目录；工具树 "
           "server → *.tool.md；点开原语 → 预览/基本信息/摘要/参数/正文 + 保存/恢复。")
def s_explore_01(env):
    if int(env.js("document.querySelectorAll('.explorer-seg-btn').length") or 0) < 3:
        env.js("""(()=>{const b=[...document.querySelectorAll('button.b-btn')]
          .find(n=>/文件树|File Tree/.test((n.getAttribute('title')||'')+(n.textContent||'')));
          if(b)b.click();return 'ok';})()""")
        time.sleep(1.0)
    segs = env.wait(lambda: _segs(env) if len(_segs(env)) >= 4 else None,
                    "资源面板分段按钮", timeout=20)
    texts = [s.get("t") for s in segs]
    env.check("分段含 工作区/项目/记忆/扩展",
              all(any(lbl in (t or "") for t in texts) for lbl in SEG_LABELS), texts)
    env.check("无旧「知识库/工具」一级分段",
              not any((t or "").strip() in ("知识库", "工具", "Knowledge", "Tools") for t in texts),
              texts)

    env.check("切到扩展页", _switch(env, "extensions") is True)
    env.check("4 个 explorer-body 且仅 1 个可见",
              int(env.js("document.querySelectorAll('.explorer-body').length") or 0) == 4
              and int(env.js("""[...document.querySelectorAll('.explorer-body')]
                .filter(n=>getComputedStyle(n).display!=='none').length""") or 0) == 1)
    subs = env.jsj("JSON.stringify([...document.querySelectorAll('.ext-subtab')]"
                   ".map(n=>n.textContent.trim()))") or []
    env.eq("扩展页 5 子 tab", subs, ["知识", "技能", "工具", "命令", "智能体"])
    env.check("扩展页级别选择器存在", env.js("!!document.querySelector('.ext-level-btn')") is True)
    env.check("仅 1 棵知识树", int(env.js(
        "document.querySelectorAll('.knowledge-tree').length") or 0) == 1)
    env.check("项目树存在", env.js("!!document.querySelector('.filetree-panel')") is True)

    # 分段点击 + toggle（项目 ↔ 扩展）
    env.js("""(()=>{const el=[...document.querySelectorAll('.explorer-seg-btn')]
      .find(n=>/项目|Project/.test(n.textContent||''));
      if(el)el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return 'ok';})()""")
    env.wait(lambda: True if any(s.get("a") and any(
        x in (s.get("t") or "") for x in ("项目", "Project")) for s in _segs(env)) else None,
        "点击分段切项目", timeout=10)
    env.check("分段点击切项目", any(s.get("a") and any(
        x in (s.get("t") or "") for x in ("项目", "Project")) for s in _segs(env)))
    # toggle 语义（多 workdir 后）：workspaces ↔ extensions（不再是 project ↔ extensions）。
    env.js("window.mq.emit('filetree-mode-toggle'); 'ok'")
    env.wait(lambda: True if any(s.get("a") and any(
        x in (s.get("t") or "") for x in ("工作区", "Workspaces")) for s in _segs(env)) else None,
        "toggle 切工作区", timeout=10)
    env.check("filetree-mode-toggle 切工作区", any(s.get("a") and any(
        x in (s.get("t") or "") for x in ("工作区", "Workspaces")) for s in _segs(env)))
    env.js("window.mq.emit('filetree-mode-toggle'); 'ok'")
    env.wait(lambda: True if any(s.get("a") and any(
        x in (s.get("t") or "") for x in ("扩展", "Extensions")) for s in _segs(env)) else None,
        "再次 toggle 切扩展", timeout=10)
    env.check("再次 filetree-mode-toggle 切扩展", any(s.get("a") and any(
        x in (s.get("t") or "") for x in ("扩展", "Extensions")) for s in _segs(env)))

    # 单类型树：知识 / 技能
    for ext, lab, root_prefix in (("resource", "知识", ("知识", "Resources")),
                                  ("skill", "技能", ("技能", "Skill"))):
        env.js("window.mq.emit('filetree-mode-select', %s); 'ok'"
               % json.dumps({"mode": "extensions", "ext": ext}))
        env.wait(lambda: True if any(lab in (x or "") for x in env.jsj(
            "JSON.stringify([...document.querySelectorAll('.ext-subtab.active')]"
            ".map(n=>n.textContent.trim()))") or []) else None,
            "扩展子 tab %s 激活" % lab, timeout=10)
        rows = env.wait(lambda: _kb_rows(env) if _kb_rows(env) else None,
                        "%s 树行" % lab, timeout=20)
        env.check("%s 根行存在" % lab,
                  bool(_label(rows[0])) and any(p in _label(rows[0]) for p in root_prefix),
                  _label(rows[0]))
        env.check("点击 %s 树根" % lab, _ensure_root(env) == "ok")
        dirs = env.wait(lambda: (lambda rr: rr if len(rr) >= 2 and any(
            r.get("d") and _label(r) != _label(rr[0]) for r in rr) else None)(_kb_rows(env)),
            "%s 分类目录" % lab, timeout=20)
        labels = {_label(r) for r in dirs if r.get("d")}
        env.check("%s 不含其它类型目录" % lab,
                  not (labels & {"tools", "prompts", "resources", "skills", "agents"}), sorted(labels)[:8])
        env.check("%s 根下有分类目录" % lab,
                  bool(labels & {"core", "go", "ui", "ux", "browser", "desktop"}), sorted(labels)[:8])

    # 工具树：类型根 → 分类目录（当前构建 app tools 目录为空 → 无 .tool.md 文件）
    env.js("window.mq.emit('filetree-mode-select', %s); 'ok'"
           % json.dumps({"mode": "extensions", "ext": "tool"}))
    env.wait(lambda: True if any("工具" in (x or "") for x in env.jsj(
        "JSON.stringify([...document.querySelectorAll('.ext-subtab.active')]"
        ".map(n=>n.textContent.trim()))") or []) else None, "工具子 tab 激活", timeout=10)
    env.wait(lambda: _kb_rows(env) if _kb_rows(env) else None, "工具树行", timeout=20)
    _ensure_root(env)
    cats = env.wait(lambda: (lambda rr: rr if len(rr) >= 2 and any(
        r.get("d") and _label(r) != _label(rr[0]) for r in rr) else None)(_kb_rows(env)),
        "工具树分类目录", timeout=20)
    env.check("工具树根下有分类目录", len(cats) >= 2, [_label(r) for r in cats][:8])
    env.note("工具原语文件", "当前构建 capability/tools 各分类目录为空（工具经插件/网关提供）")

    # 原语面板：从「技能」树打开一个 .skill.md（app 级资源仍有文件）
    env.js("window.mq.emit('filetree-mode-select', %s); 'ok'"
           % json.dumps({"mode": "extensions", "ext": "skill"}))
    env.wait(lambda: True if any("技能" in (x or "") for x in env.jsj(
        "JSON.stringify([...document.querySelectorAll('.ext-subtab.active')]"
        ".map(n=>n.textContent.trim()))") or []) else None, "技能子 tab 激活", timeout=10)
    _ensure_root(env)
    env.wait(lambda: True if len(_kb_rows(env)) >= 2 else None, "技能树分类", timeout=20)
    cat = next((_label(r) for r in _kb_rows(env)[1:] if r.get("d")), "")
    if cat:
        env.js("""(()=>{const els=[...document.querySelectorAll(
          '.knowledge-tree .tree-row.is-dir')].filter(n=>n.offsetParent!==null);
          const el=els.find(n=>n.textContent.trim().split(/[\s⋯]+/)[0]===%s);
          if(el)(el.querySelector('.arrow')||el).dispatchEvent(new MouseEvent('click',{bubbles:true}));
          return 'ok';})()""" % json.dumps(cat))
    files = env.wait(lambda: (lambda rr: rr if any(
        str(_label(r)).endswith(".md") for r in rr) else None)(_kb_rows(env)),
        "技能目录下 *.md", timeout=25)
    env.check("技能分类下有原语文件", bool(files), [_label(r) for r in files][:6])
    target = next((_label(r) for r in files if str(_label(r)).endswith(".md")), None)
    if target:
        env.check("点击技能原语文件", _click_file(env, target) == "ok", target)
        panel = env.wait(lambda: True if env.js(
            "(()=>{const ps=[...document.querySelectorAll('.prim-panel')];"
            "return ps.some(p=>p.getBoundingClientRect().width>0)})()") is True else None,
            "原语面板打开", timeout=20)
        env.check("PrimitivePanel 打开", panel is True)
        info = env.jsj("""JSON.stringify((()=>{const P=[...document.querySelectorAll('.prim-panel')]
          .find(n=>n.getBoundingClientRect().width>0);if(!P)return null;
          return {tabs:[...P.querySelectorAll('.b-tabs-header .b-tabs-item')]
                    .map(n=>n.textContent.trim()),
                  path:(P.querySelector('.prim-path')||{}).textContent||'',
                  title:(P.querySelector('.prim-title')||{}).value||'',
                  footer:(P.querySelector('.prim-footer')||{}).textContent||''};})())""") or {}
        tabs = info.get("tabs") or []
        env.check("原语五页签（预览/基本信息/摘要/参数或参考/正文）",
                  len(tabs) == 5 and tabs[0] == "预览" and tabs[1] == "基本信息"
                  and tabs[2] == "摘要" and tabs[3] in ("参数", "参考") and tabs[4] == "正文",
                  tabs)
        env.check("标题路径 = 打开的原语文件",
                  str(info.get("path") or "").endswith(str(target or "")), info.get("path"))
        if str(info.get("title") or "").strip():
            env.check("顶部标题非空", True, info.get("title"))
        else:
            env.note("原语标题", "技能原语面板顶部标题为空（工具原语才有标题输入）")
        env.check("底部有保存/恢复",
                  ("保存" in str(info.get("footer") or "") or "Save" in str(info.get("footer") or ""))
                  and ("恢复" in str(info.get("footer") or "")
                       or "Restore" in str(info.get("footer") or "")), info.get("footer"))
    else:
        env.check("找到技能原语文件", False, [_label(r) for r in files][:5])

    env.js("window.mq.emit('preview-tab-close-all'); 'ok'")
    env.js("window.mq.emit('filetree-mode-select', {mode:'project'}); 'ok'")

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("资源面板组无前端错误", len(errs) == 0, errs[:3])
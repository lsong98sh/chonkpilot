# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉙：**左侧「项目记忆」模式**（迁移自 run_memory_tree MT1–MT5）。

* 资源面板切「记忆」→ `.memory-pane` 列出启用类别（类别名 + 级别 + 预估 tokens）；
* 项目级条目点击 → 预览区 markdown 含内容；用户偏好 → 只读 `.user-pref-text`；
* `memory.category.<类别>=false` → 该行消失、其它类别保留；
* `memory.enabled=false` → 清单清空 + 「记忆库未启用」空态 + 跳转入口。

夹具：项目级「项目概要」与 user 级「用户偏好」各写哨兵内容；结束后删除并还原 prj 键。
"""

import json
import time

from .framework import case

PROJ_CATS = ["项目概要", "共同库", "开发规范", "构建发布规则",
             "接口库", "测试规范", "典型参照", "用户决策"]
USER_PREF = "用户偏好"
GATED = "开发规范"


def _prj_save(env, k, v):
    env.req("data-prj-config-save", {"data": {"key": k, "value": v}})


def _prj_delete(env, k):
    try:
        env.req("data-prj-config-delete", {"id": k})
    except Exception:
        pass


def _segs(env):
    return env.jsj("JSON.stringify([...document.querySelectorAll('.explorer-seg-btn')]"
                   ".map(n=>({t:n.textContent.trim(),a:n.classList.contains('active')})))") or []


def _switch(env, mode):
    env.js("window.mq.emit('filetree-mode-select', %s); 'ok'" % json.dumps({"mode": mode}))
    want = {"memory": ("记忆", "Memory"), "project": ("项目", "Project")}[mode]
    return env.wait(lambda: True if any(
        s.get("a") and any(x in (s.get("t") or "") for x in want) for s in _segs(env))
        else None, "切到 %s 模式" % mode, timeout=15)


def _rows(env):
    return env.jsj("""JSON.stringify([...document.querySelectorAll('.memory-pane .mem-row')]
      .map(n=>({c:n.getAttribute('data-category'),l:n.getAttribute('data-level'),
                name:(n.querySelector('.mem-row-name')||{}).textContent||'',
                lv:(n.querySelector('.mem-row-level')||{}).textContent||'',
                tk:(n.querySelector('.mem-row-tokens')||{}).textContent||''})))""") or []


def _click_row(env, cat):
    return env.js("""(()=>{const el=[...document.querySelectorAll('.memory-pane .mem-row')]
      .find(n=>n.getAttribute('data-category')===%s);
      if(!el)return 'no-row';el.dispatchEvent(new MouseEvent('click',{bubbles:true}));
      return 'ok';})()""" % json.dumps(cat))


def _visible(env, sel):
    return int(env.js("([...document.querySelectorAll(%s)]"
                      ".filter(e=>e.getBoundingClientRect().width>0)).length" % json.dumps(sel)) or 0)


@case("S-MEM-TREE-01", "记忆库", "项目记忆模式：类别清单/级别/tokens、条目预览、逐类开关、总开关空态",
      stage="seeded", order=332, fp="左侧资源面板 · 项目记忆（run_memory_tree MT1–MT5）",
      desc="记忆模式列出启用类别（行含类别/级别/tokens）；点「项目概要」→ markdown 含哨兵；"
           "点「用户偏好」→ `.user-pref-text` 只读含哨兵；关闭「开发规范」→ 该行消失其它保留；"
           "memory.enabled=false → 清单空 + 「记忆库未启用」+ 跳转入口。")
def s_mem_tree_01(env):
    rid = env.run_id
    proj_sent = "E2E-MEM-PROJ-%s" % rid
    pref_sent = "E2E-MEM-PREF-%s" % rid
    base_enabled = env.prj().get("memory.enabled")
    base_gated = env.prj().get("memory.category." + GATED)
    try:
        env.req("data-memory-save", {"data": {"category": "项目概要", "content": proj_sent}})
        env.req("data-memory-save", {"data": {"category": USER_PREF, "content": pref_sent}})
        _prj_save(env, "memory.enabled", "true")
        _prj_save(env, "memory.category." + GATED, "true")
        time.sleep(0.8)

        # 确保资源面板与记忆模式
        if int(env.js("document.querySelectorAll('.explorer-seg-btn').length") or 0) == 0:
            env.js("""(()=>{const b=[...document.querySelectorAll('button.b-btn')]
              .find(n=>/文件树|File Tree/.test((n.getAttribute('title')||'')
                +(n.textContent||'')));if(b)b.click();return 'ok';})()""")
            time.sleep(0.8)
        env.check("资源面板分段按钮存在",
                  int(env.js("document.querySelectorAll('.explorer-seg-btn').length") or 0) >= 3)
        env.check("切「记忆」模式", _switch(env, "memory") is True)

        rows = env.wait(lambda: _rows(env) if len(_rows(env)) >= 9 else None,
                        "记忆类别清单（≥9）", timeout=20)
        cats = {r.get("c") for r in rows}
        env.check("列出 8 项目级 + 用户偏好",
                  set(PROJ_CATS) <= cats and USER_PREF in cats, sorted(cats))
        bad = [r for r in rows if not r.get("name") or not r.get("lv") or r.get("tk") == ""]
        env.check("每行含类别名/级别/tokens", not bad, bad[:3])
        levels = {r.get("c"): r.get("l") for r in rows}
        env.check("级别标注：用户偏好=user / 项目概要=project",
                  levels.get(USER_PREF) == "user" and levels.get("项目概要") == "project", levels)

        env.check("点击「项目概要」", _click_row(env, "项目概要") == "ok")
        txt = env.wait(lambda: str(env.js("""(()=>{const e=[...document.querySelectorAll(
          '.markdown-preview')].find(n=>n.getBoundingClientRect().width>0);
          return e?(e.textContent||''):''})()""") or "") or None,
          "项目概要预览", timeout=20)
        env.check("项目级条目预览含哨兵", proj_sent in txt, txt[:80])

        env.check("点击「用户偏好」", _click_row(env, USER_PREF) == "ok")
        pref = env.wait(lambda: str(env.js("""(()=>{const e=[...document.querySelectorAll(
          '.user-pref-text')].find(n=>n.getBoundingClientRect().width>0);
          return e?(e.textContent||''):''})()""") or "") or None,
          "用户偏好只读页", timeout=20)
        env.check("用户偏好页含哨兵", pref_sent in pref, pref[:80])
        env.check("用户偏好页只读（无 textarea）", _visible(env, ".user-pref-view textarea") == 0)

        # 逐类开关
        _prj_save(env, "memory.category." + GATED, "false")
        env.wait(lambda: True if GATED not in {r.get("c") for r in _rows(env)} else None,
                 "关闭开发规范后行消失", timeout=15)
        cats2 = {r.get("c") for r in _rows(env)}
        env.check("关闭单类 → 该行消失、其它保留",
                  GATED not in cats2 and "项目概要" in cats2 and USER_PREF in cats2, sorted(cats2))
        _prj_save(env, "memory.category." + GATED, "true")

        # 总开关
        _prj_save(env, "memory.enabled", "false")
        env.wait(lambda: True if len(_rows(env)) == 0 else None, "关闭记忆库后清单清空", timeout=15)
        title = str(env.js("document.querySelector('.memory-pane .mem-empty-title')"
                           "?.textContent?.trim() || ''") or "")
        env.check("空态提示「记忆库未启用」", "记忆库未启用" in title or "not enabled" in title.lower(),
                  title)
        env.check("空态仍有跳转入口按钮", bool(env.js(
            "(()=>{const e=[...document.querySelectorAll('.memory-pane button.b-btn')]"
            ".find(n=>n.getBoundingClientRect().width>0);return !!e})()")))
        _prj_save(env, "memory.enabled", "true")
    finally:
        for cat in ("项目概要", USER_PREF):
            try:
                env.req("data-memory-delete", {"data": {"category": cat}})
            except Exception:
                pass
        if base_enabled is None:
            _prj_delete(env, "memory.enabled")
        else:
            _prj_save(env, "memory.enabled", str(base_enabled))
        if base_gated is None:
            _prj_delete(env, "memory.category." + GATED)
        else:
            _prj_save(env, "memory.category." + GATED, str(base_gated))
        env.js("window.mq.emit('filetree-mode-select', {mode:'project'}); 'ok'")

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("记忆树组无前端错误", len(errs) == 0, errs[:3])
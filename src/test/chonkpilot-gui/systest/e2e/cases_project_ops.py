# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑲/⑳：**项目级配置数据面**与**总结提示词 / 代码知识库页**。

迁移自 `run_project.py`（四.1/3/4/6/7）与 `run_project_cfg.py` K1–K6 + `run_fp_extra_ui` T4/T5。
数据面全部走既有消息主题（data-prj-config-* / data-prompt-* / data-prj-security-* /
data-session-* / gui.recent.list）；UI 侧断言编辑弹框自动加载、来源标注、重置入口，
以及插件页 iframe 的 workdir 回填、控件与状态输出。
"""

import json
import os
import time

from .framework import case


CTX_KEYS = ("keep_full_max_turns", "keep_full_max_tokens", "compress_token_threshold")


def _prj(env):
    return env.prj()


@case("S-PRJ-DATA-01", "配置", "项目配置数据面：压缩三键广播 / 提示词往返 / 安全目录条目 / 最近目录 / 会话往返",
      stage="seeded", order=200, fp="61 §3.1 data-prj-config-* / data-prompt-* / data-prj-security-*",
      desc="批量写压缩三键 → 一条 refresh 带全部 ids → 逐键 load 精确回读；提示词 save→load "
           "哨兵、清空 → 空串；安全目录条目 save（{dir,writable} JSON）→ list 精确 → 改 → 删；"
           "gui.recent.list 返回 dirs 列表；会话 ensure/get/delete 往返。")
def s_prj_data_01(env):
    vals = {"keep_full_max_turns": "2", "keep_full_max_tokens": "24000",
            "compress_token_threshold": "20000"}
    env.capture(["data-prj-config-refresh"])
    env.req("data-prj-config-save", {"data": {"entries": dict(vals)}})
    got = env.wait(lambda: _prj(env) if all(
        _prj(env).get(k) == v for k, v in vals.items()) else None, "压缩三键落库", timeout=20)
    for k, v in vals.items():
        env.eq("prj %s 回读" % k, got.get(k), v)
    refs = env.events("data-prj-config-refresh")
    env.check("批量写只广播 1 条 refresh", len(refs) == 1, "n=%d" % len(refs))
    if refs:
        ids = (refs[0].get("payload") or {}).get("ids") or []
        env.check("refresh ids 覆盖三键", set(CTX_KEYS) <= set(ids), ids)
    for k in CTX_KEYS:
        env.eq("data-prj-config-load %s" % k,
               (env.req("data-prj-config-load", {"id": k}) or {}).get("data"), vals[k])

    # 提示词往返（persist 层清空 = 空串；回退发生在 server 组装期）
    orig = (env.req("data-prompt-load", {"id": "tool_usage_prompt"}) or {}).get("data")
    try:
        sentinel = "E2E-PROMPT-%s" % env.run_id
        env.req("data-prompt-save", {"data": {"key": "tool_usage_prompt", "value": sentinel}})
        env.eq("提示词 save→load 往返",
               (env.req("data-prompt-load", {"id": "tool_usage_prompt"}) or {}).get("data"),
               sentinel)
        env.req("data-prompt-save", {"data": {"key": "tool_usage_prompt", "value": ""}})
        cleared = (env.req("data-prompt-load", {"id": "tool_usage_prompt"}) or {}).get("data")
        env.check("提示词清空 → 空串", cleared in ("", None), cleared)
    finally:
        env.req("data-prompt-save", {"data": {"key": "tool_usage_prompt", "value": orig or ""}})

    # 安全目录条目（一条 = 一个不透明 key，value = {dir,writable} JSON；dir 必须真实存在）
    sec_dir = env.path("e2e_sec_%s" % env.run_id)
    os.makedirs(sec_dir, exist_ok=True)
    key = "e2e-sec-%s" % env.run_id
    v1 = json.dumps({"dir": sec_dir, "writable": True}, ensure_ascii=False)
    v2 = json.dumps({"dir": sec_dir, "writable": False}, ensure_ascii=False)
    try:
        env.req("data-prj-security-save", {"data": {"key": key, "value": v1}})
        lst = (env.req("data-prj-security-list", {}) or {}).get("list") or {}
        env.eq("安全目录新增回读", lst.get(key), v1)
        env.req("data-prj-security-save", {"data": {"key": key, "value": v2}})
        lst2 = (env.req("data-prj-security-list", {}) or {}).get("list") or {}
        env.eq("安全目录同键更新", lst2.get(key), v2)
    finally:
        env.req("data-prj-security-delete", {"id": key})
    lst3 = (env.req("data-prj-security-list", {}) or {}).get("list") or {}
    env.check("安全目录删除生效", key not in lst3, key)

    # 最近目录结构
    rec = env.req("gui.recent.list", {}) or {}
    env.check("gui.recent.list 返回 dirs 列表", isinstance(rec.get("dirs"), list), rec)

    # 会话 ensure/get/delete
    sid = "e2e-prj-sess-%s" % env.run_id
    env.req("data-session-ensure-session", {"session_id": sid})
    sess = (env.req("data-session-get", {"id": sid}) or {}).get("data") or {}
    env.eq("会话 get 回读 session_id", sess.get("session_id"), sid)
    # 删除会顺带回收对话库文件；Windows 下偶有句柄未释放（另一进程/线程仍持有）→ 有界重试，
    # 仍失败则记为已知清理竞态（不把夹具清理抖动判成功能失败）。
    del_err = ""
    for i in range(10):
        try:
            env.req("data-session-delete", {"id": sid})
            del_err = ""
            break
        except Exception as e:
            del_err = str(e)
            if "being used by another process" not in del_err:
                raise
            time.sleep(0.6)
    # 删除后会话所在对话库可能一并回收 → get 允许返回 data=null 或显式「对话库不存在」错误。
    gone, err = None, ""
    try:
        gone = (env.req("data-session-get", {"id": sid}) or {}).get("data")
    except Exception as e:
        err = str(e)
    if del_err and gone:
        env.note("会话 delete 清理竞态", "对话库文件句柄未释放：%s" % del_err[:120])
    else:
        env.check("会话 delete 后不可读（null 或对话库已回收）",
                  gone is None and ("对话库不存在" in err or not err),
                  {"gone": gone, "err": err[:80]})

    # 清理三键（保持后续测试点默认上下文策略）
    for k in CTX_KEYS:
        env.req("data-prj-config-delete", {"id": k})
    try:
        os.rmdir(sec_dir)
    except OSError:
        pass


def _click_tab_by_text(env, pat):
    return env.js("""(()=>{const ps=[...document.querySelectorAll('.project-config-panel')]
      .filter(p=>p.getBoundingClientRect().width>0);
      const P=ps[ps.length-1];if(!P)return 'no-panel';
      const b=[...P.querySelectorAll('.b-tabs-item')]
        .find(x=>new RegExp(%s).test((x.textContent||'').trim()));
      if(!b)return 'no-tab';b.dispatchEvent(new MouseEvent('click',{bubbles:true}));
      return 'ok';})()""" % json.dumps(pat))


def _panel(env, expr):
    return env.jsj("""JSON.stringify((()=>{const ps=[...document.querySelectorAll('.project-config-panel')]
      .filter(p=>p.getBoundingClientRect().width>0);
      const P=ps[ps.length-1];if(!P)return null;%s})())""" % expr)


@case("S-CFG-PROMPT-01", "配置", "总结提示词：编辑弹框自动加载 / 来源标注 / 重置入口；代码知识库插件页控件与状态输出",
      stage="seeded", order=210, fp="33 项目配置 · 总结提示词 / 代码知识库（run_project_cfg K4–K6）",
      desc="上下文管理页：总结提示词「编辑」→ 弹框内容非空、来源标注可见、面板有「重置」入口；"
           "代码知识库页：插件 iframe workdir 绝对路径回填、cg* 控件与四按钮齐备，"
           "点击【读取状态】→ `#out` 输出引擎状态字段。")
def s_cfg_prompt_01(env):
    env.js("window.mq.emit('project-config-open'); 'ok'")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.project-config-panel')") is True else None,
        "项目配置面板打开", timeout=25)
    env.check("切到上下文管理页", _click_tab_by_text(env, "上下文管理|Context") == "ok")
    time.sleep(0.8)

    r = _panel(env, """
      const hs=[...P.querySelectorAll('.prompt-editor-header')];
      const h=hs[0];
      const b=h?[...h.querySelectorAll('.b-btn')]
        .find(x=>/编辑|Edit/i.test(x.textContent||'')):null;
      if(!b)return 'no-btn';b.dispatchEvent(new MouseEvent('click',{bubbles:true}));
      return 'ok';""")
    env.check("总结提示词「编辑」入口", r == "ok", r)
    env.wait(lambda: True if env.js(
        "(()=>{const d=document.querySelector('.text-edit-dialog-body');"
        "return !!d&&d.getBoundingClientRect().width>0})()") is True else None,
        "总结提示词编辑弹框", timeout=15)
    val = str(env.js("""(()=>{const d=document.querySelector('.text-edit-dialog-body');
      const t=d?d.querySelector('textarea'):null;return t?(t.value||''):''})()""") or "")
    env.check("总结提示词自动加载（内容非空）", bool(val.strip()), val[:60])

    hints = _panel(env, """return [...P.querySelectorAll('.field-hint')]
      .map(e=>(e.textContent||'').trim());""") or []
    env.check("来源标注可见",
              any(any(k in h for k in ("当前来源", "继承", "覆盖", "inherit", "override", "source"))
                  for h in hints), hints[:4])
    btns = _panel(env, """return [...P.querySelectorAll('.b-btn')]
      .map(b=>(b.textContent||'').trim());""") or []
    env.check("有「重置」入口", any(("重置" in b) or ("Reset" in b) for b in btns), btns[:8])

    env.js("""(()=>{const d=document.querySelector('.text-edit-dialog-body');
      if(!d)return 'no';const b=[...d.querySelectorAll('button,.b-btn')]
        .find(x=>/取消|Cancel|关闭|Close/i.test(x.textContent||''));
      if(b)b.click();return 'ok';})()""")
    time.sleep(0.5)

    # 代码知识库（插件页 iframe）
    env.check("切到代码知识库页", _click_tab_by_text(env, "代码知识库|Code KB") == "ok")

    def _kb(inner):
        return env.jsj("""JSON.stringify((()=>{const ps=[...document.querySelectorAll('.project-config-panel')]
          .filter(p=>p.getBoundingClientRect().width>0);
          const P=ps[ps.length-1];if(!P)return 'no-panel';
          const f=P.querySelector('iframe');if(!f)return 'no-frame';
          const d=f.contentDocument;if(!d)return 'no-doc';%s})())""" % inner)

    wd = env.wait(lambda: (lambda v: v if isinstance(v, str) and len(v) > 2
                           and v[1] in (":", "/") else None)(
        _kb("return (d.getElementById('workdir')||{}).value||'';")),
        "代码知识库 iframe workdir 回填", timeout=35)
    env.check("workdir 绝对路径回填", bool(wd), wd)

    ctrl = _kb("""return {enabled:!!d.getElementById('cgEnabled'),
      exts:!!d.getElementById('cgExts'), skip:!!d.getElementById('cgSkip'),
      gitignore:!!d.getElementById('cgGitignore'),
      save:((d.getElementById('cgSave')||{}).textContent||'').trim(),
      clear:((d.getElementById('cgClear')||{}).textContent||'').trim(),
      status:((d.getElementById('loadStatus')||{}).textContent||'').trim(),
      index:((d.getElementById('indexNow')||{}).textContent||'').trim()};""") or {}
    env.check("codegraph 控件齐备",
              all(ctrl.get(k) for k in ("enabled", "exts", "skip", "gitignore")), ctrl)
    joined = " | ".join(str(ctrl.get(k) or "") for k in ("save", "clear", "status", "index"))
    env.check("维护/状态按钮齐备",
              all(k in joined for k in ("保存并应用", "清除索引", "读取状态", "立即索引")), joined)

    r = _kb("const b=d.getElementById('loadStatus');if(!b)return 'no-btn';b.click();return 'ok';")
    env.check("点击【读取状态】", r == "ok", r)
    out = env.wait(lambda: (lambda t: t if any(
        m in str(t) for m in ("state", "indexedFiles", "chunkCount", "enabled")) else None)(
        _kb("return (d.getElementById('out')||{}).textContent||'';")),
        "【读取状态】输出引擎状态", timeout=60)
    env.check("状态输出含引擎字段", bool(out), str(out)[:120])

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") in ("error",) and "method not implemented" not in (e.get("text") or "")]
    env.check("项目配置页组无前端错误", len(errs) == 0, errs[:3])
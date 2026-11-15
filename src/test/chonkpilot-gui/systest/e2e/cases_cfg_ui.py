# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑬：**配置 UI**（迁移自 test_config.py C1–C21b；C22「恢复出厂」不迁移）。

C22 会清空 usr/prj 配置并触发备份，属于**破坏性**用例；单实例套件中会打断后续测试点 →
保留在旧 `test_config.py`（独立实例 + 快照还原），新套件不纳入。
"""

import json
import time

from .framework import case


def _visible_dialogs(env):
    return int(env.js("(()=>[...document.querySelectorAll('.dialog-shell')]"
                      ".filter(d=>{const r=d.getBoundingClientRect();"
                      "return r.width>0&&r.height>0}).length)()") or 0)


def _top_dialog_text(env):
    return str(env.js("(()=>{const ds=[...document.querySelectorAll('.dialog-shell')]"
                      ".filter(d=>{const r=d.getBoundingClientRect();return r.width>0&&r.height>0});"
                      "const d=ds[ds.length-1];"
                      "return d?(d.querySelector('.dialog-body')?.textContent||''):''})()") or "")


def _top_dialog_click(env, idx=0):
    return env.js("(()=>{const ds=[...document.querySelectorAll('.dialog-shell')]"
                  ".filter(d=>{const r=d.getBoundingClientRect();return r.width>0&&r.height>0});"
                  "const d=ds[ds.length-1];if(!d)return -1;"
                  "const bs=d.querySelector('.dialog-body')?.querySelectorAll('button')||[];"
                  "if(bs[%d])bs[%d].click();return bs.length})()" % (idx, idx))


def _close_visible_dialogs(env):
    for _ in range(4):
        if _visible_dialogs(env) == 0:
            return
        r = env.js("(()=>{const ds=[...document.querySelectorAll('.dialog-shell')]"
                   ".filter(d=>{const r=d.getBoundingClientRect();return r.width>0&&r.height>0});"
                   "const d=ds[ds.length-1];if(!d)return 'no-dialog';"
                   "const b=[...d.querySelectorAll('button')].find(x=>"
                   "/关闭|取消|close|cancel|知道了/i.test(x.textContent||''));"
                   "if(b){b.click();return 'ok'}return 'no-btn'})()")
        if r != "ok":
            env.js("document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));"
                   "'ok'")
            time.sleep(0.3)
        else:
            time.sleep(0.4)


@case("S-CFG-UI-01", "配置", "配置 UI：项目配置 8 页签 / 安全页添加 / 用户配置 / 场景 / 导入导出",
      stage="seeded", order=380, fp="33 配置 UI（test_config C1–C21b）",
      desc="project-config-open → 8 页签，日志页有级别+目录，安全页表头并可新增行；config-open → "
           "设置页表格与「添加 LLM」编辑框（取消）；scenario-open → 场景弹窗可关闭；配置 IO 页 "
           "含导出/导入/恢复厂三区（排除密钥默认开）；非法 JSON 导入有提示+详情；合法导入弹确认、"
           "取消后不写结果。")
def s_cfg_ui_01(env):
    # ── 项目配置 ──
    env.js("window.mq.emit('project-config-open'); 'ok'")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.project-config-panel')") is True else None,
        "项目配置面板打开", timeout=25)
    tabs = int(env.js(
        "document.querySelectorAll('.project-config-panel .b-tabs-item').length") or 0)
    env.eq("项目配置 8 页签", tabs, 8)

    env.js("document.querySelectorAll('.project-config-panel .b-tabs-item')[6]?.click(); 'ok'")
    time.sleep(0.9)
    env.check("日志页有级别选择", int(env.js(
        "document.querySelectorAll('.project-config-panel .log-root .b-select').length") or 0) >= 1)
    logtxt = str(env.js("document.querySelector('.project-config-panel .log-root')"
                        "?.textContent || ''") or "")
    env.check("日志页有日志目录入口", ("日志目录" in logtxt) or ("Log directory" in logtxt), logtxt[:80])

    env.js("document.querySelectorAll('.project-config-panel .b-tabs-item')[0]?.click(); 'ok'")
    time.sleep(0.8)
    env.check("安全页签 active", env.js(
        "document.querySelectorAll('.project-config-panel .b-tabs-item')[0]"
        "?.classList.contains('active')") is True)
    env.check("安全页表头 ≥3 列", int(env.js(
        "document.querySelectorAll('.project-config-panel .b-table th').length") or 0) >= 3)

    env.js("(()=>{const b=document.querySelector('.project-config-panel .tab-toolbar button');"
           "if(b)b.click();return !!b})()")
    time.sleep(0.8)
    env.check("安全页添加 → 新增行 + 输入框",
              int(env.js("document.querySelectorAll('.project-config-panel .b-table tbody tr')"
                         ".length") or 0) >= 1
              and int(env.js("document.querySelectorAll('.project-config-panel "
                             ".security-dir-row .b-input').length") or 0) >= 1)
    env.client.console(clear=True)
    env.js("""(()=>{const el=document.querySelector('.project-config-panel .security-dir-row .b-input');
      if(!el)return false;const s=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;
      s.call(el,'D:\\\\trust');el.dispatchEvent(new Event('input',{bubbles:true}));
      el.dispatchEvent(new Event('change',{bubbles:true}));return true})()""")
    time.sleep(0.8)
    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("安全目录输入无前端错误", len(errs) == 0, errs[:2])

    # ── 用户配置 ──
    env.client.console(clear=True)
    env.js("window.mq.emit('config-open'); 'ok'")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.code-view .special-tab.settings-page')") is True else None,
        "用户配置页打开", timeout=25)
    env.check("用户配置表格 ≥4 列", int(env.js(
        "document.querySelectorAll('.settings-page .b-table thead th').length") or 0) >= 4)
    env.check("用户配置有添加按钮", int(env.js(
        "document.querySelectorAll('.settings-page .config-toolbar-actions .b-btn').length")
        or 0) >= 1)
    env.js("document.querySelector('.settings-page .config-toolbar-actions button')?.click(); 'ok'")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.dialog-shell .form-layout')") is True else None,
        "添加 LLM 编辑框", timeout=15)
    env.check("添加 LLM 打开编辑框", env.js(
        "!!document.querySelector('.dialog-shell .form-layout')") is True)
    env.js("(()=>{const bs=[...document.querySelectorAll('.dialog-shell .dialog-footer button')];"
           "if(bs[0])bs[0].click();return 'ok'})()")
    time.sleep(0.6)
    _close_visible_dialogs(env)

    # ── 场景配置（弹窗，读完即关）──
    env.client.console(clear=True)
    env.js("window.mq.emit('scenario-open'); 'ok'")
    env.wait(lambda: True if int(env.js(
        "document.querySelectorAll('.dialog-content').length") or 0) >= 1 else None,
        "场景配置弹窗打开", timeout=20)
    body = str(env.js("document.body.textContent") or "")
    env.check("场景弹窗含场景文案", ("场景" in body) or ("Scenario" in body))
    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("场景打开无前端错误", len(errs) == 0, errs[:2])
    _close_visible_dialogs(env)
    env.check("场景弹窗可关闭", _visible_dialogs(env) == 0)

    # ── 配置导入/导出（页签 7）──
    env.js("document.querySelectorAll('.project-config-panel .b-tabs-item')[7]?.click(); 'ok'")
    time.sleep(1.0)
    env.check("IO 页三区齐全", all(env.js(s) is True for s in (
        "!!document.querySelector('.config-io-root .io-export')",
        "!!document.querySelector('.config-io-root .io-import')",
        "!!document.querySelector('.config-io-root .io-reset')")))
    warn = str(env.js("document.querySelector('.config-io-root .io-secret-warn')"
                      "?.textContent || ''") or "")
    env.check("IO 页含 API Key 警示", "API Key" in warn, warn[:60])
    env.check("排除密钥默认开", env.js(
        "!!document.querySelector('.config-io-root .io-exclude-switch .b-switch.is-checked')")
        is True)
    env.check("导入粘贴框存在", env.js(
        "!!document.querySelector('.config-io-root .io-import textarea')") is True)
    scope = str(env.js("document.querySelector('.config-io-root .io-reset-scope')"
                       "?.textContent || ''") or "")
    env.check("恢复厂范围文案", ("不含项目级配置" in scope) or ("Project-level" in scope), scope[:80])

    # C21 非法 JSON → 人话提示 + 原始详情
    env.js("(()=>{const el=document.querySelector('.config-io-root .io-import textarea');"
           "if(!el)return false;const s=Object.getOwnPropertyDescriptor("
           "HTMLTextAreaElement.prototype,'value').set;s.call(el,'{bad json');"
           "el.dispatchEvent(new Event('input',{bubbles:true}));return true})()")
    time.sleep(0.3)
    env.js("(()=>{const b=[...document.querySelectorAll('.config-io-root .io-import button')]"
           ".find(x=>/解析并导入|Parse and import/.test(x.textContent||''));"
           "if(b)b.click();return !!b})()")
    time.sleep(0.9)
    ierr = str(env.js("document.querySelector('.config-io-root .io-import-error')"
                      "?.textContent || ''") or "")
    env.check("非法 JSON 导入提示含 JSON", "JSON" in ierr, ierr[:80])
    env.check("非法 JSON 原始详情可展开", env.js(
        "!!document.querySelector('.config-io-root .io-import-error details pre')") is True)

    # C21b 合法 JSON（含未知键）→ 确认弹窗 → 取消不落盘
    env.js("(()=>{const el=document.querySelector('.config-io-root .io-import textarea');"
           "if(!el)return false;const s=Object.getOwnPropertyDescriptor("
           "HTMLTextAreaElement.prototype,'value').set;"
           "s.call(el,JSON.stringify({retryCount:3,bogus_key:1}));"
           "el.dispatchEvent(new Event('input',{bubbles:true}));return true})()")
    time.sleep(0.3)
    env.js("(()=>{const b=[...document.querySelectorAll('.config-io-root .io-import button')]"
           ".find(x=>/解析并导入|Parse and import/.test(x.textContent||''));"
           "if(b)b.click();return !!b})()")
    time.sleep(0.9)
    dlg = _top_dialog_text(env)
    env.check("导入确认弹窗写明键覆盖 + retryCount",
              (("键覆盖" in dlg) or ("overwritten" in dlg)) and "retryCount" in dlg, dlg[:100])
    _top_dialog_click(env, 0)
    time.sleep(0.6)
    env.check("取消导入后无结果提示", env.js(
        "!document.querySelector('.config-io-root .io-import-result')") is True)
    _close_visible_dialogs(env)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("配置 UI 组无前端错误", len(errs) == 0, errs[:3])


def _fill_form(env, label_cands, value):
    r = env.js("""(()=>{const cands=%s;
      const items=[...document.querySelectorAll('.form-layout .form-item')];
      const it=items.find(x=>{const lb=x.querySelector('.form-label');
        return lb&&cands.some(c=>lb.textContent.includes(c))});
      const inp=it?it.querySelector('input, textarea'):null;
      if(!inp)return 'not-found';
      const proto=inp instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto,'value').set.call(inp,%s);
      inp.dispatchEvent(new Event('input',{bubbles:true}));
      inp.dispatchEvent(new Event('change',{bubbles:true}));
      return 'ok';})()""" % (json.dumps(label_cands, ensure_ascii=False), json.dumps(value)))
    env.check("填写字段 %r" % (label_cands[0],), r == "ok", r)


@case("S-CFG-DATA-01", "配置", "LLM 增删改查 + 保存广播 / MCP 新增删除 / 列表展示",
      stage="seeded", order=386, fp="33 配置数据面（run_config 三.1/三.3/三.6）",
      desc="settings-llm 页显示 mock/mock-model；config-add-llm → 填 Name/API Key/Model/Base URL/"
           "Max Tokens → edit-llm-save → 落库且广播 data-user-config-refresh、列表即时可见；"
           "settings-mcp 新增 test mcp → data-mcp-list 命中 → 删除闭合。")
def s_cfg_data_01(env):
    base = [l for l in (env.usr().get("llms") or []) if isinstance(l, dict)]
    mcp = "e2e_mcp_%s" % env.run_id.replace("-", "_")
    name = "e2e-llm-%s" % env.run_id
    try:
        env.js("window.mq.emit('preview-tab-close-all'); 'ok'")
        time.sleep(0.5)
        env.emit("preview-tab-open", {"kind": "settings-llm"})
        env.wait(lambda: True if env.js(
            "!!document.querySelector('.code-view .special-tab.settings-page')")
            is True else None, "LLM 配置页打开", timeout=25)
        body = env.wait(lambda: str(env.js("document.body.innerText") or "")
                        if "mock" in str(env.js("document.body.innerText") or "")
                        and "mock-model" in str(env.js("document.body.innerText") or "")
                        else None, "LLM 列表显示 mock/mock-model", timeout=15)
        env.check("LLM 列表显示 mock/mock-model",
                  "mock" in body and "mock-model" in body, body[:160])

        env.capture(["data-user-config-refresh"])
        env.emit("config-add-llm")
        env.wait(lambda: True if env.js("!!document.querySelector('.form-layout')")
                 is True else None, "添加 LLM 编辑框", timeout=15)
        _fill_form(env, ["Name", "名称"], name)
        _fill_form(env, ["API Key", "API 密钥"], "e2e-key")
        _fill_form(env, ["Model", "模型"], "e2e-model-x")
        _fill_form(env, ["Base URL", "接口地址"], "http://127.0.0.1:9999/v1")
        _fill_form(env, ["Max Output Tokens", "最大输出 Token"], "2048")
        env.emit("edit-llm-save")
        rows = env.wait(lambda: [l for l in (env.usr().get("llms") or [])
                                 if l.get("name") == name] or None, "LLM 落库", timeout=20)
        env.eq("LLM model 落库", (rows[0] or {}).get("model"), "e2e-model-x")
        env.eq("LLM apiKey 落库", (rows[0] or {}).get("apiKey"), "e2e-key")
        env.wait_events("data-user-config-refresh", lambda p: True,
                        "保存广播 data-user-config-refresh", timeout=15)
        env.check("LLM 保存后广播", True)
        env.wait(lambda: True if name in str(env.js("document.body.innerText") or "")
                 else None, "列表即时显示新 LLM", timeout=15)
        env.check("列表即时显示新 LLM", name in str(env.js("document.body.innerText") or ""))

        # MCP 新增/删除（先清残留弹窗/页签：LLM 保存框或前序配置页可能仍覆盖在页面上）
        env.js("""(()=>{[...document.querySelectorAll('.dialog-shell')]
          .filter(d=>{const r=d.getBoundingClientRect();return r.width>0;})
          .forEach(d=>{const b=[...d.querySelectorAll('button')]
            .find(x=>/取消|Cancel/.test((x.textContent||'').trim()));if(b)b.click();});
          return 'ok';})()""")
        time.sleep(0.5)
        env.emit("preview-tab-open", {"kind": "settings-mcp"})
        env.wait(lambda: True if env.js(
            "!!document.querySelector('.code-view .special-tab.settings-page')")
            is True else None, "MCP 配置页打开", timeout=25)
        mcp = "e2e_mcp_%s" % env.run_id.replace("-", "_")
        opened = False
        for _ in range(3):
            env.emit("config-add-mcp")
            try:
                env.wait(lambda: True if env.js("!!document.querySelector('.form-layout')")
                         is True else None, "添加 MCP 编辑框", timeout=10)
                opened = True
                break
            except Exception:
                time.sleep(0.8)
        env.check("添加 MCP 编辑框打开", opened, opened)
        _fill_form(env, ["Name", "名称"], mcp)
        _fill_form(env, ["Server URL", "URL", "地址"], "http://127.0.0.1:9999/mcp")
        env.emit("edit-mcp-save")
        names = env.wait(lambda: [m.get("name") for m in
                                  ((env.req("data-mcp-list", {}) or {}).get("list") or [])]
                         if mcp in [m.get("name") for m in
                                    ((env.req("data-mcp-list", {}) or {}).get("list") or [])]
                         else None, "MCP 落库", timeout=25)
        env.check("MCP 新增落库", mcp in names, names)
        env.req("data-mcp-delete", {"name": mcp})
        env.wait(lambda: True if mcp not in [
            m.get("name") for m in ((env.req("data-mcp-list", {}) or {}).get("list") or [])]
            else None, "MCP 删除闭合", timeout=15)
        env.check("MCP 删除闭合", mcp not in [
            m.get("name") for m in ((env.req("data-mcp-list", {}) or {}).get("list") or [])])

    finally:
        try:
            env.req("data-user-config-save", {"data": {"llms": base}})
        except Exception:
            pass
        try:
            env.req("data-mcp-delete", {"name": mcp})
        except Exception:
            pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("配置数据面组无前端错误", len(errs) == 0, errs[:3])

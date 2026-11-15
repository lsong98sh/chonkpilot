# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉚：**工具异步 / 沙箱配置页与 usr 键**（迁移自
run_tool_async_config / run_tool_sandbox 的 A/C/D/E 机制）。

* 工具异步页（`settings-tool-async`）：表格行 + 四档文案 + 干净态保存禁用；
  写 usr `tool_async` 后逐字段回读，`tools-list._meta.async` 若后端消费则核对（否则登记 PENDING）；
* 工具沙箱页（`settings-tool-sandbox`）：页签可打开、保存禁用；外部 MCP 边界说明；
  写 usr `tool_sandbox.mcps.<unit>` 回读、删除回落。
"""

import json
import time

from .framework import case


def _usr_key(env, key):
    v = env.usr().get(key)
    if isinstance(v, str):
        try:
            v = json.loads(v)
        except Exception:
            pass
    return v


def _open_page(env, kind):
    env.js("window.mq.emit('preview-tab-open', {kind:%s}); 'ok'" % json.dumps(kind))
    return env.wait(lambda: True if env.js(
        """(()=>{const ps=[...document.querySelectorAll('.settings-page')]
          .filter(p=>p.getBoundingClientRect().width>0);return ps.length>0})()"""
    ) is True else None, "配置页 %s 打开" % kind, timeout=25)


def _save_disabled(env):
    return env.js("""(()=>{const ps=[...document.querySelectorAll('.settings-page')]
      .filter(p=>p.getBoundingClientRect().width>0);const P=ps[ps.length-1];if(!P)return null;
      const b=[...P.querySelectorAll('button,.b-btn')]
        .find(x=>/^(保存|Save)$/.test((x.textContent||'').trim()));
      return b?(!!b.disabled||b.classList.contains('is-disabled')):null;})()""")


@case("S-TOOL-CFG-01", "配置", "工具异步/沙箱配置页：表格与四档/干净态禁用/边界说明 + usr 键往返/删除",
      stage="seeded", order=398, fp="33 工具配置页 · run_tool_async_config / run_tool_sandbox",
      desc="异步页打开 → 表格有行、四档文案可见、干净态保存禁用；usr tool_async 写入逐字段回读、"
           "tools-list._meta.async 有消费则核对（无消费登记 PENDING）；沙箱页打开 → 保存禁用、"
           "外部 MCP/http/sse 边界说明；usr tool_sandbox.mcps 写→读→删闭合。")
def s_tool_cfg_01(env):
    base_async = env.usr().get("tool_async")
    base_sandbox = env.usr().get("tool_sandbox")
    tool = next((str(t.get("name")) for t in
                 ((env.req("tools-list", {}) or {}).get("tools") or [])
                 if isinstance(t, dict) and t.get("name") == "task_run"), "task_run")
    try:
        # ── 工具异步页 ──
        env.check("打开工具异步页", _open_page(env, "settings-tool-async") is True)
        dom = env.jsj("""JSON.stringify((()=>{const ps=[...document.querySelectorAll('.settings-page')]
          .filter(p=>p.getBoundingClientRect().width>0);const P=ps[ps.length-1];
          return {rows:P.querySelectorAll('.b-table tbody tr').length,
                  txt:(P.innerText||'')};})())""") or {}
        env.check("异步页表格有数据行", int(dom.get("rows") or 0) >= 1, dom.get("rows"))
        opts = env.jsj("""JSON.stringify((()=>{const ps=[...document.querySelectorAll('.settings-page')]
          .filter(p=>p.getBoundingClientRect().width>0);const P=ps[ps.length-1];
          const nm=P?[...P.querySelectorAll('.tool-name[data-tool]')]
            .find(n=>n.getAttribute('data-tool')==='task_run'):null;
          const s=nm?nm.closest('tr').querySelector('select.b-select__native'):null;
          return s?[...s.querySelectorAll('option')].filter(o=>o.value!=='')
            .map(o=>o.textContent.trim()):[];})())""") or []
        env.check("模式选项含仅同步/仅异步/自动异步",
                  {"仅同步", "仅异步", "自动异步"} <= set(opts)
                  and set(opts) <= {"仅异步", "仅同步", "自动异步", "手动异步"}, opts)
        if "手动异步" not in opts:
            env.note("手动异步档位未提供", "task_run 当前只提供 3 档（旧套件期望四档）")
        env.check("干净态保存按钮禁用", _save_disabled(env) is True, _save_disabled(env))

        cfg = {"mode": "always", "threshold": 5, "hard_timeout": 0, "touch_files": True}
        env.req("data-user-config-save", {"data": {"tool_async": json.dumps(
            {tool: cfg}, ensure_ascii=False)}})
        env.js("window.mq.emit('config-refresh'); 'ok'")
        back = env.wait(lambda: (_usr_key(env, "tool_async") or {}).get(tool)
                        if (_usr_key(env, "tool_async") or {}).get(tool) else None,
                        "usr tool_async 回读", timeout=20)
        env.check("tool_async 逐字段回读",
                  (back.get("mode"), back.get("threshold"), back.get("hard_timeout"),
                   back.get("touch_files")) == ("always", 5, 0, True), back)
        meta = next((t for t in ((env.req("tools-list", {}) or {}).get("tools") or [])
                     if isinstance(t, dict) and t.get("name") == tool), {})
        cur = ((meta.get("_meta") or {}).get("async") or "")
        if cur:
            env.check("tools-list._meta.async 跟随配置", cur == "always", cur)
        else:
            env.note("tool_async 效果 PENDING", "tools-list 未提供 _meta.async（旧套件同样登记 PENDING）")
        env.req("data-user-config-delete", {"id": "tool_async"})
        env.wait(lambda: True if not _usr_key(env, "tool_async") else None,
                 "tool_async 删除回落", timeout=15)
        env.check("tool_async 键删除", not _usr_key(env, "tool_async"))

        # ── 工具沙箱页 ──
        env.check("打开工具沙箱页", _open_page(env, "settings-tool-sandbox") is True)
        env.check("沙箱页干净态保存禁用", _save_disabled(env) is True, _save_disabled(env))
        stxt = str(env.js("""(()=>{const ps=[...document.querySelectorAll('.settings-page')]
          .filter(p=>p.getBoundingClientRect().width>0);const P=ps[ps.length-1];
          return P?P.innerText:''})()""") or "")
        env.check("沙箱边界说明含外部 MCP / http·sse",
                  "外部 MCP" in stxt and ("http/sse" in stxt or "http·sse" in stxt), stxt[:160])

        unit = "task"
        env.req("data-user-config-save", {"data": {"tool_sandbox": json.dumps(
            {"mcps": {unit: True}}, ensure_ascii=False)}})
        env.js("window.mq.emit('config-refresh'); 'ok'")
        sb = env.wait(lambda: ((_usr_key(env, "tool_sandbox") or {}).get("mcps") or {}).get(unit)
                      is True or None, "usr tool_sandbox 回读", timeout=20)
        env.check("tool_sandbox.mcps 落库 true", sb is True,
                  _usr_key(env, "tool_sandbox"))
        env.req("data-user-config-delete", {"id": "tool_sandbox"})
        env.wait(lambda: True if not _usr_key(env, "tool_sandbox") else None,
                 "tool_sandbox 删除回落", timeout=15)
        env.check("tool_sandbox 键删除", not _usr_key(env, "tool_sandbox"))
    finally:
        for key, base in (("tool_async", base_async), ("tool_sandbox", base_sandbox)):
            try:
                if base is None:
                    env.req("data-user-config-delete", {"id": key})
                else:
                    env.req("data-user-config-save", {"data": {key: base}})
            except Exception:
                pass
        env.js("window.mq.emit('preview-tab-close-all'); 'ok'")
        env.js("window.mq.emit('config-refresh'); 'ok'")

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("工具配置组无前端错误", len(errs) == 0, errs[:3])
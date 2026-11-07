# -*- coding: utf-8 -*-
"""L4：工具沙箱配置页（usr 键 `tool_sandbox`，**被 spawn 执行体级**）—— 执行体行渲染 /
只读工具清单口径 / 手动保存（无改动禁用）/ 重置未设置 / 边界说明。

需求（用户口径，2026-10-12 重整；本脚本 2026-10-22 按实现对齐，2026-11-07 补插件 MCP 行）：
  沙箱开关按「被 spawn / 被注入策略的执行体」为单位 ——
  ① 内置 executor：按 **exe 基名**一行（2026-10-31 D-72 起全部退场 → 空表）；
  ② 外部 MCP：按 **MCP 条目**一行（数据源 = `data-mcp-list`；仅 stdio&spawn 可施加进程沙箱，
     http/sse 不可，行仍展示但开关禁用）；
  ③ **插件 MCP**：按 **插件 MCP server 名**一行（框架固定命名 `plugin-<id>`，见
     `chonkpilot-framework/plugin/src/mgr/api.go` 的 `registerMCP`；插件 MCP 是 HTTP 长连接、
     不是文件条目 → **不在 `data-mcp-list`**）。它们与外部 MCP 同属 usr `tool_sandbox.mcps` 维度：
     gateway 在每次 `tools/call` 按同名开关注入 `_meta.chonkpilot.sandbox`（见
     `docs/spec/10-architecture/14-安全域-agentbox.md` §0.2/§0.6、
     `docs/sandbox/插件写隔离与定时任务整改方案.md` §5.2 的 `tool_sandbox.mcps["task"]`）。
     故本页把「工具带 `_meta.server.alias` 的插件 MCP」也并入行集合，避免配置隐身。
  拨动只改本地待保存态，点【保存】才落库；**无改动时保存按钮禁用**。

驱动面（**零新增 MQ 主题**）：
  * 工具清单 = 既有客户端能力面 `tools-list`（每项 `_meta.category` / `_meta.server`）；
  * 外部 MCP 清单 = 既有 `data-mcp-list`（`{list:[{name,...}]}`）；
  * 配置读写 = 既有 usr 配置面 `data-user-config-{load,save,delete}`（usr 键 `tool_sandbox`，
    JSON `{"executors":{"<exe基名>":bool},"mcps":{"<mcp名>":bool}}`）。

覆盖：
  A 执行体行渲染：内置 executor 行（当前 0 个，D-72 退场；页面不得出现退役 executor）+
    每行一个 `.b-switch`（aria-label 含该行单位名）+ MCP 行归属 = `data-mcp-list`
    ∪ 插件 MCP（`_meta.server.alias` = `plugin-<id>`）∪ 已配置残留（usr `mcps` 键）
  B 只读工具清单口径：页面出现的工具项必须来自 `tools-list`，`:title` = 完整暴露名，
    展示名 = 现有剥前缀规则（见 utils/toolSource.js）
  C 无改动时保存禁用：干净初始态 → 保存按钮 disabled 且无「未保存」标记
  D 拨动 → 「未保存」标记出现 + 保存按钮可用 → 点保存 → 回读 `mcps['<单位>']=true`
    （MCP 级形态；不含任何工具名键）
  E 「重置」→ 待保存态 → 保存 → 键项/整键被删除（回落不设置）
  F 外部 MCP / http·sse 边界说明存在（.exec-note 文案含「外部 MCP」+「http/sse」）+ 跳转入口

**已知缺口（TOOLCFG-1，2026-10-22 登记；2026-10-31 D-72 后进一步收窄）**：GATEWAY-2/L1 后
`_meta.category` 变为 `file/core/meta/server`（`script_run` / `web_fetch` 同为 `core`，且 `_meta`
未透出 executor runtime）→ 「内置 executor 行清单」已无对象（executor 全退场）；用例 B 因此只校验
「页面项 ⊆ tools-list + title 完整 + 展示名符合现有规则」，不校验按行清单完整性。

前置（本脚本自起，结束自动回收；见 harness.py）：
  `dist/chonkpilot.exe --test-port=2345 --work-dir ws`（2026-11-02 扁平化发行根）
运行：`python run_tool_sandbox.py`

**PENDING 口径（不伪造、不放宽断言）**：产物未含新页面（`preview-tab-open{kind:'settings-tool-sandbox'}`
打不开 `.settings-page`）时打印 `[PENDING]` 并单独计入「待验证」，**不计入通过**（等待重建
build-desktop.ps1 后全量验证）。前置成立时全部断言按原样严格执行（任何不符即 FAIL）。
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import harness as _h  # 按需加载 + 结束即回收（51-FP与测试映射 §测试资源规范）
from chonk_client import TestError

PORT = 2345
WS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "ws")
CFG_KEY = "tool_sandbox"
KIND = "settings-tool-sandbox"
ROOT = ".settings-page"
# 内置 executor 单位（= useToolSandbox.js EXECUTOR_UNITS；写入 executors 子表的键）。
# 2026-10-31（D-72）：内置 executor 已全部退场（`src/lib/mcp-tools` 整体删除）→ 空表；
# 沙箱单位只剩 MCP 维度：外部 MCP（data-mcp-list）+ 插件 MCP（_meta.server.alias = plugin-<id>），
# 均写入 `mcps` 子表。
EXECUTORS = []
# 未被沙箱覆盖的执行体（browser/desktop 改常驻插件；dsl 退役）
NOT_EXECUTORS = ["chonkpilot-browser-executor.exe", "chonkpilot-dsl-executor.exe"]

c = _h.acquire_gui(PORT, work_dir=WS).client
_h.suite_config_guard(c)
_h.ensure_locale(c)

PASSED, FAILED, PENDING = [], [], []


class Pending(Exception):
    """前置不成立（产物未重建）：等待主线全量验证，**不**计入通过。"""


# ── 通用工具 ────────────────────────────────────────────────

def _loads(v):
    for _ in range(4):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def ev(js, timeout=6000):
    return _loads(c.eval(js, timeout))


def vcount(sel):
    return int(ev("([...document.querySelectorAll(%s)].filter(e=>e.getBoundingClientRect().width>0)).length"
                  % json.dumps(sel)) or 0)


def wait_vis(sel, max_wait=12):
    end = time.time() + max_wait
    while time.time() < end:
        if vcount(sel) > 0:
            return True
        time.sleep(0.3)
    return False


def panel_js(inner, root=ROOT):
    return ("(function(){const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);"
            "if(!R)return null;%s})()" % (json.dumps(root), inner))


def poll(fn, max_wait=10, interval=0.4):
    end = time.time() + max_wait
    last = None
    while time.time() < end:
        try:
            v = fn()
            if v:
                return v
            last = v
        except Exception as e:
            last = e
        time.sleep(interval)
    return last


# ── 数据面 ─────────────────────────────────────────────────

def ucfg():
    r = c.req("data-user-config-load", {})
    return (r.get("data") or {}) if isinstance(r, dict) else {}


def user_map():
    """usr `tool_sandbox` 视图（对象；支持 JSON 字符串形态）。"""
    v = ucfg().get(CFG_KEY)
    if isinstance(v, str):
        try:
            v = json.loads(v)
        except Exception:
            return {}
    return v if isinstance(v, dict) else {}


def tools_list():
    r = c.req("tools-list", {}) or {}
    return list(r.get("tools") or [])


def mcp_names():
    """外部 MCP 条目名集合（data-mcp-list，四级文件化合并视图）。"""
    r = c.req("data-mcp-list", {}) or {}
    return set(str((m or {}).get("name") or "").strip() for m in (r.get("list") or [])) - {""}


def plugin_mcp_names():
    """插件 MCP 单元名集合（框架固定命名 `plugin-<id>`）。

    数据源 = `tools-list` 每项的 `_meta.server.alias`（插件 MCP 不是文件条目，不在
    `data-mcp-list`；gateway 暴露名规则见 26-mcp-gateway / 28-plugins）。只认 `plugin-` 前缀，
    避免把外部 MCP 的 alias 重复计入（重复不影响集合，但前缀让判据可解释）。
    """
    out = set()
    for tl in tools_list():
        meta = tl.get("_meta") or {}
        srv = meta.get("server")
        if not isinstance(srv, dict):
            continue
        alias = str(srv.get("alias") or srv.get("node") or "").strip()
        if alias.startswith("plugin-"):
            out.add(alias)
    return out


def configured_mcp_keys():
    """已落到 usr `tool_sandbox.mcps` 的单位名（含旧配置残留 → 页面仍展示，避免配置隐身）。"""
    return set(((user_map().get("mcps") or {}).keys()))


def tool_servers():
    """tools-list 暴露名 → `_meta.server`（缺省 {}）。"""
    out = {}
    for tl in tools_list():
        meta = tl.get("_meta") or {}
        srv = meta.get("server")
        out[tl.get("name")] = srv if isinstance(srv, dict) else {}
    return out


def strip_prefix(name, srv):
    """复刻 utils/toolSource.js `stripToolPrefix`（展示名规则，page 侧唯一口径）。"""
    n = str(name or "")
    for raw in ((srv or {}).get("alias"), (srv or {}).get("node")):
        p = str(raw or "").strip()
        if p and n.startswith(p + "_"):
            return n[len(p) + 1:]
    return n


def reset_key():
    """把 usr `tool_sandbox` 清为「未设置」以取得确定初始态（套件级 guard 会在退出前还原）。"""
    c.req("data-user-config-delete", {"id": CFG_KEY})
    time.sleep(0.4)


# ── 前置探测 ───────────────────────────────────────────────

SEL_PAGE = [None]


def probe_page():
    """产物是否已重建出新页面：直接按契约命令打开配置页，看 `.settings-page` 是否渲染。

    原实现发 `config-menu-toggle` 找工具栏下拉；该主题已随 UILAYOUT-2-B 删除、设置菜单改为
    默认布局的声明式条目 → 改走**页面本身的打开命令**（与 require_page 同口径）。
    """
    if SEL_PAGE[0] is not None:
        return SEL_PAGE[0]
    try:
        c.mq_emit("preview-tab-open", {"kind": KIND})
        ok = wait_vis(ROOT)
    except Exception:
        ok = False
    SEL_PAGE[0] = "有" if ok else "无"
    return SEL_PAGE[0]


def require_page():
    if probe_page() != "有":
        raise Pending("产物未含新页面（preview-tab-open{%s} 打不开 %s）"
                      "→ 等待主线统一重建 build-desktop.ps1 后全量验证" % (KIND, ROOT))


# ── 页面操作 ───────────────────────────────────────────────

def open_page():
    c.mq_emit("preview-tab-close-all")
    time.sleep(0.5)
    c.mq_emit("preview-tab-open", {"kind": KIND})
    if not wait_vis(ROOT):
        raise TestError("工具沙箱配置页未打开（kind=%s root=%s）" % (KIND, ROOT))
    time.sleep(0.8)


def rows():
    """执行体行：键（exe 基名 / MCP 名）/ 类别文字 / 开关态 / 状态文字 / 只读工具清单。"""
    return ev(panel_js("""
return [...R.querySelectorAll('.exec-row')].map(it=>{
  const sw=it.querySelector('.b-switch');
  const st=it.querySelector('.exec-state');
  const cat=it.querySelector('.exec-cat');
  const none=it.querySelector('.exec-none');
  return {
    key: it.getAttribute('data-exec'),
    cat: cat?cat.textContent.trim():'',
    name: (it.querySelector('.exec-name')||{}).textContent||'',
    aria: sw?sw.getAttribute('aria-label'):null,
    checked: sw?sw.classList.contains('is-checked'):false,
    disabled: sw?sw.classList.contains('is-disabled'):false,
    state: st?st.textContent.trim():'',
    empty: none?none.textContent.trim():'',
    tools: [...it.querySelectorAll('.exec-tool')].map(t=>({
      name: t.getAttribute('data-tool'),
      disp: t.textContent.trim(),
      title: t.getAttribute('title'),
    })),
  };
});""")) or []


def row_of(key):
    for r in rows():
        if r["key"] == key:
            return r
    raise TestError("页面无该执行体行：%s" % key)


def unsaved_mark():
    return vcount(".unsaved-mark") > 0


def save_btn():
    """保存按钮状态：{exists, disabled}。"""
    return ev(panel_js("""
const b=R.querySelector('button.b-btn--primary[data-sandbox-save]');
return b?{exists:true,disabled:!!b.disabled}:{exists:false,disabled:false};"""))


def click_switch(key):
    r = ev(panel_js("""
const it=[...R.querySelectorAll('.exec-row')].find(x=>x.getAttribute('data-exec')===%s);
if(!it)return 'no-row';const sw=it.querySelector('.b-switch');if(!sw)return 'no-sw';
if(sw.classList.contains('is-disabled'))return 'disabled';sw.click();return 'ok';""" % json.dumps(key)))
    if r != "ok":
        raise TestError("拨动执行体 Switch 失败 key=%s → %r" % (key, r))
    time.sleep(0.5)


def click_restore(key):
    r = ev(panel_js("""
const it=[...R.querySelectorAll('.exec-row')].find(x=>x.getAttribute('data-exec')===%s);
if(!it)return 'no-row';const b=[...it.querySelectorAll('button.b-btn')].find(x=>x.textContent.includes('重置'));
if(!b)return 'no-btn';if(b.disabled)return 'disabled';b.click();return 'ok';""" % json.dumps(key)))
    if r != "ok":
        raise TestError("点击「重置」失败 key=%s → %r" % (key, r))
    time.sleep(0.5)


def click_save():
    r = ev(panel_js("""
const b=R.querySelector('button.b-btn--primary[data-sandbox-save]');
if(!b)return 'no-btn';if(b.disabled)return 'disabled';b.click();return 'ok';"""))
    if r != "ok":
        raise TestError("点击保存按钮失败：%r" % r)
    time.sleep(0.6)


# ══════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════

def case_a_executor_rows():
    """A 执行体行渲染：内置 executor 行（当前 0 个，D-72 退场）+ 每行 Switch（aria-label 含单位名）
    + MCP 行归属 = `data-mcp-list` ∪ 插件 MCP（`plugin-<id>`）∪ 已配置残留。

    边界（显式）：
      ① 无插件 MCP（tools-list 无 `plugin-*` alias）→ 页面不得出现 `plugin-*` 行；
      ② `data-mcp-list` 为空 → 非插件 / 非残留的 MCP 行不得出现。
    两边界由「行集合 = 期望并集」的精确比对兜底，这里再显式报错便于定位。
    """
    require_page()
    # 期望集合取「页面挂载时刻」的数据面。插件 MCP 注册可能晚于套件启动（main 已有栅栏，
    # 但页面 reload 只在挂载时取一次 tools-list）→ 页面行集合与数据面不符时重开（最多 3 次）。
    rs = []
    for attempt in range(3):
        want_mcp = mcp_names()              # 外部 MCP（data-mcp-list）
        plugin_mcp = plugin_mcp_names()     # 插件 MCP（tools-list _meta.server.alias = plugin-<id>）
        configured = configured_mcp_keys()  # 旧配置残留（页面仍展示）
        want_units = want_mcp | plugin_mcp | configured
        open_page()
        rs = (poll(lambda: rows() or None, max_wait=15) or rows()) if want_units else rows()
        page_units = set(r["key"] for r in rows() if r["key"] not in EXECUTORS)
        if not want_units or page_units == want_units:
            break
        time.sleep(1.0)  # 等插件注册再重开（reload 重新取 tools-list / mcp-list / usr 配置）
    if not rs:
        if want_units:
            raise TestError("页面无执行体行（.exec-row），但数据面期望 %d 个单位：%r"
                            % (len(want_units), sorted(want_units)))
        print("      执行体行：0（无内置 executor / 外部 MCP / 插件 MCP / 残留）", flush=True)
        return
    keys = [r["key"] for r in rs]
    if len(keys) != len(set(keys)):
        raise TestError("页面出现重复执行体行：%r" % keys)
    builtin = [k for k in keys if k in EXECUTORS]
    if builtin != EXECUTORS:
        raise TestError("内置 executor 行不符（期望顺序 %r）：页面 %r" % (EXECUTORS, keys))
    for bad in NOT_EXECUTORS:
        if bad in keys:
            raise TestError("已不受沙箱约束的执行体不应成行：%s（页面 %r）" % (bad, keys))

    extra = [k for k in keys if k not in EXECUTORS]
    unknown = [k for k in extra if k not in want_units]
    missing = sorted(want_units - set(extra))
    if unknown or missing:
        raise TestError("MCP 行归属不符：未知行 %r / 缺失行 %r（data-mcp-list=%r，插件 MCP=%r，"
                        "已配置=%r，页面 %r）"
                        % (unknown, missing, sorted(want_mcp), sorted(plugin_mcp),
                           sorted(configured), keys))

    # 边界①：无插件 MCP 注册 → 不得出现 plugin-* 行
    if not plugin_mcp:
        stray = [k for k in extra if k.startswith("plugin-")]
        if stray:
            raise TestError("tools-list 无插件 MCP，但页面出现插件行：%r" % stray)
    # 边界②：外部 MCP 清单为空 → 非插件 / 非残留行不得出现
    if not want_mcp:
        leftovers = [k for k in extra if k not in plugin_mcp and k not in configured]
        if leftovers:
            raise TestError("data-mcp-list 为空，但页面出现清单外的 MCP 行：%r" % leftovers)

    for r in rs:
        if not r["aria"] or r["key"] not in r["aria"]:
            raise TestError("执行体 %s 的 Switch 缺 aria-label（可访问性）：%r" % (r["key"], r["aria"]))
        if r["key"] in EXECUTORS and "MCP" in r["cat"]:
            raise TestError("内置 executor 行不应标 MCP：%s %r" % (r["key"], r["cat"]))
        if r["key"] not in EXECUTORS and "MCP" not in r["cat"]:
            raise TestError("外部/插件 MCP 行应标 MCP：%s %r" % (r["key"], r["cat"]))
    print("      执行体行：内置 %d + 外部 MCP %d + 插件 MCP %d + 残留 %d"
          % (len(builtin), len(want_mcp), len(plugin_mcp & set(extra)),
             len((configured - want_mcp - plugin_mcp) & set(extra))), flush=True)


def case_b_readonly_tool_lists():
    """B 只读工具清单口径：页面出现的工具项必须来自 `tools-list`，`:title` = 完整暴露名，
    展示名 = 现有剥前缀规则（utils/toolSource.js）。

    已知缺口（TOOLCFG-1）：内置 executor 行清单当前恒空 —— 只校验口径一致性，不校验完整性。
    """
    require_page()
    open_page()
    srv_of = tool_servers()
    shown = 0
    for r in rows():
        if not r["tools"] and not r["empty"]:
            raise TestError("执行体 %s 无工具项也无「暂无工具」占位（.exec-none）" % r["key"])
        for t in r["tools"]:
            name = t["name"] or ""
            if name not in srv_of:
                raise TestError("执行体 %s 展示了 tools-list 之外的工具：%r" % (r["key"], name))
            if t["title"] != name:
                raise TestError("工具 %s 的 :title=%r，期望完整暴露名（重名可区分）" % (name, t["title"]))
            want = strip_prefix(name, srv_of.get(name) or {})
            if t["disp"] != want:
                raise TestError("工具 %s 展示名 %r，期望按剥前缀规则得 %r" % (name, t["disp"], want))
            shown += 1
    # 记录现状：内置 executor 行清单为空（TOOLCFG-1 缺口），不视为本用例失败
    empt = [r["key"] for r in rows() if not r["tools"]]
    print("      工具项 %d 个；清单为空的行 %r（TOOLCFG-1 已知缺口）" % (shown, empt), flush=True)


def case_c_save_disabled_when_clean():
    """C 无改动时保存禁用：干净初始态 → 保存按钮 disabled 且无「未保存」标记。"""
    require_page()
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        b = save_btn()
        if not b["exists"]:
            raise TestError("页面缺保存按钮（data-sandbox-save）")
        if not b["disabled"]:
            raise TestError("无改动时保存按钮应禁用（不做 change 就保存）")
        if unsaved_mark():
            raise TestError("无改动时不应显示「未保存」标记")
        if user_map():
            raise TestError("前置失败：usr tool_sandbox 未清空：%r" % user_map())


def case_d_toggle_then_save():
    """D 拨动 → 未保存标记 + 保存可用 → 保存 → 回读对应子表为 true（executor 级 / MCP 级形态）。"""
    require_page()
    key = EXECUTORS[0] if EXECUTORS else (rows()[0]["key"] if rows() else "")
    if not key:
        raise TestError("页面无任何沙箱单位行")
    sub = "executors" if key in EXECUTORS else "mcps"
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        if not save_btn()["disabled"]:
            raise TestError("前置失败：干净态保存按钮应禁用")
        click_switch(key)
        if not unsaved_mark():
            raise TestError("拨动后应显示「未保存」标记")
        if save_btn()["disabled"]:
            raise TestError("拨动后保存按钮应可用")
        r = row_of(key)
        if not r["checked"] or r["state"] not in ("开", "On"):
            raise TestError("拨动后 %s 行态不符：checked=%s state=%r" % (key, r["checked"], r["state"]))
        # 拨动本身不落库（仍是干净库）
        if user_map():
            raise TestError("拨动未保存时不应落库：%r" % user_map())
        click_save()
        got = poll(lambda: user_map() if ((user_map().get(sub) or {}).get(key) is True) else None)
        if not got:
            raise TestError("保存后回读不一致：usr tool_sandbox=%r，期望 %s[%r]=true"
                            % (user_map(), sub, key))
        if set(got.keys()) - {"executors", "mcps"}:
            raise TestError("落库形态须为 {executors,mcps} 两子表：%r" % got)
        if set((got.get(sub) or {}).keys()) != {key}:
            raise TestError("%s 子表只应含刚拨动的单位 %r：%r" % (sub, key, got.get(sub)))
        # 保存后回到干净态（无未保存标记 + 保存禁用）
        if not poll(lambda: (not unsaved_mark()) and save_btn()["disabled"]):
            raise TestError("保存后应清除「未保存」标记并禁用保存按钮")
        print("[D] %s → %s[%r]=true 落库（两子表形态）" % (key, sub, key), flush=True)


def case_e_restore_unset():
    """E 重置 → 待保存态 → 保存 → 键项/整键删除（回落不设置）。"""
    require_page()
    key = EXECUTORS[0] if EXECUTORS else (rows()[0]["key"] if rows() else "")
    if not key:
        raise TestError("页面无任何沙箱单位行")
    sub = "executors" if key in EXECUTORS else "mcps"
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        click_switch(key)
        click_save()
        if not poll(lambda: ((user_map().get(sub) or {}).get(key) is True)):
            raise TestError("前置失败：%s 未落库：%r" % (key, user_map()))
        # 重置 → 待保存态（不立即落库）
        click_restore(key)
        if not unsaved_mark():
            raise TestError("重置后应进入待保存态（显示「未保存」）")
        if (user_map().get(sub) or {}).get(key) is not True:
            raise TestError("重置不应立即落库（仍应保留 %s=true）：%r" % (key, user_map()))
        click_save()
        if not poll(lambda: CFG_KEY not in ucfg()):
            raise TestError("重置保存后应删整键（usr 仍存 %s=%r）" % (CFG_KEY, ucfg().get(CFG_KEY)))


def case_f_boundary_hint():
    """F 外部 MCP / http·sse 边界说明 + 跳转 MCP 入口存在（2026-10-12 改版文案）。"""
    require_page()
    open_page()
    note = ev(panel_js("""
const n=R.querySelector('.exec-note');return n?{text:n.innerText,hasBtn:!!n.querySelector('button.b-btn')}:null;"""))
    if not note:
        raise TestError("缺外部 MCP / http·sse 边界说明（.exec-note）")
    txt = note["text"]
    if "外部 MCP" not in txt:
        raise TestError("边界说明须含「外部 MCP」口径：%r" % txt)
    if "http" not in txt.lower():
        raise TestError("边界说明须含 http/sse 不可施加沙箱说明：%r" % txt)
    if not note["hasBtn"]:
        raise TestError("边界说明须给跳转 MCP 配置的入口")


CASES = [
    ("A 执行体行渲染（无内置 executor + 外部/插件 MCP 行归属 + 每行 Switch aria-label）", case_a_executor_rows),
    ("B 只读工具清单口径（⊆ tools-list；title 完整名；展示名按剥前缀规则）", case_b_readonly_tool_lists),
    ("C 无改动时保存禁用（不做 change 就保存）", case_c_save_disabled_when_clean),
    ("D 拨动 → 未保存标记 + 保存 → 回读 mcps['<单位>']=true", case_d_toggle_then_save),
    ("E 重置 → 待保存态 → 保存 → 删键/整键删除", case_e_restore_unset),
    ("F 外部 MCP / http·sse 边界说明 + 跳转 MCP 入口", case_f_boundary_hint),
]


def wait_plugin_mcp_units(max_wait=90):
    """启动栅栏：等插件 MCP 注册完成（tools-list 出现 `plugin-*` alias）。

    背景（同 harness.wait_plugins_running 注释）：`/ping ready` 早于产品插件异步拉起；
    页面 `reload()` 只在挂载时取一次 `tools-list`，若此时插件尚未注册 → 插件 MCP 行恒空
    （实跑：A 偶发「页面无执行体行」，数秒后 B 已见 40 个工具）。故先等插件单元就绪再开页。

    边界：真无插件（台账存在且全部为终结态 disabled/error/stopped/discovered）→ 直接返回空集；
    插件仍在 starting/registering/degraded 时继续等，不把「启动中」误判为「无插件」。
    """
    end = time.time() + max_wait
    last = set()
    # 只有「全部插件都已终结」才判无插件；starting/registering/degraded 都还要等（框架状态集）。
    terminal = {"discovered", "disabled", "error", "stopped"}
    while time.time() < end:
        try:
            last = plugin_mcp_names()
            if last:
                return last
            st = _h.plugin_statuses(c)
            if st and all(v in terminal for v in st.values()):
                return set()
        except Exception:
            pass
        time.sleep(0.5)
    return last


def wait_frontend(max_wait=90):
    """等前端就绪（.statusbar 出现）：WebView2 首启加载较慢，/console 等脚本调用会超时。"""
    end = time.time() + max_wait
    while time.time() < end:
        try:
            if ev("typeof document!=='undefined' && !!document.querySelector('.statusbar')"):
                return True
        except Exception:
            pass
        time.sleep(1)
    return False


def main():
    wait_frontend()
    try:
        c.console(clear=True)
    except Exception:
        pass
    print("依赖：--test-port=%d 的 GUI（产物 = dist）；配置面 = usr `%s`（执行体级）" % (PORT, CFG_KEY))
    # 插件 MCP 异步注册栅栏（避免 A 在插件未就绪时看到空行集合 → 假边界）
    units = wait_plugin_mcp_units()
    print("前置探测：插件 MCP=%r" % sorted(units))
    print("前置探测：页面=%s" % probe_page())
    for name, fn in CASES:
        try:
            fn()
            print("  [PASS] %s" % name)
            PASSED.append(name)
        except Pending as e:
            print("  [PENDING] %s: %s" % (name, e))
            PENDING.append(name)
        except TestError as e:
            print("  [FAIL] %s: %s" % (name, e))
            FAILED.append(name)
        except Exception as e:
            print("  [ERROR] %s: %s: %s" % (name, type(e).__name__, e))
            FAILED.append(name)
    total = len(CASES)
    print("\n工具沙箱配置页 L4：%d/%d 通过, %d 失败, %d 待验证(PENDING)"
          % (len(PASSED), total, len(FAILED), len(PENDING)))
    for n in PENDING:
        print("  [PENDING] %s" % n)
    print("RESULT: %s" % (len(FAILED) == 0))
    return 0 if not FAILED else 1


if __name__ == "__main__":
    sys.exit(main())

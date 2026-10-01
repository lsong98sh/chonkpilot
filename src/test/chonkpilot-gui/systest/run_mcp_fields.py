# -*- coding: utf-8 -*-
"""MCP 配置字段端到端补齐（P3 批次）：**A 落库逐字段回读** + **B 可观测真实效果**。

背景（已审计缺口，2026-09-17）：`EditMCPDialog.vue` 的字段中 env / headers / cwd / timeout /
hot_tools 只被保存、从未被回读；isolate（三态）无回读；`transport=stdio` 的落库值无直接断言；
cwd / env / args / isolate 的**效果**（子进程真实工作目录/环境变量/隔离连接）无任何观测。
（name / url / enabled / description / runtime / args 已有覆盖：run_config.py:203-220、
run_config_ui.py B2、run_tool_async.py:169-198。）

覆盖矩阵（每条 = A + B；B 必须有可观测证据，不以「保存成功」充数）
  M1 全字段哨兵落库（A）  ：一条 stdio 条目 + 一条 http 条目，每字段互不相同哨兵 →
      data-mcp-list 逐字段回读（env 多键 / headers 多键 / cwd / timeout /
      runtime / args / transport / description / url / enabled；hot_tools 默认空）
  M2 transport=stdio（B） ：回读 transport=="stdio"（M1）+ tools-list 出现该 server 工具
      （证明按 stdio 真实拉起了 runtime+args 子进程）+ probe_info 回显 argv == 配置 args
  M3 cwd 效果（B）        ：probe_info 回显**子进程真实 cwd** == 配置 cwd
  M4 env 效果（B）        ：probe_info 回显 PROBE_* 注入成功 + 值内 ${VAR} 展开语义
  M5 isolate 三态（A+B）  ：①未拨动 → 键不存在（= 按 transport 推断）+ 跨 work_dir 子进程 pid 不同；
      ②显式 true → 回读 true + 跨 work_dir pid 不同；③显式 false → 回读 false + 跨 work_dir pid 相同
  M8 sandbox 三态（A）    ：①stdio 未拨动 → 缺键；拨开 → 回读 true；拨关 → 回读 false；
      ②http 条目开关禁用（仅 stdio 的 spawn 可隔离）
  M6 hot_tools 效果（B）  ：经「工具」页签【加载工具】后勾选单个工具 → 写库为**下游原名** →
      该工具 _meta.hot=true（未列入者无）；清空 → 标记消失；并做**重启后工具面**对照
      （重启 = 启动期装配路径，同样带 hot_tools → _meta.hot 不变）
M9 hot_tools「全部」+ 空态（A+B）：勾「全部」→ 写库 `["*"]` → 全部工具 _meta.hot=true；
      另验空态（无工具 server → 提示而非列表）+ 清单展示名 = 原名、data-tool = 暴露名
  M7 enabled=false（B）   ：列表开关点击保存 → 工具面**不含**该 server 工具（保存即生效 T-25）

2026-09-26 弹窗重构：拆「基本信息 / 运行信息」两页签（Tabs 只渲染当前页签 → 跨页字段先切页签）；
「分类」输入摘除（后端字段保留）；isolate / sandbox 归入「运行信息」页。
2026-09-27 重排：新增第 3 页签「工具」承载高频工具勾选（顶部【加载工具】按钮，点击后才读取
`tools-list`；「全部工具」+ 逐项 checkbox），**移除原独立弹窗 SetMCPHotToolsDialog**；
「启用」移至底栏（与保存同一行）；headers/env/args 改行编辑器（KeyValueEditor，kv/list 模式）。
写库仍为**原名**列表（`"*"` = 全部 hot），主对话框「保存」时才落库（零新增消息面）。

观测渠道（**全部为 61-消息一览既有主题，零新增**）
  data-mcp-{list,save,delete}（§3.1，2026-10-01 起 MCP 四级文件化）· tools-list（§4.5 客户端能力面）
  · chonk.mcp-tools-call（§5.1 方法面，点分相对主题直通、与 run_tool_async 同法）
驱动：真实前端弹窗 EditMCPDialog（config-add-mcp / config-edit-mcp / config-toggle-mcp / edit-mcp-save）
配置写入 = 四级文件化 MCP（`<级别>/capability/mcps/<名>.json`）；清理走 `data-mcp-delete`（按名）——
  usr/prj 业务配置写入仍走 harness 的套件级快照-还原（`suite_config_guard`，51 §6-8）→ 退出前回滚。

前置：dist-desktop\\chonkpilot.exe（本套件自起**隔离实例**：动态端口 + 私有 work-dir +
      独立 USERPROFILE/home，见下）；
      夹具 = 本目录 mock_mcp_probe.py（纯标准库 stdio MCP，回显 cwd/pid/argv/env）。
运行：python run_mcp_fields.py
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402
import harness as _h  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
FIXTURE = os.path.join(HERE, "mock_mcp_probe.py")
PY = sys.executable or "python"

# 隔离实例（动态端口 + 私有 work-dir + 独立 USERPROFILE）：本套件写真实配置（usr mcps / 台账），
# 与他套件并行时不共用实例/端口/库，避免驱动同一 WebView2 与配置互写（用例本身不依赖机器既有配置）。
WS = _h.tmp_dir("ck-mcpf-ws-")   # 私有 work-dir（= 隔离观测的 project A）
HOME = _h.tmp_home()             # 独立 usr 主库根（重启后复用 → 配置跨重启仍在）
_g = _h.acquire_gui(work_dir=WS, home=HOME)
c = _g.client
PORT = _g.port
# 套件级快照-还原（51 §6-8）：传 callable → M6 中途重启 GUI 后还原仍用**当前** client
_h.suite_config_guard(lambda: c)
_h.ensure_locale(c)       # 语言确定性：本套件按 zh-CN 文案/弹窗 label 断言
print("[run_mcp_fields] 隔离实例 port=%d work_dir=%s home=%s pid=%s" % (PORT, WS, HOME, _g.pid()), flush=True)

# ── 哨兵（每字段互不相同，便于定位「哪个字段没落库」）──────────────
NAME = "mcpfld3p"
HTTP_NAME = "mcpfldhttp"
TOOL_INFO = NAME + "_probe_info"      # 暴露名 = 默认前缀 <id>_ + 下游原名
TOOL_PID = NAME + "_probe_pid"
MARKER = "M-9917"
ARGV = [FIXTURE, "--marker", MARKER]
DESC = "probe-desc-4477-描述哨兵"
TIMEOUT_S = 7
HTTP_TIMEOUT_S = 5
URL = "http://127.0.0.1:9/probe"
HEADERS = {"X-Probe-One": "one-5512", "X-Probe-Two": "two-6613"}
HOT = ["probe_info"]                  # hot_tools 口径 = **下游原名**（registry.isHot）
ENV_LINES = ["PROBE_A=alpha-3391", "PROBE_B=beta-8246", "PROBE_EXPANDED=${SystemRoot}"]
ENV_NAMES = [kv.split("=", 1)[0] for kv in ENV_LINES]
CWD_DIR = _h.tmp_dir("ck-mcpf-cwd-")  # 配置给子进程的工作目录（必须真实存在才能 spawn）
WD_A = WS                             # 隔离观测用 project A（= GUI 工作目录）
WD_B = _h.tmp_dir("ck-mcpf-wd2-")     # 隔离观测用 project B（另一个 work_dir）

LBL_TAB_BASIC = ["基本信息", "Basic"]
LBL_TAB_RUNTIME = ["运行信息", "Runtime"]
LBL_TAB_TOOLS = ["工具", "Tools"]
LBL_NAME = ["名称", "Name"]
LBL_ENABLED = ["启用", "Enabled"]
LBL_URL = ["服务地址", "Server URL"]
LBL_RUNTIME = ["运行时", "Runtime"]
LBL_ARGS = ["启动参数", "Args"]
LBL_CWD = ["工作目录", "Cwd"]
LBL_ISO = ["按实例隔离", "Per-instance"]
LBL_SANDBOX = ["沙箱", "Sandbox"]
LBL_TIMEOUT = ["超时", "Timeout"]
LBL_DESC = ["描述", "Description"]
LBL_ENV = ["环境变量", "Env"]
LBL_HEADERS = ["请求头", "Headers"]


# ── 通用工具 ────────────────────────────────────────────────

def _loads(v):
    """循环解包 JSON 字符串（测试通道 eval 结果可能被多重编码）。"""
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


def wait_for(pred, desc, max_wait=20, interval=0.4):
    deadline = time.time() + max_wait
    while True:
        v = pred()
        if v:
            return v
        if time.time() >= deadline:
            raise TestError("等待超时：%s" % desc)
        time.sleep(interval)


def vcount(sel):
    return int(ev("([...document.querySelectorAll(%s)].filter(e=>e.getBoundingClientRect().width>0)).length"
                  % json.dumps(sel)) or 0)


def wait_vis(sel, max_wait=12):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if vcount(sel) > 0:
            return True
        time.sleep(0.3)
    return False


# ── 配置读写（既有消息面）────────────────────────────────────

def mcp_entries():
    """读四级文件化 MCP **生效视图**（data-mcp-list；2026-10-01 起，替代读取 usr `mcps`）。"""
    res = c.req("data-mcp-list", {})
    return list((res or {}).get("list") or [])


def entry_of(name):
    for m in mcp_entries():
        if isinstance(m, dict) and m.get("name") == name:
            return m
    return None


def index_of(name):
    for i, m in enumerate(mcp_entries()):
        if isinstance(m, dict) and m.get("name") == name:
            return i
    raise TestError("配置中不存在条目 %s（当前 %r）" % (name, [m.get("name") for m in mcp_entries()]))


def remove_entries(names):
    """幂等清理同名残留（2026-10-01 起按名删四级文件化 MCP 文件；页面未挂载时调用亦一致）。"""
    existing = {m.get("name") for m in mcp_entries() if isinstance(m, dict)}
    for n in names:
        if n in existing:
            c.req("data-mcp-delete", {"name": n}, timeout=15000)


def expect(entry, key, want, label):
    if entry is None:
        raise TestError("%s：条目不存在（当前 %r）" % (label, [m.get("name") for m in mcp_entries()]))
    got = entry.get(key)
    if got != want:
        raise TestError("%s 回读不符：got=%r want=%r" % (label, got, want))


def wait_entry_field(name, key, want, desc, max_wait=20):
    def _p():
        e = entry_of(name)
        if e is not None and e.get(key) == want and (not isinstance(want, bool) or isinstance(e.get(key), bool)):
            return e
        return None
    return wait_for(_p, "%s（%s.%s=%r，当前 %r）" % (desc, name, key, want, (entry_of(name) or {}).get(key)),
                    max_wait=max_wait)


# ── 工具面 / 网关方法面（既有主题）──────────────────────────

def tools_map():
    res = c.req("tools-list", {}) or {}
    out = {}
    for t in (res.get("tools") or []):
        if isinstance(t, dict) and t.get("name"):
            out[t["name"]] = t
    return out


def wait_tools_present(names, desc, max_wait=40):
    def _p():
        tm = tools_map()
        return tm if all(n in tm for n in names) else None
    tm = wait_for(_p, "%s（当前工具面 %d 个，缺 %r）"
                  % (desc, len(tools_map()), [n for n in names if n not in tools_map()]), max_wait=max_wait)
    return {n: tm[n] for n in names}


def wait_tools_absent(names, desc, max_wait=40):
    def _p():
        tm = tools_map()
        return tm if all(n not in tm for n in names) else None
    return wait_for(_p, "%s（仍在工具面：%r）" % (desc, [n for n in names if n in tools_map()]), max_wait=max_wait)


def _result_text(res):
    if not isinstance(res, dict):
        raise TestError("工具返回非对象：%r" % (res,))
    if res.get("isError"):
        raise TestError("工具返回 isError：%r" % (res,))
    parts = []
    for b in (res.get("content") or []):
        if isinstance(b, dict) and b.get("type") == "text":
            parts.append(b.get("text") or "")
    if not parts:
        raise TestError("工具返回无文本内容：%r" % (res,))
    return "\n".join(parts)


def gw_call(tool, args=None, work_dir=None, max_wait=40):
    """经 gateway 方法面 chonk.mcp-tools-call（§5.1）调用工具，返回解析后的 JSON 文本。"""
    payload = {"name": tool, "arguments": dict(args or {}), "tool_call_display_name": "探针"}
    if work_dir:
        payload["work_dir"] = work_dir
    res = c.req("chonk.mcp-tools-call", payload, timeout=max_wait * 1000)
    return json.loads(_result_text(res))


def probe_pid(work_dir):
    return int(gw_call(TOOL_PID, {}, work_dir=work_dir)["pid"])


# ── 弹窗操作（真实前端 EditMCPDialog）───────────────────────

def _dlg(inner):
    return ("(function(){const R=[...document.querySelectorAll('.dialog-shell')]"
            ".find(e=>e.getBoundingClientRect().width>0);if(!R)return null;"
            + inner + "})()")


_ITEM_JS = """
const cands=%s;
const it=[...R.querySelectorAll('.form-item')].find(x=>{const l=x.querySelector('.form-label');
  return l&&cands.some(cc=>l.textContent.includes(cc));});
"""


def open_page():
    c.mq_emit("preview-tab-close-all")
    time.sleep(0.5)
    c.mq_emit("preview-tab-open", {"kind": "settings-mcp"})
    if not wait_vis(".settings-page", 12):
        raise TestError("MCP 配置页未打开")
    time.sleep(0.8)


def restart_gui():
    """重启本套件**自起**的 GUI（同端口 / 同 work-dir / 同 HOME）→ 配置跨重启仍在。

    仅用于观测「启动期装配」路径（gateway_servers.loadGatewayServers → mergeGatewayServers）
    与热生效路径（reconcileUserMCPs → userMCPServerSpec）的差异；复用实例（owned=False）不重启。
    """
    global _g, c
    if not getattr(_g, "owned", False):
        raise TestError("GUI 非自起（owned=False）→ 不自动重启")
    _g.stop()
    time.sleep(1)
    _g = _h.start_gui(port=PORT, work_dir=WS, home=HOME)
    c = _g.client
    _h.ensure_locale(c)
    time.sleep(1.5)
    return c


def close_dialog():
    if vcount(".dialog-shell") > 0:
        c.mq_emit("edit-mcp-cancel")
        deadline = time.time() + 5
        while time.time() < deadline and vcount(".dialog-shell") > 0:
            time.sleep(0.2)


def open_new():
    close_dialog()
    c.mq_emit("config-add-mcp")
    if not wait_vis(".dialog-shell", 10):
        raise TestError("MCP 编辑弹窗未打开（config-add-mcp）")
    time.sleep(0.5)


def open_edit(index, want_name=None):
    """按前端列表索引打开编辑弹窗；打开后复核「名称」= want_name（防索引错位）。"""
    close_dialog()
    c.mq_emit("config-edit-mcp", {"index": int(index)})
    if not wait_vis(".dialog-shell", 10):
        raise TestError("MCP 编辑弹窗未打开（config-edit-mcp index=%s）" % index)
    time.sleep(0.5)
    if want_name is not None:
        got = field_value(LBL_NAME)
        if got != want_name:
            raise TestError("编辑弹窗索引错位：index=%s 期望条目 %r，实际 %r" % (index, want_name, got))


def open_edit_by_name(name):
    open_edit(index_of(name), want_name=name)


def save_dialog():
    """点保存（edit-mcp-save）→ 等弹窗关闭；未关闭 = 表单校验拦截（给出提示文本）。"""
    c.mq_emit("edit-mcp-save")
    deadline = time.time() + 8
    while time.time() < deadline:
        if vcount(".dialog-shell") == 0:
            time.sleep(0.4)
            return
        time.sleep(0.2)
    toast = ev("(function(){const m=document.querySelector('.b-message');return m?m.innerText:'';})()")
    raise TestError("弹窗未关闭（保存被校验拦截）：toast=%r" % toast)


def field_value(label_cands):
    return ev(_dlg(_ITEM_JS % json.dumps(label_cands, ensure_ascii=False) +
                   "if(!it)return null;const inp=it.querySelector('input,textarea');return inp?inp.value:null;"))


def item_has_help(label_cands):
    """该 form-item 的 label 右侧是否带 ? Tooltip 图标（.label-help）。

    2026-09-27：isolate 的说明文字由常显 .form-hint 改为 ? Tooltip（内容仅在 hover 时入 DOM），
    故此处只断言「? 入口存在」这一可观测结构，不读 tooltip 文案。
    """
    return ev(_dlg(_ITEM_JS % json.dumps(label_cands, ensure_ascii=False) +
                   "if(!it)return null;return !!it.querySelector('.label-help');"))


def fill(label_cands, value):
    r = ev(_dlg(_ITEM_JS % json.dumps(label_cands, ensure_ascii=False) +
                "if(!it)return 'no-item';const inp=it.querySelector('input,textarea');if(!inp)return 'no-input';"
                "const proto=inp instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;"
                "Object.getOwnPropertyDescriptor(proto,'value').set.call(inp,%s);"
                "inp.dispatchEvent(new Event('input',{bubbles:true}));"
                "inp.dispatchEvent(new Event('change',{bubbles:true}));return 'ok';" % json.dumps(value)))
    if r != "ok":
        raise TestError("填表失败（label≈%s）：%s" % (label_cands, r))
    time.sleep(0.15)


def _kv_js(label_cands, inner):
    """在可见弹窗内、按 label 定位的 form-item 上执行 inner（内嵌 it）；该 form-item 含 .kv-editor。"""
    return _dlg(_ITEM_JS % json.dumps(label_cands, ensure_ascii=False) +
                "if(!it)return 'no-item';if(!it.querySelector('.kv-editor'))return 'no-editor';" + inner)


def _kv_row_count(label_cands):
    return int(ev(_kv_js(label_cands, "return it.querySelectorAll('.kv-row').length;")) or 0)


def fill_row_editor(label_cands, values, kv=False):
    """把「……行编辑器」（KeyValueEditor）填成 values。

    KeyValueEditor 为受控行编辑：Vue 重渲染异步 → 增删行须**每次点击后 sleep** 再复核。
    kv=True 时按**第一个 '='** 切分，分别填 .kv-key / .kv-value（与 textToHeaders 口径一致）；
    kv=False（list 模式）整行填入 .kv-value。
    """
    # ① 先清空已有行
    for _ in range(_kv_row_count(label_cands)):
        r = ev(_kv_js(label_cands, "const d=it.querySelector('.kv-row .kv-del');if(!d)return 'no-del';d.click();return 'ok';"))
        if r != "ok":
            raise TestError("行编辑器删除失败（label≈%s）：%s" % (label_cands, r))
        time.sleep(0.15)
    # ② 逐行添加并赋值
    for i, v in enumerate(values):
        r = ev(_kv_js(label_cands, "const a=it.querySelector('.kv-add');if(!a)return 'no-add';a.click();return 'ok';"))
        if r != "ok":
            raise TestError("行编辑器添加失败（label≈%s）：%s" % (label_cands, r))
        time.sleep(0.2)
        if kv:
            k, _, val = str(v).partition("=")
            r = ev(_kv_js(label_cands,
                          "const rows=[...it.querySelectorAll('.kv-row')];const row=rows[%d];if(!row)return 'no-row';"
                          "const ki=row.querySelector('.kv-key');const vi=row.querySelector('.kv-value');"
                          "if(!ki||!vi)return 'no-input';"
                          "const sp=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;"
                          "sp.call(ki,%s);ki.dispatchEvent(new Event('input',{bubbles:true}));"
                          "sp.call(vi,%s);vi.dispatchEvent(new Event('input',{bubbles:true}));return 'ok';"
                          % (i, json.dumps(k), json.dumps(val))))
        else:
            r = ev(_kv_js(label_cands,
                          "const rows=[...it.querySelectorAll('.kv-row')];const row=rows[%d];if(!row)return 'no-row';"
                          "const vi=row.querySelector('.kv-value');if(!vi)return 'no-input';"
                          "Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(vi,%s);"
                          "vi.dispatchEvent(new Event('input',{bubbles:true}));return 'ok';" % (i, json.dumps(v))))
        if r != "ok":
            raise TestError("行编辑器填值失败（label≈%s, row=%d）：%s" % (label_cands, i, r))
        time.sleep(0.15)


def set_transport(value):
    """切换「传输方式」Select（Vue 从属显示：stdio → runtime/args；http/sse → 服务地址）。

    2026-10-01 起基本信息页新增「级别」Select（排在「传输方式」之前）→ 按 label 定位传输方式下拉，
    不再取「第一个可见 Select」。
    """
    r = ev(_dlg("const it=[...R.querySelectorAll('.form-item')].find(x=>{const l=x.querySelector('.form-label');"
                "return l&&l.textContent.includes('传输方式');});"
                "const s=it?it.querySelector('select.b-select__native'):null;if(!s)return 'no-select';"
                "Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(s,%s);"
                "s.dispatchEvent(new Event('change',{bubbles:true}));"
                "s.dispatchEvent(new Event('input',{bubbles:true}));return s.value;" % json.dumps(value)))
    time.sleep(0.5)
    if r != value:
        raise TestError("传输方式切换失败：%r（期望 %r）" % (r, value))


def switch_state(label_cands):
    return ev(_dlg(_ITEM_JS % json.dumps(label_cands, ensure_ascii=False) +
                   "if(!it)return null;const sw=it.querySelector('.b-switch');"
                   "return sw?sw.classList.contains('is-checked'):null;"))


def click_switch(label_cands):
    """点击开关（真实 .b-switch click）→ **等一帧后**回读 checked 态（Vue 重渲染是异步的）。"""
    r = ev(_dlg(_ITEM_JS % json.dumps(label_cands, ensure_ascii=False) +
                "if(!it)return 'no-item';const sw=it.querySelector('.b-switch');if(!sw)return 'no-switch';"
                "sw.click();return 'ok';"))
    if r != "ok":
        raise TestError("开关不可点（label≈%s）：%s" % (label_cands, r))
    time.sleep(0.5)
    return switch_state(label_cands)


def switch_enabled(label_cands):
    """开关是否可交互（.b-switch 无 is-disabled）。"""
    return ev(_dlg(_ITEM_JS % json.dumps(label_cands, ensure_ascii=False) +
                   "if(!it)return null;const sw=it.querySelector('.b-switch');"
                   "return sw?!sw.classList.contains('is-disabled'):null;"))


def footer_switch_state():
    """底栏「启用」开关状态（2026-09-27 起「启用」= label + Switch，位于 .edit-footer）。"""
    return ev(_dlg("const f=R.querySelector('.edit-footer .footer-enabled');if(!f)return null;"
                   "const sw=f.querySelector('.b-switch');return sw?sw.classList.contains('is-checked'):null;"))


def click_footer_switch():
    """点击底栏「启用」开关 → 等一帧回读（Vue 重渲染异步）。底栏固定显示，与当前页签无关。"""
    r = ev(_dlg("const f=R.querySelector('.edit-footer .footer-enabled');if(!f)return 'no-item';"
                "const sw=f.querySelector('.b-switch');if(!sw)return 'no-switch';sw.click();return 'ok';"))
    if r != "ok":
        raise TestError("底栏「启用」开关不可点：%s" % r)
    time.sleep(0.5)
    return footer_switch_state()


def click_tab(label_cands):
    """切换弹窗页签（.b-tabs-item，2026-09-27 起为「基本信息 / 运行信息 / 工具」三页签）。

    Tabs 只渲染当前页签内容 → 跨页字段必须先切页签再断言/填表。
    """
    r = ev(_dlg("const T=[...R.querySelectorAll('.b-tabs-item')].find(x=>%s.some(c=>x.textContent.includes(c)));"
                "if(!T)return 'no-tab';T.click();return 'ok';" % json.dumps(label_cands, ensure_ascii=False)))
    if r != "ok":
        raise TestError("切换页签失败（≈%s）：%s" % (label_cands, r))
    time.sleep(0.5)


# ── 「工具」页签（2026-09-27：原独立弹窗 SetMCPHotToolsDialog 已并入主对话框）──────
# 主对话框第 3 页签；点【加载工具】后才读取既有 tools-list（零新增消息面）。

def open_tools_tab():
    click_tab(LBL_TAB_TOOLS)


def load_tools():
    """点【加载工具】→ 等加载完成（data-loading 归零 + data-loaded=1）。

    `data-loading`/`data-loaded` 判据走 `str(st)=="0"/"1"`：测试通道 eval 结果会被 JSON 解析。
    """
    r = ev(_dlg("const b=R.querySelector('.tools-tab [data-load-tools]');if(!b)return 'no-btn';b.click();return 'ok';"))
    if r != "ok":
        raise TestError("【加载工具】按钮不可点：%s" % r)
    deadline = time.time() + 20
    while time.time() < deadline:
        st = ev(_dlg("const t=R.querySelector('.tools-tab');return t?t.getAttribute('data-loading'):'-';"))
        loaded = ev(_dlg("const t=R.querySelector('.tools-tab');return t?t.getAttribute('data-loaded'):'-';"))
        if str(st) == "0" and str(loaded) == "1":
            time.sleep(0.2)
            return
        time.sleep(0.3)
    raise TestError("工具加载未完成（data-loading=%r data-loaded=%r）" % (st, loaded))


def tools_hint():
    """未加载时的初始提示文字（非该态 → null）。"""
    return ev(_dlg("const t=R.querySelector('.tools-tab');if(!t)return null;"
                   "const h=t.querySelector('.tools-state');return h?h.textContent.trim():null;"))


def tools_rows():
    """当前「工具」页签工具行：[{exposed(data-tool), display, checked, disabled}]。"""
    return ev(_dlg("const t=R.querySelector('.tools-tab');if(!t)return [];"
                   "return [...t.querySelectorAll('.hot-tool-item')].map(x=>{"
                   "const n=x.querySelector('.hot-tool-name');"
                   "return {exposed:x.getAttribute('data-tool'),display:n?n.textContent.trim():'',"
                   "checked:!!x.querySelector('input').checked,disabled:!!x.querySelector('input').disabled};});"))


def toggle_tool(exposed):
    """按暴露名（data-tool）勾选/取消该工具（禁用态 → 报错）。"""
    r = ev(_dlg("const it=R.querySelector('.tools-tab .hot-tool-item[data-tool=%s]');"
                "if(!it)return 'no-item';const cb=it.querySelector('input');if(cb.disabled)return 'disabled';"
                "cb.click();return 'ok';" % json.dumps(exposed)))
    if r != "ok":
        raise TestError("工具勾选失败（%s）：%s" % (exposed, r))
    time.sleep(0.3)


def toggle_all():
    """勾/取消「全部工具（所有工具均高频）」复选框。"""
    r = ev(_dlg("const cb=R.querySelector('.tools-tab [data-hot-all]');if(!cb)return 'no-all';cb.click();return 'ok';"))
    if r != "ok":
        raise TestError("「全部工具」勾选失败：%s" % r)
    time.sleep(0.3)


def tools_empty():
    """空态提示文字（无工具行；非空态 → null）。"""
    return ev(_dlg("const t=R.querySelector('.tools-tab');if(!t)return null;"
                   "const e=t.querySelector('.tools-state');return e?e.textContent.trim():null;"))


# ══════════════════════════════════════════════════════════
# M1 全字段哨兵落库（A）
# ══════════════════════════════════════════════════════════

def case_m1_fields_roundtrip():
    """M1（A）：经 EditMCPDialog 逐字段填**互不相同哨兵** → 保存 → data-user-config-load 逐字段回读。

    条目 1 = stdio（runtime/args/cwd/env/timeout/description/enabled/transport；hot_tools 默认空）
    条目 2 = http（url/headers 多键/enabled=false）——headers 只在 http/sse 语义下有意义。
    2026-09-27 起弹窗为「基本信息 / 运行信息 / 工具」三页签（Tabs 只渲染当前页签）→ 填表按页签切换；
    headers/env/args 为行编辑器（KeyValueEditor）→ 用 fill_row_editor 逐行填；「启用」在底栏。
    2026-09-27 起「高频工具」并入「工具」页签 → 本用例只验默认落库空列表；
    实际勾选（原名 / "*"）由 M6 / M9 走【加载工具】+ 勾选覆盖。
    isolate 本用例**不拨动** → 断言落库无该键（三态之「未设置」）。
    """
    remove_entries([NAME, HTTP_NAME])  # 幂等清理（页面未挂载 → 索引与库一致）
    open_page()

    # ── 条目 1：stdio 全字段（基本信息页 + 运行信息页）──
    open_new()
    set_transport("stdio")               # 传输方式在「基本信息」页
    fill(LBL_NAME, NAME)
    fill(LBL_TIMEOUT, str(TIMEOUT_S))
    fill(LBL_DESC, DESC)
    click_tab(LBL_TAB_RUNTIME)           # 连接/运行字段在「运行信息」页
    fill(LBL_RUNTIME, PY)
    fill_row_editor(LBL_ARGS, ARGV)      # 启动参数：list 行编辑器（每行一个参数）
    fill(LBL_CWD, CWD_DIR)
    fill_row_editor(LBL_ENV, ENV_LINES, kv=True)  # 环境变量：K=V 行编辑器
    # isolate 三态 ①：未拨动 → 开关显示按 transport 推断（stdio → 开）；说明为 ? Tooltip
    if item_has_help(LBL_ISO) is not True:
        raise TestError("「按实例隔离」label 右侧应带 ? Tooltip 图标")
    if switch_state(LBL_ISO) is not True:
        raise TestError("transport=stdio 未拨动 isolate → 开关应显示推断值 true")
    # 「启用」已移至底栏（与保存同一行），与当前页签无关
    if click_footer_switch() is not True:
        raise TestError("点击底栏「启用」开关未置为开（DEFAULT_MCP.enabled=false）")
    save_dialog()
    e1 = wait_entry_field(NAME, "name", NAME, "条目 1 落库")
    print("    [M1] 条目 1 落库：%s" % json.dumps(e1, ensure_ascii=False)[:400])

    expect(e1, "enabled", True, "enabled")
    expect(e1, "transport", "stdio", "transport")
    expect(e1, "description", DESC, "description")
    expect(e1, "runtime", PY, "runtime")
    expect(e1, "args", ARGV, "args")
    expect(e1, "cwd", CWD_DIR, "cwd")
    expect(e1, "timeout", TIMEOUT_S, "timeout")
    # hot_tools：勾选改由 M6/M9 覆盖（本条目保存前未注册 → 「工具」页签加载亦无工具可勾）→ 默认空列表
    expect(e1, "hot_tools", [], "hot_tools（默认空）")
    expect(e1, "env", ENV_LINES, "env（多键）")
    if "isolate" in e1:
        raise TestError("isolate 未拨动 → 不应落库该键（应为「未设置」），实际 %r" % e1.get("isolate"))

    # ── 条目 2：http + headers 多键 + enabled=false（全部在「基本信息」页）──
    open_new()
    set_transport("http")
    fill(LBL_NAME, HTTP_NAME)
    fill(LBL_URL, URL)
    fill(LBL_TIMEOUT, str(HTTP_TIMEOUT_S))
    fill_row_editor(LBL_HEADERS, ["%s=%s" % (k, v) for k, v in HEADERS.items()], kv=True)
    save_dialog()
    e2 = wait_entry_field(HTTP_NAME, "name", HTTP_NAME, "条目 2 落库")
    print("    [M1] 条目 2 落库：%s" % json.dumps(e2, ensure_ascii=False)[:400])

    expect(e2, "enabled", False, "enabled(默认 false 未拨动)")
    expect(e2, "transport", "http", "transport")
    expect(e2, "url", URL, "url")
    expect(e2, "timeout", HTTP_TIMEOUT_S, "timeout")
    expect(e2, "headers", HEADERS, "headers（多键）")
    print("    [M1] A 逐字段回读一致：env=%r headers=%r cwd=%r timeout=%s hot_tools=%r transport=%r"
          % (e1.get("env"), e2.get("headers"), e1.get("cwd"), e1.get("timeout"),
             e1.get("hot_tools"), e1.get("transport")))


# ══════════════════════════════════════════════════════════
# M2 / M3 / M4 —— stdio 拉起、cwd、env 的真实效果（B）
# ══════════════════════════════════════════════════════════

def case_m2_stdio_spawn_and_args():
    """M2（B）：transport=stdio → 工具面出现该 server 工具（runtime+args 真实拉起子进程）；
    probe_info 回显 argv == 配置 args（逐个 exec 参数、含反斜杠路径整体传递）。"""
    expect(entry_of(NAME), "transport", "stdio", "M2 前置 transport 回读")
    wait_tools_present([TOOL_INFO, TOOL_PID], "transport=stdio 条目经 gateway 拉起后工具进入工具面")
    info = gw_call(TOOL_INFO, {"keys": ENV_NAMES}, work_dir=WD_A)
    if info.get("argv") != ARGV:
        raise TestError("args 未按逐个 exec 参数传递：argv=%r，期望 %r" % (info.get("argv"), ARGV))
    print("    [M2] 工具面含 %s/%s；子进程 argv=%r（pid=%s）"
          % (TOOL_INFO, TOOL_PID, info.get("argv"), info.get("pid")))


def case_m3_cwd_effect():
    """M3（B）：子进程真实工作目录 == 配置 cwd。"""
    wait_tools_present([TOOL_INFO], "M3 前置：工具在工具面")
    info = gw_call(TOOL_INFO, {}, work_dir=WD_A)
    got = info.get("cwd") or ""
    if os.path.normcase(os.path.normpath(got)) != os.path.normcase(os.path.normpath(CWD_DIR)):
        raise TestError("子进程 cwd=%r，期望配置值 %r" % (got, CWD_DIR))
    print("    [M3] 子进程真实 cwd=%r == 配置 cwd=%r（pid=%s）" % (got, CWD_DIR, info.get("pid")))


def case_m4_env_effect():
    """M4（B）：env 哨兵注入成功 + 值内 ${VAR} 展开（落库=原文，效果=展开）。"""
    wait_tools_present([TOOL_INFO], "M4 前置：工具在工具面")
    info = gw_call(TOOL_INFO, {"keys": ENV_NAMES}, work_dir=WD_A)
    env = info.get("env") or {}
    got = {k: env.get(k) for k in ENV_NAMES}
    print("    [M4] 子进程环境变量回显：%r" % (got,))
    if got.get("PROBE_A") != "alpha-3391" or got.get("PROBE_B") != "beta-8246":
        raise TestError("env 注入未生效：%r" % (got,))
    sysroot = os.environ.get("SystemRoot") or ""
    if not sysroot:
        raise TestError("测试机无 SystemRoot，无法验证 ${VAR} 展开")
    if got.get("PROBE_EXPANDED") != sysroot:
        raise TestError("${SystemRoot} 未展开：got=%r want=%r" % (got.get("PROBE_EXPANDED"), sysroot))


# ══════════════════════════════════════════════════════════
# M5 isolate 三态（A + B）
# ══════════════════════════════════════════════════════════

def case_m5_isolate_tristate():
    """M5：isolate 三态 A（回读）+ B（按 work_dir 的子进程同一性）。

    gateway 判据（ServerEntry.IsolateEnabled，61 §5.1.1 / 26-mcp-gateway）：
    显式值优先；未设置（null）→ 按 transport 推断：stdio → 隔离 true / http·sse → 共享 false。
    B 证据 = 同 server 不同 work_dir 调用 probe_pid：隔离 → 各自子进程（pid 不同）；
    共享 → 同一连接/进程（pid 相同）；同 work_dir 复用 → pid 稳定。
    """
    def _trio(tag):
        a1 = probe_pid(WD_A)
        a2 = probe_pid(WD_A)
        b1 = probe_pid(WD_B)
        print("    [M5] %s：pid(%s)=%s 复调=%s / pid(%s)=%s" % (tag, WD_A, a1, a2, WD_B, b1))
        if a1 != a2:
            raise TestError("%s：同 work_dir 应复用同一子进程，pid %s != %s" % (tag, a1, a2))
        return a1, b1

    # ① 未拨动（M1 保存态）→ 键不存在；stdio 推断 = 隔离
    e = entry_of(NAME)
    if "isolate" in e:
        raise TestError("① 未拨动 isolate 不应落库该键：%r" % e.get("isolate"))
    wait_tools_present([TOOL_PID], "M5-① 前置：工具在工具面")
    a1, b1 = _trio("① 未设置（按 transport 推断）")
    if b1 == a1:
        raise TestError("① 未设置 + transport=stdio 应按 transport 推断为隔离：不同 work_dir 应各持子进程（pid=%s 相同）" % a1)

    # ② 显式 true（开关当前显示推断 true → 点两次得到显式 true）
    open_edit_by_name(NAME)
    click_tab(LBL_TAB_RUNTIME)                 # isolate 在「运行信息」页
    if switch_state(LBL_ISO) is not True:
        raise TestError("② 前置：stdio 条目的 isolate 开关应显示推断值 true")
    click_switch(LBL_ISO)                     # → 显式 false
    if click_switch(LBL_ISO) is not True:     # → 显式 true
        raise TestError("② 拨到显式 true 失败")
    save_dialog()
    e = wait_entry_field(NAME, "isolate", True, "② 显式 true 落库")
    wait_tools_present([TOOL_PID], "M5-② 前置：重注册后工具回到工具面")
    a1, b1 = _trio("② 显式 isolate=true")
    if b1 == a1:
        raise TestError("② isolate=true 应各 work_dir 独立子进程：pid=%s 相同" % a1)

    # ③ 显式 false（开关当前显示 true → 点一次得到显式 false）
    open_edit_by_name(NAME)
    click_tab(LBL_TAB_RUNTIME)
    if switch_state(LBL_ISO) is not True:
        raise TestError("③ 前置：显式 true 应回显为开")
    if click_switch(LBL_ISO) is not False:
        raise TestError("③ 拨到显式 false 失败")
    save_dialog()
    e = wait_entry_field(NAME, "isolate", False, "③ 显式 false 落库")
    wait_tools_present([TOOL_PID], "M5-③ 前置：重注册后工具回到工具面")
    a1, b1 = _trio("③ 显式 isolate=false")
    if b1 != a1:
        raise TestError("③ isolate=false 应共享单连接/单进程：不同 work_dir pid 应相同（%s != %s）" % (a1, b1))
    print("    [M5] 三态结论：未设置=按 transport 推断（stdio→隔离）/ 显式 true=隔离 / 显式 false=共享")


# ══════════════════════════════════════════════════════════
# M8 sandbox 三态 + 仅 stdio 可开（A）
# ══════════════════════════════════════════════════════════

def case_m8_sandbox_tristate():
    """M8（A）：sandbox 编辑入口唯一 = 对话框「运行信息」页签（2026-09-26 列表页列已摘除）。

    三态（未设置 = 缺键 = 不隔离；拨动才写 true/false）+ 语义约束：仅 stdio 可开；
    http（及 auto 只填 url）→ 开关禁用（不写键）。
    """
    # stdio：未设置 → 显示为关且可操作 → 拨开 → 回读 true
    open_edit_by_name(NAME)
    click_tab(LBL_TAB_RUNTIME)
    if switch_enabled(LBL_SANDBOX) is not True:
        raise TestError("stdio 条目 sandbox 开关应可操作（非禁用）")
    if switch_state(LBL_SANDBOX) is not False:
        raise TestError("sandbox 未设置时应显示为关")
    if click_switch(LBL_SANDBOX) is not True:
        raise TestError("拨开 sandbox 失败")
    save_dialog()
    wait_entry_field(NAME, "sandbox", True, "sandbox=true 落库")

    # stdio：拨关 → 回读 false（显式）
    open_edit_by_name(NAME)
    click_tab(LBL_TAB_RUNTIME)
    if switch_state(LBL_SANDBOX) is not True:
        raise TestError("sandbox=true 应回显为开")
    if click_switch(LBL_SANDBOX) is not False:
        raise TestError("拨关 sandbox 失败")
    save_dialog()
    wait_entry_field(NAME, "sandbox", False, "sandbox=false 落库")

    # http：开关禁用（不落地子进程，agentbox 无从注入）
    open_edit_by_name(HTTP_NAME)
    click_tab(LBL_TAB_RUNTIME)
    if switch_enabled(LBL_SANDBOX) is not False:
        raise TestError("http 条目 sandbox 开关应禁用")
    print("    [M8] sandbox 三态（缺键/true/false）落库 + 仅 stdio 可开（http 禁用）")


# ══════════════════════════════════════════════════════════
# M6 hot_tools 效果（B）
# ══════════════════════════════════════════════════════════

def case_m6_hot_tools():
    """M6（A+B）：经「工具」页签【加载工具】+ 逐项勾选 → 写库为**下游原名** → 工具面 _meta.hot=true；
    清空（取消勾选）→ 标记消失；并做**重启后工具面**对照。

    口径：gateway registry.isHot 以 **下游原名** 匹配（HotTools 含 "*" = 全部 hot）；
    该标记经 tools/list 的 _meta.hot 透出，是 server `toolsForLLM`（只取 hot==true）决定
    「会话内可见工具面」的依据——故 _meta.hot 即「与会话内可见性一致的口径」。
    2026-09-27：入口由独立弹窗并入「工具」页签（顶部【加载工具】按钮点后才读 tools-list）；
    清单按 `_meta.server.alias|node == 记录 name` 归属该 server，展示名 = 原名（剥离前缀），
    data-tool = 暴露名；勾选结果在**主对话框保存**时才落库（时机不变）。
    """
    # ── 「工具」页签加载 + 勾选单个工具（probe_info），写库值必须是**原名** ──
    open_page()
    open_edit_by_name(NAME)
    open_tools_tab()
    if not tools_hint():
        raise TestError("「工具」页签未加载时应给初始提示（点击『加载工具』）")
    load_tools()
    rows = tools_rows()
    got = {r["exposed"]: r for r in (rows or [])}
    print("    [M6] 工具页签工具行：%r" % (rows,))
    if TOOL_INFO not in got or TOOL_PID not in got:
        raise TestError("「工具」页签未按别名列出该 server 全部工具：%r" % (rows,))
    # 展示名 = 剥离前缀后的原名；data-tool = 完整暴露名（区分重名）
    if got[TOOL_INFO]["display"] != "probe_info" or got[TOOL_PID]["display"] != "probe_pid":
        raise TestError("展示名应为原名（probe_info / probe_pid）：%r" % (rows,))
    if got[TOOL_INFO]["exposed"] != TOOL_INFO:
        raise TestError("data-tool 应为完整暴露名 %s：%r" % (TOOL_INFO, rows))
    if got[TOOL_INFO]["checked"] or got[TOOL_PID]["checked"]:
        raise TestError("初始无 hot → 逐项应均未勾选：%r" % (rows,))
    toggle_tool(TOOL_INFO)                     # 只勾 probe_info（草稿）
    save_dialog()                              # 主对话框【保存】才落库
    e = wait_entry_field(NAME, "hot_tools", HOT, "M6 高频工具（原名）落库")
    print("    [M6] 落库 hot_tools=%r（原名，非暴露名 %s）" % (e.get("hot_tools"), TOOL_INFO))

    tm = wait_tools_present([TOOL_INFO, TOOL_PID], "M6 前置：工具在工具面")
    hot_info = (tm[TOOL_INFO].get("_meta") or {}).get("hot")
    hot_pid = (tm[TOOL_PID].get("_meta") or {}).get("hot")
    print("    [M6] 工具面 _meta：%s.hot=%r / %s.hot=%r" % (TOOL_INFO, hot_info, TOOL_PID, hot_pid))
    if hot_info is not True:
        raise TestError("hot_tools=[probe_info] 时 %s 应带 _meta.hot=true，实际 %r" % (TOOL_INFO, hot_info))
    if hot_pid is not None:
        raise TestError("未列入 hot_tools 的 %s 不应带 _meta.hot，实际 %r" % (TOOL_PID, hot_pid))

    # 「重启后工具面」对照：重启走**启动期装配**路径（gateway_servers.loadGatewayServers →
    # mergeGatewayServers），与热生效路径（reconcileUserMCPs → userMCPServerSpec）映射同一
    # ServerEntry（含 hot_tools）→ 工具面 _meta.hot 应一致（此处以实测确权，非推断）。
    restart_gui()
    open_page()
    tm2 = wait_tools_present([TOOL_INFO, TOOL_PID], "M6 重启后（启动期装配路径）工具面")
    hot_restart = (tm2[TOOL_INFO].get("_meta") or {}).get("hot")
    print("    [M6] 重启后（启动装配路径）%s.hot=%r" % (TOOL_INFO, hot_restart))
    if hot_restart is not True:
        raise TestError("重启后（启动装配路径）hot_tools 应同样标记 _meta.hot=true，实际 %r" % hot_restart)

    # 清空 hot_tools（「工具」页签内取消勾选）→ 标记消失（证明由配置驱动，非固有）
    open_edit_by_name(NAME)
    open_tools_tab()
    load_tools()
    rows = tools_rows()
    if not [r for r in (rows or []) if r["checked"]]:
        raise TestError("重开「工具」页签应回显已勾选项：%r" % (rows,))
    toggle_tool(TOOL_INFO)                     # 取消勾选 probe_info
    save_dialog()
    wait_entry_field(NAME, "hot_tools", [], "清空 hot_tools 落库")
    tm = wait_tools_present([TOOL_INFO, TOOL_PID], "M6 清理后：工具仍在工具面")
    hot_after = (tm[TOOL_INFO].get("_meta") or {}).get("hot")
    print("    [M6] 清空 hot_tools 后 %s.hot=%r（工具仍在工具面，仅 hot 标记消失）" % (TOOL_INFO, hot_after))
    if hot_after is not None:
        raise TestError("清空 hot_tools 后 _meta.hot 应消失，实际 %r" % hot_after)


# ══════════════════════════════════════════════════════════
# M9 hot_tools「全部」+ 空态（A + B，2026-09-27）
# ══════════════════════════════════════════════════════════

def case_m9_hot_tools_all_and_empty():
    """M9（A+B）：「工具」页签「全部工具」→ 写库 `["*"]` → 全部工具 _meta.hot=true；
    另验空态（无工具 server → 加载后提示而非列表）+ 【取消】不改主对话框值。

    `"*"` = gateway `isHot` 的「全部 hot」哨兵（内嵌 self 节点同口径），故逐项应全部 high；
    空态 = 该 server 未启用 / 未连接 / 无工具（此处用 enabled=false 的 http 条目）。
    """
    # ① NAME（stdio，已注册）→ 加载 → 勾「全部」→ 落库 ["*"] → 全部工具 hot
    open_page()
    open_edit_by_name(NAME)
    open_tools_tab()
    load_tools()
    if not tools_rows():
        raise TestError("已注册的 stdio 条目加载后应有工具行")
    toggle_all()
    after = tools_rows()
    if not all(r["checked"] and r["disabled"] for r in after):
        raise TestError("勾「全部」后逐项应全勾且禁用（只读）：%r" % (after,))
    save_dialog()
    e = wait_entry_field(NAME, "hot_tools", ["*"], "M9 全部 hot 落库为 ['*']")
    print("    [M9] 「全部」落库 hot_tools=%r" % (e.get("hot_tools"),))
    tm = wait_tools_present([TOOL_INFO, TOOL_PID], "M9 前置：工具在工具面")
    for n in (TOOL_INFO, TOOL_PID):
        hot = (tm[n].get("_meta") or {}).get("hot")
        if hot is not True:
            raise TestError("hot_tools=['*'] 时 %s 应带 _meta.hot=true，实际 %r" % (n, hot))
    print("    [M9] ['*'] → %s / %s 均 _meta.hot=true" % (TOOL_INFO, TOOL_PID))

    # ② 空态 + 【取消】不改值：http 条目（enabled=false → 加载后无工具）
    open_edit_by_name(HTTP_NAME)
    open_tools_tab()
    load_tools()
    if tools_rows():
        raise TestError("无工具 server 加载后不应有工具行：%r" % (tools_rows(),))
    empty = tools_empty()
    if not empty:
        raise TestError("无工具 server 加载后应显示空态提示")
    print("    [M9] 空态提示：%r" % empty)
    close_dialog()                             # 取消 → 勾选只改草稿，未保存即丢弃
    if entry_of(HTTP_NAME).get("hot_tools") != []:
        raise TestError("【取消】后不应落库 hot_tools：%r" % entry_of(HTTP_NAME).get("hot_tools"))


# ══════════════════════════════════════════════════════════
# M7 enabled=false 生效（B）
# ══════════════════════════════════════════════════════════

def case_m7_enabled_false():
    """M7（B）：列表开关点击保存 → enabled=false 落库 → 工具面**不含**该 server 工具（保存即生效 T-25）。"""
    wait_tools_present([TOOL_INFO], "M7 前置：工具在工具面")
    idx = index_of(NAME)
    c.mq_emit("config-toggle-mcp", {"index": idx})
    wait_entry_field(NAME, "enabled", False, "M7 开关落库 enabled=false")
    wait_tools_absent([TOOL_INFO, TOOL_PID], "enabled=false → 工具面移除该 server 工具（无重启，保存即生效）")
    left = [n for n in tools_map() if n.startswith(HTTP_NAME + "_")]
    if left:
        raise TestError("enabled=false 条目不应有工具：%r" % left)
    print("    [M7] enabled=false 落库 + 工具面已移除 %s/%s（http 条目 %s 亦无工具）"
          % (TOOL_INFO, TOOL_PID, HTTP_NAME))


def main():
    ok = 0
    total = 0
    c.console(clear=True)
    print("依赖：--test-port=%d 的 GUI（harness 自起/复用）+ 夹具 mock_mcp_probe.py；"
          "驱动 = EditMCPDialog + 既有消息面 tools-list / chonk.mcp-tools-call" % PORT)
    total += 1
    ok += run_case("M1 全字段哨兵落库（A：env/headers/cwd/timeout/runtime/args/transport/description/enabled）",
                   case_m1_fields_roundtrip)
    total += 1
    ok += run_case("M2 transport=stdio 真实拉起 + args 逐个传递（B）", case_m2_stdio_spawn_and_args)
    total += 1
    ok += run_case("M3 cwd 效果：子进程真实工作目录 == 配置值（B）", case_m3_cwd_effect)
    total += 1
    ok += run_case("M4 env 效果：注入 + 值内 ${VAR} 展开（B）", case_m4_env_effect)
    total += 1
    ok += run_case("M5 isolate 三态（A 回读 + B 跨 work_dir 子进程同一性）", case_m5_isolate_tristate)
    total += 1
    ok += run_case("M8 sandbox 三态（A：缺键/true/false）+ 仅 stdio 可开", case_m8_sandbox_tristate)
    total += 1
    ok += run_case("M6 hot_tools：「工具」页签加载 + 勾选 → 原名落库 → 工具面 _meta.hot（A+B）", case_m6_hot_tools)
    total += 1
    ok += run_case("M9 hot_tools「全部」= ['*'] → 全部 _meta.hot=true；加载后空态 / 取消不改值（A+B）",
                   case_m9_hot_tools_all_and_empty)
    total += 1
    ok += run_case("M7 enabled=false 保存即生效：工具面移除（B）", case_m7_enabled_false)
    errs = c.console()
    for e in errs.get("entries", []):
        if e.get("level") in ("error",):
            print("  [CONSOLE-ERROR] %s" % e.get("text"))
    print("\nMCP 配置字段端到端（A 落库 + B 效果）：%d/%d 通过" % (ok, total))
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

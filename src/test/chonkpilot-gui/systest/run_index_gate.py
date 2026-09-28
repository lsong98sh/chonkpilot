# -*- coding: utf-8 -*-
"""P4 批次 L4 套件：项目级「索引 / 执行参数」配置的**落库 + 真实效果**端到端。

覆盖（每条 = A 数据面回读 + B 真实效果；B 恒以可观测证据收口，不用"保存成功即算过"）：

  G1 `enable-codegraph`  开 → 工具面出现 8 个 `self_codegraph_*`；关 → 消失。**保存即生效**
                         （同一实例内先取基线 → 开 → 关，不作重启用例；见 G1 docstring）
  G2 `enable-vfts`       开 → 工具面出现 `self_vfts_query`；关 → 消失。**保存即生效**
  G3 `codegraph.exts` / `codegraph.skip-dirs` → **索引范围终效**：
                         `codegraph.status.exts/skipDirs/indexedFiles` + 落盘 bbolt 索引库
                         （`index.db`）命中集合 → **夹具推导的显式期望集合字面全等**
                         （一个不多一个不少；差集打印在失败信息里）。判据已从「成员检查 + 计数」
                         升级为「集合字面全等」（基线/A 6 个 .go、跳过 gate_skip 后 5）——见 G3 docstring。
  G4 `codegraph.action`  rebuild / retry → 状态流出现**进行中阶段**（`state=indexing` 且
                         `phase∈{configure,index}`）后回到 `ready`；clear → 回「未初始化」
                         （`state=""`、`indexedFiles=0`、`index.db` 被删除且落盘无命中）。
  G5 prj `skip_dirs`     （**GUI 配置面**写入：设置 → 参数 → 项目标签页）+ mock LLM 真实
                         `file_find` 调用 → 命中集合**不再含被跳过目录**（同目录内未跳过文件仍命中）。
  G6 `timeout_sec` / `max_concurrency` → GUI 改后 **prj 回读一致**（含重开页面输入框回显）。
                         B 不可稳定观测的原因见 G6 docstring（附代码级依据）。
  G7 `codegraph.stack-gitignore` = **gitignore 语义端到端**（勾选「叠加 gitignore」）：
                         `.gitignore` 生效 + **文件级排除**（`*.gen.go`）+ `!` 反选
                         （`!special.gen.go` + **目录剪枝**（`gate_ign/sub/` 命中即不下降，
                         其内 `!gate_ign/sub/u.go` 无效）。判据 = 落盘 bbolt 索引命中集合 +
                         `status.indexedFiles`；收尾取消勾选（回退，避免污染后续）。
  G8 用户规则（输入框「排除的目录和文件」）**最高优先级** + 文件级排除 + `!` 反选：
                         `codegraph.skip-dirs` 原样透传（不折名、保留 `!`/glob/顺序）；
                         对照「叠加 gitignore」+ 用户规则 `!gate_ign/sub/` 压过 .gitignore 目录排除。
  G9 vfts 侧同语义：`vfts.stack-gitignore` + exts/排除规则 → **既有清单面
                         `data-filelist-list`（61 §3.1 file_list 表）**的文件集合 + `vfts.status`
                         （vfts 引擎遍历与 codegraph 共用 `src/lib/ignore` 匹配器 → 行为一致）。

观测手段（**零新增 MQ 主题**，全部取自 61-消息一览既有面）：
  * 工具面 = 客户端能力面 `tools-list`（61 §4.5：桥 → gateway `mcp-tools-list`；桥按当前实例
    作用域过滤）→ `{tools:[{name}]}`。内置能力源暴露名带 `self_` 前缀（gateway applyPrefix，
    self 节点 entry.ID）→ codegraph/vfts 插件注册的工具同样经 self 节点聚合。
  * 索引状态 = prj 键 `codegraph.status` / `vfts.status`（插件回写；prjusr 路由，--data-dir 下与
    prj 同库）→ `data-prj-config-list` 回读；**进行中瞬态**经既有广播 `data-prj-config-refresh`
    （61 §3：persist 保存/删除后广播）捕获 → 不依赖轮询赛跑，不漏阶段。
  * vfts 文件清单 = 既有 `data-filelist-list`（61 §3.1：项目级 prj 库 `file_list` 表读面，
    每行含 `path` 绝对路径）→ 逐文件命中集合（与落盘 codegraph 索引同判据口径）。
  * codegraph 落盘索引 = **bbolt 单文件库** `<ws>/.chonkpilot/codegraph/index.db`（引擎侧
    store.go；不再有 index.json）→ 经引擎只读自检 `-dump <ws>` 打印 JSON `{count,files}` 读回
    命中文件集合（不重建、不写盘）。
  * 端到端工具调用 = mock LLM 回 tool_call（`mock_llm.py` 专用路由 `call find-skip`，
    路径由提示词携带 `findskip=<绝对路径>`）→ 走真实 server→gateway→executor 链路。

隔离与环境（51-FP与测试映射 §5/§6-8）：
  * **自起** GUI：动态端口 + 独立 work-dir（临时目录）+ 独立 `--data-dir` + 独立 `HOME`
    （usr 主库全新 → enable-* 天然为「未启用」基线；prj 库全新 → 无历史覆盖）。
  * 不碰共享 `systest/ws`（其 `codegraph/vfts` 索引是 K5/K6 夹具）——本套件**不清除**他套件索引。
  * 套件级配置快照-还原 = `_h.suite_config_guard(c)`（退出前自动回滚 usr+prj，含异常/中断路径）。
  * mock LLM 由 harness 自起自收；索引落盘（`<ws>/.chonkpilot/{codegraph,vfts}`）在临时
    work-dir 内 → 随 `harness.tmp_dir` 一并删除 → **零残留**（进程/端口/索引目录）。

运行：python run_index_gate.py   （自起自收，无需外部底座）
"""

import json
import os
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51 §6 测试资源规范）  # noqa: E402

# ── 工具面（网关暴露名）──────────────────────────────────
# codegraph 查询工具全集 = 8（`plugin-codegraph/callgate.go:36-104 queryTools`；
# 含 `*_callers` / `*_callees`，同 `codegraph_test.go:19` 断言；2026-09-27 由 6 订正为 8，
# 对齐产品现网工具面——原 6 项列表漏 callers/callees 使 G1 假红）。
CG_TOOLS = [
    "self_codegraph_symbol_search",
    "self_codegraph_get_symbol_info",
    "self_codegraph_callers",
    "self_codegraph_callees",
    "self_codegraph_get_dependency_graph",
    "self_codegraph_find_circular_deps",
    "self_codegraph_analyze_complexity",
    "self_codegraph_get_module_summary",
]
VF_TOOL = "self_vfts_query"

REFRESH = "data-prj-config-refresh"          # 既有广播（61 §3）
STATUS_KEY = "codegraph.status"              # 插件回写的索引状态（61/64 §4.2）
VSTATUS_KEY = "vfts.status"                  # vfts 插件回写的索引状态（同源口径）


# ══════════════════════════════════════════════════════════
# 环境：独立 work-dir / data-dir / HOME（临时目录，结束即删）
# ══════════════════════════════════════════════════════════

WS = _h.tmp_dir("ck-idxgate-ws-")
DD = _h.tmp_dir("ck-idxgate-dd-")
HOME = _h.tmp_home()

# codegraph 索引夹具：2 个 .go（gate_src 命中 / gate_skip 待排除）+ 1 个 .md（非 code 语言）
os.makedirs(os.path.join(WS, "gate_src"), exist_ok=True)
with open(os.path.join(WS, "gate_src", "alpha.go"), "w", encoding="utf-8") as f:
    f.write("package alpha\n\nfunc Alpha() int { return 1 }\n")
with open(os.path.join(WS, "gate_src", "note.md"), "w", encoding="utf-8") as f:
    f.write("# note\n")
os.makedirs(os.path.join(WS, "gate_skip"), exist_ok=True)
with open(os.path.join(WS, "gate_skip", "delta.go"), "w", encoding="utf-8") as f:
    f.write("package skip\n\nfunc Delta() int { return 2 }\n")

# skip_dirs 端到端夹具（G5）：同目录下「未跳过」与「待跳过」文件内容同哨兵
FIND_ROOT = os.path.join(WS, "gate_find")
os.makedirs(os.path.join(FIND_ROOT, "_skipme"), exist_ok=True)
with open(os.path.join(FIND_ROOT, "ok.txt"), "w", encoding="utf-8") as f:
    f.write("GATE_SKIP_SENTINEL ok\n")
with open(os.path.join(FIND_ROOT, "_skipme", "dep.txt"), "w", encoding="utf-8") as f:
    f.write("GATE_SKIP_SENTINEL dep\n")

# gitignore 语义端到端夹具（G7/G8/G9）：文件级排除 + '!' 反选 + 目录剪枝。
# 每个 .go 写唯一哨兵字符串（便于在落盘产物 / 清单里定位）；.gitignore 故意把「子级 '!' 反选」
# 写在「父目录排除」之后，验证 gitignore 语义「目录命中即不下降 → 其内 '!' 无效」。
IGN_ROOT = os.path.join(WS, "gate_ign")
os.makedirs(os.path.join(IGN_ROOT, "sub"), exist_ok=True)
for _name, _pkg, _sent in (
    ("keep.go", "ign", "SG_IGN_KEEP"),          # 不被任何规则命中
    ("gen.gen.go", "ign", "SG_IGN_GEN"),        # 被 *.gen.go 命中（文件级排除）
    ("special.gen.go", "ign", "SG_IGN_SPECIAL"),# 被 !special.gen.go 反选（应保留）
):
    with open(os.path.join(IGN_ROOT, _name), "w", encoding="utf-8") as f:
        f.write("package %s\n\n// %s\nfunc %s() int { return 1 }\n" % (_pkg, _sent, _sent))
with open(os.path.join(IGN_ROOT, "sub", "u.go"), "w", encoding="utf-8") as f:
    f.write("package sub\n\n// SG_IGN_SUB\nfunc SG_IGN_SUB() int { return 1 }\n")
with open(os.path.join(WS, ".gitignore"), "w", encoding="utf-8") as f:
    f.write("*.gen.go\n!special.gen.go\ngate_ign/sub/\n!gate_ign/sub/u.go\n")

# ── 落盘索引「命中集合」期望值（逐用例由上方夹具 + exts / skip-dirs / .gitignore / '!' / 目录剪枝
#    规则人工推导；**禁循环论证**——绝不用引擎 `-dump` 输出反推期望）。
# 受支持扩展名（引擎 lang.go extLang）= {.go,.js,.jsx,.mjs,.cjs,.ts,.tsx,.py,.pyw,.rs,.java}；
# 故夹具里的 `gate_src/note.md` / `gate_find/*.txt` / `.gitignore` 一律**不被索引**；
# `gate_find/` 仅 G5（file_find）用，与索引无关。未写 `codegraph.exts` → 引擎回落全部受支持语言。
# 内置强制排除（.git/ 等）+ 默认排除（node_modules/、dist/ 等）不命中本夹具任何路径。
_ALL_GO = {                          # 夹具 .go 全集（6）
    "gate_src/alpha.go",             # gate_src/ 唯一 .go
    "gate_skip/delta.go",            # gate_skip/ 唯一 .go（G3 B 跳过它）
    "gate_ign/keep.go",              # 无规则命中
    "gate_ign/gen.gen.go",           # 被 *.gen.go 命中
    "gate_ign/special.gen.go",       # 被 *.gen.go 命中后被 !special.gen.go 反选
    "gate_ign/sub/u.go",             # 父目录 gate_ign/sub/ 命中 → 目录剪枝
}
# G3 基线 / 步骤 A：exts=默认全语言（基线）/ ".go"（A）、skip-dirs 空、stack-gitignore 关
#                     → 6 个 .go 全收（.md/.txt 非受支持语言）。
_G3_AB = _ALL_GO
# G3 步骤 B：再追加 skip-dirs="gate_skip"（用户规则，目录命中最优先级）→ gate_skip/ 目录剪枝
#            → gate_skip/delta.go 出（6 → 5）。
_G3_B = {
    "gate_src/alpha.go",
    "gate_ign/keep.go",
    "gate_ign/gen.gen.go",
    "gate_ign/special.gen.go",
    "gate_ign/sub/u.go",
}
# G4 clear：落盘索引库删除 + 内存索引置空 → 无命中。
_CLEAR = set()
# G7：exts=".go" + stack-gitignore=true（.gitignore=*.gen.go / !special.gen.go / gate_ign/sub/ /
#      !gate_ign/sub/u.go）
_G7 = {
    "gate_src/alpha.go",             # 在：.gitignore 无匹配
    "gate_skip/delta.go",            # 在：.gitignore 无匹配
    "gate_ign/keep.go",              # 在：*.gen.go 不匹配（keep.go 不以 ".gen.go" 结尾）
    "gate_ign/special.gen.go",       # 在：*.gen.go 命中后被 !special.gen.go 反选回来
    # 不在：gate_ign/gen.gen.go（*.gen.go 命中，且无 '!' 救回）
    # 不在：gate_ign/sub/u.go（父目录 gate_ign/sub/ 命中 → 不下降，其内 !gate_ign/sub/u.go 无效）
}
# G8 步骤 1：用户规则 "*.gen.go"（stack 关 → .gitignore 不读）
_G8_S1 = {
    "gate_src/alpha.go",             # 在：无规则命中
    "gate_skip/delta.go",            # 在：无规则命中
    "gate_ign/keep.go",              # 在：*.gen.go 不匹配
    "gate_ign/sub/u.go",             # 在：目录未排除（*.gen.go 不匹配目录）
    # 不在：gate_ign/gen.gen.go、gate_ign/special.gen.go（均被 *.gen.go 命中）
}
# G8 步骤 2：用户规则 "*.gen.go, !special.gen.go"（stack 关）
_G8_S2 = {
    "gate_src/alpha.go",
    "gate_skip/delta.go",
    "gate_ign/keep.go",
    "gate_ign/sub/u.go",
    "gate_ign/special.gen.go",       # 在：!special.gen.go 反选回来
    # 不在：gate_ign/gen.gen.go（*.gen.go 命中）
}
# G8 步骤 3：stack=true + 用户规则 "!gate_ign/sub/"（用户规则最高优先级）
_G8_S3 = {
    "gate_src/alpha.go",
    "gate_skip/delta.go",
    "gate_ign/keep.go",
    "gate_ign/special.gen.go",       # 在：.gitignore 的 *.gen.go / !special.gen.go 仍生效
    "gate_ign/sub/u.go",             # 在：用户规则 !gate_ign/sub/ 目录级反选 → 压过 .gitignore 目录排除
    # 不在：gate_ign/gen.gen.go（.gitignore 的 *.gen.go 仍生效）
}

MOCK_PORT = _h.free_port()
_h.start_mock_llm(MOCK_PORT)                 # mock LLM 自起自收（harness 登记）
_G = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME,
                  extra_args=("--llm-base=http://127.0.0.1:%d/v1" % MOCK_PORT,))
c = _G.client
_h.suite_config_guard(c)                     # 套件级快照-还原（51 §6-8）
_h.ensure_locale(c)                          # 语言确定性（本套件按 zh-CN 文案定位控件）
print("[env] ws=%s data=%s mock=%d gui=%d（临时目录，结束即删）"
      % (WS, DD, MOCK_PORT, _G.port), flush=True)


# ══════════════════════════════════════════════════════════
# 通用工具（前端 eval / 数据面 / UI 控件定位）
# ══════════════════════════════════════════════════════════

def deep(v):
    """循环解包 JSON 字符串（test-port eval 结果可能被多重编码）。"""
    for _ in range(4):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def ev(js, timeout=6000):
    return deep(c.eval(js, timeout))


def wait_vis(sel, max_wait=12):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if c.exists(sel).get("count", 0) > 0:
            return True
        time.sleep(0.3)
    return False


def prj():
    r = c.req("data-prj-config-list", {}) or {}
    return r.get("list") or {}


def prj_save(k, v):
    return c.req("data-prj-config-save", {"data": {"key": k, "value": v}})


def status_obj(key=STATUS_KEY):
    raw = prj().get(key) or ""
    try:
        return json.loads(raw)
    except Exception:
        return {}


# ── 工具面（客户端能力面 tools-list）─────────────────────

def tools():
    r = c.req("tools-list", {}) or {}
    return [t.get("name") or "" for t in (r.get("tools") or [])]


def wait_tools(pred, desc, max_wait=30):
    deadline = time.time() + max_wait
    cur = []
    while time.time() < deadline:
        cur = tools()
        if pred(set(cur)):
            return cur
        time.sleep(0.4)
    raise TestError("%s 超时（%ss）；当前工具面=%r" % (desc, max_wait, sorted(cur)))


# ── 索引状态流（既有广播 data-prj-config-refresh，捕获瞬态阶段）──

def arm_refresh():
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture([REFRESH])               # 每次调用先清空旧事件
    time.sleep(0.2)


def refresh_values(key=STATUS_KEY):
    """本窗口捕获的 key 变更值（原始文本，去重保序）。"""
    out = []
    for e in c.events_of(REFRESH, clear=False):
        p = e.get("payload") or {}
        if isinstance(p, str):
            try:
                p = json.loads(p)
            except Exception:
                p = {}
        if not isinstance(p, dict) or p.get("id") != key:
            continue
        lst = p.get("list") or {}
        if key in lst and lst[key] not in out:
            out.append(lst[key])
    return out


def stream_states(key=STATUS_KEY):
    out = []
    for raw in refresh_values(key):
        try:
            out.append(json.loads(raw))
        except Exception:
            out.append({"_raw": raw})
    return out


def wait_stream(pred, desc, max_wait=120):
    deadline = time.time() + max_wait
    states = []
    while time.time() < deadline:
        states = stream_states()
        if pred(states):
            return states
        time.sleep(0.2)
    raise TestError("%s 超时（%ss）；已捕获状态流末段=%r" % (desc, max_wait, states[-6:]))


def wait_status(pred, desc, max_wait=120, key=STATUS_KEY):
    """轮询 prj 回读的索引状态（默认 codegraph.status；收敛判据；失败时打印末次状态）。"""
    deadline = time.time() + max_wait
    last = {}
    while time.time() < deadline:
        last = status_obj(key)
        if pred(last):
            return last
        time.sleep(0.3)
    raise TestError("%s 超时（%ss）；末次 status=%r" % (desc, max_wait, last))


def filelist_paths():
    """读 vfts 增量清单（既有 `data-filelist-list`，61 §3.1）→ 文件绝对路径列表（'/' 分隔）。"""
    r = c.req("data-filelist-list", {}) or {}
    return [str(e.get("path") or "") for e in (r.get("list") or [])]


def wait_filelist(pred, desc, max_wait=120):
    """轮询 vfts 清单直至 `pred(paths)` 成立（清单在引擎索引后由插件重建，晚于 status=ready）。"""
    deadline = time.time() + max_wait
    paths = []
    while time.time() < deadline:
        paths = filelist_paths()
        if pred(paths):
            return paths
        time.sleep(0.3)
    raise TestError("%s 超时（%ss）；末次 file_list=%r" % (desc, max_wait, paths))


def has_path(paths, suffix):
    """清单里是否存在以 suffix 结尾的文件（绝对路径 → 按后缀定位）。"""
    return any(p.replace("\\", "/").endswith(suffix) for p in paths)


def _cycle_pred(ss):
    """状态流里先出现 indexing（含 phase），其后回到 ready。"""
    idx = next((i for i, s in enumerate(ss) if s.get("state") == "indexing"), None)
    if idx is None:
        return False
    return any(s.get("state") == "ready" for s in ss[idx + 1:])


# ── 前端页面/控件（口径同 run_config_ui.py 的 A 类用例）────

def open_page(kind, root_sel):
    c.mq_emit("preview-tab-close-all")
    time.sleep(0.5)
    c.mq_emit("preview-tab-open", {"kind": kind})
    if not wait_vis(root_sel):
        raise TestError("配置页未打开：kind=%s root=%s" % (kind, root_sel))
    time.sleep(0.8)


def click_tab(root_sel, label):
    r = ev("""(()=>{const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);
      if(!R)return 'no-root';const t=[...R.querySelectorAll('.b-tabs-item')]
      .find(x=>x.textContent.trim()===%s);if(!t)return 'no-tab';t.click();return 'ok';})()"""
           % (json.dumps(root_sel), json.dumps(label)))
    time.sleep(0.9)
    if r != "ok":
        raise TestError("切换页签失败 %s：%s" % (label, r))


def panel_js(inner):
    """在可见的项目配置面板（.project-config-panel）内执行 inner（内嵌 R）。"""
    return ev("""(()=>{const R=[...document.querySelectorAll('.project-config-panel')]
      .find(e=>e.getBoundingClientRect().width>0);if(!R)return null;%s})()""" % inner)


def find_switch(label):
    return panel_js("""
const row=[...R.querySelectorAll('.switch-row,.cg-toggle,.vf-toggle,.history-toggle,.form-item')]
  .find(x=>{const l=x.querySelector('.form-label');return l&&l.textContent.trim()===%s;});
if(!row)return null;const sw=row.querySelector('.b-switch');if(!sw)return null;
return {checked:sw.classList.contains('is-checked'),disabled:sw.classList.contains('is-disabled')};"""
                    % json.dumps(label))


def click_switch(label):
    r = panel_js("""
const row=[...R.querySelectorAll('.switch-row,.cg-toggle,.vf-toggle,.history-toggle,.form-item')]
  .find(x=>{const l=x.querySelector('.form-label');return l&&l.textContent.trim()===%s;});
if(!row)return 'no-row';const sw=row.querySelector('.b-switch');if(!sw)return 'no-sw';
if(sw.classList.contains('is-disabled'))return 'disabled';sw.click();return 'ok';""" % json.dumps(label))
    time.sleep(0.9)
    return r


def set_textarea(idx, value):
    r = panel_js("""
const ts=[...R.querySelectorAll('textarea.b-textarea')];const t=ts[%d];if(!t)return 'no-ta';
Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(t,%s);
t.dispatchEvent(new Event('input',{bubbles:true}));
t.dispatchEvent(new Event('change',{bubbles:true}));return 'ok';"""
                 % (idx, json.dumps(value)))
    if r != "ok":
        raise TestError("写文本框失败(idx=%d)：%s" % (idx, r))
    time.sleep(0.3)


def click_save():
    r = panel_js("""
const b=[...R.querySelectorAll('button.b-btn--primary')].filter(x=>x.getBoundingClientRect().width>0)[0];
if(!b)return 'no-btn';b.click();return 'ok';""")
    if r != "ok":
        raise TestError("点击保存按钮失败：%s" % r)


def set_stack_gitignore(checked):
    """勾选/取消「叠加 gitignore」（「排除的目录和文件」label 右侧原生 input[type=checkbox]）。

    口径 = `CodegraphConfig.vue` / `VftsConfig.vue` 的 `<label class="b-checkbox">` 内
    `<input type=checkbox>`（`v-model` 绑 `change`）；原生 `click()` 同步翻转 checked 并触发
    change → 组件 ref 更新。操作后校验 DOM 状态达预期。
    """
    r = panel_js("""
const cb=R.querySelector('.b-checkbox input[type=checkbox]');if(!cb)return 'no-cb';
if(cb.checked!==%s){cb.click();}
return cb.checked?'on':'off';""" % ("true" if checked else "false"))
    time.sleep(0.4)
    if r not in ("on", "off"):
        raise TestError("「叠加 gitignore」复选框操作失败：%s" % r)
    if (r == "on") != bool(checked):
        raise TestError("「叠加 gitignore」复选框未达预期（期望 checked=%s，实际 %s）" % (checked, r))


def open_index_tab(tab_label):
    """打开「设置 → 项目」配置面板并切到指定索引页签（CodeGraph 索引 / Vfts 全文索引）。"""
    open_page("settings-project", ".project-config-panel")
    click_tab(".project-config-panel", tab_label)


# ── 设置 → 参数（settings-params）项目级控件 ─────────────

def open_params_project():
    open_page("settings-params", ".settings-page")
    click_tab(".settings-page", "项目")
    rows = ev("""JSON.stringify([...document.querySelectorAll('.settings-page .param-row')]
      .map(r=>((r.querySelector('.param-label')||{}).textContent||'').trim()))""") or []
    if not any("跳过目录" in x for x in rows):
        raise TestError("参数页项目标签页缺控件：%r" % rows)


def param_rows():
    return ev("""JSON.stringify([...document.querySelectorAll('.settings-page .param-row')]
      .map(r=>({label:((r.querySelector('.param-label')||{}).textContent||'').trim(),
                value:(r.querySelector('input')||{}).value})))""")


def set_param_input(label_sub, value):
    """按标签子串定位项目级参数输入框：原生 setter + input + blur（= 用户编辑后失焦落库）。"""
    r = ev("""(()=>{const R=document.querySelector('.settings-page');if(!R)return 'no-root';
      const row=[...R.querySelectorAll('.param-row')]
        .find(x=>((x.querySelector('.param-label')||{}).textContent||'').includes(%s));
      if(!row)return 'no-row';const inp=row.querySelector('input');if(!inp)return 'no-input';
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(inp,%s);
      inp.dispatchEvent(new Event('input',{bubbles:true}));
      inp.dispatchEvent(new Event('blur',{bubbles:true}));return 'ok';})()"""
           % (json.dumps(label_sub), json.dumps(value)))
    if r != "ok":
        raise TestError("写参数输入框失败（%s）：%s" % (label_sub, r))
    time.sleep(1.2)


# ── mock LLM 驱动的一次真实 file_find（G5）────────────────

def run_find_turn():
    """发一轮 llm-start（mock 按 `call find-skip` 回 self_file_find）→ 返回（工具结果原文, 原始 payload）。"""
    sid = "idxgate-%d" % int(time.time() * 1000)
    turn = "t-" + sid
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(["turn-start", "tool-result", "llm-complete"])
    c.mq_emit("session-changed", {"session_id": sid})
    c.mq_emit("llm-start", {
        "session_id": sid, "turn": turn,
        "q": "call find-skip findskip=%s" % FIND_ROOT,   # 路径经提示词携带（无空格）
        "llm": "mock", "think": "", "effort": "", "scenario_id": "",
    })
    deadline = time.time() + 90
    payload = None
    while time.time() < deadline:
        for e in c.events_of("tool-result", clear=False):
            p = e.get("payload") or {}
            if isinstance(p, str):
                try:
                    p = json.loads(p)
                except Exception:
                    p = {}
            if not isinstance(p, dict) or p.get("turn_id") != turn:
                continue
            if (p.get("tool") or "").endswith("file_find") and p.get("status") == "done":
                payload = p
        if payload is not None:
            break
        time.sleep(0.3)
    if payload is None:
        raise TestError("mock LLM 轮次未回 file_find tool-result（turn=%s）" % turn)
    # 等该轮终态，避免下一轮与在飞轮次排队
    deadline = time.time() + 60
    while time.time() < deadline:
        done = [e for e in c.events_of("llm-complete", clear=False)
                if isinstance(e.get("payload"), (dict, str))]
        if any(turn in json.dumps((e.get("payload") or {}), ensure_ascii=False) for e in done):
            break
        time.sleep(0.3)
    txt = payload.get("result") or ""
    try:
        o = json.loads(txt)
        if isinstance(o, dict):
            txt = o.get("output") or json.dumps(o, ensure_ascii=False)
    except Exception:
        pass
    return txt, payload


# ══════════════════════════════════════════════════════════
# G1 enable-codegraph → 工具面对 LLM 的可见性
# ══════════════════════════════════════════════════════════

def case_g1_enable_codegraph_surface():
    """开 → 工具面出现 8 个 `self_codegraph_*`；关 → 消失（**保存即生效**，不作重启用例）。

    保存即生效依据：`CodegraphConfig.vue handleChange` → `setConfig('enable-codegraph', …)`
    → `data-prj-config-save` → persist 广播 `data-prj-config-refresh` →
    `plugin-codegraph onPrjConfigRefresh`（订阅该既有主题）→ `syncTools()` 即注册/注销 gateway
    工具面（`codegraph.go:433-472`/`:980-1034`）。故断言：**同一实例**内开关后 `tools-list`
    集合即时变化（实测 ≤0.3s），全程无重启。
    """
    base = set(tools())
    stray = sorted(n for n in base if "codegraph" in n)
    if stray:
        raise TestError("基线工具面不应含 codegraph 工具（独立 work-dir 应为未启用）：%r" % stray)
    print("[G1] 基线工具面 %d 项：%r" % (len(base), sorted(base)), flush=True)

    open_page("settings-project", ".project-config-panel")
    click_tab(".project-config-panel", "CodeGraph 索引")
    st = find_switch("CodeGraph 索引")
    if not st:
        raise TestError("未找到「CodeGraph 索引」开关")
    if st["checked"]:
        raise TestError("基线 enable-codegraph 应为关（独立 work-dir），实际开关为开")

    if click_switch("CodeGraph 索引") != "ok":
        raise TestError("点击 CodeGraph 开关失败")
    got = prj().get("enable-codegraph")
    if got != "true":
        raise TestError("点击后 enable-codegraph=%r，期望 'true'（A：prj 回读）" % (got,))
    on = set(wait_tools(lambda s: set(CG_TOOLS) <= s, "开启后 8 个 codegraph 工具注入工具面"))
    extra = sorted(on - base)
    if extra != sorted(CG_TOOLS):
        raise TestError("开启后工具面增量应为 8 个 codegraph 工具，实际增量=%r" % (extra,))
    print("[G1] 开 → 工具面 +%r（A: enable-codegraph=%r；B: 可见 8/8，未重启）"
          % (extra, got), flush=True)

    if click_switch("CodeGraph 索引") != "ok":
        raise TestError("再次点击 CodeGraph 开关失败")
    got2 = prj().get("enable-codegraph")
    if got2 != "false":
        raise TestError("关闭后 enable-codegraph=%r，期望 'false'" % (got2,))
    off = set(wait_tools(lambda s: not any("codegraph" in n for n in s),
                         "关闭后 codegraph 工具从工具面移除"))
    if off != base:
        raise TestError("关闭后工具面应回到基线，差异=%r" % (sorted(off ^ base),))
    print("[G1] 关 → 工具面 -%r（A: enable-codegraph=%r；B: 8/8 消失，未重启）"
          % (sorted(CG_TOOLS), got2), flush=True)


# ══════════════════════════════════════════════════════════
# G2 enable-vfts → 工具面对 LLM 的可见性
# ══════════════════════════════════════════════════════════

def case_g2_enable_vfts_surface():
    """开 → `self_vfts_query` 出现；关 → 消失（**保存即生效**）。

    依据：`VftsConfig.vue` 开关 → `setConfig('enable-vfts', …)` → `data-prj-config-refresh`
    → `plugin-vfts onPrjConfigRefresh`/`readEnableAndEnsure` → `syncTools()`（`vfts.go:383-423`/
    `:790-844`）。断言同一实例内即时可见性变化。
    """
    base = set(tools())
    if any("vfts" in n for n in base):
        raise TestError("基线工具面不应含 vfts 工具：%r" % sorted(base))
    open_page("settings-project", ".project-config-panel")
    click_tab(".project-config-panel", "Vfts 全文索引")
    st = find_switch("Vfts 全文索引")
    if not st:
        raise TestError("未找到「Vfts 全文索引」开关")
    if st["checked"]:
        raise TestError("基线 enable-vfts 应为关（独立 work-dir），实际开关为开")

    if click_switch("Vfts 全文索引") != "ok":
        raise TestError("点击 Vfts 开关失败")
    got = prj().get("enable-vfts")
    if got != "true":
        raise TestError("点击后 enable-vfts=%r，期望 'true'（A：prj 回读）" % (got,))
    on = set(wait_tools(lambda s: VF_TOOL in s, "开启后 vfts_query 注入工具面"))
    if sorted(on - base) != [VF_TOOL]:
        raise TestError("开启后工具面增量应为 %s，实际=%r" % (VF_TOOL, sorted(on - base)))
    print("[G2] 开 → 工具面 +[%s]（A: enable-vfts=%r；B: 可见，未重启）" % (VF_TOOL, got), flush=True)

    if click_switch("Vfts 全文索引") != "ok":
        raise TestError("再次点击 Vfts 开关失败")
    got2 = prj().get("enable-vfts")
    if got2 != "false":
        raise TestError("关闭后 enable-vfts=%r，期望 'false'" % (got2,))
    off = set(wait_tools(lambda s: VF_TOOL not in s, "关闭后 vfts_query 从工具面移除"))
    if off != base:
        raise TestError("关闭后工具面应回到基线，差异=%r" % (sorted(off ^ base),))
    print("[G2] 关 → 工具面 -[%s]（A: enable-vfts=%r；B: 消失，未重启）" % (VF_TOOL, got2), flush=True)


# ══════════════════════════════════════════════════════════
# G3 codegraph.exts / codegraph.skip-dirs → 索引范围终效
# ══════════════════════════════════════════════════════════

IDX_DB = os.path.join(WS, ".chonkpilot", "codegraph", "index.db")


def _engine_exe():
    """codegraph 引擎 exe 路径（发行根，与宿主 exe 同级——插件按「宿主 exe 同目录」解析）。"""
    return os.path.join(os.path.dirname(_h.resolve_gui_exe()),
                        "chonkpilot-codegraph-mcp-server.exe")


def _idx_files():
    """落盘索引命中文件集合（相对路径，'/' 分隔）。

    索引落盘 = **bbolt 单文件库** `<ws>/.chonkpilot/codegraph/index.db`（引擎侧 store.go），
    Python 无法直读 → 经引擎**只读自检** `-dump <ws>` 打印 JSON `{count,files}` 读回；
    `-dump` 只 LoadIndex、不重建、不写盘（库缺失时也不会创建 index.db）。
    """
    exe = _engine_exe()
    if not os.path.isfile(exe):
        raise TestError("codegraph 引擎 exe 未找到（L4 需发行产物）：%s" % exe)
    p = subprocess.run([exe, "-dump", WS], capture_output=True, text=True, timeout=180,
                       creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
    if p.returncode != 0:
        raise TestError("引擎 -dump 失败（rc=%d）：%s"
                        % (p.returncode, (p.stderr or "").strip()[:400]))
    try:
        o = json.loads(p.stdout or "")
    except Exception as e:
        raise TestError("引擎 -dump 输出非 JSON：%r（%s）" % ((p.stdout or "")[:400], e))
    return set(str(x) for x in (o.get("files") or []))


def _want_idx(expected, desc):
    """断言落盘索引命中集合与期望**字面全等**（一个不多一个不少）；差集打印在失败信息里。

    期望集合由夹具 + exts / skip-dirs / .gitignore / '!' / 目录剪枝规则人工推导（见上方常量），
    **不得**用引擎 `-dump` 输出反推（禁循环论证）。返回实际集合。
    """
    got = _idx_files()
    want = set(expected)
    if got != want:
        raise TestError("%s：落盘索引命中集合不符（实际 %d / 期望 %d）\n"
                        "  多出（不应含却有）=%r\n  缺少（应含而缺）=%r\n  实际=%r"
                        % (desc, len(got), len(want),
                           sorted(got - want), sorted(want - got), sorted(got)))
    return got


def case_g3_index_scope():
    """写 exts/skip-dirs（**GUI 配置面**：文本框 + 保存按钮）→ 断言索引范围终效。

    可重复判据（三重，均来自产品面/落盘产物）：
      ① prj 回读：`codegraph.exts` / `codegraph.skip-dirs` 等于写入值；
      ② `codegraph.status`（引擎状态回写）：`exts` = 生效扩展名集合、`skipDirs` = 用户追加集、
         `indexedFiles` 命中文件数（与③期望集合规模一致）；
      ③ 落盘 bbolt 索引库 `<ws>/.chonkpilot/codegraph/index.db` 的**命中文件集合**
         （经引擎只读自检 `-dump` 读回）→ 与**夹具推导的显式期望集合字面全等**
         （一个不多一个不少，差集在失败信息里打印）。

    期望集合口径（夹具推导，见上方 `_G3_AB` / `_G3_B` 常量）：
      基线/A：6 个 .go 全收（gate_src/alpha.go + gate_skip/delta.go +
      gate_ign/{keep,gen.gen,special.gen,sub/u}.go；note.md/.txt 非受支持语言；stack 关 → .gitignore 不读）；
      步骤 B：追加 skip-dirs=gate_skip → gate_skip/ 目录剪枝 → gate_skip/delta.go 出（6 → 5）。
    """
    if prj().get("enable-codegraph") != "true":
        prj_save("enable-codegraph", "true")           # 独立 work-dir：由上用例关闭后重新启用
    base = wait_status(lambda o: o.get("state") == "ready", "启用后 codegraph 索引就绪")
    if base.get("indexedFiles") != len(_G3_AB):
        raise TestError("基线索引文件数应为 %d（夹具 .go 全集；note.md/.txt 非受支持语言；"
                        "stack-gitignore 关 → .gitignore 不读），实际=%r"
                        % (len(_G3_AB), base.get("indexedFiles")))
    _want_idx(_G3_AB, "G3 基线（exts=默认全语言，skip-dirs 空，stack 关）")
    print("[G3] 基线 status: state=ready exts=%r indexedFiles=%r；落盘索引命中集合字面全等 %d 个"
          % (base.get("exts"), base.get("indexedFiles"), len(_G3_AB)), flush=True)

    # ── 步骤 A：exts=".go"（+清空 skip-dirs）──
    open_page("settings-project", ".project-config-panel")
    click_tab(".project-config-panel", "CodeGraph 索引")
    set_textarea(0, ".go")
    set_textarea(1, "")
    click_save()
    a = wait_status(lambda o: o.get("state") == "ready" and o.get("exts") == [".go"],
                    "exts=.go 重建完成")
    if prj().get("codegraph.exts") != ".go" or prj().get("codegraph.skip-dirs") != "":
        raise TestError("A：prj 回读不符（exts=%r skip-dirs=%r）"
                        % (prj().get("codegraph.exts"), prj().get("codegraph.skip-dirs")))
    if a.get("indexedFiles") != len(_G3_AB):
        raise TestError("A：exts=.go 后 indexedFiles 应=%d，实际=%r"
                        % (len(_G3_AB), a.get("indexedFiles")))
    _want_idx(_G3_AB, "G3 A（exts=.go，skip-dirs 空，stack 关）")
    print("[G3] A exts=.go → status.exts=%r indexedFiles=%r；"
          "落盘索引命中集合字面全等 %d 个（含 gate_src+gate_skip）"
          % (a.get("exts"), a.get("indexedFiles"), len(_G3_AB)), flush=True)

    # ── 步骤 B：追加 skip-dirs="gate_skip" ──
    set_textarea(0, ".go")
    set_textarea(1, "gate_skip")
    click_save()
    b = wait_status(lambda o: o.get("state") == "ready" and "gate_skip" in (o.get("skipDirs") or []),
                    "skip-dirs=gate_skip 重建完成")
    if prj().get("codegraph.skip-dirs") != "gate_skip":
        raise TestError("B：prj 回读 codegraph.skip-dirs=%r" % (prj().get("codegraph.skip-dirs"),))
    if b.get("indexedFiles") != len(_G3_B):
        raise TestError("B：跳过 gate_skip 后 indexedFiles 应=%d，实际=%r"
                        % (len(_G3_B), b.get("indexedFiles")))
    _want_idx(_G3_B, "G3 B（skip-dirs=gate_skip）")
    print("[G3] B skip-dirs=gate_skip → status.skipDirs=%r indexedFiles=%r；"
          "落盘索引命中集合字面全等（gate_skip/delta.go 已剔除）"
          % (b.get("skipDirs"), b.get("indexedFiles")), flush=True)


# ══════════════════════════════════════════════════════════
# G4 codegraph.action → rebuild / retry / clear
# ══════════════════════════════════════════════════════════

def _emit_action(action):
    """写动作信号键（= `CodegraphConfig.vue emitAction()` 同一条消息面：
    `api/config.setConfig('codegraph.action', action)` → `data-prj-config-save`）。"""
    arm_refresh()
    prj_save("codegraph.action", action)
    if prj().get("codegraph.action") != action:
        raise TestError("A：codegraph.action 回读=%r，期望 %r" % (prj().get("codegraph.action"), action))


def case_g4_action_rebuild_retry_clear():
    """rebuild/retry → 状态流出现进行中阶段（`state=indexing` + `phase∈{configure,index}`）并回到
    `ready`；clear → 回「未初始化」（`state=""`、`indexedFiles=0`、`index.db` 被删且落盘无命中）。

    观测：`codegraph.status` 的写入经既有广播 `data-prj-config-refresh` 逐次到达前端 →
    捕获**完整状态流**（不靠轮询赛跑，瞬态也不漏）。插件侧阶段写入点 =
    `pushStatusPhase`（configure/index）；收口 = `refreshStatus`/`clearWorkspace`。
    """
    if status_obj().get("state") != "ready":
        prj_save("enable-codegraph", "true")
        wait_status(lambda o: o.get("state") == "ready", "前置：codegraph 就绪")

    for action, why in (("rebuild", "强制全量重建"), ("retry", "重试失败（降级全量重建）")):
        _emit_action(action)
        ss = wait_stream(_cycle_pred, "%s 状态流：indexing → ready" % action)
        phases = [s.get("phase") for s in ss if s.get("state") == "indexing"]
        if not any(p in ("configure", "index") for p in phases):
            raise TestError("%s：进行中阶段应含 configure/index，实际 phases=%r" % (action, phases))
        if ss[-1].get("state") != "ready":
            raise TestError("%s：状态流末尾应回到 ready，实际=%r" % (action, ss[-1]))
        print("[G4] %s（%s）→ A: codegraph.action=%r；B: 状态流 %d 条，"
              "进行中 phases=%r → 末态 state=%r"
              % (action, why, prj().get("codegraph.action"), len(ss), phases, ss[-1].get("state")),
              flush=True)

    # ── clear ──
    _emit_action("clear")
    ss = wait_stream(lambda x: any(s.get("state") in ("", "not_initialized") for s in x),
                     "clear 后状态回未初始化")
    st = status_obj()
    if st.get("state") not in ("", "not_initialized"):
        raise TestError("clear 后 codegraph.status.state=%r，期望未初始化" % (st.get("state"),))
    if st.get("indexedFiles") != 0:
        raise TestError("clear 后 indexedFiles 应=0，实际=%r" % (st.get("indexedFiles"),))
    if os.path.isfile(IDX_DB):
        raise TestError("clear 后索引库 index.db 应被删除：%s" % IDX_DB)
    _want_idx(_CLEAR, "G4 clear 后落盘索引（应为空集）")
    print("[G4] clear → A: codegraph.action=%r；B: state=%r indexedFiles=%r index.db 已删除且落盘命中集合为空"
          % (prj().get("codegraph.action"), st.get("state"), st.get("indexedFiles")), flush=True)


# ══════════════════════════════════════════════════════════
# G5 prj skip_dirs → file_find 命中集合
# ══════════════════════════════════════════════════════════

def case_g5_prj_skip_dirs_end_to_end():
    """prj `skip_dirs`（GUI「参数 → 项目」写入）→ 真实 `file_find` 不再返回被跳过目录下文件。

    链路：GUI 输入框失焦 → `setConfig('skip_dirs', '["_skipme"]')` → persist prj 键 →
    `data-prj-config-refresh` → llm-server `loadExecConfig`（`server.go:1957-1995`）重跑 →
    `mcp-server Config.SetRuntime` → `defaultsMap().skip_dirs` 注入每次 `tools/call`
    （`config.go:198-212`）→ `fileops.ParseWalkFilter` 并入跳过集。
    判据：同一轮提示词、同一工具、同一目录，仅改 prj 键 → 命中集合从 {_skipme/dep.txt, ok.txt}
    收敛为 {ok.txt}（未跳过文件仍命中 = 工具确已执行，非"没跑"）。
    """
    if prj().get("skip_dirs"):
        raise TestError("基线 prj skip_dirs 应为空（独立 work-dir），实际=%r" % prj().get("skip_dirs"))

    txt1, p1 = run_find_turn()
    if "ok.txt" not in txt1 or "_skipme" not in txt1:
        raise TestError("基线 file_find 应同时命中 ok.txt 与 _skipme/dep.txt，实际=%r" % txt1)
    print("[G5] 基线 file_find（无 skip_dirs）→ tool=%s 命中=%r" % (p1.get("tool"), txt1), flush=True)

    open_params_project()
    set_param_input("跳过目录", "_skipme")
    got = prj().get("skip_dirs")
    if got != '["_skipme"]':
        raise TestError("A：GUI 写后 prj skip_dirs=%r，期望 '[\"_skipme\"]'" % (got,))

    txt2, p2 = run_find_turn()
    if "ok.txt" not in txt2:
        raise TestError("设置 skip_dirs 后仍应命中未跳过文件 ok.txt，实际=%r" % txt2)
    if "_skipme" in txt2:
        raise TestError("设置 skip_dirs=_skipme 后命中集合仍含被跳过目录：%r" % txt2)
    print("[G5] skip_dirs=%r → tool=%s 命中=%r（A: prj 回读一致；B: 被跳过目录消失，未重启）"
          % (got, p2.get("tool"), txt2), flush=True)


# ══════════════════════════════════════════════════════════
# G6 timeout_sec / max_concurrency（A 必做；B 不可稳定观测，见 docstring）
# ══════════════════════════════════════════════════════════

def case_g6_exec_params_readback():
    """`timeout_sec` / `max_concurrency`：GUI 改后 **prj 回读一致**（含重开页面输入框回显）。

    B（效果）**不做**——依据（代码级，非"懒得做"）：
      ① `timeout_sec` → `mcp-server Config.execTimeout`，但 `executor.go:106-109`
         **契约级 `timeout` 优先**（`if td.Timeout > 0 { timeout = td.Timeout }`），而随包发布的
         8 个内置契约全部声明了 `timeout`（file_read/file_find/file_diff/filesync_run/script_run
         =60~300s、web_fetch=120、desktop_run/browser_run=300）→ prj `timeout_sec` 对**内置工具
         无效果**；要稳定观测需"无契约 timeout 的工具"，当前无此类内置工具 → 无法在 GUI 侧构造。
      ② `max_concurrency` → `mcp-server limiter` 并发闸门，仅在"同时在飞 ≥2 个 tools/call"时
         表现为**等待**；既无对外状态面，也无稳定时序判据（且受契约 timeout 影响）→ 不可稳定观测。
    故本条只做 A（写入 + 回读 + 重开页面回显），并如实标注 B 不可覆盖。
    """
    open_params_project()
    set_param_input("服务超时(秒)", "123")
    if prj().get("timeout_sec") != "123":
        raise TestError("A：timeout_sec 回读=%r" % (prj().get("timeout_sec"),))
    set_param_input("最大并发", "7")
    if prj().get("max_concurrency") != "7":
        raise TestError("A：max_concurrency 回读=%r" % (prj().get("max_concurrency"),))

    # 回读（重开页面 → 输入框回显 = 从 prj 库重新加载）
    open_params_project()
    rows = {r["label"]: r["value"] for r in (param_rows() or [])}
    if rows.get("服务超时(秒)") != "123" or rows.get("最大并发") != "7":
        raise TestError("A：重开页面后回显不符：%r" % (rows,))
    print("[G6] timeout_sec=%r max_concurrency=%r（prj 回读 + 重开页面回显一致；"
          "B 不可稳定观测，依据见 docstring）" % (prj().get("timeout_sec"), prj().get("max_concurrency")),
          flush=True)


# ══════════════════════════════════════════════════════════
# G7 codegraph.stack-gitignore → gitignore 语义（文件级排除 + '!' 反选 + 目录剪枝）
# ══════════════════════════════════════════════════════════

def case_g7_codegraph_stack_gitignore():
    """勾选「叠加 gitignore」→ 引擎按 **完整 gitignore 语义** 排除（与 git 一致）。

    夹具（文件头创建于 WS）：
      WS/.gitignore = `*.gen.go` / `!special.gen.go` / `gate_ign/sub/` / `!gate_ign/sub/u.go`
      WS/gate_ign/{keep.go, gen.gen.go, special.gen.go, sub/u.go}

    链路：GUI 勾选 + `handleIndexSave`（**无条件写** exts/skip-dirs/stack-gitignore 三键，
    `CodegraphConfig.vue:215-230`）→ `data-prj-config-refresh` → 插件 `readIndexConfig`
    （原样透传 stack_gitignore，`codegraph.go:746-775`）→ 引擎 `excludeOptions`
    （`server/index.go:134-141`）→ `ignore.WalkDir`（`src/lib/ignore/ignore.go`，逐级读各级 .gitignore）。

    判据 A（数据面回读）：`prj['codegraph.stack-gitignore']=="true"`、
    `status.stackGitignore==True`、`status.skipDirs` 空（用户规则空）。
    判据 B（终效，落盘 bbolt 索引库命中集合 + `status.indexedFiles`）：
      与夹具推导的**显式期望集合字面全等**（`_G7` 常量，一个不多一个不少）：
      * 含 `gate_ign/keep.go`（无规则命中）✔
      * 不含 `gate_ign/gen.gen.go`（`*.gen.go` **文件级排除**）✔
      * 含 `gate_ign/special.gen.go`（`!special.gen.go` 反选成功）✔
      * 不含 `gate_ign/sub/u.go`（`gate_ign/sub/` 目录命中 → **不下降**，其内 `!` **无效**）✔
    全集 = gate_src/alpha.go + gate_skip/delta.go + keep.go + special.gen.go = **4**。
    收尾：取消勾选并保存（回 `stack-gitignore=false`）→ A 回落，避免污染后续用例。
    """
    if prj().get("enable-codegraph") != "true":
        prj_save("enable-codegraph", "true")

    open_index_tab("CodeGraph 索引")
    set_textarea(0, ".go")
    set_textarea(1, "")
    set_stack_gitignore(True)
    click_save()

    st = wait_status(lambda o: o.get("state") == "ready" and o.get("stackGitignore") is True
                     and not (o.get("skipDirs") or []),
                     "叠加 gitignore 勾选后重建完成（stackGitignore=true）")
    if prj().get("codegraph.stack-gitignore") != "true":
        raise TestError("A：prj codegraph.stack-gitignore=%r，期望 'true'"
                        % (prj().get("codegraph.stack-gitignore"),))
    if (st.get("skipDirs") or []):
        raise TestError("A：勾选 stack-gitignore 时用户规则应为空，实际 skipDirs=%r"
                        % (st.get("skipDirs"),))

    if st.get("indexedFiles") != len(_G7):
        raise TestError("B：勾选后 indexedFiles 应=%d，实际=%r"
                        % (len(_G7), st.get("indexedFiles")))
    _want_idx(_G7, "G7 勾选叠加 gitignore（.gitignore 文件级排除 + '!' 反选 + 目录剪枝）")
    print("[G7] 勾选叠加 gitignore → A: stack-gitignore=%r stackGitignore=%r skipDirs=%r；"
          "B: 落盘索引命中集合字面全等（含 keep/special、不含 gen.gen/sub/u），indexedFiles=%r"
          % (prj().get("codegraph.stack-gitignore"), st.get("stackGitignore"),
             st.get("skipDirs"), st.get("indexedFiles")), flush=True)

    # 收尾：取消勾选 → stack-gitignore=false（A 回落）
    set_stack_gitignore(False)
    click_save()
    st2 = wait_status(lambda o: o.get("state") == "ready" and not o.get("stackGitignore"),
                      "取消勾选后重建完成（stackGitignore 回落）")
    if prj().get("codegraph.stack-gitignore") != "false":
        raise TestError("收尾：codegraph.stack-gitignore=%r，期望 'false'"
                        % (prj().get("codegraph.stack-gitignore"),))
    print("[G7] 收尾 取消勾选 → A: stack-gitignore=%r stackGitignore=%r（回落）"
          % (prj().get("codegraph.stack-gitignore"), st2.get("stackGitignore")), flush=True)


# ══════════════════════════════════════════════════════════
# G8 用户规则（输入框）最高优先级 + 文件级排除 + '!' 反选
# ══════════════════════════════════════════════════════════

def case_g8_user_rules_priority():
    """用户规则（输入框「排除的目录和文件」）**最高优先级** + 文件级排除 + `!` 反选。

    口径：`codegraph.skip-dirs` 由插件 `splitRules` 按逗号/分号/换行拆分（**保序、保留 `!`**，
    `codegraph.go:794-807`）**原样透传**给引擎（不折成目录名，`codegraph.go:741-743/809-813`）；
    引擎 `ignore.Options.UserRules` 为最高优先级（等价 `git --exclude`，`ignore.go:13/80-98`）。
    判据 A：`status.skipDirs` 与写入值一致（含 `!` 项、顺序不变，**未折名**）。
    判据 B：落盘 bbolt 索引库命中集合与**夹具推导的显式期望集合字面全等**（`_G8_S1/S2/S3` 常量）。

    步骤 1：规则 `*.gen.go`（stack 关）→ 期望 = {alpha.go, delta.go, keep.go, sub/u.go}
            （两个 gen 文件被 *.gen.go 排除；keep.go / sub/u.go 仍命中）。
    步骤 2：规则 `*.gen.go, !special.gen.go` → 期望 = {alpha.go, delta.go, keep.go, special.gen.go, sub/u.go}
            （special.gen.go **反选回来**；gen.gen.go 仍排除）。
    步骤 3（对照）：勾选 stack + 用户规则 `!gate_ign/sub/` → 期望同步骤 2（5 个）——
            sub/u.go **重新出现**（用户规则最高优先级、且是目录级反选 → 压过 .gitignore 的 `gate_ign/sub/` 目录排除），
            同时 .gitignore 的 `*.gen.go` / `!special.gen.go` 仍生效（gen.gen.go 排除、special.gen.go 保留）。
    收尾：清空排除规则 + 关闭 stack（避免污染后续 / G9）。
    """
    if prj().get("enable-codegraph") != "true":
        prj_save("enable-codegraph", "true")
    if prj().get("codegraph.stack-gitignore") not in (None, "", "false"):
        raise TestError("前置：stack-gitignore 应为关（G7 收尾回落后），实际=%r"
                        % (prj().get("codegraph.stack-gitignore"),))

    # ── 步骤 1：用户规则 "*.gen.go"（仅用户规则，stack 关）──
    open_index_tab("CodeGraph 索引")
    set_textarea(0, ".go")
    set_textarea(1, "*.gen.go")
    click_save()
    s1 = wait_status(lambda o: o.get("state") == "ready" and o.get("skipDirs") == ["*.gen.go"],
                     "用户规则 *.gen.go 生效")
    if prj().get("codegraph.skip-dirs") != "*.gen.go":
        raise TestError("步骤1 A：prj skip-dirs=%r" % (prj().get("codegraph.skip-dirs"),))
    _want_idx(_G8_S1, "G8 步骤1（用户规则 *.gen.go，stack 关）")
    print("[G8] 步骤1 skip-dirs=%r → A: status.skipDirs=%r（原样未折名）；"
          "B: 命中集合字面全等（不含两个 gen、含 keep/sub/u）"
          % (prj().get("codegraph.skip-dirs"), s1.get("skipDirs")), flush=True)

    # ── 步骤 2："*.gen.go, !special.gen.go"（保序含 '!'）──
    set_textarea(1, "*.gen.go, !special.gen.go")
    click_save()
    s2 = wait_status(lambda o: o.get("state") == "ready"
                     and o.get("skipDirs") == ["*.gen.go", "!special.gen.go"],
                     "用户规则含 '!' 反选生效")
    if prj().get("codegraph.skip-dirs") != "*.gen.go, !special.gen.go":
        raise TestError("步骤2 A：prj skip-dirs=%r" % (prj().get("codegraph.skip-dirs"),))
    _want_idx(_G8_S2, "G8 步骤2（用户规则 *.gen.go, !special.gen.go，stack 关）")
    print("[G8] 步骤2 skip-dirs=%r → A: status.skipDirs=%r（保序含 '!'）；"
          "B: 命中集合字面全等（special.gen.go 反选回来、gen.gen.go 仍排除）"
          % (prj().get("codegraph.skip-dirs"), s2.get("skipDirs")), flush=True)

    # ── 步骤 3：勾选 stack + 用户规则 "!gate_ign/sub/"（用户规则压过 .gitignore 目录排除）──
    set_stack_gitignore(True)
    set_textarea(1, "!gate_ign/sub/")
    click_save()
    s3 = wait_status(lambda o: o.get("state") == "ready" and o.get("stackGitignore") is True
                     and o.get("skipDirs") == ["!gate_ign/sub/"],
                     "用户规则 !gate_ign/sub/ 压过 .gitignore 目录排除")
    _want_idx(_G8_S3, "G8 步骤3（stack + 用户规则 !gate_ign/sub/）")
    print("[G8] 步骤3 勾选 stack + 用户规则 %r → A: skipDirs=%r stackGitignore=%r；"
          "B: 命中集合字面全等（sub/u.go 重新出现；gen.gen.go 仍排除）"
          % (prj().get("codegraph.skip-dirs"), s3.get("skipDirs"), s3.get("stackGitignore")),
          flush=True)

    # 收尾：清空规则 + 关闭 stack
    set_textarea(1, "")
    set_stack_gitignore(False)
    click_save()
    wait_status(lambda o: o.get("state") == "ready" and not o.get("skipDirs")
                and not o.get("stackGitignore"), "G8 收尾（规则清空 + stack 关）")
    if prj().get("codegraph.skip-dirs") not in (None, "") or \
       prj().get("codegraph.stack-gitignore") != "false":
        raise TestError("G8 收尾：skip-dirs=%r stack-gitignore=%r"
                        % (prj().get("codegraph.skip-dirs"), prj().get("codegraph.stack-gitignore")))


# ══════════════════════════════════════════════════════════
# G9 vfts 侧同语义（引擎遍历 + 清单扫描共用同一匹配器）
# ══════════════════════════════════════════════════════════

def case_g9_vfts_gitignore_semantics():
    """vfts 侧同语义：引擎遍历 + 清单扫描共用 `src/lib/ignore` → 与 codegraph 行为一致。

    证据选择（**清单面**，非计数退化）：vfts 的文件清单落 **项目级 prj 库 `file_list` 表**
    （`src/lib/ignore` 消费方之一 = `plugin-vfts/manifest.go:182-213 scanFiles` 走 `ignore.WalkDir`），
    经**既有** `data-filelist-list`（61 §3.1）逐文件读回 `path` —— 可逐文件断言「谁该在 / 谁不该在」，
    比 `vfts.status.indexedFiles` 计数强。清单**不落盘为独立文件**（引擎落盘只有 `meta.json` 计数 +
    zvec `collection/` 目录，无 `manifest.json`）→ 故读其唯一产品读面 `data-filelist-list`。

    链路同 G7：GUI Vfts 页写入 exts/排除规则/勾选 → `data-prj-config-refresh` → 插件
    `ensureWorkspace`（`vfts.go:479-555`）→ 引擎 `vfts_index`（`server/index.go:91-118 collectFiles`
    走 `ignore.WalkDir`）+ 插件 `rebuildManifest`（`manifest.go:360-406` 走同一 `ignore.WalkDir`）。

    判据 A：`prj['vfts.exts']==".go"`、`prj['vfts.stack-gitignore']=="true"`、`prj['vfts.skip-dirs']` 空；
            `vfts.status.stackGitignore==True`。
    判据 B：`data-filelist-list` 文件集合 含 keep.go / special.gen.go、不含 gen.gen.go / sub/u.go；
            `vfts.status.indexedFiles==4`。
    收尾：清空规则 + 取消勾选 + 关闭 vfts（`enable-vfts=false`）。
    """
    # 前置：开启 vfts（G2 已关闭）
    if prj().get("enable-vfts") != "true":
        open_index_tab("Vfts 全文索引")
        st = find_switch("Vfts 全文索引")
        if not st:
            raise TestError("未找到「Vfts 全文索引」开关")
        if st["checked"]:
            raise TestError("前置：enable-vfts 应为关")
        if click_switch("Vfts 全文索引") != "ok":
            raise TestError("点击 Vfts 开关失败")
        if prj().get("enable-vfts") != "true":
            raise TestError("前置：点击后 enable-vfts=%r，期望 'true'" % (prj().get("enable-vfts"),))

    # 写 exts=".go" / 排除规则清空 / 勾选叠加 gitignore
    open_index_tab("Vfts 全文索引")
    set_textarea(0, ".go")
    set_textarea(1, "")
    set_stack_gitignore(True)
    click_save()

    a = wait_status(lambda o: o.get("state") == "ready" and o.get("stackGitignore") is True
                    and o.get("indexedFiles") == 4,
                    "vfts 叠加 gitignore 重建完成（ready / indexedFiles=4）", key=VSTATUS_KEY)
    if prj().get("vfts.exts") != ".go":
        raise TestError("A：prj vfts.exts=%r" % (prj().get("vfts.exts"),))
    if prj().get("vfts.stack-gitignore") != "true":
        raise TestError("A：prj vfts.stack-gitignore=%r" % (prj().get("vfts.stack-gitignore"),))
    if prj().get("vfts.skip-dirs") not in (None, ""):
        raise TestError("A：vfts.skip-dirs 应为空（用户规则空），实际=%r" % (prj().get("vfts.skip-dirs"),))

    # B：清单逐文件集合（清单在引擎索引后由插件重建 → 轮询等齐口径同 codegraph）
    paths = wait_filelist(
        lambda ps: has_path(ps, "gate_ign/keep.go") and has_path(ps, "gate_ign/special.gen.go")
        and not has_path(ps, "gate_ign/gen.gen.go") and not has_path(ps, "gate_ign/sub/u.go"),
        "vfts file_list 收敛（含 keep/special、不含 gen.gen/sub/u）")
    miss = [s for s in ("gate_ign/keep.go", "gate_ign/special.gen.go") if not has_path(paths, s)]
    hit = [s for s in ("gate_ign/gen.gen.go", "gate_ign/sub/u.go") if has_path(paths, s)]
    if miss or hit:
        raise TestError("B：vfts 清单集合不符（应含而缺=%r；不应含却有=%r）；全部=%r"
                        % (miss, hit, [p.split("/")[-1] for p in paths]))
    print("[G9] vfts 勾选叠加 gitignore → A: exts=%r stackGitignore=%r skipDirs=%r；"
          "B: file_list(%d) 含 keep/special、不含 gen.gen/sub/u（与 codegraph 同匹配器）"
          % (prj().get("vfts.exts"), a.get("stackGitignore"), a.get("skipDirs"), len(paths)), flush=True)

    # 收尾：清空规则 + 取消勾选 + 关闭 vfts
    set_textarea(1, "")
    set_stack_gitignore(False)
    click_save()
    time.sleep(1.0)
    open_index_tab("Vfts 全文索引")
    if click_switch("Vfts 全文索引") != "ok":
        raise TestError("G9 收尾：关闭 Vfts 开关失败")
    if prj().get("enable-vfts") != "false":
        raise TestError("G9 收尾：enable-vfts=%r，期望 'false'" % (prj().get("enable-vfts"),))
    print("[G9] 收尾 enable-vfts=%r stack-gitignore=%r（已回落）"
          % (prj().get("enable-vfts"), prj().get("vfts.stack-gitignore")), flush=True)


# ══════════════════════════════════════════════════════════
# main
# ══════════════════════════════════════════════════════════

def main():
    c.wait_ready()
    c.console(clear=True)
    total = 0
    ok = 0
    cases = [
        ("G1 enable-codegraph → 工具面出现/消失 8 个 codegraph 工具（保存即生效）",
         case_g1_enable_codegraph_surface),
        ("G2 enable-vfts → 工具面出现/消失 vfts_query（保存即生效）",
         case_g2_enable_vfts_surface),
        ("G3 codegraph.exts/skip-dirs → 索引范围终效（status + bbolt 索引命中集合）",
         case_g3_index_scope),
        ("G4 codegraph.action rebuild/retry 进行中阶段→ready；clear→未初始化",
         case_g4_action_rebuild_retry_clear),
        ("G5 prj skip_dirs → file_find 命中集合剔除被跳过目录（GUI 配置面）",
         case_g5_prj_skip_dirs_end_to_end),
        ("G6 timeout_sec/max_concurrency → prj 回读一致（B 不可稳定观测）",
         case_g6_exec_params_readback),
        ("G7 codegraph.stack-gitignore → .gitignore 生效 + 文件级排除 + '!' 反选 + 目录剪枝",
         case_g7_codegraph_stack_gitignore),
        ("G8 用户规则最高优先级 + 文件级排除 + '!' 反选（含压过 .gitignore 目录排除）",
         case_g8_user_rules_priority),
        ("G9 vfts.stack-gitignore → 同一匹配器（file_list 清单逐文件集合）",
         case_g9_vfts_gitignore_semantics),
    ]
    for name, fn in cases:
        total += 1
        ok += run_case(name, fn)
    # 与其它套件统一口径：计数汇总 + 退出码（0=全过）——见 51-FP与测试映射 §1
    print("\n索引/执行参数断言：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
    print("RESULT:", ok == total)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

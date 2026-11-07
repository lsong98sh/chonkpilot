# -*- coding: utf-8 -*-
"""P4 批次 L4 套件：项目级「索引 / 执行参数」配置的**落库 + 真实效果**端到端。

覆盖（每条 = A 数据面回读 + B 真实效果；B 恒以可观测证据收口，不用"保存成功即算过"）：

  G1 `codegraph` 插件启停 → 工具面出现/消失 codegraph 8 个 + `vfts_query`。**保存即生效**
                         （2026-10-23 口径：索引工具已插件化，唯一门控 = 插件启用开关）
  G2 `enable-codegraph` / `enable-vfts` → **历史 prj 键不再门控工具面**（防旧门控回归）
  G3 `codegraph.exts` / `codegraph.skip-dirs` → **索引范围终效**：
                         `codegraph.status.exts/skipDirs/indexedFiles` + 落盘 bbolt 索引库
                         （`index.db`）命中集合 → **夹具推导的显式期望集合字面全等**
                         （一个不多一个不少；差集打印在失败信息里）。判据已从「成员检查 + 计数」
                         升级为「集合字面全等」（基线/A 6 个 .go、跳过 gate_skip 后 5）——见 G3 docstring。
  G4 `codegraph.action`  rebuild / retry → 状态流出现**进行中阶段**（`state=indexing` 且
                         `phase∈{configure,index}`）后回到 `ready`；clear → 回「未初始化」
                         （`state=""`、`indexedFiles=0`、`index.db` 被删除且落盘无命中）。
                         **2026-10-28（CODEGRAPH-IDXDB-1 已修）**：重建后交叉硬断言
                         `status.indexedFiles == 落盘 index.db 条数`；clear 后硬断言 status 回
                         未初始化（此前因双 Workspace 缺陷不做交叉断言）。
  G5 prj `skip_dirs`     （**GUI 配置面**写入：设置 → 参数 → 项目标签页）→ prj 回读 + 重开页面回显；
                         B（终效）不可覆盖：find 已由 `file_find` 单工具承载，跳过目录为调用级
                         `targets[].skipDirs`，prj `skip_dirs` 不再参与执行（依据见 G5 docstring）。
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
  G9 vfts 侧同语义：`vfts.stack-gitignore` + exts/排除规则 → **插件自持清单
                         `<workdir>/.chonkpilot/vfts/file_list.json`** 的文件集合（**夹具 + 配置推导的
                         显式期望集合字面全等**，同 G3/G4/G7/G8 体例）+ `vfts.status`
                         （vfts 引擎遍历与 codegraph 共用插件仓 `shared/ignore` 匹配器 → 行为一致）。

观测手段（**零新增 MQ 主题**，全部取自 61-消息一览既有面）：
  * 工具面 = 客户端能力面 `tools-list`（61 §4.5：桥 → gateway `mcp-tools-list`；桥按当前实例
    作用域过滤）→ `{tools:[{name}]}`。**2026-10-23 口径**：P0a 去 `self_` 前缀后，
    codegraph 插件注册的工具名 = manifest `mcp.hot_tools` 原名（无前缀）。
  * 索引状态 = **插件动作 `engine-status`** 现取（PLUGIN-UI-2，2026-10-28；与配置页 / 状态栏
    `badge` 同一数据源）。插件**不再回写** prj 键 `codegraph.status` / `vfts.status`，宿主零引用。
  * vfts 文件清单 = 插件自持文件 `<workdir>/.chonkpilot/vfts/file_list.json`（每行含 `path`
    绝对路径）→ 逐文件命中集合（与落盘 codegraph 索引同判据口径）。
  * codegraph 落盘索引 = **bbolt 单文件库** `<ws>/.chonkpilot/codegraph/index.db`（引擎侧
    store.go；不再有 index.json）。CODEGRAPH-INLINE（2026-11-02）后引擎库内联进插件进程、
    发行根不再投放 `mcps/codegraph/` 引擎 exe（原 `-dump` 只读自检面退场）→ 命中文件集合
    经**查询工具** `codegraph_get_symbol_info`（file+name 精确查，tool.call 动作面）对夹具
    全部哨兵符号逐一探测推导（符号 ↔ 文件一一映射，哨兵集合即文件集合）。
  * 端到端工具调用已随 `dsl_run` 退役移除（G5 改为配置面回读，见 G5 docstring）。

隔离与环境（51-FP与测试映射 §5/§6-8）：
  * **自起** GUI：动态端口 + 独立 work-dir（临时目录）+ 独立 `HOME`
    （usr 主库全新；prj 库全新 → 无历史覆盖；codegraph 插件按默认启用态加载）。
  * 不碰共享 `systest/ws`（其 `codegraph/vfts` 索引是 K5/K6 夹具）——本套件**不清除**他套件索引。
  * 套件级配置快照-还原 = `_h.suite_config_guard(c)`（退出前自动回滚 usr+prj，含异常/中断路径）。
  * mock LLM 由 harness 自起自收；索引落盘（`<ws>/.chonkpilot/{codegraph,vfts}`）在临时
    work-dir 内 → 随 `harness.tmp_dir` 一并删除 → **零残留**（进程/端口/索引目录）。

运行：python run_index_gate.py   （自起自收，无需外部底座）
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51 §6 测试资源规范）  # noqa: E402

# ── 工具面（网关暴露名）──────────────────────────────────
# 口径变更（2026-10-23）：P0a 去 `self_` 前缀后网关暴露名即插件清单 `mcp.hot_tools` 原名 →
# 断言用无前缀名（此前 `self_*` 已不再出现）。
# codegraph 查询工具全集 = 8（`plugin-codegraph` hot_tools；含 `*_callers` / `*_callees`）。
CG_TOOLS = [
    "codegraph_symbol_search",
    "codegraph_get_symbol_info",
    "codegraph_callers",
    "codegraph_callees",
    "codegraph_get_dependency_graph",
    "codegraph_find_circular_deps",
    "codegraph_analyze_complexity",
    "codegraph_get_module_summary",
]
VF_TOOL = "vfts_query"

STATUS_KEY = "codegraph.status"              # 选择器 token（仅用于区分引擎；**不再读该 prj 键**）
VSTATUS_KEY = "vfts.status"                  # 同上（vfts 引擎选择器）


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
#    规则人工推导；**禁循环论证**——绝不用引擎查询面输出反推期望）。
# 受支持扩展名（引擎 lang.go extLang）= {.go,.js,.jsx,.mjs,.cjs,.ts,.tsx,.py,.pyw,.rs,.java}；
# 故夹具里的 `gate_src/note.md` / `gate_find/*.txt` / `.gitignore` 一律**不被索引**；
# `gate_find/` 仅 G5（FIND）用，与索引无关。未写 `codegraph.exts` → 引擎回落全部受支持语言。
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
# G9：vfts 侧同语义（exts=".go" + stack-gitignore=true + 用户规则空）→ 与 G7 **同口径**：
#   * 扩展名：本用例经 GUI 显式写 `vfts.exts=".go"`，**不使用引擎默认集**（默认含 .md/.txt/.json/.vue
#     等，见 `chonkpilot-plugins/codegraph/engines/vfts/server/ext.go:9-25 defaultExts`；与 codegraph 默认集不同）→ 生效集 = {".go"}
#     → 夹具里 `gate_src/note.md` / `gate_find/*.txt` / `.gitignore` 一律不收录。
#   * 排除：`vfts.skip-dirs` 空（无用户规则）；`vfts.stack-gitignore="true"` → 读 `WS/.gitignore`
#     （`*.gen.go` / `!special.gen.go` / `gate_ign/sub/` / `!gate_ign/sub/u.go`）+ 内置强制（`.git/`、
#     `.chonkpilot/`）+ 默认排除（node_modules/ 等，均不命中夹具路径）。语义由插件仓
#     `shared/ignore`（`ignore.WalkDir`）承载，与 codegraph 引擎/清单扫描共用 → 集合一致。
_G9 = {
    "gate_src/alpha.go",             # 在：.go + .gitignore 无匹配
    "gate_skip/delta.go",            # 在：.go + .gitignore 无匹配（本用例未设 skip-dirs）
    "gate_ign/keep.go",              # 在：*.gen.go 不匹配（keep.go 不以 ".gen.go" 结尾）
    "gate_ign/special.gen.go",       # 在：*.gen.go 命中后被 !special.gen.go 反选回来
    # 不在：gate_ign/gen.gen.go（*.gen.go 文件级排除，无 '!' 救回）
    # 不在：gate_ign/sub/u.go（父目录 gate_ign/sub/ 命中 → 目录剪枝，其后代 '!' 无效）
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
    """索引状态（**插件自持**，PLUGIN-UI-2）：经插件动作面 `engine-status` 现取。

    2026-10-28 起插件不再回写 prj 键 `codegraph.status` / `vfts.status`（宿主也零引用）；
    本门禁改为问插件（与配置页 / 状态栏徽标同一数据源），返回该引擎的 status 对象。
    """
    eng = "vfts" if key == VSTATUS_KEY else "codegraph"
    try:
        r = plugin_api("engine-status", {"workdir": WS, "engine": eng})
    except TestError:
        return {}
    if not isinstance(r, dict):
        return {}
    v = r.get(eng)
    return v if isinstance(v, dict) else {}


# ── 插件配置/动作面（PLUGIN-UI-1：`POST /plugins/codegraph/api/<action>`）──────
# 2026-10-26（D-71）：codegraph 配置页已改为**插件自持 iframe**（`webui/config.html`），
# 主仓不再有 CodegraphConfig.vue / `codegraph.*` 用户键 / `codegraph.action` 信号键。
# 唯一配置源 = prj 键 `plugins.codegraph.*`；写入通道 = 插件动作面 `save`（页面按钮同一条）。
# 这里经页面上下文 fetch 调同一条动作面（宿主请求头转发修复后可用），断言仍取数据面 + 落盘产物。

def plugin_api(action, payload=None, timeout=180, _retries=3):
    """调用 codegraph 插件动作面（= 配置页 `api(action, payload)` 同一 URL/头）。

    瞬时锁重试（2026-10-26 批跑定位）：引擎落盘 `meta.json` 走「`.tmp` → rename」原子替换；
    Windows 下目标文件若正被并发读句柄持有（同刻 status/索引读取），rename 报
    `The process cannot access the file because it is being used by another process`
    → 动作语义此时是「配置已写入，仅引擎下发失败」，**幂等可重放** → 有界重试。
    """
    body = json.dumps(json.dumps(payload or {}))
    c.eval("window.__ckApiRes=null;fetch('/plugins/codegraph/api/%s',{method:'POST',"
           "headers:{'Content-Type':'application/json','X-CK-Plugin-Id':'codegraph'},body:%s})"
           ".then(async r=>{window.__ckApiRes={status:r.status,body:await r.text()}})"
           ".catch(e=>{window.__ckApiRes={status:0,body:String(e)}});'ok'"
           % (action, body))
    deadline = time.time() + timeout
    while time.time() < deadline:
        r = deep(c.eval("window.__ckApiRes"))
        if isinstance(r, dict):
            code = r.get("status")
            raw = r.get("body") or ""
            try:
                val = json.loads(raw) if raw else None
            except Exception:
                val = raw
            if code != 200:
                msg = str(val)
                if _retries > 0 and ("being used by another process" in msg
                                     or "used by another process" in msg):
                    time.sleep(1.0)
                    return plugin_api(action, payload, timeout, _retries - 1)
                raise TestError("插件动作 %s 失败（HTTP %s）：%s" % (action, code, msg[:300]))
            return val
        time.sleep(0.2)
    raise TestError("插件动作 %s 超时（%ss）" % (action, timeout))


def cg_save(entries):
    """保存 `plugins.codegraph.*` 子集（键可写短名：enabled/exts/skip-dirs/stack-gitignore/vfts.*）。"""
    full = {(k if k.startswith("plugins.") else "plugins.codegraph." + k): v for k, v in entries.items()}
    return plugin_api("save", {"workdir": WS, "entries": full})


def cg_index():
    """全量（重）建索引（= 配置页「立即索引」：`tool.call codegraph_initialize`）。"""
    return plugin_api("tool.call", {"name": "codegraph_initialize", "args": {"workdir": WS}})


def vfts_index():
    """vfts 全量（重）建索引（= 配置页「立即索引」的 vfts 段：`tool.call vfts_index`）。"""
    return plugin_api("tool.call", {"name": "vfts_index", "args": {"workdir": WS}})


def vfts_index_retry(tries=5):
    """vfts 建索引 + 有界重试：配置下发后的自动索引与我方「立即索引」可能并发，
    引擎侧 zvec 读写锁冲突（`INTERNAL_ERROR: Can't lock read-write collection ... LOCK`）
    属瞬态 → 退避重试（不是断言放宽：最终仍要求建索引成功）。"""
    last = None
    for _ in range(tries):
        try:
            return vfts_index()
        except TestError as e:
            last = e
            if "LOCK" not in str(e) and "lock" not in str(e):
                raise
            time.sleep(1.5)
    raise last


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


def wait_vfts_ready(pred, desc, max_wait=120, reindex_tries=3):
    """等 vfts status 收敛；若进入 `state=error` 且原因为**瞬态锁**（zvec/rocksdb 的
    `... LOCK: The process cannot access the file ...`，来自并发索引/查询句柄）→ 重新触发
    「立即索引」再等（终态仍必须是 ready；断言不放宽，只是给瞬态锁一个自愈机会）。
    """
    deadline = time.time() + max_wait
    last = {}
    while time.time() < deadline:
        last = status_obj(VSTATUS_KEY)
        if pred(last):
            return last
        msg = str(last.get("message") or "")
        if last.get("state") == "error" and "LOCK" in msg.upper() and reindex_tries > 0:
            reindex_tries -= 1
            print("[G9] vfts 索引遇瞬时锁 → 重试一次：%s" % msg[:140], flush=True)
            try:
                vfts_index()
            except TestError as e:
                print("[G9] 重试触发失败：%r" % (e,), flush=True)
            deadline = time.time() + max_wait
        time.sleep(0.5)
    raise TestError("%s 超时（%ss）；末次 status=%r" % (desc, max_wait, last))


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
    """读 vfts 增量清单（插件自持文件 `<workdir>/.chonkpilot/vfts/file_list.json`）→ 文件绝对路径列表（'/' 分隔）。"""
    path = os.path.join(WS, ".chonkpilot", "vfts", "file_list.json")
    try:
        with open(path, "r", encoding="utf-8") as f:
            rows = json.load(f)
    except (OSError, ValueError):
        return []
    return [str((e or {}).get("path") or "") for e in (rows or [])]


def _ws_rel(p):
    """清单绝对路径（'/' 或 '\\'）→ 相对 WS 的 '/' 路径（不在 WS 内/跨盘 → 仅归一分隔符）。"""
    try:
        return os.path.relpath(os.path.normpath(p), os.path.normpath(WS)).replace("\\", "/")
    except ValueError:
        return p.replace("\\", "/")


def _want_filelist(expected, desc, max_wait=120):
    """断言 vfts 清单（插件自持 `<workdir>/.chonkpilot/vfts/file_list.json`）文件集合与期望**字面全等**
    （一个不多一个不少）；差集打印在失败信息里（与 `_want_idx` 同体例）。

    清单由插件在引擎索引后**异步重建**（`plugin-vfts/manifest.go rebuildManifest`，晚于
    `vfts.status=ready`）→ 轮询至收敛。期望集合由夹具 + vfts.exts / vfts.skip-dirs /
    vfts.stack-gitignore 人工推导（见上方 `_G9` 常量），**不得**用清单输出反推（禁循环论证）。
    清单 `path` 为绝对路径 → 按 WS 转相对后比较。返回（实际相对路径集合, 原始路径列表）。
    """
    want = set(expected)
    deadline = time.time() + max_wait
    paths, got = [], set()
    while time.time() < deadline:
        paths = filelist_paths()
        got = set(_ws_rel(p) for p in paths if p)
        if got == want:
            return got, paths
        time.sleep(0.3)
    raise TestError("%s：vfts 清单集合不符（%ss 内未收敛；实际 %d / 期望 %d）\n"
                    "  多出（不应含却有）=%r\n  缺少（应含而缺）=%r\n  实际=%r"
                    % (desc, max_wait, len(got), len(want),
                       sorted(got - want), sorted(want - got), sorted(got)))


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
    """按标签子串定位项目级参数输入框：原生 setter + input 后点【保存】落库。

    参数页已改为**手动保存**（编辑只改本地待保存态，`[data-params-save-project]`
    才写 prj 库）——仅 blur 不落库（旧「失焦即落库」口径已废）。
    """
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
    r2 = ev("""(()=>{const b=document.querySelector('.settings-page [data-params-save-project]');
      if(!b)return 'no-btn';if(!b.disabled)b.click();return 'ok';})()""")
    if r2 != "ok":
        raise TestError("点击项目参数【保存】失败：%s" % (r2,))
    # 等保存完成（dirty 清除 → 按钮回 disabled），再留少量时间给 persist 写库
    end = time.time() + 6
    while time.time() < end:
        st = ev("""(()=>{const b=document.querySelector('.settings-page [data-params-save-project]');
          if(!b)return 'no-btn';return b.disabled?'saved':'dirty';})()""")
        if st == "saved":
            break
        time.sleep(0.3)
    time.sleep(0.4)


# ══════════════════════════════════════════════════════════
# G1 codegraph 插件启停 → 索引工具面（插件级门控）
# ══════════════════════════════════════════════════════════

def _plugin_enabled(pid):
    r = c.req("plugin-list", {}) or {}
    for p in (r.get("plugins") or []):
        if p.get("id") == pid:
            return bool(p.get("enabled"))
    raise TestError("plugin-list 中找不到插件 %s" % pid)


def case_g1_plugin_codegraph_gate():
    """codegraph 插件启停 → codegraph/vfts 工具面即时出现/消失（**保存即生效**，不重启）。

    口径变更（2026-10-23）：索引工具已迁入 `codegraph` 插件（manifest `mcp.hot_tools`），
    prj 键 `enable-codegraph` / `enable-vfts` **不再门控工具面**；唯一门控 = 插件启用开关
    （`plugin-set-enabled`，与插件维护页「启用」开关同源）。故断言同一实例内：
    停用插件 → 工具面移除 codegraph/vfts；重新启用 → 原样恢复。
    """
    need = set(CG_TOOLS) | {VF_TOOL}
    base = set(tools())
    miss = sorted(need - base)
    if miss:
        raise TestError("基线工具面应含 codegraph 插件全部索引工具（插件默认启用），缺：%r" % miss)
    if not _plugin_enabled("codegraph"):
        raise TestError("基线 codegraph 插件应为启用态")
    print("[G1] 基线工具面 %d 项（含 codegraph 8 + %s）" % (len(base), VF_TOOL), flush=True)

    c.req("plugin-set-enabled", {"id": "codegraph", "enabled": False})
    off = set(wait_tools(lambda s: not (need & s),
                         "停用 codegraph 插件后 codegraph/vfts 工具从工具面移除", max_wait=60))
    if _plugin_enabled("codegraph"):
        raise TestError("停用后 plugin-list.enabled 仍为 true")
    print("[G1] 停用 → 工具面 -%r（%d 项）" % (sorted(need & base), len(off)), flush=True)

    c.req("plugin-set-enabled", {"id": "codegraph", "enabled": True})
    on = set(wait_tools(lambda s: need <= s,
                        "重新启用 codegraph 插件后索引工具恢复工具面", max_wait=90))
    if not _plugin_enabled("codegraph"):
        raise TestError("启用后 plugin-list.enabled 仍为 false")
    extra = sorted(on - off)
    if not (need & set(extra)) == need:
        raise TestError("重新启用后工具面应恢复 codegraph 8 + %s，实际增量=%r" % (VF_TOOL, extra))
    print("[G1] 启用 → 工具面 +%r（未重启）" % extra, flush=True)


# ══════════════════════════════════════════════════════════
# G2 历史 prj 键 enable-codegraph / enable-vfts 不再门控工具面
# ══════════════════════════════════════════════════════════

def case_g2_legacy_enable_keys_inert():
    """prj `enable-codegraph` / `enable-vfts` 仍可读写，但**不再**控制工具面（防旧门控回归）。

    口径变更（2026-10-23）：工具归属 `codegraph` 插件（启用即注册全部 hot_tools）→ 旧实现
    「关 → 8 个 codegraph 工具 / vfts_query 消失」已作废；唯一门控见 G1。故断言：写 false 后
    工具面**不减少**（留出反应窗口排除异步摘除），且 prj 回读一致。
    """
    need = set(CG_TOOLS) | {VF_TOOL}
    if not need <= set(tools()):
        raise TestError("前置：工具面应含 codegraph/vfts 工具：%r" % sorted(need - set(tools())))
    for key in ("enable-codegraph", "enable-vfts"):
        prj_save(key, "false")
        time.sleep(1.5)  # 反应窗口：旧实现会在 data-prj-config-refresh 后异步摘除工具
        cur = set(tools())
        loss = sorted(need - cur)
        if loss:
            raise TestError("%s=false 不应再门控工具面，实际消失=%r" % (key, loss))
        got = prj().get(key)
        if got != "false":
            raise TestError("%s 回读=%r，期望 'false'" % (key, got))
        prj_save(key, "true")
        if prj().get(key) != "true":
            raise TestError("%s 回写 true 失败（回读=%r）" % (key, prj().get(key)))
    print("[G2] enable-codegraph / enable-vfts 写入 false 后工具面保持 %d 项（"
          "历史键不再门控；已回写 true）" % len(need), flush=True)


# ══════════════════════════════════════════════════════════
# G3 codegraph.exts / codegraph.skip-dirs → 索引范围终效
# ══════════════════════════════════════════════════════════

IDX_DB = os.path.join(WS, ".chonkpilot", "codegraph", "index.db")

# 夹具哨兵符号 → 相对路径（每个夹具文件唯一哨兵函数，见 G1 常量区）——
# 符号 ↔ 文件一一映射，符号集合即文件集合（I-78 同款思路）。
SENT_FILES = {
    "Alpha": "gate_src/alpha.go",
    "Delta": "gate_skip/delta.go",
    "SG_IGN_KEEP": "gate_ign/keep.go",
    "SG_IGN_GEN": "gate_ign/gen.gen.go",
    "SG_IGN_SPECIAL": "gate_ign/special.gen.go",
    "SG_IGN_SUB": "gate_ign/sub/u.go",
}


def _idx_files():
    """落盘索引命中文件集合（相对路径，'/' 分隔）。

    CODEGRAPH-INLINE（2026-11-02）：引擎已库内联进 codegraph 插件进程，发行根**不再投放**
    `mcps/codegraph/` 引擎 exe → 原 `-dump` 只读自检面退场。改经**查询工具面**探测：
    对夹具全部哨兵符号逐一 `tool.call codegraph_get_symbol_info`（file+name 精确查），
    命中即视为该文件在索引中。`{status:"not_found"}` 或未就绪（pending/error）均不算命中
    （未就绪由上层 `_want_idx_retry` 轮询自然处理）。
    """
    out = set()
    for sent, rel in SENT_FILES.items():
        r = plugin_api("tool.call", {"name": "codegraph_get_symbol_info",
                                     "args": {"workdir": WS, "file": rel, "name": sent}})
        if isinstance(r, dict) and r.get("status") is None and r.get("matches"):
            out.add(rel)
    return out


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


def _want_idx_retry(expected, desc, settle_wait=60, retries=1):
    """`_want_idx` 的**有界等待版** + 一次重建重试。

    为什么不能只看插件状态：插件 `engine-status` 的 `indexedFiles` 可能仍是**上一次**索引的数值
    （配置 apply 会先回写 skipDirs，而新一轮重建尚未落盘时，两者组合可"巧合满足"等待条件），
    批跑负载下就会读到旧 `index.db`（2026-10-26 G8 误红定位）。故这里直接轮询**落盘产物**
    （唯一权威），超时后重建一次再等；仍不符即真实回归（断言强度不变）。
    """
    want = set(expected)
    deadline = time.time() + settle_wait
    got = set()
    while time.time() < deadline:
        got = _idx_files()
        if got == want:
            return got
        time.sleep(0.5)
    if retries <= 0:
        raise TestError("%s：落盘索引命中集合不符（实际 %d / 期望 %d）\n"
                        "  多出（不应含却有）=%r\n  缺少（应含而缺）=%r"
                        % (desc, len(got), len(want), sorted(got - want), sorted(want - got)))
    print("[G8] 落盘命中集合未收敛 → 再重建一次（规则应用可能晚于首次索引）", flush=True)
    cg_index()
    return _want_idx_retry(expected, desc, settle_wait, retries - 1)


def _converge_index(expected, desc, status_pred=None, settle_wait=60, retries=1):
    """有界轮询「status 就绪 + 同一快照的 indexedFiles 计数 + 落盘命中集合」三者同时收敛。

    为什么必须三者同时、且不能单次采样：`engine-status` 的 `state=ready` 可能仍是**上一轮**的
    快照（重建/配置 apply 是异步的——触发后 status 仍报旧 `indexedFiles`，旧 `index.db` 也还在），
    批跑负载下单次采样会读到旧值（2026-11-07 全量 88 套定位：G3 基线 5/6、G4 重建 6/5）。
    因此以**落盘命中集合**（唯一权威）+ 与之同快照的 `indexedFiles` 计数为联合条件轮询；
    超时后允许一次重建重试（终态断言不变，不放宽），仍不符才判失败并打印现场快照。
    """
    want = set(expected)
    deadline = time.time() + settle_wait
    st, got, qerr = {}, set(), ""
    while time.time() < deadline:
        st = status_obj()
        try:
            got = _idx_files()
            qerr = ""
        except TestError as e:   # 插件动作瞬时失败 → 记现场，下一轮继续
            got, qerr = set(), str(e)[:200]
        n = st.get("indexedFiles")
        try:
            n = int(n) if n is not None else -1
        except (TypeError, ValueError):
            n = -1
        ok = (st.get("state") == "ready" and n == len(want) and got == want
              and (status_pred is None or bool(status_pred(st))))
        if ok:
            return st, got
        time.sleep(0.25)
    if retries > 0:
        print("[G3/G4] %s：%ss 内未收敛 → 再重建一次（异步索引/配置应用可能滞后）" % (desc, settle_wait),
              flush=True)
        cg_index()
        return _converge_index(expected, desc, status_pred, settle_wait, retries - 1)
    raise TestError("%s：索引未收敛（等待 %ss）\n  status=%r\n"
                    "  落盘实际=%d / 期望=%d\n  多出（不应含却有）=%r\n  缺少（应含而缺）=%r\n"
                    "  查询面错误=%r"
                    % (desc, settle_wait, st, len(got), len(want),
                       sorted(got - want), sorted(want - got), qerr))


def wait_applied(pred, desc, max_wait=30):
    """等「prj 配置已被插件应用到引擎」（插件动作 `engine-status` 的状态收敛）。

    超时**不判失败**（打印提示后继续）：状态上报可能晚于索引完成，最终判据仍以落盘
    命中集合（`_want_idx_retry`）为准。
    """
    try:
        return wait_status(pred, desc, max_wait=max_wait)
    except TestError:
        print("[G8] 提示：%s 未在 %ss 内回写 status（继续触发索引）" % (desc, max_wait), flush=True)
        return status_obj()


def case_g3_index_scope():
    """写 exts/skip-dirs（插件配置动作面 `save` = 配置页「保存并应用」）→ 断言索引范围终效。

    口径（2026-10-26，D-71）：配置源 = prj `plugins.codegraph.*`；索引触发 = 「立即索引」
    （`tool.call codegraph_initialize`，与配置页按钮同一条）。

    可重复判据（三重，均来自产品面/落盘产物）：
      ① prj 回读：`plugins.codegraph.exts` / `plugins.codegraph.skip-dirs` 等于写入值；
      ② 插件动作 `engine-status` 现取状态：`exts` = 生效扩展名集合、`skipDirs` = 用户追加集、
         `indexedFiles` 命中文件数（与③期望集合规模一致）；
      ③ 落盘 bbolt 索引库 `<ws>/.chonkpilot/codegraph/index.db` 的**命中文件集合**
         （经查询工具 `codegraph_get_symbol_info` 对夹具哨兵符号探测读回，见 `_idx_files`）
         → 与**夹具推导的显式期望集合字面全等**（一个不多一个不少，差集在失败信息里打印）。

    期望集合口径（夹具推导，见上方 `_G3_AB` / `_G3_B` 常量）：
      基线/A：6 个 .go 全收（gate_src/alpha.go + gate_skip/delta.go +
      gate_ign/{keep,gen.gen,special.gen,sub/u}.go；note.md/.txt 非受支持语言；stack 关 → .gitignore 不读）；
      步骤 B：追加 skip-dirs=gate_skip → gate_skip/ 目录剪枝 → gate_skip/delta.go 出（6 → 5）。
    """
    if prj().get("plugins.codegraph.enabled") != "true":
        cg_save({"enabled": "true"})                   # 独立 work-dir：由上用例关闭后重新启用
        cg_index()
    # 基线：exts=默认全语言 / skip-dirs 空 / stack 关 → 6 个 .go 全收。
    # 不能只看 `wait_status(state=ready)`（可能读到上一轮快照）+ 单次 `_want_idx`：两者都要等
    # 「status 计数 == 6 == 落盘命中集合」同时成立。
    base, _base_disk = _converge_index(
        _G3_AB, "G3 基线（exts=默认全语言，skip-dirs 空，stack 关）")
    print("[G3] 基线 status: state=ready exts=%r indexedFiles=%r；落盘索引命中集合字面全等 %d 个"
          % (base.get("exts"), base.get("indexedFiles"), len(_G3_AB)), flush=True)

    # ── 步骤 A：exts=".go"（+清空 skip-dirs）──
    cg_save({"exts": ".go", "skip-dirs": "", "stack-gitignore": "false"})
    cg_index()
    a, _a_disk = _converge_index(
        _G3_AB, "G3 A（exts=.go，skip-dirs 空，stack 关）",
        status_pred=lambda o: o.get("exts") == [".go"])
    if prj().get("plugins.codegraph.exts") != ".go" or \
       prj().get("plugins.codegraph.skip-dirs") not in (None, ""):
        raise TestError("A：prj 回读不符（exts=%r skip-dirs=%r）"
                        % (prj().get("plugins.codegraph.exts"), prj().get("plugins.codegraph.skip-dirs")))
    print("[G3] A exts=.go → status.exts=%r indexedFiles=%r；"
          "落盘索引命中集合字面全等 %d 个（含 gate_src+gate_skip）"
          % (a.get("exts"), a.get("indexedFiles"), len(_G3_AB)), flush=True)

    # ── 步骤 B：追加 skip-dirs="gate_skip" ──
    cg_save({"exts": ".go", "skip-dirs": "gate_skip"})
    cg_index()
    b, _b_disk = _converge_index(
        _G3_B, "G3 B（skip-dirs=gate_skip）",
        status_pred=lambda o: "gate_skip" in (o.get("skipDirs") or []))
    if prj().get("plugins.codegraph.skip-dirs") != "gate_skip":
        raise TestError("B：prj 回读 skip-dirs=%r" % (prj().get("plugins.codegraph.skip-dirs"),))
    print("[G3] B skip-dirs=gate_skip → status.skipDirs=%r indexedFiles=%r；"
          "落盘索引命中集合字面全等（gate_skip/delta.go 已剔除）"
          % (b.get("skipDirs"), b.get("indexedFiles")), flush=True)


# ══════════════════════════════════════════════════════════
# G4 重建（retry）/ 清除索引 → 状态与落盘产物
# ══════════════════════════════════════════════════════════

def case_g4_action_rebuild_retry_clear():
    """重建 → 回到 `ready` 且命中集合不变；清除 → 回「未初始化」（`indexedFiles=0`、index.db 删除）。

    口径（2026-10-26，D-71）：旧 `codegraph.action` 信号键（`CodegraphConfig.vue emitAction`）
    已随主仓页面下线 —— 配置页改为插件自持 iframe，动作面 =：
      * 重建 / 重试 → `tool.call codegraph_initialize`（配置页「立即索引」，全量重建即重试）；
      * 清除索引 → 插件动作 `clear`（配置页「清除索引」，等价引擎 `codegraph_configure{mode:clear}`）。
    故本用例改断言**终态 + 落盘产物**（原 `indexing` 瞬态阶段的广播流断言随旧信号键一并退场）。
    """
    if status_obj().get("state") != "ready":
        cg_save({"enabled": "true"})
        cg_index()
        wait_status(lambda o: o.get("state") == "ready", "前置：codegraph 就绪")

    # 当前配置 = G3 B（exts=.go + skip-dirs=gate_skip）→ 重建后 indexedFiles 应仍为 |_G3_B|
    # CODEGRAPH-IDXDB-1（2026-10-28 已修）：`status.indexedFiles` 与**落盘**索引库由同一索引快照
    # 派生 → 此处**交叉硬断言两者相等**（此前因双 Workspace 缺陷不做交叉断言）。
    for action, why in (("重建", "全量重建（= 配置页「立即索引」）"),
                        ("重试", "失败后重试（同全量重建，引擎无独立 retry 面）")):
        cg_index()
        # 关键：不能 `wait_status(state=ready)` 单次采样——触发重建后 status 仍是**上一轮**的
        # ready 快照（2026-11-07 批跑实测读到旧 indexedFiles=6，期望 5）。改为等「status 计数
        # == 落盘命中集合 == _G3_B」同时收敛，超时带现场快照。
        st, disk = _converge_index(_G3_B, "%s后索引（配置未变，期望 %d）" % (action, len(_G3_B)))
        if st.get("indexedFiles") != len(_G3_B):
            raise TestError("%s：indexedFiles 应=%d（配置未变），实际=%r"
                            % (action, len(_G3_B), st.get("indexedFiles")))
        if st.get("indexedFiles") != len(disk):
            raise TestError("%s：status.indexedFiles=%r ≠ 落盘 files=%d（须由同一快照派生）"
                            % (action, st.get("indexedFiles"), len(disk)))
        print("[G4] %s（%s）→ state=%r indexedFiles=%r（= 落盘 %d）"
              % (action, why, st.get("state"), st.get("indexedFiles"), len(disk)), flush=True)

    # ── 清除索引（插件动作 clear）──
    # 终态判据 = 落盘产物（index.db 删除 + 命中集合为空）**且** status 回未初始化：
    # CODEGRAPH-IDXDB-1 ③ 已修（clear 后插件立即回写运行时状态键），故两者都要断言。
    cleared = plugin_api("clear", {"workdir": WS})
    if not (isinstance(cleared, dict) and cleared.get("ok")):
        raise TestError("clear 动作应答异常：%r" % (cleared,))
    deadline = time.time() + 60
    while time.time() < deadline and os.path.isfile(IDX_DB):
        time.sleep(0.3)
    if os.path.isfile(IDX_DB):
        raise TestError("clear 后索引库 index.db 应被删除：%s" % IDX_DB)
    # 落盘空集同样有界轮询（查询面可能仍读到清理前的缓存）；clear 不重建 → 不调用 cg_index。
    clear_deadline = time.time() + 30
    got = _idx_files()
    while time.time() < clear_deadline and got != _CLEAR:
        time.sleep(0.2)
        got = _idx_files()
    if got != _CLEAR:
        raise TestError("G4 clear 后落盘索引应为空集（等待 30s），实际=%r；status=%r"
                        % (sorted(got), status_obj()))
    st = wait_status(lambda o: (not o.get("state")) and int(o.get("indexedFiles") or 0) == 0,
                     "clear 后 status 回未初始化")
    print("[G4] clear → index.db 已删除、落盘为空集；status state=%r indexedFiles=%r"
          % (st.get("state"), st.get("indexedFiles")), flush=True)


# ══════════════════════════════════════════════════════════
# G5 prj skip_dirs（配置面回读）
# ══════════════════════════════════════════════════════════

def case_g5_prj_skip_dirs_config_face():
    """prj `skip_dirs`：GUI「参数 → 项目」写入 → prj 回读一致（含重开页面输入框回显）。

    B（终效）**不做**——依据（代码级）：
      ① find 系列动作已由单工具 `file_find` 取代，跳过目录改为**调用级** `targets[].skipDirs`
        （契约 `file_find.tool.md` §参数）；
      ② 旧 `dsl_run`（FIND 动作入口）已随 DSL 退役删除 → prj `skip_dirs` 不再参与工具执行，
        也无其它可观测消费面（mcp-server `defaultsMap` 注入链随 file_find 旧实现一并移除）。
    故本条只覆盖配置面（写入 + 回读 + 重开页面回显），并如实标注 B 不可覆盖。
    """
    if prj().get("skip_dirs"):
        raise TestError("基线 prj skip_dirs 应为空（独立 work-dir），实际=%r" % prj().get("skip_dirs"))

    open_params_project()
    set_param_input("跳过目录", "_skipme")
    got = prj().get("skip_dirs")
    if got != '["_skipme"]':
        raise TestError("A：GUI 写后 prj skip_dirs=%r，期望 '[\"_skipme\"]'" % (got,))

    # 回读（重开页面 → 输入框回显 = 从 prj 库重新加载）
    open_params_project()
    rows = {r["label"]: r["value"] for r in (param_rows() or [])}
    if rows.get("跳过目录") != "_skipme":
        raise TestError("A：重开页面后回显不符：%r" % (rows,))
    # 清理（后续用例不再依赖该键）
    prj_save("skip_dirs", "")
    print("[G5] skip_dirs=%r（prj 回读 + 重开页面回显一致；B 不可覆盖，依据见 docstring）"
          % (got,), flush=True)


# ══════════════════════════════════════════════════════════
# G6 timeout_sec / max_concurrency（A 必做；B 不可稳定观测，见 docstring）
# ══════════════════════════════════════════════════════════

def case_g6_exec_params_readback():
    """`timeout_sec` / `max_concurrency`：GUI 改后 **prj 回读一致**（含重开页面输入框回显）。

    B（效果）**不做**——依据（代码级，非"懒得做"）：
      ① `timeout_sec` → `mcp-server Config.execTimeout`，但 `executor.go:106-109`
         **契约级 `timeout` 优先**（`if td.Timeout > 0 { timeout = td.Timeout }`），而随包发布的
         5 个内置契约（core: filesys_run/script_run/web_fetch；desktop_run/browser_run）全部声明了
         `timeout` → prj `timeout_sec` 对**内置工具
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

    链路（2026-10-26，D-71）：插件配置动作面 `save`（= 配置页「保存并应用」，写 prj
    `plugins.codegraph.*`）→ 插件 `data-prj-config-refresh` → 引擎 `codegraph_configure`
    → 「立即索引」`codegraph_initialize` → 引擎 `excludeOptions` → `ignore.WalkDir`
    （插件仓 `shared/ignore/ignore.go`，逐级读各级 .gitignore）。

    判据 A（数据面回读）：`prj['plugins.codegraph.stack-gitignore']=="true"`、
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
    if prj().get("plugins.codegraph.enabled") != "true":
        cg_save({"enabled": "true"})

    cg_save({"exts": ".go", "skip-dirs": "", "stack-gitignore": "true"})
    cg_index()

    st, _disk = _converge_index(
        _G7, "G7 勾选叠加 gitignore（.gitignore 文件级排除 + '!' 反选 + 目录剪枝）",
        status_pred=lambda o: o.get("stackGitignore") is True and not (o.get("skipDirs") or []))
    if prj().get("plugins.codegraph.stack-gitignore") != "true":
        raise TestError("A：prj stack-gitignore=%r，期望 'true'"
                        % (prj().get("plugins.codegraph.stack-gitignore"),))
    if (st.get("skipDirs") or []):
        raise TestError("A：勾选 stack-gitignore 时用户规则应为空，实际 skipDirs=%r"
                        % (st.get("skipDirs"),))
    if st.get("indexedFiles") != len(_G7):
        raise TestError("B：勾选后 indexedFiles 应=%d，实际=%r"
                        % (len(_G7), st.get("indexedFiles")))
    print("[G7] 勾选叠加 gitignore → A: stack-gitignore=%r stackGitignore=%r skipDirs=%r；"
          "B: 落盘索引命中集合字面全等（含 keep/special、不含 gen.gen/sub/u），indexedFiles=%r"
          % (prj().get("plugins.codegraph.stack-gitignore"), st.get("stackGitignore"),
             st.get("skipDirs"), st.get("indexedFiles")), flush=True)

    # 收尾：取消勾选 → stack-gitignore=false（A 回落）
    cg_save({"stack-gitignore": "false"})
    cg_index()
    st2 = wait_status(lambda o: o.get("state") == "ready" and not o.get("stackGitignore"),
                      "取消勾选后重建完成（stackGitignore 回落）")
    if prj().get("plugins.codegraph.stack-gitignore") != "false":
        raise TestError("收尾：stack-gitignore=%r，期望 'false'"
                        % (prj().get("plugins.codegraph.stack-gitignore"),))
    print("[G7] 收尾 取消勾选 → A: stack-gitignore=%r stackGitignore=%r（回落）"
          % (prj().get("plugins.codegraph.stack-gitignore"), st2.get("stackGitignore")), flush=True)


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
    if prj().get("plugins.codegraph.enabled") != "true":
        cg_save({"enabled": "true"})
    if prj().get("plugins.codegraph.stack-gitignore") not in (None, "", "false"):
        raise TestError("前置：stack-gitignore 应为关（G7 收尾回落后），实际=%r"
                        % (prj().get("plugins.codegraph.stack-gitignore"),))

    # ── 步骤 1：用户规则 "*.gen.go"（仅用户规则，stack 关）──
    cg_save({"exts": ".go", "skip-dirs": "*.gen.go", "stack-gitignore": "false"})
    # 先等**插件已应用**新规则（status 由插件 configure 后回写）再触发索引：
    # 批跑负载下 `save` 回执 → `data-prj-config-refresh` → 插件 apply 是异步的，
    # 若此刻立刻「立即索引」，引擎会按**旧规则**建库 → 落盘命中集合断言误红（2026-10-26 定位）。
    wait_applied(lambda o: o.get("skipDirs") == ["*.gen.go"], "用户规则 *.gen.go 已应用")
    cg_index()
    s1 = wait_applied(lambda o: o.get("state") == "ready"
                      and o.get("skipDirs") == ["*.gen.go"]
                      and o.get("indexedFiles") == len(_G8_S1),
                      "用户规则 *.gen.go 生效（索引完成）", max_wait=60)
    if prj().get("plugins.codegraph.skip-dirs") != "*.gen.go":
        raise TestError("步骤1 A：prj skip-dirs=%r" % (prj().get("plugins.codegraph.skip-dirs"),))
    _want_idx_retry(_G8_S1, "G8 步骤1（用户规则 *.gen.go，stack 关）")
    print("[G8] 步骤1 skip-dirs=%r → A: status.skipDirs=%r（原样未折名）；"
          "B: 命中集合字面全等（不含两个 gen、含 keep/sub/u）"
          % (prj().get("plugins.codegraph.skip-dirs"), s1.get("skipDirs")), flush=True)

    # ── 步骤 2："*.gen.go, !special.gen.go"（保序含 '!'）──
    cg_save({"skip-dirs": "*.gen.go, !special.gen.go"})
    wait_applied(lambda o: o.get("skipDirs") == ["*.gen.go", "!special.gen.go"],
                 "用户规则含 '!' 反选已应用")
    cg_index()
    s2 = wait_applied(lambda o: o.get("state") == "ready"
                      and o.get("skipDirs") == ["*.gen.go", "!special.gen.go"]
                      and o.get("indexedFiles") == len(_G8_S2),
                      "用户规则含 '!' 反选生效（索引完成）", max_wait=60)
    if prj().get("plugins.codegraph.skip-dirs") != "*.gen.go, !special.gen.go":
        raise TestError("步骤2 A：prj skip-dirs=%r" % (prj().get("plugins.codegraph.skip-dirs"),))
    _want_idx_retry(_G8_S2, "G8 步骤2（用户规则 *.gen.go, !special.gen.go，stack 关）")
    print("[G8] 步骤2 skip-dirs=%r → A: status.skipDirs=%r（保序含 '!'）；"
          "B: 命中集合字面全等（special.gen.go 反选回来、gen.gen.go 仍排除）"
          % (prj().get("plugins.codegraph.skip-dirs"), s2.get("skipDirs")), flush=True)

    # ── 步骤 3：勾选 stack + 用户规则 "!gate_ign/sub/"（用户规则压过 .gitignore 目录排除）──
    cg_save({"skip-dirs": "!gate_ign/sub/", "stack-gitignore": "true"})
    wait_applied(lambda o: o.get("stackGitignore") is True
                 and o.get("skipDirs") == ["!gate_ign/sub/"],
                 "用户规则 !gate_ign/sub/ + stack 已应用")
    cg_index()
    s3 = wait_applied(lambda o: o.get("state") == "ready" and o.get("stackGitignore") is True
                      and o.get("skipDirs") == ["!gate_ign/sub/"]
                      and o.get("indexedFiles") == len(_G8_S3),
                      "用户规则 !gate_ign/sub/ 压过 .gitignore 目录排除（索引完成）", max_wait=60)
    _want_idx_retry(_G8_S3, "G8 步骤3（stack + 用户规则 !gate_ign/sub/）")
    print("[G8] 步骤3 勾选 stack + 用户规则 %r → A: skipDirs=%r stackGitignore=%r；"
          "B: 命中集合字面全等（sub/u.go 重新出现；gen.gen.go 仍排除）"
          % (prj().get("plugins.codegraph.skip-dirs"), s3.get("skipDirs"), s3.get("stackGitignore")),
          flush=True)

    # 收尾：清空规则 + 关闭 stack
    cg_save({"skip-dirs": "", "stack-gitignore": "false"})
    cg_index()
    wait_status(lambda o: o.get("state") == "ready" and not o.get("skipDirs")
                and not o.get("stackGitignore"), "G8 收尾（规则清空 + stack 关）")
    if prj().get("plugins.codegraph.skip-dirs") not in (None, "") or \
       prj().get("plugins.codegraph.stack-gitignore") != "false":
        raise TestError("G8 收尾：skip-dirs=%r stack-gitignore=%r"
                        % (prj().get("plugins.codegraph.skip-dirs"),
                           prj().get("plugins.codegraph.stack-gitignore")))


# ══════════════════════════════════════════════════════════
# G9 vfts 侧同语义（引擎遍历 + 清单扫描共用同一匹配器）
# ══════════════════════════════════════════════════════════

def case_g9_vfts_gitignore_semantics():
    """vfts 侧同语义：引擎遍历 + 清单扫描共用插件仓 `shared/ignore` → 与 codegraph 行为一致。

    证据选择（**清单面**，非计数退化）：vfts 的文件清单是**插件自持文件**
    `<workdir>/.chonkpilot/vfts/file_list.json`（`shared/ignore` 消费方之一 =
    `plugin-vfts/manifest.go scanFiles` 走 `ignore.WalkDir`），逐文件读回 `path` ——
    可逐文件断言「谁该在 / 谁不该在」，比 `engine-status` 的 `indexedFiles` 计数强。

    链路（2026-10-26，D-71）：插件配置动作面 `save`（写 prj `plugins.codegraph.vfts.*`）
    → `data-prj-config-refresh` → 插件 `apply`（`vfts_configure`）→ 「立即索引」`vfts_index`
    （`server/index.go collectFiles` 走 `ignore.WalkDir`）+ 插件 `rebuildManifest`
    （`manifest.go` 走同一 `ignore.WalkDir`）。

    判据 A：`prj['plugins.codegraph.vfts.exts']==".go"`、
            `prj['plugins.codegraph.vfts.stack-gitignore']=="true"`、
            `prj['plugins.codegraph.vfts.skip-dirs']` 空；`engine-status` 的 `stackGitignore==True`。
    判据 B：`file_list.json` 文件集合与**夹具 + 配置推导的显式期望集合**（上方 `_G9` 常量）
            **字面全等**（一个不多一个不少；多出/缺少/实际三行差集打印在失败信息里）：
            4 个 .go = gate_src/alpha.go + gate_skip/delta.go + gate_ign/keep.go +
            gate_ign/special.gen.go；不含 gate_ign/gen.gen.go（`*.gen.go` 文件级排除）与
            gate_ign/sub/u.go（`gate_ign/sub/` 目录剪枝，其后代 `!` 无效）。
            `engine-status` 的 `indexedFiles==len(_G9)` 与集合规模一致。
    收尾：清空规则 + 取消勾选 + 关闭 vfts（`plugins.codegraph.vfts.enabled=false`）。
    """
    # 前置：开启 vfts + 写 exts=".go" / 排除规则清空 / 叠加 gitignore
    cg_save({"vfts.enabled": "true", "vfts.exts": ".go", "vfts.skip-dirs": "",
             "vfts.stack-gitignore": "true"})
    if prj().get("plugins.codegraph.vfts.enabled") != "true":
        raise TestError("前置：plugins.codegraph.vfts.enabled=%r，期望 'true'"
                        % (prj().get("plugins.codegraph.vfts.enabled"),))
    vfts_index_retry()

    a = wait_vfts_ready(lambda o: o.get("state") == "ready" and o.get("stackGitignore") is True
                        and o.get("indexedFiles") == len(_G9),
                        "vfts 叠加 gitignore 重建完成（ready / indexedFiles=%d）" % len(_G9))
    if prj().get("plugins.codegraph.vfts.exts") != ".go":
        raise TestError("A：prj vfts.exts=%r" % (prj().get("plugins.codegraph.vfts.exts"),))
    if prj().get("plugins.codegraph.vfts.stack-gitignore") != "true":
        raise TestError("A：prj vfts.stack-gitignore=%r"
                        % (prj().get("plugins.codegraph.vfts.stack-gitignore"),))
    if prj().get("plugins.codegraph.vfts.skip-dirs") not in (None, ""):
        raise TestError("A：vfts.skip-dirs 应为空（用户规则空），实际=%r"
                        % (prj().get("plugins.codegraph.vfts.skip-dirs"),))

    # B：清单逐文件集合字面全等（清单在引擎索引后由插件**异步重建** → 轮询至收敛，
    #    差集（多出/缺少/实际）打印在失败信息里；期望集合由夹具 + vfts 配置推导，见 `_G9`）
    got, _paths = _want_filelist(_G9, "G9 vfts（exts=.go + stack-gitignore=true + 用户规则空）")
    print("[G9] vfts 勾选叠加 gitignore → A: exts=%r stackGitignore=%r skipDirs=%r；"
          "B: file_list 命中集合字面全等 %d 个（含 keep/special、不含 gen.gen/sub/u，"
          "与 codegraph 同匹配器）"
          % (prj().get("plugins.codegraph.vfts.exts"), a.get("stackGitignore"),
             a.get("skipDirs"), len(got)), flush=True)

    # 收尾：清空规则 + 取消勾选 + 关闭 vfts
    cg_save({"vfts.skip-dirs": "", "vfts.stack-gitignore": "false", "vfts.enabled": "false"})
    if prj().get("plugins.codegraph.vfts.enabled") != "false":
        raise TestError("G9 收尾：vfts.enabled=%r，期望 'false'"
                        % (prj().get("plugins.codegraph.vfts.enabled"),))
    print("[G9] 收尾 vfts.enabled=%r stack-gitignore=%r（已回落）"
          % (prj().get("plugins.codegraph.vfts.enabled"),
             prj().get("plugins.codegraph.vfts.stack-gitignore")), flush=True)


# ══════════════════════════════════════════════════════════
# main
# ══════════════════════════════════════════════════════════

def _plugin_status(pid):
    """插件台账单插件 status（plugin-list 61 §2.3）。"""
    pl = c.req("plugin-list", {}) or {}
    plugins = ((pl.get("data") or {}).get("plugins")
               if isinstance(pl.get("data"), dict) else pl.get("plugins")) or []
    p = next((x for x in plugins if x.get("id") == pid), None)
    return (p or {}).get("status") or "absent"


def wait_plugins_running(ids, max_wait=420):
    """等插件台账转 running 后再进用例（G1/G2 工具面与 G3/G4 插件动作都依赖就绪）。

    2026-11-04 首跑定位：codegraph 引擎（CODEGRAPH-INLINE，CGO + bbolt）冷启动
    初始化可达数分钟，此前用例在插件 `starting` 阶段就断言工具面/插件动作 → 必失败。
    注：vfts 引擎**内联在 codegraph 插件进程**（配置键 `plugins.codegraph.vfts.*`，
    `vfts_query` 工具亦由 codegraph 插件注册）→ 台账无独立 vfts 插件，只需等 codegraph。
    """
    t0 = time.time()
    pending = list(ids)
    while pending and time.time() - t0 < max_wait:
        pending = [p for p in pending if _plugin_status(p) != "running"]
        if not pending:
            break
        time.sleep(3)
    if pending:
        raise TestError("插件 %r 在 %ds 内未就绪（statuses=%s）"
                        % (pending, max_wait, {p: _plugin_status(p) for p in ids}))
    print("[boot] 插件就绪：%s（等待 %.0fs）" % (ids, time.time() - t0), flush=True)


def main():
    c.wait_ready()
    wait_plugins_running(["codegraph"])
    c.console(clear=True)
    total = 0
    ok = 0
    cases = [
        ("G1 codegraph 插件启停 → 工具面出现/消失 codegraph 8 + vfts_query（保存即生效）",
         case_g1_plugin_codegraph_gate),
        ("G2 enable-codegraph/enable-vfts 历史键不再门控工具面",
         case_g2_legacy_enable_keys_inert),
        ("G3 plugins.codegraph.exts/skip-dirs → 索引范围终效（status + bbolt 索引命中集合）",
         case_g3_index_scope),
        ("G4 重建/重试→ready；清除→未初始化（插件动作面 tool.call/clear）",
         case_g4_action_rebuild_retry_clear),
        ("G5 prj skip_dirs → 配置面回读 + 重开页面回显（B 不可覆盖）",
         case_g5_prj_skip_dirs_config_face),
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

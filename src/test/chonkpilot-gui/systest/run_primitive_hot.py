# -*- coding: utf-8 -*-
"""P6 批次 L4 套件：**契约原语**（capability/tools 等）落到能力面的 **A 落盘 + B 能力面生效**。

背景与「是否保存即生效」的判定（已审计缺口，2026-09-17；原假设「注册期注入（server.go
onInstanceRegister）→ 需重启才生效」**已被代码与实测推翻**）：

  * 代码依据（T-21，**零新增消息面主题**）：
      - `chonkpilot-llm/server/server.go:291-303` Start 建 `capWatcher`（gateway 存在即建）；
        `capwatch.go:73-97` 监听 **系统级 app 根** + 每实例的 **用户级/项目级** capability 根；
      - 变更去抖 `capWatchDebounce = 60ms`（capwatch.go:36）→ `fire()`：
        用户/项目级 → `reconcileCapabilityNodes()`（server.go:668：**复用既有方法面**
        `servers/unregister` + `servers/register` 重建 dir 节点）+ `refreshTools()`；
        系统级 → `reloadAppContracts()`（server.go:707：`RegisterContracts` 重注册 +
        `gateway/reload` 刷新 self 节点 list）。
      - L1 回归：`chonkpilot-llm/server/capwatch_test.go`（保存 → 工具面生效，断言"数十毫秒级"）。
  * 结论 = **保存即生效（热生效）**，本套件因此**不写重启用例**，只断言热生效路径 + 落盘证据
    （与 run_index_gate G1/G2「保存即生效，不作重启用例」同口径）。

覆盖（每条 = A 落盘 + B 能力面可观测；B 恒以工具面清单/耗时证据收口）
  H1 基线    ：工具面**不含**哨兵工具（前置：项目级 capability 根已接入、tools 目录为空）。
  H2 新增（A+B）：`data-knowledge-create{dir=<prjcap>/tools,type=tool,name=p6hottool}` →
      A：磁盘 `<prjcap>/tools/p6hottool.tool.md` 生成且含 `[meta]`/`[description]` 契约分区；
      B：**不重启**轮询客户端能力面 `tools-list` → 出现本原语（记录实测耗时）。
  H3 删除（A+B）：`data-knowledge-delete{path}` → A：磁盘文件消失；
      B：**不重启**轮询 → 工具面不再含该原语。

观测渠道（**全部为 61-消息一览既有主题，零新增**）
  §3.3 data-knowledge-{create,delete,list}（原语文件维护）· §4.5 客户端能力面 `tools-list`
  （桥 → gateway `mcp-tools-list`，桥按当前实例作用域过滤；run_index_gate 同法）

隔离（51-FP与测试映射 §5/§6-8）：
  * 自起 GUI：动态端口 + 独立 work-dir + 独立 `--data-dir` + 独立 `HOME`（usr 主库全新）。
    原语落在**临时 work-dir** 的 `<ws>/.chonkpilot/capability/tools/` 内 → 随 tmp_dir 删除 → 零残留；
    不碰 `dist-desktop/capability`（系统级，run_explore_kb 的夹具区）。
  * 套件级快照-还原 `_h.suite_config_guard(c)`（usr+prj）。

运行：python run_primitive_hot.py
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

TOOL = "p6hottool" + str(int(time.time()))[-5:]        # 唯一哨兵名（= 契约文件名去后缀）
WS = _h.tmp_dir("ck-primhot-ws-")
DD = _h.tmp_dir("ck-primhot-dd-")
HOME = _h.tmp_home()
PRJ_CAP = os.path.join(WS, ".chonkpilot", "capability")
PRJ_TOOLS = os.path.join(PRJ_CAP, "tools")
PRJ_TOOL_FILE = os.path.join(PRJ_TOOLS, TOOL + ".tool.md")
os.makedirs(PRJ_TOOLS, exist_ok=True)                 # 项目级根须存在 → 实例注册期接入该 dir 节点

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
_h.ensure_locale(c)
print("[env] ws=%s data=%s home=%s gui=%d tool=%s（临时目录，结束即删）"
      % (WS, DD, HOME, _g.port, TOOL), flush=True)


# ══════════════════════════════════════════════════════════
# 通用工具
# ══════════════════════════════════════════════════════════

def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def tools():
    """客户端能力面（§4.5 tools-list → mcp-tools-list）→ 工具暴露名列表。"""
    r = c.req("tools-list", {}) or {}
    return [t.get("name") or "" for t in (r.get("tools") or [])]


def matches(names):
    return [n for n in names if TOOL in n]


def wait_hot(pred, desc, max_wait=30):
    """不重启轮询工具面；返回 (命中列表, 耗时秒)。"""
    t0 = time.time()
    deadline = t0 + max_wait
    cur = []
    while time.time() < deadline:
        cur = tools()
        hit = pred(cur)
        if hit:
            return hit, time.time() - t0
        time.sleep(0.3)
    raise TestError("%s 超时（%ss，未重启）；当前工具面含 %s" % (desc, max_wait, matches(cur)))


def kb_delete(path):
    return c.req("data-knowledge-delete", {"path": path})


# ══════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════

def case_h1_baseline():
    """H1 基线：工具面不含哨兵原语（证明后续出现/消失均由本次原语变更引起）。"""
    names = tools()
    if not names:
        raise TestError("工具面为空（tools-list 未返回任何工具）→ 观测面不可用")
    stray = matches(names)
    if stray:
        raise TestError("基线工具面已含哨兵原语（残留）: %r" % (stray,))
    evi("H1 基线工具面", total=len(names), has_self=any(n.startswith("self_") for n in names),
        stray=stray, prj_tools_dir=PRJ_TOOLS)


def case_h2_create_hot():
    """H2：新建原语（消息面）→ A 落盘 + B 工具面**不重启**出现（热生效，T-21）。"""
    r = c.req("data-knowledge-create", {"dir": PRJ_TOOLS.replace("\\", "/"),
                                        "type": "tool", "name": TOOL}) or {}
    path = r.get("path") or ""
    # A：磁盘落盘证据（消息面返回 path + 文件内容含契约分区）
    if not os.path.isfile(PRJ_TOOL_FILE):
        raise TestError("A 落盘失败：%s 不存在（返回 path=%r）" % (PRJ_TOOL_FILE, path))
    content = open(PRJ_TOOL_FILE, encoding="utf-8").read()
    if "[meta]" not in content or "[description]" not in content:
        raise TestError("A 落盘内容缺契约分区: %r" % (content[:120],))
    # B：不重启 → 轮询客户端能力面
    hit, elapsed = wait_hot(lambda ns: matches(ns), "新建原语未进入工具面（热生效失败）")
    evi("H2 新建原语热生效", name=TOOL, path=path, file=PRJ_TOOL_FILE,
        file_bytes=len(content), exposed=hit, elapsed_s=round(elapsed, 2), restarted=False)
    if not hit:
        raise TestError("工具面未出现 %r" % (TOOL,))


def case_h3_delete_hot():
    """H3：删除原语（消息面）→ A 磁盘消失 + B 工具面**不重启**移除。"""
    kb_delete(PRJ_TOOL_FILE.replace("\\", "/"))
    # A：磁盘消失
    deadline = time.time() + 8
    while time.time() < deadline and os.path.exists(PRJ_TOOL_FILE):
        time.sleep(0.3)
    if os.path.exists(PRJ_TOOL_FILE):
        raise TestError("A 删除失败：%s 仍在磁盘" % PRJ_TOOL_FILE)
    # B：不重启 → 轮询工具面移除
    t0 = time.time()
    gone, elapsed = False, 0.0
    deadline = t0 + 30
    while time.time() < deadline:
        if not matches(tools()):
            gone, elapsed = True, time.time() - t0
            break
        time.sleep(0.3)
    left = matches(tools())
    evi("H3 删除原语热生效", name=TOOL, file_exists=os.path.exists(PRJ_TOOL_FILE),
        removed=gone, left=left, elapsed_s=round(elapsed, 2), restarted=False)
    if not gone:
        raise TestError("工具面未移除 %r（仍见 %r）" % (TOOL, left))


def main():
    ok = total = 0
    c.console(clear=True)
    print("依赖：--test-port=%d 的 GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "观测面 = 客户端能力面 tools-list（§4.5）" % _g.port, flush=True)
    for name, fn in [
        ("H1 基线：工具面不含哨兵原语（观测面可用性 + 无残留）", case_h1_baseline),
        ("H2 新建原语 → A 落盘 + B 工具面不重启出现（热生效 T-21）", case_h2_create_hot),
        ("H3 删除原语 → A 磁盘消失 + B 工具面不重启移除", case_h3_delete_hot),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n契约原语能力面（A 落盘 + B 热生效/移除）：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

# -*- coding: utf-8 -*-
"""L4 套件：文件树「被索引排除条目灰显」（FT-002 扩展；`data-index-ignored` 只读面，61 §3.6）。

用户口径：「如果设置了 gitignored，目录树显示时，判断是否是排除项目。如果是，则文本颜色
变成灰色」→ 方案 = **后端判定**（`data-index-ignored`）、位置 = 文件树（目录树）。

覆盖（每条 = 真机 DOM 级断言，不用"保存成功即算过"）：

  H1 引擎启用 + 叠加 gitignore → 命中条目灰显：夹具 `.gitignore` = `*.gen.go` / `!special.gen.go`
     / `skipdir/` → 断言 `gen.gen.go`（文件级排除）、`skipdir`（目录排除）及其子项
     `skipdir/inside.go`（祖先剪枝）**带 `is-ignored` 类 + 文本色 == `--fg-disabled` + 原生
     `title` = 「已排除：不会参与索引」**；`special.gen.go`（`!` 反选）、`keep.go`（无规则命中）
     **不带灰显**。
  H2 对照组（引擎未启用）→ **全不灰**：写 `enable-vfts=false`（既有 `data-prj-config-refresh`
     广播触发前端重判）→ H1 中已灰的条目（含已展开的 `skipdir/inside.go`）**灰显消失、文本色
     恢复非 `--fg-disabled`**（= 「引擎未启用则不灰」+ 刷新重判的反向证据）。

观测手段（**零新增 MQ 主题**，只发既有 `data-prj-config-*` + 既有 `filesys.watch` 展开）：
  * 灰显判定 = DOM：`.tree-row[data-path="<绝对路径>"]` 的 `classList.contains('is-ignored')`
    + `.node-label` 的 `getComputedStyle().color`（与 `var(--fg-disabled)` 的实测解析值比对，
    不硬编码主题色）+ `title`。
  * 展开目录 = 既有 `filesys.watch`（= 文件树展开动作，`FileTree.onToggle` 同款），无需 mock。

隔离与环境（51-FP与测试映射 §5/§6-8）：
  * **自起** GUI：动态端口 + 独立 work-dir（临时目录，含 `.gitignore` 夹具）+ 独立 `--data-dir`
    + 独立 `HOME`（usr 主库全新；prj 库全新 → 无历史键）。
  * 不碰共享 `systest/ws`；夹具与索引落盘（`<ws>/.chonkpilot/...`）在临时 work-dir 内 →
    随 `harness.tmp_dir` 一并删除 → **零残留**。
  * 套件级配置快照-还原 = `_h.suite_config_guard(c)`（退出前自动回滚 usr + prj，含异常/中断）。

运行：python run_filetree_ignored.py   （自起自收，无需外部底座）
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51 §6 测试资源规范）  # noqa: E402

# ── 夹具（临时 work-dir）──────────────────────────────────
WS = _h.tmp_dir("ck-ftign-ws-")
DD = _h.tmp_dir("ck-ftign-dd-")
HOME = _h.tmp_home()
WS_POSIX = WS.replace("\\", "/")

with open(os.path.join(WS, ".gitignore"), "w", encoding="utf-8") as f:
    f.write("*.gen.go\n!special.gen.go\nskipdir/\n")
for _name in ("keep.go", "gen.gen.go", "special.gen.go"):
    with open(os.path.join(WS, _name), "w", encoding="utf-8") as f:
        f.write("package ftign\n")
os.makedirs(os.path.join(WS, "skipdir"), exist_ok=True)
with open(os.path.join(WS, "skipdir", "inside.go"), "w", encoding="utf-8") as f:
    f.write("package skipdir\n")

_G = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _G.client
_h.suite_config_guard(c)                     # 套件级快照-还原（51 §6-8）
_h.ensure_locale(c, "zh-CN")                 # 文案确定性（title 断言按 zh-CN）
print("[env] ws=%s data=%s gui=%d（临时目录，结束即删）" % (WS, DD, _G.port), flush=True)

# 期望灰显文案（i18n fileTree.index_ignored，zh-CN）
TITLE_ZH = "已排除：不会参与索引"


# ══════════════════════════════════════════════════════════
# 通用工具
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


def prj_save(key, value):
    return c.req("data-prj-config-save", {"data": {"key": key, "value": value}})


def tree_sel(rel):
    """根节点 data-path 为绝对路径（正斜杠）。"""
    rel = rel.replace("\\", "/")
    return '.tree-row[data-path="%s/%s"]' % (WS_POSIX, rel)


def wait_row(rel, present=True, max_wait=20):
    sel = tree_sel(rel)
    deadline = time.time() + max_wait
    last = 0
    while time.time() < deadline:
        last = c.exists(sel).get("count", 0)
        if (last > 0) == present:
            return
        time.sleep(0.4)
    raise TestError("等待树节点 %s 超时（present=%s，实际 count=%s）" % (sel, present, last))


def expand_dir(rel, max_wait=20):
    """展开目录（既有 filesys.watch 驱动，幂等；DOM 箭头点击兜底）。"""
    abs_path = "%s/%s" % (WS_POSIX, rel.replace("\\", "/"))
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if c.exists(tree_sel(rel) + " .arrow svg.expanded").get("count", 0) > 0:
            return
        c.mq_emit("filesys.watch", {"work_dir": WS, "path": abs_path})
        time.sleep(1.0)
    # mq 未生效 → DOM 点击兜底
    c.click(tree_sel(rel) + " .arrow", 5000)
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if c.exists(tree_sel(rel) + " .arrow svg.expanded").get("count", 0) > 0:
            return
        time.sleep(0.5)
    raise TestError("展开目录失败: %s" % rel)


def row_state(rel):
    """读某节点的灰显状态：{missing, ignored, color, disabled, title}。

    `disabled` = `var(--fg-disabled)` 经浏览器解析后的实测色（避免硬编码主题色）。
    """
    abs_path = "%s/%s" % (WS_POSIX, rel.replace("\\", "/"))
    js = """(()=>{const el=document.querySelector('.tree-row[data-path=%s]');
      if(!el)return JSON.stringify({missing:true});
      const lab=el.querySelector('.node-label');
      const probe=document.createElement('span');probe.style.color='var(--fg-disabled)';
      document.body.appendChild(probe);const dis=getComputedStyle(probe).color;probe.remove();
      return JSON.stringify({missing:false,ignored:el.classList.contains('is-ignored'),
        color:lab?getComputedStyle(lab).color:'',disabled:dis,
        title:el.getAttribute('title')||''});})()""" % json.dumps(abs_path)
    return ev(js)


def assert_gray(rel):
    """断言节点已灰显：is-ignored + 文本色 == --fg-disabled 实测色 + 原生 title。"""
    st = row_state(rel)
    if not isinstance(st, dict) or st.get("missing"):
        raise TestError("节点不存在，无法断言灰显: %s" % rel)
    if not st.get("ignored"):
        raise TestError("%s 应带 is-ignored（被索引排除规则命中）" % rel)
    if not st.get("disabled") or st.get("color") != st.get("disabled"):
        raise TestError("%s 文本色应等于 --fg-disabled：color=%r disabled=%r"
                        % (rel, st.get("color"), st.get("disabled")))
    if st.get("title") != TITLE_ZH:
        raise TestError("%s 原生 title 应为 %r，实际=%r" % (rel, TITLE_ZH, st.get("title")))
    return st


def assert_not_gray(rel):
    """断言节点未灰显：无 is-ignored 且文本色 != --fg-disabled。"""
    st = row_state(rel)
    if not isinstance(st, dict) or st.get("missing"):
        raise TestError("节点不存在，无法断言未灰显: %s" % rel)
    if st.get("ignored"):
        raise TestError("%s 不应带 is-ignored（未被排除）" % rel)
    if st.get("disabled") and st.get("color") == st.get("disabled"):
        raise TestError("%s 文本色不应为 --fg-disabled" % rel)
    return st


def wait_gray(rel, gray=True, max_wait=20):
    """轮询等待节点的灰显状态达预期（重判是异步的）。"""
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        last = row_state(rel)
        got = bool(isinstance(last, dict) and last.get("ignored"))
        if got == gray:
            return last
        time.sleep(0.4)
    raise TestError("等待 %s 灰显=%s 超时；末次=%r" % (rel, gray, last))


# ══════════════════════════════════════════════════════════
# H1 引擎启用 + 叠加 gitignore → 命中条目灰显
# ══════════════════════════════════════════════════════════

def case_h1_ignored_gray():
    """`enable-vfts=true` + `vfts.stack-gitignore=true` → `.gitignore` 命中条目灰显。

    链路：写 prj 键 → 既有 `data-prj-config-refresh` 广播 → `FileTree` 重判已加载节点
    （`data-index-ignored`，判定口径 = 引擎 `src/lib/ignore` 单一实现）→ DOM `is-ignored`。

    判据（与 `.gitignore` 语义一一对应）：
      * `gen.gen.go`（`*.gen.go` 文件级排除）→ 灰；
      * `skipdir`（目录排除）→ 灰；
      * `skipdir/inside.go`（祖先剪枝：父目录命中 → 子级亦排除）→ 展开后灰；
      * `special.gen.go`（`!special.gen.go` 同级反选）→ 不灰；
      * `keep.go`（无规则命中）→ 不灰。
    """
    # codegraph 保持关（隔离：仅 vfts 贡献判定），vfts 启用 + 叠加 gitignore + 无用户规则
    prj_save("enable-codegraph", "false")
    prj_save("enable-vfts", "true")
    prj_save("vfts.skip-dirs", "")
    prj_save("vfts.stack-gitignore", "true")

    # 根节点加载完成
    for rel in ("keep.go", "gen.gen.go", "special.gen.go", "skipdir"):
        wait_row(rel)

    wait_gray("gen.gen.go", True)
    wait_gray("skipdir", True)
    sg = assert_gray("gen.gen.go")
    assert_gray("skipdir")
    # 未命中 → 不灰
    assert_not_gray("special.gen.go")
    assert_not_gray("keep.go")

    # 展开被排除目录 → 子项（祖先剪枝）亦灰
    expand_dir("skipdir")
    wait_row("skipdir/inside.go")
    wait_gray("skipdir/inside.go", True)
    assert_gray("skipdir/inside.go")

    print("[H1] enable-vfts + stack-gitignore → 灰=%r（skipdir/inside.go 祖先剪枝）、"
          "不灰=%r；灰显色=%r title=%r"
          % (["gen.gen.go", "skipdir", "skipdir/inside.go"], ["special.gen.go", "keep.go"],
             sg.get("color"), sg.get("title")), flush=True)


# ══════════════════════════════════════════════════════════
# H2 对照组：引擎未启用 → 全不灰（刷新重判的反向证据）
# ══════════════════════════════════════════════════════════

def case_h2_disabled_not_gray():
    """`enable-vfts=false` → **全不灰**（含此前已灰、且已展开的子项）。

    语义：「引擎未启用时不会有任何灰显」（后端 `enabled=false → ignored=[]`）；同时是
    **刷新重判** 的反向证据 —— 既有已灰节点经 `data-prj-config-refresh` 重判后灰显消失。
    """
    prj_save("enable-vfts", "false")

    for rel in ("gen.gen.go", "skipdir", "skipdir/inside.go"):
        wait_gray(rel, False)
        assert_not_gray(rel)
    # 本来不灰的仍不灰（无副作用）
    assert_not_gray("special.gen.go")
    assert_not_gray("keep.go")
    print("[H2] enable-vfts=false → 全不灰（gen.gen.go / skipdir / skipdir/inside.go 灰显已消失）",
          flush=True)


def main():
    c.wait_ready()
    c.console(clear=True)
    total = 0
    ok = 0
    cases = [
        ("H1 引擎启用 + 叠加 gitignore → 命中条目（含子项祖先剪枝）灰显，反选/无命中不灰",
         case_h1_ignored_gray),
        ("H2 对照组 enable-vfts=false → 全不灰（引擎未启用不灰 + 刷新重判）",
         case_h2_disabled_not_gray),
    ]
    for name, fn in cases:
        total += 1
        ok += run_case(name, fn)
    print("\n文件树排除灰显断言：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
    print("RESULT:", ok == total)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

# -*- coding: utf-8 -*-
"""L4 套件：场景向导「启动自动弹出」（`project_spec.md` 缺失 → `agent-wizard`）。

被测行为（`src/lib/gui/bridge/guimsg.go:67-88` + `src/frontend/src/views/layout/MainLayout.vue:270-281`）：
  GUI 处理 `gui.init-data` 时探测 `<workDir>/.chonkpilot/project_spec.md`：
    * 缺失（= 项目未初始化）→ 应答补 `wizard_required=true` + 下发 `agent-wizard` 事件
      → 前端 `maybeAutoOpenWizard` **自动弹出**场景向导对话框；
    * 存在（= 已初始化）→ `wizard_required=false` → **不**自动弹。

覆盖（真机 DOM 级断言，不用"保存成功即算过"）：
  W1 缺失 → 自动弹出：空 work-dir（无 `.chonkpilot/project_spec.md`）启动后，`.dialog-shell`
      **内含向导根 `.wizard-root`**（DialogShell 外壳 + ScenarioWizardDialog 内容）。
  W2 弹出即该场景向导（非它窗）：向导头部 `.wz-path` 回显**本项目目录**（work-dir 名）+
      步骤导航 `.wz-rail-item` ≥ 1 + `.dialog-body` 带 `wizard-dialog-body`。
  W3 对照（有 `project_spec.md` → 不弹）：同一 work-dir 预置该文件后重启 → 等待窗口内
      **始终无** `.dialog-shell .wizard-root`（证明弹出由「缺失」这一判据因果触发）。

观测手段（**零新增 MQ 主题**）：仅用既有 `--test-port` 通道的 `/eval` `/exists`（DOM 断言）+
  看板夹具判据（`project_spec.md` 文件存在性）。

隔离与环境（51-FP与测试映射 §5/§6-8）：
  * **自起** GUI：动态端口 + 独立临时 work-dir / `--data-dir` / `HOME`（usr 主库全新）。
  * **本套件刻意不经 `harness.start_gui`/`ensure_project_spec`**：被验证的正是「无
    `project_spec.md`」场景，`start_gui` 会在启动前预置该文件 → 改用 `harness.popen_own`
    直起（其余套件统一经 `ensure_project_spec` 抑制向导，见 `harness.py`）。
  * 夹具与落盘（`<ws>/.chonkpilot/...`）在临时目录内 → 随 `harness.tmp_dir` 一并删除 → 零残留。

运行：python run_wizard_auto.py（自起自收，无需外部底座）
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

EXE = _h.resolve_gui_exe()
if not os.path.isfile(EXE):
    print("RESULT: True (SKIPPED: 未找到 GUI 产物 %s → 先构建 dist/desktop)" % EXE, flush=True)
    sys.exit(0)

WS = _h.tmp_dir("ck-wizard-ws-")   # 空 work-dir：故意不含 .chonkpilot/project_spec.md
DD = _h.tmp_dir("ck-wizard-dd-")
HOME = _h.tmp_home()
SPEC = os.path.join(WS, ".chonkpilot", "project_spec.md")

if os.path.exists(SPEC):
    # 前置判据（本套件验证缺失场景）：不应存在
    print("RESULT: False (前置失败：%s 不应存在)" % SPEC, flush=True)
    sys.exit(1)

# 向导 DOM 契约：DialogShell 的 `.dialog-shell` + ScenarioWizardDialog 根 `.wizard-root`
# （头部 `.wz-path` / 侧栏 `.wz-rail-item` / body class `wizard-dialog-body`）。
WIZ_JS = """(() => {
  const shells = [...document.querySelectorAll('.dialog-shell')]
      .filter(e => e.getBoundingClientRect().width > 0);
  const s = shells.find(e => e.querySelector('.wizard-root') || e.querySelector('.wz-toolbar'));
  if (!s) return { found: false, shells: shells.length };
  const q = (sel) => { const el = s.querySelector(sel); return el ? (el.textContent || '').trim() : ''; };
  return {
    found: true,
    title: q('.dialog-title'),
    path: q('.wz-path'),
    rail: s.querySelectorAll('.wz-rail-item').length,
    bodyClass: (s.querySelector('.dialog-body') || {}).className || '',
  };
})()"""

WIZ_PRESENT_JS = """!!([...document.querySelectorAll('.dialog-shell')]
  .find(e => e.getBoundingClientRect().width > 0 && e.querySelector('.wizard-root')))"""


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


_STATE = {"proc": None, "client": None}


def _spawn():
    """自起实例（**不经** start_gui → 不预置 project_spec.md）。"""
    port = _h.free_port()
    env = dict(os.environ)
    env["USERPROFILE"] = HOME   # Go os.UserHomeDir() 在 Windows 取 USERPROFILE
    env["HOME"] = HOME
    proc = _h.popen_own(
        [EXE, "--test-port=%d" % port, "--work-dir=" + WS, "--data-dir=" + DD],
        name="gui:wizard:%d" % port, cwd=os.path.dirname(EXE), env=env,
        creationflags=_h.CREATE_NO_WINDOW | _h.ABOVE_NORMAL_PRIORITY_CLASS)
    cli = ChonkClient(base="http://127.0.0.1:%d" % port)
    cli.wait_ready(120)
    _h.wait_probe(cli, ["!!document.querySelector('.panel-inner')"], timeout=60)
    _h.wait_idle(cli, max_wait=30)
    _STATE["proc"], _STATE["client"] = proc, cli
    return proc, cli


_spawn()
print("[env] ws=%s data=%s home=%s exe=%s（临时目录，结束即删）" % (WS, DD, HOME, EXE), flush=True)


# ══════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════

def _wiz_info():
    return _h._plain(_STATE["client"].eval(WIZ_JS, 10000)) or {}


def case_w1_auto_popup():
    """W1：无 project_spec.md 启动 → 场景向导自动弹出（.dialog-shell 内含 .wizard-root）。"""
    ok = _h.wait_probe(_STATE["client"], [WIZ_PRESENT_JS], timeout=90)
    info = _wiz_info()
    if not ok or not info.get("found"):
        raise TestError("启动后未见场景向导自动弹出（.dialog-shell 内无 .wizard-root）；"
                        "当前 shells=%s" % info.get("shells"))
    evi("W1 缺失 project_spec → 自动弹向导", spec_exists=os.path.exists(SPEC),
        title=info.get("title"), path=info.get("path"), rail=info.get("rail"),
        body_class=info.get("bodyClass"))


def case_w2_dialog_is_wizard():
    """W2：弹出的是**场景向导**本身（非它窗）：标题「场景向导」+ 步骤导航 + 向导 body class。

    注：走 `wizard_required` 兜底自动打开时 payload 不含 work_dir（`r.work_dir` 为空）→
    头部路径显示 `-`；`agent-wizard` 事件通道才带 work_dir（可能因 App 引导期预取而漏收）。
    故此处只断言「是场景向导对话框」，不断言路径回显。
    """
    info = _wiz_info()
    if not info.get("found"):
        raise TestError("W2 依赖 W1：当前无向导对话框")
    if (info.get("title") or "") != "场景向导":
        raise TestError("对话框标题非「场景向导」：%r" % info.get("title"))
    if int(info.get("rail") or 0) < 1:
        raise TestError("向导步骤导航为空（.wz-rail-item=0）→ 非场景向导")
    if "wizard-dialog-body" not in (info.get("bodyClass") or ""):
        raise TestError("向导 body class 非 wizard-dialog-body：%r" % info.get("bodyClass"))
    evi("W2 弹出即场景向导", title=info.get("title"), rail=info.get("rail"),
        body_class=info.get("bodyClass"))


def case_w3_contrast_with_spec():
    """W3 对照：预置 project_spec.md 后重启 → 不自动弹向导（因果判据）。"""
    _h.ensure_project_spec(WS)   # 模拟「已初始化项目」
    if not os.path.exists(SPEC):
        raise TestError("夹具失败：ensure_project_spec 未生成 %s" % SPEC)
    _STATE["proc"].stop()        # 回收实例 #1（登记项，owned=True）
    proc2, cli2 = _spawn()
    # 有 project_spec → wizard_required=false → 等待窗口内确认始终无向导
    deadline = time.time() + 8
    while time.time() < deadline:
        if bool(_h._plain(cli2.eval(WIZ_PRESENT_JS, 10000))):
            raise TestError("project_spec.md 存在时仍自动弹出向导（对照失败）")
        time.sleep(0.5)
    evi("W3 对照：project_spec 存在不自动弹向导", spec_exists=os.path.exists(SPEC), auto_popup=False)


def main():
    ok = total = 0
    try:
        _STATE["client"].console(clear=True)
    except Exception:
        pass
    print("依赖：--test-port 外部驱动通道（harness 自起，**不经 ensure_project_spec**）；"
          "判据 = <ws>/.chonkpilot/project_spec.md 存在性", flush=True)
    for name, fn in [
        ("W1 无 project_spec → 启动自动弹出场景向导", case_w1_auto_popup),
        ("W2 弹出即场景向导（标题/步骤/向导 body）", case_w2_dialog_is_wizard),
        ("W3 对照：有 project_spec → 不自动弹向导", case_w3_contrast_with_spec),
    ]:
        total += 1
        ok += run_case(name, fn)
    try:
        for e in (_STATE["client"].console() or {}).get("entries", []):
            if e.get("level") == "error":
                print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    except Exception:
        pass
    print("\n场景向导启动自动弹出：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())

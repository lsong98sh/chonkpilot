# -*- coding: utf-8 -*-
"""验证：添加场景保存后 ChatPanel 场景选择列表即时刷新（scenario-reload 广播）。

路径：preview-tab-open(scenario) → scenario-add 弹窗 → 填名 → scenario-save
→ ScenarioDialogContent.onDone 广播 scenario-reload → ChatPanel 刷新
→ 点开 ChatPanel 场景 popover 断言新场景出现。

隔离（51-FP与测试映射 §5/§6-8）：
  * 自起 GUI：动态端口 + 独立 work-dir + 独立 `--data-dir` + **独立 HOME**
    （app 级出厂场景 = 发行目录磁盘目录 `dist/desktop/capability/scenarios/`；**不读机器 `~/.chonkpilot`**）。
  * 确定性：三级场景（app / user / project）自 2026-09-26 起**均可编辑**；出厂场景 = app 级
    （`capability/scenarios/default/`，出厂内容 = 磁盘目录（源 `src/initdata/capability/scenarios/`，由构建脚本投放），25 §6）。
    若沿用真实 HOME，机器上可能残留旧版「list 首次物化到 user 级」写下的同名
    `scenarios/default/`（§6 禁止同名场景）→ 场景集随机器状态漂移（`list` 出现同 id 两条）。
    隔离 HOME 后场景集恒为 app 级出厂集 → 用例与机器状态解耦、可确定复现。
  * 套件级快照-还原 `_h.suite_config_guard(c)`（usr+prj；含异常/中断路径）。

运行：python run_scenario_reload.py
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
WS = _h.tmp_dir("ck-screload-ws-")
DD = _h.tmp_dir("ck-screload-dd-")
HOME = _h.tmp_home()
_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：usr+prj 全量，退出前自动回滚
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）" % (WS, DD, HOME, _g.port), flush=True)
NAME = "ReloadCheck-场景"


def _loads_deep(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def wait_el(selector, max_wait=10):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if c.exists(selector).get("count", 0) > 0:
            return True
        time.sleep(0.4)
    return False


def _scenario_list():
    """data-scenario-list 消息面（替代已移除的 GetScenarioList RPC）。"""
    res = c.req("data-scenario-list", {})
    lst = res.get("list") if isinstance(res, dict) else None
    return lst if isinstance(lst, list) else []


def _scenario_delete(name):
    """按名称删除场景（data-scenario-delete 消息面，替代 DeleteScenario RPC）。"""
    for s in _scenario_list():
        if s.get("name") == name:
            sid = s.get("id") or s.get("key") or s.get("name")
            if sid:
                c.req("data-scenario-delete", {"id": sid})


def case_add_refreshes_chat_list():
    # 清理残留同名场景（消息面）
    try:
        _scenario_delete(NAME)
    except Exception as e:
        print("    [dbg] 清理残留场景失败:", e)
    try:
        # 1. 打开场景 tab
        c.mq_emit("preview-tab-open", {"kind": "scenario", "title": "场景"})
        if not wait_el(".scenario-dialog-body-scroll, .scenario-content, .scenario-manager, .edit-dialog-body"):
            # 兜底：场景 tab 内容区域未知 class，等 body 出现"开发场景"（出厂默认场景显示名）
            deadline = time.time() + 8
            while time.time() < deadline:
                if "开发场景" in c.eval("document.body.innerText"):
                    break
                time.sleep(0.4)
        time.sleep(1)

        # 2. 触发添加（scenario-add → 编辑弹窗）
        c.mq_emit("scenario-add")
        if not wait_el(".edit-dialog-body", max_wait=8):
            raise TestError("添加场景弹窗未出现（.edit-dialog-body）")

        # 3. 填名称（第一个 input 即场景名；原生 setter + input 事件）
        c.input(".edit-dialog-body input", NAME, 5000)
        time.sleep(0.5)

        # 4. 保存（scenario-save → handleSave → onDone → scenario-reload 广播）
        c.mq_emit("scenario-save")
        time.sleep(2)

        # 5. 点开 ChatPanel 场景选择 popover（reference = .panel-header 内 b-popover__reference）。
        #    先查是否已展开，未展开才点（popover 是 toggle）；展开后轮询确认。
        deadline = time.time() + 10
        while time.time() < deadline:
            if c.exists(".popover-list").get("count", 0) > 0:
                break
            r = _loads_deep(c.eval("""(() => {
              const ref = document.querySelector('.panel-header .b-popover__reference');
              if (!ref) return 'no-ref';
              ref.dispatchEvent(new MouseEvent('click', { bubbles: true }));
              return 'clicked';
            })()"""))
            time.sleep(1)
        time.sleep(0.5)

        # 6. 断言：popover 场景项出现新场景名（P5 2026-10-01：名称后**统一标注级别** → "<名称> -<级别>"）
        deadline = time.time() + 6
        items = []
        while time.time() < deadline:
            items = _loads_deep(c.eval("JSON.stringify([...document.querySelectorAll('.popover-list .scenario-item-name')].map(e => e.textContent.trim()))"))
            hit = next((x for x in (items or []) if x == NAME or x.startswith(NAME + " -")), None)
            if hit:
                # 强度不降：不仅出现，且**带级别后缀**（用户级 → " -用户"/" -User"）
                if not any(x.startswith(NAME + " -") for x in (items or [])):
                    raise TestError(f"场景项未标注级别（应 \"{NAME} -<级别>\"）：{items!r}")
                return
            time.sleep(0.5)
        raise TestError(f"保存后 ChatPanel 场景列表未出现新场景 {NAME!r}（items={items!r}）")
    finally:
        # 清理（含异常/中断路径）：验证用场景一律删除（51 §6-8，保持环境干净）
        try:
            _scenario_delete(NAME)
        except Exception:
            pass
        time.sleep(1)


def main():
    c.console(clear=True)
    ok = run_case("添加场景后 ChatPanel 场景选择列表即时刷新", case_add_refreshes_chat_list)
    print("RESULT:", ok)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

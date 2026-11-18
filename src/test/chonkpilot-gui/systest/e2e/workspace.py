# -*- coding: utf-8 -*-
"""演示工程夹具：空工程 → 有数据工程（**幂等预置**）。

两种初始化模式（用户需求）：

* **空工程**（`empty`）：只有 `harness.start_gui` 预置的 `.chonkpilot/project_spec.md`
  （抑制场景向导），业务文件为零 → 用于测「首屏/空树/首次建文件」。
* **有数据工程**（`seeded`）：由本模块的 `DEMO_FILES` 在**同一次 GUI 启动内**、经 **LLM 工具
  `file_write`**（`llm-start` → mock LLM → 插件工具）批量落盘生成；目录由工具自动创建。
  **幂等**：`pending_plan()` 只返回磁盘上**不存在**的项 → 已有文件不重建、不覆盖。

演示工程刻意覆盖典型测试面：Markdown（预览/渲染）、Python（语法高亮）、HTML、CSV、TXT、
测试目录、Unicode 目录名、多级嵌套（`docs/design/`）。
"""

import base64
import json
import os
import time

# 预置工程的唯一哨兵前缀（测试点据此判定「是预置内容」而非碰巧通过）。
SEED_PREFIX = "E2E-SEED"

DEMO_FILES = {
    "README.md": """# E2E 演示工程

本工程由场景化 E2E 套件**通过 LLM 文件工具**预置（哨兵：E2E-SEED-README）。

## 用途

- 文件树：多级目录、排序、隐藏条目过滤
- 预览：Markdown 渲染、Python 语法高亮、HTML 文本
- 搜索：文件名/内容命中

```python
print("E2E-SEED-CODE")
```
""",
    "docs/guide.md": """# 使用指南

E2E-SEED-GUIDE

1. 打开文件树
2. 单击预览（临时页签）
3. 双击固定页签
""",
    "docs/design/arch.md": """# 架构说明

E2E-SEED-ARCH

- 前端：Vue + mq 消息面
- 后端：Go（persist / filesys / llm server）
""",
    "src/main.py": """# E2E-SEED-MAIN
def main():
    print("hello from e2e demo project")


if __name__ == "__main__":
    main()
""",
    "src/util.py": """# E2E-SEED-UTIL
def add(a, b):
    return a + b
""",
    "src/web/index.html": """<!doctype html>
<html>
<head><title>E2E-SEED-HTML</title></head>
<body><h1>E2E demo</h1></body>
</html>
""",
    "data/sample.csv": """name,value
alpha,1
beta,2
E2E-SEED-CSV,3
""",
    "data/notes.txt": """E2E-SEED-NOTES
多行文本夹具：用于内容预览与搜索命中。
""",
    "tests/test_main.py": """# E2E-SEED-TEST
def test_add():
    assert 1 + 1 == 2
""",
    "assets/banner.txt": """E2E-SEED-BANNER
""",
    "中文目录/说明.md": """# 中文目录

E2E-SEED-UNICODE
""",
}

# 各测试点常用哨兵（与 DEMO_FILES 内容对应）
SEED = {
    "README": "E2E-SEED-README",
    "GUIDE": "E2E-SEED-GUIDE",
    "ARCH": "E2E-SEED-ARCH",
    "MAIN": "E2E-SEED-MAIN",
    "UTIL": "E2E-SEED-UTIL",
    "HTML": "E2E-SEED-HTML",
    "CSV": "E2E-SEED-CSV",
    "NOTES": "E2E-SEED-NOTES",
    "TEST": "E2E-SEED-TEST",
    "BANNER": "E2E-SEED-BANNER",
    "UNICODE": "E2E-SEED-UNICODE",
}

# 目录清单（测试点断言「目录节点存在」用；由工具 createDirs 自动创建，不单独 mkdir）
DEMO_DIRS = ("docs", "docs/design", "src", "src/web", "data", "tests", "assets", "中文目录")


def demo_paths(workspace):
    """返回 {相对路径: 绝对路径}（保持 DEMO_FILES 顺序）。"""
    return {rel: os.path.join(workspace, rel) for rel in DEMO_FILES}


def pending_plan(workspace):
    """幂等预置计划：只返回磁盘上**缺失**的 (rel, content)。

    「已有文件则不再创建」是本框架的硬约定（用户需求 3）：即便内容不同也不覆盖，
    避免复用工作区时破坏上一轮测试留下的现场。
    """
    plan = []
    for rel, content in DEMO_FILES.items():
        if not os.path.exists(os.path.join(workspace, rel)):
            plan.append((rel, content))
    return plan


def seed_via_llm(env, run_turn, plan):
    """经 LLM 工具 `file_write`（一次调用、批量 targets、createDirs=true）落盘。

    返回 True = 计划内的文件**全部**落盘（磁盘为准）；False = 有缺失（调用方回退直接落盘）。
    """
    targets = [{"path": env.posix(env.path(rel)), "content": content,
                "createDirs": True, "overwrite": False}
               for rel, content in plan]
    plan_b64 = base64.b64encode(
        json.dumps(targets, ensure_ascii=False).encode("utf-8")).decode("ascii")
    q = ("【T-E2E-SEED】要求：调用 file_write 一次性写入预置工程（批量 targets、自动建目录）。"
         "e2e-seed e2eplan=%s" % plan_b64)
    print("[seed] LLM 工具预置 %d 个文件…" % len(targets), flush=True)
    run_turn(q, timeout=300)

    # 工具结果（尽力断言；最终判据 = 磁盘）。llm-complete 与 tool-result 的到端顺序不保证，
    # 故给一个有界窗口收集（不因缺失而中止预置）。
    end = time.time() + 8
    hit = []
    while time.time() < end:
        # env.events 返回 `{topic,payload,ts}` 事件信封 → 取 payload 再判工具名
        hit = [e.get("payload") for e in env.events("tool-result")
               if isinstance(e.get("payload"), dict)
               and _canon((e.get("payload") or {}).get("tool")).endswith("file_write")]
        if hit:
            break
        time.sleep(0.3)
    if hit:
        print("[seed] tool-result file_write status=%r" % (hit[-1] or {}).get("status"),
              flush=True)
    else:
        print("[seed] 未捕获 file_write tool-result（以磁盘为准继续）", flush=True)

    end = time.time() + 10
    while time.time() < end and pending_plan(env.workspace):
        time.sleep(0.3)
    missing = [rel for rel, _ in plan if not os.path.exists(env.path(rel))]
    if missing:
        print("[seed] 预置缺失 %d 项：%r" % (len(missing), missing[:5]), flush=True)
        return False
    print("[seed] LLM 工具预置完成：%d 个文件（磁盘校验通过）" % len(plan), flush=True)
    by = "LLM tool file_write" if hit else "LLM turn + 磁盘校验"
    for rel, _content in plan:
        env.evidence.append({"check": "seed:" + rel, "ok": True, "detail": "created by " + by})
    return True


# 场景 / 智能体夹具（子代理与群聊用例的固定参数前置；服务端按场景团队校验 agent）
SCENARIO_ID = "e2e-scenario"
SCENARIO_MAIN = "e2e-main"
SCENARIO_PEERS = ("e2e-peer1", "e2e-peer2")


def ensure_scenario(env):
    """幂等保存项目级场景（1 主 agent + 2 成员）→ 返回 scenario_id。

    服务端校验：非主 agent **必须以引用（ref）形式保存**（`*.agent.md` 契约文件），
    故这里先幂等落盘 3 个 agent 契约，再写场景（同一 id 重复保存 = 覆盖，内容确定 → 幂等）。
    """
    agents_dir = os.path.join(env.workspace, ".chonkpilot", "capability", "agents")
    os.makedirs(agents_dir, exist_ok=True)

    def write_agent(name, body):
        path = os.path.join(agents_dir, name + ".agent.md")
        if not os.path.exists(path):
            with open(path, "w", encoding="utf-8") as f:
                f.write("# 基本信息\n```yaml\nname: %s\n```\n\n"
                        "# 摘要\nE2E agent 夹具\n\n# 正文\n%s\n" % (name, body))

    write_agent(SCENARIO_MAIN, "E2E 主协调者提示词")
    for peer in SCENARIO_PEERS:
        write_agent(peer, "E2E 成员提示词 %s" % peer)

    ref = lambda n: "${workDir}/.chonkpilot/capability/agents/%s.agent.md" % n  # noqa: E731
    agents = [{"name": SCENARIO_MAIN, "roleTag": "main", "isMain": True,
               "prompt": "E2E 主协调者提示词"}]
    for peer in SCENARIO_PEERS:
        agents.append({"name": peer, "roleTag": "", "ref": ref(peer)})
    env.req("data-scenario-save", {"data": {
        "id": SCENARIO_ID, "name": "E2E 场景", "level": "project", "agents": agents,
    }})
    return SCENARIO_ID


def _canon(name):
    """工具名归一（当前暴露名 = 原名，恒等）。"""
    n = name or ""
    return n


def ensure_demo_file(env, rel):
    """测试点前置：确保某个预置文件存在（缺失则直接落盘补，幂等）。"""
    p = env.path(rel)
    if os.path.exists(p):
        return p
    env.write_text(rel, DEMO_FILES[rel])
    return p
# -*- coding: utf-8 -*-
"""本地 OpenAI 兼容 mock LLM 服务器（供 testplan 五 LLM 调用用例，不依赖真实 API）。

用法：python mock_llm.py [port]  （默认 8901）
端点：POST /v1/chat/completions（stream 与 non-stream）· POST /v1/responses（Responses API 分支，
      protocol=responses；SSE response.output_text.delta + response.completed，无 [DONE]）；
      测试辅助 GET /reset（清一次性状态 + 请求记录）· GET /last（最近一次请求证据：path/protocol/
      model/temperature/max_tokens/max_output_tokens/reasoning_effort/system/authorization/
      has_tool_result/n_requests）· GET /lastbody（请求体多模态摘要）。
行为：把最后一个 user 消息回显为回复；根据文本关键词返回 tool_calls 以驱动
      testplan 相关场景（工具调用 / cancel）：
        "call tool"   → self_file_read a.txt        （工具调用链路，testplan 五.3）
        "call cancel" → self_script_run 长命令      （testplan 五.8）
        "call slow-never" → self_script_run 慢命令（call-level async=never + timeout=1s；
                            run_tool_async.py 断言 mcp-tools-timeout options=[wait,cancel]）
        "call find-skip"  → self_file_find（grep=GATE_SKIP_SENTINEL；路径由提示词携带
                            `findskip=<绝对路径>`；run_index_gate.py 断言 prj skip_dirs 端到端）
        "call interp-probe" → self_script_run（runtime 由提示词携带 `rt=<runtime>`；run_paths.py
                            断言 CHONK_CHROME / CHONKPILOT_INTERPRETERS 子进程 env 注入
                            与「被启动解释器 == 配置 *Path 值」）
        "call java-real"   → self_script_run(runtime=java，打印哨兵 CK-JAVA-OK 的类；run_paths.py
                            java 真机端到端，回归 I-80：临时脚本扩展名必须 .java）
      llm_run 委派/编排 DSL（G-11 重建，run_llm.py 五.4/5/6/7 + C13/C18）：
        "call delegate-plain"  → llm_run 单次委派（无第三参 → 展示名回退提示词截断）
        "call delegate-purpose"→ llm_run 单次委派（第三参=目的 → 子节点展示名）
        "call batch"           → llm_run LOOP 批量（planner 产 JSON 数组 → concurrency=2）
        "call delegate-cancel" → llm_run 首步委派慢工具（供 task-stop 级联取消）
        "call delegate-slow"   → 子会话内慢命令（self_script_run ping 20s）
      文件历史检查点链（run_hist_git.py，2026-09-27 批次③语义；参数由提示词携带）：
        "call hist-write"         + histabs=<绝对路径> histfrom=<旧> histval=<新>
                                  → self_filesys_run（RPL 真实改文件 → 置脏 → 打点）
        "call hist-restore"       + histrel=<workdir 相对路径> [histto=<负整数|commit id>]
                                  → self_history_restore（单文件回滚）
        "call hist-restore-nopath"→ self_history_restore{}（空 path，应被拒绝）
        "call hist-restore-dir"    + histrel=<相对目录>
                                  → self_history_restore（目录，应被拒绝）
      **工具名一律用网关暴露名**（self 节点 entry.ID 前缀 `self_`）：server 按暴露名
      调 gateway tools/call，未带前缀的裸名无法路由（I-20）——file_read/script_run/
      user_ask/tool_result/llm_run 均为 self_* 形态。
      "BATCHLIST" 由 reply_override 直接回 JSON 文本数组（供 SET raw.array 解析）。
工具结果回填后再次收到调用时文本不再含关键词 → 返回普通回复，避免循环。
"""

import json
import os
import re
import socket
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SCRIPTS_WS = r"E:\BizWorks\chonkpilot\src\test\chonkpilot-gui\systest\ws"  # systest 工作目录（用例输入/输出）

# S21 空回复一次性状态："call empty" 首次返回空回复；"继续"重发同一文本时消费该标记
# 返回正常回复（验证"空回复 → 继续 → 重发原消息"链路）。
_EMPTY_ONCE = {"active": False}

# 记录最近一次请求的可观测证据（GET /last），供 run_llm_config.py / run_llm_fields.py 断言：
#   - 既有字段（向后兼容，语义与取值不变）：model / temperature / max_tokens；
#   - P2 扩展字段：path + protocol（区分 /chat/completions 与 /responses）·
#     max_output_tokens（responses 侧生成上限）· reasoning_effort（responses 侧来自 reasoning.effort）·
#     system（system 原文 / instructions 原文）· authorization（Authorization 头原文）·
#     has_tool_result（请求上下文是否已含工具结果 → 证明发生了工具回喂续轮）·
#     n_requests（本进程累计请求数 → 「是否打到本端点 / 是否发生第二轮」类断言）。
_LAST_REQ = {}

# 进程内累计请求数（GET /reset 归零）。
_REQ_SEQ = {"n": 0}

# P2-8：记录最近一次请求体的多模态摘要（图片 image_url 的 mime/前缀/长度 + 文本），
# 供图片给 LLM 的端到端断言（GET /lastbody）。仅测试桩侧采集，不改产品/消息面。
_LAST_BODY = {}


def _image_summary(url):
    """从 image_url 值（data URL）提取 mime、前缀与 base64 载荷长度。"""
    mime = ""
    if isinstance(url, str) and url.startswith("data:"):
        mime = url[5:].split(";", 1)[0]
    payload = url.split(",", 1)[1] if isinstance(url, str) and "," in url else ""
    return {"data_url_prefix": (url or "")[:48], "mime": mime, "b64len": len(payload)}


def _summarize_body(body):
    """汇总请求体：messages 数与各 user/assistant 文本块、image_url 图片块。"""
    msgs = body.get("messages", []) or []
    images, texts = [], []
    for m in msgs:
        c = m.get("content")
        if isinstance(c, list):
            for b in c:
                if not isinstance(b, dict):
                    continue
                if b.get("type") == "text" and b.get("text"):
                    texts.append(b["text"])
                if b.get("type") == "image_url":
                    images.append(_image_summary((b.get("image_url") or {}).get("url", "")))
        elif isinstance(c, str) and c:
            texts.append(c)
    return {"n_messages": len(msgs), "images": images, "texts": texts}


def _system_text_chat(body):
    """拼接 chat/completions 请求里所有 role=system 消息的文本（system 原文）。"""
    parts = []
    for m in body.get("messages", []) or []:
        if not isinstance(m, dict) or m.get("role") != "system":
            continue
        c = m.get("content")
        if isinstance(c, str) and c:
            parts.append(c)
        elif isinstance(c, list):
            for b in c:
                if isinstance(b, dict) and b.get("type") == "text" and b.get("text"):
                    parts.append(b["text"])
    return "\n\n".join(parts)


def _responses_system_text(body):
    """Responses 请求的 system 原文 = instructions + input 里的 system message 文本。"""
    parts = []
    ins = body.get("instructions")
    if isinstance(ins, str) and ins:
        parts.append(ins)
    for it in body.get("input", []) or []:
        if not isinstance(it, dict) or it.get("role") != "system":
            continue
        c = it.get("content")
        if isinstance(c, str) and c:
            parts.append(c)
        elif isinstance(c, list):
            for b in c:
                if isinstance(b, dict) and b.get("text"):
                    parts.append(b["text"])
    return "\n\n".join(parts)


def _responses_last_user_text(body):
    """取 Responses 请求里最后一段文本（工具结果 item 优先，其次 message item）。"""
    for it in reversed(body.get("input", []) or []):
        if not isinstance(it, dict):
            continue
        if it.get("type") == "function_call_output":
            return it.get("output") or ""
        c = it.get("content")
        if isinstance(c, str) and c:
            return c
        if isinstance(c, list):
            parts = [b.get("text", "") for b in c if isinstance(b, dict) and b.get("text")]
            if parts:
                return "\n".join(parts)
    return "Hello"


def _request_summary(path, body, auth):
    """把一次请求归一为 /last 记录（chat / responses 两路统一外形）。

    兼容性：既有三字段（model/temperature/max_tokens）**取值路径与语义不变**——
    chat 侧仍取请求体同名键；responses 侧 model/temperature 同键，max_tokens 恒 None
    （responses 用 max_output_tokens，见 64-配置项一览 §5 maxOutputToken 行）。
    """
    is_resp = path.endswith("/responses")
    if is_resp:
        has_tool_result = any(
            isinstance(it, dict) and it.get("type") == "function_call_output"
            for it in (body.get("input") or [])
        )
        texts = []
        for it in body.get("input", []) or []:
            if not isinstance(it, dict):
                continue
            c = it.get("content")
            if isinstance(c, str) and c:
                texts.append(c)
            elif isinstance(c, list):
                texts.extend(b.get("text", "") for b in c if isinstance(b, dict) and b.get("text"))
        body_summary = {"n_messages": len(body.get("input", []) or []), "images": [], "texts": texts}
        reasoning = body.get("reasoning")
        effort = reasoning.get("effort") if isinstance(reasoning, dict) else None
        system = _responses_system_text(body)
    else:
        has_tool_result = any((m or {}).get("role") == "tool" for m in (body.get("messages") or []))
        body_summary = _summarize_body(body)
        effort = body.get("reasoning_effort")
        system = _system_text_chat(body)
    # P2-8：请求体多模态摘要（GET /lastbody）
    _LAST_BODY.clear()
    _LAST_BODY.update(body_summary)
    _REQ_SEQ["n"] += 1
    _LAST_REQ.clear()
    _LAST_REQ.update({
        "path": path,
        "protocol": "responses" if is_resp else "chat",
        "model": body.get("model"),
        "temperature": body.get("temperature"),
        "max_tokens": body.get("max_tokens"),
        "max_output_tokens": body.get("max_output_tokens"),
        "reasoning_effort": effort,
        "system": system,
        "authorization": auth or "",
        "has_tool_result": has_tool_result,
        "n_requests": _REQ_SEQ["n"],
    })


# 工具调用 id 全局唯一计数器：真实 LLM 的 tool-call id 逐次唯一；旧实现恒用 "tc-mock-000"
# 会使 gateway 按 tool_call_id 定位在飞任务（tools/background）时命中历史同名任务（locate
# 遍历 map 非确定的旧节点）→ 报 "task ... is not running"。加计数器使各轮 tool_call_id 互异。
_TC_SEQ = {"n": 0}


def _tc_id(i):
    _TC_SEQ["n"] += 1
    return "tc-mock-%04d-%03d" % (_TC_SEQ["n"], i)


def last_user_text(messages):
    # 取最后一条非 system 且有文本内容的消息（含 tool 结果消息），
    # 使最终回复纳入工具执行结果（验证 tool 结果回填链路）。
    for m in reversed(messages):
        if m.get("role") == "system":
            continue
        c = m.get("content")
        if isinstance(c, str) and c:
            return c
        if isinstance(c, list):
            parts = []
            for b in c:
                if isinstance(b, dict):
                    if b.get("type") == "text" and b.get("text"):
                        parts.append(b["text"])
                    elif b.get("type") == "tool_result":
                        rc = b.get("content", "")
                        if isinstance(rc, list):
                            for rb in rc:
                                if isinstance(rb, dict) and rb.get("type") == "text":
                                    parts.append(rb.get("text", ""))
                        elif rc:
                            parts.append(str(rc))
                elif isinstance(b, str) and b:
                    parts.append(b)
            if parts:
                return "\n".join(parts)
    return "Hello"


def tool_calls_message(calls):
    """构造 assistant tool_calls 消息。calls: [(name, arguments_dict), ...]"""
    return {
        "choices": [{"message": {
            "role": "assistant",
            "content": "",
            "tool_calls": [{
                "id": _tc_id(i),
                "type": "function",
                "function": {"name": name, "arguments": json.dumps(args, ensure_ascii=False)},
            } for i, (name, args) in enumerate(calls)],
        }, "finish_reason": "tool_calls"}],
    }


def tool_calls_chunks(calls):
    """构造流式 tool_calls chunks（首 chunk 角色、逐条 delta、结束 finish_reason）。"""
    chunks = [{"choices": [{"delta": {"role": "assistant", "content": ""}, "finish_reason": None}]}]
    for i, (name, args) in enumerate(calls):
        chunks.append({"choices": [{"delta": {"tool_calls": [{"index": i, "id": _tc_id(i),
                                                              "type": "function",
                                                              "function": {"name": name,
                                                                           "arguments": json.dumps(args, ensure_ascii=False)}}]},
                                    "finish_reason": None}]})
    chunks.append({"choices": [{"delta": {}, "finish_reason": "tool_calls"}]})
    return chunks


def reply_override(text):
    """返回非 None 时直接作为 LLM 回复文本（先于 tool_calls 路由，不触发工具）。

    用于 llm_run DSL 场景：planner 子轮次需产出 JSON 文本数组，供 DSL
    `SET raw.array => tasks` 解析为列表后 LOOP 迭代（字符串 .array 访问器）。
    """
    if "batchlist" in text.lower():
        return json.dumps([
            {"name": "批处理一", "prompt": "batch item one"},
            {"name": "批处理二", "prompt": "batch item two"},
        ], ensure_ascii=False)
    return None


def _prompt_arg(text, name, default=""):
    """取提示词里的 `name=值` 参数（供 run_hist_git.py 传文件/内容/目标点）。

    值到空白或引号为止（路径/内容不含空格）；未命中 → default。
    """
    m = re.search(r"(?:^|\s)%s=([^\s\"']+)" % re.escape(name), text)
    return m.group(1) if m else default


def route_tool_calls(text):
    """根据最后 user 文本返回 tool_calls 列表（None = 无工具，返回普通回复）。"""
    t = text.lower()
    # P2：maxToolIterations 用例（run_llm_fields.py）——首轮返回工具调用，工具结果回喂后第二轮
    # 文本不再含关键词 → 走默认分支返回普通回复（= "需要第二轮 LLM 调用"的最小链路）。
    # provider 配 maxToolIterations=1 → 第二轮 LLM 调用在 chatOnce 入口被截断
    #（turn.go:365 TOOL_LOOP_LIMIT，mock 收不到第 2 个请求）；配 2 → 第二轮正常发生并收尾。
    if "call loop-limit" in t:
        return [("self_script_run", {"runtime": "cmd", "script": "echo loop-limit",
                                     "tool_call_display_name": "测试"})]
    if "call close" in t:
        # 关闭链：Alt+F4（key_press 带 window 参数自动聚焦）→ IDE 退出（C6 持久化测试用）。
        # window = work_dir 名（GUI 标题 = filepath.Base(workDir)，非 "Chonk Pilot"）。
        return [("key_press", {"key": "f4", "modifiers": ["alt"],
                               "window": os.path.basename(SCRIPTS_WS),
                               "tool_call_display_name": "回归"})]
    # ── I-88：真实会话工具调用 → Task 层权威行（run_tool_async.py H）──
    # 真实 script_run（cmd `ping -n 4`，约 3s）：给「层权威行 state=running（执行推进中）」留
    # 可观测窗口，工具自然结束后落 done；用于断言 data-tasktree-tasks 出现权威行且 exec_json
    # 含 gw_task_id（默认 async=manual，3s 远早于超时点 → 就地交付 done，不触发裁决）。
    if "call task-layer" in t:
        return [("self_script_run", {"runtime": "cmd", "script": "ping -n 4 127.0.0.1",
                                     "tool_call_display_name": "层落库"})]
    # ── 统一异步模型 L4（run_tool_async.py）：超时交用户裁决 ──
    # 调用级 async=never + timeout=1s（gateway 执行前剥离）→ 慢工具到超时点发 mcp-tools-timeout
    # {reason:"timeout", options:["wait","cancel"]}；第三方软缺省 never 同理（本路由用 builtin 工具
    # 复现 never 分支，使 GUI 端可断言超时事件与 options）。
    if "call slow-never" in t:
        return [("self_script_run", {"runtime": "cmd", "script": "ping -n 12 127.0.0.1",
                                     "async": "never", "timeout": 1,
                                     "tool_call_display_name": "超时裁决"})]
    # 统一异步模型 L4-E（run_tool_async.py）：「转后台后再取消」——script_run 契约软缺省即
    # manual（core/script_run.tool.md:7 async=manual），显式给出 async=manual + timeout=1s，
    # 到超时点发 mcp-tools-timeout{options:[detach,cancel]}；20s 长命令给「转后台」与后续
    # 「后台运行中取消」留足窗口；「转异步」经 task-background{tool_call_id}（server 门控
    # AsyncMode=manual 放行）→ 进程内 sink DetachExec（2026-09-18 起不再经 gateway
    # tools/background，该方法面已移除）。
    if "call slow-manual" in t:
        return [("self_script_run", {"runtime": "cmd", "script": "ping -n 20 127.0.0.1",
                                     "async": "manual", "timeout": 1,
                                     "tool_call_display_name": "转后台"})]
    # ── 第三方 spawned 夹具（slow3p，经 usr mcps 注册后网关拉起；run_tool_async.py D）──
    # 快工具（回显 pid，供取消前后比对 respawn）；慢工具 call-level async=never + timeout=1
    #（夹具软缺省 never，等价；显式给出保证 1s 到超时点）。
    if "call slow3p-echo" in t:
        return [("slow3p_fast_echo", {"text": "ping", "tool_call_display_name": "夹具"})]
    if "call slow3p-never" in t:
        return [("slow3p_slow_sleep", {"seconds": 5, "async": "never", "timeout": 1,
                                       "tool_call_display_name": "夹具"})]
    # ── llm_run 委派/编排 DSL（G-11 重建：单次委派 / LOOP 批量 / 级联取消）──
    # 主轮次命中关键词 → 返回 llm_run tool_call（script = DSL 脚本）；子轮次提示词
    # （prompt）不含 "call ..." 关键词 → 普通回复，避免递归触发。
    # 工具名用网关暴露名（self_ 前缀，见 domainmcp_test.go：域工具注入 self 节点后
    # 暴露名 = self_<契约名>）；server 侧按契约名归一（TrimPrefix "self_"）。
    if "call delegate-plain" in t:
        # 单次委派：无第三参 → 子任务展示名回退为提示词截断（delegate-plain-prompt）
        # 委派对象名须「可委派」（agentDelegable）：本套件 scenario_id=""（通用模式，无团队成员段）
        # → 只能靠 app 级场景注册表裸名唯一命中（出厂场景 = 开发场景，成员见 37-场景）。
        return [("self_llm_run", {"script": 'LLM "后端开发" "delegate-plain-prompt"',
                                  "tool_call_display_name": "委派"})]
    if "call delegate-purpose" in t:
        # 单次委派：第三参 = 目的（运行目的/展示名）
        return [("self_llm_run", {"script": 'LLM "后端开发" "delegate-purpose-prompt" "委派展示名-自定义"',
                                  "tool_call_display_name": "委派"})]
    if "call batch" in t:
        # LOOP 批量：首步产出 JSON 数组（落盘到 workDir 的 g11-batch.json）
        # → 文件句柄 .array 解析为列表（.array 是文件句柄访问器，字符串变量无此访问器）
        # → concurrency=2 迭代委派。路径用 {{env.CHONKPILOT_WORKDIR}} 显式拼绝对路径（R-11）。
        script = (
            'LLM "架构设计师" "BATCHLIST 只输出 JSON 数组" "生成批处理清单" '
            '=> #"{{env.CHONKPILOT_WORKDIR}}/g11-batch.json"\n'
            'LOOP item=#"{{env.CHONKPILOT_WORKDIR}}/g11-batch.json".array concurrency=2\n'
            '   LLM "后端开发" "{{item.prompt}}" "{{item.name}}"\n'
            'END'
        )
        return [("self_llm_run", {"script": script, "tool_call_display_name": "批量"})]
    if "call delegate-cancel" in t:
        # 级联取消：首步委派慢工具（script_run ping 20s 阻塞）→ 期间对根节点 task-stop；
        # 第二步为「取消后置步」哨兵（取消生效则不得启动）。
        script = (
            'LLM "后端开发" "please call delegate-slow" "慢步子任务"\n'
            'LLM "后端开发" "delegate-after-cancel" "取消后置步"'
        )
        return [("self_llm_run", {"script": script, "tool_call_display_name": "取消"})]
    if "call delegate-slow" in t:
        # 子会话内慢工具（llm_run 子步骤）：暴露名 self_script_run，20s 同步阻塞，
        # 供根节点 task-stop 级联取消（C13 子会话工具链 + C18 级联）。
        return [("self_script_run", {"runtime": "cmd", "script": "ping -n 20 127.0.0.1",
                                     "tool_call_display_name": "测试"})]
    # P4 索引/执行参数批次（run_index_gate.py）：file_find 走项目级 skip_dirs 端到端。
    # 搜索根路径由提示词携带（`findskip=<绝对路径>`，无空格）——mock 按关键词回一次
    # file_find 调用，使「prj skip_dirs 生效 → 命中集合不含被跳过目录」可在 GUI 侧断言。
    # 位置：必须早于下面的 `call py`/`call cancel` 等宽关键词分支（本关键词互不包含，仍前置求稳）。
    if "call find-skip" in t:
        m = re.search(r"findskip=([^\s\"']+)", text)
        root = m.group(1) if m else SCRIPTS_WS
        return [("self_file_find", {"path": root, "grep": "GATE_SKIP_SENTINEL",
                                    "output": "file", "tool_call_display_name": "跳过目录验证"})]
    # I-80 回归（run_paths.py）：java 真机单文件源码模式——runtime=java + 打印哨兵的类。
    # 与 `call interp-probe rt=java`（夹具：假解释器回显 execPath）互补：本桩用**真机 java**
    # 证明 java runtime 真能跑（临时脚本扩展名必须 .java；.jsh 会被当类名 → ClassNotFoundException）。
    # 脚本含换行 → JSON 字符串转义为 \n，executor 原样写入临时 .java 文件。
    if "call java-real" in t:
        script = ("public class CkJavaE2E {\n"
                  "  public static void main(String[] args) {\n"
                  "    System.out.println(\"CK-JAVA-OK\");\n"
                  "  }\n"
                  "}\n")
        return [("self_script_run", {"runtime": "java", "script": script,
                                     "tool_call_display_name": "java 真机探针"})]
    # P1 路径配置 B 效果确认（run_paths.py）：一次真实 script_run，runtime 由提示词携带
    # `rt=<runtime>`（缺省 js）。非 cmd → 打印自身可执行文件路径（`process.execPath`）——
    # 用于断言「被启动的解释器 == 配置的 *Path 值」；cmd → 环境变量探针（供断言
    # CHONK_CHROME / CHONKPILOT_INTERPRETERS 的子进程 env 注入）。脚本无空格（避免 argv 引号歧义）。
    # 位置：须早于下面 `call py` / `call cancel` 等宽关键词分支（"call interp-probe" 不含它们，
    # 仍前置求稳）。
    if "call interp-probe" in t:
        m = re.search(r"rt=([a-z0-9]+)", text)
        rt = m.group(1) if m else "js"
        if rt == "cmd":
            script = "echo CHROME=[%CHONK_CHROME%]&echo INTERPS=[%CHONKPILOT_INTERPRETERS%]"
        else:
            script = "console.log('INTERP='+process.execPath)"
        return [("self_script_run", {"runtime": rt, "script": script,
                                     "tool_call_display_name": "解释器探针"})]
    # ── 文件历史检查点链（run_hist_git.py；2026-09-27 批次③语义）──
    # ① 真实改文件（filesys_run RPL `histfrom=旧` → `histval=新`，`histabs=<绝对路径，正斜杠>`）
    #    → 触发 filesys.changed → 插件置脏 → 前置钩子 / 轮末补点打点（检查点链）。
    # ② history_restore 单文件回滚：`histrel=<workdir 相对路径>` + 可选 `histto=<负整数|commit id>`。
    # ③ 空 path → 拒绝；④ 目录 path（`histrel=<相对目录>`）→ 拒绝（单文件、禁止批量）。
    # ⑤ 「涉及文件变动」端到端（run_hist_git H6）：**同一轮内两次工具调用**——
    #    ① history_restore（执行时**同步置脏**，确定性，不依赖 fsnotify 去抖）
    #    → ② filesys_run RPL 改文件（其**前置钩子**读到脏位 → 该工具的 touch_files 决定是否打点）。
    if "call hist-dirty-write" in t:
        rel = _prompt_arg(text, "histrel")
        to = _prompt_arg(text, "histto")
        p = _prompt_arg(text, "histabs")
        frm = _prompt_arg(text, "histfrom")
        val = _prompt_arg(text, "histval")
        rargs = {"path": rel, "tool_call_display_name": "历史回滚"}
        if to:
            rargs["to"] = int(to) if re.fullmatch(r"-?\d+", to) else to
        return [
            ("self_history_restore", rargs),
            ("self_filesys_run", {"script": 'RPL #"%s" "%s" "%s"' % (p, frm, val),
                                  "tool_call_display_name": "历史打点"}),
        ]
    if "call hist-write" in t:
        p = _prompt_arg(text, "histabs")
        frm = _prompt_arg(text, "histfrom")
        to = _prompt_arg(text, "histval")
        return [("self_filesys_run", {"script": 'RPL #"%s" "%s" "%s"' % (p, frm, to),
                                      "tool_call_display_name": "历史打点"})]
    if "call hist-restore-nopath" in t:
        return [("self_history_restore", {"tool_call_display_name": "历史回滚"})]
    if "call hist-restore-dir" in t:
        return [("self_history_restore", {"path": _prompt_arg(text, "histrel"),
                                          "tool_call_display_name": "历史回滚"})]
    if "call hist-restore" in t:
        args = {"path": _prompt_arg(text, "histrel"), "tool_call_display_name": "历史回滚"}
        to = _prompt_arg(text, "histto")
        if to:
            args["to"] = int(to) if re.fullmatch(r"-?\d+", to) else to
        return [("self_history_restore", args)]
    # 普通工具场景
    # R-11：文件操作参数须为绝对路径或以 ~/ 开头（相对路径会被执行器拒绝），
    # 故用 ws 绝对路径（与 run_llm.py 期望的 "hello from a.txt" 对应）。
    if "call tool" in t and "without" not in t:
        return [("self_file_read", {"files": [{"path": os.path.join(SCRIPTS_WS, "a.txt")}]})]
    if "call async-cmd" in t:
        # C13/C18：子会话命令转后台（_async=true）→ 20s 长运行，使 batch 根任务保持运行中
        #（验证 task-stop 级联 / 任务树实时状态；script_run = 产品命令执行工具）
        return [("self_script_run", {"runtime": "cmd", "script": "ping -n 20 127.0.0.1",
                                "_async": True, "tool_call_display_name": "测试"})]
    if "call py-async" in t:
        # script_run 异步（async=always 转后台）；结果回填 = turn 挂起等后台完成回报
        # （run_tools.py 断言 tool-pair async + tool-result 终态，不再由 LLM 主动轮询取结果）
        return [("self_script_run", {"runtime": "cmd", "script": "echo py-slow",
                                "_async": True, "tool_call_display_name": "测试"})]
    if "call py" in t:
        # script_run（cmd）同步（快命令立即完成；真机 python 解释器未配置，改用 cmd 验证同链路）
        return [("self_script_run", {"runtime": "cmd", "script": "echo py-ok",
                                "tool_call_display_name": "测试"})]
    if "call cancel" in t:
        # 长命令（script_run 同步执行，20s）：等待期间用户 cancel（C11/五.8）
        return [("self_script_run", {"runtime": "cmd", "script": "ping -n 20 127.0.0.1",
                                "tool_call_display_name": "测试"})]
    if "call ask-multi" in t:
        # user_ask 多选 + 推荐（附录 A 扩展）：multi=true + recommended 标注推荐项
        return [("self_ask_user", {"question": "测试多选：选择要执行的操作？",
                              "options": ["构建", "测试", "部署"],
                              "multi": True, "recommended": ["测试"], "tool_call_display_name": "测试"})]
    if "call ask" in t:
        # user_ask 工具（testplan 六章 C14 双路由）：弹窗回答回填后不再含关键词 → 普通回复。
        # options=[] 让前端判定为自由文本（custom=true），弹窗显示 custom 输入框。
        return [("self_ask_user", {"question": "测试问题：是否继续？", "options": []})]
    if "call resume" in t:
        # 断链恢复（testplan 六章 C15）：中断请求由 executor 的 resumePartial 补发"继续"
        return None
    return None


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        pass

    def _read_body(self):
        n = int(self.headers.get("Content-Length", 0))
        return json.loads(self.rfile.read(n)) if n else {}

    def _send_json(self, obj):
        data = json.dumps(obj).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    # 测试辅助：GET /reset 重置一次性状态机（_EMPTY_ONCE）与请求记录，
    # 使"空回复"链用例跨次运行确定性（避免状态残留导致交替行为）。
    def do_GET(self):
        if self.path == "/reset":
            _EMPTY_ONCE["active"] = False
            _LAST_REQ.clear()
            _LAST_BODY.clear()
            _REQ_SEQ["n"] = 0
            self._send_json({"ok": True})
            return
        if self.path == "/last":
            # 最近一次请求的配置字段与证据（run_llm_config / run_llm_fields 断言用）
            self._send_json(_LAST_REQ)
            return
        if self.path == "/lastbody":
            # 最近一次请求体的多模态摘要（P2-8 图片给 LLM 端到端断言用）
            self._send_json(_LAST_BODY)
            return
        self._send_json({"error": "not found", "path": self.path})

    def do_POST(self):
        # responses 分支（protocol=responses）：POST {base}/responses
        if self.path.endswith("/responses"):
            self._handle_responses()
            return
        if not (self.path.endswith("/chat/completions")):
            self._send_json({"error": "not found", "path": self.path})
            return
        body = self._read_body()
        messages = body.get("messages", [])
        text = last_user_text(messages)
        stream = body.get("stream", False)
        # 记录请求证据（GET /last）：含既有三字段 + path/protocol/reasoning_effort/
        # authorization/system/has_tool_result/n_requests（见 _request_summary）
        _request_summary(self.path, body, self.headers.get("Authorization", ""))

        # C15 断链恢复：发送部分文本后强制 RST 中断（connection reset → 客户端识别为
        # 重试类错误 → runner resumePartial 追加"继续"续写）。干净 FIN 会被客户端当作
        # 正常流结束（无错误），无法触发断链恢复。
        if "call break" in text:
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            chunk = {"choices": [{"delta": {"role": "assistant", "content": "PARTIAL-TEXT-"},
                                  "finish_reason": None}]}
            self.wfile.write(f"data: {json.dumps(chunk)}\n\n".encode("utf-8"))
            self.wfile.flush()
            try:
                import struct
                self.connection.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack('ii', 1, 0))
            except OSError:
                pass
            try:
                self.connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            self.connection.close()
            return

        # S21 空回复（llm-error-handling S21）：流正常关闭（finish_reason=stop）但 content 为空。
        # 首次 "call empty" → 空回复；"继续"重发同一文本 → 消费 _EMPTY_ONCE 返回正常回复。
        if "call empty" in text:
            if _EMPTY_ONCE["active"]:
                _EMPTY_ONCE["active"] = False
                reply = f"mock-reply: {text}"
                if stream:
                    self._send_stream([
                        {"choices": [{"delta": {"role": "assistant", "content": reply[: len(reply) // 2]},
                                      "finish_reason": None}]},
                        {"choices": [{"delta": {"content": reply[len(reply) // 2:]},
                                      "finish_reason": "stop"}]},
                    ])
                else:
                    self._send_json({"choices": [{"message": {"role": "assistant", "content": reply},
                                                  "finish_reason": "stop"}]})
                return
            _EMPTY_ONCE["active"] = True
            self._send_stream([
                {"choices": [{"delta": {"role": "assistant", "content": ""}, "finish_reason": None}]},
                {"choices": [{"delta": {}, "finish_reason": "stop"}]},
            ])
            return

        calls = route_tool_calls(text)
        if calls is not None:
            if stream:
                self._send_stream(tool_calls_chunks(calls))
            else:
                self._send_json(tool_calls_message(calls))
            return

        # 文本覆盖回复（llm_run DSL 的 planner 步骤：输出 JSON 数组文本）
        override = reply_override(text)
        if override is not None:
            if stream:
                self._send_stream([
                    {"choices": [{"delta": {"role": "assistant", "content": override},
                                  "finish_reason": "stop"}]},
                ])
            else:
                self._send_json({"choices": [{"message": {"role": "assistant", "content": override},
                                              "finish_reason": "stop"}]})
            return

        # 分页/截断数据（FP L125/127/128/149）：返回 ~80KB 文本——单条超后端 has_more
        # 阈值触发「+更多」展开；多轮累积超 GetTurnsPaginated 4MB 目标触发 has_more 分页。
        if "call big" in text:
            big = "B" * (80 * 1024)
            if stream:
                step = len(big) // 4
                chunks = [
                    {"choices": [{"delta": {"role": "assistant", "content": big[i:i + step]},
                                  "finish_reason": None}]}
                    for i in range(0, len(big), step)
                ]
                chunks.append({"choices": [{"delta": {}, "finish_reason": "stop"}]})
                self._send_stream(chunks)
            else:
                self._send_json({"choices": [{"message": {"role": "assistant", "content": big},
                                              "finish_reason": "stop"}]})
            return

        reply = f"mock-reply: {text}"
        if stream:
            self._send_stream([
                {"choices": [{"delta": {"role": "assistant", "content": reply[: len(reply) // 2]},
                              "finish_reason": None}]},
                {"choices": [{"delta": {"content": reply[len(reply) // 2:]},
                              "finish_reason": "stop"}]},
            ])
            return
        self._send_json({
            "choices": [{"message": {"role": "assistant", "content": reply},
                         "finish_reason": "stop"}],
        })

    def _send_stream(self, chunks):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        for c in chunks:
            self.wfile.write(f"data: {json.dumps(c)}\n\n".encode("utf-8"))
            self.wfile.flush()
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

    def _handle_responses(self):
        """Responses API 分支（protocol=responses，POST {base}/responses）。

        请求语义（对齐 llm_responses.go）：model / instructions（system）/ input（item 列表）/
        stream / max_output_tokens / reasoning{effort} / tools（扁平定义）。
        响应语义：SSE `response.output_text.delta`（文本增量）+ `response.completed`（终态）；
        **不发 `data: [DONE]`**（Responses 文档明确无该标记 → 顺带验证客户端无 [DONE] 收尾）。
        回复文本 = "mock-responses-reply: <最后一段文本>"（供套件断言协议命中 + 内容回流）。
        """
        body = self._read_body()
        _request_summary(self.path, body, self.headers.get("Authorization", ""))
        reply = "mock-responses-reply: " + _responses_last_user_text(body)

        def ev(obj):
            self.wfile.write(("data: %s\n\n" % json.dumps(obj)).encode("utf-8"))
            self.wfile.flush()

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        half = len(reply) // 2
        for seq, piece in enumerate((reply[:half], reply[half:])):
            if not piece:
                continue
            ev({"type": "response.output_text.delta", "sequence_number": seq, "delta": piece})
        ev({"type": "response.completed", "response": {"status": "completed"}})


class MockLLMServer(ThreadingHTTPServer):
    """listen backlog 放大到 128（默认 5）：并行沉淀一轮 8~9 个请求时不再拒连。"""

    daemon_threads = True
    request_queue_size = 128


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8901
    # ThreadingHTTPServer：流式长连接与子任务并发请求互不阻塞（HTTPServer 单线程会
    # 在处理一个 keep-alive/SSE 连接期间阻塞新连接，导致 executor 请求超时/被重置）。
    # request_queue_size 必须放大：默认 backlog=5，而记忆沉淀一轮会**并行**打 8~9 个请求，
    # 溢出的连接在 connect 阶段被拒（ECONNREFUSED）→ 表现为"某几个类别被静默跳过"。
    # 注意该值在 TCPServer.__init__ 里 server_bind 时生效，构造后再赋值没用 → 用子类。
    srv = MockLLMServer(("127.0.0.1", port), Handler)
    print(f"mock LLM listening on 127.0.0.1:{port}", flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()

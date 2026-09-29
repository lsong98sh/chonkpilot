# -*- coding: utf-8 -*-
"""L4 套件：vfts「文档索引（Office/PDF）」端到端（SRCH-007-S04~S07）。

覆盖（每条 = A 数据面回读 + B 可观测效果；B 恒以可观测证据收口）
  D1 开 `vfts.docs` + 真转换器在跑 → `vfts.status`（`docsEnabled`/`docsService=running`/`docsPort`）
     + `data-filelist-list` 含文档类；**检索命中正文**（`self_vfts_query` 经既有 `chonk.mcp-tools-call`）。
  D2 `data-filelist-list` 文件集合与「非文档 exts ∪ 文档类」**字面全等**（一个不多一个不少）。
  D3 **二次（强制）重建走缓存**：缓存文件集合与 mtime 不变（未重转）→ 证明命中缓存不调 HTTP。
  D4 **停掉转换服务**后强制重建 → 文档类被跳过（清单不再含文档类、`vfts.status.docsService=absent`、
     正文检索不再命中）；**不报错、不中断整库索引**。

隔离与环境（51 §5/§6-8）：自起 GUI（动态端口 + 临时 work-dir/data-dir/HOME）+ `_h.suite_config_guard`；
真转换器由本脚本**自行拉起/停止**（插件**只探测、不 spawn**）——产物取 `dist/desktop/mcps/markitdown/`。
转换器状态文件写在其 exe 同目录 → 正是插件探测的 `<exeDir>/mcps/markitdown/state.json`。

运行：python run_docs_gate.py
前置：`dist/desktop/mcps/markitdown/markitdown-mcp.exe` **且** vfts 引擎为**含 docs 支持的新构建**
      （否则 preflight 明确报「未构建」并 SKIP，不算失败）。
"""

import json
import os
import subprocess
import sys
import time
import urllib.request
import zipfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

# ── 路径与产物 ────────────────────────────────────────────
HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", "..", "..", ".."))
CONV_DIR = os.path.join(REPO, "dist", "desktop", "mcps", "markitdown")
CONV_EXE = os.path.join(CONV_DIR, "markitdown-mcp.exe")
CONV_STATE = os.path.join(CONV_DIR, "state.json")

VSTATUS_KEY = "vfts.status"
VTOOL = "self_vfts_query"

DOC_NAME = "中文文档.docx"
TXT_NAME = "note.txt"
DOC_SENT = "CK-DOC-OFFICE"          # 正文 ASCII 哨兵（稳）
DOC_CN = "全文索引"                  # 正文中文词（任务要求：命中文词）

WS = _h.tmp_dir("ck-docsgate-ws-")
DD = _h.tmp_dir("ck-docsgate-dd-")
HOME = _h.tmp_home()
PORT = _h.free_port()

_conv = None                        # 转换器子进程（本脚本拉起，结束回收）


def make_docx(path, paras):
    """手写最小 docx（[Content_Types] + _rels + word/document.xml），无第三方依赖。"""
    ct = (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
        '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
        '<Default Extension="xml" ContentType="application/xml"/>'
        '<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>'
        "</Types>"
    )
    rels = (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
        '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>'
        "</Relationships>"
    )
    body = "".join(
        '<w:p><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>' % p for p in paras
    )
    doc = (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">'
        "<w:body>%s</w:body></w:document>" % body
    )
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
        z.writestr("[Content_Types].xml", ct)
        z.writestr("_rels/.rels", rels)
        z.writestr("word/document.xml", doc)


# ── 转换器生命周期（本脚本负责；插件只探测）──────────────
CREATE_NO_WINDOW = 0x08000000  # 子进程不弹控制台窗口


def start_converter():
    """拉起真转换器并等状态文件就绪（onedir 冷启动数秒 → 等 40s 留足余量）。"""
    global _conv
    _conv = subprocess.Popen([CONV_EXE], cwd=CONV_DIR, creationflags=CREATE_NO_WINDOW,
                             stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    deadline = time.time() + 40
    while time.time() < deadline:
        try:
            with open(CONV_STATE, encoding="utf-8") as f:
                st = json.load(f)
            if int(st.get("port") or 0) > 0:
                return st
        except Exception:
            pass
        time.sleep(0.5)
    raise TestError("转换器未在 40s 内写出状态文件：%s" % CONV_STATE)


def _read_state():
    """读状态文件 → (port, pid)（缺失/损坏 → (0, 0)）。"""
    try:
        with open(CONV_STATE, encoding="utf-8") as f:
            st = json.load(f)
        return int(st.get("port") or 0), int(st.get("pid") or 0)
    except Exception:
        return 0, 0


def _kill_tree(pid):
    """杀**整棵进程树**。

    onedir 形态下 Popen 即真实服务进程（无引导子进程）；仍统一用 taskkill /T /F 连子进程
    一并杀（对两种形态都安全且幂等），避免残留进程持有端口造成 D4「停服降级」假阳性。
    """
    if not pid:
        return
    try:
        subprocess.run(["taskkill", "/PID", str(pid), "/T", "/F"],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                       creationflags=CREATE_NO_WINDOW)
    except Exception:
        pass


def _pids_on_port(port):
    """按端口反查 LISTENING 的 pid（netstat -ano；pid+port 双重确认的兜底）。"""
    out = set()
    if port <= 0:
        return out
    try:
        # 注意：中文 Windows 的 netstat 输出是 GBK（如"活动连接"），
        # 用 text=True 会按 UTF-8 解码失败 → 读线程崩溃、stdout 为 None、
        # 端口反查静默失效。故取 bytes 后显式 errors="replace" 解码。
        r = subprocess.run(["netstat", "-ano", "-p", "TCP"], capture_output=True,
                           creationflags=CREATE_NO_WINDOW)
    except Exception:
        return out
    for line in (r.stdout or b"").decode("utf-8", "replace").splitlines():
        parts = line.split()
        if (len(parts) >= 5 and parts[0].upper() == "TCP" and parts[3].upper() == "LISTENING"
                and parts[1].endswith(":%d" % port)):
            try:
                out.add(int(parts[4]))
            except ValueError:
                pass
    return out


def _health_ok(port, timeout=1.0):
    """GET /vfts/health 是否可达且 ok=true。"""
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/vfts/health" % port, timeout=timeout) as r:
            return json.loads(r.read().decode("utf-8")).get("ok") is True
    except Exception:
        return False


def stop_converter():
    """停止转换器并清理状态文件（含 token，绝不留存）。

    停止 = 杀**进程树**（引导 pid + 状态文件 pid + 端口反查 pid 三方覆盖），随后轮询
    `/vfts/health` 确认端口不再可达（超时报错）—— 否则残留进程会让 D4「停服降级」假阳性。
    """
    global _conv
    port, state_pid = _read_state()
    had_live = _conv is not None and _conv.poll() is None
    pids = set()
    if had_live:
        pids.add(_conv.pid)
    if state_pid > 0:
        pids.add(state_pid)
    pids |= _pids_on_port(port)
    for pid in pids:
        _kill_tree(pid)
    _conv = None
    # 状态文件先删（含 token），避免陈旧 pid/port 误导后续探测
    for p in (CONV_STATE, CONV_STATE + ".tmp"):
        try:
            os.remove(p)
        except OSError:
            pass
    # 轮询确认端口已释放
    if port > 0:
        deadline = time.time() + 15
        while time.time() < deadline:
            if not _health_ok(port):
                return
            time.sleep(0.3)
        raise TestError("转换器进程树未停止：/vfts/health 仍可达（port=%d，残留 pid=%s）"
                        % (port, sorted(_pids_on_port(port))))
    if had_live:
        print("[warn] 转换器状态文件缺失，无法按端口确认已停止（已按 pid 杀树）", flush=True)


def preflight():
    """产物/构建就绪检查：缺失 → 明确报「未构建」，SKIP（不算失败）。"""
    if not os.path.isfile(CONV_EXE):
        print("[preflight] SKIP：转换器产物缺失（先跑 .\\src\\mcps\\markitdown\\build-mcps.ps1）→ %s" % CONV_EXE)
        return False
    return True


def main():
    if not preflight():
        print("RESULT: True (SKIPPED: 转换器未构建)")
        return 0

    # 夹具：中文 docx（正文含哨兵 + 中文词）+ 普通 txt
    os.makedirs(WS, exist_ok=True)
    make_docx(os.path.join(WS, DOC_NAME),
              ["ChonkPilot 文档索引夹具", "%s %s" % (DOC_SENT, DOC_CN)])
    with open(os.path.join(WS, TXT_NAME), "w", encoding="utf-8") as f:
        f.write("plain note\n")

    st = start_converter()
    print("[env] conv port=%s pid=%s ; ws=%s data=%s gui=%d（临时目录，结束即删）"
          % (st.get("port"), st.get("pid"), WS, DD, PORT), flush=True)

    global c
    _g = _h.start_gui(port=PORT, work_dir=WS, data_dir=DD, home=HOME)
    c = _g.client
    _h.suite_config_guard(c)
    _h.ensure_locale(c)
    c.wait_ready()

    TOTAL = []
    try:
        TOTAL.append(("D1 开 vfts.docs + 真转换器 → 文档入索引且正文可检索", case_d1_docs_indexed))
        TOTAL.append(("D2 file_list 集合字面全等（非文档 exts ∪ 文档类）", case_d2_filelist))
        TOTAL.append(("D3 二次强制重建走缓存（缓存文件与 mtime 不变）", case_d3_cache))
        TOTAL.append(("D4 停转换服务后重建 → 文档类被跳过且 docsService=absent", case_d4_service_down))

        ok = total = 0
        c.console(clear=True)
        # 前置：写索引配置并等首个 ready
        if not _bootstrap_ready():
            stop_converter()
            print("RESULT: True (SKIPPED: vfts 引擎无 docs 支持 / 未就绪 → 需重新构建)")
            return 0
        for name, fn in TOTAL:
            total += 1
            ok += run_case(name, fn)
        print("\n文档索引（vfts docs）：%d/%d 通过" % (ok, total), flush=True)
        print("RESULT: %s" % (ok == total), flush=True)
        return 0 if ok == total else 1
    finally:
        stop_converter()


# ══════════════════════════════════════════════════════════
# 数据面辅助
# ══════════════════════════════════════════════════════════

def prj():
    r = c.req("data-prj-config-list", {}) or {}
    return r.get("list") or {}


def prj_save(k, v):
    return c.req("data-prj-config-save", {"data": {"key": k, "value": v}})


def vstatus():
    raw = prj().get(VSTATUS_KEY) or ""
    try:
        return json.loads(raw)
    except Exception:
        return {}


def filelist_rels():
    r = c.req("data-filelist-list", {}) or {}
    out = set()
    for e in (r.get("list") or []):
        p = str(e.get("path") or "")
        if p == "":
            continue
        try:
            out.add(os.path.relpath(os.path.normpath(p), os.path.normpath(WS)).replace("\\", "/"))
        except ValueError:
            out.add(p.replace("\\", "/"))
    return out


def wait_pred(pred, desc, max_wait=120, interval=0.4):
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        last = pred()
        if last:
            return last
        time.sleep(interval)
    raise TestError("%s 超时（%ss）；末次=%r" % (desc, max_wait, last))


def gw_call(tool, args):
    payload = {"name": tool, "arguments": dict(args or {}), "tool_call_display_name": "docs探针"}
    res = c.req("chonk.mcp-tools-call", payload, timeout=60000)
    if isinstance(res, dict) and res.get("isError"):
        raise TestError("工具 %s 返回 isError：%r" % (tool, res))
    parts = []
    for b in ((res or {}).get("content") or []):
        if isinstance(b, dict) and b.get("type") == "text":
            parts.append(b.get("text") or "")
    if not parts:
        raise TestError("工具 %s 无文本返回：%r" % (tool, res))
    return json.loads("\n".join(parts))


def doc_cache_dir():
    return os.path.join(WS, ".chonkpilot", "vfts", "doc_text")


def cache_snapshot():
    """缓存文件 → mtime（不存在 → {}）。用于「未重转」证据。"""
    d = doc_cache_dir()
    out = {}
    if not os.path.isdir(d):
        return out
    for name in os.listdir(d):
        try:
            out[name] = os.path.getmtime(os.path.join(d, name))
        except OSError:
            pass
    return out


def _bootstrap_ready():
    """写索引配置（enable-vfts + vfts.exts + vfts.docs）并等首个 ready。

    返回 True = 引擎支持 docs 且就绪；False = 引擎为旧构建（status 无 docsEnabled）→ 上层 SKIP。
    """
    prj_save("vfts.exts", ".txt")
    prj_save("vfts.doc-max-mb", "50")
    prj_save("enable-vfts", "true")
    prj_save("vfts.docs", "true")

    def _ready():
        s = vstatus()
        return s if s.get("state") == "ready" else None

    wait_pred(_ready, "vfts 首次索引 ready", 180)
    s = vstatus()
    if "docsEnabled" not in s:
        print("[preflight] vfts.status 无 docsEnabled 字段 → 引擎为旧构建（需重新构建 build-vfts）", flush=True)
        return False
    return True


def _force_rebuild_via(vkey, vval):
    """改一个 docs 相关键触发**强制重建**（同一去抖窗口）；等回到 ready。"""
    prj_save(vkey, vval)
    time.sleep(1.2)  # 让去抖窗口（400ms）+ 进入 indexing 先发生，避免读到"重建前的旧 ready"
    wait_pred(lambda: vstatus().get("state") == "ready", "强制重建 ready（%s=%s）" % (vkey, vval), 180)


# ══════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════

def case_d1_docs_indexed():
    """D1：docs 开 + 服务在跑 → status 就绪 + 清单含 docx + 正文可检索（中文词 + 哨兵）。"""
    s = wait_pred(lambda: vstatus() if (
        vstatus().get("docsService") == "running" and vstatus().get("state") == "ready") else None,
        "vfts.status docsService=running & ready", 120)
    if not s.get("docsEnabled"):
        raise TestError("docsEnabled 应为 true：%r" % s)
    if not s.get("docsPort"):
        raise TestError("docsPort 应有端口：%r" % s)
    wait_pred(lambda: DOC_NAME in filelist_rels(), "清单含 %s" % DOC_NAME, 60)

    hit_cn = gw_call(VTOOL, {"match": DOC_CN})
    hit_sent = gw_call(VTOOL, {"match": DOC_SENT})
    print("[D1] docsService=%s docsPort=%s；命中(中文)=%d 命中(哨兵)=%d"
          % (s.get("docsService"), s.get("docsPort"),
             len(hit_cn.get("hits") or []), len(hit_sent.get("hits") or [])), flush=True)
    if not (hit_sent.get("hits") or []):
        raise TestError("正文哨兵未命中（文档未被抽取入索引）：%r" % hit_sent)
    if not (hit_cn.get("hits") or []):
        raise TestError("正文中文词 %r 未命中：%r" % (DOC_CN, hit_cn))
    paths = [h.get("path") or "" for h in (hit_sent.get("hits") or [])]
    if not any(p.replace("\\", "/").endswith(DOC_NAME) for p in paths):
        raise TestError("命中不含 %s：%r" % (DOC_NAME, paths))


def case_d2_filelist():
    """D2：file_list 集合 == {note.txt} ∪ {中文文档.docx}（字面全等）。"""
    want = {TXT_NAME, DOC_NAME}
    got = wait_pred(lambda: filelist_rels() if filelist_rels() >= want else None,
                    "file_list 收敛到期望集", 60)
    if got != want:
        raise TestError("file_list 应字面全等 %r，实际 %r（多=%r 少=%r）"
                        % (want, got, got - want, want - got))
    print("[D2] file_list == %r" % sorted(got), flush=True)


def case_d3_cache():
    """D3：二次强制重建 → 缓存文件集合与 mtime **不变**（命中缓存，未重转）。"""
    before = cache_snapshot()
    if not before:
        raise TestError("缓存目录为空：%s（首次索引应写缓存）" % doc_cache_dir())
    _force_rebuild_via("vfts.doc-max-mb", "51")
    after = cache_snapshot()
    if set(after) != set(before):
        raise TestError("缓存文件集合变化（应复用同名键）：before=%r after=%r" % (before, after))
    changed = [k for k in before if abs(after.get(k, 0) - before[k]) > 0.001]
    if changed:
        raise TestError("缓存文件被重写（= 重转了）：%r" % changed)
    print("[D3] 缓存 %d 个文件集合与 mtime 均未变 → 二次重建走缓存" % len(before), flush=True)


def case_d4_service_down():
    """D4：停掉转换服务 → 强制重建 → 文档类被跳过（清单不含 docx）+ docsService=absent。"""
    stop_converter()
    time.sleep(1)
    _force_rebuild_via("vfts.doc-max-mb", "52")
    got = wait_pred(lambda: filelist_rels() if DOC_NAME not in filelist_rels() else None,
                    "清单不再含文档类（服务不可用 → 整批跳过）", 90)
    if TXT_NAME not in got:
        raise TestError("非文档文件应保留在清单：%r" % got)
    s = vstatus()
    if s.get("docsService") != "absent":
        raise TestError("docsService 应为 absent：%r" % s)
    sent = gw_call(VTOOL, {"match": DOC_SENT})
    if sent.get("hits"):
        raise TestError("服务停止后文档正文不应再命中：%r" % sent)
    print("[D4] docsService=absent；file_list=%r；正文命中=%d（已跳过）"
          % (sorted(got), len(sent.get("hits") or [])), flush=True)


c = None  # 由 main 赋值（供上面的辅助函数引用）


if __name__ == "__main__":
    sys.exit(main())

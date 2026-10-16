# 基本信息
```yaml
name: dsl_run
title: DSL 统一编排
async: always
category: server
hot: true
```

# 摘要
**统一编排入口**——把「文件 / 浏览器 / 桌面 / LLM 委派」四类动作写进**一份 DSL 脚本**，交给独立受保护执行器进程（`chonkpilot-dsl-executor.exe`）执行：文件、浏览器、桌面动作全部在该进程内完成（进程级 agentbox 沙箱），LLM 步骤回到 server 经 MQ 执行子轮次后再回传。适用于"循环/并行批处理 + 分支判断 + 结果落盘"的编排场景；原 `file_read`/`file_find`/`file_diff` 三个单动作工具已**收纳**为本工具的 `FILE_READ`/`FILE_FIND`/`FILE_DIFF`（查询动作），其余单次便捷调用仍可用对应单动作工具（`web_fetch`/`script_run` 等）。

DSL 语法与既有批处理脚本共用同一引擎：行式指令（每行一条；`#` 或 `###` 开头为注释；文本一律双引号字符串并支持 `{{}}` 插值），核心流控 `SET`/`IF`/`LOOP`/`PARALLEL`/`BREAK`/`CONTINUE`/`EXIT`/`END` 可任意嵌套（深度 ≤ 8）。

**四种动作域（按前缀区分动词）**：
- `FILE_*`：文件域动作——建/改/删/移/复制/查询（对应 `filesys_run` 全部动作）：
  - 编辑：`FILE_RPL` / `FILE_APD` / `FILE_PTC` / `FILE_INS` / `FILE_DEL` / `FILE_MOV` / `FILE_CPY`
  - 查询（只读）：
    - `FILE_READ #"path" [start=N] [limit=N] [tail=N] [ranges=1:10,20:30] [info] [line_numbers]`：**第一行为元数据**（path/md5/encoding/lines/size），**第二行起为文件内容**；`info` 模式只返回元数据行。
    - `FILE_DIFF #"a" #"b"`：统一 diff 文本；可给多对（偶数个 `#"path"`，逐对产 diff，多对以空行分隔）。
    - `FILE_FIND #"dir" [depth=N] [glob="*.go"] [grep="re"] [output=tree|file|summary] [skip_dirs="a,b"] [ignore_files="*.log"] [from=YYYY-MM-DD] [to=YYYY-MM-DD]`：目录树/文件名 glob/内容 grep 查找。
  - 查询动作输出无 `=> 目标` 时并入作业汇总输出；**取用结果建议显式写 `=> $RETURN` / `=> #"file"` / `=> 变量`**（`filesys_run` 容器不回显动作文本，其查询动作**必须** `=> 目标`）。
- `WEB_*`：浏览器域动作——浏览器自动化与网页抓取（对应 `browser_run` 全部动作，`web_fetch` 并入本域）。
- `PC_*`：桌面域动作——桌面窗口/鼠标/键盘/截图等（对应 `desktop_run` 全部动作）。
- `LLM`：委派一次子 LLM（`LLM "agent" "提示词" "目的" [=> 目标]`）——由宿主经 MQ 执行独立子轮次（子会话，agent = 可委派对象名），结果回传脚本继续。三参均必填且支持 `{{}}` 插值。**返回值口径**：只含该子轮次的**最终回答正文**——**不含 reasoning（思维链）与 tool 相关内容**（工具调用轮的中间正文、工具结果一律剔除）。

**路径要求**：脚本内所有文件/目录引用（句柄 `#"path"`、LOOP/SET 数据源、访问器、`=> #"file"` 目标、`IF exist` 路径）与 `file` 参数必须是**绝对路径、以 `~/` 开头的用户目录路径或以 `!/` 开头的临时目录路径**；相对路径报错。项目内路径请用宿主注入的只读变量显式拼接：`{{env.CHONKPILOT_WORKDIR}}` / `{{env.CHONKPILOT_DATADIR}}` / `{{env.CHONKPILOT_TEMPDIR}}` / `{{env.CHONKPILOT_EXEDIR}}` / `{{env.CHONKPILOT_PROJECT}}`。

**`$RETURN` 结果通道（两态）**：宿主注入的**只写**保留变量。用 `SET 值 => $RETURN` 或 `动作 … => $RETURN` 写入（**累计追加**，非覆盖；字符串直拼、数字/布尔→文本、数组/对象→JSON 文本；段间换行分隔）。累计 ≤ 64K（65536 字节）时作业结果返回**内容**（inline）；超过即转**文件流式追加**并返回**文件名 + 大小**（file，落 `!/` 临时根、命名 `dsl-return-<作业id>.md`）。脚本内**不可读** `$RETURN`（`{{$RETURN}}` / `SET $RETURN => x` 一律报错）。脚本中出现过 `=> $RETURN` 时以它为准，未出现则回落作业汇总。

示例：
```
### 文件域 + LLM 委派 + $RETURN 汇总（路径用 {{env.CHONKPILOT_WORKDIR}} 拼绝对路径）
FILE_READ #"{{env.CHONKPILOT_WORKDIR}}/tasks.json"
LOOP item=#"{{env.CHONKPILOT_WORKDIR}}/tasks.json".array
   IF item.done != true
      LLM "后端开发" "实现 {{item.name}}" "实现 {{item.name}}" => $RETURN
      SET item.done => true
   END
END
```

# 参数
```json
{
  "properties": {
    "file": {
      "description": "已保存的 DSL 脚本文件路径（与 script 二选一；必须为绝对路径、以 ~/ 开头的用户目录路径或以 !/ 开头的临时目录路径，相对路径报错）",
      "type": "string"
    },
    "script": {
      "description": "DSL 脚本文本（与 file 二选一；四种动作域前缀 FILE_*/WEB_*/PC_*/LLM 与流控见上）",
      "type": "string"
    },
    "timeout": {
      "description": "作业超时秒数（可选；\u003c=0 或缺省 = 不设作业级超时，靠调用方取消）",
      "type": "number"
    }
  },
  "type": "object"
}
```


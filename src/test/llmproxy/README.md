# chonkpilot-llmproxy

本地 LLM 转发代理（**纯 Python 标准库**，Python ≥ 3.10，零依赖）：把本机请求转发到真实 LLM provider，并把**收发的完整内容**写进一个日志文件，方便实时 `tail` 观察。

> ⚠️ 安全提示：日志包含**完整请求/响应正文**（可能含提示词、代码、业务数据）。**请勿提交到版本库、公共网盘或共享给他人。**

## 1. 如何启动

```powershell
# 监听 127.0.0.1:5710 → 转发到 DeepSeek，日志追加写入 ./llmproxy.log
python llmproxy.py --target https://api.deepseek.com/v1

# 自定义端口与日志文件
python llmproxy.py --target https://api.deepseek.com/v1 --port 5710 --out D:\logs\llmproxy.log
```

参数仅 3 个：`--target`（必填，真实 base URL）、`--port`（默认 `5710`，固定监听 `127.0.0.1`）、`--out`（单个日志文件路径，默认 `./llmproxy.log`，追加写）；上游超时固定 600s。`Ctrl+C` 退出。

## 2. 如何接入

把业务侧 LLM 配置的 `base_url` 改成代理地址，**路径保持不变**：

| 项 | 原值 | 改后 |
|----|------|------|
| `base_url` | `https://api.deepseek.com/v1` | `http://127.0.0.1:5710` |
| 请求路径 | `/chat/completions` | `/chat/completions`（不变） |
| `api_key` | 真实 key | **不变**（原样透传给上游） |

即客户端请求 `http://127.0.0.1:5710/chat/completions`，代理按 `--target` 的 base 路径 + 客户端路径拼接为 `https://api.deepseek.com/v1/chat/completions` 转发。注意 `--target` 里带不带 `/v1` 要和上游一致，客户端侧 **不要再**带 `/v1`。

## 3. 如何看日志

```powershell
Get-Content .\llmproxy.log -Wait      # 实时跟随（追加写 + 每写即 flush）
```

日志只记「内容」，同一文件、追加写：每次请求写 `>>> REQ <ISO时间>` + 原始请求体 + 空行；每次响应写 `<<< RES <ISO时间>` + 上游原始响应内容（SSE 逐块即时落盘）+ 空行。代理不记录请求头（`Authorization` 天然不落盘）；转发时强制 `Accept-Encoding: identity` 以换取上游明文。上游连接异常时写一行 `!!! ERR <msg>`。

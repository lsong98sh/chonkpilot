# ChonkPilot

AI 智能体自我循环工程框架 — Agent Self-Loop Engineering

## 概述

ChonkPilot 是一个以**智能体自我循环工程**为核心的 AI 自动化框架。它不只调用 LLM，更通过可编程的循环控制机制（上下文压缩、失败重试、工具链编排）让智能体自主完成任务。

## 核心特色

- **智能体自我循环 (Agent Self-Loop Engineering)** — 智能体自主决策 → 执行工具 → 观察结果 → 再次决策，循环直到任务完成。循环行为（何时重试、何时压缩、何时停止）完全可编程
- **★ 批量 LLM 编排引擎 (batch_llm)** — Pipeline JSON 定义海量独立 LLM 任务，滑动窗口并发执行，自动循环直到全部完成。内置重试，实时状态写入 JSON，可监控进度
- **★ 独立 Executor** — executor 是独立二进制，可通过命令行/脚本调用，支持管道通信（pipe），可嵌入到 `.bat` / PowerShell 等自动化流程中
- **★ 所有提示词可配** — system prompt、agent prompt、tool usage 说明全部从嵌入或数据库读取，无需改代码即可调整行为
- **★ 所有行为可预测** — 上下文压缩策略、重试策略、超时阈值、并发限制等关键参数均有明确配置项，不依赖 LLM 随机发挥
- **★ 文件版本回溯** — 每次文件修改自动记录，可回滚任意历史版本
- **子智能体系统** — 主智能体可动态创建子智能体处理子任务，独立提示词、独立工具集、独立生命周期
- **LLM 自动选择** — 根据任务类型自动匹配最优 LLM（编码用快模型、分析用强模型、批量用低成本模型），无需手动切换
- **上下文压缩** — 长会话自动压缩历史，在保留关键信息的同时控制 token 消耗
- **失败重试** — LLM 网络错误、工具执行失败等场景内置重试机制，可配置重试次数和退避策略
- **多代理编排** — 编码、审查、测试、架构师、分析师多角色协作，每个代理可独立配置提示词和工具集
- **桌面自动化** — 真实鼠标键盘操控、窗口管理、截屏，直接操作 Windows 应用
- **浏览器控制** — 基于 Chromedp 驱动真实 Chrome/Edge，非 API 模拟
- **工具系统** — JSON Schema 声明式工具定义，扩展只需加 JSON + Handler

## 技术栈

| 层       | 技术                               |
|----------|-----------------------------------|
| 前端     | Vue 3, Element Plus, Vite          |
| 后端     | Go (Wails v2)                      |
| 数据库   | SQLite (原生 `database/sql`)       |
| 浏览器   | Chromedp (无头 Chrome/Edge)        |

## 快速开始

### 前置条件

- Go 1.22+
- Node.js 18+
- Wails CLI v2
- Chrome 或 Edge 浏览器

### 构建

```powershell
.\build.ps1          # 构建全部（IDE + executor）
.\build.ps1 ide      # 仅构建 IDE
.\build.ps1 executor # 仅构建 executor
```

## 许可

Copyright 2026 lsong98sh

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

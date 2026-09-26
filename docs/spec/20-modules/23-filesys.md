# 23 · src/lib/filesys（文件服务）

> 日期：2026-09-10 ｜ 状态：✅ 与代码一致
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [11-MQ与消息](../10-architecture/11-MQ与消息.md) · [20-gui](20-gui.md)
> 代码目录（D-28：`src/filesys/` → `src/lib/filesys/`）：`src/lib/filesys/`（`filesys.go` + `watcher.go`）

---

## 1. 职责与边界

- **一句话**：目录/文件**查询与写操作** + **变更监听**（fsnotify，60ms 去抖），经 `filesys.*` 消息面提供服务。
- **做**：列目录、读内容（截断）、创建/建目录/删除/改名/复制、watch/unwatch、`filesys.changed` 广播、`filesys.watch-error` 广播。
- **不做**：**不含 `/show`**（字节流豁免归 GUI）；不做沙箱/白名单（仅 `work_dir` 包含性校验）；不持库；无独立 exe（纯 lib）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | **纯 lib**（`package filesys`），无 `main`、无独立 exe |
| 启动方 | `src/gui/main.go`（GUI 单体）· `src/cli/main.go`（CLI 单体） |
| 依赖 | `src/lib`(mq) + `fsnotify` |
| 生命周期 | `New(bus)` → `Start()`（订阅）→ `Stop()` |

---

## 3. 对外接口（`filesys.*` 主题）

**订阅**（`bus.On(subject, 0, handler)`）：`filesys.list` · `filesys.content` · `filesys.create` · `filesys.mkdir` · `filesys.remove` · `filesys.rename` · `filesys.copy` · `filesys.watch` · `filesys.unwatch`。

| 类别 | 主题 | payload 要点 | 返回 |
|------|------|-------------|------|
| 查询 | `filesys.list` | `{work_dir, path}` | `{path, is_dir, children:[...]}` |
| 查询 | `filesys.content` | `{work_dir, path}` | `{path, kind, content, truncated}` |
| 写 | `filesys.create` / `mkdir` | `{work_dir, dir, name}` | `{ok, path}` |
| 写 | `filesys.remove` | `{work_dir, path}` | `{ok, path}`（`RemoveAll`） |
| 写 | `filesys.rename` | `{work_dir, path, new_name}` | `{ok, path}`（含分隔符=移动） |
| 写 | `filesys.copy` | `{work_dir, path, dest_dir?, new_name?}` | `{ok, path}` |
| 声明（无返回） | `filesys.watch` / `unwatch` | `{work_dir, path, recursive?}`（`recursive=true` 递归子目录） | — |
| 广播 | `filesys.changed` | `{work_dir, path, operation}` 或 `{work_dir, path, children}`（目录批次） | — |
| 广播 | `filesys.watch-error` | `{work_dir, error}` | — |

- 请求公共字段：`work_dir / path / dir / name / content / new_name / dest_dir`。
- 错误经 `v.Errors`（`{code, message}`），码：`forbidden` / `create_failed` / `mkdir_failed` / `remove_failed` / `rename_failed` / `copy_failed` / `exists`。

---

## 4. 内部控制流

### 4.1 路径校验（唯一强制边界）

```text
absPath(workDir, p)：空=workDir；相对 → Join(workDir, p)；再 withinWorkDir 校验
withinWorkDir(base, path)：filepath.Rel 后非 ".." 前缀
越界：读/写 handler 追加 forbidden；watch/unwatch 静默 return（不发错）
```

### 4.2 变更监听（`watcher.go`）

```text
watchManager：按 work_dir 组织 map[string]*dirWatcher
  watch → dirWatcher.add（recursive 时 walkAdd 递归子目录，跳过隐藏）
  fsnotify 事件 → enqueue（按路径去重）→ armTimer（60ms 重置）→ processBatch
      ├─ 单文件 → filesys.changed {path, operation(create|write|remove|rename)}
      └─ 目录批次 → filesys.changed {path, children}
  新目录 Create → 自动递归 add；**同时刷新其父目录 children 批次**（父树才能插入新目录节点）；处理中新事件 → 自动再调度一轮
```

- 去抖常量：`debounce = 60ms`。
- `recursive` 语义（T-08，2026-09-13 后）：`onWatch` **消费**载荷 `recursive` 并透传 `wm.watch(wd, p, req.Recursive, instanceID)`；`recursive=true` 时 `dirWatcher.add` 对目录 `walkAdd` 递归子目录（跳过隐藏），与新建目录自动递归一致。缺省 `false` = 仅监听本目录（白盒 `TestWatchRecursiveFlag`）。
- `watch-error`：**watcher 运行期** fsnotify 故障时广播 `{work_dir, error}`（`watcher.go` 的 `w.Errors` 路径）。

---

## 5. 数据结构与存储

- 无持久化存储（纯运行态）。
- 读内容上限 `maxTextBytes = 512KB`（超出 `truncated=true`）。
- `listDirNodes` 浅层列子项（**跳过隐藏/临时条目**：`.`/`~$` 前缀 + `~`/`-wal`/`-shm` 后缀，见 `filesys.go` `filterDirEntry`），路径统一 `/`，附 `size`/`mtime`。

---

## 6. 依赖

- **上游**：`src/lib`（mq）、`fsnotify`。
- **下游**：GUI 桥（订阅转发 `filesys.changed`）、前端（`api/file.js` → `filesys.<action>`）。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 组件化（独立 lib） | 由 GUI/CLI 以同一总线启动 | 替代旧 GUI 内 `dirWatcher` mtime 轮询中间态 |
| 去抖 60ms | 事件合并 | 高频写不刷屏，前端更新稳定 |
| 目录批次与单文件同主题 | 靠 `children` 有无区分 | 减少主题数量 |
| 越界只拦读/写 | watch 静默忽略 | watch 为声明（无返回），不报错更简单 |
| 唯一强制边界 | `withinWorkDir`（以消息 `work_dir` 为准） | 与 `security-*` 白名单无关（见 [14-安全域](../10-architecture/14-安全域-agentbox.md)） |

---

## 8. 场景与边界

- 同名冲突：`create` 命中已存在 → `exists`。
- 复制：`copy` 支持目录整树递归。
- 改名：`new_name` 含分隔符 = 移动；同目录 = 纯改名。
- 越界：读/写 → `forbidden`；watch/unwatch → 静默。
- `/show` 不在此服务（GUI `fileserver` 直读）。

---

## 9. 现状与待办

- ✅ 已组件化（fsnotify + 去抖），GUI 原 `dirWatcher` 已移除（`bridge.go` 头部确认）。
- 🔵 目标态 vs 现状：本文即目标态（独立 lib）；早期设计稿的"GUI 进程内中间态"表述已过时（该设计稿已归档）。
- ⚠️ 路径校验基于消息自带 `work_dir`，无沙箱（归 agentbox 🔵）。

---

## 10. 关联测试

`src/test/chonkpilot-filesys/unittest/filesys_test.go`（外部黑盒，内存总线直驱）：
`TestListEmptyAndPopulated` · `TestContentReadBackAndTruncated` · `TestWriteOps`（create/mkdir/remove/rename/copy/duplicate）· `TestWriteErrorsExistsForbidden` · `TestWatchChangedAndUnwatch`（真实 fsnotify + 60ms 去抖）。

模块内白盒（`src/filesys/filesys_test.go`，`package filesys`）：`TestListDirNodesFilter`（I-44 过滤）· `TestWatchNewDirBroadcastsParentBatch`（I-43 父批次）· `TestWatchRecursiveFlag`（T-08 `recursive` 消费）。

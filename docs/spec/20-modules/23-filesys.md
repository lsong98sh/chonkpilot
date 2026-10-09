# 23 · src/lib/filesys（文件服务）

> 状态：✅ 与代码一致
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [11-MQ与消息](../10-architecture/11-MQ与消息.md) · [20-gui](20-gui.md)
> 代码目录：`src/lib/filesys/`（`filesys.go` + `watcher.go`）

---

## 1. 职责与边界

- **一句话**：目录/文件**查询与写操作** + **变更监听**（fsnotify，60ms 去抖），经 `filesys.*` 消息面提供服务。
- **做**：列目录、读内容（截断）、创建/建目录/删除/改名/复制、watch/unwatch、`filesys.changed` 广播、`filesys.watch-error` 广播。
- **不做**：**不含 `/show`**（字节流豁免归 GUI）；不做跨实例沙箱/白名单（基目录**只取实例绑定**，载荷 `work_dir` 不采信，见 §4.1）；不持库；无独立 exe（纯 lib）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | **纯 lib**（`package filesys`），无 `main`、无独立 exe |
| 启动方 | `src/lib/gui/main.go`（GUI 单体）· `src/desktop/cli/main.go`（CLI 单体）· `src/lib/llm/httpapi/httpapi.go`（browser 服务端形态） |
| 依赖 | `src/lib`(mq) + `fsnotify` |
| 生命周期 | `New(bus)` → `Start()`（订阅）→ `Stop()` |

---

## 3. 对外接口（`filesys.*` 主题）

**订阅**（`bus.On(subject, 0, handler)`）：`filesys.list` · `filesys.content` · `filesys.create` · `filesys.mkdir` · `filesys.remove` · `filesys.rename` · `filesys.copy` · `filesys.watch` · `filesys.unwatch` · **`instance-register` / `instance-exit`**（自持 `instance → work_dir` 绑定，见 §4.1；**不订阅** `instance-heartbeat`） · **`data-prj-config-refresh`**（既有广播：同步「不显示的目录」，见 §4.2；零新增消息）。

| 类别 | 主题 | payload 要点 | 返回 |
|------|------|-------------|------|
| 查询 | `filesys.list` | `{instance_id, work_dir, path}` | `{path, is_dir, children:[...]}` |
| 查询 | `filesys.content` | `{instance_id, work_dir, path}` | `{path, kind, content, truncated}` |
| 写 | `filesys.create` / `mkdir` | `{instance_id, work_dir, dir, name}` | `{ok, path}` |
| 写 | `filesys.remove` | `{instance_id, work_dir, path}` | `{ok, path}`（`RemoveAll`） |
| 写 | `filesys.rename` | `{instance_id, work_dir, path, new_name, overwrite?}` | `{ok, path}`（含分隔符=移动） |
| 写 | `filesys.copy` | `{instance_id, work_dir, path, dest_dir?, new_name?, overwrite?}` | `{ok, path}` |
| 声明（无返回） | `filesys.watch` / `unwatch` | `{instance_id, work_dir, path, recursive?}`（`recursive=true` 递归子目录） | — |
| 广播 | `filesys.changed` | `{instance_id, work_dir, path, operation?}` 或 `{instance_id, work_dir, path, children}`（目录批次） | — |
| 广播 | `filesys.watch-error` | `{instance_id, work_dir, error}` | — |

- 请求公共字段：`instance_id`（**必带**，基目录解析依据，见 §4.1）· `work_dir`（**契约保留、不采信**）· `path` / `dir` / `name` / `content` / `new_name` / `dest_dir` / `overwrite`（`rename`/`copy` 覆盖开关；`0`/缺省 = 不覆盖，目标存在 → `exists`）。
- 错误经 `v.Errors`（`{code, message}`），码：`forbidden` / `create_failed` / `mkdir_failed` / `remove_failed` / `rename_failed` / `copy_failed` / `exists`。

---

## 4. 内部控制流

### 4.1 基目录解析与路径校验（唯一强制边界）

```text
基目录解析（自持 insts 表）：只取 `instance-register` 登记的 instance → work_dir 绑定
  · 载荷 work_dir **一律不采信**（契约字段保留：越权方无法借 payload 把操作指到绑外目录）
  · instance_id 缺失 / 未登记 / 已 `instance-exit` → 读/写 handler 追加 forbidden（**不回落载荷**）
absPath(workDir, p)：空=workDir；相对 → Join(workDir, p)；再 withinWorkDir 校验
withinWorkDir(base, path)：filepath.Rel 后非 ".." 前缀
越界：读/写 handler 追加 forbidden；watch/unwatch 静默 return（不发错）
```

> `instance-exit` 幂等：重复到达无副作用（绑定不复活）。`instance-heartbeat` 不订阅 —— filesys 与发布方恒**同进程同总线**，绑定生命周期 = 进程生命周期（与 persist 的一处有意差异）。见 [22-实例与会话标识 §7](../10-architecture/22-实例与会话标识.md)。

### 4.2 变更监听（`watcher.go`）

```text
watchManager：按 work_dir 组织 map[string]*dirWatcher（+ 每 work_dir 的「不显示的目录」清单缓存）
  watch → dirWatcher.add（recursive 时 walkAdd 递归子目录，跳过**不显示的目录**）
  fsnotify 事件 → enqueue（按路径去重）→ armTimer（60ms 重置，maxWait 封顶）→ processBatch
      ├─ 单文件 → filesys.changed {path, operation(create|write|remove|rename)}
      ├─ 目录批次 → filesys.changed {path, children}
      ├─ Create 且为目录 → 自动递归 add（命中「不显示的目录」清单则不加，与显示同源）
      └─ Remove / Rename → **removeTree(path)**：摘除该路径及其全部子孙的 watch（层1 闭环）
  新目录 Create → 自动递归 add；**同时刷新其父目录 children 批次**（父树才能插入新目录节点）；处理中新事件 → 自动再调度一轮
```

- 去抖常量：`debounce = 60ms`；`maxWait = 10×debounce`（D-37：批次广播不被持续事件无限推后）。
- `recursive` 语义：`onWatch` **消费**载荷 `recursive` 并透传 `wm.watch(wd, p, req.Recursive, instanceID)`；`recursive=true` 时 `dirWatcher.add` 对目录 `walkAdd` 递归子目录（跳过不显示的目录），与新建目录自动递归一致。缺省 `false` = 仅监听本目录（白盒 `TestWatchRecursiveFlag`）。
- **目录生命周期闭环（层1，D-39）**：`processBatch` 处理 `fsnotify.Remove` / `Rename` 时调 `dirWatcher.removeTree(path)` —— 按**路径前缀**（含自身）从 `paths` 摘除该目录及其全部子孙，并对每条 `w.Remove`（失败仅 `slog.Warn` 留痕，不阻断）。`paths` 只增不删会随目录反复重建累积泄漏（Java 项目 `target/`）。「出表 + `fsnotify.Remove`」在同一临界区（`pathsMu`）内完成，与 `add` 的「进表 + `fsnotify.Add`」原子配对，避免交错留下悬挂。
- **「不显示的目录」同源（层2，D-39）**：文件树**显示**过滤与 watcher **递归监听**共用 `shouldHideDirName(name, hiddenDirs)`（`.` 开头恒隐藏 + 命中可配置清单）→ **显示的必被监听、不显示的必不监听**。清单 = prj 配置键 `filetree.hide-dirs`（见 §5 / [64 §4](../60-reference/64-配置项一览.md)）；缺省 `defaultHiddenDirs = .git / .svn / .chonkpilot`（与「点开头」重叠，故缺省行为不变，价值在可配置扩展）。
  - **加载**：首次对该 work_dir 的 `filesys.watch` / `filesys.list` 发起**一次性异步**读取（经既有 `data-prj-config-load`，超时即回落缺省，**不阻塞**请求）；失败留痕并允许下次重试。
  - **变更生效**：订阅既有 `data-prj-config-refresh`（持久化 save/delete 后广播，载荷带 `list`）→ `applyHidden` 更新缓存并 `refilter` 该 work_dir 的活跃 watcher：**现已隐藏者摘除**（root 不误摘）、**由隐藏转可见者补齐**（`walkAdd` 幂等）。零新增消息、不误删用户仍在查看的目录节点。
- `watch-error`：**watcher 运行期** fsnotify 故障时广播 `{work_dir, error}`（`watcher.go` 的 `w.Errors` 路径）。watch 失败（D-40）与 walkAdd/remove 失败均 `slog.Warn` 留痕。

---

## 5. 数据结构与存储

- 无持久化存储（纯运行态）。
- 读内容上限 `maxTextBytes = 512KB`（超出 `truncated=true`）。
- `listDirNodes(dir, hiddenDirs)` 浅层列子项（跳过隐藏/临时条目），路径统一 `/`，附 `size`/`mtime`：
  - **目录** → `shouldHideDirName(name, hiddenDirs)`：`.` 开头恒隐藏 + 命中「不显示的目录」清单（`filetree.hide-dirs`）；与 watcher 递归**同源**（层2，见 §4.2）。
  - **文件** → `.` / `~$` 前缀 + `~` / `-wal` / `-shm` 后缀（原口径不变）。
  - 统一入口 = `filterDirEntry(name, isDir, hiddenDirs)`。
- 「不显示的目录」清单缓存：`watchManager.hidden`（work_dir → 解析后清单）+ `dirWatcher.hidden`（当前生效清单）；解析见 `config.go` `parseHiddenDirs`（逗号/分号/换行分隔，去空白、去尾分隔符、去重；空 → `defaultHiddenDirs`）。

---

## 6. 依赖

- **上游**：`src/lib`（mq）、`fsnotify`。**不依赖 `data` / `plugin` 模块** —— 「不显示的目录」经既有 `data-prj-config-load` / `data-prj-config-refresh` 消息面读取（`config.go` 本地请求-响应；语义同 `plugins/plugin/dataclient`，避免反向依赖）。
- **下游**：GUI 桥（订阅转发 `filesys.changed`）、前端（`api/file.js` → `filesys.<action>`）；设置页 `FileTreeConfig.vue` 写 `filetree.hide-dirs`。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 组件化（独立 lib） | 由 GUI/CLI 以同一总线启动 | 替代旧 GUI 内 `dirWatcher` mtime 轮询中间态 |
| 去抖 60ms | 事件合并 | 高频写不刷屏，前端更新稳定 |
| 目录批次与单文件同主题 | 靠 `children` 有无区分 | 减少主题数量 |
| 越界只拦读/写 | watch 静默忽略 | watch 为声明（无返回），不报错更简单 |
| 唯一强制边界 | 基目录 = 实例绑定（`instance-register`），**不采信载荷 `work_dir`**；再 `withinWorkDir` | 与 `security-*` 白名单无关（见 [14-安全域](../10-architecture/14-安全域-agentbox.md)） |
| 目录生命周期闭环（D-39 层1） | Remove/Rename → `removeTree`（前缀摘除自身 + 子孙） | `paths` 只增不删会随目录重建累积泄漏（`target/`）；对已消失路径的 watch 无意义 |
| 显示与监听同源（D-39 层2） | 文件树过滤与 watcher 递归共用 `shouldHideDirName` | 用户原话「不是不监听，是不显示的目录……」——若只监听不显示会出现「树里没有、变更也不刷新」的诡异 |
| 隐藏清单配置键 = `filetree.hide-dirs` | prj-config 域，团队共享（非个人运行态）；缺省 `.git/.svn/.chonkpilot` | 对齐既有 `<域>.<项>` 键名（`codegraph.skip-dirs` / `vfts.skip-dirs`）；缺省与「点开头」重叠 → 默认行为不变，价值在扩展（`target` / `node_modules`） |
| 配置读取经既有消息面（本地实现） | `data-prj-config-load` 读初值（首次 watch/list 一次性异步）+ `data-prj-config-refresh` 订阅变更 | filesys 不得依赖 `data` / `plugin`（依赖方向单向）→ 按既有契约本地请求-响应；零新增消息 |
| 变更即时生效 | 广播到达 → `refilter`（摘除现已隐藏 / 补齐由隐藏转可见） | 复用它方广播，免前端改 watch 时序；不误删用户仍在查看的目录节点（root 不摘） |

---

## 8. 场景与边界

- 同名冲突：`create` 命中已存在 → `exists`。
- 复制：`copy` 支持目录整树递归。
- 改名：`new_name` 含分隔符 = 移动；同目录 = 纯改名。
- 越界：读/写 → `forbidden`（含 instance 未登记 / 未绑定）；watch/unwatch → 静默。
- 「不显示的目录」：`.` 开头目录恒隐藏（不可配置放开）→ 既不显示也不监听；清单命中目录同理。文件树显示在下次 `filesys.list`（重启 / 折叠-展开 / 根刷新）时对齐；watcher 侧经 `data-prj-config-refresh` 即时重过滤。
- `/show` 不在此服务（GUI `fileserver` 直读）。

---

## 9. 现状与待办

- ✅ 已组件化（fsnotify + 去抖），GUI 内 `dirWatcher` 已移除。
- ✅ 目录生命周期闭环（D-39 层1）+ 「不显示的目录」显示/监听同源（D-39 层2，配置键 `filetree.hide-dirs`）。
- 🔵 本文即目标态（独立 lib）。
- ⚠️ 路径校验基于**实例绑定**（载荷 `work_dir` 不采信）；无跨实例沙箱（归 agentbox 🔵）。

---

## 10. 关联测试

`src/test/chonkpilot-filesys/unittest/filesys_test.go`（外部黑盒，内存总线直驱）：
`TestListEmptyAndPopulated` · `TestContentReadBackAndTruncated` · `TestWriteOps`（create/mkdir/remove/rename/copy/duplicate）· `TestWriteErrorsExistsForbidden` · `TestWorkDirBoundToInstanceOnly`（载荷 `work_dir` 不被采信 / 未登记 → `forbidden` / `instance-exit` 解绑且幂等）· `TestWatchChangedAndUnwatch`（真实 fsnotify + 60ms 去抖）· **`TestHideDirsFromPrjConfigRefresh`**（`data-prj-config-refresh` 下发 `filetree.hide-dirs` → `filesys.list` 过滤）。

模块内白盒（`src/lib/filesys/filesys_test.go`，`package filesys`）：`TestListDirNodesFilter`（过滤）· `TestWatchNewDirBroadcastsParentBatch`（父批次）· `TestNextWaitCapsAtMaxWait` / `TestWatchContinuousWritesNotStarved`（D-37）· `TestWatchRecursiveFlag`（`recursive` 消费）· **`TestRemoveTreeClearsSubtree` / `TestProcessBatchRemoveDirClearsPaths`**（层1 闭环）· **`TestShouldHideDirNameSameSource` / `TestListDirNodesHiddenDirsFilter` / `TestWalkAddSkipsHiddenDirs` / `TestRefilterAppliesHiddenChange` / `TestParseHiddenDirs`**（层2 同源与配置）。

前端（`src/frontend`）：设置页 `FileTreeConfig.vue`（`npm test` 内 i18n parity + 设置面守卫覆盖 zh-CN/en-US 齐备）。

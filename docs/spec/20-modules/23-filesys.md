# 23 · src/lib/filesys（文件服务）

> 状态：✅ 与代码一致
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [11-MQ与消息](../10-architecture/11-MQ与消息.md) · [20-gui](20-gui.md)
> 代码目录：`src/lib/filesys/`（`filesys.go` + `watcher.go`）

---

## 1. 职责与边界

- **一句话**：目录/文件**查询与写操作** + **变更监听**（fswatcher，60ms 去抖），经 `filesys.*` 消息面提供服务。
- **做**：列目录、读内容（截断）、创建/建目录/删除/改名/复制、watch/unwatch、`filesys.changed` 广播、`filesys.watch-error` 广播。
- **不做**：**不含 `/show`**（字节流豁免归 GUI）；不做跨实例沙箱/白名单（基目录**只取实例绑定**，载荷 `work_dir` 不采信，见 §4.1）；不持库；无独立 exe（纯 lib）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | **纯 lib**（`package filesys`），无 `main`、无独立 exe |
| 启动方 | `src/lib/gui/main.go`（GUI 单体）· `src/desktop/cli/main.go`（CLI 单体）· `src/lib/llm/httpapi/httpapi.go`（browser 服务端形态） |
| 依赖 | `src/lib`(mq) + `fswatcher` |
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
- 错误经 `v.Errors`（`{code, message}`），码：`forbidden` / `create_failed` / `mkdir_failed` / `remove_failed` / `rename_failed` / `copy_failed` / `exists` / `io_error`（**读失败**：`filesys.content` 遇非常规文件 / IO 错误；码取 [61 §2.5](../60-reference/61-消息一览.md) 明列的 `IO_ERROR` 家族）。

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
  watch → dirWatcher.add（fswatcher AddPath；recursive → WatchNested / 非递归 → WatchTopLevel）
  fswatcher 事件 → enqueue（按路径去重）→ armTimer（60ms 重置，maxWait 封顶）→ processBatch
      ├─ 单文件 → filesys.changed {path, operation(create|write|remove|rename)}
      ├─ 目录批次 → filesys.changed {path, children}
      ├─ 隐藏目录事件 → 跳过（不广播、不入队；显示与广播同源）
      ├─ Remove / Rename → 仅清理**显式声明根**的 paths 记录（递归子目录由 fswatcher 单句柄托管）
      └─ 每条 inWorkDir 事件 → 注入的 **IndexQueue.Append**（待索引队列；nil = 不入队，失败仅告警）
  新目录 Create → 递归监听由 fswatcher 自动覆盖；**同时刷新其父目录 children 批次**；处理中新事件 → 自动再调度一轮
```

- **重命名归一（2026-10-19，fswatcher）**：Windows 后端对 `RENAMED_OLD_NAME` / `RENAMED_NEW_NAME` **都**发 `EventRename`（不发 Create），且同一 60ms 窗口内同路径的事件类型会合并（如 `[Mod, Rename]` / `[Create, Remove]`）→ `operation(path, types)` **以处理时对象是否存在为准**：存在 → `create`（含改名新路径）/ `write`（仅修改）；不存在 → `rename`（改名旧路径）/ `remove`（含同窗口内创建后又删除）；其他 `Lstat` 错误（如权限不足）按 `write` 兜底，不误删索引。下游语义不变——文件树按 `create` 插入新节点，待索引队列把 `rename`/`remove` 当旧路径删除；两个父目录均刷新 `children`。
- **库切换（2026-10-19）**：`github.com/sgtdi/fswatcher v1.3.0`（Windows = `ReadDirectoryChangesW`，**单句柄递归**；Linux inotify / macOS FSEvents 或 kqueue；内置去抖/过滤）。本模块用 `WithCooldown(0)` + 自管 60ms 去抖（保留 D-37 的 `maxWait` 封顶）。
- **待索引入队（2026-10-19，resident indexer P2）**：`processBatch` 对每条 `inWorkDir` 事件（create/write/remove/rename）在 `pubChanged` 后调用注入的 `IndexQueue.Append(IndexChange{WorkDir, Path(ToSlash), Operation})`。入队**每事件一次**（不在按声明者复制事件的 `emit` 里）；队列为 nil 跳过；写失败仅 `slog.Warn("filesys index queue append failed", ...)`，**不影响** `filesys.changed` 与文件树。**目录自身的 `write` 不入队**（子项变动引起的目录 mtime/元数据事件——目录内容未变、索引无意义；否则启动期目录元数据噪声会拉起 indexer 引擎并长期持有索引库 LOCK）。队列实现 = data 层 prjusr `index_queue` 表（[12 §6.1](../10-architecture/12-数据层.md)），宿主用 `filesys.IndexQueueFunc` 闭包经 inline 门面写入，**不新增 MQ 主题**。

- 去抖常量：`debounce = 60ms`；`maxWait = 10×debounce`（D-37：批次广播不被持续事件无限推后）。
- `recursive` 语义：`onWatch` **消费**载荷 `recursive` 并透传 `wm.watch(wd, p, req.Recursive, instanceID)`；`recursive=true` → `AddPath(p, WithDepth(WatchNested))`，`false` → `WatchTopLevel`（缺省 false = 仅监听本目录；白盒 `TestWatchRecursiveFlag`）。
- **目录生命周期**：fswatcher 递归句柄随根目录删除/重建由库接管，**不再逐子目录 `Add/Remove`**；本模块只在 `Remove/Rename` 时清理 `paths` 中该路径下的**显式声明根**（防声明表泄漏），不对子目录做登记。
- **越界软链 / 目录联接不进表、不监听**：`dirWatcher.add` 在 `AddPath` **之前**先经 `withinWorkDirReal` 做**真实路径复检**（逐段 `os.Readlink`，兼容 Windows 目录联接）——指向 `work_dir` **外**的符号链接 / 目录联接**直接返回（不进 `paths`、不监听）**（否则会跟踪根外目标并广播其子目录清单 = 越权监听）。判目录用 `os.Lstat`（**不跟随软链**，与 `copyTree` 策略一致）。
- **广播前真实路径复检（`pubChanged` / `pubDirChanged`）**：`processBatch` 对每事件解析真实路径一次（`baseReal` 每批解析一次复用），`inWorkDir = withinWorkDir(workDir, path) && withinWorkDir(baseReal, pathReal)` —— **词法 + 真实路径双判定**；不满足则**不广播**（`pubChanged` 发单文件、`pubDirChanged` 发目录 `children` 前均复检，防经符号链接 / 目录联接广播根外路径或根外目录的 `children` 清单）。
- **「不显示的目录」同源（层2，D-39）**：文件树**显示**过滤与 watcher **事件广播**共用 `shouldHideDirName(name, hiddenDirs)`（`.` 开头恒隐藏 + 命中可配置清单）→ **显示的必广播、不显示的必不广播**。清单 = prj 配置键 `filetree.hide-dirs`（见 §5 / [64 §4](../60-reference/64-配置项一览.md)）；缺省 `defaultHiddenDirs = .git / .svn / .chonkpilot`（与「点开头」重叠，故缺省行为不变，价值在可配置扩展）。
  - **加载**：首次对该 work_dir 的 `filesys.watch` / `filesys.list` 发起**一次性异步**读取（经既有 `data-prj-config-load`，超时即回落缺省，**不阻塞**请求）；失败留痕并允许下次重试。
  - **无 watcher 不重建 hidden 项**：异步加载完成**写回缓存前**再次校验该 work_dir 仍有活跃 watcher（`m.watchers[workDir]`）——加载期间可能已被 `unwatchInstance` 清理（停并移出 watcher + 删 `hidden`）；无 watcher → **跳过写入**（否则 `hidden` 随出现过的 work_dir 无界增长；下次 watch/list 重新加载）。
  - **变更生效**：订阅既有 `data-prj-config-refresh`（持久化 save/delete 后广播，载荷带 `list`）→ `applyHidden` 更新缓存并 `refilter` 该 work_dir 的活跃 watcher；**过滤在 `processBatch` 按路径段执行**（不重建句柄）。零新增消息、不误删用户仍在查看的目录节点。
- `watch-error`：**watcher 运行期**故障时广播 `{work_dir, error}` —— 来源 = `Watch(ctx)` 非 `context.Canceled` 错误，或 `Dropped()` 通道（主事件通道满）丢弃事件。watch 失败（D-40）与 AddPath/DropPath 失败均 `slog.Warn` 留痕。

---

## 5. 数据结构与存储

- 无**自有**持久化存储（纯运行态）；变更事件额外写入 data 层 prjusr `index_queue`（经注入的窄接口 `IndexQueue`，本模块不直连 data）。
- 读内容上限 `maxTextBytes = 512KB`（超出 `truncated=true`）；**R8-C-2**：截断**按 UTF-8 边界收尾**——丢弃末尾不完整的 UTF-8 多字节序列（`trimIncompleteUTF8`），多字节字符（中文等）不被切断产生乱码；读取用 `io.ReadFull` 处理短读。
- `listDirNodes(dir, hiddenDirs)` 浅层列子项（跳过隐藏/临时条目），路径统一 `/`，附 `size`/`mtime`：
  - **目录** → `shouldHideDirName(name, hiddenDirs)`：`.` 开头恒隐藏 + 命中「不显示的目录」清单（`filetree.hide-dirs`）；与 watcher 递归**同源**（层2，见 §4.2）。
  - **文件** → `.` / `~$` 前缀 + `~` / `-wal` / `-shm` 后缀（原口径不变）。
  - 统一入口 = `filterDirEntry(name, isDir, hiddenDirs)`。
- 「不显示的目录」清单缓存：`watchManager.hidden`（work_dir → 解析后清单）+ `dirWatcher.hidden`（当前生效清单）；解析见 `config.go` `parseHiddenDirs`（逗号/分号/换行分隔，去空白、去尾分隔符、去重；空 → `defaultHiddenDirs`）。

---

## 6. 依赖

- **上游**：`src/lib`（mq）、`fswatcher`。**不依赖 `data` / `plugin` 模块** —— 「不显示的目录」经既有 `data-prj-config-load` / `data-prj-config-refresh` 消息面读取（`config.go` 本地请求-响应；语义同 `plugins/plugin/dataclient`，避免反向依赖）。
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
- 复制：`copy` 支持目录整树递归；**嵌套防护（R7-C-5）**：目标（`dest_dir` + `new_name`）落在**源目录内部**时拒绝（`copy_failed`，`target inside source dir`）——否则 `copyTree` 先建目标目录再 `ReadDir(src)` 会把新建目录递归吸入，产生**无限嵌套复制**（与 `filesys_run` 的 `CPY`/`MOV` 同款判定）。
- 改名：`new_name` 含分隔符 = 移动；同目录 = 纯改名。
- 越界：读/写 → `forbidden`（含 instance 未登记 / 未绑定）；watch/unwatch → 静默。
- 「不显示的目录」：`.` 开头目录恒隐藏（不可配置放开）→ 既不显示也不监听；清单命中目录同理。文件树显示在下次 `filesys.list`（重启 / 折叠-展开 / 根刷新）时对齐；watcher 侧经 `data-prj-config-refresh` 即时重过滤。
- `/show` 不在此服务（GUI `fileserver` 直读）。

---

## 9. 现状与待办

- ✅ 已组件化（fswatcher + 去抖），GUI 内 `dirWatcher` 已移除。
- ✅ 目录生命周期闭环（D-39 层1）+ 「不显示的目录」显示/监听同源（D-39 层2，配置键 `filetree.hide-dirs`）。
- 🔵 本文即目标态（独立 lib）。
- ⚠️ 路径校验基于**实例绑定**（载荷 `work_dir` 不采信）；无跨实例沙箱（归 agentbox 🔵）。

---

## 10. 关联测试

`src/test/chonkpilot-filesys/unittest/filesys_test.go`（外部黑盒，内存总线直驱）：
`TestListEmptyAndPopulated` · `TestContentReadBackAndTruncated` · `TestWriteOps`（create/mkdir/remove/rename/copy/duplicate）· `TestWriteErrorsExistsForbidden` · `TestWorkDirBoundToInstanceOnly`（载荷 `work_dir` 不被采信 / 未登记 → `forbidden` / `instance-exit` 解绑且幂等）· `TestWatchChangedAndUnwatch`（真实 fswatcher + 60ms 去抖）· **`TestHideDirsFromPrjConfigRefresh`**（`data-prj-config-refresh` 下发 `filetree.hide-dirs` → `filesys.list` 过滤）。

模块内白盒（`src/lib/filesys/filesys_test.go`，`package filesys`）：`TestListDirNodesFilter`（过滤）· `TestWatchNewDirBroadcastsParentBatch`（父批次）· `TestNextWaitCapsAtMaxWait` / `TestWatchContinuousWritesNotStarved`（D-37）· `TestWatchRecursiveFlag`（`recursive` 消费）· **`TestRemoveTreeClearsSubtree` / `TestProcessBatchRemoveDirClearsPaths`**（层1 闭环）· **`TestShouldHideDirNameSameSource` / `TestListDirNodesHiddenDirsFilter` / `TestWatchSkipsHiddenDirEvents` / `TestRefilterAppliesHiddenChange` / `TestParseHiddenDirs`**（层2 同源与配置）。

前端（`src/frontend`）：设置页 `FileTreeConfig.vue`（`npm test` 内 i18n parity + 设置面守卫覆盖 zh-CN/en-US 齐备）。

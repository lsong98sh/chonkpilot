# 23 · src/lib/filesys（文件服务）

> 状态：⚠️ 行为实现一致；2026-11-04 核对已订正依赖路径 / 决策行 / 测试清单
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [11-MQ与消息](../10-architecture/11-MQ与消息.md) · [20-gui](20-gui.md)
> 代码目录：`src/lib/filesys/`（`filesys.go` + `watcher.go` + `config.go`）

---

## 1. 职责与边界

- **一句话**：目录/文件**查询与写操作** + **变更监听**（fswatcher，60ms 去抖），经 `filesys.*` 消息面提供服务。
- **做**：列目录、读内容（截断）、创建/建目录/删除/改名/复制、`filesys.changed` 广播、`filesys.watch-error` 广播；按 `workdir-opened/closed` 建立/撤销**工作区级监听**。
- **不做**：**不含 `/show`**（字节流豁免归 GUI）；**不自持 `instance → work_dir` 绑定**（唯一来源 = 工作区管理 PM，[2I](2I-工作区管理.md)）；不做跨实例沙箱/白名单（基目录**只取实例绑定**，载荷 `work_dir` 不采信，见 §4.1）；不持库；无独立 exe（纯 lib）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | **纯 lib**（`package filesys`），无 `main`、无独立 exe |
| 启动方 | `src/lib/appwire`（装配 `filesys.New` / `Start`）；`src/lib/gui/host` 为调用方（2026-11-04 核对） |
| 依赖 | `chonkpilot-framework/host/core`（`mq` / `msgkeys`）· `src/lib/workspace`（PM：实例视图 / 工作区事件）· `workspace/layout`（路径语义）· `chonkpilot-util`（`pathx` / `strlist`）· `fswatcher` |
| 生命周期 | `New(bus, pm)` → `Start()`（订阅）→ `Stop()` |

---

## 3. 对外接口（`filesys.*` 主题）

**订阅**（`bus.On(subject, 0, handler)`）：`filesys.list` · `filesys.content` · `filesys.create` · `filesys.mkdir` · `filesys.remove` · `filesys.rename` · `filesys.copy` · `filesys.watch` / `filesys.unwatch`（**兼容保留、无副作用**） · **`workdir-opened` / `workdir-closed`**（工作区生命周期，驱动监听，见 §4.2） · **`data-prj-config-refresh`**（既有广播：同步「不显示的目录」，见 §4.2；零新增消息）。

| 类别 | 主题 | payload 要点 | 返回 |
|------|------|-------------|------|
| 查询 | `filesys.list` | `{instance_id, work_dir, path}` | `{path, is_dir, children:[...]}` |
| 查询 | `filesys.content` | `{instance_id, work_dir, path}` | `{path, kind, content, truncated}` |
| 写 | `filesys.create` / `mkdir` | `{instance_id, work_dir, dir, name}` | `{ok, path}` |
| 写 | `filesys.remove` | `{instance_id, work_dir, path}` | `{ok, path}`（`RemoveAll`） |
| 写 | `filesys.rename` | `{instance_id, work_dir, path, new_name, overwrite?}` | `{ok, path}`（含分隔符=移动） |
| 写 | `filesys.copy` | `{instance_id, work_dir, path, dest_dir?, new_name?, overwrite?}` | `{ok, path}` |
| 声明（无返回，**已无副作用**） | `filesys.watch` / `unwatch` | `{instance_id, work_dir, path, recursive?}` | — |
| 广播 | `filesys.changed` | `{instance_id, work_dir, path, operation?}` 或 `{instance_id, work_dir, path, children}`（目录批次） | — |
| 广播 | `filesys.watch-error` | `{instance_id, work_dir, error}` | — |

- 请求公共字段：`instance_id`（**必带**，基目录解析依据，见 §4.1）· `work_dir`（**契约保留、不采信**）· `path` / `dir` / `name` / `content` / `new_name` / `dest_dir` / `overwrite`（`rename`/`copy` 覆盖开关；`0`/缺省 = 不覆盖，目标存在 → `exists`）。
- 错误经 `v.Errors`（`{code, message}`），码：`bad_request`（载荷无法解析）/ `forbidden` / `create_failed` / `mkdir_failed` / `remove_failed` / `rename_failed` / `copy_failed` / `exists` / `io_error`（**读失败**：`filesys.content` 遇非常规文件 / IO 错误；码取 [61 §2.5](../60-reference/61-消息一览.md) 明列的 `IO_ERROR` 家族）。〔2026-11-04 核对补 `bad_request`〕

---

## 4. 内部控制流

### 4.1 基目录解析与路径校验（唯一强制边界）

```text
基目录解析（唯一来源 = 工作区管理 PM，2026-10-29）：
  pm.Resolve(instance_id) → Space.WorkDir
  · 载荷 work_dir **一律不采信**（契约字段保留：越权方无法借 payload 把操作指到绑外目录）
  · instance_id 缺失 / 未登记 / 工作区已关闭 → 读/写 handler 追加 forbidden（**不回落载荷**）
absPath(workDir, p) = workspace/layout.AbsWithin（空=workDir；相对 → Join；词法 + 真实路径双重复检）
越界：读/写 handler 追加 forbidden
```

> 路径校验是**内部纪律**，不是安全边界：in-proc 插件可直接 `os.WriteFile` 绕过。
> `instance → work_dir` 的登记与引用计数全部在 PM（[2I](2I-工作区管理.md)），filesys **不再自持 insts 表**。

### 4.2 变更监听（`watcher.go`）

```text
watchManager：按 work_dir 组织 map[string]*dirWatcher（+ 每 work_dir 的「不显示的目录」清单缓存）
  workdir-opened → open(workDir)：建 dirWatcher + AddPath(workDir, WatchNested) —— **整个工作区一次**
  workdir-closed → close(workDir)：撤销监听并移出 watcher（不删盘、不清索引）
  filesys.watch / unwatch → **无副作用**（兼容保留；前端展开/折叠不再影响监听范围）
  fswatcher 事件 → enqueue（按路径去重）→ armTimer（60ms 重置，maxWait 封顶）→ processBatch
      ├─ 单文件 → filesys.changed {path, operation(create|write|remove|rename)}
      ├─ 目录批次 → filesys.changed {path, children}
      ├─ 隐藏目录事件 → 跳过（不广播、不入队；显示与广播同源）
      └─ 每条 inWorkDir 事件 → 按该工作区**当前活动实例**逐发（instance_id 由 PM 提供）
  新目录 Create → 递归监听由 fswatcher 自动覆盖；**同时刷新其父目录 children 批次**；处理中新事件 → 自动再调度一轮
```

- **重命名归一（2026-10-19，fswatcher）**：Windows 后端对 `RENAMED_OLD_NAME` / `RENAMED_NEW_NAME` **都**发 `EventRename`（不发 Create），且同一 60ms 窗口内同路径的事件类型会合并（如 `[Mod, Rename]` / `[Create, Remove]`）→ `operation(path, types)` **以处理时对象是否存在为准**：存在 → `create`（含改名新路径）/ `write`（仅修改）；不存在 → `rename`（改名旧路径）/ `remove`（含同窗口内创建后又删除）；其他 `Lstat` 错误（如权限不足）按 `write` 兜底，不误删索引。下游语义不变——文件树按 `create` 插入新节点，订阅方（vfts / codegraph 插件）把 `rename`/`remove` 当旧路径删除索引项；两个父目录均刷新 `children`。
- **库切换（2026-10-19）**：`github.com/sgtdi/fswatcher v1.3.0`（Windows = `ReadDirectoryChangesW`，**单句柄递归**；Linux inotify / macOS FSEvents 或 kqueue；内置去抖/过滤）。本模块用 `WithCooldown(0)` + 自管 60ms 去抖（保留 D-37 的 `maxWait` 封顶）。
- ~~**待索引入队（2026-10-19，resident indexer P2）**：…（省略原文）~~ **〔2026-10-25 退场，D-71〕**：filesys **不再写任何待索引队列** —— `Options.IndexQueue` / `IndexQueue` / `IndexQueueFunc` 与 `enqueueIndex` 已删除，`filesys.changed` 成为唯一变更信号；vfts / codegraph 插件各自订阅该主题并在进程内去抖增量（vfts 走清单 diff，codegraph 走 `codegraph_index_paths`）。下游语义不变：`rename`/`remove` 仍由订阅方按旧路径删除索引项。

- 去抖常量：`debounce = 60ms`；`maxWait = 10×debounce`（D-37：批次广播不被持续事件无限推后）。
- **监听模型（2026-10-29）**：`workdir-opened` → `open(workDir)` 建 `dirWatcher` 并 `AddPath(workDir, WatchNested)`（整棵子树由 fswatcher 单句柄接管）；`workdir-closed` → `close(workDir)` 撤销并移出。**不再按前端展开逐目录声明**，`filesys.watch/unwatch` 的 `recursive` 字段仅作契约保留（无消费）。
- **目录生命周期**：递归句柄随根目录删除/重建由 fswatcher 接管，本模块不做逐子目录登记/摘除（原 `paths` 声明表与 `removeTree` 已删除）。
- **越界软链 / 目录联接**：根 = 工作区自身，无「指向根外的声明根」概念；事件路径仍在广播前做**真实路径复检**（见下条），越界事件不广播。判目录用 `os.Lstat`（**不跟随软链**，与 `copyTree` 策略一致）。
- **广播前真实路径复检（`pubChanged` / `pubDirChanged`）**：`processBatch` 对每事件解析真实路径一次（`baseReal` 每批解析一次复用），`inWorkDir = workspace.WithinLexical(workDir, path) && workspace.WithinLexical(baseReal, pathReal)` —— **词法 + 真实路径双判定**；不满足则**不广播**（防经符号链接 / 目录联接广播根外路径或根外目录的 `children` 清单）。
- **「不显示的目录」同源（层2，D-39）**：文件树**显示**过滤与 watcher **事件广播**共用 `shouldHideDirName(name, hiddenDirs)`（`.` 开头恒隐藏 + 命中可配置清单）→ **显示的必广播、不显示的必不广播**。清单 = prj 配置键 `filetree.hide-dirs`（见 §5 / [64 §4](../60-reference/64-配置项一览.md)）；缺省 `defaultHiddenDirs = .git / .svn / .chonkpilot`（与「点开头」重叠，故缺省行为不变，价值在可配置扩展）。
  - **加载**：首次对该 work_dir 的 `workdir-opened` / `filesys.list` 发起**一次性异步**读取（经既有 `data-prj-config-load`，超时即回落缺省，**不阻塞**请求）；失败留痕并允许下次重试。
  - **入队前丢弃（G-86 ①，2026-10-29）**：隐藏子树的事件在 `loop` 收到后**立即丢弃**（`droppedHidden` 计数），不进 60ms 队列、不做归一/真实路径解析/广播；`processBatch` 的同名判定保留为兜底（配置刷新竞态窗口）。**上限**：Windows RDCW 单句柄子树模型无法让内核不上报，完全消除需逐目录句柄（评估不采用，见 [2I §8](2I-工作区管理.md)）。
  - **无 watcher 不重建 hidden 项**：异步加载完成后**先 applyHidden 写入，再复验**该 work_dir 仍有活跃 watcher（`m.watchers[workDir]`）；若加载期间已被 `close` 清理 → **撤销**刚写入的值（仅当仍为本处写入值时才删，避免误删并发 refresh 的新值）——机制是「写入后复验、无 watcher 则撤销」，结果同文档原意（不残留无 watcher 的 hidden 项；下次 open/list 重新加载）。〔2026-11-04 核对〕
  - **变更生效**：订阅既有 `data-prj-config-refresh`（持久化 save/delete 后广播，载荷带 `list`）→ `applyHidden` 更新缓存并 `refilter` 该 work_dir 的活跃 watcher；**过滤在入队前按路径段执行**（不重建句柄）。零新增消息、不误删用户仍在查看的目录节点。
- `watch-error`：**watcher 运行期**故障时广播 `{work_dir, error}` —— 来源 = `Watch(ctx)` 非 `context.Canceled` 错误，或 `Dropped()` 通道（主事件通道满）丢弃事件。watch 失败（D-40）与 AddPath 失败 `slog.Warn` 留痕；DropPath（递归升级前摘除）失败用 `slog.Debug`（2026-11-04 核对）。

---

## 5. 数据结构与存储

- 无**自有**持久化存储（纯运行态）；变更事件只发 `filesys.changed`（2026-10-25 起**不再**写入任何待索引队列，见 §4）。
- 读内容上限 `maxTextBytes = 512KB`（超出 `truncated=true`）；**R8-C-2**：截断**按 UTF-8 边界收尾**——丢弃末尾不完整的 UTF-8 多字节序列（`trimIncompleteUTF8`），多字节字符（中文等）不被切断产生乱码；读取用 `io.ReadFull` 处理短读。
- `listDirNodes(dir, hiddenDirs)` 浅层列子项（跳过隐藏/临时条目），路径统一 `/`，附 `size`/`mtime`：
  - **目录** → `shouldHideDirName(name, hiddenDirs)`：`.` 开头恒隐藏 + 命中「不显示的目录」清单（`filetree.hide-dirs`）；与 watcher 递归**同源**（层2，见 §4.2）。
  - **文件** → `.` / `~$` 前缀 + `~` / `-wal` / `-shm` 后缀（原口径不变）。
  - 统一入口 = `filterDirEntry(name, isDir, hiddenDirs)`。
- 「不显示的目录」清单缓存：`watchManager.hidden`（work_dir → 解析后清单）+ `dirWatcher.hidden`（当前生效清单）；解析见 `config.go` `parseHiddenDirs`（逗号/分号/换行分隔，去空白、去尾分隔符、去重；空 → `defaultHiddenDirs`）。

---

## 6. 依赖

- **上游**：`chonkpilot-framework/host/core`（`mq`/`msgkeys`，2026-11-04 核对：原写 `src/lib`(mq) 已过期）、`chonkpilot-util`、`fswatcher`。**不依赖 `data` / `plugin` 模块** —— 「不显示的目录」经既有 `data-prj-config-load` / `data-prj-config-refresh` 消息面读取（`config.go` 本地请求-响应；语义同 `plugins/plugin/dataclient`，避免反向依赖）。
- **下游**：GUI 桥（订阅转发 `filesys.changed`）、前端（`api/file.js` → `filesys.<action>`）；设置页 `FileTreeConfig.vue` 写 `filetree.hide-dirs`。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 组件化（独立 lib） | 由宿主以同一总线启动 | 替代旧 GUI 内 `dirWatcher` mtime 轮询中间态 |
| 去抖 60ms | 事件合并 | 高频写不刷屏，前端更新稳定 |
| 目录批次与单文件同主题 | 靠 `children` 有无区分 | 减少主题数量 |
| 越界只拦读/写 | watch 静默忽略 | watch 为声明（无返回），不报错更简单 |
| 唯一强制边界 | 基目录 = 实例绑定（`instance-register`），**不采信载荷 `work_dir`**；再 `withinWorkDir` | 与 `security-*` 白名单无关（见 [14-安全域](../10-architecture/14-安全域-agentbox.md)） |
| 目录生命周期（D-39 层1，2026-11-04 核对订正） | **Remove/Rename 不再调用 `removeTree`**（该函数与 `paths` 声明表已删除）；递归句柄随根目录删除/重建由 fswatcher 接管，本模块不做逐子目录登记/摘除 | 保留递归单句柄模型后，逐目录摘除已无意义；旧 `paths` 只增不删的泄漏问题随声明表退场一并消除 |
| 显示与监听同源（D-39 层2） | 文件树过滤与 watcher 递归共用 `shouldHideDirName` | 用户原话「不是不监听，是不显示的目录……」——若只监听不显示会出现「树里没有、变更也不刷新」的诡异 |
| 隐藏清单配置键 = `filetree.hide-dirs` | prj-config 域，团队共享（非个人运行态）；缺省 `.git/.svn/.chonkpilot` | 对齐既有 `<域>.<项>` 键名（`plugins.codegraph.skip-dirs` / `plugins.codegraph.vfts.skip-dirs`）；缺省与「点开头」重叠 → 默认行为不变，价值在扩展（`target` / `node_modules`） |
| 配置读取经既有消息面（本地实现） | `data-prj-config-load` 读初值（首次 watch/list 一次性异步）+ `data-prj-config-refresh` 订阅变更 | filesys 不得依赖 `data` / `plugin`（依赖方向单向）→ 按既有契约本地请求-响应；零新增消息 |
| 变更即时生效 | 广播到达 → `refilter`（摘除现已隐藏 / 补齐由隐藏转可见） | 复用它方广播，免前端改 watch 时序；不误删用户仍在查看的目录节点（root 不摘） |

---

## 8. 场景与边界

- 同名冲突：`create` 命中已存在 → `exists`。
- 复制：`copy` 支持目录整树递归；**嵌套防护（R7-C-5）**：目标（`dest_dir` + `new_name`）落在**源目录内部**时拒绝（`copy_failed`，`target inside source dir`）——否则 `copyTree` 先建目标目录再 `ReadDir(src)` 会把新建目录递归吸入，产生**无限嵌套复制**（与 `file` 插件 `file_copy`/`file_move` 同款判定）。
- 改名：`new_name` 含分隔符 = 移动；同目录 = 纯改名。
- 越界：读/写 → `forbidden`（含 instance 未登记 / 未绑定）；watch/unwatch → 静默。
- 「不显示的目录」：`.` 开头目录恒隐藏（不可配置放开）→ 既不显示也不监听；清单命中目录同理。文件树显示在下次 `filesys.list`（重启 / 折叠-展开 / 根刷新）时对齐；watcher 侧经 `data-prj-config-refresh` 即时重过滤。
- `/show` 不在此服务（GUI `fileserver` 直读）。

---

## 9. 现状与待办

- ✅ 已组件化（fswatcher + 去抖），GUI 内 `dirWatcher` 已移除。
- ✅ 目录生命周期交由 fswatcher 递归句柄（原 `removeTree` / `paths` 声明表已删除）+ 「不显示的目录」显示/监听同源（D-39 层2，配置键 `filetree.hide-dirs`）。〔2026-11-04 核对〕
- 🔵 本文即目标态（独立 lib）。
- ⚠️ 路径校验基于**实例绑定**（载荷 `work_dir` 不采信）；无跨实例沙箱（归 agentbox 🔵）。

---

## 10. 关联测试

`src/test/chonkpilot-filesys/unittest/filesys_test.go`（外部黑盒，内存总线直驱；2026-11-04 核对）：
`TestListEmptyAndPopulated` · `TestContentReadBackAndTruncated` · `TestWriteOps`（create/mkdir/remove/rename/copy/duplicate）· `TestWriteErrorsExistsForbidden` · `TestWorkDirBoundToInstanceOnly`（载荷 `work_dir` 不被采信 / 未登记 → `forbidden`）· **`TestWorkdirOpenWatchesAndUnwatchIsNoop`**（真实 fswatcher + 60ms 去抖；原文档 `TestWatchChangedAndUnwatch` 不存在）· `TestRemoveRefusesWorkDir` · `TestCopyNewNameTraversalRejected` · `TestRenameRelativeMultiSegment` · `TestSymlinkEscapeForbidden` · **`TestHideDirsFromPrjConfigRefresh`**（`data-prj-config-refresh` 下发 `filetree.hide-dirs` → `filesys.list` 过滤）。

模块内白盒（`src/lib/filesys/filesys_test.go`，`package filesys`；2026-11-04 核对）：`TestListDirNodesFilter`（过滤）· `TestWatchNewDirBroadcastsParentBatch`（父批次）· `TestNextWaitCapsAtMaxWait` / `TestWatchContinuousWritesNotStarved`（D-37）· `TestOperationNormalize`（rename 归一）· **`TestWorkdirOpenWatchesSubtree`**（递归监听；原文档 `TestWatchRecursiveFlag` 不存在）· **`TestShouldHideDirNameSameSource` / `TestListDirNodesHiddenDirsFilter` / `TestWatchSkipsHiddenDirEvents` / `TestRefilterAppliesHiddenChange` / `TestHiddenEventsDroppedBeforeQueue` / `TestParseHiddenDirs`**（层2 同源与配置）· `TestOnCopyRejectsNestedTarget` / `TestOnCopyNormalTargetAllowed`（R7-C-5）。**`TestRemoveTreeClearsSubtree` / `TestProcessBatchRemoveDirClearsPaths` 已随 `removeTree` 退场，不存在**。

前端（`src/frontend`）：设置页 `FileTreeConfig.vue`（`npm test` 内 i18n parity + 设置面守卫覆盖 zh-CN/en-US 齐备）。

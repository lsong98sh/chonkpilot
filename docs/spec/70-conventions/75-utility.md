# 75 · utility 复用规范（chonkpilot-util）

> 状态：✅ 与代码一致（2026-10-29 建立）
> 关联：[71-编程规范](71-编程规范.md) · [10-分层与依赖](../10-architecture/10-分层与依赖.md) · [2F-ignore](../20-modules/2F-ignore.md)
> 代码目录：同级仓 `chonkpilot-util/`（module `github.com/lsong98sh/chonkpilot-util`）

---

## 1. 定位

`chonkpilot-util` 是**全仓共用的无状态 utility**：只做「输入 → 输出」，不持库、不缓存、不起 goroutine、不读写文件系统、不依赖任何业务包。

它是**单一实现**：同一语义不允许在业务包再写一份。`ignore` 只是其中一个子包（gitignore 纯内核），不是整个库的名字。

---

## 2. 硬约束

| 允许 | 禁止 |
|------|------|
| 参数 → 返回值 | 包级可变变量、缓存 map |
| 只读常量表（扩展名/MIME/VK 表） | `sync.*`、goroutine、channel |
| 确定性环境常量（`os.UserHomeDir` / `TempDir`） | 文件/网络 IO、DB、MQ |
| 标准库 | `time.Now`、随机源（需要时由调用方传入） |
| 纯状态机谓词 | 任何「记住上次」的逻辑 |

**禁止聚合入口包**：调用方必须 import 具体子包。聚合包会把全部 `init` 与重依赖一起拉进 exe。

---

## 3. 子包与职责

| 子包 | 职责 | 典型函数 |
|------|------|---------|
| `strx` | 字符串 | `FirstNonEmpty` / `TruncRunes` / `TruncBytes` / `SafeSeg` / `Slug` / `NormalizeEnum` |
| `strlist` | 分隔符列表 | `Split` / `SplitDedup` / `TrimDropEmpty` / `SplitKV` |
| `convx` | 标量转换 | `ToStr` / `Truthy` / `IntLoose` / `IntStrict` / `Clamp` |
| `pathx` | 路径 | `ExpandHome` / `ResolveDir` / `Resolve` / `SanitizeInstance` / `Within` / `Same` / `ToLogical` / `FromLogical` |
| `namex` | 文件名 | `Sanitize` / `ReplaceIllegal` / `EnsureExt` / `SafeExt` / `ExtAllowed` |
| `glob` | 通配匹配 | `Match` / `MatchAny` / `MatchCSV`（支持 `**`） |
| `ignore` | gitignore 纯内核 | `Parse` / `Match` / `MatchesAny` / `LastMatch` / `BuiltinRules` / `DefaultRules` |
| `textx` | 文本/行/二进制 | `SplitLines` / `CountLines` / `NormalizeNewlines` / `IsBinary` / `HasUTF16BOM` / `EstimateTokens` |
| `jsonval` | JSON 取值 | `Str` / `Int` / `Bool` / `Unwrap` / `UnwrapMap` / `MergeMissing` / `Clone` / `Lookup` |
| `configkit` | 配置脚手架 | `IntPtr` / `NormalizeInt` / `NormalizeBool` / `SaveEntries` / `ExplicitKeys` / `ExplicitKeysInOrder` / `IsKnownKey` |
| `mqmatch` | MQ 主题匹配 | `Match` / `MatchTokens` / `Split` |
| `extx` | 扩展名集合 | `Normalize` / `Set` |
| `safepath` | 危险路径护栏 | `RefuseUnsafeRoot` / `IsWithin` |
| `redact` | 密钥脱敏 | `Mask` / `Text` / `IsSecretName` |
| `argv` | 命令行切分 | `Split` |
| `daterange` | 日期区间 | `Parse` / `Range.Contains` |
| `keyhash` | 哈希 key | `SHA1Hex` / `SHA1Short` |
| `mimex` | MIME | `ByExt` / `IsText` |
| `jsonschemax` | JSON Schema | `NormalizeObject` |

完整签名与说明见仓根 `chonkpilot-util/CATALOG.md`（自动生成，勿手改）。

---

## 4. 判定：什么时候进 util

命中**任一**即进 util：

1. 同一逻辑出现 ≥2 处（跨包或跨仓）。
2. 跨插件复用。
3. 安全相关（路径、文件名、脱敏、危险根）。
4. 跨端必须一致（`SafeSeg`、token 估算、sandbox 布尔表）。
5. 易错（rune 截断、二进制判定、glob、Windows 保留名）。

**不进 util**：

- 依赖业务类型（`ChatMsg` / `Turn` / 插件配置结构）。
- HTTP 客户端、连接池、重试状态机、缓存、注册表。
- 单插件内一次性拼装、3 行以内的领域适配。

领域纯逻辑（`turnx` / `msgx` / `compressx`）单独一层，不混入通用 utility。

---

## 5. 使用规则（强制）

1. 新增通用函数前，先查本文第 3 节与 `CATALOG.md`，并用 `rg` 搜关键字；已有则直接 import。
2. 语义不完全一致时，给 util 函数**加参数**，不在业务包另写一份。
3. 迁移旧实现时保留原函数名做**薄封装**（函数体调用 util），避免大范围改调用点；薄封装函数体内出现 util 调用即视为合规。
4. 跨端必须一致的逻辑不做共享代码：一份规格 + 两端各一实现 + 对账测试。
5. 禁止在业务包定义 utilguard 黑名单函数名（见第 6 节）。

---

## 6. 守卫（utilguard）

新增 `chonkpilot-util/cmd/utilguard`：扫描所有同级仓（排除 old / old2 / webview2 / util 自身），检查：

- 函数名黑名单：`firstNonEmpty`、`splitRules`、`splitList`、`splitComma`、`parseHiddenDirs`、`sanitizeInstance`、`sanitizePluginID`、`sanitizeIDSeg`、`sanitizeFilename`、`truncRunes`、`truncateEventMsg`、`countLines`、`isBinary`、`IsBinaryBytes`、`HasUTF16BOM`、`samePath`、`matchSubject`、`matchTokens`、`normalizeExts`、`newExtSet`、`intPtr`、`atoiSafe`、`isTruthy`。
- 特征实现：`RuneCountInString … / 2`、`IndexByte(…, 0)`、`FieldsFunc` 分号列表、`EqualFold + filepath.Clean`。
- 依赖方向：util 自身只允许标准库。
- 已委托 util 的薄封装自动放行。

命令（在 `chonkpilot-util` 仓根）：

```powershell
go run ./cmd/utilguard                  # 只报新增违规
go run ./cmd/utilguard -all             # 报全部
go run ./cmd/utilguard -write-baseline  # 迁移期重写豁免清单
go run ./cmd/gencatalog -check          # 校验 CATALOG 是否过期
```

迁移期用 `utilguard-baseline.txt` 冻结存量，只拦新增；存量随重构逐步清零（棘轮）。

**当前存量（2026-10-29 收尾后）**：**0 条**。7 个插件的 `normalizeInt` / `saveEntries` / `explicitKeys` 已全部改为 `configkit` 薄封装（`ExplicitKeysInOrder` 保留各插件 `allKeys` 声明序）；`settingsOf` 是插件各自的 schema 字段映射（字段集不同），不属于通用 utility，已从黑名单移除。

---

## 7. 新增子包的流程

1. 在 `chonkpilot-util/<pkg>/` 建 `doc.go`（中文职责 + 英文关键词）与实现。
2. 只依赖标准库；禁止 `init` 与包级可变变量。
3. 写 table-driven 单测（`package <pkg>_test`）。
4. 运行 `gofmt -w`、`go test ./...`、`go run ./cmd/gencatalog`。
5. 把该包加入本文第 3 节、`README.md` 与 utilguard 黑名单（如有旧实现）。

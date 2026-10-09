# third_party/zvec — vendored zvec C-API 台账

> 本目录是 `src/plugins/vfts`（vfts mcp-server，CGO 链接 zvec C-API）构建/运行所需的 **vendored 头与导入库**。
> 对照 `third_party/jieba`（含 `LICENSE.cppjieba`）补齐本目录的**许可 / 来源 / 版本**台账（审查 F-33 · 视角⑯供应链）。

## 组件

| 文件 | 说明 | 是否入库 |
|---|---|---|
| `include/zvec/c_api.h` | zvec C-API 头（CGO 编译期 `#include`） | ✅ 跟踪 |
| `windows_amd64/zvec_c_api.lib` | Windows/amd64 导入库（链接期） | ✅ 跟踪 |
| `windows_amd64/zvec_c_api.dll` | Windows/amd64 运行库（≈25MB，运行时加载，**须与 vfts 引擎 exe 同目录**） | ❌ **不入库**（体积原因） |

## 来源与版本

- 上游：`github.com/zvec-ai/zvec-go`
- 版本：**v0.7.0**
- 版本锚点：与 `src/plugins/vfts/go.mod` 的 `require github.com/zvec-ai/zvec-go v0.7.0` **唯一对齐**——
  本仓跟踪的头/库必须取自同一版本；升级时**此处与 go.mod 两处同步**。

## 许可

- **Apache License 2.0**（见 `include/zvec/c_api.h` 头声明与同目录 `LICENSE.zvec`）。

## 获取与校验（clean clone）

`zvec_c_api.dll` 不入库，clean clone 后按下述步骤获取：

1. 取 `github.com/zvec-ai/zvec-go` **v0.7.0** 的 GitHub Releases 资产 `zvec-libs-windows-x64.zip`；
2. 解出 `windows_amd64/zvec_c_api.dll` 放到本目录 `windows_amd64/`
   （部分版本亦可用 `go run ./cmd/download-libs -version v0.7.0` 自动下载）；
3. **版本一致性校验**：`.dll` 必须与仓库跟踪的 `windows_amd64/zvec_c_api.lib` 取自**同一 Release 同一版本**
   ——两者版本不一致会在链接或运行时加载失败；
4. 完整性：优先核对 Release 公布的校验值；未公布时以「同版本 Release 资产」为唯一来源。

- 缺失时 `build-vfts.ps1` 会 `throw` 并给出指引。
- 完整前置说明见 `docs/spec/00-overview/03-构建与部署.md` §5.2 与 `docs/spec/50-testing/50-测试体系.md` §5.2。

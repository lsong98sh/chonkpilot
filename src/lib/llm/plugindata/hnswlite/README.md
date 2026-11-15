# hnswlite

最小 HNSW 图实现（仅 graph + distance，无持久化 / analyzer），供语义检索的近似最近邻加速。

来源：`github.com/coder/hnsw` v0.6.1 的 `graph.go` / `distance.go`（CC0 1.0，公有领域），
仅改包名为 `hnswlite`。上游 `encode.go` 依赖 `google/renameio`（Windows 上不可用），本仓
不需要图持久化（进程内缓存，按向量变更丢弃重建），故不引入该文件与依赖。

API：`NewGraph[K]` / `MakeNode` / `Add` / `Search` / `Distance`（默认 CosineDistance）。
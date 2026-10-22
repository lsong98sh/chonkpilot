// 契约来源分层（GATEWAY-2 C，2026-10-21）：
// 一个注册调用可接收**多层**契约来源，按 `Source` 顺序由低到高合并——同名（同类型 + 同原语名）
// **后者覆盖前者**，并输出 `Shadowed` 审计清单（调用方记日志）。
//
// 现行装配口径（见 registerContracts）：
//   - 磁盘契约根存在 → **磁盘单层**（部署产物 = 权威，行为与改前一致）；
//   - 磁盘契约根缺失 → **内置 embed 层**（`//go:embed all:contracts`，构建期由构建脚本从
//     `src/initdata/capability` 覆盖同步的副本），避免「exe 单独拷贝 / 源码直跑」时工具面与
//     资产面全空。
//
// 更高层（user / project / prjusr capability 根）由 dir 节点各自作为独立 MCP 下游接入 gateway，
// 层级冲突在 gateway 命名空间（前缀）上体现，不在本函数合并。
package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 契约原语类型（覆盖判定键 + 排序键）。
const (
	kindTool     = "tool"
	kindSkill    = "skill"
	kindPrompt   = "prompt"
	kindResource = "resource"
)

// Source 是一层契约来源：
//   - `Dir` 非空 → **磁盘**契约根（递归扫描；`Dir` = executor 相对解析基准，路径原样保留）；
//   - 否则 `FS` 非空 → **内嵌**契约根（扫描根 = "."；`Path`/`Dir` 为空，由调用方按部署根补）；
//   - 两者都空 → 跳过该层。
//
// `Label` 仅用于审计（Shadowed 里记录来源层级名，如 `app` / `app-embed`）。
type Source struct {
	Label string
	Dir   string
	FS    fs.FS
}

// ContractFile 是一个已读入内容的契约文件。
type ContractFile struct {
	Name string // 原语名 = 文件名去四后缀之一（同 Kind 内覆盖判定键）
	Kind string // kindTool / kindSkill / kindPrompt / kindResource
	Rel  string // 相对契约根路径（`/` 分隔；审计与内嵌 Dir 推导用）
	Path string // 磁盘绝对路径（内嵌来源 = ""）
	Dir  string // executor 相对解析基准（磁盘来源 = 契约文件所在目录）
	Data []byte // 契约内容（扫描期读入；资源「外部内容文件」仍按 Path 实时读盘）
}

// Shadowed 是一次「同名覆盖」审计记录（后层覆盖前层）。
type Shadowed struct {
	Kind      string
	Name      string
	Winner    string // 生效来源 Label
	WinnerRel string
	Loser     string // 被覆盖来源 Label
	LoserRel  string
}

// suffixOf 返回原语类型对应后缀；未知类型 → ""。
func suffixOf(kind string) string {
	switch kind {
	case kindTool:
		return suffixTool
	case kindSkill:
		return suffixSkill
	case kindPrompt:
		return suffixPrompt
	case kindResource:
		return suffixResource
	}
	return ""
}

// contractKind 按文件后缀识别原语类型；非契约文件 → ""。
func contractKind(fileName string) string {
	switch {
	case strings.HasSuffix(fileName, suffixTool):
		return kindTool
	case strings.HasSuffix(fileName, suffixSkill):
		return kindSkill
	case strings.HasSuffix(fileName, suffixPrompt):
		return kindPrompt
	case strings.HasSuffix(fileName, suffixResource):
		return kindResource
	}
	return ""
}

// primitiveName 取原语名（文件名去对应后缀；非契约文件原样返回）。
func primitiveName(fileName string) string {
	kind := contractKind(fileName)
	if kind == "" {
		return fileName
	}
	return strings.TrimSuffix(fileName, suffixOf(kind))
}

// ScanAll 按 sources 顺序（由低到高）扫描并合并契约文件：
// 同名（Kind + 原语名）**后者覆盖前者**；同层内重复同名 → 保留先扫到者并记一条同层 shadow。
// 返回生效文件（按 Kind、Name 排序）与覆盖审计。任一来源读取失败直接返回错误（不静默降级）。
func ScanAll(sources []Source) ([]*ContractFile, []Shadowed, error) {
	best := map[string]*ContractFile{}
	bestLabel := map[string]string{}
	var shadowed []Shadowed
	for _, src := range sources {
		files, err := scanSource(src)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range files {
			key := f.Kind + "\x00" + f.Name
			if prev, ok := best[key]; ok {
				shadowed = append(shadowed, Shadowed{
					Kind: f.Kind, Name: f.Name,
					Winner: src.Label, WinnerRel: f.Rel,
					Loser: bestLabel[key], LoserRel: prev.Rel,
				})
				// 同层重复同名：保留先扫到者（walk 顺序确定），避免同层内互相覆盖。
				if bestLabel[key] == src.Label {
					continue
				}
			}
			best[key] = f
			bestLabel[key] = src.Label
		}
	}
	out := make([]*ContractFile, 0, len(best))
	for _, f := range best {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, shadowed, nil
}

// scanSource 扫描单层来源。
func scanSource(src Source) ([]*ContractFile, error) {
	if strings.TrimSpace(src.Dir) != "" {
		return scanDiskSource(src)
	}
	if src.FS != nil {
		return scanFSSource(src)
	}
	return nil, nil
}

// scanDiskSource 递归扫描磁盘契约根。
func scanDiskSource(src Source) ([]*ContractFile, error) {
	root := src.Dir
	var out []*ContractFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || contractKind(d.Name()) == "" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, &ContractFile{
			Name: primitiveName(d.Name()),
			Kind: contractKind(d.Name()),
			Rel:  filepath.ToSlash(rel),
			Path: p,
			Dir:  filepath.Dir(p),
			Data: data,
		})
		return nil
	})
	return out, err
}

// scanFSSource 递归扫描内嵌契约根（路径为 `/` 分隔的相对路径）。
func scanFSSource(src Source) ([]*ContractFile, error) {
	var out []*ContractFile
	err := fs.WalkDir(src.FS, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || contractKind(d.Name()) == "" {
			return nil
		}
		data, err := fs.ReadFile(src.FS, p)
		if err != nil {
			return err
		}
		out = append(out, &ContractFile{
			Name: primitiveName(d.Name()),
			Kind: contractKind(d.Name()),
			Rel:  p,
			Data: data,
		})
		return nil
	})
	return out, err
}

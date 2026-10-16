// capfs 的文件系统辅助（路径/文件名/尺寸上限/越界校验）+ 契约解析的**薄委托**。
//
// v2（2026-10-14）：契约解析/序列化**唯一实现**已收敛到
// github.com/chonkpilot/chonkpilot-lib/contractmd（H1 分区 + yaml/json 代码块）。
// 本包不再自持分区解析（旧 `[meta]`/`[description]`/… 方括号分区**已废除**），
// 仅别名复用其类型/清单并保留 data 层所需文件系统工具。
//
// capability 根下 **6 个扁平子目录**：`prompts/` `tools/` `resources/` `skills/` `agents/`
// `scenarios/`（旧 `knowledge/**` 归并层已删除）。
package capfs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chonkpilot/chonkpilot-lib/contractmd"
)

// Type 一类能力面条目（= contractmd.Type）。
type Type = contractmd.Type

// Types 是 6 类能力面条目的规范清单（= contractmd.Types）。
var Types = contractmd.Types

// Doc 解析后的原语文档（= contractmd.Doc）。
//
// v2 字段：Name（基本信息 name，= 文件名去后缀）/ Title（基本信息 title）/ Meta（基本信息其余键）/
// Description（摘要）/ Parameters（参数，json 文本）/ Refs（参考：skills/tools）/ Delegate（委托）/ Content（正文）。
type Doc = contractmd.Doc

// ParseDoc 解析 v2 契约文本；解析失败时返回**尽力而为的 doc**（供列表/展示场景容错）。
// 需要严格错误（缺 基本信息/name、正文非最后分区等）时用 ParseDocErr。
func ParseDoc(raw string) Doc {
	doc, _ := contractmd.Parse(raw)
	return doc
}

// ParseDocErr 严格解析 v2 契约文本（返回错误）。
func ParseDocErr(raw string) (Doc, error) {
	return contractmd.Parse(raw)
}

// BuildDoc 组装 v2 契约文本（唯一序列化实现）。
func BuildDoc(doc Doc) string { return contractmd.Build(doc) }

// Template 按类型生成新建模板 doc。
func Template(token, name string) Doc {
	return contractmd.Template(contractmd.Kind(token), name)
}

// TypeOfArg 归一化创建原语的 type：接受 token（tool/…）或目录名（tools/…）。
func TypeOfArg(t string) *Type {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return nil
	}
	if p := contractmd.TypeByToken(t); p != nil {
		return p
	}
	if p := contractmd.TypeByDir(t); p != nil {
		return p
	}
	return nil
}

// TypeOfFile 由文件名（优先）或父目录推导原语类型 token：
// xxx.tool.md → tool；裸 xxx.md 在类型目录下 → 该目录 token；否则 file。
func TypeOfFile(fileName, dir string) string {
	if p := contractmd.TypeOfFileName(fileName); p != nil {
		return p.Token
	}
	dirBase := filepath.Base(filepath.ToSlash(dir))
	if p := contractmd.TypeByDir(dirBase); p != nil {
		return p.Token
	}
	return "file"
}

// SanitizeName 文件名净化：去路径分隔符与非法字符。
func SanitizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, name)
	return name
}

// memoryCategoryNameMaxRunes 记忆类别名长度上限（字符数，防超长文件名）。
const memoryCategoryNameMaxRunes = 64

// memoryReservedNames Windows 保留设备名（拼上 .md 后仍是保留设备，故整体拒绝）。
var memoryReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// ValidMemoryCategoryName 校验记忆类别名的文件名字符安全性（A-37 单源，memory 域 + config 域
// 记忆类别提示词文件复用）：非空、限长、无首尾空白、无空白/控制字符、禁路径分隔符与 Windows
// 保留字符（/ \ : * ? " < > |）、首字符非 '.'（避免 "."/".."/隐藏文件）、非保留设备名。
// 类别名将拼为 `<类别>.md` 与 `memory/<类别>.md`，故含 `..` / 分隔符即路径穿越 → 必须拒绝。
func ValidMemoryCategoryName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > memoryCategoryNameMaxRunes {
		return false
	}
	if name != strings.TrimSpace(name) || strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return false
		}
	}
	return !memoryReservedNames[strings.ToUpper(name)]
}

// NormalizeFileName 规范化原语文件名：净化 + 确保 .md 后缀（不产生双扩展）。
func NormalizeFileName(name string) string {
	name = SanitizeName(name)
	if strings.ToLower(filepath.Ext(name)) != ".md" {
		name += ".md"
	}
	return name
}

// FileName 生成 <净化名>.<token>.md（新建）；name 可带或不带 .md/类型后缀。
func FileName(name, token string) string {
	base := SanitizeName(name)
	low := strings.ToLower(base)
	// 去掉已带类型后缀/纯 .md
	if p := contractmd.TypeOfFileName(base); p != nil {
		base = strings.TrimSuffix(base, "."+p.Token+".md")
	} else if strings.HasSuffix(low, ".md") {
		base = base[:len(base)-3]
	}
	base = strings.TrimSuffix(base, ".")
	return base + "." + token + ".md"
}

// MaxDocReadBytes 是 capability 文本文件（契约文档 / 记忆类别文件 / 系统提示词文档）整读的
// **尺寸上限**（A-40，洪泛防护）：超限拒绝整读，避免单一超大文件把内存读爆。口径取 8MB ——
// 承载的是用户可编辑的系统文档 / 记忆全文，故比 filesys 的 512KB 文本预览口径更宽。
const MaxDocReadBytes = 8 << 20

// ReadFileCapped 读文件（读前 os.Stat 判尺寸上限）：超限或 stat 失败 → 报错，不整读入内存（A-40）。
func ReadFileCapped(path string, maxBytes int64) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxBytes {
		return nil, fmt.Errorf("file %s exceeds read limit (%d > %d bytes)", path, st.Size(), maxBytes)
	}
	return os.ReadFile(path)
}

// PreviewDescription 读文件取摘要首行（列表描述预览）：
//   - `.md`（含原语契约）→ 解析 v2 契约取摘要首行（整读上限 MaxDocReadBytes，A-40）；
//   - 其余（json/yaml/图片/二进制…）→ **只读文件头部**有限前缀取首个文本行（含 NUL / 该行非合法
//     UTF-8 → 视为二进制，返回空串），**不整读大文件**（req4/req5：非 md 文件也须在列表里可见）。
func PreviewDescription(path string) (string, error) {
	if strings.HasSuffix(strings.ToLower(filepath.Base(path)), ".md") {
		raw, err := ReadFileCapped(path, MaxDocReadBytes)
		if err != nil {
			return "", err
		}
		return truncatePreview(ParseDoc(string(raw)).Description), nil
	}
	raw, err := ReadPrefix(path, previewProbeBytes)
	if err != nil {
		return "", err
	}
	return truncatePreview(textPreviewLine(raw)), nil
}

// previewProbeBytes 非原语文件描述预览的**前缀探测**上限（只读文件头部，避免整读大文件）。
const previewProbeBytes = 8 << 10

// ReadPrefix 读文件头部有限字节（不超过 maxBytes；不做尺寸拒绝，供预览/探测用）。
func ReadPrefix(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxBytes))
}

// truncatePreview 取文本首行并截断为 60 字符（列表描述预览口径）。
func truncatePreview(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}

// textPreviewLine 从文件头部字节取首个文本行：含 NUL 或该行非合法 UTF-8 → 视二进制返回空串。
func textPreviewLine(raw []byte) string {
	if bytes.IndexByte(raw, 0) >= 0 {
		return ""
	}
	line := raw
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		line = raw[:i]
	}
	line = bytes.TrimRight(line, "\r")
	s := string(line)
	if !utf8.ValidString(s) {
		return ""
	}
	return s
}

// SafeJoin 拼接路径（相对 rel 在 root 下；绝对 rel 原样——归属已由调用方校验）。
func SafeJoin(root, rel string) string {
	if rel == "" {
		return root
	}
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

// RelOf 取 dir 相对 root 的相对路径（无法计算 → 空串）。
func RelOf(root, dir string) string {
	rel, _ := filepath.Rel(root, dir)
	return rel
}

// StrictlyWithin 判定 child 是否**严格**落在 root 内（root 自身不算；纯字符串前缀判定，
// 目标路径可以尚不存在）。用于移动/改名的目标作用域复检（G-26）。
func StrictlyWithin(root, child string) bool {
	r := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(root)), "/")
	c := filepath.ToSlash(filepath.Clean(child))
	return strings.HasPrefix(c, r+"/")
}

// RealPathOrAncestor 解析 path 的符号链接**真实落点**：path 存在 → EvalSymlinks 自身；不存在
// （新建目标）→ 逐级上溯到**最近存在的祖先**解析后回拼尚不存在的路径段。已到根仍不可解析
// （异常）→ ok=false。用于写前 symlink 越界复验（A-29/A-32），各域共用避免重复实现。
func RealPathOrAncestor(path string) (string, bool) {
	p := filepath.Clean(path)
	var missing []string
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real, true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", false
		}
		missing = append(missing, filepath.Base(p))
		p = parent
	}
}

// RealPathWithin 写前越界复验（A-32）：StrictlyWithin 仅纯词法校验，root 内指向 root 外的 symlink
// （或 symlink 目标）会被 os.WriteFile / os.MkdirAll / os.RemoveAll 跟随越界。此处解析 path 的
// 真实落点（不存在则解析最近存在的祖先后回拼，见 RealPathOrAncestor）后复判仍**严格**落在 root
// 内；越界 / 不可解析 → false。
func RealPathWithin(root, path string) bool {
	real, ok := RealPathOrAncestor(path)
	if !ok {
		return false
	}
	return StrictlyWithin(root, real)
}

// realTrustRoot 解析信任根自身的真实落点（符号链接前缀先解析；失败回落字面）。
func realTrustRoot(root string) string {
	if r, ok := RealPathOrAncestor(root); ok {
		return r
	}
	return root
}

// RealPathInside 写/删前越界复验（A-36/A-38）：与 RealPathWithin 同法，但**先解析 root 自身**的
// 符号链接前缀再复判 path 严格落在 root 内。capfs 各写/删入口的 root 由调用方按级别根拼接而来
// （workdir / usrPath 可能带符号链接），故不能以字面 root 直接比对；解析 root 后再比对 → 既
// 不误判合法请求，又能拦下 root **内部**指向根外的 symlink（如 mcps/、system/ 被链接到根外）。
// 越界 / 不可解析 → false。
func RealPathInside(root, path string) bool {
	return RealPathWithin(realTrustRoot(root), path)
}

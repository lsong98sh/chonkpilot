// Package uploadfs 收敛「附件/截图上传」的落盘口径（2026-10-10，61-消息一览 §1 gui.upload）：
// **落盘位置由 server 决定** = `<data root>/<dir>/<name>`；只回真实落盘文件名（不暴露服务器路径）。
// gui 桥（native，src/lib/gui/bridge）与 browser 入口（src/lib/llm/httpapi）共用本包，
// 避免「dir/name 净化 + 大小限制 + base64 解码」两端重复实现导致行为漂移。
//
// 契约（61 §1）：payload `{name, data, kind, dir?}`；`dir` = 前端指定的二级目录（缺省
// `tmp/uploads`；截图传 `screenshot`）；`data` = base64 / data URL，原样落盘。
package uploadfs

import (
	"encoding/base64"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/idgen"
)

// DefaultDir 是 payload.dir 缺省值（前端未指定二级目录时的落盘位置）。
const DefaultDir = "tmp/uploads"

// MaxDirDepth 是 dir 允许的最大层级（如 `tmp/uploads` = 2 层）；超出 → 回落 DefaultDir。
const MaxDirDepth = 2

// MaxBytes 是单次上传载荷的**明文**大小上限（DoS 防护）：100MB。
const MaxBytes = 100 << 20

// MaxBase64Len 是 base64 原文长度上限（≈ 明文 * 4/3 + 少量填充）：解码前快速拒绝，
// 避免先分配巨大缓冲再解码。
const MaxBase64Len = MaxBytes/3*4 + 8

// SanitizeDir 净化二级目录 dir：仅允许**数据根内的相对路径**（斜杠分隔、层级 ≤ MaxDirDepth、
// 每段非空且不为 `.`/`..`）。绝对路径 / 盘符 / 穿越 / 层级超限 / 空 → 回落 DefaultDir
// （不拒绝请求，落到安全缺省目录）。
func SanitizeDir(dir string) string {
	d := strings.TrimSpace(strings.ReplaceAll(dir, "\\", "/"))
	if d == "" {
		return DefaultDir
	}
	if strings.HasPrefix(d, "/") || filepath.IsAbs(filepath.FromSlash(d)) {
		return DefaultDir
	}
	clean := path.Clean(d)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return DefaultDir
	}
	segs := strings.Split(clean, "/")
	if len(segs) > MaxDirDepth {
		return DefaultDir
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return DefaultDir
		}
	}
	return strings.Join(segs, "/")
}

// SanitizeName 净化文件名 name：仅取 base（防路径穿越）；空 / `.` / `..` → "attachment"。
func SanitizeName(name string) string {
	n := filepath.Base(strings.TrimSpace(name))
	if n == "" || n == "." || n == ".." {
		return "attachment"
	}
	return n
}

// DecodeData 解码 payload.data（base64，允许 `data:<mime>;base64,` 前缀）：解码前按 base64
// 原文长度快速拒绝，解码后按明文长度二次校验（DoS 防护）。空 / 非法 base64 → 明确错误。
func DecodeData(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("UploadAttachment: data required")
	}
	if strings.HasPrefix(s, "data:") {
		if i := strings.Index(s, ","); i >= 0 {
			s = s[i+1:]
		}
	}
	s = strings.TrimSpace(s)
	if len(s) > MaxBase64Len {
		return nil, fmt.Errorf("UploadAttachment: data too large (limit %d MB)", MaxBytes>>20)
	}
	content, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("UploadAttachment: bad base64: %w", err)
	}
	if len(content) > MaxBytes {
		return nil, fmt.Errorf("UploadAttachment: data too large (limit %d MB)", MaxBytes>>20)
	}
	return content, nil
}

// Save 把 content 落盘到 `<root>/<cleaned dir>/<uuid><ext>`，返回真实落盘文件名（uuid+ext）。
// dir 经 SanitizeDir 净化（越界/超限回落 DefaultDir）；保留原扩展名（便于 /show/ 与图片类型
// 识别），文件名主体用 uuid 隔离重复上传。
//
// R7-B-4：落盘后按 KeepCap 对该目录做容量兜底（最旧优先删除）——gui 桥与 browser 入口
// （httpapi guiUpload）共用本函数 → 兜底**同源**，不再只有 GUI 启动期一次。
func Save(root, dir, origName string, content []byte) (string, error) {
	destDir := filepath.Join(root, filepath.FromSlash(SanitizeDir(dir)))
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	fileName := idgen.NewUUID() + filepath.Ext(SanitizeName(origName))
	dest := filepath.Join(destDir, fileName)
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		return "", err
	}
	EnforceKeepCap(destDir, KeepCap) // 尽力而为，不阻断落盘
	return fileName, nil
}

// KeepCap 是单个上传留存目录的容量上限（D-45 洪泛兜底，**按目录分别计算**）：2 GiB。
// 正常使用（< 2 GiB）永不触发；仅长期累积 / 洪泛时按 mtime 最旧优先删除直至 ≤ 上限。
const KeepCap = int64(2) << 30

// EnforceKeepCap 在目录总量超过 cap 时按 mtime **最旧优先**删除文件，直至总量 ≤ cap。
// 尽力而为（读目录 / 删除失败静默跳过）——不阻断调用方；目录不存在 → 无操作。
// 仅删顶层文件（不递归）；返回删除的文件数。与 gui 桥既有 SweepUploads 同口径。
func EnforceKeepCap(dir string, cap int64) int {
	if dir == "" || cap <= 0 {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	type uploadFile struct {
		path  string
		size  int64
		mtime time.Time
	}
	files := make([]uploadFile, 0, len(entries))
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, uploadFile{filepath.Join(dir, e.Name()), fi.Size(), fi.ModTime()})
		total += fi.Size()
	}
	if total <= cap {
		return 0
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.Before(files[j].mtime) })
	removed := 0
	for _, f := range files {
		if total <= cap {
			break
		}
		if err := os.Remove(f.path); err != nil {
			continue
		}
		total -= f.size
		removed++
	}
	return removed
}

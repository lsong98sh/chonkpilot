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
	"sync"
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

// safeExts 是**允许保留原扩展名**的白名单（其余一律落 `.bin`）。落盘目录经静态面 `/show/`
// 直出，若保留可执行脚本的类型（如 .html/.svg/.xhtml/.xml）会形成**存储型 XSS** 面；
// 白名单=已知安全的图片/音视频/文档/压缩类型。未知/危险 → `.bin`（下载为二进制，不内联渲染）。
var safeExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".bmp": true, ".ico": true, ".tif": true, ".tiff": true,
	".mp3": true, ".wav": true, ".ogg": true, ".m4a": true, ".flac": true, ".aac": true,
	".mp4": true, ".webm": true, ".mov": true, ".mkv": true, ".avi": true,
	".pdf": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".txt": true, ".csv": true, ".md": true, ".json": true,
	".zip": true, ".7z": true, ".rar": true, ".gz": true,
}

// sanitizeExt 返回可安全保留的扩展名（小写）；不在白名单 → ".bin"。
func sanitizeExt(name string) string {
	ext := strings.ToLower(filepath.Ext(SanitizeName(name)))
	if safeExts[ext] {
		return ext
	}
	return ".bin"
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
// dir 经 SanitizeDir 净化（越界/超限回落 DefaultDir）；扩展名经 sanitizeExt **白名单**过滤
// （不在白名单 → `.bin`，规避静态面存储型 XSS），文件名主体用 uuid 隔离重复上传。
//
// R7-B-4：落盘后按 KeepCap 对该目录做容量兜底（最旧优先删除）——gui 桥与 browser 入口
// （httpapi guiUpload）共用本函数 → 兜底**同源**，不再只有 GUI 启动期一次。
// R8-B-5：兜底改走增量记账（maybeEnforceKeepCap）——热路径避免每次全量 os.ReadDir + 排序，
// 仅在记账总量可能越限时才做一次全量统计；兜底语义不变。
func Save(root, dir, origName string, content []byte) (string, error) {
	destDir := filepath.Join(root, filepath.FromSlash(SanitizeDir(dir)))
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	fileName := idgen.NewUUID() + sanitizeExt(origName)
	dest := filepath.Join(destDir, fileName)
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		return "", err
	}
	maybeEnforceKeepCap(destDir, int64(len(content))) // 尽力而为，不阻断落盘
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

// keepCapSum 是 uploadfs 的进程内增量记账（dir → 累计落盘字节），用于在热路径快速判
// 「可能超限」（R8-B-5）；仅当记账值 > 阈值时才做一次全量统计。清零仅随进程重启。
var (
	keepCapMu  sync.Mutex
	keepCapSum = map[string]int64{}
)

// maybeEnforceKeepCap 增量记账 dir 的新增字节；仅当记账总量 > KeepCap 时调用 EnforceKeepCap
// 做**全量统计**（最旧优先删除）并以磁盘真实总量回填计数。尽力而为，不阻断落盘（R8-B-5）。
func maybeEnforceKeepCap(dir string, added int64) {
	keepCapMu.Lock()
	keepCapSum[dir] += added
	over := keepCapSum[dir] > KeepCap
	keepCapMu.Unlock()
	if !over {
		return
	}
	EnforceKeepCap(dir, KeepCap)
	total := dirTotalBytes(dir)
	keepCapMu.Lock()
	keepCapSum[dir] = total
	keepCapMu.Unlock()
}

// dirTotalBytes 统计 dir 下**顶层文件**的字节总量（不递归；目录不存在 / 读取失败 → 0）。
func dirTotalBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if fi, err := e.Info(); err == nil {
			total += fi.Size()
		}
	}
	return total
}

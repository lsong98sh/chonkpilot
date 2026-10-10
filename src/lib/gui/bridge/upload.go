// 附件上传（富文本输入区 图片/文件附件 + 截图）：server 决定落盘位置 =
// <prjusr 数据根>/<dir>/<uuid><ext>，**只回真实落盘文件名**（20-gui：附件走 api 上传，
// 消息内用相对数据根的引用 `dir/name`，见 61-消息一览 §1 gui.upload、33-对话）。
//
// 2026-09-04：原 /call UploadAttachment 注册已清零；callUploadAttachment 保留为 gui.upload
// 消息面内部实现（guimsg.go）。
// 2026-10-10：result 由 {file_id,name,path,url} 收敛为 {name}（不暴露服务器路径）；落盘目录
// 由 payload.dir 决定（缺省 tmp/uploads，截图传 screenshot），净化/大小限制抽到共享包 uploadfs。
package bridge

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/uploadfs"
)

type uploadAttachmentReq struct {
	Name string `json:"name"`
	Data string `json:"data"` // base64（允许 data:image/png;base64, 前缀）
	Kind string `json:"kind"` // image|file（仅用于前端缩略图样式，不校验）
	Dir  string `json:"dir"`  // 二级目录（缺省 tmp/uploads；截图传 screenshot）
}

// callUploadAttachment 保存附件到 <prjusr 数据根>/<dir>/<uuid><ext>（dir 净化见 uploadfs），
// 返回 {name}（真实落盘文件名）。落盘位置由 server 决定 —— 不回 path/url/file_id（不暴露服务器路径）。
func callUploadAttachment(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	var req uploadAttachmentReq
	if len(params) > 0 {
		raw := params[0]
		// 兼容调用方误传单元素数组 [{...}]（前端 Qe rest 参数 + 数组调用会包两层）
		if len(raw) > 0 && raw[0] == '[' {
			var arr []json.RawMessage
			if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
				raw = arr[0]
			}
		}
		_ = json.Unmarshal(raw, &req)
	}
	content, err := uploadfs.DecodeData(req.Data)
	if err != nil {
		return nil, err
	}
	// 目录：prjusr 数据根（注入的 prjusr 根；缺省回落 <workDir>/.chonkpilot —— 与 server 读侧
	// 同一口径，见 12-数据层 §3 / 24 §3.2 MW-8）下按 payload.dir 取二级目录。
	name, err := uploadfs.Save(b.uploadRoot(), req.Dir, req.Name, content)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{msgkeys.GuiUploadResultName: name})
}

// UploadKeepCap 是附件/截图留存目录的**容量上限**（D-45，洪泛兜底，按目录分别计算）。
//
// 口径（D-45）：留存目录为**留存目录** —— 文件被已落库消息以 `dir/name` 引用（预览经 `/show/`
// 同源取回），故**不随会话清理**（删了会破坏历史消息的缩略图）。但目录无上限会无限累积 →
// 启动期按容量兜底：总量超限时按 mtime **最旧优先**删除，直至降到上限内。正常使用（< 2 GiB）
// 永不触发；仅长期累积 / 洪泛时兜底。
const UploadKeepCap = int64(2) << 30 // 2 GiB

// sweepSubDirs 是纳入容量兜底的二级目录（= gui.upload payload.dir 取值；新增上传目录须在此登记）。
var sweepSubDirs = []string{uploadfs.DefaultDir, "screenshot"}

// SweepUploads 启动期清扫附件/截图留存目录（D-45）：对 <root> 下已知上传二级目录
// （tmp/uploads、screenshot），**仅**在目录总量超过 cap 时按 mtime 最旧优先删除文件直至
// 总量 ≤ cap。尽力而为（读目录 / 删除失败仅告警，不阻断启动）；不递归更深层。
// 返回删除的文件数（正常使用恒为 0）。
func SweepUploads(root string, cap int64) int {
	if root == "" || cap <= 0 {
		return 0
	}
	removed := 0
	for _, sub := range sweepSubDirs {
		removed += sweepUploadDir(filepath.Join(root, filepath.FromSlash(sub)), cap)
	}
	return removed
}

// sweepUploadDir 清扫单个留存目录（容量兜底：超 cap → 最旧优先删）。返回删除的文件数。
func sweepUploadDir(dir string, cap int64) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0 // 目录不存在等：无需清扫
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
			slog.Warn("sweep uploads remove failed", "path", f.path, "err", err)
			continue
		}
		total -= f.size
		removed++
	}
	if removed > 0 {
		slog.Warn("uploads 超容量上限，已按最旧优先清理", "dir", dir, "removed", removed, "cap_mb", cap>>20)
	}
	return removed
}

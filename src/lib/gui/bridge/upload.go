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
	"path/filepath"

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

// sweepSubDirs 是纳入容量兜底的二级目录（= gui.upload payload.dir 取值；新增上传目录须在此登记）。
var sweepSubDirs = []string{uploadfs.DefaultDir, "screenshot"}

// SweepUploads 启动期清扫附件/截图留存目录（D-45）：对 <root> 下已知上传二级目录
// （tmp/uploads、screenshot），**仅**在目录总量超过 `uploadfs.KeepCap` 时按 mtime 最旧优先删除
// 文件直至 ≤ 上限。兜底实现收敛到 `uploadfs.EnforceKeepCap`（gui 桥与 browser 入口同源，R8-B-4）；
// 本函数仅负责遍历已知二级目录。尽力而为（不阻断启动）；不递归更深层。
// 返回删除的文件数（正常使用恒为 0）。
func SweepUploads(root string) int {
	if root == "" {
		return 0
	}
	removed := 0
	for _, sub := range sweepSubDirs {
		dir := filepath.Join(root, filepath.FromSlash(sub))
		n := uploadfs.EnforceKeepCap(dir, uploadfs.KeepCap)
		if n > 0 {
			slog.Warn("uploads 超容量上限，已按最旧优先清理", "dir", dir, "removed", n, "cap_mb", uploadfs.KeepCap>>20)
		}
		removed += n
	}
	return removed
}

// 附件上传（富文本输入区 图片/文件附件 + 截图）：base64 → 保存数据根 tmp/uploads/，
// 返回 file_id / 本地 path / /show/ 预览 URL（20-gui：附件走 api 上传，消息内用引用）。
//
// 2026-09-04：原 /call UploadAttachment 注册已清零；callUploadAttachment 保留为 gui.upload
// 消息面内部实现（guimsg.go）。
package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// showURL 构造 /show/ 预览 URL 并追加 instance_id 查询参数（与前端 getFileUrl 口径一致：
// 业务惯例必带实例字段，61-消息一览 §0）；桥侧 /show/ 处理只按 r.URL.Path 取文件，忽略该 query。
func (b *Bridge) showURL(path string) string {
	u := "/show/" + filepath.ToSlash(path)
	if b.instanceID != "" {
		u += "?instance_id=" + url.QueryEscape(b.instanceID)
	}
	return u
}

type uploadAttachmentReq struct {
	Name string `json:"name"`
	Data string `json:"data"` // base64（允许 data:image/png;base64, 前缀）
	Kind string `json:"kind"` // image|file（仅用于前端缩略图样式，不校验）
}

// 附件上传大小上限（DoS 防护）：100MB 明文。base64 原文长度上限 ≈ 明文 * 4/3 + 少量填充，
// 用于解码前快速拒绝（避免先分配巨大缓冲再解码）。
const (
	maxUploadBytes     = 100 << 20
	maxUploadBase64Len = maxUploadBytes/3*4 + 8
)

// callUploadAttachment 保存附件到 <prjusr 数据根>/tmp/uploads/<uuid><ext>（安全文件名），
// 返回 {file_id, name, path, url}；url = /show/<abs path>（同源预览，DataDirs 放行）。
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
	raw := req.Data
	if raw == "" {
		return nil, fmt.Errorf("UploadAttachment: data required")
	}
	// 剥 data:<mime>;base64, 前缀
	if strings.HasPrefix(raw, "data:") {
		if i := strings.Index(raw, ","); i >= 0 {
			raw = raw[i+1:]
		}
	}
	raw = strings.TrimSpace(raw)
	// 解码前按 base64 原文长度快速拒绝（超限直接报错，不进入解码分配）。
	if len(raw) > maxUploadBase64Len {
		return nil, fmt.Errorf("UploadAttachment: data too large (limit %d MB)", maxUploadBytes>>20)
	}
	content, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("UploadAttachment: bad base64: %w", err)
	}
	if len(content) > maxUploadBytes {
		return nil, fmt.Errorf("UploadAttachment: data too large (limit %d MB)", maxUploadBytes>>20)
	}
	// 目录：prjusr 数据根 tmp/uploads（注入的 prjusr 根；缺省回落 <workDir>/.chonkpilot ——
	// 与 server 读侧 imageUploadDir 同一口径，见 12-数据层 §3 / 24 §3.2 MW-8）
	uploadDir := filepath.Join(b.uploadRoot(), "tmp", "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return nil, err
	}
	// 文件名净化：仅取 base，防路径穿越；重复上传用 uuid 前缀隔离
	name := filepath.Base(req.Name)
	if name == "" || name == "." {
		name = "attachment"
	}
	fileID := newUUID() + filepath.Ext(name)
	dest := filepath.Join(uploadDir, fileID)
	if err := os.WriteFile(dest, content, 0644); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"file_id": fileID,
		"name":    name,
		"path":    dest,
		"url":     b.showURL(dest),
	})
}

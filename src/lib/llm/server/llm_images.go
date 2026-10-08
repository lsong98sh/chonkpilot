// 图片（多模态）输入（40 P2-8「图片上传给 LLM」）。
//
// 数据流（**不改消息面 payload**）：前端把输入区附件序列化为 markdown 引用 `![名](路径)`
// 随用户消息文本发送（33-对话 CHAT-014-S04；gui.upload 落 <数据根>/tmp/uploads/）→ server
// 落库为 user 消息的 Content 文本 → 本文件在**构造 LLM 请求体时**把引用就地展开为多模态
// 内容块（chat / responses 两协议，见 llm.go / llm_responses.go）。持久化、快照、压缩只见文本。
//
// 请求形态：
//   - chat（openai 兼容）：content 由字符串改为数组
//     [{type:"text",text}, {type:"image_url",image_url:{url:"data:<mime>;base64,…"}}]；
//   - responses：input message item 的 content 数组
//     [{type:"input_text",text}, {type:"input_image",image_url:"data:<mime>;base64,…"}]。
//
// 为何用 base64 data URL：provider baseUrl 多为远端（DeepSeek/OpenAI 等），本地文件路径
// 模型侧不可访问；data URL 自包含、无需额外图床。取舍 = 请求体随图片线性增大（受下述上限
// 约束）与首字节耗时略增（超时已由 responseTimeout / streamTimeout 覆盖）。
//
// 历史与压缩（明确规则）：
//   - 仅**最近 keepImageTurns 轮**保留原图（轮边界 = isTurnBoundary，与 assemble.go 同源）；
//   - 更早轮次的引用降级为**文本占位** `[图片: 名]`——既不放大请求体，也不会因临时文件被
//     清理而让整轮失败；
//   - 压缩插件（plugin-compress）无需改动：早前轮次整轮被摘要文本替换，图片随原文一并消失；
//     非维持轮若保留（无结论）也已是「最近 N 轮」之外的占位文本。
//
// 上限与错误（明确报错，不静默丢）：
//   - 类型白名单 png/jpeg/webp/gif（扩展名 + 魔数双重校验，mime 以魔数为准）；
//   - 单图 ≤ maxImageBytes、单条消息 ≤ maxImagesPerMessage、单次请求 ≤ maxImagesPerRequest；
//   - 文件缺失/不可读/类型不支持/超限 → *LLMError{Kind: ErrProtocol}（不可重试，随 llm-error 可见）。
//
// 范围：仅 role=user 的消息；仅**落在上传根目录内**的引用按图片处理（上传根之外的本地路径
// 原样保留为文本，避免用户手写 markdown 时被当成附件读取）。
package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-data"
)

const (
	// defaultKeepImageTurns 保留原图的最近轮数缺省值（更早轮次降级为文本占位）。
	defaultKeepImageTurns = 3
	// maxImageBytes 单张图片原始字节上限（5MB）。
	maxImageBytes = 5 << 20
	// maxImagesPerMessage 单条消息图片数上限。
	maxImagesPerMessage = 4
	// maxImagesPerRequest 单次请求图片总数上限。
	maxImagesPerRequest = 8
)

// ImageOptions 是图片展开选项（ChatOptions.Images）。
type ImageOptions struct {
	// UploadDir 允许读取的图片根目录（= <数据根>/tmp/uploads；imageUploadDir 生成）。
	// 为空 → 不展开任何图片（保持纯文本，行为与历史一致）。
	UploadDir string
	// KeepTurns 保留原图的最近轮数（<=0 → defaultKeepImageTurns）。
	KeepTurns int
}

// imageUploadDir 返回实例的上传目录（与桥 gui.upload 落点一致，见 chonkpilot-gui/bridge/upload.go）：
// **prjusr 数据根**下 `tmp/uploads` —— data_dir 非空 → 该目录（prj 与 prjusr 同根）；
// data_dir 空 → `~/.chonkpilot/data/<prj-id>/tmp/uploads`（B 方案，[24 §3.2] MW-8）。
// work_dir/data_dir 皆空、或数据根不可解析 → 空串（调用方据此不展开，退化为纯文本）。
func imageUploadDir(dataDir, workDir string) string {
	if dataDir == "" && workDir == "" {
		return ""
	}
	root, err := data.PrjUsrDir(workDir, dataDir)
	if err != nil || root == "" {
		return ""
	}
	return filepath.Join(root, "tmp", "uploads")
}

// mdImageRe 匹配 markdown 图片引用 `![名](路径)`（与前端 InputBox.parseAttachmentMarkers 同式）。
var mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)

// imagePart 是一张已校验的图片（data URL 用 mime + base64 载荷）。
type imagePart struct {
	mime string
	b64  string
}

// dataURL 返回自包含的 data URL（chat 的 image_url.url / responses 的 image_url）。
func (p imagePart) dataURL() string { return "data:" + p.mime + ";base64," + p.b64 }

// messageImages 是一条消息的图片展开结果：text = 引用被替换（原图/占位）后的文本；
// images = 需以图片块发送的图片（按出现顺序）。
type messageImages struct {
	text   string
	images []imagePart
}

// expandImages 按 opts 展开 msgs 中的图片引用，返回与 msgs 等长的结果切片。
// UploadDir 为空 → 全部条目的 text = 原始 Content、images 为空（纯文本路径零改动）。
func expandImages(msgs []ChatMsg, opts ImageOptions) ([]messageImages, error) {
	out := make([]messageImages, len(msgs))
	for i, m := range msgs {
		out[i].text = m.Content
	}
	if opts.UploadDir == "" {
		return out, nil
	}
	root, err := filepath.Abs(opts.UploadDir)
	if err != nil {
		return out, nil // 根目录不可解析 → 退化为纯文本（非图片错误，不阻断请求）
	}
	keepTurns := opts.KeepTurns
	if keepTurns <= 0 {
		keepTurns = defaultKeepImageTurns
	}
	// 轮次归属：轮边界（isTurnBoundary）前导消息记 -1；边界后每遇一条边界 +1。
	turns := make([]int, len(msgs))
	turn, total := -1, 0
	for i, m := range msgs {
		if isTurnBoundary(m) {
			turn++
			total++
		}
		turns[i] = turn
	}
	firstRecent := total - keepTurns // 该下标及以后的轮次保留原图

	perReq := 0
	for i, m := range msgs {
		if m.Role != "user" {
			continue
		}
		locs := mdImageRe.FindAllStringSubmatchIndex(m.Content, -1)
		if len(locs) == 0 {
			continue
		}
		var b strings.Builder
		last := 0
		msgCount := 0
		recent := total > 0 && turns[i] >= firstRecent
		for _, loc := range locs {
			b.WriteString(m.Content[last:loc[0]])
			last = loc[1]
			name := m.Content[loc[2]:loc[3]]
			path := m.Content[loc[4]:loc[5]]
			if !withinDir(root, path) {
				b.WriteString(m.Content[loc[0]:loc[1]]) // 上传根之外的本地路径原样保留为文本
				continue
			}
			if name == "" {
				name = filepath.Base(path)
			}
			if !recent {
				b.WriteString("[图片: " + name + "]") // 非最近轮次 → 文本占位
				continue
			}
			msgCount++
			perReq++
			if msgCount > maxImagesPerMessage {
				return nil, imageErr("单条消息图片数量超限（%d > %d）: %s", msgCount, maxImagesPerMessage, path)
			}
			if perReq > maxImagesPerRequest {
				return nil, imageErr("单次请求图片数量超限（%d > %d）: %s", perReq, maxImagesPerRequest, path)
			}
			part, err := loadImagePartInRoot(root, path)
			if err != nil {
				return nil, err
			}
			out[i].images = append(out[i].images, part)
			b.WriteString("[图片: " + name + "]")
		}
		b.WriteString(m.Content[last:])
		out[i].text = b.String()
	}
	return out, nil
}

// loadImagePartInRoot 在上传根约束下加载一张图片（B-11：先解析符号链接，复验真实路径仍在根内）。
func loadImagePartInRoot(root, path string) (imagePart, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return imagePart{}, imageErr("图片文件不可读: %s（%v）", path, err)
	}
	if !withinDir(root, real) {
		return imagePart{}, imageErr("图片路径越界（符号链接指向上传根之外）: %s", path)
	}
	return loadImagePart(real)
}

// imageCacheKey 是图片编码缓存键（真实路径 + 大小 + mtime → 同一文件未变则命中）。
type imageCacheKey struct {
	path    string
	size    int64
	modTime int64
}

// imageCache 是进程内图片编码缓存（B-10：同一图片每请求重复读取 + base64 重编码 → 只做一次；
// 键含 size/mtime，文件变更自动失效；并发安全）。
var imageCache = struct {
	mu sync.Mutex
	m  map[imageCacheKey]imagePart
}{m: make(map[imageCacheKey]imagePart)}

// loadImagePart 读取并校验一张图片：类型白名单（扩展名 + 魔数）、单图大小上限；
// 命中进程内编码缓存则直接返回（path 须为已解析符号链接的真实路径）。
func loadImagePart(path string) (imagePart, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := imageExtMime[ext]; !ok {
		return imagePart{}, imageErr("图片类型不支持（仅支持 png/jpeg/webp/gif）: %s", path)
	}
	// 先按 (path,size,mtime) 查缓存；命中即返回（免读盘 + 免重编码）。
	fi, statErr := os.Stat(path)
	var key imageCacheKey
	if statErr == nil {
		key = imageCacheKey{path: path, size: fi.Size(), modTime: fi.ModTime().UnixNano()}
		imageCache.mu.Lock()
		if p, ok := imageCache.m[key]; ok {
			imageCache.mu.Unlock()
			return p, nil
		}
		imageCache.mu.Unlock()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return imagePart{}, imageErr("图片文件不可读: %s（%v）", path, err)
	}
	if len(data) == 0 {
		return imagePart{}, imageErr("图片文件为空: %s", path)
	}
	if len(data) > maxImageBytes {
		return imagePart{}, imageErr("图片过大（%d 字节 > 上限 %d 字节）: %s", len(data), maxImageBytes, path)
	}
	mime := sniffImageMime(data)
	if mime == "" {
		return imagePart{}, imageErr("图片内容不可识别（非 png/jpeg/webp/gif）: %s", path)
	}
	part := imagePart{mime: mime, b64: base64.StdEncoding.EncodeToString(data)}
	// 回填缓存（仅当 stat 成功、键稳定；否则退化为每次重读，不影响正确性）。
	if statErr == nil {
		imageCache.mu.Lock()
		imageCache.m[key] = part
		imageCache.mu.Unlock()
	}
	return part, nil
}

// imageExtMime 是图片扩展名白名单（错误信息与早期拒绝用；真实 mime 以魔数为准）。
var imageExtMime = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".gif": "image/gif",
}

// sniffImageMime 按魔数识别图片类型（不可识别 → 空串）。
func sniffImageMime(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case len(data) >= 6 && (bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))):
		return "image/gif"
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

// withinDir 判断 abs 路径是否位于 root 目录内（root 须为绝对路径；防越界读取）。
func withinDir(root, path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// imageErr 构造图片输入错误（Kind=ErrProtocol，不可重试；文案随 llm-error 直达用户）。
func imageErr(format string, a ...any) *LLMError {
	return llmErr(ErrProtocol, fmt.Sprintf(format, a...))
}

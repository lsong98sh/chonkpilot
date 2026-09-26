// Package memory 是 memory 域门面实现（`chonkpilot-data/facade` 的 MemoryAPI）的落地包
// （阶段 4「internal 下沉」：由 `chonkpilot-data/persist` 下沉至此）。
//
// 覆盖：记忆库 = **会话沉淀的 LLM 记忆**（类别清单 / 类别全文读写 / 删自定义类别）。
// 把「领域条目（类别 / 级别 / 全文 / 预估 token）」翻译成文件落点（用户级 `用户偏好.md` +
// 项目级 `<workdir>/.chonkpilot/memory/<类别>.md`）与目录扫描类别清单；文件名规则与合法性
// 校验留在本侧（门面不交路径规则、不复制校验）。
//
// 落点（唯一用户级 + 其余项目级）：
//
//	用户偏好（不可配置）  ~/.chonkpilot/用户偏好.md
//	其余类别（项目级）    <workdir>/.chonkpilot/memory/<类别>.md
//
// 类别集合 = **预置默认集 ∪ 用户自定义集**（41 I-66）：自定义类别**仅项目级**，以「项目记忆
// 目录内的 <类别>.md 文件」为准（无额外清单文件，目录扫描即清单）。
//
// 单一实现两处绑定（同一份代码，不并列两套写法）：
//   - inline 绑定：`facade/inline.New` 直接构造本服务（同进程直调，不经 MQ）——gui 桥 /
//     browser 入口用；
//   - mq  绑定：persist 的 `data-memory-*` handler **委托本文件的同一批方法**，
//     只负责信封（reply/fail）——故两条路径行为等价（有测试）。
//
// 变更广播（订阅面；23 §7）：Save / Delete 成功后由本实现广播既有 `data-memory-refresh`
// （{instance_id, id, op, list}）——任何绑定下订阅方（server 记忆指引缓存失效等）照旧收到。
//
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package memory

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// Service 是 memory 域门面实现（inline 绑定与 MQ 信封层共用）。
type Service struct {
	*kernel.Base
}

// New 构造 memory 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Service { return &Service{Base: base} }

// 编译期断言：实现完整 memory 域门面（缺方法即编译不过）。
var _ facade.MemoryAPI = (*Service)(nil)

// MemoryUserCategory 是唯一用户级类别（不可配置；跨项目偏好）。
// 导出供 system prompt 带出指引列举（避免类别清单两处维护）。
const MemoryUserCategory = "用户偏好"

// memoryCategorySpec 描述一个记忆类别：中文文件名（去扩展名）+ 级别 + 预置用途说明。
type memoryCategorySpec struct {
	Name    string // 类别名（= 文件名去 .md）
	Level   string // capfs.KindProject | capfs.KindUser
	Purpose string // 空模板内的一句用途说明
}

// memoryCategorySpecs 是全部记忆类别（用户级 用户偏好 置末，见 42 §2 (27) 类别清单）。
var memoryCategorySpecs = []memoryCategorySpec{
	{Name: "项目概要", Level: "project", Purpose: "记录本项目的概况：业务背景、目标、范围与边界。"},
	{Name: "共同库", Level: "project", Purpose: "记录本项目跨模块复用的公共知识（概念、约定、公共组件）。"},
	{Name: "开发规范", Level: "project", Purpose: "记录本项目的编码与开发规范（命名、风格、目录约定）。"},
	{Name: "构建发布规则", Level: "project", Purpose: "记录本项目的构建、打包与发布规则。"},
	{Name: "接口库", Level: "project", Purpose: "记录本项目对内/对外的接口契约（消息、API、数据结构）。"},
	{Name: "测试规范", Level: "project", Purpose: "记录本项目的测试规范与验证方式。"},
	{Name: "典型参照", Level: "project", Purpose: "记录可参照的典型实现与范例。"},
	{Name: "用户决策", Level: "project", Purpose: "记录用户在本项目上做出的关键决策。"},
	{Name: MemoryUserCategory, Level: "user", Purpose: "记录用户的跨项目偏好（唯一用户级，不随项目变化、不可配置）。"},
}

// MemoryCategoryNames 返回**项目级**记忆类别名（中文，去 .md；供 system prompt 带出指引列举）。
func MemoryCategoryNames() []string {
	out := make([]string, 0, len(memoryCategorySpecs))
	for _, s := range memoryCategorySpecs {
		if s.Level == "project" {
			out = append(out, s.Name)
		}
	}
	return out
}

// memorySpecOf 按类别名取规格（不存在 → ok=false；命中即预置类别）。
func memorySpecOf(category string) (memoryCategorySpec, bool) {
	for _, s := range memoryCategorySpecs {
		if s.Name == category {
			return s, true
		}
	}
	return memoryCategorySpec{}, false
}

// isPresetMemoryCategory 是否预置类别（8 项目级 + 用户偏好；预置恒存在、不可删除）。
func isPresetMemoryCategory(category string) bool {
	_, ok := memorySpecOf(category)
	return ok
}

// memoryCategoryNameMaxRunes 自定义类别名长度上限（字符数，防超长文件名）。
const memoryCategoryNameMaxRunes = 64

// memoryReservedNames Windows 保留设备名（拼上 .md 后仍是保留设备，故整体拒绝）。
var memoryReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// validMemoryCategoryName 校验类别名的文件名字符安全性（预置名同样满足）：
// 非空、限长、无首尾空白、无空白/控制字符、禁路径分隔符与 Windows 保留字符
// （/ \ : * ? " < > |）、首字符非 '.'（避免 "."/".."/隐藏文件）、非保留设备名。
func validMemoryCategoryName(name string) bool {
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

// memoryPathWithin 归一后判断 path 是否落在 dir 目录内（防目录穿越；越界即拒绝）。
func memoryPathWithin(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// memoryProjectDir 项目级记忆目录（<workdir>/.chonkpilot/memory）。
func memoryProjectDir(workDir string) string {
	return filepath.Join(workDir, ".chonkpilot", "memory")
}

// memoryUserDir 用户级记忆目录（~/.chonkpilot；usrPath 注入时随其所在目录）。
func (s *Service) memoryUserDir() string {
	if s.UsrPath != "" {
		return filepath.Dir(s.UsrPath)
	}
	return filepath.Dir(data.UserPath())
}

// memoryFileFor 解析类别落盘路径：
//   - 用户偏好（唯一用户级预置）→ 用户级目录；
//   - 其余（预置项目级 + 自定义）→ 项目级记忆目录；
//
// 自定义类别名先过文件名字符安全校验，落盘路径再经归一校验确保仍在记忆根目录内
// （防目录穿越）；名称非法 / 项目级但 workDir 空 / 越界 → ok=false。
func (s *Service) memoryFileFor(workDir, category string) (path, level string, ok bool) {
	if spec, preset := memorySpecOf(category); preset && spec.Level == "user" {
		root := s.memoryUserDir()
		p := filepath.Join(root, category+".md")
		if !memoryPathWithin(root, p) {
			return "", "", false
		}
		return p, "user", true
	}
	if !validMemoryCategoryName(category) {
		return "", "", false
	}
	if workDir == "" {
		return "", "", false
	}
	root := memoryProjectDir(workDir)
	p := filepath.Join(root, category+".md")
	if !memoryPathWithin(root, p) {
		return "", "", false
	}
	return p, "project", true
}

// memoryTemplate 生成预置模板（标题 + 一句用途说明；正文留给沉淀引擎重写）。
func memoryTemplate(spec memoryCategorySpec) string {
	return "# " + spec.Name + "\n\n> 用途：" + spec.Purpose + "\n"
}

// ensureMemoryPresets 首次访问项目时预置类别文件（不存在才写，不覆盖已沉淀内容）；
// 用户偏好文件同样保证存在（唯一用户级）。
func (s *Service) ensureMemoryPresets(workDir string) error {
	for _, spec := range memoryCategorySpecs {
		path, _, ok := s.memoryFileFor(workDir, spec.Name)
		if !ok {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(memoryTemplate(spec)), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// memoryEnabledKey 是记忆库总开关的项目配置键（与 plugin-memory 同键）。
const memoryEnabledKey = "memory.enabled"

// memoryEnabledFor 读实例的 memory.enabled：prjusr（本机）优先 → prj（团队预设），与 plugin-memory
// 读同一份 prj 配置清单同口径；缺失 / 非 "true" / 读库失败 → false（默认关闭）。
//
// 层序与门面其余读点一致（实例视图 → 调用方 Scope / data 绑定表，见 internal/kernel）：
// **inline 绑定**（宿主未持实例视图）下同样可读——否则"预置类别仅在启用时落盘"的门控在门面
// 路径会误判为关闭（与 MQ 路径行为不一致）。
func (s *Service) memoryEnabledFor(instanceID string) bool {
	workDir, dataDir, ok := s.InstBindingFor(instanceID, facade.Scope{})
	if !ok {
		return false
	}
	prj, relPrj, err := kernel.OpenPrjLayer(workDir, dataDir)
	if err != nil {
		return false
	}
	defer relPrj()
	if pudb, relPu, err := kernel.OpenPrjUsrLayer(dataDir, prj); err == nil {
		defer relPu()
		if v := kernel.ConfigGet(pudb, memoryEnabledKey); v != "" {
			return v == "true"
		}
	}
	return kernel.ConfigGet(prj, memoryEnabledKey) == "true"
}

// ensureMemoryPresetsIfEnabled 仅在**记忆库启用**时预置类别文件（P0 修复：关闭态访问 list 不得
// 落盘创建 9 个预置 .md —— 页面/指引都不应在未启用时产生记忆文件）。清单本身照常返回（预置项
// 路径/tokens 与是否落盘无关），启用后的首次 list 或首次 save 仍会补齐预置文件。
func (s *Service) ensureMemoryPresetsIfEnabled(instanceID, workDir string) error {
	if !s.memoryEnabledFor(instanceID) {
		return nil
	}
	return s.ensureMemoryPresets(workDir)
}

// readFileString 读文件全文（不存在/出错 → 空串）。
func readFileString(path string) string {
	if raw, err := os.ReadFile(path); err == nil {
		return string(raw)
	}
	return ""
}

// memoryCustomCategories 扫描项目级记忆目录，返回自定义类别名（= 非预置的 <类别>.md，
// 名称合法性再次校验；按名称排序保证列表稳定）。目录不存在 → 空。
func memoryCustomCategories(workDir string) []string {
	entries, err := os.ReadDir(memoryProjectDir(workDir))
	if err != nil {
		return nil
	}
	out := make([]string, 0)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		category := strings.TrimSuffix(e.Name(), ".md")
		if isPresetMemoryCategory(category) || !validMemoryCategoryName(category) {
			continue
		}
		out = append(out, category)
	}
	sort.Strings(out)
	return out
}

// memoryList 列出全部类别 = 预置集 ∪ 自定义集（类别/级别/路径/预估 token；
// 文件缺失 → content 空、tokens 0）；自定义类别恒为项目级，附于预置之后。
func (s *Service) memoryList(workDir string) []facade.MemoryCategory {
	out := make([]facade.MemoryCategory, 0, len(memoryCategorySpecs))
	appendItem := func(category, path, level string) {
		content := readFileString(path)
		out = append(out, facade.MemoryCategory{
			Category: category,
			Level:    level,
			Path:     filepath.ToSlash(path),
			Tokens:   data.EstimateTokens(content),
		})
	}
	for _, spec := range memoryCategorySpecs {
		path, level, ok := s.memoryFileFor(workDir, spec.Name)
		if !ok {
			continue
		}
		appendItem(spec.Name, path, level)
	}
	for _, category := range memoryCustomCategories(workDir) {
		path, level, ok := s.memoryFileFor(workDir, category)
		if !ok {
			continue
		}
		appendItem(category, path, level)
	}
	return out
}

// MemoryList 列出全部类别 = 预置集 ∪ 自定义集（类别/级别/路径/预估 token；
// 文件缺失 → Content 空、Tokens 0）；首次访问按开关预置类别文件。
func (s *Service) MemoryList(req facade.MemoryListRequest) (facade.MemoryListResponse, error) {
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.MemoryListResponse{}, err
	}
	if err := s.ensureMemoryPresetsIfEnabled(req.InstanceID, workDir); err != nil {
		return facade.MemoryListResponse{}, err
	}
	return facade.MemoryListResponse{List: s.memoryList(workDir)}, nil
}

// MemoryGet 读单类全文：预置类别恒可读（文件缺失 → 空内容）；自定义类别须已存在。
func (s *Service) MemoryGet(req facade.MemoryGetRequest) (facade.MemoryGetResponse, error) {
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.MemoryGetResponse{}, err
	}
	path, level, ok := s.memoryFileFor(workDir, req.Category)
	if !ok {
		return facade.MemoryGetResponse{}, errors.New("invalid memory category name: " + req.Category)
	}
	if !isPresetMemoryCategory(req.Category) {
		if _, statErr := os.Stat(path); statErr != nil {
			return facade.MemoryGetResponse{}, errors.New("unknown memory category: " + req.Category)
		}
	}
	content := readFileString(path)
	return facade.MemoryGetResponse{Doc: facade.MemoryDoc{
		Category: req.Category, Level: level, Path: filepath.ToSlash(path),
		Content: content, Tokens: data.EstimateTokens(content),
	}}, nil
}

// MemorySave 写单类全文（写 / 建类别；写入后广播 data-memory-refresh）。
func (s *Service) MemorySave(req facade.MemorySaveRequest) (facade.MemorySaveResponse, error) {
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.MemorySaveResponse{}, err
	}
	path, _, ok := s.memoryFileFor(workDir, req.Category)
	if !ok {
		return facade.MemorySaveResponse{}, errors.New("invalid memory category name: " + req.Category)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return facade.MemorySaveResponse{}, err
	}
	if err := os.WriteFile(path, []byte(req.Content), 0o644); err != nil {
		return facade.MemorySaveResponse{}, err
	}
	s.RefreshScoped("memory", req.InstanceID, req.Category, "save", req.Scope)
	return facade.MemorySaveResponse{OK: true, ID: req.Category}, nil
}

// MemoryDelete 删**自定义类别**（移除清单项 + 内容文件）；预置类别不可删（只可清空全文）。
func (s *Service) MemoryDelete(req facade.MemoryDeleteRequest) (facade.MemoryDeleteResponse, error) {
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.MemoryDeleteResponse{}, err
	}
	path, _, ok := s.memoryFileFor(workDir, req.Category)
	if !ok {
		return facade.MemoryDeleteResponse{}, errors.New("invalid memory category name: " + req.Category)
	}
	if isPresetMemoryCategory(req.Category) {
		return facade.MemoryDeleteResponse{}, errors.New("preset memory category cannot be deleted: " + req.Category)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return facade.MemoryDeleteResponse{}, err
	}
	s.RefreshScoped("memory", req.InstanceID, req.Category, "delete", req.Scope)
	return facade.MemoryDeleteResponse{OK: true, ID: req.Category}, nil
}

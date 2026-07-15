package analyzer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TechStackItem represents a detected technology.
type TechStackItem struct {
	Name     string `json:"name"`
	Category string `json:"category"` // language / framework / tool / database
	Version  string `json:"version,omitempty"`
	Source   string `json:"source"` // config file that detected it
}

// AnalysisResult holds the full project analysis.
type AnalysisResult struct {
	ProjectType string         `json:"projectType"`
	Description string         `json:"description"`
	TechStack   []TechStackItem `json:"techStack"`
	HasFrontend bool           `json:"hasFrontend"`
	HasBackend  bool           `json:"hasBackend"`
	HasMobile   bool           `json:"hasMobile"`
	HasDesktop  bool           `json:"hasDesktop"`
	HasPkgJSON  bool           `json:"hasPkgJson"`
	HasGoMod    bool           `json:"hasGoMod"`
}

// AnalyzeProject scans the project directory and returns the analysis result.
func AnalyzeProject(workDir string) *AnalysisResult {
	result := &AnalysisResult{
		TechStack: []TechStackItem{},
	}

	// Check common config files
	checkGoMod(workDir, result)
	checkPkgJSON(workDir, result)
	checkTSConfig(workDir, result)
	checkPython(workDir, result)
	checkJava(workDir, result)
	checkRust(workDir, result)
	checkDotNet(workDir, result)
	checkDocker(workDir, result)
	checkMakefile(workDir, result)
	checkCMake(workDir, result)
	checkSubdirs(workDir, result)

	// Determine project type
	determineProjectType(result)

	return result
}

func checkGoMod(workDir string, r *AnalysisResult) {
	data, err := os.ReadFile(filepath.Join(workDir, "go.mod"))
	if err != nil {
		return
	}
	r.HasGoMod = true
	// Parse module name from first line
	lines := strings.SplitN(string(data), "\n", 2)
	module := strings.TrimPrefix(lines[0], "module ")
	module = strings.TrimSpace(module)
	r.TechStack = append(r.TechStack, TechStackItem{
		Name:     "Go",
		Category: "language",
		Version:  extractGoVersion(string(data)),
		Source:   "go.mod",
	})
	_ = module
}

func extractGoVersion(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	return ""
}

func checkPkgJSON(workDir string, r *AnalysisResult) {
	data, err := os.ReadFile(filepath.Join(workDir, "package.json"))
	if err != nil {
		return
	}
	r.HasPkgJSON = true
	var pkg struct {
		Name         string `json:"name"`
		Dependencies map[string]string `json:"dependencies"`
		DevDeps      map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return
	}

	// Merge all deps
	allDeps := make(map[string]string)
	for k, v := range pkg.Dependencies {
		allDeps[k] = v
	}
	for k, v := range pkg.DevDeps {
		allDeps[k] = v
	}

	r.TechStack = append(r.TechStack, TechStackItem{
		Name:     "Node.js",
		Category: "runtime",
		Source:   "package.json",
	})

	// Detect frontend frameworks
	if _, ok := allDeps["vue"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Vue", Category: "framework", Source: "package.json",
		})
		r.HasFrontend = true
	}
	if _, ok := allDeps["react"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "React", Category: "framework", Source: "package.json",
		})
		r.HasFrontend = true
	}
	if _, ok := allDeps["@angular/core"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Angular", Category: "framework", Source: "package.json",
		})
		r.HasFrontend = true
	}
	if _, ok := allDeps["svelte"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Svelte", Category: "framework", Source: "package.json",
		})
		r.HasFrontend = true
	}
	if _, ok := allDeps["nuxt"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Nuxt.js", Category: "framework", Source: "package.json",
		})
		r.HasFrontend = true
	}
	if _, ok := allDeps["nuxt3"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Nuxt.js", Category: "framework", Source: "package.json",
		})
		r.HasFrontend = true
	}
	if _, ok := allDeps["next"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Next.js", Category: "framework", Source: "package.json",
		})
		r.HasFrontend = true
	}
	if _, ok := allDeps["vue-router"]; ok {
		r.HasFrontend = true
	}
	if _, ok := allDeps["react-router-dom"]; ok {
		r.HasFrontend = true
	}
	if _, ok := allDeps["taro"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Taro", Category: "framework", Source: "package.json",
		})
		r.HasMobile = true
		r.HasFrontend = true
	}
	if _, ok := allDeps["@tarojs/taro"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Taro", Category: "framework", Source: "package.json",
		})
		r.HasMobile = true
		r.HasFrontend = true
	}
	// UI libs
	if _, ok := allDeps["element-plus"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Element Plus", Category: "library", Source: "package.json",
		})
	}
	if _, ok := allDeps["element-ui"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Element UI", Category: "library", Source: "package.json",
		})
	}
	if _, ok := allDeps["antd"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Ant Design", Category: "library", Source: "package.json",
		})
	}
	if _, ok := allDeps["vant"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Vant", Category: "library", Source: "package.json",
		})
		r.HasMobile = true
	}
	// CSS frameworks
	if _, ok := allDeps["tailwindcss"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Tailwind CSS", Category: "library", Source: "package.json",
		})
	}
	if _, ok := allDeps["@tailwindcss/vite"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Tailwind CSS", Category: "library", Source: "package.json",
		})
	}
	if _, ok := allDeps["bootstrap"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Bootstrap", Category: "library", Source: "package.json",
		})
	}
	if _, ok := allDeps["bootstrap-vue-3"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Bootstrap", Category: "library", Source: "package.json",
		})
	}
	// Build tools
	if _, ok := allDeps["vite"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Vite", Category: "tool", Source: "package.json",
		})
	} else if _, ok := allDeps["webpack"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Webpack", Category: "tool", Source: "package.json",
		})
	}
	// TypeScript
	if _, ok := allDeps["typescript"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "TypeScript", Category: "language", Source: "package.json",
		})
	}
	// Electron/desktop
	if _, ok := allDeps["electron"]; ok {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Electron", Category: "framework", Source: "package.json",
		})
		r.HasDesktop = true
	}
}

func checkTSConfig(workDir string, r *AnalysisResult) {
	if _, err := os.Stat(filepath.Join(workDir, "tsconfig.json")); err == nil {
		// Already detected via package.json deps, but ensure flag
		hasTS := false
		for _, t := range r.TechStack {
			if t.Name == "TypeScript" {
				hasTS = true
				break
			}
		}
		if !hasTS {
			r.TechStack = append(r.TechStack, TechStackItem{
				Name: "TypeScript", Category: "language", Source: "tsconfig.json",
			})
		}
	}
}

func checkPython(workDir string, r *AnalysisResult) {
	// Check multiple Python config files
	files := []string{"requirements.txt", "pyproject.toml", "setup.py", "Pipfile", "poetry.lock"}
	found := ""
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(workDir, f)); err == nil {
			found = f
			break
		}
	}
	if found != "" {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Python", Category: "language", Source: found,
		})
		// Check for web frameworks
		if found == "requirements.txt" {
			data, _ := os.ReadFile(filepath.Join(workDir, found))
			content := strings.ToLower(string(data))
			if strings.Contains(content, "django") || strings.Contains(content, "djangorestframework") {
				r.TechStack = append(r.TechStack, TechStackItem{
					Name: "Django", Category: "framework", Source: found,
				})
			}
			if strings.Contains(content, "flask") {
				r.TechStack = append(r.TechStack, TechStackItem{
					Name: "Flask", Category: "framework", Source: found,
				})
			}
			if strings.Contains(content, "fastapi") {
				r.TechStack = append(r.TechStack, TechStackItem{
					Name: "FastAPI", Category: "framework", Source: found,
				})
			}
		}
		r.HasBackend = true
	}
}

func checkJava(workDir string, r *AnalysisResult) {
	if _, err := os.Stat(filepath.Join(workDir, "pom.xml")); err == nil {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Java", Category: "language", Source: "pom.xml",
		})
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Maven", Category: "tool", Source: "pom.xml",
		})
		r.HasBackend = true
		// Check for Spring Boot in pom.xml
		data, _ := os.ReadFile(filepath.Join(workDir, "pom.xml"))
		content := strings.ToLower(string(data))
		if strings.Contains(content, "spring-boot-starter") || strings.Contains(content, "<parent>") && strings.Contains(content, "spring-boot") {
			r.TechStack = append(r.TechStack, TechStackItem{
				Name: "Spring Boot", Category: "framework", Source: "pom.xml",
			})
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, "build.gradle")); err == nil {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Java", Category: "language", Source: "build.gradle",
		})
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Gradle", Category: "tool", Source: "build.gradle",
		})
		r.HasBackend = true
		// Check for Spring Boot in build.gradle
		data, _ := os.ReadFile(filepath.Join(workDir, "build.gradle"))
		content := strings.ToLower(string(data))
		if strings.Contains(content, "spring-boot") || strings.Contains(content, "org.springframework.boot") {
			r.TechStack = append(r.TechStack, TechStackItem{
				Name: "Spring Boot", Category: "framework", Source: "build.gradle",
			})
		}
	}
}

func checkRust(workDir string, r *AnalysisResult) {
	if _, err := os.Stat(filepath.Join(workDir, "Cargo.toml")); err == nil {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Rust", Category: "language", Source: "Cargo.toml",
		})
		r.HasBackend = true
	}
}

func checkDotNet(workDir string, r *AnalysisResult) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".csproj") {
			r.TechStack = append(r.TechStack, TechStackItem{
				Name: ".NET", Category: "language", Source: e.Name(),
			})
			r.HasBackend = true
			return
		}
	}
}

func checkDocker(workDir string, r *AnalysisResult) {
	if _, err := os.Stat(filepath.Join(workDir, "Dockerfile")); err == nil {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Docker", Category: "tool", Source: "Dockerfile",
		})
	}
	if _, err := os.Stat(filepath.Join(workDir, "docker-compose.yml")); err == nil {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Docker Compose", Category: "tool", Source: "docker-compose.yml",
		})
	} else if _, err := os.Stat(filepath.Join(workDir, "docker-compose.yaml")); err == nil {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Docker Compose", Category: "tool", Source: "docker-compose.yaml",
		})
	}
}

func checkMakefile(workDir string, r *AnalysisResult) {
	if _, err := os.Stat(filepath.Join(workDir, "Makefile")); err == nil {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "Make", Category: "tool", Source: "Makefile",
		})
	}
}

func checkCMake(workDir string, r *AnalysisResult) {
	if _, err := os.Stat(filepath.Join(workDir, "CMakeLists.txt")); err == nil {
		r.TechStack = append(r.TechStack, TechStackItem{
			Name: "CMake", Category: "tool", Source: "CMakeLists.txt",
		})
	}
}

func checkSubdirs(workDir string, r *AnalysisResult) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := strings.ToLower(e.Name())
		if name == "client" || name == "frontend" || name == "web" || name == "ui" {
			r.HasFrontend = true
		}
		if name == "server" || name == "backend" || name == "api" {
			r.HasBackend = true
		}
	}

	// Check for vue.config.js / nuxt.config / next.config
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lower := strings.ToLower(e.Name())
		if strings.Contains(lower, "nuxt.config") {
			r.HasFrontend = true
		}
		if strings.Contains(lower, "next.config") {
			r.HasFrontend = true
		}
	}

	// If there's an `app` or `src` dir with vue/tsx files, count as frontend
	for _, e := range entries {
		if e.IsDir() && (e.Name() == "src" || e.Name() == "app") {
			checkDirForVueFiles(filepath.Join(workDir, e.Name()), r)
		}
	}
}

func checkDirForVueFiles(dir string, r *AnalysisResult) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			checkDirForVueFiles(filepath.Join(dir, e.Name()), r)
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".vue" || ext == ".tsx" || ext == ".jsx" {
			r.HasFrontend = true
			return
		}
	}
}

func determineProjectType(r *AnalysisResult) {
	// Collect tech names for matching
	names := make(map[string]bool)
	for _, t := range r.TechStack {
		names[strings.ToLower(t.Name)] = true
	}

	hasElectron := names["electron"]
	hasTaro := names["taro"]

	// Summarize tech stack for description
	langs := []string{}
	frameworks := []string{}
	tools := []string{}
	for _, t := range r.TechStack {
		switch t.Category {
		case "language":
			langs = append(langs, t.Name)
		case "framework":
			frameworks = append(frameworks, t.Name)
		case "tool":
			tools = append(tools, t.Name)
		}
	}

	desc := ""
	if len(langs) > 0 {
		desc = strings.Join(langs, " + ")
	}
	if len(frameworks) > 0 {
		if desc != "" {
			desc += " "
		}
		desc += "(" + strings.Join(frameworks, ", ") + ")"
	}
	if len(tools) > 0 {
		if desc != "" {
			desc += " | "
		}
		desc += strings.Join(tools, ", ")
	}

	// Determine project type
	switch {
	case hasElectron:
		r.ProjectType = "desktop_app"
		if desc == "" {
			desc = "Desktop Application"
		}
	case r.HasFrontend && r.HasBackend && r.HasMobile:
		r.ProjectType = "multi_platform"
		if desc == "" {
			desc = "Multi-platform Application"
		}
	case r.HasFrontend && r.HasBackend:
		r.ProjectType = "fullstack_monolith"
		if desc == "" {
			desc = "Full-stack Application"
		}
	case hasTaro:
		r.ProjectType = "mini_program"
		if desc == "" {
			desc = "Mini Program"
		}
	case r.HasFrontend:
		r.ProjectType = "frontend_only"
		if desc == "" {
			desc = "Frontend Application"
		}
	case r.HasBackend:
		r.ProjectType = "backend_only"
		if desc == "" {
			desc = "Backend Service"
		}
	default:
		r.ProjectType = "unknown"
		if desc == "" {
			desc = "Undetermined Project"
		}
	}

	r.Description = desc

	// Sort tech stack by category
	sort.Slice(r.TechStack, func(i, j int) bool {
		order := map[string]int{"language": 0, "runtime": 1, "framework": 2, "library": 3, "tool": 4, "database": 5}
		oi := order[r.TechStack[i].Category]
		oj := order[r.TechStack[j].Category]
		if oi != oj {
			return oi < oj
		}
		return r.TechStack[i].Name < r.TechStack[j].Name
	})
}

// ProjectTypes returns the list of known project types for UI dropdown.
func ProjectTypes() []struct {
	Value string `json:"value"`
	Label string `json:"label"`
} {
	return []struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}{
		{Value: "fullstack_monolith", Label: "全栈一体应用（前后端打包到同一可执行文件）"},
		{Value: "fullstack_separate", Label: "全栈分离应用（前后端分目录/分仓库）"},
		{Value: "frontend_only", Label: "纯前端应用"},
		{Value: "backend_only", Label: "纯后端服务"},
		{Value: "multi_platform", Label: "多端应用（Web+移动端+后端）"},
		{Value: "mini_program", Label: "小程序/小游戏"},
		{Value: "desktop_app", Label: "桌面应用"},
		{Value: "library", Label: "库/包/SDK"},
		{Value: "other", Label: "其他"},
	}
}

// TechOptions returns common tech options grouped by category.
func TechOptions() map[string][]string {
	return map[string][]string{
		"前端框架": {"Vue 3", "React", "Angular", "Svelte", "Nuxt.js", "Next.js", "Taro", "uniapp", "webcomponent", "html+js+css"},
		"前端组件库": {"Element Plus", "Element UI", "Ant Design", "Vant", "Bootstrap", "Tailwind CSS"},
		"后端语言": {"Go", "Node.js", "Python", "Java", "C#", "Rust", "PHP", "Ruby", "Kotlin"},
		"后端框架": {"Chi Router", "Gin", "Echo", "Fiber", "Express", "NestJS", "FastAPI", "Flask", "Django", "Spring Boot", "ASP.NET"},
		"数据库":  {"PostgreSQL", "MySQL", "SQLite", "MongoDB", "Redis", "Elasticsearch"},
		"构建工具": {"Vite", "Webpack", "esbuild", "Go build", "Maven", "Gradle"},
		"容器化":  {"Docker", "Docker Compose", "Kubernetes"},
		"架构":    {"桌面(Desktop)", "单体(Monolith)", "分布式(Distributed)", "微服务(Microservices)"},
	}
}

// MetaPrompt generates the meta-prompt for LLM prompt generation.
func MetaPrompt(analysis *AnalysisResult) string {
	var techLines []string
	for _, t := range analysis.TechStack {
		line := fmt.Sprintf("- %s（%s）", t.Name, t.Category)
		if t.Version != "" {
			line += fmt.Sprintf(" v%s", t.Version)
		}
		line += fmt.Sprintf(" [%s]", t.Source)
		techLines = append(techLines, line)
	}
	techStr := strings.Join(techLines, "\n")

	return fmt.Sprintf(`你是一个项目规范分析师。根据以下项目分析信息，生成 7 个角色的系统提示词（System Prompt）。

每个提示词需包含对应角色在"设计→编码→测试→部署→安全"全链条中的职责和规范。
其中"编译部署打包"角色还应该提示 LLM 可以将常用操作封装为自定义工具调用，减少重复命令。

## 项目信息

- 项目类型：%s
- 描述：%s
- 请根据以上信息给出你的项目类型结论，在 description 字段中填写中文解释

## 技术栈

%s

## 输出格式

请严格以 JSON 数组格式返回，不要包含其他文字：

[
  {
    "category": "product_design",
    "useCase": "功能设计",
    "description": "一句话说明提示词的用途",
    "prompt": "完整的系统提示词..."
  },
  {
    "category": "architecture",
    "useCase": "架构设计",
    "description": "...",
    "prompt": "..."
  },
  {
    "category": "frontend",
    "useCase": "前端开发",
    "description": "...",
    "prompt": "..."
  },
  {
    "category": "backend",
    "useCase": "后端开发",
    "description": "...",
    "prompt": "..."
  },
  {
    "category": "code_review",
    "useCase": "代码审查",
    "description": "...",
    "prompt": "..."
  },
  {
    "category": "security",
    "useCase": "安全审查",
    "description": "...",
    "prompt": "..."
  },
  {
    "category": "build_deploy",
    "useCase": "编译部署打包",
    "description": "...",
    "prompt": "..."
  }
]

每个 prompt 字段内容应该是完整的、可直接使用的系统提示词，使用中文。`, analysis.ProjectType, analysis.Description, techStr)
}

// GeneratedPrompt holds a generated prompt from the LLM.
type GeneratedPrompt struct {
	Category    string `json:"category"`
	UseCase     string `json:"useCase"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt"`
}

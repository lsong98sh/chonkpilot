package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// ignoreDirs 是探测时跳过的目录（版本库 / 数据区 / 依赖 / 产物）。
var ignoreDirs = map[string]bool{
	".git": true, ".chonkpilot": true, "node_modules": true, "vendor": true,
	"dist": true, "build": true, ".idea": true, ".vscode": true, ".next": true,
	"target": true, "__pycache__": true,
}

// ignoreFiles 是空目录判定时忽略的 OS / 编辑器噪音文件。
var ignoreFiles = map[string]bool{
	".DS_Store": true, "Thumbs.db": true, "desktop.ini": true,
}

// codeExts 是「源码文件」扩展名集合（用于 HasCode 判定）。
var codeExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".vue": true,
	".java": true, ".kt": true, ".py": true, ".rs": true, ".c": true, ".h": true,
	".cpp": true, ".cs": true, ".rb": true, ".php": true, ".swift": true, ".dart": true,
	".m": true, ".mm": true, ".scala": true, ".lua": true,
}

// langByExt 是扩展名 → 语言名（用于语言清单统计）。
var langByExt = map[string]string{
	".go": "Go", ".ts": "TypeScript", ".tsx": "TypeScript", ".js": "JavaScript",
	".jsx": "JavaScript", ".vue": "Vue", ".java": "Java", ".kt": "Kotlin",
	".py": "Python", ".rs": "Rust", ".c": "C", ".h": "C", ".cpp": "C++",
	".cs": "C#", ".rb": "Ruby", ".php": "PHP", ".swift": "Swift", ".dart": "Dart",
	".scala": "Scala", ".lua": "Lua",
}

// probeMaxFiles 是文件遍历上限（防御超大仓库，超出即停止计数）。
const probeMaxFiles = 20000

// Probe 对工作目录做只读探测（不写盘；忽略集见 ignoreDirs / ignoreFiles）。
// 纯函数（不依赖实例绑定），便于单测。
func Probe(workDir string) facade.ProjectProbeResponse {
	res := facade.ProjectProbeResponse{
		WorkDir:    workDir,
		Languages:  []string{},
		Frameworks: []string{},
		TopDirs:    []string{},
	}
	if workDir == "" {
		return res
	}
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return res
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if ignoreDirs[name] {
				if name == ".git" {
					res.HasGit = true
				}
				continue
			}
			res.TopDirs = append(res.TopDirs, name)
			continue
		}
		if ignoreFiles[name] {
			continue
		}
		if strings.HasPrefix(strings.ToLower(name), "readme") {
			res.HasReadme = true
		}
	}
	sort.Strings(res.TopDirs)

	langCount := map[string]int{}
	count := 0
	_ = filepath.WalkDir(workDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != workDir && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if count >= probeMaxFiles {
			return filepath.SkipAll
		}
		if ignoreFiles[d.Name()] {
			return nil
		}
		count++
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if lang, ok := langByExt[ext]; ok {
			langCount[lang]++
		}
		if codeExts[ext] {
			res.HasCode = true
		}
		return nil
	})
	res.FileCount = count
	res.Empty = count == 0

	for l := range langCount {
		res.Languages = append(res.Languages, l)
	}
	sort.Slice(res.Languages, func(i, j int) bool {
		if langCount[res.Languages[i]] != langCount[res.Languages[j]] {
			return langCount[res.Languages[i]] > langCount[res.Languages[j]]
		}
		return res.Languages[i] < res.Languages[j]
	})

	detectToolchain(workDir, &res)
	return res
}

// statExists 返回第一个存在的候选文件（相对 root），都不存在返回空串。
func statExists(root string, names ...string) string {
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(root, n)); err == nil {
			return n
		}
	}
	return ""
}

// detectToolchain 尽力而为地识别包管理 / 构建 / 测试 / lint / 框架（只读）。
func detectToolchain(root string, res *facade.ProjectProbeResponse) {
	switch {
	case statExists(root, "pnpm-lock.yaml") != "":
		res.PackageManager = "pnpm"
	case statExists(root, "yarn.lock") != "":
		res.PackageManager = "yarn"
	case statExists(root, "package-lock.json", "package.json") != "":
		res.PackageManager = "npm"
	case statExists(root, "go.mod") != "":
		res.PackageManager = "go modules"
	case statExists(root, "poetry.lock", "Pipfile") != "":
		res.PackageManager = "pip"
	case statExists(root, "Cargo.toml") != "":
		res.PackageManager = "cargo"
	case statExists(root, "pom.xml", "build.gradle", "build.gradle.kts") != "":
		res.PackageManager = "maven/gradle"
	}

	switch {
	case statExists(root, "vite.config.ts", "vite.config.js", "vite.config.mjs") != "":
		res.BuildTool = "vite"
	case statExists(root, "webpack.config.js", "webpack.config.ts") != "":
		res.BuildTool = "webpack"
	case statExists(root, "tsconfig.json") != "":
		res.BuildTool = "tsc"
	case statExists(root, "Makefile") != "":
		res.BuildTool = "make"
	}

	switch {
	case statExists(root, "vitest.config.ts", "vitest.config.js") != "":
		res.TestTool = "vitest"
	case statExists(root, "jest.config.js", "jest.config.ts") != "":
		res.TestTool = "jest"
	case statExists(root, "pytest.ini", "pyproject.toml") != "":
		res.TestTool = "pytest"
	case statExists(root, "go.mod") != "":
		res.TestTool = "go test"
	}

	switch {
	case statExists(root, ".golangci.yml", ".golangci.yaml") != "":
		res.LintTool = "golangci-lint"
	case statExists(root, ".eslintrc", ".eslintrc.js", ".eslintrc.json", "eslint.config.js", "eslint.config.mjs") != "":
		res.LintTool = "eslint"
	case statExists(root, ".prettierrc", ".prettierrc.json", "prettier.config.js") != "":
		res.LintTool = "prettier"
	}

	res.Frameworks = detectFrameworks(root)
}

// detectFrameworks 从依赖清单尽力识别框架（package.json / go.mod，只读；有界）。
func detectFrameworks(root string) []string {
	found := map[string]bool{}
	if data, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			known := map[string]string{
				"vue": "Vue", "react": "React", "svelte": "Svelte", "next": "Next.js",
				"nuxt": "Nuxt", "@nestjs/core": "NestJS", "express": "Express",
				"fastify": "Fastify", "vite": "Vite",
			}
			for name := range pkg.Dependencies {
				if v, ok := known[name]; ok {
					found[v] = true
				}
			}
			for name := range pkg.DevDependencies {
				if v, ok := known[name]; ok {
					found[v] = true
				}
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		text := string(data)
		for mod, name := range map[string]string{
			"github.com/gin-gonic/gin": "Gin",
			"github.com/gofiber/fiber": "Fiber",
			"github.com/labstack/echo": "Echo",
		} {
			if strings.Contains(text, mod) {
				found[name] = true
			}
		}
	}
	out := make([]string, 0, len(found))
	for name := range found {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

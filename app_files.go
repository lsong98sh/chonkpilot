//go:build windows
// +build windows

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─── File operations ───────────────────────────────────────

type fileNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsDir    bool        `json:"is_dir"`
	Children []*fileNode `json:"children,omitempty"`
}

// WatchDir starts watching a directory for changes (called when tree node expands).
func (a *App) WatchDir(path string) {
	if a.fw == nil {
		return
	}
	a.fw.WatchDir(path)
}

// UnwatchDir stops watching a directory (called when tree node collapses).
// recursive=true also unwatches all watched subdirectories.
func (a *App) UnwatchDir(path string, recursive bool) {
	if a.fw == nil {
		return
	}
	a.fw.UnwatchDir(path, recursive)
}

// GetFileTree returns the file tree for the given path (or workDir).
func (a *App) GetFileTree(path string) (map[string]interface{}, error) {
	targetPath := path
	if targetPath == "" {
		targetPath = a.workDir
	}
	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(absPath, a.workDir) {
		return nil, fmt.Errorf("path outside work directory")
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, err
	}
	node := &fileNode{
		Name:  filepath.Base(absPath),
		Path:  absPath,
		IsDir: info.IsDir(),
	}
	if info.IsDir() {
		node.Children = readDirChildren(absPath)
	}
	return map[string]interface{}{"tree": node}, nil
}

// GetFileTreeChildren returns one level of directory children.
func (a *App) GetFileTreeChildren(dir string) (map[string]interface{}, error) {
	if dir == "" {
		return nil, fmt.Errorf("directory path required")
	}
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(absPath, a.workDir) {
		return nil, fmt.Errorf("path outside work directory")
	}
	children := readDirChildren(absPath)
	return map[string]interface{}{"children": children}, nil
}

func readDirChildren(dir string) []*fileNode {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var nodes []*fileNode
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") || strings.HasSuffix(name, "~") || strings.HasPrefix(name, "~$") {
			continue
		}
		childPath := filepath.Join(dir, name)
		child := &fileNode{
			Name:  name,
			Path:  childPath,
			IsDir: entry.IsDir(),
		}
		nodes = append(nodes, child)
	}
	// Sort: directories first, then by name
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].IsDir != nodes[j].IsDir {
			return nodes[i].IsDir
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	return nodes
}

// ReadFileContent reads file content. If raw is true, returns binary data with MIME type.
func (a *App) ReadFileContent(path string, raw bool) (interface{}, error) {
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(absPath, a.workDir) {
		return nil, fmt.Errorf("path outside work directory")
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	if raw {
		return map[string]interface{}{"data": data, "contentType": detectContentType(absPath)}, nil
	}
	return map[string]string{"content": string(data)}, nil
}

func detectContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	mimeMap := map[string]string{
		".pdf": "application/pdf", ".png": "image/png", ".jpg": "image/jpeg",
		".jpeg": "image/jpeg", ".gif": "image/gif", ".svg": "image/svg+xml",
		".webp": "image/webp", ".ico": "image/x-icon", ".bmp": "image/bmp",
		".mp4": "video/mp4", ".webm": "video/webm", ".mkv": "video/x-matroska",
		".avi": "video/x-msvideo", ".mov": "video/quicktime",
		".mp3": "audio/mpeg", ".wav": "audio/wav", ".wma": "audio/x-ms-wma",
		".ogg": "audio/ogg",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	}
	if mime, ok := mimeMap[ext]; ok {
		return mime
	}
	return "application/octet-stream"
}

// ─── File tree context menu operations ─────────────────────

// sanitizePath checks that the given path is within the work directory.
func (a *App) sanitizePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absPath, a.workDir) {
		return "", fmt.Errorf("path outside work directory")
	}
	return absPath, nil
}

// CreateFileInDir creates an empty file in the specified directory.
func (a *App) CreateFileInDir(dirPath, fileName string) (map[string]interface{}, error) {
	absDir, err := a.sanitizePath(dirPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("directory not found: %s", dirPath)
	}
	filePath := filepath.Join(absDir, fileName)
	if _, err := os.Stat(filePath); err == nil {
		return nil, fmt.Errorf("file already exists: %s", fileName)
	}
	if err := os.WriteFile(filePath, []byte{}, 0644); err != nil {
		return nil, err
	}
	return map[string]interface{}{"path": filePath}, nil
}

// CreateDirInDir creates a subdirectory in the specified directory.
func (a *App) CreateDirInDir(dirPath, dirName string) (map[string]interface{}, error) {
	absDir, err := a.sanitizePath(dirPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("directory not found: %s", dirPath)
	}
	dirPathNew := filepath.Join(absDir, dirName)
	if err := os.Mkdir(dirPathNew, 0755); err != nil {
		return nil, err
	}
	return map[string]interface{}{"path": dirPathNew}, nil
}

// RenameFile renames a file or directory.
func (a *App) RenameFile(oldPath, newName string) (map[string]interface{}, error) {
	absOld, err := a.sanitizePath(oldPath)
	if err != nil {
		return nil, err
	}
	newPath := filepath.Join(filepath.Dir(absOld), newName)
	if _, err := os.Stat(newPath); err == nil {
		return nil, fmt.Errorf("target already exists: %s", newName)
	}
	if err := os.Rename(absOld, newPath); err != nil {
		return nil, err
	}
	return map[string]interface{}{"path": newPath}, nil
}

// DeleteFilePath deletes a file or empty directory.
func (a *App) DeleteFilePath(path string) (map[string]interface{}, error) {
	absPath, err := a.sanitizePath(path)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(absPath); err != nil {
		return nil, err
	}
	return map[string]interface{}{"deleted": path}, nil
}

// DuplicateFile creates a copy of a file in the same directory with " - Copy" suffix.
func (a *App) DuplicateFile(path string) (map[string]interface{}, error) {
	absPath, err := a.sanitizePath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	ext := filepath.Ext(absPath)
	base := strings.TrimSuffix(filepath.Base(absPath), ext)
	newName := base + " - Copy" + ext
	newPath := filepath.Join(filepath.Dir(absPath), newName)
	for i := 2; ; i++ {
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			break
		}
		newName = fmt.Sprintf("%s - Copy (%d)%s", base, i, ext)
		newPath = filepath.Join(filepath.Dir(absPath), newName)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(newPath, data, 0644); err != nil {
		return nil, err
	}
	return map[string]interface{}{"path": newPath}, nil
}

// RevealInExplorer opens Windows File Explorer and selects the given file/directory.
func (a *App) RevealInExplorer(path string) (map[string]interface{}, error) {
	absPath, err := a.sanitizePath(path)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("explorer", "/select,", absPath)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return map[string]interface{}{"code": "OK"}, nil
}

// OpenWithDefault opens a file with the default OS application handler.
func (a *App) OpenWithDefault(path string) (map[string]interface{}, error) {
	absPath, err := a.sanitizePath(path)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("cmd", "/c", "start", "", absPath)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return map[string]interface{}{"code": "OK"}, nil
}

// OpenWithDialog shows the Windows "Open With" dialog for a file.
func (a *App) OpenWithDialog(path string) (map[string]interface{}, error) {
	absPath, err := a.sanitizePath(path)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("rundll32.exe", "shell32.dll,OpenAs_RunDLL", absPath)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return map[string]interface{}{"code": "OK"}, nil
}

// ─── File tree state persistence (DB-backed) ───────────────

// LoadInitData loads all initial data for the file tree in one call.
// Returns: { treeData, expandedKeys, selectedKey, workDir, filetreeWidth }
func (a *App) LoadInitData() (map[string]interface{}, error) {
	// 1. Read saved state from DB
	var savedTreeJSON, savedExpandedJSON, selectedPath string
	db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		savedTreeJSON, _ = db.GetConfig(sqlDB, "filetree-data")
		savedExpandedJSON, _ = db.GetConfig(sqlDB, "filetree-expanded-key")
		selectedPath, _ = db.GetConfig(sqlDB, "filetree-selected-path")
		return nil
	})

	// No saved state → return first level
	if savedTreeJSON == "" {
		children := readDirChildren(a.workDir)
		treeData := make([]map[string]interface{}, 0)
		for _, c := range children {
			treeData = append(treeData, nodeToMap(c, a.workDir))
		}
		return map[string]interface{}{
			"treeData":      treeData,
			"expandedKeys":  []string{},
			"selectedKey":   "",
			"workDir":       a.workDir,
			"filetreeWidth": 280,
		}, nil
	}

	// 2. Parse saved expanded keys
	var savedExpanded []string
	json.Unmarshal([]byte(savedExpandedJSON), &savedExpanded)
	if savedExpanded == nil {
		savedExpanded = []string{}
	}

	// Convert saved relative expanded keys to absolute
	expandedSet := make(map[string]bool)
	for _, rel := range savedExpanded {
		absPath := filepath.Join(a.workDir, rel)
		expandedSet[absPath] = true
	}

	// 3. Parse saved tree and diff against filesystem
	var savedTree []map[string]interface{}
	json.Unmarshal([]byte(savedTreeJSON), &savedTree)

	treeData := diffTreeWithFS(savedTree, a.workDir, expandedSet, a.workDir)

	// 4. Filter expandedKeys: only keep ones that still exist in tree
	validExpanded := make([]string, 0)
	validExpandedSet := collectAllDirPaths(treeData, a.workDir)
	for absPath := range expandedSet {
		if validExpandedSet[absPath] {
			validExpanded = append(validExpanded, absPath)
		}
	}

	// 5. Read window state
	filetreeWidth := 280
	db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		filetreeWidth = db.GetConfigInt(sqlDB, "filetree-window-width", 280)
		return nil
	})

	// Also restore window position
	var windowState struct {
		Width     int  `json:"width"`
		Height    int  `json:"height"`
		X         int  `json:"x"`
		Y         int  `json:"y"`
		Maximized bool `json:"maximized"`
	}
	db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		ws, _ := db.GetConfig(sqlDB, "window")
		if ws != "" {
			json.Unmarshal([]byte(ws), &windowState)
		}
		return nil
	})
	if windowState.Width > 0 && a.ctx != nil {
		runtime.WindowSetSize(a.ctx, windowState.Width, windowState.Height)
		runtime.WindowSetPosition(a.ctx, windowState.X, windowState.Y)
		if windowState.Maximized {
			runtime.WindowMaximise(a.ctx)
		}
	}

	// 6. Start watchers
	a.fw.WatchDir(a.workDir)
	for _, p := range validExpanded {
		a.fw.WatchDir(p)
	}

	return map[string]interface{}{
		"treeData":      treeData,
		"expandedKeys":  validExpanded,
		"selectedKey":   selectedPath,
		"workDir":       a.workDir,
		"filetreeWidth": filetreeWidth,
	}, nil
}

// SaveFileTreeState persists file tree state to ide.db config table.
// state contains: { tree, expanded_dirs, selected_path }
func (a *App) SaveFileTreeState(state map[string]interface{}) error {
	return db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		// Save tree data (with relative paths)
		if tree, ok := state["snapshot"]; ok {
			relTree := convertPathsToRelative(tree, a.workDir)
			data, _ := json.Marshal(relTree)
			if data != nil {
				db.SetConfig(sqlDB, "filetree-data", string(data))
			}
		}
		// Save expanded dirs (relative paths)
		if dirs, ok := state["expanded_dirs"].([]interface{}); ok {
			var relDirs []string
			for _, d := range dirs {
				if ds, ok := d.(string); ok {
					rel, _ := filepath.Rel(a.workDir, ds)
					relDirs = append(relDirs, rel)
				}
			}
			data, _ := json.Marshal(relDirs)
			if data != nil {
				db.SetConfig(sqlDB, "filetree-expanded-key", string(data))
			}
		}
		// Save selected path
		if sel, ok := state["selected_path"].(string); ok && sel != "" {
			rel, _ := filepath.Rel(a.workDir, sel)
			db.SetConfig(sqlDB, "filetree-selected-path", rel)
		}
		return nil
	})
}

// SaveWindowState persists window position/size to ide.db config table.
func (a *App) SaveWindowState(state map[string]interface{}) error {
	data, _ := json.Marshal(state)
	return db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.SetConfig(sqlDB, "window", string(data))
	})
}

// SaveFileTreeWidth persists the file tree panel width.
func (a *App) SaveFileTreeWidth(width int) error {
	return db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.SetConfig(sqlDB, "filetree-window-width", fmt.Sprintf("%d", width))
	})
}

// ─── Helper: convert fileNode to map for frontend ──────────

func nodeToMap(n *fileNode, workDir string) map[string]interface{} {
	m := map[string]interface{}{
		"label":  n.Name,
		"path":   n.Path,
		"is_dir": n.IsDir,
	}
	if n.IsDir {
		m["children"] = []interface{}{}
		m["_loaded"] = false
	}
	return m
}

// Helper: diff saved tree against filesystem
func diffTreeWithFS(saved []map[string]interface{}, workDir string, expandedSet map[string]bool, baseDir string) []map[string]interface{} {
	result := make([]map[string]interface{}, 0)
	// Read actual disk entries
	diskEntries, _ := os.ReadDir(baseDir)
	diskMap := make(map[string]bool)
	for _, e := range diskEntries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") || strings.HasSuffix(name, "~") || strings.HasPrefix(name, "~$") {
			continue
		}
		diskMap[name] = e.IsDir()
	}

	// Build a map from saved nodes for quick lookup
	savedMap := make(map[string]map[string]interface{})
	for _, node := range saved {
		if name, ok := node["name"].(string); ok {
			savedMap[name] = node
		}
	}

	// Process existing + new
	allNames := make(map[string]bool)
	for name := range diskMap {
		allNames[name] = true
	}
	for name := range savedMap {
		allNames[name] = true
	}

	for name := range allNames {
		absPath := filepath.Join(baseDir, name)
		isDir, onDisk := diskMap[name]
		savedNode, wasSaved := savedMap[name]

		if !onDisk {
			// Deleted - skip entirely
			continue
		}

		node := map[string]interface{}{
			"label":  name,
			"path":   absPath,
			"is_dir": isDir,
		}

		if isDir {
			inExpanded := expandedSet[absPath]

			if wasSaved {
				if children, ok := savedNode["children"].([]interface{}); ok && len(children) > 0 {
					// Has saved children data
					if inExpanded {
						// Was expanded → diff children recursively
						var savedChildren []map[string]interface{}
						for _, c := range children {
							if cm, ok := c.(map[string]interface{}); ok {
								savedChildren = append(savedChildren, cm)
							}
						}
						childResult := diffTreeWithFS(savedChildren, workDir, expandedSet, absPath)
						var childList []interface{}
						for _, cr := range childResult {
							childList = append(childList, cr)
						}
						node["children"] = childList
						node["_loaded"] = true
					} else {
						// Not expanded - check if children are virtual or real
						firstChild := children[0]
						if fc, ok := firstChild.(map[string]interface{}); ok {
							if isVirtual, _ := fc["virtual"].(bool); isVirtual {
								// Virtual placeholder - keep
								node["children"] = children
								node["_loaded"] = false
							} else {
								// Real data - recurse to check for expanded grandchildren
								var savedChildren []map[string]interface{}
								for _, c := range children {
									if cm, ok := c.(map[string]interface{}); ok {
										savedChildren = append(savedChildren, cm)
									}
								}
								childResult := diffTreeWithFS(savedChildren, workDir, expandedSet, absPath)
								var childList []interface{}
								for _, cr := range childResult {
									childList = append(childList, cr)
								}
								node["children"] = childList
								node["_loaded"] = false
							}
						} else {
							node["children"] = children
							node["_loaded"] = false
						}
					}
				} else {
					// No saved children
					if inExpanded {
						// Expanded but no children data - read from disk
						diskChildren := readDirChildren(absPath)
						var childList []interface{}
						for _, dc := range diskChildren {
							childList = append(childList, nodeToMap(dc, workDir))
						}
						node["children"] = childList
						node["_loaded"] = true
					} else {
						node["children"] = []interface{}{
							map[string]interface{}{
								"label":   ".",
								"virtual": true,
								"isLeaf":  true,
							},
						}
						node["_loaded"] = false
					}
				}
			} else {
				// New directory not in saved state
				if inExpanded {
					diskChildren := readDirChildren(absPath)
					var childList []interface{}
					for _, dc := range diskChildren {
						childList = append(childList, nodeToMap(dc, workDir))
					}
					node["children"] = childList
					node["_loaded"] = true
				} else {
					node["children"] = []interface{}{}
					node["_loaded"] = false
				}
			}
		}

		result = append(result, node)
	}

	// Sort: directories first, then by name
	sort.Slice(result, func(i, j int) bool {
		di, _ := result[i]["is_dir"].(bool)
		dj, _ := result[j]["is_dir"].(bool)
		if di != dj {
			return di
		}
		ni, _ := result[i]["label"].(string)
		nj, _ := result[j]["label"].(string)
		return ni < nj
	})

	return result
}

// Helper: collect all directory paths from tree
func collectAllDirPaths(nodes []map[string]interface{}, workDir string) map[string]bool {
	result := make(map[string]bool)
	for _, n := range nodes {
		if isDir, ok := n["is_dir"].(bool); ok && isDir {
			if path, ok := n["path"].(string); ok {
				result[path] = true
				if children, ok := n["children"].([]interface{}); ok {
					var childMaps []map[string]interface{}
					for _, c := range children {
						if cm, ok := c.(map[string]interface{}); ok {
							childMaps = append(childMaps, cm)
						}
					}
					for k, v := range collectAllDirPaths(childMaps, workDir) {
						result[k] = v
					}
				}
			}
		}
	}
	return result
}

// Helper: convert absolute paths in tree data to relative paths for storage
func convertPathsToRelative(tree interface{}, workDir string) interface{} {
	switch t := tree.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, v := range t {
			if k == "path" {
				if s, ok := v.(string); ok {
					rel, err := filepath.Rel(workDir, s)
					if err == nil {
						result[k] = rel
					} else {
						result[k] = v
					}
				} else {
					result[k] = convertPathsToRelative(v, workDir)
				}
			} else if k == "children" {
				result[k] = convertPathsToRelative(v, workDir)
			} else {
				result[k] = v
			}
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(t))
		for i, v := range t {
			result[i] = convertPathsToRelative(v, workDir)
		}
		return result
	default:
		return tree
	}
}

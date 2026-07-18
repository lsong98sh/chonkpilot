//go:build windows
// +build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
func (a *App) UnwatchDir(path string) {
	if a.fw == nil {
		return
	}
	a.fw.UnwatchDir(path)
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
		if strings.HasPrefix(entry.Name(), ".") && entry.Name() != ".ide" {
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

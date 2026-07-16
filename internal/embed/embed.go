// Package embed embeds the executor.exe binary into the IDE build.
// During build, build.ps1 compiles executor.exe and copies it to this directory
// so that //go:embed includes the real binary. An empty placeholder file exists
// to satisfy compile-time checks during development.
package embed

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed executor.exe
var executorData []byte

// ExtractExecutor extracts the embedded executor.exe to the given directory.
// Returns the full path to the extracted executable.
func ExtractExecutor(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	dest := filepath.Join(dir, "executor.exe")

	// Only write if the embedded data is non-empty (real binary)
	if len(executorData) == 0 {
		return "", fmt.Errorf("embedded executor.exe is empty — run build.ps1 to build executor first")
	}

	if err := os.WriteFile(dest, executorData, 0755); err != nil {
		return "", fmt.Errorf("failed to write executor.exe to %s: %w", dest, err)
	}

	return dest, nil
}

package workspace

import (
	"os"
	"testing"

	"go.uber.org/zap"
)

func TestNewInitializer(t *testing.T) {
	workDir, err := os.MkdirTemp("", "chonkpilot-ws-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)

	logger, _ := zap.NewDevelopment()
	init := NewInitializer(workDir, logger)
	if init == nil {
		t.Fatal("NewInitializer() returned nil")
	}
}

func TestInit(t *testing.T) {
	workDir, err := os.MkdirTemp("", "chonkpilot-ws-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)

	logger, _ := zap.NewDevelopment()
	init := NewInitializer(workDir, logger)

	err = init.Init()
	if err != nil {
		t.Fatalf("Init() failed: %v", err)
	}

	// Verify directories exist
	dirs := []string{".ide", ".ide/logs", ".ide/tmp"}
	for _, d := range dirs {
		info, err := os.Stat(workDir + "/" + d)
		if err != nil {
			t.Errorf("directory %s not created: %v", d, err)
		} else if !info.IsDir() {
			t.Errorf("%s is not a directory", d)
		}
	}

	// Verify database file exists
	dbPath := workDir + "/.ide/ide.db"
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("database file not created: %v", err)
	}
}

func TestInitWithGitIgnore(t *testing.T) {
	workDir, err := os.MkdirTemp("", "chonkpilot-ws-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)

	logger, _ := zap.NewDevelopment()
	init := NewInitializer(workDir, logger)

	err = init.Init()
	if err != nil {
		t.Fatalf("Init() failed: %v", err)
	}
}

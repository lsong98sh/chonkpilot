package config

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestNewConfigManager(t *testing.T) {
	workDir, err := os.MkdirTemp("", "chonkpilot-config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)

	logger, _ := zap.NewDevelopment()
	cm := NewConfigManager(workDir, logger)
	if cm == nil {
		t.Fatal("NewConfigManager() returned nil")
	}
}

func TestConfigManagerGetSet(t *testing.T) {
	workDir, err := os.MkdirTemp("", "chonkpilot-config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)

	// Create .ide dir
	os.MkdirAll(filepath.Join(workDir, ".ide"), 0755)

	logger, _ := zap.NewDevelopment()
	cm := NewConfigManager(workDir, logger)

	// Load (which creates the db)
	err = cm.Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Set a value
	err = cm.Set("test-key", "test-value")
	if err != nil {
		t.Fatalf("Set() failed: %v", err)
	}

	// Get the value
	val, ok := cm.Get("test-key")
	if !ok {
		t.Error("Get() returned ok=false")
	}
	if val != "test-value" {
		t.Errorf("Get() = %q, want %q", val, "test-value")
	}
}

func TestConfigManagerGetMissing(t *testing.T) {
	workDir, err := os.MkdirTemp("", "chonkpilot-config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	os.MkdirAll(filepath.Join(workDir, ".ide"), 0755)

	logger, _ := zap.NewDevelopment()
	cm := NewConfigManager(workDir, logger)
	cm.Load()

	_, ok := cm.Get("non-existent-key")
	if ok {
		t.Error("Get() should return ok=false for missing key")
	}
}

func TestConfigManagerConcurrency(t *testing.T) {
	workDir, err := os.MkdirTemp("", "chonkpilot-config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	os.MkdirAll(filepath.Join(workDir, ".ide"), 0755)

	logger, _ := zap.NewDevelopment()
	cm := NewConfigManager(workDir, logger)
	cm.Load()

	// Set multiple values
	for i := 0; i < 10; i++ {
		key := "key-" + string(rune('0'+i))
		err := cm.Set(key, "value-"+string(rune('0'+i)))
		if err != nil {
			t.Fatalf("Set(%q) failed: %v", key, err)
		}
		val, ok := cm.Get(key)
		if !ok {
			t.Errorf("Get(%q) returned ok=false", key)
		}
		if val != "value-"+string(rune('0'+i)) {
			t.Errorf("Get(%q) = %q", key, val)
		}
	}
}

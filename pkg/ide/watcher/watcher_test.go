package watcher

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewFileWatcher(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	fw := NewFileWatcher("/tmp/test", logger)
	if fw == nil {
		t.Fatal("NewFileWatcher() returned nil")
	}
}

func TestStopWithoutStart(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	fw := NewFileWatcher("/tmp/test", logger)
	// Stopping without starting should not panic
	fw.Stop()
}

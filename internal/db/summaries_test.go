package db

import (
	"os"
	"testing"
)

func TestSummaryFunctions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "summary-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sqlDB, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open DB: %v", err)
	}
	defer Close(sqlDB)

	// Save first summary
	err = SaveSummary(sqlDB, "session-1", `{"project":"test","status":"started"}`, "turn-1")
	if err != nil {
		t.Fatalf("SaveSummary v1 failed: %v", err)
	}

	// Get latest
	summary, err := GetLatestSummary(sqlDB, "session-1")
	if err != nil {
		t.Fatalf("GetLatestSummary failed: %v", err)
	}
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	t.Logf("v1 summary: %s", summary)

	// Save second summary (versioned)
	err = SaveSummary(sqlDB, "session-1", `{"project":"test","status":"in_progress"}`, "turn-2")
	if err != nil {
		t.Fatalf("SaveSummary v2 failed: %v", err)
	}

	// Get latest — should return v2
	summary2, err := GetLatestSummary(sqlDB, "session-1")
	if err != nil {
		t.Fatalf("GetLatestSummary v2 failed: %v", err)
	}
	if summary2 != `{"project":"test","status":"in_progress"}` {
		t.Fatalf("expected v2 summary, got: %s", summary2)
	}
	t.Logf("v2 summary: %s", summary2)

	// Non-existent session
	empty, err := GetLatestSummary(sqlDB, "nonexistent")
	if err != nil {
		t.Fatalf("GetLatestSummary nonexistent failed: %v", err)
	}
	if empty != "" {
		t.Fatalf("expected empty for nonexistent session, got: %s", empty)
	}
}

func TestConfigInt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config-int-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sqlDB, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open DB: %v", err)
	}
	defer Close(sqlDB)

	// Set configs
	if err := SetConfig(sqlDB, "keep_full_turns", "5"); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}
	if err := SetConfig(sqlDB, "keep_simplified_turns", "15"); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}
	if err := SetConfig(sqlDB, "min_compress_token", "3000"); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}

	// Read back
	if v := GetConfigInt(sqlDB, "keep_full_turns", 3); v != 5 {
		t.Fatalf("keep_full_turns = %d, expected 5", v)
	}
	if v := GetConfigInt(sqlDB, "keep_simplified_turns", 10); v != 15 {
		t.Fatalf("keep_simplified_turns = %d, expected 15", v)
	}
	if v := GetConfigInt(sqlDB, "min_compress_token", 1000); v != 3000 {
		t.Fatalf("min_compress_token = %d, expected 3000", v)
	}

	// Non-existent key — should return default
	if v := GetConfigInt(sqlDB, "nonexistent", 42); v != 42 {
		t.Fatalf("nonexistent = %d, expected 42", v)
	}
}

package output

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNewWriter_Empty(t *testing.T) {
	w := NewWriter("", "stdout", "")
	if w == nil {
		t.Fatal("NewWriter() returned nil")
	}
	w.Close()
}

func TestNewWriter_JSONOutput(t *testing.T) {
	w := NewWriter("", "json", "")
	if w == nil {
		t.Fatal("NewWriter() returned nil")
	}
	defer w.Close()
}

func TestWriteEvent_JSONOutput(t *testing.T) {
	// Capture stdout to verify JSON output
	r, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	oldStdout := os.Stdout
	os.Stdout = wPipe

	writer := NewWriter("", "json", "")
	if writer == nil {
		t.Fatal("NewWriter() returned nil")
	}

	// Write an event
	writer.WriteEvent("test_event", map[string]interface{}{
		"message": "hello",
		"count":   42,
	})
	writer.Close()

	wPipe.Close()
	os.Stdout = oldStdout

	// Read captured output
	var buf strings.Builder
	tmp := make([]byte, 4096)
	n, _ := r.Read(tmp)
	buf.Write(tmp[:n])

	// Verify it's valid JSON with correct structure
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(buf.String()), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	if result["type"] != "test_event" {
		t.Errorf("type = %v, want 'test_event'", result["type"])
	}

	payload, ok := result["payload"].(map[string]interface{})
	if !ok {
		t.Fatal("payload should be a map")
	}
	if payload["message"] != "hello" {
		t.Errorf("message = %v", payload["message"])
	}
	if payload["count"] != float64(42) {
		t.Errorf("count = %v", payload["count"])
	}

	// Verify event_id exists
	_, ok = result["event_id"]
	if !ok {
		t.Error("event_id should be present")
	}
}

func TestWriteEvent_StdoutFormat(t *testing.T) {
	r, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	oldStdout := os.Stdout
	os.Stdout = wPipe

	writer := NewWriter("", "stdout", "")
	writer.WriteEvent("test_event", map[string]interface{}{
		"message": "hello",
	})
	writer.Close()

	wPipe.Close()
	os.Stdout = oldStdout

	var buf strings.Builder
	tmp := make([]byte, 4096)
	n, _ := r.Read(tmp)
	buf.Write(tmp[:n])

	// Stdout format should contain event: prefix
	output := buf.String()
	if !strings.Contains(output, "event:") {
		t.Errorf("stdout format should contain 'event:', got: %s", output)
	}
	if !strings.Contains(output, "test_event") {
		t.Errorf("stdout format should contain event type, got: %s", output)
	}
}

func TestWriteEvent_MultipleEvents(t *testing.T) {
	r, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	oldStdout := os.Stdout
	os.Stdout = wPipe

	writer := NewWriter("", "json", "")
	writer.WriteEvent("event_1", map[string]interface{}{"seq": 1})
	writer.WriteEvent("event_2", map[string]interface{}{"seq": 2})
	writer.Close()

	wPipe.Close()
	os.Stdout = oldStdout

	var buf strings.Builder
	tmp := make([]byte, 8192)
	n, _ := r.Read(tmp)
	buf.Write(tmp[:n])

	lines := strings.TrimSpace(buf.String())
	if !strings.Contains(lines, "event_1") {
		t.Error("output should contain event_1")
	}
	if !strings.Contains(lines, "event_2") {
		t.Error("output should contain event_2")
	}
}

func TestWriteEvent_NilPayload(t *testing.T) {
	r, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	oldStdout := os.Stdout
	os.Stdout = wPipe

	writer := NewWriter("", "json", "")
	// This should not panic
	writer.WriteEvent("nil_payload", nil)
	writer.Close()

	wPipe.Close()
	os.Stdout = oldStdout

	var buf strings.Builder
	tmp := make([]byte, 4096)
	n, _ := r.Read(tmp)
	buf.Write(tmp[:n])

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(buf.String()), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if result["type"] != "nil_payload" {
		t.Errorf("type = %v", result["type"])
	}
}

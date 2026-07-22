package discover

import (
	"testing"
)

func TestNewDiscoverer(t *testing.T) {
	d := NewDiscoverer()
	if d == nil {
		t.Fatal("NewDiscoverer() returned nil")
	}
}

func TestListBuiltinTools(t *testing.T) {
	d := NewDiscoverer()
	tools := d.ListBuiltinTools()
	if len(tools) == 0 {
		t.Error("ListBuiltinTools() returned empty list")
	}
	// Verify essential tools exist
	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.Name == "" {
			t.Error("tool has empty name")
		}
		if tool.Description == "" {
			t.Errorf("tool %s has empty description", tool.Name)
		}
	}
	essential := []string{"read_file", "write_file", "execute_command", "grep", "list_directory", "run_tasks", "call_llm"}
	for _, name := range essential {
		if !names[name] {
			t.Errorf("essential tool %q not found", name)
		}
	}
}

func TestListAllTools(t *testing.T) {
	d := NewDiscoverer()
	tools := d.ListAllTools()
	if len(tools) == 0 {
		t.Error("ListAllTools() returned empty list")
	}
}

func TestToolParameters(t *testing.T) {
	d := NewDiscoverer()
	tools := d.ListBuiltinTools()
	for _, tool := range tools {
		if tool.Parameters.Type != "object" {
			t.Errorf("tool %s: Parameters.Type should be 'object', got %q", tool.Name, tool.Parameters.Type)
		}
		if len(tool.Parameters.Properties) == 0 && len(tool.Parameters.Required) > 0 {
			t.Errorf("tool %s: has required fields but no properties", tool.Name)
		}
	}
}

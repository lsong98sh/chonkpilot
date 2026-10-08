package models_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/chonkpilot/chonkpilot-gui/models"
)

func TestLLMProvider_JSONRoundTrip(t *testing.T) {
	p := models.LLMProvider{
		Name:            "test",
		APIKey:          "sk-test-key",
		Model:           "gpt-4",
		BaseURL:         "https://api.openai.com/v1",
		Temperature:     0.7,
		MaxOutputToken:  4096,
		MaxContextToken: 128000,
		Thinking:        true,
		ReasoningEffort: "high",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var got models.LLMProvider
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if got.Name != p.Name {
		t.Errorf("Name = %q, want %q", got.Name, p.Name)
	}
	if got.APIKey != p.APIKey {
		t.Errorf("APIKey = %q, want %q", got.APIKey, p.APIKey)
	}
	if got.Model != p.Model {
		t.Errorf("Model = %q, want %q", got.Model, p.Model)
	}
	if got.BaseURL != p.BaseURL {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, p.BaseURL)
	}
	if got.Temperature != p.Temperature {
		t.Errorf("Temperature = %v, want %v", got.Temperature, p.Temperature)
	}
	if got.MaxOutputToken != p.MaxOutputToken {
		t.Errorf("MaxOutputToken = %d, want %d", got.MaxOutputToken, p.MaxOutputToken)
	}
	if got.MaxContextToken != p.MaxContextToken {
		t.Errorf("MaxContextToken = %d, want %d", got.MaxContextToken, p.MaxContextToken)
	}
	if got.Thinking != p.Thinking {
		t.Errorf("Thinking = %v, want %v", got.Thinking, p.Thinking)
	}
	if got.ReasoningEffort != p.ReasoningEffort {
		t.Errorf("ReasoningEffort = %q, want %q", got.ReasoningEffort, p.ReasoningEffort)
	}
	// 写入只用新名：序列化后不得出现旧键 maxTokens（口径 Z1）。
	if bytes.Contains(data, []byte(`"maxTokens"`)) {
		t.Errorf("序列化不应含旧键 maxTokens：%s", data)
	}
	if !bytes.Contains(data, []byte(`"maxOutputToken"`)) || !bytes.Contains(data, []byte(`"maxContextToken"`)) {
		t.Errorf("序列化应含新键 maxOutputToken / maxContextToken：%s", data)
	}
}

func TestLLMProvider_DefaultValues(t *testing.T) {
	p := models.LLMProvider{
		Name:   "default-test",
		APIKey: "sk-xxx",
	}

	if p.Temperature != 0 {
		t.Errorf("Temperature should default to 0, got %v", p.Temperature)
	}
	if p.MaxOutputToken != 0 {
		t.Errorf("MaxOutputToken should default to 0, got %d", p.MaxOutputToken)
	}
	if p.MaxContextToken != 0 {
		t.Errorf("MaxContextToken should default to 0, got %d", p.MaxContextToken)
	}
	if p.Thinking != false {
		t.Errorf("Thinking should default to false, got %v", p.Thinking)
	}
	if p.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort should default to empty, got %q", p.ReasoningEffort)
	}
}

func TestToolConfig(t *testing.T) {
	tc := models.ToolConfig{
		Name:        "my-tool",
		Type:        "shell",
		Command:     "echo hello",
		Description: "A test tool",
		Source:      "user",
	}

	if tc.Name != "my-tool" {
		t.Errorf("Name = %q", tc.Name)
	}
	if tc.Source != "user" {
		t.Errorf("Source = %q", tc.Source)
	}
}

func TestToolConfig_WithParameters(t *testing.T) {
	params := map[string]interface{}{
		"arg1": "value1",
		"arg2": 42,
	}
	tc := models.ToolConfig{
		Name:       "param-tool",
		Type:       "shell",
		Command:    "echo",
		Parameters: params,
	}

	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var got models.ToolConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	gotParams, ok := got.Parameters.(map[string]interface{})
	if !ok {
		t.Fatal("Parameters should be map[string]interface{}")
	}
	if gotParams["arg1"] != "value1" {
		t.Errorf("arg1 = %v", gotParams["arg1"])
	}
	if gotParams["arg2"] != float64(42) {
		t.Errorf("arg2 = %v", gotParams["arg2"])
	}
}

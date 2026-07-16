//go:build windows
// +build windows

package main

import (
	"encoding/json"
	"fmt"

	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
	"github.com/chonkpilot/chonkpilot/pkg/ide/analyzer"
	"go.uber.org/zap"
)

// ─── Scenario / Analysis ───────────────────────────────────

type scenario struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// GetScenarios returns available scenarios.
func (a *App) GetScenarios() (map[string]interface{}, error) {
	scenarios := []scenario{
		{ID: "web-app", Name: "Web Application", Description: "Full-stack web application", Category: "scenario"},
		{ID: "blog", Name: "Blog System", Description: "Blog website with posts and comments", Category: "scenario"},
	}
	return map[string]interface{}{"scenarios": scenarios}, nil
}

type applyScenarioArgs struct {
	ScenarioID string `json:"scenario_id"`
}

// ApplyScenario applies a scenario (TODO).
func (a *App) ApplyScenario(args applyScenarioArgs) error {
	_ = args.ScenarioID
	return nil
}

// ─── AI Optimize Agent ──────────────────────────────────────

// OptimizeAgentPrompt optimizes an agent's system prompt via LLM streaming.
func (a *App) OptimizeAgentPrompt(data map[string]interface{}) error {
	title, _ := data["title"].(string)
	useCase, _ := data["useCase"].(string)
	currentPrompt, _ := data["prompt"].(string)

	provider, model, apiKey, apiURL := a.resolveLLMConfig()
	if provider == "" {
		a.push("optimize:error", map[string]string{"message": "no LLM configuration found. Please configure a default LLM in Settings."})
		return fmt.Errorf("no LLM config")
	}

	optPrompt := fmt.Sprintf(`You are an expert prompt engineer. Optimize the following AI agent prompt to be more effective, clear, and actionable.

Agent Title: %s
Use Case: %s

Current Prompt:
%s

Please provide an improved version of the prompt. Return ONLY the optimized prompt text, no explanations or markdown formatting. The prompt should be concise yet comprehensive, with clear instructions for the AI agent.`, title, useCase, currentPrompt)

	client := llm.NewClient(provider, model, apiKey, apiURL, a.logger)
	messages := []llm.Message{{Role: "user", Content: optPrompt}}

	stream, err := client.Chat(messages, llm.ChatOptions{
		Stream:      true,
		Temperature: 0.3,
		MaxTokens:   4096,
		Thinking:    false,
	})
	if err != nil {
		a.push("optimize:error", map[string]string{"message": err.Error()})
		return err
	}

	go func() {
		var fullText string
		for chunk := range stream {
			if chunk.Error != nil {
				a.push("optimize:error", map[string]string{"message": chunk.Error.Error()})
				return
			}
			if chunk.Done {
				a.push("optimize:done", map[string]interface{}{"prompt": fullText})
				return
			}
			if chunk.Content != "" {
				fullText += chunk.Content
				a.push("optimize:token", map[string]string{"content": chunk.Content})
			}
		}
		a.push("optimize:done", map[string]interface{}{"prompt": fullText})
	}()

	return nil
}

// AnalyzeProject analyzes the project structure.
func (a *App) AnalyzeProject() (map[string]interface{}, error) {
	result := analyzer.AnalyzeProject(a.workDir)
	return map[string]interface{}{
		"analysis": result,
		"types":    analyzer.ProjectTypes(),
		"options":  analyzer.TechOptions(),
	}, nil
}

// GetTechInfo returns available tech options.
func (a *App) GetTechInfo() (map[string]interface{}, error) {
	return map[string]interface{}{
		"types":   analyzer.ProjectTypes(),
		"options": analyzer.TechOptions(),
	}, nil
}

// ─── LLM streaming ─────────────────────────────────────────

// GeneratePrompts generates project prompts via LLM.
func (a *App) GeneratePrompts(data map[string]interface{}) error {
	analysisJSON, err := json.Marshal(data["analysis"])
	if err != nil {
		a.logger.Error("GeneratePrompts: failed to marshal analysis", zap.Error(err))
		return fmt.Errorf("marshal analysis: %w", err)
	}
	var analysis *analyzer.AnalysisResult
	if err := json.Unmarshal(analysisJSON, &analysis); err != nil {
		a.logger.Error("GeneratePrompts: failed to unmarshal analysis", zap.Error(err))
		return fmt.Errorf("unmarshal analysis: %w", err)
	}
	if analysis == nil {
		analysis = &analyzer.AnalysisResult{}
	}

	if pt, ok := data["projectType"].(string); ok && pt != "" {
		analysis.ProjectType = pt
	}
	if desc, ok := data["description"].(string); ok && desc != "" {
		analysis.Description = desc
	}
	if fe, ok := data["frontend"].([]interface{}); ok {
		for _, t := range fe {
			analysis.TechStack = append(analysis.TechStack, analyzer.TechStackItem{Name: fmt.Sprintf("%v", t), Category: "framework"})
			analysis.HasFrontend = true
		}
	}
	if be, ok := data["backend"].([]interface{}); ok {
		for _, t := range be {
			analysis.TechStack = append(analysis.TechStack, analyzer.TechStackItem{Name: fmt.Sprintf("%v", t), Category: "language"})
			analysis.HasBackend = true
		}
	}
	if ext, ok := data["extra"].([]interface{}); ok {
		for _, t := range ext {
			name := fmt.Sprintf("%v", t)
			analysis.TechStack = append(analysis.TechStack, analyzer.TechStackItem{Name: name, Category: "tool"})
		}
	}
	if arch, ok := data["architecture"].([]interface{}); ok {
		for _, t := range arch {
			analysis.TechStack = append(analysis.TechStack, analyzer.TechStackItem{Name: fmt.Sprintf("%v", t), Category: "architecture"})
		}
	}

	provider, model, apiKey, apiURL := a.resolveLLMConfig()
	if provider == "" {
		a.push("generate:error", map[string]string{"message": "no LLM configuration found. Please configure a default LLM in Settings."})
		return fmt.Errorf("no LLM config")
	}

	prompt := analyzer.MetaPrompt(analysis)
	client := llm.NewClient(provider, model, apiKey, apiURL, a.logger)
	messages := []llm.Message{{Role: "user", Content: prompt}}

	stream, err := client.Chat(messages, llm.ChatOptions{
		Stream:      true,
		Temperature: 0.3,
		MaxTokens:   8192,
		Thinking:    false,
	})
	if err != nil {
		a.push("generate:error", map[string]string{"message": err.Error()})
		return err
	}

	go func() {
		var fullText string
		for chunk := range stream {
			if chunk.Error != nil {
				a.push("generate:error", map[string]string{"message": chunk.Error.Error()})
				return
			}
			if chunk.Done {
				break
			}
			if chunk.Content != "" {
				fullText += chunk.Content
				a.push("generate:token", map[string]string{"content": chunk.Content})
			}
		}
		var prompts []interface{}
		json.Unmarshal([]byte(fullText), &prompts)
		a.push("generate:done", map[string]interface{}{"prompts": prompts})
	}()

	return nil
}

func (a *App) resolveLLMConfig() (provider, model, apiKey, apiURL string) {
	if a.userCfg != nil {
		uc := a.userCfg.Get()
		if uc.DefaultLLM < len(uc.LLMs) {
			p := uc.LLMs[uc.DefaultLLM]
			provider = p.Protocol
			model = p.Model
			apiKey = p.APIKey
			apiURL = p.BaseURL
		}
	}
	return
}

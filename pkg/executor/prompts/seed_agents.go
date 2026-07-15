package prompts

import (
	"database/sql"
	"embed"
	"fmt"
	"path"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
)

//go:embed agents/*.txt
var agentFiles embed.FS

const agentKeyPrefix = "agent."

// Agents lists all available agent names (excluding common rules).
func Agents() []string {
	entries, err := agentFiles.ReadDir("agents")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			name := strings.TrimSuffix(e.Name(), ".txt")
			if name == "common" {
				continue
			}
			names = append(names, name)
		}
	}
	return names
}

// ReadAgent reads an agent prompt from embedded files.
// File format: line 1 = Title, line 2 = UseCase, remaining lines = Prompt body.
func ReadAgent(name string) (title, useCase, prompt string, err error) {
	data, err := agentFiles.ReadFile(path.Join("agents", name+".txt"))
	if err != nil {
		return "", "", "", fmt.Errorf("agent %q not found: %w", name, err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 3)
	if len(lines) < 3 {
		return "", "", "", fmt.Errorf("agent %q: file must have at least 3 lines (title, useCase, prompt)", name)
	}
	title = strings.TrimSpace(lines[0])
	useCase = strings.TrimSpace(lines[1])
	prompt = strings.TrimSpace(lines[2])
	return
}

// ReadCommon returns the common rules text that should be prepended to every agent prompt.
func ReadCommon() string {
	data, err := agentFiles.ReadFile(path.Join("agents", "common.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SeedAgents seeds embedded default agents into the project_agents table.
// It always refreshes system-source agents (deletes existing system agents first),
// preserving user/LLM-created agents. This ensures embedded agents are up-to-date
// without overwriting user customizations.
func SeedAgents(sqlDB *sql.DB) error {
	entries, err := agentFiles.ReadDir("agents")
	if err != nil {
		return fmt.Errorf("failed to read embedded agents: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}

	// First, remove all existing system agents so embedded ones are always refreshed
	if err := db.DeleteProjectAgentsBySource(sqlDB, "system"); err != nil {
		return fmt.Errorf("failed to delete existing system agents: %w", err)
	}

	// Seed each embedded agent
	var agents []models.AgentConfig
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".txt")
		if name == "common" {
			continue
		}
		title, useCase, prompt, err := ReadAgent(name)
		if err != nil {
			continue
		}
		agents = append(agents, models.AgentConfig{
			Title:   title,
			UseCase: useCase,
			Prompt:  prompt,
			Source:  "system",
		})
	}

	if len(agents) > 0 {
		if err := db.SaveProjectAgents(sqlDB, agents); err != nil {
			return fmt.Errorf("failed to seed agents: %w", err)
		}
	}

	// Common rules remain in config table (user-editable via add_agent)
	commonPrompt := ReadCommon()
	if commonPrompt != "" {
		if err := db.SetConfig(sqlDB, agentKeyPrefix+"common.system_prompt", commonPrompt); err != nil {
			return fmt.Errorf("failed to seed common rules: %w", err)
		}
		if err := db.SetConfig(sqlDB, agentKeyPrefix+"common.created_by", "system"); err != nil {
			return fmt.Errorf("failed to seed common rules creator: %w", err)
		}
	}

	return nil
}

// AgentKeyPrefix returns the prefix used for agent config keys.
func AgentKeyPrefix() string {
	return agentKeyPrefix
}

//go:embed agents/*.txt
var _ embed.FS // ensure embed is used to prevent import cycle

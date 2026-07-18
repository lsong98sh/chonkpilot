package prompts

import (
	_ "embed"
)

// DefaultSystemPrompt is the default system prompt embedded into the binary.
//
//go:embed system_prompt.txt
var DefaultSystemPrompt string

// DefaultToolUsage is the default tool usage guide embedded into the binary.
//
//go:embed tool_usage.txt
var DefaultToolUsage string

// DefaultSummaryPrompt is the default summarization prompt embedded into the binary.
//
//go:embed summary_prompt.txt
var DefaultSummaryPrompt string

// DefaultScenarioPrompt is the default scenario content embedded into the binary.
// Used to seed scenario.db on first launch.
//
//go:embed system_scenario.txt
var DefaultScenarioPrompt string

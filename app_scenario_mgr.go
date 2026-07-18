//go:build windows
// +build windows

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
)

// ─── Scenario management (scenario.db) ──────────────────────

// GetScenarioList returns all scenarios from ~/.chonkpilot/scenario.db.
func (a *App) GetScenarioList() (map[string]interface{}, error) {
	sdb, err := db.OpenScenarioDB()
	if err != nil {
		return nil, fmt.Errorf("open scenario.db: %w", err)
	}
	defer sdb.Close()

	scenarios, err := db.GetAllScenarios(sdb)
	if err != nil {
		return nil, fmt.Errorf("get scenarios: %w", err)
	}
	return map[string]interface{}{"scenarios": scenarios}, nil
}

type saveScenarioArgs struct {
	Scenario json.RawMessage `json:"scenario"`
}

// SaveScenario creates or updates a scenario in scenario.db.
func (a *App) SaveScenario(args saveScenarioArgs) (map[string]interface{}, error) {
	var s models.ScenarioConfig
	if err := json.Unmarshal(args.Scenario, &s); err != nil {
		return nil, fmt.Errorf("unmarshal scenario: %w", err)
	}

	sdb, err := db.OpenScenarioDB()
	if err != nil {
		return nil, fmt.Errorf("open scenario.db: %w", err)
	}
	defer sdb.Close()

	if err := db.SaveScenario(sdb, &s); err != nil {
		return nil, fmt.Errorf("save scenario: %w", err)
	}
	return map[string]interface{}{"scenario": s}, nil
}

type deleteScenarioArgs struct {
	ID int64 `json:"id"`
}

// DeleteScenario deletes a scenario from scenario.db.
func (a *App) DeleteScenario(args deleteScenarioArgs) error {
	sdb, err := db.OpenScenarioDB()
	if err != nil {
		return fmt.Errorf("open scenario.db: %w", err)
	}
	defer sdb.Close()

	return db.DeleteScenario(sdb, args.ID)
}

// ─── Active scenario (system prompt override) ───────────────

// SetActiveScenario stores the active scenario's system prompt in the project's
// config table (ide.db) so the executor can read it. Call with id=0 to clear.
func (a *App) SetActiveScenario(id int64) error {
	return db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		if id <= 0 {
			return db.SetConfig(sqlDB, "scenario_system_prompt", "")
		}
		sdb, err := db.OpenScenarioDB()
		if err != nil {
			return fmt.Errorf("open scenario.db: %w", err)
		}
		defer sdb.Close()

		scenarios, err := db.GetAllScenarios(sdb)
		if err != nil {
			return err
		}
		for _, s := range scenarios {
			if s.ID == id {
				return db.SetConfig(sqlDB, "scenario_system_prompt", s.SystemPrompt)
			}
		}
		return fmt.Errorf("scenario %d not found", id)
	})
}

// GetActiveScenario returns the active scenario's system prompt, or empty.
func (a *App) GetActiveScenario() (map[string]interface{}, error) {
	var prompt string
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		prompt, err = db.GetConfig(sqlDB, "scenario_system_prompt")
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"prompt": prompt}, nil
}

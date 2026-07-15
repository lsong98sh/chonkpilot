package workspace

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"go.uber.org/zap"
)

// Initializer handles workspace initialization.
type Initializer struct {
	workDir string
	logger  *zap.Logger
}

// NewInitializer creates a new workspace initializer.
func NewInitializer(workDir string, logger *zap.Logger) *Initializer {
	return &Initializer{
		workDir: workDir,
		logger:  logger,
	}
}

// Init initializes the workspace directory structure and database.
func (init *Initializer) Init() error {
	// Create .ide directory
	ideDir := filepath.Join(init.workDir, ".ide")
	if err := os.MkdirAll(ideDir, 0755); err != nil {
		return fmt.Errorf("failed to create .ide directory: %w", err)
	}

	// Create logs directory
	logsDir := filepath.Join(ideDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Create tmp directory
	tmpDir := filepath.Join(ideDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create tmp directory: %w", err)
	}

	// Initialize database
	err := db.WithDB(init.workDir, func(sqlDB *sql.DB) error {
		return db.RunMigrations(sqlDB)
	})
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	// Handle .gitignore
	init.handleGitignore()

	init.logger.Info("Workspace initialized",
		zap.String("work_dir", init.workDir),
	)
	return nil
}

// handleGitignore adds .ide to .gitignore if it's a git repository.
func (init *Initializer) handleGitignore() {
	gitignorePath := filepath.Join(init.workDir, ".gitignore")
	gitDir := filepath.Join(init.workDir, ".git")

	// Check if .git exists
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return
	}

	// Check if .gitignore exists and already contains .ide
	if data, err := os.ReadFile(gitignorePath); err == nil {
		if strings.Contains(string(data), ".ide") {
			return
		}
	}

	// Append .ide to .gitignore
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		init.logger.Warn("Failed to update .gitignore", zap.Error(err))
		return
	}
	defer f.Close()

	if _, err := f.WriteString("\n# ChonkPilot AI IDE\n.ide/\n"); err != nil {
		init.logger.Warn("Failed to write .gitignore", zap.Error(err))
	}
}



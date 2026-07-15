package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// CreateTask inserts a new task record.
func CreateTask(db *sql.DB, t *models.Task) error {
	_, err := db.Exec(
		`INSERT INTO tasks (task_id, parent_task_id, turn_id, session_id, name, status, progress, result, executor_pid, pipe_path, prompt_file, depth, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TaskID, nullString(t.ParentTaskID), t.TurnID, t.SessionID, t.Name, t.Status, t.Progress, nullString(t.Result), nullInt(t.ExecutorPID), nullString(t.PipePath), nullString(t.PromptFile), t.Depth, t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}
	return nil
}

// UpdateTaskStatus updates the status, progress, result, and updated_at of a task.
func UpdateTaskStatus(db *sql.DB, taskID, status string, progress int, result string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(
		`UPDATE tasks SET status = ?, progress = ?, result = ?, updated_at = ? WHERE task_id = ?`,
		status, progress, result, now, taskID,
	)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return nil
}

// GetTaskByID retrieves a task by ID.
func GetTaskByID(db *sql.DB, taskID string) (*models.Task, error) {
	t := &models.Task{}
	var parentID, result, pipePath, promptFile sql.NullString
	var executorPID sql.NullInt64
	err := db.QueryRow(
		`SELECT task_id, parent_task_id, turn_id, session_id, name, status, progress, result, executor_pid, pipe_path, prompt_file, depth, created_at, updated_at FROM tasks WHERE task_id = ?`,
		taskID,
	).Scan(&t.TaskID, &parentID, &t.TurnID, &t.SessionID, &t.Name, &t.Status, &t.Progress, &result, &executorPID, &pipePath, &promptFile, &t.Depth, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	if parentID.Valid {
		t.ParentTaskID = parentID.String
	}
	if result.Valid {
		t.Result = result.String
	}
	if pipePath.Valid {
		t.PipePath = pipePath.String
	}
	if promptFile.Valid {
		t.PromptFile = promptFile.String
	}
	if executorPID.Valid {
		t.ExecutorPID = int(executorPID.Int64)
	}
	return t, nil
}

// GetTasksByParent returns all tasks with the given parent_task_id.
func GetTasksByParent(db *sql.DB, parentTaskID string) ([]*models.Task, error) {
	rows, err := db.Query(
		`SELECT task_id, parent_task_id, turn_id, session_id, name, status, progress, COALESCE(result,''), COALESCE(executor_pid,0), COALESCE(pipe_path,''), COALESCE(prompt_file,''), depth, created_at, updated_at FROM tasks WHERE parent_task_id = ? ORDER BY created_at ASC`,
		parentTaskID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get tasks by parent: %w", err)
	}
	defer rows.Close()

	var tasks []*models.Task
	for rows.Next() {
		t := &models.Task{}
		if err := rows.Scan(&t.TaskID, &t.ParentTaskID, &t.TurnID, &t.SessionID, &t.Name, &t.Status, &t.Progress, &t.Result, &t.ExecutorPID, &t.PipePath, &t.PromptFile, &t.Depth, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if tasks == nil {
		tasks = []*models.Task{}
	}
	return tasks, rows.Err()
}

// CancelTaskCascade cancels a task and all its descendants using BFS.
func CancelTaskCascade(db *sql.DB, taskID string) error {
	// Collect all descendant task IDs using iterative BFS
	queue := []string{taskID}
	var allIDs []string
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		allIDs = append(allIDs, current)
		children, err := GetTasksByParent(db, current)
		if err != nil {
			return fmt.Errorf("failed to get children of task %s: %w", current, err)
		}
		for _, child := range children {
			queue = append(queue, child.TaskID)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, id := range allIDs {
		_, err := tx.Exec(
			`UPDATE tasks SET status = ?, updated_at = ? WHERE task_id = ?`,
			models.TaskStatusCancelled, now, id,
		)
		if err != nil {
			// Log but don't block the cascade
			continue
		}
	}
	return tx.Commit()
}

// GetTasksByTurn returns all tasks for a turn.
func GetTasksByTurn(db *sql.DB, turnID string) ([]*models.Task, error) {
	rows, err := db.Query(
		`SELECT task_id, COALESCE(parent_task_id,''), turn_id, session_id, name, status, progress, COALESCE(result,''), COALESCE(executor_pid,0), COALESCE(pipe_path,''), COALESCE(prompt_file,''), depth, created_at, updated_at FROM tasks WHERE turn_id = ? ORDER BY created_at ASC`,
		turnID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get tasks by turn: %w", err)
	}
	defer rows.Close()

	var tasks []*models.Task
	for rows.Next() {
		t := &models.Task{}
		if err := rows.Scan(&t.TaskID, &t.ParentTaskID, &t.TurnID, &t.SessionID, &t.Name, &t.Status, &t.Progress, &t.Result, &t.ExecutorPID, &t.PipePath, &t.PromptFile, &t.Depth, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if tasks == nil {
		tasks = []*models.Task{}
	}
	return tasks, rows.Err()
}

// GetUnfinishedTasks returns all tasks with status running or paused.
func GetUnfinishedTasks(db *sql.DB) ([]*models.Task, error) {
	rows, err := db.Query(
		`SELECT task_id, COALESCE(parent_task_id,''), turn_id, session_id, name, status, progress, COALESCE(result,''), COALESCE(executor_pid,0), COALESCE(pipe_path,''), COALESCE(prompt_file,''), depth, created_at, updated_at FROM tasks WHERE status IN ('running','paused') ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get unfinished tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*models.Task
	for rows.Next() {
		t := &models.Task{}
		if err := rows.Scan(&t.TaskID, &t.ParentTaskID, &t.TurnID, &t.SessionID, &t.Name, &t.Status, &t.Progress, &t.Result, &t.ExecutorPID, &t.PipePath, &t.PromptFile, &t.Depth, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if tasks == nil {
		tasks = []*models.Task{}
	}
	return tasks, rows.Err()
}

func nullInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

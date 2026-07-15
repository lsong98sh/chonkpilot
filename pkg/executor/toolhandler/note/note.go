package note

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleNoteWrite creates or updates a note.
func HandleNoteWrite(workDir string, args map[string]interface{}) *types.ToolResult {
	title, _ := args["title"].(string)
	content, _ := args["content"].(string)
	if title == "" {
		return &types.ToolResult{Success: false, Error: "title is required", Tool: "note_write"}
	}

	sqlDB, err := db.Open(workDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "note_write"}
	}
	defer db.Close(sqlDB)

	// Try update first; if not found, create new
	if err := db.UpdateNote(sqlDB, title, content); err != nil {
		note := models.NewNote(title, content)
		if err := db.CreateNote(sqlDB, note); err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to save note: %s", err.Error()), Tool: "note_write"}
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("note saved: %s", title),
		Tool:    "note_write",
	}
}

// HandleNoteRead retrieves a note by title.
func HandleNoteRead(workDir string, args map[string]interface{}) *types.ToolResult {
	title, _ := args["title"].(string)
	if title == "" {
		return &types.ToolResult{Success: false, Error: "title is required", Tool: "note_read"}
	}

	sqlDB, err := db.Open(workDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "note_read"}
	}
	defer db.Close(sqlDB)

	note, err := db.GetNote(sqlDB, title)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "note_read"}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("title: %s\ncontent:\n%s", note.Title, note.Content),
		Tool:    "note_read",
	}
}

// HandleNoteList lists all notes.
func HandleNoteList(workDir string, args map[string]interface{}) *types.ToolResult {
	sqlDB, err := db.Open(workDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "note_list"}
	}
	defer db.Close(sqlDB)

	notes, err := db.ListNotes(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to list notes: %s", err.Error()), Tool: "note_list"}
	}

	if len(notes) == 0 {
		return &types.ToolResult{Success: true, Output: "no notes found", Tool: "note_list"}
	}

	var buf strings.Builder
	for _, n := range notes {
		preview := n.Content
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		buf.WriteString(fmt.Sprintf("[%s] %s\n  %s\n", n.UpdatedAt[:10], n.Title, preview))
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.TrimSpace(buf.String()),
		Tool:    "note_list",
	}
}

// HandleNoteDelete deletes a note by title.
func HandleNoteDelete(workDir string, args map[string]interface{}) *types.ToolResult {
	title, _ := args["title"].(string)
	if title == "" {
		return &types.ToolResult{Success: false, Error: "title is required", Tool: "note_delete"}
	}

	sqlDB, err := db.Open(workDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "note_delete"}
	}
	defer db.Close(sqlDB)

	if err := db.DeleteNote(sqlDB, title); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "note_delete"}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("note deleted: %s", title),
		Tool:    "note_delete",
	}
}

// ─── Simplify Functions ───

func init() {
	types.RegisterSimplify("note_write", simplifyNoteWrite)
	types.RegisterSimplify("note_read", simplifyNoteRead)
	types.RegisterSimplify("note_list", types.SimpleAction("note_list"))
	types.RegisterSimplify("note_delete", simplifyNoteDelete)
}

type noteTitleArg struct {
	Title string `json:"title"`
}

func simplifyNoteWrite(argsJSON json.RawMessage, result string) string {
	var a noteTitleArg
	if err := json.Unmarshal(argsJSON, &a); err != nil || a.Title == "" {
		return "note_write"
	}
	return fmt.Sprintf("note_write(%s)", a.Title)
}

func simplifyNoteRead(argsJSON json.RawMessage, result string) string {
	var a noteTitleArg
	if err := json.Unmarshal(argsJSON, &a); err != nil || a.Title == "" {
		return "note_read"
	}
	return fmt.Sprintf("note_read(%s)", a.Title)
}

func simplifyNoteDelete(argsJSON json.RawMessage, result string) string {
	var a noteTitleArg
	if err := json.Unmarshal(argsJSON, &a); err != nil || a.Title == "" {
		return "note_delete"
	}
	return fmt.Sprintf("note_delete(%s)", a.Title)
}

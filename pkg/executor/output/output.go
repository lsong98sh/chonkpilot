package output

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Writer handles output for the executor (named pipe(s) or stdout).
type Writer struct {
	pipePath     string
	outputFormat string
	conn         net.Conn // connection to --pipe-path (IDE mode)
	mu           sync.Mutex
}

// NewWriter creates a new output writer.
// pipePath: main pipe to IDE (set by IDE mode, standalone mode leaves empty).
// outputFormat: "json" or "stdout".
func NewWriter(pipePath, outputFormat string) *Writer {
	w := &Writer{
		pipePath:     pipePath,
		outputFormat: outputFormat,
	}

	// Connect to main pipe (IDE mode)
	if pipePath != "" {
		conn, err := net.DialTimeout("unix", pipePath, 5*time.Second)
		if err == nil {
			w.conn = conn
		}
	}

	return w
}

// WriteEvent writes an event to all connected outputs.
// Priority: IDE pipe > stdout (json) > fallback.
func (w *Writer) WriteEvent(eventType string, payload map[string]interface{}) {
	event := struct {
		Type    string                 `json:"type"`
		Payload map[string]interface{} `json:"payload"`
		EventID string                 `json:"event_id"`
	}{
		Type:    eventType,
		Payload: payload,
		EventID: uuid.New().String(),
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	encodeErr := func(enc *json.Encoder, dst string) {
		if err := enc.Encode(event); err != nil {
			fmt.Fprintf(os.Stderr, "output: %s encode error: %v\n", dst, err)
		}
	}

	switch {
	case w.conn != nil:
		// IDE mode: write to named pipe only
		encodeErr(json.NewEncoder(w.conn), "pipe")
	case w.outputFormat == "json":
		// JSON format output
		encodeErr(json.NewEncoder(os.Stdout), "stdout")
	default:
		// No output (silent) — events only used in IDE/JSON mode
	}
}

// Close closes all output connections.
func (w *Writer) Close() {
	if w.conn != nil {
		w.conn.Close()
	}
}

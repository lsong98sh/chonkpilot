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
	pipeAddr     string
	outputFormat string
	conn         net.Conn // connection to --pipe-path (IDE mode)
	childConn    net.Conn // connection to --pipe-addr (parent-created pipe)
	mu           sync.Mutex
}

// NewWriter creates a new output writer.
// pipePath: main pipe to IDE (set by IDE mode, standalone mode leaves empty).
// outputFormat: "json" or "stdout".
// pipeAddr: parent-created named pipe for sub-executor to write back events (empty for top-level).
func NewWriter(pipePath, outputFormat, pipeAddr string) *Writer {
	w := &Writer{
		pipePath:     pipePath,
		outputFormat: outputFormat,
		pipeAddr:     pipeAddr,
	}

	// Connect to main pipe (IDE mode)
	if pipePath != "" {
		conn, err := net.DialTimeout("unix", pipePath, 5*time.Second)
		if err == nil {
			w.conn = conn
		}
	}

	// Connect to parent-created pipe (sub-executor mode) via TCP
	if pipeAddr != "" {
		conn, err := net.DialTimeout("tcp", pipeAddr, 5*time.Second)
		if err == nil {
			w.childConn = conn
		}
	}

	return w
}

// WriteEvent writes an event to all connected outputs.
// Priority: IDE pipe > child pipe + stdout (dual) > stdout (json) > fallback.
// In sub-executor mode, events go to both the parent pipe (for real-time forwarding)
// and stdout (so callLLMSync can capture the result via parseLLMOutput).
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
	case w.childConn != nil:
		// Sub-executor mode: write to parent pipe for real-time forwarding,
		// AND also to stdout so parent's callLLMSync can capture the result.
		encodeErr(json.NewEncoder(w.childConn), "child-pipe")
		if w.outputFormat == "json" {
			encodeErr(json.NewEncoder(os.Stdout), "stdout")
		}
	case w.outputFormat == "json":
		// JSON format output (used by parent's captured stdout)
		encodeErr(json.NewEncoder(os.Stdout), "stdout")
	default:
		// Fallback: write to stdout as text
		fmt.Printf("event: %s %v\n", eventType, payload)
	}
}

// Close closes all output connections.
func (w *Writer) Close() {
	if w.conn != nil {
		w.conn.Close()
	}
	if w.childConn != nil {
		w.childConn.Close()
	}
}

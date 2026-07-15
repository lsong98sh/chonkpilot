package call_llm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// askUserMu serializes ask_user calls to prevent concurrent goroutines
// from interleaving stdout signals or racing on stdin.
var askUserMu sync.Mutex

var askUserPipeTimeout = 5 * time.Minute

// SetAskUserTimeout sets the timeout for ask_user pipe connections.
func SetAskUserTimeout(seconds int) {
	if seconds > 0 {
		askUserPipeTimeout = time.Duration(seconds) * time.Second
	}
}

// HandleAskUser sends a question to the user and waits for a response.
func HandleAskUser(writeEvent func(string, map[string]interface{}), args map[string]interface{}) *types.ToolResult {
	askUserMu.Lock()
	defer askUserMu.Unlock()

	question, _ := args["question"].(string)
	if question == "" {
		return &types.ToolResult{Success: false, Error: "question is required", Tool: "ask_user"}
	}

	optionsRaw, _ := args["options"].([]interface{})
	custom, _ := args["custom"].(bool)

	// IDE mode: use writeEvent + TCP listener
	if writeEvent != nil {
		return askUserIDE(writeEvent, question, optionsRaw, custom)
	}

	// Standalone mode: use stdout signal + stdin
	return askUserStdio(question, optionsRaw, custom)
}

// askUserIDE sends the question via writeEvent and waits for a response on a TCP connection.
func askUserIDE(writeEvent func(string, map[string]interface{}), question string, optionsRaw []interface{}, custom bool) *types.ToolResult {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create ask_user listener: %s", err.Error()),
			Tool:    "ask_user",
		}
	}
	defer listener.Close()

	pipeAddr := listener.Addr().String()

	// Send event to IDE (which forwards to frontend)
	writeEvent("ask_user", map[string]interface{}{
		"question":  question,
		"options":   optionsRaw,
		"custom":    custom,
		"pipe_addr": pipeAddr,
	})

	// Accept connection from IDE (with timeout)
	conn, err := acceptWithTimeout(listener, askUserPipeTimeout)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("ask_user pipe accept timeout or error: %s", err.Error()),
			Tool:    "ask_user",
		}
	}
	defer conn.Close()

	// Read response JSON
	var response struct {
		Answer string `json:"answer"`
		Custom string `json:"custom,omitempty"`
	}
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read ask_user response: %s", err.Error()),
			Tool:    "ask_user",
		}
	}

	if response.Custom != "" {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("user answered: %s (custom: %s)", response.Answer, response.Custom),
			Tool:    "ask_user",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("user answered: %s", response.Answer),
		Tool:    "ask_user",
	}
}

// acceptWithTimeout wraps listener.Accept with a timeout.
func acceptWithTimeout(listener net.Listener, timeout time.Duration) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		c, e := listener.Accept()
		ch <- result{c, e}
	}()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout after %v", timeout)
	}
}

// askUserStdio writes the question to stdout as ###ASK_USER:...### and reads answer from stdin.
func askUserStdio(question string, optionsRaw []interface{}, custom bool) *types.ToolResult {
	signal := map[string]interface{}{
		"type":     "ask_user",
		"question": question,
		"options":  optionsRaw,
		"custom":   custom,
	}

	signalJSON, _ := json.Marshal(signal)
	fmt.Fprintln(os.Stdout, "###ASK_USER:"+string(signalJSON)+"###")

	// Wait for response from stdin
	scanner := bufio.NewScanner(os.Stdin)
	var response struct {
		Answer string `json:"answer"`
		Custom string `json:"custom,omitempty"`
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &response); err == nil && response.Answer != "" {
			break
		}
	}

	if response.Custom != "" {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("user answered: %s (custom: %s)", response.Answer, response.Custom),
			Tool:    "ask_user",
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("user answered: %s", response.Answer),
		Tool:    "ask_user",
	}
}

func init() {
	types.RegisterSimplify("ask_user", types.SimpleAction("ask_user"))
}

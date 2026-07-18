package process

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewExecutorManager(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	em := NewExecutorManager("/tmp/test", logger)
	if em == nil {
		t.Fatal("NewExecutorManager() returned nil")
	}
}

func TestKillExecutor_NotFound(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	em := NewExecutorManager("/tmp/test", logger)

	err := em.KillExecutor("non-existent-turn")
	if err == nil {
		t.Error("KillExecutor() should return error for non-existent turn")
	}
}

func TestStop(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	em := NewExecutorManager("/tmp/test", logger)

	// Stopping with no executors should not panic
	em.Stop()
}

func TestSetOnEvent(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	em := NewExecutorManager("/tmp/test", logger)

	called := false
	em.SetOnEvent(func(eventType string, payload map[string]interface{}) {
		called = true
	})
	if em.onEvent == nil {
		t.Error("SetOnEvent() should set the callback")
	}

	// Trigger callback indirectly by calling it ourselves
	em.onEvent("test", map[string]interface{}{"key": "val"})
	if !called {
		t.Error("callback should have been called")
	}
}

func TestSplitSSELine(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantType  string
		wantPay   string
	}{
		{
			name:     "event with payload",
			input:    "event: thinking {\"content\":\"thinking...\"}",
			wantType: "thinking",
			wantPay:  `{"content":"thinking..."}`,
		},
		{
			name:     "event without payload",
			input:    "event: done",
			wantType: "done",
			wantPay:  "",
		},
		{
			name:     "data with type in payload",
			input:    "data: {\"type\":\"tool_result\",\"payload\":{\"tool\":\"read\"}}",
			wantType: "tool_result",
			wantPay:  `{"type":"tool_result","payload":{"tool":"read"}}`,
		},
		{
			name:     "data without type in payload",
			input:    "data: {\"key\":\"value\"}",
			wantType: "data",
			wantPay:  `{"key":"value"}`,
		},
		{
			name:     "data with invalid JSON",
			input:    "data: not-json",
			wantType: "data",
			wantPay:  "not-json",
		},
		{
			name:     "empty line",
			input:    "",
			wantType: "",
			wantPay:  "",
		},
		{
			name:     "short line",
			input:    "short",
			wantType: "",
			wantPay:  "",
		},
		{
			name:     "no colon",
			input:    "no colon here",
			wantType: "",
			wantPay:  "",
		},
		{
			name:     "event with leading spaces",
			input:    "event:  thinking   {\"key\":\"val\"}",
			wantType: "thinking",
			wantPay:  `{"key":"val"}`,
		},
		{
			name:     "unknown prefix",
			input:    "custom: value",
			wantType: "",
			wantPay:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitSSELine(tc.input)
			if got.eventType != tc.wantType {
				t.Errorf("eventType = %q, want %q", got.eventType, tc.wantType)
			}
			if got.payload != tc.wantPay {
				t.Errorf("payload = %q, want %q", got.payload, tc.wantPay)
			}
		})
	}
}

func TestMergeContext(t *testing.T) {
	tests := []struct {
		name      string
		payload   map[string]interface{}
		sessionID string
		turnID    string
		wantLen   int
	}{
		{
			name:      "nil payload",
			payload:   nil,
			sessionID: "s1",
			turnID:    "t1",
			wantLen:   2,
		},
		{
			name:      "existing payload",
			payload:   map[string]interface{}{"key": "val"},
			sessionID: "s2",
			turnID:    "t2",
			wantLen:   3,
		},
		{
			name:      "already has session_id - preserved",
			payload:   map[string]interface{}{"session_id": "existing"},
			sessionID: "override",
			turnID:    "t3",
			wantLen:   2, // session_id preserved, turn_id added
		},
		{
			name:      "already has turn_id - preserved",
			payload:   map[string]interface{}{"turn_id": "existing-turn"},
			sessionID: "main",
			turnID:    "override-turn",
			wantLen:   2, // turn_id preserved, session_id added
		},
		{
			name:      "has both session_id and turn_id - both preserved",
			payload:   map[string]interface{}{"session_id": "s-existing", "turn_id": "t-existing"},
			sessionID: "s-override",
			turnID:    "t-override",
			wantLen:   2, // both preserved, nothing added
		},
		{
			name:      "sub_session_id present - session_id injected",
			payload:   map[string]interface{}{"sub_session_id": "sub1"},
			sessionID: "main",
			turnID:    "t4",
			wantLen:   3, // sub_session_id preserved, session_id injected, turn_id added
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeContext(tc.payload, tc.sessionID, tc.turnID)
			if len(got) != tc.wantLen {
				t.Errorf("len = %d, want %d", len(got), tc.wantLen)
			}
			// When payload already has session_id, it should be preserved.
			originalSID, hadSID := tc.payload["session_id"]
			if hadSID {
				if got["session_id"] != originalSID {
					t.Errorf("session_id was overwritten: got %v, want preserved %v", got["session_id"], originalSID)
				}
			} else if got["session_id"] != tc.sessionID {
				t.Errorf("session_id = %v, want %v", got["session_id"], tc.sessionID)
			}
			// When payload already has turn_id, it should be preserved.
			originalTID, hadTID := tc.payload["turn_id"]
			if hadTID {
				if got["turn_id"] != originalTID {
					t.Errorf("turn_id was overwritten: got %v, want preserved %v", got["turn_id"], originalTID)
				}
			} else if got["turn_id"] != tc.turnID {
				t.Errorf("turn_id = %v, want %v", got["turn_id"], tc.turnID)
			}
		})
	}
}

func TestTrimLeft(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  hello", "hello"},
		{"\t\tworld", "world"},
		{"  \t  spaced", "spaced"},
		{"nospaces", "nospaces"},
		{"", ""},
	}
	for _, tc := range tests {
		got := stringsTrimLeft(tc.input)
		if got != tc.want {
			t.Errorf("stringsTrimLeft(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

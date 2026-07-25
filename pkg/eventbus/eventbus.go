// Package eventbus provides an in-process event bus for Go components.
//
// This is the IDE-internal EventBus described in the three-tier architecture:
// Go components can register listeners (On) and emit events (Emit).
// It supplements (not replaces) Wails EventsEmit which broadcasts to the frontend.
package eventbus

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Event represents a structured event on the bus.
type Event struct {
	Type    string                 `json:"type"`
	Payload map[string]interface{} `json:"payload,omitempty"`
	EventID string                 `json:"event_id,omitempty"`
	Source  string                 `json:"source,omitempty"` // e.g. "web", "executor", "ide"
	Time    time.Time              `json:"time"`
}

// EventHandler is a callback function that processes an event.
type EventHandler func(Event)

// Bus is a simple, thread-safe in-process event bus.
type Bus struct {
	handlers map[string][]EventHandler
	mu       sync.RWMutex
}

// New creates a new event bus.
func New() *Bus {
	return &Bus{
		handlers: make(map[string][]EventHandler),
	}
}

// On registers a handler for the given event type.
// Returns an unregister function that removes this specific handler.
// The handler is called synchronously — emit blocks until all handlers complete.
func (b *Bus) On(eventType string, handler EventHandler) func() {
	b.mu.Lock()
	b.handlers[eventType] = append(b.handlers[eventType], handler)
	idx := len(b.handlers[eventType]) - 1
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		handlers := b.handlers[eventType]
		if idx < len(handlers) {
			b.handlers[eventType] = append(handlers[:idx], handlers[idx+1:]...)
		}
		// Clean up empty slices
		if len(b.handlers[eventType]) == 0 {
			delete(b.handlers, eventType)
		}
	}
}

// Emit sends an event to all registered handlers for its type.
// Handlers are called synchronously and in order of registration.
// Panics in handlers are caught and logged to prevent bus corruption.
func (b *Bus) Emit(event Event) {
	if event.EventID == "" {
		event.EventID = uuid.New().String()
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}

	b.mu.RLock()
	handlers, ok := b.handlers[event.Type]
	globalHandlers := b.handlers["*"] // wildcard handler catches all events
	b.mu.RUnlock()

	if ok {
		for _, h := range handlers {
			callHandlerSafe(h, event)
		}
	}

	// Wildcard "*" handlers receive all events
	for _, h := range globalHandlers {
		if ok || len(globalHandlers) > 0 {
			callHandlerSafe(h, event)
		}
	}
}

// EmitSimple is a convenience method to emit an event with just a type and payload.
func (b *Bus) EmitSimple(eventType string, payload map[string]interface{}) {
	b.Emit(Event{
		Type:    eventType,
		Payload: payload,
		Source:  "ide",
	})
}

// callHandlerSafe invokes a handler with panic recovery.
func callHandlerSafe(h EventHandler, event Event) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[eventbus] panic in handler for %q: %v\n", event.Type, r)
		}
	}()
	h(event)
}

// HasHandler returns true if at least one handler is registered for the event type.
func (b *Bus) HasHandler(eventType string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.handlers[eventType]
	return ok
}

// Reset removes all registered handlers.
func (b *Bus) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = make(map[string][]EventHandler)
}

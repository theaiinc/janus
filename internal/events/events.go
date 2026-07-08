package events

import (
	"sync"
	"time"
)

type Type string

const (
	TypeInfo     Type = "info"
	TypeWarning  Type = "warning"
	TypeError    Type = "error"
	TypeRecovery Type = "recovery"
)

type Event struct {
	ID       uint64            `json:"id"`
	Time     time.Time         `json:"time"`
	Type     Type              `json:"type"`
	TunnelID string            `json:"tunnelId,omitempty"`
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type Recorder struct {
	mu     sync.RWMutex
	nextID uint64
	limit  int
	items  []Event
}

func NewRecorder(limit int) *Recorder {
	if limit <= 0 {
		limit = 500
	}
	return &Recorder{limit: limit}
}

func (r *Recorder) Add(t Type, tunnelID, message string, metadata map[string]string) Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	event := Event{
		ID:       r.nextID,
		Time:     time.Now().UTC(),
		Type:     t,
		TunnelID: tunnelID,
		Message:  message,
		Metadata: metadata,
	}
	r.items = append(r.items, event)
	if len(r.items) > r.limit {
		r.items = append([]Event(nil), r.items[len(r.items)-r.limit:]...)
	}
	return event
}

func (r *Recorder) List() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Event, len(r.items))
	copy(out, r.items)
	return out
}

// Package events provides a bounded, scoped event hub. Slow clients disconnect.
package events

import (
	"encoding/json"
	"github.com/google/uuid"
	"sync"
	"time"
)

type Event struct {
	Type        string `json:"type"`
	EventID     string `json:"eventId"`
	StreamID    string `json:"streamId"`
	Seq         uint64 `json:"seq"`
	ContainerID string `json:"containerId"`
	SessionID   string `json:"sessionId,omitempty"`
	TurnID      string `json:"turnId,omitempty"`
	Timestamp   string `json:"timestamp"`
	Payload     any    `json:"payload"`
}
type Key struct{ UserID, ContainerID string }
type Subscription struct {
	Events chan Event
	Done   chan struct{}
	once   sync.Once
}
type Hub struct {
	mu          sync.Mutex
	subscribers map[Key]map[*Subscription]struct{}
	seq         uint64
	stream      string
	maxPayload  int64
	closed      bool
}

func New(maxPayload int64) *Hub {
	return &Hub{subscribers: make(map[Key]map[*Subscription]struct{}), stream: uuid.NewString(), maxPayload: maxPayload}
}
func (h *Hub) Subscribe(k Key) *Subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Subscription{Events: make(chan Event, 16), Done: make(chan struct{})}
	if h.closed {
		s.once.Do(func() { close(s.Done) })
		return s
	}
	if len(h.subscribers[k]) >= 4 || (h.subscribers[k] == nil && len(h.subscribers) >= 256) {
		s.once.Do(func() { close(s.Done) })
		return s
	}
	if h.subscribers[k] == nil {
		h.subscribers[k] = make(map[*Subscription]struct{})
	}
	h.subscribers[k][s] = struct{}{}
	return s
}
func (h *Hub) Unsubscribe(k Key, s *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subscribers[k], s)
	s.once.Do(func() { close(s.Done) })
	if len(h.subscribers[k]) == 0 {
		delete(h.subscribers, k)
	}
}
func (h *Hub) Publish(k Key, e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.seq++
	e.Seq = h.seq
	e.StreamID = h.stream
	e.EventID = uuid.NewString()
	e.ContainerID = k.ContainerID
	e.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(e.Payload)
	if err != nil || int64(len(b)) > h.maxPayload {
		for s := range h.subscribers[k] {
			s.once.Do(func() { close(s.Done) })
		}
		delete(h.subscribers, k)
		return
	}
	for s := range h.subscribers[k] {
		select {
		case s.Events <- e:
		default:
			s.once.Do(func() { close(s.Done) })
			delete(h.subscribers[k], s)
		}
	}
}
func (h *Hub) Invalidate(user, container string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, items := range h.subscribers {
		if (user == "" || user == k.UserID) && (container == "" || container == k.ContainerID) {
			for s := range items {
				s.once.Do(func() { close(s.Done) })
			}
			delete(h.subscribers, k)
		}
	}
}
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for _, items := range h.subscribers {
		for s := range items {
			s.once.Do(func() { close(s.Done) })
		}
	}
	clear(h.subscribers)
}

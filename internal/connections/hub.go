package connections

import (
	"container/list"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"
	"unicode/utf8"
)

// Message is one message of a topic as callers receive it. A JSON payload
// is delivered as JSON, other UTF-8 as a string, and anything else in
// base64 with Encoding set.
type Message struct {
	Topic    string          `json:"topic"`
	Payload  json.RawMessage `json:"payload"`
	Encoding string          `json:"encoding,omitempty"`
	// Retained is a message the broker kept, delivered on subscribing.
	Retained bool      `json:"retained,omitempty"`
	At       time.Time `json:"at"`
	size     int
}

// Hub bounds.
const (
	// hubBytes bounds the last messages a connection keeps.
	hubBytes = 4 << 20
	// keepBytes is the largest payload kept as a topic's last message.
	keepBytes = 64 << 10
	// pendingTopics bounds the topics waiting for one slow listener.
	pendingTopics = 1000
)

// NewMessage makes the message callers receive of a payload.
func NewMessage(topic string, payload []byte, retained bool, at time.Time) Message {
	m := Message{Topic: topic, Retained: retained, At: at.UTC(), size: len(payload) + len(topic)}
	switch {
	case json.Valid(payload):
		m.Payload = append(json.RawMessage(nil), payload...)
	case utf8.Valid(payload):
		m.Payload = marshal(string(payload))
	default:
		m.Payload, m.Encoding = marshal(base64.StdEncoding.EncodeToString(payload)), "base64"
	}
	return m
}

// hub fans a connection's messages out to its listeners and keeps the
// last message of each topic, so a listener starts with current values.
type hub struct {
	mu        sync.Mutex
	last      map[string]*list.Element
	order     *list.List // of Message, least recently updated first
	bytes     int
	listeners map[*listener]struct{}
}

func newHub() *hub {
	return &hub{last: map[string]*list.Element{}, order: list.New(), listeners: map[*listener]struct{}{}}
}

// publish delivers a received message and keeps it as its topic's last.
func (h *hub) publish(m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.last[m.Topic]; ok {
		h.bytes -= e.Value.(Message).size
		h.order.Remove(e)
		delete(h.last, m.Topic)
	}
	if m.size <= keepBytes {
		h.last[m.Topic] = h.order.PushBack(m)
		h.bytes += m.size
		for h.bytes > hubBytes {
			oldest := h.order.Front()
			old := oldest.Value.(Message)
			h.order.Remove(oldest)
			delete(h.last, old.Topic)
			h.bytes -= old.size
		}
	}
	for l := range h.listeners {
		if topicMatches(l.filter, m.Topic) {
			l.offer(m)
		}
	}
}

// listen adds a listener for filter, which starts with the last message of
// each topic it matches.
func (h *hub) listen(filter string) *listener {
	l := &listener{filter: filter, pending: map[string]Message{}, signal: make(chan struct{}, 1)}
	h.mu.Lock()
	defer h.mu.Unlock()
	for e := h.order.Front(); e != nil; e = e.Next() {
		m := e.Value.(Message)
		if topicMatches(filter, m.Topic) {
			l.offer(m)
		}
	}
	h.listeners[l] = struct{}{}
	return l
}

func (h *hub) remove(l *listener) {
	h.mu.Lock()
	delete(h.listeners, l)
	h.mu.Unlock()
}

// listener is one subscription to a hub. Messages wait coalesced to the
// latest of each topic, so a fast topic does not flood a slow listener.
type listener struct {
	filter  string
	mu      sync.Mutex
	pending map[string]Message
	order   []string
	missed  int
	signal  chan struct{}
}

func (l *listener) offer(m Message) {
	l.mu.Lock()
	if _, waiting := l.pending[m.Topic]; !waiting {
		if len(l.order) >= pendingTopics {
			delete(l.pending, l.order[0])
			l.order = l.order[1:]
			l.missed++
		}
		l.order = append(l.order, m.Topic)
	}
	l.pending[m.Topic] = m
	l.mu.Unlock()
	select {
	case l.signal <- struct{}{}:
	default:
	}
}

// take returns the waiting messages, oldest topic first, and how many
// topics were dropped since the last take because too many waited.
func (l *listener) take() ([]Message, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Message, 0, len(l.order))
	for _, topic := range l.order {
		out = append(out, l.pending[topic])
	}
	missed := l.missed
	l.pending, l.order, l.missed = map[string]Message{}, nil, 0
	return out, missed
}

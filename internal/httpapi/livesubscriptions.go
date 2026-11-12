package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"papergo/internal/identity"
)

// SubscriptionSource is a module whose frames ride on the live stream
// without being stored: the stream's caller subscribes to what they want to
// hear, such as a broker's topics, and the module delivers frames to that
// stream only. Frames are not replayed after a reconnection; the module
// delivers what is current when a subscription starts.
type SubscriptionSource interface {
	// SubscriptionSource is the name subscriptions give as their source.
	SubscriptionSource() string
	// Subscribe checks spec and the caller's access and starts delivering
	// frames for subscription id. It acts in the stream's context, as the
	// stream's caller, for as long as the subscription lasts. A refusal is
	// an error the API maps to a problem.
	Subscribe(ctx context.Context, in Request, id string, spec json.RawMessage, deliver Deliver) (Subscription, error)
}

// Deliver hands a frame to the stream, waiting while the stream writes
// earlier ones. It fails once the stream or the subscription ended.
type Deliver func(ctx context.Context, frame LiveFrame) error

// Subscription is one subscription a module serves.
type Subscription interface {
	// Check says why the caller may no longer hear it; the stream calls it
	// every subscriptionCheck and ends the subscription with an access
	// frame when it fails.
	Check(ctx context.Context) error
	// Close stops its frames.
	Close()
}

// subscriptionCheck is how often a stream checks its subscriptions'
// access.
var subscriptionCheck = 30 * time.Second

// maxSubscriptions bounds the subscriptions of one stream.
const maxSubscriptions = 50

// eventStream is an open live stream that subscriptions can be added to.
type eventStream struct {
	id      string
	subject string
	// credential is the access token that opened the stream; empty for a
	// session.
	credential string
	ctx        context.Context
	in         Request
	frames     chan LiveFrame
	mu         sync.Mutex
	subs       map[string]Subscription
	closed     bool
}

// streams are the open live streams of the process, by ID.
type streams struct {
	mu   sync.Mutex
	open map[string]*eventStream
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func credentialOf(ctx context.Context) string {
	token, limited := identity.AccessTokenContext(ctx)
	if !limited {
		return ""
	}
	return token.ID
}

// openStream registers the stream r opened, until close is called.
func (a *API) openStream(r *http.Request) *eventStream {
	s := &eventStream{id: randomID(), subject: subject(r), credential: credentialOf(r.Context()), ctx: r.Context(), in: a.moduleRequest(r), frames: make(chan LiveFrame, 64), subs: map[string]Subscription{}}
	a.streams.mu.Lock()
	if a.streams.open == nil {
		a.streams.open = map[string]*eventStream{}
	}
	a.streams.open[s.id] = s
	a.streams.mu.Unlock()
	return s
}

// close ends the stream's subscriptions and forgets it.
func (a *API) closeStream(s *eventStream) {
	a.streams.mu.Lock()
	delete(a.streams.open, s.id)
	a.streams.mu.Unlock()
	s.mu.Lock()
	s.closed = true
	subs := s.subs
	s.subs = nil
	s.mu.Unlock()
	for _, sub := range subs {
		if sub != nil {
			sub.Close()
		}
	}
}

// stream returns the open stream id that r's caller opened.
func (a *API) stream(r *http.Request, id string) (*eventStream, bool) {
	a.streams.mu.Lock()
	s, ok := a.streams.open[id]
	a.streams.mu.Unlock()
	if !ok || s.subject != subject(r) || s.credential != credentialOf(r.Context()) {
		return nil, false
	}
	return s, true
}

// deliver is how subscription id hands frames to the stream.
func (s *eventStream) deliver(id string) Deliver {
	return func(ctx context.Context, f LiveFrame) error {
		s.mu.Lock()
		_, live := s.subs[id]
		s.mu.Unlock()
		if !live {
			return errors.New("the subscription ended")
		}
		select {
		case s.frames <- f:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
}

// check ends the subscriptions whose access was lost and returns their
// IDs.
func (s *eventStream) check() []string {
	s.mu.Lock()
	subs := make(map[string]Subscription, len(s.subs))
	for id, sub := range s.subs {
		subs[id] = sub
	}
	s.mu.Unlock()
	var lost []string
	for id, sub := range subs {
		if sub == nil || sub.Check(s.ctx) == nil {
			continue
		}
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
		sub.Close()
		lost = append(lost, id)
	}
	return lost
}

type subscribeRequest struct {
	Source string `json:"source"`
}

// subscribe adds a subscription to one of the caller's open streams.
func (a *API) subscribe(w http.ResponseWriter, r *http.Request) {
	s, ok := a.stream(r, r.PathValue("stream"))
	if !ok {
		a.problem(w, r, 404, "not_found", "no open live stream of yours has this ID")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var raw json.RawMessage
	if !a.decode(w, r, &raw) {
		return
	}
	var head subscribeRequest
	if err := json.Unmarshal(raw, &head); err != nil {
		a.problem(w, r, 400, "invalid_body", "the body is a JSON object naming its source")
		return
	}
	var source SubscriptionSource
	for _, module := range a.Modules {
		if candidate, ok := module.(SubscriptionSource); ok && candidate.SubscriptionSource() == head.Source {
			source = candidate
		}
	}
	if source == nil {
		a.problem(w, r, 422, "validation_failed", "source names no subscription source this deployment serves")
		return
	}
	id := randomID()
	// The subscription is live before Subscribe returns, so frames it
	// delivers at once, such as current values, are not refused. Its place
	// counts toward the stream's bound from now.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		a.problem(w, r, 404, "not_found", "no open live stream of yours has this ID")
		return
	}
	if len(s.subs) >= maxSubscriptions {
		s.mu.Unlock()
		a.problem(w, r, 422, "validation_failed", "a live stream holds at most 50 subscriptions")
		return
	}
	s.subs[id] = nil
	s.mu.Unlock()
	sub, err := source.Subscribe(s.ctx, s.in, id, raw, s.deliver(id))
	s.mu.Lock()
	if err != nil || s.closed {
		delete(s.subs, id)
		closed := s.closed
		s.mu.Unlock()
		if err == nil {
			sub.Close()
			if closed {
				a.problem(w, r, 404, "not_found", "no open live stream of yours has this ID")
				return
			}
		}
		a.failure(w, r, err)
		return
	}
	s.subs[id] = sub
	s.mu.Unlock()
	respond(w, r, http.StatusCreated, map[string]string{"id": id})
}

// unsubscribe ends a subscription of one of the caller's open streams.
func (a *API) unsubscribe(w http.ResponseWriter, r *http.Request) {
	s, ok := a.stream(r, r.PathValue("stream"))
	if !ok {
		a.problem(w, r, 404, "not_found", "no open live stream of yours has this ID")
		return
	}
	s.mu.Lock()
	sub, ok := s.subs[r.PathValue("subscription")]
	if ok && sub != nil {
		delete(s.subs, r.PathValue("subscription"))
	}
	s.mu.Unlock()
	if !ok || sub == nil {
		a.problem(w, r, 404, "not_found", "the stream has no such subscription")
		return
	}
	sub.Close()
	w.WriteHeader(http.StatusNoContent)
}

// registerLiveSubscriptions adds the routes that subscribe a stream.
func (a *API) registerLiveSubscriptions(api *router) {
	api.HandleFunc("POST /v1/events/{stream}/subscriptions", a.subscribe)
	api.HandleFunc("DELETE /v1/events/{stream}/subscriptions/{subscription}", a.unsubscribe)
}

package connections

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

// SubscriptionSpec is a subscription to an MQTT channel's messages on the
// live stream: those of topics Filter matches, which lies inside the
// channel's topics. Without a filter it is the channel's.
type SubscriptionSpec struct {
	Source    string `json:"source"`
	Workspace string `json:"workspace"`
	Key       string `json:"key"`
	Channel   string `json:"channel"`
	Filter    string `json:"filter,omitempty"`
}

// MessagesFrame is what a subscription delivers: the latest message of
// each topic since its previous frame, and how many topics it dropped
// because too many waited.
type MessagesFrame struct {
	Subscription string    `json:"subscription"`
	Connection   string    `json:"connection"`
	Channel      string    `json:"channel"`
	Messages     []Message `json:"messages"`
	Missed       int       `json:"missed,omitempty"`
}

// subscription delivers a listener's messages to a live stream.
type subscription struct {
	svc     *Service
	in      httpapi.Request
	spec    SubscriptionSpec
	conn    string
	hub     *hub
	l       *listener
	cancel  context.CancelFunc
	closing sync.Once
}

// checkSpec checks a subscription and returns the connection's ID, as the
// stream's caller, who needs see access to the channel.
func (s *Service) checkSpec(ctx context.Context, d *dms.Service, subject string, spec *SubscriptionSpec) (string, error) {
	return dms.Read(ctx, d, func(t *dms.Service) (string, error) {
		target, err := authorize(ctx, t, subject, spec.Workspace, spec.Key, spec.Channel, UseSee)
		if err != nil {
			return "", err
		}
		if target.conn.Kind != KindMQTT {
			return "", dms.Invalid("only MQTT channels are subscribed to; HTTP channels are fetched")
		}
		if spec.Filter == "" {
			spec.Filter = target.channel.Topics
		}
		if err := checkFilter(spec.Filter); err != nil {
			return "", dms.Invalid("filter: " + err.Error())
		}
		if !filterCovers(target.channel.Topics, spec.Filter) {
			return "", dms.Invalid(fmt.Sprintf("filter %s is not inside channel %s (%s)", spec.Filter, target.channel.Key, target.channel.Topics))
		}
		return target.conn.ID, nil
	})
}

func (s *Service) subscribe(ctx context.Context, in httpapi.Request, id string, raw json.RawMessage, deliver httpapi.Deliver) (httpapi.Subscription, error) {
	var spec SubscriptionSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, dms.Invalid("a subscription names its workspace, key, channel and filter")
	}
	connID, err := s.checkSpec(ctx, in.DMS, in.Subject, &spec)
	if err != nil {
		return nil, err
	}
	run, cancel := context.WithCancel(ctx)
	sub := &subscription{svc: s, in: in, spec: spec, conn: connID, hub: s.runtime.hubFor(connID), cancel: cancel}
	sub.l = sub.hub.listen(spec.Filter)
	go sub.forward(run, id, deliver)
	return sub, nil
}

// forward delivers the listener's messages until the subscription ends.
func (sub *subscription) forward(ctx context.Context, id string, deliver httpapi.Deliver) {
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.l.signal:
		}
		messages, missed := sub.l.take()
		if len(messages) == 0 && missed == 0 {
			continue
		}
		frame := MessagesFrame{Subscription: id, Connection: sub.spec.Key, Channel: sub.spec.Channel, Messages: messages, Missed: missed}
		if err := deliver(ctx, httpapi.LiveFrame{Event: "connection.messages", Data: frame}); err != nil {
			return
		}
	}
}

// Check says why the stream's caller may no longer hear the subscription:
// the connection or channel is gone, the filter left the channel, or the
// caller lost see access.
func (sub *subscription) Check(ctx context.Context) error {
	spec := sub.spec
	id, err := sub.svc.checkSpec(ctx, sub.in.DMS, sub.in.Subject, &spec)
	if err == nil && id != sub.conn {
		err = dms.ErrNotFound
	}
	return err
}

func (sub *subscription) Close() {
	sub.closing.Do(func() {
		sub.cancel()
		sub.hub.remove(sub.l)
	})
}

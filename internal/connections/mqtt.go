package connections

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
	"github.com/gorilla/websocket"

	store "papergo/internal/dal/connections"
)

// MQTT bounds.
const (
	// MaxPayload bounds a published payload; MaxAppPayload one an app
	// publishes.
	MaxPayload    = 256 << 10
	MaxAppPayload = 16 << 10
	// connectTimeout bounds connecting and signing in.
	connectTimeout = 10 * time.Second
)

// dialMQTT connects to an MQTT target as the policy allows: over TCP, TLS,
// or WebSocket with or without TLS. It never follows proxy settings.
func (r *Runtime) dialMQTT(ctx context.Context, u *url.URL, ca string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	secure := schemes[KindMQTT][u.Scheme]
	policy := r.policy(secure)
	var config *tls.Config
	if secure {
		var err error
		if config, err = tlsConfig(ca, u.Hostname()); err != nil {
			return nil, err
		}
	}
	switch u.Scheme {
	case "mqtt", "mqtts":
		port := u.Port()
		if port == "" {
			port = "1883"
			if secure {
				port = "8883"
			}
		}
		conn, err := policy.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
		if err != nil || !secure {
			return conn, err
		}
		t := tls.Client(conn, config)
		if err = t.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("TLS with %s failed: %w", u.Hostname(), err)
		}
		return packets.NewThreadSafeConn(t), nil
	case "ws", "wss":
		d := websocket.Dialer{NetDialContext: policy.DialContext, Proxy: nil, TLSClientConfig: config, Subprotocols: []string{"mqtt"}, HandshakeTimeout: connectTimeout}
		ws, resp, err := d.DialContext(ctx, u.String(), nil)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if err != nil {
			if resp != nil && resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return nil, errors.New("the WebSocket endpoint redirected; give the address it redirects to")
			}
			return nil, fmt.Errorf("WebSocket connection failed: %w", err)
		}
		return &wsConn{Conn: ws}, nil
	}
	return nil, errors.New("unknown scheme " + u.Scheme)
}

// wsConn is a WebSocket as the net.Conn MQTT reads and writes. paho
// writes a packet in pieces while it holds the connection's lock; the
// pieces go out as one binary message when it lets go, since brokers such
// as Mosquitto refuse a packet split across messages. Reads run across
// messages.
type wsConn struct {
	*websocket.Conn
	r   io.Reader
	rmu sync.Mutex

	wmu      sync.Mutex
	batching bool
	pending  []byte
	err      error
}

// Lock starts a packet.
func (c *wsConn) Lock() {
	c.wmu.Lock()
	c.batching = true
}

// Unlock sends the packet written since Lock.
func (c *wsConn) Unlock() {
	if len(c.pending) > 0 && c.err == nil {
		c.err = c.WriteMessage(websocket.BinaryMessage, c.pending)
	}
	c.pending, c.batching = c.pending[:0], false
	c.wmu.Unlock()
}

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *wsConn) Write(p []byte) (int, error) {
	if c.batching {
		if c.err != nil {
			return 0, c.err
		}
		c.pending = append(c.pending, p...)
		return len(p), nil
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.err != nil {
		return 0, c.err
	}
	if c.err = c.WriteMessage(websocket.BinaryMessage, p); c.err != nil {
		return 0, c.err
	}
	return len(p), nil
}

func (c *wsConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.r == nil {
			var err error
			if _, c.r, err = c.NextReader(); err != nil {
				return 0, err
			}
		}
		n, err := c.r.Read(p)
		if err == io.EOF {
			c.r = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

var (
	_ net.Conn    = (*wsConn)(nil)
	_ sync.Locker = (*wsConn)(nil)
)

// mqttEndpoint is a broker session: always connected while the connection
// is enabled, subscribed to the topics of its see channels.
type mqttEndpoint struct {
	conn   store.Connection
	hub    *hub
	cm     *autopaho.ConnectionManager
	cancel context.CancelFunc

	mu    sync.Mutex
	state Status
}

func (e *mqttEndpoint) status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

func (e *mqttEndpoint) set(state, lastError string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.State = state
	if lastError != "" || state == StateConnected {
		e.state.LastError = lastError
	}
	if state == StateConnected {
		at := time.Now().UTC()
		e.state.ConnectedAt = &at
	} else {
		e.state.ConnectedAt = nil
	}
}

func (e *mqttEndpoint) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = e.cm.Disconnect(ctx)
	e.cancel()
}

// backoff waits longer after each failed attempt, up to a minute.
func backoff(attempt int) time.Duration {
	if attempt == 0 {
		return 0
	}
	d := time.Second << min(attempt-1, 6)
	return min(d, time.Minute)
}

// seeFilters are the topic filters the connection subscribes to.
func seeFilters(channels []Channel) []string {
	var out []string
	for _, ch := range channels {
		if ch.Can(UseSee) {
			out = append(out, ch.Topics)
		}
	}
	return out
}

// mqttConfig is a broker session's configuration.
func (r *Runtime) mqttConfig(c store.Connection, v sealed, u *url.URL) autopaho.ClientConfig {
	return autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		KeepAlive:                     30,
		CleanStartOnInitialConnection: true,
		SessionExpiryInterval:         0,
		ReconnectBackoff:              backoff,
		ConnectTimeout:                connectTimeout,
		ConnectUsername:               v.Credentials.Username,
		ConnectPassword:               []byte(v.Credentials.Password),
		AttemptConnection: func(ctx context.Context, _ autopaho.ClientConfig, u *url.URL) (net.Conn, error) {
			return r.dialMQTT(ctx, u, v.CA)
		},
		ClientConfig: paho.ClientConfig{ClientID: "papergo-" + c.ID},
	}
}

func (r *Runtime) startMQTT(c store.Connection, v sealed, u *url.URL) (endpoint, error) {
	ctx, cancel := context.WithCancel(context.Background())
	e := &mqttEndpoint{conn: c, hub: r.hubFor(c.ID), cancel: cancel, state: Status{State: StateConnecting}}
	channels := channelsOf(c)
	filters := seeFilters(channels)
	config := r.mqttConfig(c, v, u)
	config.OnConnectionUp = func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
		e.set(StateConnected, "")
		if len(filters) == 0 {
			return
		}
		go func() {
			sub := &paho.Subscribe{}
			for _, f := range filters {
				sub.Subscriptions = append(sub.Subscriptions, paho.SubscribeOptions{Topic: f, QoS: 1})
			}
			sctx, done := context.WithTimeout(ctx, connectTimeout)
			defer done()
			ack, err := cm.Subscribe(sctx, sub)
			if err != nil {
				e.set(StateConnected, "subscribing failed: "+err.Error())
				return
			}
			var refused []string
			for i, reason := range ack.Reasons {
				if reason >= 0x80 && i < len(filters) {
					refused = append(refused, filters[i])
				}
			}
			if len(refused) > 0 {
				e.set(StateConnected, "the broker refused subscribing to "+strings.Join(refused, ", ")+"; check its ACL for this user")
			}
		}()
	}
	config.OnConnectionDown = func() bool {
		e.set(StateConnecting, "")
		return true
	}
	config.OnConnectError = func(err error) { e.set(StateFailed, describeMQTT(err)) }
	config.OnPublishReceived = []func(paho.PublishReceived) (bool, error){func(p paho.PublishReceived) (bool, error) {
		m := NewMessage(p.Packet.Topic, p.Packet.Payload, p.Packet.Retain, time.Now())
		e.hub.publish(m)
		r.fire(c, channels, m)
		return true, nil
	}}
	cm, err := autopaho.NewConnection(ctx, config)
	if err != nil {
		cancel()
		return nil, err
	}
	e.cm = cm
	return e, nil
}

// describeMQTT words what a broker or the network said.
func describeMQTT(err error) string {
	var connack *autopaho.ConnackError
	if errors.As(err, &connack) {
		switch connack.ReasonCode {
		case 0x84:
			return "the broker does not speak MQTT 5; PaperGo needs Mosquitto 1.6 or later, EMQX, HiveMQ or another MQTT 5 broker"
		case 0x86, 0x87:
			return "the broker refused the user name or password"
		}
		return fmt.Sprintf("the broker refused the connection (reason %#x)", connack.ReasonCode)
	}
	return explain(err)
}

// publish sends a message and waits for the broker's acknowledgement at
// QoS 1.
func (e *mqttEndpoint) publish(ctx context.Context, topic string, payload []byte, qos byte, retain bool, request string) error {
	p := &paho.Publish{Topic: topic, QoS: qos, Retain: retain, Payload: payload, Properties: &paho.PublishProperties{User: paho.UserProperties{{Key: "papergo-request", Value: request}}}}
	resp, err := e.cm.Publish(ctx, p)
	if errors.Is(err, autopaho.ConnectionDownError) {
		return notReady("connection " + e.conn.Key + " is not connected to its broker")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return connectionTimeout("the broker did not acknowledge the message in time")
	}
	if err != nil {
		return connectionFailed("publishing failed: " + err.Error())
	}
	if resp != nil && resp.ReasonCode >= 0x80 {
		if resp.ReasonCode == 0x87 {
			return connectionFailed("the broker refused this topic for PaperGo's user; check its ACL")
		}
		return connectionFailed(fmt.Sprintf("the broker refused the message (reason %#x)", resp.ReasonCode))
	}
	return nil
}

// testMQTT connects and signs in once, then disconnects.
func (r *Runtime) testMQTT(ctx context.Context, c store.Connection, v sealed, u *url.URL) error {
	conn, err := r.dialMQTT(ctx, u, v.CA)
	if err != nil {
		return err
	}
	client := paho.NewClient(paho.ClientConfig{ClientID: "papergo-test-" + c.ID, Conn: conn})
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	connect := &paho.Connect{KeepAlive: 10, ClientID: "papergo-test-" + c.ID, CleanStart: true, Username: v.Credentials.Username, Password: []byte(v.Credentials.Password),
		UsernameFlag: v.Credentials.Username != "", PasswordFlag: v.Credentials.Password != ""}
	ack, err := client.Connect(cctx, connect)
	if err != nil {
		if ack != nil {
			return errors.New(describeMQTT(autopaho.NewConnackError(err, ack)))
		}
		conn.Close()
		if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "EOF") {
			return errors.New("the broker closed the connection: it may not speak MQTT 5, which PaperGo needs (Mosquitto 1.6 or later, EMQX, HiveMQ)")
		}
		return err
	}
	_ = client.Disconnect(&paho.Disconnect{ReasonCode: 0})
	return nil
}

// hubFor returns connection id's hub, which outlives its sessions: a
// subscription keeps listening while its connection reconnects or is
// reconfigured.
func (r *Runtime) hubFor(id string) *hub {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hubs[id]
	if h == nil {
		h = newHub()
		r.hubs[id] = h
	}
	return h
}

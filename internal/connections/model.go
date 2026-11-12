// Package connections is the connections feature: outside endpoints of a
// workspace, MQTT brokers and HTTP services, that apps and workflows reach
// through named channels. A connection keeps its target and credentials
// sealed; apps and workflows name it by key and never see either. Who may
// use a channel is borrowed from the permissions of the workspace or of one
// of its lists, so connections keep no permissions of their own. The server
// holds the connection itself: broker sessions, the last message of each
// topic, HTTP clients and caches live in this process.
package connections

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	store "papergo/internal/dal/connections"
	"papergo/internal/dms"
)

// Scopes. ManageScope writes connections, with manage access to their
// workspace; ControlScope publishes and calls through them.
const (
	ManageScope  = "connection.manage"
	ControlScope = "connection.control"
)

// Kind is what a connection reaches.
type Kind = store.Kind

const (
	KindMQTT = store.KindMQTT
	KindHTTP = store.KindHTTP
)

// Use is what a caller does with a channel: see (subscribe, or read with
// GET and HEAD) or control (publish, or call with any other method).
type Use string

const (
	UseSee     Use = "see"
	UseControl Use = "control"
)

func (u Use) Valid() bool { return u == UseSee || u == UseControl }

// Level is the permission a channel's use needs on the resource it borrows
// from, as a workflow manual trigger's access is.
type Level string

const (
	LevelRead   Level = "read"
	LevelWrite  Level = "write"
	LevelManage Level = "manage"
)

func (l Level) Valid() bool { return l == LevelRead || l == LevelWrite || l == LevelManage }

// OnWorkspace borrows a channel's access from the connection's workspace.
const OnWorkspace = "workspace"

// Access is who may use a channel one way: those with Level on On, the
// workspace or one of its lists or libraries.
type Access struct {
	Level Level  `json:"level"`
	On    string `json:"on"`
}

// defaultAccess is a use's access when a channel names none: the
// workspace's members, for seeing as for controlling. Visitors are often
// outsiders, and what a house reports tells when nobody is home.
var defaultAccess = Access{Level: LevelWrite, On: OnWorkspace}

// Channel is a named part of what a connection reaches, and who may reach
// it. An MQTT channel is a topic filter with its uses; an HTTP channel is
// a path pattern with methods, whose uses follow from them.
type Channel struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// MQTT.
	Topics string `json:"topics,omitempty"`
	Uses   []Use  `json:"uses,omitempty"`
	// Retain lets publishes keep the topic's last message on the broker.
	Retain bool `json:"retain,omitempty"`
	// HTTP.
	Path    string   `json:"path,omitempty"`
	Methods []string `json:"methods,omitempty"`
	// Cache keeps GET answers that long, as a duration such as 30s.
	Cache  string          `json:"cache,omitempty"`
	Access map[Use]*Access `json:"access,omitempty"`
}

// Can reports whether the channel serves use.
func (c Channel) Can(use Use) bool { return slices.Contains(c.Uses, use) }

// cacheFor is how long the channel keeps GET answers; zero keeps none.
func (c Channel) cacheFor() time.Duration {
	d, _ := time.ParseDuration(c.Cache)
	return d
}

// MethodUse is the use an HTTP method is.
func MethodUse(method string) Use {
	if method == http.MethodGet || method == http.MethodHead {
		return UseSee
	}
	return UseControl
}

var methods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// MaxChannels bounds a connection's channels.
const MaxChannels = 50

var (
	keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	nonKey     = regexp.MustCompile(`[^a-z0-9]+`)
	// headerName is an HTTP token.
	headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,64}$")
)

func keyFromName(name string) string {
	k := strings.Trim(nonKey.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(k) > 90 {
		k = strings.Trim(k[:90], "-")
	}
	if k == "" {
		k = "connection"
	}
	return k
}

// normalizeChannels checks a kind's channels and fills what they leave
// out: names, uses and access. Two channels that serve the same use may
// not reach a common topic or path, or the wider would open the narrower.
func normalizeChannels(kind Kind, in []Channel) ([]Channel, error) {
	if len(in) > MaxChannels {
		return nil, dms.Invalid(fmt.Sprintf("a connection has at most %d channels", MaxChannels))
	}
	out := make([]Channel, len(in))
	keys := map[string]bool{}
	for i, c := range in {
		at := fmt.Sprintf("channels[%d]: ", i)
		if !keyPattern.MatchString(c.Key) || len(c.Key) > 64 {
			return nil, dms.Invalid(at + "key is up to 64 lowercase letters, digits, - or _")
		}
		if keys[c.Key] {
			return nil, dms.Invalid(at + "another channel has the key " + c.Key)
		}
		keys[c.Key] = true
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			c.Name = c.Key
		}
		if utf8.RuneCountInString(c.Name) > 200 {
			return nil, dms.Invalid(at + "name is limited to 200 characters")
		}
		switch kind {
		case KindMQTT:
			if c.Path != "" || len(c.Methods) > 0 || c.Cache != "" {
				return nil, dms.Invalid(at + "an MQTT channel takes topics and uses, not path, methods or cache")
			}
			if err := checkFilter(c.Topics); err != nil {
				return nil, dms.Invalid(at + "topics: " + err.Error())
			}
			if len(c.Uses) == 0 {
				return nil, dms.Invalid(at + "uses is see, control or both")
			}
			uses := []Use{}
			for _, u := range []Use{UseSee, UseControl} {
				if slices.Contains(c.Uses, u) {
					uses = append(uses, u)
				}
			}
			for _, u := range c.Uses {
				if !u.Valid() {
					return nil, dms.Invalid(at + "uses is see, control or both")
				}
			}
			c.Uses = uses
			if c.Retain && !c.Can(UseControl) {
				return nil, dms.Invalid(at + "retain belongs to channels that control")
			}
		case KindHTTP:
			if c.Topics != "" || c.Retain {
				return nil, dms.Invalid(at + "an HTTP channel takes path and methods; its uses follow from the methods")
			}
			// Uses follow from the methods, whatever a channel read back says.
			c.Uses = nil
			if err := checkPattern(c.Path); err != nil {
				return nil, dms.Invalid(at + "path: " + err.Error())
			}
			if len(c.Methods) == 0 {
				return nil, dms.Invalid(at + "methods lists GET, HEAD, POST, PUT, PATCH or DELETE")
			}
			seen := map[string]bool{}
			for j, m := range c.Methods {
				m = strings.ToUpper(m)
				if !slices.Contains(methods, m) || seen[m] {
					return nil, dms.Invalid(at + "methods are distinct among GET, HEAD, POST, PUT, PATCH and DELETE")
				}
				seen[m] = true
				c.Methods[j] = m
			}
			slices.SortFunc(c.Methods, func(a, b string) int { return slices.Index(methods, a) - slices.Index(methods, b) })
			for _, u := range []Use{UseSee, UseControl} {
				for _, m := range c.Methods {
					if MethodUse(m) == u {
						c.Uses = append(c.Uses, u)
						break
					}
				}
			}
			if c.Cache != "" {
				d, err := time.ParseDuration(c.Cache)
				if err != nil || d < time.Second || d > time.Hour || !seen[http.MethodGet] {
					return nil, dms.Invalid(at + "cache is a duration from 1s to 1h, on channels that GET")
				}
				c.Cache = d.String()
			}
		}
		access := map[Use]*Access{}
		for u, a := range c.Access {
			if !c.Can(u) {
				return nil, dms.Invalid(at + "access names a use the channel does not serve: " + string(u))
			}
			if a == nil {
				continue
			}
			if !a.Level.Valid() {
				return nil, dms.Invalid(at + "access level is read, write or manage")
			}
			if a.On == "" {
				a.On = OnWorkspace
			}
			access[u] = &Access{Level: a.Level, On: a.On}
		}
		for _, u := range c.Uses {
			if access[u] == nil {
				d := defaultAccess
				access[u] = &d
			}
		}
		c.Access = access
		out[i] = c
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			for _, u := range out[i].Uses {
				if out[j].Can(u) && overlap(kind, u, out[i], out[j]) {
					return nil, dms.Invalid(fmt.Sprintf("channels %s and %s both %s what they share; narrow one so they do not overlap", out[i].Key, out[j].Key, u))
				}
			}
		}
	}
	return out, nil
}

// overlap reports whether two channels reach something in common for use.
func overlap(kind Kind, use Use, a, b Channel) bool {
	if kind == KindMQTT {
		return filtersOverlap(a.Topics, b.Topics)
	}
	shared := false
	for _, m := range a.Methods {
		if MethodUse(m) == use && slices.Contains(b.Methods, m) {
			shared = true
		}
	}
	return shared && patternsOverlap(a.Path, b.Path)
}

// Credentials is how a connection signs in: a broker's user name and
// password, or an HTTP service's Basic, bearer or header credential.
type Credentials struct {
	Type     string `json:"type,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	Name     string `json:"name,omitempty"`
	Value    string `json:"value,omitempty"`
}

// Credential types.
const (
	CredentialNone   = "none"
	CredentialBasic  = "basic"
	CredentialBearer = "bearer"
	CredentialHeader = "header"
)

// public is what managers see of credentials: never a secret.
func (c Credentials) public() Credentials {
	return Credentials{Type: c.Type, Username: c.Username, Name: c.Name}
}

// sealed is what a connection keeps encrypted: where it goes and how it
// signs in.
type sealed struct {
	Target        string            `json:"target"`
	CA            string            `json:"ca,omitempty"`
	Credentials   Credentials       `json:"credentials"`
	Headers       map[string]string `json:"headers,omitempty"`
	SigningSecret string            `json:"signing_secret,omitempty"`
}

// Settings are a connection's plain options; HTTP ones only.
type Settings struct {
	// Timeout bounds a call, such as 10s.
	Timeout string `json:"timeout,omitempty"`
	// Sign adds the caller and a signature to each call.
	Sign bool `json:"sign,omitempty"`
	// HealthPath is what Test calls; the base URL by default.
	HealthPath string `json:"health_path,omitempty"`
}

// DefaultTimeout bounds an HTTP call whose connection sets none.
const DefaultTimeout = 10 * time.Second

func (s Settings) timeout() time.Duration {
	if d, err := time.ParseDuration(s.Timeout); err == nil && d > 0 {
		return d
	}
	return DefaultTimeout
}

// schemes are the targets each kind reaches, and whether each is TLS.
var schemes = map[Kind]map[string]bool{
	KindMQTT: {"mqtt": false, "mqtts": true, "ws": false, "wss": true},
	KindHTTP: {"http": false, "https": true},
}

// parseTarget checks a kind's target: a URL of one of its schemes, without
// credentials, query or fragment. An MQTT target over TCP has no path.
func parseTarget(kind Kind, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Hostname() == "" || u.Opaque != "" || len(raw) > 2048 {
		return nil, errors.New("target is a URL such as " + example(kind))
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if _, ok := schemes[kind][u.Scheme]; !ok {
		return nil, errors.New("target is a URL such as " + example(kind))
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("a target holds no credentials, query or fragment; give credentials apart")
	}
	if (u.Scheme == "mqtt" || u.Scheme == "mqtts") && u.Path != "" && u.Path != "/" {
		return nil, errors.New("an MQTT target over TCP has no path")
	}
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

func example(kind Kind) string {
	if kind == KindHTTP {
		return "http://192.168.1.20:1880/api or https://flows.example.com"
	}
	return "mqtt://192.168.1.20:1883, mqtts://broker.example.com:8883 or wss://broker.example.com/mqtt"
}

// tlsScheme reports whether a target's scheme uses TLS.
func tlsScheme(kind Kind, u *url.URL) bool { return schemes[kind][u.Scheme] }

// checkSealed checks what a kind keeps sealed, but not where it goes,
// which the network policy decides.
func checkSealed(kind Kind, s *sealed) error {
	c := &s.Credentials
	if c.Type == "" {
		c.Type = CredentialNone
		if c.Username != "" || c.Password != "" {
			c.Type = CredentialBasic
		}
	}
	switch {
	case c.Type == CredentialNone:
		*c = Credentials{Type: CredentialNone}
	case c.Type == CredentialBasic:
		if c.Username == "" || len(c.Username) > 256 || len(c.Password) > 1024 || c.Token != "" || c.Name != "" || c.Value != "" {
			return errors.New("basic credentials are a user name and a password")
		}
	case kind == KindHTTP && c.Type == CredentialBearer:
		if c.Token == "" || len(c.Token) > 4096 || strings.ContainsAny(c.Token, "\r\n") || c.Username != "" || c.Password != "" || c.Name != "" || c.Value != "" {
			return errors.New("bearer credentials are a token")
		}
	case kind == KindHTTP && c.Type == CredentialHeader:
		if !headerName.MatchString(c.Name) || c.Value == "" || len(c.Value) > 4096 || strings.ContainsAny(c.Value, "\r\n") || c.Username != "" || c.Password != "" || c.Token != "" {
			return errors.New("header credentials are a header name and its value")
		}
		if reservedHeader(c.Name) {
			return errors.New("the credential cannot be the " + c.Name + " header")
		}
	default:
		if kind == KindMQTT {
			return errors.New("an MQTT connection signs in with none or basic credentials")
		}
		return errors.New("credentials are none, basic, bearer or header")
	}
	if s.CA != "" && (len(s.CA) > 64<<10 || !strings.Contains(s.CA, "-----BEGIN CERTIFICATE-----")) {
		return errors.New("tls.ca is one or more PEM certificates")
	}
	if kind == KindMQTT && len(s.Headers) > 0 {
		return errors.New("headers belong to HTTP connections")
	}
	if len(s.Headers) > 20 {
		return errors.New("a connection has at most 20 headers")
	}
	for name, value := range s.Headers {
		if !headerName.MatchString(name) || len(value) > 1024 || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("header %q is not a valid header", name)
		}
		if reservedHeader(name) {
			return errors.New("the " + name + " header is PaperGo's")
		}
	}
	return nil
}

// reservedHeader reports whether a connection may not set header name:
// what the request needs to be the request, and PaperGo's own.
func reservedHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-length", "content-type", "transfer-encoding", "connection", "cookie", "accept", "if-match", "if-none-match", "idempotency-key", "te", "upgrade", "proxy-authorization":
		return true
	}
	return strings.HasPrefix(strings.ToLower(name), "papergo-")
}

// checkSettings checks a kind's plain options and fills their defaults.
func checkSettings(kind Kind, s *Settings) error {
	if kind == KindMQTT {
		if *s != (Settings{}) {
			return errors.New("timeout, sign and health_path belong to HTTP connections")
		}
		return nil
	}
	if s.Timeout == "" {
		s.Timeout = DefaultTimeout.String()
	}
	d, err := time.ParseDuration(s.Timeout)
	if err != nil || d < time.Second || d > 25*time.Second {
		return errors.New("timeout is a duration from 1s to 25s")
	}
	s.Timeout = d.String()
	if s.HealthPath != "" {
		if _, err := ParsePath(s.HealthPath); err != nil {
			return errors.New("health_path: " + err.Error())
		}
	}
	return nil
}

func marshal(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

package connections

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTopicFilters(t *testing.T) {
	for _, f := range []string{"home/#", "#", "home/+/temperature", "+", "home/boiler/set", "a//b"} {
		if err := checkFilter(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	for _, f := range []string{"", "$SYS/#", "$share/g/home/#", "home/#/x", "home/te+mp", "home/a#", strings.Repeat("a", 257)} {
		if err := checkFilter(f); err == nil {
			t.Errorf("%s accepted", f)
		}
	}
	if checkTopic("home/+/set") == nil || checkTopic("home/#") == nil || checkTopic("home/kitchen/light/set") != nil {
		t.Fatal("publish topics")
	}
	matches := []struct {
		filter, topic string
		want          bool
	}{
		{"home/#", "home", true}, {"home/#", "home/a/b", true}, {"home/+/t", "home/a/t", true},
		{"home/+/t", "home/a/b/t", false}, {"home/+", "home", false}, {"#", "anything/at/all", true}, {"home/a", "home/a/b", false},
	}
	for _, c := range matches {
		if topicMatches(c.filter, c.topic) != c.want {
			t.Errorf("%s matches %s: %v", c.filter, c.topic, !c.want)
		}
	}
	covers := []struct {
		outer, sub string
		want       bool
	}{
		{"home/#", "home/+/sensor/#", true}, {"home/#", "home", true}, {"home/+/sensor/#", "home/living/sensor/temperature", true},
		{"home/+/sensor/#", "home/#", false}, {"home/+", "home/+/x", false}, {"home/a", "home/+", false}, {"#", "#", true},
		{"home/+/sensor/#", "home/+/sensor/#", true}, {"home/boiler/#", "home/boiler", true},
	}
	for _, c := range covers {
		if filterCovers(c.outer, c.sub) != c.want {
			t.Errorf("%s covers %s: %v", c.outer, c.sub, !c.want)
		}
	}
	overlaps := []struct {
		a, b string
		want bool
	}{
		{"home/#", "home/boiler/#", true}, {"home/+/light/set", "home/kitchen/#", true}, {"home/+/sensor/#", "home/boiler/#", true}, {"home/+/sensor/#", "boiler/#", false},
		{"home/a", "home/b", false}, {"home/#", "home", true}, {"home/+", "home", false}, {"office/#", "home/#", false}, {"+/+", "a/b", true},
	}
	for _, c := range overlaps {
		if filtersOverlap(c.a, c.b) != c.want || filtersOverlap(c.b, c.a) != c.want {
			t.Errorf("%s overlaps %s: %v", c.a, c.b, !c.want)
		}
	}
}

func TestPaths(t *testing.T) {
	for _, p := range []string{"/", "/status", "/heating/**", "/devices/*/set", "/a/*/b/**"} {
		if err := checkPattern(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"", "status", "/a//b", "/**/x", "/a/../b", "/a*", "/a%2f", "/a?b"} {
		if checkPattern(p) == nil {
			t.Errorf("%s accepted", p)
		}
	}
	for _, raw := range []string{"/heating/state?zone=living", "/", "/a%20b/c", "/devices/42/set"} {
		if _, err := ParsePath(raw); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{"heating", "//evil.example.com/x", "/a/../b", "/a/%2e%2e/b", "/a/%2F/b", "/a\\b", "/a//b", "/a#x", "http://x/", "/a/./b", "/a b", "/x?" + strings.Repeat("q", 2049)} {
		if _, err := ParsePath(raw); err == nil {
			t.Errorf("%s accepted", raw)
		}
	}
	p, _ := ParsePath("/heating/zone%201/set?x=1")
	if strings.Join(p.Segments, "|") != "heating|zone 1|set" || p.Raw != "/heating/zone%201/set?x=1" {
		t.Fatalf("%+v", p)
	}
	matches := []struct {
		pattern, path string
		want          bool
	}{
		{"/heating/**", "/heating/state", true}, {"/heating/**", "/heating", false}, {"/heating/**", "/heating/a/b", true},
		{"/devices/*/set", "/devices/1/set", true}, {"/devices/*/set", "/devices/1/2/set", false}, {"/", "/", true}, {"/status", "/status/x", false},
	}
	for _, c := range matches {
		path, err := ParsePath(c.path)
		if err != nil {
			t.Fatal(err)
		}
		if pathMatches(c.pattern, path.Segments) != c.want {
			t.Errorf("%s matches %s: %v", c.pattern, c.path, !c.want)
		}
	}
	overlaps := []struct {
		a, b string
		want bool
	}{
		{"/heating/**", "/heating/set", true}, {"/heating/**", "/heating", false}, {"/**", "/status", true}, {"/a/*", "/b/*", false},
		{"/a/*/c", "/a/b/*", true}, {"/a/**", "/a/b/**", true}, {"/", "/**", false}, {"/", "/", true},
	}
	for _, c := range overlaps {
		if patternsOverlap(c.a, c.b) != c.want || patternsOverlap(c.b, c.a) != c.want {
			t.Errorf("%s overlaps %s: %v", c.a, c.b, !c.want)
		}
	}
}

func TestChannelsNormalize(t *testing.T) {
	out, err := normalizeChannels(KindMQTT, []Channel{
		{Key: "sensors", Topics: "home/+/sensor/#", Uses: []Use{UseSee}},
		{Key: "heating", Name: "Heating", Topics: "boiler/#", Uses: []Use{UseControl, UseSee}, Access: map[Use]*Access{UseControl: {Level: LevelWrite, On: "list-1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Name != "sensors" || *out[0].Access[UseSee] != defaultAccess || out[0].Access[UseControl] != nil {
		t.Fatalf("defaults: %+v", out[0])
	}
	if out[1].Uses[0] != UseSee || out[1].Access[UseControl].On != "list-1" || *out[1].Access[UseSee] != defaultAccess {
		t.Fatalf("uses and access: %+v", out[1])
	}
	refused := [][]Channel{
		{{Key: "all", Topics: "home/#", Uses: []Use{UseControl}}, {Key: "boiler", Topics: "home/boiler/#", Uses: []Use{UseControl}}},
		{{Key: "a", Topics: "home/#", Uses: []Use{UseSee}}, {Key: "a", Topics: "office/#", Uses: []Use{UseSee}}},
		{{Key: "x", Topics: "home/#"}},
		{{Key: "x", Topics: "home/#", Uses: []Use{UseSee}, Retain: true}},
		{{Key: "x", Topics: "home/#", Uses: []Use{UseSee}, Access: map[Use]*Access{UseControl: {Level: LevelWrite}}}},
		{{Key: "x", Topics: "home/#", Uses: []Use{UseSee}, Access: map[Use]*Access{UseSee: {Level: "owner"}}}},
		{{Key: "X", Topics: "home/#", Uses: []Use{UseSee}}},
		{{Key: "x", Path: "/a", Methods: []string{"GET"}}},
	}
	for i, channels := range refused {
		if _, err := normalizeChannels(KindMQTT, channels); err == nil {
			t.Errorf("mqtt %d accepted", i)
		}
	}
	// Seeing and controlling the same topics through two channels is fine:
	// they serve different uses.
	if _, err := normalizeChannels(KindMQTT, []Channel{{Key: "see", Topics: "home/#", Uses: []Use{UseSee}}, {Key: "set", Topics: "home/boiler/set", Uses: []Use{UseControl}}}); err != nil {
		t.Fatal(err)
	}
	h, err := normalizeChannels(KindHTTP, []Channel{
		{Key: "heating", Path: "/heating/**", Methods: []string{"post", "GET"}, Cache: "30s"},
		{Key: "status", Path: "/status", Methods: []string{"GET"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(h[0].Methods, ",") != "GET,POST" || len(h[0].Uses) != 2 || h[0].Cache != "30s" || h[1].Uses[0] != UseSee || h[1].Access[UseControl] != nil {
		t.Fatalf("%+v", h)
	}
	refusedHTTP := [][]Channel{
		{{Key: "a", Path: "/heating/**", Methods: []string{"POST"}}, {Key: "b", Path: "/heating/set", Methods: []string{"POST", "GET"}}},
		{{Key: "a", Path: "/x", Methods: []string{"TRACE"}}},
		{{Key: "a", Path: "/x", Methods: []string{"POST"}, Cache: "10s"}},
		{{Key: "a", Path: "x", Methods: []string{"GET"}}},
		{{Key: "a", Path: "/x", Methods: []string{"GET", "GET"}}},
		{{Key: "a", Topics: "home/#", Path: "/x", Methods: []string{"GET"}}},
	}
	for i, channels := range refusedHTTP {
		if _, err := normalizeChannels(KindHTTP, channels); err == nil {
			t.Errorf("http %d accepted", i)
		}
	}
	// One channel reads /heating/** while another posts to it.
	if _, err := normalizeChannels(KindHTTP, []Channel{{Key: "read", Path: "/heating/**", Methods: []string{"GET"}}, {Key: "set", Path: "/heating/set", Methods: []string{"POST"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestTargetsAndSealed(t *testing.T) {
	for kind, targets := range map[Kind][]string{
		KindMQTT: {"mqtt://192.168.1.20:1883", "mqtts://broker.example.com:8883", "wss://broker.example.com/mqtt", "ws://10.0.0.5:9001"},
		KindHTTP: {"http://192.168.1.20:1880/api", "https://flows.example.com"},
	} {
		for _, raw := range targets {
			if _, err := parseTarget(kind, raw); err != nil {
				t.Errorf("%s: %v", raw, err)
			}
		}
	}
	for kind, targets := range map[Kind][]string{
		KindMQTT: {"http://x:1", "mqtt://u:p@x:1883", "mqtt://x:1883/path", "mqtt://", "tcp://x:1883"},
		KindHTTP: {"ftp://x", "https://x/?q=1", "https://x/#f", "mqtt://x"},
	} {
		for _, raw := range targets {
			if _, err := parseTarget(kind, raw); err == nil {
				t.Errorf("%s accepted", raw)
			}
		}
	}
	ok := []struct {
		kind Kind
		s    sealed
	}{
		{KindMQTT, sealed{Credentials: Credentials{Username: "papergo", Password: "x"}}},
		{KindMQTT, sealed{}},
		{KindHTTP, sealed{Credentials: Credentials{Type: CredentialBearer, Token: "t"}, Headers: map[string]string{"X-Site": "home"}}},
		{KindHTTP, sealed{Credentials: Credentials{Type: CredentialHeader, Name: "X-Api-Key", Value: "k"}}},
	}
	for i, c := range ok {
		if err := checkSealed(c.kind, &c.s); err != nil {
			t.Errorf("%d: %v", i, err)
		}
	}
	bad := []struct {
		kind Kind
		s    sealed
	}{
		{KindMQTT, sealed{Credentials: Credentials{Type: CredentialBearer, Token: "t"}}},
		{KindMQTT, sealed{Headers: map[string]string{"X": "y"}}},
		{KindHTTP, sealed{Credentials: Credentials{Type: CredentialHeader, Name: "Cookie", Value: "k"}}},
		{KindHTTP, sealed{Headers: map[string]string{"PaperGo-Subject": "admin"}}},
		{KindHTTP, sealed{Headers: map[string]string{"X-Evil": "a\r\nb"}}},
		{KindHTTP, sealed{CA: "not a certificate"}},
	}
	for i, c := range bad {
		if err := checkSealed(c.kind, &c.s); err == nil {
			t.Errorf("bad %d accepted", i)
		}
	}
	s := Settings{}
	if err := checkSettings(KindHTTP, &s); err != nil || s.Timeout != "10s" {
		t.Fatal(s, err)
	}
	for _, s := range []Settings{{Timeout: "30s"}, {Timeout: "500ms"}, {HealthPath: "../x"}} {
		if checkSettings(KindHTTP, &s) == nil {
			t.Errorf("%+v accepted", s)
		}
	}
	if checkSettings(KindMQTT, &Settings{Sign: true}) == nil {
		t.Fatal("mqtt settings")
	}
}

// A changed trigger starts a run when a topic's value differs from the one
// that last started one, and a trigger remembers a bounded number of topics.
func TestTriggersRememberBoundedTopics(t *testing.T) {
	a := &armed{options: MessageTrigger{MinInterval: "1h", Condition: &MessageCondition{Op: OpChanged}}, fired: map[string]time.Time{}, last: map[string]any{}}
	now := time.Now()
	one := NewMessage("home/a", []byte(`1`), false, now)
	if !a.passes(one, now) || a.passes(one, now.Add(2*time.Hour)) {
		t.Fatal("a value starts one run")
	}
	two := NewMessage("home/a", []byte(`2`), false, now)
	if a.passes(two, now.Add(30*time.Minute)) || !a.passes(two, now.Add(3*time.Hour)) {
		t.Fatal("a change starts a run once the interval has passed")
	}
	above := &armed{options: MessageTrigger{Condition: &MessageCondition{Op: OpGt, Value: float64(0)}}, fired: map[string]time.Time{}, last: map[string]any{}}
	if !above.passes(one, now) || len(above.last) != 0 {
		t.Fatal("only changed keeps values")
	}
	for i := range armedTopics + 10 {
		if !a.passes(NewMessage(fmt.Sprintf("home/%d", i), []byte(`1`), false, now), now.Add(3*time.Hour)) {
			t.Fatalf("topic %d started no run", i)
		}
	}
	if len(a.fired) > armedTopics || len(a.last) > armedTopics {
		t.Fatalf("a trigger remembers %d topics and %d values", len(a.fired), len(a.last))
	}
}

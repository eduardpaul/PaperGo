package builtin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"papergo/extensions/mcp"
)

type subject string

func (s subject) Subject() string    { return string(s) }
func (subject) HasScope(string) bool { return true }
func (subject) Backend() mcp.Backend { return nil }

func paged(t *testing.T, tool string, who subject, args string) paging {
	t.Helper()
	p, err := newPaging(tool, who, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCursorRoundTrip(t *testing.T) {
	p := paged(t, "query_items", "alice", `{"collection_id": "c", "limit": 2, "query": {"filter": {"field": "n", "op": "eq", "value": 9007199254740993}}}`)
	query := url.Values{"$top": {"2"}}
	link := "https://public.example/v1/resources/c/query?%24top=2&%24skiptoken=abc"
	next, err := p.next(&link, "/v1/resources/c/query", query)
	if err != nil || next == nil {
		t.Fatalf("next %v %v", next, err)
	}
	// The same arguments in another order, with the cursor, continue.
	again := paged(t, "query_items", "alice", `{"query": {"filter": {"value": 9007199254740993, "op": "eq", "field": "n"}}, "limit": 2, "collection_id": "c", "cursor": "`+*next+`"}`)
	if token, err := again.resume(*next); err != nil || token != "abc" {
		t.Fatalf("resume %q %v", token, err)
	}
	if end, err := p.next(nil, "/v1/resources/c/query", query); end != nil || err != nil {
		t.Fatalf("last page %v %v", end, err)
	}
}

func TestCursorRefusesOtherCalls(t *testing.T) {
	p := paged(t, "query_items", "alice", `{"collection_id": "c"}`)
	link := "/v1/resources/c/query?%24top=50&%24skiptoken=abc"
	next, err := p.next(&link, "/v1/resources/c/query", url.Values{"$top": {"50"}})
	if err != nil {
		t.Fatal(err)
	}
	forged, _ := json.Marshal(cursor{Tool: "query_items", Caller: digest("alice"), Args: p.args})
	for name, other := range map[string]paging{
		"other tool":   paged(t, "search", "alice", `{"collection_id": "c"}`),
		"other caller": paged(t, "query_items", "bob", `{"collection_id": "c"}`),
		"other query":  paged(t, "query_items", "alice", `{"collection_id": "d"}`),
		"added limit":  paged(t, "query_items", "alice", `{"collection_id": "c", "limit": 50}`),
	} {
		if _, err := other.resume(*next); !invalid(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, raw := range []string{"%%%", base64.RawURLEncoding.EncodeToString([]byte("{")), base64.RawURLEncoding.EncodeToString(forged), strings.Repeat("a", maxCursorBytes+1)} {
		if _, err := p.resume(raw); !invalid(err) {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

func TestNextLinkMustContinueTheSameRequest(t *testing.T) {
	p := paged(t, "search", "alice", `{"query": "x"}`)
	query := url.Values{"q": {"x"}, "$top": {"10"}, "$skiptoken": {"old"}}
	for _, link := range []string{
		"https://evil.example/v1/admin?q=x&%24top=10&%24skiptoken=t",
		"/v1/search?q=y&%24top=10&%24skiptoken=t",
		"/v1/search?q=x&%24top=10&extra=1&%24skiptoken=t",
		"/v1/search?q=x&%24top=10",
		"/v1/search?q=x&%24top=10&%24skiptoken=t&%24skiptoken=u",
		"/v1/search/../admin?q=x&%24top=10&%24skiptoken=t",
		"::",
	} {
		if _, err := p.next(&link, "/v1/search", query); !errors.Is(err, mcp.ErrUnavailable) {
			t.Errorf("%s: %v", link, err)
		}
	}
	ok := "https://public.example/v1/search?%24skiptoken=t&%24top=10&q=x"
	if next, err := p.next(&ok, "/v1/search", query); err != nil || next == nil {
		t.Fatalf("%v %v", next, err)
	}
}

func invalid(err error) bool {
	var f *mcp.Failure
	return errors.As(err, &f) && f.Code == mcp.CodeInvalidArguments
}

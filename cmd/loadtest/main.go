// loadtest fills a PaperGo server with very large lists and libraries through
// the public API and measures how its reads and writes behave at that size.
//
//	loadtest seed    -url http://127.0.0.1:8080 -token TOKEN -items 500000 -state large.json
//	loadtest resume  -url http://127.0.0.1:8080 -token TOKEN -max-folders 400 -state large.json
//	loadtest measure -url http://127.0.0.1:8080 -token TOKEN -state large.json -out results.json
//
// Use a disposable database: seeding creates users, a workspace, a list and a
// library, and measuring changes and deletes some of their items.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: loadtest seed|resume|measure [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "seed":
		seed(os.Args[2:])
	case "resume":
		resume(os.Args[2:])
	case "measure":
		measure(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "usage: loadtest seed|resume|measure [flags]")
		os.Exit(2)
	}
}

// State records what seeding created, so measuring can find it.
type State struct {
	Workspace      string   `json:"workspace"`
	Suppliers      string   `json:"suppliers"`
	SupplierIDs    []string `json:"supplier_ids"`
	List           string   `json:"list"`
	Library        string   `json:"library"`
	TopFolders     []string `json:"top_folders"`
	SubFolders     []string `json:"sub_folders"`
	SampleItems    []string `json:"sample_items"`
	SampleDocs     []string `json:"sample_docs"`
	Users          []string `json:"users"`
	Items          int      `json:"items"`
	ListSeconds    float64  `json:"list_seconds"`
	LibrarySeconds float64  `json:"library_seconds"`
}

type client struct {
	base  string
	token string
	http  *http.Client
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }

func (c *client) do(method, path string, body any, ifMatch int, out any) (int, error) {
	var reader io.Reader
	if raw, ok := body.([]byte); ok {
		reader = bytes.NewReader(raw)
	} else if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(buf)
	}
	target := path
	if !strings.HasPrefix(path, "http") {
		target = c.base + path
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if _, ok := body.([]byte); ok {
		req.Header.Set("Content-Type", "application/pdf")
		req.Header.Set("X-Filename", "scan.pdf")
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifMatch > 0 {
		req.Header.Set("If-Match", fmt.Sprintf(`"%d"`, ifMatch))
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, err
	}
	if res.StatusCode >= 300 {
		return res.StatusCode, &apiError{res.StatusCode, strings.TrimSpace(string(raw))}
	}
	if out != nil && len(raw) > 0 {
		return res.StatusCode, json.Unmarshal(raw, out)
	}
	return res.StatusCode, nil
}

func (c *client) must(method, path string, body any, ifMatch int, out any) {
	if _, err := c.do(method, path, body, ifMatch, out); err != nil {
		log.Fatalf("%s %s: %v", method, path, err)
	}
}

type resource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Version int    `json:"version"`
}

type page struct {
	Value []resource `json:"value"`
	Next  *string    `json:"@odata.nextLink"`
	Total *int       `json:"total"`
}

func newClient(base, token string) *client {
	return &client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{
		Timeout:   10 * time.Minute,
		Transport: &http.Transport{MaxIdleConnsPerHost: 64},
	}}
}

var (
	companies = []string{"Acme", "Globex", "Initech", "Umbrella", "Hooli", "Stark", "Wayne", "Wonka", "Tyrell", "Cyberdyne", "Soylent", "Vandelay", "Pied", "Aperture", "Massive", "Oscorp"}
	trades    = []string{"Plumbing", "Logistics", "Consulting", "Catering", "Printing", "Security", "Cleaning", "Software", "Hardware", "Travel", "Legal", "Marketing"}
	months    = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	statuses  = []string{"Open", "Paid", "Overdue", "Disputed"}
	docTypes  = []string{"Contract", "Invoice", "Letter", "Report", "Receipt", "Statement"}
)

func seed(args []string) {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	base := fs.String("url", "http://127.0.0.1:8080", "PaperGo URL")
	token := fs.String("token", os.Getenv("PAPERGO_TOKEN"), "bearer token of an organization administrator")
	items := fs.Int("items", 500000, "items in the list and in the library each")
	suppliers := fs.Int("suppliers", 2000, "items in the looked-up suppliers list")
	topFolders := fs.Int("top-folders", 50, "top-level library folders")
	subFolders := fs.Int("sub-folders", 20, "folders inside each top-level folder")
	files := fs.Int("files", 200, "library items that get a small PDF")
	workers := fs.Int("workers", 4, "concurrent bulk requests")
	statePath := fs.String("state", "large.json", "where to write what was created")
	_ = fs.Parse(args)
	c := newClient(*base, *token)
	st := State{Items: *items}

	for i := range 20 {
		subject := fmt.Sprintf("load-user-%02d", i)
		if _, err := c.do("POST", "/v1/identity/users", map[string]string{"subject": subject, "display_name": fmt.Sprintf("Load User %02d", i), "email": subject + "@example.com"}, 0, nil); err != nil {
			var api *apiError
			if !errors.As(err, &api) || api.status != 409 {
				log.Fatal(err)
			}
		}
		st.Users = append(st.Users, subject)
	}

	var w resource
	c.must("POST", "/v1/workspaces", map[string]any{"name": fmt.Sprintf("Load test %d", time.Now().Unix()), "description": "Very large lists and libraries", "tags": []string{}}, 0, &w)
	st.Workspace = w.ID
	for _, user := range st.Users {
		c.must("POST", "/v1/workspaces/"+w.ID+"/members", map[string]string{"subject": user, "role": "member"}, 0, nil)
	}

	var sup resource
	c.must("POST", "/v1/resources/"+w.ID+"/children", map[string]any{"kind": "list", "name": "Suppliers", "tags": []string{}, "values": map[string]any{}}, 0, &sup)
	st.Suppliers = sup.ID
	c.must("POST", "/v1/resources/"+sup.ID+"/fields", map[string]any{"key": "city", "label": "City", "type": "text"}, 0, nil)
	st.SupplierIDs = bulkCreate(c, sup.ID, *suppliers, *workers, "", func(i int) map[string]any {
		return map[string]any{"name": fmt.Sprintf("%s %s %04d", companies[i%len(companies)], trades[i%len(trades)], i), "values": map[string]any{"city": months[i%len(months)] + " City"}}
	}, true)

	var list resource
	c.must("POST", "/v1/resources/"+w.ID+"/children", map[string]any{"kind": "list", "name": "Invoices", "description": fmt.Sprintf("%d invoices", *items), "tags": []string{}, "values": map[string]any{}}, 0, &list)
	st.List = list.ID
	for _, field := range []map[string]any{
		{"key": "amount", "label": "Amount", "type": "currency", "indexed": true, "options": map[string]any{"currency_code": "EUR"}},
		{"key": "status", "label": "Status", "type": "choice", "indexed": true, "choices": statuses},
		{"key": "due", "label": "Due date", "type": "date", "indexed": true},
		{"key": "owner", "label": "Owner", "type": "person", "indexed": true},
		{"key": "supplier", "label": "Supplier", "type": "lookup", "indexed": true, "options": map[string]any{"lookup_container_id": sup.ID}},
		{"key": "reference", "label": "Reference", "type": "text"},
	} {
		c.must("POST", "/v1/resources/"+list.ID+"/fields", field, 0, nil)
	}
	for _, view := range []map[string]any{
		{"name": "Open invoices", "layout": "table", "is_default": false, "columns": []string{"$name", "amount", "due", "supplier", "owner"}, "query": map[string]any{"filter": map[string]any{"field": "status", "op": "eq", "value": "Open"}, "sort": []map[string]string{{"field": "due", "direction": "asc"}}}},
		{"name": "By status", "layout": "board", "columns": []string{"$name", "amount", "status"}, "query": map[string]any{"group_by": "status"}},
		{"name": "Due calendar", "layout": "calendar", "columns": []string{"$name", "due"}, "query": map[string]any{"group_by": "due"}},
	} {
		c.must("POST", "/v1/resources/"+list.ID+"/views", view, 0, nil)
	}
	start := time.Now()
	ids := bulkCreate(c, list.ID, *items, *workers, "", func(i int) map[string]any {
		tags := []string{}
		if i%20 == 0 {
			tags = append(tags, "priority")
		}
		if i%1000 == 0 {
			tags = append(tags, "audit-2026")
		}
		return map[string]any{
			"name": fmt.Sprintf("Invoice INV-%07d %s %s %s", i, companies[i%len(companies)], trades[(i/7)%len(trades)], months[(i/31)%len(months)]),
			"tags": tags,
			"values": map[string]any{
				"amount":    float64((i*7919)%1000000) / 100,
				"status":    statuses[(i/3)%len(statuses)],
				"due":       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i%1095).Format("2006-01-02"),
				"owner":     st.Users[i%len(st.Users)],
				"supplier":  st.SupplierIDs[i%len(st.SupplierIDs)],
				"reference": fmt.Sprintf("PO-%06d", i/3),
			},
		}
	}, false)
	st.ListSeconds = time.Since(start).Seconds()
	st.SampleItems = ids

	var lib resource
	c.must("POST", "/v1/resources/"+w.ID+"/children", map[string]any{"kind": "library", "name": "Archive", "description": fmt.Sprintf("%d documents in folders", *items), "tags": []string{}, "values": map[string]any{}}, 0, &lib)
	st.Library = lib.ID
	for _, field := range []map[string]any{
		{"key": "doc_type", "label": "Document type", "type": "choice", "indexed": true, "choices": docTypes},
		{"key": "received", "label": "Received", "type": "date", "indexed": true},
	} {
		c.must("POST", "/v1/resources/"+lib.ID+"/fields", field, 0, nil)
	}
	for i := range *topFolders {
		var top resource
		c.must("POST", "/v1/resources/"+lib.ID+"/children", map[string]any{"kind": "folder", "name": fmt.Sprintf("%d %s", 2000+i/12, months[i%12]), "tags": []string{}, "values": map[string]any{}}, 0, &top)
		st.TopFolders = append(st.TopFolders, top.ID)
		for j := range *subFolders {
			var sub resource
			c.must("POST", "/v1/resources/"+top.ID+"/children", map[string]any{"kind": "folder", "name": fmt.Sprintf("%s %02d", companies[j%len(companies)], j), "tags": []string{}, "values": map[string]any{}}, 0, &sub)
			st.SubFolders = append(st.SubFolders, sub.ID)
		}
	}
	start = time.Now()
	fillLibrary(c, &st, *items/len(st.SubFolders), *workers, nil, len(st.SubFolders))
	st.LibrarySeconds = time.Since(start).Seconds()
	finishSeed(c, &st, *files, *statePath)
}

// libraryDocument is the n-th document of the library.
func libraryDocument(n int) map[string]any {
	return map[string]any{
		"name":   fmt.Sprintf("scan-%07d-%s-%s.pdf", n, strings.ToLower(companies[n%len(companies)]), strings.ToLower(docTypes[n%len(docTypes)])),
		"values": map[string]any{"doc_type": docTypes[n%len(docTypes)], "received": time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n%2190).Format("2006-01-02")},
	}
}

// fillLibrary tops every subfolder up to perFolder documents (existing counts
// what a folder already holds), at most maxFolders folders per call.
func fillLibrary(c *client, st *State, perFolder, workers int, existing map[string]int, maxFolders int) {
	start := time.Now()
	created := int64(0)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for f := range jobs {
				folder := st.SubFolders[f]
				have := existing[folder]
				first := f * perFolder
				if have > 0 {
					// Topping up after an interrupted seed: a separate range keeps names unique in the folder.
					first = 10_000_000 + f*perFolder
				}
				bulkCreate(c, st.Library, perFolder-have, 1, folder, func(i int) map[string]any { return libraryDocument(first + have + i) }, false)
				done := atomic.AddInt64(&created, int64(perFolder-have))
				log.Printf("library: folder %d done, %d documents this run, %.0f/s", f, done, float64(done)/time.Since(start).Seconds())
			}
		})
	}
	queued := 0
	for f, folder := range st.SubFolders {
		if existing[folder] >= perFolder {
			continue
		}
		if queued == maxFolders {
			break
		}
		jobs <- f
		queued++
	}
	close(jobs)
	wg.Wait()
}

// finishSeed gives the first document of the first folders a small PDF and writes the state.
func finishSeed(c *client, st *State, files int, statePath string) {
	pdf := []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[]/Count 0>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")
	st.SampleDocs = nil
	for i, folder := range st.SubFolders {
		if len(st.SampleDocs) == files || i == len(st.SubFolders) {
			break
		}
		var first page
		c.must("GET", "/v1/resources/"+folder+"/children?kind=item&$top=1", nil, 0, &first)
		if len(first.Value) == 0 {
			continue
		}
		doc := first.Value[0]
		var versions struct {
			Value []any `json:"value"`
		}
		c.must("GET", "/v1/items/"+doc.ID+"/content/versions", nil, 0, &versions)
		if len(versions.Value) == 0 {
			c.must("PUT", "/v1/items/"+doc.ID+"/content", append(append([]byte{}, pdf...), []byte(fmt.Sprintf("%% %d\n", i))...), doc.Version, nil)
		}
		st.SampleDocs = append(st.SampleDocs, doc.ID)
	}
	buf, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(statePath, buf, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("seeded: list %.0fs, library %.0fs", st.ListSeconds, st.LibrarySeconds)
}

// resume finds what an interrupted seed created and fills the library's missing documents.
func resume(args []string) {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	base := fs.String("url", "http://127.0.0.1:8080", "PaperGo URL")
	token := fs.String("token", os.Getenv("PAPERGO_TOKEN"), "bearer token of an organization administrator")
	items := fs.Int("items", 500000, "documents the library should hold")
	files := fs.Int("files", 200, "library items that get a small PDF")
	workers := fs.Int("workers", 2, "concurrent bulk requests")
	maxFolders := fs.Int("max-folders", 1000000, "subfolders to fill in this run; run again to continue")
	statePath := fs.String("state", "large.json", "where to write what was found")
	_ = fs.Parse(args)
	c := newClient(*base, *token)
	all := func(path string) []resource {
		out := []resource{}
		next := path
		for next != "" {
			var p page
			c.must("GET", next, nil, 0, &p)
			out = append(out, p.Value...)
			next = ""
			if p.Next != nil {
				u, _ := url.Parse(*p.Next)
				next = u.RequestURI()
			}
		}
		return out
	}
	st := State{Items: *items}
	for _, w := range all("/v1/workspaces?$top=200") {
		if strings.HasPrefix(w.Name, "Load test") {
			st.Workspace = w.ID
		}
	}
	if st.Workspace == "" {
		log.Fatal("no Load test workspace")
	}
	for _, r := range all("/v1/resources/" + st.Workspace + "/children?$top=200") {
		switch r.Name {
		case "Suppliers":
			st.Suppliers = r.ID
		case "Invoices":
			st.List = r.ID
		case "Archive":
			st.Library = r.ID
		}
	}
	for _, r := range all("/v1/resources/" + st.Suppliers + "/children?$top=500") {
		st.SupplierIDs = append(st.SupplierIDs, r.ID)
	}
	var sample page
	c.must("GET", "/v1/resources/"+st.List+"/children?$top=500", nil, 0, &sample)
	for _, r := range sample.Value {
		st.SampleItems = append(st.SampleItems, r.ID)
	}
	var members []struct {
		Subject string `json:"subject"`
	}
	var wrapped struct {
		Value []struct {
			Subject string `json:"subject"`
		} `json:"value"`
	}
	c.must("GET", "/v1/workspaces/"+st.Workspace+"/members", nil, 0, &wrapped)
	members = wrapped.Value
	for _, m := range members {
		if strings.HasPrefix(m.Subject, "load-user-") {
			st.Users = append(st.Users, m.Subject)
		}
	}
	existing := map[string]int{}
	for _, top := range all("/v1/resources/" + st.Library + "/children?kind=folder&$top=500") {
		st.TopFolders = append(st.TopFolders, top.ID)
		for _, sub := range all("/v1/resources/" + top.ID + "/children?kind=folder&$top=500") {
			st.SubFolders = append(st.SubFolders, sub.ID)
			var count page
			c.must("POST", "/v1/resources/"+st.Library+"/query?$top=1", map[string]any{"include_total": true, "query": map[string]any{"parent_id": sub.ID}}, 0, &count)
			if count.Total != nil {
				existing[sub.ID] = *count.Total
			}
		}
	}
	perFolder := *items / len(st.SubFolders)
	missing := 0
	for _, sub := range st.SubFolders {
		missing += max(0, perFolder-existing[sub])
	}
	log.Printf("found %d subfolders; %d documents missing", len(st.SubFolders), missing)
	start := time.Now()
	fillLibrary(c, &st, perFolder, *workers, existing, *maxFolders)
	st.LibrarySeconds = time.Since(start).Seconds()
	finishSeed(c, &st, *files, *statePath)
}

// bulkCreate creates n items in batches of 100 and returns the IDs (all of them
// when keep is set, otherwise a sample of up to 1000).
func bulkCreate(c *client, collection string, n, workers int, parent string, item func(int) map[string]any, keep bool) []string {
	type batch struct{ from, to int }
	jobs := make(chan batch)
	var mu sync.Mutex
	ids := make([]string, 0)
	start := time.Now()
	var done int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for b := range jobs {
				ops := make([]map[string]any, 0, b.to-b.from)
				for i := b.from; i < b.to; i++ {
					op := map[string]any{"action": "create", "create": item(i)}
					if parent != "" {
						op["parent_id"] = parent
					}
					ops = append(ops, op)
				}
				var out struct {
					Data []struct{ ID string } `json:"data"`
				}
				for attempt := 0; ; attempt++ {
					_, err := c.do("POST", "/v1/resources/"+collection+"/bulk", map[string]any{"operations": ops}, 0, &out)
					if err == nil {
						break
					}
					var api *apiError
					if attempt < 5 && (!errors.As(err, &api) || api.status == 503) {
						time.Sleep(time.Duration(attempt+1) * time.Second)
						continue
					}
					log.Fatalf("bulk create: %v", err)
				}
				mu.Lock()
				for _, r := range out.Data {
					if keep || len(ids) < 1000 {
						ids = append(ids, r.ID)
					}
				}
				mu.Unlock()
				total := atomic.AddInt64(&done, int64(len(out.Data)))
				if workers > 1 && total%20000 < int64(len(out.Data)) {
					log.Printf("%s: %d items, %.0f/s", collection[:8], total, float64(total)/time.Since(start).Seconds())
				}
			}
		})
	}
	for from := 0; from < n; from += 100 {
		jobs <- batch{from, min(from+100, n)}
	}
	close(jobs)
	wg.Wait()
	return ids
}

// Result is one measured request: its median and slowest time over the runs.
type Result struct {
	Name     string  `json:"name"`
	Runs     int     `json:"runs"`
	MedianMS float64 `json:"median_ms"`
	MaxMS    float64 `json:"max_ms"`
	Status   int     `json:"status"`
	Note     string  `json:"note,omitempty"`
}

func measure(args []string) {
	fs := flag.NewFlagSet("measure", flag.ExitOnError)
	base := fs.String("url", "http://127.0.0.1:8080", "PaperGo URL")
	token := fs.String("token", os.Getenv("PAPERGO_TOKEN"), "bearer token")
	statePath := fs.String("state", "large.json", "state written by seed")
	out := fs.String("out", "results.json", "where to write the results")
	runs := fs.Int("runs", 5, "repetitions of each read")
	_ = fs.Parse(args)
	raw, err := os.ReadFile(*statePath)
	if err != nil {
		log.Fatal(err)
	}
	var st State
	if err = json.Unmarshal(raw, &st); err != nil {
		log.Fatal(err)
	}
	c := newClient(*base, *token)
	results := []Result{}
	record := func(name string, n int, fn func() (int, string, error)) {
		times := []float64{}
		status, note := 0, ""
		for range n {
			start := time.Now()
			s, info, err := fn()
			times = append(times, float64(time.Since(start).Microseconds())/1000)
			status, note = s, info
			if err != nil {
				note = err.Error()
				if len(note) > 160 {
					note = note[:160]
				}
				break
			}
		}
		sort.Float64s(times)
		r := Result{Name: name, Runs: len(times), MedianMS: times[len(times)/2], MaxMS: times[len(times)-1], Status: status, Note: note}
		log.Printf("%-60s %8.1f ms (max %8.1f) %d %s", name, r.MedianMS, r.MaxMS, status, note)
		results = append(results, r)
	}
	get := func(path string) func() (int, string, error) {
		return func() (int, string, error) {
			var p page
			s, err := c.do("GET", path, nil, 0, &p)
			return s, fmt.Sprintf("%d rows", len(p.Value)), err
		}
	}
	query := func(path string, body map[string]any) func() (int, string, error) {
		return func() (int, string, error) {
			var p page
			s, err := c.do("POST", path, body, 0, &p)
			note := fmt.Sprintf("%d rows", len(p.Value))
			if p.Total != nil {
				note += fmt.Sprintf(", total %d", *p.Total)
			}
			return s, note, err
		}
	}
	l, lib := st.List, st.Library

	record("workspaces", *runs, get("/v1/workspaces"))
	record("workspace lists and libraries", *runs, get("/v1/resources/"+st.Workspace+"/children?$top=200"))
	record("list resource with access", *runs, get("/v1/resources/"+l))
	record("list fields", *runs, get("/v1/resources/"+l+"/fields"))
	record("list views", *runs, get("/v1/resources/"+l+"/views"))
	record("list: first page of children", *runs, get("/v1/resources/"+l+"/children?$top=50"))
	record("list: 100th page (keyset)", 1, func() (int, string, error) {
		next := "/v1/resources/" + l + "/children?$top=50"
		var p page
		for range 100 {
			if _, err := c.do("GET", next, nil, 0, &p); err != nil || p.Next == nil {
				return 0, "", err
			}
			u, _ := url.Parse(*p.Next)
			next = u.RequestURI()
		}
		return 200, "100 pages walked", nil
	})
	record("list: query first page", *runs, query("/v1/resources/"+l+"/query?$top=50", map[string]any{"query": map[string]any{"parent_id": l}}))
	record("list: query with total", *runs, query("/v1/resources/"+l+"/query?$top=50", map[string]any{"include_total": true, "query": map[string]any{"parent_id": l}}))
	for _, s := range []struct{ field, dir string }{{"$name", "asc"}, {"$name", "desc"}, {"amount", "desc"}, {"due", "asc"}, {"$modified_at", "desc"}, {"owner", "asc"}} {
		record(fmt.Sprintf("list: sort %s %s", s.field, s.dir), *runs, query("/v1/resources/"+l+"/query?$top=50", map[string]any{"query": map[string]any{"parent_id": l, "sort": []map[string]string{{"field": s.field, "direction": s.dir}}}}))
	}
	record("list: sort amount desc with total", *runs, query("/v1/resources/"+l+"/query?$top=50", map[string]any{"include_total": true, "query": map[string]any{"parent_id": l, "sort": []map[string]string{{"field": "amount", "direction": "desc"}}}}))
	record("list: filter status=Open sort due (view)", *runs, query("/v1/resources/"+l+"/query?$top=50", map[string]any{"include_total": true, "select": []string{"$name", "amount", "due", "supplier", "owner"}, "query": map[string]any{"filter": map[string]any{"field": "status", "op": "eq", "value": "Open"}, "sort": []map[string]string{{"field": "due", "direction": "asc"}}}}))
	record("list: filter amount >= 9990 (rare)", *runs, query("/v1/resources/"+l+"/query?$top=50", map[string]any{"include_total": true, "query": map[string]any{"filter": map[string]any{"field": "amount", "op": "gte", "value": 9990}}}))
	record("list: board counts group_by status", *runs, func() (int, string, error) {
		var p struct {
			Value []struct {
				Count int `json:"count"`
			} `json:"value"`
		}
		s, err := c.do("POST", "/v1/resources/"+l+"/query/groups", map[string]any{"query": map[string]any{"group_by": "status"}}, 0, &p)
		total := 0
		for _, g := range p.Value {
			total += g.Count
		}
		return s, fmt.Sprintf("%d groups, %d items", len(p.Value), total), err
	})
	record("list: calendar month (due in March 2025)", *runs, query("/v1/resources/"+l+"/query?$top=500", map[string]any{"query": map[string]any{"filter": map[string]any{"and": []map[string]any{{"field": "due", "op": "gte", "value": "2025-03-01"}, {"field": "due", "op": "lt", "value": "2025-04-01"}}}, "sort": []map[string]string{{"field": "due", "direction": "asc"}}}}))
	record("list: search common word (Acme)", *runs, get("/v1/resources/"+l+"/children?q=Acme&$top=50"))
	record("list: search rare word (INV-0123456)", *runs, get("/v1/resources/"+l+"/children?q=INV-0123456&$top=50"))
	record("list: search no match (zzzz)", *runs, get("/v1/resources/"+l+"/children?q=zzzz&$top=50"))
	record("list: search prefix (Acm)", *runs, get("/v1/resources/"+l+"/children?q=Acm&$top=50"))
	record("list: search Acme sorted by amount", *runs, query("/v1/resources/"+l+"/query?$top=50", map[string]any{"query": map[string]any{"parent_id": l, "search": "Acme", "sort": []map[string]string{{"field": "amount", "direction": "desc"}}}}))
	record("list: tag filter (audit-2026, 500 items)", *runs, get("/v1/resources/"+l+"/children?tag=audit-2026&$top=50"))
	record("workspace tags vocabulary", *runs, get("/v1/workspaces/"+st.Workspace+"/tags"))
	record("workspace search (Acme)", *runs, get("/v1/resources?workspace_id="+st.Workspace+"&q=Acme&$top=50"))
	record("item read", *runs, get("/v1/resources/"+st.SampleItems[0]))
	record("item revisions", *runs, get("/v1/items/"+st.SampleItems[0]+"/revisions"))
	record("lookup list first page (suppliers)", *runs, get("/v1/resources/"+st.Suppliers+"/children?$top=50"))
	record("library: root children", *runs, get("/v1/resources/"+lib+"/children?$top=50"))
	record("library: root folders only", *runs, get("/v1/resources/"+lib+"/children?kind=folder&$top=100"))
	record("library: subfolder (500 docs) first page", *runs, get("/v1/resources/"+st.SubFolders[0]+"/children?$top=50"))
	record("library: all documents query with total", *runs, query("/v1/resources/"+lib+"/query?$top=50", map[string]any{"include_total": true, "query": map[string]any{}}))
	record("library: filter doc_type=Contract sort received", *runs, query("/v1/resources/"+lib+"/query?$top=50", map[string]any{"include_total": true, "query": map[string]any{"filter": map[string]any{"field": "doc_type", "op": "eq", "value": "Contract"}, "sort": []map[string]string{{"field": "received", "direction": "desc"}}}}))
	record("library: search (acme)", *runs, get("/v1/resources/"+lib+"/children?q=acme&$top=50"))
	record("library: file versions", *runs, get("/v1/items/"+st.SampleDocs[0]+"/content/versions"))
	record("library: download", *runs, func() (int, string, error) {
		s, err := c.do("GET", "/v1/items/"+st.SampleDocs[0]+"/content", nil, 0, nil)
		return s, "", err
	})

	// Writes.
	record("item update (PATCH)", 1, func() (int, string, error) {
		var r resource
		if _, err := c.do("GET", "/v1/resources/"+st.SampleItems[1], nil, 0, &r); err != nil {
			return 0, "", err
		}
		s, err := c.do("PATCH", "/v1/resources/"+st.SampleItems[1], map[string]any{"values": map[string]any{"status": "Paid"}}, r.Version, nil)
		return s, "", err
	})
	record("item create in 500k list", 1, func() (int, string, error) {
		s, err := c.do("POST", "/v1/resources/"+l+"/children", map[string]any{"kind": "item", "name": "Fresh invoice", "tags": []string{}, "values": map[string]any{"status": "Open"}}, 0, nil)
		return s, "", err
	})
	record("bulk field update of 1000 selected items (operation)", 1, func() (int, string, error) {
		ids := st.SampleItems[:min(1000, len(st.SampleItems))]
		var op struct {
			ID string `json:"id"`
		}
		s, err := c.do("POST", "/v1/resources/"+l+"/bulk-update", map[string]any{"query": map[string]any{"filter": map[string]any{"field": "$id", "op": "in", "value": ids}}, "values": map[string]any{"status": "Disputed"}}, 0, &op)
		if err != nil {
			return s, "", err
		}
		return waitOperation(c, op.ID)
	})
	record("make a column filterable on 500k items (index build)", 1, func() (int, string, error) {
		var field struct {
			ID string `json:"id"`
		}
		var list resource
		if _, err := c.do("GET", "/v1/resources/"+l, nil, 0, &list); err != nil {
			return 0, "", err
		}
		var fields []struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		}
		var wrapped struct {
			Value []struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"value"`
		}
		if _, err := c.do("GET", "/v1/resources/"+l+"/fields", nil, 0, &wrapped); err != nil {
			return 0, "", err
		}
		fields = wrapped.Value
		for _, f := range fields {
			if f.Key == "reference" {
				field.ID = f.ID
			}
		}
		s, err := c.do("PATCH", "/v1/resources/"+l+"/fields/"+field.ID, map[string]any{"indexed": true}, list.Version, nil)
		if err != nil {
			return s, "", err
		}
		start := time.Now()
		for time.Since(start) < 30*time.Minute {
			var status struct {
				Value []struct {
					Key         string `json:"key"`
					IndexStatus string `json:"index_status"`
				} `json:"value"`
			}
			if _, err = c.do("GET", "/v1/resources/"+l+"/fields", nil, 0, &status); err != nil {
				return 0, "", err
			}
			for _, f := range status.Value {
				if f.Key == "reference" && f.IndexStatus == "ready" {
					return 200, "ready", nil
				}
			}
			time.Sleep(2 * time.Second)
		}
		return 0, "timed out", errors.New("index build took over 30 minutes")
	})
	record("delete folder with 500 documents (recursive)", 1, func() (int, string, error) {
		var f resource
		if _, err := c.do("GET", "/v1/resources/"+st.SubFolders[len(st.SubFolders)-1], nil, 0, &f); err != nil {
			return 0, "", err
		}
		s, err := c.do("DELETE", "/v1/resources/"+f.ID+"?recursive=true", nil, f.Version, nil)
		return s, "", err
	})
	record("recycle bin first page", *runs, get("/v1/resources/"+lib+"/recycle-bin"))
	record("restore folder with 500 documents", 1, func() (int, string, error) {
		s, err := c.do("POST", "/v1/resources/"+lib+"/recycle-bin/"+st.SubFolders[len(st.SubFolders)-1]+"/restore?recursive=true", nil, 0, nil)
		return s, "", err
	})
	record("delete top folder with 10000 documents (recursive)", 1, func() (int, string, error) {
		var f resource
		if _, err := c.do("GET", "/v1/resources/"+st.TopFolders[len(st.TopFolders)-1], nil, 0, &f); err != nil {
			return 0, "", err
		}
		s, err := c.do("DELETE", "/v1/resources/"+f.ID+"?recursive=true", nil, f.Version, nil)
		return s, "", err
	})
	record("reads while nothing else runs: first page", *runs, get("/v1/resources/"+l+"/children?$top=50"))

	buf, _ := json.MarshalIndent(results, "", "  ")
	if err = os.WriteFile(*out, buf, 0o644); err != nil {
		log.Fatal(err)
	}
}

func waitOperation(c *client, id string) (int, string, error) {
	start := time.Now()
	for time.Since(start) < 30*time.Minute {
		var op struct {
			Status  string `json:"status"`
			Updated int    `json:"updated"`
			Failed  int    `json:"failed"`
		}
		s, err := c.do("GET", "/v1/operations/"+id, nil, 0, &op)
		if err != nil {
			return s, "", err
		}
		if op.Status != "pending" {
			return s, fmt.Sprintf("%s, %d updated, %d failed", op.Status, op.Updated, op.Failed), nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0, "", errors.New("operation did not finish in 30 minutes")
}

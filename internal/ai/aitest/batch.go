package aitest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"papergo/internal/ai"
)

// BatchServer is a fake OpenAI-compatible batch endpoint. A create that is
// not told to fail stores the batch and, unless holds remain, reports it
// completed on the first read. Output lines are returned last-first.
type BatchServer struct {
	*httptest.Server
	mu           sync.Mutex
	files        map[string][]byte
	batches      map[string]*storedBatch
	order        []string
	failCreates  int
	landFailures bool
	failStatus   int
	holds        int
	next         int
	receiptMode  string
	seen         []batchCall
}

type storedBatch struct {
	ID         string
	Status     string
	InputFile  string
	OutputFile string
	ErrorFile  string
	Tag        string
	Held       bool
}

type batchCall struct {
	Method      string
	Path        string
	ContentType string
	Body        []byte
}

// NewBatchServer starts a fake batch endpoint, closed when the test ends.
func NewBatchServer(t testing.TB) *BatchServer {
	s := &BatchServer{files: map[string][]byte{}, batches: map[string]*storedBatch{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Endpoint is the OpenAI-style base (…/v1).
func (s *BatchServer) Endpoint() string { return s.URL + "/v1" }

// AzureEndpoint is an Azure-style base (…/openai/v1).
func (s *BatchServer) AzureEndpoint() string { return s.URL + "/openai/v1" }

// FailCreates makes the next n creates answer with HTTP 500. land stores the
// batch anyway, so a later list finds it.
func (s *BatchServer) FailCreates(n int, land bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCreates, s.landFailures, s.failStatus = n, land, 0
}

// RejectNextCreate makes the next create a definite HTTP 400.
func (s *BatchServer) RejectNextCreate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCreates, s.landFailures, s.failStatus = 1, false, http.StatusBadRequest
}

// SetReceiptMode chooses the answer for a request whose schema has store and
// lines. "" and "ok" return a receipt. "reject" returns an object the schema
// refuses. "bad-amount" matches the schema with a line amount past scale 2.
// Other structured requests still return {"total":1.25}.
func (s *BatchServer) SetReceiptMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receiptMode = mode
}

// Hold makes the next n batch reads report a non-terminal status.
func (s *BatchServer) Hold(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holds = n
}

// Creates counts successful and failed create calls.
func (s *BatchServer) Creates() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, call := range s.seen {
		if call.Method == http.MethodPost && strings.HasSuffix(call.Path, "/batches") {
			n++
		}
	}
	return n
}

// Uploads returns the JSONL bodies accepted so far, in order.
func (s *BatchServer) Uploads() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]byte
	for _, id := range s.order {
		out = append(out, append([]byte(nil), s.files[id]...))
	}
	return out
}

// Calls returns the recorded requests.
func (s *BatchServer) Calls() []batchCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]batchCall(nil), s.seen...)
}

func (s *BatchServer) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	path := r.URL.Path
	s.mu.Lock()
	s.seen = append(s.seen, batchCall{Method: r.Method, Path: path, ContentType: r.Header.Get("Content-Type"), Body: body})
	s.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/files"):
		s.upload(w, r, body)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/batches"):
		s.create(w, body)
	case r.Method == http.MethodGet && strings.Contains(path, "/files/") && strings.HasSuffix(path, "/content"):
		s.download(w, path)
	case r.Method == http.MethodGet && strings.Contains(path, "/batches/"):
		s.get(w, path)
	case r.Method == http.MethodGet && strings.HasSuffix(strings.TrimRight(path, "/"), "/batches"):
		s.list(w)
	default:
		http.NotFound(w, r)
	}
}

func (s *BatchServer) upload(w http.ResponseWriter, r *http.Request, raw []byte) {
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.FormValue("purpose") != "batch" {
		http.Error(w, "purpose", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	id := "file-" + itoa(s.next)
	s.files[id] = payload
	s.order = append(s.order, id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "purpose": "batch"})
}

func (s *BatchServer) create(w http.ResponseWriter, raw []byte) {
	var in struct {
		InputFileID string            `json:"input_file_id"`
		Endpoint    string            `json:"endpoint"`
		Window      string            `json:"completion_window"`
		Metadata    map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.InputFileID == "" || in.Endpoint != "/v1/chat/completions" || in.Window != "24h" {
		http.Error(w, `{"error":{"message":"bad batch"}}`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failCreates > 0 {
		s.failCreates--
		status := http.StatusInternalServerError
		if s.failStatus != 0 {
			status = s.failStatus
		}
		if s.landFailures && status >= 500 {
			s.storeLocked(in.InputFileID, in.Metadata[ai.MetadataKey])
		}
		http.Error(w, `{"error":{"message":"unavailable"}}`, status)
		return
	}
	batch := s.storeLocked(in.InputFileID, in.Metadata[ai.MetadataKey])
	writeJSON(w, http.StatusOK, batchJSON(batch))
}

func (s *BatchServer) storeLocked(inputFile, tag string) *storedBatch {
	s.next++
	id := "batch-" + itoa(s.next)
	output, errorsFile := s.outputLocked(inputFile)
	batch := &storedBatch{ID: id, Status: "completed", InputFile: inputFile, Tag: tag}
	if len(output) > 0 {
		file := "file-out-" + id
		s.files[file] = output
		batch.OutputFile = file
	}
	if len(errorsFile) > 0 {
		file := "file-err-" + id
		s.files[file] = errorsFile
		batch.ErrorFile = file
	}
	if s.holds > 0 {
		s.holds--
		batch.Status = "in_progress"
		batch.Held = true
	}
	s.batches[id] = batch
	return batch
}

func (s *BatchServer) outputLocked(inputFile string) (output, errorsFile []byte) {
	var success, failed bytes.Buffer
	for _, row := range bytes.Split(s.files[inputFile], []byte("\n")) {
		row = bytes.TrimSpace(row)
		if len(row) == 0 {
			continue
		}
		var line struct {
			CustomID string         `json:"custom_id"`
			Body     map[string]any `json:"body"`
		}
		if json.Unmarshal(row, &line) != nil || line.CustomID == "" {
			continue
		}
		text := userText(line.Body)
		switch {
		case strings.Contains(text, "fail-line"):
			failed.Write(mustJSON(map[string]any{"custom_id": line.CustomID, "error": map[string]string{"message": "provider failed this line"}}))
			failed.WriteByte('\n')
		case strings.Contains(text, "empty-line"):
			success.Write(completionLine(line.CustomID, ""))
		case strings.Contains(text, "reject-schema"):
			success.Write(completionLine(line.CustomID, `{"total":"invalid"}`))
		case receiptSchema(line.Body):
			success.Write(completionLine(line.CustomID, s.receiptTextLocked()))
		case line.Body["response_format"] != nil:
			success.Write(completionLine(line.CustomID, `{"total":1.25}`))
		default:
			success.Write(completionLine(line.CustomID, "answer:"+text))
		}
	}
	lines := bytes.Split(bytes.TrimSpace(success.Bytes()), []byte("\n"))
	if len(lines) == 1 && len(lines[0]) == 0 {
		lines = nil
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	if len(lines) > 0 {
		output = append(bytes.Join(lines, []byte("\n")), '\n')
	}
	return output, failed.Bytes()
}

func (s *BatchServer) get(w http.ResponseWriter, path string) {
	id := path[strings.LastIndex(path, "/")+1:]
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := s.batches[id]
	if batch == nil {
		http.Error(w, "missing batch", http.StatusNotFound)
		return
	}
	if batch.Held {
		batch.Held = false
		batch.Status = "completed"
		writeJSON(w, http.StatusOK, map[string]any{"id": batch.ID, "status": "in_progress"})
		return
	}
	writeJSON(w, http.StatusOK, batchJSON(batch))
}

func (s *BatchServer) list(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var data []any
	for _, batch := range s.batches {
		data = append(data, batchJSON(batch))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "has_more": false})
}

func (s *BatchServer) download(w http.ResponseWriter, path string) {
	id := strings.TrimSuffix(path, "/content")
	id = id[strings.LastIndex(id, "/")+1:]
	s.mu.Lock()
	payload, ok := s.files[id]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "missing file", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func batchJSON(batch *storedBatch) map[string]any {
	out := map[string]any{"id": batch.ID, "status": batch.Status, "metadata": map[string]string{ai.MetadataKey: batch.Tag}}
	if batch.OutputFile != "" && batch.Status == "completed" {
		out["output_file_id"] = batch.OutputFile
	}
	if batch.ErrorFile != "" && batch.Status == "completed" {
		out["error_file_id"] = batch.ErrorFile
	}
	return out
}

func completionLine(id, content string) []byte {
	return append(mustJSON(map[string]any{
		"custom_id": id,
		"response": map[string]any{"status_code": 200, "body": map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": "stop"}},
		}},
	}), '\n')
}

func receiptSchema(body map[string]any) bool {
	format, _ := body["response_format"].(map[string]any)
	schemaDoc, _ := format["json_schema"].(map[string]any)
	schema, _ := schemaDoc["schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	_, store := props["store"]
	_, lines := props["lines"]
	return store && lines
}

func (s *BatchServer) receiptTextLocked() string {
	switch s.receiptMode {
	case "reject":
		return `{"store":1}`
	case "bad-amount":
		return `{"store":"North Market","purchase_date":"2026-10-15","currency":"EUR","total":"12.50","lines":[{"description":"Plums","quantity":"2","unit_price":"4.00","amount":"1.999"}]}`
	default:
		return `{"store":"North Market","purchase_date":"2026-10-15","currency":"EUR","total":"12.50","lines":[{"description":"Plums","quantity":"2","unit_price":"4.00","amount":"8.00"},{"description":"Bread","quantity":"1","unit_price":"4.50","amount":"4.50"}]}`
	}
}

func userText(body map[string]any) string {
	messages, _ := body["messages"].([]any)
	if len(messages) < 2 {
		return ""
	}
	message, _ := messages[1].(map[string]any)
	switch content := message["content"].(type) {
	case string:
		return content
	default:
		raw, _ := json.Marshal(content)
		return string(raw)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

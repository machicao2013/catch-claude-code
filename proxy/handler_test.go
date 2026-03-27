package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"claude-spy/display"
	"claude-spy/recorder"
)

type memRecorder struct {
	records []recorder.Record
}

func (m *memRecorder) Write(r recorder.Record) error { m.records = append(m.records, r); return nil }
func (m *memRecorder) Close() error                  { return nil }
func (m *memRecorder) FilePath() string              { return "test.jsonl" }

func TestHandler_NonMessagesPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	rec := &memRecorder{}
	printer := display.NewPrinter(io.Discard, true)
	summary := display.NewSummary()
	h := NewHandler(upstream.URL, rec, printer, summary, false, nil)
	req := httptest.NewRequest("GET", "/v1/models", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if len(rec.records) != 0 {
		t.Errorf("should not record non-messages requests, got %d records", len(rec.records))
	}
}

func TestHandler_MessagesNonStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)
		if req["model"] != "claude-test" {
			t.Errorf("upstream got model = %v", req["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		resp := `{"id":"msg_01","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`
		w.Write([]byte(resp))
	}))
	defer upstream.Close()

	rec := &memRecorder{}
	printer := display.NewPrinter(io.Discard, true)
	summary := display.NewSummary()
	h := NewHandler(upstream.URL, rec, printer, summary, false, nil)
	body := `{"model":"claude-test","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if len(rec.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(rec.records))
	}
	if rec.records[0].Request.Method != "POST" {
		t.Errorf("recorded method = %q", rec.records[0].Request.Method)
	}
}

package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

type mockPusher struct {
	pushed []recorder.Record
}

func (m *mockPusher) Push(rec recorder.Record) {
	m.pushed = append(m.pushed, rec)
}

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

func TestHandler_WebUIPusherCalled(t *testing.T) {
	// 创建一个简单的上游服务，返回固定 JSON 响应
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer upstream.Close()

	rec := &memRecorder{}
	printer := display.NewPrinter(io.Discard, true)
	summary := display.NewSummary()
	pusher := &mockPusher{}

	handler := NewHandler(upstream.URL, rec, printer, summary, false, pusher)

	// 发送一个 /v1/messages 请求
	body := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// 验证 Push 被调用了一次
	if len(pusher.pushed) != 1 {
		t.Fatalf("expected 1 Push call, got %d", len(pusher.pushed))
	}
	if pusher.pushed[0].ID == "" {
		t.Error("pushed record has empty ID")
	}
}

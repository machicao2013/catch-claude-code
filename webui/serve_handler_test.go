package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHandleFiles_ReturnsJSON(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "20240101_120000_aaaa.jsonl")
	f2 := filepath.Join(dir, "20240102_130000_bbbb.jsonl")
	os.WriteFile(f1, []byte(`{}`), 0644)
	os.WriteFile(f2, []byte(`{}`), 0644)
	t1 := time.Now().Add(-2 * time.Second)
	t2 := time.Now()
	os.Chtimes(f1, t1, t1)
	os.Chtimes(f2, t2, t2)

	s := &Server{mode: ModeServe, logDir: dir, subs: make(map[chan []byte]struct{})}

	req := httptest.NewRequest("GET", "/api/files", nil)
	w := httptest.NewRecorder()
	s.handleFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var files []FileInfo
	if err := json.NewDecoder(w.Body).Decode(&files); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Filename != "20240102_130000_bbbb.jsonl" {
		t.Errorf("expected newest first, got %s", files[0].Filename)
	}
}

func TestHandleFiles_LimitTo100(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 150; i++ {
		name := filepath.Join(dir, fmt.Sprintf("2024010%d_120000_%04d.jsonl", i%9+1, i))
		os.WriteFile(name, []byte(`{}`), 0644)
	}
	s := &Server{mode: ModeServe, logDir: dir, subs: make(map[chan []byte]struct{})}
	req := httptest.NewRequest("GET", "/api/files", nil)
	w := httptest.NewRecorder()
	s.handleFiles(w, req)

	var files []FileInfo
	if err := json.NewDecoder(w.Body).Decode(&files); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(files) != 100 {
		t.Errorf("expected 100 files (cap), got %d", len(files))
	}
}

func TestHandleServeInfo(t *testing.T) {
	s := &Server{mode: ModeServe, logDir: "/tmp", subs: make(map[chan []byte]struct{})}
	req := httptest.NewRequest("GET", "/api/info?file=test.jsonl", nil)
	w := httptest.NewRecorder()
	s.handleServeInfo(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var info InfoResponse
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if info.Mode != "view" {
		t.Errorf("expected mode=view, got %s", info.Mode)
	}
	if info.Filename != "test.jsonl" {
		t.Errorf("expected filename=test.jsonl, got %s", info.Filename)
	}
}

func TestHandleServeRecords_ReturnsSummaries(t *testing.T) {
	dir := t.TempDir()
	// 写一条合法 JSONL 记录（最简结构）
	rec := `{"id":"req_001","timestamp":"2024-01-01T00:00:00Z","duration_ms":100,"request":{"body":{"model":"claude-3-opus","messages":[]}},"response":{"body":{"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}}}`
	os.WriteFile(filepath.Join(dir, "test.jsonl"), []byte(rec+"\n"), 0644)

	s := &Server{mode: ModeServe, logDir: dir, subs: make(map[chan []byte]struct{})}
	req := httptest.NewRequest("GET", "/api/records?file=test.jsonl", nil)
	w := httptest.NewRecorder()
	s.handleServeRecords(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var summaries []RecordSummary
	if err := json.NewDecoder(w.Body).Decode(&summaries); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].ID != "req_001" {
		t.Errorf("expected id=req_001, got %s", summaries[0].ID)
	}
	if summaries[0].Model != "claude-3-opus" {
		t.Errorf("expected model=claude-3-opus, got %s", summaries[0].Model)
	}
	if summaries[0].InTokens != 10 {
		t.Errorf("expected in_tokens=10, got %d", summaries[0].InTokens)
	}
}

func TestHandleServeRecordDetail_ReturnsRecord(t *testing.T) {
	dir := t.TempDir()
	rec := `{"id":"req_001","timestamp":"2024-01-01T00:00:00Z","duration_ms":100,"request":{"body":{"model":"claude-3-opus","messages":[]}},"response":{"body":{"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}}}`
	os.WriteFile(filepath.Join(dir, "test.jsonl"), []byte(rec+"\n"), 0644)

	s := &Server{mode: ModeServe, logDir: dir, subs: make(map[chan []byte]struct{})}
	req := httptest.NewRequest("GET", "/api/records/req_001?file=test.jsonl", nil)
	w := httptest.NewRecorder()
	s.handleServeRecordDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// 验证返回了完整记录（不是摘要）
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if result["id"] != "req_001" {
		t.Errorf("expected id=req_001, got %v", result["id"])
	}
}


package recorder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONLWriter_Write(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	w, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter: %v", err)
	}

	rec := Record{
		ID:         "req_001",
		Timestamp:  "2026-03-26T15:30:00.123Z",
		DurationMs: 3200,
		Request: RequestData{
			Method:  "POST",
			Path:    "/v1/messages",
			Headers: map[string]string{"content-type": "application/json"},
			Body:    json.RawMessage(`{"model":"claude-4.6-opus"}`),
		},
		Response: ResponseData{
			Status:  200,
			Headers: map[string]string{},
			Body:    json.RawMessage(`{"id":"msg_001","content":[]}`),
		},
	}

	if err := w.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var got Record
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ID != "req_001" {
		t.Errorf("ID = %q, want %q", got.ID, "req_001")
	}
	if got.DurationMs != 3200 {
		t.Errorf("DurationMs = %d, want 3200", got.DurationMs)
	}
}

func TestJSONLWriter_MultipleWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.jsonl")

	w, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter: %v", err)
	}

	for i := 0; i < 3; i++ {
		rec := Record{ID: "req_" + string(rune('0'+i))}
		if err := w.Write(rec); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	w.Close()

	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

func TestJSONLWriter_FilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fp.jsonl")
	w, _ := NewJSONLWriter(path)
	defer w.Close()

	if w.FilePath() != path {
		t.Errorf("FilePath() = %q, want %q", w.FilePath(), path)
	}
}

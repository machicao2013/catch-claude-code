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

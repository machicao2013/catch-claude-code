package proxy

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestServer_RandomPort(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	srv, err := NewServer(0, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.Start()
	defer srv.Shutdown(context.Background())

	if srv.Port() == 0 {
		t.Error("port should not be 0")
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(srv.BaseURL() + "/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

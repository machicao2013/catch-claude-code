// proxy/server_test.go
package proxy

import (
	"context"
	"net/http"
	"strings"
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

	// BaseURL 应绑定 0.0.0.0
	if !strings.HasPrefix(srv.BaseURL(), "http://0.0.0.0:") {
		t.Errorf("BaseURL = %q, want prefix http://0.0.0.0:", srv.BaseURL())
	}

	// 用 127.0.0.1 仍可访问（0.0.0.0 包含本地回环）
	client := &http.Client{Timeout: 2 * time.Second}
	localURL := strings.Replace(srv.BaseURL(), "0.0.0.0", "127.0.0.1", 1)
	resp, err := client.Get(localURL + "/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

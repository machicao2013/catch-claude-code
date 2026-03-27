package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"

	"claude-spy/recorder"
)

// Mode 表示 webui 运行模式
type Mode string

const (
	ModeLive Mode = "live" // 实时模式：配合代理使用
	ModeView Mode = "view" // 回顾模式：查看历史 JSONL
)

// Server 提供 Web UI 的 HTTP 服务。
type Server struct {
	mode     Mode
	filename string // 日志文件名（显示用）

	mu      sync.RWMutex
	records []recorder.Record

	// SSE 订阅者：每个连接一个 channel
	subsMu sync.Mutex
	subs   map[chan []byte]struct{}

	httpServer *http.Server
	listener   net.Listener
	port       int
}

// NewServer 创建并初始化 Server，但不启动监听。
// port=0 时自动选择空闲端口。
func NewServer(mode Mode, filename string, port int) (*Server, error) {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("webui listen on %s: %w", addr, err)
	}
	s := &Server{
		mode:     mode,
		filename: filename,
		subs:     make(map[chan []byte]struct{}),
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
	}
	mux := http.NewServeMux()
	// TODO(task2): registerRoutes will be added in handler.go
	s.httpServer = &http.Server{Handler: mux}
	return s, nil
}

func (s *Server) Port() int { return s.port }

func (s *Server) URL() string { return fmt.Sprintf("http://localhost:%d", s.port) }

// Start 在后台 goroutine 中启动 HTTP server。
func (s *Server) Start() {
	go s.httpServer.Serve(s.listener)
}

// Shutdown 优雅关闭 HTTP server。
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Push 接收新记录，存储并广播给所有 SSE 订阅者。
// 实现 proxy.WebUIPusher 接口。
func (s *Server) Push(rec recorder.Record) {
	s.mu.Lock()
	s.records = append(s.records, rec)
	s.mu.Unlock()

	data, err := json.Marshal(rec)
	if err != nil {
		return // 序列化失败时不广播
	}
	msg := []byte("event: record\ndata: " + string(data) + "\n\n")
	s.broadcast(msg)
}

func (s *Server) broadcast(msg []byte) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- msg:
		default: // 订阅者消费慢时丢弃，不阻塞
		}
	}
}

func (s *Server) subscribe() chan []byte {
	ch := make(chan []byte, 32)
	s.subsMu.Lock()
	s.subs[ch] = struct{}{}
	s.subsMu.Unlock()
	return ch
}

func (s *Server) unsubscribe(ch chan []byte) {
	s.subsMu.Lock()
	delete(s.subs, ch)
	s.subsMu.Unlock()
}

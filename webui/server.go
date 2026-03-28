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
	s.registerRoutes(mux)
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

// registerRoutes 注册所有路由。
func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/records/", s.handleRecords) // /api/records/req_001 → 详情
	mux.HandleFunc("/api/records", s.handleRecords)  // /api/records → 摘要列表
	mux.HandleFunc("/api/stream", s.handleStream)
	mux.HandleFunc("/api/info", s.handleInfo)
	// 静态文件：index.html 及 /static/* 资源
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
}

// LoadFromFile 读取 JSONL 文件并填充 records（回顾模式用）。
// loadJSONLFile 定义在同包的 handler.go 中。
func (s *Server) LoadFromFile(path string) error {
	return loadJSONLFile(path, func(rec recorder.Record) {
		s.mu.Lock()
		s.records = append(s.records, rec)
		s.mu.Unlock()
	})
}

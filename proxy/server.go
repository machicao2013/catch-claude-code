package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

type Server struct {
	httpServer *http.Server
	listener   net.Listener
	port       int
}

func NewServer(port int, handler http.Handler) (*Server, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port

	srv := &http.Server{
		Handler: handler,
	}

	return &Server{
		httpServer: srv,
		listener:   listener,
		port:       actualPort,
	}, nil
}

func (s *Server) Port() int {
	return s.port
}

func (s *Server) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.port)
}

func (s *Server) Start() {
	go s.httpServer.Serve(s.listener)
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"claude-spy/display"
	"claude-spy/proxy"
	"claude-spy/recorder"
)

type config struct {
	upstream string
	port     int
	quiet    bool
	saveSSE  bool
	logDir   string
}

func main() {
	os.Exit(run())
}

func run() int {
	args := parseArgs(os.Args[1:])

	// --upstream 必填，且须为合法 URL（有 scheme 和 host）
	if args.upstream == "" {
		fmt.Fprintf(os.Stderr, "Error: --upstream is required (or set CLAUDE_SPY_UPSTREAM)\n")
		printUsage()
		return 1
	}
	if u, err := url.Parse(args.upstream); err != nil || u.Scheme == "" || u.Host == "" {
		fmt.Fprintf(os.Stderr, "Error: --upstream must be a valid URL with scheme and host, e.g. https://api.anthropic.com\n")
		return 1
	}

	sessionID := generateSessionID()
	logPath := filepath.Join(args.logDir, sessionID+".jsonl")

	// 创建日志目录
	if err := os.MkdirAll(args.logDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: create log dir: %v\n", err)
		return 1
	}

	rec, err := recorder.NewJSONLWriter(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: create log file: %v\n", err)
		return 1
	}
	defer rec.Close()

	printer := display.NewPrinter(os.Stderr, args.quiet)
	summary := display.NewSummary()

	handler := proxy.NewHandler(args.upstream, rec, printer, summary, args.saveSSE)

	srv, err := proxy.NewServer(args.port, handler)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not start proxy server: %v\n", err)
		return 1
	}
	srv.Start()
	sessionStart := time.Now() // 记录 session 开始时间，用于退出时统计

	// 启动信息始终打印（--quiet 只抑制实时摘要）
	fmt.Fprintf(os.Stderr, "[claude-spy] Upstream API:  %s\n", args.upstream)
	fmt.Fprintf(os.Stderr, "[claude-spy] Proxy listening on http://0.0.0.0:%d\n", srv.Port())
	fmt.Fprintf(os.Stderr, "[claude-spy] Set ANTHROPIC_BASE_URL=http://<your-ip>:%d\n", srv.Port())
	fmt.Fprintf(os.Stderr, "[claude-spy] Logging to %s\n\n", logPath)

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	// 优雅关闭，10s 超时
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)

	printer.PrintSessionSummary(summary, time.Since(sessionStart), logPath)
	return 0
}

func parseArgs(args []string) config {
	cfg := config{
		port:   8080,
		logDir: defaultLogDir(),
	}

	// 优先从环境变量读取 upstream
	cfg.upstream = os.Getenv("CLAUDE_SPY_UPSTREAM")

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--upstream":
			if i+1 < len(args) {
				cfg.upstream = args[i+1]
				i++
			}
		case "--port":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &cfg.port)
				i++
			}
		case "--quiet":
			cfg.quiet = true
		case "--save-sse":
			cfg.saveSSE = true
		case "--log-dir":
			if i+1 < len(args) {
				cfg.logDir = args[i+1]
				i++
			}
		case "--help", "-h":
			printUsage()
			os.Exit(0)
		}
	}
	return cfg
}

func generateSessionID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return time.Now().Format("20060102_150405") + "_" + fmt.Sprintf("%x", b)
}

func defaultLogDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude-spy", "logs")
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `claude-spy — standalone HTTP proxy between Claude Code and the Anthropic API

Usage:
  claude-spy --upstream <url> [options]

Options:
  --upstream <url>   Upstream API base URL (required, or set CLAUDE_SPY_UPSTREAM)
  --port <n>         Proxy listen port (default: 8080)
  --quiet            Suppress per-request terminal summaries
  --save-sse         Save raw SSE events in logs
  --log-dir <dir>    Log directory (default: ~/.claude-spy/logs)
  --help             Show this help

Example:
  claude-spy --upstream https://api.anthropic.com --port 8080
  ANTHROPIC_BASE_URL=http://<this-host>:8080 claude  # on the client machine
`)
}

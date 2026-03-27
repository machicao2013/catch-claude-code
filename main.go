package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"claude-spy/display"
	"claude-spy/proxy"
	"claude-spy/recorder"
	"claude-spy/webui"
)

type config struct {
	upstream string
	port     int
	quiet    bool
	saveSSE  bool
	logDir   string
	webPort  int // 0 表示不启用 Web UI（实时模式）
}

func main() {
	os.Exit(run())
}

func run() int {
	// Handle --help first, before parsing other args
	for _, a := range os.Args[1:] {
		if a == "--help" || a == "-h" {
			printUsage()
			return 0
		}
	}

	// 检查是否为 view 子命令
	if len(os.Args) > 1 && os.Args[1] == "view" {
		return runView(os.Args[2:])
	}

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

	// 实时模式：若指定了 --web-port，启动 webui server
	var webuiSrv *webui.Server
	var pusher proxy.WebUIPusher
	if args.webPort != 0 {
		ws, err := webui.NewServer(webui.ModeLive, filepath.Base(logPath), args.webPort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: start web UI: %v\n", err)
			return 1
		}
		webuiSrv = ws
		pusher = ws
		ws.Start()
		fmt.Fprintf(os.Stderr, "[claude-spy] Web UI:  %s\n", ws.URL())
	}
	handler := proxy.NewHandler(args.upstream, rec, printer, summary, args.saveSSE, pusher)

	srv, err := proxy.NewServer(args.port, handler)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not start proxy server: %v\n", err)
		return 1
	}
	srv.Start()
	sessionStart := time.Now() // 记录 session 开始时间，用于退出时统计

	// 启动信息始终打印（--quiet 只抑制实时摘要）
	fmt.Fprintf(os.Stderr, "[claude-spy] Upstream API: %s\n", args.upstream)
	fmt.Fprintf(os.Stderr, "[claude-spy] Proxy listening on http://0.0.0.0:%d\n", srv.Port())
	fmt.Fprintf(os.Stderr, "[claude-spy] Set ANTHROPIC_BASE_URL=http://<your-ip>:%d\n", srv.Port())
	fmt.Fprintf(os.Stderr, "[claude-spy] Logging to %s\n\n", logPath)

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	signal.Stop(sigCh)

	// 优雅关闭，10s 超时
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "[claude-spy] Shutdown: %v\n", err)
	}

	if webuiSrv != nil {
		webuiCtx, webuiCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer webuiCancel()
		if err := webuiSrv.Shutdown(webuiCtx); err != nil {
			fmt.Fprintf(os.Stderr, "[claude-spy] WebUI Shutdown: %v\n", err)
		}
	}

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
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n < 0 || n > 65535 {
					fmt.Fprintf(os.Stderr, "Error: invalid --port value %q\n", args[i+1])
					os.Exit(1)
				}
				cfg.port = n
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
		case "--web-port":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n < 0 || n > 65535 {
					fmt.Fprintf(os.Stderr, "Error: invalid --web-port value %q\n", args[i+1])
					os.Exit(1)
				}
				cfg.webPort = n
				i++
			}
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

func runView(args []string) int {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: claude-spy view <file.jsonl> [--port <n>]\n")
		return 1
	}
	filePath := args[0]
	port := 0 // 0 = 随机端口

	for i := 1; i < len(args); i++ {
		if args[i] == "--port" && i+1 < len(args) {
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 || n > 65535 {
				fmt.Fprintf(os.Stderr, "Error: invalid --port value %q\n", args[i+1])
				return 1
			}
			port = n
			i++
		}
	}

	ws, err := webui.NewServer(webui.ModeView, filepath.Base(filePath), port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: start web UI: %v\n", err)
		return 1
	}
	if err := ws.LoadFromFile(filePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: load file: %v\n", err)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cleanupCancel()
		ws.Shutdown(cleanupCtx)
		return 1
	}
	ws.Start()
	fmt.Fprintf(os.Stderr, "[claude-spy] Viewing: %s\n", filePath)
	fmt.Fprintf(os.Stderr, "[claude-spy] Web UI:  %s\n", ws.URL())
	fmt.Fprintf(os.Stderr, "[claude-spy] Press Ctrl+C to exit\n\n")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	signal.Stop(sigCh)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws.Shutdown(ctx)
	return 0
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
  --web-port <n>     Start web UI on this port (live mode, alongside proxy)
  --help             Show this help

Subcommands:
  view <file.jsonl> [--port <n>]   Browse a recorded log file in the browser

Example:
  claude-spy --upstream https://api.anthropic.com --port 8080
  ANTHROPIC_BASE_URL=http://<this-host>:8080 claude  # on the client machine
`)
}

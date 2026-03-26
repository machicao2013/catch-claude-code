package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"claude-spy/display"
	"claude-spy/launcher"
	"claude-spy/proxy"
	"claude-spy/recorder"
)

func main() {
	os.Exit(run())
}

func run() int {
	spyArgs, claudeArgs := splitArgs(os.Args[1:])

	port := 0
	quiet := false
	saveSSE := false
	logDir := defaultLogDir()

	for i := 0; i < len(spyArgs); i++ {
		switch spyArgs[i] {
		case "--port":
			if i+1 < len(spyArgs) {
				fmt.Sscanf(spyArgs[i+1], "%d", &port)
				i++
			}
		case "--quiet":
			quiet = true
		case "--save-sse":
			saveSSE = true
		case "--log-dir":
			if i+1 < len(spyArgs) {
				logDir = spyArgs[i+1]
				i++
			}
		case "--help", "-h":
			printUsage()
			return 0
		}
	}

	sessionID := generateSessionID()
	logPath := filepath.Join(logDir, sessionID+".jsonl")

	printer := display.NewPrinter(os.Stderr, quiet)

	rec, err := recorder.NewJSONLWriter(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: create log file: %v\n", err)
		return 1
	}
	defer rec.Close()

	summary := display.NewSummary()

	// Determine upstream API URL
	// Priority: CLAUDE_SPY_UPSTREAM env > ANTHROPIC_BASE_URL env > extract from binary
	upstreamURL := os.Getenv("CLAUDE_SPY_UPSTREAM")
	if upstreamURL == "" {
		upstreamURL = os.Getenv("ANTHROPIC_BASE_URL")
	}

	claudePath := launcher.FindClaude()

	if upstreamURL == "" {
		// Try to extract from the claude binary
		upstreamURL = launcher.ExtractUpstreamURL(claudePath)
	}
	if upstreamURL == "" {
		fmt.Fprintf(os.Stderr, "Error: cannot determine API URL. Set CLAUDE_SPY_UPSTREAM or ANTHROPIC_BASE_URL\n")
		return 1
	}

	if !quiet {
		fmt.Fprintf(os.Stderr, "[claude-spy] Upstream API: %s\n", upstreamURL)
	}

	handler := proxy.NewHandler(upstreamURL, rec, printer, summary, saveSSE)

	var srv *proxy.Server
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		srv, err = proxy.NewServer(port, handler)
		if err == nil {
			break
		}
		if i == maxRetries-1 {
			fmt.Fprintf(os.Stderr, "Error: could not start proxy server: %v\n", err)
			return 1
		}
		port = 0
	}
	srv.Start()
	defer srv.Shutdown(context.Background())

	if !quiet {
		fmt.Fprintf(os.Stderr, "[claude-spy] Proxy listening on %s\n", srv.BaseURL())
		fmt.Fprintf(os.Stderr, "[claude-spy] Logging to %s\n\n", logPath)
	}

	env := launcher.BuildEnv(srv.BaseURL(), os.Environ())

	sessionStart := time.Now()
	exitCode, err := launcher.Launch(claudePath, claudeArgs, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	printer.PrintSessionSummary(summary, time.Since(sessionStart), logPath)

	return exitCode
}

func splitArgs(args []string) (spyArgs, claudeArgs []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return nil, args
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
	fmt.Fprintf(os.Stderr, `claude-spy — intercept and log Claude Code API interactions

Usage:
  claude-spy [spy-options] [--] [claude-options...]

Spy Options:
  --port <n>       Proxy port (default: auto)
  --quiet          Suppress terminal summaries
  --save-sse       Save raw SSE events in logs
  --log-dir <dir>  Log directory (default: ~/.claude-spy/logs)
  --help           Show this help

Examples:
  claude-spy                      # Normal usage
  claude-spy --continue           # Continue last session
  claude-spy --quiet -- -p "hi"   # Quiet mode, print mode
`)
}

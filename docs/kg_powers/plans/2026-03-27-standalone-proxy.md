# Standalone Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use kg_powers:subagent-driven-development (recommended) or kg_powers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 claude-spy 改造为独立 HTTP 代理服务，去掉启动 claude code 的子进程逻辑，使其可部署在远端机器上。

**Architecture:** 删除 `launcher/` 模块，`proxy/server.go` 改为绑定 `0.0.0.0`，重写 `main.go` 为纯代理服务入口（参数解析 + 启动代理 + 信号等待 + 优雅退出）。`proxy/`、`recorder/`、`display/` 模块完全不动。

**Tech Stack:** Go 1.23，纯标准库（`net/http`、`os/signal`、`context`、`net/url`）

## Applicable Coding Standards

| Standard | File | Scope | Severity |
|----------|------|-------|----------|
| coding_style | /data/home/jerryma/sourceCode/person_skills/kg_powers/profiles/kg_golang_proj/standards/coding-style.md | all | critical |
| build_verification | /data/home/jerryma/sourceCode/person_skills/kg_powers/profiles/kg_golang_proj/standards/build-verification.md | all | critical |

---

## File Map

| Action | File | Change |
|--------|------|--------|
| Delete | `launcher/launcher.go` | 整体删除 |
| Delete | `launcher/launcher_test.go` | 整体删除 |
| Modify | `proxy/server.go` | 绑定地址 `127.0.0.1` → `0.0.0.0` |
| Modify | `proxy/server_test.go` | 更新 BaseURL 断言（`127.0.0.1` → `0.0.0.0`） |
| Rewrite | `main.go` | 去掉 launcher 依赖，重写为独立代理入口 |
| Rewrite | `main_test.go` | 删除 splitArgs/generateSessionID 旧测试，新增 parseArgs 测试 |

---

## Task 1: 删除 launcher/ 目录

**Files:**
- Delete: `launcher/launcher.go`
- Delete: `launcher/launcher_test.go`

- [ ] **Step 1: 删除 launcher 目录**

```bash
rm -rf /data/home/jerryma/sourceCode/person_skills/catch_tools/launcher
```

- [ ] **Step 2: 验证目录已删除**

```bash
ls /data/home/jerryma/sourceCode/person_skills/catch_tools/
```

Expected: 不再出现 `launcher/` 目录

- [ ] **Step 3: 提交**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
git add -A
git commit -m "refactor: remove launcher module"
```

---

## Task 2: 修改 proxy/server.go 绑定 0.0.0.0

**Files:**
- Modify: `proxy/server.go`
- Modify: `proxy/server_test.go`

- [ ] **Step 1: 先更新测试，修改 BaseURL 断言**

打开 `proxy/server_test.go`，将 `srv.BaseURL()` 断言改为期待 `0.0.0.0`：

```go
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
```

- [ ] **Step 2: 运行测试，确认失败（因为 server.go 还没改）**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go test ./proxy/ -run TestServer_RandomPort -v
```

Expected: FAIL，`BaseURL = "http://127.0.0.1:xxxx"` 不满足新断言

- [ ] **Step 3: 修改 proxy/server.go**

将 `NewServer` 中的绑定地址从 `127.0.0.1` 改为 `0.0.0.0`，`BaseURL()` 也同步返回 `0.0.0.0`：

```go
// proxy/server.go
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
	addr := fmt.Sprintf("0.0.0.0:%d", port)
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
	return fmt.Sprintf("http://0.0.0.0:%d", s.port)
}

func (s *Server) Start() {
	go s.httpServer.Serve(s.listener)
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go test ./proxy/ -v
```

Expected: 所有 proxy 测试 PASS

- [ ] **Step 5: 编译验证**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go build ./...
```

注意：此时 `main.go` 仍引用 `launcher`，编译会报错——这是预期的，Task 3 会修复。
只验证 `proxy` 包本身编译无误即可：

```bash
go build ./proxy/...
go vet ./proxy/...
```

Expected: 无错误

- [ ] **Step 6: 提交**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
git add proxy/server.go proxy/server_test.go
git commit -m "refactor: bind proxy server to 0.0.0.0 for external access"
```

---

## Task 3: 重写 main.go

**Files:**
- Rewrite: `main.go`
- Rewrite: `main_test.go`

**说明：** `main.go` 完整替换，去掉 `launcher` 依赖，新增 `parseArgs` 函数取代旧的 `splitArgs`。
旧 `main.go` 中端口绑定有 3 次 retry loop，本次**一并删除**：绑定失败直接打印错误退出，不重试。

- [ ] **Step 1: 先写 main_test.go（TDD）**

```go
// main_test.go
package main

import (
	"os"
	"testing"
)

func TestParseArgs_Defaults(t *testing.T) {
	os.Unsetenv("CLAUDE_SPY_UPSTREAM")
	args := parseArgs([]string{})
	if args.port != 8080 {
		t.Errorf("default port = %d, want 8080", args.port)
	}
	if args.quiet != false {
		t.Error("default quiet should be false")
	}
	if args.saveSSE != false {
		t.Error("default saveSSE should be false")
	}
	if args.upstream != "" {
		t.Errorf("default upstream should be empty, got %q", args.upstream)
	}
}

func TestParseArgs_AllFlags(t *testing.T) {
	os.Unsetenv("CLAUDE_SPY_UPSTREAM")
	args := parseArgs([]string{
		"--upstream", "https://api.anthropic.com",
		"--port", "9090",
		"--quiet",
		"--save-sse",
		"--log-dir", "/tmp/logs",
	})
	if args.upstream != "https://api.anthropic.com" {
		t.Errorf("upstream = %q", args.upstream)
	}
	if args.port != 9090 {
		t.Errorf("port = %d, want 9090", args.port)
	}
	if !args.quiet {
		t.Error("quiet should be true")
	}
	if !args.saveSSE {
		t.Error("saveSSE should be true")
	}
	if args.logDir != "/tmp/logs" {
		t.Errorf("logDir = %q, want /tmp/logs", args.logDir)
	}
}

func TestParseArgs_UpstreamFromEnv(t *testing.T) {
	os.Setenv("CLAUDE_SPY_UPSTREAM", "https://env-upstream.example.com")
	defer os.Unsetenv("CLAUDE_SPY_UPSTREAM")

	args := parseArgs([]string{})
	if args.upstream != "https://env-upstream.example.com" {
		t.Errorf("upstream from env = %q", args.upstream)
	}
}

func TestParseArgs_FlagOverridesEnv(t *testing.T) {
	os.Setenv("CLAUDE_SPY_UPSTREAM", "https://env-upstream.example.com")
	defer os.Unsetenv("CLAUDE_SPY_UPSTREAM")

	args := parseArgs([]string{"--upstream", "https://flag-upstream.example.com"})
	if args.upstream != "https://flag-upstream.example.com" {
		t.Errorf("flag should override env, got %q", args.upstream)
	}
}

func TestGenerateSessionID(t *testing.T) {
	id := generateSessionID()
	if len(id) < 20 {
		t.Errorf("session ID too short: %q", id)
	}
	if id[8] != '_' {
		t.Errorf("session ID format unexpected: %q", id)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败（parseArgs 未定义）**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go test . -run TestParseArgs -v 2>&1 | head -20
```

Expected: compile error，`parseArgs` undefined

- [ ] **Step 3: 重写 main.go**

```go
// main.go
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
```

- [ ] **Step 4: 运行所有测试**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go test ./... -v
```

Expected: 全部 PASS（launcher 已删，不再有相关测试）

- [ ] **Step 5: 编译验证**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go mod tidy
go build ./...
go vet ./...
gofmt -s -w .
```

Expected: 无错误，无警告

- [ ] **Step 6: 提交**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
git add main.go main_test.go
git commit -m "refactor: rewrite main.go as standalone proxy service, remove launcher dependency"
```

---

## Task 4: 最终验证

- [ ] **Step 1: 完整测试套件**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go test ./... -v -count=1
```

Expected: 全部 PASS

- [ ] **Step 2: 构建二进制**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go build -o claude-spy .
ls -lh claude-spy
```

Expected: 生成单一二进制，大小合理

- [ ] **Step 3: 冒烟测试（需提供有效上游 URL）**

```bash
# 用无效上游测试参数校验
./claude-spy
# Expected: Error: --upstream is required

./claude-spy --upstream "not-a-url-@@"
# Expected: 启动（url.Parse 对大多数字符串不报错，但会有 scheme 缺失）
# 如需严格 scheme 校验，观察实际行为即可

./claude-spy --upstream https://httpbin.org --port 19999 &
PROXY_PID=$!
sleep 1
curl -s http://127.0.0.1:19999/get | head -5
kill $PROXY_PID
# Expected: 代理正常转发（或返回 502 若上游路径不存在）
```

- [ ] **Step 4: 清理构建产物并提交**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
rm -f claude-spy
git status
# Expected: working tree clean
```

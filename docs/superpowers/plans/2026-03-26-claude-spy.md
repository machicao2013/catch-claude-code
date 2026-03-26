# claude-spy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go reverse proxy that intercepts Claude Code ↔ API traffic, printing real-time summaries to stderr and logging full request/response pairs to JSONL files.

**Architecture:** A single binary (`claude-spy`) starts an HTTP reverse proxy on a random local port, sets `ANTHROPIC_BASE_URL` to point at it, then launches `claude-internal` as a child process. The proxy intercepts POST requests to `/v1/messages`, records full request bodies and reassembled SSE streaming responses, prints summaries to stderr, and writes complete pairs to per-session JSONL files.

**Tech Stack:** Go 1.21+ standard library only (`net/http`, `os/exec`, `encoding/json`, `io`, `crypto/rand`). No third-party dependencies.

**Environment context:**
- Claude binary: `/data/home/jerryma/.nvm/versions/node/v24.12.0/bin/claude-internal`
- Real API base URL: `https://copilot.code.woa.com/server/chat/codebuddy-gateway/codebuddy-code`
- Auth header: `x-api-key` (set via `ANTHROPIC_CUSTOM_HEADERS`)

---

## File Structure

```
claude-spy/
├── main.go                 # CLI entry: arg parsing, orchestration, graceful shutdown
├── main_test.go            # Integration test: end-to-end proxy + echo server
├── proxy/
│   ├── server.go           # HTTP server lifecycle (start on random port, shutdown)
│   ├── server_test.go
│   ├── handler.go          # Request handler: read body, forward, record
│   ├── handler_test.go
│   ├── sse.go              # SSE stream: tee-forward + accumulate + reassemble
│   └── sse_test.go
├── recorder/
│   ├── recorder.go         # Record type + Recorder interface
│   ├── jsonl_writer.go     # JSONL file writer (append per session)
│   ├── jsonl_writer_test.go
│   ├── masker.go           # Sensitive header masking
│   └── masker_test.go
├── display/
│   ├── printer.go          # Stderr summary printer (ANSI colors)
│   ├── printer_test.go
│   ├── summary.go          # Session-level aggregation + final summary
│   └── summary_test.go
├── launcher/
│   ├── launcher.go         # Child process management + signal forwarding
│   └── launcher_test.go
└── go.mod
```

---

### Task 1: Project Scaffold + recorder Types

**Files:**
- Create: `go.mod`
- Create: `recorder/recorder.go`
- Create: `recorder/masker.go`
- Create: `recorder/masker_test.go`

- [ ] **Step 1: Initialize Go module**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go mod init claude-spy
```

- [ ] **Step 2: Write masker test**

Create `recorder/masker_test.go`:

```go
package recorder

import (
	"testing"
)

func TestMaskHeaders(t *testing.T) {
	headers := map[string]string{
		"x-api-key":     "sk-ant-abc123secret",
		"content-type":  "application/json",
		"authorization": "Bearer token-xyz",
	}

	masked := MaskHeaders(headers)

	if masked["x-api-key"] != "***MASKED***" {
		t.Errorf("x-api-key should be masked, got %q", masked["x-api-key"])
	}
	if masked["authorization"] != "***MASKED***" {
		t.Errorf("authorization should be masked, got %q", masked["authorization"])
	}
	if masked["content-type"] != "application/json" {
		t.Errorf("content-type should not be masked, got %q", masked["content-type"])
	}
	// original should not be modified
	if headers["x-api-key"] == "***MASKED***" {
		t.Error("original headers should not be modified")
	}
}

func TestMaskHeadersCaseInsensitive(t *testing.T) {
	headers := map[string]string{
		"X-Api-Key": "secret",
	}
	masked := MaskHeaders(headers)
	if masked["X-Api-Key"] != "***MASKED***" {
		t.Errorf("should mask case-insensitively, got %q", masked["X-Api-Key"])
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go test ./recorder/ -v -run TestMask
```

Expected: FAIL — `MaskHeaders` not defined.

- [ ] **Step 4: Implement masker + Record type**

Create `recorder/recorder.go`:

```go
package recorder

import "encoding/json"

// Record represents one complete API request/response pair.
type Record struct {
	ID         string        `json:"id"`
	Timestamp  string        `json:"timestamp"`
	DurationMs int64         `json:"duration_ms"`
	Request    RequestData   `json:"request"`
	Response   ResponseData  `json:"response"`
}

type RequestData struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

type ResponseData struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

// Recorder is the interface for recording API interactions.
type Recorder interface {
	Write(record Record) error
	Close() error
	FilePath() string
}
```

Create `recorder/masker.go`:

```go
package recorder

import "strings"

// sensitiveKeys lists header names that should be masked (lowercase).
var sensitiveKeys = []string{
	"x-api-key",
	"authorization",
	"x-auth-token",
	"cookie",
}

// MaskHeaders returns a copy of headers with sensitive values replaced.
func MaskHeaders(headers map[string]string) map[string]string {
	masked := make(map[string]string, len(headers))
	for k, v := range headers {
		if isSensitive(k) {
			masked[k] = "***MASKED***"
		} else {
			masked[k] = v
		}
	}
	return masked
}

func isSensitive(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if lower == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./recorder/ -v -run TestMask
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod recorder/
git commit -m "feat: project scaffold with Record types and header masker"
```

---

### Task 2: JSONL Writer

**Files:**
- Create: `recorder/jsonl_writer.go`
- Create: `recorder/jsonl_writer_test.go`

- [ ] **Step 1: Write JSONL writer test**

Create `recorder/jsonl_writer_test.go`:

```go
package recorder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONLWriter_Write(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	w, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter: %v", err)
	}

	rec := Record{
		ID:         "req_001",
		Timestamp:  "2026-03-26T15:30:00.123Z",
		DurationMs: 3200,
		Request: RequestData{
			Method:  "POST",
			Path:    "/v1/messages",
			Headers: map[string]string{"content-type": "application/json"},
			Body:    json.RawMessage(`{"model":"claude-4.6-opus"}`),
		},
		Response: ResponseData{
			Status:  200,
			Headers: map[string]string{},
			Body:    json.RawMessage(`{"id":"msg_001","content":[]}`),
		},
	}

	if err := w.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var got Record
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ID != "req_001" {
		t.Errorf("ID = %q, want %q", got.ID, "req_001")
	}
	if got.DurationMs != 3200 {
		t.Errorf("DurationMs = %d, want 3200", got.DurationMs)
	}
}

func TestJSONLWriter_MultipleWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.jsonl")

	w, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter: %v", err)
	}

	for i := 0; i < 3; i++ {
		rec := Record{ID: "req_" + string(rune('0'+i))}
		if err := w.Write(rec); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	w.Close()

	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

func TestJSONLWriter_FilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fp.jsonl")
	w, _ := NewJSONLWriter(path)
	defer w.Close()

	if w.FilePath() != path {
		t.Errorf("FilePath() = %q, want %q", w.FilePath(), path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./recorder/ -v -run TestJSONLWriter
```

Expected: FAIL — `NewJSONLWriter` not defined.

- [ ] **Step 3: Implement JSONL writer**

Create `recorder/jsonl_writer.go`:

```go
package recorder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// JSONLWriter writes Record entries as newline-delimited JSON to a file.
type JSONLWriter struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewJSONLWriter creates a JSONL writer. Creates parent directories if needed.
func NewJSONLWriter(path string) (*JSONLWriter, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	return &JSONLWriter{file: f, path: path}, nil
}

func (w *JSONLWriter) Write(rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	data = append(data, '\n')
	if _, err := w.file.Write(data); err != nil {
		return fmt.Errorf("write record: %w", err)
	}
	return nil
}

func (w *JSONLWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

func (w *JSONLWriter) FilePath() string {
	return w.path
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./recorder/ -v -run TestJSONLWriter
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add recorder/jsonl_writer.go recorder/jsonl_writer_test.go
git commit -m "feat: JSONL writer for recording API interactions"
```

---

### Task 3: Display — Printer + Summary

**Files:**
- Create: `display/printer.go`
- Create: `display/printer_test.go`
- Create: `display/summary.go`
- Create: `display/summary_test.go`

- [ ] **Step 1: Write summary test**

Create `display/summary_test.go`:

```go
package display

import "testing"

func TestSummary_Add(t *testing.T) {
	s := NewSummary()

	s.Add(RequestStats{
		InputTokens:  50000,
		OutputTokens: 500,
		CacheRead:    40000,
		CacheCreate:  0,
		DurationMs:   3200,
	})
	s.Add(RequestStats{
		InputTokens:  60000,
		OutputTokens: 800,
		CacheRead:    50000,
		CacheCreate:  1000,
		DurationMs:   2100,
	})

	if s.TotalRequests() != 2 {
		t.Errorf("TotalRequests = %d, want 2", s.TotalRequests())
	}
	if s.TotalInputTokens() != 110000 {
		t.Errorf("TotalInputTokens = %d, want 110000", s.TotalInputTokens())
	}
	if s.TotalOutputTokens() != 1300 {
		t.Errorf("TotalOutputTokens = %d, want 1300", s.TotalOutputTokens())
	}
}

func TestSummary_Empty(t *testing.T) {
	s := NewSummary()
	if s.TotalRequests() != 0 {
		t.Errorf("TotalRequests = %d, want 0", s.TotalRequests())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./display/ -v -run TestSummary
```

Expected: FAIL — `NewSummary` not defined.

- [ ] **Step 3: Implement Summary**

Create `display/summary.go`:

```go
package display

import "sync"

// RequestStats holds token/timing stats from a single API interaction.
type RequestStats struct {
	InputTokens  int64
	OutputTokens int64
	CacheRead    int64
	CacheCreate  int64
	DurationMs   int64
}

// Summary accumulates stats across all requests in a session.
type Summary struct {
	mu           sync.Mutex
	requests     int
	inputTokens  int64
	outputTokens int64
	cacheRead    int64
	cacheCreate  int64
	totalMs      int64
}

func NewSummary() *Summary {
	return &Summary{}
}

func (s *Summary) Add(stats RequestStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	s.inputTokens += stats.InputTokens
	s.outputTokens += stats.OutputTokens
	s.cacheRead += stats.CacheRead
	s.cacheCreate += stats.CacheCreate
	s.totalMs += stats.DurationMs
}

func (s *Summary) TotalRequests() int       { s.mu.Lock(); defer s.mu.Unlock(); return s.requests }
func (s *Summary) TotalInputTokens() int64  { s.mu.Lock(); defer s.mu.Unlock(); return s.inputTokens }
func (s *Summary) TotalOutputTokens() int64 { s.mu.Lock(); defer s.mu.Unlock(); return s.outputTokens }
func (s *Summary) TotalCacheRead() int64    { s.mu.Lock(); defer s.mu.Unlock(); return s.cacheRead }
func (s *Summary) TotalCacheCreate() int64  { s.mu.Lock(); defer s.mu.Unlock(); return s.cacheCreate }
func (s *Summary) TotalDurationMs() int64   { s.mu.Lock(); defer s.mu.Unlock(); return s.totalMs }
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./display/ -v -run TestSummary
```

Expected: PASS

- [ ] **Step 5: Write printer test**

Create `display/printer_test.go`:

```go
package display

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintRequestSummary(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, false) // quiet=false

	p.PrintRequestSummary(1, RequestSummary{
		Model:       "claude-4.6-opus",
		SystemLen:   3241,
		MsgCounts:   map[string]int{"user": 5, "assistant": 5, "tool_result": 2},
		ToolNames:   []string{"Bash", "Read", "Edit"},
		EstInTokens: 52000,
	})

	out := buf.String()
	if !strings.Contains(out, "REQ #1") {
		t.Errorf("output should contain 'REQ #1', got:\n%s", out)
	}
	if !strings.Contains(out, "claude-4.6-opus") {
		t.Errorf("output should contain model name, got:\n%s", out)
	}
	if !strings.Contains(out, "Bash") {
		t.Errorf("output should contain tool names, got:\n%s", out)
	}
}

func TestPrintResponseSummary(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, false)

	p.PrintResponseSummary(1, ResponseSummary{
		DurationMs:  3200,
		OutputDesc:  "text(245 chars) + tool_use(Bash)",
		StopReason:  "tool_use",
		InTokens:    52103,
		OutTokens:   387,
		CacheCreate: 0,
		CacheRead:   48200,
	})

	out := buf.String()
	if !strings.Contains(out, "RES #1") {
		t.Errorf("output should contain 'RES #1', got:\n%s", out)
	}
	if !strings.Contains(out, "3.2s") {
		t.Errorf("output should contain duration, got:\n%s", out)
	}
}

func TestPrinter_Quiet(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, true) // quiet=true

	p.PrintRequestSummary(1, RequestSummary{Model: "test"})
	p.PrintResponseSummary(1, ResponseSummary{})

	if buf.Len() != 0 {
		t.Errorf("quiet mode should produce no output, got %d bytes", buf.Len())
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

```bash
go test ./display/ -v -run TestPrint
```

Expected: FAIL — `NewPrinter` not defined.

- [ ] **Step 7: Implement Printer**

Create `display/printer.go`:

```go
package display

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorBlue   = "\033[34m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorGray   = "\033[90m"
)

// RequestSummary is the data needed to print a request summary.
type RequestSummary struct {
	Model       string
	SystemLen   int
	MsgCounts   map[string]int
	ToolNames   []string
	EstInTokens int64
}

// ResponseSummary is the data needed to print a response summary.
type ResponseSummary struct {
	DurationMs  int64
	OutputDesc  string
	StopReason  string
	InTokens    int64
	OutTokens   int64
	CacheCreate int64
	CacheRead   int64
}

// Printer writes formatted summaries to an io.Writer (typically stderr).
type Printer struct {
	w     io.Writer
	quiet bool
}

func NewPrinter(w io.Writer, quiet bool) *Printer {
	return &Printer{w: w, quiet: quiet}
}

func (p *Printer) PrintRequestSummary(reqNum int, s RequestSummary) {
	if p.quiet {
		return
	}

	// Build messages description
	totalMsgs := 0
	var parts []string
	for role, count := range s.MsgCounts {
		totalMsgs += count
		parts = append(parts, fmt.Sprintf("%s:%d", role, count))
	}
	msgDesc := fmt.Sprintf("%d 条 (%s)", totalMsgs, strings.Join(parts, ", "))

	// Build tools description
	toolDesc := fmt.Sprintf("%d 个", len(s.ToolNames))
	if len(s.ToolNames) > 0 {
		shown := s.ToolNames
		if len(shown) > 5 {
			shown = shown[:5]
		}
		toolDesc += " [" + strings.Join(shown, ", ")
		if len(s.ToolNames) > 5 {
			toolDesc += ", ..."
		}
		toolDesc += "]"
	}

	fmt.Fprintf(p.w, "%s━━━ REQ #%d ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorBlue, reqNum, colorReset)
	fmt.Fprintf(p.w, "  Model:      %s\n", s.Model)
	fmt.Fprintf(p.w, "  System:     %s chars\n", formatNumber(int64(s.SystemLen)))
	fmt.Fprintf(p.w, "  Messages:   %s\n", msgDesc)
	fmt.Fprintf(p.w, "  Tools:      %s\n", toolDesc)
	fmt.Fprintf(p.w, "  Tokens(est): ~%s input\n", formatNumber(s.EstInTokens))
	fmt.Fprintf(p.w, "%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorBlue, colorReset)
}

func (p *Printer) PrintResponseSummary(reqNum int, s ResponseSummary) {
	if p.quiet {
		return
	}

	dur := formatDuration(s.DurationMs)

	fmt.Fprintf(p.w, "%s━━━ RES #%d ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorGreen, reqNum, colorReset)
	fmt.Fprintf(p.w, "  Duration:   %s\n", dur)
	fmt.Fprintf(p.w, "  Output:     %s\n", s.OutputDesc)
	fmt.Fprintf(p.w, "  Stop:       %s\n", s.StopReason)
	fmt.Fprintf(p.w, "  Tokens:     %s in / %s out / %s cache_create / %s cache_read\n",
		formatNumber(s.InTokens), formatNumber(s.OutTokens),
		formatNumber(s.CacheCreate), formatNumber(s.CacheRead))
	fmt.Fprintf(p.w, "%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorGreen, colorReset)
}

func (p *Printer) PrintSessionSummary(s *Summary, duration time.Duration, logFile string) {
	if p.quiet {
		return
	}
	fmt.Fprintf(p.w, "\n%s══════════ SESSION SUMMARY ══════════%s\n", colorYellow, colorReset)
	fmt.Fprintf(p.w, "  Requests:   %d\n", s.TotalRequests())
	fmt.Fprintf(p.w, "  Duration:   %s\n", duration.Truncate(time.Second))
	fmt.Fprintf(p.w, "  Tokens:     %s in / %s out\n",
		formatNumber(s.TotalInputTokens()), formatNumber(s.TotalOutputTokens()))
	fmt.Fprintf(p.w, "  Log file:   %s\n", logFile)
	fmt.Fprintf(p.w, "%s═════════════════════════════════════%s\n", colorYellow, colorReset)
}

func (p *Printer) PrintError(msg string) {
	fmt.Fprintf(p.w, "%s[ERROR] %s%s\n", colorRed, msg, colorReset)
}

func formatNumber(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%s,%03d", formatNumber(n/1000), n%1000)
	}
	return fmt.Sprintf("%s,%03d,%03d", formatNumber(n/1000000), (n/1000)%1000, n%1000)
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
}
```

- [ ] **Step 8: Run all display tests**

```bash
go test ./display/ -v
```

Expected: all PASS

- [ ] **Step 9: Commit**

```bash
git add display/
git commit -m "feat: terminal printer with ANSI colors and session summary"
```

---

### Task 4: SSE Stream Parser

**Files:**
- Create: `proxy/sse.go`
- Create: `proxy/sse_test.go`

- [ ] **Step 1: Write SSE parser test**

Create `proxy/sse_test.go`:

```go
package proxy

import (
	"strings"
	"testing"
)

func TestParseSSEEvents(t *testing.T) {
	raw := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","model":"claude-4.6-opus","content":[],"usage":{"input_tokens":100}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}

event: message_stop
data: {"type":"message_stop"}

`

	events := ParseSSEEvents(strings.NewReader(raw))

	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d", len(events))
	}
	if events[0].Event != "message_start" {
		t.Errorf("event[0].Event = %q, want message_start", events[0].Event)
	}
	if events[6].Event != "message_stop" {
		t.Errorf("event[6].Event = %q, want message_stop", events[6].Event)
	}
}

func TestReassembleSSEResponse(t *testing.T) {
	raw := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","model":"claude-4.6-opus","content":[],"stop_reason":null,"usage":{"input_tokens":100,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}

event: message_stop
data: {"type":"message_stop"}

`

	msg, err := ReassembleSSEResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ReassembleSSEResponse: %v", err)
	}

	if msg.ID != "msg_01" {
		t.Errorf("ID = %q, want msg_01", msg.ID)
	}
	if msg.Model != "claude-4.6-opus" {
		t.Errorf("Model = %q, want claude-4.6-opus", msg.Model)
	}
	if msg.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", msg.StopReason)
	}
	if msg.Usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", msg.Usage.InputTokens)
	}
	if msg.Usage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", msg.Usage.OutputTokens)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(msg.Content))
	}
	if msg.Content[0].Type != "text" {
		t.Errorf("Content[0].Type = %q, want text", msg.Content[0].Type)
	}
	if msg.Content[0].Text != "Hello world" {
		t.Errorf("Content[0].Text = %q, want 'Hello world'", msg.Content[0].Text)
	}
}

func TestReassembleSSE_ToolUse(t *testing.T) {
	raw := `event: message_start
data: {"type":"message_start","message":{"id":"msg_02","type":"message","role":"assistant","model":"claude-4.6-opus","content":[],"stop_reason":null,"usage":{"input_tokens":200,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_01","name":"Bash","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":30}}

event: message_stop
data: {"type":"message_stop"}

`

	msg, err := ReassembleSSEResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ReassembleSSEResponse: %v", err)
	}
	if msg.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", msg.StopReason)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(msg.Content))
	}
	if msg.Content[0].Type != "tool_use" {
		t.Errorf("Content[0].Type = %q, want tool_use", msg.Content[0].Type)
	}
	if msg.Content[0].Name != "Bash" {
		t.Errorf("Content[0].Name = %q, want Bash", msg.Content[0].Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./proxy/ -v -run TestParseSSE
```

Expected: FAIL — types not defined.

- [ ] **Step 3: Implement SSE parser and reassembler**

Create `proxy/sse.go`:

```go
package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// SSEEvent represents a single Server-Sent Event.
type SSEEvent struct {
	Event string
	Data  string
}

// AssembledMessage is the reassembled full message from SSE events.
type AssembledMessage struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Model      string            `json:"model"`
	Content    []ContentBlock    `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      AssembledUsage    `json:"usage"`
}

type ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type AssembledUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	CacheCreationTokens   int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadTokens       int64 `json:"cache_read_input_tokens,omitempty"`
}

// ParseSSEEvents reads SSE-formatted data and returns parsed events.
func ParseSSEEvents(r io.Reader) []SSEEvent {
	var events []SSEEvent
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB max line

	var currentEvent, currentData string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if currentData != "" {
				events = append(events, SSEEvent{Event: currentEvent, Data: currentData})
			}
			currentEvent = ""
			currentData = ""
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			currentData = strings.TrimPrefix(line, "data: ")
		}
	}
	return events
}

// ReassembleSSEResponse reassembles SSE events into a complete message.
func ReassembleSSEResponse(raw []byte) (*AssembledMessage, error) {
	events := ParseSSEEvents(strings.NewReader(string(raw)))

	var msg AssembledMessage
	// Track content blocks being built
	type blockBuilder struct {
		typ       string
		text      strings.Builder
		id        string
		name      string
		inputJSON strings.Builder
	}
	blocks := make(map[int]*blockBuilder)

	for _, ev := range events {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
			continue
		}

		switch ev.Event {
		case "message_start":
			var wrapper struct {
				Message struct {
					ID    string         `json:"id"`
					Type  string         `json:"type"`
					Role  string         `json:"role"`
					Model string         `json:"model"`
					Usage AssembledUsage `json:"usage"`
				} `json:"message"`
			}
			json.Unmarshal([]byte(ev.Data), &wrapper)
			msg.ID = wrapper.Message.ID
			msg.Type = wrapper.Message.Type
			msg.Role = wrapper.Message.Role
			msg.Model = wrapper.Message.Model
			msg.Usage.InputTokens = wrapper.Message.Usage.InputTokens
			msg.Usage.CacheCreationTokens = wrapper.Message.Usage.CacheCreationTokens
			msg.Usage.CacheReadTokens = wrapper.Message.Usage.CacheReadTokens

		case "content_block_start":
			var cb struct {
				Index int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					Text string `json:"text"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			json.Unmarshal([]byte(ev.Data), &cb)
			b := &blockBuilder{typ: cb.ContentBlock.Type, id: cb.ContentBlock.ID, name: cb.ContentBlock.Name}
			b.text.WriteString(cb.ContentBlock.Text)
			blocks[cb.Index] = b

		case "content_block_delta":
			var delta struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			json.Unmarshal([]byte(ev.Data), &delta)
			if b, ok := blocks[delta.Index]; ok {
				if delta.Delta.Type == "text_delta" {
					b.text.WriteString(delta.Delta.Text)
				} else if delta.Delta.Type == "input_json_delta" {
					b.inputJSON.WriteString(delta.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			// Block is finalized on message_stop

		case "message_delta":
			var md struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int64 `json:"output_tokens"`
				} `json:"usage"`
			}
			json.Unmarshal([]byte(ev.Data), &md)
			msg.StopReason = md.Delta.StopReason
			msg.Usage.OutputTokens = md.Usage.OutputTokens

		case "message_stop":
			// Finalize all blocks
		}
	}

	// Build final content blocks in order
	for i := 0; i < len(blocks); i++ {
		b, ok := blocks[i]
		if !ok {
			return nil, fmt.Errorf("missing content block at index %d", i)
		}
		cb := ContentBlock{Type: b.typ}
		switch b.typ {
		case "text":
			cb.Text = b.text.String()
		case "tool_use":
			cb.ID = b.id
			cb.Name = b.name
			inputStr := b.inputJSON.String()
			if inputStr != "" {
				cb.Input = json.RawMessage(inputStr)
			} else {
				cb.Input = json.RawMessage("{}")
			}
		}
		msg.Content = append(msg.Content, cb)
	}

	return &msg, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./proxy/ -v
```

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add proxy/sse.go proxy/sse_test.go
git commit -m "feat: SSE stream parser and message reassembler"
```

---

### Task 5: Proxy Handler

**Files:**
- Create: `proxy/handler.go`
- Create: `proxy/handler_test.go`
- Create: `proxy/server.go`
- Create: `proxy/server_test.go`

- [ ] **Step 1: Write handler test with a fake upstream server**

Create `proxy/handler_test.go`:

```go
package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"claude-spy/display"
	"claude-spy/recorder"
)

// memRecorder stores records in memory for testing.
type memRecorder struct {
	records []recorder.Record
}

func (m *memRecorder) Write(r recorder.Record) error { m.records = append(m.records, r); return nil }
func (m *memRecorder) Close() error                   { return nil }
func (m *memRecorder) FilePath() string               { return "test.jsonl" }

func TestHandler_NonMessagesPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	rec := &memRecorder{}
	printer := display.NewPrinter(io.Discard, true)
	summary := display.NewSummary()
	h := NewHandler(upstream.URL, rec, printer, summary, false)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if len(rec.records) != 0 {
		t.Errorf("should not record non-messages requests, got %d records", len(rec.records))
	}
}

func TestHandler_MessagesNonStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify we got the body forwarded
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)
		if req["model"] != "claude-test" {
			t.Errorf("upstream got model = %v", req["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		resp := `{"id":"msg_01","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`
		w.Write([]byte(resp))
	}))
	defer upstream.Close()

	rec := &memRecorder{}
	printer := display.NewPrinter(io.Discard, true)
	summary := display.NewSummary()
	h := NewHandler(upstream.URL, rec, printer, summary, false)

	body := `{"model":"claude-test","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if len(rec.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(rec.records))
	}
	if rec.records[0].Request.Method != "POST" {
		t.Errorf("recorded method = %q", rec.records[0].Request.Method)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./proxy/ -v -run TestHandler
```

Expected: FAIL — `NewHandler` not defined.

- [ ] **Step 3: Implement Handler**

Create `proxy/handler.go`:

```go
package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"claude-spy/display"
	"claude-spy/recorder"
)

// Handler is the HTTP handler that proxies and records API requests.
type Handler struct {
	targetURL string
	client    *http.Client
	recorder  recorder.Recorder
	printer   *display.Printer
	summary   *display.Summary
	saveSSE   bool
	reqCount  atomic.Int64
}

func NewHandler(targetURL string, rec recorder.Recorder, printer *display.Printer, summary *display.Summary, saveSSE bool) *Handler {
	return &Handler{
		targetURL: strings.TrimRight(targetURL, "/"),
		client:    &http.Client{Timeout: 10 * time.Minute},
		recorder:  rec,
		printer:   printer,
		summary:   summary,
		saveSSE:   saveSSE,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only intercept POST /v1/messages
	isMessages := r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/v1/messages")
	if !isMessages {
		h.forwardSimple(w, r)
		return
	}
	h.handleMessages(w, r)
}

func (h *Handler) forwardSimple(w http.ResponseWriter, r *http.Request) {
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, h.targetURL+r.URL.Path, r.Body)
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	copyHeaders(proxyReq.Header, r.Header)

	resp, err := h.client.Do(proxyReq)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	reqNum := int(h.reqCount.Add(1))
	startTime := time.Now()

	// Read request body
	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.printer.PrintError(fmt.Sprintf("read request body: %v", err))
		http.Error(w, "read body error", http.StatusBadGateway)
		return
	}

	// Print request summary
	h.printReqSummary(reqNum, reqBody)

	// Forward to upstream
	proxyReq, err := http.NewRequestWithContext(r.Context(), "POST", h.targetURL+r.URL.Path, bytes.NewReader(reqBody))
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	copyHeaders(proxyReq.Header, r.Header)

	resp, err := h.client.Do(proxyReq)
	if err != nil {
		h.printer.PrintError(fmt.Sprintf("upstream error: %v", err))
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	// Read response, forwarding to client as we go
	isSSE := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	var respBody []byte

	if isSSE {
		respBody, err = h.streamSSE(w, resp.Body)
	} else {
		respBody, err = h.forwardAndCapture(w, resp.Body)
	}
	if err != nil {
		h.printer.PrintError(fmt.Sprintf("read response: %v", err))
	}

	durationMs := time.Since(startTime).Milliseconds()

	// Build and write record
	h.recordAndSummarize(reqNum, reqBody, respBody, r, resp.StatusCode, resp.Header, durationMs, isSSE)
}

func (h *Handler) streamSSE(w http.ResponseWriter, body io.Reader) ([]byte, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return h.forwardAndCapture(w, body)
	}

	var buf bytes.Buffer
	reader := io.TeeReader(body, &buf)

	// Read in chunks and flush
	chunk := make([]byte, 4096)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			w.Write(chunk[:n])
			flusher.Flush()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return buf.Bytes(), err
		}
	}
	return buf.Bytes(), nil
}

func (h *Handler) forwardAndCapture(w http.ResponseWriter, body io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	reader := io.TeeReader(body, &buf)
	io.Copy(w, reader)
	return buf.Bytes(), nil
}

func (h *Handler) printReqSummary(reqNum int, reqBody []byte) {
	var parsed struct {
		Model    string            `json:"model"`
		System   json.RawMessage   `json:"system"`
		Messages []json.RawMessage `json:"messages"`
		Tools    []json.RawMessage `json:"tools"`
	}
	json.Unmarshal(reqBody, &parsed)

	// Count messages by role
	msgCounts := map[string]int{}
	for _, m := range parsed.Messages {
		var msg struct{ Role string `json:"role"` }
		json.Unmarshal(m, &msg)
		msgCounts[msg.Role]++
	}

	// Extract tool names
	var toolNames []string
	for _, t := range parsed.Tools {
		var tool struct{ Name string `json:"name"` }
		json.Unmarshal(t, &tool)
		if tool.Name != "" {
			toolNames = append(toolNames, tool.Name)
		}
	}

	h.printer.PrintRequestSummary(reqNum, display.RequestSummary{
		Model:       parsed.Model,
		SystemLen:   len(parsed.System),
		MsgCounts:   msgCounts,
		ToolNames:   toolNames,
		EstInTokens: int64(len(reqBody) / 4), // rough estimate: ~4 chars per token
	})
}

func (h *Handler) recordAndSummarize(reqNum int, reqBody, respBody []byte, r *http.Request, status int, respHeaders http.Header, durationMs int64, isSSE bool) {
	// Build response body for record
	var finalRespBody json.RawMessage
	var stopReason string
	var inTokens, outTokens, cacheCreate, cacheRead int64
	var outputDesc string

	if isSSE {
		assembled, err := ReassembleSSEResponse(respBody)
		if err == nil {
			data, _ := json.Marshal(assembled)
			finalRespBody = data
			stopReason = assembled.StopReason
			inTokens = assembled.Usage.InputTokens
			outTokens = assembled.Usage.OutputTokens
			cacheCreate = assembled.Usage.CacheCreationTokens
			cacheRead = assembled.Usage.CacheReadTokens
			outputDesc = describeContent(assembled.Content)
		} else {
			finalRespBody = respBody
		}
	} else {
		finalRespBody = respBody
		var msg struct {
			StopReason string `json:"stop_reason"`
			Usage      struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		json.Unmarshal(respBody, &msg)
		stopReason = msg.StopReason
		inTokens = msg.Usage.InputTokens
		outTokens = msg.Usage.OutputTokens
	}

	// Print response summary
	h.printer.PrintResponseSummary(reqNum, display.ResponseSummary{
		DurationMs:  durationMs,
		OutputDesc:  outputDesc,
		StopReason:  stopReason,
		InTokens:    inTokens,
		OutTokens:   outTokens,
		CacheCreate: cacheCreate,
		CacheRead:   cacheRead,
	})

	// Accumulate stats
	h.summary.Add(display.RequestStats{
		InputTokens:  inTokens,
		OutputTokens: outTokens,
		CacheRead:    cacheRead,
		CacheCreate:  cacheCreate,
		DurationMs:   durationMs,
	})

	// Collect request headers (masked)
	reqHeaders := map[string]string{}
	for k := range r.Header {
		reqHeaders[k] = r.Header.Get(k)
	}
	respH := map[string]string{}
	for k := range respHeaders {
		respH[k] = respHeaders.Get(k)
	}

	rec := recorder.Record{
		ID:         fmt.Sprintf("req_%03d", reqNum),
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		DurationMs: durationMs,
		Request: recorder.RequestData{
			Method:  "POST",
			Path:    r.URL.Path,
			Headers: recorder.MaskHeaders(reqHeaders),
			Body:    json.RawMessage(reqBody),
		},
		Response: recorder.ResponseData{
			Status:  status,
			Headers: respH,
			Body:    finalRespBody,
		},
	}

	if err := h.recorder.Write(rec); err != nil {
		h.printer.PrintError(fmt.Sprintf("write record: %v", err))
	}
}

func describeContent(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, fmt.Sprintf("text(%d chars)", len(b.Text)))
		case "tool_use":
			parts = append(parts, fmt.Sprintf("tool_use(%s)", b.Name))
		default:
			parts = append(parts, b.Type)
		}
	}
	return strings.Join(parts, " + ")
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
```

- [ ] **Step 4: Write server.go**

Create `proxy/server.go`:

```go
package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// Server wraps the HTTP proxy server.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
	port       int
}

// NewServer creates a proxy server. If port is 0, a random available port is chosen.
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

// Start begins serving in a goroutine. Returns immediately.
func (s *Server) Start() {
	go s.httpServer.Serve(s.listener)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
```

- [ ] **Step 5: Write server test**

Create `proxy/server_test.go`:

```go
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

	// Verify it's actually listening
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(srv.BaseURL() + "/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
```

- [ ] **Step 6: Run all proxy tests**

```bash
go test ./proxy/ -v
```

Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add proxy/handler.go proxy/handler_test.go proxy/server.go proxy/server_test.go
git commit -m "feat: reverse proxy handler with SSE forwarding and recording"
```

---

### Task 6: Launcher — Child Process Manager

**Files:**
- Create: `launcher/launcher.go`
- Create: `launcher/launcher_test.go`

- [ ] **Step 1: Write launcher test**

Create `launcher/launcher_test.go`:

```go
package launcher

import (
	"os"
	"testing"
)

func TestBuildEnv(t *testing.T) {
	env := BuildEnv("http://127.0.0.1:8080", os.Environ())

	found := false
	for _, e := range env {
		if e == "ANTHROPIC_BASE_URL=http://127.0.0.1:8080" {
			found = true
		}
	}
	if !found {
		t.Error("ANTHROPIC_BASE_URL not set in env")
	}

	// Ensure old value is overwritten, not duplicated
	count := 0
	for _, e := range env {
		if len(e) > 20 && e[:20] == "ANTHROPIC_BASE_URL=" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ANTHROPIC_BASE_URL appears %d times, want 1", count)
	}
}

func TestFindClaude(t *testing.T) {
	path := FindClaude()
	if path == "" {
		t.Skip("claude-internal not found in PATH")
	}
	// Just verify the returned path is non-empty
	if _, err := os.Stat(path); err != nil {
		t.Errorf("FindClaude returned %q but stat failed: %v", path, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./launcher/ -v
```

Expected: FAIL — `BuildEnv` not defined.

- [ ] **Step 3: Implement launcher**

Create `launcher/launcher.go`:

```go
package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

// FindClaude locates the claude-internal binary.
func FindClaude() string {
	// Check CLAUDE_CODE_TEAMMATE_COMMAND env first
	if cmd := os.Getenv("CLAUDE_CODE_TEAMMATE_COMMAND"); cmd != "" {
		if _, err := os.Stat(cmd); err == nil {
			return cmd
		}
	}
	// Fall back to PATH lookup
	if path, err := exec.LookPath("claude-internal"); err == nil {
		return path
	}
	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}
	return ""
}

// BuildEnv creates environment variables with ANTHROPIC_BASE_URL overridden.
func BuildEnv(proxyURL string, currentEnv []string) []string {
	var env []string
	for _, e := range currentEnv {
		if strings.HasPrefix(e, "ANTHROPIC_BASE_URL=") {
			continue // skip — we'll add our own
		}
		env = append(env, e)
	}
	env = append(env, "ANTHROPIC_BASE_URL="+proxyURL)
	return env
}

// Launch starts claude as a child process and waits for it to exit.
// Returns the exit code.
func Launch(claudePath string, args []string, env []string) (int, error) {
	if claudePath == "" {
		return 1, fmt.Errorf("claude-internal not found; install it or set CLAUDE_CODE_TEAMMATE_COMMAND")
	}

	cmd := exec.Command(claudePath, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Forward signals to child
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				cmd.Process.Signal(sig)
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("start claude: %w", err)
	}

	err := cmd.Wait()
	signal.Stop(sigCh)
	close(sigCh)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./launcher/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add launcher/
git commit -m "feat: child process launcher with signal forwarding"
```

---

### Task 7: main.go — Orchestration

**Files:**
- Create: `main.go`

- [ ] **Step 1: Implement main.go**

Create `main.go`:

```go
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Parse claude-spy's own args (before --) and claude's args (after --)
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

	// Generate session ID
	sessionID := generateSessionID()
	logPath := filepath.Join(logDir, sessionID+".jsonl")

	// Set up components
	printer := display.NewPrinter(os.Stderr, quiet)

	rec, err := recorder.NewJSONLWriter(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: create log file: %v\n", err)
		return 1
	}
	defer rec.Close()

	summary := display.NewSummary()

	// Resolve upstream URL
	upstreamURL := os.Getenv("ANTHROPIC_BASE_URL")
	if upstreamURL == "" {
		fmt.Fprintf(os.Stderr, "Error: ANTHROPIC_BASE_URL not set\n")
		return 1
	}

	// Create handler and server
	handler := proxy.NewHandler(upstreamURL, rec, printer, summary, saveSSE)

	// Try to start server (with retries for port conflicts)
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
		port = 0 // retry on random port
	}
	srv.Start()
	defer srv.Shutdown(context.Background())

	if !quiet {
		fmt.Fprintf(os.Stderr, "[claude-spy] Proxy listening on %s\n", srv.BaseURL())
		fmt.Fprintf(os.Stderr, "[claude-spy] Logging to %s\n\n", logPath)
	}

	// Find and launch claude
	claudePath := launcher.FindClaude()
	env := launcher.BuildEnv(srv.BaseURL(), os.Environ())

	sessionStart := time.Now()
	exitCode, err := launcher.Launch(claudePath, claudeArgs, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Print session summary
	printer.PrintSessionSummary(summary, time.Since(sessionStart), logPath)

	return exitCode
}

// splitArgs splits args at "--" into spy args and claude args.
func splitArgs(args []string) (spyArgs, claudeArgs []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	// No "--" found: all args go to claude
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
```

- [ ] **Step 2: Build and verify it compiles**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go build -o claude-spy .
```

Expected: binary `claude-spy` produced with no errors.

- [ ] **Step 3: Quick smoke test (help flag)**

```bash
./claude-spy --help
```

Expected: prints usage text and exits 0.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: main entry point with CLI arg parsing and orchestration"
```

---

### Task 8: Integration Test

**Files:**
- Create: `main_test.go`

- [ ] **Step 1: Write integration test with a fake API server**

Create `main_test.go`:

```go
package main

import "testing"

func TestSplitArgs_WithSeparator(t *testing.T) {
	spy, claude := splitArgs([]string{"--quiet", "--port", "9090", "--", "--continue", "-p", "hello"})
	if len(spy) != 3 || spy[0] != "--quiet" {
		t.Errorf("spy args = %v", spy)
	}
	if len(claude) != 3 || claude[0] != "--continue" {
		t.Errorf("claude args = %v", claude)
	}
}

func TestSplitArgs_WithoutSeparator(t *testing.T) {
	spy, claude := splitArgs([]string{"--continue", "-p", "hello"})
	if len(spy) != 0 {
		t.Errorf("spy args should be empty, got %v", spy)
	}
	if len(claude) != 3 {
		t.Errorf("claude args = %v", claude)
	}
}

func TestSplitArgs_Empty(t *testing.T) {
	spy, claude := splitArgs(nil)
	if len(spy) != 0 || len(claude) != 0 {
		t.Errorf("both should be empty: spy=%v, claude=%v", spy, claude)
	}
}

func TestGenerateSessionID(t *testing.T) {
	id := generateSessionID()
	if len(id) < 20 {
		t.Errorf("session ID too short: %q", id)
	}
	// Should contain date portion
	if id[8] != '_' {
		t.Errorf("session ID format unexpected: %q", id)
	}
}
```

- [ ] **Step 2: Run full test suite**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go test ./... -v
```

Expected: all tests PASS across all packages.

- [ ] **Step 3: Commit**

```bash
git add main_test.go
git commit -m "test: integration tests for arg parsing and session ID generation"
```

---

### Task 9: End-to-End Validation

- [ ] **Step 1: Build final binary**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go build -o claude-spy .
```

- [ ] **Step 2: Run with claude (real test)**

```bash
# Test with print mode for a quick round-trip
./claude-spy -- -p "say hello in one word"
```

Expected:
- Stderr shows REQ/RES summaries
- Claude responds normally on stdout
- Session summary printed at end
- JSONL file created in `~/.claude-spy/logs/`

- [ ] **Step 3: Verify JSONL content**

```bash
ls -la ~/.claude-spy/logs/
# Read the latest log
cat ~/.claude-spy/logs/$(ls -t ~/.claude-spy/logs/ | head -1) | python3 -m json.tool | head -60
```

Expected: valid JSON with full `request.body` (including system prompt, messages, tools) and `response.body`.

- [ ] **Step 4: Final commit**

```bash
git add -A
git commit -m "feat: claude-spy v1.0 — Claude Code API interaction interceptor"
```

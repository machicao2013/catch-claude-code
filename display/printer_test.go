package display

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintRequestSummary(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, false)

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
	p := NewPrinter(&buf, true)

	p.PrintRequestSummary(1, RequestSummary{Model: "test"})
	p.PrintResponseSummary(1, ResponseSummary{})

	if buf.Len() != 0 {
		t.Errorf("quiet mode should produce no output, got %d bytes", buf.Len())
	}
}

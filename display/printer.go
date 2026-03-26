package display

import (
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorBlue   = "\033[34m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
)

type RequestSummary struct {
	Model       string
	SystemLen   int
	MsgCounts   map[string]int
	ToolNames   []string
	EstInTokens int64
}

type ResponseSummary struct {
	DurationMs  int64
	OutputDesc  string
	StopReason  string
	InTokens    int64
	OutTokens   int64
	CacheCreate int64
	CacheRead   int64
}

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

	totalMsgs := 0
	var parts []string
	for role, count := range s.MsgCounts {
		totalMsgs += count
		parts = append(parts, fmt.Sprintf("%s:%d", role, count))
	}
	msgDesc := fmt.Sprintf("%d 条 (%s)", totalMsgs, strings.Join(parts, ", "))

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

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

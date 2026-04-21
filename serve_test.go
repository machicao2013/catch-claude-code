package main

import (
	"testing"
)

func TestParseServeArgs_Defaults(t *testing.T) {
	cfg := parseServeArgs([]string{})
	if cfg.port != 8888 {
		t.Errorf("default port: want 8888, got %d", cfg.port)
	}
	if cfg.logDir == "" {
		t.Error("logDir should not be empty")
	}
}

func TestParseServeArgs_Custom(t *testing.T) {
	cfg := parseServeArgs([]string{"--port", "7777", "--log-dir", "/tmp/logs"})
	if cfg.port != 7777 {
		t.Errorf("want 7777, got %d", cfg.port)
	}
	if cfg.logDir != "/tmp/logs" {
		t.Errorf("want /tmp/logs, got %s", cfg.logDir)
	}
}

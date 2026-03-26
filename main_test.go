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
	if id[8] != '_' {
		t.Errorf("session ID format unexpected: %q", id)
	}
}

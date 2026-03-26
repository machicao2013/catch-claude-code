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

	count := 0
	for _, e := range env {
		if len(e) > 19 && e[:19] == "ANTHROPIC_BASE_URL=" {
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
	if _, err := os.Stat(path); err != nil {
		t.Errorf("FindClaude returned %q but stat failed: %v", path, err)
	}
}

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

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

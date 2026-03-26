package display

import "sync"

type RequestStats struct {
	InputTokens  int64
	OutputTokens int64
	CacheRead    int64
	CacheCreate  int64
	DurationMs   int64
}

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

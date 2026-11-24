package gateway

// Mirrors 9router-go's StreamMetrics: the console records TTFT, the widest gap
// between streamed chunks, and the first/last chunk bracket that turns the
// token count into tok/s. None of it touches the SSE wire.

import (
	"io"
	"sync"
	"time"
)

type streamStats struct {
	mu    sync.Mutex
	first time.Time
	last  time.Time
	gap   time.Duration
}

// note records one chunk written to the client. A nil receiver is a no-op so
// non-streaming call sites can pass nil.
func (s *streamStats) note() {
	if s == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.first.IsZero() {
		s.first = now
	} else if d := now.Sub(s.last); d > s.gap {
		s.gap = d
	}
	s.last = now
}

func (s *streamStats) ttftMs(start time.Time) int64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.first.IsZero() {
		return 0
	}
	return s.first.Sub(start).Milliseconds()
}

func (s *streamStats) maxGapMs() int64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gap.Milliseconds()
}

func (s *streamStats) tokPerSec(tokens int64) float64 {
	if s == nil || tokens == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := s.last.Sub(s.first)
	if s.first.IsZero() || elapsed <= 0 {
		return 0
	}
	return float64(tokens) / elapsed.Seconds()
}

// statsWriter notes every chunk the stream path forwards.
type statsWriter struct {
	w io.Writer
	s *streamStats
}

func (w statsWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		w.s.note()
	}
	return n, err
}

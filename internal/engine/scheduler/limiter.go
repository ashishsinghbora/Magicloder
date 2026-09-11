package scheduler

import (
	"context"
	"io"
	"sync"
	"time"
)

// Limiter implements a thread-safe token-bucket rate limiter for bandwidth throttling.
type Limiter struct {
	mu           sync.Mutex
	rateBytesPerSec int64
	tokens       float64
	lastRefill   time.Time
}

// NewLimiter creates a Limiter with a given bytes/second limit. If 0, throttling is disabled.
func NewLimiter(rateBytesPerSec int64) *Limiter {
	return &Limiter{
		rateBytesPerSec: rateBytesPerSec,
		tokens:          float64(rateBytesPerSec),
		lastRefill:      time.Now(),
	}
}

// SetRate updates the rate dynamically.
func (l *Limiter) SetRate(rateBytesPerSec int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rateBytesPerSec = rateBytesPerSec
	l.tokens = float64(rateBytesPerSec)
	l.lastRefill = time.Now()
}

// Wait blocks until n bytes are permitted to be transferred or the context is cancelled.
func (l *Limiter) Wait(ctx context.Context, n int) error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	if l.rateBytesPerSec <= 0 {
		l.mu.Unlock()
		return nil
	}

	for {
		if ctx.Err() != nil {
			l.mu.Unlock()
			return ctx.Err()
		}

		now := time.Now()
		elapsed := now.Sub(l.lastRefill).Seconds()
		l.lastRefill = now

		l.tokens += elapsed * float64(l.rateBytesPerSec)
		capacity := float64(l.rateBytesPerSec)
		if l.tokens > capacity {
			l.tokens = capacity
		}

		needed := float64(n)
		if l.tokens >= needed {
			l.tokens -= needed
			l.mu.Unlock()
			return nil
		}

		// Calculate sleep duration needed for remaining tokens
		deficit := needed - l.tokens
		sleepSec := deficit / float64(l.rateBytesPerSec)
		sleepDur := time.Duration(sleepSec * float64(time.Second))
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepDur):
		}

		l.mu.Lock()
	}
}

// ThrottledReader wraps an io.Reader, pacing reads through the Limiter.
type ThrottledReader struct {
	r       io.Reader
	limiter *Limiter
	ctx     context.Context
}

// NewThrottledReader wraps an io.Reader with bandwidth limiting.
func NewThrottledReader(ctx context.Context, r io.Reader, limiter *Limiter) io.Reader {
	if limiter == nil || limiter.rateBytesPerSec <= 0 {
		return r
	}
	return &ThrottledReader{
		r:       r,
		limiter: limiter,
		ctx:     ctx,
	}
}

func (tr *ThrottledReader) Read(p []byte) (int, error) {
	n, err := tr.r.Read(p)
	if n > 0 && tr.limiter != nil {
		if wErr := tr.limiter.Wait(tr.ctx, n); wErr != nil {
			return n, wErr
		}
	}
	return n, err
}

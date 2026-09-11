package scheduler_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/engine/scheduler"
)

func TestLimiterWait(t *testing.T) {
	// 1000 bytes per second
	limiter := scheduler.NewLimiter(1000)

	ctx := context.Background()

	// Initial burst
	start := time.Now()
	if err := limiter.Wait(ctx, 1000); err != nil {
		t.Fatalf("first wait failed: %v", err)
	}

	// Next 500 bytes should wait ~500ms
	if err := limiter.Wait(ctx, 500); err != nil {
		t.Fatalf("second wait failed: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < 350*time.Millisecond {
		t.Fatalf("expected throttling delay, but elapsed only %v", elapsed)
	}
}

func TestThrottledReader(t *testing.T) {
	data := make([]byte, 3000)
	limiter := scheduler.NewLimiter(2000) // 2000 B/s

	r := scheduler.NewThrottledReader(context.Background(), bytes.NewReader(data), limiter)

	buf := make([]byte, 1000)
	start := time.Now()

	// Read 1000
	_, _ = io.ReadFull(r, buf)
	// Read 1000 (consumes 2000 initial burst)
	_, _ = io.ReadFull(r, buf)
	// Read 1000 (must throttle for ~500ms)
	n3, err3 := io.ReadFull(r, buf)
	if err3 != nil || n3 != 1000 {
		t.Fatalf("read 3 failed: n=%d, err=%v", n3, err3)
	}

	elapsed := time.Since(start)
	if elapsed < 350*time.Millisecond {
		t.Fatalf("expected throttling, got elapsed %v", elapsed)
	}
}

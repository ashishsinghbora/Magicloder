package scheduler_test

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/config"
	"github.com/ashishsinghbora/magicloder/internal/engine/downloader"
	"github.com/ashishsinghbora/magicloder/internal/engine/scheduler"
	"github.com/ashishsinghbora/magicloder/internal/model"
	"github.com/ashishsinghbora/magicloder/internal/storage"
)

func createTestScheduler(t *testing.T, maxConcurrent int) (*scheduler.Scheduler, storage.Store, string, func()) {
	tempDir, err := os.MkdirTemp("", "magicloder-sched-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.DataDir = tempDir
	cfg.DownloadDir = tempDir
	cfg.MaxConcurrentDownloads = maxConcurrent

	store, err := storage.Open(filepath.Join(tempDir, "sched.db"))
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}

	sched := scheduler.New(cfg, store, downloader.NewProber(nil))
	if err := sched.Start(); err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	cleanup := func() {
		sched.Stop()
		_ = store.Close()
		_ = os.RemoveAll(tempDir)
	}

	return sched, store, tempDir, cleanup
}

func TestSchedulerConcurrencyCap(t *testing.T) {
	payload := make([]byte, 10*1024)
	_, _ = rand.Read(payload)

	var activeConcurrent int32
	var maxObserved int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&activeConcurrent, 1)
		defer atomic.AddInt32(&activeConcurrent, -1)

		for {
			old := atomic.LoadInt32(&maxObserved)
			if cur <= old || atomic.CompareAndSwapInt32(&maxObserved, old, cur) {
				break
			}
		}

		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		time.Sleep(50 * time.Millisecond) // Ensure overlap
		_, _ = w.Write(payload)
	}))
	defer ts.Close()

	sched, _, tempDir, cleanup := createTestScheduler(t, 2)
	defer cleanup()

	var completedCount int32
	sched.AddListener(func(event string, dl *model.Download) {
		if event == "job_completed" {
			atomic.AddInt32(&completedCount, 1)
		}
	})

	// Submit 4 jobs
	for i := 0; i < 4; i++ {
		dl := &model.Download{
			ID:            fmt.Sprintf("dl-conc-%d", i),
			URL:           fmt.Sprintf("%s/file-%d.bin", ts.URL, i),
			Destination:   filepath.Join(tempDir, fmt.Sprintf("file-%d.bin", i)),
			TemporaryPath: filepath.Join(tempDir, fmt.Sprintf("file-%d.bin.part", i)),
			TotalSize:     int64(len(payload)),
			Priority:      1,
		}
		if err := sched.Submit(dl); err != nil {
			t.Fatalf("submit failed: %v", err)
		}
	}

	// Wait for all 4 to complete
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&completedCount) < 4 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if atomic.LoadInt32(&completedCount) != 4 {
		t.Fatalf("expected 4 completed jobs, got %d", completedCount)
	}

	if atomic.LoadInt32(&maxObserved) > 2 {
		t.Fatalf("max observed concurrency was %d, expected at most 2", maxObserved)
	}
}

func TestSchedulerDuplicatePrevention(t *testing.T) {
	sched, _, tempDir, cleanup := createTestScheduler(t, 2)
	defer cleanup()

	dl1 := &model.Download{
		ID:            "dl-dup-1",
		URL:           "https://example.com/unique.iso",
		Destination:   filepath.Join(tempDir, "unique.iso"),
		TemporaryPath: filepath.Join(tempDir, "unique.iso.part"),
	}
	if err := sched.Submit(dl1); err != nil {
		t.Fatalf("first submit failed: %v", err)
	}

	// Submit same URL
	dl2 := &model.Download{
		ID:            "dl-dup-2",
		URL:           "https://example.com/unique.iso",
		Destination:   filepath.Join(tempDir, "unique2.iso"),
		TemporaryPath: filepath.Join(tempDir, "unique2.iso.part"),
	}
	if err := sched.Submit(dl2); err == nil {
		t.Fatalf("expected ErrDuplicateJob on duplicate URL, got nil")
	}

	// Submit same destination
	dl3 := &model.Download{
		ID:            "dl-dup-3",
		URL:           "https://example.com/other.iso",
		Destination:   filepath.Join(tempDir, "unique.iso"),
		TemporaryPath: filepath.Join(tempDir, "other.iso.part"),
	}
	if err := sched.Submit(dl3); err == nil {
		t.Fatalf("expected ErrDuplicateJob on duplicate destination, got nil")
	}
}

func TestSchedulerPauseAndResume(t *testing.T) {
	payload := make([]byte, 50*1024)
	_, _ = rand.Read(payload)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"etag-pause-test"`)

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}

		rangeHeader = strings.TrimPrefix(rangeHeader, "bytes=")
		parts := strings.Split(rangeHeader, "-")
		start, _ := strconv.ParseInt(parts[0], 10, 64)
		end, _ := strconv.ParseInt(parts[1], 10, 64)

		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)

		flusher, _ := w.(http.Flusher)
		// Send slowly to allow pausing
		chunkSize := 1024
		for curr := start; curr <= end; curr += int64(chunkSize) {
			chunkEnd := curr + int64(chunkSize)
			if chunkEnd > end+1 {
				chunkEnd = end + 1
			}
			_, _ = w.Write(payload[curr:chunkEnd])
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(15 * time.Millisecond)
		}
	}))
	defer ts.Close()

	sched, _, tempDir, cleanup := createTestScheduler(t, 2)
	defer cleanup()

	dl := &model.Download{
		ID:             "dl-pause-test",
		URL:            ts.URL + "/slow.bin",
		Destination:    filepath.Join(tempDir, "slow.bin"),
		TemporaryPath:  filepath.Join(tempDir, "slow.bin.part"),
		TotalSize:      int64(len(payload)),
		Priority:       1,
		MaxConnections: 2,
	}

	if err := sched.Submit(dl); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait briefly for download to start
	time.Sleep(30 * time.Millisecond)

	// Pause
	if err := sched.Pause(dl.ID); err != nil {
		t.Fatalf("pause failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Resume
	if err := sched.Resume(dl.ID); err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	// Wait for completion
	deadline := time.Now().Add(5 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dl.Destination); err == nil {
			completed = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !completed {
		t.Fatalf("resumed download did not complete in time")
	}

	downloaded, err := os.ReadFile(dl.Destination)
	if err != nil || len(downloaded) != len(payload) {
		t.Fatalf("downloaded file corrupted or invalid length: %d/%d", len(downloaded), len(payload))
	}
}

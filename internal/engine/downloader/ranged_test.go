package downloader_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
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

	"github.com/ashishsinghbora/magicloder/internal/engine/chunk"
	"github.com/ashishsinghbora/magicloder/internal/engine/downloader"
)

// rangeTestServer serves byte-range requests accurately.
func rangeTestServer(payload []byte, concurrentCounter *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		total := int64(len(payload))

		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
			w.WriteHeader(http.StatusOK)
			return
		}

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}

		// Track concurrency
		if concurrentCounter != nil {
			atomic.AddInt32(concurrentCounter, 1)
			defer atomic.AddInt32(concurrentCounter, -1)
		}

		// Parse "bytes=START-END"
		rangeHeader = strings.TrimPrefix(rangeHeader, "bytes=")
		parts := strings.Split(rangeHeader, "-")
		if len(parts) != 2 {
			http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
			return
		}

		start, err1 := strconv.ParseInt(parts[0], 10, 64)
		end, err2 := strconv.ParseInt(parts[1], 10, 64)
		if err1 != nil || err2 != nil || start < 0 || end >= total || start > end {
			http.Error(w, "invalid range bounds", http.StatusRequestedRangeNotSatisfiable)
			return
		}

		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)

		// Small delay to ensure concurrency overlap during test
		time.Sleep(5 * time.Millisecond)
		_, _ = w.Write(payload[start : end+1])
	}))
}

func TestRangedDownloadSuccess(t *testing.T) {
	payloadSize := int64(512 * 1024) // 512 KiB
	payload := make([]byte, payloadSize)
	_, _ = rand.Read(payload)
	expectedHash := sha256.Sum256(payload)

	var maxConcurrent int32
	var currentConcurrent int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&currentConcurrent, 1)
		defer atomic.AddInt32(&currentConcurrent, -1)

		for {
			oldMax := atomic.LoadInt32(&maxConcurrent)
			if cur <= oldMax || atomic.CompareAndSwapInt32(&maxConcurrent, oldMax, cur) {
				break
			}
		}

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
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
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "magicloder-ranged-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempPath := filepath.Join(tempDir, "ranged.bin.part")
	finalPath := filepath.Join(tempDir, "ranged.bin")

	chunks, err := chunk.PlanChunks(payloadSize, 4, 64*1024)
	if err != nil {
		t.Fatalf("failed to plan chunks: %v", err)
	}

	opts := downloader.DefaultRangedOptions()
	opts.Client = ts.Client()
	opts.MaxWorkers = 4

	err = downloader.DownloadRanged(context.Background(), ts.URL, tempPath, finalPath, payloadSize, chunks, opts)
	if err != nil {
		t.Fatalf("DownloadRanged failed: %v", err)
	}

	// Verify temp file was removed
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("tempPath must not exist after success")
	}

	// Verify content and hash
	downloaded, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	actualHash := sha256.Sum256(downloaded)
	if actualHash != expectedHash {
		t.Fatalf("SHA256 hash mismatch! Got %x, expected %x", actualHash, expectedHash)
	}

	if atomic.LoadInt32(&maxConcurrent) < 2 {
		t.Logf("Warning: max concurrency observed was %d", maxConcurrent)
	}
}

func TestRangedDownloadFallbackOn200(t *testing.T) {
	payload := []byte("Server that ignores range requests completely")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 200 OK to range request
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "magicloder-fallback-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempPath := filepath.Join(tempDir, "fallback.bin.part")
	finalPath := filepath.Join(tempDir, "fallback.bin")

	chunks, _ := chunk.PlanChunks(int64(len(payload)), 2, 1)

	opts := downloader.DefaultRangedOptions()
	opts.Client = ts.Client()

	err = downloader.DownloadRanged(context.Background(), ts.URL, tempPath, finalPath, int64(len(payload)), chunks, opts)
	if !errors.Is(err, downloader.ErrFallbackToSingleStream) {
		t.Fatalf("expected ErrFallbackToSingleStream, got %v", err)
	}
}

func TestRangedDownloadTransientRetry(t *testing.T) {
	payload := []byte("Testing transient network retries with backoff")
	var attempts int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		if count <= 2 {
			// Fail first 2 attempts
			http.Error(w, "Temporary Gateway Error", http.StatusBadGateway)
			return
		}

		rangeHeader := r.Header.Get("Range")
		rangeHeader = strings.TrimPrefix(rangeHeader, "bytes=")
		parts := strings.Split(rangeHeader, "-")
		start, _ := strconv.ParseInt(parts[0], 10, 64)
		end, _ := strconv.ParseInt(parts[1], 10, 64)

		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "magicloder-retry-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempPath := filepath.Join(tempDir, "retry.bin.part")
	finalPath := filepath.Join(tempDir, "retry.bin")

	chunks, _ := chunk.PlanChunks(int64(len(payload)), 1, 1)

	opts := downloader.DefaultRangedOptions()
	opts.Client = ts.Client()
	opts.MaxRetries = 3
	opts.BaseBackoff = 5 * time.Millisecond

	err = downloader.DownloadRanged(context.Background(), ts.URL, tempPath, finalPath, int64(len(payload)), chunks, opts)
	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}

	saved, _ := os.ReadFile(finalPath)
	if !bytes.Equal(saved, payload) {
		t.Fatalf("payload content mismatch")
	}
}

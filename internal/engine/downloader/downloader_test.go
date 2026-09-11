package downloader_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/engine/downloader"
)

func TestProbeStandard(t *testing.T) {
	payload := []byte("Hello, Magicloder!")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"test-etag-123"`)
		w.Header().Set("Content-Disposition", `attachment; filename="hello.txt"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer ts.Close()

	prober := downloader.NewProber(ts.Client())
	res, err := prober.Probe(context.Background(), ts.URL+"/resource")
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	if res.ContentLength != int64(len(payload)) {
		t.Fatalf("expected ContentLength %d, got %d", len(payload), res.ContentLength)
	}
	if !res.SupportsRange {
		t.Fatalf("expected SupportsRange true")
	}
	if res.ETag != "test-etag-123" {
		t.Fatalf("expected etag test-etag-123, got %s", res.ETag)
	}
	if res.SuggestedFilename != "hello.txt" {
		t.Fatalf("expected hello.txt, got %s", res.SuggestedFilename)
	}
}

func TestProbeHeadFallbackToGet(t *testing.T) {
	payload := []byte("Ranged payload data")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			// Server explicitly rejects HEAD
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodGet {
			if r.Header.Get("Range") == "bytes=0-0" {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte{payload[0]})
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		}
	}))
	defer ts.Close()

	prober := downloader.NewProber(ts.Client())
	res, err := prober.Probe(context.Background(), ts.URL+"/file.bin")
	if err != nil {
		t.Fatalf("Probe failed on HEAD fallback: %v", err)
	}

	if res.ContentLength != int64(len(payload)) {
		t.Fatalf("expected ContentLength %d, got %d", len(payload), res.ContentLength)
	}
	if !res.SupportsRange {
		t.Fatalf("expected SupportsRange true from Content-Range")
	}
	if res.SuggestedFilename != "file.bin" {
		t.Fatalf("expected suggested filename file.bin, got %s", res.SuggestedFilename)
	}
}

func TestProbeRedirect(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/target/archive.tar.gz", http.StatusFound)
			return
		}
		w.Header().Set("Content-Length", "500")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	prober := downloader.NewProber(ts.Client())
	res, err := prober.Probe(context.Background(), ts.URL+"/redirect")
	if err != nil {
		t.Fatalf("Probe failed on redirect: %v", err)
	}

	if res.SuggestedFilename != "archive.tar.gz" {
		t.Fatalf("expected archive.tar.gz after redirect, got %s", res.SuggestedFilename)
	}
}

func TestSingleStreamDownloadSuccess(t *testing.T) {
	payload := make([]byte, 256*1024) // 256 KiB
	_, _ = rand.Read(payload)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "magicloder-stream-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempPath := filepath.Join(tempDir, "download.bin.part")
	finalPath := filepath.Join(tempDir, "download.bin")

	var reportedBytes int64
	opts := downloader.DefaultSingleStreamOptions()
	opts.Client = ts.Client()
	opts.OnProgress = func(completed, total int64) {
		atomic.StoreInt64(&reportedBytes, completed)
	}

	err = downloader.DownloadSingleStream(context.Background(), ts.URL, tempPath, finalPath, int64(len(payload)), opts)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	// Verify temp file does not exist (was renamed atomically)
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("tempPath still exists after finalization")
	}

	// Verify final file
	saved, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("failed to read final file: %v", err)
	}
	if !bytes.Equal(saved, payload) {
		t.Fatalf("saved content does not match source")
	}
	if atomic.LoadInt64(&reportedBytes) != int64(len(payload)) {
		t.Fatalf("expected %d progress bytes, got %d", len(payload), reportedBytes)
	}
}

func TestSingleStreamCancellationSafety(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 100; i++ {
			_, _ = w.Write(bytes.Repeat([]byte("A"), 1024))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "magicloder-cancel-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempPath := filepath.Join(tempDir, "cancel.bin.part")
	finalPath := filepath.Join(tempDir, "cancel.bin")

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	opts := downloader.DefaultSingleStreamOptions()
	opts.Client = ts.Client()

	err = downloader.DownloadSingleStream(ctx, ts.URL, tempPath, finalPath, -1, opts)
	if err == nil {
		t.Fatalf("expected error due to cancellation, got nil")
	}

	// Verify final file NEVER exists on abort
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("finalPath must NOT exist after cancellation")
	}

	// Part file must exist with partial data
	if fi, err := os.Stat(tempPath); err != nil || fi.Size() == 0 {
		t.Fatalf("temp .part file should exist with partial data")
	}
}

func TestSingleStreamOverwriteProtection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("new data"))
	}))
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "magicloder-overwrite-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	finalPath := filepath.Join(tempDir, "exists.txt")
	tempPath := filepath.Join(tempDir, "exists.txt.part")
	_ = os.WriteFile(finalPath, []byte("existing"), 0644)

	opts := downloader.DefaultSingleStreamOptions()
	opts.Client = ts.Client()
	opts.Overwrite = false

	err = downloader.DownloadSingleStream(context.Background(), ts.URL, tempPath, finalPath, 8, opts)
	if !errors.Is(err, downloader.ErrFileExists) {
		t.Fatalf("expected ErrFileExists, got %v", err)
	}
}

package downloader_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/engine/chunk"
	"github.com/ashishsinghbora/magicloder/internal/engine/downloader"
	"github.com/ashishsinghbora/magicloder/internal/model"
	"github.com/ashishsinghbora/magicloder/internal/storage"
)

func TestResumableRecoveryOnlyMissingRanges(t *testing.T) {
	fileSize := int64(256 * 1024) // 256 KiB
	sourcePayload := make([]byte, fileSize)
	_, _ = rand.Read(sourcePayload)
	expectedHash := sha256.Sum256(sourcePayload)

	var requestedRangesMu sync.Mutex
	var requestedRanges [][2]int64

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"stable-etag-1"`)

		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))
			w.WriteHeader(http.StatusOK)
			return
		}

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(sourcePayload)
			return
		}

		rangeHeader = strings.TrimPrefix(rangeHeader, "bytes=")
		parts := strings.Split(rangeHeader, "-")
		start, _ := strconv.ParseInt(parts[0], 10, 64)
		end, _ := strconv.ParseInt(parts[1], 10, 64)

		requestedRangesMu.Lock()
		requestedRanges = append(requestedRanges, [2]int64{start, end})
		requestedRangesMu.Unlock()

		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(sourcePayload[start : end+1])
	}))
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "magicloder-recovery-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "recovery.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Initial planning: 4 chunks
	plannedChunks, err := chunk.PlanChunks(fileSize, 4, 16*1024)
	if err != nil {
		t.Fatalf("failed to plan chunks: %v", err)
	}

	dlID := "dl-recovery-test"
	tempPath := filepath.Join(tempDir, "recovery.bin.part")
	finalPath := filepath.Join(tempDir, "recovery.bin")

	for i := range plannedChunks {
		plannedChunks[i].DownloadID = dlID
	}

	dl := &model.Download{
		ID:             dlID,
		URL:            ts.URL,
		Destination:    finalPath,
		TemporaryPath:  tempPath,
		TotalSize:      fileSize,
		CompletedBytes: 0,
		Status:         model.StatusDownloading,
		ETag:           "stable-etag-1",
		SupportsRange:  true,
		MaxConnections: 4,
		Priority:       1,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		Chunks:         plannedChunks,
	}

	if err := store.CreateDownload(ctx, dl); err != nil {
		t.Fatalf("failed to create download in DB: %v", err)
	}
	if err := store.SaveChunks(ctx, plannedChunks); err != nil {
		t.Fatalf("failed to save chunks: %v", err)
	}

	// Pre-allocate temp file
	partFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}
	_ = partFile.Truncate(fileSize)

	// Simulate that Chunk 0 and Chunk 1 were already downloaded before the crash:
	// Chunk 0: [0, 65535], Chunk 1: [65536, 131071]
	c0 := plannedChunks[0]
	c1 := plannedChunks[1]

	_, _ = partFile.WriteAt(sourcePayload[c0.StartByte:c0.EndByte+1], c0.StartByte)
	_, _ = partFile.WriteAt(sourcePayload[c1.StartByte:c1.EndByte+1], c1.StartByte)
	_ = partFile.Sync()
	_ = partFile.Close()

	plannedChunks[0].CompletedBytes = c0.TotalChunkBytes()
	plannedChunks[0].Status = model.ChunkCompleted
	plannedChunks[1].CompletedBytes = c1.TotalChunkBytes()
	plannedChunks[1].Status = model.ChunkCompleted

	if err := store.BatchUpdateChunks(ctx, dlID, plannedChunks); err != nil {
		t.Fatalf("failed to update completed chunks in DB: %v", err)
	}

	// Reset request tracking
	requestedRangesMu.Lock()
	requestedRanges = nil
	requestedRangesMu.Unlock()

	// --- SIMULATE DAEMON RESTART AND RECOVERY ---
	recoveredDL, err := store.GetDownload(ctx, dlID)
	if err != nil {
		t.Fatalf("failed to recover download from DB: %v", err)
	}

	// Probe remote server
	prober := downloader.NewProber(ts.Client())
	probeRes, err := prober.Probe(ctx, recoveredDL.URL)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}

	// Reconcile
	recResult, err := downloader.ReconcileDownload(ctx, recoveredDL, probeRes, store)
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}

	if !recResult.CanResume {
		t.Fatalf("expected CanResume to be true, got false")
	}
	expectedMissing := fileSize - (c0.TotalChunkBytes() + c1.TotalChunkBytes())
	if recResult.MissingBytes != expectedMissing {
		t.Fatalf("expected missing bytes %d, got %d", expectedMissing, recResult.MissingBytes)
	}

	// Resume download using reconciled chunks
	opts := downloader.DefaultRangedOptions()
	opts.Client = ts.Client()
	opts.MaxWorkers = 4

	err = downloader.DownloadRanged(ctx, recoveredDL.URL, tempPath, finalPath, fileSize, recoveredDL.Chunks, opts)
	if err != nil {
		t.Fatalf("resumed download failed: %v", err)
	}

	// Verify only missing chunks (Chunk 2 and Chunk 3) were requested from server
	requestedRangesMu.Lock()
	ranges := requestedRanges
	requestedRangesMu.Unlock()

	if len(ranges) != 2 {
		t.Fatalf("expected exactly 2 range requests for uncompleted chunks, got %d: %+v", len(ranges), ranges)
	}

	c2 := plannedChunks[2]
	c3 := plannedChunks[3]

	foundC2 := false
	foundC3 := false
	for _, r := range ranges {
		if r[0] == c2.StartByte && r[1] == c2.EndByte {
			foundC2 = true
		}
		if r[0] == c3.StartByte && r[1] == c3.EndByte {
			foundC3 = true
		}
	}

	if !foundC2 || !foundC3 {
		t.Fatalf("expected chunks 2 and 3 requested, got: %+v", ranges)
	}

	// Verify final file byte content and sha256
	finalData, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("failed to read final file: %v", err)
	}
	actualHash := sha256.Sum256(finalData)
	if actualHash != expectedHash {
		t.Fatalf("final hash does not match original! Got %x, want %x", actualHash, expectedHash)
	}
}

func TestResumableRecoveryRemoteETagChanged(t *testing.T) {
	fileSize := int64(64 * 1024)
	sourcePayload := make([]byte, fileSize)
	_, _ = rand.Read(sourcePayload)

	var currentETag atomic.Value
	currentETag.Store(`"etag-v1"`)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", currentETag.Load().(string))
		w.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(sourcePayload)
	}))
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "magicloder-etag-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "etag.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	dlID := "dl-etag-test"
	tempPath := filepath.Join(tempDir, "etag.bin.part")
	finalPath := filepath.Join(tempDir, "etag.bin")

	// Create partial file on disk
	_ = os.WriteFile(tempPath, []byte("stale-partial-bytes"), 0644)

	dl := &model.Download{
		ID:             dlID,
		URL:            ts.URL,
		Destination:    finalPath,
		TemporaryPath:  tempPath,
		TotalSize:      fileSize,
		CompletedBytes: 19,
		Status:         model.StatusDownloading,
		ETag:           "etag-v1",
		SupportsRange:  true,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	_ = store.CreateDownload(ctx, dl)

	// Remote file changes to etag-v2
	currentETag.Store(`"etag-v2"`)

	prober := downloader.NewProber(ts.Client())
	probeRes, err := prober.Probe(ctx, dl.URL)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}

	recResult, err := downloader.ReconcileDownload(ctx, dl, probeRes, store)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if !recResult.RemoteChanged {
		t.Fatalf("expected RemoteChanged to be true")
	}

	// Verify stale .part file was deleted
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("stale tempPath was not removed after remote change")
	}

	// Verify DB completed_bytes reset to 0
	updatedDL, _ := store.GetDownload(ctx, dlID)
	if updatedDL.CompletedBytes != 0 {
		t.Fatalf("expected completed bytes to reset to 0, got %d", updatedDL.CompletedBytes)
	}
	if updatedDL.ETag != "etag-v2" {
		t.Fatalf("expected etag updated to etag-v2, got %s", updatedDL.ETag)
	}
}

package uploader_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/engine/uploader"
	"github.com/ashishsinghbora/magicloder/internal/model"
	"github.com/ashishsinghbora/magicloder/internal/storage"
)

// mockTusServer simulates a Tus 1.0.0 server.
type mockTusServer struct {
	mu           sync.Mutex
	uploads      map[string]*mockUpload
	nextID       int64
	patchCalls   int32
	serverURL    string
}

type mockUpload struct {
	totalSize int64
	data      []byte
	metadata  string
}

func newMockTusServer() (*httptest.Server, *mockTusServer) {
	mts := &mockTusServer{
		uploads: make(map[string]*mockUpload),
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Tus-Resumable", "1.0.0")

		switch r.Method {
		case http.MethodPost: // Creation
			lengthStr := r.Header.Get("Upload-Length")
			totalSize, _ := strconv.ParseInt(lengthStr, 10, 64)

			mts.mu.Lock()
			mts.nextID++
			uploadID := fmt.Sprintf("upload-%d", mts.nextID)
			mts.uploads[uploadID] = &mockUpload{
				totalSize: totalSize,
				data:      make([]byte, 0, totalSize),
				metadata:  r.Header.Get("Upload-Metadata"),
			}
			mts.mu.Unlock()

			w.Header().Set("Location", mts.serverURL+"/files/"+uploadID)
			w.WriteHeader(http.StatusCreated)

		case http.MethodHead: // Offset Discovery
			uploadID := filepath.Base(r.URL.Path)
			mts.mu.Lock()
			up, ok := mts.uploads[uploadID]
			mts.mu.Unlock()

			if !ok {
				http.NotFound(w, r)
				return
			}

			w.Header().Set("Upload-Offset", strconv.Itoa(len(up.data)))
			w.Header().Set("Upload-Length", strconv.FormatInt(up.totalSize, 10))
			w.WriteHeader(http.StatusOK)

		case http.MethodPatch: // Chunk Upload
			atomic.AddInt32(&mts.patchCalls, 1)
			uploadID := filepath.Base(r.URL.Path)

			mts.mu.Lock()
			up, ok := mts.uploads[uploadID]
			mts.mu.Unlock()

			if !ok {
				http.NotFound(w, r)
				return
			}

			offsetStr := r.Header.Get("Upload-Offset")
			offset, _ := strconv.Atoi(offsetStr)

			mts.mu.Lock()
			if offset != len(up.data) {
				mts.mu.Unlock()
				http.Error(w, "Offset mismatch", http.StatusConflict)
				return
			}

			chunkData, _ := io.ReadAll(r.Body)
			up.data = append(up.data, chunkData...)
			newOffset := len(up.data)
			mts.mu.Unlock()

			w.Header().Set("Upload-Offset", strconv.Itoa(newOffset))
			w.WriteHeader(http.StatusNoContent)

		case http.MethodDelete: // Termination
			uploadID := filepath.Base(r.URL.Path)
			mts.mu.Lock()
			delete(mts.uploads, uploadID)
			mts.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	ts := httptest.NewServer(handler)
	mts.serverURL = ts.URL
	return ts, mts
}

func TestTusUploadComplete(t *testing.T) {
	ts, mts := newMockTusServer()
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "magicloder-tus-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test file (256 KiB)
	testPayload := make([]byte, 256*1024)
	_, _ = rand.Read(testPayload)
	filePath := filepath.Join(tempDir, "sample.bin")
	if err := os.WriteFile(filePath, testPayload, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	store, _ := storage.Open(filepath.Join(tempDir, "tus.db"))
	defer store.Close()

	client := uploader.NewTusClient(store)
	up := &model.Upload{
		ID:          "up-complete-test",
		SourcePath:  filePath,
		TusEndpoint: ts.URL + "/files",
		Metadata:    "filename sample.bin",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	opts := uploader.DefaultTusOptions()
	opts.Client = ts.Client()
	opts.ChunkSize = 64 * 1024 // 64 KiB chunks -> 4 PATCH calls

	err = client.UploadFile(context.Background(), up, opts)
	if err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	if up.Status != model.UploadCompleted {
		t.Fatalf("expected status completed, got %s", up.Status)
	}
	if up.UploadedBytes != int64(len(testPayload)) {
		t.Fatalf("expected uploaded bytes %d, got %d", len(testPayload), up.UploadedBytes)
	}

	// Verify data on server
	mts.mu.Lock()
	serverData := mts.uploads["upload-1"].data
	mts.mu.Unlock()

	if !bytes.Equal(serverData, testPayload) {
		t.Fatalf("uploaded bytes on server do not match source payload")
	}
}

func TestTusUploadInterruptionAndResume(t *testing.T) {
	ts, mts := newMockTusServer()
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "magicloder-tus-resume-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testPayload := make([]byte, 100*1024) // 100 KiB
	_, _ = rand.Read(testPayload)
	filePath := filepath.Join(tempDir, "resume.bin")
	_ = os.WriteFile(filePath, testPayload, 0644)

	store, _ := storage.Open(filepath.Join(tempDir, "tus_resume.db"))
	defer store.Close()

	client := uploader.NewTusClient(store)
	up := &model.Upload{
		ID:          "up-resume-test",
		SourcePath:  filePath,
		TusEndpoint: ts.URL + "/files",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	opts := uploader.DefaultTusOptions()
	opts.Client = ts.Client()
	opts.ChunkSize = 25 * 1024 // 25 KiB chunks

	// Interrupt after 2 chunks (50 KiB)
	var uploadedSoFar int64
	cancelCtx, cancel := context.WithCancel(context.Background())
	opts.OnProgress = func(uploaded, total int64) {
		uploadedSoFar = uploaded
		if uploaded >= 50*1024 {
			cancel()
		}
	}

	err = client.UploadFile(cancelCtx, up, opts)
	if err == nil {
		t.Fatalf("expected cancellation error, got nil")
	}

	if uploadedSoFar != 50*1024 {
		t.Fatalf("expected uploadedSoFar 50 KiB, got %d", uploadedSoFar)
	}

	// Verify server has 50 KiB
	mts.mu.Lock()
	partData := mts.uploads["upload-1"].data
	mts.mu.Unlock()
	if len(partData) != 50*1024 {
		t.Fatalf("expected server to have 50 KiB, got %d", len(partData))
	}

	// --- RESUME UPLOAD ---
	resumeOpts := uploader.DefaultTusOptions()
	resumeOpts.Client = ts.Client()
	resumeOpts.ChunkSize = 25 * 1024

	err = client.UploadFile(context.Background(), up, resumeOpts)
	if err != nil {
		t.Fatalf("resumed upload failed: %v", err)
	}

	if up.Status != model.UploadCompleted {
		t.Fatalf("expected completed status, got %s", up.Status)
	}

	mts.mu.Lock()
	finalData := mts.uploads["upload-1"].data
	mts.mu.Unlock()

	if !bytes.Equal(finalData, testPayload) {
		t.Fatalf("final reconstructed payload on server mismatch")
	}
}

func TestTusFileModifiedDetection(t *testing.T) {
	ts, _ := newMockTusServer()
	defer ts.Close()

	tempDir, _ := os.MkdirTemp("", "magicloder-mod-test-*")
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "mod.bin")
	_ = os.WriteFile(filePath, []byte("initial data"), 0644)

	client := uploader.NewTusClient(nil)
	up := &model.Upload{
		ID:              "up-mod-test",
		SourcePath:      filePath,
		TusEndpoint:     ts.URL + "/files",
		FileFingerprint: "stale-fingerprint-999-999",
	}

	opts := uploader.DefaultTusOptions()
	opts.Client = ts.Client()

	err := client.UploadFile(context.Background(), up, opts)
	if err == nil {
		t.Fatalf("expected ErrFileModified, got nil")
	}
}

func TestTusCancelUpload(t *testing.T) {
	ts, mts := newMockTusServer()
	defer ts.Close()

	tempDir, _ := os.MkdirTemp("", "magicloder-cancel-test-*")
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "cancel.bin")
	_ = os.WriteFile(filePath, []byte("cancel data"), 0644)

	client := uploader.NewTusClient(nil)
	up := &model.Upload{
		ID:          "up-cancel-test",
		SourcePath:  filePath,
		TusEndpoint: ts.URL + "/files",
	}

	opts := uploader.DefaultTusOptions()
	opts.Client = ts.Client()
	_ = client.UploadFile(context.Background(), up, opts)

	if up.UploadURL == "" {
		t.Fatalf("expected upload URL after creation")
	}

	// Terminate upload on server
	err := client.CancelUpload(context.Background(), ts.Client(), up.UploadURL)
	if err != nil {
		t.Fatalf("CancelUpload failed: %v", err)
	}

	mts.mu.Lock()
	_, exists := mts.uploads[filepath.Base(up.UploadURL)]
	mts.mu.Unlock()

	if exists {
		t.Fatalf("upload was not deleted on server after CancelUpload")
	}
}

package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/model"
	"github.com/ashishsinghbora/magicloder/internal/storage"
)

func TestSQLiteStoreCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "magicloder-storage-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. Create Download
	dl := &model.Download{
		ID:             "dl-001",
		URL:            "https://example.com/archive.iso",
		Destination:    filepath.Join(tempDir, "archive.iso"),
		TemporaryPath:  filepath.Join(tempDir, "archive.iso.part"),
		TotalSize:      1024 * 1024,
		CompletedBytes: 0,
		Status:         model.StatusQueued,
		ETag:           "etag-123",
		SupportsRange:  true,
		MaxConnections: 4,
		Priority:       5,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	if err := store.CreateDownload(ctx, dl); err != nil {
		t.Fatalf("CreateDownload failed: %v", err)
	}

	// 2. Get Download
	retrieved, err := store.GetDownload(ctx, "dl-001")
	if err != nil {
		t.Fatalf("GetDownload failed: %v", err)
	}
	if retrieved.ID != dl.ID || retrieved.Priority != 5 || !retrieved.SupportsRange {
		t.Fatalf("retrieved mismatch: %+v", retrieved)
	}

	// 3. Save Chunks
	chunks := []model.Chunk{
		{DownloadID: dl.ID, Index: 0, StartByte: 0, EndByte: 524287, CompletedBytes: 200, Status: model.ChunkDownloading},
		{DownloadID: dl.ID, Index: 1, StartByte: 524288, EndByte: 1048575, CompletedBytes: 0, Status: model.ChunkPending},
	}
	if err := store.SaveChunks(ctx, chunks); err != nil {
		t.Fatalf("SaveChunks failed: %v", err)
	}

	savedChunks, err := store.GetChunks(ctx, dl.ID)
	if err != nil || len(savedChunks) != 2 {
		t.Fatalf("GetChunks failed: %v, count: %d", err, len(savedChunks))
	}

	// 4. Batch Update Chunks
	savedChunks[0].CompletedBytes = 524288
	savedChunks[0].Status = model.ChunkCompleted
	savedChunks[1].CompletedBytes = 1000
	savedChunks[1].Status = model.ChunkDownloading

	if err := store.BatchUpdateChunks(ctx, dl.ID, savedChunks); err != nil {
		t.Fatalf("BatchUpdateChunks failed: %v", err)
	}

	retrievedAfterBatch, err := store.GetDownload(ctx, dl.ID)
	if err != nil {
		t.Fatalf("GetDownload failed: %v", err)
	}
	if retrievedAfterBatch.CompletedBytes != 524288+1000 {
		t.Fatalf("expected completed bytes %d, got %d", 524288+1000, retrievedAfterBatch.CompletedBytes)
	}

	// 5. List and Filter
	list, err := store.ListDownloads(ctx, model.StatusQueued)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListDownloads failed: %v, count: %d", err, len(list))
	}

	unfinished, err := store.GetUnfinishedDownloads(ctx)
	if err != nil || len(unfinished) != 1 {
		t.Fatalf("GetUnfinishedDownloads failed: %v, count: %d", err, len(unfinished))
	}

	// 6. Delete Download (cascades chunks)
	if err := store.DeleteDownload(ctx, dl.ID); err != nil {
		t.Fatalf("DeleteDownload failed: %v", err)
	}
	chunksAfterDelete, err := store.GetChunks(ctx, dl.ID)
	if err != nil || len(chunksAfterDelete) != 0 {
		t.Fatalf("chunks not cascaded on download delete")
	}
}

func TestSQLiteUploadCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "magicloder-upload-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := storage.Open(filepath.Join(tempDir, "upload_test.db"))
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	up := &model.Upload{
		ID:              "up-001",
		SourcePath:      "/path/to/source.iso",
		TusEndpoint:     "https://upload.example.com/files",
		UploadURL:       "",
		TotalSize:       1024 * 1024,
		UploadedBytes:   0,
		Status:          model.UploadQueued,
		FileFingerprint: "fingerprint-abc-123",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	if err := store.CreateUpload(ctx, up); err != nil {
		t.Fatalf("CreateUpload failed: %v", err)
	}

	retrieved, err := store.GetUpload(ctx, up.ID)
	if err != nil || retrieved.ID != up.ID {
		t.Fatalf("GetUpload failed: %v", err)
	}

	if err := store.UpdateUploadProgress(ctx, up.ID, 500, "https://upload.example.com/files/res-1", model.UploadUploading); err != nil {
		t.Fatalf("UpdateUploadProgress failed: %v", err)
	}

	updated, err := store.GetUpload(ctx, up.ID)
	if err != nil || updated.UploadedBytes != 500 || updated.UploadURL != "https://upload.example.com/files/res-1" {
		t.Fatalf("updated upload mismatch: %+v", updated)
	}
}

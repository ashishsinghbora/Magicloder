package model_test

import (
	"errors"
	"testing"

	"github.com/ashishsinghbora/magicloder/internal/model"
)

func TestDownloadStateTransitions(t *testing.T) {
	tests := []struct {
		name      string
		from      model.DownloadStatus
		to        model.DownloadStatus
		expectErr bool
	}{
		// Valid from Queued
		{"queued -> probing", model.StatusQueued, model.StatusProbing, false},
		{"queued -> downloading", model.StatusQueued, model.StatusDownloading, false},
		{"queued -> paused", model.StatusQueued, model.StatusPaused, false},
		{"queued -> cancelled", model.StatusQueued, model.StatusCancelled, false},
		{"queued -> completed (invalid)", model.StatusQueued, model.StatusCompleted, true},
		{"queued -> failed (invalid)", model.StatusQueued, model.StatusFailed, true},

		// Valid from Probing
		{"probing -> downloading", model.StatusProbing, model.StatusDownloading, false},
		{"probing -> paused", model.StatusProbing, model.StatusPaused, false},
		{"probing -> failed", model.StatusProbing, model.StatusFailed, false},
		{"probing -> cancelled", model.StatusProbing, model.StatusCancelled, false},
		{"probing -> completed (invalid)", model.StatusProbing, model.StatusCompleted, true},

		// Valid from Downloading
		{"downloading -> paused", model.StatusDownloading, model.StatusPaused, false},
		{"downloading -> completed", model.StatusDownloading, model.StatusCompleted, false},
		{"downloading -> failed", model.StatusDownloading, model.StatusFailed, false},
		{"downloading -> cancelled", model.StatusDownloading, model.StatusCancelled, false},
		{"downloading -> probing (invalid)", model.StatusDownloading, model.StatusProbing, true},

		// Valid from Paused
		{"paused -> queued", model.StatusPaused, model.StatusQueued, false},
		{"paused -> downloading", model.StatusPaused, model.StatusDownloading, false},
		{"paused -> cancelled", model.StatusPaused, model.StatusCancelled, false},
		{"paused -> completed (invalid)", model.StatusPaused, model.StatusCompleted, true},

		// Valid from Failed
		{"failed -> queued", model.StatusFailed, model.StatusQueued, false},
		{"failed -> cancelled", model.StatusFailed, model.StatusCancelled, false},
		{"failed -> downloading (invalid)", model.StatusFailed, model.StatusDownloading, true},

		// Valid from Cancelled
		{"cancelled -> queued", model.StatusCancelled, model.StatusQueued, false},
		{"cancelled -> downloading (invalid)", model.StatusCancelled, model.StatusDownloading, true},

		// Terminal Completed
		{"completed -> queued (invalid)", model.StatusCompleted, model.StatusQueued, true},
		{"completed -> downloading (invalid)", model.StatusCompleted, model.StatusDownloading, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dl := &model.Download{ID: "test-dl", Status: tc.from}
			can := dl.CanTransition(tc.to)
			err := dl.TransitionTo(tc.to)

			if tc.expectErr {
				if can {
					t.Fatalf("expected CanTransition to return false, got true")
				}
				if err == nil || !errors.Is(err, model.ErrInvalidTransition) {
					t.Fatalf("expected ErrInvalidTransition, got %v", err)
				}
				if dl.Status != tc.from {
					t.Fatalf("status should not change on error, got %s", dl.Status)
				}
			} else {
				if !can {
					t.Fatalf("expected CanTransition to return true, got false")
				}
				if err != nil {
					t.Fatalf("unexpected transition error: %v", err)
				}
				if dl.Status != tc.to {
					t.Fatalf("expected status %s, got %s", tc.to, dl.Status)
				}
			}
		})
	}
}

func TestChunkStateTransitions(t *testing.T) {
	tests := []struct {
		name      string
		from      model.ChunkStatus
		to        model.ChunkStatus
		expectErr bool
	}{
		{"pending -> downloading", model.ChunkPending, model.ChunkDownloading, false},
		{"pending -> failed", model.ChunkPending, model.ChunkFailed, false},
		{"pending -> completed (invalid)", model.ChunkPending, model.ChunkCompleted, true},

		{"downloading -> completed", model.ChunkDownloading, model.ChunkCompleted, false},
		{"downloading -> failed", model.ChunkDownloading, model.ChunkFailed, false},
		{"downloading -> pending", model.ChunkDownloading, model.ChunkPending, false},

		{"failed -> pending", model.ChunkFailed, model.ChunkPending, false},
		{"failed -> downloading", model.ChunkFailed, model.ChunkDownloading, false},
		{"failed -> completed (invalid)", model.ChunkFailed, model.ChunkCompleted, true},

		{"completed -> pending (invalid)", model.ChunkCompleted, model.ChunkPending, true},
		{"completed -> downloading (invalid)", model.ChunkCompleted, model.ChunkDownloading, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chk := &model.Chunk{ID: 1, Status: tc.from}
			can := chk.CanTransition(tc.to)
			err := chk.TransitionTo(tc.to)

			if tc.expectErr {
				if can {
					t.Fatalf("expected CanTransition to return false, got true")
				}
				if err == nil || !errors.Is(err, model.ErrInvalidTransition) {
					t.Fatalf("expected ErrInvalidTransition, got %v", err)
				}
			} else {
				if !can {
					t.Fatalf("expected CanTransition to return true, got false")
				}
				if err != nil {
					t.Fatalf("unexpected transition error: %v", err)
				}
				if chk.Status != tc.to {
					t.Fatalf("expected status %s, got %s", tc.to, chk.Status)
				}
			}
		})
	}
}

func TestChunkTotalBytes(t *testing.T) {
	c1 := &model.Chunk{StartByte: 0, EndByte: 99}
	if c1.TotalChunkBytes() != 100 {
		t.Fatalf("expected 100 bytes, got %d", c1.TotalChunkBytes())
	}

	c2 := &model.Chunk{StartByte: 100, EndByte: 99}
	if c2.TotalChunkBytes() != 0 {
		t.Fatalf("expected 0 bytes for invalid bounds, got %d", c2.TotalChunkBytes())
	}
}

func TestUploadStateTransitions(t *testing.T) {
	tests := []struct {
		name      string
		from      model.UploadStatus
		to        model.UploadStatus
		expectErr bool
	}{
		{"queued -> uploading", model.UploadQueued, model.UploadUploading, false},
		{"queued -> paused", model.UploadQueued, model.UploadPaused, false},
		{"queued -> cancelled", model.UploadQueued, model.UploadCancelled, false},
		{"queued -> completed (invalid)", model.UploadQueued, model.UploadCompleted, true},

		{"uploading -> paused", model.UploadUploading, model.UploadPaused, false},
		{"uploading -> completed", model.UploadUploading, model.UploadCompleted, false},
		{"uploading -> failed", model.UploadUploading, model.UploadFailed, false},
		{"uploading -> cancelled", model.UploadUploading, model.UploadCancelled, false},

		{"completed -> queued (invalid)", model.UploadCompleted, model.UploadQueued, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up := &model.Upload{ID: "test-up", Status: tc.from}
			can := up.CanTransition(tc.to)
			err := up.TransitionTo(tc.to)

			if tc.expectErr {
				if can || err == nil {
					t.Fatalf("expected error transitioning from %s to %s", tc.from, tc.to)
				}
			} else {
				if !can || err != nil {
					t.Fatalf("expected success transitioning from %s to %s: %v", tc.from, tc.to, err)
				}
			}
		})
	}
}

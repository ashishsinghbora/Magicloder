package downloader

import (
	"context"
	"fmt"
	"os"

	"github.com/ashishsinghbora/magicloder/internal/model"
	"github.com/ashishsinghbora/magicloder/internal/storage"
)

// ReconcileResult summarizes the reconciliation decision.
type ReconcileResult struct {
	RemoteChanged bool
	CanResume     bool
	MissingBytes  int64
	ActionTaken   string
}

// ReconcileDownload compares persisted download state against live probe metadata and the on-disk .part file.
func ReconcileDownload(ctx context.Context, dl *model.Download, probe *ProbeResult, store storage.Store) (*ReconcileResult, error) {
	if dl == nil {
		return nil, fmt.Errorf("nil download provided")
	}

	result := &ReconcileResult{
		RemoteChanged: false,
		CanResume:     false,
		MissingBytes:  dl.TotalSize,
	}

	// 1. Detect remote changes (ETag mismatch, Size mismatch)
	if dl.ETag != "" && probe.ETag != "" && dl.ETag != probe.ETag {
		result.RemoteChanged = true
	}
	if dl.TotalSize > 0 && probe.ContentLength > 0 && dl.TotalSize != probe.ContentLength {
		result.RemoteChanged = true
	}

	if result.RemoteChanged {
		result.ActionTaken = "Remote resource modified; discarding partial progress"
		_ = os.Remove(dl.TemporaryPath)
		dl.CompletedBytes = 0
		dl.ETag = probe.ETag
		dl.TotalSize = probe.ContentLength
		dl.LastModified = probe.LastModified
		dl.Status = model.StatusQueued

		for i := range dl.Chunks {
			dl.Chunks[i].CompletedBytes = 0
			dl.Chunks[i].Status = model.ChunkPending
			dl.Chunks[i].RetryCount = 0
			dl.Chunks[i].ErrorMsg = ""
		}

		if err := store.UpdateDownload(ctx, dl); err != nil {
			return nil, err
		}
		if len(dl.Chunks) > 0 {
			if err := store.BatchUpdateChunks(ctx, dl.ID, dl.Chunks); err != nil {
				return nil, err
			}
		}
		return result, nil
	}

	// 2. Check on-disk .part file
	fi, err := os.Stat(dl.TemporaryPath)
	if os.IsNotExist(err) {
		result.ActionTaken = "Partial file missing; resetting chunk progress"
		dl.CompletedBytes = 0
		for i := range dl.Chunks {
			dl.Chunks[i].CompletedBytes = 0
			dl.Chunks[i].Status = model.ChunkPending
		}
		if err := store.UpdateDownloadProgress(ctx, dl.ID, 0); err != nil {
			return nil, err
		}
		if len(dl.Chunks) > 0 {
			_ = store.BatchUpdateChunks(ctx, dl.ID, dl.Chunks)
		}
		return result, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to inspect partial file: %w", err)
	}

	// 3. If file exists and server supports range, verify chunk consistency
	if dl.SupportsRange && len(dl.Chunks) > 0 {
		var totalCompleted int64 = 0
		for i := range dl.Chunks {
			c := &dl.Chunks[i]
			chunkLen := c.TotalChunkBytes()
			if c.CompletedBytes > chunkLen {
				c.CompletedBytes = chunkLen
			}
			if c.CompletedBytes == chunkLen {
				c.Status = model.ChunkCompleted
			} else {
				c.Status = model.ChunkPending
			}
			totalCompleted += c.CompletedBytes
		}

		dl.CompletedBytes = totalCompleted
		result.MissingBytes = dl.TotalSize - totalCompleted
		result.CanResume = totalCompleted > 0
		result.ActionTaken = fmt.Sprintf("Reconciled %d chunks; %d/%d bytes completed", len(dl.Chunks), totalCompleted, dl.TotalSize)

		if err := store.BatchUpdateChunks(ctx, dl.ID, dl.Chunks); err != nil {
			return nil, err
		}
		return result, nil
	}

	// Single-stream partial file check
	partSize := fi.Size()
	if probe.SupportsRange && partSize > 0 && partSize < dl.TotalSize {
		dl.CompletedBytes = partSize
		result.CanResume = true
		result.MissingBytes = dl.TotalSize - partSize
		result.ActionTaken = fmt.Sprintf("Reconciled single-stream file; %d/%d bytes", partSize, dl.TotalSize)
		_ = store.UpdateDownloadProgress(ctx, dl.ID, partSize)
	} else {
		result.CanResume = false
		result.ActionTaken = "Starting fresh download"
	}

	return result, nil
}

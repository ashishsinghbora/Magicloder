package model

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// DownloadStatus represents the lifecycle state of a download job.
type DownloadStatus string

const (
	StatusQueued      DownloadStatus = "queued"
	StatusProbing     DownloadStatus = "probing"
	StatusDownloading DownloadStatus = "downloading"
	StatusPaused      DownloadStatus = "paused"
	StatusCompleted   DownloadStatus = "completed"
	StatusFailed      DownloadStatus = "failed"
	StatusCancelled   DownloadStatus = "cancelled"
)

// ChunkStatus represents the state of an individual chunk.
type ChunkStatus string

const (
	ChunkPending     ChunkStatus = "pending"
	ChunkDownloading ChunkStatus = "downloading"
	ChunkCompleted   ChunkStatus = "completed"
	ChunkFailed      ChunkStatus = "failed"
)

var (
	// ErrInvalidTransition is returned when a state transition is not allowed.
	ErrInvalidTransition = errors.New("invalid state transition")
)

// Download represents a persistent download entity.
type Download struct {
	mu             sync.RWMutex
	ID             string         `json:"id"`
	URL            string         `json:"url"`
	Destination    string         `json:"destination"`
	TemporaryPath  string         `json:"temporary_path"`
	TotalSize      int64          `json:"total_size"`      // -1 if unknown / non-range
	CompletedBytes int64          `json:"completed_bytes"`
	Status         DownloadStatus `json:"status"`
	ETag           string         `json:"etag,omitempty"`
	LastModified   string         `json:"last_modified,omitempty"`
	ContentType    string         `json:"content_type,omitempty"`
	SupportsRange  bool           `json:"supports_range"`
	MaxConnections int            `json:"max_connections"`
	Priority       int            `json:"priority"` // Higher number = higher priority
	ErrorMsg       string         `json:"error_msg,omitempty"`
	ExpectedHash   string         `json:"expected_hash,omitempty"`
	ActualHash     string         `json:"actual_hash,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Chunks         []Chunk        `json:"chunks,omitempty"`
}

// Chunk represents an allocated byte range within a download.
type Chunk struct {
	ID             int64       `json:"id"`
	DownloadID     string      `json:"download_id"`
	Index          int         `json:"index"`
	StartByte      int64       `json:"start_byte"`
	EndByte        int64       `json:"end_byte"` // Inclusive
	CompletedBytes int64       `json:"completed_bytes"`
	Status         ChunkStatus `json:"status"`
	RetryCount     int         `json:"retry_count"`
	ErrorMsg       string      `json:"error_msg,omitempty"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// TotalChunkBytes returns the total expected length of this chunk.
func (c *Chunk) TotalChunkBytes() int64 {
	if c.EndByte < c.StartByte {
		return 0
	}
	return (c.EndByte - c.StartByte) + 1
}

// CanTransition validates if the download can transition to the target state.
func (d *Download) CanTransition(target DownloadStatus) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.canTransitionLocked(target)
}

func (d *Download) canTransitionLocked(target DownloadStatus) bool {
	switch d.Status {
	case StatusQueued:
		return target == StatusProbing || target == StatusDownloading || target == StatusPaused || target == StatusCancelled
	case StatusProbing:
		return target == StatusDownloading || target == StatusPaused || target == StatusFailed || target == StatusCancelled
	case StatusDownloading:
		return target == StatusPaused || target == StatusCompleted || target == StatusFailed || target == StatusCancelled
	case StatusPaused:
		return target == StatusQueued || target == StatusDownloading || target == StatusCancelled
	case StatusFailed:
		return target == StatusQueued || target == StatusCancelled
	case StatusCancelled:
		return target == StatusQueued
	case StatusCompleted:
		return false // Terminal state
	default:
		return false
	}
}

// TransitionTo updates the download state if valid, or returns ErrInvalidTransition.
func (d *Download) TransitionTo(target DownloadStatus) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.canTransitionLocked(target) {
		return fmt.Errorf("%w: cannot transition download %s from %s to %s", ErrInvalidTransition, d.ID, d.Status, target)
	}
	d.Status = target
	d.UpdatedAt = time.Now().UTC()
	return nil
}

// GetStatus returns the current status safely under lock.
func (d *Download) GetStatus() DownloadStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.Status
}

// Clone returns an isolated snapshot of the download.
func (d *Download) Clone() *Download {
	d.mu.RLock()
	defer d.mu.RUnlock()
	cp := *d
	if d.Chunks != nil {
		cp.Chunks = make([]Chunk, len(d.Chunks))
		copy(cp.Chunks, d.Chunks)
	}
	return &cp
}

// CanTransition validates if the chunk can transition to the target state.
func (c *Chunk) CanTransition(target ChunkStatus) bool {
	switch c.Status {
	case ChunkPending:
		return target == ChunkDownloading || target == ChunkFailed
	case ChunkDownloading:
		return target == ChunkCompleted || target == ChunkFailed || target == ChunkPending
	case ChunkFailed:
		return target == ChunkPending || target == ChunkDownloading
	case ChunkCompleted:
		return false // Completed chunks are immutable
	default:
		return false
	}
}

// TransitionTo updates the chunk state if valid, or returns ErrInvalidTransition.
func (c *Chunk) TransitionTo(target ChunkStatus) error {
	if !c.CanTransition(target) {
		return fmt.Errorf("%w: cannot transition chunk %d from %s to %s", ErrInvalidTransition, c.ID, c.Status, target)
	}
	c.Status = target
	c.UpdatedAt = time.Now().UTC()
	return nil
}

package model

import (
	"fmt"
	"time"
)

// UploadStatus represents the lifecycle state of a Tus upload job.
type UploadStatus string

const (
	UploadQueued     UploadStatus = "queued"
	UploadUploading  UploadStatus = "uploading"
	UploadPaused     UploadStatus = "paused"
	UploadCompleted  UploadStatus = "completed"
	UploadFailed     UploadStatus = "failed"
	UploadCancelled  UploadStatus = "cancelled"
)

// Upload represents a persistent Tus resumable upload entity.
type Upload struct {
	ID             string       `json:"id"`
	SourcePath     string       `json:"source_path"`
	TusEndpoint    string       `json:"tus_endpoint"`
	UploadURL      string       `json:"upload_url,omitempty"` // Returned by Tus creation
	TotalSize      int64        `json:"total_size"`
	UploadedBytes  int64        `json:"uploaded_bytes"`
	Status         UploadStatus `json:"status"`
	FileFingerprint string      `json:"file_fingerprint"` // Hash or size+modtime identifier
	Metadata       string       `json:"metadata,omitempty"`
	ErrorMsg       string       `json:"error_msg,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// CanTransition validates if the upload can transition to the target state.
func (u *Upload) CanTransition(target UploadStatus) bool {
	switch u.Status {
	case UploadQueued:
		return target == UploadUploading || target == UploadPaused || target == UploadCancelled
	case UploadUploading:
		return target == UploadPaused || target == UploadCompleted || target == UploadFailed || target == UploadCancelled
	case UploadPaused:
		return target == UploadQueued || target == UploadUploading || target == UploadCancelled
	case UploadFailed:
		return target == UploadQueued || target == UploadCancelled
	case UploadCancelled:
		return target == UploadQueued
	case UploadCompleted:
		return false // Terminal state
	default:
		return false
	}
}

// TransitionTo updates the upload state if valid.
func (u *Upload) TransitionTo(target UploadStatus) error {
	if !u.CanTransition(target) {
		return fmt.Errorf("%w: cannot transition upload %s from %s to %s", ErrInvalidTransition, u.ID, u.Status, target)
	}
	u.Status = target
	u.UpdatedAt = time.Now().UTC()
	return nil
}

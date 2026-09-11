package events

import (
	"time"
)

// Event types emitted over WebSocket.
const (
	EventJobQueued      = "job_queued"
	EventJobProbing     = "job_probing"
	EventJobDownloading = "job_downloading"
	EventJobProgress    = "job_progress"
	EventJobPaused      = "job_paused"
	EventJobCompleted   = "job_completed"
	EventJobFailed      = "job_failed"
	EventJobCancelled   = "job_cancelled"
	EventJobRetried     = "job_retried"
)

// Event represents a typed live update sent to WebSocket subscribers.
type Event struct {
	Type              string    `json:"type"`
	Timestamp         time.Time `json:"timestamp"`
	DownloadID        string    `json:"download_id,omitempty"`
	URL               string    `json:"url,omitempty"`
	Destination       string    `json:"destination,omitempty"`
	Status            string    `json:"status,omitempty"`
	CompletedBytes    int64     `json:"completed_bytes"`
	TotalSize         int64     `json:"total_size"`
	SpeedBytesPerSec  float64   `json:"speed_bytes_per_sec"`
	ETASeconds        int64     `json:"eta_seconds"`
	ActiveConnections int       `json:"active_connections"`
	ErrorMsg          string    `json:"error_msg,omitempty"`
}

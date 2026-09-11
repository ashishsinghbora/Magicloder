package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

var (
	ErrSizeMismatch     = errors.New("downloaded file size does not match expected length")
	ErrFileExists       = errors.New("destination file already exists")
	ErrDownloadAborted  = errors.New("download aborted")
)

// ProgressFunc is called periodically as bytes are written to disk.
type ProgressFunc func(completedBytes, totalBytes int64)

// SingleStreamOptions configures single-stream execution.
type SingleStreamOptions struct {
	Client      *http.Client
	AllowResume bool
	Overwrite   bool
	OnProgress  ProgressFunc
	BufferSize  int
}

// DefaultSingleStreamOptions returns recommended defaults.
func DefaultSingleStreamOptions() *SingleStreamOptions {
	return &SingleStreamOptions{
		Client:      &http.Client{Timeout: 0}, // Stream downloads do not use overall client timeout
		AllowResume: true,
		Overwrite:   false,
		OnProgress:  nil,
		BufferSize:  32 * 1024, // 32 KiB
	}
}

// DownloadSingleStream executes a streaming HTTP download directly to disk.
// Writes to tempPath (.part file) and atomically renames to finalPath on success.
func DownloadSingleStream(ctx context.Context, targetURL, tempPath, finalPath string, expectedSize int64, opts *SingleStreamOptions) error {
	if opts == nil {
		opts = DefaultSingleStreamOptions()
	}
	if opts.BufferSize <= 0 {
		opts.BufferSize = 32 * 1024
	}

	// Check if destination exists
	if !opts.Overwrite {
		if _, err := os.Stat(finalPath); err == nil {
			return fmt.Errorf("%w: %s", ErrFileExists, finalPath)
		}
	}

	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(tempPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", tempPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", finalPath, err)
	}

	// Determine starting byte offset if resuming
	var startOffset int64 = 0
	if opts.AllowResume {
		if fi, err := os.Stat(tempPath); err == nil {
			startOffset = fi.Size()
		}
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("failed to build download request: %w", err)
	}
	req.Header.Set("User-Agent", "Magicloder/0.1.0")

	if startOffset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startOffset))
	}

	resp, err := opts.Client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: HTTP %d (%s)", ErrNonSuccessHTTP, resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	// Open temp file for writing
	var file *os.File
	var currentBytes int64 = 0

	if resp.StatusCode == http.StatusPartialContent && startOffset > 0 {
		// Server honored resume range
		file, err = os.OpenFile(tempPath, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open partial file for resume: %w", err)
		}
		currentBytes = startOffset
	} else {
		// Server returned full content (200 OK) or no existing file
		file, err = os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return fmt.Errorf("failed to create temp file %s: %w", tempPath, err)
		}
		currentBytes = 0
	}

	// Stream copying with context awareness and progress reporting
	buf := make([]byte, opts.BufferSize)
	var writeErr error

	for {
		select {
		case <-ctx.Done():
			_ = file.Sync()
			_ = file.Close()
			return ctx.Err()
		default:
		}

		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := file.Write(buf[:n]); wErr != nil {
				writeErr = fmt.Errorf("disk write error: %w", wErr)
				break
			}
			currentBytes += int64(n)
			if opts.OnProgress != nil {
				opts.OnProgress(currentBytes, expectedSize)
			}
		}

		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				break
			}
			writeErr = fmt.Errorf("network read error: %w", rErr)
			break
		}
	}

	// Flush and close file
	syncErr := file.Sync()
	closeErr := file.Close()

	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return fmt.Errorf("failed to sync temp file to disk: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close temp file: %w", closeErr)
	}

	// Validate size if known
	if expectedSize > 0 && currentBytes != expectedSize {
		return fmt.Errorf("%w: expected %d bytes, wrote %d bytes", ErrSizeMismatch, expectedSize, currentBytes)
	}

	// Atomically finalize to destination
	if err := os.Rename(tempPath, finalPath); err != nil {
		return fmt.Errorf("failed to finalize atomic rename from %s to %s: %w", tempPath, finalPath, err)
	}

	return nil
}

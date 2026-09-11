package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/engine/chunk"
	"github.com/ashishsinghbora/magicloder/internal/model"
)

var (
	ErrFallbackToSingleStream = errors.New("range requests failed or unsupported, fallback required")
	ErrMaxRetriesExceeded     = errors.New("worker exceeded maximum retry attempts")
)

// ChunkProgressCallback is called when a chunk writes bytes to disk.
type ChunkProgressCallback func(chunkIndex int, deltaBytes int64, chunkCompletedBytes int64)

// RangedDownloaderOptions configures concurrent multi-connection ranged downloads.
type RangedDownloaderOptions struct {
	Client          *http.Client
	MaxWorkers      int
	MaxRetries      int
	BaseBackoff     time.Duration
	MaxBackoff      time.Duration
	BufferSize      int
	Overwrite       bool
	OnChunkProgress ChunkProgressCallback
	OnTotalProgress ProgressFunc
}

// DefaultRangedOptions returns default configuration.
func DefaultRangedOptions() *RangedDownloaderOptions {
	return &RangedDownloaderOptions{
		Client:          &http.Client{Timeout: 0},
		MaxWorkers:      4,
		MaxRetries:      3,
		BaseBackoff:     100 * time.Millisecond,
		MaxBackoff:      2 * time.Second,
		BufferSize:      32 * 1024,
		Overwrite:       false,
		OnChunkProgress: nil,
		OnTotalProgress: nil,
	}
}

// DownloadRanged executes a concurrent multi-connection ranged HTTP download.
// Each worker writes strictly to its non-overlapping byte range using os.File.WriteAt.
func DownloadRanged(ctx context.Context, targetURL, tempPath, finalPath string, totalSize int64, chunks []model.Chunk, opts *RangedDownloaderOptions) error {
	if opts == nil {
		opts = DefaultRangedOptions()
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = 4
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

	// Open or create .part file for concurrent random-access writes
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open temp file %s: %w", tempPath, err)
	}
	defer file.Close()

	// Truncate/preallocate to totalSize so WriteAt offsets are valid
	if err := file.Truncate(totalSize); err != nil {
		return fmt.Errorf("failed to preallocate temp file %s: %w", tempPath, err)
	}

	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	// Calculate already completed bytes across chunks
	var totalCompletedAtomic int64 = 0
	for _, chk := range chunks {
		totalCompletedAtomic += chk.CompletedBytes
	}

	// Worker semaphore to bound active goroutines
	sem := make(chan struct{}, opts.MaxWorkers)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	setErr := func(err error) {
		errOnce.Do(func() {
			firstErr = err
			cancelWorkers()
		})
	}

	for i := range chunks {
		chk := &chunks[i]
		if chk.CompletedBytes >= chk.TotalChunkBytes() {
			chk.Status = model.ChunkCompleted
			continue
		}

		select {
		case <-workerCtx.Done():
			break
		case sem <- struct{}{}:
		}

		if workerCtx.Err() != nil {
			<-sem
			break
		}

		wg.Add(1)
		go func(c *model.Chunk) {
			defer func() {
				<-sem
				wg.Done()
			}()

			err := downloadChunkWithRetry(workerCtx, opts.Client, targetURL, file, c, opts, &totalCompletedAtomic, totalSize)
			if err != nil {
				c.Status = model.ChunkFailed
				c.ErrorMsg = err.Error()
				setErr(err)
			} else {
				c.Status = model.ChunkCompleted
				c.ErrorMsg = ""
			}
		}(chk)
	}

	wg.Wait()

	// Sync file to persistent storage
	syncErr := file.Sync()
	if syncErr != nil && firstErr == nil {
		firstErr = fmt.Errorf("failed to sync temp file: %w", syncErr)
	}

	if firstErr != nil {
		return firstErr
	}

	// Verify all chunks completed
	for i := range chunks {
		if chunks[i].CompletedBytes < chunks[i].TotalChunkBytes() {
			return fmt.Errorf("chunk %d incomplete: %d/%d bytes", i, chunks[i].CompletedBytes, chunks[i].TotalChunkBytes())
		}
	}

	// Close file handle before renaming (critical on Windows)
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close file before rename: %w", err)
	}

	// Atomically finalize
	if err := os.Rename(tempPath, finalPath); err != nil {
		return fmt.Errorf("failed to atomically rename %s to %s: %w", tempPath, finalPath, err)
	}

	return nil
}

func downloadChunkWithRetry(
	ctx context.Context,
	client *http.Client,
	targetURL string,
	file *os.File,
	chk *model.Chunk,
	opts *RangedDownloaderOptions,
	totalCompletedAtomic *int64,
	totalSize int64,
) error {
	chk.Status = model.ChunkDownloading
	retries := 0

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := downloadChunkStream(ctx, client, targetURL, file, chk, opts, totalCompletedAtomic, totalSize)
		if err == nil {
			return nil
		}

		// Check if server rejected ranges completely
		if errors.Is(err, chunk.ErrServerIgnoredRange) {
			return fmt.Errorf("%w: server returned 200 to chunk range request", ErrFallbackToSingleStream)
		}

		retries++
		chk.RetryCount = retries
		if retries > opts.MaxRetries {
			return fmt.Errorf("%w for chunk %d: %v", ErrMaxRetriesExceeded, chk.Index, err)
		}

		// Exponential backoff
		backoff := time.Duration(float64(opts.BaseBackoff) * math.Pow(2, float64(retries-1)))
		if backoff > opts.MaxBackoff {
			backoff = opts.MaxBackoff
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

func downloadChunkStream(
	ctx context.Context,
	client *http.Client,
	targetURL string,
	file *os.File,
	chk *model.Chunk,
	opts *RangedDownloaderOptions,
	totalCompletedAtomic *int64,
	totalSize int64,
) error {
	reqStart := chk.StartByte + chk.CompletedBytes
	reqEnd := chk.EndByte

	if reqStart > reqEnd {
		return nil // Already complete
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Magicloder/0.1.0")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", reqStart, reqEnd))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if _, err := chunk.ValidateRangeResponse(resp, reqStart, reqEnd); err != nil {
		return err
	}

	buf := make([]byte, opts.BufferSize)
	currentWriteOffset := reqStart

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			// Deterministic ranged write to dedicated offset
			if _, wErr := file.WriteAt(buf[:n], currentWriteOffset); wErr != nil {
				return fmt.Errorf("write error at offset %d: %w", currentWriteOffset, wErr)
			}

			currentWriteOffset += int64(n)
			chk.CompletedBytes += int64(n)
			newTotal := atomic.AddInt64(totalCompletedAtomic, int64(n))

			if opts.OnChunkProgress != nil {
				opts.OnChunkProgress(chk.Index, int64(n), chk.CompletedBytes)
			}
			if opts.OnTotalProgress != nil {
				opts.OnTotalProgress(newTotal, totalSize)
			}
		}

		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				break
			}
			return fmt.Errorf("stream read error: %w", rErr)
		}
	}

	if chk.CompletedBytes < chk.TotalChunkBytes() {
		return fmt.Errorf("unexpected short chunk read: got %d, expected %d", chk.CompletedBytes, chk.TotalChunkBytes())
	}

	return nil
}

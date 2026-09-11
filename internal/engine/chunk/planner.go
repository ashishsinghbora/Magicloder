package chunk

import (
	"errors"
	"fmt"

	"github.com/ashishsinghbora/magicloder/internal/model"
)

var (
	ErrInvalidFileSize    = errors.New("total file size must be greater than zero")
	ErrInvalidWorkerCount = errors.New("worker count must be greater than zero")
)

// PlanChunks divides a file of totalSize bytes into non-overlapping, inclusive byte ranges.
// It ensures complete coverage of [0, totalSize - 1] without gaps or overlaps.
func PlanChunks(totalSize int64, numWorkers int, minChunkSize int64) ([]model.Chunk, error) {
	if totalSize <= 0 {
		return nil, ErrInvalidFileSize
	}
	if numWorkers <= 0 {
		return nil, ErrInvalidWorkerCount
	}
	if minChunkSize <= 0 {
		minChunkSize = 64 * 1024 // 64 KiB minimum
	}

	// Calculate maximum possible workers based on minChunkSize
	maxWorkersByChunkSize := int(totalSize / minChunkSize)
	if maxWorkersByChunkSize < 1 {
		maxWorkersByChunkSize = 1
	}

	workers := numWorkers
	if workers > maxWorkersByChunkSize {
		workers = maxWorkersByChunkSize
	}
	if int64(workers) > totalSize {
		workers = int(totalSize)
	}

	baseChunkSize := totalSize / int64(workers)
	remainder := totalSize % int64(workers)

	chunks := make([]model.Chunk, workers)
	var currentStart int64 = 0

	for i := 0; i < workers; i++ {
		chunkLen := baseChunkSize
		if int64(i) < remainder {
			chunkLen++
		}

		end := currentStart + chunkLen - 1
		if i == workers-1 {
			end = totalSize - 1 // Enforce strict end alignment
		}

		chunks[i] = model.Chunk{
			Index:          i,
			StartByte:      currentStart,
			EndByte:        end,
			CompletedBytes: 0,
			Status:         model.ChunkPending,
		}

		currentStart = end + 1
	}

	// Invariant validation: verify no gaps, no overlaps, and exact coverage
	var totalCoverage int64 = 0
	for i, c := range chunks {
		if c.StartByte > c.EndByte {
			return nil, fmt.Errorf("chunk %d has invalid bounds [%d, %d]", i, c.StartByte, c.EndByte)
		}
		if i > 0 && c.StartByte != chunks[i-1].EndByte+1 {
			return nil, fmt.Errorf("gap or overlap between chunk %d and %d", i-1, i)
		}
		totalCoverage += (c.EndByte - c.StartByte) + 1
	}

	if totalCoverage != totalSize {
		return nil, fmt.Errorf("chunk coverage (%d) does not match total size (%d)", totalCoverage, totalSize)
	}

	return chunks, nil
}

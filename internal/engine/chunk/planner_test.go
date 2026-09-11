package chunk_test

import (
	"testing"

	"github.com/ashishsinghbora/magicloder/internal/engine/chunk"
)

func TestPlanChunks(t *testing.T) {
	tests := []struct {
		name         string
		totalSize    int64
		workers      int
		minChunkSize int64
		expectErr    bool
	}{
		{"100 bytes with 4 workers", 100, 4, 10, false},
		{"100 bytes with 3 workers (non-divisible)", 100, 3, 10, false},
		{"1 byte with 4 workers (smaller than workers)", 1, 4, 10, false},
		{"10 bytes with 100 workers", 10, 100, 1, false},
		{"large 5 GB file with 16 workers", 5 * 1024 * 1024 * 1024, 16, 1024 * 1024, false},
		{"zero total size", 0, 4, 10, true},
		{"negative total size", -10, 4, 10, true},
		{"zero worker count", 100, 0, 10, true},
		{"negative worker count", 100, -2, 10, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks, err := chunk.PlanChunks(tc.totalSize, tc.workers, tc.minChunkSize)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(chunks) == 0 {
				t.Fatalf("expected at least 1 chunk")
			}

			var covered int64 = 0
			for i, c := range chunks {
				if c.Index != i {
					t.Fatalf("chunk index mismatch: %d != %d", c.Index, i)
				}
				if c.StartByte > c.EndByte {
					t.Fatalf("chunk %d has start (%d) > end (%d)", i, c.StartByte, c.EndByte)
				}
				if i > 0 {
					if c.StartByte != chunks[i-1].EndByte+1 {
						t.Fatalf("gap/overlap between chunk %d [%d, %d] and chunk %d [%d, %d]",
							i-1, chunks[i-1].StartByte, chunks[i-1].EndByte, i, c.StartByte, c.EndByte)
					}
				}
				covered += (c.EndByte - c.StartByte) + 1
			}

			if covered != tc.totalSize {
				t.Fatalf("covered bytes (%d) != totalSize (%d)", covered, tc.totalSize)
			}
			if chunks[0].StartByte != 0 {
				t.Fatalf("first chunk does not start at 0: %d", chunks[0].StartByte)
			}
			if chunks[len(chunks)-1].EndByte != tc.totalSize-1 {
				t.Fatalf("last chunk does not end at totalSize-1: %d", chunks[len(chunks)-1].EndByte)
			}
		})
	}
}

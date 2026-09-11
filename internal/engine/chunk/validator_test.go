package chunk_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/ashishsinghbora/magicloder/internal/engine/chunk"
)

func TestParseContentRange(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		wantStart int64
		wantEnd   int64
		wantTotal int64
		expectErr bool
	}{
		{"valid standard", "bytes 0-499/1000", 0, 499, 1000, false},
		{"valid open total", "bytes 500-999/*", 500, 999, -1, false},
		{"empty header", "", 0, 0, 0, true},
		{"missing bytes prefix", "0-499/1000", 0, 0, 0, true},
		{"start greater than end", "bytes 500-400/1000", 0, 0, 0, true},
		{"total less than end", "bytes 0-500/500", 0, 0, 0, true},
		{"non-numeric start", "bytes abc-500/1000", 0, 0, 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := chunk.ParseContentRange(tc.header)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Start != tc.wantStart || res.End != tc.wantEnd || res.Total != tc.wantTotal {
				t.Fatalf("parsed %+v, want [%d, %d, %d]", res, tc.wantStart, tc.wantEnd, tc.wantTotal)
			}
		})
	}
}

func TestValidateRangeResponse(t *testing.T) {
	t.Run("valid 206 response", func(t *testing.T) {
		resp := &http.Response{
			StatusCode:    http.StatusPartialContent,
			ContentLength: 100,
			Header: http.Header{
				"Content-Range": []string{"bytes 0-99/1000"},
			},
		}
		res, err := chunk.ValidateRangeResponse(resp, 0, 99)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Start != 0 || res.End != 99 {
			t.Fatalf("expected [0, 99], got [%d, %d]", res.Start, res.End)
		}
	})

	t.Run("server returned 200 OK to range request", func(t *testing.T) {
		resp := &http.Response{
			StatusCode: http.StatusOK,
		}
		_, err := chunk.ValidateRangeResponse(resp, 0, 99)
		if !errors.Is(err, chunk.ErrServerIgnoredRange) {
			t.Fatalf("expected ErrServerIgnoredRange, got %v", err)
		}
	})

	t.Run("bounds mismatch", func(t *testing.T) {
		resp := &http.Response{
			StatusCode:    http.StatusPartialContent,
			ContentLength: 100,
			Header: http.Header{
				"Content-Range": []string{"bytes 100-199/1000"},
			},
		}
		_, err := chunk.ValidateRangeResponse(resp, 0, 99)
		if !errors.Is(err, chunk.ErrRangeBoundsMismatch) {
			t.Fatalf("expected ErrRangeBoundsMismatch, got %v", err)
		}
	})

	t.Run("body length mismatch", func(t *testing.T) {
		resp := &http.Response{
			StatusCode:    http.StatusPartialContent,
			ContentLength: 50, // Expecting 100
			Header: http.Header{
				"Content-Range": []string{"bytes 0-99/1000"},
			},
		}
		_, err := chunk.ValidateRangeResponse(resp, 0, 99)
		if !errors.Is(err, chunk.ErrBodyLengthMismatch) {
			t.Fatalf("expected ErrBodyLengthMismatch, got %v", err)
		}
	})
}

package chunk

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrServerIgnoredRange    = errors.New("server returned HTTP 200 instead of HTTP 206 Partial Content")
	ErrUnexpectedRangeStatus = errors.New("server returned unexpected HTTP status for range request")
	ErrMissingContentRange   = errors.New("missing Content-Range header in HTTP 206 response")
	ErrMalformedContentRange = errors.New("malformed Content-Range header")
	ErrRangeBoundsMismatch   = errors.New("returned range bounds do not match requested range")
	ErrBodyLengthMismatch    = errors.New("response body length does not match expected range size")
)

// contentRangeRegex matches "bytes START-END/TOTAL" where TOTAL can be digits or "*"
var contentRangeRegex = regexp.MustCompile(`^bytes\s+(\d+)-(\d+)/(\d+|\*)$`)

// ParsedContentRange holds the extracted values from a Content-Range header.
type ParsedContentRange struct {
	Start int64
	End   int64
	Total int64 // -1 if "*"
}

// ParseContentRange parses and validates a Content-Range header value.
func ParseContentRange(header string) (*ParsedContentRange, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil, ErrMissingContentRange
	}

	matches := contentRangeRegex.FindStringSubmatch(header)
	if len(matches) != 4 {
		return nil, fmt.Errorf("%w: %q", ErrMalformedContentRange, header)
	}

	start, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid start byte: %v", ErrMalformedContentRange, err)
	}

	end, err := strconv.ParseInt(matches[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid end byte: %v", ErrMalformedContentRange, err)
	}

	if start < 0 || end < start {
		return nil, fmt.Errorf("%w: start (%d) cannot exceed end (%d)", ErrMalformedContentRange, start, end)
	}

	var total int64 = -1
	if matches[3] != "*" {
		tot, err := strconv.ParseInt(matches[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid total size: %v", ErrMalformedContentRange, err)
		}
		if tot <= end {
			return nil, fmt.Errorf("%w: total size (%d) must be greater than end byte (%d)", ErrMalformedContentRange, tot, end)
		}
		total = tot
	}

	return &ParsedContentRange{
		Start: start,
		End:   end,
		Total: total,
	}, nil
}

// ValidateRangeResponse inspects an HTTP response returned for an HTTP Range request.
// It verifies HTTP 206 status, Content-Range headers, bound matching, and body lengths.
func ValidateRangeResponse(resp *http.Response, expectedStart, expectedEnd int64) (*ParsedContentRange, error) {
	if resp == nil {
		return nil, errors.New("nil HTTP response")
	}

	if resp.StatusCode == http.StatusOK {
		return nil, ErrServerIgnoredRange
	}

	if resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("%w: HTTP %d (%s)", ErrUnexpectedRangeStatus, resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	crHeader := resp.Header.Get("Content-Range")
	parsed, err := ParseContentRange(crHeader)
	if err != nil {
		return nil, err
	}

	if parsed.Start != expectedStart || parsed.End != expectedEnd {
		return nil, fmt.Errorf("%w: expected [%d, %d], server returned [%d, %d]", ErrRangeBoundsMismatch, expectedStart, expectedEnd, parsed.Start, parsed.End)
	}

	expectedLength := (expectedEnd - expectedStart) + 1
	if resp.ContentLength > 0 && resp.ContentLength != expectedLength {
		return nil, fmt.Errorf("%w: advertised Content-Length %d != expected range length %d", ErrBodyLengthMismatch, resp.ContentLength, expectedLength)
	}

	return parsed, nil
}

package downloader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/security"
)

var (
	ErrProbeFailed    = errors.New("HTTP probe failed")
	ErrNonSuccessHTTP = errors.New("server returned non-2xx HTTP status")
)

// ProbeResult contains metadata discovered during HTTP probing.
type ProbeResult struct {
	FinalURL          string
	StatusCode        int
	ContentLength     int64
	ContentType       string
	SupportsRange     bool
	ETag              string
	LastModified      string
	SuggestedFilename string
}

// Prober encapsulates the probe service.
type Prober struct {
	Client *http.Client
}

// NewProber creates a Prober with an optional HTTP client.
func NewProber(client *http.Client) *Prober {
	if client == nil {
		client = &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				}
				return nil
			},
		}
	}
	return &Prober{Client: client}
}

// Probe inspects a target URL, gathering metadata using HEAD and safe GET fallbacks.
func (p *Prober) Probe(ctx context.Context, targetURL string) (*ProbeResult, error) {
	if _, err := security.ValidateURLScheme(targetURL); err != nil {
		return nil, err
	}

	// 1. Attempt HEAD request
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HEAD request: %w", err)
	}
	req.Header.Set("User-Agent", "Magicloder/0.1.0")

	resp, err := p.Client.Do(req)
	var headErr error
	if err != nil {
		headErr = err
	} else {
		defer resp.Body.Close()
		// If HEAD succeeded with 2xx
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			res := parseResponseHeaders(resp, targetURL)
			return res, nil
		}
		// If server explicitly rejected HEAD (405, 501, or 403)
		headErr = fmt.Errorf("HEAD returned HTTP %d", resp.StatusCode)
	}

	// 2. Fallback: Safe GET with Range bytes=0-0 to discover metadata without downloading body
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET probe request (HEAD error: %v): %w", headErr, err)
	}
	getReq.Header.Set("User-Agent", "Magicloder/0.1.0")
	getReq.Header.Set("Range", "bytes=0-0")

	getResp, err := p.Client.Do(getReq)
	if err != nil {
		return nil, fmt.Errorf("GET fallback failed after HEAD error (%v): %w", headErr, err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode < 200 || getResp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: HTTP %d (%s)", ErrNonSuccessHTTP, getResp.StatusCode, http.StatusText(getResp.StatusCode))
	}

	res := parseResponseHeaders(getResp, targetURL)
	return res, nil
}

func parseResponseHeaders(resp *http.Response, originalURL string) *ProbeResult {
	finalURL := originalURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	contentLength := resp.ContentLength
	supportsRange := false

	// Check Accept-Ranges
	if strings.EqualFold(resp.Header.Get("Accept-Ranges"), "bytes") {
		supportsRange = true
	}

	// Check Content-Range (e.g. "bytes 0-0/12345" or "bytes 0-0/*")
	cr := resp.Header.Get("Content-Range")
	if cr != "" && resp.StatusCode == http.StatusPartialContent {
		supportsRange = true
		if parts := strings.Split(cr, "/"); len(parts) == 2 {
			if totalStr := strings.TrimSpace(parts[1]); totalStr != "*" {
				if total, err := strconv.ParseInt(totalStr, 10, 64); err == nil {
					contentLength = total
				}
			}
		}
	}

	// Filename resolution: prioritize Content-Disposition, then URL path
	filename := security.FilenameFromContentDisposition(resp.Header.Get("Content-Disposition"))
	if filename == "" {
		filename = security.FilenameFromURL(finalURL)
	}

	etag := strings.Trim(resp.Header.Get("ETag"), `" `)

	return &ProbeResult{
		FinalURL:          finalURL,
		StatusCode:        resp.StatusCode,
		ContentLength:     contentLength,
		ContentType:       resp.Header.Get("Content-Type"),
		SupportsRange:     supportsRange,
		ETag:              etag,
		LastModified:      resp.Header.Get("Last-Modified"),
		SuggestedFilename: filename,
	}
}

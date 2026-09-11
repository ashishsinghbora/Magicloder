package uploader

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/ashishsinghbora/magicloder/internal/model"
	"github.com/ashishsinghbora/magicloder/internal/storage"
)

var (
	ErrNotTusServer           = errors.New("server does not support Tus protocol 1.0.0")
	ErrUploadTerminated       = errors.New("upload was terminated or expired on server")
	ErrFileModified           = errors.New("source file was modified after upload creation")
	ErrOffsetMismatch         = errors.New("server reported offset exceeds local file size")
	ErrInvalidTusResponse     = errors.New("invalid Tus server response")
	ErrSourceFileNotFound     = errors.New("source upload file not found")
)

const (
	TusVersion       = "1.0.0"
	DefaultChunkSize = 2 * 1024 * 1024 // 2 MiB
)

// ProgressFunc reports upload progress.
type ProgressFunc func(uploadedBytes, totalBytes int64)

// TusClientOptions configures the Tus client.
type TusClientOptions struct {
	Client      *http.Client
	ChunkSize   int64
	MaxRetries  int
	OnProgress  ProgressFunc
}

// DefaultTusOptions returns default upload client options.
func DefaultTusOptions() *TusClientOptions {
	return &TusClientOptions{
		Client:     &http.Client{Timeout: 0},
		ChunkSize:  DefaultChunkSize,
		MaxRetries: 3,
		OnProgress: nil,
	}
}

// TusClient manages resumable file uploads conforming to Tus 1.0.0.
type TusClient struct {
	store storage.Store
}

// NewTusClient creates a new Tus uploader client.
func NewTusClient(store storage.Store) *TusClient {
	return &TusClient{store: store}
}

// ComputeFingerprint generates a unique identifier based on file size and modification time.
func ComputeFingerprint(filePath string) (string, int64, error) {
	fi, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, ErrSourceFileNotFound
		}
		return "", 0, err
	}
	fp := fmt.Sprintf("%d-%d", fi.Size(), fi.ModTime().UnixNano())
	return fp, fi.Size(), nil
}

// UploadFile uploads a file to a Tus endpoint with resumability.
func (c *TusClient) UploadFile(ctx context.Context, up *model.Upload, opts *TusClientOptions) error {
	if opts == nil {
		opts = DefaultTusOptions()
	}
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = DefaultChunkSize
	}

	// 1. Validate local file
	currFP, fileSize, err := ComputeFingerprint(up.SourcePath)
	if err != nil {
		return err
	}
	if up.FileFingerprint != "" && up.FileFingerprint != currFP {
		return ErrFileModified
	}
	up.FileFingerprint = currFP
	up.TotalSize = fileSize
	if up.Status == "" {
		up.Status = model.UploadQueued
	}

	file, err := os.Open(up.SourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer file.Close()

	// 2. Discover existing offset or Create new upload
	offset := int64(0)
	if up.UploadURL != "" {
		serverOffset, err := c.queryOffset(ctx, opts.Client, up.UploadURL)
		if err == nil {
			offset = serverOffset
		} else if errors.Is(err, ErrUploadTerminated) {
			// Upload URL expired; recreate below
			up.UploadURL = ""
		} else {
			return fmt.Errorf("failed to query upload offset: %w", err)
		}
	}

	if up.UploadURL == "" {
		uploadURL, err := c.createUpload(ctx, opts.Client, up.TusEndpoint, fileSize, up.Metadata)
		if err != nil {
			return fmt.Errorf("failed to create Tus upload: %w", err)
		}
		up.UploadURL = uploadURL
		offset = 0
		if c.store != nil {
			_ = c.store.UpdateUpload(ctx, up)
		}
	}

	if offset > fileSize {
		return fmt.Errorf("%w: server offset %d > local file size %d", ErrOffsetMismatch, offset, fileSize)
	}

	up.UploadedBytes = offset
	_ = up.TransitionTo(model.UploadUploading)
	if c.store != nil {
		_ = c.store.UpdateUploadProgress(ctx, up.ID, offset, up.UploadURL, model.UploadUploading)
	}

	// 3. Upload chunks via PATCH until complete
	for offset < fileSize {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		remaining := fileSize - offset
		chunkLen := opts.ChunkSize
		if remaining < chunkLen {
			chunkLen = remaining
		}

		// Stream bounded chunk without buffering entire file in memory
		sectionReader := io.NewSectionReader(file, offset, chunkLen)

		newOffset, pErr := c.patchChunk(ctx, opts.Client, up.UploadURL, sectionReader, offset, chunkLen)
		if pErr != nil {
			return fmt.Errorf("PATCH chunk error at offset %d: %w", offset, pErr)
		}

		offset = newOffset
		up.UploadedBytes = offset
		if opts.OnProgress != nil {
			opts.OnProgress(offset, fileSize)
		}

		if c.store != nil {
			_ = c.store.UpdateUploadProgress(ctx, up.ID, offset, up.UploadURL, model.UploadUploading)
		}
	}

	_ = up.TransitionTo(model.UploadCompleted)
	if c.store != nil {
		_ = c.store.UpdateUploadProgress(ctx, up.ID, fileSize, up.UploadURL, model.UploadCompleted)
	}

	return nil
}

func (c *TusClient) createUpload(ctx context.Context, client *http.Client, endpoint string, totalSize int64, metadata string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Tus-Resumable", TusVersion)
	req.Header.Set("Upload-Length", strconv.FormatInt(totalSize, 10))

	if metadata != "" {
		req.Header.Set("Upload-Metadata", encodeMetadata(metadata))
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("%w: expected 201 Created, got HTTP %d", ErrInvalidTusResponse, resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("%w: missing Location header in response", ErrInvalidTusResponse)
	}

	// Resolve relative Location
	uploadURL, err := resp.Request.URL.Parse(location)
	if err != nil {
		return location, nil
	}
	return uploadURL.String(), nil
}

func (c *TusClient) queryOffset(ctx context.Context, client *http.Client, uploadURL string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, uploadURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Tus-Resumable", TusVersion)

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return 0, ErrUploadTerminated
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return 0, fmt.Errorf("%w: HEAD returned HTTP %d", ErrInvalidTusResponse, resp.StatusCode)
	}

	offsetStr := resp.Header.Get("Upload-Offset")
	if offsetStr == "" {
		return 0, fmt.Errorf("%w: missing Upload-Offset header", ErrInvalidTusResponse)
	}

	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid Upload-Offset header: %v", ErrInvalidTusResponse, err)
	}

	return offset, nil
}

func (c *TusClient) patchChunk(ctx context.Context, client *http.Client, uploadURL string, r io.Reader, offset, length int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, uploadURL, r)
	if err != nil {
		return 0, err
	}

	req.Header.Set("Tus-Resumable", TusVersion)
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Content-Length", strconv.FormatInt(length, 10))

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return 0, fmt.Errorf("%w: PATCH returned HTTP %d", ErrInvalidTusResponse, resp.StatusCode)
	}

	newOffsetStr := resp.Header.Get("Upload-Offset")
	if newOffsetStr == "" {
		return offset + length, nil
	}

	newOffset, err := strconv.ParseInt(newOffsetStr, 10, 64)
	if err != nil {
		return offset + length, nil
	}

	return newOffset, nil
}

// CancelUpload issues a DELETE request to terminate an upload on the server.
func (c *TusClient) CancelUpload(ctx context.Context, client *http.Client, uploadURL string) error {
	if uploadURL == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, uploadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Tus-Resumable", TusVersion)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func encodeMetadata(raw string) string {
	parts := strings.Split(raw, ",")
	var encoded []string
	for _, p := range parts {
		kv := strings.SplitN(strings.TrimSpace(p), " ", 2)
		if len(kv) == 2 {
			enc := base64.StdEncoding.EncodeToString([]byte(kv[1]))
			encoded = append(encoded, fmt.Sprintf("%s %s", kv[0], enc))
		} else {
			encoded = append(encoded, kv[0])
		}
	}
	return strings.Join(encoded, ",")
}

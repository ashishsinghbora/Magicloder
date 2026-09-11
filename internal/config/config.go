package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ErrInvalidConfig = errors.New("invalid configuration")
)

// Config encapsulates runtime configuration for Magicloder.
type Config struct {
	// Paths
	DataDir     string `json:"data_dir"`
	DownloadDir string `json:"download_dir"`

	// Networking & API
	BindAddr      string `json:"bind_addr"`
	RemoteEnabled bool   `json:"remote_enabled"`
	AuthToken     string `json:"auth_token,omitempty"`

	// Engine limits
	MaxConcurrentDownloads    int   `json:"max_concurrent_downloads"`
	DefaultWorkersPerDownload int   `json:"default_workers_per_download"`
	MinChunkSize              int64 `json:"min_chunk_size"`
	MaxChunkSize              int64 `json:"max_chunk_size"`
	MaxGlobalBandwidth        int64 `json:"max_global_bandwidth"` // Bytes per second, 0 = unlimited

	// Tus Uploader settings
	TusDefaultChunkSize int64 `json:"tus_default_chunk_size"`
}

// DefaultConfig returns safe production defaults.
func DefaultConfig() *Config {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}

	dataDir := filepath.Join(home, ".magicloder")
	downloadDir := filepath.Join(home, "Downloads")

	return &Config{
		DataDir:                   dataDir,
		DownloadDir:               downloadDir,
		BindAddr:                  "127.0.0.1:8321",
		RemoteEnabled:             false,
		AuthToken:                 "",
		MaxConcurrentDownloads:    3,
		DefaultWorkersPerDownload: 4,
		MinChunkSize:              1024 * 1024,      // 1 MiB
		MaxChunkSize:              64 * 1024 * 1024, // 64 MiB
		MaxGlobalBandwidth:        0,                // Unlimited
		TusDefaultChunkSize:       2 * 1024 * 1024,  // 2 MiB
	}
}

// Validate checks configuration invariants.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.DataDir) == "" {
		return fmt.Errorf("%w: data_dir cannot be empty", ErrInvalidConfig)
	}
	if strings.TrimSpace(c.DownloadDir) == "" {
		return fmt.Errorf("%w: download_dir cannot be empty", ErrInvalidConfig)
	}

	host, portStr, err := net.SplitHostPort(c.BindAddr)
	if err != nil {
		return fmt.Errorf("%w: invalid bind_addr %q: %v", ErrInvalidConfig, c.BindAddr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("%w: invalid port in bind_addr %q", ErrInvalidConfig, c.BindAddr)
	}

	// Loopback enforcement if remote mode is disabled
	if !c.RemoteEnabled {
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("%w: bind_addr %q must be a loopback address unless remote_enabled is true", ErrInvalidConfig, c.BindAddr)
		}
	} else {
		// Remote mode requires auth token
		if strings.TrimSpace(c.AuthToken) == "" {
			return fmt.Errorf("%w: auth_token is required when remote_enabled is true", ErrInvalidConfig)
		}
	}

	if c.MaxConcurrentDownloads < 1 || c.MaxConcurrentDownloads > 100 {
		return fmt.Errorf("%w: max_concurrent_downloads must be between 1 and 100", ErrInvalidConfig)
	}
	if c.DefaultWorkersPerDownload < 1 || c.DefaultWorkersPerDownload > 32 {
		return fmt.Errorf("%w: default_workers_per_download must be between 1 and 32", ErrInvalidConfig)
	}
	if c.MinChunkSize < 64*1024 { // 64 KiB minimum
		return fmt.Errorf("%w: min_chunk_size must be at least 64 KiB", ErrInvalidConfig)
	}
	if c.MaxChunkSize < c.MinChunkSize {
		return fmt.Errorf("%w: max_chunk_size cannot be smaller than min_chunk_size", ErrInvalidConfig)
	}
	if c.MaxGlobalBandwidth < 0 {
		return fmt.Errorf("%w: max_global_bandwidth cannot be negative", ErrInvalidConfig)
	}
	if c.TusDefaultChunkSize < 256*1024 { // 256 KiB
		return fmt.Errorf("%w: tus_default_chunk_size must be at least 256 KiB", ErrInvalidConfig)
	}

	return nil
}

// EnsureDirectories creates DataDir and DownloadDir if they don't exist.
func (c *Config) EnsureDirectories() error {
	if err := os.MkdirAll(c.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create data directory %s: %w", c.DataDir, err)
	}
	if err := os.MkdirAll(c.DownloadDir, 0755); err != nil {
		return fmt.Errorf("failed to create download directory %s: %w", c.DownloadDir, err)
	}
	return nil
}

// DatabasePath returns the SQLite DB path within DataDir.
func (c *Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "magicloder.db")
}

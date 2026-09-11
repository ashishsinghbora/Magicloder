package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ashishsinghbora/magicloder/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultConfig should be valid: %v", err)
	}

	if cfg.BindAddr != "127.0.0.1:8321" {
		t.Fatalf("unexpected default bind addr: %s", cfg.BindAddr)
	}
	if cfg.RemoteEnabled {
		t.Fatalf("remote_enabled must be false by default")
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		modify    func(c *config.Config)
		expectErr bool
	}{
		{
			name:      "valid default",
			modify:    func(c *config.Config) {},
			expectErr: false,
		},
		{
			name: "empty data dir",
			modify: func(c *config.Config) {
				c.DataDir = ""
			},
			expectErr: true,
		},
		{
			name: "empty download dir",
			modify: func(c *config.Config) {
				c.DownloadDir = ""
			},
			expectErr: true,
		},
		{
			name: "invalid bind address format",
			modify: func(c *config.Config) {
				c.BindAddr = "not-a-valid-address"
			},
			expectErr: true,
		},
		{
			name: "non-loopback without remote enabled",
			modify: func(c *config.Config) {
				c.BindAddr = "0.0.0.0:8321"
				c.RemoteEnabled = false
			},
			expectErr: true,
		},
		{
			name: "remote enabled without auth token",
			modify: func(c *config.Config) {
				c.BindAddr = "0.0.0.0:8321"
				c.RemoteEnabled = true
				c.AuthToken = ""
			},
			expectErr: true,
		},
		{
			name: "remote enabled with auth token",
			modify: func(c *config.Config) {
				c.BindAddr = "0.0.0.0:8321"
				c.RemoteEnabled = true
				c.AuthToken = "super-secret-token-12345"
			},
			expectErr: false,
		},
		{
			name: "zero concurrent downloads",
			modify: func(c *config.Config) {
				c.MaxConcurrentDownloads = 0
			},
			expectErr: true,
		},
		{
			name: "min chunk size too small",
			modify: func(c *config.Config) {
				c.MinChunkSize = 1024
			},
			expectErr: true,
		},
		{
			name: "max chunk size smaller than min",
			modify: func(c *config.Config) {
				c.MinChunkSize = 1024 * 1024
				c.MaxChunkSize = 512 * 1024
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			tc.modify(cfg)
			err := cfg.Validate()
			if tc.expectErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestEnsureDirectories(t *testing.T) {
	tempBase, err := os.MkdirTemp("", "magicloder-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp base: %v", err)
	}
	defer os.RemoveAll(tempBase)

	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(tempBase, "data")
	cfg.DownloadDir = filepath.Join(tempBase, "downloads")

	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	if fi, err := os.Stat(cfg.DataDir); err != nil || !fi.IsDir() {
		t.Fatalf("DataDir was not created")
	}
	if fi, err := os.Stat(cfg.DownloadDir); err != nil || !fi.IsDir() {
		t.Fatalf("DownloadDir was not created")
	}
}

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ashishsinghbora/magicloder/internal/config"
	"github.com/ashishsinghbora/magicloder/internal/version"
)

const usageText = `Magicloder - High-performance concurrent download and upload manager

Usage:
  magicloder [command] [flags]
  magicloder [flags]

Available Commands:
  serve       Start the headless transfer daemon and API server
  add         Add a new download or upload task
  list        List all transfer tasks
  info        Display detailed information about a task
  pause       Pause an active task
  resume      Resume a paused or interrupted task
  cancel      Cancel an active or queued task
  retry       Retry a failed task
  remove      Remove a task and optionally delete its files
  version     Display version information

Flags:
  -v, --version  Show application version
  -h, --help     Show this help message
      --config   Path to configuration file

Use "magicloder [command] --help" for more information about a command.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usageText)
		os.Exit(0)
	}

	arg := os.Args[1]
	switch arg {
	case "-v", "--version", "version":
		fmt.Println(version.String())
		os.Exit(0)

	case "-h", "--help", "help":
		fmt.Print(usageText)
		os.Exit(0)

	case "serve":
		runServe(os.Args[2:])

	case "add", "list", "info", "pause", "resume", "cancel", "retry", "remove":
		// Command dispatcher placeholder for phase 1
		fmt.Printf("Command %q registered (full CLI client configured in Phase 13)\n", arg)
		os.Exit(0)

	default:
		fmt.Fprintf(os.Stderr, "Unknown command or flag: %s\n\nRun 'magicloder --help' for usage.\n", arg)
		os.Exit(1)
	}
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	bindAddr := fs.String("bind", "127.0.0.1:8321", "HTTP API listen address")
	dataDir := fs.String("data-dir", "", "Directory to store database and internal state")
	downloadDir := fs.String("download-dir", "", "Default directory to save downloaded files")
	remote := fs.Bool("remote", false, "Enable remote access (requires auth-token)")
	token := fs.String("auth-token", "", "Bearer token for API authorization")
	_ = fs.Parse(args)

	cfg := config.DefaultConfig()
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *downloadDir != "" {
		cfg.DownloadDir = *downloadDir
	}
	if *bindAddr != "" {
		cfg.BindAddr = *bindAddr
	}
	cfg.RemoteEnabled = *remote
	cfg.AuthToken = *token

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.EnsureDirectories(); err != nil {
		fmt.Fprintf(os.Stderr, "Filesystem error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Starting %s on %s (data: %s, downloads: %s)\n", version.String(), cfg.BindAddr, cfg.DataDir, cfg.DownloadDir)
}

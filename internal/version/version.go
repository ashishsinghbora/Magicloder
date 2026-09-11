package version

import "fmt"

var (
	// Version is the current semantic version of Magicloder.
	Version = "0.1.0"
	// GitCommit is injected at build time.
	GitCommit = "dev"
	// BuildDate is injected at build time.
	BuildDate = "unknown"
)

// String returns a formatted version string.
func String() string {
	return fmt.Sprintf("Magicloder v%s (%s, built %s)", Version, GitCommit, BuildDate)
}

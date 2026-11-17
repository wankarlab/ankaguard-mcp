// Package version provides version information for the trading-mcp service.
// Build-time variables are set via ldflags during compilation.
package version

import (
	"runtime"
	"time"
)

// Build-time variables set via ldflags
// Example: go build -ldflags "-X github.com/rantipay/trading-mcp/internal/version.Version=1.0.0 -X github.com/rantipay/trading-mcp/internal/version.BuildTime=2026-01-01T00:00:00Z -X github.com/rantipay/trading-mcp/internal/version.GitCommit=abc123"
var (
	// Version is the semantic version of the service
	Version = "1.0.0"

	// BuildTime is the time the binary was built (RFC3339 format)
	BuildTime = "unknown"

	// GitCommit is the git commit hash
	GitCommit = "unknown"

	// GitBranch is the git branch name
	GitBranch = "unknown"

	// Environment is the deployment environment (development, staging, production)
	Environment = "development"
)

// ServiceName is the name of this service
const ServiceName = "trading-mcp"

// startTime tracks when the service started
var startTime = time.Now()

// Info contains all version and build information
type Info struct {
	Service       string `json:"service"`
	Version       string `json:"version"`
	BuildTime     string `json:"build_time"`
	GitCommit     string `json:"git_commit"`
	GitBranch     string `json:"git_branch"`
	GoVersion     string `json:"go_version"`
	Platform      string `json:"platform"`
	Environment   string `json:"environment"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

// GetInfo returns the complete version information
func GetInfo() Info {
	return Info{
		Service:       ServiceName,
		Version:       Version,
		BuildTime:     BuildTime,
		GitCommit:     GitCommit,
		GitBranch:     GitBranch,
		GoVersion:     runtime.Version(),
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		Environment:   Environment,
		UptimeSeconds: GetUptimeSeconds(),
	}
}

// GetVersion returns the current version string
func GetVersion() string {
	return Version
}

// GetBuildTime returns the build time
func GetBuildTime() string {
	return BuildTime
}

// GetGitCommit returns the git commit hash
func GetGitCommit() string {
	return GitCommit
}

// GetGoVersion returns the Go version used to build the binary
func GetGoVersion() string {
	return runtime.Version()
}

// GetUptime returns the duration since the service started
func GetUptime() time.Duration {
	return time.Since(startTime)
}

// GetUptimeSeconds returns the uptime in seconds
func GetUptimeSeconds() int64 {
	return int64(time.Since(startTime).Seconds())
}

// GetStartTime returns when the service started
func GetStartTime() time.Time {
	return startTime
}

// GetEnvironment returns the deployment environment
func GetEnvironment() string {
	return Environment
}

// SetEnvironment sets the deployment environment (typically called at startup)
func SetEnvironment(env string) {
	if env != "" {
		Environment = env
	}
}

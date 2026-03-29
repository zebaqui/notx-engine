package buildinfo

// These variables are set at build time via -ldflags.
// Default values are used when building without the build script (e.g. go run).
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

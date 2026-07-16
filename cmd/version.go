package cmd

// Build metadata, injected by goreleaser via -ldflags -X at release time.
// Defaults are used for local `go build`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

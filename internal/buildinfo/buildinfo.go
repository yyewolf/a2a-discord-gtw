// Package buildinfo holds version metadata injected via -ldflags at build time.
package buildinfo

// Set with -X github.com/yyewolf/a2a-discord-gtw/internal/buildinfo.Version=... etc.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// UserAgent is sent on every request to kagent.
func UserAgent() string {
	return "a2a-discord-gtw/" + Version + " (+https://github.com/yyewolf/a2a-discord-gtw)"
}

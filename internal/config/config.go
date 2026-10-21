// Package config loads the gateway's runtime configuration: process settings
// from the environment and flags, and the agent layout from a YAML file.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
)

// Config is the process configuration. Zero values are not meaningful; use FromEnv.
type Config struct {
	ConfigPath string
	DataDir    string
	Listen     string
	LogLevel   string

	// DiscordToken is read from ADG_DISCORD_TOKEN or the file named by
	// ADG_DISCORD_TOKEN_FILE. It has no flag, so it never shows up in ps.
	DiscordToken string
}

// FromEnv builds a Config from ADG_* environment variables with defaults.
func FromEnv() Config {
	return Config{
		ConfigPath:   envOr("ADG_CONFIG", "/config/config.yaml"),
		DataDir:      envOr("ADG_DATA_DIR", "/data"),
		Listen:       envOr("ADG_LISTEN", ":8080"),
		LogLevel:     envOr("ADG_LOG_LEVEL", "info"),
		DiscordToken: os.Getenv("ADG_DISCORD_TOKEN"),
	}
}

// BindFlags registers flags whose defaults are the current (env-derived) values.
func (c *Config) BindFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.ConfigPath, "config", c.ConfigPath, "path to the YAML agent configuration (ADG_CONFIG)")
	fs.StringVar(&c.DataDir, "data-dir", c.DataDir, "data directory for the state database (ADG_DATA_DIR)")
	fs.StringVar(&c.Listen, "listen", c.Listen, "health endpoint listen address (ADG_LISTEN)")
	fs.StringVar(&c.LogLevel, "log-level", c.LogLevel, "debug, info, warn or error (ADG_LOG_LEVEL)")
}

// LoadToken fills DiscordToken from ADG_DISCORD_TOKEN_FILE when the token is
// not already set. A trailing newline in the file is ignored.
func (c *Config) LoadToken() error {
	if c.DiscordToken != "" {
		return nil
	}
	path := os.Getenv("ADG_DISCORD_TOKEN_FILE")
	if path == "" {
		return errors.New("config: ADG_DISCORD_TOKEN or ADG_DISCORD_TOKEN_FILE must be set")
	}
	b, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // the path is the operator's own setting
	if err != nil {
		return fmt.Errorf("config: read discord token: %w", err)
	}
	c.DiscordToken = strings.TrimSpace(string(b))
	if c.DiscordToken == "" {
		return fmt.Errorf("config: discord token file %s is empty", path)
	}
	return nil
}

// Validate checks the process settings.
func (c *Config) Validate() error {
	if c.DataDir == "" {
		return errors.New("config: data dir must not be empty")
	}
	if c.ConfigPath == "" {
		return errors.New("config: config path must not be empty")
	}
	if _, err := ParseLogLevel(c.LogLevel); err != nil {
		return err
	}
	return nil
}

// DBPath is the SQLite state database.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "a2a-discord-gtw.db") }

// ParseLogLevel maps a case-insensitive level name to slog.Level.
func ParseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("config: unknown log level %q", s)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"

	"github.com/yyewolf/a2a-discord-gtw/internal/config"
)

func TestFromEnvDefaults(t *testing.T) {
	for _, k := range []string{"ADG_CONFIG", "ADG_DATA_DIR", "ADG_LISTEN", "ADG_LOG_LEVEL", "ADG_DISCORD_TOKEN"} {
		t.Setenv(k, "")
	}
	c := config.FromEnv()
	if c.ConfigPath != "/config/config.yaml" || c.DataDir != "/data" || c.Listen != ":8080" || c.LogLevel != "info" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFlagsOverrideEnv(t *testing.T) {
	t.Setenv("ADG_DATA_DIR", "/from-env")
	c := config.FromEnv()
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	c.BindFlags(fs)
	if err := fs.Parse([]string{"--data-dir", "/from-flag"}); err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "/from-flag" {
		t.Fatalf("data dir = %q", c.DataDir)
	}
	if c.DBPath() != filepath.Join("/from-flag", "a2a-discord-gtw.db") {
		t.Fatalf("db path = %q", c.DBPath())
	}
}

func TestValidateRejectsBadLogLevel(t *testing.T) {
	c := config.FromEnv()
	c.LogLevel = "loud"
	if err := c.Validate(); err == nil {
		t.Fatal("expected an error")
	}
}

func TestLoadTokenFromFile(t *testing.T) {
	t.Setenv("ADG_DISCORD_TOKEN", "")
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("abc.def\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADG_DISCORD_TOKEN_FILE", path)
	c := config.FromEnv()
	if err := c.LoadToken(); err != nil {
		t.Fatal(err)
	}
	if c.DiscordToken != "abc.def" {
		t.Fatalf("token = %q", c.DiscordToken)
	}
}

func TestLoadTokenPrefersEnv(t *testing.T) {
	t.Setenv("ADG_DISCORD_TOKEN", "from-env")
	t.Setenv("ADG_DISCORD_TOKEN_FILE", "/does/not/exist")
	c := config.FromEnv()
	if err := c.LoadToken(); err != nil || c.DiscordToken != "from-env" {
		t.Fatalf("token = %q, err = %v", c.DiscordToken, err)
	}
}

func TestLoadTokenMissing(t *testing.T) {
	t.Setenv("ADG_DISCORD_TOKEN", "")
	t.Setenv("ADG_DISCORD_TOKEN_FILE", "")
	c := config.FromEnv()
	if err := c.LoadToken(); err == nil {
		t.Fatal("expected an error")
	}
}

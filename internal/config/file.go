package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// File is the YAML configuration: where the bot lives on Discord, how to reach
// kagent, and which agents it fronts.
type File struct {
	Discord Discord `yaml:"discord"`
	Kagent  Kagent  `yaml:"kagent"`
	Watch   Watch   `yaml:"watch"`
	Agents  []Agent `yaml:"agents"`
}

// Discord places the bot. Every agent gets a text channel inside CategoryID.
type Discord struct {
	GuildID    string `yaml:"guild_id"`
	CategoryID string `yaml:"category_id"`
	// AllowedUserIDs are the only users whose messages and button presses
	// reach an agent. kagent's API is unauthenticated, so this is the access
	// control.
	AllowedUserIDs []string `yaml:"allowed_user_ids"`
	// NotifyUserIDs are pinged when an agent needs input. Defaults to
	// AllowedUserIDs.
	NotifyUserIDs []string `yaml:"notify_user_ids"`
}

// Kagent is the controller's A2A gateway.
type Kagent struct {
	URL string `yaml:"url"`
	// UserID is sent as X-User-Id. Empty means kagent's default user, which is
	// also the creator of schedules made in its UI.
	UserID string `yaml:"user_id"`
}

// Watch tunes polling for tasks the bot did not start.
type Watch struct {
	Interval time.Duration `yaml:"interval"`
}

// Agent is one kagent agent shown on Discord.
type Agent struct {
	// Name is the Discord channel name and the /ask choice.
	Name string `yaml:"name"`
	// Ref is namespace/name in kagent.
	Ref string `yaml:"ref"`
	// Watch surfaces tasks the bot did not start, such as ScheduledRuns.
	Watch bool `yaml:"watch"`
}

// Namespace and AgentName split Ref.
func (a Agent) Namespace() string { ns, _, _ := strings.Cut(a.Ref, "/"); return ns }

// AgentName is the kagent name half of Ref.
func (a Agent) AgentName() string { _, n, _ := strings.Cut(a.Ref, "/"); return n }

// URL is the agent's A2A JSON-RPC endpoint under the kagent base URL, which
// may carry a path prefix of its own.
func (a Agent) URL(base string) (string, error) {
	u, err := url.JoinPath(base, "agents", a.Namespace(), a.AgentName())
	if err != nil {
		return "", fmt.Errorf("config: url of agent %s: %w", a.Name, err)
	}
	return u, nil
}

const (
	defaultWatchInterval = 20 * time.Second
	minWatchInterval     = 5 * time.Second
	// maxAgents is Discord's limit on choices for one slash command option.
	maxAgents = 25
)

var (
	channelName = regexp.MustCompile(`^[a-z0-9_-]{1,100}$`)
	k8sName     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
)

// LoadFile reads, defaults and validates the YAML configuration.
func LoadFile(path string) (*File, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return ParseFile(b)
}

// ParseFile decodes and validates YAML configuration. Unknown keys are errors,
// so a typo does not silently drop a setting.
func ParseFile(b []byte) (*File, error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	f.applyDefaults()
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

func (f *File) applyDefaults() {
	if len(f.Discord.NotifyUserIDs) == 0 {
		f.Discord.NotifyUserIDs = f.Discord.AllowedUserIDs
	}
	if f.Watch.Interval == 0 {
		f.Watch.Interval = defaultWatchInterval
	}
}

// Validate checks the file after defaults are applied.
func (f *File) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf("config: "+format, a...)) }

	if !isSnowflake(f.Discord.GuildID) {
		add("discord.guild_id must be a Discord ID, got %q", f.Discord.GuildID)
	}
	if !isSnowflake(f.Discord.CategoryID) {
		add("discord.category_id must be a Discord ID, got %q", f.Discord.CategoryID)
	}
	if len(f.Discord.AllowedUserIDs) == 0 {
		add("discord.allowed_user_ids must list at least one user")
	}
	for _, id := range append(append([]string{}, f.Discord.AllowedUserIDs...), f.Discord.NotifyUserIDs...) {
		if !isSnowflake(id) {
			add("discord user ID %q is not a Discord ID", id)
		}
	}
	if u, err := url.Parse(f.Kagent.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		add("kagent.url must be an absolute http(s) URL, got %q", f.Kagent.URL)
	}
	if f.Watch.Interval < minWatchInterval {
		add("watch.interval must be at least %s, got %s", minWatchInterval, f.Watch.Interval)
	}
	if len(f.Agents) == 0 {
		add("agents must list at least one agent")
	}
	if len(f.Agents) > maxAgents {
		add("at most %d agents are supported, got %d", maxAgents, len(f.Agents))
	}
	seen := map[string]bool{}
	for i, a := range f.Agents {
		if !channelName.MatchString(a.Name) {
			add("agents[%d].name %q must be a Discord channel name (lowercase letters, digits, - and _)", i, a.Name)
		}
		if seen[a.Name] {
			add("agents[%d].name %q is used twice", i, a.Name)
		}
		seen[a.Name] = true
		ns, name, ok := strings.Cut(a.Ref, "/")
		if !ok || !k8sName.MatchString(ns) || !k8sName.MatchString(name) {
			add("agents[%d].ref must be namespace/name, got %q", i, a.Ref)
		}
	}
	return errors.Join(errs...)
}

// Agent returns the agent with the given Discord name.
func (f *File) Agent(name string) (Agent, bool) {
	for _, a := range f.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

func isSnowflake(s string) bool {
	n, err := strconv.ParseUint(s, 10, 64)
	return err == nil && n > 0
}

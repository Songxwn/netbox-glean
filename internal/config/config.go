// Package config parses command line flags and environment variables.
package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the process level configuration. Per-device and NetBox settings
// live in the JSON store and are edited through the web UI.
type Config struct {
	Listen      string
	DataDir     string
	WebUsername string
	WebPassword string
	NetboxURL   string
	NetboxToken string
	Site        string
	DeviceRole  string
	Concurrency int
	Timeout     time.Duration
	Debug       bool
	ShowVersion bool
}

// Load reads defaults from the environment and lets flags override them.
func Load() (Config, error) {
	cfg := Config{
		Listen:      env("NETBOX_GLEAN_LISTEN", "0.0.0.0:8080"),
		DataDir:     env("NETBOX_GLEAN_DATA_DIR", "data"),
		WebUsername: env("NETBOX_GLEAN_WEB_USERNAME", ""),
		WebPassword: env("NETBOX_GLEAN_WEB_PASSWORD", ""),
		NetboxURL:   env("NETBOX_GLEAN_NETBOX_URL", ""),
		NetboxToken: env("NETBOX_GLEAN_NETBOX_TOKEN", ""),
		Site:        env("NETBOX_GLEAN_SITE", ""),
		DeviceRole:  env("NETBOX_GLEAN_DEVICE_ROLE", ""),
		Concurrency: envInt("NETBOX_GLEAN_CONCURRENCY", 4),
		Timeout:     envDuration("NETBOX_GLEAN_TIMEOUT", 10*time.Minute),
		Debug:       envBool("NETBOX_GLEAN_DEBUG", false),
	}

	fs := flag.NewFlagSet("netbox-glean", flag.ContinueOnError)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "HTTP listen address")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "directory for settings and targets")
	fs.StringVar(&cfg.WebUsername, "web-user", cfg.WebUsername, "optional HTTP basic auth username")
	fs.StringVar(&cfg.WebPassword, "web-password", cfg.WebPassword, "optional HTTP basic auth password")
	fs.StringVar(&cfg.NetboxURL, "netbox-url", cfg.NetboxURL, "initial NetBox URL")
	fs.StringVar(&cfg.NetboxToken, "netbox-token", cfg.NetboxToken, "initial NetBox API token")
	fs.StringVar(&cfg.Site, "site", cfg.Site, "initial default NetBox site slug")
	fs.StringVar(&cfg.DeviceRole, "device-role", cfg.DeviceRole, "initial default NetBox device role slug")
	fs.IntVar(&cfg.Concurrency, "concurrency", cfg.Concurrency, "maximum concurrent collection jobs")
	fs.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "timeout per collection job")
	fs.BoolVar(&cfg.Debug, "debug", cfg.Debug, "enable debug logging")
	fs.BoolVar(&cfg.ShowVersion, "version", false, "print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return Config{}, err
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return fallback
}

// Describe returns a printable summary safe for logs (no secrets).
func (c Config) Describe() string {
	return fmt.Sprintf("listen=%s data=%s concurrency=%d timeout=%s", c.Listen, c.DataDir, c.Concurrency, c.Timeout)
}
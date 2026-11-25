package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
)

// Config holds typed runtime configuration loaded from the environment.
// ponytail: only fields needed by the foundation phase are validated here.
// Secret/DB validation lands with Plan 02-03 when those consumers exist.
type Config struct {
	Env         string
	Host        string
	Port        int
	EngineURL   string
	EngineToken string
}

// Load reads configuration from the environment and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		Env:         env("APP_ENV", "development"),
		Host:        env("API_HOST", "127.0.0.1"),
		Port:        envInt("API_PORT", 8080),
		EngineURL:   env("ENGINE_URL", "http://127.0.0.1:8000"),
		EngineToken: env("ENGINE_INTERNAL_TOKEN", ""),
	}

	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("API_PORT out of range: %d", cfg.Port)
	}
	if _, err := url.ParseRequestURI(cfg.EngineURL); err != nil {
		return nil, fmt.Errorf("ENGINE_URL invalid: %w", err)
	}
	return cfg, nil
}

// HTTPAddr returns the listen address for the HTTP server.
func (c *Config) HTTPAddr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

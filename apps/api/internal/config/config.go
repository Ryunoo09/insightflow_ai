package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Development fallbacks. Load flags these as insecure so startup can warn.
const (
	devJWTSecret     = "dev-only-insecure-jwt-secret-replace-me-32"
	devOTPSecret     = "dev-only-insecure-otp-secret-replace-me"
	devDatabaseURL   = "postgres://postgres:postgres@127.0.0.1:5432/insightflow_dev?sslmode=disable"
	devRedisURL      = "redis://127.0.0.1:6379/0"
	devAllowedOrigin = "http://localhost:5173"
)

// Config holds typed runtime configuration loaded from the environment.
type Config struct {
	Env         string
	Host        string
	Port        int
	EngineURL   string
	EngineToken string

	DatabaseURL string
	RedisURL    string

	JWTSecret   string
	JWTIssuer   string
	JWTAudience string
	OTPSecret   string

	OTPTTL             time.Duration
	OTPMaxAttempts     int
	OTPResendInterval  time.Duration
	OTPMaxSendsPerHour int

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	WebBaseURL         string

	SMTPHost     string
	SMTPPort     int
	SMTPFrom     string
	SMTPUsername string
	SMTPPassword string

	AllowedOrigins []string

	// InsecureDefaults is true when development fallback values were used.
	InsecureDefaults bool
}

// Load reads configuration from the environment and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		Env:         env("APP_ENV", "development"),
		Host:        env("API_HOST", "127.0.0.1"),
		Port:        envInt("API_PORT", 8080),
		EngineURL:   env("ENGINE_URL", "http://127.0.0.1:8000"),
		EngineToken: env("ENGINE_INTERNAL_TOKEN", ""),

		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),

		JWTSecret:   os.Getenv("JWT_SECRET"),
		JWTIssuer:   env("JWT_ISSUER", "insightflow"),
		JWTAudience: env("JWT_AUDIENCE", "insightflow-web"),
		OTPSecret:   os.Getenv("OTP_SECRET"),

		OTPTTL:             time.Duration(envInt("OTP_TTL_SECONDS", 300)) * time.Second,
		OTPMaxAttempts:     envInt("OTP_MAX_ATTEMPTS", 5),
		OTPResendInterval:  time.Duration(envInt("OTP_RESEND_SECONDS", 60)) * time.Second,
		OTPMaxSendsPerHour: envInt("OTP_MAX_SENDS_PER_HOUR", 5),

		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:  env("GOOGLE_REDIRECT_URL", "http://localhost:8080/api/v1/auth/google/callback"),
		WebBaseURL:         env("WEB_BASE_URL", "http://localhost:5173"),

		SMTPHost:     env("SMTP_HOST", "localhost"),
		SMTPPort:     envInt("SMTP_PORT", 1025),
		SMTPFrom:     env("SMTP_FROM", "no-reply@insightflow.local"),
		SMTPUsername: os.Getenv("SMTP_USERNAME"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),

		AllowedOrigins: splitCSV(env("ALLOWED_ORIGINS", devAllowedOrigin)),
	}

	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("API_PORT out of range: %d", cfg.Port)
	}
	if _, err := url.ParseRequestURI(cfg.EngineURL); err != nil {
		return nil, fmt.Errorf("ENGINE_URL invalid: %w", err)
	}
	if cfg.OTPTTL <= 0 || cfg.OTPMaxAttempts <= 0 || cfg.OTPResendInterval <= 0 || cfg.OTPMaxSendsPerHour <= 0 {
		return nil, fmt.Errorf("OTP limits must all be positive")
	}

	if cfg.Env == "production" {
		if len(cfg.JWTSecret) < 32 {
			return nil, fmt.Errorf("JWT_SECRET must be at least 32 bytes in production")
		}
		if len(cfg.OTPSecret) < 16 {
			return nil, fmt.Errorf("OTP_SECRET must be at least 16 bytes in production")
		}
		if cfg.DatabaseURL == "" {
			return nil, fmt.Errorf("DATABASE_URL is required in production")
		}
		if cfg.RedisURL == "" {
			return nil, fmt.Errorf("REDIS_URL is required in production")
		}
		if len(cfg.AllowedOrigins) == 0 {
			return nil, fmt.Errorf("ALLOWED_ORIGINS is required in production")
		}
		return cfg, nil
	}

	// Development: fall back to fixed local defaults so the stack boots without
	// a .env. These values are intentionally marked insecure.
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = devJWTSecret
		cfg.InsecureDefaults = true
	}
	if cfg.OTPSecret == "" {
		cfg.OTPSecret = devOTPSecret
		cfg.InsecureDefaults = true
	}
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = devDatabaseURL
		cfg.InsecureDefaults = true
	}
	if cfg.RedisURL == "" {
		cfg.RedisURL = devRedisURL
		cfg.InsecureDefaults = true
	}
	return cfg, nil
}

// GoogleEnabled reports whether Google OAuth credentials are configured.
func (c *Config) GoogleEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

// CookieSecure reports whether refresh cookies require HTTPS.
func (c *Config) CookieSecure() bool { return c.Env == "production" }

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

func splitCSV(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

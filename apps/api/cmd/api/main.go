package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"insightflow/api/internal/config"
	"insightflow/api/internal/modules/auth"
	"insightflow/api/internal/modules/users"
	"insightflow/api/internal/modules/workspaces"
	"insightflow/api/internal/platform/cache"
	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/mailer"
	"insightflow/api/internal/platform/token"
	"insightflow/api/internal/router"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}
	if cfg.InsecureDefaults {
		slog.Warn("development fallback configuration is active; do not use in production")
	}

	ctx := context.Background()
	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	q := dbgen.New(pool)
	txm := database.NewTxManager(pool)

	// Redis holds only ephemeral state. When it is unreachable the API still
	// boots, and OTP/OAuth endpoints fail closed with AUTH_OTP_UNAVAILABLE.
	cacheClient, err := cache.NewFromURL(ctx, cfg.RedisURL)
	if err != nil {
		slog.Warn("redis unavailable; OTP and OAuth login disabled", "error", err)
		cacheClient = nil
	} else {
		defer func() { _ = cacheClient.Close() }()
	}

	issuer, err := token.NewIssuer(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	if err != nil {
		slog.Error("jwt issuer init failed", "error", err)
		os.Exit(1)
	}

	usersRepo := users.NewRepository(q)
	usersSvc := users.NewService(usersRepo)

	otpStore := auth.NewOTPStore(cacheClient, auth.OTPConfig{
		TTL:             cfg.OTPTTL,
		ResendInterval:  cfg.OTPResendInterval,
		MaxAttempts:     cfg.OTPMaxAttempts,
		MaxSendsPerHour: cfg.OTPMaxSendsPerHour,
		Secret:          cfg.OTPSecret,
		Env:             cfg.Env,
	})

	smtpMailer := mailer.NewSMTP(mailer.SMTPConfig{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		From:     cfg.SMTPFrom,
		Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword,
		UseTLS:   cfg.Env == "production",
	})

	authSvc := auth.NewService(auth.Deps{
		Cfg:     cfg,
		Users:   usersRepo,
		Queries: q,
		Tx:      txm,
		Cache:   cacheClient,
		OTP:     otpStore,
		Mailer:  smtpMailer,
		Issuer:  issuer,
		Hasher:  auth.NewHasher(),
	})

	var google *auth.GoogleOAuth
	if cfg.GoogleEnabled() {
		google, err = auth.NewGoogleOAuth(ctx, cfg)
		if err != nil {
			slog.Warn("google oidc discovery failed; google login disabled", "error", err)
			google = nil
		}
	}

	wsSvc := workspaces.NewService(workspaces.NewRepository(q), txm)

	handler := router.New(cfg, router.Deps{
		Auth:       auth.NewHandler(cfg, authSvc, google),
		Workspaces: workspaces.NewHandler(wsSvc),
		Issuer:     issuer,
		Users:      usersSvc,
		Members:    wsSvc,
		Origins:    cfg.AllowedOrigins,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("api server listening", "addr", srv.Addr, "env", cfg.Env, "google_login", google != nil, "redis", cacheClient != nil)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-stopCtx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}
}

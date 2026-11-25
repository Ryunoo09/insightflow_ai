package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"insightflow/api/internal/config"
	"insightflow/api/internal/middleware"
	"insightflow/api/internal/platform/httpx"
)

// refreshCookieName is the HttpOnly refresh-token cookie.
const refreshCookieName = "if_refresh"

// refreshCookiePath scopes the cookie to auth routes only.
const refreshCookiePath = "/api/v1/auth"

// loginRateLimit caps login attempts per IP+email (SEC 3.1).
const (
	loginRateLimit  = 5
	loginRateWindow = time.Minute
)

// Handler exposes the authentication HTTP endpoints.
type Handler struct {
	cfg    *config.Config
	svc    *Service
	google *GoogleOAuth
}

// NewHandler builds the auth HTTP handler. google may be nil when Google login
// is not configured.
func NewHandler(cfg *config.Config, svc *Service, google *GoogleOAuth) *Handler {
	return &Handler{cfg: cfg, svc: svc, google: google}
}

type registerRequest struct {
	Email    string `json:"email" binding:"required,email,max=320"`
	Password string `json:"password" binding:"required,min=10,max=128"`
	FullName string `json:"full_name" binding:"required,max=100"`
}

// Register handles POST /auth/register.
func (h *Handler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteBindError(c, err)
		return
	}
	ch, err := h.svc.Register(c.Request.Context(), req.Email, req.Password, req.FullName)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, otpRequiredBody(ch))
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email,max=320"`
	Password string `json:"password" binding:"required"`
}

// Login handles POST /auth/login.
func (h *Handler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteBindError(c, err)
		return
	}
	allowed, err := h.svc.AllowLoginAttempt(c.Request.Context(), c.ClientIP(), req.Email, loginRateLimit, loginRateWindow)
	if err != nil {
		h.writeError(c, err)
		return
	}
	if !allowed {
		c.Header("Retry-After", "60")
		h.writeError(c, ErrTooManyAttempts)
		return
	}
	ch, err := h.svc.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, otpRequiredBody(ch))
}

type verifyOTPRequest struct {
	ChallengeID string `json:"challenge_id" binding:"required,max=128"`
	Code        string `json:"code" binding:"required,len=6,numeric"`
}

// VerifyOTP handles POST /auth/otp/verify.
func (h *Handler) VerifyOTP(c *gin.Context) {
	var req verifyOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteBindError(c, err)
		return
	}
	session, err := h.svc.VerifyOTP(c.Request.Context(), req.ChallengeID, req.Code)
	if err != nil {
		h.writeError(c, err)
		return
	}
	h.setRefreshCookie(c, session.RefreshToken, session.RefreshExpires)
	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":             session.User.ID.String(),
			"email":          session.User.Email,
			"full_name":      session.User.FullName,
			"email_verified": session.User.EmailVerified,
		},
		"access_token": session.AccessToken,
		"expires_in":   session.ExpiresIn,
	})
}

type resendOTPRequest struct {
	ChallengeID string `json:"challenge_id" binding:"required,max=128"`
}

// ResendOTP handles POST /auth/otp/resend.
func (h *Handler) ResendOTP(c *gin.Context) {
	var req resendOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteBindError(c, err)
		return
	}
	ch, err := h.svc.ResendOTP(c.Request.Context(), req.ChallengeID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"expires_in":   ch.ExpiresIn,
		"resend_after": ch.ResendAfter,
		"email_hint":   ch.EmailHint,
	})
}

// Refresh handles POST /auth/refresh (rotating refresh token).
func (h *Handler) Refresh(c *gin.Context) {
	raw, _ := c.Cookie(refreshCookieName)
	session, err := h.svc.Refresh(c.Request.Context(), raw)
	if err != nil {
		h.clearRefreshCookie(c)
		h.writeError(c, err)
		return
	}
	h.setRefreshCookie(c, session.RefreshToken, session.RefreshExpires)
	c.JSON(http.StatusOK, gin.H{
		"access_token": session.AccessToken,
		"expires_in":   session.ExpiresIn,
	})
}

// Logout handles POST /auth/logout.
func (h *Handler) Logout(c *gin.Context) {
	allDevices := c.Query("all_devices") == "true"
	raw, _ := c.Cookie(refreshCookieName)
	if err := h.svc.Logout(c.Request.Context(), raw, allDevices); err != nil {
		h.writeError(c, err)
		return
	}
	h.clearRefreshCookie(c)
	c.Status(http.StatusNoContent)
}

// Me handles GET /users/me.
func (h *Handler) Me(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		h.writeError(c, middleware.ErrUnauthenticated)
		return
	}
	profile, err := h.svc.Me(c.Request.Context(), principal.ID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":             profile.ID.String(),
		"email":          profile.Email,
		"full_name":      profile.FullName,
		"email_verified": profile.EmailVerified,
		"auth_methods":   profile.AuthMethods,
	})
}

func (h *Handler) setRefreshCookie(c *gin.Context, raw string, expires time.Time) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(refreshCookieName, raw, int(time.Until(expires).Seconds()), refreshCookiePath, "", h.cfg.CookieSecure(), true)
}

func (h *Handler) clearRefreshCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(refreshCookieName, "", -1, refreshCookiePath, "", h.cfg.CookieSecure(), true)
}

func otpRequiredBody(ch OTPChallenge) gin.H {
	return gin.H{
		"status":       "otp_required",
		"challenge_id": ch.ChallengeID,
		"expires_in":   ch.ExpiresIn,
		"resend_after": ch.ResendAfter,
		"email_hint":   ch.EmailHint,
	}
}

// writeError maps a service error to the standard envelope with a stable code.
func (h *Handler) writeError(c *gin.Context, err error) {
	httpx.WriteError(c, mapAuthError(err))
}

func mapAuthError(err error) error {
	var field *FieldError
	switch {
	case errors.As(err, &field):
		return httpx.Err(http.StatusUnprocessableEntity, "VALIDATION_FAILED", "Data yang dikirim tidak valid.").
			WithDetails([]httpx.ValidationDetails{{Field: field.Field, Issue: field.Issue}})
	case errors.Is(err, ErrInvalidCredentials):
		return httpx.Err(http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS", "Email atau password salah.")
	case errors.Is(err, ErrEmailTaken):
		return httpx.Err(http.StatusConflict, "AUTH_EMAIL_TAKEN", "Email sudah terdaftar.")
	case errors.Is(err, ErrOTPUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "AUTH_OTP_UNAVAILABLE", "Layanan verifikasi sedang tidak tersedia. Coba lagi.")
	case errors.Is(err, ErrOTPExpired):
		return httpx.Err(http.StatusBadRequest, "AUTH_OTP_EXPIRED", "Kode verifikasi sudah kedaluwarsa. Silakan minta kode baru.")
	case errors.Is(err, ErrOTPAttemptsExceeded):
		return httpx.Err(http.StatusTooManyRequests, "AUTH_OTP_ATTEMPTS_EXCEEDED", "Terlalu banyak percobaan. Minta kode baru.")
	case errors.Is(err, ErrOTPResendTooSoon):
		return httpx.Err(http.StatusTooManyRequests, "AUTH_OTP_RESEND_TOO_SOON", "Tunggu sebelum meminta kode lagi.")
	case errors.Is(err, ErrTooManyAttempts):
		return httpx.Err(http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "Terlalu banyak percobaan. Coba lagi nanti.")
	case errors.Is(err, ErrRefreshInvalid):
		return httpx.Err(http.StatusUnauthorized, "AUTH_REFRESH_INVALID", "Sesi tidak valid. Silakan masuk kembali.")
	case errors.Is(err, ErrRefreshReused):
		return httpx.Err(http.StatusUnauthorized, "AUTH_REFRESH_REUSED", "Sesi dicabut karena indikasi penyalahgunaan. Silakan masuk kembali.")
	case errors.Is(err, ErrOAuthUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "AUTH_OAUTH_UNAVAILABLE", "Layanan login Google sedang tidak tersedia.")
	case errors.Is(err, ErrOAuthStateInvalid):
		return httpx.Err(http.StatusBadRequest, "AUTH_OAUTH_STATE_INVALID", "Permintaan login tidak valid.")
	}
	var invalid *InvalidCodeError
	if errors.As(err, &invalid) {
		return httpx.Err(http.StatusUnauthorized, "AUTH_OTP_INVALID", "Kode salah.").
			WithDetails([]map[string]int{{"attempts_left": invalid.AttemptsLeft}})
	}
	if errors.Is(err, middleware.ErrUnauthenticated) {
		return httpx.Err(http.StatusUnauthorized, "AUTH_REQUIRED", "Autentikasi diperlukan.")
	}
	return err
}

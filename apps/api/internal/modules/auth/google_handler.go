package auth

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"insightflow/api/internal/platform/httpx"
	"insightflow/api/internal/platform/token"
)

const oauthBindingCookie = "if_oauth"

// GoogleStart handles GET /auth/google/start.
func (h *Handler) GoogleStart(c *gin.Context) {
	if h.google == nil {
		httpx.WriteError(c, httpx.Err(http.StatusNotFound, "NOT_FOUND", "Login Google tidak tersedia."))
		return
	}
	state, err := token.RandomToken()
	if err != nil {
		h.writeError(c, ErrOAuthUnavailable)
		return
	}
	verifier, err := token.RandomToken()
	if err != nil {
		h.writeError(c, ErrOAuthUnavailable)
		return
	}
	nonce, err := token.RandomToken()
	if err != nil {
		h.writeError(c, ErrOAuthUnavailable)
		return
	}
	if err := saveOAuthState(c.Request.Context(), h.svc.cache, h.cfg.Env, state, oauthState{Verifier: verifier, Nonce: nonce}); err != nil {
		h.writeError(c, err)
		return
	}
	// Bind the state to this browser so a stolen state cannot be replayed.
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oauthBindingCookie, state, int(StateTTL.Seconds()), refreshCookiePath, "", h.cfg.CookieSecure(), true)
	c.Redirect(http.StatusFound, h.google.AuthCodeURL(state, verifier, nonce))
}

// GoogleCallback handles GET /auth/google/callback.
func (h *Handler) GoogleCallback(c *gin.Context) {
	if h.google == nil {
		httpx.WriteError(c, httpx.Err(http.StatusNotFound, "NOT_FOUND", "Login Google tidak tersedia."))
		return
	}
	state := c.Query("state")
	code := c.Query("code")
	bound, cookieErr := c.Cookie(oauthBindingCookie)
	if state == "" || code == "" || cookieErr != nil || bound != state {
		h.redirectLoginError(c, "AUTH_OAUTH_STATE_INVALID")
		return
	}
	st, err := takeOAuthState(c.Request.Context(), h.svc.cache, h.cfg.Env, state)
	if err != nil {
		h.redirectLoginError(c, "AUTH_OAUTH_FAILED")
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oauthBindingCookie, "", -1, refreshCookiePath, "", h.cfg.CookieSecure(), true)

	identity, err := h.google.Exchange(c.Request.Context(), code, st.Verifier, st.Nonce)
	if err != nil {
		if errors.Is(err, ErrOAuthEmailUnverified) {
			h.redirectLoginError(c, "AUTH_OAUTH_EMAIL_UNVERIFIED")
			return
		}
		h.redirectLoginError(c, "AUTH_OAUTH_FAILED")
		return
	}
	session, err := h.svc.CompleteGoogleLogin(c.Request.Context(), identity)
	if err != nil {
		h.redirectLoginError(c, "AUTH_OAUTH_FAILED")
		return
	}
	h.setRefreshCookie(c, session.RefreshToken, session.RefreshExpires)
	c.Redirect(http.StatusFound, h.cfg.WebBaseURL+"/app")
}

func (h *Handler) redirectLoginError(c *gin.Context, code string) {
	c.Redirect(http.StatusFound, h.cfg.WebBaseURL+"/login?error="+code)
}

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"

	"insightflow/api/internal/config"
	"insightflow/api/internal/platform/cache"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

// googleIssuer is the OIDC issuer for Google accounts.
const googleIssuer = "https://accounts.google.com"

// StateTTL bounds an in-flight OAuth round trip.
const StateTTL = 10 * time.Minute

// ErrOAuthUnavailable is returned when the OIDC provider cannot be reached.
var ErrOAuthUnavailable = errors.New("auth: oauth provider unavailable")

// ErrOAuthStateInvalid is returned when state/PKCE data is missing or expired.
var ErrOAuthStateInvalid = errors.New("auth: oauth state invalid")

// GoogleOAuth wraps the OIDC provider, OAuth2 config, and PKCE helpers.
type GoogleOAuth struct {
	conf     *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewGoogleOAuth discovers the issuer and builds the client. Callers should
// skip it when Google credentials are absent.
func NewGoogleOAuth(ctx context.Context, cfg *config.Config) (*GoogleOAuth, error) {
	return newGoogleOAuth(ctx, cfg, googleIssuer)
}

// newGoogleOAuth allows tests to point discovery at a local OIDC provider.
func newGoogleOAuth(ctx context.Context, cfg *config.Config, issuerURL string) (*GoogleOAuth, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc discovery: %w", err)
	}
	return &GoogleOAuth{
		conf: &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  cfg.GoogleRedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.GoogleClientID}),
	}, nil
}

// AuthCodeURL builds the consent-screen URL with PKCE S256 and a nonce.
func (g *GoogleOAuth) AuthCodeURL(state, codeVerifier, nonce string) string {
	return g.conf.AuthCodeURL(state,
		oauth2.S256ChallengeOption(codeVerifier),
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.AccessTypeOnline,
	)
}

// Exchange redeems the authorization code and verifies the returned ID token.
// Signature, issuer, audience, and expiry are checked by the verifier; the
// nonce and email_verified are checked here.
func (g *GoogleOAuth) Exchange(ctx context.Context, code, codeVerifier, expectedNonce string) (GoogleIdentity, error) {
	tok, err := g.conf.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return GoogleIdentity{}, fmt.Errorf("auth: code exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return GoogleIdentity{}, fmt.Errorf("auth: missing id_token")
	}
	idToken, err := g.verifier.Verify(ctx, rawID)
	if err != nil {
		return GoogleIdentity{}, fmt.Errorf("auth: id_token verify: %w", err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Nonce         string `json:"nonce"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return GoogleIdentity{}, fmt.Errorf("auth: id_token claims: %w", err)
	}
	if claims.Nonce == "" || claims.Nonce != expectedNonce {
		return GoogleIdentity{}, ErrOAuthStateInvalid
	}
	if !claims.EmailVerified {
		return GoogleIdentity{}, ErrOAuthEmailUnverified
	}
	return GoogleIdentity{
		Subject:       idToken.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		FullName:      claims.Name,
	}, nil
}

type oauthState struct {
	Verifier string `json:"verifier"`
	Nonce    string `json:"nonce"`
}

func oauthStateKey(env, state string) string {
	return cache.Key(env, cache.PrefixOAuth, "state", state)
}

// saveOAuthState stores the PKCE verifier and nonce, keyed by state.
func saveOAuthState(ctx context.Context, c *cache.Client, env, state string, st oauthState) error {
	if c == nil {
		return ErrOAuthStateInvalid
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return ErrOAuthStateInvalid
	}
	if err := c.SetWithTTL(ctx, oauthStateKey(env, state), raw, StateTTL); err != nil {
		return ErrOAuthUnavailable
	}
	return nil
}

// takeOAuthState consumes state exactly once.
func takeOAuthState(ctx context.Context, c *cache.Client, env, state string) (oauthState, error) {
	if c == nil {
		return oauthState{}, ErrOAuthUnavailable
	}
	raw, ok, err := c.Get(ctx, oauthStateKey(env, state))
	if err != nil {
		return oauthState{}, ErrOAuthUnavailable
	}
	if !ok {
		return oauthState{}, ErrOAuthStateInvalid
	}
	_ = c.Delete(ctx, oauthStateKey(env, state))
	var st oauthState
	if err := json.Unmarshal(raw, &st); err != nil {
		return oauthState{}, ErrOAuthStateInvalid
	}
	return st, nil
}

// CompleteGoogleLogin links or creates the user and issues a session. The
// anti-pre-hijacking rule (FR-GOOG-03) deletes an unverified local account with
// the same email and creates a fresh verified account instead of linking.
func (s *Service) CompleteGoogleLogin(ctx context.Context, identity GoogleIdentity) (Session, error) {
	if !identity.EmailVerified {
		return Session{}, ErrOAuthEmailUnverified
	}
	email, err := normalizeEmail(identity.Email)
	if err != nil {
		return Session{}, err
	}
	fullName := identity.FullName
	if fullName == "" {
		fullName = email
	}
	if len(fullName) > 100 {
		fullName = fullName[:100]
	}

	var userID uuid.UUID
	err = s.tx.WithinTx(ctx, func(tx pgx.Tx) error {
		q := dbgen.New(tx)

		ident, err := q.GetUserIdentity(ctx, dbgen.GetUserIdentityParams{
			Provider:        "google",
			ProviderSubject: identity.Subject,
		})
		if err == nil {
			userID = uuid.UUID(ident.UserID.Bytes)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		u, err := q.GetUserByEmail(ctx, email)
		switch {
		case err == nil:
			if u.Status == "active" {
				userID = uuid.UUID(u.ID.Bytes)
				if err := linkGoogle(ctx, q, userID, identity.Subject); err != nil {
					return err
				}
				// Linking is auditable (FR-GOOG-03).
				return q.InsertUserAuditLog(ctx, dbgen.InsertUserAuditLogParams{
					UserID:       pgconv.UUID(userID),
					Action:       "identity.google.link",
					ResourceType: pgtype.Text{String: "user", Valid: true},
					Details:      []byte(`{}`),
				})
			}
			// Unverified local account: delete then recreate to avoid pre-hijack.
			if err := q.DeleteUser(ctx, u.ID); err != nil {
				return err
			}
		case errors.Is(err, pgx.ErrNoRows):
		default:
			return err
		}

		created, err := q.CreateUser(ctx, dbgen.CreateUserParams{
			Email:        email,
			PasswordHash: pgtype.Text{}, // Google-only account has no password.
			FullName:     fullName,
			Status:       "active",
		})
		if err != nil {
			return err
		}
		userID = uuid.UUID(created.ID.Bytes)
		if err := linkGoogle(ctx, q, userID, identity.Subject); err != nil {
			return err
		}
		return q.InsertUserAuditLog(ctx, dbgen.InsertUserAuditLogParams{
			UserID:       pgconv.UUID(userID),
			Action:       "identity.google.create",
			ResourceType: pgtype.Text{String: "user", Valid: true},
			Details:      []byte(`{}`),
		})
	})
	if err != nil {
		return Session{}, err
	}

	if err := s.ensureDefaultWorkspace(ctx, userID); err != nil {
		return Session{}, err
	}
	return s.issueSession(ctx, s.q, userID, "")
}

func linkGoogle(ctx context.Context, q *dbgen.Queries, userID uuid.UUID, subject string) error {
	_, err := q.CreateUserIdentity(ctx, dbgen.CreateUserIdentityParams{
		UserID:          pgconv.UUID(userID),
		Provider:        "google",
		ProviderSubject: subject,
	})
	if err != nil && !pgconv.IsUniqueViolation(err) {
		return err
	}
	return nil
}

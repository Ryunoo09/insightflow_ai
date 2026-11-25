package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/config"
	"insightflow/api/internal/modules/users"
	"insightflow/api/internal/platform/cache"
	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/mailer"
	"insightflow/api/internal/platform/pgconv"
	"insightflow/api/internal/platform/token"
)

// Session is a freshly issued set of credentials.
type Session struct {
	User           Profile
	AccessToken    string
	ExpiresIn      int
	RefreshToken   string
	RefreshExpires time.Time
}

// Profile is the public user projection.
type Profile struct {
	ID            uuid.UUID
	Email         string
	FullName      string
	EmailVerified bool
}

// OTPChallenge describes a pending OTP step.
type OTPChallenge struct {
	ChallengeID string
	ExpiresIn   int
	ResendAfter int
	EmailHint   string
}

// GoogleIdentity is the verified OIDC payload.
type GoogleIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
	FullName      string
}

// Deps are the service collaborators.
type Deps struct {
	Cfg     *config.Config
	Users   *users.Repository
	Queries *dbgen.Queries
	Tx      *database.TxManager
	Cache   *cache.Client
	OTP     *OTPStore
	Mailer  mailer.Mailer
	Issuer  *token.Issuer
	Hasher  *Hasher
}

// Service orchestrates the authentication use cases.
type Service struct {
	cfg      *config.Config
	users    *users.Repository
	q        *dbgen.Queries
	tx       *database.TxManager
	cache    *cache.Client
	otp      *OTPStore
	mailer   mailer.Mailer
	issuer   *token.Issuer
	hasher   *Hasher
	syncMail bool
}

// Option customizes the service.
type Option func(*Service)

// WithSyncMail delivers OTP email on the calling goroutine. It exists so tests
// can assert on the mailer without racing a background goroutine.
func WithSyncMail() Option {
	return func(s *Service) { s.syncMail = true }
}

// NewService builds the auth service.
func NewService(deps Deps, opts ...Option) *Service {
	s := &Service{
		cfg:    deps.Cfg,
		users:  deps.Users,
		q:      deps.Queries,
		tx:     deps.Tx,
		cache:  deps.Cache,
		otp:    deps.OTP,
		mailer: deps.Mailer,
		issuer: deps.Issuer,
		hasher: deps.Hasher,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Register creates (or replaces an unverified) account and sends a
// registration OTP.
func (s *Service) Register(ctx context.Context, email, password, fullName string) (OTPChallenge, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return OTPChallenge{}, err
	}
	fullName = strings.TrimSpace(fullName)
	if fullName == "" || len(fullName) > 100 {
		return OTPChallenge{}, fieldErr("full_name", "1-100 karakter")
	}
	if err := ValidatePassword(password); err != nil {
		return OTPChallenge{}, fieldErr("password", fmt.Sprintf("%d-%d karakter dan tidak umum", MinPasswordLen, MaxPasswordLen))
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return OTPChallenge{}, err
	}

	var userID uuid.UUID
	err = s.tx.WithinTx(ctx, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		existing, err := q.GetUserByEmail(ctx, email)
		switch {
		case err == nil:
			// Unverified accounts are replaced so the same email can register
			// again (FR-OTP-01). Verified accounts are a conflict.
			if existing.Status != "pending_verification" {
				return ErrEmailTaken
			}
			if err := q.DeleteUser(ctx, existing.ID); err != nil {
				return err
			}
		case errors.Is(err, pgx.ErrNoRows):
		default:
			return err
		}
		created, err := q.CreateUser(ctx, dbgen.CreateUserParams{
			Email:        email,
			PasswordHash: pgtype.Text{String: hash, Valid: true},
			FullName:     fullName,
			Status:       "pending_verification",
		})
		if err != nil {
			if pgconv.IsUniqueViolation(err) {
				return ErrEmailTaken
			}
			return err
		}
		userID = uuid.UUID(created.ID.Bytes)
		return nil
	})
	if err != nil {
		return OTPChallenge{}, err
	}
	return s.issueOTP(ctx, userID.String(), PurposeRegister, email)
}

// Login verifies the password and sends a login OTP. Failures are always
// reported identically and never send mail (anti-enumeration).
func (s *Service) Login(ctx context.Context, email, password string) (OTPChallenge, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			s.hasher.EqualizeTiming(password)
			return OTPChallenge{}, ErrInvalidCredentials
		}
		return OTPChallenge{}, err
	}
	if !u.PasswordHash.Valid {
		s.hasher.EqualizeTiming(password)
		return OTPChallenge{}, ErrInvalidCredentials
	}
	if !s.hasher.Verify(u.PasswordHash.String, password) {
		return OTPChallenge{}, ErrInvalidCredentials
	}
	if u.Status == "disabled" {
		return OTPChallenge{}, ErrInvalidCredentials
	}
	purpose := PurposeLogin
	if u.Status == "pending_verification" {
		purpose = PurposeRegister
	}
	return s.issueOTP(ctx, pgconv.UUIDString(u.ID), purpose, u.Email)
}

// ResendOTP issues a new code for an existing challenge.
func (s *Service) ResendOTP(ctx context.Context, challengeID string) (OTPChallenge, error) {
	userIDStr, _, code, err := s.otp.Resend(ctx, challengeID)
	if err != nil {
		return OTPChallenge{}, err
	}
	id, err := uuid.Parse(userIDStr)
	if err != nil {
		return OTPChallenge{}, ErrOTPExpired
	}
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return OTPChallenge{}, ErrOTPExpired
	}
	s.deliverOTP(u.Email, code)
	return s.challengeInfo(challengeID, u.Email), nil
}

// VerifyOTP validates the code, activates pending accounts, and issues a
// session.
func (s *Service) VerifyOTP(ctx context.Context, challengeID, code string) (Session, error) {
	userIDStr, purpose, err := s.otp.Verify(ctx, challengeID, code)
	if err != nil {
		return Session{}, err
	}
	id, err := uuid.Parse(userIDStr)
	if err != nil {
		return Session{}, ErrOTPExpired
	}

	err = s.tx.WithinTx(ctx, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		u, err := q.GetUserByID(ctx, pgconv.UUID(id))
		if err != nil {
			return ErrOTPExpired
		}
		if purpose == PurposeRegister && u.Status == "pending_verification" {
			if _, err := q.ActivateUser(ctx, pgconv.UUID(id)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Session{}, err
	}

	if err := s.ensureDefaultWorkspace(ctx, id); err != nil {
		return Session{}, err
	}
	return s.issueSession(ctx, s.q, id, "")
}

// Refresh rotates a refresh token, detecting reuse and revoking the family.
func (s *Service) Refresh(ctx context.Context, raw string) (Session, error) {
	if raw == "" {
		return Session{}, ErrRefreshInvalid
	}
	row, err := s.q.GetRefreshTokenByHash(ctx, token.HashRefreshToken(raw))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrRefreshInvalid
		}
		return Session{}, err
	}
	if row.RevokedAt.Valid {
		// Token already used or revoked: assume theft and burn the family.
		if err := s.q.RevokeRefreshTokenFamily(ctx, row.FamilyID); err != nil {
			return Session{}, err
		}
		return Session{}, ErrRefreshReused
	}
	if !row.ExpiresAt.Valid || row.ExpiresAt.Time.Before(time.Now()) {
		return Session{}, ErrRefreshInvalid
	}

	userID := uuid.UUID(row.UserID.Bytes)
	if _, err := s.users.GetByID(ctx, userID); err != nil {
		return Session{}, ErrRefreshInvalid
	}

	var session Session
	err = s.tx.WithinTx(ctx, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := q.RevokeRefreshToken(ctx, row.ID); err != nil {
			return err
		}
		sess, err := s.issueSession(ctx, q, userID, pgconv.UUIDString(row.FamilyID))
		if err != nil {
			return err
		}
		session = sess
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

// Logout revokes the presented refresh token; allDevices additionally raises
// tokens_valid_after so outstanding access tokens die immediately.
func (s *Service) Logout(ctx context.Context, raw string, allDevices bool) error {
	if raw == "" {
		return nil
	}
	row, err := s.q.GetRefreshTokenByHash(ctx, token.HashRefreshToken(raw))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if err := s.q.RevokeRefreshToken(ctx, row.ID); err != nil {
		return err
	}
	if allDevices {
		userID := uuid.UUID(row.UserID.Bytes)
		if err := s.q.RevokeUserRefreshTokens(ctx, pgconv.UUID(userID)); err != nil {
			return err
		}
		if err := s.users.RevokeTokens(ctx, userID); err != nil {
			return err
		}
	}
	return nil
}

// AllowLoginAttempt enforces the per-IP+email login cap. It fails open when
// Redis is unavailable because OTP issuance fails closed anyway.
func (s *Service) AllowLoginAttempt(ctx context.Context, ip, email string, limit int, window time.Duration) (bool, error) {
	if s.cache == nil {
		return true, nil
	}
	key := cache.Key(s.cfg.Env, cache.PrefixRate, "login", ip, strings.ToLower(strings.TrimSpace(email)))
	n, err := s.cache.IncrementWithTTL(ctx, key, window)
	if err != nil {
		return true, nil
	}
	return int(n) <= limit, nil
}

func (s *Service) issueOTP(ctx context.Context, userID, purpose, email string) (OTPChallenge, error) {
	challengeID, code, err := s.otp.Create(ctx, userID, purpose)
	if err != nil {
		return OTPChallenge{}, err
	}
	s.deliverOTP(email, code)
	return s.challengeInfo(challengeID, email), nil
}

func (s *Service) challengeInfo(challengeID, email string) OTPChallenge {
	return OTPChallenge{
		ChallengeID: challengeID,
		ExpiresIn:   int(s.cfg.OTPTTL.Seconds()),
		ResendAfter: int(s.cfg.OTPResendInterval.Seconds()),
		EmailHint:   maskEmail(email),
	}
}

func (s *Service) deliverOTP(to, code string) {
	send := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.mailer.SendOTP(ctx, to, code); err != nil {
			// Never log the code or the recipient address.
			slog.Error("otp email delivery failed", "error", err)
		}
	}
	if s.syncMail {
		send()
		return
	}
	go send()
}

// issueSession inserts a new refresh token (within familyID when rotating) and
// signs an access token.
func (s *Service) issueSession(ctx context.Context, q *dbgen.Queries, userID uuid.UUID, familyID string) (Session, error) {
	if familyID == "" {
		familyID = token.NewFamilyID()
	}
	fam, err := pgconv.UUIDFromString(familyID)
	if err != nil {
		return Session{}, err
	}
	rt, err := token.NewRefreshToken()
	if err != nil {
		return Session{}, err
	}
	expires := time.Now().Add(token.RefreshTokenTTL)
	if _, err := q.InsertRefreshToken(ctx, dbgen.InsertRefreshTokenParams{
		UserID:    pgconv.UUID(userID),
		TokenHash: rt.Hash,
		FamilyID:  fam,
		ExpiresAt: pgconv.Timestamptz(expires),
	}); err != nil {
		return Session{}, err
	}
	access, err := s.issuer.Issue(userID.String())
	if err != nil {
		return Session{}, err
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return Session{}, err
	}
	return Session{
		User:           Profile{ID: userID, Email: u.Email, FullName: u.FullName, EmailVerified: u.Status == "active"},
		AccessToken:    access,
		ExpiresIn:      int(token.AccessTokenTTL.Seconds()),
		RefreshToken:   rt.Raw,
		RefreshExpires: expires,
	}, nil
}

// ensureDefaultWorkspace creates one workspace for a user who has none
// (FR-AUTH-07). It is idempotent and safe to call on every verification.
func (s *Service) ensureDefaultWorkspace(ctx context.Context, userID uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		rows, err := q.ListUserWorkspaces(ctx, pgconv.UUID(userID))
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			return nil
		}
		ws, err := q.CreateWorkspace(ctx, dbgen.CreateWorkspaceParams{
			Name:      "Ruang Kerja Saya",
			Slug:      "ws-" + uuid.NewString()[:8],
			CreatedBy: pgconv.UUID(userID),
		})
		if err != nil {
			return err
		}
		_, err = q.AddWorkspaceMember(ctx, dbgen.AddWorkspaceMemberParams{
			WorkspaceID: ws.ID,
			UserID:      pgconv.UUID(userID),
			Role:        "owner",
		})
		return err
	})
}

// UserProfile is the /users/me projection.
type UserProfile struct {
	ID            uuid.UUID
	Email         string
	FullName      string
	EmailVerified bool
	AuthMethods   []string
}

// Me returns the caller's profile and linked auth methods.
func (s *Service) Me(ctx context.Context, id uuid.UUID) (UserProfile, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return UserProfile{}, err
	}
	methods := make([]string, 0, 2)
	if u.PasswordHash.Valid {
		methods = append(methods, "password")
	}
	providers, err := s.q.ListUserIdentityProviders(ctx, pgconv.UUID(id))
	if err == nil {
		methods = append(methods, providers...)
	}
	if methods == nil {
		methods = []string{}
	}
	return UserProfile{
		ID:            id,
		Email:         u.Email,
		FullName:      u.FullName,
		EmailVerified: u.Status == "active",
		AuthMethods:   methods,
	}, nil
}

func normalizeEmail(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 320 {
		return "", fieldErr("email", "format email tidak valid")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Address != raw {
		return "", fieldErr("email", "format email tidak valid")
	}
	return strings.ToLower(addr.Address), nil
}

// maskEmail renders r***@gmail.com so the OTP screen can hint the recipient
// without exposing the full address.
func maskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	local, domain := email[:at], email[at+1:]
	head := "***"
	if local != "" {
		head = string(local[0]) + "***"
	}
	return head + "@" + domain
}

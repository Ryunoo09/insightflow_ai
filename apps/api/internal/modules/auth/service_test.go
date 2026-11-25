package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"insightflow/api/internal/config"
	"insightflow/api/internal/modules/users"
	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/mailer"
	"insightflow/api/internal/platform/token"
)

func newTestService(t *testing.T) (*Service, *mailer.Fake, *pgxpool.Pool) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("database.NewPool: %v", err)
	}
	t.Cleanup(pool.Close)

	q := dbgen.New(pool)
	cc := testRedis(t)
	fm := &mailer.Fake{}
	iss, err := token.NewIssuer(strings.Repeat("t", 40), "insightflow", "insightflow-web")
	if err != nil {
		t.Fatalf("token.NewIssuer: %v", err)
	}
	cfg := &config.Config{
		Env:                "test",
		OTPTTL:             5 * time.Minute,
		OTPResendInterval:  time.Minute,
		OTPMaxAttempts:     5,
		OTPMaxSendsPerHour: 5,
	}
	svc := NewService(Deps{
		Cfg:     cfg,
		Users:   users.NewRepository(q),
		Queries: q,
		Tx:      database.NewTxManager(pool),
		Cache:   cc,
		OTP:     newTestOTPStore(cc, cfg.OTPMaxAttempts),
		Mailer:  fm,
		Issuer:  iss,
		Hasher:  NewHasher(),
	}, WithSyncMail())
	return svc, fm, pool
}

func uniqueEmail(prefix string) string {
	return fmt.Sprintf("%s-%s@example.com", prefix, uuid.NewString())
}

func registerAndVerify(t *testing.T, svc *Service, fm *mailer.Fake, email string) Session {
	t.Helper()
	ctx := context.Background()
	ch, err := svc.Register(ctx, email, "correct horse battery", "Test User")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	msg, ok := fm.Last()
	if !ok || msg.To != email {
		t.Fatalf("OTP not delivered to %s: %+v", email, fm.Messages())
	}
	session, err := svc.VerifyOTP(ctx, ch.ChallengeID, msg.Code)
	if err != nil {
		t.Fatalf("VerifyOTP: %v", err)
	}
	return session
}

func TestRegisterVerifyCreatesDefaultWorkspace(t *testing.T) {
	svc, fm, pool := newTestService(t)
	email := uniqueEmail("reg")
	session := registerAndVerify(t, svc, fm, email)

	if session.User.Email != email || !session.User.EmailVerified {
		t.Fatalf("unexpected profile: %+v", session.User)
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		t.Fatal("session missing tokens")
	}
	if _, err := svc.issuer.Verify(session.AccessToken); err != nil {
		t.Fatalf("issued access token does not verify: %v", err)
	}

	var memberships int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM workspace_members WHERE user_id = $1", session.User.ID).Scan(&memberships); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if memberships != 1 {
		t.Fatalf("default workspace memberships = %d, want 1", memberships)
	}
}

func TestRegisterRejectsWeakPassword(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, err := svc.Register(context.Background(), uniqueEmail("weak"), "short", "Name")
	var field *FieldError
	if !errors.As(err, &field) || field.Field != "password" {
		t.Fatalf("err = %v, want password FieldError", err)
	}
}

func TestRegisterReplacesUnverifiedAccount(t *testing.T) {
	svc, fm, _ := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail("replace")

	if _, err := svc.Register(ctx, email, "correct horse battery", "First"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	ch, err := svc.Register(ctx, email, "correct horse battery", "Second")
	if err != nil {
		t.Fatalf("second Register should replace the unverified account: %v", err)
	}
	msg, _ := fm.Last()
	session, err := svc.VerifyOTP(ctx, ch.ChallengeID, msg.Code)
	if err != nil {
		t.Fatalf("VerifyOTP: %v", err)
	}
	if session.User.FullName != "Second" {
		t.Fatalf("full name = %q, want the replacement account", session.User.FullName)
	}
}

func TestRegisterRejectsVerifiedEmail(t *testing.T) {
	svc, fm, _ := newTestService(t)
	email := uniqueEmail("taken")
	registerAndVerify(t, svc, fm, email)

	if _, err := svc.Register(context.Background(), email, "correct horse battery", "Dup"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("err = %v, want ErrEmailTaken", err)
	}
}

func TestLoginIsAntiEnumeration(t *testing.T) {
	svc, fm, _ := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail("login")
	registerAndVerify(t, svc, fm, email)
	before := len(fm.Messages())

	if _, err := svc.Login(ctx, uniqueEmail("nobody"), "whatever-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown email err = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Login(ctx, email, "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v, want ErrInvalidCredentials", err)
	}
	if len(fm.Messages()) != before {
		t.Fatal("failed login sent an OTP email")
	}

	ch, err := svc.Login(ctx, email, "correct horse battery")
	if err != nil {
		t.Fatalf("valid login: %v", err)
	}
	if ch.EmailHint != "***" && !strings.Contains(ch.EmailHint, "***@example.com") {
		t.Fatalf("email hint = %q", ch.EmailHint)
	}
	if len(fm.Messages()) != before+1 {
		t.Fatal("valid login did not send an OTP email")
	}
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	svc, fm, _ := newTestService(t)
	ctx := context.Background()
	session := registerAndVerify(t, svc, fm, uniqueEmail("refresh"))

	rotated, err := svc.Refresh(ctx, session.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if rotated.RefreshToken == session.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}

	// Replaying the spent token must revoke the whole family.
	if _, err := svc.Refresh(ctx, session.RefreshToken); !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("replay err = %v, want ErrRefreshReused", err)
	}
	if _, err := svc.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("sibling token err = %v, want family revocation (ErrRefreshReused)", err)
	}
}

func TestRefreshRejectsUnknownToken(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.Refresh(context.Background(), "not-a-token"); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("err = %v, want ErrRefreshInvalid", err)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	svc, fm, _ := newTestService(t)
	ctx := context.Background()
	session := registerAndVerify(t, svc, fm, uniqueEmail("logout"))

	if err := svc.Logout(ctx, session.RefreshToken, false); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := svc.Refresh(ctx, session.RefreshToken); err == nil {
		t.Fatal("refresh succeeded after logout")
	}
}

func TestVerifyOTPUsesFreshCodeOnly(t *testing.T) {
	svc, fm, _ := newTestService(t)
	ctx := context.Background()
	ch, err := svc.Register(ctx, uniqueEmail("otp"), "correct horse battery", "Name")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	msg, _ := fm.Last()
	wrong := "000000"
	if msg.Code == wrong {
		wrong = "111111"
	}
	if _, err := svc.VerifyOTP(ctx, ch.ChallengeID, wrong); err == nil {
		t.Fatal("wrong code accepted")
	}
	if _, err := svc.VerifyOTP(ctx, ch.ChallengeID, msg.Code); err != nil {
		t.Fatalf("correct code rejected after a single wrong attempt: %v", err)
	}
}

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
	"github.com/shalarasgar/carpool-backend/internal/ratelimit"
	"github.com/shalarasgar/carpool-backend/internal/sms"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// AuthConfig tunes the OTP and token flow.
type AuthConfig struct {
	AppName         string
	DevMode         bool
	OTPTTL          time.Duration
	PhoneCooldown   time.Duration
	PhoneDailyLimit int
	IPLimit         int
	IPWindow        time.Duration
	DeviceLimit     int
	DeviceWindow    time.Duration
	RefreshTokenTTL time.Duration
}

// Auth implements phone + OTP login, token refresh and logout.
type Auth struct {
	pool    *pgxpool.Pool
	q       *store.Queries
	phones  *auth.PhoneNormalizer
	otp     *auth.OTP
	jwt     *auth.JWT
	sms     sms.Provider
	limiter ratelimit.Limiter
	cfg     AuthConfig
	logger  *slog.Logger
	now     func() time.Time
}

// NewAuth wires the auth service. logger may be nil.
func NewAuth(
	pool *pgxpool.Pool,
	phones *auth.PhoneNormalizer,
	otp *auth.OTP,
	jwt *auth.JWT,
	provider sms.Provider,
	limiter ratelimit.Limiter,
	cfg AuthConfig,
	logger *slog.Logger,
) *Auth {
	if logger == nil {
		logger = slog.Default()
	}
	return &Auth{
		pool:    pool,
		q:       store.New(pool),
		phones:  phones,
		otp:     otp,
		jwt:     jwt,
		sms:     provider,
		limiter: limiter,
		cfg:     cfg,
		logger:  logger,
		now:     time.Now,
	}
}

// RequestOTPInput is the request-otp call. IP and DeviceID may be empty.
type RequestOTPInput struct {
	Phone    string
	IP       string
	DeviceID string
	Locale   string
}

// RequestOTPResult tells the client how long the code lives and when it may
// ask for another one. DevCode is set only when DevMode is on.
type RequestOTPResult struct {
	ExpiresIn  time.Duration
	RetryAfter time.Duration
	DevCode    string
}

// VerifyOTPInput is the verify-otp call.
type VerifyOTPInput struct {
	Phone    string
	Code     string
	DeviceID string
	IP       string
	Locale   string
}

// User is the authenticated user's own profile.
type User struct {
	ID          string
	Phone       string
	Name        string
	PhotoURL    *string
	RatingAvg   float64
	RatingCount int
	Locale      string
	CreatedAt   time.Time
}

// Tokens is an access/refresh pair.
type Tokens struct {
	AccessToken     string
	AccessExpiresIn time.Duration
	RefreshToken    string
}

// LoginResult is the verify-otp outcome.
type LoginResult struct {
	Tokens
	User      User
	IsNewUser bool
}

// RequestOTP validates the phone, applies rate limits, stores a hashed code
// and hands the SMS to the provider. It never looks at the users table, so
// its behavior is identical for new, existing and blocked phones.
func (a *Auth) RequestOTP(ctx context.Context, in RequestOTPInput) (RequestOTPResult, error) {
	phone, err := a.phones.Normalize(in.Phone)
	if err != nil {
		return RequestOTPResult{}, ErrInvalidPhone
	}

	rules := []ratelimit.Rule{
		{Key: "otp:phone:cooldown:" + phone, Limit: 1, Window: a.cfg.PhoneCooldown},
		{Key: "otp:phone:day:" + phone, Limit: a.cfg.PhoneDailyLimit, Window: 24 * time.Hour},
	}
	if in.IP != "" {
		rules = append(rules, ratelimit.Rule{Key: "otp:ip:" + in.IP, Limit: a.cfg.IPLimit, Window: a.cfg.IPWindow})
	}
	if in.DeviceID != "" {
		rules = append(rules, ratelimit.Rule{Key: "otp:device:" + in.DeviceID, Limit: a.cfg.DeviceLimit, Window: a.cfg.DeviceWindow})
	}
	decision, err := a.limiter.Allow(ctx, rules...)
	if err != nil {
		return RequestOTPResult{}, fmt.Errorf("rate limiter: %w", err)
	}
	if !decision.Allowed {
		a.logger.Info("otp request rate limited", "phone", auth.MaskPhone(phone), "retry_after", decision.RetryAfter)
		return RequestOTPResult{}, &RateLimitError{RetryAfter: decision.RetryAfter}
	}

	code, err := a.otp.Generate()
	if err != nil {
		return RequestOTPResult{}, err
	}
	expiresAt := a.now().Add(a.cfg.OTPTTL)

	err = store.RunInTx(ctx, a.pool, func(q *store.Queries) error {
		if err := q.InvalidateActiveOTPs(ctx, phone); err != nil {
			return fmt.Errorf("invalidate otps: %w", err)
		}
		_, err := q.CreateOTP(ctx, store.CreateOTPParams{
			Phone:     phone,
			CodeHash:  a.otp.Hash(phone, code),
			ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
			Ip:        pgtype.Text{String: in.IP, Valid: in.IP != ""},
			DeviceID:  pgtype.Text{String: in.DeviceID, Valid: in.DeviceID != ""},
		})
		if err != nil {
			return fmt.Errorf("create otp: %w", err)
		}
		return nil
	})
	if err != nil {
		return RequestOTPResult{}, err
	}

	text := i18n.OTPSMS(in.Locale, a.cfg.AppName, code)
	if err := a.sms.Send(ctx, phone, text); err != nil {
		return RequestOTPResult{}, fmt.Errorf("send otp sms: %w", err)
	}
	a.logger.Info("otp issued", "phone", auth.MaskPhone(phone))

	res := RequestOTPResult{ExpiresIn: a.cfg.OTPTTL, RetryAfter: a.cfg.PhoneCooldown}
	if a.cfg.DevMode {
		res.DevCode = code
	}
	return res, nil
}

// VerifyOTP checks the code, creates the user on first login, and issues
// tokens. Wrong codes count against the attempt limit even though the call
// returns an error, so that work is committed before returning.
func (a *Auth) VerifyOTP(ctx context.Context, in VerifyOTPInput) (LoginResult, error) {
	phone, err := a.phones.Normalize(in.Phone)
	if err != nil {
		return LoginResult{}, ErrInvalidPhone
	}
	if in.DeviceID == "" {
		return LoginResult{}, ErrValidation
	}
	locale := in.Locale
	if !i18n.Supported(locale) {
		locale = i18n.DefaultLocale
	}
	now := a.now()

	var (
		result   LoginResult
		business error
	)
	err = store.RunInTx(ctx, a.pool, func(q *store.Queries) error {
		rec, err := q.GetLatestOTPForUpdate(ctx, phone)
		if errors.Is(err, pgx.ErrNoRows) {
			// Burn the same amount of work as a real comparison.
			a.otp.Check(auth.OTPRecord{Phone: phone, CodeHash: a.otp.Hash(phone, "000000"), ExpiresAt: now}, in.Code, now)
			business = ErrOTPInvalid
			return nil
		}
		if err != nil {
			return fmt.Errorf("load otp: %w", err)
		}

		outcome := a.otp.Check(auth.OTPRecord{
			Phone:     rec.Phone,
			CodeHash:  rec.CodeHash,
			ExpiresAt: rec.ExpiresAt.Time,
			Attempts:  int(rec.Attempts),
			Used:      rec.Used,
		}, in.Code, now)

		switch outcome {
		case auth.OTPInvalid:
			if err := q.IncrementOTPAttempts(ctx, store.IncrementOTPAttemptsParams{ID: rec.ID, MaxAttempts: int32(a.otp.MaxAttempts())}); err != nil {
				return fmt.Errorf("count otp attempt: %w", err)
			}
			business = ErrOTPInvalid
			return nil
		case auth.OTPExpired:
			business = ErrOTPExpired
			return nil
		case auth.OTPAttemptsExceeded:
			business = ErrOTPAttemptsExceeded
			return nil
		}

		if err := q.MarkOTPUsed(ctx, rec.ID); err != nil {
			return fmt.Errorf("mark otp used: %w", err)
		}

		created, err := q.CreateUserIfMissing(ctx, store.CreateUserIfMissingParams{Phone: phone, Locale: locale})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		user, err := q.GetUserByPhone(ctx, phone)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if user.DeletedAt.Valid {
			business = ErrUnauthorized
			return nil
		}
		if user.IsBlocked {
			business = ErrUserBlocked
			return nil
		}

		tokens, err := a.issueTokens(ctx, q, user.ID, in.DeviceID, now)
		if err != nil {
			return err
		}
		result = LoginResult{Tokens: tokens, User: toUser(user), IsNewUser: created == 1}
		return nil
	})
	if err != nil {
		return LoginResult{}, err
	}
	if business != nil {
		a.logger.Info("otp verification rejected", "phone", auth.MaskPhone(phone), "code", CodeOf(business))
		return LoginResult{}, business
	}
	a.logger.Info("login", "user_id", result.User.ID, "new_user", result.IsNewUser)
	return result, nil
}

// Refresh rotates a refresh token. Presenting an already-rotated token
// revokes every token of that device, since it indicates theft or a client
// bug; the client must log in again.
func (a *Auth) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	if refreshToken == "" {
		return Tokens{}, ErrRefreshTokenInvalid
	}
	now := a.now()
	hash := auth.HashSecret(refreshToken)

	var (
		tokens   Tokens
		business error
	)
	err := store.RunInTx(ctx, a.pool, func(q *store.Queries) error {
		rec, err := q.GetRefreshTokenByHashForUpdate(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			business = ErrRefreshTokenInvalid
			return nil
		}
		if err != nil {
			return fmt.Errorf("load refresh token: %w", err)
		}
		if rec.RevokedAt.Valid {
			if _, err := q.RevokeDeviceRefreshTokens(ctx, store.RevokeDeviceRefreshTokensParams{UserID: rec.UserID, DeviceID: rec.DeviceID}); err != nil {
				return fmt.Errorf("revoke device tokens: %w", err)
			}
			a.logger.Warn("refresh token reuse detected", "user_id", rec.UserID.String())
			business = ErrRefreshTokenInvalid
			return nil
		}
		if !now.Before(rec.ExpiresAt.Time) {
			business = ErrRefreshTokenExpired
			return nil
		}
		user, err := q.GetUserByID(ctx, rec.UserID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if user.DeletedAt.Valid {
			business = ErrUnauthorized
			return nil
		}
		if user.IsBlocked {
			business = ErrUserBlocked
			return nil
		}
		if err := q.RevokeRefreshToken(ctx, rec.ID); err != nil {
			return fmt.Errorf("revoke refresh token: %w", err)
		}
		tokens, err = a.issueTokens(ctx, q, user.ID, rec.DeviceID, now)
		return err
	})
	if err != nil {
		return Tokens{}, err
	}
	if business != nil {
		return Tokens{}, business
	}
	return tokens, nil
}

// Logout revokes every refresh token of the caller's device.
func (a *Auth) Logout(ctx context.Context, userID, deviceID string) error {
	var uid pgtype.UUID
	if err := uid.Scan(userID); err != nil {
		return ErrUnauthorized
	}
	if _, err := a.q.RevokeDeviceRefreshTokens(ctx, store.RevokeDeviceRefreshTokensParams{UserID: uid, DeviceID: deviceID}); err != nil {
		return fmt.Errorf("revoke device tokens: %w", err)
	}
	return nil
}

// VerifyAccessToken is used by the HTTP auth middleware.
func (a *Auth) VerifyAccessToken(token string) (userID, deviceID string, err error) {
	claims, err := a.jwt.Verify(token)
	if err != nil {
		return "", "", ErrUnauthorized
	}
	return claims.Subject, claims.DeviceID, nil
}

// AccountStatus rejects blocked and anonymized users on every authenticated
// route. A deleted account is unauthorized so the client signs in again;
// a blocked account is USER_BLOCKED.
func (a *Auth) AccountStatus(ctx context.Context, userID string) error {
	var id pgtype.UUID
	if err := id.Scan(userID); err != nil || !id.Valid {
		return ErrUnauthorized
	}
	user, err := a.q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	if user.DeletedAt.Valid {
		return ErrUnauthorized
	}
	if user.IsBlocked {
		return ErrUserBlocked
	}
	return nil
}

// issueTokens creates a fresh refresh token row and signs an access token.
func (a *Auth) issueTokens(ctx context.Context, q *store.Queries, userID pgtype.UUID, deviceID string, now time.Time) (Tokens, error) {
	refresh, err := auth.NewRefreshToken()
	if err != nil {
		return Tokens{}, err
	}
	_, err = q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
		UserID:    userID,
		TokenHash: auth.HashSecret(refresh),
		DeviceID:  deviceID,
		ExpiresAt: pgtype.Timestamptz{Time: now.Add(a.cfg.RefreshTokenTTL), Valid: true},
	})
	if err != nil {
		return Tokens{}, fmt.Errorf("store refresh token: %w", err)
	}
	access, err := a.jwt.Issue(userID.String(), deviceID)
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: access, AccessExpiresIn: a.jwt.TTL(), RefreshToken: refresh}, nil
}

func toUser(u store.User) User {
	out := User{
		ID:          u.ID.String(),
		Phone:       u.Phone,
		Name:        u.Name,
		RatingCount: int(u.RatingCount),
		Locale:      u.Locale,
		CreatedAt:   u.CreatedAt.Time,
	}
	if u.PhotoUrl.Valid {
		v := u.PhotoUrl.String
		out.PhotoURL = &v
	}
	if f, err := u.RatingAvg.Float64Value(); err == nil && f.Valid {
		out.RatingAvg = f.Float64
	}
	return out
}
